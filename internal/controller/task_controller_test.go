/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package controller

import (
	"context"
	"net"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
	"github.com/openconveyor/openconveyor/internal/policy"
)

// Shared cadence for the Eventually polls sprinkled through the suite.
const (
	eventuallyTimeout = 5 * time.Second
	eventuallyPoll    = 100 * time.Millisecond
)

// newTask builds a Task with the minimal valid spec (agent "hello",
// inline prompt, 5m timeout) and applies opts on top.
//
//nolint:unparam // namespace is part of the builder contract even though every spec uses "default" today
func newTask(name, namespace string, opts ...func(*conveyorv1alpha1.Task)) *conveyorv1alpha1.Task {
	task := &conveyorv1alpha1.Task{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: conveyorv1alpha1.TaskSpec{
			Agent:  conveyorv1alpha1.AgentRef{Ref: "hello"},
			Prompt: conveyorv1alpha1.PromptSource{Inline: "say hello"},
			Resources: conveyorv1alpha1.TaskResources{
				Timeout: metav1.Duration{Duration: 5 * time.Minute},
			},
		},
	}
	for _, opt := range opts {
		opt(task)
	}
	return task
}

func withAgentRef(ref string) func(*conveyorv1alpha1.Task) {
	return func(t *conveyorv1alpha1.Task) { t.Spec.Agent.Ref = ref }
}

func withEgress(hosts ...string) func(*conveyorv1alpha1.Task) {
	return func(t *conveyorv1alpha1.Task) { t.Spec.Permissions.Egress = hosts }
}

func withConfig(config string) func(*conveyorv1alpha1.Task) {
	return func(t *conveyorv1alpha1.Task) {
		t.Spec.Agent.Config = &runtime.RawExtension{Raw: []byte(config)}
	}
}

