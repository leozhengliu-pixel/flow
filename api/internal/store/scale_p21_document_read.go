package store

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// DocumentReadRequest describes the repository's document selection contracts.
// Ref is empty for list authorization; Suffix is the indexed document API's
// additional reference rule. ProjectIDs includes proposed PATCH attachments.
type DocumentReadRequest struct {
	Ref                string
	ProjectIDs         []string
	Trash              bool
	TrashOnly          bool
	Suffix             bool
	ReadOnly           bool
	ProjectionFallback bool
}

// DocumentRead owns native callback inputs as well as their response lifetime.
// In particular, ContentData retains its installed Go types.
type DocumentRead struct {
	Data  domain.Bootstrap
	owner *ResourceRead
}

func (r *DocumentRead) Close() {
	if r == nil {
		return
	}
	r.Data = domain.Bootstrap{}
	r.owner.Close()
}

var documentReadPolicyFields = []string{"teams", "teamSettings", "teamParents", "workspaceSettings"}

func documentReadRank(item domain.Document, request DocumentReadRequest) int {
	switch {
	case request.Ref == "":
		return 0
	case item.ID == request.Ref || item.SlugID == request.Ref:
		return 3
	case slices.Contains(item.PreviousSlugIDs, request.Ref):
		return 2
	case request.Suffix && domain.SlugSuffix(request.Ref) != "" && domain.SlugSuffix(item.SlugID) == domain.SlugSuffix(request.Ref):
		return 1
	}
	return 0
}

