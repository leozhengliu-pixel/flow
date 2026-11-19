package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// recurringOccurrenceImageProfile is the closed declaration of
// generateRecurringOccurrence. Its callback reads the source and live
// sub-issue templates with their teams, states, team/cycle settings, labels,
// users and project, the SLA settings, rules and the new issues' SLAs, and the
// issue number; it rewrites the source, prepends the new issue and its
// recreated sub-issues, appends their activities and may change issue SLAs
// and SLA events.
type recurringOccurrenceImageProfile struct {
	sourceID string
	scope    MutationScope
	// next is the locked sequence's next number for the scoped writer; the
	// full writer, like the literal full writer, numbers from the issues.
	next   int
	locked bool
	// Per attempt: the selected issueSlas ordinals and the stored SLA event
	// count, for the metadata patch.
	slaOrdinals []int
	slaEvents   int
}

var recurringOccurrenceInputFields = []string{
	"users", "teams", "teamSettings", "teamParents", "workspaceSettings", "notificationPreferences", "pushSubscriptions",
	"userSettings", "cycleSettings", "cycles", "labels", "labelGroups", "settings", "issueSlas", "slaRules", "members",
	"states", "projects",
}

func (*recurringOccurrenceImageProfile) kind() scopedImageWriteKind {
	return scopedImageRecurringOccurrence
}

func (*recurringOccurrenceImageProfile) extraFields() []string {
	return []string{"issueSlas", "slaEvents"}
}

func (p *recurringOccurrenceImageProfile) beginFull() { p.next, p.locked = 0, false }

// resolve hands the executor the creation scope; its expansion runs in
// issueFamily under the transaction, as the literal scoped writer runs it.
func (p *recurringOccurrenceImageProfile) resolve(context.Context, *ResourceRead, *metadataLogicalSource) (MutationScope, error) {
	p.next, p.locked = 0, false
	return MutationScope{IssueIDs: []string{p.sourceID}, CreateIssues: true}, nil
}

