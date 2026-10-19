package coding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Trusted external sources (docs/coding-sessions-design.md §15, track L).
// Issues created by integrations (email intake, Slack, Intercom, Sentry, API
// applications…) and comments by people who are not workspace members are
// external. Unless their source is listed in
// CodingAgentSettings.TrustedExternalSources, a session they trigger starts
// in awaitingApproval: the team's members are notified and any member can
// approve (→ queued) or reject (→ done, stale). An approval that nobody gives
// within FLOW_CODING_APPROVAL_TIMEOUT ends the session with approvalExpired.

// ExternalOrigin is where a trigger came from.
type ExternalOrigin struct {
	// External is true for integrations, API applications and non-members.
	External bool `json:"external"`
	// Source is the source key: "integration:<name>" (email, slack,
	// intercom…), "appUser:<userId>" (an API application), or
	// "<codeHost>:<owner>" for a PR/MR comment by a non-member.
	Source string `json:"source,omitempty"`
	// Label is a human-readable name for notifications ("Email intake").
	Label string `json:"label,omitempty"`
}

// IntegrationOrigin is the origin of an issue created by an integration.
func IntegrationOrigin(name string) ExternalOrigin {
	name = strings.ToLower(strings.TrimSpace(name))
	return ExternalOrigin{External: true, Source: "integration:" + name, Label: name}
}

// IsTrustedSource reports whether a source key is trusted by the workspace's
// TrustedExternalSources list. Entries may be full keys
// ("integration:slack", "appUser:u1", "github:acme"), bare integration
// names ("slack"), or a code host owner that also covers its repositories
// ("github:acme" trusts "github:acme/web").
func IsTrustedSource(source string, trusted []string) bool {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		return false
	}
	bare := strings.TrimPrefix(source, "integration:")
	for _, entry := range trusted {
		entry = strings.ToLower(strings.TrimSpace(entry))
		switch {
		case entry == "":
		case entry == source, entry == bare && !strings.Contains(entry, ":"):
			return true
		case "integration:"+entry == source:
			return true
		case strings.Contains(entry, ":") && strings.HasPrefix(source, entry+"/"):
			return true
		}
	}
	return false
}

// ApprovalDeadline is when an awaitingApproval session expires. Sessions
// only enter awaitingApproval at creation, so the deadline counts from then.
func (t *Triggers) ApprovalDeadline(session domain.CodingSession) time.Time {
	timeout := t.Config.Timeouts.Approval
	if timeout <= 0 {
		timeout = 7 * 24 * time.Hour
	}
	return session.CreatedAt.Add(timeout)
}

// ApprovalDecision is a member's answer.
type ApprovalDecision string

const (
	DecisionApprove ApprovalDecision = "approve"
	DecisionReject  ApprovalDecision = "reject"
)

