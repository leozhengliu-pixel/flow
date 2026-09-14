package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

const (
	maxTriageSuggestionCandidates = 2000
	maxTriageSuggestionTextRunes  = 4000
)

type triageSuggestionCandidate struct {
	issue            domain.Issue
	score            float64
	titleScore       float64
	descriptionScore float64
}

type triageSuggestionPreview struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func isTriageIssue(data *domain.Bootstrap, issue *domain.Issue) bool {
	if issue == nil || issue.TriagedAt != nil || issue.State.Type != "backlog" {
		return false
	}
	settings := teamSettings(data, issue.Team.ID)
	return settings.TriageEnabled
}

func triageIntelligenceEnabled(settings domain.WorkspaceSettings) bool {
	return workspaceFeatureEnabled(settings, "triage-intelligence")
}

func triageIntelligenceWorkspaceEnabled(s *server, workspace string) bool {
	metadata, ok := s.store.WorkspaceSettingsMetadata(workspace)
	return ok && triageIntelligenceEnabled(metadata.WorkspaceSettings)
}

func generateAndAppendIssueSuggestions(data *domain.Bootstrap, issue *domain.Issue, now time.Time) {
	if !triageIntelligenceEnabled(data.WorkspaceSettings) || !isTriageIssue(data, issue) {
		return
	}
	dismissed := slices.DeleteFunc(slices.Clone(data.IssueSuggestions), func(item domain.IssueSuggestion) bool { return item.IssueID != issue.ID || item.State != "dismissed" })
	data.IssueSuggestions = slicesDeleteIssueSuggestions(data.IssueSuggestions, issue.ID, "")
	data.IssueSuggestions = append(data.IssueSuggestions, dismissed...)
	suggestions := generateIssueSuggestions(data, issue, now)
	for index := range suggestions {
		if applyTriageSuggestionAction(data, issue, &suggestions[index], now) {
			suggestions[index].State = "accepted"
			suggestions[index].StateChangedAt = now
		}
	}
	data.IssueSuggestions = append(data.IssueSuggestions, suggestions...)
	issue.SuggestionsGeneratedAt = &now
}

func (s *server) generateTriageIntelligenceForIssue(ctx context.Context, workspace, issueID string) (domain.Issue, error) {
	updated, err := s.generateTriageIntelligenceForIssues(ctx, workspace, []string{issueID})
	if len(updated) == 0 {
		return domain.Issue{}, err
	}
	return updated[0], err
}

func (s *server) generateTriageIntelligenceForIssues(ctx context.Context, workspace string, issueIDs []string) ([]domain.Issue, error) {
	var updated domain.Issue
	results := make([]domain.Issue, 0, len(issueIDs))
	generated := false
	actor := mcpActor{WorkspaceKey: workspace}
	if user, ok := ctx.Value(authUserContextKey{}).(domain.User); ok {
		actor.User = user
	}
	if actor.User.ID == "" {
		if metadata, ok := s.store.WorkspaceMetadata(workspace); ok {
			actor.User = metadata.Viewer
		}
	}
	if key, ok := ctx.Value(apiKeyContextKey{}).(domain.APIKey); ok {
		actor.APIKey = key
	}
	query, err := s.mcpIssueQuery(ctx, actor)
	if err != nil {
		return nil, err
	}
	metadata, ok := s.store.WorkspaceMetadata(workspace)
	if !ok || !triageIntelligenceEnabled(metadata.WorkspaceSettings) {
		return nil, store.ErrNoMutation
	}
	candidateSets := map[string][]domain.Issue{}
	for _, issueID := range issueIDs {
		issue, readErr := s.store.AuthorizedIssueRecord(ctx, query, issueID)
		if readErr != nil || !isTriageIssue(&metadata, &issue) {
			continue
		}
		candidates, candidateErr := s.triageCandidateIssues(ctx, &issue, query)
		if candidateErr != nil {
			return nil, candidateErr
		}
		candidateSets[issueID] = candidates
	}
	err = s.store.MutateWorkspace(store.WithIssueRecordMutations(ctx, issueIDs...), workspace, "issue.suggestions_generated", "issue_suggestions_batch", nil, func(data *domain.Bootstrap) error {
		for _, issueID := range issueIDs {
			issue, err := issueByID(data, issueID)
			if err != nil {
				continue
			}
			if !triageIntelligenceEnabled(data.WorkspaceSettings) || !isTriageIssue(data, issue) {
				continue
			}
			data.Issues = append(data.Issues, candidateSets[issueID]...)
			issue, err = issueByID(data, issueID)
			if err != nil {
				continue
			}
			generateAndAppendIssueSuggestions(data, issue, time.Now().UTC())
			updated = *issue
			results = append(results, updated)
			generated = true
		}
		allowed := map[string]bool{}
		for _, id := range issueIDs {
			allowed[id] = true
		}
		data.Issues = slices.DeleteFunc(data.Issues, func(item domain.Issue) bool { return !allowed[item.ID] })
		if !generated {
			return store.ErrNoMutation
		}
		return nil
	})
	if err == nil && !generated {
		return nil, store.ErrNoMutation
	}
	return results, err
}

