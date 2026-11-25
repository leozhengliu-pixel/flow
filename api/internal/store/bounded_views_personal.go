package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"io"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// SavedViewAuthorization binds the middleware decision to its policy and the
// unfiltered candidates it saw, in collection order.
type SavedViewAuthorization struct {
	Access AccessContext
	Ref    string
	digest [sha256.Size]byte
}

func NewSavedViewAuthorization(access AccessContext, ref string, candidates []domain.SavedView) *SavedViewAuthorization {
	digest := sha256.New()
	for _, candidate := range candidates {
		addSavedViewHeader(digest, candidate)
	}
	proof := &SavedViewAuthorization{Access: access, Ref: ref}
	digest.Sum(proof.digest[:0])
	return proof
}

func addSavedViewHeader(digest hash.Hash, header domain.SavedView) {
	var length [8]byte
	for _, field := range []string{header.ID, header.SlugID, header.Scope, header.TeamID, header.OwnerID, header.ProjectID} {
		binary.LittleEndian.PutUint64(length[:], uint64(len(field)))
		_, _ = digest.Write(length[:])
		_, _ = io.WriteString(digest, field)
	}
}

type savedViewWriteKey struct{}

type savedViewWrite struct {
	ref           string
	create, erase bool
	favorite      *bool
	pulse         bool
	auth          *SavedViewAuthorization
}

// MutateSavedViewRecords runs the literal callback on named metadata records.
// Alias resolution and preference selection run under the workspace write
// lock on each attempt, including retries.
func (s *SQLiteStore) MutateSavedViewRecords(ctx context.Context, workspace, event, ref string, payload any, auth *SavedViewAuthorization, mutate func(*domain.Bootstrap) (string, error)) error {
	if !s.BoundedMetadataEnabled() {
		return ErrBoundedMetadataCoverage
	}
	switch event {
	case "view.created", "view.updated", "view.deleted", "favorite.added", "favorite.removed":
	default:
		return ErrBoundedMetadataCoverage
	}
	selection := &savedViewWrite{ref: ref, create: event == "view.created", erase: event == "view.deleted", auth: auth}
	input, _ := payload.(domain.SavedViewMutationInput)
	selection.favorite = input.Favorite
	selection.pulse = selection.create && input.Resource != nil && *input.Resource == "pulse"
	var scope MetadataRecordScope
	for _, item := range []struct {
		kind string
		id   *string
	}{{"teams", input.TeamID}, {"projects", input.ProjectID}, {"users", input.OwnerID}} {
		if item.id == nil || *item.id == "" || selection.pulse && item.kind == "users" {
			continue
		}
		key := RecordKey{Kind: item.kind, ID: *item.id}
		if validMetadataKey(key) {
			scope.Read = append(scope.Read, key)
		} else if !slices.Contains(scope.Fields, key.Kind) {
			// A compatibility collection can store IDs that do not fit
			// the keyed row format.
			scope.Fields = append(scope.Fields, key.Kind)
		}
	}
	var appendKinds []string
	if selection.create {
		appendKinds = append(appendKinds, "savedViews")
	}
	if selection.favorite != nil && *selection.favorite {
		appendKinds = append(appendKinds, "favorites")
	}
	ctx = context.WithValue(ctx, metadataAppendKindsKey{}, appendKinds)
	ctx = context.WithValue(WithScopedSelectionWrite(ctx), savedViewWriteKey{}, selection)
	return s.mutateWorkspaceScopedWithAggregate(ctx, workspace, event, payload, scope, mutate, nil)
}

