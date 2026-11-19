package main

import (
	"strings"

	"flow/api/internal/store"
)

// validLabelRecordKey reports a value that can name a canonical metadata
// record. Any other value names no stored record, so leaving it out of a
// scope keeps the callback's not-found answer.
func validLabelRecordKey(id string) bool {
	return id != "" && len(id) <= 191 && !strings.ContainsRune(id, '\x00')
}

// labelGroupReads is the record a label's group validation reads.
func labelGroupReads(groupID string) []store.RecordKey {
	if !validLabelRecordKey(groupID) {
		return nil
	}
	return []store.RecordKey{{Kind: "labelGroups", ID: groupID}}
}

// labelRecordScope is updateTeamLabel's reviewed record scope for a label
// write by path ID: the label itself, and the records of the same ID an
// earlier collection may hold (the event's aggregate), which a label_ ID
// never shares.
func labelRecordScope(labelID string) store.MetadataRecordScope {
	scope := store.MetadataRecordScope{}
	if !validLabelRecordKey(labelID) {
		return scope
	}
	scope.Write = []store.RecordKey{{Kind: "labels", ID: labelID}}
	scope.Read = []store.RecordKey{
		{Kind: "providerJobs", ID: labelID},
		{Kind: "users", ID: labelID},
		{Kind: "teams", ID: labelID},
		{Kind: "customers", ID: labelID},
		{Kind: "states", ID: labelID},
	}
	switch {
	case strings.HasPrefix(labelID, "label_"):
		scope.Read = nil
	case strings.HasPrefix(labelID, "team_resource_section_"):
		scope.Read = []store.RecordKey{{Kind: "teamResourceSections", ID: labelID}}
	case strings.HasPrefix(labelID, "team_resource_"):
		scope.Read = []store.RecordKey{{Kind: "teamPinnedResources", ID: labelID}}
	case strings.HasPrefix(labelID, "project_"):
		scope.Read = append(scope.Read, store.RecordKey{Kind: "projects", ID: labelID})
	case strings.HasPrefix(labelID, "cycle_"):
		scope.Read = append(scope.Read, store.RecordKey{Kind: "cycles", ID: labelID})
	}
	return scope
}
