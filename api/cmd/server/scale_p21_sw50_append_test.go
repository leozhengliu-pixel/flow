package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scaleP21SW50AppendOperation struct {
	name, kind, aggregate string
	body                  any
	run                   func(*server, http.ResponseWriter, *http.Request)
}

func scaleP21SW50AppendOperations() []scaleP21SW50AppendOperation {
	session := "unregistered-session-" + strings.Repeat("long", 60)
	return []scaleP21SW50AppendOperation{
		{
			name: "conversation", kind: "aiConversations", aggregate: "ai",
			body: map[string]any{"id": "client-id", "userId": "client-owner", "title": "New", "status": "client-state", "context": map[string]any{"count": 17, "reference": "private-reference"}},
			run:  (*server).createAIConversation,
		},
		{
			name: "activity", kind: "agentActivities", aggregate: session,
			body: map[string]any{"id": "client-id", "sessionId": session, "type": "tool", "status": "client-state", "body": "Created", "actorId": "client-actor", "metadata": map[string]any{"count": 23, "reference": "private-reference"}},
			run:  (*server).createAgentActivity,
		},
	}
}

func scaleP21SW50ResetAppend(t *testing.T, f scaleP21M2Fixture, op scaleP21SW50AppendOperation, collision bool) []string {
	t.Helper()
	repo, workspace := f.repository, f.data.Workspace.URLKey
	repo.SetWebhookSink(nil)
	repo.SetRealtimeSink(nil)
	f.probe.SetWebhookSink(nil)
	f.probe.SetRealtimeSink(nil)
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	baseline := make([]string, 61)
	if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "aiConversations", "agentActivities", "documents"), workspace, "agent.activity_updated", "", nil, func(data *domain.Bootstrap) error {
		data.AIConversations = []domain.AIConversation{}
		data.AgentActivities = []domain.AgentActivity{}
		for i := range baseline {
			id := fmt.Sprintf("sw50-existing-%03d", i)
			baseline[i] = id
			data.AIConversations = append(data.AIConversations, domain.AIConversation{
				ID: id, UserID: f.data.Viewer.ID, Title: "Warm\x00conversation", Status: "active",
				Context: map[string]any{"integer": int64(9007199254740993)}, CreatedAt: stamp, UpdatedAt: stamp,
			})
			data.AgentActivities = append(data.AgentActivities, domain.AgentActivity{
				ID: id, SessionID: "retained", Type: "thought", Status: "completed", Body: "Warm\x00activity", CreatedAt: stamp, UpdatedAt: stamp,
			})
		}
		aggregate := "unmatched-aggregate"
		if collision {
			aggregate = op.aggregate
		}
		data.Documents = []domain.Document{{
			ID: "sw50-collision-document", Title: "Earlier aggregate", Content: "Complete document",
			ContentData: map[string]any{"id": aggregate, "source": "earlier document", "body": strings.Repeat("large aggregate ", 22000), "actorId": f.data.Viewer.ID, "count": 79},
			Creator:     f.data.Viewer, TeamIDs: []string{f.public}, CreatedAt: stamp, UpdatedAt: stamp,
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return baseline
}

func scaleP21SW50MeasureAppend(t *testing.T, bounded bool) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		for _, op := range scaleP21SW50AppendOperations() {
			scaleP21SW50ResetAppend(t, f, op, true)
			r := f.request(f.data.Viewer, nil, op.body)
			sql := 0
			groups := map[string]int{}
			r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(query string) {
				sql++
				group := "other"
				for _, table := range []string{"workspace_states", "workspace_metadata_records", "workspace_memberships", "team_memberships", "auth_users", "domain_events", "record_versions", "record_journal", "metadata_catalog", "cache_revisions"} {
					if strings.Contains(query, table) {
						group = table
						break
					}
				}
				groups[group]++
			}))
			service := &server{store: repo}
			if bounded {
				service.store = f.probe
			}
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			w := httptest.NewRecorder()
			op.run(service, w, r)
			runtime.ReadMemStats(&after)
			if w.Code != http.StatusCreated {
				t.Fatalf("bounded=%t %s: %d %s", bounded, op.name, w.Code, w.Body.String())
			}
			t.Logf("bounded=%t operation=%s primarySQL=%d allocatedBytes=%d allocations=%d groups=%v", bounded, op.name, sql, after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs, groups)
		}
	}
}

func TestScaleP21SW50AppendBaseline(t *testing.T) {
	scaleP21SW50MeasureAppend(t, false)
}

func TestScaleP21SW50AppendCandidateMeasurement(t *testing.T) {
	scaleP21SW50MeasureAppend(t, true)
}