func (w *savedViewWrite) selectRecords(ctx context.Context, owner *ResourceRead, scope MetadataRecordScope, actorID string) (MetadataRecordScope, error) {
	scope.Read, scope.Write = slices.Clone(scope.Read), slices.Clone(scope.Write)
	var target domain.SavedView
	var found bool
	digest := sha256.New()
	if !w.create {
		err := owner.visitNative(ctx, "savedViews", func(_ int, node metadataLogicalNode) error {
			matchTarget := false
			var err error
			if !found {
				matchTarget, err = owner.savedViewReferenceMatches(ctx, node, w.ref)
				if err != nil {
					return err
				}
			}
			matchProof := w.auth != nil && w.auth.Ref == w.ref && matchTarget
			if w.auth != nil && (found || w.auth.Ref != w.ref) {
				matchProof, err = owner.savedViewReferenceMatches(ctx, node, w.auth.Ref)
				if err != nil {
					return err
				}
			}
			if !matchTarget && !matchProof {
				return nil
			}
			return owner.withHeader(ctx, node, recordAuthorizationKinds["savedViews"].fields, func(value reflect.Value) error {
				header := value.Interface().(domain.SavedView)
				if matchTarget {
					target, found = header, true
				}
				if matchProof {
					addSavedViewHeader(digest, header)
				} else if w.auth == nil {
					return ErrMetadataIterationDone
				}
				return nil
			})
		})
		if err != nil {
			return scope, err
		}
		if w.auth != nil && !slices.Equal(digest.Sum(nil), w.auth.digest[:]) {
			return scope, ErrResourceAccessChanged
		}
		if !found {
			return scope, nil // The literal callback returns its own not-found error.
		}
		scope = savedViewAddWrite(scope, RecordKey{Kind: "savedViews", ID: target.ID})
	}
	if actorID == "" && (w.pulse || w.favorite != nil) {
		index, _, _ := metadataLogicalField("viewer")
		id, err := issueHotField(ctx, owner.source.fields[index], "ID")
		if err != nil {
			return scope, err
		}
		value, err := owner.node(ctx, id, false)
		if err != nil {
			return scope, err
		}
		actorID = value.String()
		if key := (RecordKey{Kind: "users", ID: actorID}); w.pulse && actorID != "" && validMetadataKey(key) {
			scope.Read = append(scope.Read, key)
		}
	}
	if w.erase {
		for _, kind := range []string{"favorites", "subscriptions"} {
			seen := map[string]bool{}
			err := owner.visitNative(ctx, kind, func(_ int, node metadataLogicalNode) error {
				match, err := owner.savedViewFieldMatches(ctx, node, "ResourceType", "view")
				if err != nil || !match {
					return err
				}
				match, err = owner.savedViewFieldMatches(ctx, node, "ResourceID", target.ID)
				if err != nil || !match {
					return err
				}
				return owner.withHeader(ctx, node, []string{"ID"}, func(value reflect.Value) error {
					id := value.FieldByName("ID").String()
					if seen[id] {
						return nil
					}
					if err := owner.reserve(ctx, 128+uint64(len(id))*2); err != nil {
						return err
					}
					seen[id] = true
					scope = savedViewAddWrite(scope, RecordKey{Kind: kind, ID: id})
					if slices.Contains(scope.Fields, kind) {
						return ErrMetadataIterationDone
					}
					return nil
				})
			})
			if err != nil {
				return scope, err
			}
		}
	}
	if w.favorite != nil {
		var existing, positionSource *domain.Favorite
		position := 0.0
		err := owner.visitNative(ctx, "favorites", func(_ int, node metadataLogicalNode) error {
			match, err := owner.savedViewFieldMatches(ctx, node, "UserID", actorID)
			if err != nil || !match {
				return err
			}
			return owner.withHeader(ctx, node, []string{"ID", "ResourceType", "ResourceID", "Tab", "FolderID", "Position"}, func(value reflect.Value) error {
				item := value.Interface().(domain.Favorite)
				if !w.create && item.ResourceType == "view" && item.ResourceID == target.ID && item.Tab == "" {
					existing = &item
					return ErrMetadataIterationDone
				}
				// Replaying only the last qualifying entry computes the
				// same position as replaying all of the actor's root rows.
				if *w.favorite && item.FolderID == "" && item.Position <= position {
					position, positionSource = item.Position-1, &item
				}
				return nil
			})
		})
		if err != nil {
			return scope, err
		}
		if existing != nil && !*w.favorite {
			scope = savedViewAddWrite(scope, RecordKey{Kind: "favorites", ID: existing.ID})
		} else {
			item := existing
			if item == nil {
				item = positionSource
			}
			if item != nil {
				key := RecordKey{Kind: "favorites", ID: item.ID}
				if key.ID != "" && validMetadataKey(key) {
					scope.Read = append(scope.Read, key)
				} else {
					scope.Fields = append(scope.Fields, key.Kind)
				}
			}
		}
	}
	return scope, nil
}

