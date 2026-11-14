package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scaleP21SW48ResponseWriter struct {
	*httptest.ResponseRecorder
	beforeWrite func()
}

func (w *scaleP21SW48ResponseWriter) Write(body []byte) (int, error) {
	if w.beforeWrite != nil {
		before := w.beforeWrite
		w.beforeWrite = nil
		before()
	}
	return w.ResponseRecorder.Write(body)
}

func scaleP21SW48Collision(data *domain.Bootstrap, actor domain.User, aggregate string) {
	data.Documents = []domain.Document{{
		ID: "sw48-aggregate-document", Title: "Earlier aggregate", Creator: actor,
		ContentData: map[string]any{
			"id": aggregate, "marker": "warm\x00earlier", "hiddenCount": 37,
			"body": strings.Repeat("complete closure ", 20000),
		},
	}}
}

func TestScaleP21SW48PolicyAndAuditPermissionDifferential(t *testing.T) {
	const policyID = "sw48-application-policy"
	policySeed := func(data *domain.Bootstrap, actor domain.User) {
		setApplicationPolicies(data, []applicationPolicy{{
			ID: policyID, Name: "Before policy", Kind: "oauth", Shared: true,
			Status: "approved", OwnerID: actor.ID,
		}})
		scaleP21SW48Collision(data, actor, policyID)
	}
	scaleP21M3PointDifferential(t, []scaleP21M3PointOperation{
		{
			name: "application-policy-save", kind: "settings", id: "applicationPolicies",
			body: `{"id":"sw48-application-policy","name":"After","kind":"oauth","shared":true,"status":"approved"}`,
			seed: policySeed, handler: (*server).saveApplicationPolicy,
		},
		{
			name: "application-policy-delete", kind: "settings", id: "applicationPolicies",
			seed: policySeed,
			handler: func(s *server, w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.PathValue("id"), "-missing") {
					r.SetPathValue("id", policyID)
				}
				s.deleteApplicationPolicy(w, r)
			},
		},
		{
			name: "coding-mcp-audit", kind: "settings", id: "sw48-audit-sentinel",
			seed: func(data *domain.Bootstrap, actor domain.User) {
				data.Settings["sw48-audit-sentinel"] = "untouched"
				scaleP21SW48Collision(data, actor, "m3-coding")
			},
			handler: func(s *server, w http.ResponseWriter, r *http.Request) {
				var caller codingSandboxCaller
				caller.grant.WorkspaceKey, caller.grant.SandboxID = workspaceKey(r), "sw48-sandbox"
				caller.session.ID, caller.session.CreatorID = "m3-coding", authUser(r).ID
				s.auditCodingMCPToolCall(caller, applicationPolicy{ID: "sw48-policy", Name: "Connector"}, "read", json.RawMessage(`{"content":[]}`), nil, time.Second)
				w.WriteHeader(http.StatusNoContent)
			},
		},
		{
			name: "chat-integration-audit", kind: "settings", id: "sw48-audit-sentinel",
			seed: func(data *domain.Bootstrap, actor domain.User) {
				data.Settings["sw48-audit-sentinel"] = "untouched"
				scaleP21SW48Collision(data, actor, "sw48-chat-binding")
			},
			handler: func(s *server, w http.ResponseWriter, r *http.Request) {
				s.auditChatIntegrationContext(r.Context(), workspaceKey(r), "sw48-chat", "chat_binding", "sw48-chat-binding", map[string]any{"privateTeam": true})
				w.WriteHeader(http.StatusNoContent)
			},
		},
	})
}

