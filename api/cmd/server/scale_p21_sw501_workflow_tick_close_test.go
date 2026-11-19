package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// scaleP21SW501LegacyTick is startWorkflowScheduler's literal flag-off body
// for one owned workspace. TestScaleP21SW503WorkflowSchedulerDispatchGuard
// pins this copy to the production source.
func scaleP21SW501LegacyTick(ctx context.Context, service *server, key string, now time.Time) {
	data, ok := service.store.WorkspaceMetadataFields(key, "workflowDefinitions", "workflowRuns")
	if !ok {
		return
	}
	for _, definition := range data.WorkflowDefinitions {
		if definition.Enabled && definition.Trigger == "schedule" && definition.NextRunAt != nil && !definition.NextRunAt.After(now) {
			_, _ = service.runWorkflow(ctx, key, definition.ID, "schedule", "", "", true)
		}
	}
	for _, run := range data.WorkflowRuns {
		if run.Status == "failed" && run.NextRetryAt != nil && !run.NextRetryAt.After(now) {
			_, _ = service.runWorkflow(ctx, key, run.WorkflowID, "retry", run.ResourceType, run.ResourceID, true)
		}
	}
}

// The due scheduler at a weekday 10:00 UTC and at a Sunday 10:00 UTC: exact
// due boundaries, cron weekday rules, disabled/manual/duplicate definitions,
// missing and disabled retry targets, stale and superseded retries, and a
// failing scheduled run, through the literal loop and the bounded tick.
func TestScaleP21SW501WorkflowDueSchedulerWeekdayAndSunday(t *testing.T) {
	env := scaleP21SW500Setup(t)
	sunday := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	monday := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	if sunday.Weekday() != time.Sunday || monday.Weekday() != time.Monday || !monday.Before(time.Now()) {
		t.Fatal("fixed tick clocks must be a past Sunday and Monday")
	}
	id := func(name string) string { return env.prefix + name }
	weekday, sundayRule, daily, late := id("weekday"), id("sunday"), id("daily-exact"), id("late")
	disabled, manual, duplicate, retryTarget := id("disabled"), id("manual-next"), id("duplicate"), id("retry-target")
	retryDisabled, failing := id("retry-disabled"), id("failing")
	schedules := []string{"0 10 * * 1-5", "0 10 * * 0", "@daily", "@hourly", "@weekly"}
	aggregates := []string{weekday, sundayRule, daily, late, disabled, manual, duplicate, retryTarget, retryDisabled, failing, id("missing")}
	for _, tick := range []struct {
		name   string
		now    time.Time
		cancel bool
		order  []string
	}{
		// Duplicate definition IDs persist as one record keyed by ID (the
		// later, due entry), so both writers see and run it once.
		{"sunday-10:00Z", sunday, false, []string{sundayRule, daily, duplicate, failing, retryTarget}},
		{"monday-10:00Z", monday, false, []string{weekday, sundayRule, daily, duplicate, failing, retryTarget}},
		{"monday-canceled", monday, true, nil},
	} {
		t.Run(tick.name, func(t *testing.T) {
			now := tick.now
			seed := func(data *domain.Bootstrap) {
				at := func(value time.Time) *time.Time { return &value }
				definition := func(id, trigger, schedule string, enabled bool, next *time.Time, actions ...domain.WorkflowAction) domain.WorkflowDefinition {
					if len(actions) == 0 {
						actions = []domain.WorkflowAction{scaleP21SW500Action("notify")}
					}
					return domain.WorkflowDefinition{
						ID: id, Name: id, Trigger: trigger, Schedule: schedule, Enabled: enabled, NextRunAt: next,
						MaxAttempts: 8, CreatorID: env.viewer.ID, CreatedAt: env.fixtureClock, UpdatedAt: env.fixtureClock, Actions: actions,
					}
				}
				data.WorkflowDefinitions = []domain.WorkflowDefinition{
					definition(weekday, "schedule", "0 10 * * 1-5", true, at(monday)),
					definition(sundayRule, "schedule", "0 10 * * 0", true, at(sunday)),
					definition(daily, "schedule", "@daily", true, at(now)),
					definition(late, "schedule", "@hourly", true, at(now.Add(time.Nanosecond))),
					definition(disabled, "schedule", "@daily", false, at(now.Add(-time.Hour))),
					definition(manual, "manual", "", true, at(now.Add(-time.Hour))),
					definition(duplicate, "schedule", "@daily", true, at(now.Add(24*time.Hour))),
					definition(duplicate, "schedule", "@daily", true, at(now.Add(-time.Hour))),
					definition(retryTarget, "manual", "", true, nil,
						scaleP21SW500Action("setIssuePriority", "priority", "1"), scaleP21SW500Action("notify")),
					definition(retryDisabled, "manual", "", false, nil),
					definition(failing, "schedule", "@weekly", true, at(sunday.Add(-time.Hour)),
						scaleP21SW500Action("notify"), scaleP21SW500Action("assignIssue", "userId", "missing-user")),
				}
				data.WorkflowRuns = []domain.WorkflowRun{
					env.priorRun("retry-older", retryTarget, "failed", 1, at(now.Add(-time.Hour))),
					env.priorRun("retry-latest", retryTarget, "failed", 2, at(now)),
					env.priorRun("retry-disabled", retryDisabled, "failed", 1, at(now.Add(-time.Minute))),
					env.priorRun("retry-missing", id("missing"), "failed", 1, at(now.Add(-time.Minute))),
					env.priorRun("retry-not-due", weekday, "failed", 4, at(now.Add(time.Second))),
					env.priorRun("succeeded-with-retry", sundayRule, "succeeded", 1, at(now.Add(-time.Minute))),
					env.priorRun("retry-superseded", failing, "failed", 2, at(now.Add(-time.Minute))),
				}
			}
			expected := map[string]any{}
			for _, mode := range scaleP21SW500Modes {
				t.Run(mode, func(t *testing.T) {
					env.reset(t, scaleP21SW500Case{}, seed)
					bounded := strings.HasPrefix(mode, "bounded")
					source := env.source(t, mode)
					service := &server{store: source}
					result := env.outcome(t, source, scaleP21SW500Call{aggregates: aggregates, schedules: schedules, unowned: true,
						wrap: func(ctx context.Context) context.Context {
							if !tick.cancel {
								return ctx
							}
							canceled, cancel := context.WithCancel(ctx)
							cancel()
							return canceled
						},
						run: func(ctx context.Context) (domain.WorkflowRun, error) {
							if bounded {
								service.boundedWorkflowScheduleTick(ctx, now)
							} else {
								scaleP21SW501LegacyTick(ctx, service, env.workspace, now)
							}
							return domain.WorkflowRun{}, nil
						}})
					var order []string
					for _, run := range result.newRuns {
						order = append(order, run.WorkflowID)
					}
					if !slices.Equal(order, tick.order) || result.events != len(tick.order) {
						t.Fatal("due scheduler changed which workflows ran or their order", mode, order, result.events)
					}
					if tick.cancel && !result.unchanged {
						t.Fatal("canceled tick changed state", mode)
					}
					for _, run := range result.newRuns {
						want := "succeeded"
						if run.WorkflowID == failing {
							want = "failed"
							if run.NextRetryAt == nil || run.Attempt != 3 {
								t.Fatal("failed scheduled run lost its retry history", run.Attempt, run.NextRetryAt)
							}
						}
						if run.Status != want || run.WorkflowID == retryTarget && (run.Trigger != "retry" || run.Attempt != 3) {
							t.Fatal("scheduler run postimage changed", run)
						}
					}
					state := strings.TrimPrefix(strings.TrimPrefix(mode, "legacy-"), "bounded-")
					if !bounded {
						expected[state] = result.normalized
					} else if !reflect.DeepEqual(expected[state], result.normalized) {
						t.Fatal("bounded due scheduler changed rows, events or publication", mode)
					}
					t.Logf("tick=%s mode=%s runs=%d SQL=%d", tick.name, mode, len(result.newRuns), result.statements)
				})
			}
		})
	}
}

