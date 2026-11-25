package main

import (
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW46ChatHealthMutationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace, target := f.data.Workspace.URLKey, idgen.New("sw46-chat-health-")
		probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		old := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
		platformFailure := &chatPlatformError{Status: 503, Code: 999, Retryable: true}
		processingFailure := &chatOperationError{Path: "/api/issue-records/{id}", Reason: "response_invalid"}
		for _, test := range []struct {
			name, operation, testStatus, lastError string
			missingTarget, missingCurrent          bool
			disconnected, nested                   bool
			deliveryErr                            error
			wantNoop                               bool
		}{
			{name: "sync-existing-ready", operation: "sync", testStatus: "success"},
			{name: "sync-existing-error", operation: "sync", testStatus: "failed", lastError: "Exact tenant failure"},
			{name: "sync-preserve-test", operation: "sync"},
			{name: "sync-missing-append", operation: "sync", testStatus: "success", missingTarget: true},
			{name: "sync-earlier-nested-aggregate", operation: "sync", nested: true},
			{name: "delivery-success-clear", lastError: "Chat delivery: previous failure"},
			{name: "delivery-success-preserve", lastError: "Keep configuration failure"},
			{name: "delivery-platform-failure", deliveryErr: platformFailure},
			{name: "delivery-processing-failure", deliveryErr: processingFailure},
			{name: "delivery-platform-noop", deliveryErr: platformFailure, lastError: "Chat delivery: " + platformFailure.Error(), wantNoop: true},
			{name: "delivery-processing-noop", deliveryErr: processingFailure, lastError: chatProcessingErrorMessage(processingFailure), wantNoop: true},
			{name: "delivery-missing-target", missingTarget: true},
			{name: "delivery-missing-current", missingCurrent: true},
			{name: "delivery-disconnected", disconnected: true},
			{name: "delivery-earlier-nested-aggregate", nested: true},
		} {
			t.Run(test.name, func(t *testing.T) {
				type outcome struct {
					Connections any
					Domain      []any
					Realtime    []any
					Error       string
					Noop        bool
				}
				var outputs []outcome
				var counts []int
				var decodes []int64
				for _, source := range []*store.SQLiteStore{repo, probe} {
					repo.SetWebhookSink(nil)
					repo.SetRealtimeSink(nil)
					probe.SetWebhookSink(nil)
					probe.SetRealtimeSink(nil)
					if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					if err := func() error {
						restore := store.ForceFullMutationsForTesting(true)
						defer restore()
						return repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw46_seed", "", nil, func(data *domain.Bootstrap) error {
							data.Documents = nil
							if test.nested {
								data.Documents = []domain.Document{{
									ID: target + "-document", Title: "Earlier document", Creator: f.data.Viewer,
									ContentData: map[string]any{"nested": map[string]any{"id": target, "name": "Earlier nested health entity", "native": 7}},
								}}
							}
							data.IntegrationConnections = []domain.IntegrationConnection{
								{ID: target + "-before", Provider: "github", Name: "Retained before", Config: map[string]string{"sentinel": "before"}},
								{ID: target, Provider: "lark", Name: "Original connection", Status: "connected", ConnectedBy: f.data.Viewer.ID,
									Config: map[string]string{"original": "keep unless sync"}, OAuthAuthorized: true, OAuthState: "original-state",
									CreatedAt: old, UpdatedAt: old, LastTestAt: &old, LastTestStatus: "original-test", LastDeliveryAt: &old, LastError: test.lastError},
								{ID: target + "-after", Provider: "slack", Name: "Retained after", Config: map[string]string{"sentinel": strings.Repeat("unrelated ", 1000)}}}
							if test.missingTarget {
								data.IntegrationConnections = append(data.IntegrationConnections[:1], data.IntegrationConnections[2:]...)
							}
							return nil
						})
					}(); err != nil {
						t.Fatal(err)
					}
					record := domain.ChatIntegrationRecord{
						WorkspaceKey: workspace, ConnectionID: target, Provider: "lark", Connected: !test.disconnected,
						Name: "Exact synchronized name", AppID: "sw46-app", TenantKey: "sw46-tenant", BotOpenID: "sw46-bot",
					}
					if test.missingCurrent {
						if err := repo.DeleteChatIntegration(t.Context(), workspace, target); err != nil {
							t.Fatal(err)
						}
					} else if err := repo.PutChatIntegration(t.Context(), record); err != nil {
						t.Fatal(err)
					}
					if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
					beforeDocuments := scaleP21M3Fingerprint(t, repo, workspace, "documents")
					beforeProjects := scaleP21M3Fingerprint(t, repo, workspace, "projects")
					var got outcome
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						got.Domain = append(got.Domain, []any{event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues, event.Snapshot})
					})
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
						got.Realtime = append(got.Realtime, []any{event.Type, event.AggregateID, event.ActorID, event.ClientID, event.Payload})
					})
					statements, fullHydrationReads := 0, 0
					ctx := store.WithSQLStatementObserver(t.Context(), func(query string) {
						statements++
						if strings.HasPrefix(query, "SELECT id,data FROM issue_records WHERE workspace_key=") {
							fullHydrationReads++
						}
					})
					started := time.Now().UTC()
					beforeDecodes := source.WorkspaceMetadataDecodes()
					err := func() error {
						restore := store.ForceFullMutationsForTesting(source == repo)
						defer restore()
						service := &server{store: source}
						if test.operation == "sync" {
							return service.syncChatConnection(ctx, record, f.data.Viewer.ID, "configured", test.testStatus, test.lastError)
						}
						return service.recordChatDeliveryResultLocked(ctx, record, test.deliveryErr)
					}()
					source.SetWebhookSink(nil)
					source.SetRealtimeSink(nil)
					got.Noop = len(got.Domain) == 0 && len(got.Realtime) == 0
					if err != nil {
						got.Error = err.Error()
					}
					if got.Error != "" || test.wantNoop && !got.Noop {
						t.Fatal("chat health result differs", source.BoundedMetadataEnabled(), got.Noop, err)
					}
					if beforeDocuments != scaleP21M3Fingerprint(t, repo, workspace, "documents") || beforeProjects != scaleP21M3Fingerprint(t, repo, workspace, "projects") {
						t.Fatal("health write changed unrelated canonical records")
					}
					decoded := source.WorkspaceMetadataDecodes() - beforeDecodes
					if source.MetadataWorkUsedForTesting() != 0 || source.BoundedMetadataEnabled() && decoded != 0 {
						t.Fatal("health write leaked admission or decoded complete metadata")
					}
					if source == repo && !test.missingCurrent && !test.disconnected && fullHydrationReads == 0 {
						t.Fatal("literal oracle did not take the forced full hydration path")
					}
					connections := []domain.IntegrationConnection{}
					if err := repo.StreamMetadataRecords(t.Context(), workspace, "integrationConnections", func(_ store.RecordKey, reader io.Reader) error {
						var item domain.IntegrationConnection
						if err := json.NewDecoder(reader).Decode(&item); err != nil {
							return err
						}
						connections = append(connections, item)
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if test.operation == "sync" && test.missingTarget && connections[len(connections)-1].ID != target {
						t.Fatal("missing connection was not appended after existing records")
					}
					got.Connections = connections
					raw, err := json.Marshal(got)
					if err != nil {
						t.Fatal(err)
					}
					var normalized any
					if err := json.Unmarshal(raw, &normalized); err != nil {
						t.Fatal(err)
					}
					var normalize func(any)
					normalize = func(value any) {
						switch item := value.(type) {
						case map[string]any:
							for key, child := range item {
								if key == "createdAt" || key == "updatedAt" || key == "lastTestAt" || key == "lastDeliveryAt" {
									if text, ok := child.(string); ok {
										if at, err := time.Parse(time.RFC3339Nano, text); err == nil && !at.Before(started) {
											item[key] = "<generated-clock>"
										}
									}
								} else {
									normalize(child)
								}
							}
						case []any:
							for _, child := range item {
								normalize(child)
							}
						}
					}
					normalize(normalized)
					raw, _ = json.Marshal(normalized)
					if err := json.Unmarshal(raw, &got); err != nil {
						t.Fatal(err)
					}
					if test.nested {
						if len(got.Realtime) != 1 {
							t.Fatal("nested aggregate event missing")
						}
						payload := got.Realtime[0].([]any)[4].(map[string]any)
						entity, ok := payload["entity"].(map[string]any)
						if !ok || entity["id"] != target || entity["name"] != "Earlier nested health entity" {
							t.Fatal("earlier nested aggregate lost precedence", payload)
						}
					}
					outputs = append(outputs, got)
					counts = append(counts, statements)
					decodes = append(decodes, decoded)
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatal("full legacy/bounded chat health effects differ", scaleP21M2Diff(outputs[0], outputs[1]))
				}
				t.Logf("SQL full_legacy=%d bounded=%d full_decodes=%d/%d", counts[0], counts[1], decodes[0], decodes[1])
			})
		}
	}
}
