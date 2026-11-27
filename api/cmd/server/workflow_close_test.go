package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// env is one owned workspace shared by the workflow close-out
// differentials: legacy MutateWorkspace against the bounded MutateWorkflowRun
// writer on warm and SQL-reloaded sources, with a peer replica for conflicts.
type workflowCloseEnv struct {
	database                       *scaleDatabase
	repo, peer, probe              *store.SQLiteStore
	workspace, prefix, workflowID  string
	issue                          domain.Issue
	baseline                       domain.Bootstrap
	saved                          []byte
	viewer, other                  domain.User
	sql                            notificationsFixture
	plain, grouped, archived       string
	archivedGroup, missingGroup    string
	projectLabel, activeGroup      string
	archivedGroupID, otherIssueID  string
	fixtureClock, past, futureTime time.Time
}

func workflowCloseSetup(t *testing.T) *workflowCloseEnv {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	_, database, issue := chatFixture(t)
	env := &workflowCloseEnv{database: database, repo: database.repo, workspace: database.key, issue: issue}
	baseline, found := env.repo.BootstrapFor(env.workspace)
	if !found {
		t.Fatal("owned workflow fixture missing")
	}
	env.baseline = baseline
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	env.saved = saved
	env.viewer = baseline.Viewer
	for _, user := range baseline.Users {
		if user.ID == env.viewer.ID {
			continue
		}
		if env.other.ID == "" {
			env.other = user
		}
	}
	if env.other.ID == "" {
		t.Fatal("workflow fixture needs a member besides the viewer", len(baseline.Users))
	}
	for _, item := range baseline.Issues {
		if item.ID != issue.ID {
			env.otherIssueID = item.ID
		}
	}
	if env.otherIssueID == "" {
		t.Fatal("workflow fixture needs an unrelated issue")
	}
	env.prefix = idgen.New("sw500-")
	env.workflowID = env.prefix + "workflow"
	env.plain, env.grouped, env.archived = env.prefix+"label-plain", env.prefix+"label-grouped", env.prefix+"label-archived"
	env.archivedGroup, env.missingGroup = env.prefix+"label-archived-group", env.prefix+"label-missing-group"
	env.projectLabel, env.activeGroup, env.archivedGroupID = env.prefix+"label-project", env.prefix+"group-active", env.prefix+"group-archived"
	env.fixtureClock = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	env.past = env.fixtureClock
	env.futureTime = time.Now().UTC().Add(24 * time.Hour)
	env.sql = notificationsFixture{reader: database}
	env.sql.data.Workspace.URLKey = env.workspace
	peer, err := store.OpenDatabase(database.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	env.peer = peer
	// One bounded view refreshed per mode: fresh views per case would retain
	// their logical sources and exhaust the process work budget.
	probe, err := env.repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), env.workspace)
	if err != nil {
		t.Fatal(err)
	}
	env.probe = probe
	return env
}

var workflowCloseModes = []string{"legacy-warm", "bounded-warm", "legacy-reloaded", "bounded-reloaded"}

type workflowCloseCase struct {
	name                     string
	trigger                  string
	dueOnly                  bool
	issue                    bool
	resourceType, resourceID string
	defTrigger, schedule     string
	disabled                 bool
	maxAttempts              int
	actions                  func(env *workflowCloseEnv) []domain.WorkflowAction
	seed                     func(env *workflowCloseEnv, data *domain.Bootstrap)
	peer                     func(env *workflowCloseEnv, data *domain.Bootstrap)
	former                   func(env *workflowCloseEnv) string
	want                     string
	// pinned: the bounded writer refuses atomically where the literal writer
	// succeeds (shared aggregate-envelope admission); not certified behavior.
	pinned bool
	// full: the bounded point writer hands this run to the full-image
	// writer (errWorkflowRunFullImage); every other run must stay point.
	full bool
}

type workflowCloseResult struct {
	normalized, state any
	run               domain.WorkflowRun
	err               error
	events            int
	statements        int
	unchanged         bool
	published         int
	newRuns           []domain.WorkflowRun
	// tables: every row of the issue side tables; full: the
	// full-image attempts the call made.
	tables map[string][]string
	full   int64
}

type workflowCloseCall struct {
	aggregates []string
	schedules  []string
	// unowned runs the call without an outer metadata output owner, as the
	// production ticker does (it opens one per due item).
	unowned bool
	// peer: another server committed after the writer's image was installed.
	// The literal writer keeps its stale copy at an unknown revision (the
	// next check reloads it), while the bounded writer reloads its image
	// from SQL after the commit: the installed fields are compared with the
	// SQL reload (workflowPointInstalled) instead of with the literal copy.
	peer    bool
	wrap    func(context.Context) context.Context
	observe func()
	run     func(context.Context) (domain.WorkflowRun, error)
}

