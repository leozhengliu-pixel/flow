package store

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

type IssueDeleteSelection struct {
	ID           string
	KeepChildren bool
	Principal    string
	Key          *domain.APIKey
}

func (s *SQLiteStore) DeleteIssueScoped(ctx context.Context, workspace string, selection IssueDeleteSelection, payload any, authorize func(domain.Bootstrap, domain.Issue) bool, mutate func(*domain.Bootstrap) error) error {
	recordIDs, _ := ctx.Value(issueRecordMutationContext{}).([]string)
	profile := &issueDeleteImage{
		selection: selection, authorize: authorize, recordIDs: recordIDs,
	}
	ctx = WithoutIssueRecordMutations(ctx)
	ctx = withMutationAggregateID(ctx, selection.ID)
	ctx, _, callback := collectMutationAudit(ctx, workspace, func(data *domain.Bootstrap) (string, error) {
		return selection.ID, mutate(data)
	})
	return s.mutateBoundedScopedImage(ctx, workspace, "issue.deleted", payload, profile, callback)
}

var (
	errIssueDeleteScope   = errors.New("issue mutation scope exceeds 1000 records")
	errIssueDeleteFamily  = errors.New("issue deletion requires a batch job")
	errIssueDeleteInvalid = errors.New("invalid issue mutation scope")
)

type issueDeleteImage struct {
	selection IssueDeleteSelection
	authorize func(domain.Bootstrap, domain.Issue) bool
	recordIDs []string
	deleting  map[string]bool
	ordinals  map[string][]int
}

func (*issueDeleteImage) kind() scopedImageWriteKind { return scopedImageIssueDelete }

func (*issueDeleteImage) extraFields() []string {
	return []string{"asks", "customerRequests", "documents", "drafts", "favorites", "issueSlas",
		"issueSuggestions", "projectTemplates", "releases", "slaEvents", "subscriptions", "trash"}
}

func (p *issueDeleteImage) resolve(_ context.Context, _ *ResourceRead, _ *metadataLogicalSource) (MutationScope, error) {
	if len(p.recordIDs) == 0 || len(p.recordIDs) > 1000 {
		return MutationScope{}, errIssueDeleteInvalid
	}
	return MutationScope{IssueIDs: p.recordIDs}, nil
}

