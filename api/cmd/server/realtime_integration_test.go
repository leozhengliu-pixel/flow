package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP09RealtimeUsesJournalCursor(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_SYNC_V2", "on")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
			issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]string{"title": "Before", "teamId": f.data.Teams[0].ID}, workspace, http.StatusCreated)
			before, err := repo.RecordJournalHead(t.Context(), workspaceID)
			if err != nil {
				t.Fatal(err)
			}
			legacy, err := repo.WorkspaceChangeHead(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			// Memberships advance the journal without consuming legacy event
			// cursors. This makes comparing the two streams observably wrong.
			for range 3 {
				if err := repo.DeleteTeamMemberships(t.Context(), workspaceID, f.data.Teams[0].ID); err != nil {
					t.Fatal(err)
				}
				if err := repo.SetTeamMembership(t.Context(), workspaceID, f.data.Teams[0].ID, f.data.Viewer.ID, "member", true); err != nil {
					t.Fatal(err)
				}
			}
			if head, err := repo.WorkspaceChangeHead(t.Context(), workspace); err != nil || head != legacy {
				t.Fatalf("eventless memberships changed legacy cursor: %d -> %d %v", legacy, head, err)
			}
			before, err = repo.RecordJournalHead(t.Context(), workspaceID)
			if err != nil {
				t.Fatal(err)
			}
			authRequest[domain.Issue](t, f.admin, http.MethodPatch, f.host.URL+"/api/issues/"+issue.ID, map[string]string{"title": "Latest journal title"}, workspace, http.StatusOK)
			legacy, err = repo.WorkspaceChangeHead(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			cursor, _ := strconv.ParseInt(before, 10, 64)
			if cursor <= legacy {
				t.Fatal("test did not separate the journal and legacy cursors")
			}
			state := &syncV2Stream{workspaceID: workspaceID, cursor: cursor}
			w := httptest.NewRecorder()
			r := scaleP12Request(workspace, f.data.Viewer)
			if !f.service.writeSyncV2Batch(w, r, state, []domain.RealtimeEvent{{ID: "wake", Seq: legacy, Type: "issue.updated", AggregateID: issue.ID}}) {
				t.Fatalf("journal wake was skipped: %s", w.Body)
			}
			// History/activity records can require a complete invalidation
			// while the ordinary issue summary can be applied directly.
			if !strings.Contains(w.Body.String(), "Latest journal title") && !(strings.Contains(w.Body.String(), `"invalidations":`) && strings.Contains(w.Body.String(), `"issues"`)) {
				t.Fatalf("journal wake lost both its upsert and invalidation: %s", w.Body)
			}
			head, err := repo.RecordJournalHead(t.Context(), workspaceID)
			if err != nil || strconv.FormatInt(state.cursor, 10) != head {
				t.Fatalf("stream did not reach journal head: %d %s %v", state.cursor, head, err)
			}
			authRequest[any](t, f.admin, http.MethodDelete, f.host.URL+"/api/issues/"+issue.ID, nil, workspace, http.StatusNoContent)
			page, err := repo.ReadCommittedChanges(t.Context(), workspaceID, head, 100, 256<<10)
			if err != nil || len(page.Transactions) == 0 {
				t.Fatalf("deletion journal: %+v %v", page, err)
			}
			found := false
			for _, change := range page.Transactions {
				seq, journaled, err := repo.RecordJournalEventSeqForType(t.Context(), workspaceID, change.TxID, "issue.deleted")
				if err != nil {
					t.Fatal(err)
				}
				if journaled && seq > 0 {
					found = true
				}
			}
			if !found {
				t.Fatal("journal deletion lost its original grant evidence")
			}
		})
	}
}
