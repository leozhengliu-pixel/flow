package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

// ResourceRead owns HTTP detail values and their processing/encoding allowance.
// Data preserves JSON, not native timestamp identity, and must not become a
// mutation input or escape this lifetime, including while encoding a response.
type ResourceRead struct {
	Data    domain.Bootstrap
	Allowed bool
	mu      sync.Mutex
	closed  bool
	store   *SQLiteStore
	source  *metadataLogicalSource
	release []func()
	buffer  [4096]byte
}

type resourceOwnedReadKey struct{}

func (r *ResourceRead) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	r.Data, r.Allowed = domain.Bootstrap{}, false
	if r.source != nil {
		r.source.Release()
		r.source = nil
	}
	for _, release := range r.release {
		release()
	}
	r.release = nil
}

func (r *ResourceRead) reserve(ctx context.Context, size uint64) error {
	if size > metadataWorkBytes {
		return ErrMetadataWorkBudget
	}
	_, release, err := r.store.tryAdmitAdditionalMetadataWork(ctx, int(size))
	if err == nil {
		r.release = append(r.release, release)
	}
	return err
}

// BeginResourceRead preserves ResourceReadMetadata's certified installed-copy
// choice, canonical fallback, projection and trailing live authority check.
func (s *SQLiteStore) BeginResourceRead(ctx context.Context, access AccessContext, kind, ref string) (_ *ResourceRead, err error) {
	if !s.BoundedMetadataEnabled() {
		data, allowed, err := s.ResourceReadMetadata(ctx, access, kind, ref)
		if err != nil {
			return nil, err
		}
		return &ResourceRead{Data: data, Allowed: allowed}, nil
	}
	if err := access.checkBinding(s); err != nil {
		return nil, err
	}
	if kind != "projects" && kind != "initiatives" {
		return nil, sql.ErrNoRows
	}
	p := access.policy
	existing, borrowed := ctx.Value(workspaceReadKey{}).(workspaceRead)
	borrowed = borrowed && existing.database == s.db
	if borrowed {
		if existing.workspace != p.workspace || existing.reader == nil {
			return nil, ErrMetadataRecordQuery
		}
		// A caller already holds SQL. Preserve the legacy live-authority
		// boundary before taking any store lock or reading target bodies.
		if s.db.journalReady.Load() && s.db.Stats().MaxOpenConnections == 1 {
			return nil, ErrRecordJournalUnavailable
		}
		stamp, err := readMetadataStamp(ctx, s.db, p.workspace)
		if err != nil {
			return nil, err
		}
		if stamp != p.stamp {
			return nil, errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
		}
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	read := &ResourceRead{store: s}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err := read.reserve(ctx, 2<<20); err != nil {
		return nil, err
	}
	current := false
	if !borrowed {
		s.mu.RLock()
		_, found := s.workspaces[p.workspace]
		s.metadataResidency.mu.Lock()
		source := s.metadataResidency.logical[p.workspace]
		if !found {
			source = nil
		}
		current = found && source != nil && source.Stamp() == p.stamp
		if source != nil {
			err = source.Retain()
		}
		// Caller-owned SQL snapshots use their pre-SQL compatibility capture
		// or canonical rows, never a live store-mutex lookup.
		s.metadataResidency.mu.Unlock()
		s.mu.RUnlock()
		if err != nil {
			return nil, err
		}
		read.source = source
	}
	var data domain.Bootstrap
	if current {
		data, err = read.installed(ctx, p, kind, ref)
	} else {
		tx := existing.reader
		snapshot := ctx
		if !borrowed {
			tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
			if err != nil {
				return nil, err
			}
			snapshot = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: p.workspace, reader: tx})
		}
		snapshot = context.WithValue(snapshot, resourceOwnedReadKey{}, read)
		data, err = s.resourceReadRecords(snapshot, p, kind, ref)
		if !borrowed {
			closeErr := tx.Rollback()
			if err == nil {
				err = closeErr
			}
		}
	}
	if err == nil {
		read.Data, read.Allowed, err = resourceReadMetadata(p, data, kind, ref)
	}
	if err == nil && len(read.Data.Initiatives) == 1 && !read.Data.Initiatives[0].Favorite {
		var favorite int
		err = s.db.QueryRowContext(ctx, `SELECT CASE WHEN EXISTS (
			SELECT 1 FROM workspace_metadata_records WHERE workspace_key=? AND field='favorites' AND `+s.jsonText("data", "userId")+`=? AND `+s.jsonText("data", "resourceType")+`='initiative' AND `+s.jsonText("data", "resourceId")+`=?
		) OR EXISTS (
			SELECT 1 FROM team_default_favorites d JOIN team_memberships tm ON tm.team_id=d.team_id AND tm.workspace_id=? AND tm.user_id=?
			WHERE d.workspace_key=? AND d.resource_type='initiative' AND d.resource_id=? AND NOT EXISTS (
			SELECT 1 FROM team_default_favorite_overrides o WHERE o.workspace_key=d.workspace_key AND o.user_id=tm.user_id AND o.resource_type=d.resource_type AND o.resource_id=d.resource_id AND o.hidden=1)
		) THEN 1 ELSE 0 END`, p.workspace, access.PrincipalID, read.Data.Initiatives[0].ID, access.WorkspaceID, access.PrincipalID, p.workspace, read.Data.Initiatives[0].ID).Scan(&favorite)
		read.Data.Initiatives[0].Favorite = favorite == 1
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

func (r *ResourceRead) projectNode(ctx context.Context, node metadataLogicalNode, names ...string) (reflect.Value, error) {
	cost := uint64(2048)
	err := node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if slices.Contains(names, key.String()) {
			if child.cost > (metadataWorkBytes-cost)/8 {
				return ErrMetadataWorkBudget
			}
			cost += child.cost * 8
		}
		return nil
	})
	if err != nil {
		return reflect.Value{}, err
	}
	if err := r.reserve(ctx, cost); err != nil {
		return reflect.Value{}, err
	}
	value := reflect.New(node.typ).Elem()
	err = node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if !slices.Contains(names, key.String()) {
			return nil
		}
		field, err := child.Value(ctx, int(cost))
		if err == nil {
			value.FieldByName(key.String()).Set(field)
		}
		return err
	})
	return value, err
}