func (s *server) triageCandidateIssues(ctx context.Context, issue *domain.Issue, q store.IssueRecordQuery) ([]domain.Issue, error) {
	if issue == nil || strings.TrimSpace(issue.Title) == "" {
		return nil, nil
	}
	q.Limit, q.Summary, q.Archived = maxTriageSuggestionCandidates, true, "false"
	terms := []string{issue.Title}
	for _, token := range strings.Fields(issue.Title) {
		if len([]rune(token)) >= 3 && !slices.Contains(terms, token) {
			terms = append(terms, token)
		}
		if len(terms) >= 8 {
			break
		}
	}
	items := []domain.Issue{}
	err := s.store.SearchIssueCandidateTerms(ctx, q, terms, nil, maxTriageSuggestionCandidates, func(candidate domain.Issue) error {
		if candidate.ID != issue.ID {
			items = append(items, candidate)
		}
		return nil
	})
	return items, err
}

func slicesDeleteIssueSuggestions(items []domain.IssueSuggestion, issueID, keepID string) []domain.IssueSuggestion {
	result := items[:0]
	for _, item := range items {
		if item.IssueID == issueID && item.ID != keepID {
			continue
		}
		result = append(result, item)
	}
	return result
}

func generateIssueSuggestions(data *domain.Bootstrap, issue *domain.Issue, now time.Time) []domain.IssueSuggestion {
	settings := data.WorkspaceSettings.FeatureSettings.TriageIntelligence
	candidates := similarTriageIssues(data, issue)
	result := make([]domain.IssueSuggestion, 0, 7)
	rank := 0
	add := func(kind, target string, score float64, reasons []string) {
		action, exists := triageSuggestionAction(settings, kind)
		if !exists || action == "hide" || strings.TrimSpace(target) == "" {
			return
		}
		rank++
		metadata := map[string]any{
			"rank":    rank,
			"score":   score,
			"reasons": reasons,
		}
		suggestion := domain.IssueSuggestion{
			ID:             fmt.Sprintf("issue_suggestion_%d_%d", now.UnixNano(), rank),
			IssueID:        issue.ID,
			Type:           kind,
			State:          "active",
			StateChangedAt: now,
			Metadata:       metadata,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		switch kind {
		case "assignee":
			suggestion.SuggestedUserID = target
		case "project":
			suggestion.SuggestedProjectID = target
		case "label":
			suggestion.SuggestedLabelID = target
		case "team":
			suggestion.SuggestedTeamID = target
		case "similarIssue", "relatedIssue":
			suggestion.SuggestedIssueID = target
		default:
			return
		}
		result = append(result, suggestion)
	}

	if len(candidates) > 0 {
		best := candidates[0]
		if best.score >= 0.52 {
			add("similarIssue", best.issue.ID, best.score, duplicateReasons(issue, &best.issue))
		}
		for _, candidate := range candidates {
			if candidate.score < 0.18 || (len(result) > 0 && candidate.issue.ID == best.issue.ID && best.score >= 0.52) {
				continue
			}
			add("relatedIssue", candidate.issue.ID, candidate.score, relatedReasons(issue, &candidate.issue))
			if countSuggestionType(result, "relatedIssue") >= 3 {
				break
			}
		}
	}

	if issue.Project == nil {
		if projectID, score, reasons := inferProject(data, issue, candidates); projectID != "" {
			add("project", projectID, score, reasons)
		}
	}
	if issue.Assignee == nil {
		if userID, score, reasons := inferAssignee(data, issue, candidates); userID != "" {
			add("assignee", userID, score, reasons)
		}
	}
	if labelID, score, reasons := inferLabel(data, issue, candidates); labelID != "" {
		add("label", labelID, score, reasons)
	}
	if teamID, score, reasons := inferTeam(data, issue, candidates); teamID != "" {
		add("team", teamID, score, reasons)
	}
	return result
}

func countSuggestionType(items []domain.IssueSuggestion, kind string) int {
	count := 0
	for _, item := range items {
		if item.Type == kind {
			count++
		}
	}
	return count
}

func triageSuggestionAction(settings domain.TriageIntelligenceSettings, kind string) (string, bool) {
	var action string
	switch kind {
	case "assignee":
		action = settings.AssigneeAction
	case "project":
		action = settings.ProjectAction
	case "label":
		action = settings.LabelAction
	case "team":
		action = settings.TeamAction
	case "similarIssue":
		action = settings.DuplicateAction
	case "relatedIssue":
		action = settings.RelatedAction
	default:
		return "", false
	}
	if action == "" {
		action = "suggest"
	}
	return action, true
}

func similarTriageIssues(data *domain.Bootstrap, issue *domain.Issue) []triageSuggestionCandidate {
	if issue == nil {
		return nil
	}
	issueTitleTokens := triageTokens(issue.Title)
	issueDescriptionTokens := triageTokens(truncateTriageText(issue.Description))
	issueLabelIDs := make(map[string]bool, len(issue.Labels))
	for _, label := range issue.Labels {
		issueLabelIDs[label.ID] = true
	}
	candidates := make([]triageSuggestionCandidate, 0, min(len(data.Issues), maxTriageSuggestionCandidates))
	for index := range data.Issues {
		candidate := &data.Issues[index]
		if candidate.ID == issue.ID || candidate.ArchivedAt != nil || candidate.State.Type == "completed" || candidate.State.Type == "canceled" {
			continue
		}
		if len(candidates) >= maxTriageSuggestionCandidates {
			break
		}
		titleScore := triageSimilarity(issueTitleTokens, triageTokens(candidate.Title))
		descriptionScore := triageSimilarity(issueDescriptionTokens, triageTokens(truncateTriageText(candidate.Description)))
		score := titleScore*0.72 + descriptionScore*0.18
		if candidate.Team.ID == issue.Team.ID {
			score += 0.04
		}
		if issue.Project != nil && candidate.Project != nil && issue.Project.ID == candidate.Project.ID {
			score += 0.05
		}
		for _, label := range candidate.Labels {
			if issueLabelIDs[label.ID] {
				score += 0.03
				break
			}
		}
		score = minFloat(1, score)
		if score < 0.14 {
			continue
		}
		candidates = append(candidates, triageSuggestionCandidate{
			issue: *candidate, score: score, titleScore: titleScore, descriptionScore: descriptionScore,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].issue.UpdatedAt.After(candidates[j].issue.UpdatedAt)
		}
		return candidates[i].score > candidates[j].score
	})
	return candidates
}

