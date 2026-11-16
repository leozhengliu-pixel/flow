package store

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

// CollaborationDocumentRead owns only the socket's policy and first surviving
// collaboration header. Data is not a document detail or a mutation preimage.
type CollaborationDocumentRead struct {
	Data      domain.Bootstrap
	Document  domain.Document
	Found     bool
	Conflict  bool
	owner     *ResourceRead
	workspace string
	once      sync.Once
}

func (r *CollaborationDocumentRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Data, r.Document = domain.Bootstrap{}, domain.Document{}
			r.Found, r.Conflict = false, false
			r.workspace = ""
			r.owner.Close()
		})
	}
}

// Context validates the read lifetime and preserves its containing output
// owner. This header projection does not lend a complete source or SQL snapshot.
func (r *CollaborationDocumentRead) Context(ctx context.Context) (context.Context, error) {
	if r == nil || r.owner == nil {
		return nil, errMetadataLogicalClosed
	}
	r.owner.mu.Lock()
	closed := r.owner.closed
	r.owner.mu.Unlock()
	if closed {
		return nil, errMetadataLogicalClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, metadataWorkLeaseKey{}, nil), nil
}

func (r *CollaborationDocumentRead) TransferOutput(ctx context.Context) error {
	if r == nil || r.owner == nil {
		r.Close()
		return recordMetadataOutputError(ctx, ErrBoundedMetadataCoverage)
	}
	r.owner.mu.Lock()
	closed := r.owner.closed
	r.owner.mu.Unlock()
	if closed || !registerMetadataOutputClose(ctx, r.Close) {
		r.Close()
		return recordMetadataOutputError(ctx, ErrBoundedMetadataCoverage)
	}
	return nil
}

