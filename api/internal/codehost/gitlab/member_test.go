package gitlab

import (
	"testing"
	"time"

	"flow/api/internal/codehost"
)

// A member's own token opens the MR as them on the connection's instance; a
// token GitLab rejects falls back to the workspace connection.
func TestCreateChangeRequestActsAsTheMember(t *testing.T) {
	fake := newFakeGitLab(t)
	host, _ := newTestHost(t, fake, ModeProjectToken, time.Now())
	repo := testRepo(fake)
	// The fake accepts one token; here it stands for the member's OAuth token.
	member := &codehost.ActingUser{FlowUserID: "u1", Login: "alice", Token: fake.apiToken, Bearer: true}
	change, err := host.CreateChangeRequest(t.Context(), "ws", codehost.CreateChangeRequestInput{Repo: repo, Head: "alice/fix-1", Base: "main", Title: "Fix", Draft: true, ActAs: member})
	if err != nil || !change.OpenedByMember {
		t.Fatalf("member MR %+v %v", change, err)
	}
	revoked := &codehost.ActingUser{FlowUserID: "u1", Login: "alice", Token: "glpat-revoked"}
	change, err = host.CreateChangeRequest(t.Context(), "ws", codehost.CreateChangeRequestInput{Repo: repo, Head: "alice/fix-2", Base: "main", Title: "Fix", Draft: true, ActAs: revoked})
	if err != nil || change.OpenedByMember || change.Number == 0 {
		t.Fatalf("fallback MR %+v %v", change, err)
	}
	if got := fake.count("POST /projects/acme%2Fplatform%2Fapi/merge_requests"); got != 3 {
		t.Fatalf("MR creations = %d, want 3 (member, rejected member, connection)", got)
	}
}
