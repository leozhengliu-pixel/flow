package store

import (
	"encoding/json"
	"maps"
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
	case "1", "true", "on":
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

var teamEditCallbackFields = []string{
	"viewer", "viewerRole", "users", "teams", "teamSettings", "members", "teamMembers",
}

var teamCreateCallbackFields = []string{
	"viewer", "viewerRole", "users", "teams", "states", "labels", "cycles",
	"members", "teamMembers",
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

func teamCallbackInput(current domain.Bootstrap, eventType string, payload any) (domain.Bootstrap, []string) {
	if !narrowMutationInputs() {
		return current, nil
	}
	if eventType == "team.updated" && metadataFieldsOnly(payload, "name", "color", "icon") {
		owned := metadataFieldSnapshot(current, teamEditCallbackFields)
		copyMutationTeamDirectory(&owned, current)
		return owned, teamEditCallbackFields
	}
	if eventType == "team.created" && (payload == nil || metadataFieldsOnly(payload, "name", "key", "color", "icon", "private", "parentTeamId", "copyFromTeamId", "timezone")) {
		var input struct{ ParentTeamID, CopyFromTeamID string }
		raw, err := json.Marshal(payload)
		if err == nil && json.Unmarshal(raw, &input) == nil {
			owned := metadataFieldSnapshot(current, teamCreateCallbackFields)
			// Membership inheritance needs the hierarchy for all teams. Only
			// the copied/parent teams and their ancestors need full settings.
			owned.TeamSettings = map[string]domain.TeamSettings{}
			owned.TeamParents = make(map[string]string)
			for id, settings := range current.TeamSettings {
				if settings.ParentTeamID != "" {
					owned.TeamParents[id] = settings.ParentTeamID
				}
			}
			for _, source := range []string{input.ParentTeamID, input.CopyFromTeamID} {
				seen := map[string]bool{}
				for source != "" && !seen[source] {
					seen[source] = true
					settings, ok := current.TeamSettings[source]
					if !ok {
						break
					}
					owned.TeamSettings[source] = cloneMutationTeamSettings(settings)
					source = settings.ParentTeamID
				}
			}
			owned.CycleSettings = maps.Clone(current.CycleSettings)
			if owned.CycleSettings == nil {
				owned.CycleSettings = map[string]domain.CycleSettings{}
			}
			copyMutationTeamDirectory(&owned, current)
			return owned, teamCreateCallbackFields
		}
	}
	owned := cloneBootstrap(collectionMetadata(current))
	owned.NextIssueNumber = current.NextIssueNumber
	return owned, nil
}

func copyMutationTeamDirectory(data *domain.Bootstrap, current domain.Bootstrap) {
	data.TeamByID = maps.Clone(current.TeamByID)
	data.TeamByKey = maps.Clone(current.TeamByKey)
	data.TeamChildren = cloneSnapshotValue(reflect.ValueOf(current.TeamChildren)).Interface().(map[string][]string)
	data.LabelIndex = cloneSnapshotValue(reflect.ValueOf(current.LabelIndex)).Interface().(map[string][]int)
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
