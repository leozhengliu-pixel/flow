package main

import (
	"context"
	"errors"
	"log"
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

// staleIssueSuggestionIssues is the literal prune scan over one installed
// generation, decoding one suggestion at a time; like WorkspaceMetadata it
// ignores cancellation. A missing workspace reports false.
func (s *server) staleIssueSuggestionIssues(ctx context.Context, workspace string, stale func(domain.IssueSuggestion) bool) ([]string, bool) {
	ctx = context.WithoutCancel(ctx)
	installed, found, err := s.store.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil {
		log.Printf("prune issue suggestions workspace=%s: %v", workspace, err)
		return nil, false
	}
	if !found {
		return nil, false
	}
	defer installed.Close()
	ids := []string{}
	if err := installed.VisitIssueSuggestions(ctx, func(item domain.IssueSuggestion) error {
		if stale(item) && !slices.Contains(ids, item.IssueID) {
			ids = append(ids, item.IssueID)
		}
		return nil
	}); err != nil {
		log.Printf("prune issue suggestions workspace=%s: %v", workspace, err)
		return nil, false
	}
	return ids, true
}

// updateIssueSuggestionStateBounded is updateIssueSuggestionState over owned
// installed reads and the bounded issue suggestion writer. The literal state
// callback is shared. Accepting a team suggestion moves the issue with
// renumbering, which the bounded writer refuses.
func (s *server) updateIssueSuggestionStateBounded(w http.ResponseWriter, r *http.Request, accept bool) {
	issueID := r.PathValue("id")
	suggestionID := r.PathValue("suggestionId")
	eventType := "issue.suggestion_dismissed"
	if accept {
		eventType = "issue.suggestion_accepted"
	}
	var updated domain.IssueSuggestion
	ctx := r.Context()
	// Legacy WorkspaceMetadata ignores cancellation.
	installed, found, err := s.store.BeginInstalledMetadataRead(context.WithoutCancel(ctx), workspaceKey(r))
	if err != nil {
		respondMutation(w, err, http.StatusOK, updated)
		return
	}
	if !found {
		respondMutation(w, store.ErrAuthForbidden, http.StatusOK, updated)
		return
	}
	defer installed.Close()
	selected, err := installed.ReadIssueSuggestion(context.WithoutCancel(ctx), suggestionID)
	if err != nil {
		respondMutation(w, err, http.StatusOK, updated)
		return
	}
	defer selected.Close()
	var item *domain.IssueSuggestion
	var targetID string
	if len(selected.Suggestions) > 0 {
		item = &selected.Suggestions[0]
		targetID = item.SuggestedIssueID
	}
	actor := mcpActor{WorkspaceKey: workspaceKey(r), User: authUser(r)}
	if actor.User.ID == "" {
		viewer, ok, err := s.store.BeginInstalledViewerRead(context.WithoutCancel(ctx), actor.WorkspaceKey)
		if err != nil {
			respondMutation(w, err, http.StatusOK, updated)
			return
		}
		if ok {
			defer viewer.Close()
			actor.User = viewer.Viewer()
		}
	}
	if key, ok := ctx.Value(apiKeyContextKey{}).(domain.APIKey); ok {
		actor.APIKey = key
	}
	query, queryErr := s.mcpIssueQuery(ctx, actor)
	if queryErr != nil {
		respondMutation(w, queryErr, http.StatusOK, updated)
		return
	}
	if targetID != "" {
		if _, queryErr = s.store.AuthorizedIssueRecord(ctx, query, targetID); queryErr != nil {
			respondMutation(w, store.ErrAuthForbidden, http.StatusOK, updated)
			return
		}
	}
	scope, err := installed.ViewerProjectionScope(ctx, actor.User.ID)
	if err != nil {
		// mcpWorkspaceData flattens projection errors.
		respondMutation(w, errors.New("workspace access denied"), http.StatusOK, updated)
		return
	}
	if item != nil {
		var teamCredential func(string) bool
		labelCredential := func(string) bool { return true }
		if apiKeyTeamRestrictionSelected(actor.APIKey) {
			allowed := func(id string) bool { return slices.Contains(actor.APIKey.TeamIDs, id) }
			teamCredential = allowed
			labelCredential = func(scope string) bool {
				return (workspaceScope(scope) || allowed(scope)) && (labelScopeIsWorkspace(scope) || allowed(scope))
			}
		}
		forbidden := false
		if item.SuggestedProjectID != "" {
			visible, err := installed.ProjectVisibleInProjection(context.WithoutCancel(ctx), scope, item.SuggestedProjectID, teamCredential)
			if err != nil {
				respondMutation(w, err, http.StatusOK, updated)
				return
			}
			forbidden = !visible
		}
		if !forbidden && item.SuggestedTeamID != "" {
			forbidden = !scope.Teams[item.SuggestedTeamID] || teamCredential != nil && !teamCredential(item.SuggestedTeamID)
		}
		if !forbidden && item.SuggestedLabelID != "" {
			visible, err := installed.LabelVisibleInProjection(context.WithoutCancel(ctx), scope, item.SuggestedLabelID, labelCredential)
			if err != nil {
				respondMutation(w, err, http.StatusOK, updated)
				return
			}
			forbidden = !visible
		}
		if forbidden {
			respondMutation(w, store.ErrAuthForbidden, http.StatusOK, updated)
			return
		}
		if accept && item.Type == "team" && item.SuggestedTeamID != "" {
			// A team move renumbers the issue; the bounded writer refuses it.
			respondMutation(w, store.ErrIssueSuggestionWriteRefused, http.StatusOK, updated)
			return
		}
	}
	scopeIDs := []string{issueID}
	if targetID != "" {
		scopeIDs = append(scopeIDs, targetID)
	}
	owned, closeOwned := beginIndependentMetadataOutputReads(ctx)
	defer closeOwned()
	write := store.ContextWithIssueSuggestionWrite(store.WithIssueRecordMutations(owned, scopeIDs...), store.IssueSuggestionWrite{})
	err = s.store.MutateWorkspace(write, workspaceKey(r), eventType, suggestionID, nil, issueSuggestionStateMutation(issueID, suggestionID, accept, &updated))
	respondMutation(w, err, http.StatusOK, updated)
}

// generateTriageIntelligenceForIssuesBounded is generateTriageIntelligenceForIssues
// over owned installed reads (Viewer fallback, workspace and team settings
// points, contextless like WorkspaceMetadata) and the bounded issue suggestion
// writer with complete scoring inputs. The literal callback is shared. A
// workspace whose team suggestions may auto-move issues is refused before any
// write: the bounded writer does not renumber.
func (s *server) generateTriageIntelligenceForIssuesBounded(ctx context.Context, workspace string, issueIDs []string) ([]domain.Issue, error) {
	var updated domain.Issue
	results := make([]domain.Issue, 0, len(issueIDs))
	generated := false
	actor := mcpActor{WorkspaceKey: workspace}
	if user, ok := ctx.Value(authUserContextKey{}).(domain.User); ok {
		actor.User = user
	}
	reads := context.WithoutCancel(ctx)
	installed, found, err := s.store.BeginInstalledMetadataRead(reads, workspace)
	if err != nil {
		return nil, err
	}
	if found {
		defer installed.Close()
	}
	if actor.User.ID == "" && found {
		viewer, ok, err := s.store.BeginInstalledViewerRead(reads, workspace)
		if err != nil {
			return nil, err
		}
		if ok {
			defer viewer.Close()
			actor.User = viewer.Viewer()
		}
	}
	if key, ok := ctx.Value(apiKeyContextKey{}).(domain.APIKey); ok {
		actor.APIKey = key
	}
	query, err := s.mcpIssueQuery(ctx, actor)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, store.ErrNoMutation
	}
	core, err := installed.Select(reads, store.MetadataRecordScope{Fields: []string{"workspaceSettings"}})
	if err != nil {
		return nil, err
	}
	defer core.Close()
	if !triageIntelligenceEnabled(core.Data.WorkspaceSettings) {
		return nil, store.ErrNoMutation
	}
	// isTriageIssue reads only the issue team's TriageEnabled setting.
	triage := map[string]bool{}
	isTriage := func(issue *domain.Issue) (bool, error) {
		if issue.TriagedAt != nil || issue.State.Type != "backlog" {
			return false, nil
		}
		if enabled, seen := triage[issue.Team.ID]; seen {
			return enabled, nil
		}
		var selection store.MetadataRecordScope
		if issue.Team.ID != "" {
			selection.Read = []store.RecordKey{{Kind: "teamSettings", ID: issue.Team.ID}}
		}
		settings, err := installed.Select(reads, selection)
		if err != nil {
			return false, err
		}
		defer settings.Close()
		triage[issue.Team.ID] = isTriageIssue(&settings.Data, issue)
		return triage[issue.Team.ID], nil
	}
	if s.agent.Enabled {
		// See generateTriageIntelligenceForIssues: model runs start in the background.
		actor.WorkspaceKey = firstNonEmpty(core.Data.Workspace.URLKey, actor.WorkspaceKey)
		single := len(issueIDs) == 1
		for _, issueID := range issueIDs {
			issue, readErr := s.store.AuthorizedIssueRecord(ctx, query, issueID)
			if readErr != nil {
				continue
			}
			if ok, err := isTriage(&issue); err != nil {
				return nil, err
			} else if !ok {
				continue
			}
			s.refreshTriageIntelligence(ctx, actor, &issue, triageRefreshScope{upgrade: single, edits: single})
		}
		return nil, store.ErrNoMutation
	}
	candidateSets := map[string][]domain.Issue{}
	for _, issueID := range issueIDs {
		issue, readErr := s.store.AuthorizedIssueRecord(ctx, query, issueID)
		if readErr != nil {
			continue
		}
		if ok, err := isTriage(&issue); err != nil {
			return nil, err
		} else if !ok {
			continue
		}
		candidates, candidateErr := s.triageCandidateIssues(ctx, &issue, query)
		if candidateErr != nil {
			return nil, candidateErr
		}
		candidateSets[issueID] = candidates
	}
	if triageTeamAutoMove(core.Data.WorkspaceSettings.FeatureSettings.TriageIntelligence) {
		return nil, store.ErrIssueSuggestionWriteRefused
	}
	owned, closeOwned := beginMetadataOutputReads(ctx)
	defer closeOwned()
	write := store.ContextWithIssueSuggestionWrite(store.WithIssueRecordMutations(owned, issueIDs...), store.IssueSuggestionWrite{Complete: true})
	err = s.store.MutateWorkspace(write, workspace, "issue.suggestions_generated", "issue_suggestions_batch", nil, generateTriageMutation(issueIDs, candidateSets, &updated, &results, &generated))
	if err == nil && !generated {
		return nil, store.ErrNoMutation
	}
	return results, err
}
