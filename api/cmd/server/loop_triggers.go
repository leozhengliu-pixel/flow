package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
)

// Linear's trigger events per entity type (see docs/verification/loops-api-contract.md).
var loopTriggerEvents = map[string][]string{
	"issue":      {"created", "updated", "triage", "status", "priority", "assignee", "agent", "project", "team", "labels", "comment", "customerRequest"},
	"project":    {"created", "updated", "status", "update"},
	"initiative": {"created", "updated", "status", "update"},
	"release":    {"created", "updated", "status"},
	"team":       {"created", "updated"},
	"cycle":      {"created", "updated", "started", "completed"},
}

var loopFilterFields = []string{"status", "priority", "assignee", "label", "project", "team", "creator"}

var loopWeekdays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// normalizeLoopTriggerConfig maps stored trigger configurations, including the
// legacy {action, filter*} and {starting} shapes, onto the current shape.
func normalizeLoopTriggerConfig(triggerType string, config map[string]any) map[string]any {
	normalized := map[string]any{}
	for key, value := range config {
		normalized[key] = value
	}
	if triggerType == "schedule" {
		if _, ok := normalized["startDate"]; !ok {
			if starting, ok := normalized["starting"].(string); ok && starting != "" {
				normalized["startDate"] = starting
			}
		}
		delete(normalized, "starting")
		if _, ok := normalized["interval"]; !ok {
			normalized["interval"] = 1
		}
		if _, ok := normalized["unit"]; !ok {
			normalized["unit"] = "day"
		}
		if _, ok := normalized["time"]; !ok {
			normalized["time"] = "10:00"
		}
		for _, key := range []string{"action", "event", "value", "filters", "filter", "filterField", "filterOperator", "filterValue"} {
			delete(normalized, key)
		}
		return normalized
	}
	for _, key := range []string{"startDate", "interval", "unit", "time", "weekdays", "timezone"} {
		delete(normalized, key)
	}
	if event, _ := normalized["event"].(string); event == "" {
		action, _ := normalized["action"].(string)
		switch {
		case action == "created" || action == "" && triggerType == "cycle":
			normalized["event"] = "created"
		default:
			// Legacy "created or updated" loops keep firing on creation too.
			normalized["event"] = "updated"
			normalized["includeCreated"] = true
		}
	}
	delete(normalized, "action")
	if _, legacy := normalized["filter"]; legacy {
		value := strings.TrimSpace(fmt.Sprint(normalized["filterValue"]))
		if normalized["filterValue"] != nil && value != "" {
			field, _ := normalized["filterField"].(string)
			if field == "" {
				field = "status"
			}
			operator, _ := normalized["filterOperator"].(string)
			if operator != "isNot" {
				operator = "is"
			}
			filters, _ := normalized["filters"].([]any)
			normalized["filters"] = append(filters, map[string]any{"field": field, "operator": operator, "value": value})
		}
	}
	for _, key := range []string{"filter", "filterField", "filterOperator", "filterValue"} {
		delete(normalized, key)
	}
	return normalized
}

// validateLoopTriggerConfig checks a normalized trigger configuration.
func validateLoopTriggerConfig(triggerType string, config map[string]any) error {
	if triggerType == "schedule" {
		if value, ok := config["startDate"].(string); ok && value != "" {
			if !validDate(value) {
				return fmt.Errorf("startDate must be YYYY-MM-DD")
			}
		}
		if unit, ok := config["unit"].(string); ok && !slices.Contains([]string{"hour", "day", "week", "month"}, unit) {
			return fmt.Errorf("unit must be hour, day, week, or month")
		}
		if value, ok := config["time"].(string); ok && value != "" && !validClock(value) {
			return fmt.Errorf("time must be HH:MM")
		}
		if raw, ok := config["weekdays"]; ok && raw != nil {
			days, ok := raw.([]any)
			if !ok {
				return fmt.Errorf("weekdays must be a list")
			}
			for _, day := range days {
				if value, ok := day.(string); !ok || !slices.Contains(loopWeekdays, strings.ToLower(value)) {
					return fmt.Errorf("weekdays must use sun, mon, tue, wed, thu, fri, sat")
				}
			}
		}
		return nil
	}
	event, _ := config["event"].(string)
	if !slices.Contains(loopTriggerEvents[triggerType], event) {
		return fmt.Errorf("event must be one of %s for %s triggers", strings.Join(loopTriggerEvents[triggerType], ", "), triggerType)
	}
	if raw, ok := config["filters"]; ok && raw != nil {
		filters, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("filters must be a list")
		}
		for _, item := range filters {
			filter, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("each filter needs field, operator and value")
			}
			if field, _ := filter["field"].(string); !slices.Contains(loopFilterFields, field) {
				return fmt.Errorf("filter field must be one of %s", strings.Join(loopFilterFields, ", "))
			}
			if operator, _ := filter["operator"].(string); operator != "is" && operator != "isNot" {
				return fmt.Errorf("filter operator must be is or isNot")
			}
		}
	}
	return nil
}