// DecideApproval applies a member's approve or reject. It is idempotent: the
// same decision again returns the session unchanged; a conflicting decision
// after the first one fails. An expired request is ended with
// approvalExpired and reported as such.
func (t *Triggers) DecideApproval(ctx context.Context, workspaceKey, sessionID, userID string, isMember bool, decision ApprovalDecision) (domain.CodingSession, error) {
	if t.Sessions == nil {
		return domain.CodingSession{}, &TriggerError{Code: domain.CodingErrSandboxDisabled, Message: "Coding sessions are not available."}
	}
	if !isMember {
		return domain.CodingSession{}, &TriggerError{Code: domain.CodingErrPermissionDenied, Message: "Only workspace members can approve coding sessions from external sources."}
	}
	if decision != DecisionApprove && decision != DecisionReject {
		return domain.CodingSession{}, &TriggerError{Code: domain.CodingErrInternal, Message: "Unknown decision."}
	}
	now := t.now()
	expired := false
	if t.Approver != nil {
		return t.decideThroughApprover(ctx, workspaceKey, sessionID, userID, decision, now)
	}
	updated, err := t.Sessions.UpdateCodingSession(ctx, workspaceKey, sessionID, func(session *domain.CodingSession) error {
		expired = false
		want := ApprovalApproved
		if decision == DecisionReject {
			want = ApprovalRejected
		}
		if session.Phase != domain.CodingPhaseAwaitingApproval {
			if session.ApprovalState == want {
				return nil // already decided the same way
			}
			if session.ErrorCode == domain.CodingErrApprovalExpired {
				return &TriggerError{Code: domain.CodingErrApprovalExpired, Message: "This approval request expired."}
			}
			return &TriggerError{Code: domain.CodingErrPermissionDenied, Message: "This session is not waiting for approval."}
		}
		if !now.Before(t.ApprovalDeadline(*session)) {
			expireApproval(session, now)
			expired = true
			return nil
		}
		session.ApprovalState = want
		session.ApprovedBy = userID
		session.UpdatedAt = now
		if decision == DecisionApprove {
			session.Phase = domain.CodingPhaseQueued
			session.State = domain.CodingSessionPending
			return nil
		}
		session.Phase = domain.CodingPhaseDone
		session.State = domain.CodingSessionStale
		session.EndedAt = &now
		return nil
	})
	if err != nil {
		return domain.CodingSession{}, err
	}
	if expired {
		if notifyErr := t.notifier().ApprovalResolved(ctx, updated, "expired", ""); notifyErr != nil {
			return updated, fmt.Errorf("coding trigger: approval notification: %w", notifyErr)
		}
		return updated, &TriggerError{Code: domain.CodingErrApprovalExpired, Message: "This approval request expired."}
	}
	resolved := ApprovalApproved
	if decision == DecisionReject {
		resolved = ApprovalRejected
	}
	if err := t.notifier().ApprovalResolved(ctx, updated, resolved, userID); err != nil {
		return updated, fmt.Errorf("coding trigger: approval notification: %w", err)
	}
	return updated, nil
}

// decideThroughApprover is DecideApproval on the session core: the same
// idempotency and expiry rules, with the transition made by Approver (the
// session reconciler expires overdue requests on its own).
func (t *Triggers) decideThroughApprover(ctx context.Context, workspaceKey, sessionID, userID string, decision ApprovalDecision, now time.Time) (domain.CodingSession, error) {
	want := ApprovalApproved
	if decision == DecisionReject {
		want = ApprovalRejected
	}
	session, err := t.Sessions.CodingSession(ctx, workspaceKey, sessionID)
	if err != nil {
		return session, err
	}
	if session.Phase != domain.CodingPhaseAwaitingApproval {
		switch {
		case session.ApprovalState == want:
			return session, nil // already decided the same way
		case session.ErrorCode == domain.CodingErrApprovalExpired:
			return session, &TriggerError{Code: domain.CodingErrApprovalExpired, Message: "This approval request expired."}
		}
		return session, &TriggerError{Code: domain.CodingErrPermissionDenied, Message: "This session is not waiting for approval."}
	}
	if !now.Before(t.ApprovalDeadline(session)) {
		return session, &TriggerError{Code: domain.CodingErrApprovalExpired, Message: "This approval request expired."}
	}
	updated, err := t.Approver(ctx, workspaceKey, sessionID, userID, decision == DecisionReject)
	if err != nil {
		return updated, err
	}
	if err := t.notifier().ApprovalResolved(ctx, updated, want, userID); err != nil {
		return updated, fmt.Errorf("coding trigger: approval notification: %w", err)
	}
	return updated, nil
}

func expireApproval(session *domain.CodingSession, now time.Time) {
	session.Phase = domain.CodingPhaseDone
	session.State = domain.CodingSessionError
	session.ErrorCode = domain.CodingErrApprovalExpired
	session.Error = "Nobody approved this coding session in time."
	session.UpdatedAt = now
	session.EndedAt = &now
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}
