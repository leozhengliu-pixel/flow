package store

import (
	"context"
	"math"
	"reflect"

	"flow/api/internal/domain"
)

type workflowRunInputs struct {
	*issueHotInputs
	definitionOrdinal int
	resourceOrdinal   int
	resourceNode      metadataLogicalNode
	placeholders      int
	runOrdinals       []int
	retryOrdinal      int
	before            domain.Bootstrap
}

var workflowRunIssueInputFields = []string{
	"ID", "Number", "Assignee", "UpdatedAt", "Priority", "PriorityLabel", "Labels",
}

var workflowRunIssueWriteFields = []string{
	"Assignee", "UpdatedAt", "Priority", "PriorityLabel", "Labels",
}

func (r *workflowRunInputs) close() {
	r.before = domain.Bootstrap{}
	r.resourceNode = metadataLogicalNode{}
	r.issueHotInputs.close()
}

func (s *SQLiteStore) loadWorkflowRunInputs(ctx context.Context, source *metadataLogicalSource, id, resourceType, resourceID string) (_ *workflowRunInputs, err error) {
	if err := source.Retain(); err != nil {
		return nil, err
	}
	read := &workflowRunInputs{
		issueHotInputs:    &issueHotInputs{store: s, source: source},
		definitionOrdinal: -1, resourceOrdinal: -1, retryOrdinal: -1,
	}
	defer func() {
		if err != nil {
			read.close()
		}
	}()
	for _, field := range []string{"workspace", "viewer", "viewerRole", "teams", "teamSettings", "teamParents", "workspaceSettings"} {
		if err := read.completeField(ctx, field); err != nil {
			return nil, err
		}
	}
	numberField, _ := reflect.TypeFor[domain.Bootstrap]().FieldByName("NextIssueNumber")
	number := source.fields[numberField.Index[0]]
	if number.typ != reflect.TypeFor[int]() {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := read.reserve(ctx, number.cost*8+4096); err != nil {
		return nil, err
	}
	numberValue, err := number.Value(ctx, int(number.cost))
	if err != nil {
		return nil, err
	}
	read.data.NextIssueNumber = int(numberValue.Int())
	definitions, _, _ := metadataLogicalField("workflowDefinitions")
	err = source.fields[definitions].Children(ctx, func(ordinal reflect.Value, node metadataLogicalNode) error {
		match, err := issueHotMatches(ctx, node, "ID", map[string]bool{id: true})
		if err != nil || !match {
			return err
		}
		// The definition includes its full action/configuration values.
		cost, err := issueHotProjectionCost(ctx, node, nil)
		if err != nil {
			return err
		}
		if err := read.reserve(ctx, cost+512); err != nil {
			return err
		}
		value, err := node.Value(ctx, int(node.cost))
		if err != nil {
			return err
		}
		read.definitionOrdinal = int(ordinal.Int())
		read.data.WorkflowDefinitions = []domain.WorkflowDefinition{value.Interface().(domain.WorkflowDefinition)}
		return ErrMetadataIterationDone
	})
	if err != nil && err != ErrMetadataIterationDone {
		return nil, err
	}
	runs, _, _ := metadataLogicalField("workflowRuns")
	count, lastMatch := 0, -1
	if err := source.fields[runs].Children(ctx, func(ordinal reflect.Value, node metadataLogicalNode) error {
		count++
		match, err := issueHotMatches(ctx, node, "WorkflowID", map[string]bool{id: true})
		if match {
			lastMatch = int(ordinal.Int())
		}
		return err
	}); err != nil {
		return nil, err
	}
	read.retryOrdinal = lastMatch
	// The callback appends then keeps exactly 500 runs. One older matching
	// entry is also supplied when needed for the attempt/retry decision.
	start := max(0, count-499)
	if err := source.fields[runs].Children(ctx, func(ordinal reflect.Value, node metadataLogicalNode) error {
		if int(ordinal.Int()) < start && int(ordinal.Int()) != lastMatch {
			return nil
		}
		fields := []string{"WorkflowID", "Attempt", "Status", "NextRetryAt"}
		cost, err := issueHotProjectionCost(ctx, node, fields)
		if err != nil {
			return err
		}
		if err := read.reserve(ctx, cost+512); err != nil {
			return err
		}
		value, err := issueHotProjection(ctx, node, fields, cost)
		if err != nil {
			return err
		}
		read.data.WorkflowRuns = append(read.data.WorkflowRuns, value.Interface().(domain.WorkflowRun))
		read.runOrdinals = append(read.runOrdinals, int(ordinal.Int()))
		return nil
	}); err != nil {
		return nil, err
	}
	createTeams, users, labels := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, definition := range read.data.WorkflowDefinitions {
		users[definition.CreatorID] = true
		for _, action := range definition.Actions {
			switch action.Type {
			case "notify":
				recipient := action.Config["recipientId"]
				if recipient == "" {
					recipient = definition.CreatorID
				}
				users[recipient] = true
			case "assignIssue":
				users[action.Config["userId"]] = true
			case "addIssueLabel":
				labels[action.Config["labelId"]] = true
			case "createIssue":
				createTeams[action.Config["teamId"]] = true
			}
		}
	}
	users[read.data.Viewer.ID] = true
	if err := read.selectSlice(ctx, "users", func(node metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, node, "ID", users)
	}); err != nil {
		return nil, err
	}
	if err := read.selectSlice(ctx, "labels", func(node metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, node, "ID", labels)
	}); err != nil {
		return nil, err
	}
	stateData := read.data
	if err := read.reserve(ctx, uint64(len(createTeams))*uint64(reflect.TypeFor[domain.Issue]().Size())*2+4096); err != nil {
		return nil, err
	}
	for team := range createTeams {
		stateData.Issues = append(stateData.Issues, domain.Issue{Team: domain.Team{ID: team}})
	}
	if err := read.issueBatchStates(ctx, &stateData, domain.IssueUpdateInput{}); err != nil {
		return nil, err
	}
	issues, _, _ := metadataLogicalField("issues")
	next, overflow := 1, false
	if err := source.fields[issues].Children(ctx, func(ordinal reflect.Value, node metadataLogicalNode) error {
		work := &ResourceRead{store: s}
		defer work.Close()
		value, err := work.projectNode(ctx, node, "Number")
		if err != nil {
			return err
		}
		header := value.Interface().(domain.Issue)
		if header.Number >= next {
			next = header.Number + 1
		}
		overflow = overflow || header.Number == math.MaxInt
		match := false
		if resourceType == "issue" && resourceID != "" && read.resourceOrdinal < 0 {
			match, err = issueHotMatches(ctx, node, "ID", map[string]bool{resourceID: true})
			if err != nil {
				return err
			}
		}
		if match {
			cost, err := issueHotProjectionCost(ctx, node, workflowRunIssueInputFields)
			if err != nil {
				return err
			}
			if err := read.reserve(ctx, cost+512); err != nil {
				return err
			}
			value, err := issueHotProjection(ctx, node, workflowRunIssueInputFields, cost)
			if err != nil {
				return err
			}
			read.resourceOrdinal = int(ordinal.Int())
			read.resourceNode = node
			read.data.Issues = []domain.Issue{value.Interface().(domain.Issue)}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	// Actions only read Number from the unselected issues. A constant-size
	// suffix reproduces their ordered fold, including signed-int overflow.
	// Empty IDs cannot match a nonempty resource trigger.
	if err := read.reserve(ctx, uint64(reflect.TypeFor[domain.Issue]().Size())*4+4096); err != nil {
		return nil, err
	}
	if overflow {
		read.data.Issues = append(read.data.Issues, domain.Issue{Number: math.MaxInt})
		read.placeholders++
	}
	if !overflow || next != math.MinInt {
		read.data.Issues = append(read.data.Issues, domain.Issue{Number: next - 1})
		read.placeholders++
	}
	read.before = cloneBootstrap(read.data)
	return read, nil
}

func (r *workflowRunInputs) image(ctx context.Context) (*metadataLogicalSource, error) {
	after := r.data
	created := len(after.Issues) - len(r.before.Issues)
	if created < 0 || len(after.WorkflowRuns) > 500 || len(after.WorkflowDefinitions) != len(r.before.WorkflowDefinitions) {
		return nil, ErrBoundedMetadataCoverage
	}
	// The closed callback appends exactly one run, trims to 500, and may
	// clear only its last matching predecessor's retry time. Keep every
	// other old field framed at its original source ordinal.
	drop := max(0, len(r.before.WorkflowRuns)+1-500)
	oldCount := len(r.before.WorkflowRuns) - drop
	if len(after.WorkflowRuns) != oldCount+1 || len(r.runOrdinals) != len(r.before.WorkflowRuns) {
		return nil, ErrBoundedMetadataCoverage
	}
	cleared := -1
	for index, next := range after.WorkflowRuns[:oldCount] {
		previous := r.before.WorkflowRuns[index+drop]
		if !reflect.DeepEqual(previous.NextRetryAt, next.NextRetryAt) {
			if r.runOrdinals[index+drop] != r.retryOrdinal || previous.Status != "failed" ||
				previous.NextRetryAt == nil || next.NextRetryAt != nil {
				return nil, ErrBoundedMetadataCoverage
			}
			cleared = r.runOrdinals[index+drop]
		}
		previous.NextRetryAt = next.NextRetryAt
		if !reflect.DeepEqual(previous, next) {
			return nil, ErrBoundedMetadataCoverage
		}
	}
	definitions := map[int]domain.WorkflowDefinition{}
	if r.definitionOrdinal >= 0 {
		definitions[r.definitionOrdinal] = after.WorkflowDefinitions[0]
	}
	definitionPatch, releaseDefinition, err := issueBatchSlicePatchNode(ctx, r.store, r.source, "workflowDefinitions", nil, definitions, nil)
	if err != nil {
		return nil, err
	}
	defer releaseDefinition()
	frames, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, r.source))
	if err != nil {
		return nil, err
	}
	defer frames.Close()
	for _, storage := range r.source.stores {
		if err := frames.inheritLocations(storage); err != nil {
			return nil, err
		}
	}
	if err := frames.inheritTimestampInputs(ctx); err != nil {
		return nil, err
	}
	shapes := map[string]metadataLogicalNode{}
	for _, name := range []string{"issues", "workflowRuns", "notifications"} {
		index, _, _ := metadataLogicalField(name)
		shape, err := legacyDiscussionShape(frames, r.source.fields[index].typ, false)
		if err != nil {
			return nil, err
		}
		shapes[name] = shape
	}
	var issuePatch metadataAggregateNode
	if r.resourceOrdinal >= 0 && !reflect.DeepEqual(r.before.Issues[0], after.Issues[created]) {
		if r.resourceNode.typ != reflect.TypeFor[domain.Issue]() {
			return nil, ErrBoundedMetadataCoverage
		}
		deltas := &legacyDiscussionInputs{input: r.issueHotInputs}
		node, err := deltas.deltaFrame(ctx, frames, r.resourceNode, legacyDiscussionRecordDelta{
			before: reflect.ValueOf(r.before.Issues[0]), next: reflect.ValueOf(after.Issues[created]),
			allowed: workflowRunIssueWriteFields,
		})
		if err != nil {
			return nil, err
		}
		index, _, _ := metadataLogicalField("issues")
		issuePatch = legacyDiscussionSlicePatch{
			base: r.source.fields[index], shape: shapes["issues"],
			replacements: map[int]metadataLogicalNode{r.resourceOrdinal: node},
		}
	}
	return r.source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		switch name {
		case "workflowDefinitions":
			if definitionPatch != nil {
				return definitionPatch, true, nil
			}
		case "workflowRuns":
			return workflowRunHistoryPatch{
				base: node, shape: shapes[name], ordinals: r.runOrdinals[drop:],
				cleared: cleared, appended: after.WorkflowRuns[oldCount],
			}, true, nil
		case "issues":
			var base metadataAggregateNode = metadataAggregateStoredNode{node}
			if issuePatch != nil {
				base = issuePatch
			}
			if created > 0 {
				return workflowRunPrependIssues{base: base, shape: shapes[name], values: after.Issues[:created]}, true, nil
			}
			if issuePatch != nil {
				return issuePatch, true, nil
			}
		case "notifications":
			if len(after.Notifications) > 0 {
				return workflowRunAppendNotifications{base: node, shape: shapes[name], values: after.Notifications}, true, nil
			}
		}
		return metadataAggregateStoredNode{node}, false, nil
	})
}

