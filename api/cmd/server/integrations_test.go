package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21M2IntegrationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, operation := range []string{"error", "success", "gitlab", "probe", "display", "credential-audit"} {
				t.Run(operation, func(t *testing.T) {
					var effects, responses, audits []any
					for _, s := range []*server{{store: repo}, {store: f.probe}} {
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
						if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_m2.reset_integration", "", nil, func(data *domain.Bootstrap) error {
							data.IntegrationConnections = []domain.IntegrationConnection{{ID: "m2-connection", Provider: "gitlab", Status: "connected", Config: map[string]string{"sentinel": "retain"}}, {ID: "m2-other", Provider: "github", Config: map[string]string{"sentinel": "other"}}}
							data.ProjectDisplayDefault = json.RawMessage(`{"layout":"list"}`)
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						var event domain.DomainEvent
						var realtime domain.RealtimeEvent
						s.store.SetWebhookSink(func(_ string, e domain.DomainEvent) { event = e })
						s.store.SetRealtimeSink(func(_ string, e domain.RealtimeEvent) { realtime = e })
						r, w := f.request(user, key, map[string]any{"display": map[string]string{"layout": "board"}}), httptest.NewRecorder()
						r.SetPathValue("provider", "gitlab")
						r.SetPathValue("id", "m2-connection")
						switch operation {
						case "error":
							persistIntegrationTestError(s, r, "gitlab", "m2-connection", "M2 failed")
						case "success":
							persistIntegrationTestSuccess(s, r, "gitlab", "m2-connection", time.Unix(123456, 0).UTC())
						case "gitlab":
							_, err := s.saveGitLabConnection(r.Context(), workspace, "m2-connection", "integration.updated", func(item *domain.IntegrationConnection, now time.Time) {
								item.Config["rotationEnabled"] = "true"
							})
							if err != nil {
								t.Fatal(err)
							}
						case "probe":
							r = f.request(user, key, nil)
							r.SetPathValue("provider", "gitlab")
							r.SetPathValue("id", "m2-connection")
							s.testIntegrationConnection(w, r)
						case "display":
							s.updateProjectDisplayDefault(w, r)
						case "credential-audit":
							s.auditAPIKeyCredential(r, "m2-read", domain.OAuthApplication{ID: "m2-app", ClientID: "m2-client", Name: "M2"})
							if key != nil {
								entries, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{ResourceID: "m2-app", Limit: 1})
								if err != nil || len(entries) != 1 {
									t.Fatal("credential audit missing", err)
								}
								audit := entries[0]
								audit.ID, audit.CreatedAt, audit.Seq, audit.Hash = "", time.Time{}, 0, ""
								audits = append(audits, scaleP21M2JSON(t, audit, false))
							}
						}
						responses = append(responses, []any{w.Code, scaleP21M2Response(t, w, false)})
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
						data, _ := repo.WorkspaceMetadata(workspace)
						effects = append(effects, scaleP21M2JSON(t, []any{scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "integrationConnections", ID: "m2-connection"}, store.RecordKey{Kind: "integrationConnections", ID: "m2-other"}), data.ProjectDisplayDefault, event.Type, event.AggregateID, event.Payload, event.PreviousValues, realtime.Payload}, false))
					}
					if !reflect.DeepEqual(effects[0], effects[1]) || !reflect.DeepEqual(responses[0], responses[1]) || len(audits) == 2 && !reflect.DeepEqual(audits[0], audits[1]) {
						t.Fatalf("responses: %s; effects: %s; audit entries=%d", scaleP21M2Diff(responses[0], responses[1]), scaleP21M2Diff(effects[0], effects[1]), len(audits))
					}
				})
			}
		})
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("integration operations decoded full metadata")
		}
	}
}

