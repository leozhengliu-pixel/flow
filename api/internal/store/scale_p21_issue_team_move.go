package store

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// ErrIssueTeamMoveRefused reports a team move outside the bounded kernel's
// closure: an issue moved into a team whose workflow states were not
// selected, renumbered without changing team, or a document edit other than
// the attached documents' derived team scope. It wraps
// ErrBoundedMetadataCoverage and is returned after the callback but before
// any write.
var ErrIssueTeamMoveRefused = fmt.Errorf("%w: bounded team move refused the input", ErrBoundedMetadataCoverage)

// issueTeamMove is the bounded closure of the literal moveIssueToTeam
// (cmd/server) for the issues of one write. Besides the complete teams,
// teamSettings and teamParents fields (which the caller selects), a move
// reads:
//
//   - the workflow states of every possible target team, following
//     InheritWorkflowStatuses parents, plus the workspace states (TeamID "");
//   - the labels whose trimmed name equals, case-folded, a label the moved
//     issues carry or the callback may add, and those labels' groups;
//   - the documents attached to the scoped issues, with their ordinals in
//     the installed collection, and the projects those documents name;
//   - the issue sequence, locked in SQL before any issue row.
//
// Possible targets are the declared teams, the scoped issues' teams, the
// teams their suggestions and projects name. A callback that moves an issue
// elsewhere is refused before the write.
type issueTeamMove struct {
	targets   map[string]bool
	states    map[string]bool
	names     []string
	allLabels bool
	groups    map[string]bool
	documents []int
}

// issueTeamMoveSequence locks the workspace issue sequence and returns the
// number the callback sees as Bootstrap.NextIssueNumber: the larger of the
// installed image's value and the next free number, as the legacy writers
// combine the stored copy with the locked sequence.
func (s *SQLiteStore) issueTeamMoveSequence(ctx context.Context, tx *sqlTx, workspace string, owner *ResourceRead, image *metadataLogicalSource) (int, error) {
	next, err := s.lockedNextIssueNumber(ctx, tx, workspace)
	if err != nil {
		return 0, err
	}
	field, _ := reflect.TypeFor[domain.Bootstrap]().FieldByName("NextIssueNumber")
	node := image.fields[field.Index[0]]
	if node.typ == nil {
		return next, nil
	}
	value, err := owner.node(ctx, node, false)
	if err != nil {
		return 0, err
	}
	return max(int(value.Int()), next), nil
}

// prepareIssueTeamMove derives the move closure from the complete team
// settings already selected in r.data. labels are the label IDs the write
// selects (the issues' and the declared or suggested ones); allLabels says
// every label is selected anyway.
func (r *issueHotInputs) prepareIssueTeamMove(ctx context.Context, issues []domain.Issue, suggestions []domain.IssueSuggestion, declared []string, labels map[string]bool, allLabels bool) (*issueTeamMove, error) {
	move := &issueTeamMove{targets: map[string]bool{}, states: map[string]bool{}, groups: map[string]bool{}, allLabels: allLabels}
	add := func(id string) error {
		if id == "" {
			return nil
		}
		return r.addKey(ctx, move.targets, id)
	}
	for _, id := range declared {
		if err := add(id); err != nil {
			return nil, err
		}
	}
	projects, err := r.keys(ctx)
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		if err := add(issue.Team.ID); err != nil {
			return nil, err
		}
		if issue.Project != nil {
			if err := r.addKey(ctx, projects, issue.Project.ID); err != nil {
				return nil, err
			}
		}
	}
	for _, item := range suggestions {
		if err := add(item.SuggestedTeamID); err != nil {
			return nil, err
		}
	}
	// inferTeam also proposes the teams of the issue's project.
	if len(projects) > 0 {
		index, _, _ := metadataLogicalField("projects")
		if err := r.source.fields[index].Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
			matched, err := issueHotMatches(ctx, record, "ID", projects)
			if err != nil || !matched {
				return err
			}
			_, err = r.withHeader(ctx, record, []string{"TeamIDs"}, func(value reflect.Value) (bool, error) {
				for _, id := range value.Interface().(domain.Project).TeamIDs {
					if err := add(id); err != nil {
						return false, err
					}
				}
				return true, nil
			})
			return err
		}); err != nil {
			return nil, err
		}
	}
	// statesForTeam and stateForTeam follow inherited workflows to the team
	// that owns them and fall back to workspace states.
	for target := range move.targets {
		seen := map[string]bool{}
		for current := target; current != "" && !seen[current]; {
			seen[current] = true
			if err := r.addKey(ctx, move.states, current); err != nil {
				return nil, err
			}
			settings := r.data.TeamSettings[current]
			if !settings.InheritWorkflowStatuses || settings.ParentTeamID == "" {
				break
			}
			current = settings.ParentTeamID
		}
	}
	if err := r.addKey(ctx, move.states, ""); err != nil {
		return nil, err
	}
	if allLabels {
		return move, ctx.Err()
	}
	addName := func(name string) error {
		name = strings.TrimSpace(name)
		if slices.ContainsFunc(move.names, func(item string) bool { return strings.EqualFold(item, name) }) {
			return nil
		}
		if uint64(len(name)) > (metadataWorkBytes-512)/2 {
			return ErrMetadataWorkBudget
		}
		if err := r.reserve(ctx, uint64(len(name))*2+512); err != nil {
			return err
		}
		move.names = append(move.names, name)
		return nil
	}
	for _, issue := range issues {
		for _, label := range issue.Labels {
			if err := addName(label.Name); err != nil {
				return nil, err
			}
		}
	}
	if len(labels) > 0 {
		index, _, _ := metadataLogicalField("labels")
		if err := r.source.fields[index].Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
			matched, err := issueHotMatches(ctx, record, "ID", labels)
			if err != nil || !matched {
				return err
			}
			_, err = r.withHeader(ctx, record, []string{"Name"}, func(value reflect.Value) (bool, error) {
				return true, addName(value.Interface().(domain.IssueLabel).Name)
			})
			return err
		}); err != nil {
			return nil, err
		}
	}
	return move, ctx.Err()
}

