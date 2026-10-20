package coding

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Runner events → agent activities (design §16). Events are applied in the
// order the runner sent them; each carries a runner-generated id that is
// claimed before it is applied, so a retried batch never duplicates an
// activity. Plan, diff, response and error events change the session
// document (one CAS update per batch); thoughts and actions become anchor
// activities that the existing agent session UI renders.

// eventClaimStale is how long an unfinished event claim blocks a retry
// (a crash between the claim and the activity leaves a stale claim).
const eventClaimStale = 30 * time.Second

// Activity body limits of the anchor store.
const (
	maxActivityBody = 60 << 10
	maxResultTail   = 4 << 10
)

// EventsResult reports what POST /events did.
type EventsResult struct {
	Accepted   int `json:"accepted"`
	Duplicates int `json:"duplicates"`
	Ignored    int `json:"ignored"`
}

var validEventTypes = []string{EventThought, EventAction, EventResponse, EventElicitation, EventError, EventPlan, EventStatus, EventDiff, EventUsage}

// Events ingests a batch of runner events.
func (s *Service) Events(ctx context.Context, token string, request EventsRequest) (EventsResult, error) {
	if len(request.Events) > MaxEventsPerRequest {
		return EventsResult{}, fmt.Errorf("%w: at most %d events per request", ErrInvalidInput, MaxEventsPerRequest)
	}
	for _, event := range request.Events {
		if event.ID == "" || len(event.ID) > 191 || !slices.Contains(validEventTypes, event.Type) {
			return EventsResult{}, fmt.Errorf("%w: every event needs an id and a known type", ErrInvalidInput)
		}
	}
	grant, err := s.Authenticate(ctx, token)
	if err != nil {
		return EventsResult{}, err
	}
	session, err := s.sessionFor(ctx, grant)
	if errors.Is(err, ErrGone) {
		// Late events of a finished session are dropped, so the runner stops retrying them.
		return EventsResult{Ignored: len(request.Events)}, nil
	}
	if err != nil {
		return EventsResult{}, err
	}
	result := EventsResult{}
	patch := sessionPatch{}
	for _, event := range request.Events {
		if event.TurnID != "" && session.CurrentTurnID != "" && event.TurnID != session.CurrentTurnID {
			result.Ignored++ // an event of an earlier turn
			continue
		}
		now := s.now().UTC()
		claimed, err := s.store.ClaimRunnerEvent(ctx, session.WorkspaceKey, session.ID, event.ID, now, now.Add(-eventClaimStale))
		if err != nil {
			return result, err
		}
		if !claimed {
			result.Duplicates++
			s.metrics.event(ctx, event.Type, true)
			continue
		}
		activityID, err := s.applyEvent(ctx, session, event, &patch)
		if err != nil {
			_ = s.store.ReleaseRunnerEvent(ctx, session.ID, event.ID)
			return result, err
		}
		if err := s.store.CompleteRunnerEvent(ctx, session.ID, event.ID, activityID); err != nil {
			return result, err
		}
		result.Accepted++
		s.metrics.event(ctx, event.Type, false)
	}
	if patch.empty() {
		return result, nil
	}
	if _, err := s.update(ctx, session.WorkspaceKey, session.ID, func(current *domain.CodingSession) error {
		if current.SandboxID != session.SandboxID {
			return ErrGone
		}
		patch.apply(s, current)
		return nil
	}); err != nil && !errors.Is(err, ErrGone) {
		return result, err
	}
	return result, nil
}

// sessionPatch accumulates the session changes of one batch.
type sessionPatch struct {
	plan        []domain.AgentPlanStep
	planSet     bool
	diffs       []domain.CodingDiffSummary
	summary     string
	errorText   string
	awaiting    bool
	running     bool
	displayName string
	step        *domain.CodingSessionStep
}

func (p sessionPatch) empty() bool {
	return !p.planSet && p.diffs == nil && p.summary == "" && p.errorText == "" && !p.awaiting && !p.running && p.displayName == "" && p.step == nil
}

func (p sessionPatch) apply(s *Service, current *domain.CodingSession) {
	now := s.now().UTC()
	if p.planSet {
		current.Plan = p.plan
	}
	if p.diffs != nil {
		current.Diffs = p.diffs
	}
	if p.summary != "" {
		current.Summary = p.summary
	}
	if p.errorText != "" {
		current.Error = p.errorText
	}
	if p.step != nil {
		step := *p.step
		current.CurrentStep = &step
	}
	if p.running && current.Phase == domain.CodingPhaseSetup {
		s.step.Enter(current, domain.CodingPhaseRunning, now)
	}
	if p.awaiting && current.Phase == domain.CodingPhaseRunning {
		s.step.Enter(current, domain.CodingPhaseAwaitingUser, now)
	}
}

