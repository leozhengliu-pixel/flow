package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Recurring issues follow Linear: when an occurrence is due a new issue is
// created from the newest issue in the series and the schedule moves to the
// new issue. The previous issue keeps its content but stops recurring, so
// exactly one live issue owns Recurrence/NextOccurrenceAt at a time and the
// issue page of the newest issue always shows the schedule.

const recurringIssueSchedulerInterval = time.Minute

// runRecurringIssueScheduler is started from main and stops with ctx (server
// shutdown) or the store's worker context.
func (s *server) runRecurringIssueScheduler(ctx context.Context) {
	ticker := time.NewTicker(recurringIssueSchedulerInterval)
	defer ticker.Stop()
	// Catch up immediately after a restart instead of waiting a full interval.
	s.runRecurringIssueTick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.store.WorkerContext().Done():
			return
		case <-ticker.C:
			s.runRecurringIssueTick(ctx)
		}
	}
}

func (s *server) runRecurringIssueTick(ctx context.Context) {
	run := func() error { return s.generateRecurringIssues(ctx, time.Now().UTC()) }
	var err error
	if s.coordinator == nil {
		err = run()
	} else {
		_, err = s.coordinator.WithLeaderLock(ctx, "recurring-issues", run)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("Recurring issues: %v", err)
	}
}

func (s *server) generateRecurringIssues(ctx context.Context, now time.Time) error {
	for _, key := range s.store.WorkspaceKeys() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := s.generateWorkspaceRecurringIssues(ctx, key, now); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("Recurring issues workspace=%s: %v", key, err)
		}
	}
	return ctx.Err()
}

// generateWorkspaceRecurringIssues finds due series through the indexed
// nextOccurrenceAt attribute, so an idle workspace costs one index lookup.
func (s *server) generateWorkspaceRecurringIssues(ctx context.Context, key string, now time.Time) ([]domain.Issue, error) {
	page, err := s.store.QueryIssueRecords(ctx, store.IssueRecordQuery{Workspace: key, Limit: 100, Summary: true, Filter: store.IssueFilter{Field: "nextOccurrenceAt", Operator: "lte", Values: []string{now.UTC().Format(time.RFC3339Nano)}}})
	if err != nil {
		return nil, err
	}
	created := []domain.Issue{}
	for _, candidate := range page.Items {
		issue, err := s.createRecurringOccurrence(ctx, key, candidate.ID, now)
		if err != nil {
			log.Printf("Recurring issue workspace=%s source=%s: %v", key, candidate.ID, err)
			continue
		}
		if issue != nil {
			created = append(created, *issue)
		}
	}
	return created, nil
}

func (s *server) createRecurringOccurrence(ctx context.Context, key, sourceID string, now time.Time) (*domain.Issue, error) {
	var created *domain.Issue
	var source domain.Issue
	payload := map[string]string{"automation": "recurring", "sourceIssueId": sourceID}
	err := s.store.MutateWorkspaceWithAggregate(ctx, key, "issue.created", payload, func(data *domain.Bootstrap) (string, error) {
		issue, updated, err := generateRecurringOccurrence(data, sourceID, now)
		if err != nil {
			return "", err
		}
		created, source = issue, updated
		if issue == nil {
			return sourceID, nil
		}
		return issue.ID, nil
	})
	if err != nil {
		return nil, err
	}
	// The mutation's realtime event carries the new issue; tell clients the
	// previous issue stopped recurring as well.
	if created != nil && s.realtime != nil && source.ID != "" {
		if raw, marshalErr := json.Marshal(map[string]any{"entity": source, "recurrence": ""}); marshalErr == nil {
			s.publishRealtime(key, domain.RealtimeEvent{ID: fmt.Sprintf("evt_%d", time.Now().UnixNano()), Type: "issue.updated", AggregateID: source.ID, Payload: raw, CreatedAt: time.Now().UTC()})
		}
	}
	return created, nil
}

