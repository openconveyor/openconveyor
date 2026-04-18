/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package controller

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
	"github.com/openconveyor/openconveyor/internal/policy"
)

const (
	conditionReady = "Ready"

	reasonAgentClassMissing = "AgentClassMissing"
	reasonInvalidSpec       = "InvalidSpec"
	reasonRunning           = "Running"
	reasonCompleted         = "Completed"
	reasonFailed            = "Failed"
	reasonTimedOut          = "TimedOut"
	reasonEgressResolveFail = "EgressResolveFailed"
)

// TaskReconciler reconciles a Task object.
type TaskReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Resolver policy.Resolver
}

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
	log := logf.FromContext(ctx)

	var task conveyorv1alpha1.Task
	if err := r.Get(ctx, req.NamespacedName, &task); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if err := validateTask(&task); err != nil {
		return ctrl.Result{}, r.markInvalid(ctx, &task, reasonInvalidSpec, err.Error())
	}

	var agent conveyorv1alpha1.ClusterAgentClass
	if err := r.Get(ctx, types.NamespacedName{Name: task.Spec.Agent.Ref}, &agent); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, r.markInvalid(ctx, &task, reasonAgentClassMissing,
				fmt.Sprintf("ClusterAgentClass %q not found", task.Spec.Agent.Ref))
		}
		return ctrl.Result{}, err
	}

	if err := r.ensureServiceAccount(ctx, &task); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure service account: %w", err)
	}

	if err := r.ensureRole(ctx, &task); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure role: %w", err)
	}

	if err := r.ensureRoleBinding(ctx, &task); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure rolebinding: %w", err)
	}

	effectiveEgress := policy.MergeStringSets(task.Spec.Permissions.Egress, agent.Spec.Requires.Egress)
	effectiveSecrets := policy.MergeStringSets(task.Spec.Permissions.Secrets, agent.Spec.Requires.Secrets)

	if err := r.ensureNetworkPolicy(ctx, &task, effectiveEgress); err != nil {
		return ctrl.Result{}, r.markInvalid(ctx, &task, reasonEgressResolveFail, err.Error())
	}

	if err := r.ensurePromptConfigMap(ctx, &task); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure prompt configmap: %w", err)
	}

	job, err := r.ensureJob(ctx, &task, &agent, effectiveSecrets)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure job: %w", err)
	}

	if err := r.syncStatus(ctx, &task, job); err != nil {
		log.Error(err, "update status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func validateTask(task *conveyorv1alpha1.Task) error {
	if task.Spec.Resources.Timeout.Duration <= 0 {
		return fmt.Errorf("spec.resources.timeout must be > 0")
	}
	if task.Spec.Agent.Ref == "" {
		return fmt.Errorf("spec.agent.ref is required")
	}
	p := task.Spec.Prompt
	if p.Inline == "" && p.SecretRef == nil && p.ConfigMapRef == nil {
		return fmt.Errorf("spec.prompt requires one of inline/secretRef/configMapRef")
	}
	return nil
}

func (r *TaskReconciler) ensureServiceAccount(ctx context.Context, task *conveyorv1alpha1.Task) error {
	desired := buildServiceAccount(task)
	if err := controllerutil.SetControllerReference(task, desired, r.Scheme); err != nil {
		return err
	}

	var existing corev1.ServiceAccount
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), &existing)
	switch {
	case apierrors.IsNotFound(err):
		return r.Create(ctx, desired)
	case err != nil:
		return err
	}
	return nil
}

// ensureRole materializes (or tears down) the namespaced Role for this
// Task. No RBAC declared → delete any stray Role. Otherwise upsert.
func (r *TaskReconciler) ensureRole(ctx context.Context, task *conveyorv1alpha1.Task) error {
	desired := policy.BuildRole(task.Name, task.Namespace, task.Spec.Permissions.RBAC)
	key := types.NamespacedName{Name: taskResourceName(task), Namespace: task.Namespace}

	if desired == nil {
		return r.deleteIfExists(ctx, key, &rbacv1.Role{})
	}

	if err := controllerutil.SetControllerReference(task, desired, r.Scheme); err != nil {
		return err
	}

	var existing rbacv1.Role
	err := r.Get(ctx, key, &existing)
	switch {
	case apierrors.IsNotFound(err):
		return r.Create(ctx, desired)
	case err != nil:
		return err
	}
	existing.Rules = desired.Rules
	existing.Labels = desired.Labels
	return r.Update(ctx, &existing)
}

func (r *TaskReconciler) ensureRoleBinding(ctx context.Context, task *conveyorv1alpha1.Task) error {
	desired := policy.BuildRoleBinding(task.Name, task.Namespace, task.Spec.Permissions.RBAC)
	key := types.NamespacedName{Name: taskResourceName(task), Namespace: task.Namespace}

	if desired == nil {
		return r.deleteIfExists(ctx, key, &rbacv1.RoleBinding{})
	}

	if err := controllerutil.SetControllerReference(task, desired, r.Scheme); err != nil {
		return err
	}

	var existing rbacv1.RoleBinding
	err := r.Get(ctx, key, &existing)
	switch {
	case apierrors.IsNotFound(err):
		return r.Create(ctx, desired)
	case err != nil:
		return err
	}
	// RoleRef is immutable per K8s — if it drifted somehow, only Subjects
	// and metadata can be patched. Leaving RoleRef untouched here.
	existing.Subjects = desired.Subjects
	existing.Labels = desired.Labels
	return r.Update(ctx, &existing)
}

