package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/publicapi"
	"flow/api/internal/store"
)

type webhookInput struct {
	Name            *string   `json:"name,omitempty"`
	URL             *string   `json:"url,omitempty"`
	ApplicationID   *string   `json:"applicationId,omitempty"`
	ResourceTypes   *[]string `json:"resourceTypes,omitempty"`
	TeamIDs         *[]string `json:"teamIds,omitempty"`
	TeamRestriction *string   `json:"teamRestriction,omitempty"`
	Enabled         *bool     `json:"enabled,omitempty"`
	// PayloadVersion switches a legacy webhook to the current envelope (2);
	// new webhooks use the current envelope unless 1 is requested.
	PayloadVersion *int `json:"payloadVersion,omitempty"`
	// spellings: label (name), teamId (one team) and
	// allPublicTeams.
	Label          *string `json:"label,omitempty"`
	TeamID         *string `json:"teamId,omitempty"`
	AllPublicTeams *bool   `json:"allPublicTeams,omitempty"`
}

func (input *webhookInput) normalize() {
	if input.Name == nil && input.Label != nil {
		input.Name = input.Label
	}
	if input.TeamIDs == nil && input.TeamID != nil && strings.TrimSpace(*input.TeamID) != "" {
		ids := []string{strings.TrimSpace(*input.TeamID)}
		input.TeamIDs = &ids
	}
	if input.AllPublicTeams != nil && *input.AllPublicTeams && input.TeamRestriction == nil {
		all := "all"
		input.TeamRestriction = &all
	}
}

// webhookResponse is a webhook as the API returns it.
func webhookResponse(item domain.Webhook) publicapi.Webhook {
	status := "active"
	switch {
	case item.SecretRevokedAt != nil && !item.SigningEnabled:
		status = "revoked"
	case !item.SigningEnabled && item.ManagedBy != "application":
		// Created before signing: only a hash of the secret was kept.
		status = "rotationRequired"
	}
	return publicapi.Webhook{Webhook: publicWebhook(item), PayloadVersion: webhookPayloadVersion(item), SigningStatus: status}
}

// webhookWithSecret adds the signing secret, shown once.
func webhookWithSecret(item domain.Webhook, secret string) publicapi.WebhookWithSecret {
	return publicapi.WebhookWithSecret{Webhook: webhookResponse(item), Secret: secret}
}