func validDate(value string) bool {
	_, err := parseLoopDate(value)
	return err == nil
}

func validClock(value string) bool {
	_, _, ok := parseLoopClock(value)
	return ok
}

// loopEvent is a domain event resolved to the entity a loop trigger listens on.
type loopEvent struct {
	EntityType string // issue | project | initiative | release | team | cycle
	EntityID   string
	Kind       string // created | updated | comment | customerRequest | update | started | completed
	Issue      *domain.Issue
	Previous   map[string]json.RawMessage
}

// resolveLoopEvent maps a committed domain event onto a loop trigger entity.
// loopEventCandidate reports whether an event type can trigger a loop at all
// (the cases resolveLoopEvent accepts), so that other writes skip the
// workspace snapshot.
func loopEventCandidate(event domain.DomainEvent) bool {
	entityType, action, found := strings.Cut(event.Type, ".")
	if !found || event.AggregateID == "" {
		return false
	}
	switch {
	case event.Type == "comment.created", event.Type == "customer_request.created", event.Type == "customer_request.updated":
		return true
	case (entityType == "project" || entityType == "initiative") && action == "update_created":
		return true
	case entityType == "issue" || entityType == "project" || entityType == "initiative" || entityType == "release" || entityType == "team":
		return action == "created" || action == "updated"
	case entityType == "cycle":
		return action == "created" || action == "updated" || action == "started" || action == "completed"
	}
	return false
}

func (s *server) resolveLoopEvent(workspace string, data domain.Bootstrap, event domain.DomainEvent) (loopEvent, bool) {
	entityType, action, found := strings.Cut(event.Type, ".")
	if !found || event.AggregateID == "" || !loopEventCandidate(event) {
		return loopEvent{}, false
	}
	previous := map[string]json.RawMessage{}
	if len(event.PreviousValues) > 0 {
		_ = json.Unmarshal(event.PreviousValues, &previous)
	}
	result := loopEvent{EntityType: entityType, EntityID: event.AggregateID, Kind: action, Previous: previous}
	switch {
	case event.Type == "comment.created":
		result.EntityType, result.Kind = "issue", "comment"
	case event.Type == "customer_request.created" || event.Type == "customer_request.updated":
		var request *domain.CustomerRequest
		for index := range data.CustomerRequests {
			if data.CustomerRequests[index].ID == event.AggregateID {
				request = &data.CustomerRequests[index]
			}
		}
		if request == nil || request.IssueID == "" {
			return loopEvent{}, false
		}
		if event.Type == "customer_request.updated" {
			if _, moved := previous["issueId"]; !moved {
				return loopEvent{}, false
			}
		}
		result = loopEvent{EntityType: "issue", EntityID: request.IssueID, Kind: "customerRequest"}
	case (entityType == "project" || entityType == "initiative") && action == "update_created":
		result.Kind = "update"
	case entityType == "issue" || entityType == "project" || entityType == "initiative" || entityType == "release" || entityType == "team":
		if action != "created" && action != "updated" {
			return loopEvent{}, false
		}
	case entityType == "cycle":
		if action != "created" && action != "updated" && action != "started" && action != "completed" {
			return loopEvent{}, false
		}
	default:
		return loopEvent{}, false
	}
	if result.EntityType == "issue" {
		record, err := s.store.IssueRecord(context.Background(), workspace, result.EntityID)
		if err != nil {
			return loopEvent{}, false
		}
		result.Issue = &record
	}
	return result, true
}

// loopTriggerReason says why an event started a run: a stable code the web
// app translates and, for property changes, the new value (empty for none).
type loopTriggerReason struct {
	Code  string
	Value string
}