// ensureNetworkPolicy resolves the Task's egress allowlist to CIDRs and
// upserts a NetworkPolicy that pairs a default-deny baseline with DNS +
// allowlist exceptions. When egress is empty, the pod is still locked
// down to DNS-only via the same policy (otherwise it could reach the
// whole cluster pod network by default on most CNIs).
func (r *TaskReconciler) ensureNetworkPolicy(ctx context.Context, task *conveyorv1alpha1.Task, egress []string) error {
	resolver := r.Resolver
	if resolver == nil {
		resolver = policy.DefaultResolver{}
	}

	desired, err := policy.BuildNetworkPolicy(ctx, resolver, task.Name, task.Namespace, egress)
	if err != nil {
		return err
	}
	if err := controllerutil.SetControllerReference(task, desired, r.Scheme); err != nil {
		return err
	}

	var existing networkingv1.NetworkPolicy
	getErr := r.Get(ctx, client.ObjectKeyFromObject(desired), &existing)
	switch {
	case apierrors.IsNotFound(getErr):
		return r.Create(ctx, desired)
	case getErr != nil:
		return getErr
	}
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	return r.Update(ctx, &existing)
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

	if err := controllerutil.SetControllerReference(task, desired, r.Scheme); err != nil {
		return err
	}

	var existing corev1.ConfigMap
	err := r.Get(ctx, key, &existing)
	switch {
	case apierrors.IsNotFound(err):
		return r.Create(ctx, desired)
	case err != nil:
		return err
	}
	existing.Data = desired.Data
	existing.Labels = desired.Labels
	return r.Update(ctx, &existing)
}

func (r *TaskReconciler) deleteIfExists(ctx context.Context, key types.NamespacedName, obj client.Object) error {
	obj.SetName(key.Name)
	obj.SetNamespace(key.Namespace)
	if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (r *TaskReconciler) syncStatus(ctx context.Context, task *conveyorv1alpha1.Task, job *batchv1.Job) error {
	before := task.Status.DeepCopy()

	task.Status.JobName = job.Name
	task.Status.StartTime = job.Status.StartTime
	task.Status.CompletionTime = job.Status.CompletionTime

	phase, reason, msg := derivePhase(job)
	task.Status.Phase = phase

	cond := metav1.Condition{
		Type:               conditionReady,
		Status:             conditionStatusFor(phase),
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: task.Generation,
		LastTransitionTime: metav1.Now(),
	}
	setCondition(&task.Status.Conditions, cond)

	if equalStatus(before, &task.Status) {
		return nil
	}
	return r.Status().Update(ctx, task)
}

func derivePhase(job *batchv1.Job) (conveyorv1alpha1.TaskPhase, string, string) {
	for _, c := range job.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case batchv1.JobComplete, batchv1.JobSuccessCriteriaMet:
			return conveyorv1alpha1.TaskPhaseCompleted, reasonCompleted, c.Message
		case batchv1.JobFailed:
			if c.Reason == "DeadlineExceeded" {
				return conveyorv1alpha1.TaskPhaseTimedOut, reasonTimedOut, c.Message
			}
			return conveyorv1alpha1.TaskPhaseFailed, reasonFailed, c.Message
		}
	}
	if job.Status.Active > 0 || job.Status.StartTime != nil {
		return conveyorv1alpha1.TaskPhaseRunning, reasonRunning, "Job is running"
	}
	return conveyorv1alpha1.TaskPhasePending, reasonRunning, "Waiting for pod to start"
}

func conditionStatusFor(phase conveyorv1alpha1.TaskPhase) metav1.ConditionStatus {
	switch phase {
	case conveyorv1alpha1.TaskPhaseCompleted:
		return metav1.ConditionTrue
	case conveyorv1alpha1.TaskPhaseFailed, conveyorv1alpha1.TaskPhaseTimedOut:
		return metav1.ConditionFalse
	default:
		return metav1.ConditionUnknown
	}
}

func (r *TaskReconciler) markInvalid(ctx context.Context, task *conveyorv1alpha1.Task, reason, msg string) error {
	task.Status.Phase = conveyorv1alpha1.TaskPhaseFailed
	setCondition(&task.Status.Conditions, metav1.Condition{
		Type:               conditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: task.Generation,
		LastTransitionTime: metav1.Now(),
	})
	return r.Status().Update(ctx, task)
}

func setCondition(conds *[]metav1.Condition, new metav1.Condition) {
	for i, c := range *conds {
		if c.Type != new.Type {
			continue
		}
		if c.Status == new.Status && c.Reason == new.Reason && c.Message == new.Message {
			return // no-op; keep original LastTransitionTime
		}
		(*conds)[i] = new
		return
	}
	*conds = append(*conds, new)
}

func equalStatus(a, b *conveyorv1alpha1.TaskStatus) bool {
	if a.Phase != b.Phase || a.JobName != b.JobName {
		return false
	}
	if !timesEqual(a.StartTime, b.StartTime) || !timesEqual(a.CompletionTime, b.CompletionTime) {
		return false
	}
	if len(a.Conditions) != len(b.Conditions) {
		return false
	}
	for i := range a.Conditions {
		ac, bc := a.Conditions[i], b.Conditions[i]
		if ac.Type != bc.Type || ac.Status != bc.Status || ac.Reason != bc.Reason || ac.Message != bc.Message {
			return false
		}
	}
	return true
}

func timesEqual(a, b *metav1.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Equal(b)
	}
}

// SetupWithManager wires the reconciler up to watch Tasks and owned Jobs.
func (r *TaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&conveyorv1alpha1.Task{}).
		Owns(&batchv1.Job{}, builder.WithPredicates()).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Named("task").
		Complete(r)
}