func (r *ResourceRead) savedViewReferenceMatches(ctx context.Context, node metadataLogicalNode, ref string) (bool, error) {
	match, err := r.savedViewFieldMatches(ctx, node, "ID", ref)
	if err != nil || match {
		return match, err
	}
	return r.savedViewFieldMatches(ctx, node, "SlugID", ref)
}

func (r *ResourceRead) savedViewFieldMatches(ctx context.Context, node metadataLogicalNode, field, expected string) (bool, error) {
	value, err := issueHotField(ctx, node, field)
	if err != nil {
		return false, err
	}
	return r.nativeEqual(ctx, value, expected)
}

func savedViewAddWrite(scope MetadataRecordScope, key RecordKey) MetadataRecordScope {
	if slices.Contains(scope.Fields, key.Kind) || slices.Contains(scope.Write, key) {
		return scope
	}
	if key.ID == "" || !validMetadataKey(key) {
		// Inline compatibility collections can contain unkeyed rows.
		scope.Fields = append(scope.Fields, key.Kind)
		scope.Write = slices.DeleteFunc(scope.Write, func(other RecordKey) bool { return other.Kind == key.Kind })
	} else {
		scope.Write = append(scope.Write, key)
	}
	return scope
}

func (r *ResourceRead) savedViewInputLimit(ctx context.Context, scope MetadataRecordScope, limit int) (int, error) {
	const pointAllowance = 1 << 20
	fields := metadataRecordFields(scope)
	var total uint64
	for _, name := range fields {
		if !slices.Contains(scope.Fields, name) {
			index, _, _ := metadataLogicalField(name)
			total += r.source.fields[index].cost
		}
	}
	if total <= pointAllowance {
		return limit, nil
	}

	// Size only the selected records in large collections. Inline fields
	// already have their own allowance from prepareScopedInlineSelection.
	keys := map[string]map[string]bool{}
	for _, key := range append(slices.Clone(scope.Read), scope.Write...) {
		if slices.Contains(scope.Fields, key.Kind) {
			continue
		}
		if keys[key.Kind] == nil {
			keys[key.Kind] = map[string]bool{}
		}
		keys[key.Kind][key.ID] = true
	}
	var selected uint64
	for _, name := range fields {
		ids := keys[name]
		if len(ids) == 0 {
			continue
		}
		err := r.visitNative(ctx, name, func(_ int, node metadataLogicalNode) error {
			return r.withHeader(ctx, node, []string{"ID"}, func(value reflect.Value) error {
				id := value.FieldByName("ID").String()
				if !ids[id] {
					return nil
				}
				if node.cost > metadataWorkBytes/4-selected {
					return ErrMetadataWorkBudget
				}
				selected += node.cost
				delete(ids, id)
				if len(ids) == 0 {
					return ErrMetadataIterationDone
				}
				return nil
			})
		})
		if err != nil {
			return 0, err
		}
	}
	if selected <= pointAllowance {
		return limit, nil
	}
	extra := selected - pointAllowance
	if err := r.reserve(ctx, extra*4); err != nil {
		return 0, err
	}
	return limit + int(extra), nil
}

func (w *savedViewWrite) validateAccess(ctx context.Context, s *SQLiteStore, tx *sqlTx, workspace, workspaceID, actorID string, stamp copyStamp) error {
	if w.auth == nil {
		return nil
	}
	access := w.auth.Access
	if err := access.checkBinding(s); err != nil {
		return err
	}
	if access.policy.workspace != workspace || access.WorkspaceID != workspaceID || access.PrincipalID != actorID {
		return ErrAuthForbidden
	}
	if !s.db.journalReady.Load() {
		if stamp != access.policy.stamp {
			return ErrResourceAccessChanged
		}
		return nil
	}
	epoch, err := readAuthzEpoch(ctx, tx, workspaceID)
	if err == nil && epoch != access.AuthzEpoch {
		return ErrResourceAccessChanged
	}
	return err
}

