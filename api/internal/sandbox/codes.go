package sandbox

import (
	"errors"

	"flow/api/internal/domain"
)

// Provider errors added by track A. They wrap ErrTransient where a retry can
// help, so callers that only know ErrTransient keep working.
var (
	// ErrQuota reports a ResourceQuota (or equivalent) admission rejection.
	// It is retried without consuming start attempts until the session's
	// starting phase times out (design §22: unschedulable / capacity).
	ErrQuota = errors.New("sandbox: resource quota exceeded")
	// ErrImagePull reports that the sandbox image could not be pulled.
	ErrImagePull = errors.New("sandbox: image pull failed")
	// ErrPending reports an operation that was accepted but has not finished
	// yet (e.g. a VolumeSnapshot that is not readyToUse); call again later.
	ErrPending = errors.New("sandbox: operation pending")
)

// IsTransient reports whether err is worth retrying.
func IsTransient(err error) bool {
	return errors.Is(err, ErrTransient) || errors.Is(err, ErrQuota) || errors.Is(err, ErrPending)
}

// Terminal reports whether a phase is final for the sandbox's current
// incarnation (a new attempt always uses a new sandbox id).
func (p Phase) Terminal() bool {
	return p == PhaseSucceeded || p == PhaseFailed || p == PhaseMissing
}

// ErrorCode maps an observed sandbox failure to the session error code the
// session reconciler reports (design §4.4, §22). kind distinguishes snapshot
// builds, whose failures are snapshotFailed. It returns "" for phases that
// are not failures.
func ErrorCode(kind Kind, phase Phase, reason Reason) domain.CodingErrorCode {
	switch phase {
	case PhaseFailed, PhaseMissing:
	default:
		if phase == PhasePending && reason == ReasonImagePull {
			return domain.CodingErrImagePullFailed
		}
		return ""
	}
	switch reason {
	case ReasonImagePull:
		return domain.CodingErrImagePullFailed
	case ReasonUnschedulable:
		return domain.CodingErrUnschedulable
	case ReasonQuota:
		return domain.CodingErrCapacityUnavailable
	case ReasonInitFailed:
		// The restore init container failed: the snapshot archive is
		// missing or corrupt (design §10: retry once, then inline setup).
		return domain.CodingErrSnapshotFailed
	case ReasonDeadlineExceeded:
		if kind == KindSnapshot {
			return domain.CodingErrSnapshotFailed
		}
		return domain.CodingErrSessionTimeout
	}
	if kind == KindSnapshot {
		return domain.CodingErrSnapshotFailed
	}
	if phase == PhaseMissing || reason == ReasonEvicted || reason == ReasonOOMKilled {
		return domain.CodingErrSandboxLost
	}
	if reason == ReasonStartFailed {
		return domain.CodingErrSandboxStartFailed
	}
	return domain.CodingErrSandboxLost
}

// ReasonStartFailed marks a sandbox the reconciler could not start (retries
// exhausted or a non-transient provider error).
const ReasonStartFailed Reason = "StartFailed"
