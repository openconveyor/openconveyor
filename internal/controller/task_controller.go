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
	"k8s.io/client-go/tools/events"
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
	// taskAgentRefIndex indexes Tasks by spec.agent.ref. The
	// ClusterAgentClass watch uses it to find the Tasks that reference a class.
	taskAgentRefIndex = ".spec.agent.ref"

	// agentClassMissingRequeue is the safety-net requeue interval for a Task
	// that waits for its ClusterAgentClass. The watch is the primary wake-up
	// signal.
	agentClassMissingRequeue = 30 * time.Second
)

// TaskReconciler reconciles a Task object.
type TaskReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Resolver policy.Resolver
	// Recorder publishes Events on the Task. Phase transitions and
	// failures then show in `kubectl describe task`. Nil means no Events.
	Recorder events.EventRecorder
	// EgressRefreshInterval is the interval at which the controller
	// reconciles a non-terminal Task again when its egress allowlist
	// contains hostnames. The resolved IPs in its NetworkPolicy then follow
	// DNS changes. Zero disables the periodic re-resolution, and the
	// snapshot from creation stays.
	EgressRefreshInterval time.Duration
	// JobTTLSeconds is the ttlSecondsAfterFinished that the controller sets
	// on every Job: how long a finished Job (and its logs) stays before
	// Kubernetes deletes it. The --job-ttl-seconds flag sets it, and
	// DefaultJobTTLSeconds applies when the operator does not set the flag.
	JobTTLSeconds int32
	// DefaultTaskTTLSeconds is the cluster-wide fallback for
	// spec.ttlSecondsAfterFinished (--task-ttl-seconds). When it is nil,
	// the controller never deletes a Task that sets no TTL of its own.
	DefaultTaskTTLSeconds *int32
}

var _ reconcile.Reconciler = (*TaskReconciler)(nil)

// +kubebuilder:rbac:groups=openconveyor.ai,resources=tasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openconveyor.ai,resources=tasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openconveyor.ai,resources=tasks/finalizers,verbs=update
// +kubebuilder:rbac:groups=openconveyor.ai,resources=clusteragentclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete

