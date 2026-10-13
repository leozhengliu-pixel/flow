package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Outbound webhooks follow the contract of the reference app's webhooks: every delivery
// is a signed POST (hex HMAC-SHA256 of the raw body in Flow-Signature, the
// send time in Flow-Timestamp and in the body's webhookTimestamp), the
// receiver has 5 seconds to answer with a 2xx status, and a failed delivery
// is retried after 1 minute, 1 hour and 6 hours. Deliveries are rows in the
// webhook_deliveries table (see store/webhook_deliveries.go), so retries
// survive restarts and any instance can deliver them.

const (
	webhookFailureRetention = 50
	webhookDeliveryTimeout  = 5 * time.Second
	webhookDeliveryLease    = time.Minute
	// A webhook whose deliveries keep failing for this long (with no
	// successful delivery in between) is disabled when another delivery
	// exhausts its retries.
	webhookDisableAfter    = 24 * time.Hour
	webhookDeliveryHistory = 30 * 24 * time.Hour
	webhookUserAgent       = "Flow-Webhook"
)

// webhookRetrySchedule is the delay before each retry; a delivery makes
// len(webhookRetrySchedule)+1 attempts.
var webhookRetrySchedule = []time.Duration{time.Minute, time.Hour, 6 * time.Hour}

func webhookMaxAttempts() int { return len(webhookRetrySchedule) + 1 }

// Immediate first attempts run on a bounded pool; deliveries that do not get
// a slot are picked up by the delivery scheduler a few seconds later.
var (
	webhookDeliverySlots = make(chan struct{}, 16)
	webhookLastPrune     atomic.Int64
)

// webhookResourceTypes are the resource types a webhook can subscribe to,
// with the payload type name each one delivers.
var webhookResourceTypes = []struct{ ID, Type string }{
	{"issues", "Issue"},
	{"comments", "Comment"},
	{"attachments", "Attachment"},
	{"labels", "IssueLabel"},
	{"reactions", "Reaction"},
	{"projects", "Project"},
	{"project_updates", "ProjectUpdate"},
	{"milestones", "ProjectMilestone"},
	{"documents", "Document"},
	{"initiatives", "Initiative"},
	{"initiative_updates", "InitiativeUpdate"},
	{"cycles", "Cycle"},
	{"customers", "Customer"},
	{"customer_requests", "CustomerNeed"},
	{"users", "User"},
	{"issue_sla", "IssueSLA"},
	{"releases", "Release"},
	{"release_notes", "ReleaseNote"},
	{"relations", "IssueRelation"},
	// Application lifecycle and agent events (application webhooks).
	{"oauth_app", "OAuthApp"},
	{"permission_changes", "PermissionChange"},
	{"app_user_notifications", "AppUserNotification"},
	{"agent_session_events", "AgentSessionEvent"},
}

// workspaceWebhookResourceTypes are accepted on workspace webhooks; the
// application-only types are accepted on application webhooks.
var applicationOnlyWebhookResourceTypes = []string{"oauth_app", "permission_changes", "app_user_notifications", "agent_session_events"}

func webhookTypeName(resource string) string {
	for _, item := range webhookResourceTypes {
		if item.ID == resource {
			return item.Type
		}
	}
	return ""
}

// canonicalWebhookResourceType accepts Flow ids ("issues") and payload type
// names ("Issue", "IssueLabel"), as the reference app's webhookCreate does.
func canonicalWebhookResourceType(value string) (string, bool) {
	value = strings.TrimSpace(value)
	for _, item := range webhookResourceTypes {
		if strings.EqualFold(item.ID, value) || strings.EqualFold(item.Type, value) {
			return item.ID, true
		}
	}
	switch strings.ToLower(value) {
	case "customerneed", "customer_needs", "customerrequest":
		return "customer_requests", true
	case "projectmilestone", "project_milestones":
		return "milestones", true
	case "agentsession", "agent_sessions":
		return "agent_session_events", true
	case "inbox", "inbox_notifications":
		return "app_user_notifications", true
	case "oauthauthorization", "oauth_authorization", "oauthapprevoked":
		return "oauth_app", true
	}
	return "", false
}

type webhookActor struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// legacyWebhookEnvelope is the version 1 body, kept for webhooks created
// before signing; the actor, url, updatedFrom, organizationId, webhookId and
// eventType fields were added without changing existing ones.
type legacyWebhookEnvelope struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	Action         string          `json:"action"`
	Data           json.RawMessage `json:"data"`
	PreviousValues json.RawMessage `json:"previousValues,omitempty"`
	CreatedAt      string          `json:"createdAt"`
	Actor          *webhookActor   `json:"actor"`
	URL            string          `json:"url,omitempty"`
	UpdatedFrom    json.RawMessage `json:"updatedFrom,omitempty"`
	OrganizationID string          `json:"organizationId"`
	WebhookID      string          `json:"webhookId"`
	EventType      string          `json:"eventType"`
}

// flowWebhookEnvelope is the minimal envelope used by failure records.
type flowWebhookEnvelope struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Action string `json:"action"`
}

