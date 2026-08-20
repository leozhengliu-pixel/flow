package main

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type webhookInput struct {
	Name          *string   `json:"name,omitempty"`
	URL           *string   `json:"url,omitempty"`
	ResourceTypes *[]string `json:"resourceTypes,omitempty"`
	TeamIDs       *[]string `json:"teamIds,omitempty"`
	Enabled       *bool     `json:"enabled,omitempty"`
}

func (s *server) listWebhooks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.workspaceData(r).Webhooks)
}

func (s *server) createWebhook(w http.ResponseWriter, r *http.Request) {
	var input webhookInput
	if !decodeJSON(w, r, &input) || input.Name == nil || input.URL == nil {
		writeError(w, http.StatusBadRequest, "name and url are required")
		return
	}
	actor := requestActor(s, r)
	var created domain.Webhook
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "webhook.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		created = domain.Webhook{ID: fmt.Sprintf("webhook_%d", now.UnixNano()), CreatorID: actor.ID, ResourceTypes: []string{}, TeamIDs: []string{}, Enabled: true, CreatedAt: now, UpdatedAt: now}
		if err := applyWebhookInput(data, &created, input); err != nil {
			return "", err
		}
		data.Webhooks = append(data.Webhooks, created)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateWebhook(w http.ResponseWriter, r *http.Request) {
	var input webhookInput
	if !decodeJSON(w, r, &input) {
		return
	}
	var updated domain.Webhook
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "webhook.updated", r.PathValue("id"), input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Webhooks, func(item domain.Webhook) bool { return item.ID == r.PathValue("id") })
		if index < 0 {
			return errNotFound
		}
		if err := applyWebhookInput(data, &data.Webhooks[index], input); err != nil {
			return err
		}
		data.Webhooks[index].UpdatedAt = time.Now().UTC()
		updated = data.Webhooks[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "webhook.deleted", r.PathValue("id"), nil, func(data *domain.Bootstrap) error {
		before := len(data.Webhooks)
		data.Webhooks = slices.DeleteFunc(data.Webhooks, func(item domain.Webhook) bool { return item.ID == r.PathValue("id") })
		if len(data.Webhooks) == before {
			return errNotFound
		}
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func applyWebhookInput(data *domain.Bootstrap, item *domain.Webhook, input webhookInput) error {
	if input.Name != nil {
		item.Name = strings.TrimSpace(*input.Name)
		if item.Name == "" {
			return errInvalid
		}
	}
	if input.URL != nil {
		value := strings.TrimSpace(*input.URL)
		parsed, err := url.ParseRequestURI(value)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return fmt.Errorf("%w: webhook URL must be http or https", errInvalid)
		}
		item.URL = value
	}
	if input.ResourceTypes != nil {
		allowed := []string{"issues", "comments", "projects", "cycles", "documents", "customers"}
		for _, value := range *input.ResourceTypes {
			if !slices.Contains(allowed, value) {
				return errInvalid
			}
		}
		item.ResourceTypes = slices.Compact(slices.Clone(*input.ResourceTypes))
	}
	if input.TeamIDs != nil {
		for _, id := range *input.TeamIDs {
			if !teamExists(data, id) {
				return errInvalid
			}
		}
		item.TeamIDs = slices.Compact(slices.Clone(*input.TeamIDs))
	}
	if input.Enabled != nil {
		item.Enabled = *input.Enabled
	}
	return nil
}
