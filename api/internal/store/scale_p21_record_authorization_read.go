package store

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// recordAuthorizationKind is the header of one metadata record kind that the
// authorization middleware's literal per-record predicates read.
type recordAuthorizationKind struct {
	typ    reflect.Type
	fields []string
	// match lists the members compared with the reference: "id", "slugId",
	// or "slugRef" (domain.MatchSlugRef over ID, SlugID, PreviousSlugIDs).
	match []string
	// field is the stored collection when it differs from the kind name.
	field string
	// assign appends one decoded header to its Bootstrap collection.
	assign func(*domain.Bootstrap, reflect.Value)
}

var recordAuthorizationKinds = map[string]recordAuthorizationKind{
	"cycles": {
		typ: reflect.TypeFor[domain.Cycle](), fields: []string{"ID", "TeamID"}, match: []string{"id"},
		assign: func(data *domain.Bootstrap, value reflect.Value) {
			data.Cycles = append(data.Cycles, value.Interface().(domain.Cycle))
		},
	},
	"savedViews": {
		typ: reflect.TypeFor[domain.SavedView](), fields: []string{"ID", "SlugID", "Scope", "TeamID", "OwnerID", "ProjectID"}, match: []string{"id", "slugId"},
		assign: func(data *domain.Bootstrap, value reflect.Value) {
			data.SavedViews = append(data.SavedViews, value.Interface().(domain.SavedView))
		},
	},
	// Exact-ID project headers: the literal projection's visibleProjects.
	"projects": {
		typ: reflect.TypeFor[domain.Project](), fields: []string{"ID", "TeamIDs"}, match: []string{"id"},
		assign: func(data *domain.Bootstrap, value reflect.Value) {
			data.Projects = append(data.Projects, value.Interface().(domain.Project))
		},
	},
	"reviews": {
		typ: reflect.TypeFor[domain.CodeReview](), fields: []string{"ID", "IssueIDs"}, match: []string{"id"},
		assign: func(data *domain.Bootstrap, value reflect.Value) {
			data.Reviews = append(data.Reviews, value.Interface().(domain.CodeReview))
		},
	},
	"releases": {
		typ: reflect.TypeFor[domain.Release](), fields: []string{"ID", "PipelineID", "ProjectIDs", "IssueIDs"}, match: []string{"id"},
		assign: func(data *domain.Bootstrap, value reflect.Value) {
			data.Releases = append(data.Releases, value.Interface().(domain.Release))
		},
	},
	"releasePipelines": {
		typ: reflect.TypeFor[domain.ReleasePipeline](), fields: []string{"ID", "TeamIDs"}, match: []string{"id"},
		assign: func(data *domain.Bootstrap, value reflect.Value) {
			data.ReleasePipelines = append(data.ReleasePipelines, value.Interface().(domain.ReleasePipeline))
		},
	},
	"customers": {
		typ: reflect.TypeFor[domain.Customer](), fields: []string{"ID"}, match: []string{"id"},
		assign: func(data *domain.Bootstrap, value reflect.Value) {
			data.Customers = append(data.Customers, value.Interface().(domain.Customer))
		},
	},
	"customerRequests": {
		typ: reflect.TypeFor[domain.CustomerRequest](), fields: []string{"ID", "CustomerID", "IssueID", "ProjectID"}, match: []string{"id"},
		assign: func(data *domain.Bootstrap, value reflect.Value) {
			data.CustomerRequests = append(data.CustomerRequests, value.Interface().(domain.CustomerRequest))
		},
	},
	"labels": {
		typ: reflect.TypeFor[domain.IssueLabel](), fields: []string{"ID", "Scope", "ArchivedAt"}, match: []string{"id"},
		assign: func(data *domain.Bootstrap, value reflect.Value) {
			data.Labels = append(data.Labels, value.Interface().(domain.IssueLabel))
		},
	},
	// Exact-ID initiative headers: the literal projected data.Initiatives.
	"initiatives": {
		typ: reflect.TypeFor[domain.Initiative](), fields: []string{"ID", "LeadTeamID", "ContributingTeamIDs", "ProjectIDs"}, match: []string{"id"},
		assign: func(data *domain.Bootstrap, value reflect.Value) {
			data.Initiatives = append(data.Initiatives, value.Interface().(domain.Initiative))
		},
	},
	// Slug-referenced project and initiative headers: a selected credential's
	// favorite/subscription projection keeps only key-team resources.
	"projectRefs": {
		field: "projects",
		typ: reflect.TypeFor[domain.Project](), fields: []string{"ID", "SlugID", "PreviousSlugIDs", "TeamIDs"}, match: []string{"slugRef"},
		assign: func(data *domain.Bootstrap, value reflect.Value) {
			data.Projects = append(data.Projects, value.Interface().(domain.Project))
		},
	},
	"initiativeRefs": {
		field: "initiatives",
		typ: reflect.TypeFor[domain.Initiative](), fields: []string{"ID", "SlugID", "PreviousSlugIDs", "LeadTeamID", "ProjectIDs"}, match: []string{"slugRef"},
		assign: func(data *domain.Bootstrap, value reflect.Value) {
			data.Initiatives = append(data.Initiatives, value.Interface().(domain.Initiative))
		},
	},
	"asks": {
		typ: reflect.TypeFor[domain.Ask](), fields: []string{"ID", "TeamID"}, match: []string{"id"},
		assign: func(data *domain.Bootstrap, value reflect.Value) {
			data.Asks = append(data.Asks, value.Interface().(domain.Ask))
		},
	},
}

