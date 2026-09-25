package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

const (
	// Runs kept per loop for the history view.
	loopRunHistory = 25
	// A loop does not re-trigger on the same entity within this window, so
	// changes a run makes to its own trigger entity cannot start a cycle.
	loopRetriggerCooldown = 2 * time.Minute
	loopRunTimeout        = 10 * time.Minute
)

type agentToolFilterKey struct{}

// loopTrigger describes why a loop run started.
type loopTrigger struct {
	Kind       string // manual | schedule | event
	EventType  string
	EntityType string // issue | project | initiative | cycle
	EntityID   string
	SourceKey  string
}

var errLoopUnavailable = errors.New("loop cannot run")

type loopRunGuard struct {
	mu       sync.Mutex
	inFlight map[string]bool
	recent   map[string]time.Time
}

var loopGuards = &loopRunGuard{inFlight: map[string]bool{}, recent: map[string]time.Time{}}

// claim reserves a loop/entity pair for a run, honoring the retrigger cooldown.
func (g *loopRunGuard) claim(key string, event bool, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inFlight[key] || event && now.Sub(g.recent[key]) < loopRetriggerCooldown {
		return false
	}
	g.inFlight[key] = true
	return true
}

func (g *loopRunGuard) release(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.inFlight, key)
	g.recent[key] = time.Now()
}

func loopByID(data *domain.Bootstrap, id string) *domain.Loop {
	for index := range data.Loops {
		if data.Loops[index].ID == id {
			return &data.Loops[index]
		}
	}
	return nil
}

// loopRunnable reports why a loop cannot run right now, if it cannot.
func (s *server) loopRunnable(data domain.Bootstrap, loop domain.Loop) error {
	if enabled, ok := data.WorkspaceSettings.FeatureFlags["loops"]; ok && !enabled {
		return fmt.Errorf("%w: Loops are disabled for this workspace", errLoopUnavailable)
	}
	if !loop.Enabled {
		return fmt.Errorf("%w: the loop is paused", errLoopUnavailable)
	}
	if !s.agent.Enabled {
		return fmt.Errorf("%w: Flow Agent is not configured on this server", errLoopUnavailable)
	}
	return nil
}

func (s *server) listLoopRuns(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	id := r.PathValue("id")
	if loopByID(&data, id) == nil {
		writeError(w, http.StatusNotFound, "loop not found")
		return
	}
	runs := []domain.LoopRun{}
	for _, run := range data.LoopRuns {
		if run.LoopID == id {
			runs = append(runs, run)
		}
	}
	writeJSON(w, http.StatusOK, runs)
}

// runLoopNow starts a manual run. Only loops without a triggering entity
// (scheduled loops) can run on demand.
func (s *server) runLoopNow(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	loop := loopByID(&data, r.PathValue("id"))
	if loop == nil {
		writeError(w, http.StatusNotFound, "loop not found")
		return
	}
	if loop.TriggerType != "schedule" {
		writeError(w, http.StatusBadRequest, "Run now is only available for scheduled loops")
		return
	}
	run, err := s.startLoopRun(workspaceKey(r), loop.ID, loopTrigger{Kind: "manual"}, authUser(r).ID)
	if errors.Is(err, errLoopUnavailable) {
		writeError(w, http.StatusConflict, strings.TrimPrefix(err.Error(), errLoopUnavailable.Error()+": "))
		return
	}
	if errors.Is(err, errConflict) {
		writeError(w, http.StatusConflict, "This loop is already running")
		return
	}
	respondMutation(w, err, http.StatusAccepted, run)
}

