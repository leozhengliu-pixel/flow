package main

import (
	"context"
	"net/http"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) commentActionReadInputs(r *http.Request, issueID string, validation bool) (domain.Bootstrap, *store.IssueWriteAuthorizationRead, func(), error) {
	unchanged := func() {}
	// The list's original workspaceData observes its explicit alias handoff.
	// Start instead explicitly prefers the bounded authorization handoff.
	if validation {
		if data, handed := r.Context().Value(issueRecordAuthorizationContext{}).(domain.Bootstrap); handed {
			return data, nil, unchanged, nil
		}
	}
	if read, handed := r.Context().Value(boundedIssueWriteAuthorizationContext{}).(*store.IssueWriteAuthorizationRead); handed {
		if read == nil {
			return domain.Bootstrap{}, nil, unchanged, store.ErrMetadataRecordQuery
		}
		return read.Data, read, unchanged, nil
	}
	if data, handed := r.Context().Value(issueRecordAuthorizationContext{}).(domain.Bootstrap); handed {
		return data, nil, unchanged, nil
	}
	metadata, access, err := s.requestIssueQueryAccess(r)
	if err != nil {
		return domain.Bootstrap{}, nil, unchanged, err
	}
	query := store.IssueRecordQuery{Workspace: metadata.Workspace.URLKey}
	query.Access, query.AllowedTeamIDs = s.issueRecordAccess(r, metadata, access)
	userID := ""
	var credential *domain.APIKey
	if !s.authDisabled {
		userID = authUser(r).ID
		if key, found := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); found {
			credential = &key
		}
	}
	read, err := s.store.BeginIssueWriteAuthorizationRead(r.Context(), userID, credential, store.IssueWriteAuthorizationRequest{
		Query: query, IDs: []string{issueID}, CommentActionValidation: validation,
	})
	if err != nil {
		return domain.Bootstrap{}, nil, unchanged, err
	}
	return read.Data, read, read.Close, nil
}

func (s *server) commentActionReadSettings(r *http.Request, data *domain.Bootstrap, authorization *store.IssueWriteAuthorizationRead) (func(), error) {
	release := func() {}
	var settings domain.UserSettings
	var found bool
	if authorization != nil {
		var err error
		settings, found, err = authorization.CurrentCommentActionSettings(r.Context(), data.Viewer.ID)
		if err != nil {
			return release, err
		}
	} else {
		// The existing Data-only alias handoff remains caller-owned. Its
		// settings refresh is a separate current installed selection.
		read, ok, err := s.store.BeginAccountCollectionRead(context.WithoutCancel(r.Context()), data.Workspace.URLKey, data.Viewer.ID, false)
		if err != nil || !ok {
			return release, err
		}
		settings, found, release = read.Settings, true, read.Close
	}
	if found && settings.UserID != "" {
		data.UserSettings = map[string]domain.UserSettings{data.Viewer.ID: settings}
	}
	return release, nil
}

// The returned tuple is the synchronous gate used by the production launch.
func (s *server) commentActionStartRead(r *http.Request, issueID string, comment domain.Comment) (string, domain.User, bool) {
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok && !apiKeyHasScope(key, "write") {
		return "", domain.User{}, false
	}
	if comment.User.App || comment.BotActor != nil || comment.User.Via != nil {
		return "", domain.User{}, false
	}
	data, authorization, closeInputs, err := s.commentActionReadInputs(r, issueID, false)
	if err != nil {
		return "", domain.User{}, false
	}
	defer closeInputs()
	data.Viewer = requestActor(s, r)
	closeSettings, err := s.commentActionReadSettings(r, &data, authorization)
	if err != nil {
		return "", domain.User{}, false
	}
	defer closeSettings()
	issue, err := issueByID(&data, issueID)
	if err != nil || !s.commentActionsAllowed(data, *issue) {
		return "", domain.User{}, false
	}
	return data.Workspace.URLKey, data.Viewer, true
}

func (s *server) startCommentActionsBounded(r *http.Request, issueID string, comment domain.Comment) {
	workspace, actor, allowed := s.commentActionStartRead(r, issueID, comment)
	if !allowed {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(store.ContextWithActor(context.Background(), actor), 90*time.Second)
		defer cancel()
		select {
		case commentActionSlots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-commentActionSlots }()
		s.generateCommentActions(ctx, workspace, issueID, comment.ID, actor)
	}()
}

func (s *server) listCommentActionsBounded(w http.ResponseWriter, r *http.Request) {
	data, authorization, closeInputs, err := s.commentActionReadInputs(r, r.PathValue("id"), true)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	defer closeInputs()
	data.Viewer = requestActor(s, r)
	closeSettings, err := s.commentActionReadSettings(r, &data, authorization)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	defer closeSettings()
	issue, err := issueByID(&data, r.PathValue("id"))
	if err != nil {
		respondMutation(w, err, 200, nil)
		return
	}
	result := []domain.CommentAction{}
	if !s.commentActionsAllowed(data, *issue) {
		writeJSON(w, http.StatusOK, result)
		return
	}
	// WorkspaceMetadata is contextless and does not inherit an earlier
	// authorization generation. Actions and validation share this fresh source.
	rawContext := context.Background()
	installed, found, err := s.store.BeginInstalledMetadataRead(rawContext, data.Workspace.URLKey)
	if err != nil || !found {
		if err != nil {
			issueRecordsError(w, err)
		} else {
			writeJSON(w, http.StatusOK, result)
		}
		return
	}
	actions, err := installed.ReadCommentActions(rawContext, issue.ID)
	installed.Close()
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	transferred := false
	defer func() {
		if !transferred {
			actions.Close()
		}
	}()
	loadedValidation := false
	for _, action := range actions.Actions {
		if action.IssueID != issue.ID || action.Status == "dismissed" {
			continue
		}
		comment, err := s.store.ResourceComment(r.Context(), data.Workspace.URLKey, issue.ID, action.CommentID)
		if err != nil || action.Status == "pending" && comment.Version != action.CommentVersion {
			continue
		}
		if action.Status != "accepted" {
			if !loadedValidation {
				if err := actions.LoadValidationHeaders(rawContext); err != nil {
					issueRecordsError(w, err)
					return
				}
				loadedValidation = true
			}
			if validateCommentAction(&actions.Validation, *issue, action) != nil {
				continue
			}
		}
		result = append(result, action)
	}
	if err := actions.TransferOutput(r.Context()); err != nil {
		issueRecordsError(w, err)
		return
	}
	transferred = true
	writeJSON(w, http.StatusOK, result)
}