// RecordAuthorizationRead holds, in collection order, the authorization
// headers of every unfiltered record of one kind whose id (or slug) equals
// the reference, read in one snapshot validated against the access policy.
// It is storage, not authorization: callers apply the literal predicate with
// the access policy helpers below.
type RecordAuthorizationRead struct {
	Data  domain.Bootstrap
	owner *ResourceRead
}

func (r *RecordAuthorizationRead) Close() {
	if r == nil {
		return
	}
	r.Data = domain.Bootstrap{}
	r.owner.Close()
}

// BeginRecordAuthorizationRead is available only with bounded metadata.
func (s *SQLiteStore) BeginRecordAuthorizationRead(ctx context.Context, access AccessContext, kind, ref string) (_ *RecordAuthorizationRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrMetadataRecordQuery
	}
	if err := access.checkBinding(s); err != nil {
		return nil, err
	}
	spec, ok := recordAuthorizationKinds[kind]
	if !ok {
		return nil, ErrMetadataRecordQuery
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	read := &RecordAuthorizationRead{owner: &ResourceRead{store: s}}
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
	field := kind
	if spec.field != "" {
		field = spec.field
	}
	if current {
		err = read.installed(ctx, field, spec, ref)
	} else {
		err = read.canonical(ctx, p.workspace, field, spec, ref)
	}
	if err == nil {
		err = s.validateRecordAuthorization(ctx, access)
	}
	if err != nil {
		return nil, err
	}
	return read, nil
}

// installed reads the headers from the current installed logical source, in
// collection order, exactly as the literal projection's native values.
func (r *RecordAuthorizationRead) installed(ctx context.Context, kind string, spec recordAuthorizationKind, ref string) error {
	return r.owner.visitNative(ctx, kind, func(_ int, node metadataLogicalNode) error {
		return r.owner.withHeader(ctx, node, spec.fields, func(value reflect.Value) error {
			if !slices.ContainsFunc(spec.match, func(member string) bool { return recordAuthorizationMatch(value, member, ref) }) {
				return nil
			}
			if err := r.owner.reserve(ctx, uint64(spec.typ.Size())*2+4096); err != nil {
				return err
			}
			spec.assign(&r.Data, value)
			return nil
		})
	})
}

// canonical streams the stored collection (split rows or the inline root
// array, which keeps duplicate, empty and overlong IDs) in collection order and
// decodes each element with encoding/json, as loading does. It serves only a
// policy snapshot newer than the installed source; the caller revalidates it.
func (r *RecordAuthorizationRead) canonical(ctx context.Context, workspace, kind string, spec recordAuthorizationKind, ref string) error {
	read, err := r.owner.store.BeginMetadataArrayRead(ctx, workspace, kind)
	if err != nil {
		return err
	}
	defer read.Close()
	if read.Shape.RequiresTypedMerge {
		return ErrMetadataRecordQuery
	}
	return read.Visit(func(ctx context.Context, record MetadataArrayRecord) error {
		if err := r.owner.reserve(ctx, uint64(max(record.EncodedBytes, 0))*8+uint64(spec.typ.Size())*2+4096); err != nil {
			return err
		}
		value := reflect.New(spec.typ)
		if err := json.NewDecoder(record.Reader).Decode(value.Interface()); err != nil {
			return err
		}
		if slices.ContainsFunc(spec.match, func(member string) bool { return recordAuthorizationMatch(value.Elem(), member, ref) }) {
			spec.assign(&r.Data, value.Elem())
		}
		return nil
	})
}

func recordAuthorizationMatch(value reflect.Value, member, ref string) bool {
	if member == "slugRef" {
		var previous []string
		if field := value.FieldByName("PreviousSlugIDs"); field.IsValid() {
			previous = field.Interface().([]string)
		}
		return domain.MatchSlugRef(ref, value.FieldByName("ID").String(), value.FieldByName("SlugID").String(), previous) != domain.SlugNoMatch
	}
	return recordAuthorizationMember(value, member) == ref
}

func recordAuthorizationMember(value reflect.Value, member string) string {
	switch member {
	case "id":
		return value.FieldByName("ID").String()
	case "slugId":
		return value.FieldByName("SlugID").String()
	}
	return ""
}

