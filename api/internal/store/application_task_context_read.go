package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

const (
	taskIssueBatchRows  = 50
	taskIssueBatchBytes = 256 << 10
	taskIssueChunkBytes = 32 << 10
)

type taskIssuePhysicalHeader struct {
	id       string
	size     int64
	physical int
}

// ApplicationTaskContextRequest names the non-issue task's rendering inputs.
// MetadataOnly preserves the paged accessor's native actors and zero counts.
type ApplicationTaskContextRequest struct {
	ResourceType, ResourceID, TeamID string
	MetadataOnly                     bool
}

// ApplicationTaskContextRead owns policy, rendering inputs and the selected
// resource. Data is incomplete and must not be used as a mutation preimage.
type ApplicationTaskContextRead struct {
	Data              domain.Bootstrap
	ProjectIssueCount int
	owner             *ResourceRead
}

func (r *ApplicationTaskContextRead) Close() {
	if r != nil {
		r.Data = domain.Bootstrap{}
		r.owner.Close()
	}
}

func (r *ApplicationTaskContextRead) TransferOutput(ctx context.Context) error {
	if r == nil || !registerMetadataOutputClose(ctx, r.Close) {
		r.Close()
		return recordMetadataOutputError(ctx, ErrBoundedMetadataCoverage)
	}
	return nil
}