// BeginDocumentRead selects only the winning document and reciprocal project
// closure. Headers are transient, including on the canonical fallback. A caller
// lending a workspace snapshot owns its final live authority revalidation.
func (s *SQLiteStore) BeginDocumentRead(ctx context.Context, workspace, userID string, key *domain.APIKey, request DocumentReadRequest) (_ *DocumentRead, err error) {
	if !s.BoundedMetadataEnabled() {
		data, err := s.SelectedDocumentMetadata(ctx, workspace, userID, key, func(source domain.Bootstrap) (MetadataRecordScope, bool) {
			scope := MetadataRecordScope{Fields: append(slices.Clone(documentReadPolicyFields), "favorites")}
			best, rank := -1, 0
			for index, item := range source.Documents {
				if next := documentReadRank(item, request); next > rank {
					best, rank = index, next
				}
			}
			if best >= 0 && !request.TrashOnly {
				item := source.Documents[best]
				scope.Read = append(scope.Read, RecordKey{Kind: "documents", ID: item.ID})
				for _, project := range source.Projects {
					if slices.Contains(item.ProjectIDs, project.ID) || slices.Contains(request.ProjectIDs, project.ID) || slices.ContainsFunc(project.Resources, func(resource domain.ProjectResource) bool { return resource.ID == item.ID }) {
						scope.Read = append(scope.Read, RecordKey{Kind: "projects", ID: project.ID})
					}
				}
			}
			if request.Trash {
				for _, entry := range source.Trash {
					var item domain.Document
					if entry.ResourceType == "document" && json.Unmarshal(entry.Payload, &item) == nil && (documentReadRank(item, request) != 0 || request.Suffix && (entry.ID == request.Ref || entry.ResourceID == request.Ref)) {
						scope.Read = append(scope.Read, RecordKey{Kind: "trash", ID: entry.ID})
					}
				}
			}
			return scope, true
		})
		return &DocumentRead{Data: data}, err
	}
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	owner := &ResourceRead{store: s}
	read := &DocumentRead{owner: owner}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err = owner.reserve(ctx, 2<<20); err != nil {
		return nil, err
	}
	tx, borrowed := (*sqlTx)(nil), false
	var callerData domain.Bootstrap
	if existing, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && existing.database == s.db && existing.workspace == workspace {
		tx, borrowed = existing.reader, true
		callerData = existing.metadata
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
	snapshot := context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: tx})
	snapshot = owner.metadataOutputContext(snapshot)
	stamp, epoch, err := s.accessStampAndEpoch(snapshot, workspace)
	if err != nil {
		return nil, err
	}
	source, err := s.retainMetadataLogicalSource(ctx, workspace)
	if err != nil {
		return nil, err
	}
	owner.source = source
	current := source.Stamp() == stamp
	canonical := false
	scope, selected, err := read.documentLegacyScope(snapshot, request)
	if err != nil {
		return nil, err
	}
	valid := selected && validateMetadataRecordScope(scope) == nil
	fallback := !valid
	if fallback && !request.ProjectionFallback {
		return nil, ErrResourceAccessChanged
	}
	if !current {
		if valid {
			var raw []byte
			var size int
			err = s.db.QueryRowContext(snapshot, `SELECT LENGTH(data),CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_states WHERE workspace_key=?`, MetadataRecordPageMaxBytes, workspace).Scan(&size, &raw)
			if err == nil && size <= MetadataRecordPageMaxBytes && raw != nil {
				if err = read.documentMarkerWork(snapshot, workspace, scope); err != nil {
					return nil, err
				}
				read.Data, err = s.decodeWorkspaceMetadata(WithMetadataRecords(snapshot, scope), workspace, raw, tx)
				canonical = err == nil
			}
			if err != nil && !errors.Is(err, ErrMetadataPageBudget) && !errors.Is(err, ErrMetadataRecordQuery) {
				return nil, err
			}
		}
		if !canonical {
			if !request.ProjectionFallback {
				return nil, ErrResourceAccessChanged
			}
			// The old server's selector/budget fallback reads its installed
			// projection when there is no caller-owned workspace snapshot.
			// A SQL generation mismatch does not normalize that native input.
			fallback, err = true, nil
			read.Data = domain.Bootstrap{}
		}
	}
	if fallback && callerData.Workspace.ID != "" {
		candidate, captureErr := read.documentCallerSource(snapshot, callerData, stamp, request)
		if captureErr != nil {
			return nil, captureErr
		}
		owner.source.Release()
		owner.source = candidate
	}
	if !canonical {
		for _, kind := range append([]string{"workspace", "viewer"}, documentReadPolicyFields...) {
			if err = read.documentNativeField(snapshot, kind, request.ReadOnly); err != nil {
				return nil, err
			}
		}
	}
	if !canonical && !request.TrashOnly && request.Ref != "" {
		var node metadataLogicalNode
		var id string
		node, id, err = read.documentChoice(snapshot, request)
		if err != nil {
			return nil, err
		}
		if id != "" {
			if fallback {
				err = owner.visitNative(snapshot, "documents", func(_ int, candidate metadataLogicalNode) error {
					return owner.withHeader(snapshot, candidate, []string{"ID", "SlugID", "PreviousSlugIDs"}, func(header reflect.Value) error {
						if documentReadRank(header.Interface().(domain.Document), request) == 0 {
							return nil
						}
						value, err := owner.node(snapshot, candidate, request.ReadOnly)
						if err == nil {
							read.Data.Documents = append(read.Data.Documents, value.Interface().(domain.Document))
						}
						return err
					})
				})
			} else {
				var value reflect.Value
				value, err = owner.node(snapshot, node, request.ReadOnly)
				if err == nil {
					read.Data.Documents = []domain.Document{value.Interface().(domain.Document)}
				}
			}
			if err != nil {
				return nil, err
			}
			if err = read.documentProjects(snapshot, request.ProjectIDs, request.ReadOnly); err != nil {
				return nil, err
			}
			if err = read.documentFavorites(snapshot); err != nil {
				return nil, err
			}
		}
	}
	if !canonical && request.Trash && (request.Suffix || fallback || len(read.Data.Documents) == 0) {
		if err = read.documentTrash(snapshot, request); err != nil {
			return nil, err
		}
	}
	if userID != "" {
		err = read.documentProjectionWork(snapshot, stamp.workspaceID, userID)
		if err == nil {
			read.Data, err = s.projectDocumentRecordMetadata(snapshot, workspace, userID, key, read.Data)
		}
	} else {
		stripTrashLinks(&read.Data)
		read.Data.ViewerRole = "admin"
	}
	if err != nil {
		return nil, err
	}
	if !borrowed {
		if err = tx.Rollback(); err != nil {
			return nil, err
		}
		if userID != "" && s.db.journalReady.Load() {
			current, epochErr := s.AuthzEpoch(ctx, stamp.workspaceID)
			if epochErr != nil {
				return nil, epochErr
			}
			if current != epoch {
				return nil, errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
			}
		}
	}
	// A borrowed read preserves its enclosing snapshot, including pool size
	// one. Its enclosing boundary owns the final live authority revalidation.
	latest, err := readMetadataStamp(ctx, s.db, workspace)
	if err != nil {
		return nil, err
	}
	if latest != stamp {
		return nil, errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return read, nil
}

