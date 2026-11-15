package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
)

// ProjectDirectoryQueryRead owns the complete list page, without retaining
// fields stripped by ProjectListProjection. Close after the final response use.
type ProjectDirectoryQueryRead struct {
	Page  ProjectRecordPage
	owner *ResourceRead
	rows  []*ResourceRead
}

func (r *ProjectDirectoryQueryRead) Close() {
	if r == nil {
		return
	}
	r.Page = ProjectRecordPage{}
	for _, owner := range r.rows {
		owner.Close()
	}
	r.rows = nil
	if r.owner != nil {
		r.owner.Close()
	}
}

func newProjectDirectoryQueryRead(ctx context.Context, s *SQLiteStore, query ProjectRecordQuery) (*ProjectDirectoryQueryRead, error) {
	r := &ProjectDirectoryQueryRead{Page: ProjectRecordPage{Items: []domain.Project{}, Total: -1}, owner: &ResourceRead{store: s}}
	cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(query), 0)
	if err == nil {
		err = r.owner.reserve(ctx, uint64(cost)*2+256<<10)
	}
	if err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

// BeginProjectDirectoryQuery preserves the raw directory adapter's cache,
// current installed generation, indexed shadow and canonical fallback choices.
func (s *SQLiteStore) BeginProjectDirectoryQuery(ctx context.Context, query ProjectRecordQuery) (_ *ProjectDirectoryQueryRead, err error) {
	if !s.BoundedMetadataEnabled() {
		page, err := s.QueryProjectDirectory(ctx, query)
		if err != nil {
			return nil, err
		}
		return &ProjectDirectoryQueryRead{Page: page}, nil
	}
	ctx = metadataLogicalUnpinnedContext(ctx)
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	mode := indexedReadsMode()
	if mode == "off" {
		return s.legacyProjectDirectoryQueryOwned(ctx, query)
	}
	current, usable, err := s.projectDirectoryInstalledSource(ctx, query.Workspace)
	if current != nil {
		current.Close()
	}
	if err != nil {
		return nil, err
	}
	if usable {
		return s.legacyProjectDirectoryQueryOwned(ctx, query)
	}
	status, statusErr := s.MetadataCatalogStatus(ctx, query.Workspace, "projects")
	if statusErr != nil || !status.Ready && (mode != "shadow" || !status.Complete) {
		return s.legacyProjectDirectoryQueryOwned(ctx, query)
	}
	candidate, candidateErr := s.canonicalProjectDirectoryQueryOwned(ctx, query, true)
	if candidateErr == nil {
		after, err := s.MetadataCatalogStatus(ctx, query.Workspace, "projects")
		if err != nil || after.Revision != status.Revision || mode == "on" && !after.Ready {
			candidateErr = ErrMetadataCatalogGap
		}
	}
	if mode == "on" && candidateErr == nil {
		return candidate, nil
	}
	if candidate != nil {
		defer candidate.Close()
	}
	legacy, legacyErr := s.legacyProjectDirectoryQueryOwned(ctx, query)
	if mode == "shadow" {
		var want, got []byte
		var wantErr, gotErr error
		if legacy != nil && candidate != nil {
			if reserveErr := legacy.reserveEncoding(ctx, legacy.Page); reserveErr != nil {
				legacy.Close()
				return nil, reserveErr
			}
			if reserveErr := candidate.reserveEncoding(ctx, candidate.Page); reserveErr != nil {
				legacy.Close()
				return nil, reserveErr
			}
			want, wantErr = json.Marshal(legacy.Page)
			got, gotErr = json.Marshal(candidate.Page)
		}
		if legacyErr != nil || candidateErr != nil || wantErr != nil || gotErr != nil || !bytes.Equal(want, got) {
			slog.Warn("indexed project directory shadow difference")
		}
	}
	return legacy, legacyErr
}

