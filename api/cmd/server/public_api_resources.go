package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/publicapi"
	"flow/api/internal/store"
)

// Lightweight public REST reads (the reference app's teams, users, workflowStates,
// issueLabels, cycles, projects, projectMilestones, initiatives, documents,
// comments, attachments, customers, customerNeeds, releases, webhooks, viewer
// and organization queries). Lists return connections (public_api_pagination.go)
// and accept `filter` objects (public_api_filter.go). Visibility comes from
// the same per-user metadata projection as the web client, narrowed by API
// key team restrictions and application scopes.

// publicMetadata loads the caller's projected workspace metadata.
func (s *server) publicMetadata(w http.ResponseWriter, r *http.Request) (domain.Bootstrap, bool) {
	key := workspaceKey(r)
	if key == "" && !s.authDisabled {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "Select a workspace with the X-Workspace-Key header (API keys and OAuth tokens select it automatically)")
		return domain.Bootstrap{}, false
	}
	if s.authDisabled {
		data, ok := s.store.WorkspaceMetadata(key)
		if !ok {
			writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "workspace not found")
			return data, false
		}
		data.ViewerRole = "admin"
		return data, true
	}
	data, err := s.store.PagedWorkspaceMetadata(r.Context(), key, authUser(r).ID)
	if err != nil {
		if errors.Is(err, store.ErrAuthForbidden) {
			writeAPIError(w, http.StatusForbidden, apiErrorForbidden, "You don't have access to this workspace")
		} else {
			writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "Could not load workspace data")
		}
		return data, false
	}
	filterBootstrapForAPIKey(&data, r)
	if apiKey, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		applyAPIKeyProjection(&data, apiKey)
	}
	return data, true
}

func visibleTeamSet(data domain.Bootstrap) map[string]domain.Team {
	teams := make(map[string]domain.Team, len(data.Teams))
	for _, team := range data.Teams {
		teams[team.ID] = team
	}
	return teams
}

func teamRelation(teams map[string]domain.Team, id string) any {
	team, ok := teams[id]
	if !ok {
		return filterRelation(id, nil)
	}
	return filterRelation(team.ID, map[string]any{"key": team.Key, "name": team.Name})
}

func userRelation(user *domain.User) any {
	if user == nil || user.ID == "" {
		return nil
	}
	return filterRelation(user.ID, map[string]any{"name": user.Name, "displayName": user.DisplayName, "email": user.Email})
}

// writeConnection applies filter + pagination and writes the connection.
func writeConnection[T any](w http.ResponseWriter, r *http.Request, items []T, fields map[string]func(T) any, key func(T, string) pageKey, orderings ...string) {
	args, err := parsePageArgs(r, orderings...)
	if err != nil {
		writePageArgsError(w, err)
		return
	}
	filtered, err := applyResourceFilter(items, r.URL.Query().Get("filter"), typedFields(fields))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, paginateConnection(filtered, func(item T) pageKey { return key(item, args.OrderBy) }, args))
}

func includeArchived(r *http.Request) bool {
	value := strings.TrimSpace(r.URL.Query().Get("includeArchived"))
	return value == "true" || value == "1"
}

func queryValues(r *http.Request, name string) []string {
	return splitQueryValues(r.URL.Query().Get(name))
}

// GET /api/viewer

type viewerApplication = publicapi.ViewerApplication

type viewerResponse = publicapi.Viewer

