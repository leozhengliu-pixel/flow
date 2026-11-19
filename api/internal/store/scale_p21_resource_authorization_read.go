package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

// ResourceAuthorizationRead owns only the point-resource authorization result.
// Response projection, favorites and display references belong to the handler.
type ResourceAuthorizationRead struct {
	Allowed bool
	mu      sync.Mutex
	owner   *ResourceRead
}

func (r *ResourceAuthorizationRead) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Allowed = false
	r.owner.Close()
}

// BeginResourceAuthorizationRead preserves BeginResourceRead's source choice,
// first visible slug winner and final authority check without a detail output.
func (s *SQLiteStore) BeginResourceAuthorizationRead(ctx context.Context, access AccessContext, kind, ref string) (_ *ResourceAuthorizationRead, err error) {
	if !s.BoundedMetadataEnabled() {
		_, allowed, err := s.ResourceReadMetadata(ctx, access, kind, ref)
		if err != nil {
			return nil, err
		}
		return &ResourceAuthorizationRead{Allowed: allowed}, nil
	}
	if err := access.checkBinding(s); err != nil {
		return nil, err
	}
	if kind != "projects" && kind != "initiatives" {
		return nil, sql.ErrNoRows
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	read := &ResourceAuthorizationRead{owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err := read.owner.reserve(ctx, 2<<20); err != nil {
		return nil, err
	}
	p := access.policy
	s.mu.RLock()
	_, found := s.workspaces[p.workspace]
	s.metadataResidency.mu.Lock()
	source := s.metadataResidency.logical[p.workspace]
	if !found {
		source = nil
	}
	current := found && source != nil && source.Stamp() == p.stamp
	if source != nil {
		err = source.Retain()
	}
	s.metadataResidency.mu.Unlock()
	s.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	read.owner.source = source
	if current {
		read.Allowed, err = read.installed(ctx, p, kind, ref)
	} else {
		var tx *sqlTx
		borrowed := false
		if held, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && held.database == s.db && held.workspace == p.workspace {
			if held.reader == nil {
				return nil, ErrMetadataRecordQuery
			}
			tx, borrowed = held.reader, true
		} else {
			tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
			if err != nil {
				return nil, err
			}
		}
		snapshot := context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: p.workspace, reader: tx})
		var inline bool
		inline, err = read.owner.inlineSnapshotSource(snapshot, tx, p.workspace, resourceAuthorizationCanonicalFields)
		if err == nil && inline {
			read.Allowed, err = read.installed(ctx, p, kind, ref)
		} else if err == nil {
			read.Allowed, err = read.canonical(snapshot, tx, p, kind, ref)
		}
		if !borrowed {
			if closeErr := tx.Rollback(); err == nil {
				err = closeErr
			}
		}
	}
	if err == nil {
		if s.db.journalReady.Load() {
			var epoch string
			epoch, err = s.AuthzEpoch(ctx, access.WorkspaceID)
			if err == nil && epoch != access.AuthzEpoch {
				err = errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
			}
		} else {
			var stamp copyStamp
			stamp, err = readMetadataStamp(ctx, s.db, p.workspace)
			if err == nil && stamp != p.stamp {
				err = errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
			}
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return read, nil
}

func (r *ResourceAuthorizationRead) installed(ctx context.Context, p *resourceAccessPolicy, kind, ref string) (bool, error) {
	best, allowed := domain.SlugNoMatch, false
	names := resourceProjectHeader
	if kind == "initiatives" {
		names = resourceInitiativeHeader
	}
	err := r.owner.visitNative(ctx, kind, func(_ int, node metadataLogicalNode) error {
		rank, err := r.owner.nativeRank(ctx, node, ref)
		if err != nil || rank <= best {
			return err
		}
		return r.owner.withHeader(ctx, node, names, func(value reflect.Value) error {
			visible, permit := false, false
			if kind == "projects" {
				item := value.Interface().(domain.Project)
				visible, permit = p.projectVisible(item), p.projectAllowed(item)
			} else {
				var err error
				visible, permit, _, err = r.owner.nativeInitiativeFlags(ctx, p, value.Interface().(domain.Initiative))
				if err != nil {
					return err
				}
			}
			if visible {
				best, allowed = rank, permit
				if rank == domain.SlugMatchExact {
					return ErrMetadataIterationDone
				}
			}
			return nil
		})
	})
	return allowed, err
}

var resourceAuthorizationProjectFields = append(slices.Clone(resourceProjectHeader), "DependencyIDs", "Initiatives")
var resourceAuthorizationInitiativeFields = append(slices.Clone(resourceInitiativeHeader), "ParentInitiativeIDs")

// Small raw rows have one capped SQL transfer. Larger rows use the existing
// fixed-chunk reader and spool; both paths share the existing typed validator
// and field projector, including repeated/folded field and null semantics.
func (r *ResourceAuthorizationRead) header(ctx context.Context, tx *sqlTx, workspace, kind, key string, typ reflect.Type, fields []string) (reflect.Value, error) {
	scratch := &ResourceRead{store: r.owner.store}
	defer scratch.Close()
	if err := scratch.reserve(ctx, MetadataRecordPageMaxBytes*16+128<<10); err != nil {
		return reflect.Value{}, err
	}
	length := scratch.byteLength("data")
	var size int64
	var raw []byte
	s := r.owner.store
	err := tx.QueryRowContext(ctx, "SELECT "+length+",CASE WHEN "+length+"<=? THEN data ELSE NULL END FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND "+s.metadataRecordKeyEquals("record_key", "?"),
		MetadataRecordPageMaxBytes, workspace, kind, key).Scan(&size, &raw)
	if err != nil {
		return reflect.Value{}, err
	}
	if size < 0 {
		return reflect.Value{}, ErrMetadataRecordQuery
	}
	var input io.ReaderAt
	if size <= MetadataRecordPageMaxBytes {
		if int64(len(raw)) != size {
			return reflect.Value{}, io.ErrUnexpectedEOF
		}
		input = bytes.NewReader(raw)
	} else {
		spool, err := newMetadataLogicalRecordSpool(s.metadataLogicalScratchContext(ctx, workspace))
		if err != nil {
			return reflect.Value{}, err
		}
		defer spool.Close()
		if !spool.CanAppend(size) {
			return reflect.Value{}, ErrRecordSpoolBudget
		}
		reader := &canonicalMetadataReader{ctx: ctx, tx: tx, workspace: workspace, key: RecordKey{Kind: kind, ID: key}, size: size}
		count, err := io.CopyBuffer(spool, reader, make([]byte, metadataRecordChunkBytes))
		if err != nil {
			return reflect.Value{}, err
		}
		if count != size {
			return reflect.Value{}, io.ErrUnexpectedEOF
		}
		input = spool
	}
	part := metadataCollectionRange{size: size}
	parser := taskIssueJSON{ctx: ctx, input: input, owner: scratch, fields: map[reflect.Type][]string{typ: fields}}
	if err := parser.validate(part, typ, 0); err != nil {
		return reflect.Value{}, err
	}
	if err := r.owner.reserve(ctx, uint64(typ.Size())*2+4096); err != nil {
		return reflect.Value{}, err
	}
	value := reflect.New(typ).Elem()
	parser.owner = r.owner
	if err := parser.project(part, value, 0); err != nil {
		return reflect.Value{}, err
	}
	return value, nil
}

func (r *ResourceAuthorizationRead) canonical(ctx context.Context, tx *sqlTx, p *resourceAccessPolicy, kind, ref string) (bool, error) {
	typ, fields := reflect.TypeFor[domain.Project](), resourceAuthorizationProjectFields
	if kind == "initiatives" {
		typ, fields = reflect.TypeFor[domain.Initiative](), resourceAuthorizationInitiativeFields
	}
	add := func(data *domain.Bootstrap, key string) error {
		value, err := r.header(ctx, tx, p.workspace, kind, key, typ, fields)
		if err != nil {
			return err
		}
		if kind == "projects" {
			data.Projects = append(data.Projects, value.Interface().(domain.Project))
		} else {
			data.Initiatives = append(data.Initiatives, value.Interface().(domain.Initiative))
		}
		return nil
	}
	var data domain.Bootstrap
	err := add(&data, strings.TrimSpace(ref))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil {
		if kind == "projects" {
			item := data.Projects[0]
			if item.ID == strings.TrimSpace(ref) && p.projectVisible(item) && (!p.selected() || slices.ContainsFunc(item.TeamIDs, p.teamAllowed)) {
				return p.projectAllowed(item), nil
			}
		} else {
			if err := r.projectReferences(ctx, p.workspace, &data); err != nil {
				return false, err
			}
			item := data.Initiatives[0]
			if item.ID == strings.TrimSpace(ref) && p.initiativeVisible(data, item) && (!p.selected() || slices.ContainsFunc(item.ProjectIDs, func(id string) bool { return p.projectByID(data, id, true) })) {
				return p.initiativeAllowed(data, item), nil
			}
		}
	}
	s := r.owner.store
	expressions := []string{"record_key", s.jsonText("data", "id"), s.jsonText("data", "slugId"), s.jsonText("data", "previousSlugIds")}
	release, err := r.owner.transientRows(ctx, expressions, "workspace_key=? AND field=?", p.workspace, kind)
	if err != nil {
		return false, err
	}
	keys, err := func() ([]string, error) {
		defer release()
		rows, err := s.db.QueryContext(ctx, "SELECT "+strings.Join(expressions, ",")+" FROM workspace_metadata_records WHERE workspace_key=? AND field=? ORDER BY collection_order,record_key", p.workspace, kind)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var keys []string
		for rows.Next() {
			var key string
			var id, slug, previous sql.NullString
			if err := rows.Scan(&key, &id, &slug, &previous); err != nil {
				return nil, err
			}
			var old []string
			if previous.Valid {
				if err := json.Unmarshal([]byte(previous.String), &old); err != nil {
					return nil, err
				}
			}
			if domain.MatchSlugRef(ref, id.String, slug.String, old) != domain.SlugNoMatch {
				if err := r.owner.reserve(ctx, uint64(len(key))*8+512); err != nil {
					return nil, err
				}
				keys = append(keys, key)
			}
		}
		return keys, rows.Err()
	}()
	if err != nil {
		return false, err
	}
	data = domain.Bootstrap{}
	for _, key := range keys {
		if err := add(&data, key); err != nil {
			return false, err
		}
	}
	if err := r.parentReferences(ctx, tx, p, &data); err != nil {
		return false, err
	}
	if err := r.projectReferences(ctx, p.workspace, &data); err != nil {
		return false, err
	}
	if kind == "projects" {
		index := resourceSlugIndex(data.Projects, ref,
			func(item domain.Project) (string, string, []string) {
				return item.ID, item.SlugID, item.PreviousSlugIDs
			}, p.projectVisible)
		return index >= 0 && p.projectAllowed(data.Projects[index]), nil
	}
	index := resourceSlugIndex(data.Initiatives, ref,
		func(item domain.Initiative) (string, string, []string) {
			return item.ID, item.SlugID, item.PreviousSlugIDs
		},
		func(item domain.Initiative) bool { return p.initiativeVisible(data, item) })
	return index >= 0 && p.initiativeAllowed(data, data.Initiatives[index]), nil
}

func (r *ResourceAuthorizationRead) projectReferences(ctx context.Context, workspace string, data *domain.Bootstrap) error {
	seen := map[string]bool{}
	for _, project := range data.Projects {
		seen[project.ID] = true
	}
	var ids []string
	add := func(id string) error {
		if !seen[id] {
			if err := r.owner.reserve(ctx, uint64(len(id))*8+512); err != nil {
				return err
			}
			seen[id] = true
			ids = append(ids, id)
		}
		return nil
	}
	for _, project := range data.Projects {
		for _, id := range project.DependencyIDs {
			if err := add(id); err != nil {
				return err
			}
		}
	}
	for _, item := range data.Initiatives {
		for _, id := range item.ProjectIDs {
			if err := add(id); err != nil {
				return err
			}
		}
	}
	s := r.owner.store
	for start := 0; start < len(ids); start += MetadataRecordBatchMaxKeys {
		batch := ids[start:min(start+MetadataRecordBatchMaxKeys, len(ids))]
		predicates, args := []string{}, []any{workspace}
		for _, id := range batch {
			predicates = append(predicates, s.metadataRecordKeyEquals("record_key", "?"))
			args = append(args, id)
		}
		where := "workspace_key=? AND field='projects' AND (" + strings.Join(predicates, " OR ") + ")"
		expressions := []string{"record_key", s.jsonText("data", "teamIds")}
		release, err := r.owner.transientRows(ctx, expressions, where, args...)
		if err != nil {
			return err
		}
		err = func() error {
			defer release()
			rows, err := s.db.QueryContext(ctx, "SELECT "+strings.Join(expressions, ",")+" FROM workspace_metadata_records WHERE "+where, args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id string
				var raw sql.NullString
				if err := rows.Scan(&id, &raw); err != nil {
					return err
				}
				if err := r.owner.reserve(ctx, uint64(len(id))*8+uint64(len(raw.String))*32+4096); err != nil {
					return err
				}
				item := domain.Project{ID: id}
				if raw.Valid {
					if err := json.Unmarshal([]byte(raw.String), &item.TeamIDs); err != nil {
						return err
					}
				}
				data.Projects = append(data.Projects, item)
			}
			return rows.Err()
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

// The canonical reference loader gives a referenced parent's physical key its
// identity. Preserve that behavior even when physical and JSON IDs differ.
func (r *ResourceAuthorizationRead) parentReferences(ctx context.Context, tx *sqlTx, p *resourceAccessPolicy, data *domain.Bootstrap) error {
	seen := map[string]bool{}
	for _, item := range data.Initiatives {
		seen[item.ID] = true
	}
	var ids []string
	add := func(id string) error {
		if !seen[id] {
			if err := r.owner.reserve(ctx, uint64(len(id))*8+512); err != nil {
				return err
			}
			seen[id] = true
			ids = append(ids, id)
		}
		return nil
	}
	for _, project := range data.Projects {
		if p.selected() || p.key != nil && p.key.Actor == "app" {
			for _, id := range project.Initiatives {
				if err := add(id); err != nil {
					return err
				}
			}
		}
	}
	s := r.owner.store
	for _, item := range data.Initiatives {
		for _, id := range item.ParentInitiativeIDs {
			if err := add(id); err != nil {
				return err
			}
		}
		where := "workspace_key=? AND field='initiativeRelations' AND " + s.jsonText("data", "initiativeId") + "=?"
		release, err := r.owner.transientRows(ctx, []string{"record_key"}, where, p.workspace, item.ID)
		if err != nil {
			return err
		}
		keys, err := func() ([]string, error) {
			defer release()
			rows, err := s.db.QueryContext(ctx, "SELECT record_key FROM workspace_metadata_records WHERE "+where+" ORDER BY collection_order,record_key", p.workspace, item.ID)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			var keys []string
			for rows.Next() {
				var key string
				if err := rows.Scan(&key); err != nil {
					return nil, err
				}
				if err := r.owner.reserve(ctx, uint64(len(key))*8+512); err != nil {
					return nil, err
				}
				keys = append(keys, key)
			}
			return keys, rows.Err()
		}()
		if err != nil {
			return err
		}
		for _, key := range keys {
			value, err := r.header(ctx, tx, p.workspace, "initiativeRelations", key, reflect.TypeFor[domain.InitiativeRelation](), []string{"Type", "RelatedInitiativeID"})
			if err != nil {
				return err
			}
			relation := value.Interface().(domain.InitiativeRelation)
			if relation.Type == "parent" {
				if err := add(relation.RelatedInitiativeID); err != nil {
					return err
				}
			}
		}
	}
	for _, id := range ids {
		var lead, contributing, linked sql.NullString
		expressions := []string{s.jsonText("data", "leadTeamId"), s.jsonText("data", "contributingTeamIds"), s.jsonText("data", "projectIds")}
		where := "workspace_key=? AND field='initiatives' AND " + s.metadataRecordKeyEquals("record_key", "?")
		release, err := r.owner.transientRows(ctx, expressions, where, p.workspace, id)
		if err != nil {
			return err
		}
		err = s.db.QueryRowContext(ctx, "SELECT "+strings.Join(expressions, ",")+" FROM workspace_metadata_records WHERE "+where, p.workspace, id).Scan(&lead, &contributing, &linked)
		if err != nil {
			release()
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		if err := r.owner.reserve(ctx, uint64(len(id)+len(lead.String)+len(contributing.String)+len(linked.String))*32+4096); err != nil {
			release()
			return err
		}
		item := domain.Initiative{ID: id, LeadTeamID: lead.String}
		if contributing.Valid {
			err = json.Unmarshal([]byte(contributing.String), &item.ContributingTeamIDs)
		}
		if err == nil && linked.Valid {
			err = json.Unmarshal([]byte(linked.String), &item.ProjectIDs)
		}
		release()
		if err != nil {
			return err
		}
		data.Initiatives = append(data.Initiatives, item)
	}
	return nil
}
