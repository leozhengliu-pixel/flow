package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type webhookInput struct {
	Name            *string   `json:"name,omitempty"`
	URL             *string   `json:"url,omitempty"`
	ResourceTypes   *[]string `json:"resourceTypes,omitempty"`
	TeamIDs         *[]string `json:"teamIds,omitempty"`
	TeamRestriction *string   `json:"teamRestriction,omitempty"`
	Enabled         *bool     `json:"enabled,omitempty"`
}

func (s *server) listWebhooks(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	actor := requestActor(s, r)
	admin := strings.EqualFold(data.ViewerRole, "admin") || strings.EqualFold(data.ViewerRole, "owner")
	result := make([]domain.Webhook, 0, len(data.Webhooks))
	for _, item := range data.Webhooks {
		if (admin || item.CreatorID == actor.ID) && webhookVisibleToBootstrap(&data, item) {
			result = append(result, publicWebhook(item))
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) createWebhook(w http.ResponseWriter, r *http.Request) {
	var input webhookInput
	if !decodeJSON(w, r, &input) || input.Name == nil || input.URL == nil {
		writeError(w, http.StatusBadRequest, "name and url are required")
		return
	}
	actor := requestActor(s, r)
	secret, secretErr := randomWebhookSecret()
	if secretErr != nil {
		respondMutation(w, secretErr, http.StatusInternalServerError, nil)
		return
	}
	var created domain.Webhook
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "webhook.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		if input.TeamIDs != nil && !webhookTeamsManageable(data, actor.ID, *input.TeamIDs) {
			return "", store.ErrAuthForbidden
		}
		created = domain.Webhook{ID: fmt.Sprintf("webhook_%d", now.UnixNano()), CreatorID: actor.ID, ResourceTypes: []string{}, TeamIDs: []string{}, TeamRestriction: "all", SecretHash: secretHash(secret), SecretPrefix: secret[:min(len(secret), 17)], Enabled: true, CreatedAt: now, UpdatedAt: now}
		if err := applyWebhookInput(data, &created, input); err != nil {
			return "", err
		}
		data.Webhooks = append(data.Webhooks, created)
		return created.ID, nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	response := publicWebhookMap(created)
	response["secret"] = secret
	writeJSON(w, http.StatusCreated, response)
}

func (s *server) updateWebhook(w http.ResponseWriter, r *http.Request) {
	var input webhookInput
	if !decodeJSON(w, r, &input) {
		return
	}
	actor := requestActor(s, r)
	var updated domain.Webhook
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "webhook.updated", r.PathValue("id"), input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Webhooks, func(item domain.Webhook) bool { return item.ID == r.PathValue("id") })
		if index < 0 {
			return errNotFound
		}
		if !webhookActorCanManage(data, actor, data.Webhooks[index]) {
			return store.ErrAuthForbidden
		}
		if input.TeamIDs != nil && !webhookTeamsManageable(data, actor.ID, *input.TeamIDs) {
			return store.ErrAuthForbidden
		}
		if err := applyWebhookInput(data, &data.Webhooks[index], input); err != nil {
			return err
		}
		data.Webhooks[index].UpdatedAt = time.Now().UTC()
		updated = data.Webhooks[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, publicWebhook(updated))
}

func (s *server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	actor := requestActor(s, r)
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "webhook.deleted", r.PathValue("id"), nil, func(data *domain.Bootstrap) error {
		before := len(data.Webhooks)
		data.Webhooks = slices.DeleteFunc(data.Webhooks, func(item domain.Webhook) bool {
			return item.ID == r.PathValue("id") && webhookActorCanManage(data, actor, item)
		})
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

func webhookActorCanManage(data *domain.Bootstrap, actor domain.User, item domain.Webhook) bool {
	return webhookVisibleToBootstrap(data, item) && (strings.EqualFold(data.ViewerRole, "admin") || strings.EqualFold(data.ViewerRole, "owner") || item.CreatorID == actor.ID)
}

func webhookVisibleToBootstrap(data *domain.Bootstrap, item domain.Webhook) bool {
	if item.TeamRestriction == "" || item.TeamRestriction == "all" {
		return true
	}
	for _, teamID := range item.TeamIDs {
		if slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID }) {
			return true
		}
	}
	return false
}

