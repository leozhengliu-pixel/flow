package store

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
)

type issueCreateInputs struct {
	*issueHotInputs
	ordinals map[string][]int
	events   int
}

func (s *SQLiteStore) loadIssueCreateInputs(ctx context.Context, source *metadataLogicalSource, data *domain.Bootstrap, input domain.IssueCreateInput) (_ *issueCreateInputs, err error) {
	if err := source.Retain(); err != nil {
		return nil, err
	}
	read := &issueCreateInputs{issueHotInputs: &issueHotInputs{store: s, source: source}, ordinals: map[string][]int{}}
	defer func() {
		if err != nil {
			read.close()
		}
	}()
	for _, name := range []string{"teams", "teamSettings", "teamParents", "workspaceSettings"} {
		if err := read.completeField(ctx, name); err != nil {
			return nil, err
		}
	}
	data.Teams, data.TeamSettings, data.TeamParents, data.WorkspaceSettings = read.data.Teams, read.data.TeamSettings, read.data.TeamParents, read.data.WorkspaceSettings
	templateSelected := false
	if err := read.selectSlice(ctx, "issueTemplates", func(record metadataLogicalNode) (bool, error) {
		if input.TemplateID == "" || templateSelected {
			return false, nil
		}
		wanted, err := issueHotMatches(ctx, record, "ID", map[string]bool{input.TemplateID: true})
		templateSelected = wanted
		return wanted, err
	}); err != nil {
		return nil, err
	}
	// The literal callback uses the first matching template, including when
	// malformed imported collections contain duplicate IDs.
	effective := input
	var children []domain.TemplateSubIssue
	if len(read.data.IssueTemplates) != 0 {
		template := read.data.IssueTemplates[0]
		children = template.SubIssues
		if effective.TeamID == "" {
			effective.TeamID = template.TeamID
		}
		if effective.Description == "" {
			effective.Description = template.Body
		}
		if effective.AssigneeID == nil && template.AssigneeID != "" {
			effective.AssigneeID = &template.AssigneeID
		}
		if effective.ProjectID == nil && template.ProjectID != "" {
			effective.ProjectID = &template.ProjectID
		}
		if len(effective.LabelIDs) == 0 {
			effective.LabelIDs = template.LabelIDs
		}
	}
	if effective.TeamID == "" && len(data.Teams) != 0 {
		effective.TeamID = data.Teams[0].ID
	}
	teams, err := read.keys(ctx, effective.TeamID)
	if err != nil {
		return nil, err
	}
	users, err := read.keys(ctx, data.Viewer.ID)
	if err != nil {
		return nil, err
	}
	labels, err := read.keys(ctx, effective.LabelIDs...)
	if err != nil {
		return nil, err
	}
	for _, child := range children {
		if child.TeamID != "" {
			if err := read.addKey(ctx, teams, child.TeamID); err != nil {
				return nil, err
			}
		}
		if err := read.addKey(ctx, users, child.AssigneeID); err != nil {
			return nil, err
		}
		for _, id := range child.LabelIDs {
			if err := read.addKey(ctx, labels, id); err != nil {
				return nil, err
			}
		}
	}
	selectOrdinals := func(field string, match func(metadataLogicalNode) (bool, error)) error {
		index, _, _ := metadataLogicalField(field)
		return source.fields[index].Children(ctx, func(ordinal reflect.Value, record metadataLogicalNode) error {
			wanted, err := match(record)
			if err != nil || !wanted {
				return err
			}
			cost, err := issueHotProjectionCost(ctx, record, nil)
			if err != nil {
				return err
			}
			if err := read.reserve(ctx, cost+512); err != nil {
				return err
			}
			value, err := record.Value(ctx, int(record.cost))
			if err == nil {
				target := reflect.ValueOf(&read.data).Elem().Field(index)
				target.Set(reflect.Append(target, value))
				read.ordinals[field] = append(read.ordinals[field], int(ordinal.Int()))
			}
			return err
		})
	}
	if err := selectOrdinals("workflowDefinitions", func(record metadataLogicalNode) (bool, error) {
		return read.withHeader(ctx, record, []string{"Enabled", "Trigger", "TeamID"}, func(value reflect.Value) (bool, error) {
			item := value.Interface().(domain.WorkflowDefinition)
			return item.Enabled && item.Trigger == "issueCreated" && (item.TeamID == "" || item.TeamID == effective.TeamID), nil
		})
	}); err != nil {
		return nil, err
	}
	for _, workflow := range read.data.WorkflowDefinitions {
		if err := read.addKey(ctx, users, workflow.CreatorID); err != nil {
			return nil, err
		}
		for _, action := range workflow.Actions {
			for _, id := range []string{action.Config["recipientId"], action.Config["userId"]} {
				if err := read.addKey(ctx, users, id); err != nil {
					return nil, err
				}
			}
			if err := read.addKey(ctx, labels, action.Config["labelId"]); err != nil {
				return nil, err
			}
			if action.Type == "createIssue" {
				if err := read.addKey(ctx, teams, action.Config["teamId"]); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := read.selectSlice(ctx, "triageRoutingRules", func(record metadataLogicalNode) (bool, error) {
		return read.withHeader(ctx, record, []string{"TeamID", "Enabled"}, func(value reflect.Value) (bool, error) {
			rule := value.Interface().(domain.TriageRoutingRule)
			return rule.Enabled && rule.TeamID == effective.TeamID, nil
		})
	}); err != nil {
		return nil, err
	}
	responsibilities, err := read.keys(ctx)
	if err != nil {
		return nil, err
	}
	for _, rule := range read.data.TriageRoutingRules {
		if err := read.addKey(ctx, responsibilities, rule.ResponsibilityID); err != nil {
			return nil, err
		}
		for _, id := range rule.LabelIDs {
			if err := read.addKey(ctx, labels, id); err != nil {
				return nil, err
			}
		}
	}
	if err := selectOrdinals("triageResponsibilities", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "ID", responsibilities)
	}); err != nil {
		return nil, err
	}
	for _, responsibility := range read.data.TriageResponsibilities {
		for _, id := range responsibility.UserIDs {
			if err := read.addKey(ctx, users, id); err != nil {
				return nil, err
			}
		}
	}
	if err := read.selectSlice(ctx, "teamMembers", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "TeamID", teams)
	}); err != nil {
		return nil, err
	}
	for _, member := range read.data.TeamMembers {
		if err := read.addKey(ctx, users, member.UserID); err != nil {
			return nil, err
		}
	}
	for id := range teams {
		for _, user := range data.TeamSettings[id].TriageActionUserIDs {
			if err := read.addKey(ctx, users, user); err != nil {
				return nil, err
			}
		}
	}
	createdID := fmt.Sprintf("issue_%d", data.NextIssueNumber)
	if id := IssueCreationKey(ctx); id != "" {
		createdID = id
	}
	targets := []domain.Issue{{ID: createdID, Team: domain.Team{ID: effective.TeamID}}}
	for index, child := range children {
		team := child.TeamID
		if team == "" {
			team = effective.TeamID
		}
		targets = append(targets, domain.Issue{ID: fmt.Sprintf("issue_%d", data.NextIssueNumber+index+1), Team: domain.Team{ID: team}})
	}
	for team := range teams {
		targets = append(targets, domain.Issue{Team: domain.Team{ID: team}})
	}
	// Creation has no description-change notification: its activities and
	// any SLA notification carry no description metadata. Rich/CRDT mention
	// refusal uses the independent owned editor lookup. Avoid the update
	// selector's prose-mention scan and retained lowercased descriptions.
	update := domain.IssueUpdateInput{AssigneeID: effective.AssigneeID, DelegateID: effective.DelegateID, ProjectID: effective.ProjectID, CycleID: effective.CycleID}
	for _, id := range []*string{effective.AssigneeID, effective.DelegateID} {
		if id != nil {
			if err := read.addKey(ctx, users, *id); err != nil {
				return nil, err
			}
		}
	}
	userIDs, labelIDs := []string{}, []string{}
	for id := range users {
		userIDs = append(userIDs, id)
	}
	for id := range labels {
		labelIDs = append(labelIDs, id)
	}
	update.SubscriberIDs, update.LabelIDs = &userIDs, &labelIDs
	originalIssues := data.Issues
	data.Issues = append(slices.Clone(originalIssues), targets...)
	if err := read.selectIssueInputs(ctx, data, targets, update); err != nil {
		return nil, err
	}
	if err := read.issueBatchStates(ctx, data, update); err != nil {
		return nil, err
	}
	data.Issues = originalIssues
	// Creation reads AutoAssign in addition to update's AssignStarted.
	read.data.UserSettings = nil
	if err := read.selectMap(ctx, "userSettings", map[string]bool{data.Viewer.ID: true}); err != nil {
		return nil, err
	}
	if effective.ProjectID != nil {
		if err := read.selectSlice(ctx, "projects", func(record metadataLogicalNode) (bool, error) {
			return issueHotMatches(ctx, record, "ID", map[string]bool{*effective.ProjectID: true})
		}); err != nil {
			return nil, err
		}
	}
	loaded := map[string]bool{}
	for _, issue := range data.Issues {
		loaded[issue.ID] = true
	}
	for _, issue := range targets {
		loaded[issue.ID] = true
	}
	slaIndex, _, _ := metadataLogicalField("issueSlas")
	if err := source.fields[slaIndex].Children(ctx, func(ordinal reflect.Value, record metadataLogicalNode) error {
		wanted, err := issueHotMatches(ctx, record, "IssueID", loaded)
		if err == nil && wanted {
			read.ordinals["issueSlas"] = append(read.ordinals["issueSlas"], int(ordinal.Int()))
		}
		return err
	}); err != nil {
		return nil, err
	}
	if err := selectOrdinals("issueSuggestions", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "IssueID", loaded)
	}); err != nil {
		return nil, err
	}
	index, _, _ := metadataLogicalField("slaEvents")
	if err := source.fields[index].Children(ctx, func(reflect.Value, metadataLogicalNode) error {
		read.events++
		return nil
	}); err != nil {
		return nil, err
	}
	return read, ctx.Err()
}