// BeginCollaborationDocumentRead preserves collaborationDocument's occurrence
// order, including an earlier ID/slug conflict before a later current match.
// The empty principal is the literal unprojected development accessor.
func (s *SQLiteStore) BeginCollaborationDocumentRead(ctx context.Context, workspace, userID, ref string, key *domain.APIKey) (_ *CollaborationDocumentRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	read := &CollaborationDocumentRead{owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if uint64(len(ref)) > (metadataWorkBytes-(2<<20))/8 {
		return nil, ErrMetadataWorkBudget
	}
	if err = read.owner.reserve(ctx, 2<<20+uint64(len(ref))*8); err != nil {
		return nil, err
	}

	held, handed := ctx.Value(workspaceReadKey{}).(workspaceRead)
	handed = handed && userID != "" && held.database == s.db &&
		(workspace == "" || workspace == held.workspace)
	var caller *domain.Bootstrap
	if handed {
		if held.reader == nil {
			return nil, ErrMetadataRecordQuery
		}
		if held.metadata.Workspace.ID == "" {
			return nil, ErrAuthForbidden
		}
		workspace, caller = held.workspace, &held.metadata
	}
	if caller != nil {
		// Capture policy only. The caller already owns its document slice;
		// matching headers are detached one at a time below.
		var policy domain.Bootstrap
		policy, err = collaborationCallerPolicy(ctx, read.owner, *caller)
		if err == nil {
			read.owner.source, err = captureMetadataLogicalSource(ctx, policy, copyStamp{})
		}
	} else {
		var installed *InstalledMetadataRead
		var found bool
		installed, found, err = s.BeginInstalledMetadataRead(ctx, workspace)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrAuthForbidden
		}
		workspace = installed.workspace
		read.owner.source, err = installed.retainSource(ctx, workspace)
		installed.Close()
	}
	if err != nil {
		return nil, err
	}
	read.workspace = workspace
	source := read.owner.source

	snapshot := ctx
	var tx *sqlTx
	var access AccessContext
	if userID != "" {
		if handed {
			tx = held.reader
		} else {
			tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
			if err != nil {
				return nil, err
			}
			defer tx.Rollback()
		}
		snapshot = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{
			database: s.db, workspace: workspace, reader: tx,
		})
	}
	policy := &collaborationProjectionCapture{owner: read.owner}
	read.Data, access, err = read.collaborationProjection(snapshot, userID, key, policy)
	if err != nil {
		return nil, err
	}
	if userID != "" && !policy.captured {
		return nil, ErrBoundedMetadataCoverage
	}
	if err = read.owner.reserve(snapshot, uint64(len(read.Data.Teams))*256+4096); err != nil {
		return nil, err
	}
	credentialScope := NewDocumentLinkScope(read.Data.Teams, read.Data.TeamSettings)

	selectHeader := func(header domain.Document, conflict bool, candidate *ResourceRead) error {
		cost := uint64(len(policy.data.Teams)) + uint64(len(header.TeamIDs)) + uint64(len(header.InheritedTeamIDs))
		if cost > (metadataWorkBytes-(64<<10))/4096 {
			candidate.Close()
			return ErrMetadataWorkBudget
		}
		if err := candidate.reserve(snapshot, cost*4096+64<<10); err != nil {
			candidate.Close()
			return err
		}
		if userID != "" && !bootstrapDocumentVisible(&policy.data, header, policy.scope, policy.links) {
			candidate.Close()
			return nil
		}
		if userID != "" {
			var allowed bool
			header, allowed = collaborationCredentialDocument(header, key, credentialScope)
			if !allowed {
				candidate.Close()
				return nil
			}
		}
		read.owner.mu.Lock()
		read.owner.release = append(read.owner.release, candidate.Close)
		read.owner.mu.Unlock()
		read.Data.Documents = []domain.Document{header}
		read.Document, read.Found, read.Conflict = header, true, conflict
		return ErrMetadataIterationDone
	}
	newCandidate := func() (*ResourceRead, error) {
		if err := source.Retain(); err != nil {
			return nil, err
		}
		candidate := &ResourceRead{store: s, source: source}
		if err := candidate.reserve(snapshot, 64<<10); err != nil {
			candidate.Close()
			return nil, err
		}
		return candidate, nil
	}
	if caller != nil {
		for _, document := range caller.Documents {
			matched, conflict := collaborationDocumentMatch(document, ref)
			if !matched {
				continue
			}
			candidate, candidateErr := newCandidate()
			if candidateErr != nil {
				return nil, candidateErr
			}
			header, headerErr := collaborationCallerDocument(snapshot, candidate, document)
			if headerErr != nil {
				candidate.Close()
				return nil, headerErr
			}
			err = selectHeader(header, conflict, candidate)
			if err != nil {
				break
			}
		}
	} else {
		err = read.owner.visitNative(snapshot, "documents", func(_ int, node metadataLogicalNode) error {
			return read.owner.withHeader(snapshot, node, []string{"ID", "SlugID", "CollaborationID"}, func(value reflect.Value) error {
				matched, conflict := collaborationDocumentMatch(value.Interface().(domain.Document), ref)
				if !matched {
					return nil
				}
				candidate, err := newCandidate()
				if err != nil {
					return err
				}
				header, err := collaborationNativeDocument(snapshot, candidate, node)
				if err != nil {
					candidate.Close()
					return err
				}
				return selectHeader(header, conflict, candidate)
			})
		})
	}
	if errors.Is(err, ErrMetadataIterationDone) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	if userID != "" && !handed {
		if err = tx.Rollback(); err != nil {
			return nil, err
		}
		if err = s.ValidateAccess(ctx, access); err != nil {
			return nil, err
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return read, nil
}

func collaborationDocumentMatch(document domain.Document, ref string) (bool, bool) {
	current := document.CollaborationID
	if current == "" {
		current = document.ID
	}
	if current == ref {
		return true, false
	}
	return document.ID == ref || document.SlugID == ref, true
}

type collaborationProjectionCaptureKey struct{}

// This closed hook is populated by the bounded projector immediately before
// its original filterBootstrapTeams call. It never retains member/user bodies.
type collaborationProjectionCapture struct {
	owner    *ResourceRead
	captured bool
	data     domain.Bootstrap
	scope    bootstrapTeamScope
	links    *DocumentLinkScope
}

func (r *collaborationProjectionCapture) capture(ctx context.Context, data domain.Bootstrap, allowed map[string]bool) error {
	if r == nil || r.owner == nil || r.captured {
		return ErrBoundedMetadataCoverage
	}
	cost := uint64(64 << 10)
	for _, part := range []struct {
		count int
		bytes uint64
	}{
		{len(data.Teams), 8192},
		{len(data.TeamMembers), 512},
		{len(data.TeamSettings), 1024},
	} {
		if uint64(part.count) > (metadataWorkBytes-cost)/part.bytes {
			return ErrMetadataWorkBudget
		}
		cost += uint64(part.count) * part.bytes
	}
	// Admission precedes the extra viewerTeamScope maps, all cloned policy
	// containers and both link-scope indexes used during the candidate scan.
	if err := r.owner.reserve(ctx, cost); err != nil {
		return err
	}
	scope := viewerTeamScope(data, allowed, data.Viewer.ID, data.ViewerRole)
	r.scope = bootstrapTeamScope{
		Teams: maps.Clone(scope.Teams), Content: maps.Clone(scope.Content),
		AdminClosed: maps.Clone(scope.AdminClosed),
	}
	r.data = domain.Bootstrap{
		Workspace: data.Workspace, ViewerRole: data.ViewerRole,
		Viewer: domain.User{
			ID: data.Viewer.ID, App: data.Viewer.App, Active: data.Viewer.Active,
			AppTeamIDs: data.Viewer.AppTeamIDs, AppScopes: data.Viewer.AppScopes,
		},
		ViewerTeamIDs: data.ViewerTeamIDs,
		TeamSettings:  maps.Clone(data.TeamSettings),
	}
	if data.Teams != nil {
		r.data.Teams = make([]domain.Team, 0, len(data.Teams))
		for _, team := range data.Teams {
			if scope.Teams[team.ID] {
				r.data.Teams = append(r.data.Teams, team)
			}
		}
	}
	if data.TeamMembers != nil {
		r.data.TeamMembers = make([]domain.TeamMember, len(data.TeamMembers))
		for index, member := range data.TeamMembers {
			r.data.TeamMembers[index] = domain.TeamMember{
				TeamID: member.TeamID, UserID: member.UserID, Role: member.Role,
			}
		}
	}
	r.links = NewDocumentLinkScope(r.data.Teams, r.data.TeamSettings)
	r.captured = true
	return nil
}

// This bounded-only predicate is the literal document closure in
// filterBootstrapTeams, inverted to report visibility. The original closure
// and its caller remain unchanged for legacy paths.
func bootstrapDocumentVisible(data *domain.Bootstrap, document domain.Document, scope bootstrapTeamScope, links *DocumentLinkScope) bool {
	allowed := scope.Content
	linkOpen := links.Open(document, func(teamID string) bool { return allowed[teamID] })
	if isWorkspaceAdminRole(data.ViewerRole) &&
		!slices.ContainsFunc(document.TeamIDs, func(teamID string) bool { return scope.AdminClosed[teamID] }) {
		return linkOpen || documentGrantAllows(data, document, allowed, false)
	}
	if len(document.TeamIDs) == 0 {
		if len(document.Permissions) == 0 {
			return linkOpen
		}
		return documentGrantAllows(data, document, allowed, linkOpen)
	}
	if slices.ContainsFunc(document.TeamIDs, func(teamID string) bool { return allowed[teamID] }) {
		return true
	}
	return documentGrantAllows(data, document, allowed, linkOpen)
}

func (r *CollaborationDocumentRead) collaborationProjection(ctx context.Context, userID string, key *domain.APIKey, capture *collaborationProjectionCapture) (domain.Bootstrap, AccessContext, error) {
	ctx = r.owner.metadataOutputContext(ctx)
	data, err := r.owner.installedResourcePolicy(ctx)
	if err != nil {
		return data, AccessContext{}, err
	}
	viewerField, _, _ := metadataLogicalField("viewer")
	viewer, err := r.owner.projectNode(ctx, r.owner.source.fields[viewerField],
		"ID", "App", "Active", "AppTeamIDs", "AppScopes")
	if err != nil {
		return data, AccessContext{}, err
	}
	data.Viewer = viewer.Interface().(domain.User)
	roleField, _, _ := metadataLogicalField("viewerRole")
	role, err := r.owner.node(ctx, r.owner.source.fields[roleField], false)
	if err != nil {
		return data, AccessContext{}, err
	}
	data.ViewerRole = role.String()
	access := AccessContext{}
	if userID == "" {
		err = r.owner.visitNative(ctx, "teamMembers", func(_ int, node metadataLogicalNode) error {
			value, err := r.owner.projectNode(ctx, node, "TeamID", "UserID", "Role")
			if err == nil {
				data.TeamMembers = append(data.TeamMembers, value.Interface().(domain.TeamMember))
			}
			return err
		})
	} else {
		principal := &DocumentRead{Data: data, owner: r.owner}
		if err = principal.documentProjectionWork(ctx, data.Workspace.ID, userID); err != nil {
			return data, access, err
		}
		memoContext, closeMemo := withResourcePolicyMemo(ctx, r.owner)
		defer closeMemo()
		access, err = r.owner.store.ResolveAccess(memoContext, r.workspace, userID, key)
		if err != nil {
			return data, access, err
		}
		var found bool
		projectionContext := context.WithValue(memoContext, collaborationProjectionCaptureKey{}, capture)
		data, found, err = r.owner.store.projectBootstrapForUser(projectionContext, data, userID)
		if err == nil && !found {
			err = ErrAuthForbidden
		}
		if err == nil {
			err = metadataOutputError(ctx)
		}
		if err == nil {
			if (&resourceAccessPolicy{key: key}).selected() {
				data.Viewer.AppTeamIDs, err = collaborationCloneIDs(ctx, r.owner, data.Viewer.AppTeamIDs)
			}
			if err == nil {
				collaborationCredentialPolicy(&data, key)
			}
		}
	}
	if err != nil {
		return data, access, err
	}
	for index, member := range data.TeamMembers {
		data.TeamMembers[index] = domain.TeamMember{
			TeamID: member.TeamID, UserID: member.UserID, Role: member.Role,
		}
	}
	// The live projector's unrelated member and preference outputs are not
	// socket inputs. Their allocations remain admitted until this owner closes.
	return domain.Bootstrap{
		Workspace: data.Workspace,
		Viewer: domain.User{
			ID: data.Viewer.ID, App: data.Viewer.App, Active: data.Viewer.Active,
			AppTeamIDs: data.Viewer.AppTeamIDs, AppScopes: data.Viewer.AppScopes,
		},
		ViewerRole: data.ViewerRole, ViewerTeamIDs: data.ViewerTeamIDs,
		Teams: data.Teams, TeamSettings: data.TeamSettings, TeamMembers: data.TeamMembers,
		Documents: data.Documents,
	}, access, nil
}

// This is the policy subset of filterBootstrapForAPIKey,
// applyAPIKeyProjection and redactAPIKeyReferences consumed by documentRole.
func collaborationCredentialPolicy(data *domain.Bootstrap, key *domain.APIKey) {
	if !(&resourceAccessPolicy{key: key}).selected() {
		return
	}
	allowed := func(id string) bool { return slices.Contains(key.TeamIDs, id) }
	data.Teams = slices.DeleteFunc(data.Teams, func(team domain.Team) bool { return !allowed(team.ID) })
	data.TeamMembers = slices.DeleteFunc(data.TeamMembers, func(member domain.TeamMember) bool { return !allowed(member.TeamID) })
	for id := range data.TeamSettings {
		if !allowed(id) {
			delete(data.TeamSettings, id)
		}
	}
	RedactUserTeams(&data.Viewer, allowed)
}

// Both selected-key document predicates precede the literal final redaction.
// The supplied scope is built once from the already key-filtered policy.
func collaborationCredentialDocument(document domain.Document, key *domain.APIKey, links *DocumentLinkScope) (domain.Document, bool) {
	if !(&resourceAccessPolicy{key: key}).selected() {
		return document, true
	}
	allowed := func(id string) bool { return slices.Contains(key.TeamIDs, id) }
	if !slices.ContainsFunc(document.TeamIDs, allowed) &&
		(key.Actor != "app" || len(document.TeamIDs) > 0 || !slices.ContainsFunc(document.InheritedTeamIDs, allowed)) {
		return domain.Document{}, false
	}
	if len(document.TeamIDs) > 0 {
		if !slices.ContainsFunc(document.TeamIDs, allowed) {
			return domain.Document{}, false
		}
	} else {
		closed := links.ClosedTeams(document)
		if len(closed) > 0 && !slices.ContainsFunc(closed, allowed) {
			return domain.Document{}, false
		}
	}
	document.TeamIDs = slices.DeleteFunc(document.TeamIDs, func(id string) bool { return !allowed(id) })
	document.InheritedTeamIDs = slices.DeleteFunc(document.InheritedTeamIDs, func(id string) bool { return !allowed(id) })
	document.Permissions = slices.DeleteFunc(document.Permissions, func(permission domain.DocumentPermission) bool {
		return permission.SubjectType == "team" && !allowed(permission.SubjectID)
	})
	return document, true
}

func collaborationNativeDocument(ctx context.Context, owner *ResourceRead, node metadataLogicalNode) (domain.Document, error) {
	value, err := owner.projectNode(ctx, node, "ID", "SlugID", "CollaborationID", "TeamIDs", "InheritedTeamIDs")
	if err != nil {
		return domain.Document{}, err
	}
	document := value.Interface().(domain.Document)
	err = node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		switch key.String() {
		case "Creator":
			value, err := owner.projectNode(ctx, child, "ID")
			if err == nil {
				document.Creator = value.Interface().(domain.User)
			}
			return err
		case "Permissions":
			absent, err := child.isNil(ctx)
			if err != nil {
				return err
			}
			if !absent {
				document.Permissions = []domain.DocumentPermission{}
			}
			return child.Children(ctx, func(_ reflect.Value, permission metadataLogicalNode) error {
				value, err := owner.projectNode(ctx, permission, "SubjectType", "SubjectID", "Role")
				if err == nil {
					document.Permissions = append(document.Permissions, value.Interface().(domain.DocumentPermission))
				}
				return err
			})
		}
		return nil
	})
	return document, err
}