func (s *server) getViewer(w http.ResponseWriter, r *http.Request) {
	user := requestActor(s, r)
	response := viewerResponse{User: user, Actor: "user", Authentication: "session"}
	if user.App {
		response.Actor = "app"
	}
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		response.Authentication = "apiKey"
		if key.OAuthClientID != "" || key.AuthorizationID != "" || key.GrantType != "" || key.Actor == "app" {
			response.Authentication = "oauth"
			response.Application = &viewerApplication{ClientID: key.OAuthClientID}
		}
		if key.Actor == "app" {
			response.Actor = "app"
		}
		response.Scopes = key.Scopes
		if response.Scopes == nil {
			response.Scopes = []string{"read", "write"}
		}
		if apiKeyTeamRestrictionSelected(key) {
			response.TeamIDs = slices.Clone(key.TeamIDs)
		}
		response.ExpiresAt = key.ExpiresAt
	}
	if workspaceKey(r) != "" {
		if data, ok := s.store.WorkspaceSettingsMetadata(workspaceKey(r)); ok {
			workspace := data.Workspace
			response.Workspace = &workspace
			if s.authDisabled {
				response.Role = "admin"
			} else if role, status, err := s.store.WorkspaceRole(r.Context(), workspace.ID, user.ID); err == nil && status == "active" {
				response.Role = role
			}
			if response.Application != nil {
				for _, application := range data.OAuthApplications {
					if application.ClientID == response.Application.ClientID {
						response.Application.Name = application.Name
					}
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, response)
}

// GET /api/organization

type organizationResponse = publicapi.Organization

func (s *server) getOrganization(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	active := 0
	for _, user := range data.Users {
		if user.Active && !user.App {
			active++
		}
	}
	writeJSON(w, http.StatusOK, organizationResponse{Workspace: data.Workspace, UserCount: active, TeamCount: len(data.Teams)})
}

// GET /api/teams

func (s *server) listTeamsConnection(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	archived := includeArchived(r)
	items := slices.DeleteFunc(slices.Clone(data.Teams), func(team domain.Team) bool {
		return !archived && (team.ArchivedAt != nil || team.RetiredAt != nil)
	})
	parents := data.TeamParents
	writeConnection(w, r, items, map[string]func(domain.Team) any{
		"id":         func(t domain.Team) any { return t.ID },
		"name":       func(t domain.Team) any { return t.Name },
		"key":        func(t domain.Team) any { return t.Key },
		"private":    func(t domain.Team) any { return t.Private },
		"parent":     func(t domain.Team) any { return filterRelation(parents[t.ID], nil) },
		"createdAt":  func(t domain.Team) any { return filterTime(t.CreatedAt) },
		"updatedAt":  func(t domain.Team) any { return filterTime(t.UpdatedAt) },
		"archivedAt": func(t domain.Team) any { return filterTime(t.ArchivedAt) },
	}, func(t domain.Team, orderBy string) pageKey {
		return orderedKey(orderBy, t.ID, timeValue(t.CreatedAt), timeValue(t.UpdatedAt))
	})
}

// GET /api/users

func (s *server) listUsersConnection(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	joined := map[string]time.Time{}
	roles := map[string]string{}
	for _, member := range data.Members {
		joined[member.User.ID] = member.JoinedAt
		roles[member.User.ID] = member.Role
	}
	disabled := includeArchived(r) || r.URL.Query().Get("includeDisabled") == "true"
	items := slices.DeleteFunc(slices.Clone(data.Users), func(user domain.User) bool { return !disabled && !user.Active })
	for index := range items {
		items[index].AppScopes = nil
	}
	writeConnection(w, r, items, map[string]func(domain.User) any{
		"id":          func(u domain.User) any { return u.ID },
		"name":        func(u domain.User) any { return u.Name },
		"displayName": func(u domain.User) any { return u.DisplayName },
		"username":    func(u domain.User) any { return u.Username },
		"email":       func(u domain.User) any { return u.Email },
		"active":      func(u domain.User) any { return u.Active },
		"app":         func(u domain.User) any { return u.App },
		"admin":       func(u domain.User) any { return workspaceAdminRole(roles[u.ID]) },
		"isMe":        func(u domain.User) any { return u.ID == data.Viewer.ID },
		"createdAt":   func(u domain.User) any { return joined[u.ID] },
	}, func(u domain.User, _ string) pageKey { return pageKey{At: joined[u.ID], ID: u.ID} })
}

// GET /api/workflow-states

func (s *server) listWorkflowStatesConnection(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	teams := visibleTeamSet(data)
	filterTeams := queryValues(r, "teamId")
	items := []domain.WorkflowState{}
	for _, team := range data.Teams {
		if len(filterTeams) > 0 && !slices.Contains(filterTeams, team.ID) && !slices.Contains(filterTeams, team.Key) {
			continue
		}
		items = append(items, statesForTeam(&data, team.ID)...)
	}
	items = slices.DeleteFunc(items, func(state domain.WorkflowState) bool { _, ok := teams[state.TeamID]; return !ok })
	writeConnection(w, r, items, map[string]func(domain.WorkflowState) any{
		"id":       func(s domain.WorkflowState) any { return s.ID },
		"name":     func(s domain.WorkflowState) any { return s.Name },
		"type":     func(s domain.WorkflowState) any { return s.Type },
		"position": func(s domain.WorkflowState) any { return s.Position },
		"team":     func(s domain.WorkflowState) any { return teamRelation(teams, s.TeamID) },
	}, func(s domain.WorkflowState, _ string) pageKey { return pageKey{ID: s.ID} }, "createdAt")
}

// GET /api/issue-labels

func (s *server) listIssueLabelsConnection(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	teams := visibleTeamSet(data)
	archived := includeArchived(r)
	resourceType := r.URL.Query().Get("resourceType")
	if resourceType == "" {
		resourceType = "issue"
	}
	filterTeams := queryValues(r, "teamId")
	items := slices.DeleteFunc(slices.Clone(data.Labels), func(label domain.IssueLabel) bool {
		if !archived && label.ArchivedAt != nil || resourceType != "all" && labelResourceType(label) != resourceType {
			return true
		}
		if !labelScopeIsWorkspace(label.Scope) {
			if _, visible := teams[label.Scope]; !visible {
				return true
			}
		}
		return len(filterTeams) > 0 && !slices.Contains(filterTeams, label.Scope) && !(labelScopeIsWorkspace(label.Scope) && r.URL.Query().Get("includeWorkspace") != "false")
	})
	writeConnection(w, r, items, map[string]func(domain.IssueLabel) any{
		"id":    func(l domain.IssueLabel) any { return l.ID },
		"name":  func(l domain.IssueLabel) any { return l.Name },
		"color": func(l domain.IssueLabel) any { return l.Color },
		"team": func(l domain.IssueLabel) any {
			if labelScopeIsWorkspace(l.Scope) {
				return nil
			}
			return teamRelation(teams, l.Scope)
		},
		"parent":     func(l domain.IssueLabel) any { return filterRelation(l.GroupID, nil) },
		"createdAt":  func(l domain.IssueLabel) any { return l.CreatedAt },
		"archivedAt": func(l domain.IssueLabel) any { return filterTime(l.ArchivedAt) },
	}, func(l domain.IssueLabel, _ string) pageKey { return pageKey{At: l.CreatedAt, ID: l.ID} }, "createdAt")
}

// GET /api/cycles

func (s *server) listCyclesConnection(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	teams := visibleTeamSet(data)
	filterTeams := queryValues(r, "teamId")
	items := slices.DeleteFunc(slices.Clone(data.Cycles), func(cycle domain.Cycle) bool {
		_, visible := teams[cycle.TeamID]
		return !visible || len(filterTeams) > 0 && !slices.Contains(filterTeams, cycle.TeamID)
	})
	for index := range items {
		items[index].CalendarToken = ""
	}
	now := time.Now()
	writeConnection(w, r, items, map[string]func(domain.Cycle) any{
		"id":        func(c domain.Cycle) any { return c.ID },
		"name":      func(c domain.Cycle) any { return c.Name },
		"number":    func(c domain.Cycle) any { return c.Number },
		"status":    func(c domain.Cycle) any { return c.Status },
		"team":      func(c domain.Cycle) any { return teamRelation(teams, c.TeamID) },
		"startsAt":  func(c domain.Cycle) any { return c.StartsAt },
		"endsAt":    func(c domain.Cycle) any { return c.EndsAt },
		"isActive":  func(c domain.Cycle) any { return !c.StartsAt.After(now) && c.EndsAt.After(now) },
		"createdAt": func(c domain.Cycle) any { return c.CreatedAt },
		"updatedAt": func(c domain.Cycle) any { return c.UpdatedAt },
	}, func(c domain.Cycle, orderBy string) pageKey {
		return orderedKey(orderBy, c.ID, c.CreatedAt, c.UpdatedAt)
	})
}

// lightweightProject drops history arrays that list endpoints don't need.
func lightweightProject(project domain.Project) domain.Project {
	project.Comments = nil
	project.CommentAttachments = nil
	project.DescriptionRevisions = nil
	project.IssueCountHistory, project.ScopeHistory, project.CompletedScopeHistory, project.InProgressScopeHistory, project.ProgressHistory = nil, nil, nil, nil, nil
	for index := range project.Milestones {
		project.Milestones[index].DescriptionRevisions = nil
	}
	return project
}

// GET /api/projects?first=… (connection form of the project directory)

func (s *server) listProjectsConnection(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	teams := visibleTeamSet(data)
	archived := includeArchived(r)
	filterTeams := queryValues(r, "teamId")
	items := []domain.Project{}
	for _, project := range data.Projects {
		if !archived && project.ArchivedAt != nil || len(filterTeams) > 0 && !slices.ContainsFunc(project.TeamIDs, func(id string) bool { return slices.Contains(filterTeams, id) }) {
			continue
		}
		items = append(items, lightweightProject(project))
	}
	writeConnection(w, r, items, map[string]func(domain.Project) any{
		"id":       func(p domain.Project) any { return p.ID },
		"name":     func(p domain.Project) any { return p.Name },
		"slugId":   func(p domain.Project) any { return p.SlugID },
		"health":   func(p domain.Project) any { return p.Health },
		"priority": func(p domain.Project) any { return p.Priority },
		"status": func(p domain.Project) any {
			return filterRelation(p.Status.ID, map[string]any{"name": p.Status.Name, "type": p.Status.Type})
		},
		"lead": func(p domain.Project) any { return userRelation(p.Lead) },
		"teams": func(p domain.Project) any {
			return relationList(p.TeamIDs, func(id string) any { return teamRelation(teams, id) })
		},
		"labels": func(p domain.Project) any {
			return relationList(p.LabelIDs, func(id string) any { return filterRelation(id, nil) })
		},
		"members": func(p domain.Project) any {
			return relationList(p.MemberIDs, func(id string) any { return filterRelation(id, nil) })
		},
		"initiatives": func(p domain.Project) any {
			return relationList(p.Initiatives, func(id string) any { return filterRelation(id, nil) })
		},
		"startDate":  func(p domain.Project) any { return filterDate(p.StartDate) },
		"targetDate": func(p domain.Project) any { return filterDate(p.TargetDate) },
		"createdAt":  func(p domain.Project) any { return p.CreatedAt },
		"updatedAt":  func(p domain.Project) any { return p.UpdatedAt },
		"archivedAt": func(p domain.Project) any { return filterTime(p.ArchivedAt) },
	}, func(p domain.Project, orderBy string) pageKey {
		return orderedKey(orderBy, p.ID, p.CreatedAt, p.UpdatedAt)
	})
}

func relationList(ids []string, relation func(string) any) any {
	list := make([]any, 0, len(ids))
	for _, id := range ids {
		list = append(list, relation(id))
	}
	return list
}

// GET /api/project-milestones

func (s *server) listProjectMilestonesConnection(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	projectFilter := queryValues(r, "projectId")
	names := map[string]string{}
	items := []domain.ProjectMilestone{}
	for _, project := range data.Projects {
		if len(projectFilter) > 0 && !slices.Contains(projectFilter, project.ID) && !slices.Contains(projectFilter, project.SlugID) {
			continue
		}
		if project.ArchivedAt != nil && !includeArchived(r) {
			continue
		}
		names[project.ID] = project.Name
		for _, milestone := range project.Milestones {
			milestone.DescriptionRevisions = nil
			items = append(items, milestone)
		}
	}
	writeConnection(w, r, items, map[string]func(domain.ProjectMilestone) any{
		"id":   func(m domain.ProjectMilestone) any { return m.ID },
		"name": func(m domain.ProjectMilestone) any { return m.Name },
		"project": func(m domain.ProjectMilestone) any {
			return filterRelation(m.ProjectID, map[string]any{"name": names[m.ProjectID]})
		},
		"targetDate": func(m domain.ProjectMilestone) any { return filterDate(m.TargetDate) },
		"createdAt":  func(m domain.ProjectMilestone) any { return m.CreatedAt },
		"updatedAt":  func(m domain.ProjectMilestone) any { return m.UpdatedAt },
	}, func(m domain.ProjectMilestone, orderBy string) pageKey {
		return orderedKey(orderBy, m.ID, m.CreatedAt, m.UpdatedAt)
	})
}

// GET /api/initiatives

func (s *server) listInitiativesConnection(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	items := slices.Clone(data.Initiatives)
	for index := range items {
		items[index].Comments = nil
		items[index].DescriptionHistory = nil
	}
	writeConnection(w, r, items, map[string]func(domain.Initiative) any{
		"id":         func(i domain.Initiative) any { return i.ID },
		"name":       func(i domain.Initiative) any { return i.Name },
		"slugId":     func(i domain.Initiative) any { return i.SlugID },
		"status":     func(i domain.Initiative) any { return i.Status },
		"health":     func(i domain.Initiative) any { return i.Health },
		"owner":      func(i domain.Initiative) any { return userRelation(i.Owner) },
		"creator":    func(i domain.Initiative) any { return userRelation(&i.Creator) },
		"targetDate": func(i domain.Initiative) any { return filterDate(i.TargetDate) },
		"projects": func(i domain.Initiative) any {
			return relationList(i.ProjectIDs, func(id string) any { return filterRelation(id, nil) })
		},
		"parentInitiatives": func(i domain.Initiative) any {
			return relationList(i.ParentInitiativeIDs, func(id string) any { return filterRelation(id, nil) })
		},
		"createdAt": func(i domain.Initiative) any { return i.CreatedAt },
		"updatedAt": func(i domain.Initiative) any { return i.UpdatedAt },
	}, func(i domain.Initiative, orderBy string) pageKey {
		return orderedKey(orderBy, i.ID, i.CreatedAt, i.UpdatedAt)
	})
}

// GET /api/documents?first=…

func (s *server) listDocumentsConnection(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	teams := visibleTeamSet(data)
	archived := includeArchived(r)
	filterTeams := queryValues(r, "teamId")
	items := []domain.Document{}
	for _, document := range data.Documents {
		if !documentVisibleToViewer(s, data, document) || !archived && document.ArchivedAt != nil {
			continue
		}
		if len(filterTeams) > 0 && !slices.ContainsFunc(document.TeamIDs, func(id string) bool { return slices.Contains(filterTeams, id) }) {
			continue
		}
		document.ContentState, document.ContentData, document.Revisions, document.Permissions = "", nil, nil, nil
		items = append(items, document)
	}
	writeConnection(w, r, items, map[string]func(domain.Document) any{
		"id":      func(d domain.Document) any { return d.ID },
		"title":   func(d domain.Document) any { return d.Title },
		"slugId":  func(d domain.Document) any { return d.SlugID },
		"creator": func(d domain.Document) any { return userRelation(&d.Creator) },
		"teams": func(d domain.Document) any {
			return relationList(d.TeamIDs, func(id string) any { return teamRelation(teams, id) })
		},
		"projects": func(d domain.Document) any {
			return relationList(d.ProjectIDs, func(id string) any { return filterRelation(id, nil) })
		},
		"issue":      func(d domain.Document) any { return filterRelation(d.IssueID, nil) },
		"createdAt":  func(d domain.Document) any { return d.CreatedAt },
		"updatedAt":  func(d domain.Document) any { return d.UpdatedAt },
		"archivedAt": func(d domain.Document) any { return filterTime(d.ArchivedAt) },
	}, func(d domain.Document, orderBy string) pageKey {
		return orderedKey(orderBy, d.ID, d.CreatedAt, d.UpdatedAt)
	})
}

// GET /api/customers

func (s *server) listCustomersConnection(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	tiers := map[string]string{}
	for _, tier := range data.CustomerTiers {
		tiers[tier.ID] = tier.Name
	}
	statuses := map[string]string{}
	for _, status := range data.CustomerStatuses {
		statuses[status.ID] = status.Name
	}
	writeConnection(w, r, slices.Clone(data.Customers), map[string]func(domain.Customer) any{
		"id":          func(c domain.Customer) any { return c.ID },
		"name":        func(c domain.Customer) any { return c.Name },
		"domains":     func(c domain.Customer) any { return filterList(c.Domains) },
		"externalIds": func(c domain.Customer) any { return filterList(c.ExternalIDs) },
		"status": func(c domain.Customer) any {
			return filterRelation(c.Status, map[string]any{"name": firstNonEmpty(statuses[c.Status], c.Status)})
		},
		"tier":      func(c domain.Customer) any { return filterRelation(c.Tier, map[string]any{"name": tiers[c.Tier]}) },
		"owner":     func(c domain.Customer) any { return filterRelation(c.OwnerID, nil) },
		"revenue":   func(c domain.Customer) any { return c.AnnualRevenue },
		"size":      func(c domain.Customer) any { return c.Size },
		"createdAt": func(c domain.Customer) any { return c.CreatedAt },
		"updatedAt": func(c domain.Customer) any { return c.UpdatedAt },
	}, func(c domain.Customer, orderBy string) pageKey {
		return orderedKey(orderBy, c.ID, c.CreatedAt, c.UpdatedAt)
	})
}

// GET /api/customer-requests (the reference app: customerNeeds)

func (s *server) listCustomerNeedsConnection(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	archived := includeArchived(r)
	customerFilter, issueFilter, projectFilter := queryValues(r, "customerId"), queryValues(r, "issueId"), queryValues(r, "projectId")
	items := slices.DeleteFunc(slices.Clone(data.CustomerRequests), func(item domain.CustomerRequest) bool {
		return !archived && item.ArchivedAt != nil ||
			len(customerFilter) > 0 && !slices.Contains(customerFilter, item.CustomerID) ||
			len(issueFilter) > 0 && !slices.Contains(issueFilter, item.IssueID) ||
			len(projectFilter) > 0 && !slices.Contains(projectFilter, item.ProjectID)
	})
	writeConnection(w, r, items, map[string]func(domain.CustomerRequest) any{
		"id":         func(c domain.CustomerRequest) any { return c.ID },
		"body":       func(c domain.CustomerRequest) any { return c.Body },
		"customer":   func(c domain.CustomerRequest) any { return filterRelation(c.CustomerID, nil) },
		"issue":      func(c domain.CustomerRequest) any { return filterRelation(c.IssueID, nil) },
		"project":    func(c domain.CustomerRequest) any { return filterRelation(c.ProjectID, nil) },
		"priority":   func(c domain.CustomerRequest) any { return c.Priority },
		"source":     func(c domain.CustomerRequest) any { return c.Source },
		"creator":    func(c domain.CustomerRequest) any { return userRelation(&c.Creator) },
		"createdAt":  func(c domain.CustomerRequest) any { return c.CreatedAt },
		"updatedAt":  func(c domain.CustomerRequest) any { return c.UpdatedAt },
		"archivedAt": func(c domain.CustomerRequest) any { return filterTime(c.ArchivedAt) },
	}, func(c domain.CustomerRequest, orderBy string) pageKey {
		return orderedKey(orderBy, c.ID, c.CreatedAt, c.UpdatedAt)
	})
}

// GET /api/releases?first=…

func (s *server) listReleasesConnection(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	archived := includeArchived(r)
	pipelines := queryValues(r, "pipelineId")
	items := slices.DeleteFunc(slices.Clone(data.Releases), func(release domain.Release) bool {
		return !archived && release.ArchivedAt != nil || len(pipelines) > 0 && !slices.Contains(pipelines, release.PipelineID)
	})
	args, err := parsePageArgs(r)
	if err != nil {
		writePageArgsError(w, err)
		return
	}
	fields := map[string]func(domain.Release) any{
		"id":         func(x domain.Release) any { return x.ID },
		"name":       func(x domain.Release) any { return x.Name },
		"slugId":     func(x domain.Release) any { return x.SlugID },
		"status":     func(x domain.Release) any { return x.Status },
		"pipeline":   func(x domain.Release) any { return filterRelation(x.PipelineID, nil) },
		"createdAt":  func(x domain.Release) any { return x.CreatedAt },
		"updatedAt":  func(x domain.Release) any { return x.UpdatedAt },
		"archivedAt": func(x domain.Release) any { return filterTime(x.ArchivedAt) },
	}
	filtered, err := applyResourceFilter(items, r.URL.Query().Get("filter"), typedFields(fields))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, err.Error())
		return
	}
	page := paginateConnection(filtered, func(x domain.Release) pageKey { return orderedKey(args.OrderBy, x.ID, x.CreatedAt, x.UpdatedAt) }, args)
	// Progress is computed for the returned page only.
	data.Releases = page.Nodes
	if err := s.store.PopulateReleaseProgress(r.Context(), &data); err != nil {
		issueRecordsError(w, err)
		return
	}
	page.Nodes = data.Releases
	writeJSON(w, http.StatusOK, page)
}