// startLoopRun records a running run and executes it in the background.
func (s *server) startLoopRun(workspace, loopID string, trigger loopTrigger, actorID string) (domain.LoopRun, error) {
	data, ok := s.store.WorkspaceMetadata(workspace)
	if !ok {
		return domain.LoopRun{}, errNotFound
	}
	loop := loopByID(&data, loopID)
	if loop == nil {
		return domain.LoopRun{}, errNotFound
	}
	if err := s.loopRunnable(data, *loop); err != nil {
		return domain.LoopRun{}, err
	}
	guardKey := loopID + "|" + trigger.EntityID
	if !loopGuards.claim(guardKey, trigger.Kind == "event", time.Now()) {
		return domain.LoopRun{}, errConflict
	}
	now := time.Now().UTC()
	run := domain.LoopRun{ID: fmt.Sprintf("loop_run_%d", now.UnixNano()), LoopID: loopID, Status: "running", Trigger: trigger.Kind, EventType: trigger.EventType, EntityType: trigger.EntityType, EntityID: trigger.EntityID, ActorID: actorID, StartedAt: now}
	if trigger.EntityType == "issue" {
		if issue, err := s.store.IssueRecord(context.Background(), workspace, trigger.EntityID); err == nil {
			run.EntityIdentifier = issue.Identifier
		}
	}
	err := s.store.MutateWorkspace(context.Background(), workspace, "loop.run_started", loopID, map[string]any{"runId": run.ID, "trigger": trigger.Kind}, func(data *domain.Bootstrap) error {
		item := loopByID(data, loopID)
		if item == nil {
			return errNotFound
		}
		item.LastRunAt = &now
		data.LoopRuns = append([]domain.LoopRun{run}, data.LoopRuns...)
		data.LoopRuns = trimLoopRuns(data.LoopRuns, loopID)
		return nil
	})
	if err != nil {
		loopGuards.release(guardKey)
		return domain.LoopRun{}, err
	}
	go func() {
		defer loopGuards.release(guardKey)
		ctx, cancel := context.WithTimeout(context.Background(), loopRunTimeout)
		defer cancel()
		s.finishLoopRun(workspace, run, s.executeLoopRun(ctx, workspace, *loop, trigger, &run))
	}()
	return run, nil
}

func trimLoopRuns(runs []domain.LoopRun, loopID string) []domain.LoopRun {
	count := 0
	return slices.DeleteFunc(runs, func(run domain.LoopRun) bool {
		if run.LoopID != loopID {
			return false
		}
		count++
		return count > loopRunHistory
	})
}

func (s *server) finishLoopRun(workspace string, run domain.LoopRun, runErr error) {
	now := time.Now().UTC()
	run.FinishedAt = &now
	run.Status = "completed"
	if runErr != nil {
		run.Status, run.Error = "failed", runErr.Error()
	}
	err := s.store.MutateWorkspace(context.Background(), workspace, "loop.run_finished", run.LoopID, map[string]any{"runId": run.ID, "status": run.Status}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.LoopRuns, func(item domain.LoopRun) bool { return item.ID == run.ID })
		if index < 0 {
			return errNotFound
		}
		data.LoopRuns[index] = run
		return nil
	})
	if err != nil {
		log.Printf("Loop run finish workspace=%s run=%s: %v", workspace, run.ID, err)
	}
}

// loopRunRequest builds the request context a loop run acts under: the loop
// owner in the loop's workspace.
func (s *server) loopRunRequest(ctx context.Context, workspace string, owner domain.User) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://flow.internal/api/loops/run", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Workspace-Key", workspace)
	ctx = context.WithValue(request.Context(), workspaceKeyContextKey{}, workspace)
	ctx = context.WithValue(ctx, authUserContextKey{}, owner)
	return request.WithContext(ctx), nil
}