func (s *SQLiteStore) BeginProjectDirectoryQueryWithAccess(ctx context.Context, access AccessContext, query ProjectRecordQuery) (*ProjectDirectoryQueryRead, error) {
	if err := s.ValidateAccess(ctx, access); err != nil {
		return nil, err
	}
	if query.Workspace != access.policy.workspace {
		return nil, ErrAuthForbidden
	}
	if access.policy.selected() || access.policy.key != nil && access.policy.key.Actor == "app" {
		return nil, ErrMetadataCatalogGap
	}
	read, err := s.BeginProjectDirectoryQuery(ctx, query)
	if err == nil {
		err = s.ValidateAccess(ctx, access)
	}
	if err != nil {
		if read != nil {
			read.Close()
		}
		return nil, err
	}
	return read, nil
}

func (r *ProjectDirectoryQueryRead) reserveEncoding(ctx context.Context, value any) error {
	cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(value), 0)
	if err != nil {
		return err
	}
	return r.owner.reserve(ctx, uint64(cost)*2+4096)
}

func (s *SQLiteStore) projectDirectoryInstalledSource(ctx context.Context, workspace string) (*ResourceRead, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	// A policy-only recovery has no project snapshot. Preserve the literal
	// SQL fallback without hiding a missing source for a populated registry.
	s.mu.RLock()
	selected := workspace
	if selected == "" {
		selected = s.lastWorkspaceKey
	}
	data, found := s.workspaces[selected]
	s.metadataResidency.mu.Lock()
	policyOnly := found && data.Projects == nil && s.metadataResidency.logical[selected] == nil
	s.metadataResidency.mu.Unlock()
	s.mu.RUnlock()
	if policyOnly {
		return nil, false, ctx.Err()
	}
	installed, found, err := s.BeginInstalledMetadataRead(metadataLogicalUnpinnedContext(ctx), workspace)
	if err != nil || !found {
		return nil, false, err
	}
	defer installed.Close()
	source, err := installed.retainSource(ctx, installed.workspace)
	if err != nil {
		return nil, false, err
	}
	read := &ResourceRead{store: s, source: source}
	index, _, _ := metadataLogicalField("projects")
	missing, err := source.fields[index].isNil(ctx)
	if err != nil || missing {
		read.Close()
		return nil, false, err
	}
	return read, true, nil
}

func (s *SQLiteStore) legacyProjectDirectoryQueryOwned(ctx context.Context, query ProjectRecordQuery) (_ *ProjectDirectoryQueryRead, err error) {
	read, err := newProjectDirectoryQueryRead(ctx, s, query)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if strings.TrimSpace(query.Workspace) == "" {
		return nil, ErrIssueQuery
	}
	cacheable := !slices.ContainsFunc(query.Filters, func(filter ProjectDirectoryFilter) bool { return filter.Field == "customers" })
	cacheKey := ""
	if cacheable {
		var hit bool
		read.Page, cacheKey, hit, err = s.cacheGetProjectQueryOwned(ctx, query, read.owner)
		if err != nil {
			return nil, err
		}
		if hit {
			return read, ctx.Err()
		}
		read.Page = ProjectRecordPage{Items: []domain.Project{}, Total: -1}
	}
	source, installed, err := s.projectDirectoryInstalledSource(ctx, query.Workspace)
	if err != nil {
		return nil, err
	}
	if installed {
		read.owner.source = source.source
		source.source = nil
		source.Close()
		pager := newProjectDirectoryPager(read, query)
		err = read.owner.visitNative(ctx, "projects", func(_ int, node metadataLogicalNode) error {
			return pager.native(ctx, s, node, nil, nil)
		})
		if errors.Is(err, ErrMetadataIterationDone) {
			err = nil
		}
		pager.finish()
		if err == nil {
			err = ctx.Err()
		}
		return read, err
	}
	canonical, err := s.canonicalProjectDirectoryQueryOwned(ctx, query, false)
	if err != nil {
		return nil, err
	}
	read.Close()
	read = canonical
	if cacheable {
		if err = s.cacheSetProjectQueryOwned(ctx, cacheKey, read.Page, read.owner); err != nil {
			return nil, err
		}
	}
	return read, ctx.Err()
}