// legacyListJSON runs a legacy array endpoint in-process so its connection
// form reuses exactly the same visibility rules.
func legacyListJSON[T any](handler http.HandlerFunc, r *http.Request, query url.Values) (int, []T, []byte) {
	clone := r.Clone(r.Context())
	clone.URL.RawQuery = query.Encode()
	recorder := &memoryResponse{header: http.Header{}}
	handler(recorder, clone)
	var items []T
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	if recorder.status == http.StatusOK {
		if err := json.Unmarshal(recorder.body.Bytes(), &items); err != nil {
			return http.StatusInternalServerError, nil, nil
		}
	}
	return recorder.status, items, recorder.body.Bytes()
}

type memoryResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (m *memoryResponse) Header() http.Header { return m.header }
func (m *memoryResponse) WriteHeader(status int) {
	if m.status == 0 {
		m.status = status
	}
}
func (m *memoryResponse) Write(p []byte) (int, error) {
	if m.status == 0 {
		m.status = http.StatusOK
	}
	return m.body.Write(p)
}

// GET /api/webhooks?first=… (admin only, like the array form)

func (s *server) listWebhooksConnection(w http.ResponseWriter, r *http.Request) {
	status, items, raw := legacyListJSON[publicapi.Webhook](s.listWebhooks, r, url.Values{})
	if status != http.StatusOK {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write(raw)
		return
	}
	writeConnection(w, r, items, map[string]func(publicapi.Webhook) any{
		"id":            func(x publicapi.Webhook) any { return x.ID },
		"name":          func(x publicapi.Webhook) any { return x.Name },
		"url":           func(x publicapi.Webhook) any { return x.URL },
		"enabled":       func(x publicapi.Webhook) any { return x.Enabled },
		"resourceTypes": func(x publicapi.Webhook) any { return filterList(x.ResourceTypes) },
		"teams": func(x publicapi.Webhook) any {
			return relationList(x.TeamIDs, func(id string) any { return filterRelation(id, nil) })
		},
		"createdAt": func(x publicapi.Webhook) any { return x.CreatedAt },
		"updatedAt": func(x publicapi.Webhook) any { return x.UpdatedAt },
	}, func(x publicapi.Webhook, orderBy string) pageKey {
		return orderedKey(orderBy, x.ID, x.CreatedAt, x.UpdatedAt)
	})
}

