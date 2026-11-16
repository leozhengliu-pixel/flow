package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"slices"
	"strings"
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
// renumbering through the writer's bounded team-move closure, after the
// literal move permission check.
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
	var moveTeams []string
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
			// Accepting a team suggestion moves the issue like the team
			// picker: the literal check over the viewer's projected
			// workspace (teams, members and role need no collection).
			visible, release, visibleErr := s.mcpWorkspaceDataProjected(ctx, actor, installed)
			defer release()
			if visibleErr != nil {
				respondMutation(w, visibleErr, http.StatusOK, updated)
				return
			}
			if message := issueTeamMoveForbidden(s, visible, item.SuggestedTeamID); message != "" {
				writeError(w, http.StatusForbidden, message)
				return
			}
			moveTeams = []string{item.SuggestedTeamID}
		}
	}
	scopeIDs := []string{issueID}
	if targetID != "" {
		scopeIDs = append(scopeIDs, targetID)
	}
	owned, closeOwned := beginIndependentMetadataOutputReads(ctx)
	defer closeOwned()
	if moveTeams != nil {
		owned = store.WithIssueRenumbering(owned)
	}
	write := store.ContextWithIssueSuggestionWrite(store.WithIssueRecordMutations(owned, scopeIDs...), store.IssueSuggestionWrite{Teams: moveTeams})
	err = s.store.MutateWorkspace(write, workspaceKey(r), eventType, suggestionID, nil, issueSuggestionStateMutation(issueID, suggestionID, accept, &updated))
	respondMutation(w, err, http.StatusOK, updated)
}

// generateTriageIntelligenceForIssuesBounded is generateTriageIntelligenceForIssues
// over owned installed reads (Viewer fallback, workspace and team settings
// points, contextless like WorkspaceMetadata) and the bounded issue suggestion
// writer with complete scoring inputs. The literal callback is shared. A
// workspace whose team suggestions may auto-move issues writes with
// renumbering, declaring the candidates' teams as possible targets.
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
	candidates, err := s.store.BeginIssueSuggestionCandidates(ctx)
	if err != nil {
		return nil, err
	}
	defer candidates.Close()
	for _, issueID := range issueIDs {
		// The authorization/detail row is needed only while scoring this
		// issue's candidate query. Candidate summaries have their own values.
		if err := func() error {
			readCtx, closeRead := beginIndependentMetadataOutputReads(ctx)
			defer closeRead()
			readCtx = candidates.Context(readCtx)
			issue, readErr := s.store.AuthorizedIssueRecord(readCtx, query, issueID)
			if readErr != nil {
				return nil
			}
			if ok, err := isTriage(&issue); err != nil {
				return err
			} else if !ok {
				return nil
			}
			if err := candidates.Start(readCtx, issueID); err != nil {
				return err
			}
			if err := s.triageCandidateIssuesVisit(readCtx, &issue, query, func(candidate domain.Issue) error {
				return candidates.Append(readCtx, candidate)
			}); err != nil {
				return err
			}
			return candidates.Finish()
		}(); err != nil {
			return nil, err
		}
	}
	owned, closeOwned := beginMetadataOutputReads(ctx)
	defer closeOwned()
	input := store.IssueSuggestionWrite{Complete: true}
	if triageTeamAutoMove(core.Data.WorkspaceSettings.FeatureSettings.TriageIntelligence) {
		// inferTeam proposes the teams of scoring candidates besides the
		// store-derived ones (scoped issues and their projects).
		owned = store.WithIssueRenumbering(owned)
		input.Teams = candidates.Teams(issueIDs)
	}
	write := store.ContextWithIssueSuggestionWrite(store.WithIssueRecordMutations(owned, issueIDs...), input)
	err = s.store.MutateWorkspace(write, workspace, "issue.suggestions_generated", "issue_suggestions_batch", nil, generateBoundedTriageMutation(write, issueIDs, candidates, &updated, &results, &generated))
	if err == nil && !generated {
		return nil, store.ErrNoMutation
	}
	return results, err
}

