package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
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
	if err := checkTextLength("name", strings.TrimSpace(input.Name), 191); err != nil { // scim_tokens.name
		writeError(w, http.StatusBadRequest, err.Error())
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
			appendAudit(next, "created", "scim_token", item.ID, map[string]any{"name": item.Name})
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
	if err == nil {
		auditCtx := store.WithMetadataFields(r.Context(), "auditLog")
		if auditErr := s.store.MutateWorkspace(auditCtx, workspaceKey(r), "scim.token_revoked", r.PathValue("id"), nil, func(next *domain.Bootstrap) error {
			appendAudit(next, "revoked", "scim_token", r.PathValue("id"), nil)
			return nil
		}); auditErr != nil {
			log.Printf("SCIM token audit: %v", auditErr)
		}
	}
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
	// name is what the IdP wrote: the formatted name, else given and family
	// name.
	name := map[string]any{}
	if full := item.Name.FullName(); full != "" {
		name["formatted"] = full
	}
	if item.Name.GivenName != "" {
		name["givenName"] = item.Name.GivenName
	}
	if item.Name.FamilyName != "" {
		name["familyName"] = item.Name.FamilyName
	}
	for key, value := range map[string]string{"middleName": item.Name.MiddleName, "honorificPrefix": item.Name.HonorificPrefix, "honorificSuffix": item.Name.HonorificSuffix} {
		if value != "" {
			name[key] = value
		}
	}
	meta := scimMeta("User", base+"/Users/"+item.User.ID, item.CreatedAt, item.UpdatedAt)
	meta["version"] = scimUserVersion(item)
	resource := map[string]any{
		"schemas":  []string{scimSchemaUser},
		"id":       item.User.ID,
		"userName": item.UserName,
		"meta":     meta,
	}
	if !item.Profile.ActiveUnset {
		resource["active"] = item.Active()
	}
	if len(name) > 0 {
		resource["name"] = name
	}
	if !item.NoDisplayName && item.User.DisplayName != "" {
		resource["displayName"] = item.User.DisplayName
	}
	if !item.ExternalIDDefaulted && item.ExternalID != "" {
		resource["externalId"] = item.ExternalID
	}
	if emails := scimUserEmails(item); len(emails) > 0 {
		resource["emails"] = emails
	}
	profile := item.Profile
	for key, value := range map[string]string{"title": profile.Title, "nickName": profile.NickName, "userType": profile.UserType, "preferredLanguage": profile.PreferredLanguage, "locale": profile.Locale, "timezone": profile.Timezone} {
		if value != "" {
			resource[key] = value
		}
	}
	if len(profile.PhoneNumbers) > 0 {
		resource["phoneNumbers"] = scimObjects(profile.PhoneNumbers)
	}
	if len(profile.Addresses) > 0 {
		resource["addresses"] = scimObjects(profile.Addresses)
	}
	if !profile.Enterprise.IsZero() {
		resource[scimSchemaEnterpriseUser] = scimObject(profile.Enterprise)
		resource["schemas"] = []string{scimSchemaUser, scimSchemaEnterpriseUser}
	}
	return resource
}

// scimUserEmails is the emails list the IdP sent, with the account email (as
// Flow normalized it) in place of its entry; without a stored list, the
// account email as the primary work email.
func scimUserEmails(item store.SCIMUser) []map[string]any {
	if item.User.Email == "" {
		return nil
	}
	emails := []map[string]any{}
	found := false
	for _, email := range item.Profile.Emails {
		entry := scimObject(email)
		if strings.EqualFold(email.Value, item.User.Email) {
			entry["value"], found = item.User.Email, true
		}
		emails = append(emails, entry)
	}
	if !found {
		return []map[string]any{{"value": item.User.Email, "type": "work", "primary": true}}
	}
	return emails
}