// Cancellation at every sampled SQL statement of a complete multi-action run
// leaves either the untouched preimage or exactly the literal postimage, and
// never leaks bounded work.
func TestScaleP21SW502WorkflowRunCancellationAtomicity(t *testing.T) {
	env := scaleP21SW500Setup(t)
	test := scaleP21SW500Case{name: "cancel", trigger: "retry", issue: true, actions: scaleP21SW500AllActions,
		seed: func(env *scaleP21SW500Env, data *domain.Bootstrap) {
			data.WorkflowRuns = []domain.WorkflowRun{env.priorRun("prior", env.workflowID, "failed", 2, &env.past)}
		}}
	success := map[string]any{}
	for _, mode := range scaleP21SW500Modes {
		t.Run(mode, func(t *testing.T) {
			execute := func(t *testing.T, cancelAt int) scaleP21SW500Result {
				t.Helper()
				env.reset(t, test, nil)
				source := env.source(t, mode)
				service := &server{store: source}
				cancel, seen := context.CancelFunc(func() {}), 0
				return env.outcome(t, source, scaleP21SW500Call{aggregates: []string{env.workflowID},
					wrap: func(ctx context.Context) context.Context {
						ctx, cancel = context.WithCancel(ctx)
						t.Cleanup(cancel)
						return ctx
					},
					observe: func() {
						seen++
						if seen == cancelAt {
							cancel()
						}
					},
					run: func(ctx context.Context) (domain.WorkflowRun, error) {
						return service.runWorkflow(ctx, env.workspace, env.workflowID, "retry", "issue", env.issue.ID, false)
					}})
			}
			complete := execute(t, 0)
			if complete.err != nil || complete.run.Status != "succeeded" {
				t.Fatal("uncanceled run must succeed", complete.err, complete.run.Status)
			}
			state := strings.TrimPrefix(strings.TrimPrefix(mode, "legacy-"), "bounded-")
			if strings.HasPrefix(mode, "legacy") {
				success[state] = complete.state
			} else if !reflect.DeepEqual(success[state], complete.state) {
				t.Fatal("uncanceled bounded run differs from literal state", mode)
			}
			total := complete.statements
			points := map[int]bool{}
			for _, point := range []int{1, 2, 3, 5, 8, total / 4, total / 2, 3 * total / 4, total - 2, total - 1, total} {
				if point >= 1 && point <= total {
					points[point] = true
				}
			}
			sorted := make([]int, 0, len(points))
			for point := range points {
				sorted = append(sorted, point)
			}
			slices.Sort(sorted)
			for _, point := range sorted {
				result := execute(t, point)
				switch {
				case result.unchanged && result.err != nil && result.published == 0:
				case !result.unchanged && reflect.DeepEqual(success[state], result.state):
				default:
					t.Fatalf("cancellation at SQL statement %d/%d left a partial state (mode=%s err=%v unchanged=%t published=%d)", point, total, mode, result.err, result.unchanged, result.published)
				}
				t.Logf("mode=%s cancel-at=%d/%d err=%v unchanged=%t", mode, point, total, result.err, result.unchanged)
			}
		})
	}
}