func (r *ResourceRead) withHeader(ctx context.Context, node metadataLogicalNode, names []string, visit func(reflect.Value) error) error {
	scratch := &ResourceRead{store: r.store}
	defer scratch.Close()
	value, err := scratch.projectNode(ctx, node, names...)
	if err != nil {
		return err
	}
	return visit(value)
}

func (r *ResourceRead) node(ctx context.Context, node metadataLogicalNode, wire bool) (reflect.Value, error) {
	if node.cost > (metadataWorkBytes-(128<<10))/8 {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	if err := r.reserve(ctx, node.cost*8+128<<10); err != nil {
		return reflect.Value{}, err
	}
	if wire {
		return node.WireValue(ctx, int(node.cost))
	}
	return node.Value(ctx, int(node.cost))
}

var resourceProjectHeader = []string{"ID", "SlugID", "PreviousSlugIDs", "TeamIDs"}
var resourceInitiativeHeader = []string{"ID", "SlugID", "PreviousSlugIDs", "LeadTeamID", "ContributingTeamIDs", "ProjectIDs"}

func (r *ResourceRead) visitNative(ctx context.Context, kind string, visit func(int, metadataLogicalNode) error) error {
	field, _, _ := metadataLogicalField(kind)
	err := r.nativeArray(ctx, r.source.fields[field], visit)
	if errors.Is(err, ErrMetadataIterationDone) {
		return nil
	}
	return err
}

func (r *ResourceRead) nativeUint(cursor *metadataLogicalCursor) (uint64, error) {
	err := cursor.read(r.buffer[:8])
	return binary.LittleEndian.Uint64(r.buffer[:8]), err
}

// Match the logical codec's frame validation while reusing this request's
// parser buffer; an unmatched row allocates neither headers nor frame bytes.
func (r *ResourceRead) nativeChild(cursor *metadataLogicalCursor, typ reflect.Type) (metadataLogicalNode, error) {
	start := cursor.offset
	if err := cursor.read(r.buffer[:16]); err != nil {
		return metadataLogicalNode{}, err
	}
	size, cost := binary.LittleEndian.Uint64(r.buffer[:8]), binary.LittleEndian.Uint64(r.buffer[8:16])
	if size < 16 || size > uint64(cursor.node.offset+cursor.node.size-start) {
		return metadataLogicalNode{}, ErrMetadataRecordQuery
	}
	cursor.offset = start + int64(size)
	return metadataLogicalNode{source: cursor.node.source, offset: start, size: int64(size), cost: cost & ((uint64(1) << 62) - 1), typ: typ, unavailable: cost&(uint64(1)<<63) != 0, blocked: cost&(uint64(1)<<62) != 0}, nil
}

func (r *ResourceRead) nativeArray(ctx context.Context, node metadataLogicalNode, visit func(int, metadataLogicalNode) error) error {
	if node.blocked || node.typ.Kind() != reflect.Slice {
		return ErrBoundedMetadataCoverage
	}
	cursor := metadataLogicalCursor{node, node.offset + 16, ctx}
	err := cursor.read(r.buffer[:1])
	if err != nil || r.buffer[0] == 0 {
		return err
	}
	count, err := r.nativeUint(&cursor)
	if err != nil || count > node.cost {
		return ErrMetadataRecordQuery
	}
	for index := uint64(0); index < count; index++ {
		child, err := r.nativeChild(&cursor, node.typ.Elem())
		if err != nil {
			return err
		}
		if err := visit(int(index), child); err != nil {
			return err
		}
	}
	return nil
}

func (r *ResourceRead) nativeEqual(ctx context.Context, node metadataLogicalNode, want string) (bool, error) {
	if node.blocked || node.typ.Kind() != reflect.String {
		return false, ErrBoundedMetadataCoverage
	}
	cursor := metadataLogicalCursor{node, node.offset + 16, ctx}
	length, err := r.nativeUint(&cursor)
	if err != nil || length != uint64(len(want)) {
		return false, err
	}
	for offset := 0; offset < len(want); offset += len(r.buffer) {
		size := min(len(r.buffer), len(want)-offset)
		if err := cursor.read(r.buffer[:size]); err != nil {
			return false, err
		}
		if string(r.buffer[:size]) != want[offset:offset+size] {
			return false, nil
		}
	}
	return true, nil
}

func (r *ResourceRead) nativeID(ctx context.Context, node metadataLogicalNode, id string) (bool, error) {
	if node.blocked || node.typ != reflect.TypeFor[domain.Project]() && node.typ != reflect.TypeFor[domain.Initiative]() && node.typ != reflect.TypeFor[domain.IssueLabel]() {
		return false, ErrBoundedMetadataCoverage
	}
	cursor := metadataLogicalCursor{node, node.offset + 16, ctx}
	child, err := r.nativeChild(&cursor, reflect.TypeFor[string]())
	if err != nil {
		return false, err
	}
	return r.nativeEqual(ctx, child, id)
}

// Ranking compares ordinary exact/previous references without decoding their
// strings. Only a suffix lookup needs a transient current-slug value.
func (r *ResourceRead) nativeRank(ctx context.Context, node metadataLogicalNode, ref string) (domain.SlugMatch, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return domain.SlugNoMatch, nil
	}
	rank := domain.SlugNoMatch
	suffix := domain.SlugSuffix(ref)
	if node.blocked || node.typ != reflect.TypeFor[domain.Project]() && node.typ != reflect.TypeFor[domain.Initiative]() {
		return rank, ErrBoundedMetadataCoverage
	}
	// Both record types begin with ID, Name, SlugID, PreviousSlugIDs.
	cursor := metadataLogicalCursor{node, node.offset + 16, ctx}
	for index, typ := range []reflect.Type{reflect.TypeFor[string](), reflect.TypeFor[string](), reflect.TypeFor[string](), reflect.TypeFor[[]string]()} {
		child, err := r.nativeChild(&cursor, typ)
		if err != nil {
			return rank, err
		}
		switch index {
		case 0, 2:
			equal, err := r.nativeEqual(ctx, child, ref)
			if err != nil {
				return rank, err
			}
			if equal {
				rank = domain.SlugMatchExact
			} else if index == 2 && suffix != "" && rank < domain.SlugMatchSuffix {
				scratch := &ResourceRead{store: r.store}
				defer scratch.Close()
				value, err := scratch.node(ctx, child, false)
				if err != nil {
					return rank, err
				}
				if domain.SlugSuffix(value.String()) == suffix {
					rank = domain.SlugMatchSuffix
				}
			}
		case 3:
			if rank >= domain.SlugMatchPrevious {
				continue
			}
			err := r.nativeArray(ctx, child, func(_ int, previous metadataLogicalNode) error {
				equal, err := r.nativeEqual(ctx, previous, ref)
				if equal {
					rank = max(rank, domain.SlugMatchPrevious)
				}
				return err
			})
			if err != nil {
				return rank, err
			}
		}
	}
	return rank, nil
}

