package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// Team membership accountability: every add, removal, role change and origin
// change of a team membership is written to the audit log (resource type
// team_member, resource id the affected user, one entry per membership), and
// memberships carry an origin (manual, invitation, api, sync, scim) so a sync
// can remove only the members it added.
//
// Removal policy, the same on every path:
//   - A person removing someone in team settings (or the REST API without an
//     origin) may remove anyone except the last owner and SCIM-managed members.
//   - SCIM removes only memberships it added itself, and never an owner.
//   - Origin-scoped removals (DELETE ...?origin=sync, POST .../members/sync)
//     remove only memberships with a listed origin and never an owner; the
//     sync endpoint never removes manual members unless "manual" is listed.

const teamMemberAuditResource = "team_member"

// Audit sources (metadata.source).
const (
	teamMemberSourceManual     = "manual"
	teamMemberSourceAPI        = "api"
	teamMemberSourceAgent      = "agent"
	teamMemberSourceSync       = "sync"
	teamMemberSourceInvitation = "invitation"
	teamMemberSourceSCIM       = "scim"
)

// Audit reasons (metadata.reason) for changes that follow from another action.
const (
	teamMemberReasonWorkspaceMemberRemoved = "workspace_member_removed"
	teamMemberReasonInvitationAccepted     = "invitation_accepted"
	teamMemberReasonTeamCreated            = "team_created"
	teamMemberReasonSCIMGroupSync          = "scim_group_sync"
	teamMemberReasonTeamSync               = "team_member_sync"
)

// teamMemberAuditActor is who made a membership change and through what.
type teamMemberAuditActor struct {
	actor  domain.User
	bot    *domain.ActorBot
	source string
	meta   map[string]any
}

// requestTeamMemberActor attributes a request: a signed-in person (manual),
// an API key or OAuth application (api, naming the key), the in-app agent
// (agent), or any caller that declared origin "sync" (sync).
func requestTeamMemberActor(r *http.Request, declaredOrigin string) teamMemberAuditActor {
	user := authUser(r)
	actor := teamMemberAuditActor{actor: user, source: teamMemberSourceManual, meta: map[string]any{}}
	if user.Via != nil {
		via := *user.Via
		actor.bot = &via
	}
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		if key.ID == "" {
			// The in-app agent acts with a synthetic key on the person's behalf.
			actor.source = teamMemberSourceAgent
		} else {
			actor.source = teamMemberSourceAPI
			actor.meta["apiKeyId"] = key.ID
			if key.Name != "" {
				actor.meta["apiKeyName"] = key.Name
			}
			if key.OAuthClientID != "" {
				actor.meta["oauthClientId"] = key.OAuthClientID
			}
		}
	}
	if strings.EqualFold(strings.TrimSpace(declaredOrigin), domain.TeamMemberOriginSync) {
		actor.meta["authenticatedAs"] = actor.source
		actor.source = teamMemberSourceSync
	}
	return actor
}

// defaultTeamMemberOrigin is the origin of a membership a request adds
// without naming one: api for API keys and OAuth applications, otherwise
// manual.
func defaultTeamMemberOrigin(r *http.Request) string {
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok && key.ID != "" {
		return domain.TeamMemberOriginAPI
	}
	return domain.TeamMemberOriginManual
}

// requestedTeamMemberOrigin validates an origin a caller sets: manual, api
// or sync. Flow sets invitation and scim itself.
func requestedTeamMemberOrigin(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", domain.TeamMemberOriginManual, domain.TeamMemberOriginAPI, domain.TeamMemberOriginSync:
		return value, nil
	}
	return "", fmt.Errorf("%w: origin must be manual, api or sync", errInvalid)
}

// teamMemberOriginList parses a comma-separated or repeated origin filter.
func teamMemberOriginList(values []string) ([]string, error) {
	result := []string{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part == "" {
				continue
			}
			if !domain.ValidTeamMemberOrigin(part) {
				return nil, fmt.Errorf("%w: origin must be one of %s", errInvalid, strings.Join(domain.TeamMemberOrigins, ", "))
			}
			if !slices.Contains(result, part) {
				result = append(result, part)
			}
		}
	}
	return result, nil
}

