package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP21SW5OAuthJSON(t *testing.T, value any) any {
	t.Helper()
	result := scaleP21M2JSON(t, value, false)
	var normalize func(any)
	normalize = func(value any) {
		switch item := value.(type) {
		case map[string]any:
			for key, value := range item {
				if key == "oauthExpiresAt" && value != nil {
					item[key] = "<generated-expiry>"
				} else {
					normalize(value)
				}
			}
		case []any:
			for _, value := range item {
				normalize(value)
			}
		}
	}
	normalize(result)
	return result
}

func TestScaleP21SW5IntegrationOAuthDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	enableConnectorSecrets(t)
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		target, other := idgen.New("sw5-oauth-"), idgen.New("sw5-other-oauth-")
		var mu sync.Mutex
		var mode string
		var action func() error
		var providerErr error
		var calls []string
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "invalid form", http.StatusBadRequest)
				return
			}
			mu.Lock()
			currentMode, currentAction := mode, action
			calls = append(calls, fmt.Sprintf("%s/%s/%s/%s", r.URL.Path, r.Form.Get("client_id"), r.Form.Get("grant_type"), r.Form.Get("refresh_token")))
			mu.Unlock()
			if currentAction != nil {
				if err := currentAction(); err != nil {
					mu.Lock()
					providerErr = err
					mu.Unlock()
					http.Error(w, "fixture action failed", http.StatusInternalServerError)
					return
				}
			}
			if strings.Contains(currentMode, "provider-rejected") {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			if r.URL.Path == "/revoke" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(currentMode, "no-access-token") {
				_, _ = w.Write([]byte(`{"expires_in":60}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"sw5-refreshed","refresh_token":"sw5-rotated","expires_in":60}`))
		}))
		t.Cleanup(provider.Close)
		unrelated := scaleP21M3Fingerprint(t, repo, workspace, "projects")
		type outcome struct {
			Response, Canonical any
			Tokens              store.IntegrationOAuthTokens
			Found               bool
			Events              []any
			Calls               []string
		}
		run := func(t *testing.T, user domain.User, key *domain.APIKey, operation, variant string, expected int) {
			t.Helper()
			var outcomes []outcome
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				repo.SetWebhookSink(nil)
				repo.SetRealtimeSink(nil)
				f.probe.SetWebhookSink(nil)
				f.probe.SetRealtimeSink(nil)
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw5.reset_oauth", "", nil, func(data *domain.Bootstrap) error {
					data.IntegrationConnections = []domain.IntegrationConnection{
						{ID: target, Provider: "slack", Name: "Target", Status: "connected", OAuthAuthorized: variant != "not-authorized", Config: map[string]string{
							"tokenURL": provider.URL + "/token", "revokeURL": provider.URL + "/revoke", "clientID": "sw5-client",
						}},
						{ID: other, Provider: "github", Name: "Retained", Config: map[string]string{"unrelated": strings.Repeat("unrelated ", 40000)}},
					}
					if variant == "missing-endpoint" {
						if operation == "refresh" {
							data.IntegrationConnections[0].Config["tokenURL"] = ""
						} else {
							data.IntegrationConnections[0].Config["revokeURL"] = ""
						}
					}
					if variant == "unsafe-endpoint" {
						if operation == "refresh" {
							data.IntegrationConnections[0].Config["tokenURL"] = "http://169.254.169.254/token"
						} else {
							data.IntegrationConnections[0].Config["revokeURL"] = "http://169.254.169.254/revoke"
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				initial := store.IntegrationOAuthTokens{AccessToken: "sw5-access", RefreshToken: "sw5-refresh"}
				if variant == "no-refresh-token" {
					initial.RefreshToken = ""
				}
				if variant == "no-tokens" {
					initial = store.IntegrationOAuthTokens{}
				}
				if err := repo.PutIntegrationOAuthTokens(t.Context(), workspace, target, initial); err != nil {
					t.Fatal(err)
				}
				otherBefore := scaleP21M3Fingerprint(t, repo, workspace, "settings")
				mu.Lock()
				mode, action, providerErr, calls = operation+"-"+variant, nil, nil, nil
				if variant == "concurrent-token" {
					action = func() error {
						return repo.PutIntegrationOAuthTokens(t.Context(), workspace, target, store.IntegrationOAuthTokens{AccessToken: "sw5-reconnected", RefreshToken: "sw5-new-refresh"})
					}
				}
				if variant == "disconnected" {
					action = func() error {
						// Exercise a disconnect on the handler's own instance in
						// both phases; a separate owner's write models a stale peer.
						return source.MutateWorkspaceScoped(t.Context(), workspace, "scale_p21_sw5.concurrent_disconnect", target, nil,
							store.MetadataRecordScope{Write: []store.RecordKey{{Kind: "integrationConnections", ID: target}}}, func(data *domain.Bootstrap) error {
								kept := data.IntegrationConnections[:0]
								for _, item := range data.IntegrationConnections {
									if item.ID != target {
										kept = append(kept, item)
									}
								}
								data.IntegrationConnections = kept
								return nil
							})
					}
				}
				mu.Unlock()
				got := outcome{}
				webhook := func(_ string, event domain.DomainEvent) {
					got.Events = append(got.Events, scaleP21SW5OAuthJSON(t, []any{"webhook", event.Type, event.AggregateID, event.Payload, event.PreviousValues}))
				}
				realtime := func(_ string, event domain.RealtimeEvent) {
					got.Events = append(got.Events, scaleP21SW5OAuthJSON(t, []any{"realtime", event.Type, event.AggregateID, event.ActorID, event.Payload}))
				}
				repo.SetWebhookSink(webhook)
				repo.SetRealtimeSink(realtime)
				f.probe.SetWebhookSink(webhook)
				f.probe.SetRealtimeSink(realtime)
				r, w := f.request(user, key, nil), httptest.NewRecorder()
				r.SetPathValue("id", target)
				r.SetPathValue("provider", "slack")
				if variant == "missing" {
					r.SetPathValue("id", target+"-missing")
				}
				if variant == "wrong-provider" {
					r.SetPathValue("provider", "github")
				}
				service := &server{store: source, authDisabled: true}
				started := time.Now().UTC()
				if operation == "refresh" {
					scaleP21M3Call(service, w, r, (*server).refreshIntegrationOAuth)
				} else {
					scaleP21M3Call(service, w, r, (*server).revokeIntegrationOAuth)
				}
				if w.Code != expected {
					t.Fatalf("%s/%s status=%d want=%d body=%s", operation, variant, w.Code, expected, w.Body.String())
				}
				for _, token := range []string{"sw5-access", "sw5-refresh", "sw5-refreshed", "sw5-rotated", "sw5-reconnected", "sw5-new-refresh"} {
					if strings.Contains(w.Body.String(), token) {
						t.Fatal("OAuth response contained a fixture credential")
					}
				}
				mu.Lock()
				got.Calls = append([]string(nil), calls...)
				err := providerErr
				mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				got.Response = []any{w.Code, scaleP21SW5OAuthJSON(t, scaleP21M2Response(t, w, false))}
				got.Canonical = scaleP21SW5OAuthJSON(t, scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "integrationConnections", ID: target}))
				got.Tokens, got.Found, err = repo.IntegrationOAuthTokens(t.Context(), workspace, target)
				if err != nil {
					t.Fatal(err)
				}
				if operation == "refresh" && variant == "success" {
					if !got.Found || got.Tokens != (store.IntegrationOAuthTokens{AccessToken: "sw5-refreshed", RefreshToken: "sw5-rotated"}) {
						t.Fatal("successful refresh did not rotate encrypted tokens")
					}
					batch, err := repo.ReadCanonicalRecords(t.Context(), workspace, []store.RecordKey{{Kind: "integrationConnections", ID: target}}, "detail")
					if err != nil || len(batch.Items) != 1 {
						t.Fatal("refreshed connection missing", err)
					}
					raw, _ := json.Marshal(batch.Items[0].Data)
					var connection domain.IntegrationConnection
					if err := json.Unmarshal(raw, &connection); err != nil || connection.OAuthExpiresAt == nil || connection.OAuthExpiresAt.Before(started.Add(59*time.Second)) || connection.OAuthExpiresAt.After(time.Now().Add(61*time.Second)) {
						t.Fatal("refresh expiry did not use provider lifetime", err)
					}
				}
				if operation == "revoke" && (variant == "success" || variant == "no-tokens") && (got.Found || got.Tokens != (store.IntegrationOAuthTokens{})) {
					t.Fatal("successful revoke retained encrypted tokens")
				}
				if variant == "concurrent-token" && got.Tokens.AccessToken != "sw5-reconnected" {
					t.Fatal("concurrent reconnect tokens were overwritten")
				}
				if operation == "refresh" && variant == "disconnected" && got.Found {
					t.Fatal("disconnected refresh retained replacement tokens")
				}
				if scaleP21M3Fingerprint(t, repo, workspace, "settings") != otherBefore || scaleP21M3Fingerprint(t, repo, workspace, "projects") != unrelated {
					t.Fatal("integration OAuth changed unrelated metadata")
				}
				outcomes = append(outcomes, got)
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatalf("%s/%s differs: %s", operation, variant, scaleP21M2Diff(outcomes[0], outcomes[1]))
			}
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, operation := range []string{"refresh", "revoke"} {
				for _, variant := range []string{"success", "missing", "wrong-provider"} {
					status := http.StatusNotFound
					if variant == "success" {
						status = http.StatusOK
						if operation == "revoke" {
							status = http.StatusNoContent
						}
					}
					run(t, user, key, operation, variant, status)
				}
			}
		})
		for _, spec := range []struct {
			operation, variant string
			status             int
		}{
			{"refresh", "not-authorized", http.StatusBadRequest},
			{"refresh", "no-refresh-token", http.StatusBadRequest},
			{"refresh", "missing-endpoint", http.StatusServiceUnavailable},
			{"refresh", "unsafe-endpoint", http.StatusUnprocessableEntity},
			{"refresh", "provider-rejected", http.StatusBadGateway},
			{"refresh", "no-access-token", http.StatusBadGateway},
			{"refresh", "concurrent-token", http.StatusNotFound},
			{"refresh", "disconnected", http.StatusNotFound},
			{"revoke", "no-tokens", http.StatusNoContent},
			{"revoke", "missing-endpoint", http.StatusServiceUnavailable},
			{"revoke", "unsafe-endpoint", http.StatusUnprocessableEntity},
			{"revoke", "provider-rejected", http.StatusBadGateway},
			{"revoke", "concurrent-token", http.StatusNotFound},
		} {
			t.Run(spec.operation+"/"+spec.variant, func(t *testing.T) {
				run(t, f.data.Viewer, nil, spec.operation, spec.variant, spec.status)
			})
		}
		repo.SetWebhookSink(nil)
		repo.SetRealtimeSink(nil)
		f.probe.SetWebhookSink(nil)
		f.probe.SetRealtimeSink(nil)
		member := f.principals[len(f.principals)-1].user
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"queued-revocation", "replay-revocation"} {
			for _, operation := range []func(*server, http.ResponseWriter, *http.Request){(*server).refreshIntegrationOAuth, (*server).revokeIntegrationOAuth} {
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					r, w := f.request(member, nil, nil), httptest.NewRecorder()
					r.SetPathValue("id", target)
					r.SetPathValue("provider", "slack")
					scaleP21M3Call(&server{store: source, authDisabled: true}, w, r, operation)
					if w.Code != http.StatusForbidden {
						t.Fatalf("%s did not revalidate membership: %d", phase, w.Code)
					}
				}
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("integration OAuth decoded complete metadata")
		}
		t.Cleanup(func() {
			_ = repo.DeleteIntegrationOAuthTokens(context.Background(), workspace, target)
		})
	}
}