func previewIssueSuggestions(data *domain.Bootstrap, text, teamID string) []triageSuggestionPreview {
	text = strings.TrimSpace(text)
	if text == "" {
		return []triageSuggestionPreview{}
	}
	probe := domain.Issue{Title: text, Team: domain.Team{ID: teamID}}
	candidates := similarTriageIssues(data, &probe)
	result := make([]triageSuggestionPreview, 0, 4)
	seen := map[string]bool{}
	add := func(kind, id string) {
		if id == "" || seen[kind+":"+id] {
			return
		}
		seen[kind+":"+id] = true
		result = append(result, triageSuggestionPreview{Type: kind, ID: id})
	}
	if len(candidates) > 0 {
		best := candidates[0]
		if best.issue.Assignee != nil {
			add("assignee", best.issue.Assignee.ID)
		} else if best.issue.Creator.ID != "" {
			add("assignee", best.issue.Creator.ID)
		}
		if best.issue.Project != nil {
			add("project", best.issue.Project.ID)
		}
		if len(best.issue.Labels) > 0 {
			add("label", best.issue.Labels[0].ID)
		}
		add("team", best.issue.Team.ID)
	}
	if len(result) < 4 {
		if projectID, _, _ := inferProject(data, &probe, candidates); projectID != "" {
			add("project", projectID)
		}
	}
	if len(result) < 4 {
		if userID, _, _ := inferAssignee(data, &probe, candidates); userID != "" {
			add("assignee", userID)
		}
	}
	if len(result) < 4 {
		if labelID, _, _ := inferLabel(data, &probe, candidates); labelID != "" {
			add("label", labelID)
		}
	}
	return result
}

