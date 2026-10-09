package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

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
	triageAIMaxLinked         = 10
	// A model run replacing heuristic suggestions is retried at most this often
	// (the model may be failing, and each failure falls back to the heuristic).
	triageAIUpgradeRetry = 10 * time.Minute
	// Edits to a triage issue's title/description regenerate its suggestions
	// once the edits settle.
	triageAIEditDebounce = 20 * time.Second
	// The JSON reply is small, but reasoning models think first and that counts against the budget.
	triageAIMaxOutputTokens = 16384
)

// triageAISlots bounds concurrent background model runs (bulk moves into
// triage would otherwise fan out one provider call per issue at once).
var triageAISlots = make(chan struct{}, triageAIConcurrentRunsCap)

const triageAISystemPrompt = `You are Triage Intelligence for an issue tracker. A new issue has arrived in a team's Triage inbox. Using only the workspace context provided, suggest how to route it, the way an experienced teammate who knows how this workspace organizes its work would.

Aim for high precision without being timid: suggest when there is reasonable, specific evidence in the context, and leave a field empty when there is none. An empty result is fine; a weak guess is not. If the issue is too vague to understand (gibberish, a lone word or number, no real description), return no suggestions at all.
- duplicate: a candidate issue that reports the same problem or asks for the same thing, even in different words or with different details (e.g. both describe large CSV imports hanging or freezing). Otherwise null.
- related: candidate issues about the same feature, integration, workflow or bug area, at most 3. Shared generic words, or merely being in the same project or team, are not overlap.
- Never suggest an issue that is already linked to the triage issue (see triageIssue.linkedIssues and a candidate's alreadyLinkedAs).
- project: the issue names the project, clearly fits the project's stated scope, or its duplicate / closely related issues are in that project. Otherwise null.
- assignee: someone who owns this area: the assignee of the duplicate or closely related issues, the lead of the project you suggest (or of the project the issue is in), or the member most often assigned to issues in that project or area (see recentAssignedIssues). Never suggest the issue's creator merely because they created it. Otherwise null.
- labels: labels the workspace already uses (issuesUsing > 0) whose name or description clearly describes this issue (e.g. a crash or hang report is a bug; slowness or freezing on large inputs is performance), or that the duplicate / closely related issues carry. Do not stretch: a label must plainly fit the issue's content. At most 3. Never suggest labels already on the issue.
- team: only when a different team fits clearly better than the current one (its description or the related issues point there). Otherwise null.
- Follow the workspace guidance when it is given.

Reasons: 1-3 short bullets per suggestion, each at most 20 words, citing concrete evidence (for example "DEV-10 is assigned to Dev User and describes the same import freeze"). Do not restate the suggestion itself.

Use only identifiers, names and team keys that appear in the context. Reply with ONE JSON object and nothing else:
{"duplicate":{"identifier":"ABC-1","reasons":["..."]}|null,"related":[{"identifier":"ABC-2","reasons":["..."]}],"assignee":{"name":"...","reasons":["..."]}|null,"project":{"name":"...","reasons":["..."]}|null,"labels":[{"name":"...","reasons":["..."]}],"team":{"key":"...","reasons":["..."]}|null,"thinking":"2-4 sentence summary of how you decided"}`

// triageRunReason says why a background run was started; the run re-checks
// it against the latest issue (before and after the model call) so a newer
// "Run again" or generation is never overwritten.
type triageRunReason string

const (
	triageRunAlways  triageRunReason = ""        // "Run again"
	triageRunPending triageRunReason = "pending" // never generated
	triageRunUpgrade triageRunReason = "upgrade" // generated by the heuristic, the model is available now
	triageRunEdited  triageRunReason = "edited"  // title/description changed materially since generation
)

func (reason triageRunReason) wanted(issue *domain.Issue) bool {
	switch reason {
	case triageRunPending:
		return issue.SuggestionsGeneratedAt == nil
	case triageRunUpgrade:
		return issue.SuggestionsGeneratedAt == nil || issue.SuggestionsSource != triageSourceAI
	case triageRunEdited:
		return issue.SuggestionsGeneratedAt == nil || triageInputChanged(issue)
	}
	return true
}

