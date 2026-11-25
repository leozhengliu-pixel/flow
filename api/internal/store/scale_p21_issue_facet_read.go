package store

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

// IssueFacetRead owns the compact policy, directory names and per-issue
// authority of the filter-suggestion read. Visit streams the facet
// occurrences of every issue the literal projection keeps, in issue order,
// with at most one decoded issue alive. It never counts a selected page.
type IssueFacetRead struct {
	owner     *ResourceRead
	tx        *sqlTx
	workspace string
	principal bool
	sqlIssues bool
	// keyTeams is non-nil only for a selected-team credential.
	keyTeams []string
	names    map[string]map[string]string
	labels   map[string]bool
	projects map[string]bool
	cycles   map[string]string
	// visible[i] is the literal projection's decision for issue ordinal i.
	visible []bool
	ids     []string
	once    sync.Once
}

// issueFacetAuthority is the compact record the literal issue filter reads:
// the issue's team, its parent reference and whether one of its own grants
// reaches the viewer.
type issueFacetAuthority struct {
	id, team, parent string
	grant            bool
}

func (r *IssueFacetRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.names, r.labels, r.projects, r.cycles = nil, nil, nil, nil
			r.visible, r.ids, r.keyTeams = nil, nil, nil
			if r.tx != nil {
				_ = r.tx.Rollback()
				r.tx = nil
			}
			r.owner.Close()
		})
	}
}

// ReserveOutput covers the caller's accumulator and retained encoded result.
func (r *IssueFacetRead) ReserveOutput(ctx context.Context, bytes uint64) error {
	if r == nil || r.owner == nil || r.tx == nil {
		return errMetadataLogicalClosed
	}
	return r.owner.reserve(context.WithValue(ctx, metadataWorkLeaseKey{}, nil), bytes)
}

func (r *IssueFacetRead) ReserveValue(ctx context.Context, value any) error {
	work, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(value), 0)
	if err != nil {
		return err
	}
	return r.ReserveOutput(ctx, uint64(work))
}

// BeginIssueFacetRead preserves workspaceData's two sources: an empty userID
// is the development projection (BootstrapFor, every issue), otherwise the
// literal BootstrapForUser viewer projection followed by the credential's
// team filter. Live membership is read as projectBootstrapForUser reads it.
func (s *SQLiteStore) BeginIssueFacetRead(ctx context.Context, workspace, userID string, key *domain.APIKey) (_ *IssueFacetRead, err error) {
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{})
	installed, owner, err := readLeafBegin(ctx, s, workspace)
	if err != nil {
		return nil, err
	}
	defer installed.Close()
	r := &IssueFacetRead{
		owner: owner, principal: userID != "",
		names: map[string]map[string]string{
			"team": {}, "status": {}, "assignee": {}, "label": {}, "project": {},
		},
		labels: map[string]bool{}, projects: map[string]bool{}, cycles: map[string]string{},
	}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	ctx = metadataLogicalSourceScratchContext(ctx, owner.source)
	policy, err := owner.installedResourcePolicy(ctx)
	if err != nil {
		return nil, err
	}
	r.workspace = policy.Workspace.URLKey
	var scope bootstrapTeamScope
	if r.principal {
		user, role, members, err := s.resourceAccessPrincipal(ctx, policy.Workspace.ID, userID)
		if err != nil {
			return nil, err
		}
		policy.Viewer, policy.ViewerRole, policy.TeamMembers = user, role, members
		if err := owner.reserve(ctx, uint64(len(policy.Teams))*4096+uint64(len(members))*1024+64<<10); err != nil {
			return nil, err
		}
		allowed := map[string]bool{}
		for _, team := range policy.Teams {
			if teamVisibleToUser(policy, team.ID, userID, role) {
				allowed[team.ID] = true
			}
		}
		scope = viewerTeamScope(policy, allowed, userID, role)
		if (&resourceAccessPolicy{key: key}).selected() {
			cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(key.TeamIDs), 0)
			if err != nil {
				return nil, err
			}
			if cost > (metadataWorkBytes-4096)/8 {
				return nil, ErrMetadataWorkBudget
			}
			if err := owner.reserve(ctx, cost*8+4096); err != nil {
				return nil, err
			}
			r.keyTeams = make([]string, 0, len(key.TeamIDs))
			for _, id := range key.TeamIDs {
				r.keyTeams = append(r.keyTeams, strings.Clone(id))
			}
		}
	}
	if err := r.catalog(ctx, scope); err != nil {
		return nil, err
	}
	index, _, _ := metadataLogicalField("issues")
	r.sqlIssues, err = owner.source.fields[index].isNil(ctx)
	if err != nil {
		return nil, err
	}
	r.tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return nil, err
	}
	if r.principal {
		var records []issueFacetAuthority
		if r.sqlIssues {
			records, err = r.sqlAuthority(ctx, policy, scope)
		} else {
			records, err = r.nativeAuthority(ctx, policy, scope)
		}
		if err != nil {
			return nil, err
		}
		if err := r.decide(ctx, policy, scope, records); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *IssueFacetRead) keyAllowed(team string) bool {
	return r.keyTeams == nil || slices.Contains(r.keyTeams, team)
}