func scaleP21SW50CheckWarmAppendSibling(t *testing.T, source *store.SQLiteStore, workspace, kind string) {
	t.Helper()
	check := func(data domain.Bootstrap) {
		t.Helper()
		if kind == "aiConversations" {
			if len(data.AIConversations) == 0 || data.AIConversations[0].Title != "Warm\x00conversation" ||
				data.AIConversations[0].Context["integer"] != int64(9007199254740993) {
				t.Fatal("append changed untouched native conversation values")
			}
		} else if len(data.AgentActivities) == 0 || data.AgentActivities[0].Body != "Warm\x00activity" {
			t.Fatal("append changed untouched warm activity values")
		}
	}
	if !source.BoundedMetadataEnabled() {
		data, ok := source.WorkspaceMetadata(workspace)
		if !ok {
			t.Fatal("literal installed workspace disappeared")
		}
		check(data)
		return
	}
	read, found, err := source.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal("open owned installed sibling read", found, err)
	}
	defer read.Close()
	selected, err := read.Select(t.Context(), store.MetadataRecordScope{Read: []store.RecordKey{{Kind: kind, ID: "sw50-existing-000"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer selected.Close()
	check(selected.Data)
}

func TestScaleP21SW50AppendDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, op := range scaleP21SW50AppendOperations() {
				for _, collision := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/collision=%t", op.name, collision), func(t *testing.T) {
						var outcomes []any
						for _, source := range []*store.SQLiteStore{repo, f.probe} {
							baseline := scaleP21SW50ResetAppend(t, f, op, collision)
							_, hash := scaleP21SW6CreationState(t, repo, workspace, op.kind, baseline)
							unrelated := scaleP21SW6RecordHash(t, repo, workspace, "documents", "sw50-collision-document")
							var events []domain.DomainEvent
							var realtime []domain.RealtimeEvent
							source.SetWebhookSink(func(_ string, event domain.DomainEvent) { events = append(events, event) })
							source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = append(realtime, event) })
							generated := map[string]string{}
							var responses []any
							var created []store.RecordKey
							for index := range 2 {
								// Refresh once before the pair, then preserve the second
								// request's actual first-append installed generation.
								r := f.literalRequest(user, key, op.body)
								if index == 0 {
									if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
										t.Fatal(err)
									}
								}
								r = r.WithContext(store.ContextWithRealtimeClient(r.Context(), "sw50-client"))
								w := httptest.NewRecorder()
								op.run(&server{store: source}, w, r)
								if w.Code != http.StatusCreated {
									t.Fatalf("bounded=%t status=%d body=%s", source.BoundedMetadataEnabled(), w.Code, w.Body.String())
								}
								var response map[string]any
								if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
									t.Fatal(err)
								}
								id, _ := response["id"].(string)
								if id == "" || id == "client-id" {
									t.Fatal("creation did not replace the client ID")
								}
								generated[id] = fmt.Sprintf("<created-%d>", index)
								created = append(created, store.RecordKey{Kind: op.kind, ID: id})
								if op.name == "conversation" && (response["userId"] != user.ID || response["status"] != "active") {
									t.Fatal("conversation owner or status changed", response)
								}
								if op.name == "activity" && (response["sessionId"] != op.aggregate || response["status"] != "running" || response["actorId"] != "client-actor") {
									t.Fatal("arbitrary session, status or supplied actor semantics changed", response)
								}
								responses = append(responses, response)
							}
							if len(events) != 2 || len(realtime) != 2 {
								t.Fatalf("publication count changed: %d/%d", len(events), len(realtime))
							}
							order, after := scaleP21SW6CreationState(t, repo, workspace, op.kind, baseline)
							if hash != after || len(order) != len(baseline)+2 || !slices.Equal(order[:len(baseline)], baseline) {
								t.Fatal("append rewrote or reordered the existing collection")
							}
							if unrelated != scaleP21SW6RecordHash(t, repo, workspace, "documents", "sw50-collision-document") {
								t.Fatal("aggregate lookup changed its unrelated source document")
							}
							scaleP21SW50CheckWarmAppendSibling(t, source, workspace, op.kind)
							effects := make([]any, 0, 2)
							for index := range events {
								event, frame := events[index], realtime[index]
								if event.AggregateID != op.aggregate || frame.ClientID != "sw50-client" {
									t.Fatal("aggregate or client attribution changed")
								}
								if collision && (!strings.Contains(string(event.Snapshot), "earlier document") || len(event.Snapshot) <= store.MetadataRecordPageMaxBytes) {
									t.Fatal("legal earlier large aggregate was omitted", len(event.Snapshot))
								}
								effects = append(effects, []any{event.Type, event.AggregateID, event.Payload, event.PreviousValues, event.Snapshot, frame.Payload})
							}
							rows := scaleP21M2Canonical(t, repo, workspace, created...)
							outcomes = append(outcomes, scaleP21SW6GeneratedJSON(t, scaleP21M2JSON(t, []any{responses, rows, effects, order}, false), generated))
							source.SetWebhookSink(nil)
							source.SetRealtimeSink(nil)
						}
						if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
							t.Fatal(scaleP21M2Diff(outcomes[0], outcomes[1]))
						}
					})
				}
			}
		})
		for _, op := range scaleP21SW50AppendOperations() {
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				baseline := scaleP21SW50ResetAppend(t, f, op, true)
				r := f.request(f.data.Viewer, nil, op.body)
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				count := 0
				source.SetWebhookSink(func(string, domain.DomainEvent) { count++ })
				source.SetRealtimeSink(func(string, domain.RealtimeEvent) { count++ })
				w := httptest.NewRecorder()
				op.run(&server{store: source}, w, r.WithContext(ctx))
				if w.Code == http.StatusCreated || count != 0 {
					t.Fatal("canceled creation published", w.Code, count)
				}
				order, _ := scaleP21SW6CreationState(t, repo, workspace, op.kind, baseline)
				if !slices.Equal(order, baseline) {
					t.Fatal("canceled creation changed canonical membership")
				}
				source.SetWebhookSink(nil)
				source.SetRealtimeSink(nil)
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("append handlers decoded full metadata")
		}
	}
}