// teamMemberNames resolves user and team display data for audit entries.
type teamMemberNames struct {
	users map[string]domain.User
	teams map[string]domain.Team
}

func (s *server) teamMemberNames(ctx context.Context, workspace string) teamMemberNames {
	names := teamMemberNames{users: map[string]domain.User{}, teams: map[string]domain.Team{}}
	data, ok := s.store.WorkspaceMetadataFields(workspace, "users", "teams")
	if !ok {
		return names
	}
	for _, team := range data.Teams {
		names.teams[team.ID] = team
	}
	for _, user := range data.Users {
		names.users[user.ID] = user
	}
	if !s.authDisabled {
		if members, err := s.store.ListMembers(ctx, data.Workspace.ID); err == nil {
			for _, member := range members {
				names.users[member.User.ID] = member.User
			}
		}
	}
	return names
}

func (names teamMemberNames) decorate(changes []domain.TeamMemberChange) {
	for index := range changes {
		if team, ok := names.teams[changes[index].TeamID]; ok && changes[index].TeamKey == "" {
			changes[index].TeamKey = team.Key
		}
		if user, ok := names.users[changes[index].UserID]; ok && changes[index].UserName == "" {
			changes[index].UserName = user.DisplayName
			if changes[index].UserName == "" {
				changes[index].UserName = user.Name
			}
		}
	}
}

// appendTeamMemberAudit prepends one audit entry per change (newest first,
// capped like appendAudit). Entry ids stay unique within the batch so the
// audit stream cursor orders them.
func appendTeamMemberAudit(data *domain.Bootstrap, actor teamMemberAuditActor, names teamMemberNames, reason string, changes []domain.TeamMemberChange, extra map[string]any) {
	if len(changes) == 0 {
		return
	}
	who := actor.actor
	if who.ID == "" {
		who = data.Viewer
	}
	now := time.Now().UTC()
	entries := make([]domain.AuditLogEntry, 0, len(changes))
	for _, change := range changes {
		metadata := map[string]any{
			"teamId": change.TeamID,
			"userId": change.UserID,
			"origin": domain.TeamMemberOriginOrDefault(change.Origin),
			"source": actor.source,
		}
		if change.TeamKey != "" {
			metadata["teamKey"] = change.TeamKey
		}
		if team, ok := names.teams[change.TeamID]; ok && team.Name != "" {
			metadata["teamName"] = team.Name
		}
		if change.UserName != "" {
			metadata["userName"] = change.UserName
		}
		if user, ok := names.users[change.UserID]; ok && user.Email != "" {
			metadata["userEmail"] = user.Email
		}
		if change.RoleBefore != "" {
			metadata["roleBefore"] = change.RoleBefore
		}
		if change.RoleAfter != "" {
			metadata["roleAfter"] = change.RoleAfter
		}
		if change.OriginBefore != "" {
			metadata["originBefore"] = change.OriginBefore
		}
		if change.Inherited {
			metadata["inherited"] = true
		}
		if reason != "" {
			metadata["reason"] = reason
		}
		if who.ID != "" && who.ID == change.UserID {
			metadata["selfService"] = true
		}
		for key, value := range actor.meta {
			metadata[key] = value
		}
		for key, value := range extra {
			metadata[key] = value
		}
		entries = append(entries, domain.AuditLogEntry{ID: idgen.New("audit_"), Actor: who, BotActor: actor.bot, Action: change.Action, ResourceType: teamMemberAuditResource, ResourceID: change.UserID, Metadata: metadata, CreatedAt: now})
	}
	// Newest first: the last change of the batch leads.
	slices.Reverse(entries)
	data.AuditLog = append(entries, data.AuditLog...)
	if len(data.AuditLog) > 1000 {
		data.AuditLog = data.AuditLog[:1000]
	}
}