func webhookTeamsManageable(data *domain.Bootstrap, actorID string, teamIDs []string) bool {
	admin := strings.EqualFold(data.ViewerRole, "admin") || strings.EqualFold(data.ViewerRole, "owner")
	for _, teamID := range teamIDs {
		teamIndex := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID })
		if teamIndex < 0 {
			return false
		}
		team := data.Teams[teamIndex]
		if !team.Private || admin {
			continue
		}
		if !slices.ContainsFunc(data.TeamMembers, func(member domain.TeamMember) bool { return member.TeamID == teamID && member.UserID == actorID }) {
			return false
		}
	}
	return true
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
		allowed := []string{"issues", "comments", "attachments", "documents", "reactions", "projects", "project_updates", "cycles", "labels", "users", "issue_sla", "initiatives", "customers", "customer_requests", "releases", "milestones", "relations"}
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
		if input.TeamRestriction == nil {
			if len(item.TeamIDs) > 0 {
				item.TeamRestriction = "selected"
			} else {
				item.TeamRestriction = "all"
			}
		}
	}
	if input.TeamRestriction != nil {
		restriction := strings.ToLower(strings.TrimSpace(*input.TeamRestriction))
		if restriction != "all" && restriction != "selected" {
			return errInvalid
		}
		item.TeamRestriction = restriction
		if restriction == "all" {
			item.TeamIDs = nil
		}
	}
	if input.Enabled != nil {
		item.Enabled = *input.Enabled
	}
	return nil
}

func randomWebhookSecret() (string, error) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return "flow_wh_" + base64.RawURLEncoding.EncodeToString(buffer), nil
}

func publicWebhook(item domain.Webhook) domain.Webhook {
	item.SecretHash = ""
	return item
}

func publicWebhookMap(item domain.Webhook) map[string]any {
	raw, _ := json.Marshal(publicWebhook(item))
	result := map[string]any{}
	_ = json.Unmarshal(raw, &result)
	return result
}

func (s *server) rotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	secret, err := randomWebhookSecret()
	if err != nil {
		respondMutation(w, err, http.StatusInternalServerError, nil)
		return
	}
	actor := requestActor(s, r)
	id := r.PathValue("id")
	var rotated domain.Webhook
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "webhook.secret_rotated", id, nil, func(data *domain.Bootstrap) error {
		admin := strings.EqualFold(data.ViewerRole, "admin") || strings.EqualFold(data.ViewerRole, "owner")
		index := slices.IndexFunc(data.Webhooks, func(item domain.Webhook) bool { return item.ID == id && (admin || item.CreatorID == actor.ID) })
		if index < 0 {
			return errNotFound
		}
		data.Webhooks[index].SecretHash = secretHash(secret)
		data.Webhooks[index].SecretPrefix = secret[:min(len(secret), 17)]
		data.Webhooks[index].SecretRevokedAt = nil
		data.Webhooks[index].UpdatedAt = time.Now().UTC()
		rotated = publicWebhook(data.Webhooks[index])
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNotFound, nil)
		return
	}
	response := publicWebhookMap(rotated)
	response["secret"] = secret
	writeJSON(w, http.StatusOK, response)
}

func (s *server) revokeWebhookSecret(w http.ResponseWriter, r *http.Request) {
	actor := requestActor(s, r)
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "webhook.secret_revoked", id, nil, func(data *domain.Bootstrap) error {
		admin := strings.EqualFold(data.ViewerRole, "admin") || strings.EqualFold(data.ViewerRole, "owner")
		index := slices.IndexFunc(data.Webhooks, func(item domain.Webhook) bool { return item.ID == id && (admin || item.CreatorID == actor.ID) })
		if index < 0 {
			return errNotFound
		}
		now := time.Now().UTC()
		data.Webhooks[index].SecretHash = ""
		data.Webhooks[index].SecretPrefix = ""
		data.Webhooks[index].SecretRevokedAt = &now
		data.Webhooks[index].UpdatedAt = now
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}
