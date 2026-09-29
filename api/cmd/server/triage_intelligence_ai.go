package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Model-backed Triage Intelligence (Linear's agentic Triage Intelligence).
// The token scorer only shortlists candidate issues; the configured Agent
// model decides which duplicate/related issue, assignee, project, labels and
// team to suggest, and explains each pick. Every identifier/name in the reply
// is validated against the context we sent, so the model cannot invent
// targets. When the Agent is not configured, or the model fails, the
// conservative heuristic in triage_intelligence.go is used instead.

const (
	triageSourceAI        = "ai"
	triageSourceHeuristic = "heuristic"

	triageAIMaxTimeout        = 120 * time.Second
	triageAIShortlist         = 15
	triageAIMaxCandidates     = 25
	triageAIRecentIssues      = 200
	triageAIMaxProjects       = 50
	triageAIMaxMembers        = 50
	triageAIMaxLabels         = 100
	triageAIMaxRelated        = 3
	triageAIMaxLabelPicks     = 3
	triageAIMaxReasons        = 3
	triageAIMaxReasonWords    = 20
	triageAIMaxThinkingRunes  = 1200
	triageAIConcurrentRunsCap = 4
)

// triageAISlots bounds concurrent background model runs (bulk moves into
// triage would otherwise fan out one provider call per issue at once).
var triageAISlots = make(chan struct{}, triageAIConcurrentRunsCap)

const triageAISystemPrompt = `You are Triage Intelligence for an issue tracker. A new issue has arrived in a team's Triage inbox. Using only the workspace context provided, suggest how to route it.

Be conservative. Only suggest something when the evidence is specific; "No suggestions" is a good answer and is better than a weak guess.
- duplicate: only when a candidate issue describes the same problem or request. Otherwise null.
- related: only candidate issues with genuine topical overlap (same feature, integration, workflow or bug area), at most 3. Shared generic words are not overlap.
- assignee: only when someone clearly owns this area (e.g. they are assigned to closely related issues, lead the matching project, or guidance names them). Never suggest the issue's creator merely because they created it. Otherwise null.
- project: only when the issue clearly belongs to one project (it names it, or it matches the project's stated scope, or closely related issues are in it). Otherwise null.
- labels: only when similar issues use the label or the label's definition clearly matches the issue. At most 3. Never suggest labels already on the issue.
- team: only when a different team fits clearly better than the current one. Otherwise null.
- Follow the workspace guidance when it is given.

Reasons: 1-3 short bullets per suggestion, each at most 20 words, citing concrete evidence (for example "This project is the explicit Compare Test fixture referenced by the issue"). Do not restate the suggestion itself.

Use only identifiers, names and team keys that appear in the context. Reply with ONE JSON object and nothing else:
{"duplicate":{"identifier":"ABC-1","reasons":["..."]}|null,"related":[{"identifier":"ABC-2","reasons":["..."]}],"assignee":{"name":"...","reasons":["..."]}|null,"project":{"name":"...","reasons":["..."]}|null,"labels":[{"name":"...","reasons":["..."]}],"team":{"key":"...","reasons":["..."]}|null,"thinking":"2-4 sentence summary of how you decided"}`

type triageRunOptions struct {
	useAI bool
	// onlyPending skips issues whose suggestions were already generated
	// (a background run must not overwrite a newer "Run again").
	onlyPending bool
	bumpVersion bool
	eventType   string
}

// triageIntelligenceTimeout follows the configured Agent timeout, capped at 120s.
func (s *server) triageIntelligenceTimeout() time.Duration {
	timeout := s.agent.Timeout
	if timeout <= 0 || timeout > triageAIMaxTimeout {
		timeout = triageAIMaxTimeout
	}
	return timeout
}

// startTriageIntelligenceRun generates suggestions for a pending triage issue
// in the background. Concurrent runs for the same issue are deduplicated.
func (s *server) startTriageIntelligenceRun(ctx context.Context, actor mcpActor, issueID string) {
	key := actor.WorkspaceKey + "\x00" + issueID
	if _, running := s.triageRuns.LoadOrStore(key, struct{}{}); running {
		return
	}
	// Keep request values (actor, API key) but not the request's cancellation.
	base := context.WithoutCancel(ctx)
	go func() {
		defer s.triageRuns.Delete(key)
		runCtx, cancel := context.WithTimeout(base, s.triageIntelligenceTimeout()+30*time.Second)
		defer cancel()
		if s.store != nil {
			stop := context.AfterFunc(s.store.WorkerContext(), cancel)
			defer stop()
		}
		select {
		case triageAISlots <- struct{}{}:
			defer func() { <-triageAISlots }()
		case <-runCtx.Done():
			return
		}
		_, _, err := s.runTriageIntelligence(runCtx, actor, issueID, triageRunOptions{useAI: true, onlyPending: true, eventType: "issue.suggestions_generated"})
		if err != nil && !errors.Is(err, store.ErrNoMutation) && !errors.Is(err, errInvalid) {
			log.Printf("generate triage intelligence issue=%s: %v", issueID, err)
		}
	}()
}

// runTriageIntelligence generates and stores suggestions for one triage issue.
// The model is called before (outside) the workspace mutation lock.
func (s *server) runTriageIntelligence(ctx context.Context, actor mcpActor, issueID string, options triageRunOptions) (domain.Issue, []domain.IssueSuggestion, error) {
	query, err := s.mcpIssueQuery(ctx, actor)
	if err != nil {
		return domain.Issue{}, nil, err
	}
	issue, err := s.store.AuthorizedIssueRecord(ctx, query, issueID)
	if err != nil {
		return domain.Issue{}, nil, err
	}
	metadata, ok := s.store.WorkspaceMetadata(actor.WorkspaceKey)
	if !ok || !triageIntelligenceEnabled(metadata.WorkspaceSettings) || !isTriageIssue(&metadata, &issue) {
		return domain.Issue{}, nil, fmt.Errorf("%w: Triage Intelligence is disabled or issue is not in triage", errInvalid)
	}
	if options.onlyPending && issue.SuggestionsGeneratedAt != nil {
		return domain.Issue{}, nil, store.ErrNoMutation
	}
	candidates, err := s.triageCandidateIssues(ctx, &issue, query)
	if err != nil {
		return domain.Issue{}, nil, err
	}
	now := time.Now().UTC()
	settings := metadata.WorkspaceSettings.FeatureSettings.TriageIntelligence
	source, thinking := triageSourceHeuristic, ""
	var generated []domain.IssueSuggestion
	if options.useAI && s.agent.Enabled {
		plan, aiErr := s.requestTriageAIPlan(ctx, actor, query, &issue, candidates)
		if aiErr != nil {
			log.Printf("triage intelligence model issue=%s: %v (using heuristic)", issueID, aiErr)
		} else {
			source, thinking = triageSourceAI, plan.Thinking
			generated = plan.suggestions(settings, issue.ID, now)
		}
	}
	if source == triageSourceHeuristic {
		scoring := metadata
		scoring.Issues = candidates
		generated = generateIssueSuggestions(&scoring, &issue, now)
	}
	// Relation targets that will be auto-applied must be in the mutation scope
	// so the inverse relation is persisted too.
	scope := []string{issueID}
	for _, item := range generated {
		if action, _ := triageSuggestionAction(settings, item.Type); action == "auto" && item.SuggestedIssueID != "" && !slices.Contains(scope, item.SuggestedIssueID) {
			scope = append(scope, item.SuggestedIssueID)
		}
	}
	var updated domain.Issue
	var stored []domain.IssueSuggestion
	err = s.store.MutateWorkspace(store.WithIssueRecordMutations(ctx, scope...), actor.WorkspaceKey, options.eventType, issueID, nil, func(data *domain.Bootstrap) error {
		target, err := issueByID(data, issueID)
		if err != nil {
			return err
		}
		if !triageIntelligenceEnabled(data.WorkspaceSettings) {
			return fmt.Errorf("%w: Triage Intelligence is disabled", errInvalid)
		}
		if !isTriageIssue(data, target) {
			return fmt.Errorf("%w: issue is not in triage", errInvalid)
		}
		if options.onlyPending && target.SuggestionsGeneratedAt != nil {
			return store.ErrNoMutation
		}
		now := time.Now().UTC()
		stored = storeTriageSuggestions(data, target, generated, now, source, thinking)
		autoApplied := slices.ContainsFunc(stored, func(item domain.IssueSuggestion) bool { return item.State == "accepted" })
		if options.bumpVersion || autoApplied {
			target.UpdatedAt = now
			target.Version++
		}
		updated = *target
		return nil
	})
	return updated, stored, err
}

type triageAIReplyTarget struct {
	Identifier string   `json:"identifier"`
	Name       string   `json:"name"`
	Key        string   `json:"key"`
	Reasons    []string `json:"reasons"`
}

type triageAIReply struct {
	Duplicate *triageAIReplyTarget  `json:"duplicate"`
	Related   []triageAIReplyTarget `json:"related"`
	Assignee  *triageAIReplyTarget  `json:"assignee"`
	Project   *triageAIReplyTarget  `json:"project"`
	Labels    []triageAIReplyTarget `json:"labels"`
	Team      *triageAIReplyTarget  `json:"team"`
	Thinking  string                `json:"thinking"`
}

type triageAIPick struct {
	Kind     string
	TargetID string
	Reasons  []string
	Score    float64
}

type triageAIPlan struct {
	Picks    []triageAIPick
	Thinking string
}

type triageAIMember struct {
	User   domain.User
	Role   string
	Recent []string
}

type triageAILabel struct {
	Label domain.IssueLabel
	Uses  int
}

type triageAITeam struct {
	Team        domain.Team
	Description string
}

// triageAIContext is everything the model sees; replies are validated against it.
type triageAIContext struct {
	Issue      domain.Issue
	Candidates []domain.Issue
	Scores     map[string]float64
	Projects   []domain.Project
	Members    []triageAIMember
	Labels     []triageAILabel
	Teams      []triageAITeam
	Guidance   string
	users      map[string]domain.User
}

func (s *server) requestTriageAIPlan(ctx context.Context, actor mcpActor, query store.IssueRecordQuery, issue *domain.Issue, candidates []domain.Issue) (*triageAIPlan, error) {
	data, err := s.mcpWorkspaceData(ctx, actor)
	if err != nil {
		return nil, err
	}
	pool := slices.Clone(candidates)
	recentQuery := query
	recentQuery.Sort, recentQuery.Direction, recentQuery.Limit = "updatedAt", "desc", triageAIRecentIssues
	recentQuery.Summary, recentQuery.IncludeDescription, recentQuery.Archived = true, true, "false"
	recentQuery.Cursor, recentQuery.Text = "", ""
	if page, recentErr := s.store.QueryIssueRecords(ctx, recentQuery); recentErr == nil {
		seen := map[string]bool{}
		for _, item := range pool {
			seen[item.ID] = true
		}
		for _, item := range page.Items {
			if !seen[item.ID] && item.ID != issue.ID {
				seen[item.ID] = true
				pool = append(pool, item)
			}
		}
	}
	triageContext := buildTriageAIContext(&data, *issue, pool)
	callCtx, cancel := context.WithTimeout(ctx, s.triageIntelligenceTimeout())
	defer cancel()
	turn, err := s.requestAgentTurnWithoutTools(callCtx, []agentProviderMessage{
		{Role: "system", Content: triageAISystemPrompt},
		{Role: "user", Content: triageAIPrompt(&triageContext)},
	})
	if err != nil {
		return nil, err
	}
	return parseTriageAIReply(turn.Text, &triageContext)
}

// buildTriageAIContext shortlists candidates with the token scorer (padded
// with recent open issues so semantic matches with no shared words are still
// visible to the model) and collects the routing vocabulary.
func buildTriageAIContext(data *domain.Bootstrap, issue domain.Issue, pool []domain.Issue) triageAIContext {
	result := triageAIContext{Issue: issue, Scores: map[string]float64{}, users: map[string]domain.User{}}
	result.Guidance = strings.TrimSpace(data.WorkspaceSettings.FeatureSettings.TriageIntelligence.WorkspaceGuidance)

	scoring := *data
	scoring.Issues = pool
	included := map[string]bool{}
	for _, candidate := range similarTriageIssues(&scoring, &issue) {
		if len(result.Candidates) >= triageAIShortlist {
			break
		}
		included[candidate.issue.ID] = true
		result.Scores[candidate.issue.ID] = candidate.score
		result.Candidates = append(result.Candidates, candidate.issue)
	}
	recent := slices.Clone(pool)
	sort.SliceStable(recent, func(i, j int) bool {
		sameI, sameJ := recent[i].Team.ID == issue.Team.ID, recent[j].Team.ID == issue.Team.ID
		if sameI != sameJ {
			return sameI
		}
		return recent[i].UpdatedAt.After(recent[j].UpdatedAt)
	})
	for _, item := range recent {
		if len(result.Candidates) >= triageAIMaxCandidates {
			break
		}
		if included[item.ID] || item.ID == issue.ID || item.ArchivedAt != nil || item.State.Type == "completed" || item.State.Type == "canceled" || (item.TriagedAt == nil && item.State.Type == "backlog" && teamSettings(data, item.Team.ID).TriageEnabled) {
			continue
		}
		included[item.ID] = true
		result.Candidates = append(result.Candidates, item)
	}

	for _, project := range data.Projects {
		if project.ArchivedAt != nil {
			continue
		}
		if len(result.Projects) >= triageAIMaxProjects {
			break
		}
		result.Projects = append(result.Projects, project)
	}

	for _, user := range data.Users {
		result.users[user.ID] = user
	}
	roles := map[string]string{}
	for _, member := range data.Members {
		if member.Status == "" || member.Status == "active" {
			roles[member.User.ID] = member.Role
		}
	}
	memberIDs := []string{}
	for _, member := range data.TeamMembers {
		if member.TeamID == issue.Team.ID && !slices.Contains(memberIDs, member.UserID) {
			memberIDs = append(memberIDs, member.UserID)
			if member.Role != "" {
				roles[member.UserID] = member.Role
			}
		}
	}
	if len(memberIDs) == 0 {
		for _, member := range data.Members {
			if member.Status == "" || member.Status == "active" {
				memberIDs = append(memberIDs, member.User.ID)
			}
		}
	}
	if len(memberIDs) == 0 {
		for _, user := range data.Users {
			memberIDs = append(memberIDs, user.ID)
		}
	}
	byUpdated := slices.Clone(pool)
	sort.SliceStable(byUpdated, func(i, j int) bool { return byUpdated[i].UpdatedAt.After(byUpdated[j].UpdatedAt) })
	for _, id := range memberIDs {
		user, ok := result.users[id]
		if !ok || !user.Active || user.App || user.BuiltinAgent {
			continue
		}
		if len(result.Members) >= triageAIMaxMembers {
			break
		}
		member := triageAIMember{User: user, Role: roles[id]}
		for _, item := range byUpdated {
			if item.Assignee != nil && item.Assignee.ID == id && item.ID != issue.ID {
				member.Recent = append(member.Recent, triageClip(item.Title, 120))
				if len(member.Recent) >= 3 {
					break
				}
			}
		}
		result.Members = append(result.Members, member)
	}

	uses := map[string]int{}
	for _, item := range pool {
		for _, label := range item.Labels {
			uses[label.ID]++
		}
	}
	for _, label := range data.Labels {
		if label.ArchivedAt != nil || (label.ResourceType != "" && label.ResourceType != "issue") || (!labelScopeIsWorkspace(label.Scope) && label.Scope != issue.Team.ID) {
			continue
		}
		if len(result.Labels) >= triageAIMaxLabels {
			break
		}
		result.Labels = append(result.Labels, triageAILabel{Label: label, Uses: max(uses[label.ID], label.IssueCount)})
	}

	for _, team := range data.Teams {
		if team.ArchivedAt != nil || team.RetiredAt != nil {
			continue
		}
		result.Teams = append(result.Teams, triageAITeam{Team: team, Description: teamSettings(data, team.ID).Description})
	}
	return result
}

func userDisplayName(user domain.User) string {
	return firstNonEmpty(strings.TrimSpace(user.Name), strings.TrimSpace(user.DisplayName), user.Email)
}

func triageAIPrompt(input *triageAIContext) string {
	type issueView struct {
		Identifier  string   `json:"identifier"`
		Title       string   `json:"title"`
		Description string   `json:"description,omitempty"`
		Status      string   `json:"status,omitempty"`
		Team        string   `json:"team,omitempty"`
		Project     string   `json:"project,omitempty"`
		Assignee    string   `json:"assignee,omitempty"`
		Labels      []string `json:"labels,omitempty"`
		Creator     string   `json:"creator,omitempty"`
	}
	type projectView struct {
		Name    string   `json:"name"`
		Summary string   `json:"summary,omitempty"`
		Status  string   `json:"status,omitempty"`
		Teams   []string `json:"teams,omitempty"`
		Lead    string   `json:"lead,omitempty"`
		Members []string `json:"members,omitempty"`
	}
	type memberView struct {
		Name           string   `json:"name"`
		Role           string   `json:"role,omitempty"`
		RecentAssigned []string `json:"recentAssignedIssues,omitempty"`
	}
	type labelView struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		IssuesUsing int    `json:"issuesUsing"`
	}
	type teamView struct {
		Key         string `json:"key"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	}
	teamKey := map[string]string{}
	for _, team := range input.Teams {
		teamKey[team.Team.ID] = team.Team.Key
	}
	labelNames := func(labels []domain.IssueLabel) []string {
		names := make([]string, 0, len(labels))
		for _, label := range labels {
			names = append(names, label.Name)
		}
		return names
	}
	view := func(issue domain.Issue, descriptionLimit int) issueView {
		result := issueView{Identifier: issue.Identifier, Title: issue.Title, Description: triageClip(issue.Description, descriptionLimit), Status: issue.State.Name, Team: firstNonEmpty(issue.Team.Key, teamKey[issue.Team.ID]), Labels: labelNames(issue.Labels)}
		if issue.Project != nil {
			result.Project = issue.Project.Name
		}
		if issue.Assignee != nil {
			result.Assignee = userDisplayName(*issue.Assignee)
		}
		return result
	}
	triage := view(input.Issue, maxTriageSuggestionTextRunes)
	triage.Status = ""
	triage.Creator = userDisplayName(input.Issue.Creator)
	candidates := make([]issueView, 0, len(input.Candidates))
	for _, issue := range input.Candidates {
		candidates = append(candidates, view(issue, 300))
	}
	projects := make([]projectView, 0, len(input.Projects))
	for _, project := range input.Projects {
		item := projectView{Name: project.Name, Summary: triageClip(firstNonEmpty(project.Summary, project.Description), 200), Status: project.Status.Name}
		for _, id := range project.TeamIDs {
			if key := teamKey[id]; key != "" {
				item.Teams = append(item.Teams, key)
			}
		}
		if project.Lead != nil {
			item.Lead = userDisplayName(*project.Lead)
		}
		for _, id := range project.MemberIDs {
			if user, ok := input.users[id]; ok && len(item.Members) < 10 {
				item.Members = append(item.Members, userDisplayName(user))
			}
		}
		projects = append(projects, item)
	}
	members := make([]memberView, 0, len(input.Members))
	for _, member := range input.Members {
		role := member.Role
		if member.User.JobTitle != "" {
			role = strings.TrimSpace(strings.Join([]string{member.User.JobTitle, role}, ", "))
			role = strings.Trim(role, ", ")
		}
		members = append(members, memberView{Name: userDisplayName(member.User), Role: role, RecentAssigned: member.Recent})
	}
	labels := make([]labelView, 0, len(input.Labels))
	for _, label := range input.Labels {
		labels = append(labels, labelView{Name: label.Label.Name, Description: triageClip(label.Label.Description, 200), IssuesUsing: label.Uses})
	}
	teams := make([]teamView, 0, len(input.Teams))
	for _, team := range input.Teams {
		teams = append(teams, teamView{Key: team.Team.Key, Name: team.Team.Name, Description: triageClip(team.Description, 200)})
	}
	payload := map[string]any{
		"triageIssue":     triage,
		"candidateIssues": candidates,
		"projects":        projects,
		"teamMembers":     members,
		"labels":          labels,
		"teams":           teams,
	}
	encoded, _ := json.MarshalIndent(payload, "", " ")
	var prompt strings.Builder
	if input.Guidance != "" {
		fmt.Fprintf(&prompt, "Workspace guidance for Triage Intelligence:\n%s\n\n", triageClip(input.Guidance, 4000))
	}
	prompt.WriteString("Workspace context (JSON):\n")
	prompt.Write(encoded)
	prompt.WriteString("\n\nSuggest routing for triageIssue. Reply with the single JSON object only.")
	return prompt.String()
}

// parseTriageAIReply validates the model reply against the context: unknown
// identifiers/names are dropped and conservative rules are enforced.
func parseTriageAIReply(text string, input *triageAIContext) (*triageAIPlan, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("triage intelligence reply was not JSON")
	}
	var reply triageAIReply
	if err := json.Unmarshal([]byte(text[start:end+1]), &reply); err != nil {
		return nil, fmt.Errorf("triage intelligence reply was not valid JSON")
	}
	issue := input.Issue
	plan := &triageAIPlan{Thinking: triageClip(strings.Join(strings.Fields(reply.Thinking), " "), triageAIMaxThinkingRunes)}
	candidates := map[string]domain.Issue{}
	for _, candidate := range input.Candidates {
		if candidate.Identifier != "" {
			candidates[strings.ToLower(candidate.Identifier)] = candidate
		}
	}
	lookupIssue := func(target *triageAIReplyTarget) (domain.Issue, bool) {
		if target == nil {
			return domain.Issue{}, false
		}
		candidate, ok := candidates[strings.ToLower(strings.TrimSpace(target.Identifier))]
		return candidate, ok && candidate.ID != issue.ID
	}
	used := map[string]bool{}
	add := func(kind, id string, target *triageAIReplyTarget) {
		if used[kind+"\x00"+id] {
			return
		}
		used[kind+"\x00"+id] = true
		plan.Picks = append(plan.Picks, triageAIPick{Kind: kind, TargetID: id, Reasons: triageAIReasons(target.Reasons), Score: input.Scores[id]})
	}
	duplicateID := ""
	if candidate, ok := lookupIssue(reply.Duplicate); ok && !hasIssueRelation(&issue, candidate.ID, "duplicate") {
		duplicateID = candidate.ID
		add("similarIssue", candidate.ID, reply.Duplicate)
	}
	related := 0
	for index := range reply.Related {
		target := &reply.Related[index]
		candidate, ok := lookupIssue(target)
		if !ok || candidate.ID == duplicateID || hasIssueRelation(&issue, candidate.ID, "related") || related >= triageAIMaxRelated {
			continue
		}
		related++
		add("relatedIssue", candidate.ID, target)
	}
	if reply.Assignee != nil && issue.Assignee == nil {
		name := strings.ToLower(strings.TrimSpace(reply.Assignee.Name))
		for _, member := range input.Members {
			if name == "" || (strings.ToLower(member.User.Name) != name && strings.ToLower(member.User.DisplayName) != name && strings.ToLower(userDisplayName(member.User)) != name) {
				continue
			}
			// Creating the issue is not evidence of ownership: keep the creator
			// only when they already own a shortlisted related issue.
			if member.User.ID == issue.Creator.ID && !slices.ContainsFunc(input.Candidates, func(candidate domain.Issue) bool {
				return candidate.Assignee != nil && candidate.Assignee.ID == member.User.ID && input.Scores[candidate.ID] > 0
			}) {
				break
			}
			add("assignee", member.User.ID, reply.Assignee)
			break
		}
	}
	if reply.Project != nil && issue.Project == nil {
		name := strings.ToLower(strings.TrimSpace(reply.Project.Name))
		for _, project := range input.Projects {
			if name != "" && strings.ToLower(project.Name) == name {
				add("project", project.ID, reply.Project)
				break
			}
		}
	}
	labelPicks := 0
	for index := range reply.Labels {
		target := &reply.Labels[index]
		name := strings.ToLower(strings.TrimSpace(target.Name))
		for _, label := range input.Labels {
			if name == "" || strings.ToLower(label.Label.Name) != name || slicesContainsIssueLabel(issue.Labels, label.Label.ID) || labelPicks >= triageAIMaxLabelPicks {
				continue
			}
			labelPicks++
			add("label", label.Label.ID, target)
			break
		}
	}
	if reply.Team != nil {
		key := strings.ToLower(strings.TrimSpace(firstNonEmpty(reply.Team.Key, reply.Team.Name)))
		for _, team := range input.Teams {
			if key != "" && (strings.ToLower(team.Team.Key) == key || strings.ToLower(team.Team.Name) == key) && team.Team.ID != issue.Team.ID {
				add("team", team.Team.ID, reply.Team)
				break
			}
		}
	}
	return plan, nil
}

func triageAIReasons(values []string) []string {
	reasons := []string{}
	for _, value := range values {
		value = strings.TrimLeft(strings.TrimSpace(value), "-*• ")
		words := strings.Fields(value)
		if len(words) == 0 {
			continue
		}
		if len(words) > triageAIMaxReasonWords {
			words = append(words[:triageAIMaxReasonWords], "…")
		}
		reasons = append(reasons, triageClip(strings.Join(words, " "), 200))
		if len(reasons) >= triageAIMaxReasons {
			break
		}
	}
	return reasons
}

// suggestions maps validated picks to stored suggestions, honoring the
// per-type suggest/auto/hide settings.
func (plan *triageAIPlan) suggestions(settings domain.TriageIntelligenceSettings, issueID string, now time.Time) []domain.IssueSuggestion {
	result := []domain.IssueSuggestion{}
	rank := 0
	for _, pick := range plan.Picks {
		action, exists := triageSuggestionAction(settings, pick.Kind)
		if !exists || action == "hide" {
			continue
		}
		rank++
		metadata := map[string]any{
			"rank":     rank,
			"reasons":  pick.Reasons,
			"source":   triageSourceAI,
			"thinking": plan.Thinking,
		}
		if pick.Score > 0 {
			metadata["score"] = pick.Score
		}
		suggestion := domain.IssueSuggestion{
			ID:             fmt.Sprintf("issue_suggestion_%d_%d", now.UnixNano(), rank),
			IssueID:        issueID,
			Type:           pick.Kind,
			State:          "active",
			StateChangedAt: now,
			Metadata:       metadata,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		switch pick.Kind {
		case "assignee":
			suggestion.SuggestedUserID = pick.TargetID
		case "project":
			suggestion.SuggestedProjectID = pick.TargetID
		case "label":
			suggestion.SuggestedLabelID = pick.TargetID
		case "team":
			suggestion.SuggestedTeamID = pick.TargetID
		case "similarIssue", "relatedIssue":
			suggestion.SuggestedIssueID = pick.TargetID
		default:
			continue
		}
		result = append(result, suggestion)
	}
	return result
}

func triageClip(value string, limit int) string {
	value = strings.TrimSpace(value)
	if runes := []rune(value); len(runes) > limit {
		return strings.TrimSpace(string(runes[:limit])) + "…"
	}
	return value
}