// loopTriggerReasonTexts are the English trigger reasons by code; {value} is
// the new value. The web app mirrors this table (TRIGGER_REASON_TEXTS in
// web/src/components/loops/loop-run-labels.ts) to translate run triggers.
var loopTriggerReasonTexts = map[string]string{
	"created": "created", "updated": "updated", "comment": "new comment", "customerRequest": "new customer request",
	"triage": "entering triage", "status": "status → {value}", "statusChanged": "status changed",
	"priority": "priority → {value}", "assignee": "assignee → {value}", "agent": "agent → {value}",
	"project": "project → {value}", "team": "team → {value}", "label": "label {value} added",
	"update": "new update", "started": "started", "completed": "completed",
}

// loopTriggerReasonNone is the value shown when a property was cleared.
var loopTriggerReasonNone = map[string]string{"assignee": "No assignee", "agent": "No agent", "project": "No project"}

func (reason loopTriggerReason) text() string {
	value := reason.Value
	if value == "" {
		value = loopTriggerReasonNone[reason.Code]
	}
	return strings.Replace(loopTriggerReasonTexts[reason.Code], "{value}", value, 1)
}

// label is the English trigger label stored on the run ("Triggered by DEV-24
// entering triage"); clients that know the reason code translate it instead.
func (reason loopTriggerReason) label(entityName string) string {
	return strings.TrimSpace("Triggered by " + entityName + " " + reason.text())
}

// loopEventMatches reports whether a loop's trigger fires for an event and, if
// so, how the run should describe its trigger.
func loopEventMatches(data domain.Bootstrap, loop domain.Loop, event loopEvent) (bool, string) {
	matched, reason := matchLoopEvent(data, loop, event)
	if !matched {
		return false, ""
	}
	return true, reason.label(loopEventEntityName(data, event))
}

// matchLoopEvent reports whether a loop's trigger fires for an event and why.
func matchLoopEvent(data domain.Bootstrap, loop domain.Loop, event loopEvent) (bool, loopTriggerReason) {
	if loop.TriggerType != event.EntityType {
		return false, loopTriggerReason{}
	}
	config := normalizeLoopTriggerConfig(loop.TriggerType, loop.TriggerConfig)
	want, _ := config["event"].(string)
	if include, _ := config["includeCreated"].(bool); include && want == "updated" && event.Kind == "created" {
		want = "created"
	}
	value, hasValue := config["value"]
	if text, ok := value.(string); ok && (text == "" || strings.EqualFold(text, "any")) {
		hasValue = false
	}
	if event.Issue != nil {
		issue := *event.Issue
		matched, reason := loopIssueEventMatches(data, issue, event, want, value, hasValue)
		if !matched || !loopFiltersMatch(data, config, issue) {
			return false, loopTriggerReason{}
		}
		return true, reason
	}
	switch want {
	case "created", "update", "started", "completed":
		if event.Kind != want {
			return false, loopTriggerReason{}
		}
	case "updated":
		if event.Kind != "updated" {
			return false, loopTriggerReason{}
		}
	case "status":
		if event.Kind != "updated" {
			return false, loopTriggerReason{}
		}
		_, status := event.Previous["status"]
		_, stage := event.Previous["stage"]
		if !status && !stage {
			return false, loopTriggerReason{}
		}
	default:
		return false, loopTriggerReason{}
	}
	if want == "status" {
		return true, loopTriggerReason{Code: "statusChanged"}
	}
	return true, loopTriggerReason{Code: want}
}