// BeginApplicationTaskContextRead retains the literal installed source used by
// BootstrapForUser, rather than substituting paged detail's canonical fallback.
// Empty userID is the trusted development accessor, not an authorization grant.
func (s *SQLiteStore) BeginApplicationTaskContextRead(ctx context.Context, workspace, userID string, key *domain.APIKey, request ApplicationTaskContextRequest) (_ *ApplicationTaskContextRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	if request.ResourceType != "document" && request.ResourceType != "project" && request.ResourceType != "codingEnvironment" {
		return nil, ErrMetadataRecordQuery
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	installed, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		if err == nil {
			err = ErrAuthForbidden
		}
		return nil, err
	}
	defer installed.Close()
	source, err := installed.retainSource(ctx, installed.workspace)
	if err != nil {
		return nil, err
	}
	read := &ApplicationTaskContextRead{owner: &ResourceRead{store: s, source: source}}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err = read.owner.reserve(ctx, 2<<20); err != nil {
		return nil, err
	}
	if request.MetadataOnly && userID != "" {
		if caller, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && caller.database == s.db && caller.workspace == installed.workspace {
			if caller.metadata.Workspace.ID == "" {
				return nil, ErrAuthForbidden
			}
			selected, captureErr := read.callerSource(ctx, caller.metadata, userID, request)
			if captureErr != nil {
				return nil, captureErr
			}
			read.owner.source.Release()
			read.owner.source = selected
			source = selected
		}
	}
	ctx = read.owner.metadataOutputContext(ctx)
	var access AccessContext
	if userID != "" {
		access, err = s.ResolveAccess(ctx, installed.workspace, userID, key)
		if err != nil {
			return nil, err
		}
	}
	if err = read.native(ctx, userID, request); err != nil {
		return nil, err
	}
	if userID != "" {
		if err = s.validateMetadataLogicalOrigin(ctx, metadataLogicalOrigin{workspace: installed.workspace, stamp: source.Stamp()}, access.WorkspaceID); err != nil {
			return nil, err
		}
	}
	if err := read.issueInputs(ctx, installed.workspace, request); err != nil {
		return nil, err
	}
	if !request.MetadataOnly {
		refreshDisplayReferences(&read.Data)
		refreshIssueReferences(&read.Data)
	}
	if userID != "" {
		document := &DocumentRead{Data: read.Data, owner: read.owner}
		if err = document.documentProjectionWork(ctx, access.WorkspaceID, userID); err != nil {
			return nil, err
		}
		var ok bool
		read.Data, ok, err = s.projectBootstrapForUser(ctx, read.Data, userID)
		if err != nil || !ok {
			if err == nil {
				err = ErrAuthForbidden
			}
			return nil, err
		}
	} else {
		read.Data.ViewerRole = "admin"
	}
	if request.ResourceType == "project" && !request.MetadataOnly {
		for _, issue := range read.Data.Issues {
			if issue.ArchivedAt == nil && issue.Project != nil && issue.Project.ID == request.ResourceID &&
				(userID == "" || key == nil || !taskContextSelectedKey(key) || slices.Contains(key.TeamIDs, issue.Team.ID)) {
				read.ProjectIssueCount++
			}
		}
	}
	read.Data.Issues = nil
	if userID != "" {
		if err = s.ValidateAccess(ctx, access); err != nil {
			return nil, err
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return read, nil
}

func taskContextSelectedKey(key *domain.APIKey) bool {
	return key.TeamRestriction == "selected" || key.TeamRestriction == "" && len(key.TeamIDs) > 0
}

func (r *ApplicationTaskContextRead) callerSource(ctx context.Context, data domain.Bootstrap, userID string, request ApplicationTaskContextRequest) (*metadataLogicalSource, error) {
	candidate := domain.Bootstrap{
		Workspace: data.Workspace, Viewer: data.Viewer, WorkspaceSettings: taskContextWorkspaceSettings(data.WorkspaceSettings),
		TeamSettings: map[string]domain.TeamSettings{}, InitiativeRelations: data.InitiativeRelations, Favorites: data.Favorites,
	}
	if settings, ok := data.UserSettings[userID]; ok {
		candidate.UserSettings = map[string]domain.UserSettings{userID: {AgentInstructions: settings.AgentInstructions}}
	}
	for _, team := range data.Teams {
		if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.Team]().Size())*2+2048); err != nil {
			return nil, err
		}
		candidate.Teams = append(candidate.Teams, domain.Team{ID: team.ID, Name: team.Name, Key: team.Key, ExternalSource: team.ExternalSource, Private: team.Private, ArchivedAt: team.ArchivedAt, RetiredAt: team.RetiredAt})
	}
	for id, policy := range data.TeamSettings {
		if err := r.owner.reserve(ctx, uint64(len(id))*8+4096); err != nil {
			return nil, err
		}
		candidate.TeamSettings[id] = domain.TeamSettings{ParentTeamID: policy.ParentTeamID, Access: policy.Access, MembershipRestriction: policy.MembershipRestriction}
	}
	seen := map[string]bool{}
	for id := request.TeamID; id != "" && !seen[id] && len(seen) < 16; id = data.TeamSettings[id].ParentTeamID {
		seen[id] = true
		policy := candidate.TeamSettings[id]
		policy.AgentGuidance = data.TeamSettings[id].AgentGuidance
		candidate.TeamSettings[id] = policy
	}
	for _, item := range data.Projects {
		if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.Project]().Size())*2+2048); err != nil {
			return nil, err
		}
		project := domain.Project{ID: item.ID, TeamIDs: item.TeamIDs}
		if request.ResourceType == "project" && item.ID == request.ResourceID {
			project = item
		}
		candidate.Projects = append(candidate.Projects, project)
	}
	for _, item := range data.Initiatives {
		if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.Initiative]().Size())*2+2048); err != nil {
			return nil, err
		}
		candidate.Initiatives = append(candidate.Initiatives, domain.Initiative{ID: item.ID, LeadTeamID: item.LeadTeamID, ContributingTeamIDs: item.ContributingTeamIDs, ProjectIDs: item.ProjectIDs})
	}
	if request.ResourceType == "document" {
		for _, item := range data.Documents {
			if documentReadRank(item, DocumentReadRequest{Ref: request.ResourceID}) == 0 {
				continue
			}
			if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.Document]().Size())*2+2048); err != nil {
				return nil, err
			}
			candidate.Documents = append(candidate.Documents, item)
		}
	}
	for _, label := range data.Labels {
		if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.IssueLabel]().Size())*2+2048); err != nil {
			return nil, err
		}
		candidate.Labels = append(candidate.Labels, domain.IssueLabel{ID: label.ID, Scope: label.Scope})
	}
	return captureMetadataLogicalSource(ctx, candidate, copyStamp{workspaceID: data.Workspace.ID, revision: -1})
}