func (s *server) listWebhooks(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	actor := requestActor(s, r)
	admin := strings.EqualFold(data.ViewerRole, "admin") || strings.EqualFold(data.ViewerRole, "owner")
	result := make([]publicapi.Webhook, 0, len(data.Webhooks))
	for _, item := range data.Webhooks {
		if (admin || item.CreatorID == actor.ID) && webhookVisibleToBootstrap(&data, item) {
			result = append(result, webhookResponse(item))
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) manageableWebhook(r *http.Request) (domain.Webhook, bool) {
	data := s.workspaceData(r)
	actor := requestActor(s, r)
	id := r.PathValue("id")
	index := slices.IndexFunc(data.Webhooks, func(item domain.Webhook) bool { return item.ID == id })
	if index < 0 || !webhookActorCanManage(&data, actor, data.Webhooks[index]) {
		return domain.Webhook{}, false
	}
	return data.Webhooks[index], true
}

func (s *server) getWebhook(w http.ResponseWriter, r *http.Request) {
	webhook, ok := s.manageableWebhook(r)
	if !ok {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	writeJSON(w, http.StatusOK, webhookResponse(webhook))
}

// listWebhookFailures lists failed attempts, newest first: those recorded
// before the delivery log existed and failed attempts from the log.
func (s *server) listWebhookFailures(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	webhook, ok := s.manageableWebhook(r)
	if !ok {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	result := make([]domain.WebhookFailureEvent, 0)
	page, err := s.store.WebhookDeliveries(r.Context(), store.WebhookDeliveryQuery{Workspace: workspaceKey(r), WebhookID: webhook.ID, Status: "failed", Limit: 50})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	for _, delivery := range page.Items {
		for index := len(delivery.Attempts) - 1; index >= 0; index-- {
			attempt := delivery.Attempts[index]
			if attempt.Succeeded {
				continue
			}
			failure := domain.WebhookFailureEvent{ID: fmt.Sprintf("%s:%d", delivery.ID, attempt.Attempt), WebhookID: webhook.ID, ApplicationID: webhook.ApplicationID, ExecutionID: delivery.ID, URL: delivery.URL, ResponseOrError: firstNonEmpty(attempt.Error, "delivery failed"), CreatedAt: attempt.At}
			if attempt.ResponseStatus > 0 {
				status := attempt.ResponseStatus
				failure.HTTPStatus = &status
				if index == len(delivery.Attempts)-1 && delivery.ResponseBody != "" {
					failure.ResponseOrError = delivery.ResponseBody
				}
			}
			result = append(result, failure)
		}
	}
	legacy := []domain.WebhookFailureEvent{}
	for _, item := range data.WebhookFailureEvents {
		if item.WebhookID == webhook.ID {
			legacy = append(legacy, item)
		}
	}
	slices.Reverse(legacy)
	result = append(result, legacy...)
	slices.SortStableFunc(result, func(a, b domain.WebhookFailureEvent) int { return b.CreatedAt.Compare(a.CreatedAt) })
	writeJSON(w, http.StatusOK, result)
}

// validWebhookURL requires a public HTTPS URL. Development servers (auth
// disabled) may also use http on a loopback host.
func (s *server) validWebhookURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("%w: webhook URL must be a valid HTTPS URL", errInvalid)
	}
	if s.authDisabled && safeLocalDevelopmentURL(value) {
		return value, nil
	}
	if parsed.Scheme != "https" {
		return "", fmt.Errorf("%w: webhook URL must use HTTPS", errInvalid)
	}
	host := strings.ToLower(parsed.Hostname())
	if outboundPolicy.HostTrusted(host) {
		// An operator-trusted private endpoint: deliveries re-check that it
		// resolves inside FLOW_TRUSTED_PRIVATE_NETWORKS.
		return value, nil
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return "", fmt.Errorf("%w: webhook URL must be publicly reachable", errInvalid)
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
		return "", fmt.Errorf("%w: webhook URL must be publicly reachable", errInvalid)
	}
	return value, nil
}

func (s *server) createWebhook(w http.ResponseWriter, r *http.Request) {
	var input webhookInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.normalize()
	if input.Name == nil {
		// The reference app names a webhook after its URL when no label is given.
		if input.URL != nil {
			name := strings.TrimSpace(*input.URL)
			input.Name = &name
		}
	}
	if input.Name == nil || input.URL == nil {
		writeError(w, http.StatusBadRequest, "name and url are required")
		return
	}
	target, err := s.validWebhookURL(*input.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), errInvalid.Error()+": "))
		return
	}
	input.URL = &target
	actor := requestActor(s, r)
	secret, secretErr := randomWebhookSecret()
	if secretErr != nil {
		respondMutation(w, secretErr, http.StatusInternalServerError, nil)
		return
	}
	var created domain.Webhook
	err = s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "webhook.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		if input.TeamIDs != nil && !webhookTeamsManageable(data, actor.ID, *input.TeamIDs) {
			return "", store.ErrAuthForbidden
		}
		created = domain.Webhook{ID: idgen.New("webhook_"), CreatorID: actor.ID, ResourceTypes: []string{}, TeamIDs: []string{}, TeamRestriction: "all", SecretHash: secretHash(secret), SecretPrefix: secret[:min(len(secret), 17)], Enabled: true, PayloadVersion: domain.WebhookPayloadVersion, SigningEnabled: true, CreatedAt: now, UpdatedAt: now}
		if err := applyWebhookInput(data, &created, input); err != nil {
			return "", err
		}
		data.Webhooks = append(data.Webhooks, created)
		appendAudit(data, "created", "webhook", created.ID, map[string]any{"url": created.URL})
		return created.ID, nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	if err := s.store.SetWebhookSigningSecret(r.Context(), store.WebhookSecretRef(created.ID), workspaceKey(r), secret); err != nil {
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	writeJSON(w, http.StatusCreated, webhookWithSecret(created, secret))
}