// webhookEvent is a classified domain event ready to be delivered.
type webhookEvent struct {
	resource  string // Flow resource type ("issues")
	legacy    string // version 1 resource type
	action    string // create | update | remove, or an event-specific action
	eventType string // Flow domain event ("issue.updated")
	eventID   string
	createdAt time.Time
	actor     *webhookActor
	data      json.RawMessage
	url       string
	updated   json.RawMessage
	teamIDs   []string
	extra     map[string]any
	payload   json.RawMessage // raw mutation input (version 1 data)
	previous  json.RawMessage
}

// classifyWebhookEvent maps a Flow domain event to the webhook resource type
// and the normalised action. Internal events (collaboration sync, drafts,
// suggestions, reminders) return "".
func classifyWebhookEvent(eventType string) (string, string) {
	index := strings.IndexByte(eventType, '.')
	if index < 0 {
		return "", ""
	}
	prefix, verb := eventType[:index], eventType[index+1:]
	if strings.HasSuffix(verb, "reaction_toggled") {
		return "reactions", "create"
	}
	if strings.Contains(verb, "thread_subscription") || strings.Contains(verb, "reminder_created") || strings.HasPrefix(verb, "draft_") ||
		strings.HasPrefix(verb, "suggestion") || slices.Contains([]string{"awareness", "join", "sync", "update", "conflict", "readonly", "preview_ready", "calendar_token_requested", "batch_updated"}, verb) {
		return "", ""
	}
	normal := func(verb string) string {
		switch {
		case verb == "created" || verb == "imported" || verb == "created_from_ask" || verb == "posted" || verb == "added" || verb == "commented":
			return "create"
		case verb == "deleted" || verb == "removed" || verb == "purged":
			return "remove"
		default:
			return "update"
		}
	}
	switch prefix {
	case "issue":
		switch verb {
		case "relation_added":
			return "relations", "create"
		case "relation_removed":
			return "relations", "remove"
		case "sla_breached":
			return "", ""
		}
		return "issues", normal(verb)
	case "comment":
		return "comments", normal(verb)
	case "attachment":
		return "attachments", normal(verb)
	case "document":
		if after, ok := strings.CutPrefix(verb, "comment_"); ok {
			return "comments", normal(after)
		}
		if strings.HasPrefix(verb, "permission") || strings.HasPrefix(verb, "subscriber") || strings.HasPrefix(verb, "attachment") || verb == "restored" || verb == "revision_restored" {
			return "documents", "update"
		}
		return "documents", normal(verb)
	case "project":
		switch {
		case strings.HasPrefix(verb, "update_"):
			after := strings.TrimPrefix(verb, "update_")
			if after == "commented" {
				return "comments", "create"
			}
			if strings.HasPrefix(after, "attachment") {
				return "project_updates", "update"
			}
			return "project_updates", normal(after)
		case strings.HasPrefix(verb, "milestone_"):
			return "milestones", normal(strings.TrimPrefix(verb, "milestone_"))
		case verb == "milestones_reordered":
			return "milestones", "update"
		case verb == "commented":
			return "comments", "create"
		case strings.HasPrefix(verb, "comment_"):
			after := strings.TrimPrefix(verb, "comment_")
			if strings.HasPrefix(after, "attachment") {
				return "comments", "update"
			}
			return "comments", normal(after)
		case verb == "created" || verb == "deleted":
			return "projects", normal(verb)
		}
		return "projects", "update"
	case "initiative":
		switch {
		case strings.HasPrefix(verb, "update_"):
			after := strings.TrimPrefix(verb, "update_")
			if after == "commented" {
				return "comments", "create"
			}
			if strings.HasPrefix(after, "attachment") {
				return "initiative_updates", "update"
			}
			return "initiative_updates", normal(after)
		case verb == "commented":
			return "comments", "create"
		case strings.HasPrefix(verb, "comment_"):
			return "comments", normal(strings.TrimPrefix(verb, "comment_"))
		case verb == "created" || verb == "deleted":
			return "initiatives", normal(verb)
		}
		return "initiatives", "update"
	case "cycle":
		if verb == "created" {
			return "cycles", "create"
		}
		if verb == "deleted" || verb == "team_cycles_deleted" {
			return "cycles", "remove"
		}
		return "cycles", "update"
	case "label":
		if verb == "merged" || verb == "moved_to_teams" {
			return "labels", "update"
		}
		return "labels", normal(verb)
	case "user", "member":
		return "users", normal(verb)
	case "customer":
		if verb == "merged" {
			return "customers", "update"
		}
		return "customers", normal(verb)
	case "customer_request":
		if strings.HasPrefix(verb, "attachment") {
			return "customer_requests", "update"
		}
		return "customer_requests", normal(verb)
	case "release":
		if after, ok := strings.CutPrefix(verb, "note_"); ok {
			return "release_notes", normal(after)
		}
		if strings.HasPrefix(verb, "ci_") {
			return "releases", "update"
		}
		return "releases", normal(verb)
	case "milestone":
		return "milestones", normal(verb)
	case "relation":
		return "relations", normal(verb)
	}
	return "", ""
}