func (r *ResourceRead) nativeProjectChoice(ctx context.Context, p *resourceAccessPolicy, id string) (metadataLogicalNode, int, error) {
	var best metadataLogicalNode
	score := 0
	err := r.visitNative(ctx, "projects", func(_ int, node metadataLogicalNode) error {
		matched, err := r.nativeID(ctx, node, id)
		if err != nil || !matched {
			return err
		}
		return r.withHeader(ctx, node, []string{"TeamIDs"}, func(value reflect.Value) error {
			item := value.Interface().(domain.Project)
			flags := 0
			if p.projectVisible(item) {
				flags = 1
			}
			if p.projectAllowed(item) {
				flags |= 2
				if !p.selected() || slices.ContainsFunc(item.TeamIDs, p.teamAllowed) {
					flags |= 4
				}
			}
			if flags > score {
				best, score = node, flags
			}
			if score == 7 {
				return ErrMetadataIterationDone
			}
			return nil
		})
	})
	return best, score, err
}

func (r *ResourceRead) nativeInitiativeFlags(ctx context.Context, p *resourceAccessPolicy, item domain.Initiative) (visible, allowed, readable bool, err error) {
	if p.metadata.ViewerRole == "guest" || item.LeadTeamID != "" && !p.teams.Content[item.LeadTeamID] {
		return false, false, false, nil
	}
	linkedVisible, linkedAllowed, linkedCredential := false, false, false
	for _, id := range item.ProjectIDs {
		_, score, err := r.nativeProjectChoice(ctx, p, id)
		if err != nil {
			return false, false, false, err
		}
		linkedVisible = linkedVisible || score > 0
		linkedAllowed = linkedAllowed || score&2 != 0
		linkedCredential = linkedCredential || score&4 != 0
	}
	contributing := slices.ContainsFunc(item.ContributingTeamIDs, func(id string) bool { return p.teams.Content[id] })
	visible = item.LeadTeamID != "" || len(item.ContributingTeamIDs) == 0 && len(item.ProjectIDs) == 0 || contributing || linkedVisible
	if item.LeadTeamID != "" {
		allowed = p.teamAllowed(item.LeadTeamID)
	} else if !contributing && !linkedVisible {
		allowed = p.metadata.ViewerRole != "guest"
	} else {
		allowed = slices.ContainsFunc(item.ContributingTeamIDs, p.teamAllowed) || linkedAllowed
	}
	readable = visible && allowed && (!p.selected() || linkedCredential)
	if p.key != nil && p.key.Actor == "app" && !resourceKeyHasScope(*p.key, "initiative:read") && !resourceKeyHasScope(*p.key, "initiative:write") {
		readable = false
	}
	return visible, allowed, readable, nil
}

type resourceNativeChoice struct {
	node    metadataLogicalNode
	ordinal int
	rank    domain.SlugMatch
	allowed bool
}