func loopIssueEventMatches(data domain.Bootstrap, issue domain.Issue, event loopEvent, want string, value any, hasValue bool) (bool, loopTriggerReason) {
	previous := event.Previous
	changed := func(key string) bool { _, ok := previous[key]; return ok }
	switch want {
	case "created":
		return event.Kind == "created", loopTriggerReason{Code: "created"}
	case "updated":
		return event.Kind == "updated", loopTriggerReason{Code: "updated"}
	case "comment":
		return event.Kind == "comment", loopTriggerReason{Code: "comment"}
	case "customerRequest":
		return event.Kind == "customerRequest", loopTriggerReason{Code: "customerRequest"}
	case "triage":
		if !loopIssueInTriage(data, issue) {
			return false, loopTriggerReason{}
		}
		if event.Kind == "created" {
			return true, loopTriggerReason{Code: "triage"}
		}
		if event.Kind != "updated" || !changed("state") && !changed("triagedAt") && !changed("team") {
			return false, loopTriggerReason{}
		}
		return !loopIssueWasInTriage(data, issue, previous), loopTriggerReason{Code: "triage"}
	}
	if event.Kind != "updated" && !(event.Kind == "created" && want != "team") {
		return false, loopTriggerReason{}
	}
	// On creation a property "changes" to its initial value only when it is set.
	created := event.Kind == "created"
	switch want {
	case "status":
		if !created && !changed("state") {
			return false, loopTriggerReason{}
		}
		if created && !hasValue {
			return false, loopTriggerReason{}
		}
		return !hasValue || loopStatusMatches(data, issue, fmt.Sprint(value)), loopTriggerReason{Code: "status", Value: loopStatusName(data, issue)}
	case "priority":
		if !created && !changed("priority") || created && issue.Priority == 0 {
			return false, loopTriggerReason{}
		}
		return !hasValue || loopPriorityMatches(issue, value), loopTriggerReason{Code: "priority", Value: firstNonEmpty(issue.PriorityLabel, strconv.Itoa(issue.Priority))}
	case "assignee":
		if !created && !changed("assignee") || created && issue.Assignee == nil {
			return false, loopTriggerReason{}
		}
		return !hasValue || loopUserMatches(issue.Assignee, value), loopTriggerReason{Code: "assignee", Value: loopUserName(issue.Assignee, "")}
	case "agent":
		if !created && !changed("delegate") || created && issue.Delegate == nil {
			return false, loopTriggerReason{}
		}
		return !hasValue || loopUserMatches(issue.Delegate, value), loopTriggerReason{Code: "agent", Value: loopUserName(issue.Delegate, "")}
	case "project":
		if !created && !changed("project") || created && issue.Project == nil {
			return false, loopTriggerReason{}
		}
		reason := loopTriggerReason{Code: "project"}
		if issue.Project != nil {
			reason.Value = issue.Project.Name
		}
		if !hasValue {
			return true, reason
		}
		if value == nil {
			return issue.Project == nil, reason
		}
		return issue.Project != nil && (issue.Project.ID == fmt.Sprint(value) || strings.EqualFold(issue.Project.Name, fmt.Sprint(value))), reason
	case "team":
		if !changed("team") {
			return false, loopTriggerReason{}
		}
		return !hasValue || issue.Team.ID == fmt.Sprint(value) || strings.EqualFold(issue.Team.Key, fmt.Sprint(value)), loopTriggerReason{Code: "team", Value: issue.Team.Name}
	case "labels":
		added := loopAddedLabels(issue, previous, created)
		if len(added) == 0 {
			return false, loopTriggerReason{}
		}
		for _, label := range added {
			if !hasValue || label.ID == fmt.Sprint(value) || strings.EqualFold(label.Name, fmt.Sprint(value)) {
				return true, loopTriggerReason{Code: "label", Value: label.Name}
			}
		}
	}
	return false, loopTriggerReason{}
}

func loopIssueInTriage(data domain.Bootstrap, issue domain.Issue) bool {
	return issue.State.Type == "triage" || isTriageIssue(&data, &issue)
}

// loopIssueWasInTriage rebuilds the issue's triage membership before the update.
func loopIssueWasInTriage(data domain.Bootstrap, issue domain.Issue, previous map[string]json.RawMessage) bool {
	before := issue
	if raw, ok := previous["state"]; ok {
		var state domain.WorkflowState
		if json.Unmarshal(raw, &state) == nil {
			before.State = state
		}
	}
	if raw, ok := previous["triagedAt"]; ok {
		before.TriagedAt = nil
		_ = json.Unmarshal(raw, &before.TriagedAt)
	}
	if raw, ok := previous["team"]; ok {
		var team domain.Team
		if json.Unmarshal(raw, &team) == nil && team.ID != "" {
			before.Team = team
		}
	}
	return loopIssueInTriage(data, before)
}

func loopAddedLabels(issue domain.Issue, previous map[string]json.RawMessage, created bool) []domain.IssueLabel {
	if created {
		return issue.Labels
	}
	raw, ok := previous["labels"]
	if !ok {
		return nil
	}
	var before []domain.IssueLabel
	_ = json.Unmarshal(raw, &before)
	added := []domain.IssueLabel{}
	for _, label := range issue.Labels {
		if !slices.ContainsFunc(before, func(item domain.IssueLabel) bool { return item.ID == label.ID }) {
			added = append(added, label)
		}
	}
	return added
}

func loopStatusName(data domain.Bootstrap, issue domain.Issue) string {
	if loopIssueInTriage(data, issue) {
		return "Triage"
	}
	return issue.State.Name
}