// Reconcile drives a Task through Pending → Running → Completed/Failed/TimedOut.
//
// The order is NetworkPolicy before Job on purpose. Without the
// NetworkPolicy in place when the pod starts, the CNI default-allow window
// lets an agent escape its egress allowlist. That window lasts the first
// few hundred milliseconds.
func (r *TaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var task conveyorv1alpha1.Task
	if err := r.Get(ctx, req.NamespacedName, &task); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// The controller never reconciles a terminal Task again. When the Job
	// finishes, its TTLSecondsAfterFinished deletion queues a reconcile.
	// Without this check, that reconcile recreates the Job and runs an
	// agent with side effects again. The only work that remains for a
	// terminal Task is garbage collection.
	if isTerminal(task.Status.Phase) {
		return r.collectTerminal(ctx, &task)
	}

	if err := validateTask(&task); err != nil {
		metrics.ObserveReconcileError(metrics.StepValidate)
		return ctrl.Result{}, r.failTask(ctx, &task, reasonInvalidSpec, err)
	}

	var agent conveyorv1alpha1.ClusterAgentClass
	if err := r.Get(ctx, types.NamespacedName{Name: task.Spec.Agent.Ref}, &agent); err != nil {
		if apierrors.IsNotFound(err) {
			// A Task often exists before its class with GitOps sync waves
			// and webhook-created Tasks. A missing class is therefore a
			// wait state, not a terminal failure. The ClusterAgentClass
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

	// A step with a non-empty invalidReason can return a PermanentError
	// (bad CIDR, NXDOMAIN) that fails the Task. The loop wraps and returns
	// every other error, so controller-runtime requeues with backoff and
	// the Task does not stop in Failed. The order matters: the
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
		// Config that the controller can never project (malformed JSON, a
		// field with no delivery path) is a spec problem. Fail the Task
		// instead of a requeue that never ends.
		if policy.IsPermanent(err) {
			return ctrl.Result{}, r.failTask(ctx, &task, reasonInvalidSpec, err)
		}
		return ctrl.Result{}, fmt.Errorf("ensure job: %w", err)
	}

	if err := r.syncStatus(ctx, &task, job); err != nil {
		metrics.ObserveReconcileError(metrics.StepStatus)
		return ctrl.Result{}, err
	}

	// The steps above resolve the hostnames to a snapshot. CDN and anycast
	// records rotate, so the controller requeues on an interval while the
	// Task is alive. ensureNetworkPolicy then patches in the current set.
	if r.EgressRefreshInterval > 0 && !isTerminal(task.Status.Phase) && policy.HasHostnames(effectiveEgress) {
		return ctrl.Result{RequeueAfter: r.EgressRefreshInterval}, nil
	}
	return ctrl.Result{}, nil
}

// collectTerminal deletes a terminal Task when its TTL has elapsed, or
// requeues for the remaining time. The owned resources go with it through
// their controller references. Without a TTL, the controller does not
// touch the Task.
func (r *TaskReconciler) collectTerminal(ctx context.Context, task *conveyorv1alpha1.Task) (ctrl.Result, error) {
	deadline, ok := deletionDeadline(task, r.DefaultTaskTTLSeconds)
	if !ok {
		return ctrl.Result{}, nil
	}
	if remaining := time.Until(deadline); remaining > 0 {
		return ctrl.Result{RequeueAfter: remaining}, nil
	}
	logf.FromContext(ctx).Info("Deleted terminal Task after its TTL", "phase", task.Status.Phase, "deadline", deadline)
	if err := r.Delete(ctx, task, client.Preconditions{UID: &task.UID}); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

// ensureOwned upserts one owned resource. It sets the controller
// reference, creates the resource when it is absent, and otherwise copies
// the reconcilable fields onto the live object and updates it. Labels
// always sync; the optional mutate function copies the type-specific
// mutable fields. It skips the update when nothing changed. Every Job
// status change triggers a full reconcile, and unconditional writes would
// churn the resourceVersions on all owned resources each time.
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
	return ensureOwned(ctx, r, task, buildServiceAccount(task), &corev1.ServiceAccount{},
		func(existing, desired *corev1.ServiceAccount) {
			existing.AutomountServiceAccountToken = desired.AutomountServiceAccountToken
		})
}

// ensureRole creates or deletes the namespaced Role for this Task. When
// the Task declares no RBAC, it deletes any stray Role. Otherwise it
// upserts the Role.
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
		// Kubernetes makes RoleRef immutable. If it drifted, the controller
		// can patch only Subjects and metadata. This function does not touch
		// RoleRef.
		existing.Subjects = desired.Subjects
	})
}

// ensureNetworkPolicy resolves the Task's egress allowlist to CIDRs and
// upserts a NetworkPolicy. The policy pairs a default-deny baseline with a
// DNS exception and an allowlist exception. spec.permissions.egressPorts
// (443 when unset) limits the allowlist exception. When egress is empty,
// the policy has zero egress rules: the pod cannot reach anything, not
// even DNS.
func (r *TaskReconciler) ensureNetworkPolicy(ctx context.Context, task *conveyorv1alpha1.Task, egress []string) error {
	resolver := r.Resolver
	if resolver == nil {
		// SetupWithManager sets the default. The fallback covers a reconciler
		// that a test constructs directly and drives through Reconcile by hand.
		resolver = policy.DefaultResolver{}
	}

	desired, err := policy.BuildNetworkPolicy(ctx, resolver, task.Name, task.Namespace, egress, task.Spec.Permissions.EgressPorts)
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
	desired, err := buildJob(task, agent, effectiveSecrets, r.JobTTLSeconds)
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
	// Jobs are mostly immutable, so the controller does not reconcile spec
	// drift. If a Task's spec changes, the user deletes the Task and applies
	// it again.
	return &existing, nil
}

// ensurePromptConfigMap owns the inline-prompt ConfigMap. When the Task's
// prompt is inline, it upserts the ConfigMap with the prompt body. When
// the Task changes from an inline prompt to secretRef or configMapRef, it
// deletes any leftover owned ConfigMap. The ConfigMap then does not stay
// with stale content that differs from the current spec.
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
// for an agent that declares a config mount. It upserts the ConfigMap when
// needed. It deletes the ConfigMap when the Task has no config, or when
// the agent no longer mounts it, so stale content does not stay.
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
// Tasks that reference it. A Task that exists before its class then
// starts to reconcile as soon as the class appears.
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

// SetupWithManager sets up the reconciler to watch Tasks, every owned
// resource, and the ClusterAgentClasses that Tasks reference.
func (r *TaskReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if r.Resolver == nil {
		r.Resolver = policy.DefaultResolver{}
	}
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorder("conveyor")
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
