package store

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

// IssueSuggestionRead owns one issue's selected Triage Intelligence
// suggestions through response encoding. It does not authorize or filter
// their state or targets.
type IssueSuggestionRead struct {
	Suggestions []domain.IssueSuggestion
	owner       *ResourceRead
	once        sync.Once
}

func (r *IssueSuggestionRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Suggestions = nil
			if r.owner != nil {
				r.owner.Close()
				r.owner = nil
			}
		})
	}
}

// ReadIssueSuggestions selects every exact IssueID match in source order,
// including duplicates and every state. It decodes only IssueID headers of
// other suggestions. No match returns a non-nil empty slice. The result
// remains valid after its InstalledMetadataRead is closed.
func (r *InstalledMetadataRead) ReadIssueSuggestions(ctx context.Context, issueID string) (_ *IssueSuggestionRead, err error) {
	owner, ctx, err := r.commentActionOwner(ctx)
	if err != nil {
		return nil, err
	}
	read := &IssueSuggestionRead{Suggestions: []domain.IssueSuggestion{}, owner: owner}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	err = owner.visitNative(ctx, "issueSuggestions", func(_ int, node metadataLogicalNode) error {
		return owner.withHeader(ctx, node, []string{"IssueID"}, func(header reflect.Value) error {
			if header.Interface().(domain.IssueSuggestion).IssueID != issueID {
				return nil
			}
			value, err := owner.node(ctx, node, true)
			if err == nil {
				read.Suggestions = append(read.Suggestions, value.Interface().(domain.IssueSuggestion))
			}
			return err
		})
	})
	if canceled := ctx.Err(); canceled != nil {
		err = canceled
	}
	if err != nil {
		return nil, err
	}
	return read, nil
}

// ViewerProjectionScope is the team part of PagedWorkspaceMetadata's viewer
// projection: Teams are the projected team rows, Content the teams whose
// projects survive the projection (filterBootstrapTeams' allowed set).
type ViewerProjectionScope struct {
	Teams   map[string]bool
	Content map[string]bool
}

// viewerProjectionProbe names a synthetic project bound to one team. Probes
// are the only projects in the projected input, so they cannot collide with
// stored projects.
const viewerProjectionProbe = "\x00viewer-projection-probe\x00"

