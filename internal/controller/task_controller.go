/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
	"github.com/openconveyor/openconveyor/internal/metrics"
	"github.com/openconveyor/openconveyor/internal/policy"
)

const (
	// taskAgentRefIndex indexes Tasks by spec.agent.ref so the
	// ClusterAgentClass watch can find the Tasks that reference it.
	taskAgentRefIndex = ".spec.agent.ref"

	// agentClassMissingRequeue is a safety-net requeue for Tasks waiting
	// on a ClusterAgentClass; the watch is the primary wake-up signal.
	agentClassMissingRequeue = 30 * time.Second
)

// TaskReconciler reconciles a Task object.
type TaskReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Resolver policy.Resolver
}

var _ reconcile.Reconciler = (*TaskReconciler)(nil)

// +kubebuilder:rbac:groups=openconveyor.ai,resources=tasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openconveyor.ai,resources=tasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openconveyor.ai,resources=tasks/finalizers,verbs=update
// +kubebuilder:rbac:groups=openconveyor.ai,resources=clusteragentclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete

// Reconcile drives a Task through Pending → Running → Completed/Failed/TimedOut.
//
// Ordering is policy-before-Job on purpose: if the NetworkPolicy isn't in
// place when the pod starts, the CNI default-allow window lets an agent
// escape its egress allowlist for the first few hundred milliseconds.
func (r *TaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var task conveyorv1alpha1.Task
	if err := r.Get(ctx, req.NamespacedName, &task); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Terminal Tasks are never reconciled again: once the Job finishes,
	// its TTLSecondsAfterFinished deletion queues a reconcile that would
	// otherwise recreate the Job and re-run a side-effectful agent.
	if isTerminal(task.Status.Phase) {
		return ctrl.Result{}, nil
	}

	if err := validateTask(&task); err != nil {
		metrics.ObserveReconcileError(metrics.StepValidate)
		return ctrl.Result{}, r.failTask(ctx, &task, reasonInvalidSpec, err)
	}

	var agent conveyorv1alpha1.ClusterAgentClass
	if err := r.Get(ctx, types.NamespacedName{Name: task.Spec.Agent.Ref}, &agent); err != nil {
		if apierrors.IsNotFound(err) {
			// Task-before-class ordering is the normal case with GitOps
			// sync waves and webhook-created Tasks, so a missing class is
			// a wait-state, not a terminal failure. The ClusterAgentClass
			// watch re-enqueues this Task when the class appears; the
			// requeue is a safety net.
			metrics.ObserveReconcileError(metrics.StepAgentClassLookup)
			if err := r.markWaitingForAgentClass(ctx, &task); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: agentClassMissingRequeue}, nil
		}
		metrics.ObserveReconcileError(metrics.StepAgentClassLookup)
		return ctrl.Result{}, err
	}

	effectiveEgress := policy.MergeStringSets(task.Spec.Permissions.Egress, agent.Spec.Requires.Egress)
	effectiveSecrets := policy.MergeStringSets(task.Spec.Permissions.Secrets, agent.Spec.Requires.Secrets)

	// A step with a non-empty invalidReason may surface a PermanentError
	// (bad CIDR, NXDOMAIN) that fails the Task; every other error is
	// wrapped and returned so controller-runtime requeues with backoff
	// instead of wedging the Task in Failed. Order matters: the
	// NetworkPolicy must exist before the Job (ADR-0005).
	steps := []struct {
		metricsStep   string
		wrap          string
		invalidReason string
		run           func(context.Context) error
	}{
		{metrics.StepServiceAccount, "ensure service account", "",
			func(ctx context.Context) error { return r.ensureServiceAccount(ctx, &task) }},
		{metrics.StepRole, "ensure role", "",
			func(ctx context.Context) error { return r.ensureRole(ctx, &task) }},
		{metrics.StepRoleBinding, "ensure rolebinding", "",
			func(ctx context.Context) error { return r.ensureRoleBinding(ctx, &task) }},
		{metrics.StepNetworkPolicy, "ensure network policy", reasonEgressResolveFail,
			func(ctx context.Context) error { return r.ensureNetworkPolicy(ctx, &task, effectiveEgress) }},
		{metrics.StepPromptConfigMap, "ensure prompt configmap", "",
			func(ctx context.Context) error { return r.ensurePromptConfigMap(ctx, &task) }},
		{metrics.StepAgentConfigMap, "ensure config configmap", "",
			func(ctx context.Context) error { return r.ensureAgentConfigMap(ctx, &task, &agent) }},
	}
	for _, step := range steps {
		if err := step.run(ctx); err != nil {
			metrics.ObserveReconcileError(step.metricsStep)
			if step.invalidReason != "" && policy.IsPermanent(err) {
				return ctrl.Result{}, r.failTask(ctx, &task, step.invalidReason, err)
			}
			return ctrl.Result{}, fmt.Errorf("%s: %w", step.wrap, err)
		}
	}

	job, err := r.ensureJob(ctx, &task, &agent, effectiveSecrets)
	if err != nil {
		metrics.ObserveReconcileError(metrics.StepJob)
		// Config that can never be projected (malformed JSON, field with no
		// delivery path) is a spec problem — fail the Task instead of
		// requeueing forever.
		if policy.IsPermanent(err) {
			return ctrl.Result{}, r.failTask(ctx, &task, reasonInvalidSpec, err)
		}
		return ctrl.Result{}, fmt.Errorf("ensure job: %w", err)
	}

	if err := r.syncStatus(ctx, &task, job); err != nil {
		metrics.ObserveReconcileError(metrics.StepStatus)
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// ensureOwned upserts one owned resource: set the controller reference,
// create when absent, otherwise copy the reconcilable fields onto the
// live object and update. Labels always sync; mutate (optional) copies
// the type-specific mutable fields. The update is skipped when nothing
// changed — every Job status flip triggers a full reconcile, and
// unconditional writes would churn resourceVersions on all owned
// resources each time.
func ensureOwned[T client.Object](
	ctx context.Context,
	r *TaskReconciler,
	task *conveyorv1alpha1.Task,
	desired, existing T,
	mutate func(existing, desired T),
) error {
	if err := controllerutil.SetControllerReference(task, desired, r.Scheme); err != nil {
		return err
	}

	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	switch {
	case apierrors.IsNotFound(err):
		return r.Create(ctx, desired)
	case err != nil:
		return err
	}
	before := existing.DeepCopyObject()
	if mutate != nil {
		mutate(existing, desired)
	}
	existing.SetLabels(desired.GetLabels())
	if apiequality.Semantic.DeepEqual(before, existing) {
		return nil
	}
	return r.Update(ctx, existing)
}

func (r *TaskReconciler) ensureServiceAccount(ctx context.Context, task *conveyorv1alpha1.Task) error {
	return ensureOwned(ctx, r, task, buildServiceAccount(task), &corev1.ServiceAccount{}, nil)
}

// ensureRole materializes (or tears down) the namespaced Role for this
// Task. No RBAC declared → delete any stray Role. Otherwise upsert.
func (r *TaskReconciler) ensureRole(ctx context.Context, task *conveyorv1alpha1.Task) error {
	desired := policy.BuildRole(task.Name, task.Namespace, task.Spec.Permissions.RBAC)
	if desired == nil {
		key := types.NamespacedName{Name: taskResourceName(task), Namespace: task.Namespace}
		return r.deleteIfExists(ctx, key, &rbacv1.Role{})
	}
	return ensureOwned(ctx, r, task, desired, &rbacv1.Role{}, func(existing, desired *rbacv1.Role) {
		existing.Rules = desired.Rules
	})
}

func (r *TaskReconciler) ensureRoleBinding(ctx context.Context, task *conveyorv1alpha1.Task) error {
	desired := policy.BuildRoleBinding(task.Name, task.Namespace, task.Spec.Permissions.RBAC)
	if desired == nil {
		key := types.NamespacedName{Name: taskResourceName(task), Namespace: task.Namespace}
		return r.deleteIfExists(ctx, key, &rbacv1.RoleBinding{})
	}
	return ensureOwned(ctx, r, task, desired, &rbacv1.RoleBinding{}, func(existing, desired *rbacv1.RoleBinding) {
		// RoleRef is immutable per K8s — if it drifted somehow, only Subjects
		// and metadata can be patched. Leaving RoleRef untouched here.
		existing.Subjects = desired.Subjects
	})
}

// ensureNetworkPolicy resolves the Task's egress allowlist to CIDRs and
// upserts a NetworkPolicy that pairs a default-deny baseline with DNS +
// allowlist exceptions. When egress is empty the policy has zero egress
// rules — the pod cannot reach anything, not even DNS.
func (r *TaskReconciler) ensureNetworkPolicy(ctx context.Context, task *conveyorv1alpha1.Task, egress []string) error {
	resolver := r.Resolver
	if resolver == nil {
		// SetupWithManager defaults this; the fallback covers reconcilers
		// constructed directly (tests driving Reconcile by hand).
		resolver = policy.DefaultResolver{}
	}

	desired, err := policy.BuildNetworkPolicy(ctx, resolver, task.Name, task.Namespace, egress)
	if err != nil {
		return err
	}
	return ensureOwned(ctx, r, task, desired, &networkingv1.NetworkPolicy{},
		func(existing, desired *networkingv1.NetworkPolicy) {
			existing.Spec = desired.Spec
		})
}

func (r *TaskReconciler) ensureJob(
	ctx context.Context,
	task *conveyorv1alpha1.Task,
	agent *conveyorv1alpha1.ClusterAgentClass,
	effectiveSecrets []string,
) (*batchv1.Job, error) {
	desired, err := buildJob(task, agent, effectiveSecrets)
	if err != nil {
		return nil, err
	}
	if err := controllerutil.SetControllerReference(task, desired, r.Scheme); err != nil {
		return nil, err
	}

	var existing batchv1.Job
	getErr := r.Get(ctx, client.ObjectKeyFromObject(desired), &existing)
	switch {
	case apierrors.IsNotFound(getErr):
		if err := r.Create(ctx, desired); err != nil {
			return nil, err
		}
		return desired, nil
	case getErr != nil:
		return nil, getErr
	}
	// Jobs are mostly immutable — don't try to reconcile spec drift.
	// If a Task's spec changes, the user deletes the Task and re-applies.
	return &existing, nil
}

// ensurePromptConfigMap owns the inline-prompt ConfigMap. When the Task's
// prompt is inline, upsert the ConfigMap with the prompt body. When the
// Task stops using an inline prompt (switched to secretRef/configMapRef),
// delete any leftover owned ConfigMap so it does not linger with stale
// content that drifts from the current spec.
func (r *TaskReconciler) ensurePromptConfigMap(ctx context.Context, task *conveyorv1alpha1.Task) error {
	key := types.NamespacedName{
		Name:      policy.InlinePromptConfigMapName(task.Name),
		Namespace: task.Namespace,
	}

	desired := policy.BuildInlinePromptConfigMap(task.Name, task.Namespace, task.Spec.Prompt)
	if desired == nil {
		return r.deleteIfExists(ctx, key, &corev1.ConfigMap{})
	}
	return ensureOwned(ctx, r, task, desired, &corev1.ConfigMap{}, func(existing, desired *corev1.ConfigMap) {
		existing.Data = desired.Data
	})
}

// ensureAgentConfigMap owns the ConfigMap that carries spec.agent.config
// for agents that declare a config mount. Upserted when needed, deleted
// when the Task has no config (or the agent no longer mounts it) so stale
// content does not linger.
func (r *TaskReconciler) ensureAgentConfigMap(
	ctx context.Context,
	task *conveyorv1alpha1.Task,
	agent *conveyorv1alpha1.ClusterAgentClass,
) error {
	key := types.NamespacedName{
		Name:      policy.AgentConfigMapName(task.Name),
		Namespace: task.Namespace,
	}

	desired := policy.BuildAgentConfigMap(task.Name, task.Namespace, task.Spec.Agent.Config, agent.Spec.Inputs)
	if desired == nil {
		return r.deleteIfExists(ctx, key, &corev1.ConfigMap{})
	}
	return ensureOwned(ctx, r, task, desired, &corev1.ConfigMap{}, func(existing, desired *corev1.ConfigMap) {
		existing.Data = desired.Data
	})
}

func (r *TaskReconciler) deleteIfExists(ctx context.Context, key types.NamespacedName, obj client.Object) error {
	obj.SetName(key.Name)
	obj.SetNamespace(key.Namespace)
	if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// tasksForAgentClass maps a ClusterAgentClass event to the non-terminal
// Tasks that reference it, so Tasks created before their class start
// reconciling as soon as the class appears.
func (r *TaskReconciler) tasksForAgentClass(ctx context.Context, obj client.Object) []reconcile.Request {
	var tasks conveyorv1alpha1.TaskList
	if err := r.List(ctx, &tasks, client.MatchingFields{taskAgentRefIndex: obj.GetName()}); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list Tasks for ClusterAgentClass", "clusterAgentClass", obj.GetName())
		return nil
	}
	var reqs []reconcile.Request
	for i := range tasks.Items {
		if isTerminal(tasks.Items[i].Status.Phase) {
			continue
		}
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&tasks.Items[i])})
	}
	return reqs
}

// SetupWithManager wires the reconciler up to watch Tasks, owned Jobs,
// and the ClusterAgentClasses that Tasks reference.
func (r *TaskReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if r.Resolver == nil {
		r.Resolver = policy.DefaultResolver{}
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &conveyorv1alpha1.Task{}, taskAgentRefIndex,
		func(obj client.Object) []string {
			task := obj.(*conveyorv1alpha1.Task)
			if task.Spec.Agent.Ref == "" {
				return nil
			}
			return []string{task.Spec.Agent.Ref}
		}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&conveyorv1alpha1.Task{}).
		Owns(&batchv1.Job{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Watches(&conveyorv1alpha1.ClusterAgentClass{}, handler.EnqueueRequestsFromMapFunc(r.tasksForAgentClass)).
		Named("task").
		Complete(r)
}
