package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// This certifies only MutateWorkspace's fixed-aggregate callback adapter.
// Its scoped project callback uses the already-proven point writer contract.
func TestScaleP21SW103MutateWorkspaceForwarding(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	callbackError := errors.New("sw103 callback error")
	for _, item := range []struct {
		name             string
		defaultWorkspace bool
		emptyAggregate   bool
		callbackError    error
	}{
		{name: "success"},
		{name: "default-workspace", defaultWorkspace: true},
		{name: "empty-aggregate", emptyAggregate: true},
		{name: "callback-error", callbackError: callbackError},
		{name: "no-mutation", callbackError: ErrNoMutation},
	} {
		t.Run(item.name, func(t *testing.T) {
			f := scaleP09Database(t)
			workspace := f.data.Workspace.URLKey
			target, unrelated := idgen.New("sw103-project-"), idgen.New("sw103-unrelated-")
			if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw103_seed", "", nil, func(data *domain.Bootstrap) error {
				data.Projects = append(data.Projects,
					domain.Project{ID: target, Name: "Before", TeamIDs: []string{data.Teams[0].ID}},
					domain.Project{ID: unrelated, Name: "Untouched"})
				data.Webhooks = append(data.Webhooks, domain.Webhook{ID: idgen.New("sw103-hook-"), Enabled: true, URL: "https://example.test/sw103"})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			row := func(id string) []byte {
				t.Helper()
				var raw []byte
				if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, id).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				return raw
			}
			original, untouched := row(target), row(unrelated)
			preimage, found := f.storedWorkspace(workspace)
			if !found {
				t.Fatal("owned fixture workspace missing")
			}
			preimage = cloneBootstrap(preimage)
			type observation struct {
				calls, hooks, realtime int
				result, hookWorkspace  string
				event                  domain.DomainEvent
				realtimeEvent          domain.RealtimeEvent
				row                    []byte
				err                    error
			}
			var want observation
			for _, bounded := range []bool{false, true} {
				repo := f.SQLiteStore
				if bounded {
					if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='projects' AND record_key=?`, original, workspace, target); err != nil {
						t.Fatal(err)
					}
					f.mu.Lock()
					f.setWorkspaceLocked(workspace, preimage)
					f.mu.Unlock()
					var err error
					repo, err = f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(repo.releaseMetadataLogicalSources)
				}
				var got observation
				repo.SetWebhookSink(func(key string, event domain.DomainEvent) {
					got.hooks++
					got.hookWorkspace, got.event = key, event
				})
				repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
					got.realtime++
					got.realtimeEvent = event
				})
				key, aggregate := workspace, target
				if item.defaultWorkspace {
					key = ""
				}
				if item.emptyAggregate {
					aggregate = ""
				}
				ctx := WithMetadataRecords(t.Context(), MetadataRecordScope{Write: []RecordKey{{Kind: "projects", ID: target}}})
				payload := map[string]string{"name": "After"}
				got.err = repo.MutateWorkspace(ctx, key, "project.updated", aggregate, payload, func(data *domain.Bootstrap) error {
					got.calls++
					if len(data.Projects) != 1 || data.Projects[0].ID != target {
						t.Fatal("adapter lost the declared callback scope")
					}
					data.Projects[0].Name = "After"
					got.result = data.Projects[0].ID + ":" + data.Projects[0].Name
					return item.callbackError
				})
				got.row = row(target)
				expectedError := item.callbackError
				if errors.Is(expectedError, ErrNoMutation) {
					expectedError = nil
				}
				if got.err != expectedError || got.calls != 1 || got.result != target+":After" {
					t.Fatalf("adapter changed callback result/error: result=%q calls=%d err=%v expected=%v", got.result, got.calls, got.err, expectedError)
				}
				if item.callbackError != nil {
					if !bytes.Equal(got.row, original) || got.hooks != 0 || got.realtime != 0 {
						t.Fatal("callback refusal/no-op wrote or published")
					}
				} else {
					encoded, _ := json.Marshal(payload)
					if got.hooks != 1 || got.realtime != 1 || got.hookWorkspace != workspace ||
						got.event.Type != "project.updated" || got.event.AggregateID != aggregate || !bytes.Equal(got.event.Payload, encoded) ||
						got.realtimeEvent.Type != "project.updated" || got.realtimeEvent.AggregateID != aggregate {
						t.Fatal("adapter changed workspace/event/aggregate/payload forwarding")
					}
				}
				if !bytes.Equal(row(unrelated), untouched) {
					t.Fatal("adapter changed the unselected control row")
				}
				if bounded {
					if got.calls != want.calls || got.result != want.result || got.err != want.err ||
						!bytes.Equal(got.row, want.row) || got.hooks != want.hooks || got.realtime != want.realtime ||
						!reflect.DeepEqual(got.event.Payload, want.event.Payload) || !reflect.DeepEqual(got.event.PreviousValues, want.event.PreviousValues) ||
						!reflect.DeepEqual(got.realtimeEvent.Payload, want.realtimeEvent.Payload) {
						t.Fatal("legacy/bounded adapter output differs")
					}
					if repo.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("adapter retained point-writer work")
					}
				} else {
					want = got
				}
			}
		})
	}
}