// Preflight the columns actually transferred by the legacy live projector in
// the same repeatable snapshot. Installation JSON and its derived users have
// their own reader, registered into this read's output lifetime above.
func (r *DocumentRead) documentProjectionWork(ctx context.Context, accessWorkspaceID, userID string) error {
	s := r.owner.store
	identity := `COALESCE((SELECT i.subject FROM auth_identities i WHERE i.user_id=u.id AND (i.provider IN ('oidc','saml') OR (i.provider='scim' AND i.issuer=m.workspace_id)) ORDER BY CASE WHEN i.provider='scim' THEN 0 ELSE 1 END,i.created_at,i.id LIMIT 1),'')`
	memberColumns := []string{"u.id", "u.email", "u.name", "u.display_name", "u.avatar_url", "u.email_verified_at", "u.active", "u.deleted_at", "m.role", "m.status", "m.joined_at", "m.last_seen_at", identity}
	if err := r.documentProjectionRows(ctx, memberColumns,
		`FROM workspace_memberships m JOIN auth_users u ON u.id=m.user_id WHERE m.workspace_id=?`, r.Data.Workspace.ID); err != nil {
		return err
	}
	// ResolveAccess runs before and after projection. Its principal query has
	// an independent workspace binding and can repeat the user on each grant.
	if err := r.documentProjectionRows(ctx,
		[]string{"u.id", "u.email", "u.name", "u.display_name", "u.avatar_url", "u.email_verified_at", "u.active", "u.deleted_at", "m.role", "m.status", "t.team_id", "t.role"},
		`FROM workspace_memberships m JOIN auth_users u ON u.id=m.user_id LEFT JOIN team_memberships t ON t.workspace_id=m.workspace_id AND t.user_id=m.user_id WHERE m.workspace_id=? AND m.user_id=?`,
		accessWorkspaceID, userID); err != nil {
		return err
	}
	if err := r.documentProjectionRows(ctx,
		[]string{"team_id", "user_id", "role", "joined_at", "origin"},
		`FROM team_memberships WHERE workspace_id=?`, r.Data.Workspace.ID); err != nil {
		return err
	}
	if err := r.documentProjectionRows(ctx,
		[]string{"d.id", "d.team_id", "d.resource_type", "d.resource_id", "d.position", "d.created_at", "d.updated_at"},
		`FROM team_default_favorites d JOIN team_memberships tm ON tm.team_id=d.team_id AND tm.workspace_id=? AND tm.user_id=? JOIN workspace_memberships m ON m.workspace_id=tm.workspace_id AND m.user_id=tm.user_id AND m.status='active' WHERE d.workspace_key=?`,
		r.Data.Workspace.ID, userID, r.Data.Workspace.URLKey); err != nil {
		return err
	}
	if err := r.documentProjectionRows(ctx, []string{"resource_type", "resource_id"},
		`FROM team_default_favorite_overrides WHERE workspace_key=? AND user_id=? AND hidden=1`,
		r.Data.Workspace.URLKey, userID); err != nil {
		return err
	}
	role, _, err := s.WorkspaceRole(ctx, r.Data.Workspace.ID, userID)
	if err != nil || !isWorkspaceAdminRole(role) {
		return err
	}
	// team_ids is decoded as []string; its byte allowance also covers all
	// structural elements, including compact null/empty-string arrays.
	if err := r.documentProjectionRows(ctx,
		[]string{"id", "workspace_id", "email", "role", "team_ids", "inviter_id", "status", "expires_at", "created_at", "accepted_at"},
		`FROM workspace_invitations WHERE workspace_id=?`, r.Data.Workspace.ID); err != nil {
		return err
	}
	columns := []string{"u.id", "u.email", "u.name", "u.display_name", "u.avatar_url", "d.role", "d.joined_at", "d.left_at"}
	if err := r.documentProjectionRows(ctx, columns,
		`FROM workspace_departures d JOIN auth_users u ON u.id=d.user_id WHERE d.workspace_id=? AND NOT EXISTS (SELECT 1 FROM workspace_memberships m WHERE m.workspace_id=d.workspace_id AND m.user_id=d.user_id)`,
		r.Data.Workspace.ID); err != nil {
		return err
	}
	return r.documentProjectionRows(ctx,
		[]string{"u.id", "u.email", "u.name", "u.display_name", "u.avatar_url", "u.created_at", "u.updated_at"},
		`FROM workspace_metadata_records r JOIN auth_users u ON u.id=r.record_key WHERE r.workspace_key=? AND r.field='users' AND r.record_key LIKE 'app_%' AND u.active=0 AND NOT EXISTS (SELECT 1 FROM workspace_memberships m WHERE m.workspace_id=? AND m.user_id=u.id) AND NOT EXISTS (SELECT 1 FROM workspace_departures d WHERE d.workspace_id=? AND d.user_id=u.id)`,
		r.Data.Workspace.URLKey, r.Data.Workspace.ID, r.Data.Workspace.ID)
}