func (r *ApplicationTaskContextRead) native(ctx context.Context, userID string, request ApplicationTaskContextRequest) error {
	for _, field := range []string{"workspace", "viewer"} {
		index, _, _ := metadataLogicalField(field)
		value, err := r.owner.node(ctx, r.owner.source.fields[index], true)
		if err != nil {
			return err
		}
		reflect.ValueOf(&r.Data).Elem().Field(index).Set(value)
	}
	settingsIndex, _, _ := metadataLogicalField("workspaceSettings")
	settingsNode := r.owner.source.fields[settingsIndex]
	settingsValue, err := r.owner.projectNode(ctx, settingsNode, "AgentInstructions", "FeatureFlags", "HIPAACompliance", "PreventGuestAgents", "AgentWebAccess", "AgentWebSearchDisabled")
	if err != nil {
		return err
	}
	r.Data.WorkspaceSettings = settingsValue.Interface().(domain.WorkspaceSettings)
	if err := settingsNode.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		if key.String() != "FeatureSettings" {
			return nil
		}
		value, err := r.owner.projectNode(ctx, node, "ProjectUpdateGuidance", "InitiativeUpdateGuidance")
		if err == nil {
			r.Data.WorkspaceSettings.FeatureSettings = value.Interface().(domain.FeatureSettings)
		}
		return err
	}); err != nil {
		return err
	}
	if userID == "" {
		userID = r.Data.Viewer.ID
	}
	r.Data.TeamSettings = map[string]domain.TeamSettings{}
	for _, collection := range []struct {
		name   string
		fields []string
	}{
		{"teams", []string{"ID", "Name", "Key", "ExternalSource", "Private", "ArchivedAt", "RetiredAt"}},
		{"users", nil},
		{"projects", []string{"ID", "TeamIDs"}},
		{"initiatives", []string{"ID", "LeadTeamID", "ContributingTeamIDs", "ProjectIDs"}},
		{"initiativeRelations", nil},
		{"labels", []string{"ID", "Scope"}},
	} {
		index, _, _ := metadataLogicalField(collection.name)
		if err := r.owner.source.fields[index].Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
			full := collection.fields == nil
			if collection.name == "projects" && request.ResourceType == "project" {
				var err error
				full, err = r.owner.nativeID(ctx, node, request.ResourceID)
				if err != nil {
					return err
				}
			}
			var value reflect.Value
			var err error
			if full {
				value, err = r.owner.node(ctx, node, true)
			} else {
				value, err = r.owner.projectNode(ctx, node, collection.fields...)
			}
			if err == nil {
				field := reflect.ValueOf(&r.Data).Elem().Field(index)
				field.Set(reflect.Append(field, value))
			}
			return err
		}); err != nil {
			return err
		}
	}
	settings, _, _ := metadataLogicalField("teamSettings")
	if err := r.owner.source.fields[settings].MapEntries(ctx, func(key, node metadataLogicalNode) error {
		value, err := r.owner.node(ctx, key, true)
		if err != nil {
			return err
		}
		policy, err := r.owner.projectNode(ctx, node, "ParentTeamID", "Access", "MembershipRestriction")
		if err == nil {
			r.Data.TeamSettings[value.String()] = policy.Interface().(domain.TeamSettings)
		}
		return err
	}); err != nil {
		return err
	}
	seen := map[string]bool{}
	for id := request.TeamID; id != "" && !seen[id] && len(seen) < 16; id = r.Data.TeamSettings[id].ParentTeamID {
		seen[id] = true
		if err := r.owner.source.fields[settings].MapEntries(ctx, func(key, node metadataLogicalNode) error {
			match, err := key.StringEqual(ctx, id)
			if err != nil || !match {
				return err
			}
			value, err := r.owner.projectNode(ctx, node, "AgentGuidance")
			if err == nil {
				policy := r.Data.TeamSettings[id]
				policy.AgentGuidance = value.Interface().(domain.TeamSettings).AgentGuidance
				r.Data.TeamSettings[id] = policy
			}
			return err
		}); err != nil {
			return err
		}
	}
	settings, _, _ = metadataLogicalField("userSettings")
	if err := r.owner.source.fields[settings].MapEntries(ctx, func(key, node metadataLogicalNode) error {
		match, err := key.StringEqual(ctx, userID)
		if err != nil || !match {
			return err
		}
		value, err := r.owner.projectNode(ctx, node, "AgentInstructions")
		if err == nil {
			r.Data.UserSettings = map[string]domain.UserSettings{userID: value.Interface().(domain.UserSettings)}
		}
		return err
	}); err != nil {
		return err
	}
	if request.ResourceType == "document" {
		if err := r.owner.visitNative(ctx, "documents", func(_ int, node metadataLogicalNode) error {
			return r.owner.withHeader(ctx, node, []string{"ID", "SlugID", "PreviousSlugIDs"}, func(value reflect.Value) error {
				if documentReadRank(value.Interface().(domain.Document), DocumentReadRequest{Ref: request.ResourceID}) == 0 {
					return nil
				}
				value, err := r.owner.node(ctx, node, true)
				if err == nil {
					r.Data.Documents = append(r.Data.Documents, value.Interface().(domain.Document))
				}
				return err
			})
		}); err != nil {
			return err
		}
		document := &DocumentRead{Data: r.Data, owner: r.owner}
		if err := document.documentFavorites(ctx); err != nil {
			return err
		}
		r.Data = document.Data
	}
	return nil
}

