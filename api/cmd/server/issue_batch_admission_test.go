package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/store"
)

func TestThousandIssueBatchDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleIssueUpdateSetup(t)
	ids := scaleIssueBatchBulk(t, f, 1000)
	f.runBatchCases(t, []scaleIssueBatchCase{
		{name: "1000-description-growth", user: f.data.Viewer, ids: ids, update: map[string]any{"description": strings.Repeat("description <>& ", 128)}, code: http.StatusOK},
		{name: "1000-one-archived-rolls-back", setup: scaleIssueBatchArchive(f, ids[len(ids)-1]), user: f.data.Viewer, ids: ids, update: map[string]any{"priority": 2}, code: http.StatusBadRequest},
		{name: "1000-cancelled", user: f.data.Viewer, ids: ids, update: map[string]any{"priority": 2}, stop: true, code: http.StatusRequestTimeout},
	})
}

func TestIssueBatchCancelledSQL(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleIssueUpdateSetup(t)
	ids := []string{f.triage.ID, f.visible.ID}
	f.editBaseline = pinIssueFixtureAges(false, ids...)
	var before scaleIssueUpdateState
	outcomes := f.differential(t, "cancelled SQL argument batch", func(service *server) *httptest.ResponseRecorder {
		if !service.store.BoundedMetadataEnabled() {
			before = f.state(t)
		}
		body, err := json.Marshal(map[string]any{"issueIds": ids, "update": map[string]any{"priority": 2}})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/api/issue-records/batch", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
		r.SetPathValue("workspaceKey", f.data.Workspace.URLKey)
		r.SetPathValue("workspace", f.data.Workspace.URLKey)
		ctx := context.WithValue(r.Context(), authUserContextKey{}, f.data.Viewer)
		ctx = store.WithIssueRecordMutations(store.ContextWithActor(ctx, f.data.Viewer), ids...)
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		cancelled := false
		ctx = store.WithSQLStatementObserver(ctx, func(query string) {
			if strings.HasPrefix(query, "INSERT INTO issue_records(") {
				cancelled = true
				cancel()
			}
		})
		r, closeOutput := beginRequestResourceReads(r.WithContext(ctx))
		defer closeOutput()
		w := httptest.NewRecorder()
		service.batchUpdate(w, r)
		if !cancelled {
			t.Fatal("did not reach the owned SQL argument batch")
		}
		return w
	})
	for _, got := range outcomes {
		if got.Code != http.StatusInternalServerError || len(got.State.Events) != 0 ||
			!reflect.DeepEqual(before.Records, got.State.Records) || !reflect.DeepEqual(before.Content, got.State.Content) {
			t.Fatalf("cancelled SQL batch changed state: status=%d events=%d", got.Code, len(got.State.Events))
		}
	}
}