func (s *server) updateWebhook(w http.ResponseWriter, r *http.Request) {
	var input webhookInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.normalize()
	if input.URL != nil {
		target, err := s.validWebhookURL(*input.URL)
		if err != nil {
			writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), errInvalid.Error()+": "))
			return
		}
		input.URL = &target
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
		if data.Webhooks[index].ManagedBy == "application" && (input.URL != nil || input.ResourceTypes != nil || input.TeamIDs != nil || input.TeamRestriction != nil || input.ApplicationID != nil) {
			// The application's developer owns these settings; the workspace
			// can only pause or resume the webhook.
			return fmt.Errorf("%w: this webhook is managed by its application", errInvalid)
		}
		if input.TeamIDs != nil && !webhookTeamsManageable(data, actor.ID, *input.TeamIDs) {
			return store.ErrAuthForbidden
		}
		wasEnabled := data.Webhooks[index].Enabled
		if err := applyWebhookInput(data, &data.Webhooks[index], input); err != nil {
			return err
		}
		item := &data.Webhooks[index]
		if item.Enabled && !wasEnabled {
			// Re-enabling starts a fresh failure window.
			item.DisabledReason, item.DisabledAt, item.FailingSince = "", nil, nil
		}
		item.UpdatedAt = time.Now().UTC()
		updated = *item
		return nil
	})
	if err == nil && !updated.Enabled {
		_ = s.store.CancelWebhookDeliveries(r.Context(), workspaceKey(r), updated.ID)
	}
	respondMutation(w, err, http.StatusOK, webhookResponse(updated))
}

func (s *server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	actor := requestActor(s, r)
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "webhook.deleted", id, nil, func(data *domain.Bootstrap) error {
		before := len(data.Webhooks)
		managed := false
		data.Webhooks = slices.DeleteFunc(data.Webhooks, func(item domain.Webhook) bool {
			if item.ID == id && item.ManagedBy == "application" {
				managed = true
				return false
			}
			return item.ID == id && webhookActorCanManage(data, actor, item)
		})
		if managed {
			return fmt.Errorf("%w: uninstall the application to remove its webhook", errInvalid)
		}
		if len(data.Webhooks) == before {
			return errNotFound
		}
		data.WebhookFailureEvents = slices.DeleteFunc(data.WebhookFailureEvents, func(item domain.WebhookFailureEvent) bool {
			return item.WebhookID == id
		})
		appendAudit(data, "deleted", "webhook", id, nil)
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	_ = s.store.DeleteWebhookSigningSecret(r.Context(), store.WebhookSecretRef(id))
	_ = s.store.CancelWebhookDeliveries(r.Context(), workspaceKey(r), id)
	_ = s.store.DeleteWebhookDeliveries(r.Context(), workspaceKey(r), id)
	w.WriteHeader(http.StatusNoContent)
}

func webhookActorCanManage(data *domain.Bootstrap, actor domain.User, item domain.Webhook) bool {
	return webhookVisibleToBootstrap(data, item) && (strings.EqualFold(data.ViewerRole, "admin") || strings.EqualFold(data.ViewerRole, "owner") || item.CreatorID == actor.ID)
}

func webhookVisibleToBootstrap(data *domain.Bootstrap, item domain.Webhook) bool {
	// The audit log stream is managed from the audit log page only.
	if item.AuditLog {
		return false
	}
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
		// Webhooks are workspace settings. Admins manage every team's settings,
		// private ones included, without joining (the reference app: "As an admin you have
		// access to the team's settings"); reading the team's issues in Flow
		// still needs membership. Other members need to belong to the team.
		if !team.Private || admin {
			continue
		}
		if !slices.ContainsFunc(data.TeamMembers, func(member domain.TeamMember) bool { return member.TeamID == teamID && member.UserID == actorID }) {
			return false
		}
	}
	return true
}

// normalizeWebhookResourceTypes validates resource types (Flow ids or payload
// type names) and returns the Flow ids without duplicates.
func normalizeWebhookResourceTypes(values []string, allowApplication bool) ([]string, error) {
	result := []string{}
	for _, value := range values {
		id, ok := canonicalWebhookResourceType(value)
		if !ok || !allowApplication && slices.Contains(applicationOnlyWebhookResourceTypes, id) {
			return nil, fmt.Errorf("%w: unknown webhook resource type %q", errInvalid, value)
		}
		if !slices.Contains(result, id) {
			result = append(result, id)
		}
	}
	return result, nil
}

