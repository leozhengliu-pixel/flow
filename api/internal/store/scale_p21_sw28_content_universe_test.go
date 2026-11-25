package store

import (
	"encoding/json"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW28AggregateHydratedIssueUniverse(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, id := f.data.Workspace.URLKey, idgen.New("issue_sw28_universe_")
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21_sw28.seed_issue", "", nil, func(data *domain.Bootstrap) error {
		data.Issues = append(data.Issues, domain.Issue{
			ID: id, Identifier: "SW28-1", Number: 1, Title: "Actual hydrated issue",
			Team: data.Teams[0], State: data.States[0], Creator: data.Viewer,
		})
		data.Documents = []domain.Document{{ID: "document_sw28_issue_collision", ContentData: map[string]any{"id": id, "marker": "wrong metadata-only fallback"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var results []domain.RealtimeEvent
	for _, bounded := range []bool{false, true} {
		f.SetWebhookSink(nil)
		f.SetRealtimeSink(nil)
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
			data.Settings["sw28-universe"] = "before"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		repo := f.SQLiteStore
		if bounded {
			var err error
			repo, err = f.BoundedMetadataViewForTesting(t.Context())
			if err != nil {
				t.Fatal(err)
			}
		}
		var got domain.RealtimeEvent
		repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { got = event })
		if err := repo.MutateWorkspaceScoped(t.Context(), workspace, "scale_p21_sw28.full_universe", id, nil,
			MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "sw28-universe"}}},
			func(data *domain.Bootstrap) error {
				data.Settings["sw28-universe"] = "after"
				return nil
			}); err != nil {
			t.Fatal(err)
		}
		results = append(results, got)
	}
	var oldEnvelope struct {
		Entity map[string]any `json:"entity"`
	}
	if err := json.Unmarshal(results[0].Payload, &oldEnvelope); err != nil ||
		oldEnvelope.Entity["title"] != "Actual hydrated issue" || oldEnvelope.Entity["marker"] != nil {
		t.Fatalf("old full-hydration oracle not exercised: payload=%s err=%v", results[0].Payload, err)
	}
	if !jsonEqual(results[0].Payload, results[1].Payload) {
		t.Fatalf("full-hydrated aggregate universe changed: old=%s bounded=%s", results[0].Payload, results[1].Payload)
	}
}