type projectDirectoryPager struct {
	read                   *ProjectDirectoryQueryRead
	query, predicate       ProjectRecordQuery
	offset, limit, matched int
	fields                 []string
}

func newProjectDirectoryPager(read *ProjectDirectoryQueryRead, query ProjectRecordQuery) *projectDirectoryPager {
	limit := query.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	predicate := query
	predicate.Cursor, predicate.Limit, predicate.IncludeTotal = "", 1, false
	return &projectDirectoryPager{read: read, query: query, predicate: predicate, limit: limit, offset: decodeProjectCursor(query.Cursor), fields: projectDirectoryPredicateFields(query)}
}

func (p *projectDirectoryPager) accepts(project domain.Project) (bool, bool) {
	if len(filterProjectDirectory([]domain.Project{project}, p.predicate).Items) == 0 {
		return false, false
	}
	p.matched++
	if p.matched <= p.offset {
		return false, false
	}
	if len(p.read.Page.Items) < p.limit {
		return true, false
	}
	p.read.Page.HasMore = true
	return false, !p.query.IncludeTotal
}

func (p *projectDirectoryPager) finish() {
	if p.query.IncludeTotal {
		p.read.Page.Total = int64(p.matched)
	}
	if p.read.Page.HasMore {
		p.read.Page.NextCursor = encodeProjectCursor(p.offset + len(p.read.Page.Items))
	}
}

func projectDirectoryPredicateFields(query ProjectRecordQuery) []string {
	fields := []string{}
	add := func(names ...string) {
		for _, name := range names {
			if !slices.Contains(fields, name) {
				fields = append(fields, name)
			}
		}
	}
	if query.Archived != "all" {
		add("ArchivedAt")
	}
	if len(query.TeamIDs) != 0 || !query.Admin && query.AllowedTeamIDs != nil {
		add("TeamIDs")
	}
	if strings.TrimSpace(query.Search) != "" {
		add("Name", "SlugID", "Summary")
	}
	for _, filter := range query.Filters {
		switch filter.Field {
		case "customers":
			add("ID")
		case "status":
			add("Status")
		case "priority":
			add("Priority")
		case "lead":
			add("Lead")
		case "members":
			add("MemberIDs")
		case "health":
			add("Health")
		case "dates":
			add("TargetDate")
		case "milestones":
			add("Milestones")
		case "labels":
			add("LabelIDs")
		case "teams":
			add("TeamIDs")
		case "project":
			add("ID", "SlugID", "PreviousSlugIDs")
		}
	}
	return fields
}

var projectDirectoryListFields = directoryOutputFields(reflect.TypeFor[domain.Project](),
	"Description", "Resources", "Comments", "CommentAttachments", "DescriptionRevisions",
	"IssueCountHistory", "ScopeHistory", "CompletedScopeHistory", "InProgressScopeHistory",
	"ProgressHistory", "UpdateSchedule", "Milestones")

