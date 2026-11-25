package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func addIssueActivityBeforeValues(before, after domain.Issue, changes map[string]string) {
	userID := func(user *domain.User) string {
		if user == nil {
			return ""
		}
		return user.ID
	}
	projectID := func(project *domain.ProjectSummary) string {
		if project == nil {
			return ""
		}
		return project.ID
	}
	timestamp := func(value *time.Time) string {
		if value == nil {
			return ""
		}
		return value.UTC().Format(time.RFC3339Nano)
	}
	values := map[string]string{
		"state": before.State.Name,
		"title": before.Title, "priority": priorityLabel(before.Priority), "dueDate": optionalID(before.DueDate),
		"assignee": userID(before.Assignee), "delegate": userID(before.Delegate), "project": projectID(before.Project),
		"projectMilestone": optionalID(before.ProjectMilestoneID), "cycle": optionalID(before.CycleID),
		"parent": optionalID(before.ParentID), "team": before.Team.ID, "icon": before.Icon,
		"archived":   strconv.FormatBool(before.ArchivedAt != nil),
		"recurrence": before.Recurrence, "snoozedUntil": timestamp(before.SnoozedUntil), "nextOccurrenceAt": timestamp(before.NextOccurrenceAt),
	}
	if _, changed := changes["state"]; changed {
		changes["stateBeforeId"], changes["stateBeforeType"] = before.State.ID, before.State.Type
	}
	for field, value := range values {
		if _, changed := changes[field]; changed {
			changes[field+"Before"] = value
		}
	}
	if _, changed := changes["title"]; changed {
		changes["title"] = after.Title
	}
	if _, changed := changes["description"]; changed {
		checksum := sha256.Sum256([]byte(after.Description))
		changes["descriptionHash"] = hex.EncodeToString(checksum[:])
	}
	for field, value := range map[string]string{"snoozedUntil": timestamp(after.SnoozedUntil), "nextOccurrenceAt": timestamp(after.NextOccurrenceAt)} {
		if _, changed := changes[field]; changed {
			changes[field] = value
		}
	}
	if optionalID(before.ProjectMilestoneID) != optionalID(after.ProjectMilestoneID) {
		changes["projectMilestoneBefore"] = optionalID(before.ProjectMilestoneID)
		changes["projectMilestone"] = optionalID(after.ProjectMilestoneID)
	}
	if _, changed := changes["labels"]; changed || !slices.Equal(issueActivityLabelIDs(before), issueActivityLabelIDs(after)) {
		oldIDs, newIDs := issueActivityLabelIDs(before), issueActivityLabelIDs(after)
		added, removed := []string{}, []string{}
		for _, id := range newIDs {
			if !slices.Contains(oldIDs, id) {
				added = append(added, id)
			}
		}
		for _, id := range oldIDs {
			if !slices.Contains(newIDs, id) {
				removed = append(removed, id)
			}
		}
		changes["labels"] = strings.Join(newIDs, ",")
		addedJSON, _ := json.Marshal(added)
		removedJSON, _ := json.Marshal(removed)
		changes["addedLabelIds"], changes["removedLabelIds"] = string(addedJSON), string(removedJSON)
	}
	duplicate := after.State.Type == "duplicate" || after.State.Type == "canceled" && (after.State.Reserved || strings.EqualFold(after.State.Name, "Duplicate"))
	if duplicate && before.State.ID != after.State.ID {
		changes["viaDuplicate"] = "true"
	}
}

func issueActivityLabelIDs(issue domain.Issue) []string {
	ids := make([]string, 0, len(issue.Labels))
	for _, label := range issue.Labels {
		ids = append(ids, label.ID)
	}
	slices.Sort(ids)
	return ids
}

func issueLinkActivityMetadata(attachment domain.Attachment) map[string]string {
	source := "url"
	if parsed, err := url.Parse(attachment.URL); err == nil {
		host := strings.ToLower(parsed.Hostname())
		for _, name := range []string{"github", "gitlab"} {
			if host == name+".com" || strings.HasSuffix(host, "."+name+".com") {
				source = name
			}
		}
	}
	return map[string]string{
		"attachmentId": attachment.ID, "title": attachment.Title, "url": attachment.URL,
		"linkTitle": attachment.Title, "linkUrl": attachment.URL, "linkSource": source,
	}
}

func appendRelationActivities(data *domain.Bootstrap, relation domain.IssueRelation, eventType string) {
	metadata := func(kind, direction, target string) map[string]string {
		return map[string]string{"relationId": relation.ID, "type": kind, "relationType": kind, "direction": direction, "relatedIssueId": target}
	}
	appendActivity(data, relation.IssueID, eventType, data.Viewer, metadata(relation.Type, "outgoing", relation.RelatedIssueID))
	appendActivity(data, relation.RelatedIssueID, eventType, data.Viewer, metadata(inverseRelation(relation.Type), "inverse", relation.IssueID))
}
