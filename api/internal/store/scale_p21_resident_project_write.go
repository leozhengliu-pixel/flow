package store

import (
	"context"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

type residentScopedProjectEdit struct {
	source *metadataLogicalSource
	image  *coldDecodedImage
	scope  MetadataRecordScope
	actor  scopedMetadataActor

	before, next, validationBefore domain.Bootstrap
	aggregate                      string
	callbackErr                    error
}

func (p *residentScopedProjectEdit) close() { p.source.Release() }

func (p *residentScopedProjectEdit) matches(ctx context.Context, source *metadataLogicalSource, scope MetadataRecordScope, role string, teams []string, active bool) bool {
	if !reflect.DeepEqual(p.scope, scope) || p.actor.active != active || p.actor.role != role || !slices.Equal(p.actor.teams, teams) {
		return false
	}
	if p.source == source {
		return true
	}
	// Unrelated collections can change while this edit waits for the lock.
	// Compare every collection the callback read, including its core fields.
	fields := append(metadataRecordFields(scope), "viewer", "viewerRole")
	current, err := coldImageCurrent(ctx, p.image, source, fields)
	return err == nil && current
}

// Prepare the same selected callback inputs as the scoped writer. The
// writer still validates the current generation and actor under its lock
// and rebases the changed rows through its existing canonical writer.
func (s *SQLiteStore) prepareResidentScopedProject(ctx context.Context, workspace string, scope MetadataRecordScope, actor domain.User, hasActor, refreshActor bool, callback func(*domain.Bootstrap) (string, error)) *residentScopedProjectEdit {
	if residentWritesOff.Load() || coldDecodeCacheBudget() <= 0 {
		return nil
	}
	source, err := s.retainMetadataLogicalSource(ctx, workspace)
	if err != nil {
		return nil
	}
	keep := false
	defer func() {
		if !keep {
			source.Release()
		}
	}()
	if source.behind.Load() || source.Stamp().revision < 0 {
		return nil
	}
	image, ok, err := s.metadataResidency.residentImage(ctx, workspace, source)
	if err != nil || !ok {
		return nil
	}
	actorState := scopedMetadataActor{actor: mutationInputActor(actor), hasActor: hasActor, refreshActor: refreshActor}
	if hasActor {
		if role, status, err := s.WorkspaceRole(ctx, source.Stamp().workspaceID, actor.ID); err == nil && status == "active" {
			actorState.role, actorState.active = role, true
			actorState.teams = s.actorTeamIDs(ctx, source.Stamp().workspaceID, actor.ID)
		}
	}
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return nil
	}
	defer tx.Rollback()
	if current, err := s.residentStampCurrent(ctx, tx, workspace, source); err != nil || !current {
		return nil
	}
	loadScope, inlineFields, inputLimit, releaseInline, err := s.prepareScopedInlineSelection(ctx, tx, workspace, source, scope)
	if err != nil {
		return nil
	}
	defer releaseInline()
	if len(inlineFields) != 0 {
		return nil
	}
	ctx = withResidentRecordOrdinals(ctx, image)
	before, err := source.selectMetadata(ctx, loadScope, inputLimit, image)
	if err != nil || before.Workspace.ID != source.Stamp().workspaceID || !scopedSelectionDirectoryKept(before, actor, hasActor && refreshActor) {
		return nil
	}
	result := &residentScopedProjectEdit{
		source: source, image: image, scope: loadScope, actor: actorState,
		before: before, next: exactCopyValue(reflect.ValueOf(before)).Interface().(domain.Bootstrap),
	}
	usedLookup := false
	result.aggregate, result.callbackErr = s.runScopedMetadataCallback(ctx, tx, workspace, &result.next, &result.validationBefore, actorState, true, callback, &usedLookup)
	if usedLookup || ctx.Err() != nil {
		return nil
	}
	keep = true
	return result
}

type scopedMetadataActor struct {
	actor                          domain.User
	hasActor, refreshActor, active bool
	role                           string
	teams                          []string
}

func (s *SQLiteStore) runScopedMetadataCallback(ctx context.Context, tx *sqlTx, workspace string, next, validationBefore *domain.Bootstrap, actor scopedMetadataActor, revalidate bool, callback func(*domain.Bootstrap) (string, error), usedLookup *bool) (string, error) {
	originalRole := next.ViewerRole
	refreshDisplayReferences(next)
	if actor.hasActor {
		next.Viewer = actor.actor
		if actor.active {
			next.ViewerRole, next.ViewerTeamIDs = actor.role, actor.teams
		}
		if actor.refreshActor {
			if index := slices.IndexFunc(next.Users, func(user domain.User) bool { return user.ID == actor.actor.ID }); index >= 0 {
				next.Users[index] = actor.actor
			} else {
				next.Users = append(next.Users, actor.actor)
			}
		}
	}
	if revalidate {
		*validationBefore = cloneBootstrap(*next)
		validationBefore.ViewerRole, validationBefore.ViewerTeamIDs = originalRole, nil
	}
	next.FormerMembers = s.formerMemberLookup(ctx, tx, next)
	next.CollaborationUpdates = collaborationUpdatesLookup(ctx, tx, workspace)
	if usedLookup != nil {
		former := next.FormerMembers
		next.FormerMembers = func() map[string]bool {
			*usedLookup = true
			return former()
		}
		updates := next.CollaborationUpdates
		next.CollaborationUpdates = func(id string) ([][]byte, error) {
			*usedLookup = true
			return updates(id)
		}
	}
	aggregate, err := callback(next)
	next.ViewerRole, next.ViewerTeamIDs, next.FormerMembers, next.CollaborationUpdates = originalRole, nil, nil, nil
	return aggregate, err
}
