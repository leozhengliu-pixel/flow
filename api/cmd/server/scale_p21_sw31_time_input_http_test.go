package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW31TimeInputTeamLabelHTTP(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, id := f.data.Workspace.URLKey, idgen.New("label_time_input_")
		stamp := time.Date(2026, 11, 13, 1, 2, 3, 4, time.UTC)
		seed := func() {
			t.Helper()
			if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "labels"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
				data.Labels = []domain.IssueLabel{{ID: id, Name: "Time input", Scope: f.public, ResourceType: "issue", CreatedAt: stamp}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		codes := []int{}
		bodies := []string{}
		for _, bounded := range []bool{false, true} {
			seed()
			r := f.request(f.data.Viewer, nil, map[string]string{"archivedAt": "2026-11-13T01:02:03.000000004+05:45"})
			r.Method = http.MethodPatch
			r.SetPathValue("id", f.public)
			r.SetPathValue("labelId", id)
			source := repo
			if bounded {
				source = f.probe
			}
			w := httptest.NewRecorder()
			(&server{store: source}).updateTeamLabel(w, r)
			codes = append(codes, w.Code)
			bodies = append(bodies, w.Body.String())
			t.Logf("actual updateTeamLabel bounded=%t HTTP=%d body=%s", bounded, w.Code, w.Body.String())
			if bounded && w.Code == http.StatusOK {
				scope := store.MetadataRecordScope{Read: []store.RecordKey{{Kind: "labels", ID: id}}}
				data, _, err := source.WorkspaceMetadataScoped(t.Context(), workspace, scope)
				if err != nil || len(data.Labels) != 1 || data.Labels[0].ArchivedAt == nil {
					t.Errorf("successful HTTP offset archive poisoned subsequent bounded read: %v", err)
					continue
				}
				native := *data.Labels[0].ArchivedAt
				if native.Format(time.RFC3339Nano) != "2026-11-13T01:02:03.000000004+05:45" {
					t.Fatal("bounded timestamp input changed its offset or precision")
				}
				next := f.literalRequest(f.data.Viewer, nil, map[string]string{"name": "After offset"})
				next.Method = http.MethodPatch
				next.SetPathValue("id", f.public)
				next.SetPathValue("labelId", id)
				response := httptest.NewRecorder()
				(&server{store: source}).updateTeamLabel(response, next)
				if response.Code != http.StatusOK {
					t.Fatalf("subsequent bounded update failed: %d %s", response.Code, response.Body.String())
				}
				again, _, err := source.WorkspaceMetadataScoped(t.Context(), workspace, scope)
				if err != nil || len(again.Labels) != 1 || again.Labels[0].ArchivedAt == nil || *again.Labels[0].ArchivedAt != native {
					t.Fatalf("subsequent update changed native time identity: %v", err)
				}
			}
		}
		if codes[0] != http.StatusOK || codes[1] != codes[0] || bodies[0] != bodies[1] {
			t.Fatalf("legal RFC3339 offset archivedAt changed public producer result: literal=%d bounded=%d", codes[0], codes[1])
		}
		for _, spec := range []struct {
			id   string
			code int
		}{{id, http.StatusBadRequest}, {id + "-missing", http.StatusNotFound}} {
			var responses []string
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				r := f.request(f.data.Viewer, nil, map[string]string{"archivedAt": "invalid timestamp"})
				r.Method = http.MethodPatch
				r.SetPathValue("id", f.public)
				r.SetPathValue("labelId", spec.id)
				w := httptest.NewRecorder()
				(&server{store: source}).updateTeamLabel(w, r)
				if w.Code != spec.code {
					t.Fatalf("timestamp validation changed target-before-input ordering: %d %s", w.Code, w.Body.String())
				}
				responses = append(responses, w.Body.String())
			}
			if responses[0] != responses[1] {
				t.Fatal("timestamp validation response differs")
			}
		}
	}
}
