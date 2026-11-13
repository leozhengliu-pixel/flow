package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

var issueActivityFields = []string{
	"state", "assignee", "priority", "estimate", "title", "dueDate", "project",
	"projectMilestone", "cycle", "parent", "team", "delegate", "description",
	"documentContent", "icon", "snoozedUntil", "archived", "recurrence", "nextOccurrenceAt",
}

// RecordIssueActivity edits only the issue's timeline. The enclosing workspace
// transaction persists updates/deletions through the usual content-record path.
// The return value describes this edit, so notifications do not replay a burst.
func RecordIssueActivity(data *domain.Bootstrap, issueID string, event domain.ActivityEvent) domain.ActivityEvent {
	if data.Activities == nil {
		data.Activities = map[string][]domain.ActivityEvent{}
	}
	if event.Type != "issue.updated" {
		data.Activities[issueID] = append(data.Activities[issueID], event)
		return event
	}
	event.Metadata = maps.Clone(event.Metadata)
	delete(event.Metadata, "subscribers")
	delete(event.Metadata, "sortOrder")
	if event.Metadata["stateType"] == "duplicate" || event.Metadata["viaDuplicate"] == "true" {
		dropActivityField(event.Metadata, "state")
	}
	event.Metadata = mergeIssueActivityMetadata(nil, event.Metadata)
	if !hasIssueActivityChanges(event.Metadata) {
		return domain.ActivityEvent{}
	}
	for _, issue := range data.Issues {
		age := event.CreatedAt.Sub(issue.CreatedAt)
		if issue.ID == issueID && issue.Creator.ID != "" && issue.Creator.ID == event.Actor.ID && age >= 0 && age < 3*time.Minute {
			return domain.ActivityEvent{}
		}
	}
	items := data.Activities[issueID]
	_, loaded := data.ActivityHistoryHeads[issueID]
	if !loaded && len(items) == 0 && data.ActivityHistoryLoader != nil {
		snapshot, err := data.ActivityHistoryLoader(issueID)
		if err != nil {
			data.ActivityHistoryError = err
			return domain.ActivityEvent{}
		}
		if data.ActivityHistoryHeads == nil {
			data.ActivityHistoryHeads = map[string]domain.ActivityEvent{}
		}
		data.ActivityHistoryHeads[issueID] = domain.ActivityEvent{}
		if snapshot.Activity != nil {
			items = append(items, *snapshot.Activity)
			data.Activities[issueID] = items
			data.ActivityHistoryHeads[issueID] = *snapshot.Activity
		}
		if snapshot.Comment != nil {
			if data.Comments == nil {
				data.Comments = map[string][]domain.Comment{}
			}
			data.Comments[issueID] = []domain.Comment{*snapshot.Comment}
		}
	}
	latest := -1
	for i := range items {
		if latest < 0 || !items[i].CreatedAt.Before(items[latest].CreatedAt) {
			latest = i
		}
	}
	if latest >= 0 {
		previous := items[latest]
		lastEdit := previous.CreatedAt
		if previous.UpdatedAt != nil {
			lastEdit = *previous.UpdatedAt
		}
		gap := event.CreatedAt.Sub(lastEdit)
		merge := previous.Type == "issue.updated" && hasIssueActivityChanges(previous.Metadata) && previous.Actor.ID == event.Actor.ID && reflect.DeepEqual(previous.BotActor, event.BotActor) && gap >= 0 && gap < time.Hour
		for _, comment := range data.Comments[issueID] {
			if !comment.CreatedAt.Before(lastEdit) {
				merge = false
			}
		}
		if merge {
			previous.Metadata = mergeIssueActivityMetadata(previous.Metadata, event.Metadata)
			previous.UpdatedAt = &event.CreatedAt
			if !hasIssueActivityChanges(previous.Metadata) {
				data.Activities[issueID] = slices.Delete(items, latest, latest+1)
				return domain.ActivityEvent{}
			}
			items[latest] = previous
			event.ID = previous.ID
			return event
		}
	}
	data.Activities[issueID] = append(items, event)
	return event
}