func applyWebhookInput(data *domain.Bootstrap, item *domain.Webhook, input webhookInput) error {
	if input.Name != nil {
		item.Name = strings.TrimSpace(*input.Name)
		if item.Name == "" || len(item.Name) > 200 {
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
	if input.ApplicationID != nil {
		value := strings.TrimSpace(*input.ApplicationID)
		if value != "" && !slices.ContainsFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == value }) {
			return errInvalid
		}
		item.ApplicationID = value
	}
	if input.ResourceTypes != nil {
		types, err := normalizeWebhookResourceTypes(*input.ResourceTypes, false)
		if err != nil {
			return err
		}
		item.ResourceTypes = types
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
	if input.PayloadVersion != nil {
		if *input.PayloadVersion != 1 && *input.PayloadVersion != domain.WebhookPayloadVersion {
			return fmt.Errorf("%w: payloadVersion must be 1 or %d", errInvalid, domain.WebhookPayloadVersion)
		}
		item.PayloadVersion = *input.PayloadVersion
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

// rotateWebhookSecret issues a new signing secret, shown once. Webhooks
// created before signing start signing their deliveries from now on.
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
		index := slices.IndexFunc(data.Webhooks, func(item domain.Webhook) bool {
			return item.ID == id && !item.AuditLog && item.ManagedBy != "application" && (admin || item.CreatorID == actor.ID)
		})
		if index < 0 {
			return errNotFound
		}
		data.Webhooks[index].SecretHash = secretHash(secret)
		data.Webhooks[index].SecretPrefix = secret[:min(len(secret), 17)]
		data.Webhooks[index].SecretRevokedAt = nil
		data.Webhooks[index].SigningEnabled = true
		data.Webhooks[index].UpdatedAt = time.Now().UTC()
		appendAudit(data, "secret_rotated", "webhook", id, nil)
		rotated = publicWebhook(data.Webhooks[index])
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNotFound, nil)
		return
	}
	if err := s.store.SetWebhookSigningSecret(r.Context(), store.WebhookSecretRef(id), workspaceKey(r), secret); err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, webhookWithSecret(rotated, secret))
}

// revokeWebhookSecret removes the signing secret; deliveries go out unsigned
// (Flow-Signature-Status: unsigned) until a new secret is rotated in.
func (s *server) revokeWebhookSecret(w http.ResponseWriter, r *http.Request) {
	actor := requestActor(s, r)
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "webhook.secret_revoked", id, nil, func(data *domain.Bootstrap) error {
		admin := strings.EqualFold(data.ViewerRole, "admin") || strings.EqualFold(data.ViewerRole, "owner")
		index := slices.IndexFunc(data.Webhooks, func(item domain.Webhook) bool {
			return item.ID == id && !item.AuditLog && item.ManagedBy != "application" && (admin || item.CreatorID == actor.ID)
		})
		if index < 0 {
			return errNotFound
		}
		now := time.Now().UTC()
		data.Webhooks[index].SecretHash = ""
		data.Webhooks[index].SecretPrefix = ""
		data.Webhooks[index].SecretRevokedAt = &now
		data.Webhooks[index].SigningEnabled = false
		data.Webhooks[index].UpdatedAt = now
		appendAudit(data, "secret_revoked", "webhook", id, nil)
		return nil
	})
	if err == nil {
		err = s.store.DeleteWebhookSigningSecret(r.Context(), store.WebhookSecretRef(id))
	}
	respondMutation(w, err, http.StatusNoContent, nil)
}

// listWebhookDeliveries pages through the delivery log, newest first.
// Query: status (pending, retrying, succeeded, failed, canceled), cursor, limit.
func (s *server) listWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	webhook, ok := s.manageableWebhook(r)
	if !ok {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	status := r.URL.Query().Get("status")
	if status != "" && !slices.Contains([]string{"pending", "delivering", "retrying", "succeeded", "failed", "canceled"}, status) {
		writeError(w, http.StatusBadRequest, "invalid status filter")
		return
	}
	page, err := s.store.WebhookDeliveries(r.Context(), store.WebhookDeliveryQuery{Workspace: workspaceKey(r), WebhookID: webhook.ID, Cursor: r.URL.Query().Get("cursor"), Status: status, Limit: limit})
	if err == store.ErrIssueQuery {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return
	}
	respondMutation(w, err, http.StatusOK, page)
}