func inferProject(data *domain.Bootstrap, issue *domain.Issue, candidates []triageSuggestionCandidate) (string, float64, []string) {
	if issue.Project != nil || len(data.Projects) == 0 {
		return "", 0, nil
	}
	issueTokens := triageTokens(issue.Title, truncateTriageText(issue.Description), data.WorkspaceSettings.FeatureSettings.TriageIntelligence.WorkspaceGuidance)
	scores := map[string]float64{}
	reasons := map[string][]string{}
	for _, candidate := range candidates[:min(len(candidates), 12)] {
		if candidate.issue.Project == nil {
			continue
		}
		id := candidate.issue.Project.ID
		scores[id] += candidate.score
		reasons[id] = append(reasons[id], fmt.Sprintf("%s is linked to a closely related issue.", identifierOrTitle(&candidate.issue)))
	}
	for index := range data.Projects {
		project := &data.Projects[index]
		if project.ArchivedAt != nil {
			continue
		}
		score := triageSimilarity(issueTokens, triageTokens(project.Name, project.Summary, project.Description))
		if score > scores[project.ID] {
			scores[project.ID] = score
		}
		if score >= 0.3 {
			reasons[project.ID] = append(reasons[project.ID], "The project name and context overlap with this request.")
		}
		if project.Lead != nil && project.Lead.ID == issue.Creator.ID {
			scores[project.ID] += 0.04
		}
	}
	var bestID string
	var bestScore float64
	for id, score := range scores {
		if score > bestScore {
			bestID, bestScore = id, score
		}
	}
	if bestScore < 0.22 {
		return "", 0, nil
	}
	if len(reasons[bestID]) == 0 {
		reasons[bestID] = []string{"The project is the closest match in the current workspace."}
	}
	return bestID, minFloat(1, bestScore), uniqueStrings(reasons[bestID])
}

func inferAssignee(data *domain.Bootstrap, issue *domain.Issue, candidates []triageSuggestionCandidate) (string, float64, []string) {
	if issue.Assignee != nil {
		return "", 0, nil
	}
	scores := map[string]float64{}
	reasons := map[string][]string{}
	for _, candidate := range candidates[:min(len(candidates), 12)] {
		if candidate.issue.Assignee != nil {
			id := candidate.issue.Assignee.ID
			scores[id] += candidate.score
			reasons[id] = append(reasons[id], fmt.Sprintf("Assigned to related issue %s.", identifierOrTitle(&candidate.issue)))
		} else if candidate.issue.Creator.ID != "" {
			id := candidate.issue.Creator.ID
			scores[id] += candidate.score * 0.7
			reasons[id] = append(reasons[id], fmt.Sprintf("Created related issue %s.", identifierOrTitle(&candidate.issue)))
		}
	}
	for index := range data.Projects {
		project := &data.Projects[index]
		if project.Lead != nil && project.Lead.ID != "" {
			scores[project.Lead.ID] += 0.08
			if project.ID == issueProjectID(issue) {
				scores[project.Lead.ID] += 0.2
				reasons[project.Lead.ID] = append(reasons[project.Lead.ID], "Leads the issue's project.")
			}
		}
	}
	if issue.Creator.ID != "" {
		scores[issue.Creator.ID] += 0.42
		reasons[issue.Creator.ID] = append(reasons[issue.Creator.ID], "Created this request and is already involved in the workspace.")
	}
	var bestID string
	var bestScore float64
	for id, score := range scores {
		if userByID(data, id) == nil {
			continue
		}
		if score > bestScore {
			bestID, bestScore = id, score
		}
	}
	if bestID == "" || bestScore < 0.35 {
		return "", 0, nil
	}
	return bestID, minFloat(1, bestScore), uniqueStrings(reasons[bestID])
}

func inferLabel(data *domain.Bootstrap, issue *domain.Issue, candidates []triageSuggestionCandidate) (string, float64, []string) {
	if len(issue.Labels) > 0 || len(data.Labels) == 0 {
		return "", 0, nil
	}
	issueTokens := triageTokens(issue.Title, truncateTriageText(issue.Description), data.WorkspaceSettings.FeatureSettings.TriageIntelligence.WorkspaceGuidance)
	scores := map[string]float64{}
	reasons := map[string][]string{}
	for _, candidate := range candidates[:min(len(candidates), 12)] {
		for _, label := range candidate.issue.Labels {
			scores[label.ID] += candidate.score
			reasons[label.ID] = append(reasons[label.ID], fmt.Sprintf("Applied to related issue %s.", identifierOrTitle(&candidate.issue)))
		}
	}
	for index := range data.Labels {
		label := &data.Labels[index]
		if label.ArchivedAt != nil || (label.ResourceType != "" && label.ResourceType != "issue") {
			continue
		}
		score := triageSimilarity(issueTokens, triageTokens(label.Name, label.Description))
		if score > scores[label.ID] {
			scores[label.ID] = score
		}
		if score >= 0.35 {
			reasons[label.ID] = append(reasons[label.ID], "The label name and description match the request.")
		}
	}
	var bestID string
	var bestScore float64
	for id, score := range scores {
		if score > bestScore {
			bestID, bestScore = id, score
		}
	}
	if bestID == "" || bestScore < 0.2 {
		return "", 0, nil
	}
	return bestID, minFloat(1, bestScore), uniqueStrings(reasons[bestID])
}