// catalog keeps the reference names the literal response reads: teams,
// states, users and project summaries refresh from their last duplicate
// directory entry; labels from the last entry left by the viewer projection
// (and a selected credential's, whose projection refreshes counts again);
// cycles from the first entry left by the viewer and credential projections.
func (r *IssueFacetRead) catalog(ctx context.Context, scope bootstrapTeamScope) error {
	for _, collection := range []struct {
		kind, facet string
		fields      []string
	}{
		{"teams", "team", []string{"ID", "Name"}},
		{"states", "status", []string{"ID", "Name"}},
		{"users", "assignee", []string{"ID", "DisplayName"}},
		{"labels", "label", []string{"ID", "Name", "Scope"}},
		{"projects", "project", []string{"ID", "Name", "TeamIDs"}},
		{"cycles", "cycle", []string{"ID", "Name", "TeamID"}},
	} {
		err := r.owner.visitNative(ctx, collection.kind, func(_ int, node metadataLogicalNode) error {
			return r.owner.withHeader(ctx, node, collection.fields, func(value reflect.Value) error {
				id := value.FieldByName("ID").String()
				name := "Name"
				if collection.kind == "users" {
					name = "DisplayName"
				}
				label := value.FieldByName(name).String()
				if err := r.owner.reserve(ctx, uint64(len(id)+len(label))*8+2048); err != nil {
					return err
				}
				id, label = strings.Clone(id), strings.Clone(label)
				switch collection.kind {
				case "labels":
					if !r.principal {
						break
					}
					labelScope := value.Interface().(domain.IssueLabel).Scope
					// filterBootstrapTeams, then filterBootstrapForAPIKey.
					normalized := strings.ToLower(strings.TrimSpace(labelScope))
					if normalized != "" && normalized != "workspace" && !scope.Teams[labelScope] {
						return nil
					}
					trimmed := strings.TrimSpace(labelScope)
					if r.keyTeams != nil && trimmed != "" && !strings.EqualFold(trimmed, "workspace") && !r.keyAllowed(labelScope) {
						// A selected credential refreshes resource counts
						// again over its own label list (applyAPIKeyProjection).
						return nil
					}
					r.labels[id] = true
				case "projects":
					if !r.principal {
						break
					}
					teams := value.Interface().(domain.Project).TeamIDs
					visible := len(teams) == 0
					keyVisible := false
					for _, team := range teams {
						if scope.Content[team] {
							visible = true
							keyVisible = keyVisible || r.keyAllowed(team)
						}
					}
					if visible && (r.keyTeams == nil || keyVisible) {
						r.projects[id] = true
					}
				case "cycles":
					team := value.Interface().(domain.Cycle).TeamID
					if r.principal && (!scope.Content[team] || !r.keyAllowed(team)) {
						return nil
					}
					if _, exists := r.cycles[id]; !exists {
						r.cycles[id] = label
					}
					return nil
				}
				r.names[collection.facet][id] = label
				return nil
			})
		})
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

// The viewer's own grants. issue_permission_records hold exactly the
// non-empty, non-"none" grants issuePermissionAllows considers.
func issueFacetGrant(policy domain.Bootstrap, scope bootstrapTeamScope, subjectType, subjectID string) bool {
	switch subjectType {
	case "user":
		return subjectID == policy.Viewer.ID
	case "workspace":
		return subjectID == "" || subjectID == policy.Workspace.ID || subjectID == policy.Workspace.URLKey
	case "team":
		return scope.Content[subjectID]
	}
	return false
}

func (r *IssueFacetRead) retain(ctx context.Context, records *[]issueFacetAuthority, record issueFacetAuthority) error {
	cost := uint64(len(record.id)+len(record.team)+len(record.parent))*2 + uint64(reflect.TypeFor[issueFacetAuthority]().Size())*2 + 64
	if err := r.owner.reserve(ctx, cost); err != nil {
		return err
	}
	record.id, record.team, record.parent = strings.Clone(record.id), strings.Clone(record.team), strings.Clone(record.parent)
	*records = append(*records, record)
	return nil
}

func (r *IssueFacetRead) nativeAuthority(ctx context.Context, policy domain.Bootstrap, scope bootstrapTeamScope) ([]issueFacetAuthority, error) {
	records := []issueFacetAuthority{}
	err := r.owner.visitNative(ctx, "issues", func(_ int, node metadataLogicalNode) error {
		return r.owner.withHeader(ctx, node, []string{"ID", "Team", "ParentID", "Permissions"}, func(value reflect.Value) error {
			issue := value.Interface().(domain.Issue)
			record := issueFacetAuthority{id: issue.ID, team: issue.Team.ID}
			if issue.ParentID != nil {
				record.parent = *issue.ParentID
			}
			for _, permission := range issue.Permissions {
				if permission.Role == "" || strings.EqualFold(permission.Role, "none") {
					continue
				}
				if issueFacetGrant(policy, scope, permission.SubjectType, permission.SubjectID) {
					record.grant = true
					break
				}
			}
			return r.retain(ctx, &records, record)
		})
	})
	return records, err
}

func (r *IssueFacetRead) sqlAuthority(ctx context.Context, policy domain.Bootstrap, scope bootstrapTeamScope) ([]issueFacetAuthority, error) {
	records := []issueFacetAuthority{}
	err := r.walkIssueHeaders(ctx, true, func(headers []issueFacetHeader) error {
		for _, header := range headers {
			if err := r.retain(ctx, &records, issueFacetAuthority{id: header.id, team: header.team, parent: header.parent}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Grants are matched exactly in Go, independent of SQL collation.
	ordinals := make(map[string]int, len(records))
	if err := r.owner.reserve(ctx, uint64(len(records))*128+4096); err != nil {
		return nil, err
	}
	for index, record := range records {
		if _, exists := ordinals[record.id]; !exists {
			ordinals[record.id] = index
		}
	}
	rows, err := r.tx.QueryContext(ctx, `SELECT issue_id,subject_type,subject_id FROM issue_permission_records WHERE workspace_key=?`, r.workspace)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var issueID, subjectType, subjectID string
		if err := rows.Scan(&issueID, &subjectType, &subjectID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if index, found := ordinals[issueID]; found && issueFacetGrant(policy, scope, subjectType, subjectID) {
			records[index].grant = true
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return records, ctx.Err()
}

// decide replays filterBootstrapTeams' in-place slices.DeleteFunc exactly:
// while issue j is judged, the slice holds the kept issues before j followed
// by the original issues from the kept count onward, and
// issuePermissionAllows resolves a parent by its first match in that slice.
// The credential's team filter is applied afterwards, as in the server.
func (r *IssueFacetRead) decide(ctx context.Context, policy domain.Bootstrap, scope bootstrapTeamScope, records []issueFacetAuthority) error {
	// first/last maps, the duplicate chain, kept and visible flags and the
	// retained ID slice headers.
	if err := r.owner.reserve(ctx, uint64(len(records))*256+4096); err != nil {
		return err
	}
	first := make(map[string]int, len(records))
	next := make([]int, len(records))
	last := map[string]int{}
	for index, record := range records {
		next[index] = -1
		if previous, exists := last[record.id]; exists {
			next[previous] = index
		} else {
			first[record.id] = index
		}
		last[record.id] = index
	}
	clear(last)
	archived := map[string]bool{}
	for _, id := range ArchivedTeamIDs(policy) {
		archived[id] = true
	}
	admin := isWorkspaceAdminRole(policy.ViewerRole)
	kept := make([]bool, len(records))
	keptCount := 0
	lookup := func(id string, judged int) int {
		start, found := first[id]
		if !found {
			return -1
		}
		for index := start; index >= 0; index = next[index] {
			if index < judged && kept[index] {
				return index
			}
		}
		for index := start; index >= 0; index = next[index] {
			if index >= keptCount {
				return index
			}
		}
		return -1
	}
	allows := func(judged int) bool {
		if admin && !scope.AdminClosed[records[judged].team] {
			return true
		}
		visited := map[string]bool{}
		for current := judged; current >= 0 && !visited[records[current].id]; {
			visited[records[current].id] = true
			if records[current].grant {
				return true
			}
			if records[current].parent == "" {
				break
			}
			current = lookup(records[current].parent, judged)
		}
		return false
	}
	r.visible = make([]bool, len(records))
	r.ids = make([]string, len(records))
	for index, record := range records {
		if index%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		keep := false
		switch {
		case archived[record.team]:
		case scope.Content[record.team]:
			keep = true
		default:
			keep = allows(index)
		}
		kept[index] = keep
		if keep {
			keptCount++
		}
		r.visible[index] = keep && r.keyAllowed(record.team)
		r.ids[index] = record.id
	}
	return nil
}

// Visit emits occurrences, including repeated labels, of every kept issue in
// the literal issue order.
func (r *IssueFacetRead) Visit(ctx context.Context, visit func(field, value, label string) error) error {
	if r == nil || r.owner == nil || r.tx == nil || visit == nil {
		return errMetadataLogicalClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	ctx = metadataLogicalSourceScratchContext(ctx, r.owner.source)
	emit := func(issue domain.Issue) error {
		add := func(field, id, fallback string) error {
			label, known := r.names[field][id]
			if !known {
				label = fallback
			}
			return visit(field, id, label)
		}
		if err := add("team", issue.Team.ID, issue.Team.Name); err != nil {
			return err
		}
		if err := add("status", issue.State.ID, issue.State.Name); err != nil {
			return err
		}
		if issue.Assignee != nil {
			if err := add("assignee", issue.Assignee.ID, issue.Assignee.DisplayName); err != nil {
				return err
			}
		}
		for _, label := range issue.Labels {
			if r.principal && !r.labels[label.ID] {
				continue
			}
			if err := add("label", label.ID, label.Name); err != nil {
				return err
			}
		}
		if issue.Project != nil && (!r.principal || r.projects[issue.Project.ID]) {
			if err := add("project", issue.Project.ID, issue.Project.Name); err != nil {
				return err
			}
		}
		if issue.CycleID != nil {
			if label, visible := r.cycles[*issue.CycleID]; visible {
				if err := visit("cycle", *issue.CycleID, label); err != nil {
					return err
				}
			}
		}
		return ctx.Err()
	}
	if r.sqlIssues {
		return r.visitSQL(ctx, emit)
	}
	return r.owner.visitNative(ctx, "issues", func(ordinal int, node metadataLogicalNode) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.principal && (ordinal >= len(r.visible) || !r.visible[ordinal]) {
			if ordinal >= len(r.visible) {
				return ErrBoundedMetadataCoverage
			}
			return nil
		}
		return r.owner.withHeader(ctx, node, []string{"Team", "State", "Assignee", "Labels", "Project", "CycleID"}, func(value reflect.Value) error {
			return emit(value.Interface().(domain.Issue))
		})
	})
}

func (r *IssueFacetRead) visitSQL(ctx context.Context, emit func(domain.Issue) error) error {
	refs := newIssueReferences(domain.Bootstrap{})
	ordinal := 0
	return r.walkIssueHeaders(ctx, false, func(headers []issueFacetHeader) error {
		selected := make([]legacyIssueScanHeader, 0, len(headers))
		for _, header := range headers {
			if r.principal {
				if ordinal >= len(r.ids) || r.ids[ordinal] != header.id {
					return ErrResourceAccessChanged
				}
				if !r.visible[ordinal] {
					ordinal++
					continue
				}
			}
			ordinal++
			selected = append(selected, header.legacyIssueScanHeader)
		}
		for start := 0; start < len(selected); {
			end, size := start+1, selected[start].size
			for end < len(selected) && end-start < bootstrapIssuePayloadBatchRows && size <= bootstrapIssuePayloadBatchBytes &&
				selected[end].size <= bootstrapIssuePayloadBatchBytes-size {
				size += selected[end].size
				end++
			}
			if err := r.owner.store.visitBootstrapIssueWalkBatch(ctx, r.tx, r.workspace, selected[start:end], size, refs, false, emit); err != nil {
				return err
			}
			start = end
		}
		return nil
	}, func() error {
		if r.principal && ordinal != len(r.ids) {
			return ErrResourceAccessChanged
		}
		return nil
	})
}

type issueFacetHeader struct {
	legacyIssueScanHeader
	team, parent string
}

// walkIssueHeaders pages every issue row of the workspace in the literal
// collection order, at most 50 locators per page, inside the read snapshot.
func (r *IssueFacetRead) walkIssueHeaders(ctx context.Context, authority bool, page func([]issueFacetHeader) error, done ...func() error) error {
	s := r.owner.store
	cursor := &ResourceRead{store: s}
	defer cursor.Close()
	length := cursor.byteLength
	var maxID, maxTeam, maxRef int64
	if err := r.tx.QueryRowContext(ctx, "SELECT COALESCE(MAX("+length("id")+"),0),COALESCE(MAX("+length("team_id")+"),0),COALESCE(MAX("+length("parent_id")+"),0) FROM issue_records WHERE workspace_key=?",
		r.workspace).Scan(&maxID, &maxTeam, &maxRef); err != nil {
		return err
	}
	if maxID < 0 || maxTeam < 0 || maxRef < 0 || maxID > (metadataWorkBytes-4096)/32 || maxTeam > (metadataWorkBytes-4096)/32 || maxRef > (metadataWorkBytes-4096)/32 {
		return ErrMetadataWorkBudget
	}
	perHeader := uint64(maxID)*8 + 512
	if authority {
		perHeader += uint64(maxTeam+maxRef) * 8
	}
	pageRows := min(legacyIssueScanPageRows, max(1, int((256<<10)/perHeader)))
	if err := cursor.reserve(ctx, uint64(pageRows)*perHeader+uint64(maxID)*8+4096); err != nil {
		return err
	}
	storage := "0"
	if s.dialect == "sqlite" {
		storage = "CASE WHEN typeof(i.id)='blob' THEN 1 ELSE 0 END"
	}
	lastID, lastOrder, lastBlob, started := "", int64(0), false, false
	for {
		where := "i.workspace_key=?"
		values := []any{maxID, r.workspace}
		if started {
			where += " AND (i.collection_order,i.id)>(?,?)"
			values = append(values, lastOrder, bootstrapContentKey(s, lastID, lastBlob))
		}
		columns := "CASE WHEN " + length("i.id") + "<=? THEN i.id ELSE NULL END,i.collection_order," + length("i.id") + "," + length("i.data") + "," + storage
		if authority {
			columns += ",i.team_id,i.parent_id"
		}
		rows, err := r.tx.QueryContext(ctx, "SELECT "+columns+" FROM issue_records i WHERE "+where+" ORDER BY i.collection_order,i.id LIMIT "+strconv.Itoa(pageRows), values...)
		if err != nil {
			return err
		}
		headers := make([]issueFacetHeader, 0, pageRows)
		for rows.Next() {
			var header issueFacetHeader
			var id sql.NullString
			var keySize, bodySize sql.NullInt64
			targets := []any{&id, &header.order, &keySize, &bodySize, &header.blob}
			if authority {
				targets = append(targets, &header.team, &header.parent)
			}
			if err := rows.Scan(targets...); err != nil {
				_ = rows.Close()
				return err
			}
			if !id.Valid || !keySize.Valid || !bodySize.Valid || keySize.Int64 < 0 || keySize.Int64 > maxID ||
				int64(len(id.String)) != keySize.Int64 || bodySize.Int64 < 0 || len(headers) >= pageRows ||
				int64(len(header.team)) > maxTeam || int64(len(header.parent)) > maxRef {
				_ = rows.Close()
				return io.ErrUnexpectedEOF
			}
			header.id, header.size = id.String, bodySize.Int64
			headers = append(headers, header)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if len(headers) > 0 {
			if err := page(headers); err != nil {
				return err
			}
		}
		if len(headers) < pageRows {
			if len(done) == 1 {
				if err := done[0](); err != nil {
					return err
				}
			}
			return ctx.Err()
		}
		last := headers[len(headers)-1]
		lastID, lastOrder, lastBlob, started = strings.Clone(last.id), last.order, last.blob, true
	}
}
