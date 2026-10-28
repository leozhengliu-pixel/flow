package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/publicapi"
	"flow/api/internal/store"
)

// Application webhooks: an OAuth application's developer configures one
// webhook URL, its resource types and a signing secret. Every workspace that
// installs the application (actor=app) or authorizes it (actor=user) gets a
// managed webhook (ManagedBy "application") that delivers those events to the
// URL, scoped to the teams the application can access. As in the reference app, an
// installation authorized before the application had a webhook gets none
// until it is authorized again (ApplicationInstallation.WithoutRealtime). Uninstalling or
// revoking the application removes the managed webhook and sends an OAuthApp
// "revoked" event; a change of team access sends a PermissionChange event;
// inbox notifications of the application's user are sent as
// AppUserNotification events.

type applicationWebhookInput = publicapi.ApplicationWebhookInput

type applicationWebhookSettings = publicapi.ApplicationWebhookSettings

func (s *server) applicationWebhookSettings(ctx context.Context, app domain.OAuthApplication) applicationWebhookSettings {
	_, signing, _ := s.store.WebhookSigningSecret(ctx, store.ApplicationWebhookSecretRef(app.ClientID))
	settings := applicationWebhookSettings{ApplicationID: app.ID, ClientID: app.ClientID, Enabled: app.WebhookEnabled, URL: app.WebhookURL, ResourceTypes: app.WebhookResourceTypes, SecretPrefix: app.WebhookSecretPrefix, SigningEnabled: signing}
	if settings.ResourceTypes == nil {
		settings.ResourceTypes = []string{}
	}
	for _, key := range s.store.WorkspaceKeys() {
		hooks, _ := s.store.WorkspaceMetadataFields(key, "webhooks")
		if slices.ContainsFunc(hooks.Webhooks, func(item domain.Webhook) bool {
			return item.ManagedBy == "application" && item.ApplicationClientID == app.ClientID
		}) {
			settings.Installations++
		}
	}
	return settings
}

func (s *server) getApplicationWebhook(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	index := slices.IndexFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == r.PathValue("id") })
	if index < 0 {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}
	writeJSON(w, http.StatusOK, s.applicationWebhookSettings(r.Context(), data.OAuthApplications[index]))
}

// updateApplicationWebhook saves the developer's webhook settings. The first
// save that enables the webhook creates the signing secret, returned once.
func (s *server) updateApplicationWebhook(w http.ResponseWriter, r *http.Request) {
	var input applicationWebhookInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.URL != nil && strings.TrimSpace(*input.URL) != "" {
		target, err := s.validWebhookURL(*input.URL)
		if err != nil {
			writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), errInvalid.Error()+": "))
			return
		}
		input.URL = &target
	}
	var types []string
	if input.ResourceTypes != nil {
		ids, err := normalizeWebhookResourceTypes(*input.ResourceTypes, true)
		if err != nil {
			writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), errInvalid.Error()+": "))
			return
		}
		// Stored under the payload type names, as app manifests store them.
		types = []string{}
		for _, id := range ids {
			types = append(types, webhookTypeName(id))
		}
	}
	id := r.PathValue("id")
	var updated domain.OAuthApplication
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "oauth_application.updated", id, map[string]any{"webhook": input}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == id })
		if index < 0 {
			return errNotFound
		}
		app := &data.OAuthApplications[index]
		if input.URL != nil {
			app.WebhookURL = strings.TrimSpace(*input.URL)
		}
		if input.ResourceTypes != nil {
			app.WebhookResourceTypes = types
		}
		if input.Enabled != nil {
			app.WebhookEnabled = *input.Enabled
		}
		if app.WebhookEnabled && app.WebhookURL == "" {
			return fmt.Errorf("%w: a webhook URL is required", errInvalid)
		}
		app.UpdatedAt = time.Now().UTC()
		appendAudit(data, "webhook_updated", "oauth_application", app.ID, map[string]any{"url": app.WebhookURL, "enabled": app.WebhookEnabled})
		updated = *app
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	settings := s.applicationWebhookSettings(r.Context(), updated)
	if updated.WebhookEnabled && !settings.SigningEnabled {
		secret, err := s.storeApplicationWebhookSecret(r, updated, "")
		if err != nil {
			respondMutation(w, err, http.StatusOK, nil)
			return
		}
		settings = s.applicationWebhookSettings(r.Context(), updated)
		settings.SecretPrefix, settings.Secret = secret[:min(len(secret), 17)], secret
		s.auditAPIKeyCredential(r, "webhook_secret_issued", updated)
		w.Header().Set("Cache-Control", "no-store")
	}
	go s.reconcileAllApplicationWebhooks(context.Background())
	writeJSON(w, http.StatusOK, settings)
}

