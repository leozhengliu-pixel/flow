package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type legacyProgressImage struct {
	store  *SQLiteStore
	source *metadataLogicalSource
	stage  *sw26MigrationStage
	work   *ResourceRead
	index  *legacyProgressIndex
	closed bool
}

var legacyProgressProjectFields = []string{
	"ID", "StartDate", "IssueCountHistory", "ScopeHistory",
	"CompletedScopeHistory", "InProgressScopeHistory", "ProgressHistory",
}

var legacyProgressHistoryFields = legacyProgressProjectFields[2:]

// prepareLegacyProgressImage is progressChangedProjects over the frozen
// callback image. Only one projected project/input group is live at a time;
// the possibly workspace-sized changed-ID set is indexed on disk.
func (s *SQLiteStore) prepareLegacyProgressImage(ctx context.Context, source *metadataLogicalSource, now time.Time) (_ *legacyProgressImage, err error) {
	if s == nil || !s.BoundedMetadataEnabled() || source == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = metadataLogicalSourceScratchContext(ctx, source)
	if err := source.Retain(); err != nil {
		return nil, err
	}
	p := &legacyProgressImage{store: s, source: source, work: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			p.Close()
		}
	}()
	if err := p.work.reserve(ctx, 128<<10); err != nil {
		return nil, err
	}
	p.stage, err = sw26NewMigrationStage(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := p.stage.exec(ctx, 4096, `CREATE TABLE legacy_progress_ids(id TEXT PRIMARY KEY)`); err != nil {
		return nil, err
	}
	p.index = &legacyProgressIndex{store: s, source: source, stage: p.stage}
	if err := p.index.build(ctx, "projects", "issues", "activities", "states", "teamSettings"); err != nil {
		return nil, err
	}
	err = p.index.projects(ctx, func(_ int, node metadataLogicalNode) error {
		group, err := newLegacyProgressGroup(ctx, s, source, p.index)
		if err != nil {
			return err
		}
		defer group.close()
		value, err := group.project(ctx, node, legacyProgressProjectFields...)
		if err != nil {
			return err
		}
		project := value.Interface().(domain.Project)
		issues, activities, err := group.imageInputs(ctx, project.ID)
		if err != nil {
			return err
		}
		if err := group.grid(ctx, project, issues, now); err != nil {
			return err
		}
		if !refreshProjectProgress(&project, issues, group.progress.states, activities, group.inputs.data.TeamSettings, now) {
			return ctx.Err()
		}
		_, err = p.stage.exec(ctx, int64(len(project.ID)+512), `INSERT INTO legacy_progress_ids(id) VALUES(?) ON CONFLICT(id) DO NOTHING`, project.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (p *legacyProgressImage) Close() {
	if p == nil || p.closed {
		return
	}
	p.closed = true
	if p.stage != nil {
		_ = p.stage.Close()
		p.stage = nil
	}
	if p.source != nil {
		p.source.Release()
		p.source = nil
	}
	p.work.Close()
}

func (p *legacyProgressImage) HasProject(ctx context.Context, id string) (bool, error) {
	if p == nil || p.closed {
		return false, errMetadataLogicalClosed
	}
	var found bool
	err := p.stage.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM legacy_progress_ids WHERE id=?)`, id).Scan(&found)
	return found, err
}

// ForEachProject visits unique changed IDs in the literal SQL lock/rebuild
// order. Each cursor key is admitted before the driver transfers its bytes.
func (p *legacyProgressImage) ForEachProject(ctx context.Context, visit func(string) error) error {
	if p == nil || p.closed {
		return errMetadataLogicalClosed
	}
	first, previous := true, ""
	var held *ResourceRead
	defer func() {
		if held != nil {
			held.Close()
		}
	}()
	for {
		query, args := `SELECT LENGTH(CAST(id AS BLOB)) FROM legacy_progress_ids ORDER BY id LIMIT 1`, []any(nil)
		if !first {
			query, args = `SELECT LENGTH(CAST(id AS BLOB)) FROM legacy_progress_ids WHERE id>? ORDER BY id LIMIT 1`, []any{previous}
		}
		var size int64
		err := p.stage.db.QueryRowContext(ctx, query, args...).Scan(&size)
		if errors.Is(err, sql.ErrNoRows) {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if size < 0 || uint64(size) > (metadataWorkBytes-4096)/8 {
			return ErrMetadataWorkBudget
		}
		current := &ResourceRead{store: p.store}
		if err := current.reserve(ctx, uint64(size)*8+4096); err != nil {
			current.Close()
			return err
		}
		query = `SELECT id FROM legacy_progress_ids ORDER BY id LIMIT 1`
		if !first {
			query = `SELECT id FROM legacy_progress_ids WHERE id>? ORDER BY id LIMIT 1`
		}
		var id string
		if err := p.stage.db.QueryRowContext(ctx, query, args...).Scan(&id); err != nil {
			current.Close()
			return err
		}
		if held != nil {
			held.Close()
		}
		held = current
		if err := visit(id); err != nil {
			return err
		}
		previous, first = id, false
	}
}

// Rebuild runs after the caller's complete sorted metadata lock/rebase pass and
// external writes, in that same SQL transaction. Only first native project
// occurrences are replaced, matching rebuildProgressTx's IndexFunc lookup.
func (p *legacyProgressImage) Rebuild(ctx context.Context, tx *sqlTx, workspace string, source *metadataLogicalSource, now time.Time) (_ *metadataLogicalSource, err error) {
	if p == nil || p.closed {
		return nil, errMetadataLogicalClosed
	}
	if tx == nil || tx.database != p.store.db || tx.dialect != p.store.dialect || source == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = metadataLogicalSourceScratchContext(ctx, source)
	if err := source.Retain(); err != nil {
		return nil, err
	}
	defer source.Release()
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return nil, err
	}
	if stamp.workspaceID == "" || source.Stamp().workspaceID != stamp.workspaceID {
		return nil, ErrMetadataRecordQuery
	}
	patches, err := sw26NewMigrationStage(ctx)
	if err != nil {
		return nil, err
	}
	defer patches.Close()
	if _, err := patches.exec(ctx, 4096, `CREATE TABLE legacy_progress_patches(ordinal INTEGER PRIMARY KEY,frame_offset INTEGER NOT NULL,frame_size INTEGER NOT NULL,frame_cost INTEGER NOT NULL)`); err != nil {
		return nil, err
	}
	values, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer values.Close()
	for _, storage := range source.stores {
		if err := values.inheritLocations(storage); err != nil {
			return nil, err
		}
	}
	index := &legacyProgressIndex{store: p.store, source: source, stage: patches}
	if err := index.build(ctx, "projects", "states", "teamSettings"); err != nil {
		return nil, err
	}
	err = p.ForEachProject(ctx, func(id string) error {
		return index.firstProject(ctx, id, func(ordinal int, node metadataLogicalNode) error {
			group, err := newLegacyProgressGroup(ctx, p.store, source, index)
			if err != nil {
				return err
			}
			defer group.close()
			value, err := group.project(ctx, node, legacyProgressProjectFields...)
			if err != nil {
				return err
			}
			project := value.Interface().(domain.Project)
			if err := group.rebuildSQL(ctx, tx, workspace, &project, now); err != nil {
				return err
			}
			patch, err := values.writeValue(reflect.ValueOf(project), 0, false)
			if err != nil {
				return err
			}
			if _, err := patches.exec(ctx, 512, `INSERT INTO legacy_progress_patches(ordinal,frame_offset,frame_size,frame_cost) VALUES(?,?,?,?)`, ordinal, patch.offset, patch.size, patch.cost); err != nil {
				return err
			}
			return ctx.Err()
		})
	})
	if err != nil {
		return nil, err
	}
	return source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if name != "projects" {
			return metadataAggregateStoredNode{original}, false, nil
		}
		return legacyProgressCollection{base: original, patches: patches, values: values.storage}, true, nil
	})
}

type legacyProgressGroup struct {
	inputs   *issueHotInputs
	progress *issueProgressWork
	index    *legacyProgressIndex
}

func newLegacyProgressGroup(ctx context.Context, store *SQLiteStore, source *metadataLogicalSource, index *legacyProgressIndex) (_ *legacyProgressGroup, err error) {
	if err := source.Retain(); err != nil {
		return nil, err
	}
	group := &legacyProgressGroup{inputs: &issueHotInputs{store: store, source: source}, index: index}
	defer func() {
		if err != nil {
			group.close()
		}
	}()
	if err := group.inputs.reserve(ctx, 128<<10); err != nil {
		return nil, err
	}
	group.progress = &issueProgressWork{inputs: group.inputs, states: map[string]domain.WorkflowState{}, seen: map[string]bool{}}
	return group, nil
}

func (g *legacyProgressGroup) close() { g.inputs.close() }

func (g *legacyProgressGroup) project(ctx context.Context, node metadataLogicalNode, names ...string) (reflect.Value, error) {
	cost, err := issueHotProjectionCost(ctx, node, names)
	if err != nil {
		return reflect.Value{}, err
	}
	if err := g.inputs.reserve(ctx, cost); err != nil {
		return reflect.Value{}, err
	}
	return issueHotProjection(ctx, node, names, cost)
}

func (g *legacyProgressGroup) scalar(ctx context.Context, node metadataLogicalNode) (reflect.Value, error) {
	if node.cost > (metadataWorkBytes-2048)/8 {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	if err := g.inputs.reserve(ctx, node.cost*8+2048); err != nil {
		return reflect.Value{}, err
	}
	return node.Value(ctx, int(node.cost))
}

func (g *legacyProgressGroup) imageInputs(ctx context.Context, project string) ([]progressIssue, map[string][]domain.ActivityEvent, error) {
	issues := []progressIssue{}
	issueIDs, teams := map[string]bool{}, map[string]bool{}
	err := g.index.issues(ctx, project, func(node metadataLogicalNode) error {
		value, err := g.project(ctx, node, "ID", "CreatedAt", "Estimate")
		if err != nil {
			return err
		}
		issue := value.Interface().(domain.Issue)
		state, err := issueHotField(ctx, node, "State")
		if err != nil {
			return err
		}
		value, err = g.project(ctx, state, "ID", "Type")
		if err != nil {
			return err
		}
		issue.State = value.Interface().(domain.WorkflowState)
		team, err := issueHotField(ctx, node, "Team")
		if err != nil {
			return err
		}
		value, err = g.project(ctx, team, "ID")
		if err != nil {
			return err
		}
		issue.Team = value.Interface().(domain.Team)
		if err := g.inputs.reserve(ctx, 2048); err != nil {
			return err
		}
		issues = append(issues, progressIssueOf(issue))
		if err := g.inputs.addKey(ctx, issueIDs, issue.ID); err != nil {
			return err
		}
		return g.inputs.addKey(ctx, teams, issue.Team.ID)
	})
	if err != nil {
		return nil, nil, err
	}
	activities := map[string][]domain.ActivityEvent{}
	for id := range issueIDs {
		events, found, err := g.index.lookup(ctx, "activities", id)
		if err != nil {
			return nil, nil, err
		}
		if !found {
			continue
		}
		err = events.Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
			value, err := g.project(ctx, node, "Type", "CreatedAt")
			if err != nil {
				return err
			}
			event := value.Interface().(domain.ActivityEvent)
			if event.Type != "issue.updated" {
				return nil
			}
			metadata, err := issueHotField(ctx, node, "Metadata")
			if err != nil {
				return err
			}
			if err := g.inputs.reserve(ctx, 2048); err != nil {
				return err
			}
			event.Metadata = domain.ActivityMetadata{}
			err = metadata.MapEntries(ctx, func(key, value metadataLogicalNode) error {
				for _, name := range []string{"stateBeforeId", "stateBeforeType", "stateBefore", "estimateBefore"} {
					match, err := key.StringEqual(ctx, name)
					if err != nil {
						return err
					}
					if match {
						text, err := g.scalar(ctx, value)
						if err == nil {
							event.Metadata[name] = text.String()
						}
						return err
					}
				}
				return nil
			})
			if err == nil {
				activities[id] = append(activities[id], event)
			}
			return err
		})
		if err != nil {
			return nil, nil, err
		}
	}
	if err := g.historicalStates(ctx, activities); err != nil {
		return nil, nil, err
	}
	if err := g.teamSettings(ctx, teams); err != nil {
		return nil, nil, err
	}
	return issues, activities, nil
}

func (g *legacyProgressGroup) grid(ctx context.Context, project domain.Project, issues []progressIssue, now time.Time) error {
	start := progressStartDate(&project, issues)
	if start.IsZero() {
		return ctx.Err()
	}
	return g.progress.admitGrid(ctx, start, now)
}

func (g *legacyProgressGroup) loadState(ctx context.Context, id string) error {
	if g.progress.seen[id] {
		return nil
	}
	if err := g.inputs.addKey(ctx, g.progress.seen, id); err != nil {
		return err
	}
	node, found, err := g.index.lookup(ctx, "states", id)
	if err != nil || !found {
		return err
	}
	value, err := g.project(ctx, node, "ID", "Type")
	if err == nil {
		g.progress.states[id] = value.Interface().(domain.WorkflowState)
	}
	return err
}

func (g *legacyProgressGroup) historicalStates(ctx context.Context, activities map[string][]domain.ActivityEvent) error {
	for _, events := range activities {
		for _, event := range events {
			if id := strings.TrimSpace(event.Metadata["stateBeforeId"]); id != "" {
				if err := g.loadState(ctx, id); err != nil {
					return err
				}
			}
		}
	}
	return ctx.Err()
}

func (g *legacyProgressGroup) teamSettings(ctx context.Context, teams map[string]bool) error {
	for id := range teams {
		node, found, err := g.index.lookup(ctx, "teamSettings", id)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		value, err := g.project(ctx, node, "EstimateCountUnestimated")
		if err != nil {
			return err
		}
		if g.inputs.data.TeamSettings == nil {
			g.inputs.data.TeamSettings = map[string]domain.TeamSettings{}
		}
		g.inputs.data.TeamSettings[id] = value.Interface().(domain.TeamSettings)
	}
	return ctx.Err()
}

func (g *legacyProgressGroup) rebuildSQL(ctx context.Context, tx *sqlTx, workspace string, project *domain.Project, now time.Time) error {
	// Run the same SQL readers with explicit admission and indexed state
	// resolution, avoiding their optional whole-catalogue state scan.
	ctx = context.WithValue(ctx, issueProgressWorkKey{}, (*issueProgressWork)(nil))
	if err := g.progress.admitIssues(ctx, tx, workspace, project.ID); err != nil {
		return err
	}
	issues, err := projectProgressIssues(ctx, tx, workspace, project.ID, g.progress.states)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(issues))
	teams := map[string]bool{}
	for _, issue := range issues {
		ids = append(ids, issue.id)
		if err := g.inputs.addKey(ctx, teams, issue.teamID); err != nil {
			return err
		}
		if err := g.loadState(ctx, issue.state.ID); err != nil {
			return err
		}
	}
	for index := range issues {
		if state, ok := g.progress.states[issues[index].state.ID]; ok {
			issues[index].state = state
		}
	}
	for start := 0; start < len(ids); start += 500 {
		clause, args := bindList("resource_id", ids[start:min(start+500, len(ids))])
		if err := g.progress.admitActivities(ctx, tx, workspace, clause, args); err != nil {
			return err
		}
	}
	activities, err := progressActivities(ctx, tx, workspace, ids)
	if err != nil {
		return err
	}
	if err := g.historicalStates(ctx, activities); err != nil {
		return err
	}
	if err := g.teamSettings(ctx, teams); err != nil {
		return err
	}
	if err := g.grid(ctx, *project, issues, now); err != nil {
		return err
	}
	refreshProjectProgress(project, issues, g.progress.states, activities, g.inputs.data.TeamSettings, now)
	return ctx.Err()
}

// The index stores only native frame descriptors and lookup keys. In
// particular, issue/project bodies and transition payloads stay in their
// immutable logical files until a selected group projects their inputs.
type legacyProgressIndex struct {
	store  *SQLiteStore
	source *metadataLogicalSource
	stage  *sw26MigrationStage
}

func legacyProgressTable(field string) string {
	switch field {
	case "projects", "issues", "activities", "states", "teamSettings":
		return "legacy_progress_" + field
	}
	return ""
}

func (x *legacyProgressIndex) build(ctx context.Context, fields ...string) error {
	for _, name := range fields {
		table := legacyProgressTable(name)
		if table == "" {
			return ErrMetadataRecordQuery
		}
		orderKey := "id TEXT PRIMARY KEY,ordinal INTEGER NOT NULL"
		if name == "projects" || name == "issues" {
			orderKey = "id TEXT NOT NULL,ordinal INTEGER PRIMARY KEY"
		}
		if _, err := x.stage.exec(ctx, 4096, `CREATE TABLE `+table+`(`+orderKey+`,frame_offset INTEGER NOT NULL,frame_size INTEGER NOT NULL,frame_cost INTEGER NOT NULL,unavailable INTEGER NOT NULL,blocked INTEGER NOT NULL)`); err != nil {
			return err
		}
		if name == "projects" || name == "issues" {
			if _, err := x.stage.exec(ctx, 4096, `CREATE INDEX `+table+`_ids ON `+table+`(id,ordinal)`); err != nil {
				return err
			}
		}
		index, _, _ := metadataLogicalField(name)
		field := x.source.fields[index]
		put := func(id string, ordinal int, node metadataLogicalNode) error {
			query := `INSERT INTO ` + table + `(id,ordinal,frame_offset,frame_size,frame_cost,unavailable,blocked) VALUES(?,?,?,?,?,?,?)`
			if name != "projects" && name != "issues" {
				query += ` ON CONFLICT(id) DO UPDATE SET ordinal=excluded.ordinal,frame_offset=excluded.frame_offset,frame_size=excluded.frame_size,frame_cost=excluded.frame_cost,unavailable=excluded.unavailable,blocked=excluded.blocked`
			}
			_, err := x.stage.exec(ctx, int64(len(id)+1024), query, id, ordinal, node.offset, node.size, node.cost, node.unavailable, node.blocked)
			return err
		}
		if field.typ.Kind() == reflect.Map {
			ordinal := 0
			err := field.MapEntries(ctx, func(key, node metadataLogicalNode) error {
				work := &ResourceRead{store: x.store}
				defer work.Close()
				value, err := work.node(ctx, key, false)
				if err != nil {
					return err
				}
				err = put(value.String(), ordinal, node)
				ordinal++
				return err
			})
			if err != nil {
				return err
			}
			continue
		}
		err := field.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
			work := &ResourceRead{store: x.store}
			defer work.Close()
			if name == "issues" {
				project, err := issueHotField(ctx, node, "Project")
				if err != nil {
					return err
				}
				return project.Children(ctx, func(_ reflect.Value, summary metadataLogicalNode) error {
					value, err := work.projectNode(ctx, summary, "ID")
					if err != nil {
						return err
					}
					return put(value.FieldByName("ID").String(), int(key.Int()), node)
				})
			}
			value, err := work.projectNode(ctx, node, "ID")
			if err != nil {
				return err
			}
			return put(value.FieldByName("ID").String(), int(key.Int()), node)
		})
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (x *legacyProgressIndex) frame(field string) metadataLogicalNode {
	index, _, _ := metadataLogicalField(field)
	parent := x.source.fields[index]
	return metadataLogicalNode{source: parent.source, typ: parent.typ.Elem()}
}

func (x *legacyProgressIndex) projects(ctx context.Context, visit func(int, metadataLogicalNode) error) error {
	previous := -1
	for {
		node := x.frame("projects")
		var ordinal int
		err := x.stage.db.QueryRowContext(ctx, `SELECT ordinal,frame_offset,frame_size,frame_cost,unavailable,blocked FROM legacy_progress_projects WHERE ordinal>? ORDER BY ordinal LIMIT 1`, previous).
			Scan(&ordinal, &node.offset, &node.size, &node.cost, &node.unavailable, &node.blocked)
		if errors.Is(err, sql.ErrNoRows) {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if err := visit(ordinal, node); err != nil {
			return err
		}
		previous = ordinal
	}
}

func (x *legacyProgressIndex) firstProject(ctx context.Context, id string, visit func(int, metadataLogicalNode) error) error {
	node := x.frame("projects")
	var ordinal int
	err := x.stage.db.QueryRowContext(ctx, `SELECT ordinal,frame_offset,frame_size,frame_cost,unavailable,blocked FROM legacy_progress_projects WHERE id=? ORDER BY ordinal LIMIT 1`, id).
		Scan(&ordinal, &node.offset, &node.size, &node.cost, &node.unavailable, &node.blocked)
	if errors.Is(err, sql.ErrNoRows) {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	return visit(ordinal, node)
}

func (x *legacyProgressIndex) issues(ctx context.Context, project string, visit func(metadataLogicalNode) error) error {
	previous := -1
	for {
		node := x.frame("issues")
		var ordinal int
		err := x.stage.db.QueryRowContext(ctx, `SELECT ordinal,frame_offset,frame_size,frame_cost,unavailable,blocked FROM legacy_progress_issues WHERE id=? AND ordinal>? ORDER BY ordinal LIMIT 1`, project, previous).
			Scan(&ordinal, &node.offset, &node.size, &node.cost, &node.unavailable, &node.blocked)
		if errors.Is(err, sql.ErrNoRows) {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if err := visit(node); err != nil {
			return err
		}
		previous = ordinal
	}
}

func (x *legacyProgressIndex) lookup(ctx context.Context, field, id string) (metadataLogicalNode, bool, error) {
	if field != "activities" && field != "states" && field != "teamSettings" {
		return metadataLogicalNode{}, false, ErrMetadataRecordQuery
	}
	node := x.frame(field)
	err := x.stage.db.QueryRowContext(ctx, `SELECT frame_offset,frame_size,frame_cost,unavailable,blocked FROM `+legacyProgressTable(field)+` WHERE id=?`, id).
		Scan(&node.offset, &node.size, &node.cost, &node.unavailable, &node.blocked)
	if errors.Is(err, sql.ErrNoRows) {
		return node, false, nil
	}
	return node, err == nil, err
}

type legacyProgressCollection struct {
	base    metadataLogicalNode
	patches *sw26MigrationStage
	values  *metadataLogicalStorage
}

func (n legacyProgressCollection) nodeType() reflect.Type           { return n.base.typ }
func (n legacyProgressCollection) logicalBase() metadataLogicalNode { return n.base }
func (n legacyProgressCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyProgressCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		patch := metadataLogicalNode{source: n.values, typ: reflect.TypeFor[domain.Project]()}
		err := n.patches.db.QueryRowContext(ctx, `SELECT frame_offset,frame_size,frame_cost FROM legacy_progress_patches WHERE ordinal=?`, key.Int()).Scan(&patch.offset, &patch.size, &patch.cost)
		if errors.Is(err, sql.ErrNoRows) {
			return visit(key, metadataAggregateStoredNode{node})
		}
		if err != nil {
			return err
		}
		return visit(key, legacyProgressProject{base: node, patch: patch})
	})
}

type legacyProgressProject struct {
	base, patch metadataLogicalNode
}

func (n legacyProgressProject) nodeType() reflect.Type { return n.base.typ }
func (n legacyProgressProject) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyProgressProject) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.Children(ctx, func(key reflect.Value, original metadataLogicalNode) error {
		if slices.Contains(legacyProgressHistoryFields, key.String()) {
			value, err := issueHotField(ctx, n.patch, key.String())
			if err != nil {
				return err
			}
			return visit(key, metadataAggregateStoredNode{value})
		}
		return visit(key, metadataAggregateStoredNode{original})
	})
}