func taskContextWorkspaceSettings(settings domain.WorkspaceSettings) domain.WorkspaceSettings {
	return domain.WorkspaceSettings{
		AgentInstructions: settings.AgentInstructions, FeatureFlags: settings.FeatureFlags,
		HIPAACompliance: settings.HIPAACompliance, PreventGuestAgents: settings.PreventGuestAgents,
		AgentWebAccess: settings.AgentWebAccess, AgentWebSearchDisabled: settings.AgentWebSearchDisabled,
		FeatureSettings: domain.FeatureSettings{ProjectUpdateGuidance: settings.FeatureSettings.ProjectUpdateGuidance, InitiativeUpdateGuidance: settings.FeatureSettings.InitiativeUpdateGuidance},
	}
}

func (r *ApplicationTaskContextRead) issueInputs(ctx context.Context, physical string, request ApplicationTaskContextRequest) error {
	if request.MetadataOnly {
		return nil
	}
	index, _, _ := metadataLogicalField("issues")
	absent, err := r.owner.source.fields[index].isNil(ctx)
	if err != nil {
		return err
	}
	if absent {
		return r.rawIssueInputs(ctx, physical, request.ResourceType == "project")
	}
	if request.ResourceType != "project" {
		return nil
	}
	err = r.owner.visitNative(ctx, "issues", func(_ int, node metadataLogicalNode) error {
		value, err := r.owner.projectNode(ctx, node, "ID", "Team", "Project", "ArchivedAt", "ParentID", "Permissions")
		if err == nil {
			r.Data.Issues = append(r.Data.Issues, value.Interface().(domain.Issue))
		}
		return err
	})
	return err
}

// Raw JSON owns identity and grants. Indexed team/project/permission columns
// can differ after supported SQL writes and cannot reproduce BootstrapFor.
func (r *ApplicationTaskContextRead) rawIssueInputs(ctx context.Context, physical string, retain bool) error {
	store := r.owner.store
	tx, borrowed := (*sqlTx)(nil), false
	if held, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && held.database == store.db && held.workspace == physical && held.reader != nil {
		tx, borrowed = held.reader, true
	} else {
		var err error
		tx, err = store.db.BeginTx(ctx, metadataReadOptions(store.dialect))
		if err != nil {
			return err
		}
		defer tx.Rollback()
	}
	idLength := r.owner.byteLength("id")
	var count, size int64
	workspace := r.Data.Workspace.URLKey
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(`+idLength+`),0) FROM issue_records WHERE workspace_key=?`, workspace).Scan(&count, &size); err != nil {
		return err
	}
	if count < 0 || size < 0 || count > metadataWorkBytes/256 || size > metadataWorkBytes/16 || count*256+size*16 > metadataWorkBytes-(8<<20) {
		return ErrMetadataWorkBudget
	}
	_, release, err := store.tryAdmitAdditionalMetadataWork(ctx, int(count*256+size*16)+(8<<20))
	if err != nil {
		return err
	}
	defer release()
	headers := make([]taskIssuePhysicalHeader, 0, int(count))
	storage := "0"
	if store.dialect == "sqlite" {
		storage = "CASE WHEN typeof(id)='blob' THEN 1 ELSE 0 END"
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,`+r.owner.byteLength("data")+`,`+storage+` FROM issue_records WHERE workspace_key=? ORDER BY collection_order,id`, workspace)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item taskIssuePhysicalHeader
		if err := rows.Scan(&item.id, &item.size, &item.physical); err != nil {
			rows.Close()
			return err
		}
		if int64(len(headers)) >= count || item.size < 0 {
			rows.Close()
			return ErrMetadataWorkBudget
		}
		headers = append(headers, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for start := 0; start < len(headers); {
		if headers[start].size <= taskIssueBatchBytes {
			end, size := start, int64(0)
			for end < len(headers) && end-start < taskIssueBatchRows && headers[end].size <= taskIssueBatchBytes-size {
				size += headers[end].size
				end++
			}
			if err := r.rawIssueBatch(ctx, tx, workspace, headers[start:end], size, retain); err != nil {
				return err
			}
			start = end
			continue
		}
		item := headers[start]
		if err := func() error {
			spool, err := newMetadataLogicalRecordSpool(ctx)
			if err != nil {
				return taskIssueSpoolError(err)
			}
			defer spool.Close()
			if !spool.CanAppend(item.size) {
				return ErrMetadataWorkBudget
			}
			data, id := "data", any(item.id)
			if store.dialect == "sqlite" {
				data = "CAST(data AS BLOB)"
				if item.physical != 0 {
					id = []byte(item.id)
				}
			}
			for offset := int64(0); offset < item.size; {
				wanted := min(int64(taskIssueChunkBytes), item.size-offset)
				var chunk []byte
				if err := tx.QueryRowContext(ctx, `SELECT SUBSTR(`+data+`,?,?) FROM issue_records WHERE workspace_key=? AND id=?`, offset+1, wanted, workspace, id).Scan(&chunk); err != nil {
					return err
				}
				if int64(len(chunk)) != wanted {
					return io.ErrUnexpectedEOF
				}
				if _, err := spool.Write(chunk); err != nil {
					return taskIssueSpoolError(err)
				}
				offset += wanted
			}
			return r.rawIssueHeader(ctx, spool, item.size, retain)
		}(); err != nil {
			return err
		}
		start++
	}
	if !borrowed {
		return tx.Rollback()
	}
	return nil
}