// publicIssueQuery is the caller's issue access without reading list query
// parameters (the public endpoints use their own filter syntax).
func (s *server) publicIssueQuery(r *http.Request) (domain.Bootstrap, store.IssueRecordQuery, error) {
	data, access, err := s.requestIssueQueryAccess(r)
	query := store.IssueRecordQuery{Workspace: data.Workspace.URLKey, Archived: "all"}
	if err != nil {
		return data, query, err
	}
	if s.authDisabled {
		if archived := store.ArchivedTeamIDs(data); len(archived) > 0 {
			query.Access = &store.IssueRecordAccess{Admin: true, ArchivedTeamIDs: archived}
		}
		return data, query, nil
	}
	query.Access = &access
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok && apiKeyTeamRestrictionSelected(key) {
		query.AllowedTeamIDs = slices.Clone(key.TeamIDs)
		if query.AllowedTeamIDs == nil {
			query.AllowedTeamIDs = []string{}
		}
	}
	return data, query, nil
}

func (s *server) authorizedPublicIssue(w http.ResponseWriter, r *http.Request, id string) (domain.Issue, store.IssueRecordQuery, bool) {
	_, query, err := s.publicIssueQuery(r)
	if err != nil {
		issueRecordsError(w, err)
		return domain.Issue{}, query, false
	}
	issue, err := s.store.AuthorizedIssueRecord(r.Context(), query, id)
	if err != nil {
		if errors.Is(err, store.ErrIssueQuery) || isNoRows(err) {
			writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "issue not found")
		} else {
			issueRecordsError(w, err)
		}
		return issue, query, false
	}
	return issue, query, true
}

