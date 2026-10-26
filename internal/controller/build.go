/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package controller

import (
	"cmp"
	"fmt"
	"maps"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
	"github.com/openconveyor/openconveyor/internal/policy"
)

const (
	// nonRootUID is a conventional unprivileged UID that many hardened
	// base images use (distroless, chainguard). Tasks run as this UID,
	// unless the AgentClass image refuses to start as non-root. In that
	// case the AgentClass author does something that the project
	// explicitly forbids.
	nonRootUID int64 = 65532

	// DefaultJobTTLSeconds is the default for --job-ttl-seconds: how long a
	// finished Job stays so its logs stay readable. One hour is enough for
	// a human to notice and triage.
	DefaultJobTTLSeconds int32 = 3600

	// scratchVolumeName names the emptyDir that the Job mounts at /tmp.
	// Agents then have a writable path despite readOnlyRootFilesystem.
	scratchVolumeName = "scratch"

	// agentContainerName is the name of the single container in every Task
	// Job. It is part of the user-facing contract:
	// `kubectl logs <job-pod> -c agent`.
	agentContainerName = "agent"
)

// buildServiceAccount returns the Task's dedicated ServiceAccount. Its
// automount flag mirrors the pod's flag: false unless the Task declares
// RBAC. The pod spec already withholds the token. The flag on the
// ServiceAccount also withholds the token from any other pod that ever
// runs as this ServiceAccount (defense in depth, #68).
func buildServiceAccount(task *conveyorv1alpha1.Task) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskResourceName(task),
			Namespace: task.Namespace,
			Labels:    ownershipLabels(task),
		},
		AutomountServiceAccountToken: new(policy.NeedsServiceAccountToken(task.Spec.Permissions.RBAC)),
	}
}

// buildJob creates a hardened one-shot Job from the Task and the AgentClass.
//
//   - The pod runs as non-root with the RuntimeDefault seccomp profile,
//     which covers injected sidecars. The container has a read-only
//     rootfs, no privilege escalation, and all capabilities dropped.
//   - The Job mounts the ServiceAccount token only when the Task declares
//     RBAC rules.
//   - The Job projects the Secrets (Task.permissions.secrets ∪
//     AgentClass.requires.secrets) at /run/secrets/<name>/<key>, one file
//     for each Secret key.
//   - The Job mounts the prompt at the path that the AgentClass declares,
//     or not at all when the AgentClass does not read a prompt.
//   - The Task's NetworkPolicy controls egress from outside the Job.
//   - activeDeadlineSeconds is mandatory: Task.spec.resources.timeout
//     bounds the cost.
//   - backoffLimit=0: agent runs are expensive, so there are no blind
//     retries.
//   - ttlSecondsAfterFinished comes from the controller's --job-ttl-seconds.
//
// The caller must first union the agent-required secrets with the
// task-declared secrets into `effectiveSecrets`. The caller must also
// first create any owned ConfigMap that an inline prompt references.
func buildJob(
	task *conveyorv1alpha1.Task,
	agent *conveyorv1alpha1.ClusterAgentClass,
	effectiveSecrets []string,
	ttlSeconds int32,
) (*batchv1.Job, error) {
	secretVolumes, secretMounts := policy.BuildSecretVolumes(effectiveSecrets)
	prompt, err := policy.BuildPromptProjection(task.Name, task.Spec.Prompt, agent.Spec.Inputs)
	if err != nil {
		return nil, err
	}
	config, err := policy.BuildConfigProjection(task.Name, task.Spec.Agent.Config, agent.Spec.Inputs)
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
	if prompt.Volume != nil {
		volumes = append(volumes, *prompt.Volume)
	}
	if config.Volume != nil {
		volumes = append(volumes, *config.Volume)
	}

	mounts := make([]corev1.VolumeMount, 0, 2+len(secretMounts))
	mounts = append(mounts, corev1.VolumeMount{Name: scratchVolumeName, MountPath: "/tmp"})
	mounts = append(mounts, secretMounts...)
	if prompt.Mount != nil {
		mounts = append(mounts, *prompt.Mount)
	}
	if config.Mount != nil {
		mounts = append(mounts, *config.Mount)
	}

	resourceReqs, err := buildResourceRequirements(task.Spec.Resources)
	if err != nil {
		return nil, err
	}

	container := corev1.Container{
		Name:            agentContainerName,
		Image:           agent.Spec.Image,
		ImagePullPolicy: corev1.PullPolicy(cmp.Or(agent.Spec.ImagePullPolicy, string(corev1.PullIfNotPresent))),
		Command:         agent.Spec.Command,
		Args:            agent.Spec.Args,
		Env:             config.Env,
		Resources:       resourceReqs,
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot:             new(true),
			RunAsUser:                new(nonRootUID),
			RunAsGroup:               new(nonRootUID),
			ReadOnlyRootFilesystem:   new(true),
			AllowPrivilegeEscalation: new(false),
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
			AutomountServiceAccountToken: new(policy.NeedsServiceAccountToken(task.Spec.Permissions.RBAC)),
			// The Job sets identity and seccomp on the pod and on the
			// container. Pod-level values apply to containers that a
			// service mesh injects later, and Pod Security Standards
			// "restricted" reads them first. The container block repeats
			// them, so a hand-edited pod template that drops the pod field
			// still runs hardened. The container block also adds the
			// container-only fields (read-only rootfs, no privilege
			// escalation, no capabilities).
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: new(true),
				RunAsUser:    new(nonRootUID),
				RunAsGroup:   new(nonRootUID),
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			Containers: []corev1.Container{container},
			Volumes:    volumes,
		},
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskResourceName(task),
			Namespace: task.Namespace,
			Labels:    ownershipLabels(task),
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            new(int32(0)),
			ActiveDeadlineSeconds:   new(int64(task.Spec.Resources.Timeout.Seconds())),
			TTLSecondsAfterFinished: new(ttlSeconds),
			Template:                pod,
		},
	}, nil
}

