/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package controller

import (
	"context"
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
	"github.com/openconveyor/openconveyor/internal/policy"
)

type fakeResolver struct {
	table map[string][]string
	err   error
}

func (f fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.table[host], nil
}

var _ = Describe("Task Controller", func() {
	const (
		agentClassName = "hello"
		taskName       = "test-task"
		taskNamespace  = "default"
	)

	ctx := context.Background()
	taskKey := types.NamespacedName{Name: taskName, Namespace: taskNamespace}

	reconciler := func() *TaskReconciler {
		return &TaskReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			Resolver: fakeResolver{
				table: map[string][]string{
					"api.example.com": {"203.0.113.10"},
				},
			},
		}
	}

	BeforeEach(func() {
		By("ensuring a ClusterAgentClass exists")
		agent := &conveyorv1alpha1.ClusterAgentClass{
			ObjectMeta: metav1.ObjectMeta{Name: agentClassName},
			Spec: conveyorv1alpha1.ClusterAgentClassSpec{
				Image:   "busybox:1.36",
				Command: []string{"echo"},
				Args:    []string{"hello"},
			},
		}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: agentClassName}, agent)
		if apierrors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
	})

	AfterEach(func() {
		By("cleaning up Task + owned resources (envtest has no GC)")
		task := &conveyorv1alpha1.Task{}
		if err := k8sClient.Get(ctx, taskKey, task); err == nil {
			Expect(k8sClient.Delete(ctx, task)).To(Succeed())
		}
		// Manually delete owned resources — envtest does not run the GC controller
		// that would cascade from ownerReferences. Also poll until the Job is gone
		// so the next spec doesn't hit a stale immutable Job from this one.
		propagation := metav1.DeletePropagationBackground
		deleteOpts := &client.DeleteOptions{PropagationPolicy: &propagation}
		owned := []client.Object{
			&batchv1.Job{},
			&corev1.ServiceAccount{},
			&networkingv1.NetworkPolicy{},
			&rbacv1.Role{},
			&rbacv1.RoleBinding{},
		}
		for _, obj := range owned {
			obj.SetName(taskName)
			obj.SetNamespace(taskNamespace)
			_ = k8sClient.Delete(ctx, obj, deleteOpts)
		}
		// The prompt and config ConfigMaps have different names — handle separately.
		for _, name := range []string{policy.InlinePromptConfigMapName(taskName), policy.ConfigConfigMapName(taskName)} {
			cm := &corev1.ConfigMap{}
			cm.SetName(name)
			cm.SetNamespace(taskNamespace)
			_ = k8sClient.Delete(ctx, cm, deleteOpts)
		}
		Eventually(func() bool {
			for _, obj := range owned {
				if err := k8sClient.Get(ctx, taskKey, obj); !apierrors.IsNotFound(err) {
					return false
				}
			}
			for _, name := range []string{policy.InlinePromptConfigMapName(taskName), policy.ConfigConfigMapName(taskName)} {
				cmKey := types.NamespacedName{Name: name, Namespace: taskNamespace}
				if err := k8sClient.Get(ctx, cmKey, &corev1.ConfigMap{}); !apierrors.IsNotFound(err) {
					return false
				}
			}
			return true
		}, 5*time.Second, 100*time.Millisecond).Should(BeTrue(), "owned resources must be gone before next spec")
	})

	Context("security baseline — a Task with no declared permissions", func() {
		It("materializes a hardened Job and ServiceAccount with zero k8s/egress/secret access", func() {
			By("creating a minimal valid Task")
			task := &conveyorv1alpha1.Task{
				ObjectMeta: metav1.ObjectMeta{Name: taskName, Namespace: taskNamespace},
				Spec: conveyorv1alpha1.TaskSpec{
					Agent:  conveyorv1alpha1.AgentRef{Ref: agentClassName},
					Prompt: conveyorv1alpha1.PromptSource{Inline: "say hello"},
					Resources: conveyorv1alpha1.TaskResources{
						Timeout: metav1.Duration{Duration: 5 * time.Minute},
					},
				},
			}
			Expect(k8sClient.Create(ctx, task)).To(Succeed())

			By("reconciling")
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			By("owning a ServiceAccount (token automount is enforced at the pod spec, not the SA)")
			var sa corev1.ServiceAccount
			Expect(k8sClient.Get(ctx, taskKey, &sa)).To(Succeed())

			By("owning a Job with hardened pod-level security defaults")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, taskKey, &job)).To(Succeed())

			Expect(job.Spec.BackoffLimit).NotTo(BeNil())
			Expect(*job.Spec.BackoffLimit).To(Equal(int32(0)), "no blind retries on failing agents")
			Expect(job.Spec.ActiveDeadlineSeconds).NotTo(BeNil())
			Expect(*job.Spec.ActiveDeadlineSeconds).To(Equal(int64(300)), "timeout plumbed from Task.spec.resources.timeout")
			Expect(job.Spec.TTLSecondsAfterFinished).NotTo(BeNil())

			pod := job.Spec.Template.Spec
			Expect(pod.RestartPolicy).To(Equal(corev1.RestartPolicyNever))
			Expect(pod.ServiceAccountName).To(Equal(taskName))
			Expect(pod.AutomountServiceAccountToken).NotTo(BeNil())
			Expect(*pod.AutomountServiceAccountToken).To(BeFalse(), "Phase 1 security baseline: no SA token in pod")

			By("hardening the container security context")
			Expect(pod.Containers).To(HaveLen(1))
			sc := pod.Containers[0].SecurityContext
			Expect(sc).NotTo(BeNil())
			Expect(sc.RunAsNonRoot).NotTo(BeNil())
			Expect(*sc.RunAsNonRoot).To(BeTrue())
			Expect(sc.ReadOnlyRootFilesystem).NotTo(BeNil())
			Expect(*sc.ReadOnlyRootFilesystem).To(BeTrue())
			Expect(sc.AllowPrivilegeEscalation).NotTo(BeNil())
			Expect(*sc.AllowPrivilegeEscalation).To(BeFalse())
			Expect(sc.Capabilities).NotTo(BeNil())
			Expect(sc.Capabilities.Drop).To(ContainElement(corev1.Capability("ALL")))
			Expect(sc.SeccompProfile).NotTo(BeNil())
			Expect(sc.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeRuntimeDefault))

			By("setting controller ownership so Job + SA get GC'd with the Task")
			Expect(job.OwnerReferences).To(HaveLen(1))
			Expect(job.OwnerReferences[0].Kind).To(Equal("Task"))
			Expect(*job.OwnerReferences[0].Controller).To(BeTrue())

			By("emitting a default-deny NetworkPolicy with no egress allowlist")
			var np networkingv1.NetworkPolicy
			Expect(k8sClient.Get(ctx, taskKey, &np)).To(Succeed())
			Expect(np.Spec.Egress).To(BeEmpty(), "zero egress declared → deny-all")
			Expect(np.Spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeEgress))
			Expect(np.Spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeIngress))
			Expect(np.Spec.Ingress).To(BeNil(), "ingress always denied for agent jobs")

			By("not creating a Role or RoleBinding when RBAC is empty")
			var role rbacv1.Role
			err = k8sClient.Get(ctx, taskKey, &role)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
			var rb rbacv1.RoleBinding
			err = k8sClient.Get(ctx, taskKey, &rb)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("declared permissions — Phase 2 policy generation", func() {
		It("materializes NetworkPolicy, Role, RoleBinding, and projects secrets", func() {
			By("creating a Task with secrets, egress, and RBAC all declared")
			task := &conveyorv1alpha1.Task{
				ObjectMeta: metav1.ObjectMeta{Name: taskName, Namespace: taskNamespace},
				Spec: conveyorv1alpha1.TaskSpec{
					Agent:  conveyorv1alpha1.AgentRef{Ref: agentClassName},
					Prompt: conveyorv1alpha1.PromptSource{Inline: "do work"},
					Permissions: conveyorv1alpha1.Permissions{
						Secrets: []string{"github-token"},
						Egress:  []string{"api.example.com", "10.0.0.0/8"},
						RBAC: []conveyorv1alpha1.RBACRule{
							{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"get"}},
						},
					},
					Resources: conveyorv1alpha1.TaskResources{
						Timeout: metav1.Duration{Duration: 5 * time.Minute},
					},
				},
			}
			Expect(k8sClient.Create(ctx, task)).To(Succeed())

			By("reconciling")
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			By("projecting the declared Secret as a read-only volume at /run/secrets/<name>")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, taskKey, &job)).To(Succeed())
			pod := job.Spec.Template.Spec
			Expect(pod.AutomountServiceAccountToken).NotTo(BeNil())
			Expect(*pod.AutomountServiceAccountToken).To(BeTrue(), "RBAC declared → SA token mounted")

			var secretMount *corev1.VolumeMount
			for i, m := range pod.Containers[0].VolumeMounts {
				if m.MountPath == policy.SecretMountRoot+"/github-token" {
					secretMount = &pod.Containers[0].VolumeMounts[i]
				}
			}
			Expect(secretMount).NotTo(BeNil(), "expected secret volume mount")
			Expect(secretMount.ReadOnly).To(BeTrue())

			var secretVol *corev1.Volume
			for i, v := range pod.Volumes {
				if v.Secret != nil && v.Secret.SecretName == "github-token" {
					secretVol = &pod.Volumes[i]
				}
			}
			Expect(secretVol).NotTo(BeNil(), "expected secret volume in pod")

			By("emitting a NetworkPolicy allowlisting resolved egress + DNS")
			var np networkingv1.NetworkPolicy
			Expect(k8sClient.Get(ctx, taskKey, &np)).To(Succeed())
			Expect(np.Spec.Egress).NotTo(BeEmpty())
			cidrs := map[string]bool{}
			for _, rule := range np.Spec.Egress {
				for _, peer := range rule.To {
					if peer.IPBlock != nil {
						cidrs[peer.IPBlock.CIDR] = true
					}
				}
			}
			Expect(cidrs).To(HaveKey("10.0.0.0/8"), "CIDR passthrough")
			Expect(cidrs).To(HaveKey("203.0.113.10/32"), "hostname resolved via fakeResolver")

			By("creating a Role + RoleBinding from the declared verbs")
			var role rbacv1.Role
			Expect(k8sClient.Get(ctx, taskKey, &role)).To(Succeed())
			Expect(role.Rules).To(HaveLen(1))
			Expect(role.Rules[0].Resources).To(ContainElement("configmaps"))
			Expect(role.Rules[0].Verbs).To(ContainElement("get"))

			var rb rbacv1.RoleBinding
			Expect(k8sClient.Get(ctx, taskKey, &rb)).To(Succeed())
			Expect(rb.Subjects).To(HaveLen(1))
			Expect(rb.Subjects[0].Name).To(Equal(taskName))
			Expect(rb.Subjects[0].Kind).To(Equal(rbacv1.ServiceAccountKind))
			Expect(rb.RoleRef.Name).To(Equal(taskName))
		})
	})

	Context("prompt projection — inline prompts go through an owned ConfigMap", func() {
		const (
			promptAgentName = "prompt-reader"
			promptMountPath = "/task/prompt"
		)

		BeforeEach(func() {
			agent := &conveyorv1alpha1.ClusterAgentClass{
				ObjectMeta: metav1.ObjectMeta{Name: promptAgentName},
				Spec: conveyorv1alpha1.ClusterAgentClassSpec{
					Image: "busybox:1.36",
					Inputs: conveyorv1alpha1.AgentInputs{
						Prompt: &conveyorv1alpha1.AgentInputMount{Mount: promptMountPath},
					},
				},
			}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: promptAgentName}, agent)
			if apierrors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		})

		It("materializes a ConfigMap with the inline body and projects it at the AgentClass mount", func() {
			task := &conveyorv1alpha1.Task{
				ObjectMeta: metav1.ObjectMeta{Name: taskName, Namespace: taskNamespace},
				Spec: conveyorv1alpha1.TaskSpec{
					Agent:  conveyorv1alpha1.AgentRef{Ref: promptAgentName},
					Prompt: conveyorv1alpha1.PromptSource{Inline: "refactor foo.go"},
					Resources: conveyorv1alpha1.TaskResources{
						Timeout: metav1.Duration{Duration: 5 * time.Minute},
					},
				},
			}
			Expect(k8sClient.Create(ctx, task)).To(Succeed())

			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			By("owning a ConfigMap whose data matches the inline prompt")
			var cm corev1.ConfigMap
			cmKey := types.NamespacedName{
				Name:      policy.InlinePromptConfigMapName(taskName),
				Namespace: taskNamespace,
			}
			Expect(k8sClient.Get(ctx, cmKey, &cm)).To(Succeed())
			Expect(cm.Data[policy.PromptKey]).To(Equal("refactor foo.go"))
			Expect(cm.OwnerReferences).To(HaveLen(1))

			By("mounting the ConfigMap at the AgentClass-declared path via subPath")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, taskKey, &job)).To(Succeed())
			pod := job.Spec.Template.Spec

			var promptMount *corev1.VolumeMount
			for i, m := range pod.Containers[0].VolumeMounts {
				if m.MountPath == promptMountPath {
					promptMount = &pod.Containers[0].VolumeMounts[i]
				}
			}
			Expect(promptMount).NotTo(BeNil(), "expected prompt volume mount at %s", promptMountPath)
			Expect(promptMount.SubPath).To(Equal(policy.PromptKey), "subPath pins the mount to a single file")
			Expect(promptMount.ReadOnly).To(BeTrue())

			var promptVol *corev1.Volume
			for i, v := range pod.Volumes {
				if v.Name == policy.PromptVolumeName {
					promptVol = &pod.Volumes[i]
				}
			}
			Expect(promptVol).NotTo(BeNil())
			Expect(promptVol.ConfigMap).NotTo(BeNil())
			Expect(promptVol.ConfigMap.Name).To(Equal(cmKey.Name))
		})
	})

	Context("AgentClass requires — merged into effective policy", func() {
		const requiresAgentName = "requires-agent"

		BeforeEach(func() {
			agent := &conveyorv1alpha1.ClusterAgentClass{
				ObjectMeta: metav1.ObjectMeta{Name: requiresAgentName},
				Spec: conveyorv1alpha1.ClusterAgentClassSpec{
					Image: "busybox:1.36",
					Requires: conveyorv1alpha1.AgentRequirements{
						Egress:  []string{"api.example.com"},
						Secrets: []string{"agent-baseline-token"},
					},
				},
			}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: requiresAgentName}, agent)
			if apierrors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		})

		It("unions AgentClass requires with Task permissions into the materialized policy", func() {
			task := &conveyorv1alpha1.Task{
				ObjectMeta: metav1.ObjectMeta{Name: taskName, Namespace: taskNamespace},
				Spec: conveyorv1alpha1.TaskSpec{
					Agent:  conveyorv1alpha1.AgentRef{Ref: requiresAgentName},
					Prompt: conveyorv1alpha1.PromptSource{Inline: "go"},
					Permissions: conveyorv1alpha1.Permissions{
						Secrets: []string{"task-token"},
						Egress:  []string{"10.0.0.0/8"},
					},
					Resources: conveyorv1alpha1.TaskResources{
						Timeout: metav1.Duration{Duration: 5 * time.Minute},
					},
				},
			}
			Expect(k8sClient.Create(ctx, task)).To(Succeed())

			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			By("mounting both the Task's secret and the AgentClass's required secret")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, taskKey, &job)).To(Succeed())
			mounts := map[string]bool{}
			for _, m := range job.Spec.Template.Spec.Containers[0].VolumeMounts {
				mounts[m.MountPath] = true
			}
			Expect(mounts).To(HaveKey(policy.SecretMountRoot + "/task-token"))
			Expect(mounts).To(HaveKey(policy.SecretMountRoot + "/agent-baseline-token"))

			By("allowlisting both the Task's CIDR and the AgentClass's resolved hostname")
			var np networkingv1.NetworkPolicy
			Expect(k8sClient.Get(ctx, taskKey, &np)).To(Succeed())
			cidrs := map[string]bool{}
			for _, rule := range np.Spec.Egress {
				for _, peer := range rule.To {
					if peer.IPBlock != nil {
						cidrs[peer.IPBlock.CIDR] = true
					}
				}
			}
			Expect(cidrs).To(HaveKey("10.0.0.0/8"))
			Expect(cidrs).To(HaveKey("203.0.113.10/32"), "AgentClass requires.egress merged in and resolved")
		})
	})

	Context("terminal Tasks — TTL Job deletion must not re-run the agent", func() {
		It("does not recreate the Job of a Completed Task", func() {
			By("creating a Task and reconciling it into a Job")
			task := &conveyorv1alpha1.Task{
				ObjectMeta: metav1.ObjectMeta{Name: taskName, Namespace: taskNamespace},
				Spec: conveyorv1alpha1.TaskSpec{
					Agent:  conveyorv1alpha1.AgentRef{Ref: agentClassName},
					Prompt: conveyorv1alpha1.PromptSource{Inline: "run once"},
					Resources: conveyorv1alpha1.TaskResources{
						Timeout: metav1.Duration{Duration: 5 * time.Minute},
					},
				},
			}
			Expect(k8sClient.Create(ctx, task)).To(Succeed())

			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			By("marking the Job complete")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, taskKey, &job)).To(Succeed())
			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.CompletionTime = &now
			job.Status.Succeeded = 1
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: batchv1.JobSuccessCriteriaMet, Status: corev1.ConditionTrue},
				batchv1.JobCondition{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			)
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			_, err = reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			var got conveyorv1alpha1.Task
			Expect(k8sClient.Get(ctx, taskKey, &got)).To(Succeed())
			Expect(got.Status.Phase).To(Equal(conveyorv1alpha1.TaskPhaseCompleted))

			By("deleting the Job, as TTLSecondsAfterFinished would")
			propagation := metav1.DeletePropagationBackground
			Expect(k8sClient.Delete(ctx, &job, &client.DeleteOptions{PropagationPolicy: &propagation})).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, taskKey, &batchv1.Job{}))
			}, 5*time.Second, 100*time.Millisecond).Should(BeTrue())

			By("reconciling again, as the Job deletion event would trigger")
			_, err = reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			By("asserting no new Job appears and the Task stays Completed")
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, taskKey, &batchv1.Job{}))).To(BeTrue(),
				"terminal Task must not get a new Job")
			Expect(k8sClient.Get(ctx, taskKey, &got)).To(Succeed())
			Expect(got.Status.Phase).To(Equal(conveyorv1alpha1.TaskPhaseCompleted))
		})
	})

	Context("egress resolution failures", func() {
		egressTask := func() *conveyorv1alpha1.Task {
			return &conveyorv1alpha1.Task{
				ObjectMeta: metav1.ObjectMeta{Name: taskName, Namespace: taskNamespace},
				Spec: conveyorv1alpha1.TaskSpec{
					Agent:  conveyorv1alpha1.AgentRef{Ref: agentClassName},
					Prompt: conveyorv1alpha1.PromptSource{Inline: "hi"},
					Permissions: conveyorv1alpha1.Permissions{
						Egress: []string{"api.example.com"},
					},
					Resources: conveyorv1alpha1.TaskResources{
						Timeout: metav1.Duration{Duration: time.Minute},
					},
				},
			}
		}

		It("retries instead of failing the Task on a transient resolver error", func() {
			Expect(k8sClient.Create(ctx, egressTask())).To(Succeed())

			By("reconciling with a resolver that times out")
			broken := reconciler()
			broken.Resolver = fakeResolver{
				err: &net.DNSError{Err: "i/o timeout", Name: "api.example.com", IsTimeout: true, IsTemporary: true},
			}
			_, err := broken.Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).To(HaveOccurred(), "transient error must be returned for backoff requeue")

			var got conveyorv1alpha1.Task
			Expect(k8sClient.Get(ctx, taskKey, &got)).To(Succeed())
			Expect(got.Status.Phase).NotTo(Equal(conveyorv1alpha1.TaskPhaseFailed),
				"transient DNS blip must not wedge the Task in Failed")

			By("reconciling again once DNS recovers")
			_, err = reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			var job batchv1.Job
			Expect(k8sClient.Get(ctx, taskKey, &job)).To(Succeed(), "Task proceeds normally after retry")
		})

		It("marks the Task Failed when the hostname does not exist", func() {
			Expect(k8sClient.Create(ctx, egressTask())).To(Succeed())

			broken := reconciler()
			broken.Resolver = fakeResolver{
				err: &net.DNSError{Err: "no such host", Name: "api.example.com", IsNotFound: true},
			}
			_, err := broken.Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			var got conveyorv1alpha1.Task
			Expect(k8sClient.Get(ctx, taskKey, &got)).To(Succeed())
			Expect(got.Status.Phase).To(Equal(conveyorv1alpha1.TaskPhaseFailed))
			Expect(got.Status.Conditions[0].Reason).To(Equal(reasonEgressResolveFail))
		})
	})

	Context("config projection", func() {
		configTask := func(config string) *conveyorv1alpha1.Task {
			return &conveyorv1alpha1.Task{
				ObjectMeta: metav1.ObjectMeta{Name: taskName, Namespace: taskNamespace},
				Spec: conveyorv1alpha1.TaskSpec{
					Agent: conveyorv1alpha1.AgentRef{
						Ref:    agentClassName,
						Config: &runtime.RawExtension{Raw: []byte(config)},
					},
					Prompt: conveyorv1alpha1.PromptSource{Inline: "do work"},
					Resources: conveyorv1alpha1.TaskResources{
						Timeout: metav1.Duration{Duration: 5 * time.Minute},
					},
				},
			}
		}

		It("projects scalar config fields as container env vars", func() {
			Expect(k8sClient.Create(ctx, configTask(`{"TARGET_REPO":"https://github.com/o/r.git","TARGET_BRANCH":"main"}`))).To(Succeed())

			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			var job batchv1.Job
			Expect(k8sClient.Get(ctx, taskKey, &job)).To(Succeed())
			env := job.Spec.Template.Spec.Containers[0].Env
			Expect(env).To(ContainElement(corev1.EnvVar{Name: "TARGET_REPO", Value: "https://github.com/o/r.git"}))
			Expect(env).To(ContainElement(corev1.EnvVar{Name: "TARGET_BRANCH", Value: "main"}))
		})

		It("mounts the full config JSON when the AgentClass declares a config mount", func() {
			const mountedClass = "config-mount-agent"
			agent := &conveyorv1alpha1.ClusterAgentClass{
				ObjectMeta: metav1.ObjectMeta{Name: mountedClass},
				Spec: conveyorv1alpha1.ClusterAgentClassSpec{
					Image:   "busybox:1.36",
					Command: []string{"echo"},
					Inputs: conveyorv1alpha1.AgentInputs{
						Config: &conveyorv1alpha1.AgentInputMount{Mount: "/task/config.json"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, agent)
			})

			task := configTask(`{"TARGET_REPO":"o/r","nested":{"a":1}}`)
			task.Spec.Agent.Ref = mountedClass
			Expect(k8sClient.Create(ctx, task)).To(Succeed())

			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			By("owning a ConfigMap that carries the config JSON")
			var cm corev1.ConfigMap
			cmKey := types.NamespacedName{Name: policy.ConfigConfigMapName(taskName), Namespace: taskNamespace}
			Expect(k8sClient.Get(ctx, cmKey, &cm)).To(Succeed())
			Expect(cm.Data[policy.ConfigKey]).To(MatchJSON(`{"TARGET_REPO":"o/r","nested":{"a":1}}`))
			Expect(cm.OwnerReferences).To(HaveLen(1))

			By("mounting it into the container at the declared path")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, taskKey, &job)).To(Succeed())
			container := job.Spec.Template.Spec.Containers[0]
			Expect(container.VolumeMounts).To(ContainElement(corev1.VolumeMount{
				Name:      policy.ConfigVolumeName,
				MountPath: "/task/config.json",
				SubPath:   policy.ConfigKey,
				ReadOnly:  true,
			}))
			Expect(container.Env).To(ContainElement(corev1.EnvVar{Name: "TARGET_REPO", Value: "o/r"}))
		})

		It("marks the Task Failed when config cannot be projected", func() {
			Expect(k8sClient.Create(ctx, configTask(`{"nested":{"a":1}}`))).To(Succeed())

			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			var got conveyorv1alpha1.Task
			Expect(k8sClient.Get(ctx, taskKey, &got)).To(Succeed())
			Expect(got.Status.Phase).To(Equal(conveyorv1alpha1.TaskPhaseFailed))
			Expect(got.Status.Conditions[0].Reason).To(Equal(reasonInvalidSpec))
		})
	})

	Context("invalid spec", func() {
		It("rejects a Task with a zero timeout at admission", func() {
			task := &conveyorv1alpha1.Task{
				ObjectMeta: metav1.ObjectMeta{Name: taskName, Namespace: taskNamespace},
				Spec: conveyorv1alpha1.TaskSpec{
					Agent:  conveyorv1alpha1.AgentRef{Ref: agentClassName},
					Prompt: conveyorv1alpha1.PromptSource{Inline: "hi"},
					// Timeout intentionally zero
				},
			}
			err := k8sClient.Create(ctx, task)
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("spec.resources.timeout"))
		})

		It("rejects a Task without a prompt source at admission", func() {
			task := &conveyorv1alpha1.Task{
				ObjectMeta: metav1.ObjectMeta{Name: taskName, Namespace: taskNamespace},
				Spec: conveyorv1alpha1.TaskSpec{
					Agent: conveyorv1alpha1.AgentRef{Ref: agentClassName},
					Resources: conveyorv1alpha1.TaskResources{
						Timeout: metav1.Duration{Duration: time.Minute},
					},
				},
			}
			err := k8sClient.Create(ctx, task)
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("exactly one of inline, secretRef or configMapRef"))
		})

		It("rejects a Task with two prompt sources at admission", func() {
			task := &conveyorv1alpha1.Task{
				ObjectMeta: metav1.ObjectMeta{Name: taskName, Namespace: taskNamespace},
				Spec: conveyorv1alpha1.TaskSpec{
					Agent: conveyorv1alpha1.AgentRef{Ref: agentClassName},
					Prompt: conveyorv1alpha1.PromptSource{
						Inline: "hi",
						ConfigMapRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: "prompts"},
							Key:                  "p",
						},
					},
					Resources: conveyorv1alpha1.TaskResources{
						Timeout: metav1.Duration{Duration: time.Minute},
					},
				},
			}
			err := k8sClient.Create(ctx, task)
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("exactly one of inline, secretRef or configMapRef"))
		})

		It("rejects a Task with a malformed cpu quantity at admission", func() {
			task := &conveyorv1alpha1.Task{
				ObjectMeta: metav1.ObjectMeta{Name: taskName, Namespace: taskNamespace},
				Spec: conveyorv1alpha1.TaskSpec{
					Agent:  conveyorv1alpha1.AgentRef{Ref: agentClassName},
					Prompt: conveyorv1alpha1.PromptSource{Inline: "hi"},
					Resources: conveyorv1alpha1.TaskResources{
						CPU:     "not-a-cpu",
						Timeout: metav1.Duration{Duration: time.Minute},
					},
				},
			}
			err := k8sClient.Create(ctx, task)
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("spec.resources.cpu"))
		})

		It("rejects a Task with a malformed memory quantity at admission", func() {
			task := &conveyorv1alpha1.Task{
				ObjectMeta: metav1.ObjectMeta{Name: taskName, Namespace: taskNamespace},
				Spec: conveyorv1alpha1.TaskSpec{
					Agent:  conveyorv1alpha1.AgentRef{Ref: agentClassName},
					Prompt: conveyorv1alpha1.PromptSource{Inline: "hi"},
					Resources: conveyorv1alpha1.TaskResources{
						Memory:  "10Gigabytes",
						Timeout: metav1.Duration{Duration: time.Minute},
					},
				},
			}
			err := k8sClient.Create(ctx, task)
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("spec.resources.memory"))
		})

		It("keeps the Task Pending with a requeue when the referenced AgentClass does not exist", func() {
			task := &conveyorv1alpha1.Task{
				ObjectMeta: metav1.ObjectMeta{Name: taskName, Namespace: taskNamespace},
				Spec: conveyorv1alpha1.TaskSpec{
					Agent:  conveyorv1alpha1.AgentRef{Ref: "nope-missing"},
					Prompt: conveyorv1alpha1.PromptSource{Inline: "hi"},
					Resources: conveyorv1alpha1.TaskResources{
						Timeout: metav1.Duration{Duration: time.Minute},
					},
				},
			}
			Expect(k8sClient.Create(ctx, task)).To(Succeed())

			res, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(Equal(agentClassMissingRequeue))

			var got conveyorv1alpha1.Task
			Expect(k8sClient.Get(ctx, taskKey, &got)).To(Succeed())
			Expect(got.Status.Phase).To(Equal(conveyorv1alpha1.TaskPhasePending))
			Expect(got.Status.Conditions[0].Reason).To(Equal(reasonAgentClassMissing))
			Expect(got.Status.Conditions[0].Status).To(Equal(metav1.ConditionUnknown))
		})

		It("proceeds once the missing AgentClass is created afterwards", func() {
			const lateClass = "late-agent-class"
			task := &conveyorv1alpha1.Task{
				ObjectMeta: metav1.ObjectMeta{Name: taskName, Namespace: taskNamespace},
				Spec: conveyorv1alpha1.TaskSpec{
					Agent:  conveyorv1alpha1.AgentRef{Ref: lateClass},
					Prompt: conveyorv1alpha1.PromptSource{Inline: "hi"},
					Resources: conveyorv1alpha1.TaskResources{
						Timeout: metav1.Duration{Duration: time.Minute},
					},
				},
			}
			Expect(k8sClient.Create(ctx, task)).To(Succeed())

			By("reconciling before the class exists: Task waits")
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			By("creating the class the Task was waiting for")
			agent := &conveyorv1alpha1.ClusterAgentClass{
				ObjectMeta: metav1.ObjectMeta{Name: lateClass},
				Spec: conveyorv1alpha1.ClusterAgentClassSpec{
					Image:   "busybox:1.36",
					Command: []string{"echo"},
					Args:    []string{"hello"},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, agent)
			})

			By("reconciling again: the Task proceeds to a Job")
			_, err = reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			var job batchv1.Job
			Expect(k8sClient.Get(ctx, taskKey, &job)).To(Succeed())
		})
	})
})
