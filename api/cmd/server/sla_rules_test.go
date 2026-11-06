package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func activeSLARules(data domain.Bootstrap, issueID string) []string {
	var rules []string
	for _, sla := range data.IssueSLAs {
		if sla.IssueID == issueID && sla.Status != "removed" {
			rules = append(rules, sla.RuleID)
		}
	}
	return rules
}

// The reference rule set: urgent and high issues get a deadline, the rest none.
func priorityRuleSet() []domain.SLARule {
	return []domain.SLARule{
		{ID: "urgent", Enabled: true, Action: domain.SLAActionAdd, Filters: map[string]any{"priority": 1}, TargetMinutes: 1440},
		{ID: "high", Enabled: true, Action: domain.SLAActionAdd, Filters: map[string]any{"priority": 2}, TargetMinutes: 10080},
		{ID: "medium", Enabled: true, Action: domain.SLAActionRemove, Filters: map[string]any{"priority": 3}},
		{ID: "none", Enabled: true, Action: domain.SLAActionRemove, Filters: map[string]any{"priority": 0}},
	}
}

func TestSLARulesFollowTheFirstMatchingRuleAndRestartOnReEscalation(t *testing.T) {
	data := domain.Bootstrap{SLARules: priorityRuleSet()}
	now := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	issue := domain.Issue{ID: "issue", Priority: 1, State: domain.WorkflowState{Type: "started"}}

	applySLARules(&data, &issue, now)
	if got := activeSLARules(data, issue.ID); !slices.Equal(got, []string{"urgent"}) {
		t.Fatalf("urgent issue SLAs = %v", got)
	}
	if due := data.IssueSLAs[0].DueAt; !due.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("urgent deadline = %s", due)
	}

	// Lowering the priority hits a remove rule: the SLA goes away and nothing replaces it.
	issue.Priority = 3
	applySLARules(&data, &issue, now.Add(time.Hour))
	if got := activeSLARules(data, issue.ID); len(got) != 0 {
		t.Fatalf("medium issue kept SLAs %v", got)
	}

	// Raising it again starts a new clock from the escalation, not the old deadline.
	escalated := now.Add(48 * time.Hour)
	issue.Priority = 2
	applySLARules(&data, &issue, escalated)
	if got := activeSLARules(data, issue.ID); !slices.Equal(got, []string{"high"}) {
		t.Fatalf("high issue SLAs = %v", got)
	}
	issue.Priority = 1
	later := escalated.Add(time.Hour)
	applySLARules(&data, &issue, later)
	index := slices.IndexFunc(data.IssueSLAs, func(sla domain.IssueSLA) bool { return sla.RuleID == "urgent" })
	if index < 0 || data.IssueSLAs[index].Status != "active" || !data.IssueSLAs[index].StartedAt.Equal(later) || !data.IssueSLAs[index].DueAt.Equal(later.Add(24*time.Hour)) {
		t.Fatalf("re-escalated SLA did not restart: %#v", data.IssueSLAs)
	}
	if got := activeSLARules(data, issue.ID); !slices.Equal(got, []string{"urgent"}) {
		t.Fatalf("re-escalated issue SLAs = %v", got)
	}
}