// legacyWebhookResourceType is the version 1 mapping: by event prefix.
func legacyWebhookResourceType(eventType string) string {
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

// webhookResourceType is kept for callers that only need the version 1 type.
func webhookResourceType(eventType string) string { return legacyWebhookResourceType(eventType) }

func webhookAction(eventType string) string {
	if index := strings.IndexByte(eventType, '.'); index >= 0 && index+1 < len(eventType) {
		return eventType[index+1:]
	}
	return eventType
}

func webhookPayloadVersion(webhook domain.Webhook) int {
	if webhook.PayloadVersion <= 1 {
		return 1
	}
	return webhook.PayloadVersion
}

func webhookResourceTypeAllowed(webhook domain.Webhook, resourceType string) bool {
	if resourceType == "" {
		return false
	}
	if len(webhook.ResourceTypes) == 0 {
		return !slices.Contains(applicationOnlyWebhookResourceTypes, resourceType)
	}
	return containsString(webhook.ResourceTypes, resourceType)
}

// webhookMatches reports whether a webhook subscribes to the event.
func webhookMatches(webhook domain.Webhook, event webhookEvent) bool {
	if webhookPayloadVersion(webhook) == 1 {
		// Version 1 keeps its subscriptions by event prefix, and receives the
		// event types it could not receive before (reactions, SLAs).
		if event.legacy != "" && event.resource != "reactions" {
			return webhookResourceTypeAllowed(webhook, event.legacy)
		}
	}
	return webhookResourceTypeAllowed(webhook, event.resource)
}

func (s *server) appBaseURL() string {
	origin := strings.TrimSpace(s.allowedOrigin)
	if origin == "*" {
		origin = ""
	}
	return strings.TrimRight(firstNonEmpty(origin, os.Getenv("FLOW_APP_URL")), "/")
}

func (s *server) dispatchWebhookEvent(workspace string, event domain.DomainEvent) {
	if applicationWebhookLifecycleEvent(event.Type) {
		go s.reconcileApplicationWebhooks(context.Background(), workspace)
	}
	if strings.HasPrefix(event.Type, "issue.") && len(event.PreviousValues) > 0 {
		s.dispatchAppAssignmentNotifications(workspace, event, snapshotEntity(event))
	}
	resource, action := classifyWebhookEvent(event.Type)
	legacy := legacyWebhookResourceType(event.Type)
	if resource == "" && legacy == "" {
		return
	}
	// Runs synchronously after every mutation: check the (small) webhook list
	// before reading anything else.
	hooks, ok := s.store.WorkspaceMetadataFields(workspace, "webhooks")
	if !ok || !slices.ContainsFunc(hooks.Webhooks, func(webhook domain.Webhook) bool {
		return !webhook.AuditLog && webhook.Enabled && webhook.URL != "" && webhookMatches(webhook, webhookEvent{resource: resource, legacy: legacy})
	}) {
		return
	}
	ctx := context.Background()
	classified := s.describeWebhookEvent(ctx, workspace, event, resource, action, legacy)
	s.enqueueWebhookEvent(ctx, workspace, hooks.Webhooks, classified)
}

// enqueueWebhookEvent queues the event for every matching webhook and starts
// the first attempts.
func (s *server) enqueueWebhookEvent(ctx context.Context, workspace string, hooks []domain.Webhook, event webhookEvent) {
	var full *domain.Bootstrap
	metadata, _ := s.store.WorkspaceMetadataFields(workspace, "users")
	organizationID := metadata.Workspace.ID
	for _, webhook := range hooks {
		if webhook.AuditLog || !webhook.Enabled || webhook.URL == "" || !webhookMatches(webhook, event) {
			continue
		}
		if webhook.TeamRestriction == "selected" && len(webhook.TeamIDs) > 0 {
			allowed := false
			if len(event.teamIDs) > 0 {
				allowed = slices.ContainsFunc(event.teamIDs, func(id string) bool { return containsString(webhook.TeamIDs, id) })
			} else {
				if full == nil {
					data, _ := s.store.WorkspaceMetadata(workspace)
					full = &data
				}
				allowed = webhookTeamAllowed(webhook, event.payload, *full, event.eventID)
			}
			if !allowed {
				continue
			}
		}
		body, err := webhookEnvelopeBody(webhook, organizationID, event)
		if err != nil {
			log.Printf("Flow webhook envelope id=%s event=%s: %v", webhook.ID, event.eventType, err)
			continue
		}
		s.queueWebhookDelivery(ctx, workspace, webhook, domain.WebhookDelivery{
			ID: webhookDeliveryID(webhook.ID, event.eventID), Kind: domain.WebhookDeliveryKindEvent, EventID: event.eventID,
			EventType: webhookTypeName(event.resource), Action: event.action, Payload: body,
		})
	}
}

// queueWebhookDelivery fills the delivery from the webhook, stores it and
// starts its first attempt.
func (s *server) queueWebhookDelivery(ctx context.Context, workspace string, webhook domain.Webhook, delivery domain.WebhookDelivery) bool {
	delivery.WorkspaceKey, delivery.WebhookID, delivery.URL = workspace, webhook.ID, webhook.URL
	if delivery.SecretRef == "" {
		delivery.SecretRef = webhookSecretRef(webhook)
	}
	if delivery.MaxAttempts == 0 {
		delivery.MaxAttempts = webhookMaxAttempts()
	}
	inserted, err := s.store.EnqueueWebhookDelivery(ctx, delivery)
	if err != nil {
		log.Printf("Flow webhook enqueue id=%s event=%s: %v", webhook.ID, delivery.EventID, err)
		return false
	}
	if inserted {
		s.kickWebhookDelivery(delivery.ID)
	}
	return inserted
}

func webhookSecretRef(webhook domain.Webhook) string {
	if webhook.ManagedBy == "application" && webhook.ApplicationClientID != "" {
		return store.ApplicationWebhookSecretRef(webhook.ApplicationClientID)
	}
	return store.WebhookSecretRef(webhook.ID)
}

// webhookDeliveryID is stable per webhook and event, which makes enqueueing
// idempotent across instances and repeated sweeps.
func webhookDeliveryID(webhookID, eventKey string) string {
	sum := sha256.Sum256([]byte(webhookID + "\x00" + eventKey))
	return "whd_" + hex.EncodeToString(sum[:16])
}

func webhookEnvelopeBody(webhook domain.Webhook, organizationID string, event webhookEvent) (json.RawMessage, error) {
	createdAt := event.createdAt.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	if webhookPayloadVersion(webhook) == 1 && event.legacy != "" && event.resource != "reactions" {
		data := event.payload
		if len(data) == 0 {
			data = json.RawMessage(`null`)
		}
		return json.Marshal(legacyWebhookEnvelope{
			ID: event.eventID, Type: event.legacy, Action: webhookAction(event.eventType), Data: data, PreviousValues: event.previous,
			CreatedAt: event.createdAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), Actor: event.actor, URL: event.url,
			UpdatedFrom: event.updated, OrganizationID: organizationID, WebhookID: webhook.ID, EventType: event.eventType,
		})
	}
	data := event.data
	if len(data) == 0 {
		data = json.RawMessage(`null`)
	}
	body := map[string]any{
		"action": event.action, "type": webhookTypeName(event.resource), "actor": event.actor, "createdAt": createdAt, "data": data,
		"organizationId": organizationID, "webhookId": webhook.ID, "eventId": event.eventID,
	}
	if event.eventType != "" {
		body["eventType"] = event.eventType
	}
	if event.url != "" {
		body["url"] = event.url
	}
	if len(event.updated) > 0 && event.action == "update" {
		body["updatedFrom"] = event.updated
	}
	for key, value := range event.extra {
		body[key] = value
	}
	return json.Marshal(body)
}