// applyEvent maps one event; it returns the anchor activity id it created.
func (s *Service) applyEvent(ctx context.Context, session domain.CodingSession, event RunnerEvent, patch *sessionPatch) (string, error) {
	switch event.Type {
	case EventThought:
		if strings.TrimSpace(event.Body) == "" {
			return "", nil
		}
		return s.anchorActivity(ctx, session, domain.AgentActivity{Type: "thought", Body: clip(event.Body, maxActivityBody), Ephemeral: event.Ephemeral}), nil
	case EventAction:
		if event.Action == nil || strings.TrimSpace(event.Action.Action) == "" {
			return "", nil
		}
		patch.step = &domain.CodingSessionStep{TurnID: firstNonEmpty(event.TurnID, session.CurrentTurnID), Action: clip(strings.TrimSpace(event.Action.Action), 256), Parameter: firstLine(event.Action.Parameter, 512), At: s.now().UTC()}
		return s.anchorActivity(ctx, session, actionActivity(*event.Action, event.Ephemeral)), nil
	case EventStatus:
		if event.Phase == "running" {
			patch.running = true
		}
		if strings.TrimSpace(event.Body) == "" {
			return "", nil
		}
		return s.anchorActivity(ctx, session, domain.AgentActivity{Type: "action", Body: clip(event.Body, 256), Metadata: map[string]any{"action": clip(event.Body, 256)}}), nil
	case EventResponse:
		// The final answer is posted with the PR link once the turn is
		// published (Service.afterTransition), so it is kept on the session.
		patch.summary = clip(strings.TrimSpace(event.Body), 1<<20)
		return "", nil
	case EventElicitation:
		if strings.TrimSpace(event.Body) == "" {
			return "", nil
		}
		patch.awaiting = true
		return s.anchorActivity(ctx, session, domain.AgentActivity{Type: "elicitation", Body: clip(event.Body, maxActivityBody)}), nil
	case EventError:
		if event.Error != nil {
			patch.errorText = clip(event.Error.Message, 4<<10)
		} else {
			patch.errorText = clip(event.Body, 4<<10)
		}
		return "", nil
	case EventPlan:
		plan := make([]domain.AgentPlanStep, 0, len(event.Plan))
		for _, step := range event.Plan {
			status := step.Status
			if status == "in_progress" {
				status = "inProgress"
			}
			switch status {
			case "pending", "inProgress", "completed", "canceled":
			default:
				status = "pending"
			}
			plan = append(plan, domain.AgentPlanStep{Content: clip(step.Content, 1<<10), Status: status})
			if len(plan) == 100 {
				break
			}
		}
		patch.plan, patch.planSet = plan, true
		return "", nil
	case EventDiff:
		diffs := event.Diff
		if len(diffs) > 2000 {
			diffs = diffs[:2000]
		}
		patch.diffs = slices.Clone(diffs)
		if patch.diffs == nil {
			patch.diffs = []domain.CodingDiffSummary{}
		}
		return "", nil
	}
	return "", nil // usage: informational, billing uses the LLM proxy's metering
}

// actionActivity renders a tool call the way agent-thread-items.ts expects:
// the body is the action, metadata carries action/parameter/result.
func actionActivity(action ActionDetail, ephemeral bool) domain.AgentActivity {
	body := clip(action.Action, 256)
	if action.Parameter != "" {
		body += " " + clip(action.Parameter, 1<<10)
	}
	metadata := map[string]any{"action": clip(action.Action, 256), "parameter": clip(action.Parameter, 4<<10), "result": tail(action.Result, maxResultTail)}
	if action.Tool != "" {
		metadata["tool"] = action.Tool
	}
	if action.ExitCode != nil {
		metadata["exitCode"] = *action.ExitCode
	}
	return domain.AgentActivity{Type: "action", Body: body, Metadata: metadata, Ephemeral: ephemeral}
}

func clip(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	return text[:cut] + "…"
}

func tail(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	start := len(text) - limit
	for start < len(text) && (text[start]&0xC0) == 0x80 {
		start++
	}
	return "…" + text[start:]
}
