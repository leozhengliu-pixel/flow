package store

import (
	"context"
	"reflect"
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