// ViewerProjectionScope runs the literal PagedWorkspaceMetadata projection for
// userID over this generation's core and resident policy fields only (the
// fields its team scope reads), with live membership, role, user and team
// default favorite rows read exactly as the full accessor reads them. One
// synthetic project per team row reports the content scope its project filter
// applies. Errors are the projection's own, plus this read's admission.
func (r *InstalledMetadataRead) ViewerProjectionScope(ctx context.Context, userID string) (_ ViewerProjectionScope, err error) {
	owner, bound, err := r.commentActionOwner(ctx)
	if err != nil {
		return ViewerProjectionScope{}, err
	}
	defer owner.Close()
	var data domain.Bootstrap
	root := reflect.ValueOf(&data).Elem()
	for _, name := range []string{"workspace", "viewer", "viewerRole", "teams", "teamSettings", "workspaceSettings", "teamParents"} {
		index, _, _ := metadataLogicalField(name)
		value, err := owner.node(bound, owner.source.fields[index], false)
		if err != nil {
			return ViewerProjectionScope{}, err
		}
		root.Field(index).Set(value)
	}
	if err := owner.reserve(bound, uint64(len(data.Teams))*512+4096); err != nil {
		return ViewerProjectionScope{}, err
	}
	data.Projects = make([]domain.Project, 0, len(data.Teams))
	for _, team := range data.Teams {
		data.Projects = append(data.Projects, domain.Project{ID: viewerProjectionProbe + team.ID, TeamIDs: []string{team.ID}})
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	tx, err := r.store.db.BeginTx(ctx, metadataReadOptions(r.store.dialect))
	if err != nil {
		return ViewerProjectionScope{}, err
	}
	defer tx.Rollback()
	snapshot := owner.metadataOutputContext(ctx)
	snapshot = context.WithValue(snapshot, workspaceReadKey{}, workspaceRead{
		database: r.store.db, workspace: r.workspace, reader: tx, metadata: data,
	})
	// Admit the live directory and principal rows the projection reads, as
	// the paged bootstrap reader does before the same projector.
	document := &DocumentRead{Data: data, owner: owner}
	if err := document.documentProjectionWork(snapshot, data.Workspace.ID, userID); err != nil {
		return ViewerProjectionScope{}, err
	}
	projected, err := r.store.PagedWorkspaceMetadata(snapshot, r.workspace, userID)
	if outputErr := metadataOutputError(snapshot); outputErr != nil {
		return ViewerProjectionScope{}, outputErr
	}
	if err != nil {
		return ViewerProjectionScope{}, err
	}
	scope := ViewerProjectionScope{Teams: map[string]bool{}, Content: map[string]bool{}}
	for _, team := range projected.Teams {
		scope.Teams[team.ID] = true
	}
	for _, project := range projected.Projects {
		if len(project.ID) > len(viewerProjectionProbe) && project.ID[:len(viewerProjectionProbe)] == viewerProjectionProbe {
			scope.Content[project.ID[len(viewerProjectionProbe):]] = true
		}
	}
	return scope, ctx.Err()
}

// ProjectVisibleInProjection reports whether any occurrence of id in this
// generation survives the projection's project filter for scope: an
// occurrence without teams, or one bound to a content team. A non-nil
// credential further requires a content team it allows, as a selected-team
// credential filter applied after the projection (which keeps only content
// teams) does. It decodes only ID headers of other projects.
func (r *InstalledMetadataRead) ProjectVisibleInProjection(ctx context.Context, scope ViewerProjectionScope, id string, credential func(teamID string) bool) (bool, error) {
	owner, ctx, err := r.commentActionOwner(ctx)
	if err != nil {
		return false, err
	}
	defer owner.Close()
	visible := false
	err = owner.visitNative(ctx, "projects", func(_ int, node metadataLogicalNode) error {
		return owner.withHeader(ctx, node, []string{"ID"}, func(header reflect.Value) error {
			if header.Interface().(domain.Project).ID != id {
				return nil
			}
			value, err := owner.projectNode(ctx, node, "ID", "TeamIDs")
			if err != nil {
				return err
			}
			teams := value.Interface().(domain.Project).TeamIDs
			if len(teams) == 0 && credential == nil {
				visible = true
			}
			for _, team := range teams {
				if scope.Content[team] && (credential == nil || credential(team)) {
					visible = true
				}
			}
			if visible {
				return ErrMetadataIterationDone
			}
			return nil
		})
	})
	if canceled := ctx.Err(); canceled != nil {
		err = canceled
	}
	if err != nil {
		return false, err
	}
	return visible, nil
}

// ReadIssueSuggestion selects the first suggestion with exactly this ID in
// source order, of any issue and state. No match returns an empty selection.
func (r *InstalledMetadataRead) ReadIssueSuggestion(ctx context.Context, id string) (_ *IssueSuggestionRead, err error) {
	owner, ctx, err := r.commentActionOwner(ctx)
	if err != nil {
		return nil, err
	}
	read := &IssueSuggestionRead{Suggestions: []domain.IssueSuggestion{}, owner: owner}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	err = owner.visitNative(ctx, "issueSuggestions", func(_ int, node metadataLogicalNode) error {
		return owner.withHeader(ctx, node, []string{"ID"}, func(header reflect.Value) error {
			if header.Interface().(domain.IssueSuggestion).ID != id {
				return nil
			}
			value, err := owner.node(ctx, node, true)
			if err != nil {
				return err
			}
			read.Suggestions = append(read.Suggestions, value.Interface().(domain.IssueSuggestion))
			return ErrMetadataIterationDone
		})
	})
	if canceled := ctx.Err(); canceled != nil {
		err = canceled
	}
	if err != nil {
		return nil, err
	}
	return read, nil
}

// VisitIssueSuggestions decodes every suggestion in source order, one at a
// time. Each value is admitted only while visit runs.
func (r *InstalledMetadataRead) VisitIssueSuggestions(ctx context.Context, visit func(domain.IssueSuggestion) error) error {
	owner, ctx, err := r.commentActionOwner(ctx)
	if err != nil {
		return err
	}
	defer owner.Close()
	err = owner.visitNative(ctx, "issueSuggestions", func(_ int, node metadataLogicalNode) error {
		scratch := &ResourceRead{store: owner.store}
		defer scratch.Close()
		value, err := scratch.node(ctx, node, false)
		if err != nil {
			return err
		}
		return visit(value.Interface().(domain.IssueSuggestion))
	})
	if canceled := ctx.Err(); canceled != nil {
		err = canceled
	}
	return err
}

// LabelVisibleInProjection reports whether any occurrence of id survives the
// projection's label filter (a workspace label or a projected team row's
// label). A non-nil credential receives the label's raw scope and further
// decides, as a selected-team credential filter applied afterwards does.
func (r *InstalledMetadataRead) LabelVisibleInProjection(ctx context.Context, scope ViewerProjectionScope, id string, credential func(labelScope string) bool) (bool, error) {
	owner, ctx, err := r.commentActionOwner(ctx)
	if err != nil {
		return false, err
	}
	defer owner.Close()
	visible := false
	err = owner.visitNative(ctx, "labels", func(_ int, node metadataLogicalNode) error {
		return owner.withHeader(ctx, node, []string{"ID"}, func(header reflect.Value) error {
			if header.Interface().(domain.IssueLabel).ID != id {
				return nil
			}
			value, err := owner.projectNode(ctx, node, "ID", "Scope")
			if err != nil {
				return err
			}
			labelScope := value.Interface().(domain.IssueLabel).Scope
			normalized := strings.ToLower(strings.TrimSpace(labelScope))
			if (normalized == "" || normalized == "workspace" || scope.Teams[labelScope]) && (credential == nil || credential(labelScope)) {
				visible = true
				return ErrMetadataIterationDone
			}
			return nil
		})
	})
	if canceled := ctx.Err(); canceled != nil {
		err = canceled
	}
	if err != nil {
		return false, err
	}
	return visible, nil
}

// ProjectedWorkspaceRead owns one literal paged projection through response
// encoding. Data must not escape Close.
type ProjectedWorkspaceRead struct {
	Data       domain.Bootstrap
	owner      *ResourceRead
	projection *collaborationProjectionCapture
	once       sync.Once
}

func (r *ProjectedWorkspaceRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Data = domain.Bootstrap{}
			r.projection = nil
			if r.owner != nil {
				r.owner.Close()
				r.owner = nil
			}
		})
	}
}

