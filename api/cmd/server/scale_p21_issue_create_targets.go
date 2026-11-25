package main

import (
	"context"
	"net/http"
	"slices"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) boundedIssueCreateTargets(w http.ResponseWriter, r *http.Request, workspace string, input domain.IssueCreateInput) (domain.Bootstrap, func(), bool) {
	ctx := context.WithoutCancel(r.Context())
	installed, found, err := s.store.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		if err == nil {
			err = store.ErrAuthForbidden
		}
		issueRecordsError(w, err)
		return domain.Bootstrap{}, nil, false
	}
	releases := []func(){installed.Close}
	release := func() {
		for index := len(releases) - 1; index >= 0; index-- {
			releases[index]()
		}
	}
	fail := func(err error) (domain.Bootstrap, func(), bool) {
		release()
		issueRecordsError(w, err)
		return domain.Bootstrap{}, nil, false
	}
	var data domain.Bootstrap
	if s.authDisabled {
		data, _ = s.store.IssueAccessMetadata(ctx, workspace)
	}
	var scope *store.ViewerProjectionScope
	if !s.authDisabled {
		_, apiKey := r.Context().Value(apiKeyContextKey{}).(domain.APIKey)
		if authorization, ok := r.Context().Value(boundedIssueWriteAuthorizationContext{}).(*store.IssueWriteAuthorizationRead); ok && !apiKey && authorization.Data.Workspace.URLKey == workspace {
			selected, err := authorization.AuthorizationProjectionScope(r.Context())
			if err != nil {
				return fail(err)
			}
			data, scope = authorization.Data, &selected
			if input.AssigneeID != nil && *input.AssigneeID != "" {
				projected, err := installed.ProjectedWorkspace(r.Context(), authUser(r).ID)
				if err != nil {
					return fail(err)
				}
				releases = append(releases, projected.Close)
				data.Users = projected.Data.Users
			}
		} else {
			projected, err := installed.ProjectedWorkspaceWithScope(r.Context(), authUser(r).ID)
			if err != nil {
				return fail(err)
			}
			releases = append(releases, projected.Close)
			data = projected.Data
			selected, err := projected.ProjectionScope(r.Context())
			if err != nil {
				return fail(err)
			}
			scope = &selected
		}
	}
	filterBootstrapForAPIKey(&data, r)
	var credential func(string) bool
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok && apiKeyTeamRestrictionSelected(key) {
		credential = func(id string) bool { return slices.Contains(key.TeamIDs, id) }
		if s.authDisabled {
			scope = &store.ViewerProjectionScope{Content: map[string]bool{}}
			for _, id := range key.TeamIDs {
				scope.Content[id] = true
			}
		}
	}
	for _, target := range []struct {
		kind string
		id   *string
	}{{"projects", input.ProjectID}, {"cycles", input.CycleID}, {"users", input.AssigneeID}} {
		if target.id == nil || *target.id == "" {
			continue
		}
		var present bool
		if s.authDisabled && (credential == nil || target.kind == "users") {
			present, err = installed.RecordIDPresent(ctx, target.kind, *target.id)
		} else {
			switch target.kind {
			case "projects":
				present, err = installed.ProjectVisibleInProjection(ctx, *scope, *target.id, credential)
			case "cycles":
				present, err = installed.CycleVisibleInProjection(ctx, *scope, *target.id, credential)
			case "users":
				present = slices.ContainsFunc(data.Users, func(user domain.User) bool { return user.ID == *target.id })
			}
		}
		if err != nil {
			return fail(err)
		}
		if present {
			switch target.kind {
			case "projects":
				data.Projects = []domain.Project{{ID: *target.id}}
			case "cycles":
				data.Cycles = []domain.Cycle{{ID: *target.id}}
			case "users":
				if s.authDisabled {
					data.Users = []domain.User{{ID: *target.id}}
				}
			}
		}
	}
	if input.TemplateID != "" {
		templateScope := scope
		if s.authDisabled {
			templateScope = nil
		}
		template, err := installed.ReadIssueCreateTemplate(ctx, input.TemplateID, templateScope, credential)
		if err != nil {
			return fail(err)
		}
		releases = append(releases, template.Close)
		if template.Template != nil {
			data.IssueTemplates = []domain.IssueTemplate{*template.Template}
		}
	}
	return data, release, true
}

func (s *server) boundedIssueCreateDefaultTeam(r *http.Request) (domain.Bootstrap, func(), error) {
	installed, found, err := s.store.BeginInstalledMetadataRead(r.Context(), workspaceKey(r))
	if err != nil || !found {
		if err == nil {
			err = store.ErrAuthForbidden
		}
		return domain.Bootstrap{}, nil, err
	}
	projected, err := installed.ProjectedWorkspace(r.Context(), authUser(r).ID)
	if err != nil {
		installed.Close()
		return domain.Bootstrap{}, nil, err
	}
	return projected.Data, func() {
		projected.Close()
		installed.Close()
	}, nil
}
