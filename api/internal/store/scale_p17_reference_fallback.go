package store

import (
	"reflect"

	"flow/api/internal/domain"
)

// The canonical mutation also refreshes display people on unrelated records.
// Old rows needing that repair keep the complete callback until repaired; a
// selected write must not silently drop those existing side effects.
func documentRecordReferenceFallback(data domain.Bootstrap, scope MetadataRecordScope) bool {
	users := make(map[string]domain.User, len(data.Users))
	for _, user := range data.Users {
		users[user.ID] = user
	}
	changed := func(user domain.User) bool {
		current, ok := users[user.ID]
		return ok && !reflect.DeepEqual(current, user)
	}
	for _, project := range data.Projects {
		if metadataRecordSelected(scope, "projects", project.ID, false) {
			continue
		}
		if project.Creator != nil && changed(*project.Creator) || project.Lead != nil && changed(*project.Lead) {
			return true
		}
	}
	for _, initiative := range data.Initiatives {
		if changed(initiative.Creator) || initiative.Owner != nil && changed(*initiative.Owner) {
			return true
		}
	}
	for _, updates := range data.ProjectUpdates {
		for _, update := range updates {
			if changed(update.User) {
				return true
			}
		}
	}
	for _, updates := range data.InitiativeUpdates {
		for _, update := range updates {
			if changed(update.User) {
				return true
			}
		}
	}
	return false
}
