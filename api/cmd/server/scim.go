package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) listSCIMTokens(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkspaceAdmin(w, r) {
		return
	}
	data := s.workspaceData(r)
	items, err := s.store.ListSCIMTokens(r.Context(), data.Workspace.ID)
	respondMutation(w, err, http.StatusOK, items)
}

func (s *server) createSCIMToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkspaceAdmin(w, r) {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	data := s.workspaceData(r)
	item, err := s.store.CreateSCIMToken(r.Context(), data.Workspace.ID, input.Name)
	if err == nil {
		// Token issuance enables the SCIM surface for this workspace. The
		// plaintext secret is returned exactly once by CreateSCIMToken.
		err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "scim.enabled", "workspace", nil, func(next *domain.Bootstrap) error {
			next.WorkspaceSettings.SCIMEnabled = true
			if next.WorkspaceSettings.SCIMDefaultRole == "" {
				next.WorkspaceSettings.SCIMDefaultRole = "member"
			}
			return nil
		})
	}
	respondMutation(w, err, http.StatusCreated, item)
}

func (s *server) revokeSCIMToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkspaceAdmin(w, r) {
		return
	}
	data := s.workspaceData(r)
	err := s.store.RevokeSCIMToken(r.Context(), data.Workspace.ID, r.PathValue("id"))
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) requireWorkspaceAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.authDisabled {
		return true
	}
	role, _, err := s.store.WorkspaceRole(r.Context(), s.workspaceData(r).Workspace.ID, authUser(r).ID)
	if err != nil || !workspaceAdminRole(role) {
		writeError(w, http.StatusForbidden, "workspace administrator access required")
		return false
	}
	return true
}