func inferTeam(data *domain.Bootstrap, issue *domain.Issue, candidates []triageSuggestionCandidate) (string, float64, []string) {
	scores := map[string]float64{}
	reasons := map[string][]string{}
	for _, candidate := range candidates[:min(len(candidates), 12)] {
		id := candidate.issue.Team.ID
		if id == "" || id == issue.Team.ID {
			continue
		}
		scores[id] += candidate.score
		reasons[id] = append(reasons[id], fmt.Sprintf("%s belongs to this team.", identifierOrTitle(&candidate.issue)))
	}
	for index := range data.Projects {
		project := &data.Projects[index]
		if project.ID != issueProjectID(issue) {
			continue
		}
		for _, teamID := range project.TeamIDs {
			if teamID != issue.Team.ID {
				scores[teamID] += 0.3
				reasons[teamID] = append(reasons[teamID], "The issue's project is owned by this team.")
			}
		}
	}
	var bestID string
	var bestScore float64
	for id, score := range scores {
		if teamByID(data, id) == nil {
			continue
		}
		if score > bestScore {
			bestID, bestScore = id, score
		}
	}
	if bestID == "" || bestScore < 0.25 {
		return "", 0, nil
	}
	return bestID, minFloat(1, bestScore), uniqueStrings(reasons[bestID])
}

func applyTriageSuggestionAction(data *domain.Bootstrap, issue *domain.Issue, suggestion *domain.IssueSuggestion, now time.Time) bool {
	action, exists := triageSuggestionAction(data.WorkspaceSettings.FeatureSettings.TriageIntelligence, suggestion.Type)
	if !exists || action != "auto" {
		return false
	}
	if err := applyIssueSuggestion(data, issue, suggestion, now); err != nil {
		return false
	}
	return true
}

func applyIssueSuggestion(data *domain.Bootstrap, issue *domain.Issue, suggestion *domain.IssueSuggestion, now time.Time) error {
	switch suggestion.Type {
	case "assignee":
		user := userByID(data, suggestion.SuggestedUserID)
		if user == nil {
			return errInvalid
		}
		issue.Assignee = user
	case "project":
		project := projectByID(data, suggestion.SuggestedProjectID)
		if project == nil {
			return errInvalid
		}
		issue.Project = project
		issue.ProjectMilestoneID = nil
	case "label":
		label := labelByID(data, suggestion.SuggestedLabelID)
		if label == nil {
			return errInvalid
		}
		if !slicesContainsIssueLabel(issue.Labels, label.ID) {
			issue.Labels = append(issue.Labels, *label)
		}
	case "team":
		team := teamByID(data, suggestion.SuggestedTeamID)
		if team == nil {
			return errInvalid
		}
		previousTeamID := issue.Team.ID
		if previousTeamID == team.ID {
			return nil
		}
		issue.Team = *team
		issue.CycleID = nil
		issue.AddedToCycle = ""
		settings := teamSettings(data, team.ID)
		nextState := stateForTeam(data, team.ID, settings.DefaultStateID)
		if nextState == nil {
			states := statesForTeam(data, team.ID)
			if len(states) == 0 {
				return errInvalid
			}
			nextState = &states[0]
		}
		issue.State = *nextState
	case "similarIssue":
		target := issueByIDValue(data, suggestion.SuggestedIssueID)
		if target == nil {
			return errInvalid
		}
		addBidirectionalIssueRelation(data, issue, target, "duplicate", now)
	case "relatedIssue":
		target := issueByIDValue(data, suggestion.SuggestedIssueID)
		if target == nil {
			return errInvalid
		}
		addBidirectionalIssueRelation(data, issue, target, "related", now)
	default:
		return errInvalid
	}
	return nil
}

func addBidirectionalIssueRelation(data *domain.Bootstrap, issue, target *domain.Issue, relationType string, now time.Time) {
	if issue == nil || target == nil || issue.ID == target.ID || hasIssueRelation(issue, target.ID, relationType) {
		return
	}
	relation := domain.IssueRelation{
		ID:             fmt.Sprintf("relation_%d", now.UnixNano()),
		Type:           relationType,
		IssueID:        issue.ID,
		RelatedIssueID: target.ID,
	}
	issue.Relations = append(issue.Relations, relation)
	if targetIssue, err := issueByID(data, target.ID); err == nil {
		targetIssue.Relations = append(targetIssue.Relations, domain.IssueRelation{
			ID:             relation.ID,
			Type:           relationType,
			IssueID:        target.ID,
			RelatedIssueID: issue.ID,
		})
	}
}

func issueProjectID(issue *domain.Issue) string {
	if issue == nil || issue.Project == nil {
		return ""
	}
	return issue.Project.ID
}