func TestSLARuleOrderDecidesWhichOfSeveralMatchingRulesApplies(t *testing.T) {
	bug := domain.SLARule{ID: "bug", Enabled: true, Filters: map[string]any{"label": "Bug"}, TargetMinutes: 60}
	urgent := domain.SLARule{ID: "urgent", Enabled: true, Filters: map[string]any{"priority": 1}, TargetMinutes: 1440}
	issue := domain.Issue{ID: "issue", Priority: 1, Labels: []domain.IssueLabel{{ID: "label", Name: "Bug"}}, State: domain.WorkflowState{Type: "started"}}
	now := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

	data := domain.Bootstrap{SLARules: []domain.SLARule{bug, urgent}}
	applySLARules(&data, &issue, now)
	if got := activeSLARules(data, issue.ID); !slices.Equal(got, []string{"bug"}) {
		t.Fatalf("first rule did not win: %v", got)
	}

	// Reordering hands the issue to the other rule and retires the first one's SLA.
	data.SLARules = []domain.SLARule{urgent, bug}
	applySLARules(&data, &issue, now.Add(time.Minute))
	if got := activeSLARules(data, issue.ID); !slices.Equal(got, []string{"urgent"}) {
		t.Fatalf("reordered rules did not hand over the issue: %v", got)
	}

	// A disabled rule never wins, so the next matching rule applies.
	data.SLARules[0].Enabled = false
	applySLARules(&data, &issue, now.Add(2*time.Minute))
	if got := activeSLARules(data, issue.ID); !slices.Equal(got, []string{"bug"}) {
		t.Fatalf("disabled rule still won: %v", got)
	}
}

func TestSLARulesWithoutAnActionStillAddAnSLA(t *testing.T) {
	legacy := domain.SLARule{ID: "legacy", Enabled: true, TargetMinutes: 60}
	if legacy.Removes() {
		t.Fatal("a rule saved before actions existed must add SLAs")
	}
	data := domain.Bootstrap{SLARules: []domain.SLARule{legacy}}
	issue := domain.Issue{ID: "issue", State: domain.WorkflowState{Type: "started"}}
	applySLARules(&data, &issue, time.Now())
	if got := activeSLARules(data, issue.ID); !slices.Equal(got, []string{"legacy"}) {
		t.Fatalf("legacy rule SLAs = %v", got)
	}
}

func TestSLARuleAPIActionOrderAndValidation(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	requestJSON[map[string]any](t, handler, http.MethodPut, "/api/sla-settings", map[string]any{"enabled": true}, http.StatusOK)

	first := requestJSON[domain.SLARule](t, handler, http.MethodPost, "/api/sla-rules", map[string]any{"name": "Urgent", "filters": map[string]any{"priority": 1}, "targetMinutes": 1440}, http.StatusCreated)
	second := requestJSON[domain.SLARule](t, handler, http.MethodPost, "/api/sla-rules", map[string]any{"name": "Medium", "filters": map[string]any{"priority": 3}, "action": "remove"}, http.StatusCreated)
	if first.Action != domain.SLAActionAdd || second.Action != domain.SLAActionRemove {
		t.Fatalf("rule actions = %q and %q", first.Action, second.Action)
	}
	rules := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).SLARules
	if len(rules) != 2 || rules[0].ID != first.ID || rules[1].ID != second.ID {
		t.Fatalf("new rules must be listed last, in creation order: %#v", rules)
	}

	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/sla-rules", map[string]any{"name": "Bad", "action": "pause"}, http.StatusBadRequest)
	requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/sla-rules/"+first.ID, map[string]any{"action": "pause"}, http.StatusBadRequest)

	reordered := requestJSON[[]domain.SLARule](t, handler, http.MethodPost, "/api/sla-rules/reorder", map[string]any{"ids": []string{second.ID, first.ID}}, http.StatusOK)
	if len(reordered) != 2 || reordered[0].ID != second.ID {
		t.Fatalf("reorder did not return the new order: %#v", reordered)
	}
	rules = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).SLARules
	if rules[0].ID != second.ID || rules[1].ID != first.ID {
		t.Fatalf("reorder was not saved: %#v", rules)
	}
	// Unknown, missing and repeated ids are refused and leave the order alone.
	for _, ids := range [][]string{{first.ID}, {first.ID, first.ID}, {first.ID, "missing"}} {
		requestJSON[map[string]any](t, handler, http.MethodPost, "/api/sla-rules/reorder", map[string]any{"ids": ids}, http.StatusBadRequest)
	}
	rules = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).SLARules
	if rules[0].ID != second.ID {
		t.Fatalf("a refused reorder changed the order: %#v", rules)
	}
}