func collaborationCloneIDs(ctx context.Context, owner *ResourceRead, ids []string) ([]string, error) {
	if ids == nil {
		return nil, nil
	}
	cost := uint64(4096)
	for _, id := range ids {
		if uint64(len(id))+32 > (metadataWorkBytes-cost)/8 {
			return nil, ErrMetadataWorkBudget
		}
		cost += (uint64(len(id)) + 32) * 8
	}
	if err := owner.reserve(ctx, cost); err != nil {
		return nil, err
	}
	result := make([]string, len(ids))
	for index, id := range ids {
		result[index] = strings.Clone(id)
	}
	return result, nil
}

func collaborationCallerDocument(ctx context.Context, owner *ResourceRead, source domain.Document) (domain.Document, error) {
	cost := uint64(4096)
	for _, value := range []string{source.ID, source.SlugID, source.CollaborationID, source.Creator.ID} {
		if uint64(len(value)) > (metadataWorkBytes-cost)/8 {
			return domain.Document{}, ErrMetadataWorkBudget
		}
		cost += uint64(len(value)) * 8
	}
	for _, permission := range source.Permissions {
		for _, value := range []string{permission.SubjectType, permission.SubjectID, permission.Role} {
			if uint64(len(value))+128 > (metadataWorkBytes-cost)/8 {
				return domain.Document{}, ErrMetadataWorkBudget
			}
			cost += (uint64(len(value)) + 128) * 8
		}
	}
	if err := owner.reserve(ctx, cost); err != nil {
		return domain.Document{}, err
	}
	document := domain.Document{
		ID: strings.Clone(source.ID), SlugID: strings.Clone(source.SlugID),
		CollaborationID: strings.Clone(source.CollaborationID),
		Creator:         domain.User{ID: strings.Clone(source.Creator.ID)},
	}
	var err error
	document.TeamIDs, err = collaborationCloneIDs(ctx, owner, source.TeamIDs)
	if err == nil {
		document.InheritedTeamIDs, err = collaborationCloneIDs(ctx, owner, source.InheritedTeamIDs)
	}
	if err != nil {
		return domain.Document{}, err
	}
	if source.Permissions != nil {
		document.Permissions = make([]domain.DocumentPermission, len(source.Permissions))
		for index, permission := range source.Permissions {
			document.Permissions[index] = domain.DocumentPermission{
				SubjectType: strings.Clone(permission.SubjectType),
				SubjectID:   strings.Clone(permission.SubjectID), Role: strings.Clone(permission.Role),
			}
		}
	}
	return document, nil
}