func (r *ApplicationTaskContextRead) rawIssueBatch(ctx context.Context, tx *sqlTx, workspace string, headers []taskIssuePhysicalHeader, size int64, retain bool) error {
	if len(headers) == 0 || len(headers) > taskIssueBatchRows || size < 0 || size > taskIssueBatchBytes {
		return ErrMetadataRecordQuery
	}
	// Hold raw rows and driver copies until the shared parser finishes. Close
	// SQL first so parser authority/admission never waits on an occupied cursor.
	_, release, err := r.owner.store.tryAdmitAdditionalMetadataWork(ctx, int(size)*3+len(headers)*512+64<<10)
	if err != nil {
		return err
	}
	defer release()
	queries, args := make([]string, 0, len(headers)), make([]any, 0, len(headers)*3)
	for index, header := range headers {
		id := any(header.id)
		if r.owner.store.dialect == "sqlite" && header.physical != 0 {
			id = []byte(header.id)
		}
		queries = append(queries, `SELECT `+strconv.Itoa(index)+` AS ordinal,CASE WHEN `+r.owner.byteLength("data")+`<=? THEN data ELSE NULL END AS data FROM issue_records WHERE workspace_key=? AND id=?`)
		args = append(args, header.size, workspace, id)
	}
	rows, err := tx.QueryContext(ctx, strings.Join(queries, " UNION ALL ")+` ORDER BY ordinal`, args...)
	if err != nil {
		return err
	}
	raw := make([][]byte, 0, len(headers))
	for rows.Next() {
		var ordinal int
		var body []byte
		if err := rows.Scan(&ordinal, &body); err != nil {
			rows.Close()
			return err
		}
		if ordinal != len(raw) || ordinal >= len(headers) || int64(len(body)) != headers[ordinal].size {
			rows.Close()
			return io.ErrUnexpectedEOF
		}
		raw = append(raw, body)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if len(raw) != len(headers) {
		return io.ErrUnexpectedEOF
	}
	for index, body := range raw {
		if err := r.rawIssueHeader(ctx, bytes.NewReader(body), headers[index].size, retain); err != nil {
			return err
		}
	}
	return nil
}

func (r *ApplicationTaskContextRead) rawIssueHeader(ctx context.Context, input io.ReaderAt, size int64, retain bool) error {
	part := metadataCollectionRange{size: size}
	parser := taskIssueJSON{ctx: ctx, input: input, owner: r.owner}
	if err := parser.validate(part, reflect.TypeFor[domain.Issue](), 0); err != nil {
		return err
	}
	if !retain {
		return nil
	}
	if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.Issue]().Size())*2+4096); err != nil {
		return err
	}
	var issue domain.Issue
	if err := parser.project(part, reflect.ValueOf(&issue).Elem(), 0); err != nil {
		return err
	}
	r.Data.Issues = append(r.Data.Issues, issue)
	return nil
}

