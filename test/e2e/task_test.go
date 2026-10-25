//go:build e2e

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
	"github.com/openconveyor/openconveyor/test/utils"
)

// taskNamespace is where the e2e Tasks run. It is separate from the
// controller's namespace so the owned resources can be listed and
// deleted without touching the manager.
const taskNamespace = "e2e-tasks"

// taskSpecs holds the real Task scenarios from #39. It runs inside the
// Ordered "Manager" container so the controller deployed in its
// BeforeAll is available; the manifests under testdata/ are the ones
// from the manual kind smoke (#57).
func taskSpecs() {
	BeforeAll(func() {
		By("creating the Task namespace")
		cmd := exec.Command("kubectl", "create", "ns", taskNamespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create Task namespace")
	})

	AfterAll(func() {
		By("removing the Task namespace")
		cmd := exec.Command("kubectl", "delete", "ns", taskNamespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)
	})

	AfterEach(func() {
		if !CurrentSpecReport().Failed() {
			return
		}
		for _, args := range [][]string{
			{"get", "task", "-n", taskNamespace, "-o", "yaml"},
			{"get", "sa,networkpolicy,job,pod", "-n", taskNamespace, "-o", "wide"},
			{"get", "events", "-n", taskNamespace, "--sort-by=.lastTimestamp"},
			{"logs", "-n", taskNamespace, "-l", "openconveyor.ai/task", "--tail=50"},
			{"logs", "-n", namespace, "-l", "control-plane=controller-manager", "--tail=100"},
		} {
			out, err := utils.Run(exec.Command("kubectl", args...))
			if err != nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "kubectl %v failed: %s\n", args, err)
				continue
			}
			_, _ = fmt.Fprintf(GinkgoWriter, "kubectl %v:\n%s\n", args, out)
		}
	})

	It("runs a Task to Completed inside the hardened Job and deny-all NetworkPolicy", func() {
		const taskName = "e2e-completes"
		manifest := "test/e2e/testdata/task_completes.yaml"
		DeferCleanup(func() {
			_, _ = utils.Run(exec.Command("kubectl", "delete", "-f", manifest, "--ignore-not-found"))
		})

		By("applying the ClusterAgentClass and Task")
		_, err := utils.Run(exec.Command("kubectl", "apply", "-f", manifest))
		Expect(err).NotTo(HaveOccurred(), "Failed to apply Task manifests")

		By("waiting for the Task to reach Completed")
		var task conveyorv1alpha1.Task
		Eventually(func(g Gomega) {
			g.Expect(kubectlGet(&task, "task", taskName, "-n", taskNamespace)).To(Succeed())
			g.Expect(task.Status.Phase).To(Equal(conveyorv1alpha1.TaskPhaseCompleted))
		}).Should(Succeed())
		ready := meta.FindStatusCondition(task.Status.Conditions, "Ready")
		Expect(ready).NotTo(BeNil(), "Task has no Ready condition")
		Expect(ready.Status).To(Equal(metav1.ConditionTrue))

		selector := "openconveyor.ai/task=" + taskName

		By("checking the owned ServiceAccount exists")
		var sas corev1.ServiceAccountList
		Expect(kubectlGet(&sas, "serviceaccount", "-n", taskNamespace, "-l", selector)).To(Succeed())
		Expect(sas.Items).To(HaveLen(1))

		By("checking the owned NetworkPolicy is deny-all")
		var nps networkingv1.NetworkPolicyList
		Expect(kubectlGet(&nps, "networkpolicy", "-n", taskNamespace, "-l", selector)).To(Succeed())
		Expect(nps.Items).To(HaveLen(1))
		np := nps.Items[0].Spec
		Expect(np.PolicyTypes).To(ConsistOf(networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress))
		Expect(np.Ingress).To(BeEmpty())
		Expect(np.Egress).To(BeEmpty())

		By("checking the owned Job carries the hardening defaults")
		var jobs batchv1.JobList
		Expect(kubectlGet(&jobs, "job", "-n", taskNamespace, "-l", selector)).To(Succeed())
		Expect(jobs.Items).To(HaveLen(1))
		job := jobs.Items[0]
		Expect(job.Name).To(Equal(task.Status.JobName))
		Expect(job.Spec.BackoffLimit).To(HaveValue(BeEquivalentTo(0)))
		Expect(job.Spec.ActiveDeadlineSeconds).To(HaveValue(BeEquivalentTo(120)))
		podSpec := job.Spec.Template.Spec
		Expect(podSpec.AutomountServiceAccountToken).To(HaveValue(BeFalse()))
		Expect(podSpec.ServiceAccountName).To(Equal(sas.Items[0].Name))
		Expect(podSpec.Containers).To(HaveLen(1))
		Expect(podSpec.Containers[0].SecurityContext.RunAsUser).To(HaveValue(BeEquivalentTo(65532)))
	})

	It("fails a Task with no egress whose agent attempts an outbound connection", func() {
		const taskName = "e2e-egress-denied"
		manifest := "test/e2e/testdata/task_egress_denied.yaml"
		DeferCleanup(func() {
			_, _ = utils.Run(exec.Command("kubectl", "delete", "-f", manifest, "--ignore-not-found"))
		})

		By("applying the ClusterAgentClass and Task")
		_, err := utils.Run(exec.Command("kubectl", "apply", "-f", manifest))
		Expect(err).NotTo(HaveOccurred(), "Failed to apply Task manifests")

		// The Task timeout is 2m, so even if the connection attempt hangs
		// the Job deadline turns it into TimedOut inside this window.
		By("waiting for the Task to reach a terminal phase")
		var task conveyorv1alpha1.Task
		Eventually(func(g Gomega) {
			g.Expect(kubectlGet(&task, "task", taskName, "-n", taskNamespace)).To(Succeed())
			g.Expect(task.Status.Phase).To(BeElementOf(
				conveyorv1alpha1.TaskPhaseCompleted,
				conveyorv1alpha1.TaskPhaseFailed,
				conveyorv1alpha1.TaskPhaseTimedOut,
			))
		}, 3*time.Minute).Should(Succeed())

		By("checking the Task did not complete")
		Expect(task.Status.Phase).To(BeElementOf(
			conveyorv1alpha1.TaskPhaseFailed,
			conveyorv1alpha1.TaskPhaseTimedOut,
		), "a Task without egress must never reach Completed")
		ready := meta.FindStatusCondition(task.Status.Conditions, "Ready")
		Expect(ready).NotTo(BeNil(), "Task has no Ready condition")
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
	})
}

// kubectlGet runs `kubectl get <args> -o json` and decodes the result
// into out, which is a typed object or list.
func kubectlGet(out any, args ...string) error {
	args = append([]string{"get"}, args...)
	args = append(args, "-o", "json")
	raw, err := utils.Run(exec.Command("kubectl", args...))
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), out)
}