func (s *server) getWebhookDelivery(w http.ResponseWriter, r *http.Request) {
	webhook, ok := s.manageableWebhook(r)
	if !ok {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	delivery, err := s.store.WebhookDelivery(r.Context(), workspaceKey(r), r.PathValue("deliveryId"))
	if err != nil || delivery.WebhookID != webhook.ID {
		writeError(w, http.StatusNotFound, "delivery not found")
		return
	}
	writeJSON(w, http.StatusOK, delivery)
}

// redeliverWebhookDelivery sends a delivery's payload again as a new
// delivery (with a new id, timestamp and signature) and waits for the first
// attempt.
func (s *server) redeliverWebhookDelivery(w http.ResponseWriter, r *http.Request) {
	webhook, ok := s.manageableWebhook(r)
	if !ok {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	original, err := s.store.WebhookDelivery(r.Context(), workspaceKey(r), r.PathValue("deliveryId"))
	if err != nil || original.WebhookID != webhook.ID {
		writeError(w, http.StatusNotFound, "delivery not found")
		return
	}
	if !webhook.Enabled {
		writeError(w, http.StatusConflict, "Enable the webhook before redelivering")
		return
	}
	now := time.Now().UTC()
	delivery := domain.WebhookDelivery{
		ID: webhookDeliveryID(webhook.ID, fmt.Sprintf("redeliver:%s:%s", original.ID, idgen.Stamp())), WorkspaceKey: workspaceKey(r), WebhookID: webhook.ID,
		SecretRef: webhookSecretRef(webhook), Kind: original.Kind, EventID: original.EventID, EventType: original.EventType, Action: original.Action,
		URL: webhook.URL, Payload: original.Payload, MaxAttempts: webhookMaxAttempts(), RedeliveryOf: original.ID, CreatedAt: now, NextAttemptAt: &now,
	}
	if delivery.Kind == domain.WebhookDeliveryKindTest {
		delivery.MaxAttempts = 1
	}
	s.deliverNow(w, r, delivery, now)
}

// testWebhook sends a test event and returns the result of the attempt.
func (s *server) testWebhook(w http.ResponseWriter, r *http.Request) {
	webhook, ok := s.manageableWebhook(r)
	if !ok {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	data := s.workspaceData(r)
	actor := requestActor(s, r)
	now := time.Now().UTC()
	eventID := idgen.New("evt_test_")
	body, _ := json.Marshal(map[string]any{
		"action": "test", "type": "WebhookTest", "createdAt": now.Format("2006-01-02T15:04:05.000Z07:00"),
		"actor":          webhookActorFor([]domain.User{actor}, actor.ID, s.appBaseURL()+"/"+url.PathEscape(data.Workspace.URLKey)),
		"data":           map[string]any{"webhookId": webhook.ID, "message": "This is a test event from Flow."},
		"organizationId": data.Workspace.ID, "webhookId": webhook.ID, "eventId": eventID,
	})
	delivery := domain.WebhookDelivery{
		ID: webhookDeliveryID(webhook.ID, eventID), WorkspaceKey: workspaceKey(r), WebhookID: webhook.ID, SecretRef: webhookSecretRef(webhook),
		Kind: domain.WebhookDeliveryKindTest, EventID: eventID, EventType: "WebhookTest", Action: "test", URL: webhook.URL, Payload: body,
		MaxAttempts: 1, CreatedAt: now, NextAttemptAt: &now,
	}
	s.deliverNow(w, r, delivery, now)
}

func (s *server) deliverNow(w http.ResponseWriter, r *http.Request, delivery domain.WebhookDelivery, now time.Time) {
	if _, err := s.store.EnqueueWebhookDelivery(r.Context(), delivery); err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webhookDeliveryTimeout+5*time.Second)
	defer cancel()
	completed, claimed := s.runWebhookDelivery(ctx, delivery.ID, now)
	if !claimed {
		completed, _ = s.store.WebhookDelivery(r.Context(), delivery.WorkspaceKey, delivery.ID)
	}
	completed.Payload = nil
	writeJSON(w, http.StatusOK, completed)
}
