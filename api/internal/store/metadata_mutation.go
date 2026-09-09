package store

import "flow/api/internal/domain"

// Keep this allowlist explicit: these callbacks never inspect or modify issue
// or discussion collections. A generic prefix match could silently lose data
// when a new event introduces a cascade.
func metadataOnlyMutation(event string, payload any) bool {
	if event == "project.created" {
		input, ok := payload.(domain.ProjectMutationInput)
		return ok && input.TemplateID == ""
	}
	switch event {
	case "api_key.used", "view.created", "view.updated", "view.deleted", "view.shared", "view.unshared",
		"import.previewed", "import.queued", "import.cancelled", "import.retried", "import.resumed", "import.failed",
		"project_display_default.updated", "workspace_preferences.updated", "user_settings.updated", "draft.created", "draft.updated", "draft.deleted", "drafts.deleted":
		return true
	}
	return false
}