func (r *issueCreateInputs) install(data *domain.Bootstrap) {
	input, target := reflect.ValueOf(r.data), reflect.ValueOf(data).Elem()
	for _, name := range []string{
		"users", "teams", "teamSettings", "teamParents", "workspaceSettings", "teamMembers",
		"notificationPreferences", "pushSubscriptions", "userSettings", "cycleSettings",
		"cycles", "labels", "labelGroups", "settings", "issueSlas", "slaRules", "members",
		"states", "projects", "issueTemplates", "workflowDefinitions", "triageRoutingRules",
		"triageResponsibilities", "issueSuggestions",
	} {
		index, _, _ := metadataLogicalField(name)
		target.Field(index).Set(input.Field(index))
	}
}

func (r *issueCreateInputs) patch(ctx context.Context, before, after domain.Bootstrap) (map[string]metadataAggregateNode, func(), error) {
	patches := map[string]metadataAggregateNode{}
	var releases []func()
	close := func() {
		for _, release := range releases {
			release()
		}
	}
	a, b := reflect.ValueOf(before), reflect.ValueOf(after)
	for _, name := range []string{"workflowDefinitions", "triageResponsibilities", "issueSlas"} {
		index, _, _ := metadataLogicalField(name)
		old, next := a.Field(index), b.Field(index)
		ordinals := r.ordinals[name]
		if old.Len() != len(ordinals) || next.Len() < old.Len() {
			close()
			return nil, nil, ErrBoundedMetadataCoverage
		}
		edits := map[int]reflect.Value{}
		for i := 0; i < old.Len(); i++ {
			if !reflect.DeepEqual(old.Index(i).Interface(), next.Index(i).Interface()) {
				edits[ordinals[i]] = next.Index(i)
			}
		}
		patch, release, err := issueCreateSlicePatch(ctx, r.store, r.source, name, edits, next.Slice(old.Len(), next.Len()))
		if err != nil {
			close()
			return nil, nil, err
		}
		releases = append(releases, release)
		if patch != nil {
			patches[name] = patch
		}
	}
	scope := map[string]bool{}
	for _, issue := range after.Issues {
		scope[issue.ID] = true
	}
	dropped, edits, appended, err := issueSuggestionPatch(before.IssueSuggestions, after.IssueSuggestions,
		r.ordinals["issueSuggestions"], before.Issues, after.Issues, scope)
	if err != nil {
		close()
		return nil, nil, err
	}
	suggestionPatch, releaseSuggestions, err := r.store.issueSuggestionPatchNode(ctx, r.source, dropped, edits, appended)
	if err != nil {
		close()
		return nil, nil, err
	}
	releases = append(releases, releaseSuggestions)
	if suggestionPatch != nil {
		patches["issueSuggestions"] = suggestionPatch
	}
	events, err := issueBatchSLAEvents(after.SLAEvents, r.events)
	if err != nil {
		close()
		return nil, nil, err
	}
	runs := slices.Clone(after.WorkflowRuns)
	runIndex := 0
	for index, definition := range before.WorkflowDefinitions {
		if reflect.DeepEqual(definition, after.WorkflowDefinitions[index]) {
			continue
		}
		if runIndex >= len(runs) {
			close()
			return nil, nil, ErrBoundedMetadataCoverage
		}
		cut := strings.LastIndexByte(runs[runIndex].ID, '_')
		if cut < 0 || runs[runIndex].ID[cut+1:] != strconv.Itoa(index) {
			close()
			return nil, nil, ErrBoundedMetadataCoverage
		}
		runs[runIndex].ID = runs[runIndex].ID[:cut+1] + strconv.Itoa(r.ordinals["workflowDefinitions"][index])
		runIndex++
	}
	for name, values := range map[string]reflect.Value{
		"slaEvents": reflect.ValueOf(events), "workflowRuns": reflect.ValueOf(runs), "triageAssignments": reflect.ValueOf(after.TriageAssignments),
	} {
		patch, release, err := issueCreateSlicePatch(ctx, r.store, r.source, name, nil, values)
		if err != nil {
			close()
			return nil, nil, err
		}
		releases = append(releases, release)
		if patch != nil {
			patches[name] = patch
		}
	}
	return patches, close, nil
}