type workflowRunHistoryPatch struct {
	base, shape metadataLogicalNode
	ordinals    []int
	cleared     int
	appended    domain.WorkflowRun
}

func (n workflowRunHistoryPatch) nodeType() reflect.Type           { return n.base.typ }
func (n workflowRunHistoryPatch) logicalBase() metadataLogicalNode { return n.shape }
func (n workflowRunHistoryPatch) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n workflowRunHistoryPatch) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	selected := 0
	err := n.base.Children(ctx, func(ordinal reflect.Value, node metadataLogicalNode) error {
		if selected == len(n.ordinals) || int(ordinal.Int()) != n.ordinals[selected] {
			return nil
		}
		var child metadataAggregateNode = metadataAggregateStoredNode{node}
		if int(ordinal.Int()) == n.cleared {
			value := reflect.ValueOf(domain.WorkflowRun{}).FieldByName("NextRetryAt")
			child = legacyDiscussionNativeDelta{base: node, changed: map[string]reflect.Value{"NextRetryAt": value}}
		}
		if err := visit(reflect.ValueOf(selected), child); err != nil {
			return err
		}
		selected++
		return nil
	})
	if err != nil {
		return err
	}
	if selected != len(n.ordinals) {
		return ErrBoundedMetadataCoverage
	}
	return visit(reflect.ValueOf(selected), legacyDiscussionNativeRecord{reflect.ValueOf(n.appended)})
}