func buildResourceRequirements(r conveyorv1alpha1.TaskResources) (corev1.ResourceRequirements, error) {
	reqs := corev1.ResourceRequirements{}
	if r.CPU == "" && r.Memory == "" {
		return reqs, nil
	}
	list := corev1.ResourceList{}
	if r.CPU != "" {
		q, err := resource.ParseQuantity(r.CPU)
		if err != nil {
			return reqs, &policy.PermanentError{Err: fmt.Errorf("spec.resources.cpu %q: %w", r.CPU, err)}
		}
		list[corev1.ResourceCPU] = q
	}
	if r.Memory != "" {
		q, err := resource.ParseQuantity(r.Memory)
		if err != nil {
			return reqs, &policy.PermanentError{Err: fmt.Errorf("spec.resources.memory %q: %w", r.Memory, err)}
		}
		list[corev1.ResourceMemory] = q
	}
	// Requests == Limits gives predictable scheduling; there is no burst headroom.
	reqs.Requests = list.DeepCopy()
	reqs.Limits = list
	return reqs, nil
}

// taskResourceName is the shared name for all resources that the controller
// creates for a Task (Job, ServiceAccount, NetworkPolicy, Role, RoleBinding).
// Task names are already unique in a namespace; the shared name makes the
// resources easy to grep.
func taskResourceName(task *conveyorv1alpha1.Task) string {
	return task.Name
}

// ownershipLabels returns the labels that go on every created resource,
// including the pod-selector label. The ownership labels are a superset of
// the pod selector, so the same map works on the Job, ServiceAccount and
// NetworkPolicy ObjectMeta and on the pod template. The labels that the
// NetworkPolicy selects and the labels that the pod advertises then do not
// differ.
func ownershipLabels(task *conveyorv1alpha1.Task) map[string]string {
	labels := policy.OwnershipLabels(task.Name)
	labels[policy.LabelComponent] = policy.ComponentTask
	return labels
}

// podLabels are the labels on the pod template itself. They must include
// the selector labels that the Task's NetworkPolicy uses.
func podLabels(task *conveyorv1alpha1.Task) map[string]string {
	labels := ownershipLabels(task)
	// PodSelectorLabels is already a subset of OwnershipLabels. The copy is
	// explicit in case the policy package splits them in the future.
	maps.Copy(labels, policy.PodSelectorLabels(task.Name))
	return labels
}