// GET /api/comments?issueId=|documentId=|projectId=|initiativeId=

func (s *server) listCommentsConnection(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	parents := map[string]string{}
	for _, name := range []string{"issueId", "documentId", "projectId", "initiativeId"} {
		if value := strings.TrimSpace(values.Get(name)); value != "" {
			parents[name] = value
		}
	}
	if len(parents) != 1 {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "Provide exactly one of issueId, documentId, projectId or initiativeId")
		return
	}
	fields := map[string]func(domain.Comment) any{
		"id":        func(c domain.Comment) any { return c.ID },
		"body":      func(c domain.Comment) any { return c.Body },
		"user":      func(c domain.Comment) any { return userRelation(&c.User) },
		"parent":    func(c domain.Comment) any { return filterRelation(stringValue(c.ParentID), nil) },
		"resolved":  func(c domain.Comment) any { return c.Resolved },
		"createdAt": func(c domain.Comment) any { return c.CreatedAt },
		"updatedAt": func(c domain.Comment) any { return commentUpdatedAt(c) },
	}
	key := func(c domain.Comment, orderBy string) pageKey {
		return orderedKey(orderBy, c.ID, c.CreatedAt, commentUpdatedAt(c))
	}
	if id, ok := parents["projectId"]; ok {
		data, ok := s.publicMetadata(w, r)
		if !ok {
			return
		}
		index := domain.ProjectIndexBySlugRef(data.Projects, id)
		if index < 0 {
			writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "project not found")
			return
		}
		writeConnection(w, r, data.Projects[index].Comments, fields, key)
		return
	}
	if id, ok := parents["initiativeId"]; ok {
		data, ok := s.publicMetadata(w, r)
		if !ok {
			return
		}
		index := domain.InitiativeIndexBySlugRef(data.Initiatives, id)
		if index < 0 {
			writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "initiative not found")
			return
		}
		writeConnection(w, r, data.Initiatives[index].Comments, fields, key)
		return
	}
	// Issue and document discussions live in indexed content records.
	args, err := parsePageArgs(r, "createdAt")
	if err != nil {
		writePageArgsError(w, err)
		return
	}
	if r.URL.Query().Get("filter") != "" {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "filter is not supported for issue and document comments; page with first/after")
		return
	}
	resource, workspace := "", ""
	if id, ok := parents["issueId"]; ok {
		issue, query, ok := s.authorizedPublicIssue(w, r, id)
		if !ok {
			return
		}
		resource, workspace = issue.ID, query.Workspace
	} else {
		data, ok := s.publicMetadata(w, r)
		if !ok {
			return
		}
		document, err := documentByID(&data, parents["documentId"])
		if err != nil || !documentVisibleToViewer(s, data, *document) {
			writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "document not found")
			return
		}
		resource, workspace = document.ID, data.Workspace.URLKey
	}
	toContent := func(cursor *pageCursor) *store.ContentCursor {
		if cursor == nil {
			return nil
		}
		return &store.ContentCursor{CreatedAt: cursor.At, ID: cursor.ID}
	}
	page, err := s.store.ResourceCommentConnection(r.Context(), workspace, resource, toContent(args.After), toContent(args.Before), args.Size, args.Backward)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	result := connection[domain.Comment]{Nodes: page.Comments, PageInfo: pageInfo{HasNextPage: page.HasNext, HasPreviousPage: page.HasPrevious}}
	if len(page.Keys) > 0 {
		encode := func(key store.ContentCursor) *string {
			raw, _ := json.Marshal(pageCursor{OrderBy: "createdAt", At: key.CreatedAt, ID: key.ID})
			value := encodeCursorBytes(raw)
			return &value
		}
		result.PageInfo.StartCursor = encode(page.Keys[0])
		result.PageInfo.EndCursor = encode(page.Keys[len(page.Keys)-1])
	}
	writeJSON(w, http.StatusOK, result)
}

func commentUpdatedAt(c domain.Comment) time.Time {
	if c.EditedAt != nil {
		return *c.EditedAt
	}
	return c.CreatedAt
}
