package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scimNameValue struct {
	Value   string `json:"value,omitempty"`
	Primary bool   `json:"primary,omitempty"`
}

type scimUserResource struct {
	Schemas     []string          `json:"schemas"`
	ID          string            `json:"id,omitempty"`
	ExternalID  string            `json:"externalId,omitempty"`
	UserName    string            `json:"userName"`
	DisplayName string            `json:"displayName,omitempty"`
	Active      bool              `json:"active"`
	Emails      []scimNameValue   `json:"emails,omitempty"`
	Meta        map[string]string `json:"meta,omitempty"`
}

type scimListResponse struct {
	Schemas      []string           `json:"schemas"`
	TotalResults int                `json:"totalResults"`
	StartIndex   int                `json:"startIndex"`
	ItemsPerPage int                `json:"itemsPerPage"`
	Resources    []scimUserResource `json:"Resources"`
}

type scimGroupResource struct {
	Schemas     []string          `json:"schemas"`
	ID          string            `json:"id,omitempty"`
	ExternalID  string            `json:"externalId,omitempty"`
	DisplayName string            `json:"displayName"`
	Members     []scimGroupMember `json:"members,omitempty"`
	Meta        map[string]string `json:"meta,omitempty"`
}

type scimGroupMember struct {
	Value string `json:"value"`
	Ref   string `json:"$ref,omitempty"`
}

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
	if !strings.EqualFold(data.WorkspaceSettings.Plan, "enterprise") {
		writeError(w, http.StatusForbidden, "SCIM provisioning requires an Enterprise workspace")
		return
	}
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
		writeError(w, http.StatusUnauthorized, "SCIM bearer token required")
		return "", "", false
	}
	secret := strings.TrimSpace(header[1])
	workspaceKey := strings.TrimSpace(r.PathValue("workspace"))
	workspaceID, err := s.store.AuthenticateSCIMToken(r.Context(), secret)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="Flow SCIM", error="invalid_token"`)
		writeError(w, http.StatusUnauthorized, "invalid SCIM bearer token")
		return "", "", false
	}
	data, ok := s.store.BootstrapFor(workspaceKey)
	if !ok || data.Workspace.ID != workspaceID {
		writeError(w, http.StatusForbidden, "SCIM token is not valid for this workspace")
		return "", "", false
	}
	if !data.WorkspaceSettings.SCIMEnabled {
		writeError(w, http.StatusForbidden, "SCIM provisioning is disabled")
		return "", "", false
	}
	if !strings.EqualFold(data.WorkspaceSettings.Plan, "enterprise") {
		writeError(w, http.StatusForbidden, "SCIM provisioning requires an Enterprise workspace")
		return "", "", false
	}
	return workspaceKey, workspaceID, true
}

func (s *server) scimServiceProviderConfig(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.authenticateSCIMRequest(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schemas":               []string{"urn:ietf:params:scim:schemas:core:2.0"},
		"patch":                 map[string]bool{"supported": true},
		"bulk":                  map[string]any{"supported": false, "maxOperations": 0, "maxPayloadSize": 0},
		"filter":                map[string]any{"supported": true, "maxResults": 1000},
		"changePassword":        map[string]bool{"supported": false},
		"sort":                  map[string]bool{"supported": false},
		"etag":                  map[string]bool{"supported": false},
		"authenticationSchemes": []map[string]string{{"type": "oauth2", "name": "SCIM bearer token", "description": "Bearer token issued by Flow"}},
	})
}

func scimResource(item store.SCIMUser) scimUserResource {
	resource := scimUserResource{Schemas: []string{"urn:ietf:params:scim:schemas:core:2.0:User"}, ID: item.User.ID, ExternalID: item.ExternalID, UserName: item.UserName, DisplayName: item.User.DisplayName, Active: item.User.Active, Meta: map[string]string{"resourceType": "User"}}
	if item.User.Email != "" {
		resource.Emails = []scimNameValue{{Value: item.User.Email, Primary: true}}
	}
	return resource
}

func (s *server) scimUsers(w http.ResponseWriter, r *http.Request) {
	workspaceKey, _, ok := s.authenticateSCIMRequest(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		start, _ := strconv.Atoi(r.URL.Query().Get("startIndex"))
		if start < 1 {
			start = 1
		}
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		filter := parseSCIMFilter(r.URL.Query().Get("filter"))
		items, total, err := s.store.ListSCIMUsers(r.Context(), s.store.MustWorkspaceID(workspaceKey), start-1, count, filter)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not list SCIM users")
			return
		}
		resources := make([]scimUserResource, 0, len(items))
		for _, item := range items {
			resources = append(resources, scimResource(item))
		}
		writeJSON(w, http.StatusOK, scimListResponse{Schemas: []string{"urn:ietf:params:scim:api:messages:2.0:ListResponse"}, TotalResults: total, StartIndex: start, ItemsPerPage: len(resources), Resources: resources})
		return
	}
	var input struct {
		ExternalID  string          `json:"externalId"`
		UserName    string          `json:"userName"`
		DisplayName string          `json:"displayName"`
		Active      *bool           `json:"active"`
		Emails      []scimNameValue `json:"emails"`
		Roles       []struct {
			Value   string `json:"value"`
			Display string `json:"display"`
		} `json:"roles"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	active := true
	if input.Active != nil {
		active = *input.Active
	}
	role := s.scimRole(workspaceKey, firstSCIMRole(input.Roles))
	email := ""
	if len(input.Emails) > 0 {
		email = input.Emails[0].Value
	}
	item, err := s.store.ProvisionSCIMUser(r.Context(), s.store.MustWorkspaceID(workspaceKey), input.ExternalID, input.UserName, input.DisplayName, email, "", role, active)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.Header().Set("Location", "/scim/v2/"+workspaceKey+"/Users/"+item.User.ID)
	writeJSON(w, http.StatusCreated, scimResource(item))
}