// ProjectionScope borrows the content scope of this exact projection. Its maps
// remain read-only and valid only until Close.
func (r *ProjectedWorkspaceRead) ProjectionScope(ctx context.Context) (ViewerProjectionScope, error) {
	if err := ctx.Err(); err != nil {
		return ViewerProjectionScope{}, err
	}
	if r == nil || r.owner == nil || r.projection == nil || !r.projection.captured {
		return ViewerProjectionScope{}, ErrBoundedMetadataCoverage
	}
	return ViewerProjectionScope{Teams: r.projection.scope.Teams, Content: r.projection.scope.Content}, nil
}

// projectedWorkspaceCollections are the complete collections a projected
// workspace may request: each is selected whole, so every projection rule
// and resource count reading it sees exactly the stored records.
var projectedWorkspaceCollections = []string{"projects", "labels", "labelGroups", "initiatives"}

// ProjectedWorkspace runs the literal PagedWorkspaceMetadata for userID over
// this generation's core and resident policy fields plus the named complete
// collections, with live membership, role, user, member, team-membership,
// invitation and team-default-favorite rows read exactly as the full accessor
// reads them. Collections that are not requested are absent from the input,
// so the result is exact only for the requested collections, the policy
// fields and the live directory (Users, Members, TeamMembers).
func (r *InstalledMetadataRead) ProjectedWorkspace(ctx context.Context, userID string, collections ...string) (_ *ProjectedWorkspaceRead, err error) {
	return r.projectedWorkspace(ctx, userID, false, collections...)
}