func TestScaleP21SW48PolicyAuditCompleteAggregateEffects(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, aggregate := f.data.Workspace.URLKey, "sw48-policy-effect"
		for _, operation := range []string{"save", "delete", "coding-audit", "chat-audit"} {
			t.Run(operation, func(t *testing.T) {
				var outcomes []any
				for _, current := range []*store.SQLiteStore{repo, f.probe} {
					repo.SetWebhookSink(nil)
					repo.SetRealtimeSink(nil)
					if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw48.reset", "", nil, func(data *domain.Bootstrap) error {
						setApplicationPolicies(data, []applicationPolicy{{
							ID: aggregate, Kind: "oauth", Name: "Before policy", Status: "approved",
							OwnerID: f.data.Viewer.ID, Shared: true,
						}})
						scaleP21SW48Collision(data, f.data.Viewer, aggregate)
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
					var event domain.DomainEvent
					var realtime domain.RealtimeEvent
					webhooks, broadcasts := 0, 0
					current.SetWebhookSink(func(_ string, value domain.DomainEvent) { event = value; webhooks++ })
					current.SetRealtimeSink(func(_ string, value domain.RealtimeEvent) { realtime = value; broadcasts++ })
					service := &server{store: current}
					r := f.literalRequest(f.data.Viewer, nil, map[string]any{
						"id": aggregate, "kind": "oauth", "name": "After", "shared": true, "status": "approved",
					})
					r.SetPathValue("id", aggregate)
					w := httptest.NewRecorder()
					started := time.Now().UTC()
					switch operation {
					case "save":
						service.saveApplicationPolicy(w, r)
					case "delete":
						service.deleteApplicationPolicy(w, r)
					case "coding-audit":
						var caller codingSandboxCaller
						caller.grant.WorkspaceKey, caller.grant.SandboxID = workspace, "sw48-sandbox"
						caller.session.ID, caller.session.CreatorID = aggregate, f.data.Viewer.ID
						service.auditCodingMCPToolCall(caller, applicationPolicy{ID: "sw48-policy", Name: "Connector"}, "read", json.RawMessage(`{"isError":true}`), nil, time.Second)
					case "chat-audit":
						service.auditChatIntegrationContext(r.Context(), workspace, "sw48-chat", "chat_binding", aggregate, map[string]any{"privateTeam": true})
					}
					if operation == "save" && w.Code != http.StatusOK || operation == "delete" && w.Code != http.StatusNoContent ||
						webhooks != 1 || broadcasts != 1 {
						t.Fatalf("status/publication changed: status=%d webhooks=%d realtime=%d body=%s", w.Code, webhooks, broadcasts, w.Body.String())
					}
					var payload struct {
						Entity struct {
							ID, Marker, Body string
							HiddenCount      int `json:"hiddenCount"`
						} `json:"entity"`
					}
					if err := json.Unmarshal(realtime.Payload, &payload); err != nil || payload.Entity.ID != aggregate ||
						payload.Entity.Marker != "warm\x00earlier" || payload.Entity.HiddenCount != 37 || len(payload.Entity.Body) <= store.MetadataRecordPageMaxBytes {
						t.Fatal("complete earlier warm aggregate was lost", err)
					}
					var snapshot struct {
						Entity struct {
							Marker, Body string
						} `json:"entity"`
					}
					if err := json.Unmarshal(event.Snapshot, &snapshot); err != nil || snapshot.Entity.Marker != "warm\x00earlier" ||
						len(snapshot.Entity.Body) != len(payload.Entity.Body) {
						t.Fatal("webhook snapshot was truncated or substituted", err)
					}
					entries, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{Since: started})
					if err != nil {
						t.Fatal(err)
					}
					for i := range entries {
						entries[i].ID, entries[i].Hash = "", ""
						entries[i].Seq, entries[i].CreatedAt = 0, time.Time{}
					}
					rows := scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "settings", ID: "applicationPolicies"})
					outcomes = append(outcomes, scaleP21M3PointJSON(t, []any{
						w.Code, scaleP21M2Response(t, w, false), rows, event.Type, event.AggregateID, event.Payload,
						event.PreviousValues, event.Snapshot, realtime.Payload, entries,
					}))
					current.SetWebhookSink(nil)
					current.SetRealtimeSink(nil)
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("complete aggregate effects differ: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			})
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("bounded consumer decoded complete workspace metadata")
		}
	}
}