func (s *server) executeLoopRun(ctx context.Context, workspace string, loop domain.Loop, trigger loopTrigger, run *domain.LoopRun) error {
	metadata, ok := s.store.WorkspaceMetadata(workspace)
	if !ok {
		return errNotFound
	}
	owner := userByID(&metadata, loop.OwnerID)
	if owner == nil {
		return fmt.Errorf("the loop owner is no longer in this workspace")
	}
	var data domain.Bootstrap
	if s.authDisabled {
		data = metadata
		data.Viewer, data.ViewerRole = *owner, "admin"
	} else {
		var err error
		if data, _, err = s.store.BootstrapForUser(ctx, workspace, owner.ID); err != nil {
			return fmt.Errorf("the loop owner cannot access this workspace")
		}
	}
	if err := agentWorkspacePolicy(data.WorkspaceSettings, data.ViewerRole); err != nil {
		return err
	}
	request, err := s.loopRunRequest(ctx, workspace, *owner)
	if err != nil {
		return err
	}
	scope := s.newLoopScope(data, loop, trigger)
	if connectors, err := s.discoverConnectorTools(ctx, data); err == nil {
		connectors = slices.DeleteFunc(connectors, func(tool connectorTool) bool { return !slices.Contains(loop.ConnectorIDs, tool.Policy.ID) })
		request = request.WithContext(context.WithValue(request.Context(), connectorToolsKey{}, connectors))
	}
	request = request.WithContext(context.WithValue(request.Context(), connectorContextKey{}, connectorRequestContext{Workspace: workspace, UserID: owner.ID}))
	request = request.WithContext(context.WithValue(request.Context(), agentToolFilterKey{}, scope.offers))

	entity := s.loopEntityContext(ctx, workspace, data, trigger)
	messages := []agentProviderMessage{
		{Role: "system", Content: loopSystemPrompt(data, loop, trigger, entity, scope)},
		{Role: "user", Content: "Run this loop now and follow its instructions."},
	}
	output := ""
	for turnIndex := 0; turnIndex < maxAgentToolTurns; turnIndex++ {
		turn, err := s.requestAgentTurn(request.Context(), messages, nil)
		if err != nil {
			run.Output = strings.TrimSpace(output)
			return err
		}
		output += turn.Text
		if len(turn.ToolCalls) == 0 {
			run.Output = strings.TrimSpace(output)
			if run.Output == "" && len(run.ToolCalls) == 0 {
				return fmt.Errorf("Flow Agent returned an empty response")
			}
			return nil
		}
		messages = append(messages, agentProviderMessage{Role: "assistant", Content: turn.Text, ToolCalls: turn.ToolCalls})
		for _, call := range turn.ToolCalls {
			record := domain.LoopRunToolCall{Name: call.Name, Status: "completed"}
			var result []byte
			callErr := scope.check(ctx, call)
			if callErr != nil {
				record.Status = "blocked"
			} else if result, callErr = s.executeAgentTool(request, data, call); callErr != nil {
				record.Status = "error"
			}
			content := string(result)
			if callErr != nil {
				record.Error, content = callErr.Error(), callErr.Error()
			}
			run.ToolCalls = append(run.ToolCalls, record)
			messages = append(messages, agentProviderMessage{Role: "tool", ToolResult: &agentProviderToolResult{CallID: call.ID, Content: content, IsError: callErr != nil}})
		}
	}
	run.Output = strings.TrimSpace(output)
	return fmt.Errorf("the loop exceeded the tool turn limit")
}

// loopScope enforces a loop's permissions on the tools a run may use.
type loopScope struct {
	s          *server
	workspace  string
	loop       domain.Loop
	trigger    loopTrigger
	entityIDs  []string // IDs and identifiers naming the triggering entity
	teams      map[string]bool
	codeAccess bool
}

func (s *server) newLoopScope(data domain.Bootstrap, loop domain.Loop, trigger loopTrigger) *loopScope {
	scope := &loopScope{s: s, workspace: data.Workspace.URLKey, loop: loop, trigger: trigger, teams: loopTeams(data, loop)}
	if data.WorkspaceSettings.FeatureSettings.RepositoryAccess != nil {
		scope.codeAccess = data.WorkspaceSettings.FeatureSettings.RepositoryAccess.AllowAutomationAccess
	}
	if trigger.EntityID != "" {
		scope.entityIDs = []string{trigger.EntityID}
		if trigger.EntityType == "issue" {
			if issue, err := s.store.IssueRecord(context.Background(), data.Workspace.URLKey, trigger.EntityID); err == nil {
				scope.entityIDs = append(scope.entityIDs, issue.Identifier)
			}
		}
	}
	return scope
}