func errorClass(err error) string {
	switch {
	case err == nil:
		return "succeeded"
	case errors.Is(err, errWorkflowNotDue):
		return "not-due"
	case errors.Is(err, errNotFound):
		return "not-found"
	case errors.Is(err, errInvalid):
		return "invalid"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	return "error: " + err.Error()
}

func (env *workflowCloseEnv) labels(data *domain.Bootstrap) {
	label := func(id, group, resource string, archived bool) domain.IssueLabel {
		item := domain.IssueLabel{ID: id, Name: id, Color: "#123456", GroupID: group, ResourceType: resource, CreatorID: env.viewer.ID, CreatedAt: env.fixtureClock}
		if archived {
			at := env.fixtureClock
			item.ArchivedAt = &at
		}
		return item
	}
	data.Labels = append(data.Labels,
		label(env.plain, "", "", false), label(env.grouped, env.activeGroup, "", false), label(env.archived, "", "", true),
		label(env.archivedGroup, env.archivedGroupID, "", false), label(env.missingGroup, env.prefix+"group-missing", "", false),
		label(env.projectLabel, "", "project", false))
	archivedAt := env.fixtureClock
	data.LabelGroups = append(data.LabelGroups,
		domain.LabelGroup{ID: env.activeGroup, Name: "Active group", Color: "#654321", ResourceType: "issue", CreatedAt: env.fixtureClock},
		domain.LabelGroup{ID: env.archivedGroupID, Name: "Archived group", Color: "#654321", ResourceType: "issue", CreatedAt: env.fixtureClock, ArchivedAt: &archivedAt})
}

// reset installs the literal fixture plus the case's definition through an
// ordinary full write, so every mode starts from identical SQL rows.
func (env *workflowCloseEnv) reset(t *testing.T, test workflowCloseCase, seed func(*domain.Bootstrap)) {
	t.Helper()
	env.repo.SetWebhookSink(nil)
	env.repo.SetRealtimeSink(nil)
	restore := store.ForceFullMutationsForTesting(true)
	err := env.repo.MutateWorkspace(t.Context(), env.workspace, "scale_p21.sw500_fixture", "", nil, func(data *domain.Bootstrap) error {
		var original domain.Bootstrap
		if err := json.Unmarshal(env.saved, &original); err != nil {
			return err
		}
		*data = original
		env.labels(data)
		for index := range data.Issues {
			if data.Issues[index].ID == env.issue.ID {
				data.Issues[index].Priority, data.Issues[index].PriorityLabel = 4, priorityLabel(4)
			}
		}
		data.WorkflowRuns = []domain.WorkflowRun{}
		data.WorkflowDefinitions = nil
		if seed != nil {
			seed(data)
			return nil
		}
		definition := domain.WorkflowDefinition{
			ID: env.workflowID, Name: "SW500 workflow", Trigger: firstNonEmpty(test.defTrigger, "manual"), Schedule: test.schedule,
			Enabled: !test.disabled, MaxAttempts: test.maxAttempts, CreatorID: env.viewer.ID,
			CreatedAt: env.fixtureClock, UpdatedAt: env.fixtureClock,
		}
		if test.maxAttempts == 0 {
			definition.MaxAttempts = 8
		}
		if test.maxAttempts < 0 {
			definition.MaxAttempts = 0
		}
		if definition.Trigger == "schedule" {
			definition.NextRunAt = &env.past
		}
		if test.actions != nil {
			definition.Actions = test.actions(env)
		} else {
			definition.Actions = []domain.WorkflowAction{{Type: "notify", Config: map[string]string{}}}
		}
		data.WorkflowDefinitions = []domain.WorkflowDefinition{definition}
		if test.seed != nil {
			test.seed(env, data)
		}
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	metadataReloadSourceExec(t, env.database, `UPDATE issue_number_sequences SET last_number=? WHERE workspace_key=?`, env.baseline.NextIssueNumber-1, env.workspace)
	// A server's copy is exact at its stored revision once loaded, and a
	// peer's commit then provably moves the stored revision past it. A
	// fixture write on a copy left at an unknown revision (a previous peer
	// race, or none loaded yet) stays unknown: peer cases load it, so their
	// warm images start exact whatever ran before (a current copy is kept).
	if test.peer != nil {
		if err := env.repo.ReloadWorkspace(t.Context(), env.workspace); err != nil {
			t.Fatal(err)
		}
	}
}

// source returns the mode's writer. Reloaded modes advance the metadata
// revision so the writer installs its image through its actual SQL loader;
// each bounded result is compared with the literal writer in the same state.
func (env *workflowCloseEnv) source(t *testing.T, mode string) *store.SQLiteStore {
	t.Helper()
	reloaded := strings.HasSuffix(mode, "reloaded")
	if reloaded {
		metadataReloadSourceReloadSource(t, env.database, env.repo)
	}
	if strings.HasPrefix(mode, "legacy") {
		return env.repo
	}
	if err := env.probe.RefreshInstalledMetadataViewForTesting(t.Context(), env.repo, env.workspace); err != nil {
		t.Fatal(err)
	}
	if reloaded {
		metadataReloadSourceReloadSource(t, env.database, env.probe)
	}
	return env.probe
}

// peerWrite is another server's ordinary write after both sources installed
// their images: the stale writer must merge or refresh and retry.
func (env *workflowCloseEnv) peerWrite(t *testing.T, edit func(*workflowCloseEnv, *domain.Bootstrap)) {
	t.Helper()
	if err := env.peer.ForgetInstalledMetadataRevisionForTesting(env.workspace); err != nil {
		t.Fatal(err)
	}
	if err := env.peer.ReloadWorkspace(t.Context(), env.workspace); err != nil {
		t.Fatal(err)
	}
	if err := env.peer.MutateWorkspace(t.Context(), env.workspace, "scale_p21.sw500_peer", "", nil, func(data *domain.Bootstrap) error {
		edit(env, data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func (env *workflowCloseEnv) setMembership(t *testing.T, user, status string) {
	t.Helper()
	metadataReloadSourceExec(t, env.database, `UPDATE workspace_memberships SET status=? WHERE workspace_id=? AND user_id=?`, status, env.database.workspaceID, user)
}

// nextRuns maps a recomputed schedule to its rule: the writer
// computes it from its own completion clock inside [started, finished].
func nextRuns(value any, schedules []string, started, finished time.Time) any {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if text, ok := child.(string); ok && strings.EqualFold(key, "nextRunAt") {
				if clock, err := time.Parse(time.RFC3339Nano, text); err == nil {
					for _, schedule := range schedules {
						low, lowErr := nextWorkflowSchedule(schedule, started)
						high, highErr := nextWorkflowSchedule(schedule, finished)
						if lowErr == nil && highErr == nil && !clock.Before(low) && !clock.After(high) {
							item[key] = "<next-run:" + schedule + ">"
							break
						}
					}
				}
				continue
			}
			item[key] = nextRuns(child, schedules, started, finished)
		}
	case []any:
		for index, child := range item {
			item[index] = nextRuns(child, schedules, started, finished)
		}
	}
	return value
}

func workflowCloseReplace(value any, ids map[string]string) any {
	switch item := value.(type) {
	case string:
		if replacement, ok := ids[item]; ok {
			return replacement
		}
	case map[string]any:
		for key, child := range item {
			item[key] = workflowCloseReplace(child, ids)
		}
	case []any:
		for index, child := range item {
			item[index] = workflowCloseReplace(child, ids)
		}
	}
	return value
}

func (env *workflowCloseEnv) workflowRuns(t *testing.T) []domain.WorkflowRun {
	t.Helper()
	rows := env.database.query(t, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='workflowRuns' ORDER BY collection_order,record_key`, env.workspace)
	defer rows.Close()
	var runs []domain.WorkflowRun
	for rows.Next() {
		var raw []byte
		var run domain.WorkflowRun
		if err := rows.Scan(&raw); err != nil || json.Unmarshal(raw, &run) != nil {
			t.Fatal("canonical workflow run", err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return runs
}

func (env *workflowCloseEnv) outcome(t *testing.T, source *store.SQLiteStore, call workflowCloseCall) workflowCloseResult {
	t.Helper()
	before := discussionWriteSql(t, env.sql)
	beforeMetadata, err := json.Marshal(workflowWriteMetadata(t, env.database))
	if err != nil {
		t.Fatal(err)
	}
	seeded := map[string]bool{}
	for _, run := range env.workflowRuns(t) {
		seeded[run.ID] = true
	}
	priorIDs := map[string]bool{}
	for _, aggregate := range call.aggregates {
		prior, err := env.repo.Events(t.Context(), aggregate)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range prior {
			priorIDs[event.ID] = true
		}
	}
	got := discussionWriteOutcome{Header: make(http.Header)}
	source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		got.Webhooks = append(got.Webhooks, discussionRoutesEnvelope(t, event))
	})
	captureRealtime := func(_ string, event domain.RealtimeEvent) {
		got.Realtime = append(got.Realtime, discussionRoutesEnvelope(t, event))
	}
	source.SetRealtimeSink(captureRealtime)
	if source != env.repo {
		env.repo.SetRealtimeSink(captureRealtime)
	}
	result := workflowCloseResult{}
	fallbacks := store.WorkflowRunFullImageAttemptsForTesting()
	started := time.Now().UTC()
	func() {
		ctx := t.Context()
		if !call.unowned {
			var closeOutput func()
			ctx, closeOutput = beginMetadataOutputReads(ctx)
			defer closeOutput()
		}
		ctx = store.WithSQLStatementObserver(ctx, func(string) {
			result.statements++
			if call.observe != nil {
				call.observe()
			}
		})
		if call.wrap != nil {
			ctx = call.wrap(ctx)
		}
		result.run, result.err = call.run(ctx)
	}()
	finished := time.Now().UTC()
	result.full = store.WorkflowRunFullImageAttemptsForTesting() - fallbacks
	result.tables = workflowPointRows(t, env.database, started, finished)
	source.SetWebhookSink(nil)
	source.SetRealtimeSink(nil)
	env.repo.SetRealtimeSink(nil)
	if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
		t.Fatal("workflow writer leaked work or hydrated full metadata", source.MetadataWorkUsedForTesting(), source.WorkspaceMetadataDecodes())
	}
	got.Rows = discussionWriteSql(t, env.sql)
	for _, aggregate := range call.aggregates {
		events, err := env.repo.Events(t.Context(), aggregate)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if !priorIDs[event.ID] {
				got.Events = append(got.Events, event)
			}
		}
	}
	result.events = len(got.Events)
	result.published = len(got.Webhooks) + len(got.Realtime)
	metadata := workflowWriteMetadata(t, env.database)
	afterMetadata, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	result.unchanged = reflect.DeepEqual(before, got.Rows) && string(beforeMetadata) == string(afterMetadata) && len(got.Events) == 0
	// Generated run IDs are positional in the canonical history.
	ids := map[string]string{}
	for _, run := range env.workflowRuns(t) {
		if !seeded[run.ID] {
			ids[run.ID] = fmt.Sprintf("<generated-run:%d>", len(result.newRuns))
			result.newRuns = append(result.newRuns, run)
		}
	}
	normalize := func(value discussionWriteOutcome, response any) any {
		value.Response, err = json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		normalized := workflowWriteNormalize(t, discussionWriteNormalized(t, value, before, started, finished), result.run.ID, started, finished)
		return workflowCloseReplace(nextRuns(normalized, call.schedules, started, finished), ids)
	}
	// The installed workflow fields: the literal writer's snapshot, the
	// bounded writer's installed image.
	var installed any
	if !call.peer {
		if err := json.Unmarshal(scaleWriterInstalled(t, env.workspace, source, []string{"workflowDefinitions", "workflowRuns"}), &installed); err != nil {
			t.Fatal(err)
		}
	}
	result.normalized = normalize(got, map[string]any{"run": result.run, "error": errorClass(result.err), "metadata": metadata, "installed": installed})
	result.state = normalize(discussionWriteOutcome{Header: make(http.Header), Rows: got.Rows, Events: got.Events}, metadata)
	return result
}

func (env *workflowCloseEnv) runCase(t *testing.T, test workflowCloseCase) {
	t.Helper()
	expected := map[string]any{}
	tables := map[string]map[string][]string{}
	for _, mode := range workflowCloseModes {
		t.Run(mode, func(t *testing.T) {
			env.reset(t, test, nil)
			if test.former != nil {
				user := test.former(env)
				env.setMembership(t, user, "suspended")
				defer env.setMembership(t, user, "active")
			}
			source := env.source(t, mode)
			if test.peer != nil {
				env.peerWrite(t, test.peer)
			}
			resourceType, resourceID := test.resourceType, test.resourceID
			if test.issue {
				resourceType, resourceID = "issue", env.issue.ID
			}
			service := &server{store: source}
			result := env.outcome(t, source, workflowCloseCall{aggregates: []string{env.workflowID}, schedules: []string{test.schedule}, peer: test.peer != nil,
				run: func(ctx context.Context) (domain.WorkflowRun, error) {
					return service.runWorkflow(ctx, env.workspace, env.workflowID, firstNonEmpty(test.trigger, "manual"), resourceType, resourceID, test.dueOnly)
				}})
			class := errorClass(result.err)
			if class == "succeeded" && result.run.Status != "succeeded" {
				class = result.run.Status
			}
			if test.pinned && strings.HasPrefix(mode, "bounded") {
				if !errors.Is(result.err, store.ErrMetadataWorkBudget) || !result.unchanged || result.published != 0 {
					t.Fatal("pinned aggregate-envelope refusal is no longer atomic", mode, result.err, result.unchanged, result.published)
				}
				t.Logf("workflow case=%s mode=%s pinned refusal=%v SQL=%d", test.name, mode, result.err, result.statements)
				return
			}
			if test.want != "" && class != test.want {
				t.Fatalf("workflow case %s/%s: got %s (run error %q), want %s", test.name, mode, class, result.run.Error, test.want)
			}
			if result.err == nil && result.events != 1 || result.err != nil && result.events != 0 {
				t.Fatal("workflow lost its atomic committed event", result.err, result.events)
			}
			state := strings.TrimPrefix(strings.TrimPrefix(mode, "legacy-"), "bounded-")
			if strings.HasPrefix(mode, "legacy") {
				expected[state] = result.normalized
				tables[state] = result.tables
			} else {
				if !reflect.DeepEqual(expected[state], result.normalized) {
					want, _ := json.Marshal(expected[state])
					got, _ := json.Marshal(result.normalized)
					t.Fatalf("workflow response/rows/events/publication differ from literal (%s/%s)\nlegacy:  %.4000s\nbounded: %.4000s", test.name, mode, workflowCloseFirstDifference(want, got), workflowCloseFirstDifference(got, want))
				}
				if difference := workflowPointEqual(tables[state], result.tables); difference != "" {
					t.Fatalf("workflow issue side tables differ from literal (%s/%s): %s", test.name, mode, difference)
				}
				wantFull := test.full
				if full := result.full != 0; full != wantFull {
					t.Fatalf("workflow case %s/%s: full-image attempts %d, want full=%v", test.name, mode, result.full, wantFull)
				}
				// A write that lost a race to a peer leaves an installed
				// image equal to the SQL reload too. A retry refused after
				// its conflict refresh (not due) installs only that refresh,
				// which these peers' edits are confined to.
				if result.err == nil || test.peer != nil {
					workflowPointInstalled(t, env, source)
				}
			}
			t.Logf("workflow case=%s mode=%s result=%s SQL=%d full=%d", test.name, mode, class, result.statements, result.full)
		})
	}
}

// workflowCloseFirstDifference returns a's tail from shortly before its first
// byte that differs from b, to keep failure messages readable.
func workflowCloseFirstDifference(a, b []byte) []byte {
	index := 0
	for index < len(a) && index < len(b) && a[index] == b[index] {
		index++
	}
	return a[max(0, index-600):]
}

func workflowCloseActions(actions ...domain.WorkflowAction) func(*workflowCloseEnv) []domain.WorkflowAction {
	return func(*workflowCloseEnv) []domain.WorkflowAction { return actions }
}

func workflowCloseAction(kind string, config ...string) domain.WorkflowAction {
	values := map[string]string{}
	for index := 0; index+1 < len(config); index += 2 {
		values[config[index]] = config[index+1]
	}
	return domain.WorkflowAction{Type: kind, Config: values}
}

func (env *workflowCloseEnv) priorRun(id, workflow, status string, attempt int, retry *time.Time) domain.WorkflowRun {
	return domain.WorkflowRun{
		ID: env.prefix + id, WorkflowID: workflow, Trigger: "manual", ResourceType: "issue", ResourceID: env.issue.ID,
		Status: status, Attempt: attempt, Output: map[string]string{}, ScheduledAt: env.fixtureClock, StartedAt: env.fixtureClock, NextRetryAt: retry,
	}
}

func (env *workflowCloseEnv) definition(data *domain.Bootstrap) *domain.WorkflowDefinition {
	for index := range data.WorkflowDefinitions {
		if data.WorkflowDefinitions[index].ID == env.workflowID {
			return &data.WorkflowDefinitions[index]
		}
	}
	return nil
}

func (env *workflowCloseEnv) target(data *domain.Bootstrap) *domain.Issue {
	for index := range data.Issues {
		if data.Issues[index].ID == env.issue.ID {
			return &data.Issues[index]
		}
	}
	return nil
}

func allActions(env *workflowCloseEnv) []domain.WorkflowAction {
	return []domain.WorkflowAction{
		workflowCloseAction("setIssuePriority", "priority", "2"),
		workflowCloseAction("assignIssue", "userId", env.other.ID),
		workflowCloseAction("addIssueLabel", "labelId", env.grouped),
		workflowCloseAction("addIssueLabel", "labelId", env.plain),
		workflowCloseAction("notify", "recipientId", env.other.ID),
		workflowCloseAction("notify"),
		workflowCloseAction("createIssue", "teamId", env.issue.Team.ID, "title", "  SW500 created  "),
	}
}

// projectDrift adds projects whose stored IssueCount has drifted
// (too high, too low, and for an unused project), an active and an archived
// issue naming one of them.
func projectDrift(env *workflowCloseEnv, data *domain.Bootstrap) {
	project := func(suffix string, count int) domain.Project {
		return domain.Project{ID: env.prefix + "project-" + suffix, Name: "SW500 " + suffix, SlugID: "sw500-" + suffix, Color: "#123456",
			IssueCount: count, CreatedAt: env.fixtureClock, UpdatedAt: env.fixtureClock}
	}
	data.Projects = append(data.Projects, project("high", 7), project("low", 0), project("unused", 3))
	summary := func(suffix string) *domain.ProjectSummary {
		return &domain.ProjectSummary{ID: env.prefix + "project-" + suffix, Name: "SW500 " + suffix, Color: "#123456"}
	}
	used := 0
	for index := range data.Issues {
		if data.Issues[index].ID == env.issue.ID {
			continue
		}
		switch used {
		case 0, 1:
			data.Issues[index].Project = summary("low")
		case 2:
			data.Issues[index].Project = summary("high")
			at := env.fixtureClock
			data.Issues[index].ArchivedAt = &at
		}
		if used++; used > 2 {
			break
		}
	}
}

func workflowCloseCases() []workflowCloseCase {
	notify := workflowCloseActions(workflowCloseAction("notify"))
	large := strings.Repeat("x", 4<<20)
	cases := []workflowCloseCase{
		// Every action type, success postimages.
		{name: "notify-default-creator", actions: notify, want: "succeeded"},
		{name: "notify-explicit-recipient", issue: true, actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("notify", "recipientId", env.other.ID)}
		}, want: "succeeded"},
		{name: "assign-issue", issue: true, actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("assignIssue", "userId", env.other.ID)}
		}, want: "succeeded"},
		{name: "assign-issue-same-assignee", issue: true, actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("assignIssue", "userId", env.other.ID)}
		}, seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			user := env.other
			env.target(data).Assignee = &user
		}, want: "succeeded"},
		{name: "priority-none", issue: true, actions: workflowCloseActions(workflowCloseAction("setIssuePriority", "priority", "0")), want: "succeeded"},
		{name: "priority-unchanged", issue: true, actions: workflowCloseActions(workflowCloseAction("setIssuePriority", "priority", "4")), want: "succeeded"},
		{name: "label-plain", issue: true, actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("addIssueLabel", "labelId", env.plain)}
		}, want: "succeeded"},
		{name: "label-already-present", issue: true, actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("addIssueLabel", "labelId", env.plain)}
		}, seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			target := env.target(data)
			target.Labels = append(target.Labels, data.Labels[len(data.Labels)-6])
		}, want: "succeeded"},
		{name: "label-active-group", issue: true, actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("addIssueLabel", "labelId", env.grouped)}
		}, want: "succeeded"},
		{name: "create-issue", actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("createIssue", "teamId", env.issue.Team.ID, "title", "  SW500 created  ")}
		}, want: "succeeded"},
		{name: "create-then-assign-detached", issue: true, actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{
				workflowCloseAction("createIssue", "teamId", env.issue.Team.ID, "title", "SW500 created"),
				workflowCloseAction("assignIssue", "userId", env.other.ID),
				workflowCloseAction("addIssueLabel", "labelId", env.plain),
			}
		}, want: "succeeded"},
		{name: "all-actions", issue: true, actions: allActions, want: "succeeded"},
		// Projects' derived IssueCount: a run recounts every project from the
		// active issues naming it, rewriting only the rows that drifted.
		{name: "project-count-drift", issue: true, actions: allActions, seed: projectDrift, want: "succeeded"},
		{name: "project-count-drift-create", actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("createIssue", "teamId", env.issue.Team.ID, "title", "SW500 created")}
		}, seed: projectDrift, want: "succeeded"},
		{name: "project-count-exact", actions: notify, seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			projectDrift(env, data)
			for index := range data.Projects {
				data.Projects[index].IssueCount = 0
				for _, issue := range data.Issues {
					if issue.ArchivedAt == nil && issue.Project != nil && issue.Project.ID == data.Projects[index].ID {
						data.Projects[index].IssueCount++
					}
				}
			}
		}, want: "succeeded"},
		{name: "unknown-action-ignored", issue: true, actions: workflowCloseActions(workflowCloseAction("webhook", "url", "https://example.test"), workflowCloseAction("notify")), want: "succeeded"},
		{name: "no-actions", issue: true, actions: workflowCloseActions(), want: "succeeded"},

		// Every action's failure postimage: earlier actions persist.
		{name: "notify-missing-recipient", issue: true, actions: workflowCloseActions(
			workflowCloseAction("setIssuePriority", "priority", "1"), workflowCloseAction("notify", "recipientId", "missing-user")), want: "failed"},
		{name: "notify-former-member", issue: true, actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("setIssuePriority", "priority", "1"), workflowCloseAction("notify", "recipientId", env.other.ID)}
		}, former: func(env *workflowCloseEnv) string { return env.other.ID }, want: "failed"},
		{name: "notify-former-creator", issue: true, actions: notify, seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			env.definition(data).CreatorID = env.other.ID
		}, former: func(env *workflowCloseEnv) string { return env.other.ID }, want: "failed"},
		{name: "assign-missing-user", issue: true, actions: workflowCloseActions(workflowCloseAction("assignIssue", "userId", "missing-user")), want: "failed"},
		{name: "assign-former-member", issue: true, actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("notify"), workflowCloseAction("assignIssue", "userId", env.other.ID)}
		}, former: func(env *workflowCloseEnv) string { return env.other.ID }, want: "failed"},
		{name: "assign-without-issue", actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("notify"), workflowCloseAction("assignIssue", "userId", env.other.ID)}
		}, want: "failed"},
		{name: "assign-missing-issue", resourceType: "issue", resourceID: "missing-issue", actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("assignIssue", "userId", env.other.ID)}
		}, want: "failed"},
		{name: "assign-project-resource", resourceType: "project", resourceID: "any", actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("assignIssue", "userId", env.other.ID)}
		}, want: "failed"},
		{name: "priority-out-of-range", issue: true, actions: workflowCloseActions(workflowCloseAction("notify"), workflowCloseAction("setIssuePriority", "priority", "5")), want: "failed"},
		{name: "priority-not-number", issue: true, actions: workflowCloseActions(workflowCloseAction("setIssuePriority", "priority", "high")), want: "failed"},
		{name: "priority-without-issue", actions: workflowCloseActions(workflowCloseAction("setIssuePriority", "priority", "1")), want: "failed"},
		{name: "label-missing", issue: true, actions: workflowCloseActions(workflowCloseAction("addIssueLabel", "labelId", "missing-label")), want: "failed"},
		{name: "label-archived", issue: true, actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("setIssuePriority", "priority", "3"), workflowCloseAction("addIssueLabel", "labelId", env.archived)}
		}, want: "failed"},
		{name: "label-archived-group", issue: true, actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("addIssueLabel", "labelId", env.archivedGroup)}
		}, want: "failed"},
		{name: "label-missing-group", issue: true, actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("addIssueLabel", "labelId", env.missingGroup)}
		}, want: "failed"},
		{name: "label-project-resource", issue: true, actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("addIssueLabel", "labelId", env.projectLabel)}
		}, want: "failed"},
		{name: "label-without-issue", actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("addIssueLabel", "labelId", env.plain)}
		}, want: "failed"},
		{name: "create-missing-team", actions: workflowCloseActions(workflowCloseAction("notify"), workflowCloseAction("createIssue", "teamId", "missing-team", "title", "x")), want: "failed"},
		{name: "create-blank-title", actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("createIssue", "teamId", env.issue.Team.ID, "title", "   ")}
		}, want: "failed"},
		{name: "create-no-default-state", actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("createIssue", "teamId", env.issue.Team.ID, "title", "x")}
		}, seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			settings := teamSettings(data, env.issue.Team.ID)
			settings.DefaultStateID = "missing-state"
			data.TeamSettings[env.issue.Team.ID] = settings
		}, want: "failed"},

		// Retry history and failure bookkeeping.
		{name: "failure-schedules-backoff", trigger: "retry", dueOnly: true, issue: true, actions: workflowCloseActions(workflowCloseAction("assignIssue", "userId", "missing-user")),
			seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				data.WorkflowRuns = []domain.WorkflowRun{
					env.priorRun("old", env.workflowID, "failed", 1, nil), env.priorRun("other", "other-workflow", "failed", 9, &env.past),
					env.priorRun("due", env.workflowID, "failed", 5, &env.past), env.priorRun("later-other", "other-workflow", "succeeded", 1, nil),
				}
			}, want: "failed"},
		{name: "failure-at-max-attempts", trigger: "retry", dueOnly: true, issue: true, maxAttempts: 3, actions: workflowCloseActions(workflowCloseAction("assignIssue", "userId", "missing-user")),
			seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				data.WorkflowRuns = []domain.WorkflowRun{env.priorRun("due", env.workflowID, "failed", 2, &env.past)}
			}, want: "failed"},
		{name: "failure-zero-max-attempts", maxAttempts: -1, issue: true, actions: workflowCloseActions(workflowCloseAction("assignIssue", "userId", "missing-user")), want: "failed"},
		{name: "failure-capped-backoff", trigger: "retry", issue: true, maxAttempts: 20, actions: workflowCloseActions(workflowCloseAction("setIssuePriority", "priority", "9")),
			seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				data.WorkflowRuns = []domain.WorkflowRun{env.priorRun("due", env.workflowID, "failed", 9, &env.futureTime)}
			}, want: "failed"},
		{name: "manual-retry-not-yet-due", trigger: "retry", issue: true, actions: notify,
			seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				data.WorkflowRuns = []domain.WorkflowRun{env.priorRun("future", env.workflowID, "failed", 3, &env.futureTime)}
			}, want: "succeeded"},
		{name: "retry-after-success", trigger: "retry", issue: true, actions: notify,
			seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				data.WorkflowRuns = []domain.WorkflowRun{env.priorRun("done", env.workflowID, "succeeded", 2, &env.past)}
			}, want: "succeeded"},
		{name: "due-retry-latest-succeeded", trigger: "retry", dueOnly: true, issue: true, actions: notify,
			seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				data.WorkflowRuns = []domain.WorkflowRun{env.priorRun("due", env.workflowID, "failed", 1, &env.past), env.priorRun("done", env.workflowID, "succeeded", 2, nil)}
			}, want: "not-due"},
		{name: "duplicate-run-ids", trigger: "retry", dueOnly: true, issue: true, actions: notify,
			seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				data.WorkflowRuns = []domain.WorkflowRun{env.priorRun("dup", env.workflowID, "failed", 1, &env.past), env.priorRun("dup", env.workflowID, "failed", 2, &env.past)}
			}, want: "succeeded"},
		{name: "tail-500-predecessor-dropped", trigger: "retry", dueOnly: true, issue: true, actions: notify,
			seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				data.WorkflowRuns = []domain.WorkflowRun{env.priorRun("due", env.workflowID, "failed", 7, &env.past)}
				for index := range 499 {
					data.WorkflowRuns = append(data.WorkflowRuns, env.priorRun(fmt.Sprintf("tail-%03d", index), "other-workflow", "succeeded", 1, nil))
				}
			}, want: "succeeded"},
		{name: "tail-499-kept", issue: true, actions: notify,
			seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				for index := range 499 {
					data.WorkflowRuns = append(data.WorkflowRuns, env.priorRun(fmt.Sprintf("tail-%03d", index), "other-workflow", "succeeded", 1, nil))
				}
			}, want: "succeeded"},
		{name: "consecutive-errors-reset", issue: true, actions: notify, seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			definition := env.definition(data)
			definition.ConsecutiveErr, definition.LastRunStatus, definition.LastRunAt = 3, "failed", &env.past
		}, want: "succeeded"},
		{name: "consecutive-errors-increment", issue: true, actions: workflowCloseActions(workflowCloseAction("setIssuePriority", "priority", "-1")), seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			definition := env.definition(data)
			definition.ConsecutiveErr, definition.LastRunStatus = 3, "failed"
		}, want: "failed"},

		// Triggers.
		{name: "manual-runs-disabled", disabled: true, issue: true, actions: notify, want: "succeeded"},
		{name: "retry-disabled-refused", trigger: "retry", disabled: true, issue: true, actions: notify, want: "invalid"},
		{name: "schedule-trigger-disabled-refused", trigger: "schedule", dueOnly: true, defTrigger: "schedule", schedule: "@hourly", disabled: true, actions: notify, want: "invalid"},
		{name: "missing-definition", trigger: "schedule", dueOnly: true, actions: notify, seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			data.WorkflowDefinitions = nil
		}, want: "not-found"},
		{name: "issue-created-definition-run-manually", defTrigger: "issueCreated", issue: true, actions: workflowCloseActions(workflowCloseAction("setIssuePriority", "priority", "1")),
			seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				definition := env.definition(data)
				definition.TeamID, definition.Conditions = env.issue.Team.ID, map[string]string{"priority": "2", "titleContains": "never"}
			}, want: "succeeded"},
		{name: "schedule-definition-run-manually", defTrigger: "schedule", schedule: "0 10 * * 1-5", actions: notify, seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			env.definition(data).NextRunAt = &env.futureTime
		}, want: "succeeded"},
		{name: "schedule-future-not-due", trigger: "schedule", dueOnly: true, defTrigger: "schedule", schedule: "@daily", actions: notify, seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			env.definition(data).NextRunAt = &env.futureTime
		}, want: "not-due"},
		{name: "schedule-nil-next-not-due", trigger: "schedule", dueOnly: true, defTrigger: "schedule", schedule: "@daily", actions: notify, seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			env.definition(data).NextRunAt = nil
		}, want: "not-due"},
		{name: "schedule-failure-retry-and-next", trigger: "schedule", dueOnly: true, defTrigger: "schedule", schedule: "0 10 * * 0", issue: true,
			actions: workflowCloseActions(workflowCloseAction("notify"), workflowCloseAction("createIssue", "teamId", "missing-team", "title", "x")), want: "failed"},

		// Large legal rows.
		{name: "large-issue-all-actions", issue: true, actions: allActions, seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			env.target(data).Description = large
		}, want: "succeeded"},
		{name: "large-unrelated-issue-create", actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("createIssue", "teamId", env.issue.Team.ID, "title", "SW500 created"), workflowCloseAction("notify")}
		}, seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			for index := range data.Issues {
				if data.Issues[index].ID == env.otherIssueID {
					data.Issues[index].Description = large
				}
			}
		}, want: "succeeded"},
		{name: "large-definition-row", issue: true, actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("notify", "note", strings.Repeat("d", 3<<20)), workflowCloseAction("setIssuePriority", "priority", "1")}
		}, want: "succeeded"},
		// The workflow event's aggregate is the definition. The shared
		// aggregate plan charges sixteen copies of both definition images, so
		// a definition over about 4 MiB exceeds the 256 MiB work ceiling.
		{name: "definition-over-aggregate-envelope", issue: true, actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("notify", "note", large), workflowCloseAction("setIssuePriority", "priority", "1")}
		}, want: "succeeded", pinned: true},
		{name: "large-predecessor-cleared", trigger: "retry", dueOnly: true, issue: true, actions: notify, seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			run := env.priorRun("large", env.workflowID, "failed", 2, &env.past)
			run.Error, run.Output = large, map[string]string{"body": large}
			data.WorkflowRuns = []domain.WorkflowRun{run}
		}, want: "succeeded"},

		// A peer replica writes after both sources installed their images.
		{name: "peer-reschedules-retry", trigger: "retry", dueOnly: true, issue: true, actions: notify,
			seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				data.WorkflowRuns = []domain.WorkflowRun{env.priorRun("due", env.workflowID, "failed", 2, &env.past)}
			}, peer: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				data.WorkflowRuns[0].NextRetryAt = &env.futureTime
			}},
		{name: "peer-claims-retry", trigger: "retry", dueOnly: true, issue: true, actions: notify,
			seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				data.WorkflowRuns = []domain.WorkflowRun{env.priorRun("due", env.workflowID, "failed", 2, &env.past)}
			}, peer: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				data.WorkflowRuns[0].NextRetryAt = nil
				data.WorkflowRuns = append(data.WorkflowRuns, env.priorRun("peer-ran", env.workflowID, "succeeded", 3, nil))
			}},
		{name: "peer-claims-schedule", trigger: "schedule", dueOnly: true, defTrigger: "schedule", schedule: "@hourly", actions: notify,
			peer: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				definition := env.definition(data)
				definition.NextRunAt, definition.LastRunAt, definition.LastRunStatus = &env.futureTime, &env.past, "succeeded"
			}},
		{name: "peer-renames-definition", issue: true, actions: notify, peer: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			definition := env.definition(data)
			definition.Name, definition.Description, definition.Enabled = "Peer renamed", "peer description", false
		}},
		{name: "peer-deletes-definition", issue: true, actions: notify, peer: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			data.WorkflowDefinitions = nil
		}},
		{name: "peer-edits-issue-title", issue: true, actions: workflowCloseActions(workflowCloseAction("setIssuePriority", "priority", "1")),
			peer: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				env.target(data).Title = "Peer title"
			}},
		{name: "peer-sets-same-issue-field", issue: true, actions: workflowCloseActions(workflowCloseAction("setIssuePriority", "priority", "1")),
			peer: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				target := env.target(data)
				target.Priority, target.PriorityLabel = 2, priorityLabel(2)
			}},
		{name: "peer-appends-other-run", issue: true, actions: notify, peer: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			data.WorkflowRuns = append(data.WorkflowRuns, env.priorRun("peer-other", "other-workflow", "succeeded", 1, nil))
		}},
	}
	cases = append(cases,
		// Event-triggered definitions retried by the scheduler on their issue.
		workflowCloseCase{name: "event-retry-multi-action", trigger: "retry", dueOnly: true, defTrigger: "issueCreated", issue: true, actions: allActions,
			seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				env.definition(data).TeamID = env.issue.Team.ID
				data.WorkflowRuns = []domain.WorkflowRun{env.priorRun("due", env.workflowID, "failed", 1, &env.past)}
			}, want: "succeeded"},
		workflowCloseCase{name: "event-retry-label-fails-after-priority", trigger: "retry", dueOnly: true, defTrigger: "issueCreated", issue: true,
			actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
				return []domain.WorkflowAction{workflowCloseAction("setIssuePriority", "priority", "2"), workflowCloseAction("notify"), workflowCloseAction("addIssueLabel", "labelId", env.archived)}
			}, seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				data.WorkflowRuns = []domain.WorkflowRun{env.priorRun("due", env.workflowID, "failed", 1, &env.past)}
			}, want: "failed"},
		// Scheduled multi-action runs.
		workflowCloseCase{name: "schedule-due-multi-action", trigger: "schedule", dueOnly: true, defTrigger: "schedule", schedule: "*/5 * * * *",
			actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
				return []domain.WorkflowAction{workflowCloseAction("notify"), workflowCloseAction("createIssue", "teamId", env.issue.Team.ID, "title", "Scheduled one"),
					workflowCloseAction("notify", "recipientId", env.other.ID), workflowCloseAction("createIssue", "teamId", env.issue.Team.ID, "title", "Scheduled two")}
			}, want: "succeeded"},
		workflowCloseCase{name: "label-on-archived-issue", issue: true, actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("addIssueLabel", "labelId", env.plain), workflowCloseAction("addIssueLabel", "labelId", env.grouped)}
		}, seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			env.target(data).ArchivedAt = &env.past
		}, want: "succeeded"},
		// The point writer hands these to the full-image writer.
		workflowCloseCase{name: "full-workflow-id-is-notification-id", issue: true, actions: workflowCloseActions(workflowCloseAction("setIssuePriority", "priority", "1")),
			seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
				data.Notifications = append(data.Notifications, domain.Notification{ID: env.workflowID, RecipientID: env.viewer.ID, Type: "comment",
					IssueID: env.issue.ID, Actor: env.viewer, Category: "comments", LatestActorIDs: []string{}, CreatedAt: env.fixtureClock})
			}, want: "succeeded", full: true},
		workflowCloseCase{name: "full-created-id-stored", actions: func(env *workflowCloseEnv) []domain.WorkflowAction {
			return []domain.WorkflowAction{workflowCloseAction("createIssue", "teamId", env.issue.Team.ID, "title", "Collides")}
		}, seed: func(env *workflowCloseEnv, data *domain.Bootstrap) {
			number := 0
			for _, issue := range data.Issues {
				number = max(number, issue.Number)
			}
			for index := range data.Issues {
				if data.Issues[index].ID == env.otherIssueID {
					data.Issues[index].ID = fmt.Sprintf("issue_%d", number+1)
					data.Issues[index].Identifier = env.prefix + "COLLIDE-1"
				}
			}
		}, want: "succeeded", full: true},
	)
	for _, schedule := range []string{"@hourly", "@daily", "@weekly", "0 10 * * 1-5", "0 10 * * 0", "*/5 * * * *", "30 9-17 * 1,6,12 1-5", "bad schedule", "@every 30s"} {
		cases = append(cases, workflowCloseCase{
			name: "schedule-due/" + schedule, trigger: "schedule", dueOnly: true, defTrigger: "schedule", schedule: schedule,
			actions: notify, want: "succeeded",
		})
	}
	return cases
}

func TestWorkflowActionTriggerDifferential(t *testing.T) {
	env := workflowCloseSetup(t)
	for _, test := range workflowCloseCases() {
		t.Run(test.name, func(t *testing.T) { env.runCase(t, test) })
	}
}