func hasIssueActivityChanges(metadata domain.ActivityMetadata) bool {
	for _, field := range issueActivityFields {
		if _, ok := metadata[field]; ok {
			return true
		}
	}
	return metadata["labels"] != "" || metadata["addedLabelIds"] != "" || metadata["removedLabelIds"] != ""
}

func dropActivityField(metadata domain.ActivityMetadata, field string) {
	delete(metadata, field)
	delete(metadata, field+"Before")
	switch field {
	case "state":
		for _, key := range []string{"stateId", "stateType", "stateBeforeId", "stateBeforeType", "viaDuplicate", "automation"} {
			delete(metadata, key)
		}
	case "team":
		for _, key := range []string{"teamName", "teamBeforeName", "identifier", "identifierBefore"} {
			delete(metadata, key)
		}
	case "assignee":
		delete(metadata, "previousAssignee")
	case "delegate":
		delete(metadata, "agentSessionId")
	case "description":
		delete(metadata, "descriptionStateBefore")
		delete(metadata, "descriptionHash")
		delete(metadata, "documentContent")
	}
}

func mergeIssueActivityMetadata(before, next domain.ActivityMetadata) domain.ActivityMetadata {
	merged := maps.Clone(before)
	if merged == nil {
		merged = domain.ActivityMetadata{}
	}
	for key, value := range next {
		if strings.Contains(key, "Before") || key == "previousAssignee" {
			if _, exists := merged[key]; exists {
				continue
			}
			// Older records without before-values cannot acquire a later
			// edit's intermediate value as their starting value.
			field, _, _ := strings.Cut(key, "Before")
			if key == "previousAssignee" {
				field = "assignee"
			}
			if _, exists := before[field]; exists {
				continue
			}
		}
		merged[key] = value
	}
	for _, field := range issueActivityFields {
		value, changed := merged[field]
		original, known := merged[field+"Before"]
		if field == "description" {
			// Keep the existing after-value marker without storing another
			// full copy of the description just to detect a round trip.
			checksum := sha256.Sum256([]byte(original))
			value, original = merged["descriptionHash"], hex.EncodeToString(checksum[:])
		}
		if field == "state" && merged["stateBeforeId"] != "" && merged["stateId"] != "" {
			value, original = merged["stateId"], merged["stateBeforeId"]
		}
		if changed && known && original == value {
			dropActivityField(merged, field)
		}
	}
	if _, changed := next["labels"]; changed {
		added, removed := labelDelta(before, "addedLabelIds"), labelDelta(before, "removedLabelIds")
		for _, id := range labelDelta(next, "addedLabelIds") {
			if slices.Contains(removed, id) {
				removed = slices.DeleteFunc(removed, func(value string) bool { return value == id })
			} else if !slices.Contains(added, id) {
				added = append(added, id)
			}
		}
		for _, id := range labelDelta(next, "removedLabelIds") {
			if slices.Contains(added, id) {
				added = slices.DeleteFunc(added, func(value string) bool { return value == id })
			} else if !slices.Contains(removed, id) {
				removed = append(removed, id)
			}
		}
		if _, modern := next["addedLabelIds"]; modern {
			if len(added)+len(removed) == 0 {
				delete(merged, "labels")
				delete(merged, "labelsBefore")
				delete(merged, "addedLabelIds")
				delete(merged, "removedLabelIds")
			} else {
				merged["addedLabelIds"], merged["removedLabelIds"] = encodeLabelDelta(added), encodeLabelDelta(removed)
			}
		}
	}
	return merged
}

func labelDelta(metadata domain.ActivityMetadata, key string) []string {
	ids := []string{}
	_ = json.Unmarshal([]byte(metadata[key]), &ids)
	return ids
}

func encodeLabelDelta(ids []string) string {
	slices.Sort(ids)
	encoded, _ := json.Marshal(ids)
	return string(encoded)
}
