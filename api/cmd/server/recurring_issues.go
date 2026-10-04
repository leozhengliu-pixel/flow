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

// Recurring issues follow Linear: a recurring issue is the current instance
// of its series, due on Issue.DueDate. Once that due date has passed (00:01
// the following day in the team's time zone, Issue.NextOccurrenceAt) the
// scheduler creates the next instance, due on the next date of the cadence,
// recreating the sub-issues, and the schedule moves to the new issue. The
// previous issue keeps its content but stops recurring, so exactly one live
// issue owns Recurrence/NextOccurrenceAt at a time.

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
// nextOccurrenceAt attribute, so an idle workspace costs one index range read.
// The first run per workspace and process also upgrades schedules written by
// the previous timing model (see normalizeLegacyRecurringIssues).
func (s *server) generateWorkspaceRecurringIssues(ctx context.Context, key string, now time.Time) ([]domain.Issue, error) {
	if _, done := s.recurringNormalized.Load(key); !done {
		if err := s.normalizeLegacyRecurringIssues(ctx, key, now); err != nil {
			log.Printf("Recurring issues normalize workspace=%s: %v", key, err)
		} else {
			s.recurringNormalized.Store(key, true)
		}
	}
	ids, err := s.store.RecurringIssueIDs(ctx, key, now, 100)
	if err != nil {
		return nil, err
	}
	created := []domain.Issue{}
	for _, id := range ids {
		issue, err := s.createRecurringOccurrence(ctx, key, id, now)
		if err != nil {
			log.Printf("Recurring issue workspace=%s source=%s: %v", key, id, err)
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
	var children []domain.Issue
	payload := map[string]string{"automation": "recurring", "sourceIssueId": sourceID}
	err := s.store.MutateWorkspaceWithAggregate(recurringIssueScope(ctx, sourceID, now), key, "issue.created", payload, func(data *domain.Bootstrap) (string, error) {
		issue, subIssues, updated, err := generateRecurringOccurrence(data, sourceID, now)
		if err != nil {
			return "", err
		}
		created, children, source = issue, subIssues, updated
		if issue == nil {
			return sourceID, nil
		}
		return issue.ID, nil
	})
	if err != nil {
		return nil, err
	}
	// The mutation's realtime event carries the new issue; tell clients the
	// previous issue stopped recurring and about the recreated sub-issues.
	if created != nil && source.ID != "" {
		s.publishIssueEntity(key, "issue.updated", source, map[string]any{"recurrence": "", "automation": "recurring"})
		for _, child := range children {
			s.publishIssueEntity(key, "issue.created", child, map[string]any{"automation": "recurring", "parentId": created.ID})
		}
	}
	return created, nil
}

// publishIssueEntity sends a realtime event for an issue a mutation changed
// besides its aggregate.
func (s *server) publishIssueEntity(key, eventType string, issue domain.Issue, fields map[string]any) {
	if s.realtime == nil {
		return
	}
	body := maps.Clone(fields)
	if body == nil {
		body = map[string]any{}
	}
	body["entity"] = issue
	raw, err := json.Marshal(body)
	if err != nil {
		return
	}
	s.publishRealtime(key, domain.RealtimeEvent{ID: fmt.Sprintf("evt_%d", time.Now().UnixNano()), Type: eventType, AggregateID: issue.ID, Payload: raw, CreatedAt: time.Now().UTC()})
}

// issueDueDate parses the issue's due date (a calendar date).
func issueDueDate(issue domain.Issue) (time.Time, bool) {
	if issue.DueDate == nil {
		return time.Time{}, false
	}
	date, err := time.Parse("2006-01-02", strings.TrimSpace(*issue.DueDate))
	return date, err == nil
}

// normalizeRecurrenceTiming upgrades a schedule written by the previous
// timing model, where an instance was created at local midnight of its
// occurrence date and NextOccurrenceAt was that midnight: the next occurrence
// date becomes the due date of an issue without one, and NextOccurrenceAt
// becomes 00:01 after the due date. It reports whether the issue changed.
func normalizeRecurrenceTiming(issue *domain.Issue, loc *time.Location) bool {
	if issue.Recurrence == "" {
		if issue.NextOccurrenceAt == nil {
			return false
		}
		issue.NextOccurrenceAt = nil
		return true
	}
	due, hasDue := issueDueDate(*issue)
	if hasDue && issue.NextOccurrenceAt != nil && currentRecurrenceTiming(*issue.NextOccurrenceAt, due, loc) {
		return false
	}
	if !hasDue {
		if issue.NextOccurrenceAt == nil {
			return false
		}
		due = civilDate(*issue.NextOccurrenceAt, loc)
		value := due.Format("2006-01-02")
		issue.DueDate = &value
	}
	next := recurrenceCreationInstant(due, loc)
	issue.NextOccurrenceAt = &next
	return true
}

// normalizeLegacyRecurringIssues applies normalizeRecurrenceTiming to every
// recurring issue of the workspace. Recurring issues are found through the
// nextOccurrenceAt index and read in bounded batches; only issues that still
// need the upgrade are written, one scoped issue mutation each, so the pass is
// idempotent and a no-op once every schedule uses the current model.
func (s *server) normalizeLegacyRecurringIssues(ctx context.Context, key string, now time.Time) error {
	ids, err := s.store.RecurringIssueIDs(ctx, key, time.Time{}, 0)
	if err != nil {
		return err
	}
	locations := map[string]*time.Location{}
	location := func(teamID string) *time.Location {
		if loc, ok := locations[teamID]; ok {
			return loc
		}
		settings, _ := s.store.TeamSettingsFor(key, teamID)
		loc := teamLocation(settings.Timezone)
		locations[teamID] = loc
		return loc
	}
	for start := 0; start < len(ids); start += 200 {
		page, err := s.store.QueryIssueRecords(ctx, store.IssueRecordQuery{Workspace: key, IssueIDs: ids[start:min(start+200, len(ids))], RestrictToIssueIDs: true, Archived: "all", Summary: true, Limit: 500})
		if err != nil {
			return err
		}
		for _, candidate := range page.Items {
			if !normalizeRecurrenceTiming(&candidate, location(candidate.Team.ID)) {
				continue
			}
			id := candidate.ID
			var updated domain.Issue
			err := s.store.MutateWorkspace(withIssueScope(ctx, &id), key, "issue.updated", id, map[string]string{"automation": "recurring"}, func(data *domain.Bootstrap) error {
				issue, err := issueByID(data, id)
				if err != nil {
					return store.ErrNoMutation
				}
				if !normalizeRecurrenceTiming(issue, teamLocation(teamSettings(data, issue.Team.ID).Timezone)) {
					return store.ErrNoMutation
				}
				issue.UpdatedAt = now
				issue.Version++
				updated = *issue
				return nil
			})
			if err != nil {
				return err
			}
			if updated.ID != "" {
				next := "none"
				if updated.NextOccurrenceAt != nil {
					next = updated.NextOccurrenceAt.Format(time.RFC3339)
				}
				log.Printf("Recurring issue workspace=%s issue=%s upgraded: due %s next %s", key, id, optionalID(updated.DueDate), next)
			}
		}
	}
	return nil
}

// generateRecurringOccurrence creates the next instance of the series owned
// by sourceID once its due date has passed. It returns the new issue and its
// recreated sub-issues (nil when only the schedule was updated) and the
// updated source issue. Missed periods (server downtime) collapse into a
// single instance due on the first cadence date that is not already past.
func generateRecurringOccurrence(data *domain.Bootstrap, sourceID string, now time.Time) (*domain.Issue, []domain.Issue, domain.Issue, error) {
	source, err := issueByID(data, sourceID)
	if err != nil {
		return nil, nil, domain.Issue{}, store.ErrNoMutation
	}
	if source.Recurrence == "" || source.NextOccurrenceAt == nil || source.NextOccurrenceAt.After(now) || source.ArchivedAt != nil {
		return nil, nil, domain.Issue{}, store.ErrNoMutation
	}
	settings := teamSettings(data, source.Team.ID)
	loc := teamLocation(settings.Timezone)
	touch := func() {
		source.UpdatedAt = now
		source.Version++
	}
	stop := func() (*domain.Issue, []domain.Issue, domain.Issue, error) {
		source.Recurrence, source.NextOccurrenceAt = "", nil
		touch()
		appendActivity(data, source.ID, "issue.updated", source.Creator, map[string]string{"recurrence": "", "automation": "recurring"})
		return nil, nil, *source, nil
	}
	teamIndex := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == source.Team.ID })
	if teamIndex < 0 || data.Teams[teamIndex].ArchivedAt != nil || data.Teams[teamIndex].RetiredAt != nil {
		// A team that no longer takes issues ends the series; a schedule left
		// due would be retried (and hold a due-window slot) every tick.
		return stop()
	}
	team := data.Teams[teamIndex]
	rule, err := parseRecurrence(source.Recurrence)
	if err != nil {
		// An unparseable schedule would stay due forever; stop the series.
		return stop()
	}
	due, hasDue := issueDueDate(*source)
	if !hasDue {
		// Legacy schedule without a due date: its next occurrence date becomes
		// this issue's due date.
		normalizeRecurrenceTiming(source, loc)
		touch()
		return nil, nil, *source, nil
	}
	if expected := recurrenceCreationInstant(due, loc); expected.After(now) {
		// Legacy midnight timing, or a due date moved later: wait until the
		// due date has passed.
		source.NextOccurrenceAt = &expected
		touch()
		return nil, nil, *source, nil
	}
	rule = rule.anchored(due)
	target := recurrenceTarget(rule, due, civilDate(now, loc))
	series := source.RecurrenceSeriesID
	if series == "" {
		series = source.ID
	}
	date := target.Format("2006-01-02")
	if slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool {
		return issue.ID != source.ID && issue.RecurrenceSeriesID == series && issue.RecurrenceOccurrence == date
	}) {
		// That instance already exists (the schedule was re-enabled on an older
		// issue): keep the schedule here and try again once its date passed.
		next := recurrenceCreationInstant(target, loc)
		source.NextOccurrenceAt, source.RecurrenceSeriesID = &next, series
		touch()
		return nil, nil, *source, nil
	}
	number := max(data.NextIssueNumber, nextIssueNumber(data.Issues))
	created, err := newTeamIssue(data, team, number, source.Creator, now)
	if err != nil {
		return nil, nil, domain.Issue{}, err
	}
	next := recurrenceCreationInstant(target, loc)
	copyRecurringContent(data, &created, *source, now)
	created.Icon = source.Icon
	created.DueDate = &date
	created.Recurrence, created.NextOccurrenceAt, created.RecurrenceSeriesID, created.RecurrenceOccurrence = storedRecurrence(source.Recurrence, rule), &next, series, date
	if source.Project != nil {
		created.Project = projectByID(data, source.Project.ID)
	}
	// Linear's recurring issues skip triage.
	skipRecurringTriage(data, &created, now)
	applyCycleAutomation(data, &created)
	applySLARules(data, &created, now)
	children := []domain.Issue{}
	for index, template := range recurringSubIssueTemplates(data, *source) {
		childTeam := team
		if i := slices.IndexFunc(data.Teams, func(item domain.Team) bool { return item.ID == template.Team.ID }); i >= 0 && data.Teams[i].ArchivedAt == nil && data.Teams[i].RetiredAt == nil {
			childTeam = data.Teams[i]
		}
		child, err := newTeamIssue(data, childTeam, number+index+1, source.Creator, now)
		if err != nil {
			return nil, nil, domain.Issue{}, err
		}
		copyRecurringContent(data, &child, template, now)
		child.ParentID = &created.ID
		skipRecurringTriage(data, &child, now)
		created.SubIssueIDs = append(created.SubIssueIDs, child.ID)
		children = append(children, child)
	}
	sourceIdentifier := source.Identifier
	if source.RecurrenceOccurrence == "" {
		source.RecurrenceOccurrence = due.Format("2006-01-02")
	}
	source.Recurrence, source.NextOccurrenceAt, source.RecurrenceSeriesID = "", nil, series
	touch()
	updatedSource := *source
	data.Issues = append(append([]domain.Issue{created}, children...), data.Issues...)
	appendActivity(data, created.ID, "issue.created", created.Creator, map[string]string{"stateId": created.State.ID, "state": created.State.Name, "automation": "recurring", "recurringFrom": sourceIdentifier, "recurringFromId": sourceID})
	for _, child := range children {
		appendActivity(data, child.ID, "issue.created", child.Creator, map[string]string{"stateId": child.State.ID, "state": child.State.Name, "automation": "recurring"})
	}
	appendActivity(data, sourceID, "issue.updated", created.Creator, map[string]string{"recurrence": "", "automation": "recurring", "recurringNext": created.Identifier, "recurringNextId": created.ID})
	return &created, children, updatedSource, nil
}