func (s *server) rotateApplicationWebhookSecret(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	index := slices.IndexFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == r.PathValue("id") })
	if index < 0 {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}
	secret, err := s.storeApplicationWebhookSecret(r, data.OAuthApplications[index], "")
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	app := data.OAuthApplications[index]
	app.WebhookSecretPrefix = secret[:min(len(secret), 17)]
	settings := s.applicationWebhookSettings(r.Context(), app)
	settings.Secret = secret
	s.auditAPIKeyCredential(r, "webhook_secret_rotated", app)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, settings)
}

// storeApplicationWebhookSecret stores a signing secret for the application
// (the given one, which the settings form generated and showed, or a new
// one) and records its prefix.
func (s *server) storeApplicationWebhookSecret(r *http.Request, app domain.OAuthApplication, secret string) (string, error) {
	if !webhookSecretPattern.MatchString(secret) {
		var err error
		if secret, err = randomWebhookSecret(); err != nil {
			return "", err
		}
	}
	if err := s.store.SetWebhookSigningSecret(r.Context(), store.ApplicationWebhookSecretRef(app.ClientID), workspaceKey(r), secret); err != nil {
		return "", err
	}
	prefix := secret[:min(len(secret), 17)]
	err := s.store.MutateWorkspace(store.WithMetadataFields(r.Context(), "oauthApplications"), workspaceKey(r), "oauth_application.updated", app.ID, map[string]any{"webhookSecretRotated": true}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.OAuthApplications, func(item domain.OAuthApplication) bool { return item.ID == app.ID })
		if index < 0 {
			return errNotFound
		}
		data.OAuthApplications[index].WebhookSecretPrefix = prefix
		return nil
	})
	return secret, err
}

// applicationWebhookLifecycleEvent reports events that can change which
// applications a workspace has installed or the teams they reach.
func applicationWebhookLifecycleEvent(eventType string) bool {
	return strings.HasPrefix(eventType, "application.") || strings.HasPrefix(eventType, "oauth_authorization.") ||
		slices.Contains([]string{"team.created", "team.updated", "team.archived", "team.unarchived", "team.deleted", "team.settings_updated", "oauth_application.deleted"}, eventType)
}

// oauthApplicationsByClient indexes every workspace's OAuth applications.
func (s *server) oauthApplicationsByClient() map[string]domain.OAuthApplication {
	result := map[string]domain.OAuthApplication{}
	for _, key := range s.store.WorkspaceKeys() {
		data, ok := s.store.WorkspaceMetadataFields(key, "oauthApplications")
		if !ok {
			continue
		}
		for _, app := range data.OAuthApplications {
			result[app.ClientID] = app
		}
	}
	return result
}

func (s *server) reconcileAllApplicationWebhooks(ctx context.Context) {
	apps := s.oauthApplicationsByClient()
	for _, key := range s.store.WorkspaceKeys() {
		s.reconcileApplicationWebhooksWith(ctx, key, apps)
	}
}

func (s *server) reconcileApplicationWebhooks(ctx context.Context, workspace string) {
	s.reconcileApplicationWebhooksWith(ctx, workspace, s.oauthApplicationsByClient())
}

var applicationWebhookReconcile sync.Mutex