// recordTeamMemberAudit writes the audit entries of changes already applied
// to the membership tables. A failure is logged: the membership change has
// been committed and must not be reported as failed.
func (s *server) recordTeamMemberAudit(ctx context.Context, workspace string, actor teamMemberAuditActor, reason string, changes []domain.TeamMemberChange, extra map[string]any) {
	if len(changes) == 0 || workspace == "" {
		return
	}
	names := s.teamMemberNames(ctx, workspace)
	names.decorate(changes)
	ctx = store.WithMetadataFields(context.WithoutCancel(ctx), "auditLog")
	err := s.store.MutateWorkspace(ctx, workspace, "team_member.updated", changes[0].TeamID, map[string]any{"auditEntries": len(changes)}, func(data *domain.Bootstrap) error {
		appendTeamMemberAudit(data, actor, names, reason, changes, extra)
		return nil
	})
	if err != nil {
		log.Printf("team member audit workspace=%s: %v", workspace, err)
	}
}

// developmentTeamMemberAudit audits AUTH_DISABLED membership edits made
// inside a workspace mutation (the members live in the workspace document).
func developmentTeamMemberAudit(data *domain.Bootstrap, r *http.Request, reason string, changes []domain.TeamMemberChange, extra map[string]any) {
	names := teamMemberNames{users: map[string]domain.User{}, teams: map[string]domain.Team{}}
	for _, team := range data.Teams {
		names.teams[team.ID] = team
	}
	for _, user := range data.Users {
		names.users[user.ID] = user
	}
	for _, member := range data.Members {
		names.users[member.User.ID] = member.User
	}
	names.decorate(changes)
	actor := requestTeamMemberActor(r, "")
	appendTeamMemberAudit(data, actor, names, reason, changes, extra)
}

// writeTeamMemberError maps membership errors, adding the membership's actual
// origin to an origin mismatch.
func writeTeamMemberError(w http.ResponseWriter, err error, kept *domain.TeamMemberKept) {
	var mismatch store.TeamMemberOriginMismatchError
	switch {
	case errors.As(err, &mismatch):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "TEAM_MEMBER_ORIGIN_MISMATCH", "origin": mismatch.Origin})
	case errors.Is(err, store.ErrTeamOwnerProtected):
		body := map[string]any{"error": err.Error(), "code": "TEAM_OWNER_PROTECTED", "role": "owner"}
		if kept != nil {
			body["origin"] = kept.Origin
		}
		writeJSON(w, http.StatusConflict, body)
	case errors.Is(err, store.ErrInvalidTeamMemberOrigin):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, errLastActiveTeam):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "LAST_ACTIVE_TEAM"})
	default:
		respondMutation(w, err, http.StatusNoContent, nil)
	}
}