// recurrenceTarget is the due date of the instance that follows the one due
// on due: the first cadence date after it that is not before today, so missed
// periods collapse into one instance that is not already overdue.
func recurrenceTarget(rule recurrenceRule, due, today time.Time) time.Time {
	target := rule.nextAfter(due)
	for range 100000 {
		if !target.Before(today) {
			break
		}
		target = rule.nextAfter(target)
	}
	return target
}

// newTeamIssue builds an empty issue in team, numbered number, in the team's
// default status.
func newTeamIssue(data *domain.Bootstrap, team domain.Team, number int, creator domain.User, now time.Time) (domain.Issue, error) {
	state := stateForTeam(data, team.ID, teamSettings(data, team.ID).DefaultStateID)
	if state == nil {
		states := statesForTeam(data, team.ID)
		if len(states) == 0 {
			return domain.Issue{}, errInvalid
		}
		state = &states[0]
	}
	return domain.Issue{
		ID: fmt.Sprintf("issue_%d", number), Version: 1, Identifier: fmt.Sprintf("%s-%d", team.Key, number), Number: number,
		PriorityLabel: priorityLabel(0), SortOrder: float64(number), CreatedAt: now, UpdatedAt: now, Team: team, State: *state, Creator: creator,
		Labels: []domain.IssueLabel{}, SubscriberIDs: []string{creator.ID}, Reactions: map[string][]string{}, SubIssueIDs: []string{},
		Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}, SuggestedLabelIDs: []string{},
	}, nil
}