// loopTeams lists the teams whose data a loop may touch: all public teams, or
// the teams selected in its trigger scope.
func loopTeams(data domain.Bootstrap, loop domain.Loop) map[string]bool {
	selected := map[string]bool{}
	if ids, ok := loop.TriggerConfig["teamIds"].([]any); ok {
		for _, id := range ids {
			if value, ok := id.(string); ok {
				selected[value] = true
			}
		}
	}
	teams := map[string]bool{}
	for _, team := range data.Teams {
		if team.ArchivedAt != nil || team.RetiredAt != nil {
			continue
		}
		settings := data.TeamSettings[team.ID]
		public := !team.Private && !strings.EqualFold(settings.Access, "private") && !strings.EqualFold(settings.Access, "restricted")
		if loop.TeamAccess == "selected" && selected[team.ID] || loop.TeamAccess != "selected" && public {
			teams[team.ID] = true
		}
	}
	return teams
}

func codeIntelligenceTool(name string) bool {
	return strings.Contains(strings.TrimPrefix(name, "mcp__flow."), "diff")
}

func (scope *loopScope) offers(tool agentProviderTool) bool {
	return scope.codeAccess || !codeIntelligenceTool(tool.Name)
}

func loopToolArgs(call domain.AgentToolCall) map[string]any {
	args := map[string]any{}
	_ = json.Unmarshal(call.Arguments, &args)
	return args
}

func loopStringArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// check refuses a tool call the loop's permissions do not allow.
func (scope *loopScope) check(ctx context.Context, call domain.AgentToolCall) error {
	name := strings.TrimPrefix(call.Name, "mcp__flow.")
	if codeIntelligenceTool(name) && !scope.codeAccess {
		return fmt.Errorf("Code Intelligence is not enabled for loops in this workspace")
	}
	if strings.HasPrefix(name, "external_") {
		return nil
	}
	if !scope.s.agentToolRequiresApproval(name) {
		return nil
	}
	args := loopToolArgs(call)
	if teamID := loopStringArg(args, "teamId", "team"); teamID != "" && !scope.teams[teamID] && !scope.teamKeyAllowed(teamID) {
		return fmt.Errorf("This loop cannot access that team")
	}
	target := loopStringArg(args, "issueId", "issue", "id")
	if name == "save_issue" || name == "save_comment" || name == "create_attachment" || name == "delete_attachment" || name == "delete_comment" {
		if target != "" {
			issue, err := scope.s.store.IssueRecord(ctx, scope.workspace, target)
			if err != nil {
				issue, err = scope.s.issueByIdentifier(ctx, scope.workspace, target)
			}
			if err == nil {
				if !scope.teams[issue.Team.ID] {
					return fmt.Errorf("This loop cannot access that team")
				}
			}
		}
	}
	if len(scope.entityIDs) == 0 || scope.loop.AllowChangesOutsideTrigger {
		return nil
	}
	if target == "" || !slices.ContainsFunc(scope.entityIDs, func(id string) bool { return strings.EqualFold(id, target) }) {
		return fmt.Errorf("This loop can only change the %s that triggered it", scope.trigger.EntityType)
	}
	return nil
}

func (scope *loopScope) teamKeyAllowed(key string) bool {
	data, ok := scope.s.store.WorkspaceMetadata(scope.workspace)
	if !ok {
		return false
	}
	for _, team := range data.Teams {
		if strings.EqualFold(team.Key, key) || strings.EqualFold(team.Name, key) {
			return scope.teams[team.ID]
		}
	}
	return false
}

func (s *server) issueByIdentifier(ctx context.Context, workspace, identifier string) (domain.Issue, error) {
	data, ok := s.store.WorkspaceMetadata(workspace)
	if !ok {
		return domain.Issue{}, errNotFound
	}
	for _, issue := range data.Issues {
		if strings.EqualFold(issue.Identifier, identifier) {
			return issue, nil
		}
	}
	return domain.Issue{}, errNotFound
}