// generateRecurringOccurrence creates the latest due occurrence of the series
// owned by sourceID. It returns the new issue (nil when only the schedule was
// advanced) and the updated source issue. Missed periods (server downtime)
// collapse into a single issue for the latest missed occurrence date.
func generateRecurringOccurrence(data *domain.Bootstrap, sourceID string, now time.Time) (*domain.Issue, domain.Issue, error) {
	source, err := issueByID(data, sourceID)
	if err != nil {
		return nil, domain.Issue{}, store.ErrNoMutation
	}
	if source.Recurrence == "" || source.NextOccurrenceAt == nil || source.NextOccurrenceAt.After(now) || source.ArchivedAt != nil {
		return nil, domain.Issue{}, store.ErrNoMutation
	}
	settings := teamSettings(data, source.Team.ID)
	loc := teamLocation(settings.Timezone)
	rule, err := parseRecurrence(source.Recurrence)
	if err != nil {
		// An unparseable schedule would stay due forever; stop the series.
		source.Recurrence, source.NextOccurrenceAt, source.UpdatedAt = "", nil, now
		source.Version++
		appendActivity(data, source.ID, "issue.updated", source.Creator, map[string]string{"recurrence": "", "automation": "recurring"})
		return nil, *source, nil
	}
	occurrence := civilDate(*source.NextOccurrenceAt, loc)
	rule = rule.anchored(occurrence)
	for range 100000 {
		following := rule.nextAfter(occurrence)
		if occurrenceInstant(following, loc).After(now) {
			break
		}
		occurrence = following
	}
	next := occurrenceInstant(rule.nextAfter(occurrence), loc)
	series := source.RecurrenceSeriesID
	if series == "" {
		series = source.ID
	}
	date := occurrence.Format("2006-01-02")
	if slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool {
		return issue.RecurrenceSeriesID == series && issue.RecurrenceOccurrence == date
	}) {
		// Already generated (e.g. the schedule was re-enabled on an older issue):
		// only advance the schedule.
		source.Recurrence, source.NextOccurrenceAt, source.RecurrenceSeriesID, source.UpdatedAt = storedRecurrence(source.Recurrence, rule), &next, series, now
		source.Version++
		return nil, *source, nil
	}
	teamIndex := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == source.Team.ID })
	if teamIndex < 0 || data.Teams[teamIndex].ArchivedAt != nil || data.Teams[teamIndex].RetiredAt != nil {
		return nil, domain.Issue{}, store.ErrNoMutation
	}
	team := data.Teams[teamIndex]
	state := stateForTeam(data, team.ID, settings.DefaultStateID)
	if state == nil {
		states := statesForTeam(data, team.ID)
		if len(states) == 0 {
			return nil, domain.Issue{}, errInvalid
		}
		state = &states[0]
	}
	number := max(data.NextIssueNumber, nextIssueNumber(data.Issues))
	created := domain.Issue{
		ID: fmt.Sprintf("issue_%d", number), Version: 1, Identifier: fmt.Sprintf("%s-%d", team.Key, number), Number: number,
		Title: source.Title, Description: source.Description, DescriptionState: source.DescriptionState,
		Priority: source.Priority, PriorityLabel: priorityLabel(source.Priority), SortOrder: float64(number),
		DueDate: &date, CreatedAt: now, UpdatedAt: now, Team: team, State: *state, Creator: source.Creator,
		Labels: labelsByID(data, issueLabelIDs(source.Labels)), SubscriberIDs: []string{source.Creator.ID},
		Reactions: map[string][]string{}, SubIssueIDs: []string{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}, SuggestedLabelIDs: []string{},
		Recurrence: storedRecurrence(source.Recurrence, rule), NextOccurrenceAt: &next, RecurrenceSeriesID: series, RecurrenceOccurrence: date,
	}
	if source.Estimate != nil {
		estimate := *source.Estimate
		created.Estimate = &estimate
	}
	if source.Assignee != nil {
		created.Assignee = userByID(data, source.Assignee.ID)
		if created.Assignee != nil {
			created.SubscriberIDs = appendUnique(created.SubscriberIDs, created.Assignee.ID)
		}
	}
	if source.Project != nil {
		created.Project = projectByID(data, source.Project.ID)
	}
	if source.DocumentContent != nil {
		// A fresh document generation: the copy shares no CRDT history.
		content := *source.DocumentContent
		content.ID, content.Version, content.ContentState, content.UpdatedAt = "document_content_"+newCollaborationID(), 1, "", now
		content.ContentData = maps.Clone(source.DocumentContent.ContentData)
		created.DocumentContent = &content
	}
	// Linear's recurring issues skip triage: a backlog default in a triage team
	// is marked triaged so the copy lands in the backlog, not the triage queue.
	if settings.TriageEnabled && created.State.Type == "backlog" {
		created.TriagedAt = &now
	}
	applyCycleAutomation(data, &created)
	applySLARules(data, &created, now)
	sourceIdentifier := source.Identifier
	source.Recurrence, source.NextOccurrenceAt, source.RecurrenceSeriesID, source.UpdatedAt = "", nil, series, now
	source.Version++
	updatedSource := *source
	data.Issues = append([]domain.Issue{created}, data.Issues...)
	appendActivity(data, created.ID, "issue.created", created.Creator, map[string]string{"stateId": created.State.ID, "state": created.State.Name, "automation": "recurring", "recurringFrom": sourceIdentifier, "recurringFromId": sourceID})
	appendActivity(data, sourceID, "issue.updated", created.Creator, map[string]string{"recurrence": "", "automation": "recurring", "recurringNext": created.Identifier, "recurringNextId": created.ID})
	return &created, updatedSource, nil
}