func generateBoundedTriageMutation(ctx context.Context, issueIDs []string, source *store.IssueSuggestionCandidates, updated *domain.Issue, results *[]domain.Issue, generated *bool) func(*domain.Bootstrap) error {
	return func(data *domain.Bootstrap) error {
		scoped := len(data.Issues)
		var encountered []string
		maxNumber := 0
		for _, issueID := range issueIDs {
			issue, err := issueByID(data, issueID)
			var fallback *domain.Issue
			if err != nil {
				if mutation, found := source.Mutation(issueID); found {
					fallback = &mutation
				}
				for _, id := range encountered {
					if fallback != nil {
						break
					}
					if err := source.Walk(ctx, id, func(candidate domain.Issue) (bool, error) {
						if candidate.ID != issueID {
							return true, nil
						}
						if err := source.KeepResult(ctx, candidate); err != nil {
							return false, err
						}
						fallback = &candidate
						return false, nil
					}); err != nil {
						return err
					}
					if fallback != nil {
						break
					}
				}
				issue = fallback
			}
			if issue == nil || !triageIntelligenceEnabled(data.WorkspaceSettings) || !isTriageIssue(data, issue) {
				continue
			}
			encountered = append(encountered, issueID)
			maxNumber = max(maxNumber, source.Number(issueID))
			if err := func() error {
				scores, err := source.BeginScores(ctx)
				if err != nil {
					return err
				}
				defer scores.Close()
				score := triageIssueScorer(issue)
				ranked := make([]triageSuggestionCandidate, 0, min(scoped, maxTriageSuggestionCandidates))
				mutated := map[string]bool{}
				visit := func(candidate domain.Issue) (bool, error) {
					if len(ranked) >= maxTriageSuggestionCandidates {
						return false, nil
					}
					if mutation, found := source.Mutation(candidate.ID); found && !mutated[candidate.ID] {
						candidate = mutation
						mutated[candidate.ID] = true
					}
					if candidate, ok := score(candidate); ok {
						candidate.issue, err = scores.Project(ctx, candidate.issue)
						if err != nil {
							return false, err
						}
						ranked = append(ranked, candidate)
					}
					return len(ranked) < maxTriageSuggestionCandidates, nil
				}
				for _, candidate := range data.Issues[:scoped] {
					if more, err := visit(candidate); err != nil {
						return err
					} else if !more {
						break
					}
				}
				for _, id := range encountered {
					if len(ranked) >= maxTriageSuggestionCandidates {
						break
					}
					if err := source.Walk(ctx, id, visit); err != nil {
						return err
					}
				}
				sortTriageSuggestionCandidates(ranked)
				now := time.Now().UTC()
				items := generateIssueSuggestionsWithCandidates(data, issue, now, ranked)
				if fallback != nil {
					data.Issues = append(data.Issues, *fallback)
				}
				// These IDs can outlive the current scoring snapshots.
				for index := range items {
					item := &items[index]
					item.SuggestedIssueID = strings.Clone(item.SuggestedIssueID)
					item.SuggestedProjectID = strings.Clone(item.SuggestedProjectID)
					item.SuggestedUserID = strings.Clone(item.SuggestedUserID)
					item.SuggestedLabelID = strings.Clone(item.SuggestedLabelID)
					item.SuggestedTeamID = strings.Clone(item.SuggestedTeamID)
					if item.SuggestedIssueID != "" && issueByIDValue(data, item.SuggestedIssueID) == nil {
						var target *domain.Issue
						if mutation, found := source.Mutation(item.SuggestedIssueID); found {
							target = &mutation
						} else {
							for _, id := range encountered {
								if err := source.Walk(ctx, id, func(candidate domain.Issue) (bool, error) {
									if candidate.ID != item.SuggestedIssueID {
										return true, nil
									}
									if err := scores.Keep(ctx, candidate); err != nil {
										return false, err
									}
									target = &candidate
									return false, nil
								}); err != nil {
									return err
								}
								if target != nil {
									break
								}
							}
						}
						if target != nil {
							data.Issues = append(data.Issues, *target)
						}
					}
				}
				// Team moves read the maximum number of every encountered
				// summary, even summaries outside the scoring cutoff.
				data.Issues = append(data.Issues, domain.Issue{Number: maxNumber})
				issue, err = issueByID(data, issueID)
				if err != nil {
					return err
				}
				storeTriageSuggestions(data, issue, items, now, triageSourceHeuristic, "")
				*updated = *issue
				if fallback != nil {
					if err := source.KeepResult(ctx, *updated); err != nil {
						return err
					}
				}
				*results = append(*results, *updated)
				*generated = true
				for _, candidate := range data.Issues[scoped : len(data.Issues)-1] {
					if err := source.SetMutation(ctx, candidate); err != nil {
						return err
					}
					maxNumber = max(maxNumber, candidate.Number)
				}
				data.Issues = data.Issues[:scoped]
				return nil
			}(); err != nil {
				return err
			}
		}
		allowed := map[string]bool{}
		for _, id := range issueIDs {
			allowed[id] = true
		}
		data.Issues = slices.DeleteFunc(data.Issues[:scoped], func(item domain.Issue) bool { return !allowed[item.ID] })
		if !*generated {
			return store.ErrNoMutation
		}
		return nil
	}
}