// state reports whether a workflow state record belongs to the closure.
func (m *issueTeamMove) state(ctx context.Context, record metadataLogicalNode) (bool, error) {
	return issueHotMatches(ctx, record, "TeamID", m.states)
}

// label reports whether teamMoveLabels may pick this label record as the
// same-named replacement of one the moved issue carries.
func (m *issueTeamMove) label(ctx context.Context, r *issueHotInputs, record metadataLogicalNode) (bool, error) {
	if m.allLabels {
		return true, nil
	}
	if len(m.names) == 0 {
		return false, nil
	}
	return r.withHeader(ctx, record, []string{"Name"}, func(value reflect.Value) (bool, error) {
		name := strings.TrimSpace(value.Interface().(domain.IssueLabel).Name)
		return slices.ContainsFunc(m.names, func(item string) bool { return strings.EqualFold(item, name) }), nil
	})
}

// selectIssueTeamMoveGroups selects the label groups of the selected labels:
// labelAvailableForResource rejects a label whose group is archived or gone.
func (r *issueHotInputs) selectIssueTeamMoveGroups(ctx context.Context, move *issueTeamMove) error {
	for _, label := range r.data.Labels {
		if label.GroupID != "" {
			if err := r.addKey(ctx, move.groups, label.GroupID); err != nil {
				return err
			}
		}
	}
	if err := r.discussionCollectionShape(ctx, "labelGroups"); err != nil {
		return err
	}
	if len(move.groups) == 0 {
		return nil
	}
	return r.selectSlice(ctx, "labelGroups", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "ID", move.groups)
	})
}

// selectIssueTeamMoveDocuments selects, in collection order, the documents
// attached to issueIDs (moveIssueDocuments re-derives their team scope) and
// remembers their ordinals; then the projects those documents name that are
// not selected yet, with the fields InheritedDocumentTeams reads.
func (r *issueHotInputs) selectIssueTeamMoveDocuments(ctx context.Context, move *issueTeamMove, issueIDs map[string]bool, projectFields []string) error {
	if err := r.discussionCollectionShape(ctx, "documents"); err != nil {
		return err
	}
	index, _, _ := metadataLogicalField("documents")
	if err := r.source.fields[index].Children(ctx, func(ordinal reflect.Value, record metadataLogicalNode) error {
		matched, err := issueHotMatches(ctx, record, "IssueID", issueIDs)
		if err != nil || !matched {
			return err
		}
		if record.cost > (metadataWorkBytes-4096)/8 {
			return ErrMetadataWorkBudget
		}
		if err := r.reserve(ctx, record.cost*8+4096); err != nil {
			return err
		}
		value, err := record.Value(ctx, int(record.cost))
		if err != nil {
			return err
		}
		r.data.Documents = append(r.data.Documents, value.Interface().(domain.Document))
		move.documents = append(move.documents, int(ordinal.Int()))
		return nil
	}); err != nil {
		return err
	}
	projects, err := r.keys(ctx)
	if err != nil {
		return err
	}
	for _, document := range r.data.Documents {
		for _, id := range document.ProjectIDs {
			if err := r.addKey(ctx, projects, id); err != nil {
				return err
			}
		}
	}
	for _, project := range r.data.Projects {
		delete(projects, project.ID)
	}
	if len(projects) == 0 {
		return ctx.Err()
	}
	// Every copy of a project ID is selected in one pass, so first-match
	// lookups by ID see the same record as the complete collection.
	return r.selectSlice(ctx, "projects", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "ID", projects)
	}, projectFields...)
}

