/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package controller

import (
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
	"github.com/openconveyor/openconveyor/internal/policy"
)

const (
	// nonRootUID is a conventional unprivileged UID used by many hardened
	// base images (distroless, chainguard). Tasks run as this UID unless
	// the AgentClass image refuses to start as non-root — in which case
	// the AgentClass author is doing something we explicitly forbid.
	nonRootUID int64 = 65532

	// defaultTTLSeconds is how long a finished Job sticks around so its
	// logs stay readable. An hour is enough for a human to notice + triage.
	defaultTTLSeconds int32 = 3600

	// scratchVolumeName is an emptyDir mounted at /tmp so agents have a
	// writable path despite readOnlyRootFilesystem.
	scratchVolumeName = "scratch"
)

// buildServiceAccount returns the Task's dedicated ServiceAccount. Token
// automounting is governed by the pod spec (not the SA) so we always keep
// the SA's AutomountServiceAccountToken unset; the Job decides.
func buildServiceAccount(task *conveyorv1alpha1.Task) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskResourceName(task),
			Namespace: task.Namespace,
			Labels:    ownershipLabels(task),
		},
	}
}

// buildJob materializes the Task + AgentClass into a hardened one-shot Job.
//
//   - non-root, read-only rootfs, no privilege escalation, all caps dropped
//   - SA token mounted only when the Task declared RBAC rules
//   - Secrets (Task.permissions.secrets ∪ AgentClass.requires.secrets)
//     projected at /run/secrets/<name>
//   - Prompt mounted at the AgentClass-declared path (or absent if the
//     AgentClass does not read a prompt)
//   - egress is governed externally by the Task's NetworkPolicy
//   - activeDeadlineSeconds mandatory — Task.spec.resources.timeout bounds cost
//   - backoffLimit=0 — agent runs are expensive; no blind retries
//
// The caller is expected to have already unioned agent-required secrets
// with task-declared secrets into `effectiveSecrets`, and to have
// created any owned ConfigMap that an inline prompt references.
func buildJob(
	task *conveyorv1alpha1.Task,
	agent *conveyorv1alpha1.ClusterAgentClass,
	effectiveSecrets []string,
) (*batchv1.Job, error) {
	timeoutSeconds := int64(task.Spec.Resources.Timeout.Duration.Seconds())
	backoffLimit := int32(0)
	ttl := defaultTTLSeconds
	runAsNonRoot := true
	runAsUser := nonRootUID
	runAsGroup := nonRootUID
	readOnlyRoot := true
	allowPrivEsc := false
	automount := policy.NeedsServiceAccountToken(task.Spec.Permissions.RBAC)

	secretVolumes, secretMounts := policy.BuildSecretVolumes(effectiveSecrets)
	promptVol, promptMount, err := policy.BuildPromptProjection(task.Name, task.Spec.Prompt, agent.Spec.Inputs)
	if err != nil {
		return nil, err
	}

	volumes := make([]corev1.Volume, 0, 2+len(secretVolumes))
	volumes = append(volumes, corev1.Volume{
		Name: scratchVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	})
	volumes = append(volumes, secretVolumes...)
	if promptVol != nil {
		volumes = append(volumes, *promptVol)
	}

	mounts := make([]corev1.VolumeMount, 0, 2+len(secretMounts))
	mounts = append(mounts, corev1.VolumeMount{Name: scratchVolumeName, MountPath: "/tmp"})
	mounts = append(mounts, secretMounts...)
	if promptMount != nil {
		mounts = append(mounts, *promptMount)
	}

	container := corev1.Container{
		Name:            "agent",
		Image:           agent.Spec.Image,
		ImagePullPolicy: corev1.PullPolicy(defaultString(agent.Spec.ImagePullPolicy, string(corev1.PullIfNotPresent))),
		Command:         agent.Spec.Command,
		Args:            agent.Spec.Args,
		Resources:       buildResourceRequirements(task.Spec.Resources),
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot:             &runAsNonRoot,
			RunAsUser:                &runAsUser,
			RunAsGroup:               &runAsGroup,
			ReadOnlyRootFilesystem:   &readOnlyRoot,
			AllowPrivilegeEscalation: &allowPrivEsc,
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
		VolumeMounts: mounts,
	}

	pod := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: podLabels(task),
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			ServiceAccountName:           taskResourceName(task),
			AutomountServiceAccountToken: &automount,
			Containers:                   []corev1.Container{container},
			Volumes:                      volumes,
		},
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskResourceName(task),
			Namespace: task.Namespace,
			Labels:    ownershipLabels(task),
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			ActiveDeadlineSeconds:   &timeoutSeconds,
			TTLSecondsAfterFinished: &ttl,
			Template:                pod,
		},
	}, nil
}

func buildResourceRequirements(r conveyorv1alpha1.TaskResources) corev1.ResourceRequirements {
	reqs := corev1.ResourceRequirements{}
	if r.CPU == "" && r.Memory == "" {
		return reqs
	}
	list := corev1.ResourceList{}
	if r.CPU != "" {
		list[corev1.ResourceCPU] = resource.MustParse(r.CPU)
	}
	if r.Memory != "" {
		list[corev1.ResourceMemory] = resource.MustParse(r.Memory)
	}
	// Requests == Limits for predictable scheduling; no burst headroom.
	reqs.Requests = list.DeepCopy()
	reqs.Limits = list
	return reqs
}

// taskResourceName is the shared name for all resources materialized for a
// Task (Job, ServiceAccount, NetworkPolicy, Role, RoleBinding). Task names
// are already namespace-unique; reusing the name keeps things greppable.
func taskResourceName(task *conveyorv1alpha1.Task) string {
	return task.Name
}

// ownershipLabels go on every materialized resource and include the
// pod-selector label. Keeping ownership labels a superset of the pod
// selector means the same map works on the Job/SA/NP ObjectMeta and on
// the pod template — no mismatch between what the NetworkPolicy selects
// and what the pod advertises.
func ownershipLabels(task *conveyorv1alpha1.Task) map[string]string {
	labels := policy.OwnershipLabels(task.Name)
	labels["app.kubernetes.io/component"] = "task"
	return labels
}

// podLabels are the labels on the pod template itself. Must include the
// selector labels used by the Task's NetworkPolicy.
func podLabels(task *conveyorv1alpha1.Task) map[string]string {
	labels := policy.OwnershipLabels(task.Name)
	labels["app.kubernetes.io/component"] = "task"
	// PodSelectorLabels is already a subset of OwnershipLabels, but be
	// explicit in case the policy package splits them in the future.
	for k, v := range policy.PodSelectorLabels(task.Name) {
		labels[k] = v
	}
	return labels
}

func defaultString(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
