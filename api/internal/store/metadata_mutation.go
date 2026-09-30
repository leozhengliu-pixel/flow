package store

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// Keep this allowlist explicit: these callbacks never inspect or modify issue
// or discussion collections. A generic prefix match could silently lose data
// when a new event introduces a cascade.
func metadataOnlyMutation(event string, payload any) bool {
	if fullMutationsForced.Load() {
		return false
	}
	if event == "team.created" || event == "team.settings_updated" {
		return true
	}
	// Label and label group edits (including archiving, regrouping and
	// resource changes) only copy the label onto the issues that carry it.
	// Issue label display data is resolved from the catalog on read and such
	// copies are never persisted (see issueReferences.equalOwned), so these
	// writes change no issue record.
	if event == "label.updated" || event == "issue_label.updated" || event == "label_group.updated" {
		return true
	}
	if event == "team.updated" && metadataFieldsOnly(payload, "name", "color", "icon") {
		return true
	}
	if event == "project.created" {
		input, ok := payload.(domain.ProjectMutationInput)
		return ok && input.TemplateID == ""
	}
	switch event {
	// Team deletion/restoration only stamps the team, detaches sub-teams and
	// appends an audit entry; issue visibility follows the team's archivedAt at
	// read time. Development-mode membership edits only touch member lists.
	case "team.archived", "team.unarchived", "team_member.updated",
		"workspace_member.username_updated", "workspace_member.updated", "workspace_member.suspended", "workspace_member.resumed", "workspace_member.removed":
		return true
	// Dashboards and posts live in workspace settings; asks only validate
	// templates. None of these callbacks read issues or discussions (meetings
	// validate linked issue ids, so they keep the full path).
	case "dashboard.updated", "post.updated", "ask.created", "ask.updated":
		return true
	case "review.thread_resolved", "review.comment_deleted":
		return true
	case "integration.adapter_configured", "integration.ask_imported", "integration.job_updated", "integration.calendar_synced":
		return true
	case "pulse.summary_scheduled":
		// Only appends new notifications/deliveries; persistWorkspace handles
		// this event without replacing or hydrating existing content records.
		return true
	case "application_policy.updated":
		return true
	// Audit log streaming only advances the streaming webhook's cursor.
	case "webhook.audit_stream_progress":
		return true
	case "workspace.agent_guidance_updated":
		return true
	case "settings.project_archive":
		return true
	case "loop.created", "loop.updated", "loop.deleted", "loop.duplicated", "loop.described",
		"loop.run_started", "loop.run_progress", "loop.run_finished", "loop.scheduled",
		"loop.version_restored", "loop.run_feedback", "loop.attachment_uploaded",
		"workflow_definition.created", "workflow_definition.updated", "workflow_definition.deleted",
		"ai.conversation_created", "ai.conversation_updated", "ai.prompt_progress_created":
		return true
	case "export.queued", "export.completed":
		return true
	case "integration_delivery.claimed", "integration_delivery.retry":
		return true
	case "label.created", "label_group.created", "workspace_invitations.created", "workspace_invitation.revoked", "workspace_invitation.resent":
		return true
	case "api_key.created", "api_key.secret_rotated", "api_key.revoked", "account.profile_updated", "workspace_member.identity_cascaded":
		return true
	case "agent.session_created", "agent.message_created", "agent.message_updated", "agent.message_completed", "agent.session_updated", "agent.session_titled", "agent.session_deleted", "agent.skill_created", "agent.skill_updated", "agent.skill_deleted":
		return true
	case "project.updated", "project.resource_created", "project.resource_updated", "project.resource_deleted", "project.milestone_created", "project.milestone_updated", "project.milestones_reordered", "notification_preferences.updated":
		return true
	// Project discussion, relations and settings live on the project and
	// project-update metadata; none of these callbacks read issues, comments,
	// activities or notifications (project.update_created archives reminder
	// notifications and runs with a mutation scope instead).
	case "project.commented", "project.comment_updated", "project.comment_resolved", "project.comment_unresolved",
		"project.comment_deleted", "project.comment_reaction_toggled", "project.comment_thread_subscription", "project.comment_attachment_created",
		"project.update_updated", "project.update_deleted", "project.update_commented", "project.update_reaction_toggled",
		"project.update_attachment_created", "project.update_attachment_deleted",
		"project.relation_created", "project.relation_updated", "project.relation_deleted",
		"project_status.created", "project_status.updated", "project_status.deleted", "project_status.reordered",
		"project_template.deleted", "project_update_settings.updated":
		return true
	case "initiative.created", "initiative.updated", "initiative.deleted",
		"initiative.commented", "initiative.comment_updated", "initiative.comment_deleted", "initiative.comment_reaction_toggled",
		"initiative.update_created", "initiative.update_updated", "initiative.update_deleted", "initiative.update_commented", "initiative.update_reaction_toggled",
		"initiative.update_attachment_created", "initiative.update_attachment_deleted",
		"initiative.relation_created", "initiative.relation_updated", "initiative.relation_deleted",
		"initiative.resource_created", "initiative.resource_updated", "initiative.resource_deleted":
		return true
	case "cycle.updated", "cycle.capacity_updated", "cycle.resource_created", "cycle.resource_deleted", "cycle.calendar_token_requested":
		return true
	case "document.deleted", "document.revision_restored", "document.permissions_updated", "document.permission_updated", "document.permission_deleted",
		"document.draft_created", "document.draft_updated", "document.draft_deleted", "document.draft_published",
		"document_template.created", "document_template.updated", "document_template.deleted":
		return true
	// Releases keep their issue links as ids on the release; progress is
	// derived at read time. release.created/updated/ci_event validate issue
	// ids and may run completion automations, so they use a mutation scope.
	case "release.deleted", "release.reordered", "release.note_created", "release.note_updated", "release.note_deleted", "release.timestamps_updated",
		"release_pipeline.created", "release_pipeline.updated", "release_pipeline.deleted", "release_pipeline.reordered", "release_pipeline.access_key_rotated":
		return true
	case "customer.created", "customer.updated", "customer.deleted", "customer_need.archive_toggled",
		"customer_request.deleted", "customer_request.attachment_created", "customer_request.attachment_deleted",
		"customer_taxonomy.created", "customer_taxonomy.updated", "customer_taxonomy.deleted":
		return true
	case "issue_template.created", "issue_template.updated", "issue_template.deleted", "issue_label.created",
		"team.resource_updated", "team.resource_deleted", "team.resource_section_created", "team.resource_section_updated", "team.resource_section_deleted":
		return true
	// Settings, integration, auth and delivery bookkeeping. Several of these
	// run from background workers or on every sign-in (webhook delivery
	// failures, OAuth token refreshes, passkey ceremonies), where a full
	// workspace load would stall every other write behind the lock.
	case "webhook.created", "webhook.updated", "webhook.deleted", "webhook.secret_rotated", "webhook.secret_revoked", "webhook.delivery_failed",
		"integration.connected", "integration.connection_tested", "integration.disconnected", "integration.updated",
		"integration.oauth_started", "integration.oauth_completed", "integration.oauth_error", "integration.oauth_expired",
		"integration.oauth_refreshed", "integration.oauth_revoked", "integration.oauth_unavailable", "integration_delivery.queued",
		"jira.link_created", "jira.link_updated", "jira.link_deleted", "git_automation.upserted", "git_automation.deleted",
		"target_branch.upserted", "target_branch.deleted", "identity_provider.created", "identity_provider.updated",
		"identity_provider.deleted", "identity_provider.verified", "triage_responsibility.created", "triage_responsibility.updated",
		"triage_responsibility.deleted", "triage_rule.created", "triage_rule.updated", "triage_rule.deleted",
		"push_subscription.created", "push_subscription.deleted", "email_intake.created", "email_intake.deleted",
		"email_intake.rotated", "email_intake.verified", "passkey.registration_started", "passkey.created", "passkey.updated",
		"passkey.deleted", "passkey.authentication_started", "passkey.authentication_finished", "passkey.authentication_used",
		"oauth_application.created", "oauth_application.updated", "oauth_application.deleted", "oauth_token.created",
		"agent.activity_created", "agent.activity_updated", "api_key.updated", "commit_signing_key.added", "commit_signing_key.removed",
		"custom_emoji.created", "custom_emoji.updated", "dashboard.created", "dashboard.deleted", "dashboard.share.updated",
		"dashboard.subscription.updated", "meeting.created", "meeting.deleted", "post.created", "post.deleted", "export.retried",
		"scim.enabled", "scim.token_rotated", "workspace_invite_link.disabled", "workspace_invite_link.rotated", "ask.deleted",
		"review.commented", "review.submitted", "sla_rule.deleted", "sla_settings.updated", "workspace.settings_updated":
		return true
	case "favorite.added", "favorite.removed", "favorite.updated",
		"favorite_folder.created", "favorite_folder.updated", "favorite_folder.deleted",
		"subscription.added", "subscription.removed":
		return true
	case "api_key.used", "view.created", "view.updated", "view.deleted", "view.shared", "view.unshared",
		"import.previewed", "import.queued", "import.cancelled", "import.retried", "import.resumed", "import.failed",
		"project_display_default.updated", "workspace_preferences.updated", "user_settings.updated", "draft.created", "draft.updated", "draft.deleted", "drafts.deleted":
		return true
	}
	return false
}