func teamByID(data *domain.Bootstrap, id string) *domain.Team {
	for index := range data.Teams {
		if data.Teams[index].ID == id {
			team := data.Teams[index]
			return &team
		}
	}
	return nil
}

func labelByID(data *domain.Bootstrap, id string) *domain.IssueLabel {
	for index := range data.Labels {
		if data.Labels[index].ID == id {
			label := data.Labels[index]
			return &label
		}
	}
	return nil
}

func issueByIDValue(data *domain.Bootstrap, id string) *domain.Issue {
	if id == "" {
		return nil
	}
	for index := range data.Issues {
		if data.Issues[index].ID == id {
			issue := data.Issues[index]
			return &issue
		}
	}
	return nil
}

func hasIssueRelation(issue *domain.Issue, relatedID, relationType string) bool {
	for _, relation := range issue.Relations {
		if relation.RelatedIssueID == relatedID && relation.Type == relationType {
			return true
		}
	}
	return false
}

func slicesContainsIssueLabel(labels []domain.IssueLabel, id string) bool {
	for _, label := range labels {
		if label.ID == id {
			return true
		}
	}
	return false
}

func duplicateReasons(issue, candidate *domain.Issue) []string {
	reasons := []string{fmt.Sprintf("Title and description overlap strongly with %s.", identifierOrTitle(candidate))}
	if issue.Team.ID == candidate.Team.ID {
		reasons = append(reasons, "Both issues are in the same team.")
	}
	if issue.Project != nil && candidate.Project != nil && issue.Project.ID == candidate.Project.ID {
		reasons = append(reasons, "Both issues are linked to the same project.")
	}
	return reasons
}

func relatedReasons(issue, candidate *domain.Issue) []string {
	reasons := []string{fmt.Sprintf("The wording and context are similar to %s.", identifierOrTitle(candidate))}
	if len(candidate.Labels) > 0 {
		reasons = append(reasons, "The related issue carries matching labels.")
	}
	if issue.Team.ID == candidate.Team.ID {
		reasons = append(reasons, "Both issues belong to the same team.")
	}
	return reasons
}

func identifierOrTitle(issue *domain.Issue) string {
	if issue == nil {
		return "the related issue"
	}
	if issue.Identifier != "" {
		return issue.Identifier
	}
	return issue.Title
}

func truncateTriageText(value string) string {
	runes := []rune(value)
	if len(runes) > maxTriageSuggestionTextRunes {
		runes = runes[:maxTriageSuggestionTextRunes]
	}
	return string(runes)
}

func triageTokens(values ...string) map[string]bool {
	tokens := map[string]bool{}
	for _, value := range values {
		var word []rune
		flush := func() {
			if len(word) < 2 {
				word = word[:0]
				return
			}
			tokens[strings.ToLower(string(word))] = true
			word = word[:0]
		}
		for _, character := range []rune(strings.ToLower(value)) {
			if unicode.IsLetter(character) || unicode.IsDigit(character) {
				word = append(word, character)
				continue
			}
			flush()
		}
		flush()
		for _, segment := range strings.FieldsFunc(value, func(character rune) bool {
			return !unicode.IsLetter(character) && !unicode.IsDigit(character)
		}) {
			runes := []rune(segment)
			if len(runes) < 2 {
				continue
			}
			cjk := 0
			for _, character := range runes {
				if unicode.In(character, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
					cjk++
				}
			}
			if cjk == 0 {
				continue
			}
			for size := 2; size <= 4 && size <= len(runes); size++ {
				for index := 0; index+size <= len(runes); index++ {
					tokens[strings.ToLower(string(runes[index:index+size]))] = true
				}
			}
		}
	}
	return tokens
}

func triageSimilarity(left, right map[string]bool) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	intersection := 0
	for token := range left {
		if right[token] {
			intersection++
		}
	}
	union := len(left) + len(right) - intersection
	if union == 0 {
		return 0
	}
	coverage := float64(intersection) / float64(min(len(left), len(right)))
	jaccard := float64(intersection) / float64(union)
	return coverage*0.72 + jaccard*0.28
}

func minFloat(left, right float64) float64 {
	if left < right {
		return left
	}
	return right
}