func (s *server) authenticateSCIMRequest(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	header := strings.Fields(r.Header.Get("Authorization"))
	if len(header) != 2 || !strings.EqualFold(header[0], "Bearer") {
		w.Header().Set("WWW-Authenticate", `Bearer realm="Flow SCIM"`)
		writeSCIMError(w, http.StatusUnauthorized, "", "SCIM bearer token required")
		return "", "", false
	}
	secret := strings.TrimSpace(header[1])
	workspaceKey := strings.TrimSpace(r.PathValue("workspace"))
	workspaceID, err := s.store.AuthenticateSCIMToken(r.Context(), secret)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="Flow SCIM", error="invalid_token"`)
		writeSCIMError(w, http.StatusUnauthorized, "", "invalid SCIM bearer token")
		return "", "", false
	}
	data, ok := s.store.WorkspaceMetadata(workspaceKey)
	if !ok || data.Workspace.ID != workspaceID {
		writeSCIMError(w, http.StatusForbidden, "", "SCIM token is not valid for this workspace")
		return "", "", false
	}
	if !data.WorkspaceSettings.SCIMEnabled {
		writeSCIMError(w, http.StatusForbidden, "", "SCIM provisioning is disabled")
		return "", "", false
	}
	return workspaceKey, workspaceID, true
}

// writeSCIMStoreError maps store errors to SCIM status codes and types.
func writeSCIMStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrSCIMUniqueness):
		writeSCIMError(w, http.StatusConflict, scimUniqueness, err.Error())
	case errors.Is(err, store.ErrSCIMNotFound), errors.Is(err, sql.ErrNoRows):
		writeSCIMError(w, http.StatusNotFound, "", "resource not found")
	case errors.Is(err, store.ErrSCIMMemberNotFound), errors.Is(err, store.ErrSCIMInvalid):
		writeSCIMError(w, http.StatusBadRequest, scimInvalidValue, err.Error())
	default:
		writeSCIMRequestError(w, err)
	}
}

func scimTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func scimMeta(resourceType, location string, created, modified time.Time) map[string]any {
	meta := map[string]any{"resourceType": resourceType, "location": location}
	if stamp := scimTime(created); stamp != "" {
		meta["created"] = stamp
	}
	if stamp := scimTime(modified); stamp != "" {
		meta["lastModified"] = stamp
	}
	return meta
}

// scimUserResource is the User resource for a SCIM user, as a map so the
// same value is filtered, projected and written.
func scimUserResource(base string, item store.SCIMUser) map[string]any {
	name := map[string]any{"formatted": item.User.Name}
	if full := item.Name.FullName(); full != "" {
		name["formatted"] = full
	}
	if item.Name.GivenName != "" {
		name["givenName"] = item.Name.GivenName
	}
	if item.Name.FamilyName != "" {
		name["familyName"] = item.Name.FamilyName
	}
	resource := map[string]any{
		"schemas":     []string{scimSchemaUser},
		"id":          item.User.ID,
		"userName":    item.UserName,
		"name":        name,
		"displayName": item.User.DisplayName,
		"active":      item.User.Active,
		"meta":        scimMeta("User", base+"/Users/"+item.User.ID, item.CreatedAt, item.UpdatedAt),
	}
	if !item.ExternalIDDefaulted && item.ExternalID != "" {
		resource["externalId"] = item.ExternalID
	}
	if item.User.Email != "" {
		resource["emails"] = []map[string]any{{"value": item.User.Email, "type": "work", "primary": true}}
	}
	return resource
}

// scimFilterable converts a resource to plain JSON values (slices of maps
// become []any) for filter evaluation.
func scimFilterable(resource map[string]any) map[string]any {
	raw, _ := json.Marshal(resource)
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	return result
}

var scimUserPushdown = map[string]string{"username": "userName", "externalid": "externalId", "id": "id", "emails.value": "email", "displayname": "displayName"}

// listSCIMUserResources returns one page of users matching filter and the
// total match count. An equality on userName, externalId, id, email or
// displayName is answered from the store's indexes; other filters scan the
// workspace in batches (bounded by scimFilterScanLimit).
func (s *server) listSCIMUserResources(ctx context.Context, workspaceID, base string, filter scimFilter, params scimListParams) ([]map[string]any, int, error) {
	if filter == nil {
		items, total, err := s.store.ListSCIMUsers(ctx, workspaceID, store.SCIMUserQuery{Offset: params.startIndex - 1, Limit: params.count})
		resources := make([]map[string]any, 0, len(items))
		for _, item := range items {
			resources = append(resources, scimUserResource(base, item))
		}
		return resources, total, err
	}
	query := store.SCIMUserQuery{Limit: 1000}
	keys := make([]string, 0, len(scimUserPushdown))
	for key := range scimUserPushdown {
		keys = append(keys, key)
	}
	if attribute, value, ok := scimEqualityPushdown(filter, keys...); ok {
		query.Attribute, query.Value = scimUserPushdown[attribute], value
	}
	resources := []map[string]any{}
	matched := 0
	for {
		items, total, err := s.store.ListSCIMUsers(ctx, workspaceID, query)
		if err != nil {
			return nil, 0, err
		}
		if query.Attribute == "" && total > scimFilterScanLimit {
			return nil, 0, &scimRequestError{status: http.StatusBadRequest, scimType: scimTooMany, detail: "filter would scan too many users; filter by userName, externalId, id, emails.value or displayName with eq"}
		}
		for _, item := range items {
			resource := scimUserResource(base, item)
			if !filter.matches(scimFilterable(resource)) {
				continue
			}
			matched++
			if matched >= params.startIndex && len(resources) < params.count {
				resources = append(resources, resource)
			}
		}
		query.Offset += len(items)
		if len(items) == 0 || query.Offset >= total {
			return resources, matched, nil
		}
	}
}

func (s *server) scimUsers(w http.ResponseWriter, r *http.Request) {
	workspaceKey, workspaceID, ok := s.authenticateSCIMRequest(w, r)
	if !ok {
		return
	}
	base := scimBaseURL(r, workspaceKey)
	if r.Method == http.MethodGet {
		filter, err := parseSCIMFilter(r.URL.Query().Get("filter"))
		if err != nil {
			writeSCIMRequestError(w, err)
			return
		}
		params := readSCIMListParams(r)
		resources, total, err := s.listSCIMUserResources(r.Context(), workspaceID, base, filter, params)
		if err != nil {
			writeSCIMRequestError(w, err)
			return
		}
		projection := readSCIMProjection(r)
		for index := range resources {
			resources[index] = projection.apply(resources[index])
		}
		writeSCIM(w, http.StatusOK, scimListResponse(total, params, resources))
		return
	}
	var payload scimUserPayload
	if !decodeSCIM(w, r, &payload) {
		return
	}
	input, err := payload.input(nil)
	if err != nil {
		writeSCIMRequestError(w, err)
		return
	}
	input.Role, input.RoleExplicit = s.scimRole(workspaceKey, payload.role()), payload.role() != ""
	item, err := s.store.CreateSCIMUser(r.Context(), workspaceID, input)
	if err != nil {
		writeSCIMStoreError(w, err)
		return
	}
	resource := scimUserResource(base, item)
	w.Header().Set("Location", base+"/Users/"+item.User.ID)
	writeSCIM(w, http.StatusCreated, readSCIMProjection(r).apply(resource))
}

func (s *server) scimUser(w http.ResponseWriter, r *http.Request) {
	workspaceKey, workspaceID, ok := s.authenticateSCIMRequest(w, r)
	if !ok {
		return
	}
	base := scimBaseURL(r, workspaceKey)
	current, err := s.store.SCIMUser(r.Context(), workspaceID, r.PathValue("id"))
	if err != nil {
		writeSCIMStoreError(w, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeSCIM(w, http.StatusOK, readSCIMProjection(r).apply(scimUserResource(base, current)))
		return
	case http.MethodDelete:
		groups, err := s.store.DeprovisionSCIMUser(r.Context(), workspaceID, current.User.ID)
		if err != nil {
			writeSCIMStoreError(w, err)
			return
		}
		for _, groupID := range groups {
			group, err := s.store.SCIMGroup(r.Context(), workspaceID, groupID)
			if err != nil {
				continue
			}
			if teamID := s.scimTeamForGroup(workspaceKey, group); teamID != "" {
				if err := s.syncSCIMGroupTeam(r, workspaceKey, workspaceID, group, teamID); err != nil {
					writeSCIMRequestError(w, err)
					return
				}
			}
		}
		writeSCIM(w, http.StatusNoContent, nil)
		return
	}
	var input store.SCIMUserInput
	if r.Method == http.MethodPatch {
		var patch scimPatchRequest
		if !decodeSCIM(w, r, &patch) {
			return
		}
		state := scimUserStateFrom(current)
		for _, operation := range patch.Operations {
			if err := state.apply(operation); err != nil {
				writeSCIMRequestError(w, err)
				return
			}
		}
		input = state.input
		if state.roleValue != nil {
			input.Role, input.RoleExplicit = s.scimRole(workspaceKey, *state.roleValue), true
		}
	} else {
		var payload scimUserPayload
		if !decodeSCIM(w, r, &payload) {
			return
		}
		input, err = payload.input(&current)
		if err != nil {
			writeSCIMRequestError(w, err)
			return
		}
		if role := payload.role(); role != "" {
			input.Role, input.RoleExplicit = s.scimRole(workspaceKey, role), true
		}
	}
	item, err := s.store.UpdateSCIMUser(r.Context(), workspaceID, current.User.ID, input)
	if err != nil {
		writeSCIMStoreError(w, err)
		return
	}
	writeSCIM(w, http.StatusOK, readSCIMProjection(r).apply(scimUserResource(base, item)))
}

// Groups.

func scimGroupResourceFromStore(base string, group store.SCIMGroup, withMembers bool) map[string]any {
	resource := map[string]any{
		"schemas":     []string{scimSchemaGroup},
		"id":          group.ID,
		"displayName": group.DisplayName,
		"meta":        scimMeta("Group", base+"/Groups/"+group.ID, group.CreatedAt, group.UpdatedAt),
	}
	if group.ExternalID != "" {
		resource["externalId"] = group.ExternalID
	}
	if withMembers {
		members := make([]map[string]any, 0, len(group.Members))
		for _, member := range group.Members {
			members = append(members, map[string]any{"value": member.UserID, "$ref": base + "/Users/" + member.UserID})
		}
		resource["members"] = members
	}
	return resource
}

// scimTeamResource presents a team that no SCIM group is linked to yet, so
// an IdP can find it by displayName and link it: the first write to it
// creates a SCIM group with the team id as externalId.
func scimTeamResource(base string, team domain.Team, members []domain.TeamMember, withMembers bool) map[string]any {
	resource := map[string]any{
		"schemas":     []string{scimSchemaGroup},
		"id":          team.ID,
		"displayName": team.Name,
		"meta":        map[string]any{"resourceType": "Group", "location": base + "/Groups/" + team.ID},
	}
	if withMembers {
		values := []map[string]any{}
		for _, member := range members {
			if member.TeamID == team.ID {
				values = append(values, map[string]any{"value": member.UserID, "$ref": base + "/Users/" + member.UserID})
			}
		}
		resource["members"] = values
	}
	return resource
}

// unlinkedSCIMTeams returns workspace teams no SCIM group is linked to.
func (s *server) unlinkedSCIMTeams(workspaceKey string, groups []store.SCIMGroup) []domain.Team {
	data, ok := s.store.WorkspaceMetadata(workspaceKey)
	if !ok {
		return nil
	}
	linked := map[string]bool{}
	for _, group := range groups {
		if teamID := s.scimTeamForGroup(workspaceKey, group); teamID != "" {
			linked[teamID] = true
		}
	}
	teams := []domain.Team{}
	for _, team := range data.Teams {
		if !linked[team.ID] {
			teams = append(teams, team)
		}
	}
	return teams
}

func (s *server) scimGroups(w http.ResponseWriter, r *http.Request) {
	workspaceKey, workspaceID, ok := s.authenticateSCIMRequest(w, r)
	if !ok {
		return
	}
	base := scimBaseURL(r, workspaceKey)
	if r.Method == http.MethodPost {
		var input scimGroupPayload
		if !decodeSCIM(w, r, &input) {
			return
		}
		role := scimGroupRole(s.scimWorkspaceSettings(workspaceKey), input.DisplayName)
		group, err := s.store.CreateSCIMGroup(r.Context(), workspaceID, input.ExternalID, input.DisplayName, role)
		if err != nil {
			writeSCIMStoreError(w, err)
			return
		}
		if group, err = s.store.ReplaceSCIMGroupMembers(r.Context(), workspaceID, group.ID, input.memberValues()); err != nil {
			_ = s.store.DeleteSCIMGroup(r.Context(), workspaceID, group.ID)
			writeSCIMStoreError(w, err)
			return
		}
		if err := s.syncSCIMGroupTeamIfLinked(r, workspaceKey, workspaceID, group); err != nil {
			writeSCIMRequestError(w, err)
			return
		}
		w.Header().Set("Location", base+"/Groups/"+group.ID)
		writeSCIM(w, http.StatusCreated, readSCIMProjection(r).apply(scimGroupResourceFromStore(base, group, true)))
		return
	}
	filter, err := parseSCIMFilter(r.URL.Query().Get("filter"))
	if err != nil {
		writeSCIMRequestError(w, err)
		return
	}
	params, projection := readSCIMListParams(r), readSCIMProjection(r)
	withMembers := projection.wants("members") || strings.Contains(strings.ToLower(r.URL.Query().Get("filter")), "members")
	groups, err := s.store.ListSCIMGroups(r.Context(), workspaceID, withMembers)
	if err != nil {
		writeSCIMRequestError(w, err)
		return
	}
	var teamMembers []domain.TeamMember
	if withMembers {
		if teamMembers, err = s.store.ListTeamMembers(r.Context(), workspaceID); err != nil {
			writeSCIMRequestError(w, err)
			return
		}
	}
	all := make([]map[string]any, 0, len(groups))
	for _, group := range groups {
		all = append(all, scimGroupResourceFromStore(base, group, withMembers))
	}
	for _, team := range s.unlinkedSCIMTeams(workspaceKey, groups) {
		all = append(all, scimTeamResource(base, team, teamMembers, withMembers))
	}
	resources := []map[string]any{}
	total := 0
	for _, resource := range all {
		if filter != nil && !filter.matches(scimFilterable(resource)) {
			continue
		}
		total++
		if total >= params.startIndex && len(resources) < params.count {
			resources = append(resources, projection.apply(resource))
		}
	}
	writeSCIM(w, http.StatusOK, scimListResponse(total, params, resources))
}

func (s *server) syncSCIMGroupTeamIfLinked(r *http.Request, workspaceKey, workspaceID string, group store.SCIMGroup) error {
	if teamID := s.scimTeamForGroup(workspaceKey, group); teamID != "" {
		return s.syncSCIMGroupTeam(r, workspaceKey, workspaceID, group, teamID)
	}
	return nil
}

// scimGroupOrTeam resolves a group id. A team that no group is linked to
// yet resolves to the team; with link set, a SCIM group linked to it is
// created first.
func (s *server) scimGroupOrTeam(ctx context.Context, workspaceKey, workspaceID, id string, link bool) (store.SCIMGroup, *domain.Team, error) {
	group, err := s.store.SCIMGroup(ctx, workspaceID, id)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return group, nil, err
	}
	data, ok := s.store.WorkspaceMetadata(workspaceKey)
	if !ok {
		return store.SCIMGroup{}, nil, err
	}
	index := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == id })
	if index < 0 {
		return store.SCIMGroup{}, nil, err
	}
	team := data.Teams[index]
	if !link {
		return store.SCIMGroup{}, &team, nil
	}
	group, err = s.store.CreateSCIMGroup(ctx, workspaceID, team.ID, team.Name, scimGroupRole(data.WorkspaceSettings, team.Name))
	return group, nil, err
}

func (s *server) scimGroup(w http.ResponseWriter, r *http.Request) {
	workspaceKey, workspaceID, ok := s.authenticateSCIMRequest(w, r)
	if !ok {
		return
	}
	base := scimBaseURL(r, workspaceKey)
	projection := readSCIMProjection(r)
	writes := r.Method == http.MethodPatch || r.Method == http.MethodPut
	group, team, err := s.scimGroupOrTeam(r.Context(), workspaceKey, workspaceID, r.PathValue("id"), writes)
	if err != nil {
		writeSCIMStoreError(w, err)
		return
	}
	switch {
	case r.Method == http.MethodGet && team != nil:
		var members []domain.TeamMember
		if projection.wants("members") {
			if members, err = s.store.ListTeamMembers(r.Context(), workspaceID); err != nil {
				writeSCIMRequestError(w, err)
				return
			}
		}
		writeSCIM(w, http.StatusOK, projection.apply(scimTeamResource(base, *team, members, projection.wants("members"))))
		return
	case team != nil:
		writeSCIMError(w, http.StatusNotFound, "", "resource not found")
		return
	case r.Method == http.MethodGet:
		writeSCIM(w, http.StatusOK, projection.apply(scimGroupResourceFromStore(base, group, projection.wants("members"))))
		return
	case r.Method == http.MethodDelete:
		if err := s.store.DeleteSCIMGroup(r.Context(), workspaceID, group.ID); err != nil {
			writeSCIMStoreError(w, err)
			return
		}
		writeSCIM(w, http.StatusNoContent, nil)
		return
	}
	var change scimGroupChange
	if r.Method == http.MethodPatch {
		var patch scimPatchRequest
		if !decodeSCIM(w, r, &patch) {
			return
		}
		change, err = s.scimGroupPatchChange(r.Context(), workspaceID, group, patch)
		if err != nil {
			writeSCIMStoreError(w, err)
			return
		}
	} else {
		var payload scimGroupPayload
		if !decodeSCIM(w, r, &payload) {
			return
		}
		change = scimGroupChange{displayName: payload.DisplayName, externalID: payload.ExternalID, members: payload.memberValues(), replaceMembers: true}
	}
	updated, err := s.applySCIMGroupChange(r, workspaceKey, workspaceID, group, change)
	if err != nil {
		writeSCIMStoreError(w, err)
		return
	}
	writeSCIM(w, http.StatusOK, projection.apply(scimGroupResourceFromStore(base, updated, true)))
}

// scimGroupChange is the net effect of a PUT or a PATCH on a group.
type scimGroupChange struct {
	displayName, externalID string
	members                 []string // replacement list when replaceMembers
	replaceMembers          bool
	add, remove             []string
}

func (s *server) applySCIMGroupChange(r *http.Request, workspaceKey, workspaceID string, group store.SCIMGroup, change scimGroupChange) (store.SCIMGroup, error) {
	ctx := r.Context()
	displayName := group.DisplayName
	if strings.TrimSpace(change.displayName) != "" {
		displayName = strings.TrimSpace(change.displayName)
	}
	role := scimGroupRole(s.scimWorkspaceSettings(workspaceKey), displayName)
	var err error
	if displayName != group.DisplayName || role != group.Role || (change.externalID != "" && change.externalID != group.ExternalID) {
		if group, err = s.store.UpdateSCIMGroup(ctx, workspaceID, group.ID, change.externalID, displayName, role); err != nil {
			return store.SCIMGroup{}, err
		}
	}
	switch {
	case change.replaceMembers:
		group, err = s.store.ReplaceSCIMGroupMembers(ctx, workspaceID, group.ID, change.members)
	case len(change.add) > 0 || len(change.remove) > 0:
		group, err = s.store.PatchSCIMGroupMembers(ctx, workspaceID, group.ID, change.add, change.remove)
	}
	if err != nil {
		return store.SCIMGroup{}, err
	}
	return group, s.syncSCIMGroupTeamIfLinked(r, workspaceKey, workspaceID, group)
}

func (s *server) scimWorkspaceSettings(workspaceKey string) domain.WorkspaceSettings {
	data, _ := s.store.WorkspaceMetadata(workspaceKey)
	return data.WorkspaceSettings
}

// scimTeamForGroup resolves an explicit workspace mapping, or a group whose
// externalId is the Flow team ID/key. Display names are not matched implicitly
// because IdP group labels are not guaranteed to be unique.
func (s *server) scimTeamForGroup(workspaceKey string, group store.SCIMGroup) string {
	data, ok := s.store.WorkspaceMetadata(workspaceKey)
	if !ok {
		return ""
	}
	for teamID, groupName := range data.WorkspaceSettings.SCIMTeamGroupMapping {
		if strings.EqualFold(strings.TrimSpace(groupName), group.DisplayName) || strings.EqualFold(strings.TrimSpace(groupName), group.ExternalID) {
			if slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID }) {
				return teamID
			}
		}
	}
	for _, team := range data.Teams {
		if strings.EqualFold(team.ID, group.ExternalID) || strings.EqualFold(team.Key, group.ExternalID) {
			return team.ID
		}
	}
	return ""
}

func scimGroupRole(settings domain.WorkspaceSettings, displayName string) string {
	name := strings.ToLower(strings.TrimSpace(displayName))
	for role, configured := range settings.SCIMRoleGroups {
		if strings.TrimSpace(configured) != "" && strings.EqualFold(strings.TrimSpace(configured), strings.TrimSpace(displayName)) {
			return strings.ToLower(strings.TrimSpace(role))
		}
	}
	switch name {
	case "flow-owners":
		return "owner"
	case "flow-admins":
		return "admin"
	case "flow-guests":
		return "guest"
	default:
		return ""
	}
}

func (s *server) scimRole(workspaceKey, value string) string {
	data, _ := s.store.WorkspaceMetadata(workspaceKey)
	settings := data.WorkspaceSettings
	value = strings.TrimSpace(value)
	if mapped := settings.SCIMRoleMapping[strings.ToLower(value)]; mapped != "" {
		value = mapped
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if value != "owner" && value != "admin" && value != "member" && value != "guest" {
		value = strings.ToLower(strings.TrimSpace(settings.SCIMDefaultRole))
	}
	if value != "owner" && value != "admin" && value != "guest" {
		value = "member"
	}
	return value
}
