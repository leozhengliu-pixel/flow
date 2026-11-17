package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// CommentActionResolution declares one comment action resolution: the issue
// whose action is resolved, the action and the requested status. The scope
// stays the original MutationScope (the issue, its status-automation family
// and issue creation); the declaration only selects the closed writer.
type CommentActionResolution struct {
	IssueID, ActionID, Status string
}

type commentActionResolutionKey struct{}

// ContextWithCommentActionResolution routes a bounded issue.updated
// resolution to the resource-scope image writer with its closed profile.
func ContextWithCommentActionResolution(ctx context.Context, resolution CommentActionResolution) context.Context {
	return context.WithValue(ctx, commentActionResolutionKey{}, resolution)
}

func commentActionResolutionFromContext(ctx context.Context) (CommentActionResolution, bool) {
	resolution, ok := ctx.Value(commentActionResolutionKey{}).(CommentActionResolution)
	return resolution, ok && resolution.IssueID != "" && (resolution.Status == "accepted" || resolution.Status == "dismissed")
}

// commentActionResolveProfile is the resolution callback's closure on the
// scoped image writer: the authorization inputs of commentActionsAllowed,
// every comment action with the resolved ID (with ordinals), and, for an
// accepted pending action, the literal update (status, assignee, priority,
// labels) or sub-issue creation closure. A link only appends to the issue.
type commentActionResolveProfile struct {
	resolution CommentActionResolution
	ready      bool
	next       int
	ordinals   []int
	batch      *issueBatchInputs
	create     *issueCreateInputs
	fields     []string
}

func (p *commentActionResolveProfile) kind() scopedImageWriteKind {
	return scopedImageCommentActionResolve
}

func (p *commentActionResolveProfile) extraFields() []string { return slices.Clone(p.fields) }

func (p *commentActionResolveProfile) resolve(ctx context.Context, _ *ResourceRead, _ *metadataLogicalSource) (MutationScope, error) {
	resolution, ok := commentActionResolutionFromContext(ctx)
	scope, found := mutationScopeFromContext(ctx)
	want := MutationScope{IssueIDs: []string{p.resolution.IssueID}, StatusAutomation: true, CreateIssues: true}
	if !ok || !found || resolution != p.resolution || !reflect.DeepEqual(scope, want) {
		return MutationScope{}, ErrBoundedMetadataCoverage
	}
	p.ready = false
	return scope, nil
}