// changeTeamMember applies one membership change for a request (team settings,
// REST, MCP), enforcing the owner-role rule, and audits it.
func (s *server) changeTeamMember(r *http.Request, workspace, workspaceID string, request store.TeamMembershipChangeRequest) (store.TeamMembershipChangeResult, error) {
	actorID := authUser(r).ID
	workspaceRole, _, _ := s.store.WorkspaceRole(r.Context(), workspaceID, actorID)
	actorTeamRole, _ := s.store.TeamRole(r.Context(), workspaceID, request.TeamID, actorID)
	target, isMember, err := s.store.TeamMembership(r.Context(), workspaceID, request.TeamID, request.UserID)
	if err != nil {
		return store.TeamMembershipChangeResult{}, fmt.Errorf("could not verify team membership: %w", err)
	}
	currentRole := ""
	if isMember {
		currentRole = target.Role
	}
	ownerRoleChange := request.Member && request.Role == "owner" && currentRole != "owner" || currentRole == "owner" && (!request.Member || request.Role != "owner")
	if ownerRoleChange && !workspaceAdminRole(workspaceRole) && actorTeamRole != "owner" && !request.KeepOwners {
		return store.TeamMembershipChangeResult{}, errTeamOwnerRequired
	}
	// The reference app: "You should be a member of at least one active team". Nobody takes
	// themselves out of their last active team; admins may still remove someone
	// else, as the reference app's team member list allows. The dry run keeps the store's
	// own refusals (origin mismatch, protected owners) first.
	if !request.Member && isMember && request.UserID == actorID {
		preview := request
		preview.DryRun = true
		planned, err := s.store.ChangeTeamMembership(r.Context(), preview)
		if err != nil {
			return planned, err
		}
		if len(planned.Changes) > 0 {
			last, err := s.store.LeavingLastActiveTeam(r.Context(), workspaceID, request.TeamID, actorID)
			if err != nil {
				return store.TeamMembershipChangeResult{}, fmt.Errorf("could not verify team membership: %w", err)
			}
			if last {
				return store.TeamMembershipChangeResult{}, errLastActiveTeam
			}
		}
	}
	result, err := s.store.ChangeTeamMembership(r.Context(), request)
	if err == nil && !request.DryRun {
		declared := request.Origin
		if len(request.RequireOrigins) == 1 {
			declared = request.RequireOrigins[0]
		}
		s.recordTeamMemberAudit(r.Context(), workspace, requestTeamMemberActor(r, declared), "", result.Changes, nil)
	}
	return result, err
}

var errTeamOwnerRequired = errors.New("Team owner access required to change owner roles")

var errLastActiveTeam = errors.New("You should be a member of at least one active team")

// teamForMemberRequest resolves {id} (team id or key) among the teams the
// caller can see, applying API key team restrictions.
func (s *server) teamForMemberRequest(w http.ResponseWriter, r *http.Request) (domain.Bootstrap, domain.Team, bool) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return data, domain.Team{}, false
	}
	id := r.PathValue("id")
	index := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == id })
	if index < 0 {
		index = slices.IndexFunc(data.Teams, func(team domain.Team) bool { return strings.EqualFold(team.Key, id) })
	}
	if index < 0 {
		writePublicRecordNotFound(w, "team")
		return data, domain.Team{}, false
	}
	return data, data.Teams[index], true
}

func (s *server) registerTeamMemberRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/teams/{id}/members", s.listTeamMemberships)
	mux.HandleFunc("PUT /api/teams/{id}/members/{userId}", s.putTeamMembership)
	mux.HandleFunc("DELETE /api/teams/{id}/members/{userId}", s.deleteTeamMembership)
	mux.HandleFunc("POST /api/teams/{id}/members/sync", s.syncTeamMemberships)
}