func projectDirectoryNative(ctx context.Context, owner *ResourceRead, node metadataLogicalNode, fields []string, output bool) (domain.Project, error) {
	header := &DocumentDirectoryRead{owner: owner}
	selected := slices.DeleteFunc(slices.Clone(fields), func(name string) bool { return name == "Milestones" || !output && (name == "Lead" || name == "Status") })
	value, err := header.header(ctx, node, selected)
	if err != nil {
		return domain.Project{}, err
	}
	project := value.Interface().(domain.Project)
	err = node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if !output && key.String() == "Status" && slices.Contains(fields, "Status") {
			status, err := header.header(ctx, child, []string{"ID", "Name", "Type"})
			if err == nil {
				project.Status = status.Interface().(domain.ProjectStatus)
			}
			return err
		}
		if !output && key.String() == "Lead" && slices.Contains(fields, "Lead") {
			return child.Children(ctx, func(_ reflect.Value, user metadataLogicalNode) error {
				actor, err := header.header(ctx, user, []string{"ID"})
				if err == nil {
					value := actor.Interface().(domain.User)
					project.Lead = &value
				}
				return err
			})
		}
		if key.String() != "Milestones" || !slices.Contains(fields, "Milestones") && !output {
			return nil
		}
		isNil, err := child.isNil(ctx)
		if err != nil || isNil {
			return err
		}
		project.Milestones = []domain.ProjectMilestone{}
		milestoneFields := []string{"ID", "Name"}
		if output {
			milestoneFields = directoryOutputFields(reflect.TypeFor[domain.ProjectMilestone](), "Description", "DescriptionRevisions")
		}
		return owner.nativeArray(ctx, child, func(_ int, milestone metadataLogicalNode) error {
			value, err := header.header(ctx, milestone, milestoneFields)
			if err == nil {
				project.Milestones = append(project.Milestones, value.Interface().(domain.ProjectMilestone))
			}
			return err
		})
	})
	return project, err
}

func (p *projectDirectoryPager) native(ctx context.Context, s *SQLiteStore, node metadataLogicalNode, core *domain.Project, directory *ResourceDirectoryRead) error {
	predicateOwner := &ResourceRead{store: s}
	defer predicateOwner.Close()
	project, err := projectDirectoryNative(ctx, predicateOwner, node, p.fields, false)
	if err != nil {
		return err
	}
	if core != nil {
		project.TeamIDs, project.LabelIDs = core.TeamIDs, core.LabelIDs
	}
	selected, stop := p.accepts(project)
	if stop {
		return ErrMetadataIterationDone
	}
	if !selected {
		return nil
	}
	output := &ResourceRead{store: s}
	item, err := projectDirectoryNative(ctx, output, node, projectDirectoryListFields, true)
	if err == nil && core != nil {
		owned := &ResourceDirectoryValue{Project: item, owner: output}
		for _, pair := range []resourceDirectoryIDs{
			{&owned.Project.TeamIDs, core.TeamIDs}, {&owned.Project.Initiatives, core.Initiatives},
			{&owned.Project.LabelIDs, core.LabelIDs}, {&owned.Project.DependencyIDs, core.DependencyIDs},
			{&owned.Project.Customers, core.Customers},
		} {
			if err = owned.copyIDs(ctx, pair.target, pair.source); err != nil {
				break
			}
		}
		owned.Project.Progress, owned.Project.IssueCount = core.Progress, core.IssueCount
		if err == nil && directory.key != nil && (directory.key.TeamRestriction == "selected" || directory.key.TeamRestriction == "" && len(directory.key.TeamIDs) > 0) {
			for _, user := range []*domain.User{owned.Project.Creator, owned.Project.Lead} {
				if user != nil {
					RedactUserTeams(user, func(id string) bool { return slices.Contains(directory.key.TeamIDs, id) })
				}
			}
		}
		item = owned.Project
	}
	if err != nil {
		output.Close()
		return err
	}
	p.read.Page.Items = append(p.read.Page.Items, item)
	p.read.rows = append(p.read.rows, output)
	return nil
}

// QueryProjectDirectory pages the already-projected occurrence graph. Full
// connection Load is deliberately avoided: it materializes stripped details.
func (r *ResourceDirectoryRead) QueryProjectDirectory(ctx context.Context, query ProjectRecordQuery) (_ *ProjectDirectoryQueryRead, err error) {
	if r == nil || r.owner == nil || r.owner.source == nil || r.kind != "projects" {
		return nil, ErrMetadataRecordQuery
	}
	if err := r.prepare(); err != nil {
		return nil, err
	}
	read, err := newProjectDirectoryQueryRead(ctx, r.owner.store, query)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	pager := newProjectDirectoryPager(read, query)
	for _, project := range r.Data.Projects {
		ordinal, lookupErr := r.ordinal("projects", project.Name, project.ID)
		if lookupErr != nil {
			return nil, lookupErr
		}
		if err = pager.native(ctx, r.owner.store, r.nodes["projects"][ordinal], &project, r); err != nil {
			if errors.Is(err, ErrMetadataIterationDone) {
				err = nil
				break
			}
			return nil, err
		}
	}
	pager.finish()
	if err := r.Validate(ctx); err != nil {
		return nil, err
	}
	return read, nil
}