// scimObject converts a struct to its JSON object form.
func scimObject(value any) map[string]any {
	raw, _ := json.Marshal(value)
	result := map[string]any{}
	_ = json.Unmarshal(raw, &result)
	return result
}

func scimObjects[T any](values []T) []map[string]any {
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		result = append(result, scimObject(value))
	}
	return result
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
//
// sortBy on userName, displayName, externalId, emails.value, id,
// meta.created or meta.lastModified is ordered in SQL; other attributes
// (name.familyName, title, the enterprise extension, …) are sorted in
// memory after the same bounded scan.
func (s *server) listSCIMUserResources(ctx context.Context, workspaceID, base string, filter scimFilter, params scimListParams) ([]map[string]any, int, error) {
	sortBy, sqlSort := scimUserSQLSort(params.sortPath)
	if filter == nil && sqlSort {
		items, total, err := s.store.ListSCIMUsers(ctx, workspaceID, store.SCIMUserQuery{SortBy: sortBy, Descending: params.descending, Offset: params.startIndex - 1, Limit: params.count})
		resources := make([]map[string]any, 0, len(items))
		for _, item := range items {
			resources = append(resources, scimUserResource(base, item))
		}
		return resources, total, err
	}
	query := store.SCIMUserQuery{Limit: 1000}
	if sqlSort {
		query.SortBy, query.Descending = sortBy, params.descending
	}
	keys := make([]string, 0, len(scimUserPushdown))
	for key := range scimUserPushdown {
		keys = append(keys, key)
	}
	if attribute, value, ok := scimEqualityPushdown(filter, keys...); ok {
		query.Attribute, query.Value = scimUserPushdown[attribute], value
	}
	// Without a SQL order the whole match set is sorted before paging.
	keepAll := !sqlSort
	resources := []map[string]any{}
	matched := 0
	for {
		items, total, err := s.store.ListSCIMUsers(ctx, workspaceID, query)
		if err != nil {
			return nil, 0, err
		}
		if query.Attribute == "" && total > scimFilterScanLimit {
			return nil, 0, &scimRequestError{status: http.StatusBadRequest, scimType: scimTooMany, detail: "filter or sort would scan too many users; filter by userName, externalId, id, emails.value or displayName with eq"}
		}
		for _, item := range items {
			resource := scimUserResource(base, item)
			if filter != nil && !filter.matches(scimFilterable(resource)) {
				continue
			}
			matched++
			if keepAll || matched >= params.startIndex && len(resources) < params.count {
				resources = append(resources, resource)
			}
		}
		query.Offset += len(items)
		if len(items) == 0 || query.Offset >= total {
			break
		}
	}
	if keepAll {
		sortSCIMResources(resources, params)
		resources = scimPage(resources, params)
	}
	return resources, matched, nil
}

