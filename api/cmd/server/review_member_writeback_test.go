package main

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func lastReviewEvent(review domain.CodeReview) domain.ReviewEvent {
	return review.Events[len(review.Events)-1]
}

// Comment write-back posts as the member's linked account: the comment and
// a threaded reply reach GitLab with the member's token and record who they
// were posted as.
func TestReviewWriteBackPostsAsTheMember(t *testing.T) {
	f := newMemberAccountsFixture(t)
	review := f.addReview()
	if rec := f.linkPAT("glpat-member"); rec.Code != http.StatusOK {
		t.Fatalf("link: %d %s", rec.Code, rec.Body.String())
	}
	base := "/api/reviews/" + review.ID
	updated := requestJSON[domain.CodeReview](t, f.handler, http.MethodPost, base+"/comments?workspace=test-workspace", map[string]string{"body": "Root"}, http.StatusCreated)
	root := lastReviewEvent(updated)
	if root.Sync != nil || root.Remote == nil || root.PostedAs == nil || *root.PostedAs != (domain.ReviewPostedAs{Provider: "gitlab", Login: "alice-gl", Via: domain.CodeHostAccountActorMember}) {
		t.Fatalf("root = sync %+v remote %+v postedAs %+v", root.Sync, root.Remote, root.PostedAs)
	}
	updated = requestJSON[domain.CodeReview](t, f.handler, http.MethodPost, base+"/comments?workspace=test-workspace", map[string]string{"body": "Reply", "parentId": root.ID}, http.StatusCreated)
	reply := lastReviewEvent(updated)
	if reply.Sync != nil || reply.Remote == nil || reply.Remote.ThreadID != root.Remote.ThreadID || reply.PostedAs == nil || reply.PostedAs.Via != domain.CodeHostAccountActorMember {
		t.Fatalf("reply = sync %+v remote %+v postedAs %+v", reply.Sync, reply.Remote, reply.PostedAs)
	}
	want := []string{"discussions glpat-member", "discussions/" + root.Remote.ThreadID + "/notes glpat-member"}
	if posts := f.gitlab.postsSnapshot(); !slices.Equal(posts, want) {
		t.Fatalf("posts %v, want %v", posts, want)
	}
}

// A member token the host rejects as invalid (401) falls back to the
// workspace credential, says so, and asks the member to reconnect.
func TestReviewWriteBackFallsBackToTheWorkspaceOnRevokedToken(t *testing.T) {
	f := newMemberAccountsFixture(t)
	review := f.addReview()
	if rec := f.linkPAT("glpat-member"); rec.Code != http.StatusOK {
		t.Fatalf("link: %d %s", rec.Code, rec.Body.String())
	}
	f.gitlab.mu.Lock()
	delete(f.gitlab.scopes, "glpat-member")
	f.gitlab.mu.Unlock()
	updated := requestJSON[domain.CodeReview](t, f.handler, http.MethodPost, "/api/reviews/"+review.ID+"/comments?workspace=test-workspace", map[string]string{"body": "Via the workspace"}, http.StatusCreated)
	event := lastReviewEvent(updated)
	if event.Sync != nil || event.Remote == nil || event.PostedAs == nil || *event.PostedAs != (domain.ReviewPostedAs{Provider: "gitlab", Login: "flow-bot", Via: domain.CodeHostAccountActorBot}) {
		t.Fatalf("event = sync %+v remote %+v postedAs %+v", event.Sync, event.Remote, event.PostedAs)
	}
	if posts := f.gitlab.postsSnapshot(); !slices.Equal(posts, []string{"discussions " + testWorkspaceToken}) {
		t.Fatalf("posts %v", posts)
	}
	if accounts := f.accounts().Accounts; len(accounts) != 1 || accounts[0].Status != domain.CodeHostAccountReconnect {
		t.Fatalf("revoked token must ask to reconnect: %+v", accounts)
	}
}