// BeginSharedSavedViewRead owns the first matching token and the exact
// private team/project headers consulted by the public handler.
func (s *SQLiteStore) BeginSharedSavedViewRead(ctx context.Context, workspace, token string) (domain.Bootstrap, func(), error) {
	for attempt := 0; attempt < 3; attempt++ {
		data, release, err := s.readSharedSavedView(ctx, workspace, token)
		if err != ErrResourceAccessChanged {
			return data, release, err
		}
		if attempt < 2 {
			if err := s.reloadSavedViewSource(ctx, workspace); err != nil {
				return domain.Bootstrap{}, nil, err
			}
		}
	}
	return domain.Bootstrap{}, nil, ErrResourceAccessChanged
}

func (s *SQLiteStore) reloadSavedViewSource(ctx context.Context, workspace string) error {
	if s.dialect == "sqlite" {
		return s.ReloadWorkspace(ctx, workspace)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.reloadResidentWorkspace(ctx, workspace)
}

func (s *SQLiteStore) readSharedSavedView(ctx context.Context, workspace, token string) (domain.Bootstrap, func(), error) {
	installed, owner, err := readLeafBegin(ctx, s, workspace)
	if err != nil {
		return domain.Bootstrap{}, nil, err
	}
	defer installed.Close()
	data, err := owner.sharedSavedView(ctx, token)
	if err != nil {
		owner.Close()
		return domain.Bootstrap{}, nil, err
	}
	stamp, err := readMetadataStamp(ctx, s.db, workspace)
	if err != nil || owner.source.Stamp() != stamp {
		owner.Close()
		if err == nil {
			err = ErrResourceAccessChanged
		}
		return domain.Bootstrap{}, nil, err
	}
	return data, owner.Close, nil
}

func (r *ResourceRead) sharedSavedView(ctx context.Context, token string) (data domain.Bootstrap, err error) {
	var view domain.SavedView
	found := false
	err = r.visitNative(ctx, "savedViews", func(_ int, node metadataLogicalNode) error {
		match, err := r.savedViewFieldMatches(ctx, node, "ShareToken", token)
		if err != nil || !match {
			return err
		}
		value, err := r.node(ctx, node, false)
		if err != nil {
			return err
		}
		view, found = value.Interface().(domain.SavedView), true
		return ErrMetadataIterationDone
	})
	if err != nil || !found {
		return data, err
	}
	data.SavedViews = []domain.SavedView{view}
	index, _, _ := metadataLogicalField("workspace")
	identity, err := r.node(ctx, r.source.fields[index], false)
	if err != nil {
		return data, err
	}
	data.Workspace = identity.Interface().(domain.Workspace)
	var teamIDs []string
	if view.Scope == "team" {
		teamIDs = append(teamIDs, view.TeamID)
	}
	if view.ProjectID != "" {
		err = r.visitNative(ctx, "projects", func(_ int, node metadataLogicalNode) error {
			match, err := r.savedViewFieldMatches(ctx, node, "ID", view.ProjectID)
			if err != nil || !match {
				return err
			}
			header, err := r.projectNode(ctx, node, "ID", "TeamIDs")
			if err != nil {
				return err
			}
			project := header.Interface().(domain.Project)
			data.Projects = []domain.Project{project}
			teamIDs = append(teamIDs, project.TeamIDs...)
			return ErrMetadataIterationDone
		})
		if err != nil {
			return data, err
		}
	}
	if len(teamIDs) != 0 {
		err = r.visitNative(ctx, "teams", func(_ int, node metadataLogicalNode) error {
			return r.withHeader(ctx, node, []string{"ID", "Private"}, func(value reflect.Value) error {
				team := value.Interface().(domain.Team)
				if slices.Contains(teamIDs, team.ID) && team.Private {
					if err := r.reserve(ctx, uint64(len(team.ID))*2+1024); err != nil {
						return err
					}
					data.Teams = append(data.Teams, team)
					return ErrMetadataIterationDone
				}
				return nil
			})
		})
	}
	return data, err
}
