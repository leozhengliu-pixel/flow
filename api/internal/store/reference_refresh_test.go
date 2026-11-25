package store

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW28AggregateUnselectedReferenceRefresh(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, fieldScoped := range []bool{false, true} {
		name := "full-metadata-only"
		if fieldScoped {
			name = "legacy-field-scoped"
		}
		t.Run(name, func(t *testing.T) {
			f := scaleP09Database(t)
			workspace := f.data.Workspace.URLKey
			id, personID := idgen.New("project_sw28_refresh_"), idgen.New("usr_sw28_refresh_")
			fresh := domain.User{ID: personID, Name: "Fresh directory name", DisplayName: "Fresh directory display", Active: true}
			stale := fresh
			stale.Name, stale.DisplayName = "Old embedded name", "Old embedded display"
			type outcome struct {
				event    domain.DomainEvent
				realtime domain.RealtimeEvent
				followup domain.RealtimeEvent
				setting  string
				project  string
			}
			reset := func() {
				t.Helper()
				f.SetWebhookSink(nil)
				f.SetRealtimeSink(nil)
				err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects", "users", "settings", "webhooks"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
					data.Users = append(slices.DeleteFunc(data.Users, func(user domain.User) bool { return user.ID == personID }), fresh)
					data.Projects = []domain.Project{{ID: id, Name: "Unselected aggregate", Lead: &stale, Creator: &stale}}
					data.Settings["sw28-refresh"] = "before"
					data.Webhooks = []domain.Webhook{{ID: "hook_sw28_refresh", URL: "https://example.test/sw28-refresh", Enabled: true}}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			row := func(field, key string) string {
				t.Helper()
				var value []byte
				if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, field, key).Scan(&value); err != nil {
					t.Fatal(err)
				}
				return string(value)
			}
			var results []outcome
			for _, bounded := range []bool{false, true} {
				reset()
				repo := f.SQLiteStore
				if bounded {
					var err error
					repo, err = f.BoundedMetadataViewForTesting(t.Context())
					if err != nil {
						t.Fatal(err)
					}
				}
				got := outcome{}
				repo.SetWebhookSink(func(_ string, event domain.DomainEvent) { got.event = event })
				repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { got.realtime = event })
				ctx := context.Background()
				if fieldScoped {
					ctx = WithMetadataFields(ctx, "settings")
				}
				err := repo.MutateWorkspaceScoped(ctx, workspace, "application_policy.updated", id, nil,
					MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "sw28-refresh"}}},
					func(data *domain.Bootstrap) error {
						data.Settings["sw28-refresh"] = "after"
						return nil
					})
				if err != nil {
					t.Fatal(err)
				}
				got.setting, got.project = row("settings", "sw28-refresh"), row("projects", id)
				if !fieldScoped {
					repo.SetWebhookSink(nil)
					repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { got.followup = event })
					if err := repo.MutateWorkspaceScoped(WithMetadataFields(t.Context(), "settings"), workspace, "application_policy.updated", id, nil,
						MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "sw28-refresh"}}},
						func(data *domain.Bootstrap) error {
							data.Settings["sw28-refresh"] = "followup"
							return nil
						}); err != nil {
						t.Fatal(err)
					}
				}
				if bounded && (repo.WorkspaceMetadataDecodes() != 0 || repo.metadataResidency.work.used != 0) {
					t.Fatal("reference closure decoded full workspace or leaked work")
				}
				results = append(results, got)
			}
			var oldEnvelope struct {
				Entity domain.Project `json:"entity"`
			}
			if err := json.Unmarshal(results[0].realtime.Payload, &oldEnvelope); err != nil || oldEnvelope.Entity.Lead == nil {
				t.Fatalf("old path did not emit project: %v", err)
			}
			want := fresh.Name
			if fieldScoped {
				want = stale.Name
			}
			if oldEnvelope.Entity.Lead.Name != want {
				t.Fatalf("old oracle setup lost refresh boundary: got=%q want=%q", oldEnvelope.Entity.Lead.Name, want)
			}
			if results[0].setting != results[1].setting || results[0].project != results[1].project {
				t.Fatal("bounded mutation changed canonical target/reference records")
			}
			if !jsonEqual(results[0].event.PreviousValues, results[1].event.PreviousValues) ||
				!jsonEqual(results[0].event.Snapshot, results[1].event.Snapshot) ||
				!jsonEqual(results[0].realtime.Payload, results[1].realtime.Payload) ||
				!jsonEqual(results[0].followup.Payload, results[1].followup.Payload) ||
				!reflect.DeepEqual(results[0].event.Payload, results[1].event.Payload) {
				t.Fatalf("unselected reference refresh differs: previous=%s/%s realtime=%s/%s",
					results[0].event.PreviousValues, results[1].event.PreviousValues,
					results[0].realtime.Payload, results[1].realtime.Payload)
			}
		})
	}
}