// GET /api/teams/{id}/members: the team's memberships with role and origin.
func (s *server) listTeamMemberships(w http.ResponseWriter, r *http.Request) {
	data, team, ok := s.teamForMemberRequest(w, r)
	if !ok {
		return
	}
	members := data.TeamMembers
	if !s.authDisabled {
		var err error
		if members, err = s.store.ListTeamMembers(r.Context(), data.Workspace.ID); err != nil {
			writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "Could not load team members")
			return
		}
	}
	result := []domain.TeamMember{}
	for _, member := range members {
		if member.TeamID == team.ID {
			member.Origin = domain.TeamMemberOriginOrDefault(member.Origin)
			if member.Managed {
				member.Origin = domain.TeamMemberOriginSCIM
			}
			result = append(result, member)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// PUT /api/teams/{id}/members/{userId}: add the user to the team or change
// their role. A given origin is stored (also on an existing membership).
func (s *server) putTeamMembership(w http.ResponseWriter, r *http.Request) {
	data, team, ok := s.teamForMemberRequest(w, r)
	if !ok {
		return
	}
	var input domain.TeamMemberInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Role == "" {
		input.Role = "member"
	}
	if input.Role != "member" && input.Role != "owner" {
		writeError(w, http.StatusBadRequest, "Team role must be owner or member")
		return
	}
	origin, err := requestedTeamMemberOrigin(input.Origin)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.authDisabled {
		writeError(w, http.StatusConflict, "Team member API requires authentication to be enabled")
		return
	}
	userID := r.PathValue("userId")
	request := store.TeamMembershipChangeRequest{WorkspaceID: data.Workspace.ID, TeamID: team.ID, UserID: userID, Member: true, Role: input.Role, Origin: origin, SetOrigin: origin != ""}
	if origin == "" {
		request.Origin = defaultTeamMemberOrigin(r)
	}
	if _, err := s.changeTeamMember(r, workspaceKey(r), data.Workspace.ID, request); err != nil {
		if errors.Is(err, errTeamOwnerRequired) {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		writeTeamMemberError(w, err, nil)
		return
	}
	member, found, err := s.store.TeamMembership(r.Context(), data.Workspace.ID, team.ID, userID)
	if err != nil || !found {
		writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "Could not load the team membership")
		return
	}
	writeJSON(w, http.StatusOK, member)
}

// DELETE /api/teams/{id}/members/{userId}[?origin=sync][&dryRun=true]:
// remove the user from the team. With origin the removal only happens when
// the membership has that origin (409 TEAM_MEMBER_ORIGIN_MISMATCH with the
// actual origin otherwise) and never removes an owner (409
// TEAM_OWNER_PROTECTED). dryRun writes nothing and returns what would be
// removed, with the reason a member would be kept.
func (s *server) deleteTeamMembership(w http.ResponseWriter, r *http.Request) {
	data, team, ok := s.teamForMemberRequest(w, r)
	if !ok {
		return
	}
	origins, err := teamMemberOriginList(r.URL.Query()["origin"])
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	dryRun, ok := queryBool(w, r, "dryRun")
	if !ok {
		return
	}
	if s.authDisabled {
		writeError(w, http.StatusConflict, "Team member API requires authentication to be enabled")
		return
	}
	request := store.TeamMembershipChangeRequest{WorkspaceID: data.Workspace.ID, TeamID: team.ID, UserID: r.PathValue("userId"), Member: false, Role: "member", RequireOrigins: origins, KeepOwners: len(origins) > 0, DryRun: dryRun}
	result, err := s.changeTeamMember(r, workspaceKey(r), data.Workspace.ID, request)
	if err != nil {
		if errors.Is(err, errTeamOwnerRequired) {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		writeTeamMemberError(w, err, result.Kept)
		return
	}
	names := s.teamMemberNames(r.Context(), workspaceKey(r))
	removal := domain.TeamMemberRemoval{DryRun: dryRun, Removed: result.Changes, Kept: []domain.TeamMemberKept{}}
	if removal.Removed == nil {
		removal.Removed = []domain.TeamMemberChange{}
	}
	names.decorate(removal.Removed)
	if result.Kept != nil {
		kept := *result.Kept
		kept.UserName = names.userName(kept.UserID)
		removal.Kept = append(removal.Kept, kept)
	}
	writeJSON(w, http.StatusOK, removal)
}

func (names teamMemberNames) userName(userID string) string {
	user := names.users[userID]
	if user.DisplayName != "" {
		return user.DisplayName
	}
	return user.Name
}

// POST /api/teams/{id}/members/sync: make the team match a directory list.
// Listed users missing from the team are added with origin (default sync);
// members not listed are removed only when their origin is in removeOrigins
// (default: the sync's own origin). Owners and SCIM-managed members are
// always kept, and manual members unless "manual" is listed explicitly.
// dryRun writes nothing.
func (s *server) syncTeamMemberships(w http.ResponseWriter, r *http.Request) {
	data, team, ok := s.teamForMemberRequest(w, r)
	if !ok {
		return
	}
	var input domain.TeamMemberSyncInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Members == nil {
		writeError(w, http.StatusBadRequest, "members is required (an empty list removes every member the sync may remove)")
		return
	}
	origin, err := requestedTeamMemberOrigin(input.Origin)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if origin == "" {
		origin = domain.TeamMemberOriginSync
	}
	removeOrigins, err := teamMemberOriginList(input.RemoveOrigins)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(removeOrigins) == 0 {
		removeOrigins = []string{origin}
	}
	if s.authDisabled {
		writeError(w, http.StatusConflict, "Team member API requires authentication to be enabled")
		return
	}
	workspace := workspaceKey(r)
	members, err := s.store.ListMembers(r.Context(), data.Workspace.ID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "Could not load workspace members")
		return
	}
	current, err := s.store.ListTeamMembers(r.Context(), data.Workspace.ID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "Could not load team members")
		return
	}
	result := domain.TeamMemberSyncResult{DryRun: input.DryRun, Added: []domain.TeamMemberChange{}, Removed: []domain.TeamMemberChange{}, Kept: []domain.TeamMemberKept{}, Skipped: []domain.TeamMemberKept{}}
	desired := map[string]bool{}
	for _, value := range input.Members {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		index := slices.IndexFunc(members, func(member domain.WorkspaceMember) bool {
			return member.Status == "active" && (member.User.ID == value || member.User.Email != "" && strings.EqualFold(member.User.Email, value))
		})
		if index < 0 {
			result.Skipped = append(result.Skipped, domain.TeamMemberKept{TeamID: team.ID, UserID: value, Reason: domain.TeamMemberSkippedUnknownUser})
			continue
		}
		desired[members[index].User.ID] = true
	}
	onTeam := map[string]domain.TeamMember{}
	for _, member := range current {
		if member.TeamID == team.ID {
			onTeam[member.UserID] = member
		}
	}
	actor := requestTeamMemberActor(r, origin)
	audit := func(changes []domain.TeamMemberChange) {
		if !input.DryRun {
			s.recordTeamMemberAudit(r.Context(), workspace, actor, teamMemberReasonTeamSync, changes, nil)
		}
	}
	addIDs := make([]string, 0, len(desired))
	for userID := range desired {
		if _, ok := onTeam[userID]; !ok {
			addIDs = append(addIDs, userID)
		}
	}
	slices.Sort(addIDs)
	for _, userID := range addIDs {
		changed, err := s.store.ChangeTeamMembership(r.Context(), store.TeamMembershipChangeRequest{WorkspaceID: data.Workspace.ID, TeamID: team.ID, UserID: userID, Member: true, Role: "member", Origin: origin, DryRun: input.DryRun})
		if err != nil {
			audit(append(slices.Clone(result.Added), result.Removed...))
			writeTeamMemberError(w, err, nil)
			return
		}
		result.Added = append(result.Added, changed.Changes...)
	}
	removeIDs := make([]string, 0, len(onTeam))
	for userID := range onTeam {
		if !desired[userID] {
			removeIDs = append(removeIDs, userID)
		}
	}
	slices.Sort(removeIDs)
	for _, userID := range removeIDs {
		changed, err := s.store.ChangeTeamMembership(r.Context(), store.TeamMembershipChangeRequest{WorkspaceID: data.Workspace.ID, TeamID: team.ID, UserID: userID, Member: false, Role: "member", RequireOrigins: removeOrigins, KeepOwners: true, DryRun: input.DryRun})
		if changed.Kept != nil {
			result.Kept = append(result.Kept, *changed.Kept)
			continue
		}
		if err != nil {
			audit(append(slices.Clone(result.Added), result.Removed...))
			writeTeamMemberError(w, err, nil)
			return
		}
		result.Removed = append(result.Removed, changed.Changes...)
	}
	names := s.teamMemberNames(r.Context(), workspace)
	names.decorate(result.Added)
	names.decorate(result.Removed)
	for _, list := range [][]domain.TeamMemberKept{result.Kept, result.Skipped} {
		for index := range list {
			list[index].UserName = names.userName(list[index].UserID)
		}
	}
	audit(append(slices.Clone(result.Added), result.Removed...))
	writeJSON(w, http.StatusOK, result)
}

// teamMemberDiff lists userID's membership changes between two member lists
// (AUTH_DISABLED edits of the workspace document). Memberships added on a
// team other than directTeamID are inherited parent-team memberships.
func teamMemberDiff(before, after []domain.TeamMember, userID, directTeamID string) []domain.TeamMemberChange {
	key := func(member domain.TeamMember) string { return member.TeamID + "\x00" + member.UserID }
	previous := map[string]domain.TeamMember{}
	for _, member := range before {
		if userID == "" || member.UserID == userID {
			previous[key(member)] = member
		}
	}
	changes := []domain.TeamMemberChange{}
	for _, member := range after {
		if userID != "" && member.UserID != userID {
			continue
		}
		origin := domain.TeamMemberOriginOrDefault(member.Origin)
		old, existed := previous[key(member)]
		delete(previous, key(member))
		if !existed {
			changes = append(changes, domain.TeamMemberChange{TeamID: member.TeamID, UserID: member.UserID, Action: domain.TeamMemberAdded, RoleAfter: member.Role, Origin: origin, Inherited: directTeamID != "" && member.TeamID != directTeamID})
			continue
		}
		if old.Role != member.Role {
			changes = append(changes, domain.TeamMemberChange{TeamID: member.TeamID, UserID: member.UserID, Action: domain.TeamMemberRoleChanged, RoleBefore: old.Role, RoleAfter: member.Role, Origin: origin})
		}
		if previousOrigin := domain.TeamMemberOriginOrDefault(old.Origin); previousOrigin != origin {
			changes = append(changes, domain.TeamMemberChange{TeamID: member.TeamID, UserID: member.UserID, Action: domain.TeamMemberOriginChanged, RoleBefore: old.Role, RoleAfter: member.Role, Origin: origin, OriginBefore: previousOrigin})
		}
	}
	for _, member := range before {
		if old, ok := previous[key(member)]; ok {
			delete(previous, key(member))
			changes = append(changes, domain.TeamMemberChange{TeamID: old.TeamID, UserID: old.UserID, Action: domain.TeamMemberRemoved, RoleBefore: old.Role, Origin: domain.TeamMemberOriginOrDefault(old.Origin)})
		}
	}
	return changes
}

// scimTeamMemberActor attributes SCIM provisioning changes to the SCIM
// connection (token id and name) that made them.
func (s *server) scimTeamMemberActor(r *http.Request, groupID, groupName string) teamMemberAuditActor {
	actor := teamMemberAuditActor{actor: domain.User{ID: "scim", Name: "SCIM", DisplayName: "SCIM provisioning"}, source: teamMemberSourceSCIM, meta: map[string]any{}}
	header := strings.Fields(r.Header.Get("Authorization"))
	if len(header) == 2 && strings.EqualFold(header[0], "Bearer") {
		if token, err := s.store.SCIMTokenIdentity(r.Context(), header[1]); err == nil {
			actor.meta["scimTokenId"] = token.ID
			actor.meta["scimTokenName"] = token.Name
			actor.bot = &domain.ActorBot{ID: token.ID, Type: "scim", Name: token.Name}
		}
	}
	if groupID != "" {
		actor.meta["scimGroupId"] = groupID
	}
	if groupName != "" {
		actor.meta["scimGroupName"] = groupName
	}
	return actor
}

// syncSCIMGroupTeam mirrors a SCIM group into its mapped team and audits
// the memberships SCIM added or removed.
func (s *server) syncSCIMGroupTeam(r *http.Request, workspace, workspaceID string, group store.SCIMGroup, teamID string) error {
	changes, err := s.store.SyncSCIMGroupTeamChanges(r.Context(), workspaceID, group.ID, teamID)
	if err == nil {
		s.recordTeamMemberAudit(r.Context(), workspace, s.scimTeamMemberActor(r, group.ID, group.DisplayName), teamMemberReasonSCIMGroupSync, changes, nil)
	}
	return err
}
