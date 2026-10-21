package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The paged issue list renders the blocked / blocking chips from the records it
// receives, so a page of records must carry both sides of every relation (and
// the hover card's issue ids) without any per-row lookup.
func TestIssueRecordListCarriesRelationsOnBothSides(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	teamID := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Teams[0].ID
	newIssue := func(title string) domain.Issue {
		return requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": title, "teamId": teamID}, http.StatusCreated)
	}
	blocker, blocked, blockedToo := newIssue("Blocker"), newIssue("Blocked"), newIssue("Blocked too")
	for _, target := range []domain.Issue{blocked, blockedToo} {
		requestJSON[domain.IssueRelation](t, handler, http.MethodPost, "/api/issue-records/"+blocker.ID+"/relations", map[string]any{"type": "blocks", "relatedIssueId": target.ID}, http.StatusCreated)
	}

	page := requestJSON[store.IssueRecordPage](t, handler, http.MethodGet, "/api/issue-records?teamId="+teamID+"&limit=50", nil, http.StatusOK)
	relationsOf := func(id string) []domain.IssueRelation {
		t.Helper()
		for _, item := range page.Items {
			if item.ID == id {
				return item.Relations
			}
		}
		t.Fatalf("issue %s is not on the page", id)
		return nil
	}
	blocking := relationsOf(blocker.ID)
	if len(blocking) != 2 {
		t.Fatalf("blocker relations = %#v", blocking)
	}
	for _, relation := range blocking {
		if relation.Type != "blocks" || !slices.Contains([]string{blocked.ID, blockedToo.ID}, relation.RelatedIssueID) {
			t.Fatalf("blocker relation = %#v", relation)
		}
	}
	for _, id := range []string{blocked.ID, blockedToo.ID} {
		inverse := relationsOf(id)
		if len(inverse) != 1 || inverse[0].Type != "blocked_by" || inverse[0].RelatedIssueID != blocker.ID {
			t.Fatalf("blocked issue %s relations = %#v", id, inverse)
		}
	}
}