// mcpWorkspaceDataProjected is mcpWorkspaceData whose paged projection runs
// over installed's core and policy fields and the named complete
// collections (see ProjectedWorkspace). Every later credential, application
// and preference filter is the literal mcpWorkspaceData code. The result is
// exact for those collections, policy fields and the live directory; the
// returned release must outlive every use of the data.
func (s *server) mcpWorkspaceDataProjected(ctx context.Context, actor mcpActor, installed *store.InstalledMetadataRead, collections ...string) (domain.Bootstrap, func(), error) {
	var reads []*store.ProjectedWorkspaceRead
	release := func() {
		for _, read := range reads {
			read.Close()
		}
	}
	data, err := s.mcpWorkspaceDataFrom(ctx, actor, func(ctx context.Context, _ string, userID string) (domain.Bootstrap, error) {
		read, err := installed.ProjectedWorkspace(ctx, userID, collections...)
		if err != nil {
			return domain.Bootstrap{}, err
		}
		reads = append(reads, read)
		return read.Data, nil
	})
	return data, release, err
}

// triageWorkspaceCollections are the complete collections the triage
// scorers read from the viewer's projected workspace (labelGroups and
// initiatives keep label groups and label resource counts exact).
var triageWorkspaceCollections = []string{"projects", "labels", "labelGroups", "initiatives"}

// previewIssueSuggestionsBounded is previewIssueSuggestions over the
// installed Viewer fallback and an owned exact projection of the collections
// the preview scores; the scoring code is shared.
func (s *server) previewIssueSuggestionsBounded(w http.ResponseWriter, r *http.Request, text, teamID string) {
	ctx := r.Context()
	actor := mcpActor{WorkspaceKey: workspaceKey(r), User: authUser(r)}
	if actor.User.ID == "" {
		// Legacy WorkspaceMetadata ignores cancellation.
		viewer, ok, err := s.store.BeginInstalledViewerRead(context.WithoutCancel(ctx), actor.WorkspaceKey)
		if err != nil {
			issueRecordsError(w, errors.Join(errors.New("installed viewer"), err))
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
	data, release, err := s.triageAIWorkspaceBounded(ctx, actor)
	if release != nil {
		defer release()
	}
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	s.writeIssueSuggestionPreview(w, r, actor, data, text, teamID)
}

// triageAIWorkspaceBounded replaces mcpWorkspaceData for the triage scorers.
// A missing installed workspace fails as the paged accessor does.
func (s *server) triageAIWorkspaceBounded(ctx context.Context, actor mcpActor) (domain.Bootstrap, func(), error) {
	installed, found, err := s.store.BeginInstalledMetadataRead(context.WithoutCancel(ctx), actor.WorkspaceKey)
	if err != nil {
		return domain.Bootstrap{}, nil, err
	}
	if !found {
		return domain.Bootstrap{}, nil, errors.New("workspace access denied")
	}
	data, release, err := s.mcpWorkspaceDataProjected(ctx, actor, installed, triageWorkspaceCollections...)
	return data, func() {
		release()
		installed.Close()
	}, err
}

// runTriageIntelligenceBounded is runTriageIntelligence after the issue read,
// over owned unprojected scoring inputs (contextless like WorkspaceMetadata)
// and the bounded suggestion writer, which receives the issue scope and the
// targets the run generated. A run that may auto-move the issue to another
// team writes with renumbering, its generated team targets declared.
func (s *server) runTriageIntelligenceBounded(ctx context.Context, actor mcpActor, query store.IssueRecordQuery, issue domain.Issue, issueID string, options triageRunOptions) (domain.Issue, []domain.IssueSuggestion, error) {
	reads := context.WithoutCancel(ctx)
	installed, found, err := s.store.BeginInstalledMetadataRead(reads, actor.WorkspaceKey)
	if err != nil {
		return domain.Issue{}, nil, err
	}
	var metadata domain.Bootstrap
	if found {
		defer installed.Close()
		scoring, err := installed.TriageScoringInputs(reads)
		if err != nil {
			return domain.Issue{}, nil, err
		}
		defer scoring.Close()
		metadata = scoring.Data
	}
	return s.runTriageIntelligenceWith(ctx, actor, query, issue, issueID, options, metadata, found,
		func(ctx context.Context, scope []string, generated []domain.IssueSuggestion, teamMove bool) (context.Context, func(), error) {
			var input store.IssueSuggestionWrite
			for _, item := range generated {
				input.Users = append(input.Users, item.SuggestedUserID)
				input.Projects = append(input.Projects, item.SuggestedProjectID)
				input.Labels = append(input.Labels, item.SuggestedLabelID)
				input.Teams = append(input.Teams, item.SuggestedTeamID)
			}
			owned, release := beginMetadataOutputReads(ctx)
			if teamMove {
				owned = store.WithIssueRenumbering(owned)
			}
			return store.ContextWithIssueSuggestionWrite(store.WithIssueRecordMutations(owned, scope...), input), release, nil
		})
}