func (s *server) previewIssueSuggestions(w http.ResponseWriter, r *http.Request) {
	text := strings.TrimSpace(r.URL.Query().Get("text"))
	if len([]rune(text)) > 500 {
		writeError(w, http.StatusBadRequest, "text is too long")
		return
	}
	teamID := strings.TrimSpace(r.URL.Query().Get("teamId"))
	actor := mcpActor{WorkspaceKey: workspaceKey(r), User: authUser(r)}
	if actor.User.ID == "" {
		if metadata, ok := s.store.WorkspaceMetadata(actor.WorkspaceKey); ok {
			actor.User = metadata.Viewer
		}
	}
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		actor.APIKey = key
	}
	data, err := s.mcpWorkspaceData(r.Context(), actor)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	if teamID == "" && len(data.Teams) > 0 {
		teamID = data.Teams[0].ID
	}
	query, err := s.mcpIssueQuery(r.Context(), actor)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	probe := domain.Issue{Title: text, Team: domain.Team{ID: teamID}}
	candidates, err := s.triageCandidateIssues(r.Context(), &probe, query)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	data.Issues = candidates
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": previewIssueSuggestions(&data, text, teamID)})
}

func (s *server) listIssueSuggestions(w http.ResponseWriter, r *http.Request) {
	actor := mcpActor{WorkspaceKey: workspaceKey(r), User: authUser(r)}
	if actor.User.ID == "" {
		if metadata, ok := s.store.WorkspaceMetadata(actor.WorkspaceKey); ok {
			actor.User = metadata.Viewer
		}
	}
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		actor.APIKey = key
	}
	data, err := s.mcpWorkspaceData(r.Context(), actor)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	query, err := s.mcpIssueQuery(r.Context(), actor)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	issue, err := s.store.AuthorizedIssueRecord(r.Context(), query, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	if metadata, ok := s.store.WorkspaceMetadata(actor.WorkspaceKey); ok {
		data.IssueSuggestions = slices.DeleteFunc(metadata.IssueSuggestions, func(item domain.IssueSuggestion) bool {
			if item.IssueID != issue.ID {
				return true
			}
			if item.SuggestedIssueID != "" {
				_, targetErr := s.store.AuthorizedIssueRecord(r.Context(), query, item.SuggestedIssueID)
				if targetErr != nil {
					return true
				}
			}
			if item.SuggestedProjectID != "" && !slices.ContainsFunc(data.Projects, func(project domain.Project) bool { return project.ID == item.SuggestedProjectID }) {
				return true
			}
			if item.SuggestedTeamID != "" && !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == item.SuggestedTeamID }) {
				return true
			}
			return false
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"issueId":                issue.ID,
		"suggestionsGeneratedAt": issue.SuggestionsGeneratedAt,
		"suggestions":            activeIssueSuggestions(&data, issue.ID),
	})
}