// describeWebhookEvent builds the subject entity, URL, actor and teams.
func (s *server) describeWebhookEvent(ctx context.Context, workspace string, event domain.DomainEvent, resource, action, legacy string) webhookEvent {
	result := webhookEvent{resource: resource, legacy: legacy, action: action, eventType: event.Type, eventID: event.ID, createdAt: event.CreatedAt, payload: event.Payload, previous: event.PreviousValues, updated: event.PreviousValues}
	if result.createdAt.IsZero() {
		result.createdAt = time.Now().UTC()
	}
	metadata, _ := s.store.WorkspaceMetadataFields(workspace, "users")
	base := s.appBaseURL()
	root := base + "/" + url.PathEscape(metadata.Workspace.URLKey)
	result.actor = webhookActorFor(metadata.Users, event.ActorID, root)
	var input map[string]any
	_ = json.Unmarshal(event.Payload, &input)
	var entity map[string]any
	deleted := false
	if len(event.Snapshot) > 0 {
		var snapshot struct {
			Entity  json.RawMessage `json:"entity"`
			Deleted bool            `json:"deleted"`
		}
		if json.Unmarshal(event.Snapshot, &snapshot) == nil {
			_ = json.Unmarshal(snapshot.Entity, &entity)
			deleted = snapshot.Deleted
		}
	}
	if entity == nil && len(event.PreviousValues) > 0 && action == "remove" {
		_ = json.Unmarshal(event.PreviousValues, &entity)
	}
	var issue *domain.Issue
	loadIssue := func(id string) *domain.Issue {
		if issue != nil || id == "" {
			return issue
		}
		if record, err := s.store.IssueRecord(ctx, workspace, id); err == nil {
			issue = &record
		}
		return issue
	}
	issueURL := func(item *domain.Issue) string {
		if item == nil || base == "" {
			return ""
		}
		return root + "/issue/" + url.PathEscape(item.Identifier) + "/" + url.PathEscape(routeSlug(item.Title))
	}
	activityValue := func(issueID, key string) string {
		activities, err := s.store.RecentResourceActivities(ctx, workspace, issueID, 10)
		if err != nil {
			return ""
		}
		for _, activity := range activities {
			if activity.Type == event.Type && activity.Metadata[key] != "" {
				return activity.Metadata[key]
			}
		}
		return ""
	}
	setData := func(value any) {
		if raw, err := json.Marshal(value); err == nil {
			result.data = raw
		}
	}
	switch {
	case resource == "reactions":
		emoji, _ := input["emoji"].(string)
		data := map[string]any{"emoji": emoji, "userId": event.ActorID}
		owner := strings.TrimSuffix(strings.TrimSuffix(event.Type, "reaction_toggled"), "_")
		switch owner {
		case "issue":
			data["issueId"] = event.AggregateID
		case "comment":
			data["issueId"] = event.AggregateID
			if id, _ := input["commentId"].(string); id != "" {
				data["commentId"] = id
			}
		case "project.update":
			data["projectUpdateId"], data["projectId"] = input["updateId"], event.AggregateID
		case "initiative.update":
			data["initiativeUpdateId"], data["initiativeId"] = input["updateId"], event.AggregateID
		default:
			data["parentType"], data["parentId"] = owner, event.AggregateID
			if id, _ := input["commentId"].(string); id != "" {
				data["commentId"] = id
			}
		}
		key := event.AggregateID
		if id, _ := data["commentId"].(string); id != "" {
			key = id
		}
		data["id"] = key + ":" + emoji + ":" + event.ActorID
		if added, ok := input["added"].(bool); ok && !added {
			result.action = "remove"
		}
		if strings.HasPrefix(event.Type, "issue.") || strings.HasPrefix(event.Type, "comment.") {
			if item := loadIssue(event.AggregateID); item != nil {
				result.teamIDs, result.url = []string{item.Team.ID}, issueURL(item)
			}
		}
		setData(data)
	case resource == "issues" || strings.HasPrefix(event.Type, "issue.") && resource != "reactions" && resource != "relations":
		if entity != nil && entity["identifier"] != nil {
			setData(entity)
			if team, ok := entity["team"].(map[string]any); ok {
				if id, _ := team["id"].(string); id != "" {
					result.teamIDs = []string{id}
				}
			}
			if identifier, _ := entity["identifier"].(string); identifier != "" && base != "" {
				title, _ := entity["title"].(string)
				result.url = root + "/issue/" + url.PathEscape(identifier) + "/" + url.PathEscape(routeSlug(title))
			}
		} else if item := loadIssue(event.AggregateID); item != nil {
			setData(item)
			result.teamIDs, result.url = []string{item.Team.ID}, issueURL(item)
		}
	case strings.HasPrefix(event.Type, "comment."):
		item := loadIssue(event.AggregateID)
		commentID, _ := input["commentId"].(string)
		if commentID == "" {
			commentID = activityValue(event.AggregateID, "commentId")
		}
		data := map[string]any{"id": commentID, "issueId": event.AggregateID}
		if commentID != "" && action != "remove" {
			if comment, err := s.store.ResourceComment(ctx, workspace, event.AggregateID, commentID); err == nil {
				if raw, err := json.Marshal(comment); err == nil {
					_ = json.Unmarshal(raw, &data)
				}
				data["issueId"], data["userId"] = event.AggregateID, comment.User.ID
			}
		}
		setData(data)
		if item != nil {
			result.teamIDs = []string{item.Team.ID}
			if result.url = issueURL(item); result.url != "" && commentID != "" {
				result.url += "#comment-" + commentID
			}
		}
	case strings.HasPrefix(event.Type, "attachment."):
		item := loadIssue(event.AggregateID)
		attachmentID, _ := input["attachmentId"].(string)
		if attachmentID == "" {
			attachmentID = activityValue(event.AggregateID, "attachmentId")
		}
		data := map[string]any{"id": attachmentID, "issueId": event.AggregateID}
		if item != nil {
			for _, attachment := range item.Attachments {
				if attachment.ID == attachmentID {
					if raw, err := json.Marshal(attachment); err == nil {
						_ = json.Unmarshal(raw, &data)
					}
					data["issueId"] = event.AggregateID
				}
			}
			result.teamIDs, result.url = []string{item.Team.ID}, issueURL(item)
		}
		setData(data)
	case resource == "relations":
		data := map[string]any{}
		for key, value := range input {
			data[key] = value
		}
		if strings.HasPrefix(event.Type, "issue.") {
			data["issueId"] = event.AggregateID
			if item := loadIssue(event.AggregateID); item != nil {
				result.teamIDs, result.url = []string{item.Team.ID}, issueURL(item)
			}
		}
		setData(data)
	default:
		if entity != nil {
			setData(entity)
			result.teamIDs = webhookEntityTeams(entity)
			result.url = webhookEntityURL(root, resource, entity)
		} else {
			setData(input)
			result.teamIDs = webhookEntityTeams(input)
		}
		if base == "" {
			result.url = ""
		}
	}
	if deleted && result.action == "update" {
		result.action = "remove"
	}
	return result
}

