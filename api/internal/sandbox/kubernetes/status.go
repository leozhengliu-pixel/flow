package kubernetes

import (
	"encoding/json"
	"maps"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"

	"flow/api/internal/sandbox"
)

// imagePullReasons are container waiting reasons that mean the image cannot
// be pulled.
var imagePullReasons = map[string]bool{
	"ErrImagePull": true, "ImagePullBackOff": true, "InvalidImageName": true,
	"ErrImageNeverPull": true, "RegistryUnavailable": true,
}

// podStatus maps a Pod to the provider-neutral status (design §22).
func (p *Provider) podStatus(pod *corev1.Pod) sandbox.SandboxStatus {
	status := sandbox.SandboxStatus{
		ID:          pod.Labels[sandbox.LabelSandboxID],
		Kind:        sandbox.Kind(pod.Labels[sandbox.LabelKind]),
		ProviderRef: pod.Name,
		Node:        pod.Spec.NodeName,
		Region:      pod.Labels[sandbox.LabelRegion],
		CreatedAt:   pod.CreationTimestamp.Time,
		Labels:      maps.Clone(pod.Labels),
	}
	runner := containerStatus(pod.Status.ContainerStatuses, runnerContainer)
	restore := containerStatus(pod.Status.InitContainerStatuses, restoreContainer)
	if runner != nil {
		if running := runner.State.Running; running != nil {
			status.StartedAt = new(running.StartedAt.Time)
		}
		if done := runner.State.Terminated; done != nil {
			if !done.StartedAt.IsZero() {
				status.StartedAt = new(done.StartedAt.Time)
			}
			status.FinishedAt = new(done.FinishedAt.Time)
			status.ExitCode = new(int(done.ExitCode))
		}
	}
	switch pod.Status.Phase {
	case corev1.PodPending, "":
		status.Phase = sandbox.PhasePending
		if scheduled := podCondition(pod, corev1.PodScheduled); scheduled != nil {
			if scheduled.Status == corev1.ConditionTrue {
				status.Phase = sandbox.PhaseStarting
			} else if scheduled.Reason == corev1.PodReasonUnschedulable {
				status.Reason, status.Message = sandbox.ReasonUnschedulable, scheduled.Message
			}
		}
		for _, container := range []*corev1.ContainerStatus{restore, runner} {
			if container != nil && container.State.Waiting != nil && imagePullReasons[container.State.Waiting.Reason] {
				status.Reason, status.Message = sandbox.ReasonImagePull, container.State.Waiting.Message
			}
		}
	case corev1.PodRunning:
		status.Phase = sandbox.PhaseRunning
		if runner != nil && runner.State.Terminated != nil {
			status.Phase = exitPhase(runner.State.Terminated.ExitCode)
		}
	case corev1.PodSucceeded:
		status.Phase = sandbox.PhaseSucceeded
	case corev1.PodFailed:
		status.Phase = sandbox.PhaseFailed
		status.Reason, status.Message = failureReason(pod, runner, restore)
	default: // PodUnknown: the node stopped reporting (node lost).
		status.Phase = sandbox.PhaseFailed
		status.Reason, status.Message = sandbox.ReasonUnknown, "pod status unknown (node lost)"
	}
	if status.FinishedAt == nil && status.Phase.Terminal() {
		if pod.DeletionTimestamp != nil {
			status.FinishedAt = new(pod.DeletionTimestamp.Time)
		}
	}
	return status
}

func exitPhase(code int32) sandbox.Phase {
	if code == 0 {
		return sandbox.PhaseSucceeded
	}
	return sandbox.PhaseFailed
}

func failureReason(pod *corev1.Pod, runner, restore *corev1.ContainerStatus) (sandbox.Reason, string) {
	switch pod.Status.Reason {
	case "Evicted":
		return sandbox.ReasonEvicted, pod.Status.Message
	case "DeadlineExceeded":
		return sandbox.ReasonDeadlineExceeded, pod.Status.Message
	}
	if restore != nil && restore.State.Terminated != nil && restore.State.Terminated.ExitCode != 0 {
		return sandbox.ReasonInitFailed, restore.State.Terminated.Message
	}
	if restore != nil && restore.State.Waiting != nil && imagePullReasons[restore.State.Waiting.Reason] {
		return sandbox.ReasonImagePull, restore.State.Waiting.Message
	}
	if runner != nil {
		if done := runner.State.Terminated; done != nil {
			if done.Reason == "OOMKilled" {
				return sandbox.ReasonOOMKilled, "the runner ran out of memory; use a larger sandbox size"
			}
			if done.ExitCode != 0 {
				return sandbox.ReasonExitNonZero, done.Message
			}
		}
		if runner.State.Waiting != nil && imagePullReasons[runner.State.Waiting.Reason] {
			return sandbox.ReasonImagePull, runner.State.Waiting.Message
		}
	}
	if pod.Status.Reason != "" {
		return sandbox.ReasonUnknown, pod.Status.Reason + ": " + pod.Status.Message
	}
	return sandbox.ReasonUnknown, pod.Status.Message
}

func containerStatus(list []corev1.ContainerStatus, name string) *corev1.ContainerStatus {
	for i := range list {
		if list[i].Name == name {
			return &list[i]
		}
	}
	return nil
}

func podCondition(pod *corev1.Pod, kind corev1.PodConditionType) *corev1.PodCondition {
	for i := range pod.Status.Conditions {
		if pod.Status.Conditions[i].Type == kind {
			return &pod.Status.Conditions[i]
		}
	}
	return nil
}

// mergePatch encodes a JSON merge patch.
func mergePatch(value any) ([]byte, error) { return json.Marshal(value) }

// equalJSON compares two API structs by their JSON form (ignores nil vs
// empty differences the API server normalizes).
func equalJSON(a, b any) bool {
	left, err1 := json.Marshal(a)
	right, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return reflect.DeepEqual(a, b)
	}
	var l, r any
	_ = json.Unmarshal(left, &l)
	_ = json.Unmarshal(right, &r)
	return reflect.DeepEqual(l, r)
}

// age is how long ago t was.
func (p *Provider) age(t time.Time) time.Duration { return p.now().Sub(t) }