func loopStatusMatches(data domain.Bootstrap, issue domain.Issue, want string) bool {
	if strings.EqualFold(want, "triage") {
		return loopIssueInTriage(data, issue)
	}
	return issue.State.ID == want || strings.EqualFold(issue.State.Name, want) || strings.EqualFold(issue.State.Type, want)
}

func loopPriorityMatches(issue domain.Issue, value any) bool {
	text := strings.TrimSpace(fmt.Sprint(value))
	if number, ok := value.(float64); ok {
		text = strconv.Itoa(int(number))
	}
	return text == strconv.Itoa(issue.Priority) || strings.EqualFold(text, issue.PriorityLabel)
}

func loopUserMatches(user *domain.User, value any) bool {
	if value == nil {
		return user == nil
	}
	want := fmt.Sprint(value)
	return user != nil && (user.ID == want || strings.EqualFold(user.Email, want) || strings.EqualFold(user.Name, want) || strings.EqualFold(user.DisplayName, want))
}

func loopUserName(user *domain.User, empty string) string {
	if user == nil {
		return empty
	}
	return firstNonEmpty(user.DisplayName, user.Name, user.Email)
}

// loopFiltersMatch applies the trigger's "+ Add filter" conditions to the issue.
func loopFiltersMatch(data domain.Bootstrap, config map[string]any, issue domain.Issue) bool {
	filters, _ := config["filters"].([]any)
	for _, item := range filters {
		filter, ok := item.(map[string]any)
		if !ok {
			continue
		}
		field, _ := filter["field"].(string)
		value := filter["value"]
		text := ""
		if value != nil {
			text = fmt.Sprint(value)
		}
		matched := false
		switch field {
		case "status":
			matched = value != nil && loopStatusMatches(data, issue, text)
		case "priority":
			matched = value != nil && loopPriorityMatches(issue, value)
		case "assignee":
			matched = loopUserMatches(issue.Assignee, value)
		case "creator":
			creator := issue.Creator
			matched = loopUserMatches(&creator, value)
		case "label":
			matched = slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool {
				return value != nil && (label.ID == text || strings.EqualFold(label.Name, text))
			})
			if value == nil {
				matched = len(issue.Labels) == 0
			}
		case "project":
			if value == nil {
				matched = issue.Project == nil
			} else {
				matched = issue.Project != nil && (issue.Project.ID == text || strings.EqualFold(issue.Project.Name, text))
			}
		case "team":
			matched = issue.Team.ID == text || strings.EqualFold(issue.Team.Key, text)
		default:
			continue
		}
		if operator, _ := filter["operator"].(string); operator == "isNot" {
			matched = !matched
		}
		if !matched {
			return false
		}
	}
	return true
}

func loopEventEntityName(data domain.Bootstrap, event loopEvent) string {
	if event.Issue != nil {
		return event.Issue.Identifier
	}
	switch event.EntityType {
	case "project":
		for _, project := range data.Projects {
			if project.ID == event.EntityID {
				return project.Name
			}
		}
	case "initiative":
		for _, initiative := range data.Initiatives {
			if initiative.ID == event.EntityID {
				return initiative.Name
			}
		}
	case "release":
		for _, release := range data.Releases {
			if release.ID == event.EntityID {
				return release.Name
			}
		}
	case "team":
		for _, team := range data.Teams {
			if team.ID == event.EntityID {
				return team.Name
			}
		}
	case "cycle":
		for _, cycle := range data.Cycles {
			if cycle.ID == event.EntityID {
				return firstNonEmpty(cycle.Name, "Cycle "+strconv.Itoa(cycle.Number))
			}
		}
	}
	return event.EntityType
}