func (s *server) loopEntityContext(ctx context.Context, workspace string, data domain.Bootstrap, trigger loopTrigger) string {
	if trigger.EntityID == "" {
		return ""
	}
	var value any
	switch trigger.EntityType {
	case "issue":
		issue, err := s.store.IssueRecord(ctx, workspace, trigger.EntityID)
		if err != nil {
			return ""
		}
		labels := []string{}
		for _, label := range issue.Labels {
			labels = append(labels, label.Name)
		}
		assignee := ""
		if issue.Assignee != nil {
			assignee = issue.Assignee.Name
		}
		value = map[string]any{"id": issue.ID, "identifier": issue.Identifier, "title": issue.Title, "description": truncateSettingsText(issue.Description, 6000), "status": issue.State.Name, "priority": issue.PriorityLabel, "labels": labels, "assignee": assignee, "team": issue.Team.Name}
	case "project":
		for _, project := range data.Projects {
			if project.ID == trigger.EntityID {
				value = map[string]any{"id": project.ID, "name": project.Name, "description": truncateSettingsText(project.Description, 6000)}
			}
		}
	case "initiative":
		for _, initiative := range data.Initiatives {
			if initiative.ID == trigger.EntityID {
				value = map[string]any{"id": initiative.ID, "name": initiative.Name, "description": truncateSettingsText(initiative.Description, 6000)}
			}
		}
	case "cycle":
		for _, cycle := range data.Cycles {
			if cycle.ID == trigger.EntityID {
				value = map[string]any{"id": cycle.ID, "number": cycle.Number, "name": cycle.Name, "teamId": cycle.TeamID}
			}
		}
	}
	if value == nil {
		return ""
	}
	raw, _ := json.MarshalIndent(value, "", "  ")
	return string(raw)
}

func loopSystemPrompt(data domain.Bootstrap, loop domain.Loop, trigger loopTrigger, entity string, scope *loopScope) string {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "You are running the Flow automation loop %q in the %s workspace. Work autonomously: there is no user to answer questions. Use the Flow tools to act, then reply with a short summary of what you did.\n", loop.Name, data.Workspace.Name)
	if guidance := strings.TrimSpace(data.WorkspaceSettings.AgentInstructions); guidance != "" {
		fmt.Fprintf(&prompt, "\nWorkspace guidance:\n%s\n", truncateSettingsText(guidance, 8000))
	}
	fmt.Fprintf(&prompt, "\nLoop instructions:\n%s\n", truncateSettingsText(loop.Instructions, 12000))
	switch trigger.Kind {
	case "event":
		fmt.Fprintf(&prompt, "\nThis run was triggered because a %s was %s.\n", trigger.EntityType, strings.TrimPrefix(trigger.EventType, trigger.EntityType+"."))
	case "schedule":
		prompt.WriteString("\nThis run was started by the loop's schedule.\n")
	default:
		prompt.WriteString("\nThis run was started manually.\n")
	}
	if entity != "" {
		fmt.Fprintf(&prompt, "\nTriggering %s:\n%s\n", trigger.EntityType, entity)
		if !loop.AllowChangesOutsideTrigger {
			fmt.Fprintf(&prompt, "\nOnly change the triggering %s; changes to anything else will be refused.\n", trigger.EntityType)
		}
	}
	names := []string{}
	for _, team := range data.Teams {
		if scope.teams[team.ID] {
			names = append(names, team.Name)
		}
	}
	fmt.Fprintf(&prompt, "\nTeams this loop may use: %s.\n", strings.Join(names, ", "))
	return prompt.String()
}

// runDueLoops starts scheduled loops whose next run time has passed.
func (s *server) runDueLoops(workspace string, now time.Time) {
	data, ok := s.store.WorkspaceMetadata(workspace)
	if !ok {
		return
	}
	for _, loop := range data.Loops {
		if loop.TriggerType != "schedule" || !loop.Enabled {
			continue
		}
		next := loop.NextRunAt
		if next == nil {
			computed := nextLoopRun(loop.TriggerConfig, loop.CreatedAt, now)
			s.setLoopNextRun(workspace, loop.ID, computed)
			next = &computed
		}
		if now.Before(*next) {
			continue
		}
		s.setLoopNextRun(workspace, loop.ID, nextLoopRun(loop.TriggerConfig, loop.CreatedAt, now))
		if _, err := s.startLoopRun(workspace, loop.ID, loopTrigger{Kind: "schedule"}, ""); err != nil && !errors.Is(err, errConflict) {
			log.Printf("Scheduled loop workspace=%s loop=%s: %v", workspace, loop.ID, err)
		}
	}
}