func (s *server) refreshIssueSuggestions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var updated []domain.IssueSuggestion
	actor := mcpActor{WorkspaceKey: workspaceKey(r), User: authUser(r)}
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		actor.APIKey = key
	}
	if actor.User.ID == "" {
		if metadata, ok := s.store.WorkspaceMetadata(actor.WorkspaceKey); ok {
			actor.User = metadata.Viewer
		}
	}
	query, queryErr := s.mcpIssueQuery(r.Context(), actor)
	if queryErr != nil {
		respondMutation(w, queryErr, http.StatusOK, updated)
		return
	}
	issue, readErr := s.store.AuthorizedIssueRecord(r.Context(), query, id)
	if readErr != nil {
		respondMutation(w, readErr, http.StatusOK, updated)
		return
	}
	metadata, ok := s.store.WorkspaceMetadata(workspaceKey(r))
	if !ok || !triageIntelligenceEnabled(metadata.WorkspaceSettings) || !isTriageIssue(&metadata, &issue) {
		respondMutation(w, fmt.Errorf("%w: Triage Intelligence is disabled or issue is not in triage", errInvalid), http.StatusOK, updated)
		return
	}
	candidates, candidateErr := s.triageCandidateIssues(r.Context(), &issue, query)
	if candidateErr != nil {
		respondMutation(w, candidateErr, http.StatusOK, updated)
		return
	}
	err := s.store.MutateWorkspace(store.WithIssueRecordMutations(r.Context(), id), workspaceKey(r), "issue.suggestions_refreshed", id, nil, func(data *domain.Bootstrap) error {
		issue, err := issueByID(data, id)
		if err != nil {
			return err
		}
		if !triageIntelligenceEnabled(data.WorkspaceSettings) {
			return fmt.Errorf("%w: Triage Intelligence is disabled", errInvalid)
		}
		if !isTriageIssue(data, issue) {
			return fmt.Errorf("%w: issue is not in triage", errInvalid)
		}
		now := time.Now().UTC()
		data.IssueSuggestions = slicesDeleteIssueSuggestions(data.IssueSuggestions, issue.ID, "")
		data.Issues = append(data.Issues, candidates...)
		issue, err = issueByID(data, id)
		if err != nil {
			return err
		}
		generated := generateIssueSuggestions(data, issue, now)
		for index := range generated {
			if applyTriageSuggestionAction(data, issue, &generated[index], now) {
				generated[index].State = "accepted"
				generated[index].StateChangedAt = now
			}
		}
		data.IssueSuggestions = append(data.IssueSuggestions, generated...)
		issue.SuggestionsGeneratedAt = &now
		issue.UpdatedAt = now
		issue.Version++
		// issueByID returns a pointer into data.Issues. Apply the bookkeeping
		// fields before replacing the scoped issue slice, otherwise those writes
		// land on an abandoned backing array and are not persisted.
		data.Issues = []domain.Issue{*issue}
		updated = generated
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) acceptIssueSuggestion(w http.ResponseWriter, r *http.Request) {
	s.updateIssueSuggestionState(w, r, true)
}

func (s *server) dismissIssueSuggestion(w http.ResponseWriter, r *http.Request) {
	s.updateIssueSuggestionState(w, r, false)
}

func (s *server) updateIssueSuggestionState(w http.ResponseWriter, r *http.Request, accept bool) {
	issueID := r.PathValue("id")
	suggestionID := r.PathValue("suggestionId")
	eventType := "issue.suggestion_dismissed"
	if accept {
		eventType = "issue.suggestion_accepted"
	}
	var updated domain.IssueSuggestion
	metadata, ok := s.store.WorkspaceMetadata(workspaceKey(r))
	if !ok {
		respondMutation(w, store.ErrAuthForbidden, http.StatusOK, updated)
		return
	}
	var targetID string
	for _, item := range metadata.IssueSuggestions {
		if item.ID == suggestionID {
			targetID = item.SuggestedIssueID
			break
		}
	}
	actor := mcpActor{WorkspaceKey: workspaceKey(r), User: authUser(r)}
	if actor.User.ID == "" {
		actor.User = metadata.Viewer
	}
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		actor.APIKey = key
	}
	query, queryErr := s.mcpIssueQuery(r.Context(), actor)
	if queryErr != nil {
		respondMutation(w, queryErr, http.StatusOK, updated)
		return
	}
	if targetID != "" {
		if _, queryErr = s.store.AuthorizedIssueRecord(r.Context(), query, targetID); queryErr != nil {
			respondMutation(w, store.ErrAuthForbidden, http.StatusOK, updated)
			return
		}
	}
	visible, visibleErr := s.mcpWorkspaceData(r.Context(), actor)
	if visibleErr != nil {
		respondMutation(w, visibleErr, http.StatusOK, updated)
		return
	}
	for _, item := range metadata.IssueSuggestions {
		if item.ID != suggestionID {
			continue
		}
		if item.SuggestedProjectID != "" && !slices.ContainsFunc(visible.Projects, func(project domain.Project) bool { return project.ID == item.SuggestedProjectID }) || item.SuggestedTeamID != "" && !slices.ContainsFunc(visible.Teams, func(team domain.Team) bool { return team.ID == item.SuggestedTeamID }) || item.SuggestedLabelID != "" && !slices.ContainsFunc(visible.Labels, func(label domain.IssueLabel) bool { return label.ID == item.SuggestedLabelID }) {
			respondMutation(w, store.ErrAuthForbidden, http.StatusOK, updated)
			return
		}
		break
	}
	scope := []string{issueID}
	if targetID != "" {
		scope = append(scope, targetID)
	}
	err := s.store.MutateWorkspace(store.WithIssueRecordMutations(r.Context(), scope...), workspaceKey(r), eventType, suggestionID, nil, func(data *domain.Bootstrap) error {
		index := -1
		for itemIndex := range data.IssueSuggestions {
			item := &data.IssueSuggestions[itemIndex]
			if item.ID == suggestionID && item.IssueID == issueID && item.State == "active" {
				index = itemIndex
				break
			}
		}
		if index < 0 {
			return errNotFound
		}
		issue, err := issueByID(data, issueID)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		suggestion := &data.IssueSuggestions[index]
		if accept {
			if err := applyIssueSuggestion(data, issue, suggestion, now); err != nil {
				return err
			}
			suggestion.State = "accepted"
			for other := range data.IssueSuggestions {
				item := &data.IssueSuggestions[other]
				if item.ID != suggestion.ID && item.IssueID == issueID && item.State == "active" && item.Type == suggestion.Type {
					item.State = "dismissed"
					item.StateChangedAt = now
					item.UpdatedAt = now
				}
			}
			issue.UpdatedAt = now
			issue.Version++
			appendActivity(data, issue.ID, "issue.suggestion_accepted", data.Viewer, map[string]string{
				"type": suggestion.Type,
			})
		} else {
			suggestion.State = "dismissed"
		}
		suggestion.StateChangedAt = now
		suggestion.UpdatedAt = now
		updated = *suggestion
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}
