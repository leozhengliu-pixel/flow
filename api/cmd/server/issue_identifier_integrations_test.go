package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A pull request naming an issue by the identifier it had before it moved
// to another team (branch, title or magic words in the description) links
// the moved issue; the store loads it through the identifier alias.
func TestCodeReviewLinksIssueByPreviousIdentifier(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	instance := &server{store: repository, authDisabled: true, uploadPath: t.TempDir()}
	handler := newHandler(instance)
	target := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Review Target", "key": "RVW"}, http.StatusCreated)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Linked by an old identifier"}, http.StatusCreated)
	oldIdentifier := issue.Identifier
	moved := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"teamId": target.ID}, http.StatusOK)

	for index, item := range []struct{ title, body, branch string }{
		{title: "Fix the thing", branch: "feature/" + oldIdentifier + "-fix"},
		{title: "Fix the thing", body: "Fixes " + oldIdentifier},
		{title: "[" + oldIdentifier + "] Fix the thing"},
	} {
		var event externalCodeReviewEvent
		event.Action, event.Number = "opened", 40+index
		event.Repository.FullName = "acme/app"
		event.PullRequest.ID, event.PullRequest.Number, event.PullRequest.Title, event.PullRequest.Body = int64(9000+index), 40+index, item.title, item.body
		event.PullRequest.HTMLURL = "https://github.com/acme/app/pull/" + string(rune('0'+index))
		event.PullRequest.Head.Ref, event.PullRequest.Base.Ref = item.branch, "main"
		if item.branch == "" {
			event.PullRequest.Head.Ref = "topic"
		}
		review, err := instance.applyCodeReviewEvent(t.Context(), "test-workspace", "github", "delivery-alias-"+string(rune('a'+index)), event)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(review.IssueIDs, []string{issue.ID}) {
			t.Fatalf("review %d (%+v) linked %v, want the moved issue %s (%s, was %s)", index, item, review.IssueIDs, issue.ID, moved.Identifier, oldIdentifier)
		}
	}
}

func TestIssueIDsNamedIn(t *testing.T) {
	issues := []domain.Issue{
		{ID: "issue_moved", Identifier: "NEW-7", PreviousIdentifiers: []string{"OLD-1", "TAKEN-3"}},
		{ID: "issue_current", Identifier: "TAKEN-3"},
		{ID: "issue_twelve", Identifier: "OLD-12"},
	}
	for text, want := range map[string][]string{
		"fixes old-1":            {"issue_moved"},
		"OLD-12 only":            {"issue_twelve"},
		"branch eng/old-1-retry": {"issue_moved"},
		"closes NEW-7":           {"issue_moved"},
		"refs TAKEN-3":           {"issue_current"},
		"nothing here OLD-123":   {},
	} {
		if got := issueIDsNamedIn(issues, text); !slices.Equal(got, want) {
			t.Fatalf("issueIDsNamedIn(%q) = %v, want %v", text, got, want)
		}
	}
}

// Release CI references found in commit messages resolve previous
// identifiers too; an identifier another issue has now names that issue.
func TestReleaseCIIssuesResolvePreviousIdentifiers(t *testing.T) {
	data := &domain.Bootstrap{Issues: []domain.Issue{
		{ID: "issue_moved", Identifier: "NEW-7", PreviousIdentifiers: []string{"OLD-1", "TAKEN-3"}},
		{ID: "issue_current", Identifier: "TAKEN-3"},
	}}
	release := &domain.Release{ID: "release_1"}
	_, unknown := applyReleaseCIIssues(data, release, releaseCIInput{IssueReferences: []releaseCIIssueReference{{Identifier: "old-1"}, {Identifier: "TAKEN-3"}, {Identifier: "GONE-9"}}})
	if !slices.Equal(release.IssueIDs, []string{"issue_moved", "issue_current"}) || !slices.Equal(unknown, []string{"GONE-9"}) {
		t.Fatalf("release issues %v, unknown %v", release.IssueIDs, unknown)
	}
	applyReleaseCIIssues(data, release, releaseCIInput{RevertedIssueReferences: []releaseCIIssueReference{{Identifier: "OLD-1"}}})
	if !slices.Equal(release.IssueIDs, []string{"issue_current"}) {
		t.Fatalf("after reverting OLD-1: %v", release.IssueIDs)
	}
}