// ProjectedWorkspaceWithScope also retains the literal pre-filter viewer scope,
// avoiding a second directory projection for related record validation.
func (r *InstalledMetadataRead) ProjectedWorkspaceWithScope(ctx context.Context, userID string, collections ...string) (*ProjectedWorkspaceRead, error) {
	return r.projectedWorkspace(ctx, userID, true, collections...)
}

func (r *InstalledMetadataRead) projectedWorkspace(ctx context.Context, userID string, captureScope bool, collections ...string) (_ *ProjectedWorkspaceRead, err error) {
	for _, name := range collections {
		if !slices.Contains(projectedWorkspaceCollections, name) {
			return nil, ErrMetadataRecordQuery
		}
	}
	owner, bound, err := r.commentActionOwner(ctx)
	if err != nil {
		return nil, err
	}
	read := &ProjectedWorkspaceRead{owner: owner}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	var data domain.Bootstrap
	root := reflect.ValueOf(&data).Elem()
	for _, name := range append([]string{"workspace", "viewer", "viewerRole", "teams", "teamSettings", "workspaceSettings", "teamParents"}, collections...) {
		index, _, _ := metadataLogicalField(name)
		value, err := owner.node(bound, owner.source.fields[index], false)
		if err != nil {
			return nil, err
		}
		root.Field(index).Set(value)
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	tx, err := r.store.db.BeginTx(ctx, metadataReadOptions(r.store.dialect))
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	snapshot := owner.metadataOutputContext(ctx)
	snapshot = context.WithValue(snapshot, workspaceReadKey{}, workspaceRead{
		database: r.store.db, workspace: r.workspace, reader: tx, metadata: data,
	})
	document := &DocumentRead{Data: data, owner: owner}
	if err := document.documentProjectionWork(snapshot, data.Workspace.ID, userID); err != nil {
		return nil, err
	}
	if captureScope {
		read.projection = &collaborationProjectionCapture{owner: owner}
		snapshot = context.WithValue(snapshot, collaborationProjectionCaptureKey{}, read.projection)
	}
	projected, err := r.store.PagedWorkspaceMetadata(snapshot, r.workspace, userID)
	if outputErr := metadataOutputError(snapshot); outputErr != nil {
		return nil, outputErr
	}
	if err != nil {
		return nil, err
	}
	read.Data = projected
	return read, ctx.Err()
}

// TriageScoringInputs selects, unprojected and from this generation, what
// the heuristic suggestion scorer and run checks read: core and policy
// fields, every label, every project's triage fields and the ordered user
// directory as ID/Active headers.
func (r *InstalledMetadataRead) TriageScoringInputs(ctx context.Context) (_ *ProjectedWorkspaceRead, err error) {
	owner, ctx, err := r.commentActionOwner(ctx)
	if err != nil {
		return nil, err
	}
	read := &ProjectedWorkspaceRead{owner: owner}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	root := reflect.ValueOf(&read.Data).Elem()
	for _, name := range []string{"workspace", "viewer", "viewerRole", "teams", "teamSettings", "workspaceSettings", "teamParents", "labels"} {
		index, _, _ := metadataLogicalField(name)
		value, err := owner.node(ctx, owner.source.fields[index], false)
		if err != nil {
			return nil, err
		}
		root.Field(index).Set(value)
	}
	for _, selection := range []struct {
		name   string
		fields []string
	}{{"projects", triageProjectFields}, {"users", []string{"ID", "Active"}}} {
		index, _, _ := metadataLogicalField(selection.name)
		node := owner.source.fields[index]
		absent, err := node.isNil(ctx)
		if err != nil {
			return nil, err
		}
		target := root.Field(index)
		if !absent {
			target.Set(reflect.MakeSlice(target.Type(), 0, 0))
		}
		if err := owner.nativeArray(ctx, node, func(_ int, record metadataLogicalNode) error {
			value, err := owner.projectNode(ctx, record, selection.fields...)
			if err == nil {
				target.Set(reflect.Append(target, value))
			}
			return err
		}); err != nil {
			return nil, err
		}
	}
	return read, ctx.Err()
}