type triageRunOptions struct {
	useAI       bool
	reason      triageRunReason
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

func triageRunKey(workspace, issueID string) string {
	return workspace + "\x00" + issueID
}

// triageRunActive reports whether a background run for the issue is queued or in flight.
func (s *server) triageRunActive(workspace, issueID string) bool {
	_, running := s.triageRuns.Load(triageRunKey(workspace, issueID))
	return running
}

// startTriageIntelligenceRun generates suggestions for a triage issue in the
// background. Concurrent runs for the same issue are deduplicated.
func (s *server) startTriageIntelligenceRun(ctx context.Context, actor mcpActor, issueID string, reason triageRunReason) bool {
	key := triageRunKey(actor.WorkspaceKey, issueID)
	if _, running := s.triageRuns.LoadOrStore(key, struct{}{}); running {
		return false
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
		_, _, err := s.runTriageIntelligence(runCtx, actor, issueID, triageRunOptions{useAI: true, reason: reason, eventType: "issue.suggestions_generated"})
		if err != nil && !errors.Is(err, store.ErrNoMutation) && !errors.Is(err, errInvalid) {
			log.Printf("generate triage intelligence issue=%s: %v", issueID, err)
		}
	}()
	return true
}

type triageRefreshScope struct {
	upgrade bool // replace heuristic suggestions with model ones
	edits   bool // regenerate after material title/description edits
}

// refreshTriageIntelligence starts whatever background model run a triage
// issue needs: a first generation, an upgrade of heuristic suggestions (at most
// once per triageAIUpgradeRetry), or a debounced regeneration after material
// edits. Only the given issue is considered: there is no workspace sweep. It
// reports whether a run is now queued or in flight.
func (s *server) refreshTriageIntelligence(ctx context.Context, actor mcpActor, issue *domain.Issue, scope triageRefreshScope) bool {
	if !s.agent.Enabled {
		return false
	}
	key := triageRunKey(actor.WorkspaceKey, issue.ID)
	switch {
	case triageRunPending.wanted(issue):
		s.startTriageIntelligenceRun(ctx, actor, issue.ID, triageRunPending)
	case scope.upgrade && triageRunUpgrade.wanted(issue):
		if last, ok := s.triageUpgrades.Load(key); ok && time.Since(last.(time.Time)) < triageAIUpgradeRetry {
			return s.triageRunActive(actor.WorkspaceKey, issue.ID)
		}
		s.triageUpgrades.Store(key, time.Now())
		s.startTriageIntelligenceRun(ctx, actor, issue.ID, triageRunUpgrade)
	case scope.edits && triageRunEdited.wanted(issue):
		s.scheduleTriageEditRun(ctx, actor, issue.ID)
	}
	return s.triageRunActive(actor.WorkspaceKey, issue.ID)
}

type triageEdit struct{ due atomic.Int64 }

// scheduleTriageEditRun regenerates suggestions once edits have been quiet
// for the debounce period; every further edit pushes the run back.
func (s *server) scheduleTriageEditRun(ctx context.Context, actor mcpActor, issueID string) {
	delay := s.triageEditDebounce
	if delay <= 0 {
		delay = triageAIEditDebounce
	}
	key := triageRunKey(actor.WorkspaceKey, issueID)
	entry := &triageEdit{}
	entry.due.Store(time.Now().Add(delay).UnixNano())
	if existing, loaded := s.triageEdits.LoadOrStore(key, entry); loaded {
		existing.(*triageEdit).due.Store(time.Now().Add(delay).UnixNano())
		return
	}
	base := context.WithoutCancel(ctx)
	var done <-chan struct{}
	if s.store != nil {
		done = s.store.WorkerContext().Done()
	}
	go func() {
		defer s.triageEdits.Delete(key)
		for {
			wait := time.Until(time.Unix(0, entry.due.Load()))
			if wait <= 0 {
				// A run still working from the previous text would swallow this
				// one (runs are deduplicated): wait for it to finish first.
				if !s.triageRunActive(actor.WorkspaceKey, issueID) {
					break
				}
				wait = delay
				entry.due.Store(time.Now().Add(wait).UnixNano())
			}
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-done:
				timer.Stop()
				return
			}
		}
		s.startTriageIntelligenceRun(base, actor, issueID, triageRunEdited)
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
	if !options.reason.wanted(&issue) {
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
	if options.useAI && s.agent.Enabled && !triageIssueHasSubstance(&issue) {
		// Nothing to compare against other work: don't spend a model call on
		// noise, and record the run as final so it is not retried.
		source, thinking = triageSourceAI, "The issue has too little content to compare against other work, so nothing was suggested."
	} else if options.useAI && s.agent.Enabled {
		plan, aiErr := s.requestTriageAIPlan(ctx, actor, query, &issue, candidates)
		if aiErr != nil {
			log.Printf("triage intelligence model issue=%s: %v (using heuristic)", issueID, aiErr)
			// Retrying the model to replace this fallback waits for triageAIUpgradeRetry.
			s.triageUpgrades.Store(triageRunKey(actor.WorkspaceKey, issueID), time.Now())
		} else {
			source, thinking = triageSourceAI, plan.Thinking
			generated = plan.suggestions(settings, issue.ID, now)
		}
	}
	if source == triageSourceHeuristic && triageIssueHasSubstance(&issue) {
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
		if !options.reason.wanted(target) {
			return store.ErrNoMutation
		}
		now := time.Now().UTC()
		stored = storeTriageSuggestions(data, target, generated, now, source, thinking)
		// Fingerprint the text the suggestions were generated from (it may
		// have been edited while the model was thinking).
		target.SuggestionsInputSketch = triageInputSketch(&issue)
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
	// Linked are issues already related to the triage issue (relation type by
	// issue ID); they are shown to the model and never re-suggested.
	Linked map[string]triageAILink
	users  map[string]domain.User
}

type triageAILink struct {
	Type       string
	Identifier string
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
	for id, link := range triageContext.Linked {
		if link.Identifier == "" {
			if linked, linkErr := s.store.AuthorizedIssueRecord(ctx, query, id); linkErr == nil {
				link.Identifier = linked.Identifier
				triageContext.Linked[id] = link
			}
		}
	}
	callCtx, cancel := context.WithTimeout(withAgentMaxOutputTokens(ctx, triageAIMaxOutputTokens), s.triageIntelligenceTimeout())
	defer cancel()
	turn, err := s.requestAgentTurnWithoutTools(callCtx, []agentProviderMessage{
		{Role: "system", Content: triageAISystemPrompt},
		{Role: "user", Content: triageAIPrompt(&triageContext)},
	})
	if err != nil {
		return nil, err
	}
	plan, err := parseTriageAIReply(turn.Text, &triageContext)
	if err != nil && turn.StopReason != "" {
		err = fmt.Errorf("%w (stop reason %q, %d characters)", err, turn.StopReason, len(turn.Text))
	}
	return plan, err
}

// buildTriageAIContext shortlists candidates with the token scorer (padded
// with recent open issues so semantic matches with no shared words are still
// visible to the model) and collects the routing vocabulary.
func buildTriageAIContext(data *domain.Bootstrap, issue domain.Issue, pool []domain.Issue) triageAIContext {
	result := triageAIContext{Issue: issue, Scores: map[string]float64{}, Linked: map[string]triageAILink{}, users: map[string]domain.User{}}
	result.Guidance = strings.TrimSpace(data.WorkspaceSettings.FeatureSettings.TriageIntelligence.WorkspaceGuidance)
	for _, relation := range issue.Relations {
		if len(result.Linked) >= triageAIMaxLinked {
			break
		}
		if _, seen := result.Linked[relation.RelatedIssueID]; relation.RelatedIssueID == "" || relation.RelatedIssueID == issue.ID || seen {
			continue
		}
		link := triageAILink{Type: relation.Type}
		if index := slices.IndexFunc(pool, func(item domain.Issue) bool { return item.ID == relation.RelatedIssueID }); index >= 0 {
			link.Identifier = pool[index].Identifier
		}
		result.Linked[relation.RelatedIssueID] = link
	}

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
	type linkView struct {
		Identifier string `json:"identifier"`
		Relation   string `json:"relation"`
	}
	type issueView struct {
		Identifier  string     `json:"identifier"`
		Title       string     `json:"title"`
		Description string     `json:"description,omitempty"`
		Status      string     `json:"status,omitempty"`
		Team        string     `json:"team,omitempty"`
		Project     string     `json:"project,omitempty"`
		Assignee    string     `json:"assignee,omitempty"`
		Labels      []string   `json:"labels,omitempty"`
		Creator     string     `json:"creator,omitempty"`
		LinkedAs    string     `json:"alreadyLinkedAs,omitempty"`
		Linked      []linkView `json:"linkedIssues,omitempty"`
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
	links := []linkView{}
	for _, link := range input.Linked {
		if link.Identifier != "" {
			links = append(links, linkView{Identifier: link.Identifier, Relation: link.Type})
		}
	}
	sort.Slice(links, func(i, j int) bool { return links[i].Identifier < links[j].Identifier })
	triage.Linked = links
	candidates := make([]issueView, 0, len(input.Candidates))
	for _, issue := range input.Candidates {
		item := view(issue, 300)
		item.LinkedAs = input.Linked[issue.ID].Type
		candidates = append(candidates, item)
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
		// Issues already linked to the triage issue (in any way) are not news.
		_, linked := input.Linked[candidate.ID]
		return candidate, ok && candidate.ID != issue.ID && !linked && !slices.ContainsFunc(issue.Relations, func(relation domain.IssueRelation) bool { return relation.RelatedIssueID == candidate.ID })
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
	if candidate, ok := lookupIssue(reply.Duplicate); ok {
		duplicateID = candidate.ID
		add("similarIssue", candidate.ID, reply.Duplicate)
	}
	related := 0
	for index := range reply.Related {
		target := &reply.Related[index]
		candidate, ok := lookupIssue(target)
		if !ok || candidate.ID == duplicateID || related >= triageAIMaxRelated {
			continue
		}
		related++
		add("relatedIssue", candidate.ID, target)
	}
	// Evidence the model can point at: issues it linked, or ones the scorer found similar.
	similar := func(candidate domain.Issue) bool {
		return used["relatedIssue\x00"+candidate.ID] || used["similarIssue\x00"+candidate.ID] || input.Scores[candidate.ID] > 0
	}
	var project *domain.Project
	if reply.Project != nil && issue.Project == nil {
		name := strings.ToLower(strings.TrimSpace(reply.Project.Name))
		for index := range input.Projects {
			if name != "" && strings.ToLower(input.Projects[index].Name) == name {
				project = &input.Projects[index]
				add("project", project.ID, reply.Project)
				break
			}
		}
	}
	if reply.Assignee != nil && issue.Assignee == nil {
		name := strings.ToLower(strings.TrimSpace(reply.Assignee.Name))
		for _, member := range input.Members {
			if name == "" || (strings.ToLower(member.User.Name) != name && strings.ToLower(member.User.DisplayName) != name && strings.ToLower(userDisplayName(member.User)) != name) {
				continue
			}
			// Creating the issue is not evidence of ownership: keep the creator only
			// when they lead the suggested project or own a similar issue or one in it.
			leadsProject := project != nil && project.Lead != nil && project.Lead.ID == member.User.ID
			if member.User.ID == issue.Creator.ID && !leadsProject && !slices.ContainsFunc(input.Candidates, func(candidate domain.Issue) bool {
				inProject := project != nil && candidate.Project != nil && candidate.Project.ID == project.ID
				return candidate.Assignee != nil && candidate.Assignee.ID == member.User.ID && (similar(candidate) || inProject)
			}) {
				break
			}
			add("assignee", member.User.ID, reply.Assignee)
			break
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
			// Like Linear, labels follow how the workspace organizes similar work:
			// a similar issue carries the label, or the workspace uses it (the
			// model is told to pick only labels that plainly fit the content).
			// A label nobody uses is never introduced by a guess.
			if label.Uses == 0 && !slices.ContainsFunc(input.Candidates, func(candidate domain.Issue) bool {
				return similar(candidate) && slicesContainsIssueLabel(candidate.Labels, label.Label.ID)
			}) {
				break
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

const (
	triageSketchTitleSize       = 8
	triageSketchDescriptionSize = 16
	// Below this Jaccard similarity the title or description changed materially.
	triageSketchMaterialSimilarity = 0.5
)

// triageInputSketch fingerprints an issue's title and description as two
// bottom-k MinHash sketches (k hashed tokens each), so a later edit can be
// judged material — a typo fix is not, a rewritten title or a newly written
// description is — without storing the text twice.
func triageInputSketch(issue *domain.Issue) string {
	description := triageTokens(truncateTriageText(issue.Description))
	if len(description) < 3 {
		description = nil // a stub description carries no routing signal
	}
	return triageBottomK(triageTokens(issue.Title), triageSketchTitleSize) + "|" + triageBottomK(description, triageSketchDescriptionSize)
}

func triageBottomK(tokens map[string]bool, k int) string {
	values := make([]uint32, 0, len(tokens))
	for token := range tokens {
		hash := fnv.New32a()
		_, _ = hash.Write([]byte(token))
		values = append(values, hash.Sum32())
	}
	slices.Sort(values)
	values = slices.Compact(values)
	var result strings.Builder
	for _, value := range values[:min(k, len(values))] {
		fmt.Fprintf(&result, "%08x", value)
	}
	return result.String()
}

func parseTriageBottomK(value string) []uint32 {
	result := make([]uint32, 0, len(value)/8)
	for index := 0; index+8 <= len(value); index += 8 {
		var item uint32
		if _, err := fmt.Sscanf(value[index:index+8], "%08x", &item); err == nil {
			result = append(result, item)
		}
	}
	return result
}

// triageSketchSimilarity estimates the Jaccard similarity of two token sets
// from their bottom-k sketches (exact while the sets are smaller than k).
func triageSketchSimilarity(left, right []uint32, k int) float64 {
	if len(left) == 0 && len(right) == 0 {
		return 1
	}
	union := slices.Concat(left, right)
	slices.Sort(union)
	union = slices.Compact(union)
	union = union[:min(k, len(union))]
	shared := 0
	for _, value := range union {
		if slices.Contains(left, value) && slices.Contains(right, value) {
			shared++
		}
	}
	return float64(shared) / float64(len(union))
}

// triageInputChanged reports whether the issue's title or description changed
// materially since its suggestions were generated. Issues generated before
// sketches were recorded have no baseline and never count as changed.
func triageInputChanged(issue *domain.Issue) bool {
	previous := issue.SuggestionsInputSketch
	if previous == "" {
		return false
	}
	return triageSketchMaterial(previous, triageInputSketch(issue))
}

func triageSketchMaterial(previous, current string) bool {
	previousTitle, previousDescription, _ := strings.Cut(previous, "|")
	currentTitle, currentDescription, _ := strings.Cut(current, "|")
	return triageSketchSimilarity(parseTriageBottomK(previousTitle), parseTriageBottomK(currentTitle), triageSketchTitleSize) < triageSketchMaterialSimilarity ||
		triageSketchSimilarity(parseTriageBottomK(previousDescription), parseTriageBottomK(currentDescription), triageSketchDescriptionSize) < triageSketchMaterialSimilarity
}

// triageIssueHasSubstance reports whether an issue has enough words to be
// compared with other work: at least two real words (three or more letters,
// or CJK text). "212312" or "asd" has none.
func triageIssueHasSubstance(issue *domain.Issue) bool {
	words := 0
	for token := range triageTokens(issue.Title, truncateTriageText(issue.Description)) {
		runes := []rune(token)
		cjk := slices.ContainsFunc(runes, func(character rune) bool {
			return unicode.In(character, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
		})
		if (cjk || len(runes) >= 3) && slices.ContainsFunc(runes, unicode.IsLetter) {
			words++
			if words >= 2 {
				return true
			}
		}
	}
	return false
}
