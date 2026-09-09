package store

import (
	"encoding/json"
	"flow/api/internal/domain"
	"strings"
)

// Keep this allowlist explicit: these callbacks never inspect or modify issue
// or discussion collections. A generic prefix match could silently lose data
// when a new event introduces a cascade.
func metadataOnlyMutation(event string, payload any) bool {
	if event == "label.updated" && metadataFieldsOnly(payload, "name", "description", "color") {
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
	case "pulse.summary_scheduled":
		// Only appends new notifications/deliveries; persistWorkspace handles
		// this event without replacing or hydrating existing content records.
		return true
	case "application_policy.updated":
		return true
	case "workspace.agent_guidance_updated":
		return true
	case "settings.project_archive":
		return true
	case "loop.created", "loop.updated", "loop.deleted",
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
	case "agent.session_created", "agent.message_created", "agent.message_updated", "agent.message_completed", "agent.session_updated", "agent.session_deleted", "agent.skill_created", "agent.skill_updated", "agent.skill_deleted":
		return true
	case "project.updated", "project.resource_created", "project.resource_updated", "project.resource_deleted", "project.milestone_created", "project.milestone_updated", "project.milestones_reordered", "notification_preferences.updated":
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