// BeginSettingAuthorizationRead reads one workspace setting value (the
// collection the literal settingCollection decodes), in the same snapshot
// rules as BeginRecordAuthorizationRead. A missing setting leaves Settings nil.
func (s *SQLiteStore) BeginSettingAuthorizationRead(ctx context.Context, access AccessContext, key string) (_ *RecordAuthorizationRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrMetadataRecordQuery
	}
	if err := access.checkBinding(s); err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	read := &RecordAuthorizationRead{owner: &ResourceRead{store: s}}
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
		field, _, _ := metadataLogicalField("settings")
		err = source.fields[field].Children(ctx, func(name reflect.Value, node metadataLogicalNode) error {
			if name.Kind() != reflect.String || name.String() != key {
				return nil
			}
			value, err := read.owner.node(ctx, node, false)
			if err != nil {
				return err
			}
			if read.Data.Settings == nil {
				read.Data.Settings = map[string]any{}
			}
			read.Data.Settings[key] = value.Interface()
			return ErrMetadataIterationDone
		})
		if errors.Is(err, ErrMetadataIterationDone) {
			err = nil
		}
	} else {
		err = read.setting(ctx, p.workspace, key)
	}
	if err == nil {
		err = s.validateRecordAuthorization(ctx, access)
	}
	if err != nil {
		return nil, err
	}
	return read, nil
}

// setting replays the stored settings map's decoding occurrences (split rows
// or the inline root object) and keeps the final value of key.
func (r *RecordAuthorizationRead) setting(ctx context.Context, workspace, key string) error {
	read, err := r.owner.store.BeginMetadataCollectionRead(ctx, workspace, "settings")
	if err != nil {
		return err
	}
	defer read.Close()
	var raw []byte
	found := false
	err = read.VisitMap("settings", func(ctx context.Context, item MetadataMapOccurrence) error {
		if item.Reset {
			raw, found = nil, false
			return nil
		}
		if item.BeginObject {
			return nil
		}
		name := item.Key
		if !item.CanonicalKey {
			encoded, err := io.ReadAll(io.LimitReader(item.RawKey, max(item.EncodedKeyBytes, 0)+1))
			if err != nil {
				return err
			}
			if err := json.Unmarshal(encoded, &name); err != nil {
				return err
			}
		}
		if name != key {
			return nil
		}
		if err := r.owner.reserve(ctx, uint64(max(item.EncodedBytes, 0))*8+128<<10); err != nil {
			return err
		}
		raw, err = io.ReadAll(item.Reader)
		found = err == nil
		return err
	})
	if err != nil || !found {
		return err
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	r.Data.Settings = map[string]any{key: value}
	return nil
}

func (s *SQLiteStore) validateRecordAuthorization(ctx context.Context, access AccessContext) error {
	p := access.policy
	if s.db.journalReady.Load() {
		epoch, err := s.AuthzEpoch(ctx, access.WorkspaceID)
		if err == nil && epoch != access.AuthzEpoch {
			err = errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
		}
		if err != nil {
			return err
		}
	} else {
		stamp, err := readMetadataStamp(ctx, s.db, p.workspace)
		if err == nil && stamp != p.stamp {
			err = errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
		}
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

// TeamContentVisible is the literal projection's allowed[teamID] (content
// scope), without the credential restriction.
func (access AccessContext) TeamContentVisible(teamID string) bool {
	return access.policy != nil && access.policy.teams.Content[teamID]
}

// TeamRowVisible is membership of the literal projected data.Teams.
func (access AccessContext) TeamRowVisible(teamID string) bool {
	return access.policy != nil && access.policy.teams.Teams[teamID]
}

// TeamActiveRow is the literal "team" reference check after teamAllowed: a
// projected team row with this ID that is not retired.
func (access AccessContext) TeamActiveRow(teamID string) bool {
	p := access.policy
	return p != nil && p.teams.Teams[teamID] && slices.ContainsFunc(p.metadata.Teams, func(team domain.Team) bool { return team.ID == teamID && team.RetiredAt == nil })
}

// ProjectVisible is the literal projection's project filter.
func (access AccessContext) ProjectVisible(project domain.Project) bool {
	return access.policy != nil && access.policy.projectVisible(project)
}

// ViewerID is the principal of the policy snapshot.
func (access AccessContext) ViewerID() string {
	if access.policy == nil {
		return ""
	}
	return access.policy.principal
}

// FilterSettingsByVisibility applies the literal projection's settings filter
// (posts, meetings, dashboards) to data with the policy's viewer and scope.
func (access AccessContext) FilterSettingsByVisibility(data *domain.Bootstrap) {
	if access.policy == nil {
		return
	}
	data.Viewer.ID, data.ViewerRole = access.policy.principal, access.policy.metadata.ViewerRole
	filterSettingsByVisibility(data, access.policy.teams.Content)
}

// TeamContentAllowed is the literal teamAllowed: the viewer reads the team's
// content and a selected credential names it.
func (access AccessContext) TeamContentAllowed(teamID string) bool {
	return access.policy != nil && access.policy.teamAllowed(teamID)
}

// TeamRowAllowed is the literal teamRowAllowed: the team row is in the
// viewer's projection and a selected credential names it.
func (access AccessContext) TeamRowAllowed(teamID string) bool {
	p := access.policy
	return p != nil && p.teams.Teams[teamID] && (!p.selected() || slices.Contains(p.key.TeamIDs, teamID))
}

// ViewerRole is the viewer's workspace role in the policy snapshot.
func (access AccessContext) ViewerRole() string {
	if access.policy == nil {
		return ""
	}
	return access.policy.metadata.ViewerRole
}