type desiredApplicationWebhook struct {
	app       domain.OAuthApplication
	teamIDs   []string
	appUserID string
	installer string
	// withoutRealtime: the installation was authorized before the
	// application had a webhook and must be authorized again to get one.
	withoutRealtime bool
}

func managedWebhookID(workspace, clientID string) string {
	sum := sha256.Sum256([]byte(workspace + "\x00" + clientID))
	return "webhook_app_" + hex.EncodeToString(sum[:12])
}

// reconcileApplicationWebhooksWith brings the workspace's managed webhooks in
// line with the installed applications, sending revoked and permission
// change events for the differences.
func (s *server) reconcileApplicationWebhooksWith(ctx context.Context, workspace string, apps map[string]domain.OAuthApplication) {
	applicationWebhookReconcile.Lock()
	defer applicationWebhookReconcile.Unlock()
	data, ok := s.store.WorkspaceMetadataFields(workspace, "webhooks", "oauthAuthorizations", "teams", "teamSettings")
	if !ok {
		return
	}
	installations, err := s.store.ListApplications(ctx, workspace)
	if err != nil {
		return
	}
	authorized := map[string]desiredApplicationWebhook{}
	for _, installation := range installations {
		if installation.Builtin || !installation.Active {
			continue
		}
		teams := slices.Clone(installation.AccessTeamIDs())
		sort.Strings(teams)
		authorized[installation.ClientID] = desiredApplicationWebhook{teamIDs: teams, appUserID: installation.UserID, installer: installation.InstalledBy, withoutRealtime: installation.WithoutRealtime}
	}
	for _, grant := range data.OAuthAuthorizations {
		if grant.RevokedAt != nil || grant.ClientID == "" {
			continue
		}
		if _, ok := authorized[grant.ClientID]; ok {
			continue
		}
		teams := []string{}
		for _, team := range data.Teams {
			if !team.Private && team.ArchivedAt == nil && team.RetiredAt == nil {
				teams = append(teams, team.ID)
			}
		}
		sort.Strings(teams)
		authorized[grant.ClientID] = desiredApplicationWebhook{teamIDs: teams, installer: grant.UserID}
	}
	desired := map[string]desiredApplicationWebhook{}
	for clientID, item := range authorized {
		app, ok := apps[clientID]
		if !ok || !app.WebhookEnabled || app.WebhookURL == "" || item.withoutRealtime {
			continue
		}
		app.WebhookResourceTypes = applicationWebhookResourceIDs(app.WebhookResourceTypes)
		item.app = app
		desired[clientID] = item
	}
	existing := map[string]domain.Webhook{}
	for _, hook := range data.Webhooks {
		if hook.ManagedBy == "application" {
			existing[hook.ApplicationClientID] = hook
		}
	}
	changed := len(desired) != len(existing)
	for clientID, item := range desired {
		hook, ok := existing[clientID]
		if !ok || hook.URL != item.app.WebhookURL || hook.Name != item.app.Name || !slices.Equal(hook.ResourceTypes, item.app.WebhookResourceTypes) || !slices.Equal(hook.TeamIDs, item.teamIDs) {
			changed = true
		}
	}
	if !changed {
		return
	}
	type permissionChange struct {
		hook           domain.Webhook
		added, removed []string
		appUserID      string
	}
	var revoked []domain.Webhook
	var permissions []permissionChange
	err = s.store.MutateWorkspace(store.WithMetadataFields(ctx, "webhooks"), workspace, "webhook.updated", "application_webhooks", map[string]any{"managed": true}, func(next *domain.Bootstrap) error {
		revoked, permissions = nil, nil
		now := time.Now().UTC()
		mutated := false
		kept := next.Webhooks[:0:0]
		for _, hook := range next.Webhooks {
			if hook.ManagedBy != "application" {
				kept = append(kept, hook)
				continue
			}
			item, ok := desired[hook.ApplicationClientID]
			if !ok {
				mutated = true
				if _, stillAuthorized := authorized[hook.ApplicationClientID]; !stillAuthorized {
					revoked = append(revoked, hook)
				}
				continue
			}
			if hook.URL != item.app.WebhookURL || hook.Name != item.app.Name || !slices.Equal(hook.ResourceTypes, item.app.WebhookResourceTypes) || !slices.Equal(hook.TeamIDs, item.teamIDs) {
				added, removed := diffStrings(hook.TeamIDs, item.teamIDs)
				hook.URL, hook.Name, hook.ResourceTypes, hook.TeamIDs, hook.UpdatedAt = item.app.WebhookURL, item.app.Name, slices.Clone(item.app.WebhookResourceTypes), item.teamIDs, now
				if len(added)+len(removed) > 0 {
					permissions = append(permissions, permissionChange{hook: hook, added: added, removed: removed, appUserID: item.appUserID})
				}
				mutated = true
			}
			kept = append(kept, hook)
		}
		for clientID, item := range desired {
			if slices.ContainsFunc(kept, func(hook domain.Webhook) bool {
				return hook.ManagedBy == "application" && hook.ApplicationClientID == clientID
			}) {
				continue
			}
			kept = append(kept, domain.Webhook{
				ID: managedWebhookID(workspace, clientID), Name: item.app.Name, URL: item.app.WebhookURL, ApplicationID: item.app.ID, ApplicationClientID: clientID,
				ManagedBy: "application", ResourceTypes: slices.Clone(item.app.WebhookResourceTypes), TeamIDs: item.teamIDs, TeamRestriction: "selected",
				Enabled: true, CreatorID: item.installer, PayloadVersion: domain.WebhookPayloadVersion, SigningEnabled: true, CreatedAt: now, UpdatedAt: now,
			})
			mutated = true
		}
		if !mutated {
			return store.ErrNoMutation
		}
		next.Webhooks = kept
		return nil
	})
	if err != nil {
		log.Printf("Application webhooks workspace=%s: %v", workspace, err)
		return
	}
	organizationID := data.Workspace.ID
	for _, hook := range revoked {
		app := apps[hook.ApplicationClientID]
		target := firstNonEmpty(app.WebhookURL, hook.URL)
		body, _ := json.Marshal(map[string]any{
			"action": "revoked", "type": "OAuthApp", "createdAt": time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
			"oauthClientId": hook.ApplicationClientID, "organizationId": organizationID, "webhookId": hook.ID,
			"data": map[string]any{"oauthClientId": hook.ApplicationClientID, "organizationId": organizationID},
		})
		hook.URL = target
		s.queueWebhookDelivery(ctx, workspace, hook, domain.WebhookDelivery{
			ID: webhookDeliveryID(hook.ID, "revoked:"+hook.CreatedAt.String()), Kind: domain.WebhookDeliveryKindLifecycle, EventID: "revoked:" + hook.ID,
			EventType: "OAuthApp", Action: "revoked", Payload: body,
		})
		_ = s.store.CancelWebhookDeliveries(ctx, workspace, hook.ID)
	}
	for _, change := range permissions {
		if !change.hook.Enabled || !slices.Contains(change.hook.ResourceTypes, "permission_changes") {
			continue
		}
		eventKey := fmt.Sprintf("permission:%s:%s", strings.Join(change.hook.TeamIDs, ","), idgen.Stamp())
		body, _ := json.Marshal(map[string]any{
			"action": "teamAccessChanged", "type": "PermissionChange", "createdAt": time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
			"oauthClientId": change.hook.ApplicationClientID, "appUserId": change.appUserID, "organizationId": organizationID, "webhookId": change.hook.ID,
			"addedTeamIds": change.added, "removedTeamIds": change.removed, "canAccessAllPublicTeams": false,
			"data": map[string]any{"teamIds": change.hook.TeamIDs, "addedTeamIds": change.added, "removedTeamIds": change.removed},
		})
		s.queueWebhookDelivery(ctx, workspace, change.hook, domain.WebhookDelivery{
			ID: webhookDeliveryID(change.hook.ID, eventKey), Kind: domain.WebhookDeliveryKindEvent, EventID: eventKey,
			EventType: "PermissionChange", Action: "teamAccessChanged", Payload: body,
		})
	}
}