// check admits the callback's move effects and returns the document edits
// by installed ordinal. Issues may change team only into a target; number
// and identifier change only with the team. Documents may change only in
// IssueTeamID and InheritedTeamIDs, only when attached to a moved issue.
func (m *issueTeamMove) check(before, after domain.Bootstrap) (map[int]domain.Document, error) {
	refuse := func(reason string) error {
		return fmt.Errorf("%w: callback %s", ErrIssueTeamMoveRefused, reason)
	}
	moved := map[string]bool{}
	for index, issue := range after.Issues {
		if index >= len(before.Issues) || before.Issues[index].ID != issue.ID {
			return nil, refuse("changed its issue scope")
		}
		old := before.Issues[index]
		if issue.Team.ID != old.Team.ID {
			if !m.targets[issue.Team.ID] {
				return nil, refuse("moved an issue into a team outside its closure")
			}
			moved[issue.ID] = true
		} else if issue.Number != old.Number || issue.Identifier != old.Identifier {
			return nil, refuse("renumbered an issue without moving it")
		}
	}
	if len(after.Documents) != len(before.Documents) || (after.Documents == nil) != (before.Documents == nil) {
		return nil, refuse("changed the attached documents")
	}
	edits := map[int]domain.Document{}
	for index, document := range after.Documents {
		old := before.Documents[index]
		if reflect.DeepEqual(old, document) {
			continue
		}
		scoped := document
		scoped.IssueTeamID, scoped.InheritedTeamIDs = old.IssueTeamID, old.InheritedTeamIDs
		if !moved[old.IssueID] || !reflect.DeepEqual(old, scoped) {
			return nil, refuse("changed a document beyond its derived team scope")
		}
		edits[m.documents[index]] = document
	}
	return edits, nil
}

// issueTeamMoveDocumentPatch returns the documents collection with edits in
// place and a release for its frames, which the caller holds until its
// Transform consumers finish.
func (s *SQLiteStore) issueTeamMoveDocumentPatch(ctx context.Context, source *metadataLogicalSource, edits map[int]domain.Document) (_ metadataAggregateNode, release func(), err error) {
	release = func() {}
	if len(edits) == 0 {
		return nil, release, nil
	}
	index, _, _ := metadataLogicalField("documents")
	base := source.fields[index]
	frames, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, source))
	if err != nil {
		return nil, release, err
	}
	work := &ResourceRead{store: s}
	release = func() {
		frames.Close()
		work.Close()
	}
	defer func() {
		if err != nil {
			release()
			release = func() {}
		}
	}()
	for _, storage := range source.stores {
		if err := frames.inheritLocations(storage); err != nil {
			return nil, release, err
		}
	}
	if err := frames.inheritTimestampInputs(ctx); err != nil {
		return nil, release, err
	}
	nilValue, err := base.isNil(ctx)
	if err != nil {
		return nil, release, err
	}
	shape, err := legacyDiscussionShape(frames, base.typ, nilValue)
	if err != nil {
		return nil, release, err
	}
	patch := legacyDiscussionSlicePatch{base: base, shape: shape, replacements: map[int]metadataLogicalNode{}}
	for ordinal, value := range edits {
		native := reflect.ValueOf(value)
		cost, err := metadataAggregateNativeCost(ctx, native, 0)
		if err != nil {
			return nil, release, err
		}
		if cost > (metadataWorkBytes-4096)/8 {
			return nil, release, ErrMetadataWorkBudget
		}
		if err := work.reserve(ctx, cost*8+4096); err != nil {
			return nil, release, err
		}
		node, err := frames.writeAggregateNode(legacyDiscussionNativeRecord{value: native}, 0)
		if err != nil {
			return nil, release, err
		}
		if node.blocked || node.unavailable {
			return nil, release, ErrBoundedMetadataCoverage
		}
		patch.replacements[ordinal] = node
	}
	return patch, release, ctx.Err()
}