// snapshotEntity decodes the post-mutation entity carried by the event.
func snapshotEntity(event domain.DomainEvent) map[string]any {
	var snapshot struct {
		Entity map[string]any `json:"entity"`
	}
	if len(event.Snapshot) == 0 || json.Unmarshal(event.Snapshot, &snapshot) != nil {
		return nil
	}
	return snapshot.Entity
}

func webhookActorFor(users []domain.User, id, root string) *webhookActor {
	if id == "" {
		return nil
	}
	for _, user := range users {
		if user.ID != id {
			continue
		}
		actor := &webhookActor{ID: user.ID, Type: "user", Name: firstNonEmpty(user.DisplayName, user.Name), Email: user.Email}
		if user.App {
			actor.Type, actor.Email = "application", ""
		}
		if user.Username != "" && !strings.HasPrefix(root, "/") {
			actor.URL = root + "/profiles/" + url.PathEscape(user.Username)
		}
		return actor
	}
	return &webhookActor{ID: id, Type: "user"}
}

func webhookEntityTeams(entity map[string]any) []string {
	if id, ok := entity["teamId"].(string); ok && id != "" {
		return []string{id}
	}
	if team, ok := entity["team"].(map[string]any); ok {
		if id, _ := team["id"].(string); id != "" {
			return []string{id}
		}
	}
	ids := []string{}
	if values, ok := entity["teamIds"].([]any); ok {
		for _, value := range values {
			if id, ok := value.(string); ok && id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func webhookEntityURL(root, resource string, entity map[string]any) string {
	text := func(key string) string { value, _ := entity[key].(string); return value }
	switch resource {
	case "projects":
		if slug := text("slugId"); slug != "" {
			return root + "/project/" + url.PathEscape(slug) + "/overview"
		}
	case "documents":
		if slug := text("slugId"); slug != "" {
			return root + "/document/" + url.PathEscape(slug)
		}
	case "initiatives":
		if slug := text("slugId"); slug != "" {
			return root + "/initiative/" + url.PathEscape(slug) + "/overview"
		}
	case "customers":
		if id := text("id"); id != "" && len(id) >= 12 {
			return root + "/customer/" + url.PathEscape(routeSlug(text("name"))) + "-" + id[len(id)-12:]
		}
	case "users":
		if username := text("username"); username != "" {
			return root + "/profiles/" + url.PathEscape(username)
		}
	}
	return ""
}

// webhookTeamAllowed is the fallback team check for events whose subject
// does not name a team directly: it finds the aggregate in the workspace.
func webhookTeamAllowed(webhook domain.Webhook, payload json.RawMessage, data domain.Bootstrap, aggregateID string) bool {
	if webhook.TeamRestriction != "selected" || len(webhook.TeamIDs) == 0 {
		return true
	}
	var value map[string]any
	if json.Unmarshal(payload, &value) == nil {
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
	}
	if aggregateID != "" {
		if raw, err := json.Marshal(data); err == nil {
			var root any
			if json.Unmarshal(raw, &root) == nil {
				if resource, ok := webhookObjectByID(root, aggregateID); ok {
					for _, id := range webhookEntityTeams(resource) {
						if containsString(webhook.TeamIDs, id) {
							return true
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

// kickWebhookDelivery makes the first attempt right away when a slot is free.
func (s *server) kickWebhookDelivery(id string) {
	select {
	case webhookDeliverySlots <- struct{}{}:
	default:
		return
	}
	go func() {
		defer func() { <-webhookDeliverySlots }()
		ctx, cancel := context.WithTimeout(s.store.WorkerContext(), webhookDeliveryLease)
		defer cancel()
		s.runWebhookDelivery(ctx, id, time.Now().UTC())
	}()
}

// processDueWebhookDeliveries delivers queued and retrying deliveries. It runs
// on every delivery scheduler tick.
func (s *server) processDueWebhookDeliveries(ctx context.Context, now time.Time) {
	ids, err := s.store.DueWebhookDeliveries(ctx, now, 50)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("Webhook deliveries: %v", err)
		}
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		s.runWebhookDelivery(ctx, id, now)
	}
	if last := webhookLastPrune.Load(); now.Unix()-last >= 3600 && webhookLastPrune.CompareAndSwap(last, now.Unix()) {
		if _, err := s.store.PruneWebhookDeliveries(ctx, now.Add(-webhookDeliveryHistory)); err != nil {
			log.Printf("Webhook delivery prune: %v", err)
		}
	}
}

// runWebhookDelivery claims a delivery and makes one attempt.
func (s *server) runWebhookDelivery(ctx context.Context, id string, now time.Time) (domain.WebhookDelivery, bool) {
	delivery, claimed, err := s.store.ClaimWebhookDelivery(ctx, id, now, webhookDeliveryLease)
	if err != nil || !claimed {
		return delivery, false
	}
	completed, err := s.attemptWebhookDelivery(ctx, delivery, now)
	if err != nil && ctx.Err() == nil {
		log.Printf("Flow webhook delivery id=%s: %v", id, err)
	}
	return completed, true
}

// attemptWebhookDelivery sends a claimed delivery once and records the result.
func (s *server) attemptWebhookDelivery(ctx context.Context, delivery domain.WebhookDelivery, now time.Time) (domain.WebhookDelivery, error) {
	var webhook *domain.Webhook
	if delivery.Kind != domain.WebhookDeliveryKindLifecycle {
		hooks, _ := s.store.WorkspaceMetadataFields(delivery.WorkspaceKey, "webhooks")
		index := slices.IndexFunc(hooks.Webhooks, func(item domain.Webhook) bool { return item.ID == delivery.WebhookID })
		if index < 0 || !hooks.Webhooks[index].Enabled && delivery.Kind != domain.WebhookDeliveryKindTest {
			return s.store.CompleteWebhookDeliveryAttempt(ctx, delivery, store.WebhookDeliveryOutcome{Canceled: true, At: now})
		}
		item := hooks.Webhooks[index]
		webhook = &item
		// A webhook's URL can change while a retry waits.
		delivery.URL = item.URL
	}
	started := time.Now()
	status, body, sendErr := s.sendWebhookRequest(ctx, delivery, now)
	outcome := store.WebhookDeliveryOutcome{Succeeded: sendErr == nil, ResponseStatus: status, ResponseBody: body, Duration: time.Since(started), At: now}
	if sendErr != nil {
		outcome.Error = sendErr.Error()
		if delivery.Attempt+1 < delivery.MaxAttempts {
			index := min(delivery.Attempt, len(webhookRetrySchedule)-1)
			next := now.Add(webhookRetrySchedule[index])
			outcome.NextAttemptAt = &next
		}
	}
	completed, err := s.store.CompleteWebhookDeliveryAttempt(ctx, delivery, outcome)
	if err != nil {
		return completed, err
	}
	if webhook != nil && delivery.Kind == domain.WebhookDeliveryKindEvent {
		s.recordWebhookHealth(delivery.WorkspaceKey, *webhook, completed, now)
	}
	return completed, nil
}

// sendWebhookRequest posts the delivery with a fresh timestamp and signature.
func (s *server) sendWebhookRequest(ctx context.Context, delivery domain.WebhookDelivery, now time.Time) (int, string, error) {
	timestamp := now.UnixMilli()
	body := webhookBodyWithTimestamp(delivery.Payload, timestamp)
	if !integrationEndpointSafe(ctx, delivery.URL, s.authDisabled) {
		return 0, "", errors.New("endpoint not allowed: webhooks must use a public HTTPS URL")
	}
	secret, _, err := s.store.WebhookSigningSecret(ctx, delivery.SecretRef)
	if err != nil {
		return 0, "", fmt.Errorf("signing secret unavailable: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, webhookDeliveryTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, delivery.URL, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	setWebhookHeaders(request.Header, delivery, timestamp, secret, body)
	client := secureOutboundClient(webhookDeliveryTimeout)
	if s.authDisabled && safeLocalDevelopmentURL(delivery.URL) {
		client = &http.Client{Timeout: webhookDeliveryTimeout}
	}
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Client.Timeout") {
			return 0, "", fmt.Errorf("timed out after %s", webhookDeliveryTimeout)
		}
		return 0, "", err
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, domain.WebhookDeliveryBodyLimit))
	text := strings.TrimSpace(string(payload))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, text, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return response.StatusCode, text, nil
}

// setWebhookHeaders writes Flow's webhook headers. The X-Flow-* names are the
// headers sent before signing was added and stay for existing receivers.
func setWebhookHeaders(header http.Header, delivery domain.WebhookDelivery, timestamp int64, secret string, body []byte) {
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Set("Accept-Charset", "utf-8")
	header.Set("User-Agent", webhookUserAgent)
	header.Set("Flow-Delivery", delivery.ID)
	header.Set("Flow-Event", delivery.EventType)
	header.Set("Flow-Timestamp", strconv.FormatInt(timestamp, 10))
	header.Set("Flow-Attempt", strconv.Itoa(delivery.Attempt+1))
	header.Set("X-Flow-Delivery", delivery.ID)
	header.Set("X-Flow-Event", delivery.EventType)
	if secret == "" {
		header.Set("Flow-Signature-Status", "unsigned")
		return
	}
	signature := webhookSignature(secret, body)
	header.Set("Flow-Signature", signature)
	header.Set("X-Flow-Signature", signature)
}

// webhookSignature is the hex HMAC-SHA256 of the raw body.
func webhookSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// verifyWebhookSignature checks a signature and the signed webhookTimestamp
// (within tolerance of now), as receivers should.
func verifyWebhookSignature(secret string, body []byte, signature string, now time.Time, tolerance time.Duration) bool {
	expected, err := hex.DecodeString(webhookSignature(secret, body))
	if err != nil {
		return false
	}
	given, err := hex.DecodeString(strings.TrimSpace(signature))
	if err != nil || !hmac.Equal(expected, given) {
		return false
	}
	var envelope struct {
		WebhookTimestamp int64 `json:"webhookTimestamp"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.WebhookTimestamp == 0 {
		return false
	}
	delta := now.Sub(time.UnixMilli(envelope.WebhookTimestamp))
	return delta <= tolerance && delta >= -tolerance
}

// webhookBodyWithTimestamp appends webhookTimestamp to the stored body.
func webhookBodyWithTimestamp(payload json.RawMessage, timestamp int64) []byte {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return payload
	}
	trimmed = bytes.TrimSpace(trimmed[:len(trimmed)-1])
	body := make([]byte, 0, len(trimmed)+40)
	body = append(body, trimmed...)
	if len(trimmed) > 1 {
		body = append(body, ',')
	}
	body = append(body, `"webhookTimestamp":`...)
	body = strconv.AppendInt(body, timestamp, 10)
	return append(body, '}')
}

// recordWebhookHealth tracks a webhook's failing period and disables it when
// deliveries keep failing. It only writes when the state changes.
func (s *server) recordWebhookHealth(workspace string, webhook domain.Webhook, delivery domain.WebhookDelivery, now time.Time) {
	switch {
	case delivery.Status == domain.WebhookDeliverySucceeded && webhook.FailingSince != nil:
		s.updateWebhookHealth(workspace, webhook.ID, func(item *domain.Webhook) bool {
			if item.FailingSince == nil {
				return false
			}
			item.FailingSince = nil
			return true
		})
	case delivery.Status == domain.WebhookDeliveryRetrying && webhook.FailingSince == nil:
		s.updateWebhookHealth(workspace, webhook.ID, func(item *domain.Webhook) bool {
			if item.FailingSince != nil {
				return false
			}
			item.FailingSince = &now
			return true
		})
	case delivery.Status == domain.WebhookDeliveryFailed:
		if webhook.FailingSince == nil {
			s.updateWebhookHealth(workspace, webhook.ID, func(item *domain.Webhook) bool {
				if item.FailingSince != nil {
					return false
				}
				item.FailingSince = &now
				return true
			})
			return
		}
		if now.Sub(*webhook.FailingSince) >= webhookDisableAfter {
			s.disableFailingWebhook(workspace, webhook.ID, now)
		}
	}
}

func (s *server) updateWebhookHealth(workspace, webhookID string, apply func(*domain.Webhook) bool) {
	ctx := store.WithMetadataFields(context.Background(), "webhooks")
	err := s.store.MutateWorkspace(ctx, workspace, "webhook.updated", webhookID, map[string]any{"health": true}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Webhooks, func(item domain.Webhook) bool { return item.ID == webhookID })
		if index < 0 || !apply(&data.Webhooks[index]) {
			return store.ErrNoMutation
		}
		return nil
	})
	if err != nil {
		log.Printf("Flow webhook health workspace=%s id=%s: %v", workspace, webhookID, err)
	}
}

// disableFailingWebhook turns a webhook off after sustained failures and
// tells its creator in the inbox (with the usual email/desktop deliveries).
func (s *server) disableFailingWebhook(workspace, webhookID string, now time.Time) {
	scope := store.WithMutationScope(context.Background(), store.MutationScope{Resources: []string{webhookID}})
	var disabled bool
	err := s.store.MutateWorkspace(scope, workspace, "webhook.auto_disabled", webhookID, map[string]any{"reason": "deliveryFailures"}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Webhooks, func(item domain.Webhook) bool { return item.ID == webhookID })
		if index < 0 || !data.Webhooks[index].Enabled {
			return store.ErrNoMutation
		}
		item := &data.Webhooks[index]
		item.Enabled, item.DisabledReason, item.DisabledAt, item.UpdatedAt = false, "deliveryFailures", &now, now
		appendAudit(data, "disabled", "webhook", item.ID, map[string]any{"reason": "deliveryFailures", "failingSince": item.FailingSince})
		recipient := userByID(data, item.CreatorID)
		if recipient != nil && recipient.Active {
			notification := domain.Notification{
				ID: "notification_webhook_disabled_" + item.ID + "_" + strconv.FormatInt(now.Unix(), 10), RecipientID: recipient.ID, Type: "integration",
				SourceType: "webhook", SourceID: item.ID, Actor: *recipient, Category: "integrations", GroupKey: "webhook:" + item.ID,
				OccurrenceCount: 1, LatestActorIDs: []string{recipient.ID},
				Title:     "Webhook disabled: " + item.Name,
				Text:      "Deliveries to " + item.URL + " kept failing, so the webhook was disabled. Fix the endpoint, then re-enable it in Settings › API › Webhooks.",
				CreatedAt: now, UpdatedAt: now,
			}
			data.Notifications = append(data.Notifications, notification)
			preferences, ok := data.NotificationPreferences[recipient.ID]
			if !ok {
				preferences = defaultPreferences(recipient.ID)
			}
			enqueueNotificationDeliveries(data, notification, preferences)
		}
		disabled = true
		return nil
	})
	if err != nil {
		log.Printf("Flow webhook disable workspace=%s id=%s: %v", workspace, webhookID, err)
		return
	}
	if disabled {
		_ = s.store.CancelWebhookDeliveries(context.Background(), workspace, webhookID)
		s.dispatchNotificationEmails(context.Background(), workspace)
	}
}

// recordWebhookFailure stores a failure of the audit log stream.
func (s *server) recordWebhookFailure(workspace string, webhook domain.Webhook, envelope flowWebhookEnvelope, httpStatus int, responseOrError string) {
	if strings.TrimSpace(responseOrError) == "" {
		responseOrError = "delivery failed"
	}
	var statusPtr *int
	if httpStatus > 0 {
		status := httpStatus
		statusPtr = &status
	}
	now := time.Now().UTC()
	failure := domain.WebhookFailureEvent{
		ID:              fmt.Sprintf("whfail_%d", now.UnixNano()),
		WebhookID:       webhook.ID,
		ExecutionID:     envelope.ID,
		URL:             webhook.URL,
		HTTPStatus:      statusPtr,
		ResponseOrError: truncateWebhookFailureText(responseOrError, 4096),
		CreatedAt:       now,
	}
	if err := s.store.MutateWorkspace(context.Background(), workspace, "webhook.delivery_failed", webhook.ID, map[string]any{
		"executionId": failure.ExecutionID,
		"httpStatus":  httpStatus,
	}, func(data *domain.Bootstrap) error {
		if !slices.ContainsFunc(data.Webhooks, func(item domain.Webhook) bool { return item.ID == webhook.ID }) {
			return nil
		}
		data.WebhookFailureEvents = append(data.WebhookFailureEvents, failure)
		data.WebhookFailureEvents = trimWebhookFailures(data.WebhookFailureEvents, webhook.ID, webhookFailureRetention)
		return nil
	}); err != nil {
		log.Printf("Flow webhook failure persist id=%s: %v", webhook.ID, err)
	}
}

func trimWebhookFailures(events []domain.WebhookFailureEvent, webhookID string, limit int) []domain.WebhookFailureEvent {
	if limit <= 0 || len(events) == 0 {
		return events
	}
	kept := make([]domain.WebhookFailureEvent, 0, len(events))
	count := 0
	for i := len(events) - 1; i >= 0; i-- {
		item := events[i]
		if item.WebhookID == webhookID {
			if count >= limit {
				continue
			}
			count++
		}
		kept = append(kept, item)
	}
	slices.Reverse(kept)
	return kept
}

func truncateWebhookFailureText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