// applicationWebhookResourceIDs maps an application's stored resource types
// (payload type names from the settings page or a manifest, or Flow ids) to
// Flow ids, dropping types Flow does not deliver.
func applicationWebhookResourceIDs(values []string) []string {
	ids := []string{}
	for _, value := range values {
		if id, ok := canonicalWebhookResourceType(value); ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func diffStrings(before, after []string) (added, removed []string) {
	added, removed = []string{}, []string{}
	for _, value := range after {
		if !slices.Contains(before, value) {
			added = append(added, value)
		}
	}
	for _, value := range before {
		if !slices.Contains(after, value) {
			removed = append(removed, value)
		}
	}
	return added, removed
}

// appUserNotificationAction maps a Flow inbox notification to the action of
// an AppUserNotification event.
func appUserNotificationAction(notification domain.Notification) string {
	switch {
	case notification.Category == "mentions" && notification.CommentID != "":
		return "issueCommentMention"
	case notification.Category == "mentions":
		return "issueMention"
	case notification.Category == "reactions" && notification.CommentID != "":
		return "issueCommentReaction"
	case notification.Category == "reactions":
		return "issueEmojiReaction"
	case notification.Category == "comments" || notification.Type == "comment":
		return "issueNewComment"
	case notification.Category == "statusChanges":
		return "issueStatusChanged"
	case notification.Category == "assignments":
		return "issueAssignedToYou"
	}
	return ""
}

// sweepAppUserNotifications sends the recent inbox notifications of each
// installed application's user to its webhook. Delivery ids are derived from
// the notification and its update time, so each change is sent once.
func (s *server) sweepAppUserNotifications(ctx context.Context, workspace string, now time.Time) {
	hooks, ok := s.store.WorkspaceMetadataFields(workspace, "webhooks")
	if !ok || !slices.ContainsFunc(hooks.Webhooks, func(item domain.Webhook) bool {
		return item.ManagedBy == "application" && item.Enabled && slices.Contains(item.ResourceTypes, "app_user_notifications")
	}) {
		return
	}
	installations, err := s.store.ListApplications(ctx, workspace)
	if err != nil {
		return
	}
	for _, hook := range hooks.Webhooks {
		if hook.ManagedBy != "application" || !hook.Enabled || !slices.Contains(hook.ResourceTypes, "app_user_notifications") {
			continue
		}
		index := slices.IndexFunc(installations, func(item domain.ApplicationInstallation) bool {
			return item.ClientID == hook.ApplicationClientID && item.Active
		})
		if index < 0 {
			continue
		}
		appUserID := installations[index].UserID
		page, err := s.store.QueryNotifications(ctx, store.NotificationQuery{Workspace: workspace, UserID: appUserID, IncludeArchived: true, IncludeSnoozed: true, Limit: 50})
		if err != nil {
			continue
		}
		for _, notification := range page.Notifications {
			if now.Sub(notification.UpdatedAt) > 15*time.Minute || notification.Category == "assignments" {
				continue
			}
			action := appUserNotificationAction(notification)
			if action == "" {
				continue
			}
			s.queueAppUserNotification(ctx, workspace, hooks.Workspace.ID, hook, appUserID, action, notification.ID+":"+notification.UpdatedAt.UTC().Format(time.RFC3339Nano), map[string]any{
				"id": notification.ID, "type": action, "issueId": notification.IssueID, "commentId": notification.CommentID,
				"actorId": notification.Actor.ID, "createdAt": notification.CreatedAt, "updatedAt": notification.UpdatedAt,
			}, notification.IssueID, notification.CommentID)
		}
	}
}

func (s *server) queueAppUserNotification(ctx context.Context, workspace, organizationID string, hook domain.Webhook, appUserID, action, eventKey string, notification map[string]any, issueID, commentID string) {
	if issueID != "" {
		if issue, err := s.store.IssueRecord(ctx, workspace, issueID); err == nil {
			if len(hook.TeamIDs) > 0 && !slices.Contains(hook.TeamIDs, issue.Team.ID) {
				return
			}
			notification["issue"] = map[string]any{"id": issue.ID, "identifier": issue.Identifier, "title": issue.Title, "teamId": issue.Team.ID, "url": s.appBaseURL() + "/" + url.PathEscape(workspace) + "/issue/" + url.PathEscape(issue.Identifier)}
		}
	}
	if commentID != "" && issueID != "" {
		if comment, err := s.store.ResourceComment(ctx, workspace, issueID, commentID); err == nil {
			notification["comment"] = map[string]any{"id": comment.ID, "body": comment.Body, "userId": comment.User.ID, "parentId": comment.ParentID}
		}
	}
	body, _ := json.Marshal(map[string]any{
		"action": action, "type": "AppUserNotification", "createdAt": time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		"oauthClientId": hook.ApplicationClientID, "appUserId": appUserID, "organizationId": organizationID, "webhookId": hook.ID,
		"notification": notification,
	})
	s.queueWebhookDelivery(ctx, workspace, hook, domain.WebhookDelivery{
		ID: webhookDeliveryID(hook.ID, "notification:"+eventKey), Kind: domain.WebhookDeliveryKindEvent, EventID: "notification:" + eventKey,
		EventType: "AppUserNotification", Action: action, Payload: body,
	})
}

// dispatchAppAssignmentNotifications sends issueAssignedToYou and
// issueUnassignedFromYou when an issue's assignee or delegate changes to or
// from an installed application's user.
func (s *server) dispatchAppAssignmentNotifications(workspace string, event domain.DomainEvent, issue map[string]any) {
	if !strings.HasPrefix(event.Type, "issue.") || len(event.PreviousValues) == 0 {
		return
	}
	var previous map[string]json.RawMessage
	if json.Unmarshal(event.PreviousValues, &previous) != nil || previous["assignee"] == nil && previous["delegate"] == nil {
		return
	}
	hooks, ok := s.store.WorkspaceMetadataFields(workspace, "webhooks")
	if !ok || !slices.ContainsFunc(hooks.Webhooks, func(item domain.Webhook) bool {
		return item.ManagedBy == "application" && item.Enabled && slices.Contains(item.ResourceTypes, "app_user_notifications")
	}) {
		return
	}
	ctx := context.Background()
	installations, err := s.store.ListApplications(ctx, workspace)
	if err != nil {
		return
	}
	userID := func(raw json.RawMessage) string {
		var value struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &value)
		return value.ID
	}
	current := func(key string) string {
		if value, ok := issue[key].(map[string]any); ok {
			id, _ := value["id"].(string)
			return id
		}
		return ""
	}
	for _, field := range []string{"assignee", "delegate"} {
		raw, changed := previous[field]
		if !changed {
			continue
		}
		before, after := userID(raw), current(field)
		if before == after {
			continue
		}
		for _, change := range []struct{ user, action string }{{after, "issueAssignedToYou"}, {before, "issueUnassignedFromYou"}} {
			if change.user == "" {
				continue
			}
			index := slices.IndexFunc(installations, func(item domain.ApplicationInstallation) bool { return item.UserID == change.user && item.Active })
			if index < 0 {
				continue
			}
			for _, hook := range hooks.Webhooks {
				if hook.ManagedBy != "application" || !hook.Enabled || hook.ApplicationClientID != installations[index].ClientID || !slices.Contains(hook.ResourceTypes, "app_user_notifications") {
					continue
				}
				s.queueAppUserNotification(ctx, workspace, hooks.Workspace.ID, hook, change.user, change.action, event.ID+":"+field+":"+change.action, map[string]any{
					"id": event.ID + ":" + field, "type": change.action, "issueId": event.AggregateID, "actorId": event.ActorID, "createdAt": event.CreatedAt, "field": field,
				}, event.AggregateID, "")
			}
		}
	}
}

// sweepIssueSLAWebhooks sends IssueSLA events (set, highRisk, breached) for
// SLA changes of the last 15 minutes to webhooks subscribed to issue_sla.
func (s *server) sweepIssueSLAWebhooks(ctx context.Context, workspace string, now time.Time) {
	hooks, ok := s.store.WorkspaceMetadataFields(workspace, "webhooks")
	if !ok || !slices.ContainsFunc(hooks.Webhooks, func(item domain.Webhook) bool {
		return !item.AuditLog && item.Enabled && slices.Contains(item.ResourceTypes, "issue_sla")
	}) {
		return
	}
	data, ok := s.store.WorkspaceMetadataFields(workspace, "issueSlas", "slaEvents", "users")
	if !ok {
		return
	}
	type slaEvent struct {
		key, action, issueID string
		sla                  domain.IssueSLA
		at                   time.Time
	}
	events := []slaEvent{}
	slas := map[string]domain.IssueSLA{}
	for _, sla := range data.IssueSLAs {
		slas[sla.ID] = sla
	}
	for _, event := range data.SLAEvents {
		if now.Sub(event.CreatedAt) > 15*time.Minute {
			continue
		}
		action := map[string]string{"started": "set", "breached": "breached"}[event.Type]
		if action == "" {
			continue
		}
		events = append(events, slaEvent{key: event.ID, action: action, issueID: event.IssueID, sla: slas[event.SLAID], at: event.CreatedAt})
	}
	// High risk: less than a quarter of the SLA window (at most 24 hours) left.
	for _, sla := range data.IssueSLAs {
		if sla.Status != "active" || sla.BreachedAt != nil || !now.Before(sla.DueAt) {
			continue
		}
		window := min(sla.DueAt.Sub(sla.StartedAt)/4, 24*time.Hour)
		if sla.DueAt.Sub(now) <= window {
			events = append(events, slaEvent{key: sla.ID + ":highRisk:" + sla.DueAt.UTC().Format(time.RFC3339), action: "highRisk", issueID: sla.IssueID, sla: sla, at: now})
		}
	}
	if len(events) == 0 {
		return
	}
	root := s.appBaseURL() + "/" + url.PathEscape(workspace)
	for _, event := range events {
		issue, err := s.store.IssueRecord(ctx, workspace, event.issueID)
		if err != nil {
			continue
		}
		issueURL := ""
		if s.appBaseURL() != "" {
			issueURL = root + "/issue/" + url.PathEscape(issue.Identifier) + "/" + url.PathEscape(routeSlug(issue.Title))
		}
		for _, hook := range hooks.Webhooks {
			if hook.AuditLog || !hook.Enabled || !slices.Contains(hook.ResourceTypes, "issue_sla") || hook.TeamRestriction == "selected" && len(hook.TeamIDs) > 0 && !slices.Contains(hook.TeamIDs, issue.Team.ID) {
				continue
			}
			body, _ := json.Marshal(map[string]any{
				"action": event.action, "type": "IssueSLA", "createdAt": event.at.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
				"issueData": issue, "url": issueURL, "organizationId": hooks.Workspace.ID, "webhookId": hook.ID, "eventId": event.key,
				"data":  map[string]any{"id": event.sla.ID, "issueId": issue.ID, "ruleId": event.sla.RuleID, "startedAt": event.sla.StartedAt, "dueAt": event.sla.DueAt, "breachedAt": event.sla.BreachedAt, "status": event.sla.Status},
				"actor": nil,
			})
			s.queueWebhookDelivery(ctx, workspace, hook, domain.WebhookDelivery{
				ID: webhookDeliveryID(hook.ID, "sla:"+event.key), Kind: domain.WebhookDeliveryKindEvent, EventID: "sla:" + event.key,
				EventType: "IssueSLA", Action: event.action, Payload: body,
			})
		}
	}
}