func (s *server) setLoopNextRun(workspace, loopID string, next time.Time) {
	err := s.store.MutateWorkspace(context.Background(), workspace, "loop.scheduled", loopID, nil, func(data *domain.Bootstrap) error {
		loop := loopByID(data, loopID)
		if loop == nil {
			return errNotFound
		}
		loop.NextRunAt = &next
		return nil
	})
	if err != nil {
		log.Printf("Loop schedule workspace=%s loop=%s: %v", workspace, loopID, err)
	}
}

// nextLoopRun returns the first scheduled time strictly after `after`. The
// schedule starts on config.starting (a date) at config.time in
// config.timezone and repeats every config.interval days, weeks or months.
func nextLoopRun(config map[string]any, created, after time.Time) time.Time {
	location := time.UTC
	if name, ok := config["timezone"].(string); ok {
		if loaded, err := time.LoadLocation(name); err == nil {
			location = loaded
		}
	}
	hour, minute := 10, 0
	if value, ok := config["time"].(string); ok {
		if parsed, err := time.Parse("15:04", value); err == nil {
			hour, minute = parsed.Hour(), parsed.Minute()
		}
	}
	start := created.In(location)
	if value, ok := config["starting"].(string); ok {
		if parsed, err := time.ParseInLocation("2006-01-02", value, location); err == nil {
			start = parsed
		}
	}
	start = time.Date(start.Year(), start.Month(), start.Day(), hour, minute, 0, 0, location)
	interval := 1
	switch value := config["interval"].(type) {
	case float64:
		interval = int(value)
	case string:
		interval, _ = strconv.Atoi(value)
	}
	if interval < 1 {
		interval = 1
	}
	unit, _ := config["unit"].(string)
	step := func(at time.Time, n int) time.Time {
		switch unit {
		case "week":
			return at.AddDate(0, 0, 7*interval*n)
		case "month":
			return at.AddDate(0, interval*n, 0)
		default:
			return at.AddDate(0, 0, interval*n)
		}
	}
	if start.After(after) {
		return start.UTC()
	}
	// Jump close to `after`, then step forward.
	periods := 0
	switch unit {
	case "week":
		periods = int(after.Sub(start).Hours() / (24 * 7 * float64(interval)))
	case "month":
		periods = ((after.Year()-start.Year())*12 + int(after.Month()) - int(start.Month())) / interval
	default:
		periods = int(after.Sub(start).Hours() / (24 * float64(interval)))
	}
	next := step(start, max(periods-1, 0))
	for !next.After(after) {
		periods++
		next = step(start, periods)
	}
	return next.UTC()
}

// dispatchLoopTriggers starts entity-triggered loops that match a domain event.
func (s *server) dispatchLoopTriggers(workspace string, event domain.DomainEvent) {
	entityType, action, found := strings.Cut(event.Type, ".")
	if !found || action != "created" && action != "updated" || event.AggregateID == "" {
		return
	}
	if !slices.Contains([]string{"issue", "project", "initiative", "cycle"}, entityType) {
		return
	}
	data, ok := s.store.WorkspaceMetadata(workspace)
	if !ok || !slices.ContainsFunc(data.Loops, func(loop domain.Loop) bool { return loop.Enabled && loop.TriggerType == entityType }) {
		return
	}
	var issue *domain.Issue
	if entityType == "issue" {
		record, err := s.store.IssueRecord(context.Background(), workspace, event.AggregateID)
		if err != nil {
			return
		}
		issue = &record
	}
	sourceKey, fromLoop := loopEventSource(data, issue, action)
	if fromLoop {
		return
	}
	for _, loop := range data.Loops {
		if !loop.Enabled || loop.TriggerType != entityType || !loopActionMatches(loop, action) {
			continue
		}
		teamIDs := loopEntityTeams(data, entityType, event.AggregateID, issue)
		if !loopScopeMatches(data, loop, teamIDs) || issue != nil && !loopFilterMatches(loop.TriggerConfig, *issue) {
			continue
		}
		if sourceKey != "" && !loopSourceTrusted(data.WorkspaceSettings, loop, sourceKey) {
			continue
		}
		trigger := loopTrigger{Kind: "event", EventType: event.Type, EntityType: entityType, EntityID: event.AggregateID, SourceKey: sourceKey}
		if _, err := s.startLoopRun(workspace, loop.ID, trigger, ""); err != nil && !errors.Is(err, errConflict) && !errors.Is(err, errLoopUnavailable) {
			log.Printf("Loop trigger workspace=%s loop=%s: %v", workspace, loop.ID, err)
		}
	}
}