func collaborationCallerPolicy(ctx context.Context, owner *ResourceRead, source domain.Bootstrap) (domain.Bootstrap, error) {
	cost := uint64(4096)
	add := func(values ...string) error {
		for _, value := range values {
			if uint64(len(value))+1024 > (metadataWorkBytes-cost)/8 {
				return ErrMetadataWorkBudget
			}
			cost += (uint64(len(value)) + 1024) * 8
		}
		return nil
	}
	if err := add(source.Workspace.ID, source.Workspace.URLKey, source.Viewer.ID, source.ViewerRole); err != nil {
		return domain.Bootstrap{}, err
	}
	for _, team := range source.Teams {
		if err := add(team.ID, team.Key, team.ExternalSource); err != nil {
			return domain.Bootstrap{}, err
		}
	}
	for id, settings := range source.TeamSettings {
		if err := add(id, settings.ParentTeamID, settings.Access, settings.MembershipRestriction); err != nil {
			return domain.Bootstrap{}, err
		}
	}
	if err := owner.reserve(ctx, cost); err != nil {
		return domain.Bootstrap{}, err
	}
	data := domain.Bootstrap{
		Workspace:  domain.Workspace{ID: source.Workspace.ID, URLKey: source.Workspace.URLKey},
		Viewer:     domain.User{ID: source.Viewer.ID, App: source.Viewer.App, Active: source.Viewer.Active},
		ViewerRole: source.ViewerRole,
	}
	var err error
	data.Viewer.AppTeamIDs, err = collaborationCloneIDs(ctx, owner, source.Viewer.AppTeamIDs)
	if err == nil {
		data.Viewer.AppScopes, err = collaborationCloneIDs(ctx, owner, source.Viewer.AppScopes)
	}
	if err != nil {
		return domain.Bootstrap{}, err
	}
	if source.Teams != nil {
		data.Teams = make([]domain.Team, len(source.Teams))
		for index, team := range source.Teams {
			data.Teams[index] = domain.Team{
				ID: team.ID, Key: team.Key, ExternalSource: team.ExternalSource, Private: team.Private,
			}
			if team.ArchivedAt != nil {
				data.Teams[index].ArchivedAt = new(time.Time)
			}
			if team.RetiredAt != nil {
				data.Teams[index].RetiredAt = new(time.Time)
			}
		}
	}
	if source.TeamSettings != nil {
		data.TeamSettings = make(map[string]domain.TeamSettings, len(source.TeamSettings))
		for id, settings := range source.TeamSettings {
			data.TeamSettings[id] = domain.TeamSettings{
				ParentTeamID: settings.ParentTeamID, Access: settings.Access,
				MembershipRestriction: settings.MembershipRestriction,
			}
		}
	}
	return data, nil
}