func issueCreateSlicePatch(ctx context.Context, s *SQLiteStore, source *metadataLogicalSource, field string, edits map[int]reflect.Value, appended reflect.Value) (metadataAggregateNode, func(), error) {
	switch field {
	case "workflowDefinitions":
		return issueCreateTypedSlicePatch[domain.WorkflowDefinition](ctx, s, source, field, edits, appended)
	case "triageResponsibilities":
		return issueCreateTypedSlicePatch[domain.TriageResponsibility](ctx, s, source, field, edits, appended)
	case "issueSuggestions":
		return issueCreateTypedSlicePatch[domain.IssueSuggestion](ctx, s, source, field, edits, appended)
	case "issueSlas":
		return issueCreateTypedSlicePatch[domain.IssueSLA](ctx, s, source, field, edits, appended)
	case "slaEvents":
		return issueCreateTypedSlicePatch[domain.SLAEvent](ctx, s, source, field, edits, appended)
	case "workflowRuns":
		return issueCreateTypedSlicePatch[domain.WorkflowRun](ctx, s, source, field, edits, appended)
	case "triageAssignments":
		return issueCreateTypedSlicePatch[domain.TriageAssignment](ctx, s, source, field, edits, appended)
	}
	return nil, nil, ErrMetadataRecordQuery
}

func issueCreateTypedSlicePatch[T any](ctx context.Context, s *SQLiteStore, source *metadataLogicalSource, field string, edits map[int]reflect.Value, appended reflect.Value) (metadataAggregateNode, func(), error) {
	typed := make(map[int]T, len(edits))
	for ordinal, value := range edits {
		typed[ordinal] = value.Interface().(T)
	}
	return issueBatchSlicePatchNode(ctx, s, source, field, nil, typed, appended.Interface().([]T))
}
