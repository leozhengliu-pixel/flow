package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestIntegrationOAuthRefreshAndRevokeUseConfiguredProvider(t *testing.T) {
	handler, repository := enterpriseTestServer(t)
	var tokenCalls, revokeCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenCalls.Add(1)
			if err := r.ParseForm(); err != nil || r.Form.Get("client_id") != "client" {
				t.Fatalf("token request missing client id: %#v", r.Form)
			}
			w.Header().Set("Content-Type", "application/json")
			if r.Form.Get("grant_type") == "refresh_token" {
				_, _ = io.WriteString(w, `{"access_token":"refreshed","refresh_token":"rotated","expires_in":60}`)
				return
			}
			_, _ = io.WriteString(w, `{"access_token":"access","refresh_token":"refresh","expires_in":60}`)
		case "/revoke":
			revokeCalls.Add(1)
			if err := r.ParseForm(); err != nil || r.Form.Get("token") == "" {
				t.Fatalf("revoke request missing token: %#v", r.Form)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer provider.Close()
	connection := requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/slack?workspace=test-workspace", map[string]any{
		"name": "Slack", "config": map[string]string{
			"authorizationURL": "https://slack.example.test/oauth",
			"tokenURL":         provider.URL + "/token",
			"revokeURL":        provider.URL + "/revoke",
			"clientID":         "client",
			"redirectURI":      "https://flow.example.test/api/integrations/slack/oauth/callback",
		},
	}, http.StatusOK)
	started := requestJSON[map[string]string](t, handler, http.MethodPost, "/api/integrations/slack/oauth/start?workspace=test-workspace", nil, http.StatusOK)
	completed := requestJSON[map[string]string](t, handler, http.MethodGet, "/api/integrations/slack/oauth/callback?workspace=test-workspace&state="+url.QueryEscape(started["state"])+"&code=auth-code", nil, http.StatusOK)
	if completed["status"] != "configured" {
		t.Fatalf("oauth callback status=%#v", completed)
	}
	refreshed := requestJSON[domain.IntegrationConnection](t, handler, http.MethodPost, "/api/integrations/slack/"+connection.ID+"/oauth/refresh?workspace=test-workspace", nil, http.StatusOK)
	if refreshed.Status != "configured" || refreshed.OAuthAccessToken != "" {
		t.Fatalf("refresh leaked token or status: %#v", refreshed)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/integrations/slack/"+connection.ID+"/oauth/token?workspace=test-workspace", nil, http.StatusNoContent)
	if tokenCalls.Load() != 2 || revokeCalls.Load() != 1 {
		t.Fatalf("provider lifecycle calls token=%d revoke=%d", tokenCalls.Load(), revokeCalls.Load())
	}
	data, _ := repository.BootstrapFor("test-workspace")
	index := 0
	for i := range data.IntegrationConnections {
		if data.IntegrationConnections[i].ID == connection.ID {
			index = i
		}
	}
	if data.IntegrationConnections[index].Status != "disconnected" || data.IntegrationConnections[index].OAuthAccessToken != "" || data.IntegrationConnections[index].OAuthRefreshToken != "" {
		t.Fatalf("revoked credentials persisted: %#v", data.IntegrationConnections[index])
	}
}

func TestSlackWebhookSignatureAndReplayProtection(t *testing.T) {
	handler, repository := enterpriseTestServer(t)
	requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/slack?workspace=test-workspace", map[string]any{
		"name": "Slack", "config": map[string]string{"signingSecret": "signing-secret"},
	}, http.StatusOK)
	payload := []byte(`{"type":"event_callback","event_id":"Ev-1","team_id":"T1","event":{"type":"message","user":"U1","text":"hello"}}`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte("signing-secret"))
	_, _ = mac.Write([]byte("v0:" + timestamp + ":" + string(payload)))
	signature := "v0=" + hex.EncodeToString(mac.Sum(nil))
	request := func(sig string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/integrations/slack/webhook?workspace=test-workspace", bytes.NewReader(payload))
		req.Header.Set("X-Slack-Request-Timestamp", timestamp)
		req.Header.Set("X-Slack-Signature", sig)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := request(signature); rec.Code != http.StatusAccepted {
		t.Fatalf("valid Slack webhook status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := request(signature); rec.Code != http.StatusAccepted {
		t.Fatalf("replayed Slack webhook status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := request("v0=bad"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid Slack webhook status=%d", rec.Code)
	}
	data, _ := repository.BootstrapFor("test-workspace")
	count := 0
	for _, item := range data.Notifications {
		if item.SourceID == "Ev-1" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("Slack replay created %d notifications", count)
	}
}

func TestIntegrationDeliverySignsAndRetries(t *testing.T) {
	handler, repository := enterpriseTestServer(t)
	secret := "delivery-secret"
	var requests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body, _ := io.ReadAll(r.Body)
		var envelope map[string]any
		previous, previousOK := map[string]any(nil), false
		if err := json.Unmarshal(body, &envelope); err == nil {
			previous, previousOK = envelope["previousValues"].(map[string]any)
		}
		if !previousOK || previous["status"] != "backlog" {
			t.Errorf("outbound previousValues missing: body=%s", body)
		}
		timestamp := r.Header.Get("X-Flow-Timestamp")
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(timestamp + "." + string(body)))
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if r.Header.Get("X-Flow-Signature-256") != want {
			t.Errorf("invalid outbound signature got=%s want=%s", r.Header.Get("X-Flow-Signature-256"), want)
		}
		if requests.Load() == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer destination.Close()
	connection := requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/slack?workspace=test-workspace", map[string]any{
		"config": map[string]string{"deliveryURL": destination.URL, "deliverySecret": secret}, "scopes": []string{"events:write"},
	}, http.StatusOK)
	delivery := requestJSON[domain.IntegrationDelivery](t, handler, http.MethodPost, "/api/integration-deliveries?workspace=test-workspace", map[string]any{
		"connectionId": connection.ID, "eventType": "issue.updated", "resourceId": "issue-1", "payload": map[string]string{"id": "issue-1"}, "previousValues": map[string]string{"status": "backlog"},
	}, http.StatusAccepted)
	failed := requestJSON[domain.IntegrationDelivery](t, handler, http.MethodPost, "/api/integration-deliveries/"+delivery.ID+"/retry?workspace=test-workspace", nil, http.StatusOK)
	if failed.Status != "failed" || failed.Attempts != 1 || failed.NextAttemptAt == nil {
		t.Fatalf("failed delivery state=%#v", failed)
	}
	// Manual retry is allowed before the scheduled backoff and must retain the
	// same idempotency key while incrementing attempts.
	if err := repository.MutateWorkspace(t.Context(), "test-workspace", "test.retry_due", delivery.ID, nil, func(data *domain.Bootstrap) error {
		for i := range data.IntegrationDeliveries {
			if data.IntegrationDeliveries[i].ID == delivery.ID {
				data.IntegrationDeliveries[i].NextAttemptAt = ptrTime(time.Now().Add(-time.Minute))
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	delivered := requestJSON[domain.IntegrationDelivery](t, handler, http.MethodPost, "/api/integration-deliveries/"+delivery.ID+"/retry?workspace=test-workspace", nil, http.StatusOK)
	if delivered.Status != "delivered" || delivered.Attempts != 2 {
		t.Fatalf("delivered retry state=%#v", delivered)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