// issueFamily is statusAutomationIssueIDs over the locked records and the
// installed team settings and states, then the creation sequence lock.
func (p *commentActionResolveProfile) issueFamily(ctx context.Context, tx *sqlTx, workspace string, owner *ResourceRead, rows *issueMutationRows, source *metadataLogicalSource, scope MutationScope, ids []string) ([]string, error) {
	s := owner.store
	records, err := rows.load(ctx, ids, true)
	if err != nil {
		return nil, err
	}
	if err := owner.reserve(ctx, uint64(len(ids))*1024+64<<10); err != nil {
		return nil, err
	}
	result, parents := []string{}, []string{}
	teams := []string{}
	for _, id := range ids {
		record, ok := records[id]
		if !ok {
			continue
		}
		parents = append(parents, id)
		if record.issue.ParentID != nil && *record.issue.ParentID != "" {
			result = append(result, *record.issue.ParentID)
			parents = append(parents, *record.issue.ParentID)
		}
		if !slices.Contains(teams, record.issue.Team.ID) {
			teams = append(teams, record.issue.Team.ID)
		}
	}
	collect := func(query string, args ...any) error {
		found, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		for found.Next() {
			var id string
			if err := found.Scan(&id); err != nil {
				found.Close()
				return err
			}
			if err := owner.reserve(ctx, uint64(len(id))*2+256); err != nil {
				found.Close()
				return err
			}
			result = append(result, id)
			if len(result) > maxScopedIssues {
				found.Close()
				return errScopeTooLarge
			}
		}
		return errors.Join(found.Err(), found.Close())
	}
	for start := 0; start < len(parents); start += 500 {
		clause, args := bindList("parent_id", parents[start:min(start+500, len(parents))])
		if err := collect(`SELECT id FROM issue_records WHERE workspace_key=? AND `+clause+` ORDER BY id LIMIT 5001`, append([]any{workspace}, args...)...); err != nil {
			return nil, err
		}
	}
	if len(teams) != 0 {
		settingsIndex, _, _ := metadataLogicalField("teamSettings")
		parentOf := map[string]string{}
		if err := source.fields[settingsIndex].MapEntries(ctx, func(key, record metadataLogicalNode) error {
			name, err := cycleScheduleNativeValue(ctx, owner, key)
			if err != nil {
				return err
			}
			header, err := cycleScheduleProjectNode(ctx, owner, record, "ParentTeamID")
			if err == nil {
				parentOf[name.String()] = header.Interface().(domain.TeamSettings).ParentTeamID
			}
			return err
		}); err != nil {
			return nil, err
		}
		type stateHeader struct{ id, team string }
		states := []stateHeader{}
		statesIndex, _, _ := metadataLogicalField("states")
		if err := source.fields[statesIndex].Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
			header, err := cycleScheduleProjectNode(ctx, owner, record, "ID", "TeamID")
			if err == nil {
				state := header.Interface().(domain.WorkflowState)
				states = append(states, stateHeader{state.ID, state.TeamID})
			}
			return err
		}); err != nil {
			return nil, err
		}
		for _, team := range teams {
			// A team's issues use its own statuses, the shared legacy
			// statuses, or (when inheriting) an ancestor's.
			owners := map[string]bool{team: true, "": true}
			seen := map[string]bool{team: true}
			for parent := parentOf[team]; parent != "" && !seen[parent]; parent = parentOf[parent] {
				seen[parent], owners[parent] = true, true
			}
			for _, state := range states {
				if !owners[state.team] {
					continue
				}
				for _, direction := range []string{"ASC", "DESC"} {
					if err := collect(`SELECT id FROM issue_records WHERE workspace_key=? AND team_id=? AND state_id=? ORDER BY sort_order `+direction+`,id `+direction+` LIMIT 1`, workspace, team, state.id); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	result = slices.DeleteFunc(result, func(id string) bool { _, loaded := records[id]; return loaded })
	if scope.CreateIssues {
		if p.next, err = s.issueTeamMoveSequence(ctx, tx, workspace, owner, source); err != nil {
			return nil, err
		}
	}
	p.ready = true
	return append(slices.Clone(ids), result...), ctx.Err()
}

func (p *commentActionResolveProfile) inputs(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource) (domain.Bootstrap, error) {
	var data domain.Bootstrap
	if !p.ready {
		return data, ErrBoundedMetadataCoverage
	}
	// One family resolution admits one input selection: the over-limit
	// full writer never reaches this profile without its own family.
	p.ready = false
	s := owner.store
	p.batch, p.create, p.ordinals, p.fields = nil, nil, nil, nil
	for _, name := range []string{"workspace", "viewer", "viewerRole", "issues"} {
		index, _, _ := metadataLogicalField(name)
		value, err := cycleScheduleNativeValue(ctx, owner, source.fields[index])
		if err != nil {
			return data, err
		}
		reflect.ValueOf(&data).Elem().Field(index).Set(value)
	}
	data.NextIssueNumber = p.next
	if err := source.Retain(); err != nil {
		return data, err
	}
	inputs := &issueHotInputs{store: s, source: source}
	owner.mu.Lock()
	owner.release = append(owner.release, inputs.close)
	owner.mu.Unlock()
	if err := inputs.reserve(ctx, 64<<10); err != nil {
		return data, err
	}
	// Every action with the resolved ID, in order: the callback takes the
	// first that also names the issue.
	actionsIndex, _, _ := metadataLogicalField("commentActions")
	if absent, err := source.fields[actionsIndex].isNil(ctx); err != nil {
		return data, err
	} else if !absent {
		data.CommentActions = []domain.CommentAction{}
	}
	wanted, err := inputs.keys(ctx, p.resolution.ActionID)
	if err != nil {
		return data, err
	}
	if err := source.fields[actionsIndex].Children(ctx, func(ordinal reflect.Value, record metadataLogicalNode) error {
		matched, err := issueHotMatches(ctx, record, "ID", wanted)
		if err != nil || !matched {
			return err
		}
		value, err := cycleScheduleNativeValue(ctx, owner, record)
		if err == nil {
			data.CommentActions = append(data.CommentActions, value.Interface().(domain.CommentAction))
			p.ordinals = append(p.ordinals, int(ordinal.Int()))
		}
		return err
	}); err != nil {
		return data, err
	}
	var target *domain.Issue
	for index := range data.Issues {
		if data.Issues[index].ID == p.resolution.IssueID {
			target = &data.Issues[index]
			break
		}
	}
	action := slices.IndexFunc(data.CommentActions, func(a domain.CommentAction) bool { return a.IssueID == p.resolution.IssueID })
	if target != nil && action >= 0 && data.CommentActions[action].Status == "pending" && p.resolution.Status == "accepted" {
		accepted := data.CommentActions[action]
		a := accepted.Arguments
		switch accepted.Action {
		case "subIssue":
			parent := p.resolution.IssueID
			p.create, err = s.loadIssueCreateInputs(ctx, source, &data, domain.IssueCreateInput{Title: a.Title, Description: a.Description, TeamID: target.Team.ID, ParentID: &parent})
			if err != nil {
				return data, err
			}
			owner.mu.Lock()
			owner.release = append(owner.release, p.create.close)
			owner.mu.Unlock()
			p.create.install(&data)
		case "status", "assignee", "priority", "labels":
			p.batch, err = s.loadIssueBatchInputs(ctx, source, &data, []domain.Issue{*target},
				domain.IssueUpdateInput{StateID: a.StateID, AssigneeID: a.AssigneeID, Priority: a.Priority, LabelIDs: a.LabelIDs})
			if err != nil {
				return data, err
			}
			owner.mu.Lock()
			owner.release = append(owner.release, p.batch.close)
			owner.mu.Unlock()
			p.batch.install(&data)
		}
	}
	// commentActionsAllowed: complete teams, team settings and workspace
	// settings, the viewer's complete settings and memberships (with any
	// creation closure's team members, in source order).
	for _, name := range []string{"teams", "teamSettings", "workspaceSettings"} {
		index, _, _ := metadataLogicalField(name)
		value, err := cycleScheduleNativeValue(ctx, owner, source.fields[index])
		if err != nil {
			return data, err
		}
		reflect.ValueOf(&data).Elem().Field(index).Set(value)
	}
	viewer, err := inputs.keys(ctx, data.Viewer.ID)
	if err != nil {
		return data, err
	}
	if err := inputs.selectMap(ctx, "userSettings", viewer); err != nil {
		return data, err
	}
	data.UserSettings = inputs.data.UserSettings
	teams, err := inputs.keys(ctx)
	if err != nil {
		return data, err
	}
	if p.create != nil {
		for _, member := range data.TeamMembers {
			if err := inputs.addKey(ctx, teams, member.TeamID); err != nil {
				return data, err
			}
		}
	}
	members, err := agentReplySelectNodes(ctx, owner, source, "teamMembers", func(record metadataLogicalNode) (bool, error) {
		byUser, err := issueHotMatches(ctx, record, "UserID", viewer)
		if err != nil || byUser {
			return byUser, err
		}
		return issueHotMatches(ctx, record, "TeamID", teams)
	})
	if err != nil {
		return data, err
	}
	data.TeamMembers = members.Interface().([]domain.TeamMember)
	return data, ctx.Err()
}

var commentActionResolveFields = map[string]bool{
	"commentActions": true, "issueSlas": true, "slaEvents": true, "issueSuggestions": true,
	"workflowDefinitions": true, "workflowRuns": true, "triageResponsibilities": true, "triageAssignments": true,
}

func (p *commentActionResolveProfile) validate(before, after domain.Bootstrap) error {
	refuse := func(what string) error {
		return fmt.Errorf("%w: comment action resolution changed %s", ErrBoundedMetadataCoverage, what)
	}
	for _, field := range changedMetadataFields(collectionMetadata(before), collectionMetadata(after)) {
		if !commentActionResolveFields[field.name] {
			return refuse(field.name)
		}
		if p.batch == nil && p.create == nil && field.name != "commentActions" {
			return refuse(field.name)
		}
	}
	if !reflect.DeepEqual(before.Viewer, after.Viewer) || before.ViewerRole != after.ViewerRole ||
		!reflect.DeepEqual(before.ViewerTeamIDs, after.ViewerTeamIDs) {
		return refuse("its viewer")
	}
	if len(before.CommentActions) != len(after.CommentActions) || before.CommentActions != nil && after.CommentActions == nil {
		return refuse("the comment action scope")
	}
	changed := 0
	for index, action := range before.CommentActions {
		if reflect.DeepEqual(action, after.CommentActions[index]) {
			continue
		}
		changed++
		if action.ID != after.CommentActions[index].ID || action.IssueID != after.CommentActions[index].IssueID {
			return refuse("a comment action identity")
		}
	}
	if changed > 1 || p.create == nil && len(after.Issues) != len(before.Issues) {
		return refuse("its issue scope")
	}
	return nil
}

func (p *commentActionResolveProfile) image(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource, before, after domain.Bootstrap) (*metadataLogicalSource, error) {
	if err := p.validate(before, after); err != nil {
		return nil, err
	}
	s := owner.store
	patches := map[string]metadataAggregateNode{}
	hold := func(release func()) {
		owner.mu.Lock()
		owner.release = append(owner.release, release)
		owner.mu.Unlock()
	}
	edits := map[int]domain.CommentAction{}
	for index, action := range before.CommentActions {
		if !reflect.DeepEqual(action, after.CommentActions[index]) {
			edits[p.ordinals[index]] = after.CommentActions[index]
		}
	}
	node, release, err := issueBatchSlicePatchNode(ctx, s, source, "commentActions", nil, edits, nil)
	if err != nil {
		return nil, err
	}
	hold(release)
	if node != nil {
		patches["commentActions"] = node
	}
	scope := map[string]bool{}
	for _, issue := range after.Issues {
		scope[issue.ID] = true
	}
	if p.batch != nil {
		dropped, suggestionEdits, appended, err := issueSuggestionPatch(before.IssueSuggestions, after.IssueSuggestions,
			p.batch.suggestions, before.Issues, after.Issues, scope)
		if err != nil {
			return nil, err
		}
		suggestions, release, err := s.issueSuggestionPatchNode(ctx, source, dropped, suggestionEdits, appended)
		if err != nil {
			return nil, err
		}
		hold(release)
		if suggestions != nil {
			patches["issueSuggestions"] = suggestions
		}
		slaEdits, slaAppended, err := issueBatchSLAPatch(before.IssueSLAs, after.IssueSLAs, p.batch.slas, scope)
		if err != nil {
			return nil, err
		}
		slas, release, err := issueBatchSlicePatchNode(ctx, s, source, "issueSlas", nil, slaEdits, slaAppended)
		if err != nil {
			return nil, err
		}
		hold(release)
		if slas != nil {
			patches["issueSlas"] = slas
		}
		events, err := issueBatchSLAEvents(after.SLAEvents, p.batch.slaEvents)
		if err != nil {
			return nil, err
		}
		eventPatch, release, err := issueBatchSlicePatchNode[domain.SLAEvent](ctx, s, source, "slaEvents", nil, nil, events)
		if err != nil {
			return nil, err
		}
		hold(release)
		if eventPatch != nil {
			patches["slaEvents"] = eventPatch
		}
	}
	if p.create != nil {
		created, release, err := p.create.patch(ctx, before, after)
		if err != nil {
			return nil, err
		}
		hold(release)
		for name, node := range created {
			patches[name] = node
		}
	}
	if after.NextIssueNumber != 0 {
		patches["nextIssueNumber"] = metadataAggregateNativeNode{reflect.ValueOf(after.NextIssueNumber)}
	}
	p.fields = p.fields[:0]
	for name := range patches {
		p.fields = append(p.fields, name)
	}
	slices.Sort(p.fields)
	return source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if patch, ok := patches[name]; ok {
			return patch, true, nil
		}
		return metadataAggregateStoredNode{original}, false, nil
	})
}