type projectDirectorySQLHeader struct {
	key  string
	blob bool
	size int64
}

func (s *SQLiteStore) canonicalProjectDirectoryQueryOwned(ctx context.Context, query ProjectRecordQuery, catalog bool) (_ *ProjectDirectoryQueryRead, err error) {
	read, err := newProjectDirectoryQueryRead(ctx, s, query)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	var tx *sqlTx
	if held, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); !catalog && ok && held.database == s.db {
		tx = held.reader
		if tx == nil {
			return nil, ErrMetadataRecordQuery
		}
	} else {
		tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
	}
	if catalog {
		var phase int
		var omissions int64
		if err := tx.QueryRowContext(ctx, `SELECT phase,omissions FROM metadata_catalog_collections WHERE workspace_key=? AND field='projects'`, query.Workspace).Scan(&phase, &omissions); err != nil {
			return nil, err
		}
		if phase != 2 || omissions != 0 {
			return nil, ErrMetadataCatalogGap
		}
	}
	table, payload := "workspace_metadata_records", "data"
	if catalog {
		table, payload = "metadata_catalog", "summary"
	}
	pager := newProjectDirectoryPager(read, query)
	cursor := &ResourceRead{store: s}
	defer cursor.Close()
	length := cursor.byteLength("record_key")
	var maxKey int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX("+length+"),0) FROM "+table+" WHERE workspace_key=? AND field='projects'", query.Workspace).Scan(&maxKey); err != nil {
		return nil, err
	}
	if maxKey < 0 || maxKey > (metadataWorkBytes-4096)/16 {
		return nil, ErrMetadataWorkBudget
	}
	width := min(50, max(1, int((256<<10)/(uint64(maxKey)*16+512))))
	if err := cursor.reserve(ctx, uint64(width)*(uint64(maxKey)*16+512)+128<<10); err != nil {
		return nil, err
	}
	keyType := "'text'"
	if s.dialect == "sqlite" {
		keyType = "typeof(record_key)"
	}
	for offset := 0; ; {
		rows, err := tx.QueryContext(ctx, "SELECT CASE WHEN "+length+"<=? THEN record_key ELSE NULL END,"+keyType+","+cursor.byteLength(payload)+
			" FROM "+table+" WHERE workspace_key=? AND field='projects' ORDER BY collection_order,record_key LIMIT "+strconv.Itoa(width)+" OFFSET "+strconv.Itoa(offset), maxKey, query.Workspace)
		if err != nil {
			return nil, err
		}
		headers := make([]projectDirectorySQLHeader, 0, width)
		for rows.Next() {
			var key sql.NullString
			var storage string
			var header projectDirectorySQLHeader
			if err := rows.Scan(&key, &storage, &header.size); err != nil {
				rows.Close()
				return nil, err
			}
			if !key.Valid || int64(len(key.String)) > maxKey || header.size < 0 {
				rows.Close()
				return nil, ErrConcurrentWrite
			}
			header.key, header.blob = key.String, storage == "blob"
			headers = append(headers, header)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
		for _, header := range headers {
			if err := pager.canonical(ctx, s, tx, query.Workspace, table, payload, header); err != nil {
				if errors.Is(err, ErrMetadataIterationDone) {
					pager.finish()
					return read, nil
				}
				return nil, err
			}
		}
		if len(headers) < width {
			break
		}
		offset += len(headers)
	}
	pager.finish()
	return read, ctx.Err()
}