// A member refused by the host (403) is not escalated to the workspace
// credential: the comment stays in Flow with a failed sync and a clear
// error, and Retry posts it as the member once they have access. The same
// holds for resolving a thread.
func TestReviewWriteBackRefusedMemberFailsThenRetrySucceeds(t *testing.T) {
	f := newMemberAccountsFixture(t)
	review := f.addReview()
	if rec := f.linkPAT("glpat-member"); rec.Code != http.StatusOK {
		t.Fatalf("link: %d %s", rec.Code, rec.Body.String())
	}
	base := "/api/reviews/" + review.ID
	updated := requestJSON[domain.CodeReview](t, f.handler, http.MethodPost, base+"/comments?workspace=test-workspace", map[string]any{"body": "Thread"}, http.StatusCreated)
	thread := lastReviewEvent(updated)
	// The thread is on a line, so it resolves on GitLab.
	if err := f.repo.MutateWorkspace(t.Context(), "test-workspace", "test.thread_path", review.ID, nil, func(data *domain.Bootstrap) error {
		index := reviewIndex(*data, review.ID)
		for i := range data.Reviews[index].Events {
			if data.Reviews[index].Events[i].ID == thread.ID {
				data.Reviews[index].Events[i].Path, data.Reviews[index].Events[i].Line = "main.go", 3
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	f.gitlab.mu.Lock()
	f.gitlab.readOnly["glpat-member"] = true
	f.gitlab.mu.Unlock()
	updated = requestJSON[domain.CodeReview](t, f.handler, http.MethodPost, base+"/comments?workspace=test-workspace", map[string]string{"body": "Refused"}, http.StatusCreated)
	refused := lastReviewEvent(updated)
	if refused.Body != "Refused" || refused.Remote != nil || refused.PostedAs != nil || refused.Sync == nil || refused.Sync.State != "failed" || refused.Sync.Op != "create" ||
		!strings.Contains(refused.Sync.Error, "Your GitLab account (@alice-gl) can't") {
		t.Fatalf("refused comment = %+v sync %+v", refused, refused.Sync)
	}
	updated = requestJSON[domain.CodeReview](t, f.handler, http.MethodPatch, base+"/comments/"+thread.ID+"?workspace=test-workspace", map[string]bool{"resolved": true}, http.StatusOK)
	resolved := findReviewEvent(t, updated, thread.ID)
	if !resolved.Resolved || resolved.Sync == nil || resolved.Sync.State != "failed" || resolved.Sync.Op != "resolve" || !strings.Contains(resolved.Sync.Error, "Your GitLab account") {
		t.Fatalf("refused resolution = %+v", resolved.Sync)
	}
	if posts := f.gitlab.postsSnapshot(); slices.ContainsFunc(posts, func(post string) bool { return strings.HasSuffix(post, testWorkspaceToken) }) {
		t.Fatalf("a refused member change was retried with the workspace token: %v", posts)
	}
	if seen := f.gitlab.seenSnapshot(); slices.ContainsFunc(seen, func(line string) bool {
		return strings.HasPrefix(line, "PUT ") && strings.HasSuffix(line, testWorkspaceToken)
	}) {
		t.Fatalf("a refused resolution was retried with the workspace token: %v", seen)
	}

	// The member gets access: Retry (sync) posts both as the member.
	f.gitlab.mu.Lock()
	delete(f.gitlab.readOnly, "glpat-member")
	f.gitlab.mu.Unlock()
	updated = requestJSON[domain.CodeReview](t, f.handler, http.MethodPost, base+"/sync?workspace=test-workspace", map[string]bool{"force": true}, http.StatusOK)
	retried := findReviewEvent(t, updated, refused.ID)
	if retried.Sync != nil || retried.Remote == nil || retried.PostedAs == nil || retried.PostedAs.Via != domain.CodeHostAccountActorMember || retried.PostedAs.Login != "alice-gl" {
		t.Fatalf("retried comment = sync %+v remote %+v postedAs %+v", retried.Sync, retried.Remote, retried.PostedAs)
	}
	if root := findReviewEvent(t, updated, thread.ID); !root.Resolved || root.Sync != nil {
		t.Fatalf("retried resolution = %+v", root.Sync)
	}
	if seen := f.gitlab.seenSnapshot(); !slices.ContainsFunc(seen, func(line string) bool {
		return strings.HasPrefix(line, "PUT ") && strings.Contains(line, "/discussions/"+thread.Remote.ThreadID) && strings.HasSuffix(line, "glpat-member")
	}) {
		t.Fatalf("resolution was not sent as the member: %v", seen)
	}
}

// Member refusals (403) and objects the member cannot see (404) never fall
// back to the workspace credential; other remotes are not tried.
func TestMemberReviewRemoteDoesNotEscalate(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		calls := []string{}
		remote := &memberReviewRemote{
			member:    scriptedReviewRemote{name: "member", err: &reviewRemoteError{Provider: "github", Status: status}, calls: &calls},
			account:   domain.CodeHostAccount{Login: "alice"},
			workspace: scriptedReviewRemote{name: "workspace", calls: &calls},
		}
		err := remote.REST(t.Context(), http.MethodPost, "/issues/1/comments", nil, nil)
		var memberErr *reviewMemberAccessError
		if !errors.As(err, &memberErr) || strings.Join(calls, ",") != "member" || remote.PostedAs() != nil {
			t.Fatalf("%d: err %v calls %v", status, err, calls)
		}
		if message := reviewSyncErrorMessage("github", err); !strings.HasPrefix(message, "Your GitHub account (@alice) can't") {
			t.Fatalf("%d: message %q", status, message)
		}
	}
}