func (r *DocumentRead) documentProjectionRows(ctx context.Context, columns []string, from string, args ...any) error {
	length := "0"
	for _, column := range columns {
		// Projection columns include numeric scalars; PostgreSQL's byte
		// length function accepts text or bytea, without implicit casts.
		if r.owner.store.dialect == "postgres" {
			column = "CAST(" + column + " AS TEXT)"
		}
		length += "+COALESCE(" + r.owner.byteLength(column) + ",0)"
	}
	var count, size int64
	if err := r.owner.store.db.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM("+length+"),0) "+from, args...).Scan(&count, &size); err != nil {
		return err
	}
	if count < 0 || size < 0 || uint64(count) > metadataWorkBytes/4096 || uint64(size) > metadataWorkBytes/32 {
		return ErrMetadataWorkBudget
	}
	if count == 0 && size == 0 {
		return nil
	}
	// Fixed records, slice growth, maps, repeated principal checks, parsed
	// timestamps and JSON escaping remain charged through response encoding.
	return r.owner.reserve(ctx, uint64(size)*32+uint64(count)*4096)
}

func (r *DocumentRead) documentCallerSource(ctx context.Context, data domain.Bootstrap, stamp copyStamp, request DocumentReadRequest) (*metadataLogicalSource, error) {
	candidate := domain.Bootstrap{Workspace: data.Workspace, Viewer: data.Viewer, Teams: data.Teams, TeamSettings: data.TeamSettings, TeamParents: data.TeamParents, WorkspaceSettings: data.WorkspaceSettings}
	projectIDs, documentIDs := slices.Clone(request.ProjectIDs), []string{}
	for _, item := range data.Documents {
		if request.TrashOnly || documentReadRank(item, request) == 0 {
			continue
		}
		if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.Document]().Size())*2+2048); err != nil {
			return nil, err
		}
		candidate.Documents = append(candidate.Documents, item)
		projectIDs = append(projectIDs, item.ProjectIDs...)
		documentIDs = append(documentIDs, item.ID)
	}
	for _, item := range data.Projects {
		if !slices.Contains(projectIDs, item.ID) && !slices.ContainsFunc(item.Resources, func(resource domain.ProjectResource) bool { return slices.Contains(documentIDs, resource.ID) }) {
			continue
		}
		if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.Project]().Size())*2+2048); err != nil {
			return nil, err
		}
		candidate.Projects = append(candidate.Projects, item)
	}
	for _, item := range data.Favorites {
		if item.ResourceType == "document" && slices.Contains(documentIDs, item.ResourceID) {
			if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.Favorite]().Size())*2+2048); err != nil {
				return nil, err
			}
			candidate.Favorites = append(candidate.Favorites, item)
		}
	}
	exactTrash := request.Suffix && slices.ContainsFunc(data.Trash, func(item domain.TrashEntry) bool {
		return item.ResourceType == "document" && (item.ID == request.Ref || item.ResourceID == request.Ref)
	})
	if request.Trash {
		for _, item := range data.Trash {
			if item.ResourceType != "document" {
				continue
			}
			match := exactTrash && (item.ID == request.Ref || item.ResourceID == request.Ref)
			if !exactTrash {
				if err := r.documentJSONHeader(ctx, bytes.NewReader(item.Payload), 0, int64(len(item.Payload)), func(header domain.Document) error {
					match = documentReadRank(header, request) != 0
					return nil
				}); err != nil {
					return nil, err
				}
			}
			if match {
				if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.TrashEntry]().Size())*2+2048); err != nil {
					return nil, err
				}
				item.Links = nil
				candidate.Trash = append(candidate.Trash, item)
			}
		}
	}
	return captureMetadataLogicalSource(ctx, candidate, stamp)
}