// issueFamily follows mutateScoped for the recurrence scope: the source,
// then its expansion (direct children by parent_id and the series' candidate
// instances by the recurrenceInstance attribute), with the literal 20,000
// record limits, and then locks the issue sequence.
func (p *recurringOccurrenceImageProfile) issueFamily(ctx context.Context, tx *sqlTx, workspace string, owner *ResourceRead, rows *issueMutationRows, _ *metadataLogicalSource, _ MutationScope, direct []string) ([]string, error) {
	if !slices.Equal(direct, []string{p.sourceID}) {
		return nil, fmt.Errorf("%w: recurring occurrence input refused", ErrBoundedMetadataCoverage)
	}
	s := owner.store
	// Only selection/existence escapes this preliminary read; its SQL locks
	// stay with the borrowed transaction after the native values are closed.
	sourceRows := s.newIssueMutationRows(tx, workspace)
	defer sourceRows.Close()
	records, err := sourceRows.load(ctx, []string{p.sourceID}, true)
	if err != nil {
		return nil, err
	}
	issues := make([]domain.Issue, 0, len(records))
	for _, record := range records {
		issues = append(issues, record.issue)
	}
	extra := p.scope.Expand(issues)
	extra.Resolve, extra.Expand = nil, nil
	checked := extra
	checked.IssueColumns, checked.IssueAttributes = nil, nil
	if !reflect.DeepEqual(checked, MutationScope{}) {
		return nil, fmt.Errorf("%w: recurring occurrence input refused", ErrBoundedMetadataCoverage)
	}
	type selection struct {
		column, from string
		args         []any
	}
	var selections []selection
	chunks := func(values []string, each func([]string)) {
		values = slices.DeleteFunc(slices.Clone(values), func(value string) bool { return value == "" })
		for start := 0; start < len(values); start += 500 {
			each(values[start:min(start+500, len(values))])
		}
	}
	for _, column := range extra.IssueColumns {
		if column.Column != "parent_id" {
			return nil, fmt.Errorf("%w: recurring occurrence input refused", ErrBoundedMetadataCoverage)
		}
		chunks(column.Values, func(values []string) {
			clause, args := bindList("parent_id", values)
			selections = append(selections, selection{"id", ` FROM issue_records WHERE workspace_key=? AND ` + clause, append([]any{workspace}, args...)})
		})
	}
	for _, attribute := range extra.IssueAttributes {
		if attribute.Column != "recurrenceInstance" || !isIssueAttributeField(attribute.Column) {
			return nil, fmt.Errorf("%w: recurring occurrence input refused", ErrBoundedMetadataCoverage)
		}
		chunks(attribute.Values, func(values []string) {
			keys := make([]string, len(values))
			for index, value := range values {
				keys[index] = indexKey(value)
			}
			clause, args := bindList("value", keys)
			selections = append(selections, selection{"issue_id", ` FROM issue_attribute_records WHERE workspace_key=? AND field=? AND ` + clause,
				append([]any{workspace, indexKey(attribute.Column)}, args...)})
		})
	}
	var filtered []string
	for _, query := range selections {
		var count, size int64
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM("+owner.byteLength(query.column)+"),0)"+query.from, query.args...).Scan(&count, &size); err != nil {
			return nil, err
		}
		if count < 0 || size < 0 {
			return nil, ErrMetadataRecordQuery
		}
		if count > int64(maxScopedIssues-len(filtered)) {
			return nil, errScopeTooLarge
		}
		if uint64(size) > (metadataWorkBytes-(64<<10)-uint64(count)*512)/8 {
			return nil, ErrMetadataWorkBudget
		}
		if err := owner.reserve(ctx, uint64(size)*8+uint64(count)*512+64<<10); err != nil {
			return nil, err
		}
		rows, err := tx.QueryContext(ctx, "SELECT "+query.column+query.from, query.args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			filtered = append(filtered, id)
			if len(filtered) > maxScopedIssues {
				rows.Close()
				return nil, errScopeTooLarge
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
	}
	// Every filtered ID names a stored row; the literal limit counts the
	// distinct loaded records, the source included when it exists.
	ids := slices.Clone(direct)
	for _, id := range filtered {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	distinct := len(ids)
	if _, found := records[p.sourceID]; !found {
		distinct--
	}
	if distinct > maxScopedIssues {
		return nil, errScopeTooLarge
	}
	// Number new issues from the durable sequence, as the literal scoped
	// writer does; its snapshot clone carries no stored next number.
	if err := writeIssueNumber(ctx, tx, workspace, 0); err != nil {
		return nil, err
	}
	lock := ""
	if s.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	var last int
	if err := tx.QueryRowContext(ctx, `SELECT last_number FROM issue_number_sequences WHERE workspace_key=?`+lock, workspace).Scan(&last); err != nil {
		return nil, err
	}
	p.next, p.locked = last+1, true
	return ids, ctx.Err()
}

var recurringOccurrenceIssueHeader = []string{"ID", "Number", "ParentID", "ArchivedAt", "Team", "Creator", "Assignee", "Delegate", "SubscriberIDs", "Labels", "Project"}

func (p *recurringOccurrenceImageProfile) inputs(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource) (domain.Bootstrap, error) {
	var data domain.Bootstrap
	for _, name := range []string{"workspace", "viewer", "viewerRole"} {
		index, _, _ := metadataLogicalField(name)
		value, err := cycleScheduleNativeValue(ctx, owner, source.fields[index])
		if err != nil {
			return data, err
		}
		reflect.ValueOf(&data).Elem().Field(index).Set(value)
	}
	if p.locked {
		data.NextIssueNumber = p.next
	}
	if err := source.Retain(); err != nil {
		return data, err
	}
	read := &issueHotInputs{store: owner.store, source: source}
	owner.mu.Lock()
	owner.release = append(owner.release, read.close)
	owner.mu.Unlock()
	if err := read.reserve(ctx, 64<<10); err != nil {
		return data, err
	}
	for _, name := range []string{"teams", "teamSettings", "teamParents", "workspaceSettings"} {
		if err := read.completeField(ctx, name); err != nil {
			return data, err
		}
	}
	data.Teams, data.TeamSettings = read.data.Teams, read.data.TeamSettings
	// The callback's issues: every reference the instance and its
	// sub-issues copy comes from the source and its live sub-issues.
	// One header pass over the callback's issues (every issue on the full
	// writer path) folds the literal issue number and finds the source (its
	// first occurrence) and live sub-issue templates; only those are
	// projected with their references.
	number, sourceOrdinal := 1, -1
	var templates []int
	if err := scopedImageVisit(ctx, source, "issues", func(ordinal int, node metadataLogicalNode) error {
		_, err := read.withHeader(ctx, node, []string{"ID", "Number", "ParentID", "ArchivedAt"}, func(value reflect.Value) (bool, error) {
			issue := value.Interface().(domain.Issue)
			if issue.Number >= number {
				number = issue.Number + 1
			}
			if issue.ID == p.sourceID && sourceOrdinal < 0 {
				sourceOrdinal = ordinal
			}
			if issue.ParentID != nil && *issue.ParentID == p.sourceID && issue.ArchivedAt == nil {
				if err := owner.reserve(ctx, 64); err != nil {
					return false, err
				}
				templates = append(templates, ordinal)
			}
			return false, nil
		})
		return err
	}); err != nil {
		return data, err
	}
	var headers []domain.Issue
	var sourceIssue *domain.Issue
	if err := scopedImageVisit(ctx, source, "issues", func(ordinal int, node metadataLogicalNode) error {
		if ordinal != sourceOrdinal && !slices.Contains(templates, ordinal) {
			return nil
		}
		value, err := cycleScheduleProjectNode(ctx, owner, node, recurringOccurrenceIssueHeader...)
		if err == nil {
			headers = append(headers, value.Interface().(domain.Issue))
		}
		return err
	}); err != nil {
		return data, err
	}
	for index := range headers {
		if headers[index].ID == p.sourceID {
			sourceIssue = &headers[index]
			break
		}
	}
	// The issues the callback can create: the instance numbered like the
	// literal callback (max(NextIssueNumber, nextIssueNumber(issues))) in the
	// source team, and one sub-issue per live template in its live team.
	targets := []domain.Issue{}
	projects := map[string]bool{}
	if sourceIssue != nil {
		number = max(data.NextIssueNumber, number)
		targets = append(targets, domain.Issue{ID: fmt.Sprintf("issue_%d", number), Team: domain.Team{ID: sourceIssue.Team.ID}})
		children := 0
		for _, issue := range headers {
			if issue.ParentID == nil || *issue.ParentID != p.sourceID || issue.ArchivedAt != nil {
				continue
			}
			children++
			team := sourceIssue.Team.ID
			if index := slices.IndexFunc(data.Teams, func(item domain.Team) bool { return item.ID == issue.Team.ID }); index >= 0 &&
				data.Teams[index].ArchivedAt == nil && data.Teams[index].RetiredAt == nil {
				team = data.Teams[index].ID
			}
			targets = append(targets, domain.Issue{ID: fmt.Sprintf("issue_%d", number+children), Team: domain.Team{ID: team}})
		}
		if sourceIssue.Project != nil {
			projects[sourceIssue.Project.ID] = true
		}
	}
	data.Issues = append(headers, targets...)
	update := domain.IssueUpdateInput{SubscriberIDs: &[]string{}, LabelIDs: &[]string{}}
	if err := read.selectIssueInputs(ctx, &data, targets, update); err != nil {
		return data, err
	}
	if err := read.issueBatchStates(ctx, &data, update); err != nil {
		return data, err
	}
	if len(projects) != 0 {
		if err := read.selectSlice(ctx, "projects", func(node metadataLogicalNode) (bool, error) {
			return issueHotMatches(ctx, node, "ID", projects)
		}, "ID", "Name", "Icon", "Color"); err != nil {
			return data, err
		}
	}
	// The selected issue SLAs (loadReferences: those of these issues) keep
	// their native ordinals; new SLA events are numbered after the stored ones.
	loaded := map[string]bool{}
	for _, issue := range data.Issues {
		loaded[issue.ID] = true
	}
	p.slaOrdinals, p.slaEvents = nil, 0
	if err := scopedImageVisit(ctx, source, "issueSlas", func(ordinal int, node metadataLogicalNode) error {
		match, err := issueHotMatches(ctx, node, "IssueID", loaded)
		if match {
			p.slaOrdinals = append(p.slaOrdinals, ordinal)
		}
		return err
	}); err != nil {
		return data, err
	}
	if len(p.slaOrdinals) != len(read.data.IssueSLAs) {
		return data, fmt.Errorf("%w: recurring occurrence input refused", ErrBoundedMetadataCoverage)
	}
	if err := scopedImageVisit(ctx, source, "slaEvents", func(int, metadataLogicalNode) error {
		p.slaEvents++
		return nil
	}); err != nil {
		return data, err
	}
	input, target := reflect.ValueOf(read.data), reflect.ValueOf(&data).Elem()
	for _, name := range recurringOccurrenceInputFields {
		index, _, _ := metadataLogicalField(name)
		target.Field(index).Set(input.Field(index))
	}
	data.Issues = nil
	return data, ctx.Err()
}

// recurringOccurrenceSourceFields are the source issue fields the callback
// rewrites: its schedule, series, occurrence, due date and edit stamp.
var recurringOccurrenceSourceFields = []string{"Recurrence", "NextOccurrenceAt", "RecurrenceSeriesID", "RecurrenceOccurrence", "DueDate", "UpdatedAt", "Version"}

func (p *recurringOccurrenceImageProfile) validate(before, after domain.Bootstrap) error {
	refuse := func(reason string) error {
		return fmt.Errorf("%w: recurring occurrence %s", ErrBoundedMetadataCoverage, reason)
	}
	prefix := len(after.Issues) - len(before.Issues)
	if prefix < 0 {
		return refuse("removed issues")
	}
	sourceSeen := false
	for index, original := range before.Issues {
		next := after.Issues[prefix+index]
		if original.ID == p.sourceID && !sourceSeen {
			sourceSeen = true
			if !cycleScheduleOnlyChanged(reflect.ValueOf(original), reflect.ValueOf(next), recurringOccurrenceSourceFields) {
				return refuse("changed an undeclared source field")
			}
			continue
		}
		if !reflect.DeepEqual(original, next) {
			return refuse("changed an existing issue")
		}
	}
	if prefix > 0 && !sourceSeen {
		return refuse("created without its source")
	}
	if len(before.SLAEvents) != 0 || len(after.IssueSLAs) < len(before.IssueSLAs) {
		return refuse("removed SLA records")
	}
	left, right := reflect.ValueOf(before), reflect.ValueOf(after)
	for _, field := range persistedMetadataFields() {
		if field.name == "issueSlas" || field.name == "slaEvents" {
			continue
		}
		if !metadataValueEqual(left.Field(field.index), right.Field(field.index)) {
			return refuse("changed metadata " + field.name)
		}
	}
	return nil
}

func (p *recurringOccurrenceImageProfile) validateAggregate(before, after domain.Bootstrap, aggregate string) error {
	expected := p.sourceID
	if len(after.Issues) > len(before.Issues) {
		expected = after.Issues[0].ID
	}
	if aggregate != expected {
		return fmt.Errorf("%w: recurring occurrence replaced its aggregate", ErrBoundedMetadataCoverage)
	}
	return nil
}

// image patches the selected issue SLAs by native ordinal, appends new ones,
// and appends new SLA events numbered after the stored events.
func (p *recurringOccurrenceImageProfile) image(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource, before, after domain.Bootstrap) (*metadataLogicalSource, error) {
	if len(before.IssueSLAs) != len(p.slaOrdinals) {
		return nil, fmt.Errorf("%w: recurring occurrence input refused", ErrBoundedMetadataCoverage)
	}
	edits := map[int]domain.IssueSLA{}
	for index, original := range before.IssueSLAs {
		if !reflect.DeepEqual(original, after.IssueSLAs[index]) {
			edits[p.slaOrdinals[index]] = after.IssueSLAs[index]
		}
	}
	slas, releaseSLAs, err := issueBatchSlicePatchNode(ctx, owner.store, source, "issueSlas", nil, edits, after.IssueSLAs[len(before.IssueSLAs):])
	if err != nil {
		return nil, err
	}
	defer releaseSLAs()
	appended, err := issueBatchSLAEvents(after.SLAEvents, p.slaEvents)
	if err != nil {
		return nil, err
	}
	events, releaseEvents, err := issueBatchSlicePatchNode[domain.SLAEvent](ctx, owner.store, source, "slaEvents", nil, nil, appended)
	if err != nil {
		return nil, err
	}
	defer releaseEvents()
	return source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		switch {
		case name == "issueSlas" && slas != nil:
			return slas, true, nil
		case name == "slaEvents" && events != nil:
			return events, true, nil
		}
		return metadataAggregateStoredNode{value: node}, false, nil
	})
}

func (*recurringOccurrenceImageProfile) reserveCallback(ctx context.Context, owner *ResourceRead, data domain.Bootstrap, payload any) error {
	budget := &boundedDiscussionAttempt{owner: owner}
	release, err := budget.reserveIssueCreateCallback(ctx, data, payload)
	if err != nil {
		return err
	}
	// The created family shares the callback's strings until the output
	// owner closes the attempt.
	owner.mu.Lock()
	owner.release = append(owner.release, release)
	owner.mu.Unlock()
	return nil
}