func metadataFieldsOnly(payload any, allowed ...string) bool {
	raw, err := json.Marshal(payload)
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &fields) != nil {
		return false
	}
	found := false
	for key, value := range fields {
		if string(value) == "null" {
			continue
		}
		if string(value) == `""` && !strings.EqualFold(key, "parentTeamId") {
			continue
		}
		ok := false
		for _, field := range allowed {
			if strings.EqualFold(field, key) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
		found = true
	}
	return found
}

type metadataFieldsKey struct{}

// WithMetadataFields declares the only workspace metadata fields (JSON
// names) a metadata-only mutation's callback reads or changes. The store then
// copies just those fields for the callback instead of the whole metadata
// snapshot; the others read as empty and are persisted unchanged. Background
// jobs that write small settings at high volume use it. Viewer and workspace
// identity are always present.
func WithMetadataFields(ctx context.Context, fields ...string) context.Context {
	return context.WithValue(ctx, metadataFieldsKey{}, fields)
}

func metadataFieldsFromContext(ctx context.Context) ([]string, bool) {
	if fullMutationsForced.Load() {
		return nil, false
	}
	fields, ok := ctx.Value(metadataFieldsKey{}).([]string)
	return fields, ok && len(fields) > 0
}

// metadataFieldSnapshot copies the listed metadata fields of data (and its
// workspace and viewer identity); every other field is left empty.
func metadataFieldSnapshot(data domain.Bootstrap, fields []string) domain.Bootstrap {
	result := domain.Bootstrap{Workspace: data.Workspace, Viewer: data.Viewer, ViewerRole: data.ViewerRole, NextIssueNumber: data.NextIssueNumber}
	source, target := reflect.ValueOf(&data).Elem(), reflect.ValueOf(&result).Elem()
	for _, field := range persistedMetadataFields() {
		if slices.Contains(fields, field.name) {
			target.Field(field.index).Set(source.Field(field.index))
		}
	}
	result = cloneBootstrap(result)
	result.NextIssueNumber = data.NextIssueNumber
	return result
}

// restoreUnlistedMetadata puts stored's values back into every metadata
// field outside fields (including the workspace record).
func restoreUnlistedMetadata(data *domain.Bootstrap, stored domain.Bootstrap, fields []string) {
	source, target := reflect.ValueOf(&stored).Elem(), reflect.ValueOf(data).Elem()
	for _, field := range persistedMetadataFields() {
		if !slices.Contains(fields, field.name) {
			target.Field(field.index).Set(source.Field(field.index))
		}
	}
}