func (r *ResourceRead) installed(ctx context.Context, p *resourceAccessPolicy, kind, ref string) (domain.Bootstrap, error) {
	var data domain.Bootstrap
	first, selected := resourceNativeChoice{}, resourceNativeChoice{}
	names := resourceProjectHeader
	if kind == "initiatives" {
		names = resourceInitiativeHeader
	}
	err := r.visitNative(ctx, kind, func(ordinal int, node metadataLogicalNode) error {
		rank, err := r.nativeRank(ctx, node, ref)
		if err != nil || rank <= first.rank && (!p.selected() || rank <= selected.rank) {
			return err
		}
		return r.withHeader(ctx, node, names, func(value reflect.Value) error {
			visible, allowed, credential := false, false, false
			if kind == "projects" {
				item := value.Interface().(domain.Project)
				visible, allowed = p.projectVisible(item), p.projectAllowed(item)
				credential = visible && slices.ContainsFunc(item.TeamIDs, p.teamAllowed)
			} else {
				item := value.Interface().(domain.Initiative)
				var err error
				visible, allowed, _, err = r.nativeInitiativeFlags(ctx, p, item)
				if err != nil {
					return err
				}
				for _, id := range item.ProjectIDs {
					_, score, err := r.nativeProjectChoice(ctx, p, id)
					if err != nil {
						return err
					}
					credential = credential || score&4 != 0
				}
				credential = visible && credential
			}
			if visible && rank > first.rank {
				first = resourceNativeChoice{node, ordinal, rank, allowed}
			}
			if credential && rank > selected.rank {
				selected = resourceNativeChoice{node, ordinal, rank, allowed}
			}
			if first.rank == domain.SlugMatchExact && (!first.allowed || !p.selected() || selected.rank == domain.SlugMatchExact) {
				return ErrMetadataIterationDone
			}
			return nil
		})
	})
	if err != nil || first.rank == domain.SlugNoMatch {
		return data, err
	}
	chosen := first
	if p.selected() && first.allowed {
		chosen = selected
	}
	choices := []resourceNativeChoice{first}
	if chosen.rank != domain.SlugNoMatch && chosen.ordinal != first.ordinal {
		choices = append(choices, chosen)
		slices.SortFunc(choices, func(a, b resourceNativeChoice) int { return a.ordinal - b.ordinal })
	}
	field, _, _ := metadataLogicalField(kind)
	for _, choice := range choices {
		var value reflect.Value
		full := first.allowed && chosen.rank != domain.SlugNoMatch && choice.ordinal == chosen.ordinal
		if kind == "initiatives" && p.key != nil && p.key.Actor == "app" && !resourceKeyHasScope(*p.key, "initiative:read") && !resourceKeyHasScope(*p.key, "initiative:write") {
			full = false
		}
		if full {
			value, err = r.node(ctx, choice.node, true)
		} else {
			value, err = r.projectNode(ctx, choice.node, names...)
		}
		if err != nil {
			return data, err
		}
		target := reflect.ValueOf(&data).Elem().Field(field)
		target.Set(reflect.Append(target, value))
	}
	err = r.nativeReferences(ctx, p, &data)
	return data, err
}