func TestScaleP21M2ConnectorDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	enableConnectorSecrets(t)
	var calls, tools atomic.Int32
	tools.Store(2)
	host := fakeCodeMCP(t, &calls, &tools)
	defer host.Close()
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		policy := applicationPolicy{ID: "m2-connector", Kind: "mcp", URL: host.URL, Name: connectorHostName(host.URL), OwnerID: f.data.Viewer.ID, Shared: true, Status: "approved"}
		reset := func() {
			t.Helper()
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_m2.reset_connectors", "", nil, func(data *domain.Bootstrap) error {
				data.WorkspaceSettings.MCPConnectorsEnabled = true
				setApplicationPolicies(data, []applicationPolicy{policy})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		reset()
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			var outcomes []any
			for _, s := range []*server{{store: repo}, {store: f.probe}} {
				r := f.request(user, key, nil)
				r.SetPathValue("id", policy.ID)
				data, item, err := s.connectorForRequest(r)
				outcomes = append(outcomes, []any{data.Viewer.ID, data.ViewerRole, item.ID, err == nil, errors.Is(err, store.ErrAuthForbidden)})
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatalf("connector permission differential: %v", outcomes)
			}
		})
		var connected []any
		for _, s := range []*server{{store: repo, authDisabled: true}, {store: f.probe, authDisabled: true}} {
			reset()
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.SetPathValue("id", policy.ID)
			s.connectConnector(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("connect: %d %s", w.Code, w.Body.String())
			}
			var result connectorConnectResult
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || !result.Connected {
				t.Fatalf("connect failed: %s (%v)", w.Body.String(), err)
			}
			result.Policy.ConnectedAt = nil
			connected = append(connected, scaleP21M2JSON(t, result, false))
			s.agent = appconfig.AgentConfig{WriteTools: true, ToolsEnabled: true}
			tool := connectorTool{Policy: policy, RemoteName: "search_code", Definition: agentProviderTool{Name: "m2-offered", Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)}}
			ctx := context.WithValue(r.Context(), connectorToolsKey{}, []connectorTool{tool})
			// A local scripted peer exercises the actual connector transport.
			body, err := s.executeConnectorTool(r.WithContext(ctx), domain.Bootstrap{Viewer: f.data.Viewer}, domain.AgentToolCall{Name: "m2-offered", Arguments: json.RawMessage(`{"query":"M2"}`)})
			if err != nil || !strings.Contains(string(body), "roundTotal") {
				t.Fatalf("connector call: %s (%v)", body, err)
			}
		}
		if !reflect.DeepEqual(connected[0], connected[1]) {
			t.Fatalf("connector write differential: %v", connected)
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("connector operations decoded full metadata")
		}
	}
}

func TestScaleP21M2AgentWebhookScanDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_m2.hooks", "", nil, func(data *domain.Bootstrap) error {
			for i := 0; i < 121; i++ {
				data.Webhooks = append(data.Webhooks, domain.Webhook{ID: fmt.Sprintf("m2-hook-%03d", i), ManagedBy: "application", ApplicationClientID: fmt.Sprint(i), Enabled: true, URL: "https://example.test/events", ResourceTypes: []string{"agent_session_events"}})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, client := range []string{"0", "60", "120", "missing"} {
			app := domain.ApplicationInstallation{WorkspaceKey: workspace, ClientID: client}
			before, beforeErr := (&server{store: repo}).agentSessionTarget(t.Context(), app)
			after, afterErr := (&server{store: f.probe}).agentSessionTarget(t.Context(), app)
			if beforeErr != nil || afterErr != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("streamed target %s: %v/%v", client, beforeErr, afterErr)
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := (&server{store: f.probe}).agentSessionTarget(ctx, domain.ApplicationInstallation{WorkspaceKey: workspace}); !errors.Is(err, context.Canceled) {
			t.Fatalf("scan cancellation: %v", err)
		}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_m2.large_hook", "", nil, func(data *domain.Bootstrap) error {
			data.Webhooks[0].Name = strings.Repeat("x", store.MetadataRecordPageMaxBytes+1)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := (&server{store: f.probe}).agentSessionTarget(t.Context(), domain.ApplicationInstallation{WorkspaceKey: workspace}); !errors.Is(err, store.ErrMetadataPageBudget) {
			t.Fatalf("oversized hook was truncated: %v", err)
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("hook scan decoded full metadata")
		}
	}
}