// scimPage is one page (startIndex, count) of a complete, ordered result.
func scimPage(resources []map[string]any, params scimListParams) []map[string]any {
	start := min(params.startIndex-1, len(resources))
	return resources[start:min(start+params.count, len(resources))]
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
	input.KeepRemoved = s.scimDeprovisionAction(workspaceKey) == scimDeprovisionRemove
	item, err := s.store.CreateSCIMUser(r.Context(), workspaceID, input)
	if err != nil {
		writeSCIMStoreError(w, err)
		return
	}
	action := "provisioned"
	if item.Restored {
		action = "restored"
	}
	s.recordSCIMAudit(r, workspaceKey, []scimAuditEntry{scimUserAudit(action, item, map[string]any{"role": item.Role, "active": item.Active(), "linkedExistingAccount": item.Linked})})
	s.applySCIMProfileSettings(r.Context(), workspaceKey, item.User.ID, store.SCIMProfile{}, item.Profile)
	resource := scimUserResource(base, item)
	w.Header().Set("Location", base+"/Users/"+item.User.ID)
	writeSCIMResource(w, http.StatusCreated, readSCIMProjection(r).apply(resource), scimUserVersion(item))
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
	version := scimUserVersion(current)
	if r.Method == http.MethodGet {
		if !scimNotModified(w, r, version) {
			writeSCIMResource(w, http.StatusOK, readSCIMProjection(r).apply(scimUserResource(base, current)), version)
		}
		return
	}
	if scimPreconditionFailed(w, r, version) {
		return
	}
	mode := s.scimDeprovisionAction(workspaceKey)
	if r.Method == http.MethodDelete {
		if err := s.deprovisionSCIMUser(r, workspaceKey, workspaceID, current, mode); err != nil {
			writeSCIMStoreError(w, err)
			return
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
	input.KeepRemoved = mode == scimDeprovisionRemove
	before := s.scimMemberStates(r.Context(), workspaceID, current.User.ID)
	item, err := s.store.UpdateSCIMUser(r.Context(), workspaceID, current.User.ID, input)
	if err != nil {
		writeSCIMStoreError(w, err)
		return
	}
	entries := []scimAuditEntry{}
	changed := scimChangedAttributes(scimUserResource(base, current), scimUserResource(base, item))
	extra := map[string]any{}
	if item.Profile.ActiveUnset != current.Profile.ActiveUnset {
		// "active" is audited through suspend/restore; unsetting it changes
		// no membership state, so it is recorded here.
		changed = append(changed, "active")
		slices.Sort(changed)
		extra["activeUnset"], extra["active"] = item.Profile.ActiveUnset, item.Active()
	}
	if len(changed) > 0 {
		extra["changedFields"] = changed
		entries = append(entries, scimUserAudit("updated", item, extra))
	}
	switch {
	case current.Active() && !item.Active():
		removed := false
		if mode == scimDeprovisionRemove {
			if removed, err = s.removeSCIMMember(r, workspaceKey, workspaceID, item.User.ID); err != nil {
				writeSCIMStoreError(w, err)
				return
			}
			if item, err = s.store.SCIMUser(r.Context(), workspaceID, item.User.ID); err != nil {
				writeSCIMStoreError(w, err)
				return
			}
		}
		action := "suspended"
		if removed {
			action = "removed"
		}
		entries = append(entries, scimUserAudit(action, item, map[string]any{"deprovisionAction": mode}))
	case !current.Active() && item.Active():
		entries = append(entries, scimUserAudit("restored", item, map[string]any{"role": item.Role}))
		if _, wasMember := before[item.User.ID]; !wasMember {
			// Removed on deprovisioning: rejoin the teams linked to their groups.
			if err := s.resyncSCIMUserTeams(r, workspaceKey, workspaceID, item.User.ID); err != nil {
				writeSCIMRequestError(w, err)
				return
			}
		}
	}
	entries = append(entries, s.scimRoleChangeAudits(r.Context(), workspaceID, before, s.scimMemberStates(r.Context(), workspaceID, item.User.ID), nil)...)
	s.recordSCIMAudit(r, workspaceKey, entries)
	s.applySCIMProfileSettings(r.Context(), workspaceKey, item.User.ID, current.Profile, item.Profile)
	writeSCIMResource(w, http.StatusOK, readSCIMProjection(r).apply(scimUserResource(base, item)), scimUserVersion(item))
}

// deprovisionSCIMUser handles DELETE /Users/{id}: the member is suspended
// (default) or, with the workspace's deprovisioning set to remove, removed
// from the workspace. Either way the account and its work stay, the SCIM
// identity is kept as deleted (so creating the same externalId again
// restores the same account) and the user's SCIM group and linked team
// memberships end.
func (s *server) deprovisionSCIMUser(r *http.Request, workspaceKey, workspaceID string, current store.SCIMUser, mode string) error {
	groups, err := s.store.DeprovisionSCIMUser(r.Context(), workspaceID, current.User.ID)
	if err != nil {
		return err
	}
	for _, groupID := range groups {
		group, err := s.store.SCIMGroup(r.Context(), workspaceID, groupID)
		if err != nil {
			continue
		}
		if err := s.syncSCIMGroupTeamIfLinked(r, workspaceKey, workspaceID, group); err != nil {
			return err
		}
	}
	metadata := map[string]any{"deprovisionAction": mode, "scimGroupsLeft": len(groups)}
	if mode == scimDeprovisionRemove {
		removed, err := s.removeSCIMMember(r, workspaceKey, workspaceID, current.User.ID)
		if err != nil {
			return err
		}
		metadata["removedFromWorkspace"] = removed
	}
	s.recordSCIMAudit(r, workspaceKey, []scimAuditEntry{scimUserAudit("deprovisioned", current, metadata)})
	return nil
}

// Groups.

func scimGroupResourceFromStore(base string, group store.SCIMGroup, withMembers bool) map[string]any {
	meta := scimMeta("Group", base+"/Groups/"+group.ID, group.CreatedAt, group.UpdatedAt)
	meta["version"] = scimGroupVersion(group)
	resource := map[string]any{
		"schemas":     []string{scimSchemaGroup},
		"id":          group.ID,
		"displayName": group.DisplayName,
		"meta":        meta,
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
	resource["meta"].(map[string]any)["version"] = scimTeamVersion(team)
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
		roleUsers := s.scimRoleAffectedUsers(r.Context(), workspaceID, group, role, input.memberValues())
		beforeStates := s.scimMemberStates(r.Context(), workspaceID, roleUsers...)
		empty := group
		if group, err = s.store.ReplaceSCIMGroupMembers(r.Context(), workspaceID, group.ID, input.memberValues()); err != nil {
			_ = s.store.DeleteSCIMGroup(r.Context(), workspaceID, group.ID)
			writeSCIMStoreError(w, err)
			return
		}
		if err := s.syncSCIMGroupTeamIfLinked(r, workspaceKey, workspaceID, group); err != nil {
			writeSCIMRequestError(w, err)
			return
		}
		s.auditSCIMGroupWrite(r, workspaceKey, workspaceID, "created", &empty, &group, roleUsers, beforeStates)
		w.Header().Set("Location", base+"/Groups/"+group.ID)
		writeSCIMResource(w, http.StatusCreated, readSCIMProjection(r).apply(scimGroupResourceFromStore(base, group, true)), scimGroupVersion(group))
		return
	}
	filter, err := parseSCIMFilter(r.URL.Query().Get("filter"))
	if err != nil {
		writeSCIMRequestError(w, err)
		return
	}
	params, projection := readSCIMListParams(r), readSCIMProjection(r)
	matched, err := s.listSCIMGroupResources(r.Context(), workspaceKey, workspaceID, base, filter, r.URL.Query().Get("filter"), projection)
	if err != nil {
		writeSCIMRequestError(w, err)
		return
	}
	sortSCIMResources(matched, params)
	resources := scimPage(matched, params)
	for index := range resources {
		resources[index] = projection.apply(resources[index])
	}
	writeSCIM(w, http.StatusOK, scimListResponse(len(matched), params, resources))
}

// listSCIMGroupResources returns every group resource (SCIM groups and
// unlinked teams) that matches filter, unsorted. Members are loaded only
// when the projection or the raw filter needs them.
func (s *server) listSCIMGroupResources(ctx context.Context, workspaceKey, workspaceID, base string, filter scimFilter, rawFilter string, projection scimProjection) ([]map[string]any, error) {
	withMembers := projection.wants("members") || strings.Contains(strings.ToLower(rawFilter), "members")
	groups, err := s.store.ListSCIMGroups(ctx, workspaceID, withMembers)
	if err != nil {
		return nil, err
	}
	var teamMembers []domain.TeamMember
	if withMembers {
		if teamMembers, err = s.store.ListTeamMembers(ctx, workspaceID); err != nil {
			return nil, err
		}
	}
	all := make([]map[string]any, 0, len(groups))
	for _, group := range groups {
		all = append(all, scimGroupResourceFromStore(base, group, withMembers))
	}
	for _, team := range s.unlinkedSCIMTeams(workspaceKey, groups) {
		all = append(all, scimTeamResource(base, team, teamMembers, withMembers))
	}
	matched := []map[string]any{}
	for _, resource := range all {
		if filter == nil || filter.matches(scimFilterable(resource)) {
			matched = append(matched, resource)
		}
	}
	return matched, nil
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
	group, team, err := s.scimGroupOrTeam(r.Context(), workspaceKey, workspaceID, r.PathValue("id"), false)
	if err != nil {
		writeSCIMStoreError(w, err)
		return
	}
	linkedTeam := false
	if team != nil {
		version := scimTeamVersion(*team)
		switch {
		case r.Method == http.MethodGet:
			if scimNotModified(w, r, version) {
				return
			}
			var members []domain.TeamMember
			if projection.wants("members") {
				if members, err = s.store.ListTeamMembers(r.Context(), workspaceID); err != nil {
					writeSCIMRequestError(w, err)
					return
				}
			}
			writeSCIMResource(w, http.StatusOK, projection.apply(scimTeamResource(base, *team, members, projection.wants("members"))), version)
			return
		case !writes:
			writeSCIMError(w, http.StatusNotFound, "", "resource not found")
			return
		case scimPreconditionFailed(w, r, version):
			return
		}
		// The first write to a team links a SCIM group to it.
		if group, _, err = s.scimGroupOrTeam(r.Context(), workspaceKey, workspaceID, team.ID, true); err != nil {
			writeSCIMStoreError(w, err)
			return
		}
		linkedTeam = true
	} else {
		version := scimGroupVersion(group)
		if r.Method == http.MethodGet {
			if !scimNotModified(w, r, version) {
				writeSCIMResource(w, http.StatusOK, projection.apply(scimGroupResourceFromStore(base, group, projection.wants("members"))), version)
			}
			return
		}
		if scimPreconditionFailed(w, r, version) {
			return
		}
	}
	if r.Method == http.MethodDelete {
		roleUsers := s.scimRoleAffectedUsers(r.Context(), workspaceID, group, group.Role, nil)
		beforeStates := s.scimMemberStates(r.Context(), workspaceID, roleUsers...)
		if err := s.store.DeleteSCIMGroup(r.Context(), workspaceID, group.ID); err != nil {
			writeSCIMStoreError(w, err)
			return
		}
		s.auditSCIMGroupWrite(r, workspaceKey, workspaceID, "deleted", &group, nil, roleUsers, beforeStates)
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
	nextRole := group.Role
	if strings.TrimSpace(change.displayName) != "" {
		nextRole = scimGroupRole(s.scimWorkspaceSettings(workspaceKey), strings.TrimSpace(change.displayName))
	}
	roleUsers := s.scimRoleAffectedUsers(r.Context(), workspaceID, group, nextRole, append(append(slices.Clone(change.members), change.add...), change.remove...))
	beforeStates := s.scimMemberStates(r.Context(), workspaceID, roleUsers...)
	updated, err := s.applySCIMGroupChange(r, workspaceKey, workspaceID, group, change)
	if err != nil {
		writeSCIMStoreError(w, err)
		return
	}
	action := "updated"
	if linkedTeam {
		action = "linked"
	}
	s.auditSCIMGroupWrite(r, workspaceKey, workspaceID, action, &group, &updated, roleUsers, beforeStates)
	writeSCIMResource(w, http.StatusOK, projection.apply(scimGroupResourceFromStore(base, updated, true)), scimGroupVersion(updated))
}

// scimRoleAffectedUsers lists the users whose workspace role a write to a
// role group can change (its current members and the member values the
// write names), or none for a group that is not a role group before or
// after the write.
func (s *server) scimRoleAffectedUsers(ctx context.Context, workspaceID string, group store.SCIMGroup, nextRole string, values []string) []string {
	if group.Role == "" && nextRole == "" {
		return nil
	}
	ids := scimGroupMemberIDs(group.Members)
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	for _, value := range values {
		if item, err := s.store.SCIMUser(ctx, workspaceID, value); err == nil && !seen[item.User.ID] {
			seen[item.User.ID] = true
			ids = append(ids, item.User.ID)
		}
	}
	return ids
}

// auditSCIMGroupWrite audits a group create, link, update or delete: the
// group entry, its net member change and the role changes it caused.
func (s *server) auditSCIMGroupWrite(r *http.Request, workspaceKey, workspaceID, action string, before, after *store.SCIMGroup, roleUsers []string, beforeStates map[string]store.SCIMMemberState) {
	entries := []scimAuditEntry{}
	subject := before
	if after != nil {
		subject = after
	}
	metadata := scimGroupAuditSubject(*subject)
	if teamID := s.scimTeamForGroup(workspaceKey, *subject); teamID != "" {
		metadata["teamId"] = teamID
	}
	switch action {
	case "updated":
		changed := []string{}
		if before.DisplayName != after.DisplayName {
			changed = append(changed, "displayName")
		}
		if before.ExternalID != after.ExternalID {
			changed = append(changed, "externalId")
		}
		if before.Role != after.Role {
			changed = append(changed, "role")
			metadata["roleBefore"] = before.Role
		}
		if len(changed) > 0 {
			metadata["changedFields"] = changed
			entries = append(entries, scimAuditEntry{action: action, resourceType: scimAuditGroupResource, resourceID: subject.ID, metadata: metadata})
		}
	case "deleted":
		metadata["memberCount"] = len(before.Members)
		entries = append(entries, scimAuditEntry{action: action, resourceType: scimAuditGroupResource, resourceID: subject.ID, metadata: metadata})
	default: // created, linked
		entries = append(entries, scimAuditEntry{action: action, resourceType: scimAuditGroupResource, resourceID: subject.ID, metadata: metadata})
	}
	if after != nil {
		if entry, ok := scimGroupMemberAudit(*after, before.Members); ok {
			entries = append(entries, entry)
		}
	}
	if len(roleUsers) > 0 {
		afterStates := s.scimMemberStates(r.Context(), workspaceID, roleUsers...)
		entries = append(entries, s.scimRoleChangeAudits(r.Context(), workspaceID, beforeStates, afterStates, map[string]any{"scimGroupId": subject.ID, "scimGroupName": subject.DisplayName})...)
	}
	s.recordSCIMAudit(r, workspaceKey, entries)
}

// scimGroupChange is the net effect of a PUT or a PATCH on a group.
type scimGroupChange struct {
	displayName, externalID string
	clearExternalID         bool     // PATCH remove of externalId
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
	linkedTeam := s.scimTeamForGroup(workspaceKey, group)
	if change.clearExternalID && group.ExternalID != "" {
		if group, err = s.store.ClearSCIMGroupExternalID(ctx, workspaceID, group.ID); err != nil {
			return store.SCIMGroup{}, err
		}
	}
	if displayName != group.DisplayName || role != group.Role || (change.externalID != "" && change.externalID != group.ExternalID) {
		if group, err = s.store.UpdateSCIMGroup(ctx, workspaceID, group.ID, change.externalID, displayName, role); err != nil {
			return store.SCIMGroup{}, err
		}
	}
	if linkedTeam != "" && linkedTeam != s.scimTeamForGroup(workspaceKey, group) {
		// No longer linked to that team: its members stay as ordinary
		// members, as when the group is deleted.
		if err := s.store.DetachSCIMGroupTeams(ctx, workspaceID, group.ID); err != nil {
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