func loopActionMatches(loop domain.Loop, action string) bool {
	configured, _ := loop.TriggerConfig["action"].(string)
	if configured == "" {
		configured = "created or updated"
		if loop.TriggerType == "cycle" {
			configured = "created"
		}
	}
	return action == "created" || configured == "created or updated"
}

func loopEntityTeams(data domain.Bootstrap, entityType, id string, issue *domain.Issue) []string {
	switch entityType {
	case "issue":
		return []string{issue.Team.ID}
	case "project":
		for _, project := range data.Projects {
			if project.ID == id {
				return project.TeamIDs
			}
		}
	case "cycle":
		for _, cycle := range data.Cycles {
			if cycle.ID == id {
				return []string{cycle.TeamID}
			}
		}
	}
	return nil
}

// loopScopeMatches applies the trigger's team scope and the loop's team access.
func loopScopeMatches(data domain.Bootstrap, loop domain.Loop, teamIDs []string) bool {
	if len(teamIDs) == 0 {
		return loop.TriggerType == "initiative" || loop.TriggerType == "project"
	}
	allowed := loopTeams(data, loop)
	scoped, _ := loop.TriggerConfig["teamIds"].([]any)
	return slices.ContainsFunc(teamIDs, func(teamID string) bool {
		if !allowed[teamID] {
			return false
		}
		return len(scoped) == 0 || slices.ContainsFunc(scoped, func(value any) bool { return value == teamID })
	})
}

func loopFilterMatches(config map[string]any, issue domain.Issue) bool {
	if config["filter"] == nil {
		return true
	}
	want := strings.ToLower(strings.TrimSpace(fmt.Sprint(config["filterValue"])))
	if want == "" || want == "<nil>" {
		return true
	}
	values := []string{}
	switch field, _ := config["filterField"].(string); field {
	case "priority":
		values = append(values, issue.PriorityLabel, strconv.Itoa(issue.Priority))
	case "label":
		for _, label := range issue.Labels {
			values = append(values, label.Name)
		}
	case "assignee":
		if issue.Assignee != nil {
			values = append(values, issue.Assignee.Name, issue.Assignee.DisplayName, issue.Assignee.Email)
		}
	default:
		values = append(values, issue.State.Name, issue.State.Type)
	}
	matched := slices.ContainsFunc(values, func(value string) bool { return strings.EqualFold(strings.TrimSpace(value), want) })
	if operator, _ := config["filterOperator"].(string); operator == "isNot" {
		return !matched
	}
	return matched
}

// loopEventSource names the external source behind an issue event, if any.
// Issues created by a loop run never trigger loops.
func loopEventSource(data domain.Bootstrap, issue *domain.Issue, action string) (key string, fromLoop bool) {
	if issue == nil || action != "created" {
		return "", false
	}
	for _, ask := range data.Asks {
		if ask.IssueID != issue.ID {
			continue
		}
		if ask.Source == "loop" {
			return "", true
		}
		if ask.Source != "" && ask.Source != "web" {
			return "integration:" + ask.Source, false
		}
	}
	for _, member := range data.Members {
		if member.User.ID == issue.Creator.ID && member.Role == "app" {
			return "appUser:" + member.User.ID, false
		}
	}
	return "", false
}

func loopSourceTrusted(settings domain.WorkspaceSettings, loop domain.Loop, key string) bool {
	if !settings.ExternalLoopTriggers {
		return false
	}
	if slices.Contains(loop.TrustedSourceKeys, key) {
		return true
	}
	return settings.TrustedSourcesMode == "allowlist" && slices.Contains(settings.TrustedSourcesAllowlist, key)
}