func (s *server) scimUser(w http.ResponseWriter, r *http.Request) {
	workspaceKey, _, ok := s.authenticateSCIMRequest(w, r)
	if !ok {
		return
	}
	workspaceID := s.store.MustWorkspaceID(workspaceKey)
	externalID := r.PathValue("id")
	if r.Method == http.MethodGet {
		item, err := s.store.SCIMUser(r.Context(), workspaceID, externalID)
		if err != nil {
			writeError(w, http.StatusNotFound, "SCIM user not found")
			return
		}
		writeJSON(w, http.StatusOK, scimResource(item))
		return
	}
	if r.Method == http.MethodDelete {
		err := s.store.DeprovisionSCIMUser(r.Context(), workspaceID, externalID)
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	current, err := s.store.SCIMUser(r.Context(), workspaceID, externalID)
	if err != nil {
		writeError(w, http.StatusNotFound, "SCIM user not found")
		return
	}
	username, displayName, email, active := current.UserName, current.User.DisplayName, current.User.Email, current.User.Active
	role := s.scimRole(workspaceKey, "")
	if r.Method == http.MethodPatch {
		var patch struct {
			Operations []struct {
				Op    string          `json:"op"`
				Path  string          `json:"path"`
				Value json.RawMessage `json:"value"`
			} `json:"Operations"`
		}
		if !decodeJSON(w, r, &patch) {
			return
		}
		for _, operation := range patch.Operations {
			path := strings.ToLower(operation.Path)
			if path == "active" {
				_ = json.Unmarshal(operation.Value, &active)
			} else if path == "displayname" {
				_ = json.Unmarshal(operation.Value, &displayName)
			} else if path == "username" {
				_ = json.Unmarshal(operation.Value, &username)
			} else if path == "emails[type eq \"work\"].value" || path == "emails" {
				var values []scimNameValue
				if json.Unmarshal(operation.Value, &values) == nil && len(values) > 0 {
					email = values[0].Value
				} else {
					_ = json.Unmarshal(operation.Value, &email)
				}
			} else if path == "roles" {
				var value string
				if json.Unmarshal(operation.Value, &value) == nil {
					role = s.scimRole(workspaceKey, value)
				}
			}
		}
	} else {
		var input struct {
			UserName    string          `json:"userName"`
			DisplayName string          `json:"displayName"`
			Active      *bool           `json:"active"`
			Emails      []scimNameValue `json:"emails"`
			Roles       []struct {
				Value string `json:"value"`
			} `json:"roles"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if input.UserName != "" {
			username = input.UserName
		}
		if input.DisplayName != "" {
			displayName = input.DisplayName
		}
		if input.Active != nil {
			active = *input.Active
		}
		if len(input.Emails) > 0 {
			email = input.Emails[0].Value
		}
		if len(input.Roles) > 0 {
			role = s.scimRole(workspaceKey, input.Roles[0].Value)
		}
	}
	if role == "" {
		role = s.scimRole(workspaceKey, "")
	}
	item, err := s.store.ProvisionSCIMUser(r.Context(), workspaceID, current.ExternalID, username, displayName, email, current.User.AvatarURL, role, active)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, scimResource(item))
}

func (s *server) scimGroups(w http.ResponseWriter, r *http.Request) {
	workspaceKey, workspaceID, ok := s.authenticateSCIMRequest(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		var input scimGroupResource
		if !decodeJSON(w, r, &input) {
			return
		}
		role := scimGroupRole(s.scimWorkspaceSettings(workspaceKey), input.DisplayName)
		group, err := s.store.CreateSCIMGroup(r.Context(), workspaceID, input.ExternalID, input.DisplayName, role)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		members := make([]string, 0, len(input.Members))
		for _, member := range input.Members {
			if strings.TrimSpace(member.Value) != "" {
				members = append(members, strings.TrimSpace(member.Value))
			}
		}
		if _, err = s.store.ReplaceSCIMGroupMembers(r.Context(), workspaceID, group.ID, members); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		group, _ = s.store.SCIMGroup(r.Context(), workspaceID, group.ID)
		if teamID := s.scimTeamForGroup(workspaceKey, group); teamID != "" {
			if err = s.store.SyncSCIMGroupTeam(r.Context(), workspaceID, group.ID, teamID); err != nil {
				writeError(w, http.StatusUnprocessableEntity, err.Error())
				return
			}
		}
		resource := scimGroupResourceFromStore(workspaceKey, group)
		w.Header().Set("Location", "/scim/v2/"+workspaceKey+"/Groups/"+group.ID)
		writeJSON(w, http.StatusCreated, resource)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "SCIM group method is not supported")
		return
	}
	data, _ := s.store.BootstrapFor(workspaceKey)
	groups, _ := s.store.ListSCIMGroups(r.Context(), workspaceID)
	resources := make([]scimGroupResource, 0, len(data.Teams)+len(groups))
	teamMembers, _ := s.store.ListTeamMembers(r.Context(), workspaceID)
	for _, team := range data.Teams {
		resource := scimGroupResource{Schemas: []string{"urn:ietf:params:scim:schemas:core:2.0:Group"}, ID: team.ID, DisplayName: team.Name, Meta: map[string]string{"resourceType": "Team"}}
		for _, member := range teamMembers {
			if member.TeamID != team.ID {
				continue
			}
			resource.Members = append(resource.Members, scimGroupMember{Value: member.UserID, Ref: fmt.Sprintf("/scim/v2/%s/Users/%s", workspaceKey, member.UserID)})
		}
		resources = append(resources, resource)
	}
	for _, group := range groups {
		resources = append(resources, scimGroupResourceFromStore(workspaceKey, group))
	}
	writeJSON(w, http.StatusOK, map[string]any{"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:ListResponse"}, "totalResults": len(resources), "startIndex": 1, "itemsPerPage": len(resources), "Resources": resources})
}

func (s *server) scimGroup(w http.ResponseWriter, r *http.Request) {
	workspaceKey, workspaceID, ok := s.authenticateSCIMRequest(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	group, err := s.store.SCIMGroup(r.Context(), workspaceID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "SCIM group not found")
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, scimGroupResourceFromStore(workspaceKey, group))
		return
	}
	if r.Method == http.MethodDelete {
		if teamID := s.scimTeamForGroup(workspaceKey, group); teamID != "" {
			if err := s.store.SyncSCIMGroupTeam(r.Context(), workspaceID, group.ID, teamID); err != nil {
				writeError(w, http.StatusUnprocessableEntity, err.Error())
				return
			}
		}
		respondMutation(w, s.store.DeleteSCIMGroup(r.Context(), workspaceID, group.ID), http.StatusNoContent, nil)
		return
	}
	var input scimGroupResource
	if !decodeJSON(w, r, &input) {
		return
	}
	role := group.Role
	if input.DisplayName != "" {
		role = scimGroupRole(s.scimWorkspaceSettings(workspaceKey), input.DisplayName)
	}
	updated, err := s.store.UpdateSCIMGroup(r.Context(), workspaceID, group.ID, input.ExternalID, input.DisplayName, role)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if input.Members != nil {
		members := make([]string, 0, len(input.Members))
		for _, member := range input.Members {
			if strings.TrimSpace(member.Value) != "" {
				members = append(members, strings.TrimSpace(member.Value))
			}
		}
		updated, err = s.store.ReplaceSCIMGroupMembers(r.Context(), workspaceID, updated.ID, members)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	if teamID := s.scimTeamForGroup(workspaceKey, updated); teamID != "" {
		if err := s.store.SyncSCIMGroupTeam(r.Context(), workspaceID, updated.ID, teamID); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, scimGroupResourceFromStore(workspaceKey, updated))
}

func (s *server) scimWorkspaceSettings(workspaceKey string) domain.WorkspaceSettings {
	data, _ := s.store.BootstrapFor(workspaceKey)
	return data.WorkspaceSettings
}

// scimTeamForGroup resolves an explicit workspace mapping, or a group whose
// externalId is the Flow team ID/key. Display names are not matched implicitly
// because IdP group labels are not guaranteed to be unique.
func (s *server) scimTeamForGroup(workspaceKey string, group store.SCIMGroup) string {
	data, ok := s.store.BootstrapFor(workspaceKey)
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

func scimGroupResourceFromStore(workspaceKey string, group store.SCIMGroup) scimGroupResource {
	resource := scimGroupResource{Schemas: []string{"urn:ietf:params:scim:schemas:core:2.0:Group"}, ID: group.ID, ExternalID: group.ExternalID, DisplayName: group.DisplayName, Meta: map[string]string{"resourceType": "Group"}}
	for _, member := range group.Members {
		value := member.ExternalID
		if value == "" {
			value = member.UserID
		}
		resource.Members = append(resource.Members, scimGroupMember{Value: value, Ref: fmt.Sprintf("/scim/v2/%s/Users/%s", workspaceKey, member.UserID)})
	}
	return resource
}

func scimGroupRole(settings domain.WorkspaceSettings, displayName string) string {
	name := strings.ToLower(strings.TrimSpace(displayName))
	for role, configured := range settings.SCIMRoleGroups {
		if strings.EqualFold(strings.TrimSpace(configured), displayName) {
			return strings.ToLower(strings.TrimSpace(role))
		}
	}
	switch name {
	case "linear-owners":
		return "owner"
	case "linear-admins":
		return "admin"
	case "linear-guests":
		return "guest"
	default:
		return ""
	}
}

func parseSCIMFilter(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parts := strings.Fields(value)
	if len(parts) >= 3 && strings.EqualFold(parts[1], "eq") {
		return strings.Trim(parts[2], "\"")
	}
	return ""
}

func firstSCIMRole(values []struct {
	Value   string `json:"value"`
	Display string `json:"display"`
}) string {
	if len(values) == 0 {
		return ""
	}
	if values[0].Value != "" {
		return values[0].Value
	}
	return values[0].Display
}

func (s *server) scimRole(workspaceKey, value string) string {
	data, _ := s.store.BootstrapFor(workspaceKey)
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