func projectDirectoryJSONFields(projectFields []string, output bool) map[reflect.Type][]string {
	fields := map[reflect.Type][]string{
		reflect.TypeFor[domain.Project]():          projectFields,
		reflect.TypeFor[domain.ProjectStatus]():    directoryOutputFields(reflect.TypeFor[domain.ProjectStatus]()),
		reflect.TypeFor[domain.User]():             {"ID"},
		reflect.TypeFor[domain.ActorBot]():         directoryOutputFields(reflect.TypeFor[domain.ActorBot]()),
		reflect.TypeFor[domain.ProjectMilestone](): {"ID", "Name"},
	}
	if output {
		fields[reflect.TypeFor[domain.User]()] = directoryOutputFields(reflect.TypeFor[domain.User](), "Via")
		fields[reflect.TypeFor[domain.ProjectMilestone]()] = directoryOutputFields(reflect.TypeFor[domain.ProjectMilestone](), "Description", "DescriptionRevisions")
	} else {
		fields[reflect.TypeFor[domain.ProjectStatus]()] = []string{"ID", "Name", "Type"}
	}
	return fields
}

func (p *projectDirectoryPager) canonical(ctx context.Context, s *SQLiteStore, tx *sqlTx, workspace, table, payload string, header projectDirectorySQLHeader) (err error) {
	cursor := &ResourceRead{store: s}
	defer cursor.Close()
	if err := cursor.reserve(ctx, metadataRecordChunkBytes*4+128<<10); err != nil {
		return err
	}
	spool, err := newMetadataLogicalRecordSpool(s.metadataLogicalScratchContext(ctx, workspace))
	if err != nil {
		return taskIssueSpoolError(err)
	}
	defer func() {
		if closeErr := spool.Close(); closeErr != nil {
			if errors.Is(err, ErrMetadataIterationDone) {
				err = closeErr
			} else {
				err = errors.Join(err, closeErr)
			}
		}
	}()
	if !spool.CanAppend(header.size) {
		return ErrMetadataWorkBudget
	}
	locator := any(header.key)
	if s.dialect == "sqlite" && header.blob {
		locator = []byte(header.key)
	}
	column := payload
	if s.dialect == "sqlite" {
		column = "CAST(" + payload + " AS BLOB)"
	}
	for offset := int64(0); offset < header.size; {
		size := min(int64(metadataRecordChunkBytes), header.size-offset)
		var chunk []byte
		if err := tx.QueryRowContext(ctx, "SELECT CASE WHEN "+cursor.byteLength(payload)+"=? THEN SUBSTR("+column+",?,?) ELSE NULL END FROM "+table+
			" WHERE workspace_key=? AND field='projects' AND record_key=?", header.size, offset+1, size, workspace, locator).Scan(&chunk); err != nil {
			return err
		}
		if int64(len(chunk)) != size {
			return ErrConcurrentWrite
		}
		if _, err := spool.Write(chunk); err != nil {
			return taskIssueSpoolError(err)
		}
		offset += size
	}
	part := metadataCollectionRange{size: header.size}
	parser := taskIssueJSON{ctx: ctx, input: spool, owner: cursor, fields: projectDirectoryJSONFields(p.fields, false)}
	if err := parser.validate(part, reflect.TypeFor[domain.Project](), 0); err != nil {
		return err
	}
	var predicate domain.Project
	if err := parser.project(part, reflect.ValueOf(&predicate).Elem(), 0); err != nil {
		return err
	}
	selected, stop := p.accepts(predicate)
	if stop {
		return ErrMetadataIterationDone
	}
	if !selected {
		return nil
	}
	output := &ResourceRead{store: s}
	parser.owner = output
	parser.fields = projectDirectoryJSONFields(append(slices.Clone(projectDirectoryListFields), "Milestones"), true)
	var project domain.Project
	if err := parser.project(part, reflect.ValueOf(&project).Elem(), 0); err != nil {
		output.Close()
		return err
	}
	p.read.Page.Items = append(p.read.Page.Items, project)
	p.read.rows = append(p.read.rows, output)
	return ctx.Err()
}