// The scheduler's bounded dispatch precedes its literal snapshot read, the
// test copy of the literal loop stays byte-identical, and runWorkflow keeps
// one callback for both writers.
func TestScaleP21SW503WorkflowSchedulerDispatchGuard(t *testing.T) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "automation_intake.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	print := func(node ast.Node) string {
		var out strings.Builder
		if err := printer.Fprint(&out, set, node); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	functions := map[string]*ast.FuncDecl{}
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok {
			functions[function.Name.Name] = function
		}
	}
	scheduler, runner := functions["startWorkflowScheduler"], functions["runWorkflow"]
	if scheduler == nil || runner == nil {
		t.Fatal("workflow scheduler or runner moved")
	}
	var literal, literalBody string
	var dispatch, literalLoop token.Pos
	dispatches := 0
	ast.Inspect(scheduler.Body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.IfStmt:
			if print(node.Cond) == "s.store.BoundedMetadataEnabled()" {
				dispatches++
				if len(node.Body.List) == 2 && print(node.Body.List[0]) == "s.boundedWorkflowScheduleTick(ctx, now)" && print(node.Body.List[1]) == "continue" {
					dispatch = node.Pos()
				}
			}
		case *ast.RangeStmt:
			if print(node.X) == "s.store.WorkspaceKeys()" {
				literalLoop, literalBody = node.Pos(), print(node.Body)
			}
		}
		return true
	})
	if dispatches != 1 || dispatch == token.NoPos || literalLoop == token.NoPos || dispatch > literalLoop {
		t.Fatal("bounded scheduler dispatch no longer precedes the literal snapshot loop")
	}
	copyFile, err := parser.ParseFile(set, "scale_p21_sw501_workflow_tick_close_test.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var copied string
	for _, declaration := range copyFile.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == "scaleP21SW501LegacyTick" {
			copied = print(function.Body)
		}
	}
	literal = strings.NewReplacer("s.store.", "service.store.", "s.runWorkflow(", "service.runWorkflow(", "continue", "return").Replace(literalBody)
	if literalBody == "" || strings.Join(strings.Fields(literal), " ") != strings.Join(strings.Fields(copied), " ") {
		t.Fatalf("SW501 literal tick copy drifted from startWorkflowScheduler\nsource: %s\ncopy: %s", literal, copied)
	}
	runText := print(runner.Body)
	for _, required := range []string{
		`err = s.store.MutateWorkflowRun(ctx, key, id, resourceType, resourceID, map[string]string{"trigger": trigger}, mutate)`,
		`err = s.store.MutateWorkspace(ctx, key, "workflow_run.executed", id, map[string]string{"trigger": trigger}, mutate)`,
	} {
		if strings.Count(runText, required) != 1 {
			t.Fatal("runWorkflow no longer shares one literal callback between its writers", required)
		}
	}
}
