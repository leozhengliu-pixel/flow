package coding

import (
	"context"
	"testing"

	"flow/api/internal/domain"
)

type actorDirectoryFake struct {
	allowed  bool
	readable bool
}

func (f *actorDirectoryFake) CodingIssue(context.Context, string, string) (IssueContext, error) {
	return IssueContext{Title: "server read"}, nil
}
func (f *actorDirectoryFake) CodingUser(context.Context, string, string) (UserInfo, error) {
	return UserInfo{}, nil
}
func (f *actorDirectoryFake) CodingActorAllowed(_ context.Context, _, _ string, allowGuest bool) error {
	if !f.allowed {
		return ErrForbidden
	}
	return nil
}
func (f *actorDirectoryFake) CodingIssueFor(context.Context, string, string, string) (IssueContext, error) {
	if !f.readable {
		return IssueContext{}, ErrNotFound
	}
	return IssueContext{Title: "actor read"}, nil
}
func (f *actorDirectoryFake) CodingSourceReadable(context.Context, string, string, string, string, string) error {
	return nil
}

// Provisioning (environment secrets, git credentials) happens only for a
// creator who is still allowed and can still read the issue.
func TestCreatorStillAllowedBeforeProvisioning(t *testing.T) {
	directory := &actorDirectoryFake{allowed: true, readable: true}
	s := &Service{directory: directory}
	session := domain.CodingSession{WorkspaceKey: "w", CreatorID: "u", IssueID: "i"}
	if outcome := s.creatorStillAllowed(t.Context(), session); outcome != nil {
		t.Fatalf("allowed creator: %+v", outcome)
	}
	directory.readable = false
	if outcome := s.creatorStillAllowed(t.Context(), session); outcome == nil || outcome.Code != domain.CodingErrPermissionDenied || outcome.Transient {
		t.Fatalf("creator lost the issue: %+v", outcome)
	}
	directory.readable, directory.allowed = true, false
	if outcome := s.creatorStillAllowed(t.Context(), session); outcome == nil || outcome.Code != domain.CodingErrPermissionDenied {
		t.Fatalf("creator left the workspace: %+v", outcome)
	}
	// Later reads of the issue use the creator's access, not the server's.
	directory.readable = false
	if _, err := s.issueFor(t.Context(), "w", "u", "i"); err != ErrNotFound {
		t.Fatalf("issueFor: %v", err)
	}
	// A directory without actor support keeps the server's read.
	plain := &Service{}
	if outcome := plain.creatorStillAllowed(t.Context(), session); outcome != nil {
		t.Fatalf("no actor directory: %+v", outcome)
	}
}
