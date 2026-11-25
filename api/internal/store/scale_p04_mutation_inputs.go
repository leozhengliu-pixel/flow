package store

import (
	"os"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

func narrowMutationInputs() bool {
	if fullMutationsForced.Load() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_NARROW_MUTATION_INPUTS"))) {
	case "", "1", "true", "on":
		return true
	}
	return false
}

// These are callback read/write sets, not persistence sets. Omitted fields
// are restored only after the callback returns, before the existing hooks run.
var issueCallbackFields = []string{
	"viewer", "viewerRole", "users", "teams", "teamSettings", "teamParents",
	"members", "teamMembers", "states", "labels", "labelGroups", "cycles", "cycleSettings",
	"userSettings", "workspaceSettings", "settings", "notificationPreferences", "pushSubscriptions",
	"threadSubscriptions", "slaRules", "issueSlas", "slaEvents", "issueSuggestions",
}

var discussionCallbackFields = []string{
	"viewer", "viewerRole", "users", "teams", "teamSettings", "teamParents",
	"members", "teamMembers", "states", "labels", "cycles", "cycleSettings",
	"userSettings", "workspaceSettings", "settings", "notificationPreferences", "pushSubscriptions",
	"threadSubscriptions", "commentActions",
}

func issueRecordCallbackFields(payload any) []string {
	if !narrowMutationInputs() {
		return nil
	}
	input, ok := payload.(domain.IssueUpdateInput)
	if !ok {
		// Schedulers and other callbacks keep their complete input until they
		// declare a field set; recognizing an event name is not sufficient.
		return nil
	}
	fields := slices.Clone(issueCallbackFields)
	if input.ProjectID != nil || input.ProjectMilestoneID != nil || input.TeamID != nil {
		fields = append(fields, "projects")
	}
	if input.TeamID != nil {
		fields = append(fields, "documents")
	}
	return fields
}

// Team inputs retain the legacy path: allocating an owned input would regress
// a callback that already has a small, constant allocation cost.
func teamCallbackInput(current domain.Bootstrap, _ string, _ any) (domain.Bootstrap, []string) {
	return current, nil
}

func issueScopeCallbackInput(current domain.Bootstrap, discussionOnly bool) (domain.Bootstrap, []string) {
	if narrowMutationInputs() && discussionOnly {
		return metadataFieldSnapshot(current, discussionCallbackFields), discussionCallbackFields
	}
	metadata := collectionMetadata(current)
	if discussionOnly {
		metadata.Trash = nil
	}
	return cloneBootstrap(metadata), nil
}

func mutationInputActor(actor domain.User) domain.User {
	if narrowMutationInputs() {
		owned := cloneSnapshotValue(reflect.ValueOf(actor)).Interface().(domain.User)
		// Via is request attribution, deliberately omitted from persisted
		// user snapshots. Callbacks still need it when creating BotActor.
		if actor.Via != nil {
			owned.Via = cloneSnapshotValue(reflect.ValueOf(actor.Via)).Interface().(*domain.ActorBot)
		}
		return owned
	}
	return actor
}

// Discussion reference refreshes and issue suggestions need the linked
// projects, not every project's history and discussion. Each selected record
// is still deeply owned, including its nested permissions and user copies.
func copyMutationProjects(data *domain.Bootstrap, stored domain.Bootstrap) {
	ids := make(map[string]bool)
	for _, issue := range data.Issues {
		if issue.Project != nil {
			ids[issue.Project.ID] = true
		}
	}
	for _, suggestion := range data.IssueSuggestions {
		if suggestion.SuggestedProjectID != "" {
			ids[suggestion.SuggestedProjectID] = true
		}
	}
	for _, project := range stored.Projects {
		if ids[project.ID] {
			data.Projects = append(data.Projects, cloneSnapshotValue(reflect.ValueOf(project)).Interface().(domain.Project))
		}
	}
}
