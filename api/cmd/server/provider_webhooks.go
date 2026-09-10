package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) providerWebhook(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	if provider != "sentry" && provider != "intercom" {
		writeError(w, 404, "Unsupported webhook provider")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "Webhook exceeds limit")
		return
	}
	secret := os.Getenv(providerEnv(provider) + "_WEBHOOK_SECRET")
	if secret == "" {
		writeError(w, 503, "Webhook signature verification is not configured")
		return
	}
	supplied := ""
	var digest []byte
	if provider == "sentry" {
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(body)
		digest = mac.Sum(nil)
		supplied = r.Header.Get("Sentry-Hook-Signature")
	} else {
		mac := hmac.New(sha1.New, []byte(secret))
		_, _ = mac.Write(body)
		digest = mac.Sum(nil)
		supplied = strings.TrimPrefix(r.Header.Get("X-Hub-Signature"), "sha1=")
	}
	signature, err := hex.DecodeString(supplied)
	if err != nil || !hmac.Equal(signature, digest) {
		writeError(w, 401, "Invalid webhook signature")
		return
	}
	key := workspaceKey(r)
	if key == "" {
		writeError(w, 400, "Workspace is required")
		return
	}
	metadata, ok := s.store.WorkspaceMetadata(key)
	if !ok {
		writeError(w, 404, "Workspace not found")
		return
	}
	if metadata.WorkspaceSettings.HIPAACompliance {
		writeError(w, 403, "External processing is disabled")
		return
	}
	var connection *domain.IntegrationConnection
	for _, item := range metadata.IntegrationConnections {
		if item.Provider == provider && item.Status == "connected" {
			connection = &item
			break
		}
	}
	if connection == nil {
		writeError(w, 409, "Integration is not connected")
		return
	}
	data, err := s.store.PagedWorkspaceMetadata(r.Context(), key, connection.ConnectedBy)
	if err != nil {
		writeError(w, 403, "Integration owner no longer has access")
		return
	}
	var payload struct {
		Action string `json:"action"`
		Topic  string `json:"topic"`
		Data   struct {
			Issue struct {
				ID json.RawMessage `json:"id"`
			} `json:"issue"`
			Item struct {
				ID json.RawMessage `json:"id"`
			} `json:"item"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &payload) != nil {
		writeError(w, 400, "Invalid webhook payload")
		return
	}
	raw := payload.Data.Issue.ID
	if provider == "sentry" && payload.Action != "created" && payload.Action != "triggered" {
		w.WriteHeader(204)
		return
	}
	if provider == "intercom" {
		if payload.Topic != "conversation.user.created" && payload.Topic != "conversation.admin.replied" {
			w.WriteHeader(204)
			return
		}
		raw = payload.Data.Item.ID
	}
	id := strings.Trim(string(raw), `"`)
	if id == "" {
		writeError(w, 400, "Provider resource ID is required")
		return
	}
	ctx := context.WithValue(r.Context(), authUserContextKey{}, data.Viewer)
	ctx = context.WithValue(ctx, workspaceKeyContextKey{}, key)
	ctx = store.ContextWithActor(ctx, data.Viewer)
	r = r.WithContext(ctx)
	resource, err := s.readProviderResource(r, *connection, id)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	teamID := connection.Config["teamId"]
	issueID, err := s.importProviderResource(r, data, *connection, resource, teamID)
	if err != nil {
		writeError(w, 400, fmt.Sprint(err))
		return
	}
	writeJSON(w, 202, map[string]string{"issueId": issueID})
}
