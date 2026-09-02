package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type flowWebhookEnvelope struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	Action         string          `json:"action"`
	Data           json.RawMessage `json:"data"`
	PreviousValues json.RawMessage `json:"previousValues,omitempty"`
	CreatedAt      string          `json:"createdAt"`
}

func (s *server) dispatchWebhookEvent(workspace string, event domain.DomainEvent) {
	data, ok := s.store.BootstrapFor(workspace)
	if !ok || len(data.Webhooks) == 0 {
		return
	}
	resourceType := webhookResourceType(event.Type)
	action := webhookAction(event.Type)
	for _, webhook := range data.Webhooks {
		if !webhook.Enabled || webhook.URL == "" || !webhookResourceTypeAllowed(webhook, resourceType) || !webhookTeamAllowed(webhook, event.Payload, data, event.AggregateID) {
			continue
		}
		item := webhook
		go func() {
			if err := s.sendWebhookEvent(context.Background(), item, flowWebhookEnvelope{ID: event.ID, Type: resourceType, Action: action, Data: event.Payload, PreviousValues: event.PreviousValues, CreatedAt: event.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")}); err != nil {
				log.Printf("Flow webhook delivery id=%s event=%s: %v", item.ID, event.Type, err)
			}
		}()
	}
}

func webhookResourceType(eventType string) string {
	switch {
	case strings.HasPrefix(eventType, "issue."):
		return "issues"
	case strings.HasPrefix(eventType, "comment."):
		return "comments"
	case strings.HasPrefix(eventType, "attachment."):
		return "attachments"
	case strings.HasPrefix(eventType, "document."):
		return "documents"
	case strings.HasPrefix(eventType, "project.update_"):
		return "project_updates"
	case strings.HasPrefix(eventType, "project."):
		return "projects"
	case strings.HasPrefix(eventType, "cycle."):
		return "cycles"
	case strings.HasPrefix(eventType, "label."):
		return "labels"
	case strings.HasPrefix(eventType, "user."), strings.HasPrefix(eventType, "member."):
		return "users"
	case strings.HasPrefix(eventType, "initiative."):
		return "initiatives"
	case strings.HasPrefix(eventType, "customer_request."):
		return "customer_requests"
	case strings.HasPrefix(eventType, "customer."):
		return "customers"
	case strings.HasPrefix(eventType, "release."):
		return "releases"
	case strings.HasPrefix(eventType, "milestone."):
		return "milestones"
	case strings.HasPrefix(eventType, "relation."):
		return "relations"
	default:
		return ""
	}
}

func webhookAction(eventType string) string {
	if index := strings.IndexByte(eventType, '.'); index >= 0 && index+1 < len(eventType) {
		return eventType[index+1:]
	}
	return eventType
}

func webhookResourceTypeAllowed(webhook domain.Webhook, resourceType string) bool {
	if resourceType == "" {
		return false
	}
	if len(webhook.ResourceTypes) == 0 {
		return true
	}
	return containsString(webhook.ResourceTypes, resourceType)
}

func webhookTeamAllowed(webhook domain.Webhook, payload json.RawMessage, data domain.Bootstrap, aggregateID string) bool {
	if webhook.TeamRestriction != "selected" || len(webhook.TeamIDs) == 0 {
		return true
	}
	var value map[string]any
	if json.Unmarshal(payload, &value) != nil {
		return false
	}
	if teamID, ok := value["teamId"].(string); ok {
		return containsString(webhook.TeamIDs, teamID)
	}
	if teamIDs, ok := value["teamIds"].([]any); ok {
		for _, value := range teamIDs {
			if id, ok := value.(string); ok && containsString(webhook.TeamIDs, id) {
				return true
			}
		}
	}
	if aggregateID != "" {
		if raw, err := json.Marshal(data); err == nil {
			var root any
			if json.Unmarshal(raw, &root) == nil {
				if resource, ok := webhookObjectByID(root, aggregateID); ok {
					if id, ok := resource["teamId"].(string); ok {
						return containsString(webhook.TeamIDs, id)
					}
					if team, ok := resource["team"].(map[string]any); ok {
						if id, ok := team["id"].(string); ok {
							return containsString(webhook.TeamIDs, id)
						}
					}
					if ids, ok := resource["teamIds"].([]any); ok {
						for _, value := range ids {
							if id, ok := value.(string); ok && containsString(webhook.TeamIDs, id) {
								return true
							}
						}
					}
				}
			}
		}
	}
	return false
}

func webhookObjectByID(value any, id string) (map[string]any, bool) {
	switch item := value.(type) {
	case map[string]any:
		if candidate, ok := item["id"].(string); ok && candidate == id {
			return item, true
		}
		for _, child := range item {
			if found, ok := webhookObjectByID(child, id); ok {
				return found, true
			}
		}
	case []any:
		for _, child := range item {
			if found, ok := webhookObjectByID(child, id); ok {
				return found, true
			}
		}
	}
	return nil, false
}

func (s *server) sendWebhookEvent(ctx context.Context, webhook domain.Webhook, envelope flowWebhookEnvelope) error {
	body, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	if !integrationEndpointSafe(ctx, webhook.URL, s.authDisabled) {
		return errInvalid
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Flow-Event", envelope.Type+"."+envelope.Action)
	request.Header.Set("X-Flow-Delivery", envelope.ID)
	client := secureOutboundClient(10 * time.Second)
	if s.authDisabled && safeLocalDevelopmentURL(webhook.URL) {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return nil
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
