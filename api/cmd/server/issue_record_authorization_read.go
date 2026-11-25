package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type boundedIssueRecordAuthorizationQueryContext struct{}

// The general alias authorization reads the same issue headers as child writes.
// Destination collections are used only for existence checks; they are never
// advertised as the complete workspace passed to a legacy handler.
func (s *server) boundedIssueRecordAuthorizationMetadata(r *http.Request) (domain.Bootstrap, error) {
	previous, _ := r.Context().Value(boundedIssueRecordAuthorizationQueryContext{}).(bool)
	*r = *r.WithContext(context.WithValue(r.Context(), boundedIssueRecordAuthorizationQueryContext{}, true))
	_, query, err := s.issueRecordsQuery(r)
	*r = *r.WithContext(context.WithValue(r.Context(), boundedIssueRecordAuthorizationQueryContext{}, previous))
	if err != nil {
		return domain.Bootstrap{}, err
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	ids := []string{}
	if len(parts) > 2 && parts[2] != "batch" {
		ids = append(ids, parts[2])
		if issueIdentifierPattern.MatchString(parts[2]) {
			if id, err := s.store.AuthorizedIssueRecordID(r.Context(), query, parts[2]); err == nil {
				ids = append(ids, id)
			}
		}
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var input map[string]json.RawMessage
		if peekRequestJSON(r, &input) {
			var batchIDs []string
			if json.Unmarshal(input["issueIds"], &batchIDs) == nil {
				ids = append(ids, batchIDs...)
			}
			for _, field := range []string{"parentId", "relatedIssueId"} {
				var id string
				if json.Unmarshal(input[field], &id) == nil && id != "" {
					ids = append(ids, id)
				}
			}
		}
	}
	request := store.IssueWriteAuthorizationRequest{Query: query, IDs: ids}
	if len(parts) > 4 && parts[3] == "comments" {
		request.CommentIssueID, request.CommentID = parts[2], parts[4]
	}
	userID := ""
	var credential *domain.APIKey
	if !s.authDisabled {
		userID = authUser(r).ID
		if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
			credential = &key
		}
	}
	read, err := s.store.BeginIssueWriteAuthorizationRead(r.Context(), userID, credential, request)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	ctx, err := read.Context(r.Context())
	if err != nil {
		read.Close()
		return domain.Bootstrap{}, err
	}
	if err := read.TransferOutput(ctx); err != nil {
		return domain.Bootstrap{}, err
	}
	ctx = context.WithValue(ctx, boundedIssueWriteAuthorizationContext{}, read)
	*r = *r.WithContext(ctx)
	data := read.Data
	scope, err := read.AuthorizationProjectionScope(ctx)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	var update domain.IssueUpdateInput
	destinationChecks := false
	if r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "batch" {
		var input domain.BatchIssueUpdateInput
		if peekRequestJSON(r, &input) {
			update, destinationChecks = input.Update, true
		}
	} else if r.Method == http.MethodPatch && len(parts) >= 3 {
		destinationChecks = peekRequestJSON(r, &update)
	} else if r.Method == http.MethodPost && len(parts) == 2 {
		var input domain.IssueCreateInput
		if peekRequestJSON(r, &input) {
			update = domain.IssueUpdateInput{ProjectID: input.ProjectID, CycleID: input.CycleID, AssigneeID: input.AssigneeID}
			destinationChecks = true
		}
	}
	if destinationChecks {
		data, err = s.boundedIssueRecordAuthorizationTargets(r, data, scope, userID, credential, update)
		if err != nil {
			return domain.Bootstrap{}, err
		}
	}
	if len(parts) >= 4 && parts[3] == "releases" && r.Method == http.MethodPut {
		var input issueReleasesInput
		if peekRequestJSON(r, &input) {
			installed, found, err := s.store.BeginInstalledMetadataRead(r.Context(), data.Workspace.URLKey)
			if err != nil {
				return domain.Bootstrap{}, err
			}
			if !found {
				return domain.Bootstrap{}, store.ErrAuthForbidden
			}
			defer installed.Close()
			var visible *store.ViewerProjectionScope
			if !s.authDisabled {
				visible = &scope
			}
			var allows func(string) bool
			if credential != nil && apiKeyTeamRestrictionSelected(*credential) {
				allows = func(id string) bool { return slices.Contains(credential.TeamIDs, id) }
			}
			for _, id := range input.ReleaseIDs {
				ok, err := installed.IssueAuthorizationReleaseVisible(r.Context(), visible, id, allows)
				if err != nil {
					return domain.Bootstrap{}, err
				}
				if ok {
					data.Releases = append(data.Releases, domain.Release{ID: id})
				}
			}
		}
	}
	return data, nil
}

func (s *server) boundedIssueRecordAuthorizationTargets(r *http.Request, data domain.Bootstrap, scope store.ViewerProjectionScope, userID string, key *domain.APIKey, update domain.IssueUpdateInput) (domain.Bootstrap, error) {
	if update.ProjectID == nil && update.CycleID == nil && update.AssigneeID == nil {
		return data, nil
	}
	installed, found, err := s.store.BeginInstalledMetadataRead(r.Context(), data.Workspace.URLKey)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	if !found {
		return domain.Bootstrap{}, store.ErrAuthForbidden
	}
	defer installed.Close()
	if s.authDisabled {
		for _, target := range []struct {
			kind string
			id   *string
		}{{"projects", update.ProjectID}, {"cycles", update.CycleID}, {"users", update.AssigneeID}} {
			if target.id == nil || *target.id == "" {
				continue
			}
			ok, err := installed.RecordIDPresent(r.Context(), target.kind, *target.id)
			if err != nil {
				return domain.Bootstrap{}, err
			}
			if ok {
				switch target.kind {
				case "projects":
					data.Projects = []domain.Project{{ID: *target.id}}
				case "cycles":
					data.Cycles = []domain.Cycle{{ID: *target.id}}
				case "users":
					data.Users = []domain.User{{ID: *target.id}}
				}
			}
		}
		return data, nil
	}
	var credential func(string) bool
	if key != nil && apiKeyTeamRestrictionSelected(*key) {
		credential = func(id string) bool { return slices.Contains(key.TeamIDs, id) }
	}
	if update.ProjectID != nil && *update.ProjectID != "" {
		ok, err := installed.ProjectVisibleInProjection(r.Context(), scope, *update.ProjectID, credential)
		if err != nil {
			return domain.Bootstrap{}, err
		}
		if ok {
			data.Projects = []domain.Project{{ID: *update.ProjectID}}
		}
	}
	if update.CycleID != nil && *update.CycleID != "" {
		ok, err := installed.CycleVisibleInProjection(r.Context(), scope, *update.CycleID, credential)
		if err != nil {
			return domain.Bootstrap{}, err
		}
		if ok {
			data.Cycles = []domain.Cycle{{ID: *update.CycleID}}
		}
	}
	if update.AssigneeID != nil && *update.AssigneeID != "" {
		projected, err := installed.ProjectedWorkspace(r.Context(), userID)
		if err != nil {
			return domain.Bootstrap{}, err
		}
		if !registerRequestReadClose(r.Context(), projected.Close) {
			projected.Close()
			return domain.Bootstrap{}, store.ErrBoundedMetadataCoverage
		}
		data.Users = projected.Data.Users
	}
	return data, nil
}
