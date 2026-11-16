package main

import (
	"context"
	"net/http"
	"slices"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// boundedIssueUpdateTargets is updateIssueRecord's read of the viewer's
// paged projection (PagedWorkspaceMetadata, filtered for the API key) and its
// destination checks, over one installed generation. The projection runs
// literally over the core and resident policy fields with the live
// directory (ProjectedWorkspace), which is everything the write reads from
// it: the viewer, role, teams, team settings and team memberships
// (issueRole, issueTeamMoveForbidden) and the users (the assignee check).
// The destination project and cycle are looked up in the installed
// collections under the same projection and credential filters
// (ProjectVisibleInProjection, CycleVisibleInProjection). The checks, their
// order, statuses and messages are the literal handler's. On failure it has
// written the response; otherwise the caller releases the projection after
// the write.
func (s *server) boundedIssueUpdateTargets(w http.ResponseWriter, r *http.Request, workspace string, input domain.IssueUpdateInput) (domain.Bootstrap, func(), bool) {
	ctx := r.Context()
	userID := authUser(r).ID
	// Like the legacy accessor, the installed read ignores cancellation.
	installed, found, err := s.store.BeginInstalledMetadataRead(context.WithoutCancel(ctx), workspace)
	if err != nil {
		issueRecordsError(w, err)
		return domain.Bootstrap{}, nil, false
	}
	if !found {
		// PagedWorkspaceMetadata reports a missing workspace as forbidden.
		issueRecordsError(w, store.ErrAuthForbidden)
		return domain.Bootstrap{}, nil, false
	}
	projected, err := installed.ProjectedWorkspace(ctx, userID)
	if err != nil {
		installed.Close()
		issueRecordsError(w, err)
		return domain.Bootstrap{}, nil, false
	}
	release := func() {
		projected.Close()
		installed.Close()
	}
	metadata := projected.Data
	filterBootstrapForAPIKey(&metadata, r)
	var credential func(string) bool
	if key, ok := ctx.Value(apiKeyContextKey{}).(domain.APIKey); ok && apiKeyTeamRestrictionSelected(key) {
		credential = func(id string) bool { return slices.Contains(key.TeamIDs, id) }
	}
	var scope *store.ViewerProjectionScope
	projection := func() (store.ViewerProjectionScope, error) {
		if scope == nil {
			value, err := installed.ViewerProjectionScope(ctx, userID)
			if err != nil {
				return value, err
			}
			scope = &value
		}
		return *scope, nil
	}
	fail := func(err error) (domain.Bootstrap, func(), bool) {
		release()
		issueRecordsError(w, err)
		return domain.Bootstrap{}, nil, false
	}
	forbid := func(message string) (domain.Bootstrap, func(), bool) {
		release()
		writeError(w, http.StatusForbidden, message)
		return domain.Bootstrap{}, nil, false
	}
	if input.ProjectID != nil && *input.ProjectID != "" {
		visible, err := projection()
		if err != nil {
			return fail(err)
		}
		ok, err := installed.ProjectVisibleInProjection(context.WithoutCancel(ctx), visible, *input.ProjectID, credential)
		if err != nil {
			return fail(err)
		}
		if !ok {
			return forbid("Project is outside your teams")
		}
	}
	if input.CycleID != nil && *input.CycleID != "" {
		visible, err := projection()
		if err != nil {
			return fail(err)
		}
		ok, err := installed.CycleVisibleInProjection(context.WithoutCancel(ctx), visible, *input.CycleID, credential)
		if err != nil {
			return fail(err)
		}
		if !ok {
			return forbid("Cycle is outside your teams")
		}
	}
	if input.AssigneeID != nil && *input.AssigneeID != "" && !slices.ContainsFunc(metadata.Users, func(user domain.User) bool { return user.ID == *input.AssigneeID }) && !s.formerMemberOutsideWrite(ctx, workspace, *input.AssigneeID) {
		return forbid("Assignee is outside this workspace")
	}
	return metadata, release, true
}

// boundedIssueUpdatePresence is the destination checks of updateIssueRecord
// without authentication, whose metadata is the complete unprojected
// workspace: the project, cycle and assignee must exist in the installed
// projects, cycles and users. On failure it has written the response.
func (s *server) boundedIssueUpdatePresence(w http.ResponseWriter, r *http.Request, workspace string, input domain.IssueUpdateInput) bool {
	ctx := context.WithoutCancel(r.Context())
	checks := []struct {
		kind, message string
		id            *string
	}{
		{"projects", "Project is outside your teams", input.ProjectID},
		{"cycles", "Cycle is outside your teams", input.CycleID},
		{"users", "Assignee is outside this workspace", input.AssigneeID},
	}
	var installed *store.InstalledMetadataRead
	for _, check := range checks {
		if check.id == nil || *check.id == "" {
			continue
		}
		if installed == nil {
			read, found, err := s.store.BeginInstalledMetadataRead(ctx, workspace)
			if err != nil {
				issueRecordsError(w, err)
				return false
			}
			if !found {
				// The legacy lookups find nothing in a missing workspace.
				writeError(w, http.StatusForbidden, check.message)
				return false
			}
			defer read.Close()
			installed = read
		}
		present, err := installed.RecordIDPresent(ctx, check.kind, *check.id)
		if err != nil {
			issueRecordsError(w, err)
			return false
		}
		if !present && (check.kind != "users" || !s.formerMemberOutsideWrite(r.Context(), workspace, *check.id)) {
			writeError(w, http.StatusForbidden, check.message)
			return false
		}
	}
	return true
}