func (r *DocumentRead) documentMarkerWork(ctx context.Context, workspace string, scope MetadataRecordScope) error {
	s := r.owner.store
	for _, key := range append(slices.Clone(scope.Read), scope.Write...) {
		if key.Kind != "documents" && key.Kind != "trash" {
			continue
		}
		var size int
		var raw []byte
		if err := s.db.QueryRowContext(ctx, `SELECT LENGTH(data),CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+s.metadataRecordKeyEquals("record_key", "?"), MetadataRecordPageMaxBytes, workspace, key.Kind, key.ID).Scan(&size, &raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		if raw == nil {
			continue
		}
		if err := r.owner.reserve(ctx, uint64(size)*32+4096); err != nil {
			return err
		}
		document := raw
		if key.Kind == "trash" {
			var entry struct{ Payload json.RawMessage }
			if err := json.Unmarshal(raw, &entry); err != nil {
				return err
			}
			document = entry.Payload
		}
		var header struct {
			Revisions []struct {
				Offloaded map[string]string `json:"_offloaded"`
			}
		}
		if json.Unmarshal(document, &header) != nil {
			continue
		}
		var count uint64
		for _, revision := range header.Revisions {
			if revision.Offloaded["body"] == "document" {
				count++
			}
		}
		if count != 0 {
			if uint64(size) > metadataWorkBytes/16/count {
				return ErrMetadataWorkBudget
			}
			if err := r.owner.reserve(ctx, uint64(size)*16*count); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *DocumentRead) documentLegacyScope(ctx context.Context, request DocumentReadRequest) (MetadataRecordScope, bool, error) {
	scope := MetadataRecordScope{Fields: append(slices.Clone(documentReadPolicyFields), "favorites")}
	if request.Ref == "" {
		return scope, true, nil
	}
	node, id, err := r.documentChoice(ctx, request)
	if err != nil {
		return scope, false, err
	}
	if id != "" && !request.TrashOnly {
		scope.Write = append(scope.Write, RecordKey{Kind: "documents", ID: id})
		err := r.owner.withHeader(ctx, node, []string{"ProjectIDs"}, func(value reflect.Value) error {
			projectIDs := append(slices.Clone(value.Interface().(domain.Document).ProjectIDs), request.ProjectIDs...)
			return r.owner.visitNative(ctx, "projects", func(_ int, project metadataLogicalNode) error {
				return r.documentProjectMatch(ctx, project, projectIDs, []string{id}, func(projectID string, match bool) error {
					if match {
						if err := r.owner.reserve(ctx, uint64(len(projectID))*16+2048); err != nil {
							return err
						}
						scope.Write = append(scope.Write, RecordKey{Kind: "projects", ID: projectID})
					}
					return nil
				})
			})
		})
		if err != nil || !request.Suffix {
			return scope, true, err
		}
	}
	selected := request.Suffix
	if request.Trash {
		if request.Suffix {
			_, err := r.documentNativeTrash(ctx, request, func(id string, _ metadataLogicalNode) error {
				if err := r.owner.reserve(ctx, uint64(len(id))*16+2048); err != nil {
					return err
				}
				scope.Read = append(scope.Read, RecordKey{Kind: "trash", ID: id})
				return nil
			})
			return scope, true, err
		}
		err := r.owner.visitNative(ctx, "trash", func(_ int, node metadataLogicalNode) error {
			return r.owner.withHeader(ctx, node, []string{"ID", "ResourceType", "ResourceID"}, func(value reflect.Value) error {
				item := value.Interface().(domain.TrashEntry)
				if item.ResourceType == "document" && (item.ResourceID == request.Ref || request.Suffix && item.ID == request.Ref) {
					if err := r.owner.reserve(ctx, uint64(len(item.ID))*16+2048); err != nil {
						return err
					}
					scope.Read = append(scope.Read, RecordKey{Kind: "trash", ID: item.ID})
					selected = true
				}
				return nil
			})
		})
		return scope, selected, err
	}
	return scope, selected, nil
}

func (r *DocumentRead) documentProjectMatch(ctx context.Context, node metadataLogicalNode, projectIDs, documentIDs []string, visit func(string, bool) error) error {
	return r.owner.withHeader(ctx, node, []string{"ID"}, func(value reflect.Value) error {
		id := value.Interface().(domain.Project).ID
		match := slices.Contains(projectIDs, id)
		if !match {
			err := node.Children(ctx, func(key reflect.Value, resources metadataLogicalNode) error {
				if key.String() != "Resources" {
					return nil
				}
				return r.owner.nativeArray(ctx, resources, func(_ int, resource metadataLogicalNode) error {
					return r.owner.withHeader(ctx, resource, []string{"ID"}, func(value reflect.Value) error {
						if slices.Contains(documentIDs, value.Interface().(domain.ProjectResource).ID) {
							match = true
							return ErrMetadataIterationDone
						}
						return nil
					})
				})
			})
			if err != nil && !errors.Is(err, ErrMetadataIterationDone) {
				return err
			}
		}
		return visit(id, match)
	})
}

func (r *DocumentRead) documentNativeField(ctx context.Context, kind string, wire bool) error {
	field, _, _ := metadataLogicalField(kind)
	value, err := r.owner.node(ctx, r.owner.source.fields[field], wire)
	if err == nil {
		reflect.ValueOf(&r.Data).Elem().Field(field).Set(value)
	}
	return err
}

func (r *DocumentRead) documentChoice(ctx context.Context, request DocumentReadRequest) (metadataLogicalNode, string, error) {
	best, id, rank := metadataLogicalNode{}, "", 0
	err := r.owner.visitNative(ctx, "documents", func(_ int, node metadataLogicalNode) error {
		return r.owner.withHeader(ctx, node, []string{"ID", "SlugID", "PreviousSlugIDs"}, func(value reflect.Value) error {
			item := value.Interface().(domain.Document)
			if next := documentReadRank(item, request); next > rank {
				best, id, rank = node, item.ID, next
			}
			if rank == 3 {
				return ErrMetadataIterationDone
			}
			return nil
		})
	})
	return best, id, err
}

func (r *DocumentRead) documentProjects(ctx context.Context, proposed []string, wire bool) error {
	ids, documentIDs := slices.Clone(proposed), []string{}
	for _, item := range r.Data.Documents {
		ids = append(ids, item.ProjectIDs...)
		documentIDs = append(documentIDs, item.ID)
	}
	return r.owner.visitNative(ctx, "projects", func(_ int, node metadataLogicalNode) error {
		match := false
		err := r.documentProjectMatch(ctx, node, ids, documentIDs, func(_ string, found bool) error {
			match = found
			return nil
		})
		if err != nil || !match {
			return err
		}
		value, err := r.owner.node(ctx, node, wire)
		if err == nil {
			r.Data.Projects = append(r.Data.Projects, value.Interface().(domain.Project))
		}
		return err
	})
}

func (r *DocumentRead) documentFavorites(ctx context.Context) error {
	ids := []string{}
	for _, item := range r.Data.Documents {
		ids = append(ids, item.ID)
	}
	names := []string{"ID", "UserID", "ResourceType", "ResourceID"}
	return r.owner.visitNative(ctx, "favorites", func(_ int, node metadataLogicalNode) error {
		return r.owner.withHeader(ctx, node, names, func(value reflect.Value) error {
			item := value.Interface().(domain.Favorite)
			if item.ResourceType != "document" || !slices.Contains(ids, item.ResourceID) {
				return nil
			}
			owned, err := r.owner.projectNode(ctx, node, names...)
			if err == nil {
				r.Data.Favorites = append(r.Data.Favorites, owned.Interface().(domain.Favorite))
			}
			return err
		})
	})
}

func (r *DocumentRead) documentTrash(ctx context.Context, request DocumentReadRequest) error {
	_, err := r.documentNativeTrash(ctx, request, func(id string, node metadataLogicalNode) error {
		value, err := r.owner.node(ctx, node, request.ReadOnly)
		if err == nil {
			r.Data.Trash = append(r.Data.Trash, value.Interface().(domain.TrashEntry))
		}
		return err
	})
	return err
}

func (r *DocumentRead) documentPayloadHeader(ctx context.Context, node metadataLogicalNode, visit func(domain.Document) error) error {
	var payload metadataLogicalNode
	if err := node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if key.String() == "Payload" {
			payload = child
		}
		return nil
	}); err != nil {
		return err
	}
	cursor := metadataLogicalCursor{payload, payload.offset + 16, ctx}
	var prefix [9]byte
	if err := cursor.read(prefix[:1]); err != nil || prefix[0] == 0 {
		return err
	}
	length, err := r.owner.nativeUint(&cursor)
	if err != nil {
		return err
	}
	if length > uint64(payload.size) || cursor.offset+int64(length) > payload.offset+payload.size {
		return ErrMetadataRecordQuery
	}
	return r.documentJSONHeader(ctx, payload.source.file, cursor.offset, int64(length), visit)
}

func (r *DocumentRead) documentJSONHeader(ctx context.Context, source io.ReaderAt, offset, length int64, visit func(domain.Document) error) error {
	scratch := &ResourceRead{store: r.owner.store}
	defer scratch.Close()
	if err := scratch.reserve(ctx, metadataRecordChunkBytes+4096); err != nil {
		return err
	}
	reader := io.NewSectionReader(source, offset, length)
	scanner := metadataCollectionScanner{ctx: ctx, input: bufio.NewReaderSize(reader, metadataRecordChunkBytes)}
	if err := scanner.spaces(); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	var header domain.Document
	selectField := func(key, value metadataCollectionRange) error {
		if key.size > 256 {
			return nil
		}
		if err := scratch.reserve(ctx, uint64(key.size)*4+4096); err != nil {
			return err
		}
		raw := make([]byte, int(key.size))
		if _, err := source.ReadAt(raw, offset+key.offset); err != nil {
			return err
		}
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return err
		}
		field, found := resourceWindowJSONField(reflect.TypeFor[domain.Document](), name)
		if !found || !slices.Contains([]string{"ID", "SlugID", "PreviousSlugIDs"}, field.Name) {
			return nil
		}
		if value.size < 0 || uint64(value.size) > resourceWindowMaxWork {
			return ErrMetadataWorkBudget
		}
		if err := scratch.reserve(ctx, uint64(value.size)+4096); err != nil {
			return err
		}
		raw = make([]byte, int(value.size))
		if _, err := source.ReadAt(raw, offset+value.offset); err != nil {
			return err
		}
		cost, err := resourceWindowTypedJSONWork(ctx, raw, field.Type)
		if err != nil {
			return err
		}
		if cost > resourceWindowMaxWork/3 {
			return ErrMetadataWorkBudget
		}
		if err := scratch.reserve(ctx, cost*3+4096); err != nil {
			return err
		}
		return json.Unmarshal(raw, reflect.ValueOf(&header).Elem().Field(field.Index[0]).Addr().Interface())
	}
	var err error
	if next, peekErr := scanner.peek(); peekErr != nil {
		err = peekErr
	} else if next == 'n' {
		err = scanner.literal("null")
	} else {
		err = scanner.object(selectField, 0)
	}
	if err == nil {
		err = scanner.end()
	}
	if errors.Is(err, errMetadataCollectionJSON) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return nil
	}
	var syntax *json.SyntaxError
	var valueType *json.UnmarshalTypeError
	if errors.As(err, &syntax) || errors.As(err, &valueType) {
		return nil
	}
	if err != nil {
		return err
	}
	return visit(header)
}

func (r *DocumentRead) documentNativeTrash(ctx context.Context, request DocumentReadRequest, visit func(string, metadataLogicalNode) error) (bool, error) {
	exact, selected := false, false
	if request.Suffix {
		err := r.owner.visitNative(ctx, "trash", func(_ int, node metadataLogicalNode) error {
			return r.owner.withHeader(ctx, node, []string{"ID", "ResourceID", "ResourceType"}, func(value reflect.Value) error {
				entry := value.Interface().(domain.TrashEntry)
				exact = exact || entry.ResourceType == "document" && (entry.ID == request.Ref || entry.ResourceID == request.Ref)
				return nil
			})
		})
		if err != nil {
			return false, err
		}
	}
	err := r.owner.visitNative(ctx, "trash", func(_ int, node metadataLogicalNode) error {
		return r.owner.withHeader(ctx, node, []string{"ID", "ResourceID", "ResourceType"}, func(value reflect.Value) error {
			entry := value.Interface().(domain.TrashEntry)
			if entry.ResourceType != "document" {
				return nil
			}
			match := func() error {
				selected = true
				return visit(entry.ID, node)
			}
			if exact {
				if entry.ID == request.Ref || entry.ResourceID == request.Ref {
					return match()
				}
				return nil
			}
			return r.documentPayloadHeader(ctx, node, func(header domain.Document) error {
				if documentReadRank(header, request) != 0 {
					return match()
				}
				return nil
			})
		})
	})
	return selected, err
}