func TestScaleP21SW48PolicyConnectorDisableDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, setting := range []string{"enabled", "disabled", "unset", "hipaa"} {
				t.Run(setting, func(t *testing.T) {
					var results []any
					for _, current := range []*store.SQLiteStore{repo, f.probe} {
						repo.SetWebhookSink(nil)
						repo.SetRealtimeSink(nil)
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
						if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw48.connector_policy", "", nil, func(data *domain.Bootstrap) error {
							on := setting != "disabled"
							data.WorkspaceSettings.MCPConnectors = &on
							if setting == "unset" {
								data.WorkspaceSettings.MCPConnectors = nil
							}
							data.WorkspaceSettings.HIPAACompliance = setting == "hipaa"
							setApplicationPolicies(data, []applicationPolicy{{
								ID: "sw48-mcp-policy", Name: "Before", Kind: "mcp", URL: "https://93.184.216.34/mcp",
								OwnerID: user.ID, Shared: true, Status: "approved",
							}})
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
							t.Fatal(err)
						}
						r := f.literalRequest(user, key, map[string]any{
							"id": "sw48-mcp-policy", "name": "After", "kind": "mcp", "url": "https://93.184.216.34/mcp",
							"shared": true, "status": "approved",
						})
						w := httptest.NewRecorder()
						(&server{store: current}).saveApplicationPolicy(w, r)
						if setting == "disabled" || setting == "hipaa" {
							if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "MCP connectors are disabled") {
								t.Fatalf("disabled MCP connector accepted or permission precedence changed: status=%d body=%s", w.Code, w.Body.String())
							}
						}
						results = append(results, []any{
							w.Code, scaleP21M2Response(t, w, false),
							scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "settings", ID: "applicationPolicies"}),
						})
					}
					if !reflect.DeepEqual(results[0], results[1]) {
						t.Fatalf("connector policy differs: %s", scaleP21M2Diff(results[0], results[1]))
					}
				})
			}
		})
	}
}

func TestScaleP21SW48PolicyReadOwnsResponseLifetime(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw48.read_lifetime", "", nil, func(data *domain.Bootstrap) error {
			setApplicationPolicies(data, []applicationPolicy{{
				ID: "sw48-read-policy", Name: "Owned response", Kind: "oauth", Shared: true,
				OwnerID: f.data.Viewer.ID, Status: "approved",
			}})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		read, found, err := f.probe.BeginInstalledMetadataRead(t.Context(), workspace)
		if err != nil || !found {
			t.Fatal("owned test read", found, err)
		}
		defer read.Close()
		var held []*store.OwnedMetadataSelection
		saturated := false
		for range 256 {
			selection, err := read.Select(t.Context(), store.MetadataRecordScope{})
			if errors.Is(err, store.ErrMetadataWorkBudget) {
				saturated = true
				break
			}
			if err != nil {
				t.Fatal("fill owned output allowance", err)
			}
			held = append(held, selection)
		}
		if !saturated || len(held) < 2 {
			t.Fatal("test did not saturate owned response allowances")
		}
		held[len(held)-1].Close()
		r := f.literalRequest(f.data.Viewer, nil, nil)
		parent := r.Context()
		wrote := false
		w := &scaleP21SW48ResponseWriter{
			ResponseRecorder: httptest.NewRecorder(),
			beforeWrite: func() {
				wrote = true
				extra, err := read.Select(t.Context(), store.MetadataRecordScope{})
				if extra != nil {
					extra.Close()
				}
				if !errors.Is(err, store.ErrMetadataWorkBudget) {
					t.Error("handler released response admission before writing JSON", err)
				}
			},
		}
		(&server{store: f.probe}).listApplicationPolicies(w, r)
		if w.Code != http.StatusOK || !wrote || r.Context() != parent || !strings.Contains(w.Body.String(), "Owned response") {
			t.Fatalf("owned response or parent context changed: code=%d wrote=%v body=%s", w.Code, wrote, w.Body.String())
		}
		extra, err := read.Select(t.Context(), store.MetadataRecordScope{})
		if err != nil {
			t.Fatal("handler leaked response allowance", err)
		}
		extra.Close()
	}
}

func TestScaleP21SW48PolicyReadSQLBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		service := &server{store: f.probe}
		counts := []int{}
		for _, owned := range []bool{false, true} {
			queries := 0
			r := f.literalRequest(f.data.Viewer, nil, nil)
			r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(string) { queries++ }))
			w := httptest.NewRecorder()
			if owned {
				data, release, ok := service.applicationPolicyRead(w, r)
				if !ok || data.Workspace.ID != f.data.Workspace.ID {
					release()
					t.Fatalf("owned read failed: status=%d body=%s", w.Code, w.Body.String())
				}
				release()
			} else if data, ok := service.workspaceDataScoped(w, r, applicationPolicyScope()); !ok || data.Workspace.ID != f.data.Workspace.ID {
				t.Fatalf("existing scoped read failed: status=%d body=%s", w.Code, w.Body.String())
			}
			counts = append(counts, queries)
		}
		t.Logf("bounded read primary SQL before=%d after=%d; two policy flags come from the pinned installed generation", counts[0], counts[1])
		if counts[0] == 0 || counts[1] != counts[0] {
			t.Fatal("owned read/policy closure regressed primary SQL", counts)
		}
	}
}
