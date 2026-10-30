// Adapted from zeabur-oss/zeabur (Apache-2.0), crates/zeabur-graph/src/workload.rs
// (deployment_phase, phase, job_phase) and crates/zeabur-graph/src/health.rs (failing reasons).
// Copyright Zeabur contributors; modified for atoll. See THIRD_PARTY_NOTICES.md.

package k8s

// Phase is a workload's phase, from its rollout conditions rather than replica counts alone,
// so a stuck rollout is reported as one.
type Phase string

const (
	PhaseRunning     Phase = "Running"     // every desired replica up to date and ready
	PhaseProgressing Phase = "Progressing" // a rollout is under way
	PhaseDegraded    Phase = "Degraded"    // nothing ready although no rollout is pending
	PhasePaused      Phase = "Paused"      // paused, or scaled to zero
	PhaseSuspended   Phase = "Suspended"   // a suspended Job or CronJob
	PhaseCompleted   Phase = "Completed"   // a Job that finished
	PhaseFailed      Phase = "Failed"      // rollout past its deadline, or a failed Job
	PhaseUnknown     Phase = "Unknown"
)

func desiredReplicas(r *int) int {
	if r == nil {
		return 1
	}
	return *r
}

// DeploymentPhase: a rollout is under way until the controller has observed the current
// generation and its Progressing condition says the new ReplicaSet is available. Progressing
// False with ProgressDeadlineExceeded is a failed rollout. With no ready pod and no rollout
// under way, pods that were once available have stopped serving: degraded.
func DeploymentPhase(d Object) Phase {
	if d.Spec.Paused {
		return PhasePaused
	}
	unobserved := d.Status.ObservedGeneration < d.Metadata.Generation
	rolling := unobserved
	for _, c := range d.Status.Conditions {
		if c.Type != "Progressing" {
			continue
		}
		if c.Status == "False" && c.Reason == "ProgressDeadlineExceeded" {
			return PhaseFailed
		}
		if c.Status == "True" && c.Reason != "NewReplicaSetAvailable" {
			rolling = true
		}
	}
	desired := desiredReplicas(d.Spec.Replicas)
	ready, updated := d.Status.ReadyReplicas, d.Status.UpdatedReplicas
	switch {
	case desired == 0:
		return PhasePaused
	case !unobserved && ready >= desired && updated >= desired:
		return PhaseRunning
	case ready == 0 && !rolling:
		return PhaseDegraded
	}
	return PhaseProgressing
}

// WorkloadPhase is the phase of any workload kind.
func WorkloadPhase(w Object) Phase {
	switch w.Kind {
	case "Deployment":
		return DeploymentPhase(w)
	case "StatefulSet":
		desired := desiredReplicas(w.Spec.Replicas)
		switch {
		case desired == 0:
			return PhasePaused
		case w.Status.ReadyReplicas >= desired:
			return PhaseRunning
		case w.Status.ReadyReplicas == 0:
			return PhaseDegraded
		}
		return PhaseProgressing
	case "Job":
		desired := desiredReplicas(w.Spec.Completions)
		complete, failed := false, false
		for _, c := range w.Status.Conditions {
			if c.Status == "True" && c.Type == "Complete" {
				complete = true
			}
			if c.Status == "True" && c.Type == "Failed" {
				failed = true
			}
		}
		switch {
		case w.Spec.Suspend != nil && *w.Spec.Suspend:
			return PhaseSuspended
		case complete || w.Status.Succeeded >= desired:
			return PhaseCompleted
		case failed:
			return PhaseFailed
		case w.Status.Active > 0:
			return PhaseProgressing
		case w.Status.Failed > 0:
			return PhaseFailed
		}
		return PhaseUnknown
	case "CronJob":
		if w.Spec.Suspend != nil && *w.Spec.Suspend {
			return PhaseSuspended
		}
		return PhaseRunning
	}
	return PhaseUnknown
}

// failingReasons are waiting/termination reasons meaning a container keeps failing.
var failingReasons = map[string]bool{
	"CrashLoopBackOff": true, "ImagePullBackOff": true, "ErrImagePull": true, "InvalidImageName": true,
	"CreateContainerConfigError": true, "CreateContainerError": true, "RunContainerError": true,
	"OOMKilled": true, "Error": true,
}

// PodFailing reports whether a pod keeps failing: phase Failed, a container waiting in
// CrashLoopBackOff, or — when not ready — a container waiting/terminated with a failing reason.
// (The time-based restart back-off rule of zeabur-oss is not ported: scans have no clock.)
func PodFailing(p Object) bool {
	switch p.Status.Phase {
	case "Failed":
		return true
	case "Succeeded":
		return false
	}
	ready := len(p.Status.ContainerStatuses) > 0
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			return true
		}
		ready = ready && cs.Ready
	}
	if ready {
		return false
	}
	if failingReasons[p.Status.Reason] {
		return true
	}
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil && failingReasons[cs.State.Waiting.Reason] {
			return true
		}
		if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 && failingReasons[cs.State.Terminated.Reason] {
			return true
		}
	}
	return false
}

// Scan statuses reported on api.ZeaburService.Status.
const (
	StatusRunning     = "running"
	StatusProgressing = "progressing"
	StatusDegraded    = "degraded"
	StatusFailed      = "failed"
	StatusSuspended   = "suspended"
	StatusUnknown     = "unknown"
)

// ScanStatus folds a workload's phase and its pods into one word: a failing pod fails the
// service (a stuck rollout with a ready replica is "degraded": the old release still serves);
// otherwise the phase decides. No workload (Zeabur deletes it on suspend) or zero replicas is
// "suspended". Pods without status (not collected) are ignored.
func ScanStatus(w *Object, pods []Object) string {
	if w == nil {
		return StatusSuspended
	}
	ph := WorkloadPhase(*w)
	if (w.Kind == "Deployment" || w.Kind == "StatefulSet") && desiredReplicas(w.Spec.Replicas) > 0 &&
		w.Metadata.Generation == 0 && w.Status.ObservedGeneration == 0 && len(w.Status.Conditions) == 0 && w.Status.ReadyReplicas == 0 {
		// The API server always sets generation; none means the status was not collected
		// (hand-written manifest), so the phase cannot be told.
		ph = PhaseUnknown
	}
	for _, p := range pods {
		if PodFailing(p) {
			if w.Kind == "Deployment" && (ph == PhaseProgressing || ph == PhaseFailed) && w.Status.ReadyReplicas > 0 {
				return StatusDegraded
			}
			return StatusFailed
		}
	}
	switch ph {
	case PhaseRunning, PhaseCompleted:
		return StatusRunning
	case PhaseProgressing:
		return StatusProgressing
	case PhaseDegraded:
		return StatusDegraded
	case PhaseFailed:
		return StatusFailed
	case PhasePaused, PhaseSuspended:
		return StatusSuspended
	}
	return StatusUnknown
}