// storedRecurrence keeps the preset names clients and MCP tools already use
// ("daily", "weekdays", "weekly", "biweekly"): stepping them from the current
// occurrence never drifts. "monthly"/"yearly" are stored in their anchored
// RRULE form so Jan 31 -> Feb 28 -> Mar 31 keeps the original day.
func storedRecurrence(value string, anchored recurrenceRule) string {
	preset := strings.ToLower(strings.TrimSpace(value))
	if slices.Contains([]string{"daily", "weekdays", "weekly", "biweekly"}, preset) {
		return preset
	}
	return anchored.String()
}

func issueLabelIDs(labels []domain.IssueLabel) []string {
	ids := make([]string, len(labels))
	for i, label := range labels {
		ids[i] = label.ID
	}
	return ids
}

// applyRecurrenceUpdate validates and normalises recurrence input on create
// and update. A schedule change without an explicit start computes the first
// occurrence after today in the team timezone; "" stops recurring.
func applyRecurrenceUpdate(data *domain.Bootstrap, issue *domain.Issue, recurrence, nextOccurrence *string, now time.Time, changes map[string]string) error {
	if recurrence == nil && nextOccurrence == nil {
		return nil
	}
	loc := teamLocation(teamSettings(data, issue.Team.ID).Timezone)
	today := civilDate(now, loc)
	schedule := issue.Recurrence
	if recurrence != nil {
		schedule = strings.TrimSpace(*recurrence)
	}
	explicit := nextOccurrence != nil && strings.TrimSpace(*nextOccurrence) != ""
	if schedule == "" {
		if explicit {
			return fmt.Errorf("%w: set a recurrence schedule before its next occurrence", errInvalid)
		}
		if issue.Recurrence != "" || issue.NextOccurrenceAt != nil {
			changes["recurrence"] = ""
		}
		issue.Recurrence, issue.NextOccurrenceAt = "", nil
		return nil
	}
	if recurrence != nil && !explicit && schedule == issue.Recurrence && issue.NextOccurrenceAt != nil {
		return nil
	}
	rule, err := parseRecurrence(schedule)
	if err != nil {
		return err
	}
	var first time.Time
	if explicit {
		start, err := parseRecurrenceDate(*nextOccurrence, loc)
		if err != nil {
			return err
		}
		if start.Before(today) {
			start = today
		}
		rule = rule.anchored(start)
		first = rule.firstOnOrAfter(start)
	} else if rule.needsAnchor() {
		// "Weekly" made today repeats on today's weekday, starting next week.
		rule = rule.anchored(today)
		first = rule.nextAfter(today)
	} else {
		first = rule.firstOnOrAfter(today.AddDate(0, 0, 1))
	}
	next := occurrenceInstant(first, loc)
	issue.Recurrence, issue.NextOccurrenceAt = storedRecurrence(schedule, rule), &next
	changes["recurrence"] = issue.Recurrence
	changes["nextOccurrenceAt"] = next.Format(time.RFC3339)
	return nil
}