// reserveCallback charges the callback's growth: the payload (which the
// callback extends with the complete issue), the resolved action and one
// created issue with its activities, notifications and deliveries.
func (p *commentActionResolveProfile) reserveCallback(ctx context.Context, owner *ResourceRead, data domain.Bootstrap, payload any) error {
	for _, value := range []any{payload, data.Issues, data.CommentActions} {
		cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(value), 0)
		if err != nil {
			return err
		}
		if uint64(cost) > (metadataWorkBytes-(64<<10))/3 {
			return ErrMetadataWorkBudget
		}
		if err := owner.reserve(ctx, uint64(cost)*3+64<<10); err != nil {
			return err
		}
	}
	recipients := uint64(len(data.Users)) + 4
	for _, issue := range data.Issues {
		recipients += uint64(len(issue.SubscriberIDs))
	}
	unit := uint64(reflect.TypeFor[domain.Notification]().Size())*2 + uint64(reflect.TypeFor[domain.NotificationDelivery]().Size())*8 +
		uint64(reflect.TypeFor[domain.ActivityEvent]().Size())*2 + uint64(reflect.TypeFor[domain.Issue]().Size())*2 + 8192
	if recipients > (metadataWorkBytes-(64<<10))/unit {
		return ErrMetadataWorkBudget
	}
	return owner.reserve(ctx, recipients*unit+64<<10)
}