// dispatchLoopTriggers starts entity-triggered loops that match a domain event.
func (s *server) dispatchLoopTriggers(workspace string, event domain.DomainEvent) {
	// Runs synchronously after every mutation; most events cannot trigger a
	// loop and most workspaces have no event loops, so check both before
	// cloning the workspace metadata.
	if !loopEventCandidate(event) {
		return
	}
	if loops, ok := s.store.WorkspaceMetadataFields(workspace, "loops"); !ok || !slices.ContainsFunc(loops.Loops, func(loop domain.Loop) bool { return loopLive(loop) && loop.TriggerType != "schedule" }) {
		return
	}
	data, ok := s.store.WorkspaceMetadata(workspace)
	if !ok || !slices.ContainsFunc(data.Loops, func(loop domain.Loop) bool { return loopLive(loop) && loop.TriggerType != "schedule" }) {
		return
	}
	resolved, ok := s.resolveLoopEvent(workspace, data, event)
	if !ok {
		return
	}
	sourceKey, fromLoop := loopEventSource(data, resolved.Issue, resolved.Kind)
	if fromLoop {
		return
	}
	for _, loop := range data.Loops {
		if !loopLive(loop) || loop.TriggerType != resolved.EntityType {
			continue
		}
		matched, reason := matchLoopEvent(data, loop, resolved)
		if !matched {
			continue
		}
		teamIDs := loopEntityTeams(data, resolved.EntityType, resolved.EntityID, resolved.Issue)
		if !loopScopeMatches(data, loop, teamIDs) {
			continue
		}
		if sourceKey != "" && !loopSourceTrusted(data.WorkspaceSettings, loop, sourceKey) {
			continue
		}
		trigger := loopTrigger{Kind: "event", EventType: event.Type, EntityType: resolved.EntityType, EntityID: resolved.EntityID, SourceKey: sourceKey, Label: reason.label(loopEventEntityName(data, resolved)), Reason: reason}
		if _, err := s.startLoopRun(workspace, loop.ID, trigger, ""); err != nil && !errors.Is(err, errConflict) && !errors.Is(err, errLoopUnavailable) {
			log.Printf("Loop trigger workspace=%s loop=%s: %v", workspace, loop.ID, err)
		}
	}
}

// loopLive reports whether a loop is published and enabled.
func loopLive(loop domain.Loop) bool {
	return loop.Enabled && loop.Status != "draft"
}

func loopEntityTeams(data domain.Bootstrap, entityType, id string, issue *domain.Issue) []string {
	switch entityType {
	case "issue":
		if issue != nil {
			return []string{issue.Team.ID}
		}
	case "project":
		for _, project := range data.Projects {
			if project.ID == id {
				return project.TeamIDs
			}
		}
	case "cycle":
		for _, cycle := range data.Cycles {
			if cycle.ID == id {
				return []string{cycle.TeamID}
			}
		}
	case "team":
		return []string{id}
	}
	return nil
}

// loopScopeMatches applies the loop's team, the trigger's team scope and the
// loop's team access to the teams an entity belongs to.
func loopScopeMatches(data domain.Bootstrap, loop domain.Loop, teamIDs []string) bool {
	if len(teamIDs) == 0 {
		return loop.TriggerType == "initiative" || loop.TriggerType == "project" || loop.TriggerType == "release"
	}
	allowed := loopTeams(data, loop)
	scoped := loopConfigStrings(loop.TriggerConfig, "teamIds")
	return slices.ContainsFunc(teamIDs, func(teamID string) bool {
		if !allowed[teamID] && loop.TriggerType != "team" {
			return false
		}
		if loop.Level == "team" && loop.TeamID != "" && teamID != loop.TeamID {
			return false
		}
		return len(scoped) == 0 || slices.Contains(scoped, teamID)
	})
}

func loopConfigStrings(config map[string]any, key string) []string {
	values := []string{}
	switch raw := config[key].(type) {
	case []any:
		for _, item := range raw {
			if value, ok := item.(string); ok && value != "" {
				values = append(values, value)
			}
		}
	case []string:
		values = append(values, raw...)
	}
	return values
}

// loopEventSource names the external source behind an issue event, if any.
// Issues created by a loop run never trigger loops.
func loopEventSource(data domain.Bootstrap, issue *domain.Issue, action string) (key string, fromLoop bool) {
	if issue == nil || action != "created" {
		return "", false
	}
	for _, ask := range data.Asks {
		if ask.IssueID != issue.ID {
			continue
		}
		if ask.Source == "loop" {
			return "", true
		}
		if ask.Source != "" && ask.Source != "web" {
			return "integration:" + ask.Source, false
		}
	}
	for _, member := range data.Members {
		if member.User.ID == issue.Creator.ID && member.Role == "app" {
			return "appUser:" + member.User.ID, false
		}
	}
	return "", false
}

func loopSourceTrusted(settings domain.WorkspaceSettings, loop domain.Loop, key string) bool {
	if !settings.ExternalLoopTriggers {
		return false
	}
	if slices.Contains(loop.TrustedSourceKeys, key) {
		return true
	}
	return settings.TrustedSourcesMode == "allowlist" && slices.Contains(settings.TrustedSourcesAllowlist, key)
}
