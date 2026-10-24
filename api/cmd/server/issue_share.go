package main

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

const sharedResourceNotFoundMessage = "shared resource not found"

// Shared links must not reveal whether the workspace, token, or backing
// resource exists. All public share handlers use the same 404 response so an
// unauthenticated caller cannot enumerate workspace keys through error text.
func writeSharedResourceNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, sharedResourceNotFoundMessage)
}

func (s *server) shareIssue(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var issue domain.Issue
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue.shared", id, nil, func(data *domain.Bootstrap) error {
		item, err := issueByID(data, id)
		if err != nil {
			return err
		}
		if issueRole(s, *data, *item) != "owner" {
			return errNotFound
		}
		settings := teamSettings(data, item.Team.ID)
		if !settings.IssueSharingEnabled {
			return fmt.Errorf("%w: issue sharing is disabled for this team", errInvalid)
		}
		if item.ShareToken == "" {
			token, tokenErr := randomSecret("issue_share_")
			if tokenErr != nil {
				return tokenErr
			}
			now := time.Now().UTC()
			item.ShareToken, item.SharedAt = token, &now
		}
		issue = *item
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"issue": issue, "token": issue.ShareToken, "url": "/shared/issues/" + url.PathEscape(issue.ShareToken) + "?workspace=" + url.QueryEscape(workspaceKey(r))})
}

func (s *server) unshareIssue(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue.unshared", id, nil, func(data *domain.Bootstrap) error {
		item, err := issueByID(data, id)
		if err != nil {
			return err
		}
		if issueRole(s, *data, *item) != "owner" {
			return errNotFound
		}
		item.ShareToken, item.SharedAt = "", nil
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) getSharedIssue(w http.ResponseWriter, r *http.Request) {
	workspace := strings.TrimSpace(r.URL.Query().Get("workspace"))
	if workspace == "" {
		writeError(w, http.StatusBadRequest, "workspace is required")
		return
	}
	data, ok := s.store.BootstrapFor(workspace)
	if !ok {
		writeSharedResourceNotFound(w)
		return
	}
	token := r.PathValue("token")
	for _, item := range data.Issues {
		if item.ShareToken != token {
			continue
		}
		// Shared links intentionally return a narrow read-only projection. The
		// token itself is not echoed back and private team membership is omitted.
		item.ShareToken = ""
		item.SharedAt = nil
		item.Team = domain.Team{}
		item.Assignee = nil
		item.Delegate = nil
		item.Creator.Email = ""
		comments := slices.Clone(data.Comments[item.ID])
		if comments == nil {
			comments = []domain.Comment{}
		}
		for index := range comments {
			comments[index].User.Email = ""
		}
		writeJSON(w, http.StatusOK, map[string]any{"issue": item, "comments": comments, "workspace": data.Workspace})
		return
	}
	writeSharedResourceNotFound(w)
}
