package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW20ImportedTeamAggregateCollision(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		targetID := idgen.New("sw20-imported-")
		stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for _, providerJob := range []bool{false, true} {
			var outcomes []any
			for _, current := range []*store.SQLiteStore{repo, f.probe} {
				repo.SetWebhookSink(nil)
				repo.SetRealtimeSink(nil)
				f.probe.SetWebhookSink(nil)
				f.probe.SetRealtimeSink(nil)
				if err := repo.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
					t.Fatal(err)
				}
				if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams", "documents", "users", "providerJobs"), f.data.Workspace.URLKey, "scale_p21_sw20.reset", "", nil, func(data *domain.Bootstrap) error {
					data.Teams = []domain.Team{{ID: targetID, Key: "IMPORTED", Name: "Imported", ArchivedAt: &stamp, UpdatedAt: &stamp}}
					data.Users = append(slices.DeleteFunc(data.Users, func(user domain.User) bool { return user.ID == targetID }), domain.User{
						ID: targetID, Name: "Earlier imported user", DisplayName: "Earlier imported user", Active: true,
						Username: "retained", UserID: "external-user", AppScopes: []string{"read"}, AppTeamIDs: []string{"retained-private-reference"}, OutOfOfficeUntil: &stamp,
					})
					data.ProviderJobs = nil
					if providerJob {
						data.ProviderJobs = []domain.ProviderJob{{ID: targetID, Provider: "collision", Status: "retained", UpdatedAt: stamp}}
					}
					data.Documents = []domain.Document{{ID: idgen.New("doc_sw20_imported_"), Title: "Earlier collision", ContentData: map[string]any{"id": targetID, "retained": "imported team collision"}, Creator: f.data.Viewer}}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				var event domain.DomainEvent
				var realtime domain.RealtimeEvent
				current.SetWebhookSink(func(_ string, value domain.DomainEvent) { event = value })
				current.SetRealtimeSink(func(_ string, value domain.RealtimeEvent) { realtime = value })
				beforeUser := scaleP21SW6RecordHash(t, repo, f.data.Workspace.URLKey, "users", targetID)
				beforeSettings := scaleP21SW20Rows(t, repo, f.data.Workspace.URLKey, store.RecordKey{Kind: "userSettings", ID: targetID})
				r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
				r.SetPathValue("teamId", targetID)
				(&server{store: current}).restoreDeletedTeam(w, r)
				if w.Code != http.StatusOK {
					t.Fatal(w.Code, w.Body.String())
				}
				if scaleP21SW6RecordHash(t, repo, f.data.Workspace.URLKey, "users", targetID) != beforeUser ||
					!reflect.DeepEqual(beforeSettings, scaleP21SW20Rows(t, repo, f.data.Workspace.URLKey, store.RecordKey{Kind: "userSettings", ID: targetID})) {
					t.Fatal("aggregate read changed user or virtual settings defaults")
				}
				var payload, previous any
				if err := json.Unmarshal(realtime.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				if len(event.PreviousValues) > 0 {
					if err := json.Unmarshal(event.PreviousValues, &previous); err != nil {
						t.Fatal(err)
					}
				}
				if object, ok := payload.(map[string]any); ok {
					if entity, ok := object["entity"].(map[string]any); ok {
						delete(entity, "updatedAt")
					}
				}
				outcomes = append(outcomes, []any{payload, previous})
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatalf("imported team aggregate differs providerJob=%t: %s", providerJob, scaleP21M2Diff(outcomes[0], outcomes[1]))
			}
		}
	}
}