var _ = Describe("Task Controller", func() {
	const (
		agentClassName = "hello"
		taskName       = "test-task"
		taskNamespace  = "default"
	)

	ctx := context.Background()
	taskKey := types.NamespacedName{Name: taskName, Namespace: taskNamespace}

	// recorder captures the Events a reconcile emits; drained by
	// recordedEvents. Replaced per spec so Events never leak across.
	var recorder *events.FakeRecorder

	reconciler := func() *TaskReconciler {
		return &TaskReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: recorder,
			Resolver: policy.StaticResolver{
				Hosts: map[string][]string{
					"api.example.com": {"203.0.113.10"},
				},
			},
		}
	}

	// recordedEvents drains everything the recorder captured so far,
	// each as "<Type> <Reason> <Message>".
	recordedEvents := func() []string {
		var got []string
		for {
			select {
			case e := <-recorder.Events:
				got = append(got, e)
			default:
				return got
			}
		}
	}

	// ensureAgentClass gets-or-creates a ClusterAgentClass and registers
	// its deletion for cleanup after the spec.
	ensureAgentClass := func(spec conveyorv1alpha1.ClusterAgentClassSpec, name string) {
		agent := &conveyorv1alpha1.ClusterAgentClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       spec,
		}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, agent)
		if apierrors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, agent)
		})
	}

	// readyCondition fetches the "Ready" condition off a Task's status.
	readyConditionOf := func(task *conveyorv1alpha1.Task) *metav1.Condition {
		cond := meta.FindStatusCondition(task.Status.Conditions, conditionReady)
		ExpectWithOffset(1, cond).NotTo(BeNil(), "expected a Ready condition")
		return cond
	}

	BeforeEach(func() {
		recorder = events.NewFakeRecorder(32)

		By("ensuring a ClusterAgentClass exists")
		ensureAgentClass(conveyorv1alpha1.ClusterAgentClassSpec{
			Image:   "busybox:1.36",
			Command: []string{"echo"},
			Args:    []string{"hello"},
		}, agentClassName)
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
		for _, name := range []string{policy.InlinePromptConfigMapName(taskName), policy.AgentConfigMapName(taskName)} {
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
			for _, name := range []string{policy.InlinePromptConfigMapName(taskName), policy.AgentConfigMapName(taskName)} {
				cmKey := types.NamespacedName{Name: name, Namespace: taskNamespace}
				if err := k8sClient.Get(ctx, cmKey, &corev1.ConfigMap{}); !apierrors.IsNotFound(err) {
					return false
				}
			}
			return true
		}, eventuallyTimeout, eventuallyPoll).Should(BeTrue(), "owned resources must be gone before next spec")
	})

	Context("security baseline — a Task with no declared permissions", func() {
		var job batchv1.Job

		BeforeEach(func() {
			By("creating a minimal valid Task and reconciling once")
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace))).To(Succeed())
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
			job = batchv1.Job{}
			Expect(k8sClient.Get(ctx, taskKey, &job)).To(Succeed())
		})

		It("owns a ServiceAccount that withholds its token too", func() {
			var sa corev1.ServiceAccount
			Expect(k8sClient.Get(ctx, taskKey, &sa)).To(Succeed())
			Expect(sa.AutomountServiceAccountToken).To(HaveValue(BeFalse()),
				"no RBAC declared → the SA itself must not hand out tokens")
		})

		It("materializes a Job with no retries and the Task timeout as its deadline", func() {
			Expect(job.Spec.BackoffLimit).NotTo(BeNil())
			Expect(*job.Spec.BackoffLimit).To(Equal(int32(0)), "no blind retries on failing agents")
			Expect(job.Spec.ActiveDeadlineSeconds).NotTo(BeNil())
			Expect(*job.Spec.ActiveDeadlineSeconds).To(Equal(int64(300)), "timeout plumbed from Task.spec.resources.timeout")
			Expect(job.Spec.TTLSecondsAfterFinished).NotTo(BeNil())
		})

		It("hardens the pod spec and withholds the ServiceAccount token", func() {
			pod := job.Spec.Template.Spec
			Expect(pod.RestartPolicy).To(Equal(corev1.RestartPolicyNever))
			Expect(pod.ServiceAccountName).To(Equal(taskName))
			Expect(pod.AutomountServiceAccountToken).NotTo(BeNil())
			Expect(*pod.AutomountServiceAccountToken).To(BeFalse(), "Phase 1 security baseline: no SA token in pod")
		})

		It("hardens the pod security context so injected sidecars inherit it", func() {
			psc := job.Spec.Template.Spec.SecurityContext
			Expect(psc).NotTo(BeNil())
			Expect(psc.RunAsNonRoot).To(HaveValue(BeTrue()))
			Expect(psc.RunAsUser).To(HaveValue(Equal(nonRootUID)))
			Expect(psc.RunAsGroup).To(HaveValue(Equal(nonRootUID)))
			Expect(psc.SeccompProfile).NotTo(BeNil())
			Expect(psc.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeRuntimeDefault))
		})

		It("hardens the container security context", func() {
			pod := job.Spec.Template.Spec
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
		})

		It("sets controller ownership so Job + SA get GC'd with the Task", func() {
			Expect(job.OwnerReferences).To(HaveLen(1))
			Expect(job.OwnerReferences[0].Kind).To(Equal("Task"))
			Expect(*job.OwnerReferences[0].Controller).To(BeTrue())
		})

		It("emits a default-deny NetworkPolicy with no egress allowlist", func() {
			var np networkingv1.NetworkPolicy
			Expect(k8sClient.Get(ctx, taskKey, &np)).To(Succeed())
			Expect(np.Spec.Egress).To(BeEmpty(), "zero egress declared → deny-all")
			Expect(np.Spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeEgress))
			Expect(np.Spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeIngress))
			Expect(np.Spec.Ingress).To(BeNil(), "ingress always denied for agent jobs")
		})

		It("does not create a Role or RoleBinding when RBAC is empty", func() {
			var role rbacv1.Role
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, taskKey, &role))).To(BeTrue())
			var rb rbacv1.RoleBinding
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, taskKey, &rb))).To(BeTrue())
		})

		It("repairs externally mutated ServiceAccount labels on the next reconcile", func() {
			By("stripping the ownership labels out-of-band")
			var sa corev1.ServiceAccount
			Expect(k8sClient.Get(ctx, taskKey, &sa)).To(Succeed())
			sa.Labels = nil
			Expect(k8sClient.Update(ctx, &sa)).To(Succeed())

			By("reconciling again restores them")
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, taskKey, &sa)).To(Succeed())
			Expect(sa.Labels).To(HaveKeyWithValue(policy.LabelTaskName, taskName))
		})

		It("repairs an out-of-band re-enabled ServiceAccount token automount", func() {
			By("flipping automount on out-of-band")
			var sa corev1.ServiceAccount
			Expect(k8sClient.Get(ctx, taskKey, &sa)).To(Succeed())
			sa.AutomountServiceAccountToken = new(true)
			Expect(k8sClient.Update(ctx, &sa)).To(Succeed())

			By("reconciling again restores the default")
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, taskKey, &sa)).To(Succeed())
			Expect(sa.AutomountServiceAccountToken).To(HaveValue(BeFalse()))
		})
	})

	Context("declared permissions — Phase 2 policy generation", func() {
		var job batchv1.Job

		BeforeEach(func() {
			By("creating a Task with secrets, egress, and RBAC all declared, then reconciling once")
			task := newTask(taskName, taskNamespace, func(t *conveyorv1alpha1.Task) {
				t.Spec.Permissions = conveyorv1alpha1.Permissions{
					Secrets: []string{"github-token"},
					Egress:  []string{"api.example.com", "10.0.0.0/8"},
					RBAC: []conveyorv1alpha1.RBACRule{
						{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"get"}},
					},
				}
			})
			Expect(k8sClient.Create(ctx, task)).To(Succeed())
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
			job = batchv1.Job{}
			Expect(k8sClient.Get(ctx, taskKey, &job)).To(Succeed())
		})

		It("mounts the ServiceAccount token on both the pod and the SA when RBAC is declared", func() {
			pod := job.Spec.Template.Spec
			Expect(pod.AutomountServiceAccountToken).To(HaveValue(BeTrue()), "RBAC declared → SA token mounted")
			var sa corev1.ServiceAccount
			Expect(k8sClient.Get(ctx, taskKey, &sa)).To(Succeed())
			Expect(sa.AutomountServiceAccountToken).To(HaveValue(BeTrue()))
		})

		It("projects the declared Secret as read-only files at /run/secrets/<name>/<key>", func() {
			pod := job.Spec.Template.Spec

			var secretMount *corev1.VolumeMount
			for i, m := range pod.Containers[0].VolumeMounts {
				if m.MountPath == policy.SecretMountRoot+"/github-token" {
					secretMount = &pod.Containers[0].VolumeMounts[i]
				}
			}
			Expect(secretMount).NotTo(BeNil(), "expected secret volume mount")
			Expect(secretMount.ReadOnly).To(BeTrue())
			Expect(secretMount.SubPath).To(BeEmpty(), "whole Secret mounted as a directory, one file per key")

			var secretVol *corev1.Volume
			for i, v := range pod.Volumes {
				if v.Secret != nil && v.Secret.SecretName == "github-token" {
					secretVol = &pod.Volumes[i]
				}
			}
			Expect(secretVol).NotTo(BeNil(), "expected secret volume in pod")
			Expect(secretVol.Secret.Items).To(BeEmpty(), "every key of the Secret becomes a file under /run/secrets/<name>/")
		})

		It("emits a NetworkPolicy allowlisting resolved egress + DNS", func() {
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
		})

		It("restricts the allowlist rule to port 443 when egressPorts is unset", func() {
			var np networkingv1.NetworkPolicy
			Expect(k8sClient.Get(ctx, taskKey, &np)).To(Succeed())
			Expect(allowlistPorts(&np)).To(ConsistOf("TCP/443", "UDP/443"))
		})

		It("creates a Role + RoleBinding from the declared verbs", func() {
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

		It("does not rewrite unchanged owned resources on a second reconcile", func() {
			By("recording resourceVersions after the first reconcile")
			versions := func() map[string]string {
				var sa corev1.ServiceAccount
				Expect(k8sClient.Get(ctx, taskKey, &sa)).To(Succeed())
				var role rbacv1.Role
				Expect(k8sClient.Get(ctx, taskKey, &role)).To(Succeed())
				var rb rbacv1.RoleBinding
				Expect(k8sClient.Get(ctx, taskKey, &rb)).To(Succeed())
				var np networkingv1.NetworkPolicy
				Expect(k8sClient.Get(ctx, taskKey, &np)).To(Succeed())
				return map[string]string{
					"serviceaccount": sa.ResourceVersion,
					"role":           role.ResourceVersion,
					"rolebinding":    rb.ResourceVersion,
					"networkpolicy":  np.ResourceVersion,
				}
			}
			before := versions()

			By("reconciling again with no spec change")
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			Expect(versions()).To(Equal(before), "no-op reconcile must not bump resourceVersions")
		})

		It("restores Role rules and RoleBinding subjects mutated out-of-band", func() {
			By("widening the Role and repointing the RoleBinding out-of-band")
			var role rbacv1.Role
			Expect(k8sClient.Get(ctx, taskKey, &role)).To(Succeed())
			role.Rules = []rbacv1.PolicyRule{
				{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get", "list"}},
			}
			Expect(k8sClient.Update(ctx, &role)).To(Succeed())

			var rb rbacv1.RoleBinding
			Expect(k8sClient.Get(ctx, taskKey, &rb)).To(Succeed())
			rb.Subjects = []rbacv1.Subject{
				{Kind: rbacv1.ServiceAccountKind, Name: "intruder", Namespace: taskNamespace},
			}
			Expect(k8sClient.Update(ctx, &rb)).To(Succeed())

			By("reconciling again restores the declared policy")
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, taskKey, &role)).To(Succeed())
			Expect(role.Rules).To(HaveLen(1))
			Expect(role.Rules[0].Resources).To(ConsistOf("configmaps"))
			Expect(role.Rules[0].Verbs).To(ConsistOf("get"))

			Expect(k8sClient.Get(ctx, taskKey, &rb)).To(Succeed())
			Expect(rb.Subjects).To(HaveLen(1))
			Expect(rb.Subjects[0].Name).To(Equal(taskName))
		})
	})

	Context("egress ports — spec.permissions.egressPorts narrows the allowlist rule", func() {
		It("opens exactly the declared ports on the resolved CIDRs", func() {
			task := newTask(taskName, taskNamespace, withEgress("10.0.0.0/8"), func(t *conveyorv1alpha1.Task) {
				t.Spec.Permissions.EgressPorts = []int32{22, 8443}
			})
			Expect(k8sClient.Create(ctx, task)).To(Succeed())
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			var np networkingv1.NetworkPolicy
			Expect(k8sClient.Get(ctx, taskKey, &np)).To(Succeed())
			Expect(allowlistPorts(&np)).To(ConsistOf("TCP/22", "UDP/22", "TCP/8443", "UDP/8443"))
		})
	})

	Context("egress re-resolution — hostnames are re-resolved while the Task runs", func() {
		const refresh = 30 * time.Second

		refreshing := func(hosts map[string][]string) *TaskReconciler {
			r := reconciler()
			r.EgressRefreshInterval = refresh
			r.Resolver = policy.StaticResolver{Hosts: hosts}
			return r
		}
		resolvedCIDRs := func() []string {
			var np networkingv1.NetworkPolicy
			ExpectWithOffset(1, k8sClient.Get(ctx, taskKey, &np)).To(Succeed())
			var out []string
			for _, rule := range np.Spec.Egress {
				for _, peer := range rule.To {
					if peer.IPBlock != nil {
						out = append(out, peer.IPBlock.CIDR)
					}
				}
			}
			return out
		}

		It("requeues after the refresh interval while a hostname is allowlisted", func() {
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace, withEgress("api.example.com")))).To(Succeed())
			res, err := refreshing(map[string][]string{"api.example.com": {"203.0.113.10"}}).
				Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(Equal(refresh))
		})

		It("patches the NetworkPolicy when the resolved set changes", func() {
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace, withEgress("api.example.com")))).To(Succeed())
			_, err := refreshing(map[string][]string{"api.example.com": {"203.0.113.10"}}).
				Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(resolvedCIDRs()).To(ConsistOf("203.0.113.10/32"))

			By("re-reconciling after the DNS record moved")
			_, err = refreshing(map[string][]string{"api.example.com": {"203.0.113.20", "203.0.113.21"}}).
				Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(resolvedCIDRs()).To(ConsistOf("203.0.113.20/32", "203.0.113.21/32"))
		})

		It("does not requeue when the allowlist has no hostnames", func() {
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace, withEgress("10.0.0.0/8", "203.0.113.5")))).To(Succeed())
			res, err := refreshing(nil).Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero())
		})

		It("does not requeue when the refresh interval is disabled", func() {
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace, withEgress("api.example.com")))).To(Succeed())
			res, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero())
		})

		It("stops requeueing once the Task is terminal", func() {
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace, withEgress("api.example.com")))).To(Succeed())
			r := refreshing(map[string][]string{"api.example.com": {"203.0.113.10"}})
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			By("failing the Job")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, taskKey, &job)).To(Succeed())
			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Failed = 1
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: batchv1.JobFailureTarget, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"},
				batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"},
			)
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero(), "the reconcile that reaches a terminal phase must not schedule another")
		})
	})

	Context("prompt projection — inline prompts go through an owned ConfigMap", func() {
		const (
			promptAgentName = "prompt-reader"
			promptMountPath = "/task/prompt"
		)

		cmKey := types.NamespacedName{
			Name:      policy.InlinePromptConfigMapName(taskName),
			Namespace: taskNamespace,
		}

		BeforeEach(func() {
			ensureAgentClass(conveyorv1alpha1.ClusterAgentClassSpec{
				Image: "busybox:1.36",
				Inputs: conveyorv1alpha1.AgentInputs{
					Prompt: &conveyorv1alpha1.AgentInputMount{Mount: promptMountPath},
				},
			}, promptAgentName)

			task := newTask(taskName, taskNamespace, withAgentRef(promptAgentName), func(t *conveyorv1alpha1.Task) {
				t.Spec.Prompt.Inline = "refactor foo.go"
			})
			Expect(k8sClient.Create(ctx, task)).To(Succeed())
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
		})

		It("materializes a ConfigMap with the inline body and projects it at the AgentClass mount", func() {
			By("owning a ConfigMap whose data matches the inline prompt")
			var cm corev1.ConfigMap
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

		It("restores the prompt ConfigMap data after out-of-band mutation", func() {
			By("hijacking the prompt body out-of-band")
			var cm corev1.ConfigMap
			Expect(k8sClient.Get(ctx, cmKey, &cm)).To(Succeed())
			cm.Data[policy.PromptKey] = "rm -rf / instead"
			Expect(k8sClient.Update(ctx, &cm)).To(Succeed())

			By("reconciling again restores the declared prompt")
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, cmKey, &cm)).To(Succeed())
			Expect(cm.Data[policy.PromptKey]).To(Equal("refactor foo.go"))
		})
	})

	Context("AgentClass requires — merged into effective policy", func() {
		const requiresAgentName = "requires-agent"

		BeforeEach(func() {
			ensureAgentClass(conveyorv1alpha1.ClusterAgentClassSpec{
				Image: "busybox:1.36",
				Requires: conveyorv1alpha1.AgentRequirements{
					Egress:  []string{"api.example.com"},
					Secrets: []string{"agent-baseline-token"},
				},
			}, requiresAgentName)
		})

		It("unions AgentClass requires with Task permissions into the materialized policy", func() {
			task := newTask(taskName, taskNamespace, withAgentRef(requiresAgentName), func(t *conveyorv1alpha1.Task) {
				t.Spec.Permissions = conveyorv1alpha1.Permissions{
					Secrets: []string{"task-token"},
					Egress:  []string{"10.0.0.0/8"},
				}
			})
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
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace))).To(Succeed())

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
			}, eventuallyTimeout, eventuallyPoll).Should(BeTrue())

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

	Context("Job outcome — failure conditions drive the terminal phase", func() {
		// failJob marks the live Job failed with the given condition reason,
		// mirroring how the job controller reports it.
		failJob := func(reason string) {
			var job batchv1.Job
			ExpectWithOffset(1, k8sClient.Get(ctx, taskKey, &job)).To(Succeed())
			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Failed = 1
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: batchv1.JobFailureTarget, Status: corev1.ConditionTrue, Reason: reason},
				batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: reason},
			)
			ExpectWithOffset(1, k8sClient.Status().Update(ctx, &job)).To(Succeed())
		}

		BeforeEach(func() {
			By("creating a Task and reconciling it into a Job")
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace))).To(Succeed())
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
		})

		It("marks the Task Failed when the Job fails", func() {
			failJob("BackoffLimitExceeded")

			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			var got conveyorv1alpha1.Task
			Expect(k8sClient.Get(ctx, taskKey, &got)).To(Succeed())
			Expect(got.Status.Phase).To(Equal(conveyorv1alpha1.TaskPhaseFailed))
			cond := readyConditionOf(&got)
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(reasonFailed))
		})

		It("marks the Task TimedOut when the Job exceeded its deadline", func() {
			failJob(batchv1.JobReasonDeadlineExceeded)

			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			var got conveyorv1alpha1.Task
			Expect(k8sClient.Get(ctx, taskKey, &got)).To(Succeed())
			Expect(got.Status.Phase).To(Equal(conveyorv1alpha1.TaskPhaseTimedOut))
			cond := readyConditionOf(&got)
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(reasonTimedOut))
		})
	})

	Context("egress resolution failures", func() {
		It("retries instead of failing the Task on a transient resolver error", func() {
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace, withEgress("api.example.com")))).To(Succeed())

			By("reconciling with a resolver that times out")
			broken := reconciler()
			broken.Resolver = policy.StaticResolver{
				Err: &net.DNSError{Err: "i/o timeout", Name: "api.example.com", IsTimeout: true, IsTemporary: true},
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
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace, withEgress("api.example.com")))).To(Succeed())

			broken := reconciler()
			broken.Resolver = policy.StaticResolver{
				Err: &net.DNSError{Err: "no such host", Name: "api.example.com", IsNotFound: true},
			}
			_, err := broken.Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			var got conveyorv1alpha1.Task
			Expect(k8sClient.Get(ctx, taskKey, &got)).To(Succeed())
			Expect(got.Status.Phase).To(Equal(conveyorv1alpha1.TaskPhaseFailed))
			Expect(readyConditionOf(&got).Reason).To(Equal(reasonEgressResolveFail))
		})
	})

	Context("config projection", func() {
		It("projects scalar config fields as container env vars", func() {
			task := newTask(taskName, taskNamespace,
				withConfig(`{"TARGET_REPO":"https://github.com/o/r.git","TARGET_BRANCH":"main"}`))
			Expect(k8sClient.Create(ctx, task)).To(Succeed())

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
			ensureAgentClass(conveyorv1alpha1.ClusterAgentClassSpec{
				Image:   "busybox:1.36",
				Command: []string{"echo"},
				Inputs: conveyorv1alpha1.AgentInputs{
					Config: &conveyorv1alpha1.AgentInputMount{Mount: "/task/config.json"},
				},
			}, mountedClass)

			task := newTask(taskName, taskNamespace,
				withAgentRef(mountedClass),
				withConfig(`{"TARGET_REPO":"o/r","nested":{"a":1}}`))
			Expect(k8sClient.Create(ctx, task)).To(Succeed())

			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			By("owning a ConfigMap that carries the config JSON")
			var cm corev1.ConfigMap
			cmKey := types.NamespacedName{Name: policy.AgentConfigMapName(taskName), Namespace: taskNamespace}
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
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace, withConfig(`{"nested":{"a":1}}`)))).To(Succeed())

			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			var got conveyorv1alpha1.Task
			Expect(k8sClient.Get(ctx, taskKey, &got)).To(Succeed())
			Expect(got.Status.Phase).To(Equal(conveyorv1alpha1.TaskPhaseFailed))
			Expect(readyConditionOf(&got).Reason).To(Equal(reasonInvalidSpec))
		})
	})

	Context("invalid spec", func() {
		DescribeTable("rejects invalid Tasks at admission",
			func(mutate func(*conveyorv1alpha1.Task), wantSubstring string) {
				task := newTask(taskName, taskNamespace)
				mutate(task)
				err := k8sClient.Create(ctx, task)
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring(wantSubstring))
			},
			Entry("zero timeout",
				func(t *conveyorv1alpha1.Task) { t.Spec.Resources.Timeout = metav1.Duration{} },
				"spec.resources.timeout"),
			Entry("no prompt source",
				func(t *conveyorv1alpha1.Task) { t.Spec.Prompt = conveyorv1alpha1.PromptSource{} },
				"exactly one of inline, secretRef or configMapRef"),
			Entry("two prompt sources",
				func(t *conveyorv1alpha1.Task) {
					t.Spec.Prompt.ConfigMapRef = &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "prompts"},
						Key:                  "p",
					}
				},
				"exactly one of inline, secretRef or configMapRef"),
			Entry("egress port out of range",
				func(t *conveyorv1alpha1.Task) { t.Spec.Permissions.EgressPorts = []int32{70000} },
				"spec.permissions.egressPorts"),
			Entry("egress port zero",
				func(t *conveyorv1alpha1.Task) { t.Spec.Permissions.EgressPorts = []int32{0} },
				"spec.permissions.egressPorts"),
			Entry("malformed cpu quantity",
				func(t *conveyorv1alpha1.Task) { t.Spec.Resources.CPU = "not-a-cpu" },
				"spec.resources.cpu"),
			Entry("malformed memory quantity",
				func(t *conveyorv1alpha1.Task) { t.Spec.Resources.Memory = "10Gigabytes" },
				"spec.resources.memory"),
			Entry("egress entry that is a URL, not a DNS name or CIDR",
				func(t *conveyorv1alpha1.Task) { t.Spec.Permissions.Egress = []string{"https://api.github.com"} },
				"spec.permissions.egress"),
			Entry("RBAC rule with no verbs",
				func(t *conveyorv1alpha1.Task) {
					t.Spec.Permissions.RBAC = []conveyorv1alpha1.RBACRule{
						{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{}},
					}
				},
				"spec.permissions.rbac[0].verbs"),
			Entry("RBAC rule with no apiGroups",
				func(t *conveyorv1alpha1.Task) {
					t.Spec.Permissions.RBAC = []conveyorv1alpha1.RBACRule{
						{APIGroups: []string{}, Resources: []string{"configmaps"}, Verbs: []string{"get"}},
					}
				},
				"spec.permissions.rbac[0].apiGroups"),
			Entry("RBAC rule with no resources",
				func(t *conveyorv1alpha1.Task) {
					t.Spec.Permissions.RBAC = []conveyorv1alpha1.RBACRule{
						{APIGroups: []string{""}, Resources: []string{}, Verbs: []string{"get"}},
					}
				},
				"spec.permissions.rbac[0].resources"),
			Entry("inline prompt over 256 KiB",
				func(t *conveyorv1alpha1.Task) { t.Spec.Prompt.Inline = strings.Repeat("x", 256*1024+1) },
				"spec.prompt.inline"),
		)

		DescribeTable("accepts every egress shape the resolver understands",
			func(entry string) {
				task := newTask(taskName, taskNamespace, withEgress(entry))
				Expect(k8sClient.Create(ctx, task)).To(Succeed())
				Expect(k8sClient.Delete(ctx, task)).To(Succeed())
			},
			Entry("fully qualified DNS name", "api.github.com"),
			Entry("single-label DNS name", "kube-dns"),
			Entry("DNS name with trailing dot", "example.com."),
			Entry("bare IPv4 address", "203.0.113.10"),
			Entry("IPv4 CIDR block", "10.0.0.0/8"),
			Entry("bare IPv6 address", "2001:db8::1"),
			Entry("IPv6 CIDR block", "2001:db8::/32"),
		)

		DescribeTable("rejects invalid ClusterAgentClasses at admission",
			func(mutate func(*conveyorv1alpha1.ClusterAgentClass), wantSubstring string) {
				agent := &conveyorv1alpha1.ClusterAgentClass{
					ObjectMeta: metav1.ObjectMeta{Name: "invalid-agent"},
					Spec:       conveyorv1alpha1.ClusterAgentClassSpec{Image: "busybox:1.36"},
				}
				mutate(agent)
				err := k8sClient.Create(ctx, agent)
				Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected an Invalid error, got: %v", err)
				Expect(err.Error()).To(ContainSubstring(wantSubstring))
			},
			Entry("empty image",
				func(a *conveyorv1alpha1.ClusterAgentClass) { a.Spec.Image = "" },
				"spec.image"),
			Entry("unknown imagePullPolicy",
				func(a *conveyorv1alpha1.ClusterAgentClass) { a.Spec.ImagePullPolicy = "Sometimes" },
				"spec.imagePullPolicy"),
			Entry("required egress entry that is a URL, not a DNS name or CIDR",
				func(a *conveyorv1alpha1.ClusterAgentClass) {
					a.Spec.Requires.Egress = []string{"https://api.anthropic.com"}
				},
				"spec.requires.egress"),
		)

		It("keeps the Task Pending with a requeue when the referenced AgentClass does not exist", func() {
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace, withAgentRef("nope-missing")))).To(Succeed())

			res, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeNumerically(">", 0), "waiting Task must come back without a watch event")
			Expect(res.RequeueAfter).To(Equal(agentClassMissingRequeue))

			var got conveyorv1alpha1.Task
			Expect(k8sClient.Get(ctx, taskKey, &got)).To(Succeed())
			Expect(got.Status.Phase).To(Equal(conveyorv1alpha1.TaskPhasePending))
			cond := readyConditionOf(&got)
			Expect(cond.Reason).To(Equal(reasonAgentClassMissing))
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		})

		It("proceeds once the missing AgentClass is created afterwards", func() {
			const lateClass = "late-agent-class"
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace, withAgentRef(lateClass)))).To(Succeed())

			By("reconciling before the class exists: Task waits")
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			By("creating the class the Task was waiting for")
			ensureAgentClass(conveyorv1alpha1.ClusterAgentClassSpec{
				Image:   "busybox:1.36",
				Command: []string{"echo"},
				Args:    []string{"hello"},
			}, lateClass)

			By("reconciling again: the Task proceeds to a Job")
			_, err = reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			var job batchv1.Job
			Expect(k8sClient.Get(ctx, taskKey, &job)).To(Succeed())
		})
	})

	Context("Events — phase transitions and failures surface in kubectl describe", func() {
		It("emits a Pending event when the Task is first reconciled into a Job", func() {
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace))).To(Succeed())
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			Expect(recordedEvents()).To(ConsistOf("Normal Pending Waiting for pod to start"))
		})

		It("emits a Warning when the Job fails, and nothing on a no-op reconcile", func() {
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace))).To(Succeed())
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
			_ = recordedEvents()

			By("reconciling again with nothing changed")
			_, err = reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(recordedEvents()).To(BeEmpty(), "an unchanged status must not re-emit")

			By("failing the Job")
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, taskKey, &job)).To(Succeed())
			now := metav1.Now()
			job.Status.StartTime = &now
			job.Status.Failed = 1
			job.Status.Conditions = append(job.Status.Conditions,
				batchv1.JobCondition{Type: batchv1.JobFailureTarget, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"},
				batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"},
			)
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())
			_, err = reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			Expect(recordedEvents()).To(ConsistOf("Warning Failed Job failed"))
		})

		It("emits a Warning when the egress allowlist cannot be resolved", func() {
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace, withEgress("api.example.com")))).To(Succeed())
			broken := reconciler()
			broken.Resolver = policy.StaticResolver{
				Err: &net.DNSError{Err: "no such host", Name: "api.example.com", IsNotFound: true},
			}
			_, err := broken.Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			events := recordedEvents()
			Expect(events).To(HaveLen(1))
			Expect(events[0]).To(HavePrefix("Warning EgressResolveFailed Egress allowlist could not be resolved: "))
			Expect(events[0]).To(ContainSubstring("no such host"))
		})

		It("emits a Warning when the spec cannot be turned into a Job", func() {
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace, withConfig(`{"nested":{"a":1}}`)))).To(Succeed())
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			events := recordedEvents()
			Expect(events).To(HaveLen(1))
			Expect(events[0]).To(HavePrefix("Warning InvalidSpec Task spec is invalid: "))
		})

		It("emits a Warning once while the ClusterAgentClass is missing", func() {
			Expect(k8sClient.Create(ctx, newTask(taskName, taskNamespace, withAgentRef("nope")))).To(Succeed())
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(recordedEvents()).To(ConsistOf(
				`Warning AgentClassMissing Waiting for ClusterAgentClass "nope" to be created`))

			By("requeueing while the class is still missing")
			_, err = reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(recordedEvents()).To(BeEmpty(), "the periodic requeue must not spam Events")
		})
	})

	Context("ClusterAgentClass events — mapping back to waiting Tasks", func() {
		// The suite's direct envtest client cannot serve the custom
		// .spec.agent.ref field selector — that index lives in the manager's
		// cache and the suite never runs SetupWithManager. A fake client
		// carrying the same index exercises tasksForAgentClass's filtering.
		It("enqueues only non-terminal Tasks that reference the class", func() {
			completed := newTask("map-done", taskNamespace, withAgentRef("map-agent"))
			completed.Status.Phase = conveyorv1alpha1.TaskPhaseCompleted
			pending := newTask("map-pending", taskNamespace, withAgentRef("map-agent"))
			other := newTask("map-other", taskNamespace, withAgentRef("other-agent"))

			fakeClient := fake.NewClientBuilder().
				WithScheme(k8sClient.Scheme()).
				WithIndex(&conveyorv1alpha1.Task{}, taskAgentRefIndex, func(obj client.Object) []string {
					// Mirrors the index function registered in SetupWithManager.
					task := obj.(*conveyorv1alpha1.Task)
					if task.Spec.Agent.Ref == "" {
						return nil
					}
					return []string{task.Spec.Agent.Ref}
				}).
				WithObjects(completed, pending, other).
				Build()
			mapper := &TaskReconciler{Client: fakeClient, Scheme: k8sClient.Scheme()}

			reqs := mapper.tasksForAgentClass(ctx, &conveyorv1alpha1.ClusterAgentClass{
				ObjectMeta: metav1.ObjectMeta{Name: "map-agent"},
			})
			Expect(reqs).To(ConsistOf(reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "map-pending", Namespace: taskNamespace},
			}), "Completed Task and other-class Task must not be enqueued")
		})
	})
})

// allowlistPorts returns "PROTO/port" for every port on the NetworkPolicy
// egress rules that target IPBlocks, i.e. the allowlist rule (the DNS rule
// targets a pod selector and is skipped).
func allowlistPorts(np *networkingv1.NetworkPolicy) []string {
	var out []string
	for _, rule := range np.Spec.Egress {
		targetsIPBlock := false
		for _, peer := range rule.To {
			if peer.IPBlock != nil {
				targetsIPBlock = true
			}
		}
		if !targetsIPBlock {
			continue
		}
		for _, p := range rule.Ports {
			out = append(out, string(*p.Protocol)+"/"+p.Port.String())
		}
	}
	return out
}