func taskIssueSpoolError(err error) error {
	if errors.Is(err, ErrRecordSpoolBudget) {
		return errors.Join(ErrMetadataWorkBudget, err)
	}
	return errors.Join(ErrBoundedMetadataCoverage, err)
}

type taskIssueJSON struct {
	ctx    context.Context
	input  io.ReaderAt
	owner  *ResourceRead
	fields map[reflect.Type][]string
}

func (p taskIssueJSON) first(part metadataCollectionRange) (byte, error) {
	scanner := sw17CredentialScanner(p.ctx, p.input, part.offset, part.size)
	if err := scanner.spaces(); err != nil {
		return 0, err
	}
	return scanner.peek()
}

func (p taskIssueJSON) scalar(part metadataCollectionRange, target any, retained bool) error {
	if part.size < 0 || part.size > (metadataWorkBytes-4096)/16 {
		return ErrMetadataWorkBudget
	}
	cost := uint64(part.size)*16 + 4096
	if retained {
		if err := p.owner.reserve(p.ctx, cost); err != nil {
			return err
		}
	} else {
		_, release, err := p.owner.store.tryAdmitAdditionalMetadataWork(p.ctx, int(cost))
		if err != nil {
			return err
		}
		defer release()
	}
	return json.NewDecoder(io.NewSectionReader(p.input, part.offset, part.size)).Decode(target)
}

// Validate every known field, even when a later occurrence overwrites it.
// Unknown fields retain encoding/json's syntax-only validation.
func (p taskIssueJSON) validate(part metadataCollectionRange, typ reflect.Type, depth int) error {
	if depth >= metadataLogicalDepth {
		return ErrMetadataWorkBudget
	}
	first, err := p.first(part)
	if err != nil {
		return err
	}
	if typ == reflect.TypeFor[json.RawMessage]() {
		return validateMetadataCollectionJSON(p.ctx, io.NewSectionReader(p.input, part.offset, part.size))
	}
	if typ == reflect.TypeFor[time.Time]() {
		_, err := scaleP21SW23TeamTime(p.ctx, io.NewSectionReader(p.input, part.offset, part.size))
		return err
	}
	if typ.Kind() != reflect.Pointer && typ != reflect.TypeFor[domain.OptionalInt]() &&
		(typ.Implements(reflect.TypeFor[json.Unmarshaler]()) || reflect.PointerTo(typ).Implements(reflect.TypeFor[json.Unmarshaler]())) {
		return ErrBoundedMetadataCoverage
	}
	if first == 'n' {
		scanner := sw17CredentialScanner(p.ctx, p.input, part.offset, part.size)
		if err := scanner.spaces(); err != nil {
			return err
		}
		if err := scanner.literal("null"); err != nil {
			return err
		}
		return scanner.end()
	}
	if typ.Kind() == reflect.Pointer {
		return p.validate(part, typ.Elem(), depth+1)
	}
	if typ == reflect.TypeFor[domain.OptionalInt]() {
		var value domain.OptionalInt
		return p.scalar(part, &value, false)
	}
	scanner := sw17CredentialScanner(p.ctx, p.input, part.offset, part.size)
	if err := scanner.spaces(); err != nil {
		return err
	}
	switch typ.Kind() {
	case reflect.String:
		_, err := VisitMetadataJSONStringChunks(p.ctx, io.NewSectionReader(p.input, part.offset, part.size), func([]byte) error { return nil })
		return err
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return p.scalar(part, reflect.New(typ).Interface(), false)
	case reflect.Struct:
		if first != '{' {
			return ErrMetadataRecordQuery
		}
		if err := scanner.object(func(key, value metadataCollectionRange) error {
			index, err := sw17CredentialField(p.input, key, typ)
			if err != nil || index < 0 {
				return err
			}
			field := typ.Field(index)
			if field.Anonymous || strings.Contains(field.Tag.Get("json"), ",string") {
				return ErrBoundedMetadataCoverage
			}
			return p.validate(value, field.Type, depth+1)
		}, 0); err != nil {
			return err
		}
	case reflect.Slice, reflect.Array:
		if first != '[' {
			return ErrMetadataRecordQuery
		}
		if typ.Elem().Kind() == reflect.Uint8 {
			return ErrBoundedMetadataCoverage
		}
		index := 0
		if err := scanner.array(func(value metadataCollectionRange) error {
			index++
			if typ.Kind() == reflect.Array && index > typ.Len() {
				return nil
			}
			return p.validate(value, typ.Elem(), depth+1)
		}, 0); err != nil {
			return err
		}
	case reflect.Map:
		if first != '{' {
			return ErrMetadataRecordQuery
		}
		if typ.Key().Kind() != reflect.String {
			return ErrBoundedMetadataCoverage
		}
		if err := scanner.object(func(_ metadataCollectionRange, value metadataCollectionRange) error {
			return p.validate(value, typ.Elem(), depth+1)
		}, 0); err != nil {
			return err
		}
	case reflect.Interface:
		switch first {
		case '{':
			if err := scanner.object(func(_ metadataCollectionRange, value metadataCollectionRange) error {
				return p.validate(value, typ, depth+1)
			}, 0); err != nil {
				return err
			}
		case '[':
			if err := scanner.array(func(value metadataCollectionRange) error { return p.validate(value, typ, depth+1) }, 0); err != nil {
				return err
			}
		case '"':
			_, err := VisitMetadataJSONStringChunks(p.ctx, io.NewSectionReader(p.input, part.offset, part.size), func([]byte) error { return nil })
			return err
		default:
			var value any
			return p.scalar(part, &value, false)
		}
	default:
		return ErrBoundedMetadataCoverage
	}
	return scanner.end()
}