func (*issueDeleteImage) actorRole(ctx context.Context, tx *sqlTx, workspaceID, actorID string) (string, error) {
	var role, status string
	if err := tx.QueryRowContext(ctx, `SELECT role,status FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, actorID).Scan(&role, &status); err != nil || status != "active" {
		return "", ErrAuthForbidden
	}
	return role, nil
}

// The mutation never relies on a request-owned issue header for write access.
// Reapply the issue visibility and role checks to the current transaction.
func (p *issueDeleteImage) checkAccess(ctx context.Context, s *SQLiteStore, tx *sqlTx, workspace string, owner *ResourceRead, source *metadataLogicalSource) error {
	if p.selection.Principal == "" {
		return nil
	}
	caller, err := owner.installedResourcePolicy(ctx)
	if err != nil {
		return err
	}
	ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{
		database: s.db, workspace: workspace, reader: tx, metadata: caller,
	})
	policy := &IssueWriteAuthorizationRead{owner: &ResourceRead{store: s, source: source}, query: IssueRecordQuery{
		Workspace: workspace, Archived: "all", Filter: IssueFilter{Field: "id", Values: []string{p.selection.ID}},
	}}
	// This owner borrows the attempt's source.
	if err := source.Retain(); err != nil {
		return err
	}
	defer policy.owner.Close()
	if err := policy.loadPolicy(ctx, p.selection.Principal, p.selection.Key); err != nil {
		return err
	}
	user, role, members, err := s.resourceAccessPrincipal(ctx, caller.Workspace.ID, p.selection.Principal)
	if err != nil {
		return err
	}
	caller.Viewer, caller.ViewerRole, caller.TeamMembers = user, role, members
	visible := visibleIssueTeams(caller, user.ID, role)
	active := len(caller.Teams) - len(ArchivedTeamIDs(caller))
	policy.query.Access = &IssueRecordAccess{UserID: user.ID, WorkspaceID: caller.Workspace.ID,
		Admin: isWorkspaceAdminRole(role) && len(visible) == active, WorkspaceAdmin: isWorkspaceAdminRole(role),
		VisibleTeamIDs: visible, ArchivedTeamIDs: ArchivedTeamIDs(caller)}
	if (&resourceAccessPolicy{key: p.selection.Key}).selected() {
		policy.query.AllowedTeamIDs = append([]string{}, p.selection.Key.TeamIDs...)
	}
	if err := policy.loadIssueHeaders(ctx); err != nil {
		return err
	}
	for _, issue := range policy.Data.Issues {
		if issue.ID == p.selection.ID && p.authorize != nil && p.authorize(policy.Data, issue) {
			return nil
		}
	}
	return errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
}

func (p *issueDeleteImage) loadRecords(ctx context.Context, attempt *scopedImageAttempt, tx *sqlTx, workspace string, scope MutationScope, source *metadataLogicalSource) (map[string]storedIssueRecord, error) {
	s := attempt.owner.store
	p.deleting = map[string]bool{}
	records, err := attempt.issueRecords(ctx, scope.IssueIDs)
	if err != nil {
		return nil, err
	}
	if _, ok := records[p.selection.ID]; !ok {
		return records, nil
	}
	if err := p.checkAccess(ctx, s, tx, workspace, attempt.owner, source); err != nil {
		return nil, err
	}
	add := func(ids []string, raw bool) error {
		if !raw {
			ids = slices.DeleteFunc(slices.Clone(ids), func(id string) bool { _, ok := records[id]; return ok })
		}
		if len(ids) == 0 {
			return nil
		}
		found, err := attempt.issueRecords(ctx, ids)
		if err != nil {
			return err
		}
		for id, record := range found {
			if raw {
				// The legacy scoped writer takes the child row as stored,
				// even when it replaces an already selected issue.
				record.issue = domain.Issue{}
				if err := json.Unmarshal(record.raw, &record.issue); err != nil {
					return err
				}
			}
			records[id] = record
		}
		return nil
	}
	p.deleting[p.selection.ID] = true
	// Match the legacy issue-record preimage: explicit targets, their
	// parent and relation rows, the indexed children of explicit targets
	// and, unless children are kept, their descendants.
	var linked []string
	for _, id := range scope.IssueIDs {
		if record, ok := records[id]; ok {
			if record.issue.ParentID != nil {
				linked = append(linked, *record.issue.ParentID)
			}
			for _, relation := range record.issue.Relations {
				linked = append(linked, relation.RelatedIssueID)
			}
		}
	}
	if err := add(linked, false); err != nil {
		return nil, err
	}
	if len(records) > 1000 {
		return nil, errIssueDeleteScope
	}
	children, err := issueDeleteChildren(ctx, tx, workspace, scope.IssueIDs, 1001)
	if err != nil {
		return nil, err
	}
	if len(children) > 1000 {
		return nil, errIssueDeleteFamily
	}
	if err := add(children, true); err != nil {
		return nil, err
	}
	if len(records) > 1000 {
		return nil, errIssueDeleteFamily
	}
	if !p.selection.KeepChildren {
		// A deletion trashes the whole family: follow the sub-issues down
		// one indexed level at a time, within the same family limit.
		seen := map[string]bool{}
		for _, id := range append(slices.Clone(scope.IssueIDs), children...) {
			seen[id] = true
		}
		for level := children; len(level) > 0; {
			found, err := issueDeleteChildren(ctx, tx, workspace, level, 1001)
			if err != nil {
				return nil, err
			}
			level = slices.DeleteFunc(found, func(id string) bool { return seen[id] })
			for _, id := range level {
				seen[id] = true
			}
			if err := add(level, true); err != nil {
				return nil, err
			}
			if len(records) > 1000 {
				return nil, errIssueDeleteFamily
			}
		}
		childrenByParent := map[string][]string{}
		for id, record := range records {
			if parent := record.issue.ParentID; parent != nil {
				childrenByParent[*parent] = append(childrenByParent[*parent], id)
			}
		}
		queue := []string{p.selection.ID}
		for i := 0; i < len(queue); i++ {
			for _, child := range childrenByParent[queue[i]] {
				if !p.deleting[child] {
					p.deleting[child] = true
					queue = append(queue, child)
				}
			}
		}
	}
	return records, nil
}

func issueDeleteChildren(ctx context.Context, tx *sqlTx, workspace string, parents []string, limit int) ([]string, error) {
	parents = slices.Clone(parents)
	slices.Sort(parents)
	parents = slices.Compact(parents)
	ids := []string{}
	lock := ""
	if tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	for start := 0; start < len(parents) && len(ids) < limit; start += 500 {
		where, args := bindList("parent_id", parents[start:min(start+500, len(parents))])
		args = append(append([]any{workspace}, args...), limit-len(ids))
		found, err := issueDeleteIDs(ctx, tx, `SELECT id FROM issue_records WHERE workspace_key=? AND `+where+` ORDER BY parent_id,id LIMIT ?`+lock, args...)
		if err != nil {
			return nil, err
		}
		ids = append(ids, found...)
	}
	return ids, nil
}

func issueDeleteIDs(ctx context.Context, tx *sqlTx, query string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Link-only rows keep their native bodies in the source. The callback needs
// only their ID and the field it clears, or the full removed preference row.
var issueDeleteRows = []struct {
	field, key string
	link       bool
}{
	{"asks", "IssueID", false}, {"customerRequests", "IssueID", true}, {"documents", "IssueID", true},
	{"drafts", "ResourceID", false}, {"favorites", "ResourceID", false}, {"issueSlas", "IssueID", false},
	{"issueSuggestions", "IssueID", false}, {"projectTemplates", "IssueIDs", true}, {"releases", "IssueIDs", true},
	{"slaEvents", "IssueID", false}, {"subscriptions", "ResourceID", false},
}

func (p *issueDeleteImage) inputs(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource) (domain.Bootstrap, error) {
	data := domain.Bootstrap{}
	p.ordinals = map[string][]int{}
	out := reflect.ValueOf(&data).Elem()
	for _, name := range []string{"workspace", "viewer", "viewerRole"} {
		index, _, _ := metadataLogicalField(name)
		value, err := cycleScheduleNativeValue(ctx, owner, source.fields[index])
		if err != nil {
			return data, err
		}
		out.Field(index).Set(value)
	}
	if len(p.deleting) == 0 {
		return data, nil
	}
	for _, selected := range issueDeleteRows {
		index, _, _ := metadataLogicalField(selected.field)
		outField := out.Field(index)
		err := scopedImageVisit(ctx, source, selected.field, func(ordinal int, node metadataLogicalNode) error {
			matched := false
			names := []string{selected.key}
			if selected.link {
				names = append(names, "ID")
			}
			err := owner.withHeader(ctx, node, names, func(value reflect.Value) error {
				key := value.FieldByName(selected.key)
				if key.Kind() == reflect.String {
					matched = p.deleting[key.String()]
				} else {
					matched = slices.ContainsFunc(key.Interface().([]string), func(id string) bool { return p.deleting[id] })
				}
				if matched && selected.link {
					value, err := cycleScheduleProjectNode(ctx, owner, node, names...)
					if err != nil {
						return err
					}
					outField.Set(reflect.Append(outField, value))
				}
				return nil
			})
			if err != nil || !matched {
				return err
			}
			if err := owner.reserve(ctx, uint64(node.typ.Size())*2+128); err != nil {
				return err
			}
			if !selected.link {
				value, err := cycleScheduleNativeValue(ctx, owner, node)
				if err != nil {
					return err
				}
				outField.Set(reflect.Append(outField, value))
			}
			p.ordinals[selected.field] = append(p.ordinals[selected.field], ordinal)
			return nil
		})
		if err != nil {
			return data, err
		}
	}
	return data, nil
}

func (*issueDeleteImage) reserveCallback(ctx context.Context, owner *ResourceRead, data domain.Bootstrap, payload any) error {
	// The callback retains encoded issue preimages and captured links as trash.
	return (&boundedDiscussionAttempt{owner: owner}).reserveCallback(ctx, data, payload)
}

func (p *issueDeleteImage) validate(before, after domain.Bootstrap) error {
	if len(after.Trash) != len(p.deleting) || len(before.Issues)-len(after.Issues) != len(p.deleting) {
		return ErrBoundedMetadataCoverage
	}
	seen := map[string]bool{}
	for _, entry := range after.Trash {
		if entry.ResourceType != "issue" || !p.deleting[entry.ResourceID] || seen[entry.ResourceID] {
			return ErrBoundedMetadataCoverage
		}
		seen[entry.ResourceID] = true
	}
	for _, issue := range after.Issues {
		if p.deleting[issue.ID] {
			return ErrBoundedMetadataCoverage
		}
	}
	return nil
}

func (p *issueDeleteImage) image(ctx context.Context, _ *ResourceRead, source *metadataLogicalSource, before, after domain.Bootstrap) (*metadataLogicalSource, error) {
	frames, err := newMetadataLogicalBuilderIn(ctx, source.spoolGroup())
	if err != nil {
		return nil, err
	}
	defer frames.Close()
	return source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		overlay := issueDeleteCollection{base: original}
		if name == "trash" && len(after.Trash) > 0 {
			overlay.shape, err = legacyDiscussionShape(frames, original.typ, false)
			for i := range after.Trash {
				overlay.prefix = append(overlay.prefix, legacyMetadataWriteNativeRecord{reflect.ValueOf(after.Trash[i])})
			}
			return overlay, true, err
		}
		for _, selected := range issueDeleteRows {
			if name != selected.field || len(p.ordinals[name]) == 0 {
				continue
			}
			index, _, _ := metadataLogicalField(name)
			old, next := reflect.ValueOf(before).Field(index), reflect.ValueOf(after).Field(index)
			if selected.link && old.Len() != next.Len() {
				return nil, false, ErrBoundedMetadataCoverage
			}
			overlay.remove, overlay.fields = map[int]bool{}, map[int]map[string]reflect.Value{}
			j := 0
			for i, ordinal := range p.ordinals[name] {
				switch {
				case selected.link:
					oldField, nextField := old.Index(i).FieldByName(selected.key), next.Index(i).FieldByName(selected.key)
					if !metadataValueEqual(oldField, nextField) {
						overlay.fields[ordinal] = map[string]reflect.Value{selected.key: nextField}
					}
				case j < next.Len() && metadataValueEqual(old.Index(i), next.Index(j)):
					j++
				default:
					overlay.remove[ordinal] = true
				}
			}
			if !selected.link && j != next.Len() {
				return nil, false, ErrBoundedMetadataCoverage
			}
			return overlay, len(overlay.remove)+len(overlay.fields) != 0, nil
		}
		return metadataAggregateStoredNode{original}, false, nil
	})
}

type issueDeleteCollection struct {
	base, shape metadataLogicalNode
	prefix      []metadataAggregateNode
	remove      map[int]bool
	fields      map[int]map[string]reflect.Value
}

func (n issueDeleteCollection) logicalBase() metadataLogicalNode {
	if n.shape.typ != nil {
		return n.shape
	}
	return n.base
}
func (n issueDeleteCollection) nodeType() reflect.Type { return n.base.typ }
func (n issueDeleteCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n issueDeleteCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	pos := 0
	for _, entry := range n.prefix {
		if err := visit(reflect.ValueOf(pos), entry); err != nil {
			return err
		}
		pos++
	}
	return n.base.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		i := int(key.Int())
		if n.remove[i] {
			return nil
		}
		var entry metadataAggregateNode = metadataAggregateStoredNode{node}
		if patch := n.fields[i]; len(patch) > 0 {
			entry = legacyDiscussionNativeDelta{base: node, changed: patch}
		}
		err := visit(reflect.ValueOf(pos), entry)
		pos++
		return err
	})
}

func (p *issueDeleteImage) persistDeleted(ctx context.Context, tx *sqlTx, workspace string, loaded map[string]domain.Issue) ([]string, error) {
	ids := slices.Sorted(maps.Keys(p.deleting))
	deltas := map[issueStatsKey]int64{}
	for _, id := range ids {
		issue, ok := loaded[id]
		if !ok {
			return nil, ErrBoundedMetadataCoverage
		}
		addIssueStats(deltas, issueStatsOf(issue), -1)
		delete(loaded, id)
	}
	if err := writeIssueStats(ctx, tx, workspace, deltas); err != nil {
		return nil, err
	}
	for _, table := range []string{"issue_label_records", "issue_permission_records", "issue_subscriber_records", "issue_actor_records",
		"issue_attribute_records", "issue_attachment_records", "issue_search_documents", "issue_identifier_aliases"} {
		if err := deleteRowsByIDs(ctx, tx, table, "issue_id", workspace, ids); err != nil {
			return nil, err
		}
	}
	if err := deleteRowsByIDs(ctx, tx, "issue_records", "id", workspace, ids); err != nil {
		return nil, err
	}
	_, err := tx.ExecContext(ctx, `UPDATE issue_collection_counts SET total=total-? WHERE workspace_key=?`, len(ids), workspace)
	return ids, err
}