type workflowRunPrependIssues struct {
	base   metadataAggregateNode
	shape  metadataLogicalNode
	values []domain.Issue
}

func (n workflowRunPrependIssues) nodeType() reflect.Type           { return n.base.nodeType() }
func (n workflowRunPrependIssues) logicalBase() metadataLogicalNode { return n.shape }
func (n workflowRunPrependIssues) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n workflowRunPrependIssues) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	for index, issue := range n.values {
		if err := visit(reflect.ValueOf(index), legacyDiscussionNativeRecord{reflect.ValueOf(issue)}); err != nil {
			return err
		}
	}
	return n.base.nodeChildren(ctx, func(index reflect.Value, child metadataAggregateNode) error {
		return visit(reflect.ValueOf(int(index.Int())+len(n.values)), child)
	})
}

type workflowRunAppendNotifications struct {
	base, shape metadataLogicalNode
	values      []domain.Notification
}

func (n workflowRunAppendNotifications) nodeType() reflect.Type           { return n.base.typ }
func (n workflowRunAppendNotifications) logicalBase() metadataLogicalNode { return n.shape }
func (n workflowRunAppendNotifications) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n workflowRunAppendNotifications) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	count := 0
	if err := n.base.Children(ctx, func(index reflect.Value, node metadataLogicalNode) error {
		count++
		return visit(index, metadataAggregateStoredNode{node})
	}); err != nil {
		return err
	}
	for index, item := range n.values {
		if err := visit(reflect.ValueOf(count+index), legacyDiscussionNativeRecord{reflect.ValueOf(item)}); err != nil {
			return err
		}
	}
	return ctx.Err()
}