func (r *ResourceRead) nativeReferences(ctx context.Context, p *resourceAccessPolicy, data *domain.Bootstrap) error {
	projects, parents, labels := []string{}, []string{}, []string{}
	for _, item := range data.Projects {
		projects = append(projects, item.DependencyIDs...)
		labels = append(labels, item.LabelIDs...)
		if p.selected() || p.key != nil && p.key.Actor == "app" {
			parents = append(parents, item.Initiatives...)
		}
	}
	for index := range data.Initiatives {
		item := &data.Initiatives[index]
		projects = append(projects, item.ProjectIDs...)
		labels = append(labels, item.LabelIDs...)
		if err := r.visitNative(ctx, "initiativeRelations", func(_ int, node metadataLogicalNode) error {
			return r.withHeader(ctx, node, []string{"Type", "InitiativeID", "RelatedInitiativeID"}, func(value reflect.Value) error {
				relation := value.Interface().(domain.InitiativeRelation)
				if relation.Type == "parent" && relation.InitiativeID == item.ID && !slices.Contains(item.ParentInitiativeIDs, relation.RelatedInitiativeID) {
					if err := r.reserve(ctx, uint64(len(relation.RelatedInitiativeID))*16+2048); err != nil {
						return err
					}
					item.ParentInitiativeIDs = append(item.ParentInitiativeIDs, strings.Clone(relation.RelatedInitiativeID))
				}
				return nil
			})
		}); err != nil {
			return err
		}
		parents = append(parents, item.ParentInitiativeIDs...)
		if err := r.visitNative(ctx, "favorites", func(_ int, node metadataLogicalNode) error {
			return r.withHeader(ctx, node, []string{"UserID", "ResourceType", "ResourceID"}, func(value reflect.Value) error {
				favorite := value.Interface().(domain.Favorite)
				if favorite.UserID == p.principal && favorite.ResourceType == "initiative" && favorite.ResourceID == item.ID {
					data.Favorites = append(data.Favorites, domain.Favorite{UserID: p.principal, ResourceType: "initiative", ResourceID: item.ID})
					return ErrMetadataIterationDone
				}
				return nil
			})
		}); err != nil {
			return err
		}
	}
	seenParents := map[string]bool{}
	for _, id := range parents {
		if seenParents[id] {
			continue
		}
		seenParents[id] = true
		var best metadataLogicalNode
		score := 0
		if err := r.visitNative(ctx, "initiatives", func(_ int, node metadataLogicalNode) error {
			matched, err := r.nativeID(ctx, node, id)
			if err != nil || !matched {
				return err
			}
			return r.withHeader(ctx, node, resourceInitiativeHeader, func(value reflect.Value) error {
				visible, _, readable, err := r.nativeInitiativeFlags(ctx, p, value.Interface().(domain.Initiative))
				if visible && score == 0 {
					best, score = node, 1
				}
				if readable {
					best, score = node, 2
					return ErrMetadataIterationDone
				}
				return err
			})
		}); err != nil {
			return err
		}
		if score > 0 {
			value, err := r.projectNode(ctx, best, "ID", "LeadTeamID", "ContributingTeamIDs", "ProjectIDs")
			if err != nil {
				return err
			}
			item := value.Interface().(domain.Initiative)
			projects = append(projects, item.ProjectIDs...)
			data.Initiatives = append(data.Initiatives, item)
		}
	}
	seenProjects := map[string]bool{}
	for _, id := range projects {
		if seenProjects[id] || p.projectByID(*data, id, true) {
			continue
		}
		seenProjects[id] = true
		best, score, err := r.nativeProjectChoice(ctx, p, id)
		if err != nil {
			return err
		}
		if score > 0 {
			value, err := r.projectNode(ctx, best, "ID", "TeamIDs")
			if err != nil {
				return err
			}
			data.Projects = append(data.Projects, value.Interface().(domain.Project))
		}
	}
	if p.selected() {
		seen := map[string]bool{}
		for _, id := range labels {
			if seen[id] {
				continue
			}
			seen[id] = true
			if err := r.visitNative(ctx, "labels", func(_ int, node metadataLogicalNode) error {
				matched, err := r.nativeID(ctx, node, id)
				if err != nil || !matched {
					return err
				}
				return r.withHeader(ctx, node, []string{"Scope"}, func(value reflect.Value) error {
					scope := value.Interface().(domain.IssueLabel).Scope
					normal := strings.ToLower(strings.TrimSpace(scope))
					if normal == "" || normal == "workspace" || p.teamAllowed(scope) {
						if err := r.reserve(ctx, uint64(len(scope))*16+2048); err != nil {
							return err
						}
						data.Labels = append(data.Labels, domain.IssueLabel{ID: id, Scope: strings.Clone(scope)})
						return ErrMetadataIterationDone
					}
					return nil
				})
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *ResourceRead) byteLength(expr string) string {
	if r.store.dialect == "sqlite" {
		return "LENGTH(CAST(" + expr + " AS BLOB))"
	}
	return "OCTET_LENGTH(" + expr + ")"
}

// Each preflight shares the extraction transaction. Retained charges also
// cover decoding, cloning, normalization and conservative JSON encoding growth.
func (r *ResourceRead) preflight(ctx context.Context, expressions []string, where string, args ...any) error {
	length := "0"
	for _, expression := range expressions {
		length += "+COALESCE(" + r.byteLength(expression) + ",0)"
	}
	var count, size int64
	err := r.store.db.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM("+length+"),0) FROM workspace_metadata_records WHERE "+where, args...).Scan(&count, &size)
	if err != nil {
		return err
	}
	if size < 0 || count < 0 || uint64(count) > metadataWorkBytes/4096 || uint64(size) > metadataWorkBytes/32 {
		return ErrMetadataWorkBudget
	}
	return r.reserve(ctx, uint64(size)*32+uint64(count)*4096+4096)
}

func (r *ResourceRead) transientRows(ctx context.Context, expressions []string, where string, args ...any) (func(), error) {
	length := "0"
	for _, expression := range expressions {
		length += "+COALESCE(" + r.byteLength(expression) + ",0)"
	}
	var size int64
	if err := r.store.db.QueryRowContext(ctx, "SELECT COALESCE(MAX("+length+"),0) FROM workspace_metadata_records WHERE "+where, args...).Scan(&size); err != nil {
		return nil, err
	}
	if size < 0 || size > (metadataWorkBytes-(16<<10))/32 {
		return nil, ErrMetadataWorkBudget
	}
	_, release, err := r.store.tryAdmitAdditionalMetadataWork(ctx, int(size)*32+16<<10)
	return release, err
}

func (r *ResourceRead) jsonRecord(ctx context.Context, workspace, kind, id string) ([]byte, error) {
	where := "workspace_key=? AND field=? AND " + r.store.metadataRecordKeyEquals("record_key", "?")
	args := []any{workspace, kind, id}
	if err := r.preflight(ctx, []string{"data"}, where, args...); err != nil {
		return nil, err
	}
	var raw []byte
	err := r.store.db.QueryRowContext(ctx, "SELECT data FROM workspace_metadata_records WHERE "+where, args...).Scan(&raw)
	if err == nil {
		// Compact arrays of empty structs can expand much further than their
		// JSON byte length. Charge structural tokens before typed decoding.
		var work uint64
		work, err = metadataAggregatePayloadJSONWork(ctx, raw)
		if err == nil {
			err = r.reserve(ctx, work)
		}
	}
	return raw, err
}

func (r *ResourceRead) records(ctx context.Context, p *resourceAccessPolicy, kind, ref string) (domain.Bootstrap, error) {
	s := r.store
	data := domain.Bootstrap{}
	add := func(raw []byte) error {
		candidate := domain.Bootstrap{}
		if kind == "projects" {
			var item domain.Project
			if err := json.Unmarshal(raw, &item); err != nil {
				return err
			}
			candidate.Projects = []domain.Project{item}
		} else {
			var item domain.Initiative
			if err := json.Unmarshal(raw, &item); err != nil {
				return err
			}
			candidate.Initiatives = []domain.Initiative{item}
		}
		if err := s.resourceReadCompatibility(ctx, p, &candidate); err != nil {
			return err
		}
		data.Projects = append(data.Projects, candidate.Projects...)
		data.Initiatives = append(data.Initiatives, candidate.Initiatives...)
		return nil
	}
	raw, err := r.jsonRecord(ctx, p.workspace, kind, strings.TrimSpace(ref))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return data, err
	}
	if err == nil {
		if err := add(raw); err != nil {
			return data, err
		}
		if err := r.references(ctx, p, &data); err != nil {
			return data, err
		}
		if kind == "projects" && data.Projects[0].ID == strings.TrimSpace(ref) && p.projectVisible(data.Projects[0]) && (!p.selected() || slices.ContainsFunc(data.Projects[0].TeamIDs, p.teamAllowed)) {
			return data, nil
		}
		if kind == "initiatives" && data.Initiatives[0].ID == strings.TrimSpace(ref) && p.initiativeVisible(data, data.Initiatives[0]) && (!p.selected() || slices.ContainsFunc(data.Initiatives[0].ProjectIDs, func(id string) bool { return p.projectByID(data, id, true) })) {
			return data, nil
		}
	}
	expressions := []string{"record_key", s.jsonText("data", "id"), s.jsonText("data", "slugId"), s.jsonText("data", "previousSlugIds")}
	releaseRows, err := r.transientRows(ctx, expressions, "workspace_key=? AND field=?", p.workspace, kind)
	if err != nil {
		return data, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+strings.Join(expressions, ",")+" FROM workspace_metadata_records WHERE workspace_key=? AND field=? ORDER BY collection_order,record_key", p.workspace, kind)
	if err != nil {
		releaseRows()
		return data, err
	}
	ids := []string{}
	err = func() error {
		defer rows.Close()
		for rows.Next() {
			var key string
			var id, slug, previous sql.NullString
			if err := rows.Scan(&key, &id, &slug, &previous); err != nil {
				return err
			}
			var old []string
			if previous.Valid {
				if err := json.Unmarshal([]byte(previous.String), &old); err != nil {
					return err
				}
			}
			if domain.MatchSlugRef(ref, id.String, slug.String, old) != domain.SlugNoMatch {
				if err := r.reserve(ctx, uint64(len(key))*8+512); err != nil {
					return err
				}
				ids = append(ids, key)
			}
		}
		return rows.Err()
	}()
	releaseRows()
	if err != nil {
		return data, err
	}
	data = domain.Bootstrap{}
	for _, id := range ids {
		raw, err := r.jsonRecord(ctx, p.workspace, kind, id)
		if err != nil {
			return data, err
		}
		if err := add(raw); err != nil {
			return data, err
		}
	}
	err = r.references(ctx, p, &data)
	return data, err
}

func (r *ResourceRead) compatibility(ctx context.Context, p *resourceAccessPolicy, data *domain.Bootstrap) error {
	matchesSource := func(source domain.Bootstrap) (bool, error) {
		compare := func(item, cached any) (bool, error) {
			cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(cached), 0)
			if err != nil {
				return false, err
			}
			if cost > (metadataWorkBytes-(128<<10))/8 {
				return false, ErrMetadataWorkBudget
			}
			scratch := &ResourceRead{store: r.store}
			defer scratch.Close()
			if err := scratch.reserve(ctx, cost*8+128<<10); err != nil {
				return false, err
			}
			left, err := resourceWireDigest(ctx, item)
			if err != nil {
				return false, err
			}
			right, err := resourceWireDigest(ctx, cached)
			return left == right, err
		}
		for _, item := range data.Projects {
			for _, cached := range source.Projects {
				if item.ID == cached.ID {
					if same, err := compare(item, cached); same || err != nil {
						return same, err
					}
				}
			}
		}
		for _, item := range data.Initiatives {
			for _, cached := range source.Initiatives {
				if item.ID == cached.ID {
					if same, err := compare(item, cached); same || err != nil {
						return same, err
					}
				}
			}
		}
		return false, nil
	}
	setViewer := func(viewer domain.User) error {
		cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(viewer), 0)
		if err != nil {
			return err
		}
		if cost > (metadataWorkBytes-(128<<10))/8 {
			return ErrMetadataWorkBudget
		}
		if err := r.reserve(ctx, cost*8+128<<10); err != nil {
			return err
		}
		data.Viewer = cloneBootstrap(domain.Bootstrap{Viewer: viewer}).Viewer
		return nil
	}
	snapshot, pinned := ctx.Value(workspaceReadKey{}).(workspaceRead)
	pinned = pinned && snapshot.database == r.store.db
	captured, capturedOK := ctx.Value(resourceReadCompatibilityKey{}).(resourceReadCompatibilitySnapshot)
	var retry func() (bool, error)
	if pinned && capturedOK && captured.database == r.store.db && captured.workspace == p.workspace &&
		captured.data.Workspace.ID == p.stamp.workspaceID {
		retry = func() (bool, error) {
			if captured.digests != nil {
				return captured.matches(ctx, *data)
			}
			return matchesSource(captured.data)
		}
		same, err := retry()
		if err != nil || same {
			return err
		}
		if err := setViewer(captured.data.Viewer); err != nil {
			return err
		}
	} else if pinned && snapshot.workspace == p.workspace && snapshot.metadata.Workspace.ID == p.stamp.workspaceID {
		if _, scoped := metadataRecordScopeFromContext(ctx); !scoped {
			retry = func() (bool, error) { return matchesSource(snapshot.metadata) }
			if same, err := retry(); same || err != nil {
				return err
			}
		}
		if err := setViewer(snapshot.metadata.Viewer); err != nil {
			return err
		}
	} else if r.source != nil {
		retry = func() (bool, error) {
			for _, kind := range []string{"projects", "initiatives"} {
				same := false
				err := r.visitNative(ctx, kind, func(_ int, node metadataLogicalNode) error {
					var candidate any
					if kind == "projects" {
						for _, item := range data.Projects {
							matched, err := r.nativeID(ctx, node, item.ID)
							if err != nil {
								return err
							}
							if matched {
								candidate = item
							}
						}
					} else {
						for _, item := range data.Initiatives {
							matched, err := r.nativeID(ctx, node, item.ID)
							if err != nil {
								return err
							}
							if matched {
								candidate = item
							}
						}
					}
					if candidate != nil {
						scratch := &ResourceRead{store: r.store}
						defer scratch.Close()
						value, err := scratch.node(ctx, node, true)
						if err != nil {
							return err
						}
						left, err := resourceWireDigest(ctx, candidate)
						if err != nil {
							return err
						}
						right, err := resourceWireDigest(ctx, value.Interface())
						if err != nil {
							return err
						}
						same = same || left == right
						if same {
							return ErrMetadataIterationDone
						}
					}
					return nil
				})
				if errors.Is(err, ErrMetadataIterationDone) {
					err = nil
				}
				if err != nil {
					return false, err
				}
				if same {
					return true, nil
				}
			}
			return false, nil
		}
		if same, err := retry(); same || err != nil {
			return err
		}
		field, _, _ := metadataLogicalField("viewer")
		value, err := r.node(ctx, r.source.fields[field], false)
		if err != nil {
			return err
		}
		data.Viewer = value.Interface().(domain.User)
	} else {
		// A low-level transaction has no installed body witness. Its root
		// viewer supplies legacy defaults independently of the principal.
		s := r.store
		expression := s.jsonText("data", "viewer")
		where := ` FROM workspace_states WHERE workspace_key=? AND workspace_id=?`
		var size int64
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(`+r.byteLength(expression)+`,0)`+where, p.workspace, p.stamp.workspaceID).Scan(&size); err != nil {
			return err
		}
		if size < 0 || size > (metadataWorkBytes-4096)/32 {
			return ErrMetadataWorkBudget
		}
		if err := r.reserve(ctx, uint64(size)*32+4096); err != nil {
			return err
		}
		var raw []byte
		if err := s.db.QueryRowContext(ctx, `SELECT `+expression+where, p.workspace, p.stamp.workspaceID).Scan(&raw); err != nil {
			return err
		}
		if len(raw) > 0 {
			work, err := metadataAggregatePayloadJSONWork(ctx, raw)
			if err != nil {
				return err
			}
			if err := r.reserve(ctx, work); err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &data.Viewer); err != nil {
				return err
			}
		}
	}
	ids := []string{}
	for _, item := range data.Projects {
		if item.Lead != nil {
			ids = append(ids, item.Lead.ID)
		}
		if item.Creator != nil {
			ids = append(ids, item.Creator.ID)
		}
	}
	for _, item := range data.Initiatives {
		ids = append(ids, item.Creator.ID)
		if item.Owner != nil {
			ids = append(ids, item.Owner.ID)
		}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		raw, err := r.jsonRecord(ctx, p.workspace, "users", id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		var user domain.User
		if err := json.Unmarshal(raw, &user); err != nil {
			return err
		}
		data.Users = append(data.Users, user)
	}
	// Display references can advance without rewriting a canonical body.
	// Retry the witness before applying unrelated load-time defaults.
	refreshDisplayReferences(data)
	if retry != nil {
		if same, err := retry(); same || err != nil {
			return err
		}
	}
	for _, item := range data.Projects {
		raw, err := r.jsonRecord(ctx, p.workspace, "projectStatuses", item.Status.ID)
		var ordinal int64
		if errors.Is(err, sql.ErrNoRows) {
			// Keep an absent target in a nonempty catalog distinct from an
			// empty catalog, which receives the canonical legacy defaults.
			s := r.store
			where := ` FROM workspace_metadata_records WHERE workspace_key=? AND field='projectStatuses' ORDER BY collection_order,record_key LIMIT 1`
			var size int64
			err = s.db.QueryRowContext(ctx, `SELECT `+r.byteLength("data")+where, p.workspace).Scan(&size)
			if err == nil {
				if size < 0 || size > (metadataWorkBytes-4096)/32 {
					return ErrMetadataWorkBudget
				}
				if err := r.reserve(ctx, uint64(size)*32+4096); err != nil {
					return err
				}
				err = s.db.QueryRowContext(ctx, `SELECT data`+where, p.workspace).Scan(&raw)
				if err == nil {
					work, workErr := metadataAggregatePayloadJSONWork(ctx, raw)
					if workErr != nil {
						return workErr
					}
					if err := r.reserve(ctx, work); err != nil {
						return err
					}
				}
			}
		} else if err == nil {
			s := r.store
			err = s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM workspace_metadata_records prior_status
				WHERE prior_status.workspace_key=chosen.workspace_key AND prior_status.field=chosen.field
				AND (prior_status.collection_order<chosen.collection_order OR
					(prior_status.collection_order=chosen.collection_order AND prior_status.record_key<chosen.record_key)))
				FROM workspace_metadata_records chosen WHERE chosen.workspace_key=? AND chosen.field='projectStatuses' AND `+
				s.metadataRecordKeyEquals("chosen.record_key", "?"), p.workspace, item.Status.ID).Scan(&ordinal)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			var status domain.ProjectStatus
			if err := json.Unmarshal(raw, &status); err != nil {
				return err
			}
			if status.Position == 0 && ordinal > 0 {
				status.Position = float64(ordinal)
			}
			data.ProjectStatuses = append(data.ProjectStatuses, status)
		}
	}
	normalizeStoredMetadata(data)
	return nil
}