// copyRecurringContent copies what a recurring instance (or a recreated
// sub-issue) inherits: title, description, priority, estimate, labels and
// assignee.
func copyRecurringContent(data *domain.Bootstrap, target *domain.Issue, source domain.Issue, now time.Time) {
	target.Title, target.Description, target.DescriptionState = source.Title, source.Description, source.DescriptionState
	target.Priority, target.PriorityLabel = source.Priority, priorityLabel(source.Priority)
	target.Labels = labelsByID(data, issueLabelIDs(source.Labels))
	if source.Estimate != nil {
		estimate := *source.Estimate
		target.Estimate = &estimate
	}
	if source.Assignee != nil {
		target.Assignee = userByID(data, source.Assignee.ID)
		if target.Assignee != nil {
			target.SubscriberIDs = appendUnique(target.SubscriberIDs, target.Assignee.ID)
		}
	}
	if source.DocumentContent != nil {
		// A fresh document generation: the copy shares no CRDT history.
		content := *source.DocumentContent
		content.ID, content.Version, content.ContentState, content.UpdatedAt = "document_content_"+newCollaborationID(), 1, "", now
		content.ContentData = maps.Clone(source.DocumentContent.ContentData)
		target.DocumentContent = &content
	}
}

// skipRecurringTriage marks a backlog default in a triage team as triaged so
// a generated issue lands in the backlog, not the triage queue.
func skipRecurringTriage(data *domain.Bootstrap, issue *domain.Issue, now time.Time) {
	if teamSettings(data, issue.Team.ID).TriageEnabled && issue.State.Type == "backlog" && issue.TriagedAt == nil {
		issue.TriagedAt = &now
	}
}