var taskIssueHeaderFields = map[reflect.Type][]string{
	reflect.TypeFor[domain.Issue]():           {"ID", "Team", "Project", "ArchivedAt", "ParentID", "Permissions"},
	reflect.TypeFor[domain.Team]():            {"ID"},
	reflect.TypeFor[domain.ProjectSummary]():  {"ID"},
	reflect.TypeFor[domain.IssuePermission](): {"SubjectType", "SubjectID", "Role"},
}

func (p taskIssueJSON) project(part metadataCollectionRange, target reflect.Value, depth int) error {
	if depth >= metadataLogicalDepth {
		return ErrMetadataWorkBudget
	}
	first, err := p.first(part)
	if err != nil {
		return err
	}
	if first == 'n' {
		if target.Kind() == reflect.Pointer || target.Kind() == reflect.Slice {
			target.SetZero()
		}
		return nil
	}
	if target.Kind() == reflect.Pointer {
		if target.IsNil() {
			if err := p.owner.reserve(p.ctx, uint64(target.Type().Elem().Size())*2+512); err != nil {
				return err
			}
			target.Set(reflect.New(target.Type().Elem()))
		}
		return p.project(part, target.Elem(), depth+1)
	}
	if target.Type() == reflect.TypeFor[time.Time]() {
		value, err := scaleP21SW23TeamTime(p.ctx, io.NewSectionReader(p.input, part.offset, part.size))
		if err == nil && value != nil {
			target.Set(reflect.ValueOf(*value))
		}
		return err
	}
	switch target.Kind() {
	case reflect.String:
		return p.scalar(part, target.Addr().Interface(), true)
	case reflect.Struct:
		fields := p.fields
		if fields == nil {
			fields = taskIssueHeaderFields
		}
		scanner := sw17CredentialScanner(p.ctx, p.input, part.offset, part.size)
		if err := scanner.spaces(); err != nil {
			return err
		}
		return scanner.object(func(key, value metadataCollectionRange) error {
			index, err := sw17CredentialField(p.input, key, target.Type())
			if err != nil || index < 0 || !slices.Contains(fields[target.Type()], target.Type().Field(index).Name) {
				return err
			}
			return p.project(value, target.Field(index), depth+1)
		}, 0)
	case reflect.Slice:
		target.SetLen(0)
		index := 0
		scanner := sw17CredentialScanner(p.ctx, p.input, part.offset, part.size)
		err := scanner.array(func(value metadataCollectionRange) error {
			if index == target.Cap() {
				if err := p.owner.reserve(p.ctx, uint64(target.Type().Elem().Size())*4+512); err != nil {
					return err
				}
				target.Set(reflect.Append(target, reflect.Zero(target.Type().Elem())))
			} else {
				target.SetLen(index + 1)
			}
			err := p.project(value, target.Index(index), depth+1)
			index++
			return err
		}, 0)
		if err == nil && index == 0 {
			target.Set(reflect.MakeSlice(target.Type(), 0, 0))
		}
		return err
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return p.scalar(part, target.Addr().Interface(), true)
	default:
		return ErrBoundedMetadataCoverage
	}
}