func (r *ResourceRead) references(ctx context.Context, p *resourceAccessPolicy, data *domain.Bootstrap) error {
	s := r.store
	projects, initiatives := map[string]bool{}, map[string]bool{}
	projectIDs, parentIDs, labelIDs := []string{}, []string{}, []string{}
	for _, item := range data.Projects {
		projects[item.ID] = true
		projectIDs = append(projectIDs, item.DependencyIDs...)
		labelIDs = append(labelIDs, item.LabelIDs...)
		if p.selected() || p.key != nil && p.key.Actor == "app" {
			parentIDs = append(parentIDs, item.Initiatives...)
		}
	}
	for _, item := range data.Initiatives {
		initiatives[item.ID] = true
		projectIDs = append(projectIDs, item.ProjectIDs...)
		labelIDs = append(labelIDs, item.LabelIDs...)
		parentIDs = append(parentIDs, item.ParentInitiativeIDs...)
		where := "workspace_key=? AND field='initiativeRelations' AND " + s.jsonText("data", "initiativeId") + "=?"
		if err := r.preflight(ctx, []string{"data"}, where, p.workspace, item.ID); err != nil {
			return err
		}
		rows, err := s.db.QueryContext(ctx, "SELECT data FROM workspace_metadata_records WHERE "+where+" ORDER BY collection_order,record_key", p.workspace, item.ID)
		if err != nil {
			return err
		}
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var raw []byte
				var relation domain.InitiativeRelation
				if err := rows.Scan(&raw); err != nil {
					return err
				}
				if err := json.Unmarshal(raw, &relation); err != nil {
					return err
				}
				data.InitiativeRelations = append(data.InitiativeRelations, relation)
				if relation.Type == "parent" {
					parentIDs = append(parentIDs, relation.RelatedInitiativeID)
				}
			}
			return rows.Err()
		}()
		if err != nil {
			return err
		}
	}
	point := func(kind, id string, expressions []string, destinations ...any) error {
		where := "workspace_key=? AND field=? AND " + s.metadataRecordKeyEquals("record_key", "?")
		if err := r.preflight(ctx, expressions, where, p.workspace, kind, id); err != nil {
			return err
		}
		return s.db.QueryRowContext(ctx, "SELECT "+strings.Join(expressions, ",")+" FROM workspace_metadata_records WHERE "+where, p.workspace, kind, id).Scan(destinations...)
	}
	for _, id := range parentIDs {
		if initiatives[id] {
			continue
		}
		initiatives[id] = true
		var lead, contributing, linked sql.NullString
		err := point("initiatives", id, []string{s.jsonText("data", "leadTeamId"), s.jsonText("data", "contributingTeamIds"), s.jsonText("data", "projectIds")}, &lead, &contributing, &linked)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		item := domain.Initiative{ID: id, LeadTeamID: lead.String}
		if contributing.Valid {
			if err := json.Unmarshal([]byte(contributing.String), &item.ContributingTeamIDs); err != nil {
				return err
			}
		}
		if linked.Valid {
			if err := json.Unmarshal([]byte(linked.String), &item.ProjectIDs); err != nil {
				return err
			}
		}
		projectIDs = append(projectIDs, item.ProjectIDs...)
		data.Initiatives = append(data.Initiatives, item)
	}
	if p.selected() {
		for _, id := range labelIDs {
			var scope sql.NullString
			err := point("labels", id, []string{s.jsonText("data", "scope")}, &scope)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			data.Labels = append(data.Labels, domain.IssueLabel{ID: id, Scope: scope.String})
		}
	}
	for _, id := range projectIDs {
		if projects[id] {
			continue
		}
		projects[id] = true
		var teams sql.NullString
		err := point("projects", id, []string{s.jsonText("data", "teamIds")}, &teams)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		item := domain.Project{ID: id}
		if teams.Valid {
			if err := json.Unmarshal([]byte(teams.String), &item.TeamIDs); err != nil {
				return err
			}
		}
		data.Projects = append(data.Projects, item)
	}
	return nil
}
