package main

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// listIssueSuggestionsBounded is listIssueSuggestions over owned installed
// reads. The literal handler reads the full paged projection only for its
// error, its team rows and its project filter; the issue's suggestions, core
// workspace, workspace settings and one team's settings come from the same
// installed generation, never from a full metadata decode.
func (s *server) listIssueSuggestionsBounded(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor := mcpActor{WorkspaceKey: workspaceKey(r), User: authUser(r)}
	if actor.User.ID == "" {
		// Legacy WorkspaceMetadata ignores cancellation.
		read, ok, err := s.store.BeginInstalledViewerRead(context.WithoutCancel(ctx), actor.WorkspaceKey)
		if err != nil {
			issueRecordsError(w, errors.Join(errors.New("installed viewer"), err))
			return
		}
		if ok {
			defer read.Close()
			actor.User = read.Viewer()
		}
	}
	if key, ok := ctx.Value(apiKeyContextKey{}).(domain.APIKey); ok {
		actor.APIKey = key
	}
	installed, found, err := s.store.BeginInstalledMetadataRead(context.WithoutCancel(ctx), actor.WorkspaceKey)
	if err != nil {
		issueRecordsError(w, errors.Join(errors.New("installed metadata"), err))
		return
	}
	if !found {
		// mcpWorkspaceData reports a missing workspace as access denied.
		issueRecordsError(w, errors.New("workspace access denied"))
		return
	}
	defer installed.Close()
	// mcpWorkspaceData flattens every projection error, including
	// cancellation, into one unwrapped error.
	scope, err := installed.ViewerProjectionScope(ctx, actor.User.ID)
	if err != nil {
		issueRecordsError(w, errors.New("workspace access denied"))
		return
	}
	query, err := s.mcpIssueQuery(ctx, actor)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	issue, err := s.store.AuthorizedIssueRecord(ctx, query, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	core, err := installed.Select(context.WithoutCancel(ctx), store.MetadataRecordScope{})
	if err != nil {
		issueRecordsError(w, errors.Join(errors.New("installed workspace"), err))
		return
	}
	defer core.Close()
	runActor := actor
	runActor.WorkspaceKey = firstNonEmpty(core.Data.Workspace.URLKey, actor.WorkspaceKey)
	if s.agent.Enabled {
		selection := store.MetadataRecordScope{Fields: []string{"workspaceSettings"}}
		if issue.Team.ID != "" {
			selection.Read = []store.RecordKey{{Kind: "teamSettings", ID: issue.Team.ID}}
		}
		settings, err := installed.Select(context.WithoutCancel(ctx), selection)
		if err != nil {
			issueRecordsError(w, errors.Join(errors.New("installed settings"), err))
			return
		}
		defer settings.Close()
		if triageIntelligenceEnabled(settings.Data.WorkspaceSettings) && isTriageIssue(&settings.Data, &issue) {
			// See listIssueSuggestions: self-heal a lost pending run and
			// replace heuristic suggestions once the model is available.
			lost := issue.SuggestionsGeneratedAt == nil && time.Since(issue.UpdatedAt) > s.triageIntelligenceTimeout()+30*time.Second
			if lost || issue.SuggestionsGeneratedAt != nil {
				s.refreshTriageIntelligence(ctx, runActor, &issue, triageRefreshScope{upgrade: true})
			}
		}
	}
	running := s.triageRunActive(runActor.WorkspaceKey, issue.ID)
	selected, err := installed.ReadIssueSuggestions(context.WithoutCancel(ctx), issue.ID)
	if err != nil {
		issueRecordsError(w, errors.Join(errors.New("installed suggestions"), err))
		return
	}
	defer selected.Close()
	// mcpWorkspaceData then applies a selected-team credential to team rows
	// and to the projected projects' remaining (content) teams.
	var credential func(string) bool
	if apiKeyTeamRestrictionSelected(actor.APIKey) {
		credential = func(id string) bool { return slices.Contains(actor.APIKey.TeamIDs, id) }
	}
	// Only active suggestions reach the response; the literal filter's
	// target checks are reads without side effects.
	suggestions := make([]domain.IssueSuggestion, 0)
	for _, item := range selected.Suggestions {
		if item.IssueID != issue.ID || item.State != "active" {
			continue
		}
		if item.SuggestedIssueID != "" {
			if _, targetErr := s.store.AuthorizedIssueRecord(ctx, query, item.SuggestedIssueID); targetErr != nil {
				continue
			}
		}
		if item.SuggestedProjectID != "" {
			visible, visibleErr := installed.ProjectVisibleInProjection(context.WithoutCancel(ctx), scope, item.SuggestedProjectID, credential)
			if visibleErr != nil {
				issueRecordsError(w, errors.Join(errors.New("installed project"), visibleErr))
				return
			}
			if !visible {
				continue
			}
		}
		if item.SuggestedTeamID != "" && (!scope.Teams[item.SuggestedTeamID] || credential != nil && !credential(item.SuggestedTeamID)) {
			continue
		}
		suggestions = append(suggestions, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"issueId":                issue.ID,
		"suggestionsGeneratedAt": issue.SuggestionsGeneratedAt,
		"suggestions":            suggestions,
		"pending":                issue.SuggestionsGeneratedAt == nil || running,
		"source":                 issue.SuggestionsSource,
		"thinking":               issue.SuggestionsThinking,
	})
}