// recurringSubIssueTemplates lists the source's live sub-issues in their
// sub-issue order; each recurring instance recreates them.
func recurringSubIssueTemplates(data *domain.Bootstrap, source domain.Issue) []domain.Issue {
	children := []domain.Issue{}
	for _, issue := range data.Issues {
		if issue.ParentID != nil && *issue.ParentID == source.ID && issue.ArchivedAt == nil {
			children = append(children, issue)
		}
	}
	position := func(id string) int {
		if index := slices.Index(source.SubIssueIDs, id); index >= 0 {
			return index
		}
		return len(source.SubIssueIDs)
	}
	slices.SortStableFunc(children, func(a, b domain.Issue) int { return position(a.ID) - position(b.ID) })
	return children
}

// storedRecurrence keeps the preset names clients and MCP tools already use
// ("daily", "weekdays", "weekly", "biweekly"): stepping them from the current
// due date never drifts. Other schedules are stored in their anchored RRULE
// form (FREQ=WEEKLY;INTERVAL=2 made on a Wednesday is stored with BYDAY=WE;
// monthly keeps BYMONTHDAY so Jan 31 -> Feb 28 -> Mar 31 keeps the day).
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
// and update. It runs after the due date of the same write was applied:
//
//   - recurrence with the issue's due date (or firstDue, the legacy
//     nextOccurrenceAt alias, which sets the due date) starts the schedule
//     anchored on that first due date;
//   - without any due date the first due date defaults to the first cadence
//     date after today (team time zone);
//   - a due date change (or a move to a team in another time zone) on a
//     recurring issue recomputes NextOccurrenceAt;
//   - "" stops recurring and keeps the due date.
//
// NextOccurrenceAt is always 00:01 on the day after the due date.
func applyRecurrenceUpdate(data *domain.Bootstrap, issue *domain.Issue, recurrence, firstDue *string, previousDue string, retime bool, now time.Time, changes map[string]string) error {
	if recurrence == nil && firstDue == nil && (!retime || issue.Recurrence == "") {
		return nil
	}
	loc := teamLocation(teamSettings(data, issue.Team.ID).Timezone)
	today := civilDate(now, loc)
	schedule := issue.Recurrence
	if recurrence != nil {
		schedule = strings.TrimSpace(*recurrence)
	}
	explicit := firstDue != nil && strings.TrimSpace(*firstDue) != ""
	if schedule == "" {
		if explicit {
			return fmt.Errorf("%w: set a recurrence schedule before its first due date", errInvalid)
		}
		if issue.Recurrence != "" || issue.NextOccurrenceAt != nil {
			changes["recurrence"] = ""
		}
		issue.Recurrence, issue.NextOccurrenceAt = "", nil
		return nil
	}
	rule, err := parseRecurrence(schedule)
	if err != nil {
		return err
	}
	if explicit {
		start, err := parseRecurrenceDate(*firstDue, loc)
		if err != nil {
			return err
		}
		value := start.Format("2006-01-02")
		if optionalID(issue.DueDate) != value {
			issue.DueDate = &value
			changes["dueDate"] = value
		}
	}
	scheduleChanged := schedule != issue.Recurrence
	if !scheduleChanged && !explicit && !retime && issue.NextOccurrenceAt != nil && issue.DueDate != nil {
		// Re-sending the stored schedule keeps it.
		return nil
	}
	due, hasDue := issueDueDate(*issue)
	if issue.DueDate != nil && !hasDue {
		return fmt.Errorf("%w: a recurring issue needs a YYYY-MM-DD due date", errInvalid)
	}
	switch {
	case !hasDue && previousDue != "" && issue.Recurrence != "":
		// Clearing the due date of a recurring issue is refused even while the
		// schedule is re-sent or changed: the due date anchors the schedule.
		return fmt.Errorf("%w: a recurring issue needs a due date; stop the recurrence first", errInvalid)
	case !hasDue && previousDue == "" && issue.NextOccurrenceAt != nil && !scheduleChanged:
		// Legacy schedule without a due date: its next occurrence date is due.
		due = civilDate(*issue.NextOccurrenceAt, loc)
		rule = rule.anchored(due)
		value := due.Format("2006-01-02")
		issue.DueDate = &value
		changes["dueDate"] = value
	case !hasDue:
		// No due date yet: "weekly" made today is first due a week from today.
		if rule.needsAnchor() {
			rule = rule.anchored(today)
			due = rule.nextAfter(today)
		} else {
			due = rule.firstOnOrAfter(today.AddDate(0, 0, 1))
		}
		value := due.Format("2006-01-02")
		issue.DueDate = &value
		changes["dueDate"] = value
	case scheduleChanged || explicit:
		rule = rule.anchored(due)
	default:
		if previous, err := time.Parse("2006-01-02", previousDue); err == nil {
			rule = rule.reanchored(previous, due)
		}
		rule = rule.anchored(due)
	}
	stored := storedRecurrence(schedule, rule)
	next := recurrenceCreationInstant(due, loc)
	if stored != issue.Recurrence {
		changes["recurrence"] = stored
	}
	if issue.NextOccurrenceAt == nil || !issue.NextOccurrenceAt.Equal(next) {
		changes["nextOccurrenceAt"] = next.Format(time.RFC3339)
	}
	issue.Recurrence, issue.NextOccurrenceAt = stored, &next
	return nil
}
