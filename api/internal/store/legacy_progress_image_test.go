package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW119Data(workspace domain.Workspace, bodyBytes int) domain.Bootstrap {
	at := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	start, noUnestimated := "2026-10-15", false
	body := strings.Repeat("x", bodyBytes)
	data := domain.Bootstrap{
		Workspace: workspace,
		States: []domain.WorkflowState{
			{ID: "sw119-historical", Type: "completed", Description: body},
			{ID: "sw119-historical", TeamID: "another-team", Type: "backlog", Description: body},
		},
		TeamSettings: map[string]domain.TeamSettings{"sw119-team": {EstimateCountUnestimated: &noUnestimated}},
		Projects: []domain.Project{
			{ID: "sw119-z", Name: "Empty histories", Description: body},
			{ID: "sw119-a", Name: "First native duplicate", StartDate: &start, Description: body},
			{ID: "sw119-a", Name: "Later native duplicate", StartDate: &start, Description: body,
				ProgressHistory: []domain.ProjectProgressHistoryPoint{{Date: at, Value: 701}}},
			{ID: "sw119-empty", Name: "No issues, stale histories", Description: body,
				ScopeHistory: []domain.ProjectProgressHistoryPoint{{Date: at, Value: 702}}},
			{ID: "sw119-noop", Name: "No issues, empty nonnil histories", Description: body,
				ScopeHistory: []domain.ProjectProgressHistoryPoint{}},
		},
		Activities: map[string][]domain.ActivityEvent{},
	}
	for index, project := range []string{"sw119-a", "sw119-z", "sw119-a"} {
		estimate := float64(index + 2)
		issue := domain.Issue{
			ID: fmt.Sprint("sw119-issue-", index), Description: body, CreatedAt: at.AddDate(0, 0, index),
			Project: &domain.ProjectSummary{ID: project}, State: domain.WorkflowState{ID: "sw119-current", Type: "started", Description: body},
			Team: domain.Team{ID: "sw119-team", Name: body},
		}
		if index != 1 {
			issue.Estimate = &estimate
		}
		data.Issues = append(data.Issues, issue)
		data.Activities[issue.ID] = []domain.ActivityEvent{
			{ID: issue.ID + "-newer", Type: "issue.updated", CreatedAt: at.AddDate(0, 0, 9), Metadata: domain.ActivityMetadata{
				"stateBeforeId": " sw119-historical ", "stateBeforeType": "completed", "estimateBefore": " -3 ", "unrelated": body,
			}},
			{ID: issue.ID + "-ignored", Type: "other.event", CreatedAt: at.AddDate(0, 0, 8), Metadata: domain.ActivityMetadata{"stateBeforeId": "sw119-historical", "unrelated": body}},
			{ID: issue.ID + "-older", Type: "issue.updated", CreatedAt: at.AddDate(0, 0, 4), Metadata: domain.ActivityMetadata{
				"stateBeforeId": "missing-catalog-state", "stateBeforeType": "unstarted", "stateBefore": "Preserved fallback", "estimateBefore": " 5 ", "unrelated": body,
			}},
		}
	}
	// Native duplicate issues count separately and replay the same activity list.
	data.Issues = append(data.Issues, data.Issues[0])
	return data
}

func scaleP21SW119Source(t *testing.T, data domain.Bootstrap, reload bool) (*metadataLogicalSource, domain.Bootstrap) {
	t.Helper()
	if reload {
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		var decoded domain.Bootstrap
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		data = decoded
	}
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID, revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.Release)
	return source, data
}

func TestScaleP21SW119LegacyProgressImageDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	for _, reload := range []bool{false, true} {
		t.Run(fmt.Sprintf("reload=%t", reload), func(t *testing.T) {
			source, data := scaleP21SW119Source(t, scaleP21SW119Data(f.data.Workspace, 64<<10), reload)
			want := slices.Compact(progressChangedProjects(data, now))
			plan, err := view.prepareLegacyProgressImage(t.Context(), source, now)
			if err != nil {
				t.Fatal(err)
			}
			defer plan.Close()
			var got []string
			if err := plan.ForEachProject(t.Context(), func(id string) error {
				got = append(got, id)
				found, err := plan.HasProject(t.Context(), id)
				if err == nil && !found {
					t.Fatal("changed project omitted from stage index", id)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("logical progressChangedProjects differs", want, got)
			}
			if found, err := plan.HasProject(t.Context(), "sw119-noop"); err != nil || found {
				t.Fatal("empty nonnil histories unexpectedly changed", found, err)
			}
			if view.MetadataWorkUsedForTesting() == 0 {
				t.Fatal("progress plan lost its cursor/staging owner")
			}
			path := plan.stage.path
			plan.Close()
			plan.Close()
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("progress stage survived release", err)
			}
			if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("progress plan leaked work or hydrated full metadata")
			}
			if _, err := plan.HasProject(t.Context(), "sw119-a"); !errors.Is(err, errMetadataLogicalClosed) {
				t.Fatal("closed progress plan remained readable", err)
			}
		})
	}
}

func TestScaleP21SW119LegacyProgressSQLRebuildDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, reload := range []bool{false, true} {
		t.Run(fmt.Sprintf("reload=%t", reload), func(t *testing.T) {
			f, issue := scaleP21IssuePersistenceFixture(t, []domain.Project{
				{ID: "p21-project", Name: "Canonical first project", Description: strings.Repeat("Retained project body ", 4000)},
				{ID: "sw119-other", Name: "Other empty project"},
			}, nil)
			workspace := f.data.Workspace.URLKey
			now := utcDay(time.Now().UTC()).Add(12 * time.Hour)
			if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw119_seed", "", nil, func(data *domain.Bootstrap) error {
				data.Activities[issue.ID] = []domain.ActivityEvent{{
					ID: "sw119-transition", Type: "issue.updated", CreatedAt: issue.CreatedAt.Add(8 * 24 * time.Hour),
					Metadata: domain.ActivityMetadata{"stateBeforeId": "sw119-history", "estimateBefore": "7"},
				}}
				data.States = append(data.States,
					domain.WorkflowState{ID: "sw119-history", Type: "completed"},
					domain.WorkflowState{ID: "sw119-history", Type: "backlog"},
					domain.WorkflowState{ID: issue.State.ID, Type: "completed"},
				)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			view := scaleP21IssuePersistenceView(t, f, true, reload)
			data, found := f.BootstrapFor(workspace)
			if !found {
				t.Fatal("SQL progress fixture missing")
			}
			first := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == "p21-project" })
			if first < 0 {
				t.Fatal("SQL fixture target project missing")
			}
			data.Projects[first].ProgressHistory = nil
			duplicate := data.Projects[first]
			duplicate.Name = "Later native occurrence stays literal"
			duplicate.ProgressHistory = []domain.ProjectProgressHistoryPoint{{Date: now, Value: 719}}
			data.Projects = append(data.Projects, duplicate)
			source, data := scaleP21SW119Source(t, data, reload)
			plan, err := view.prepareLegacyProgressImage(t.Context(), source, now)
			if err != nil {
				t.Fatal(err)
			}
			defer plan.Close()
			ids := progressChangedProjects(data, now)
			want := cloneBootstrap(data)
			tx := scaleP21SW76Begin(t, f)
			before := scaleP21SW93Rows(t, tx, workspace)
			// The root writer owns the forced sorted rebase. This test begins
			// at that helper's immutable, already-rebased input boundary.
			states := progressStates(want)
			for _, id := range ids {
				index := slices.IndexFunc(want.Projects, func(project domain.Project) bool { return project.ID == id })
				if index >= 0 {
					if _, err := rebuildProjectProgress(t.Context(), tx, workspace, &want, &want.Projects[index], states, now); err != nil {
						t.Fatal(err)
					}
				}
			}
			rebuilt, err := plan.Rebuild(t.Context(), tx, workspace, source, now)
			if err != nil {
				t.Fatal(err)
			}
			defer rebuilt.Release()
			got, err := rebuilt.Select(t.Context(), MetadataRecordScope{Fields: []string{"projects"}}, 64<<20)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Projects, want.Projects) {
				t.Fatal("logical SQL rebuild differs from literal first-native project loop", scaleP21SW98FirstDiff(scaleP21SW63JSON(t, want.Projects), scaleP21SW63JSON(t, got.Projects), "projects"))
			}
			if got.Projects[first].Name != "Canonical first project" || got.Projects[len(got.Projects)-1].ProgressHistory[0].Value != 719 {
				t.Fatal("progress rewrite changed a later duplicate or an unrelated project field")
			}
			if !reflect.DeepEqual(before, scaleP21SW93Rows(t, tx, workspace)) {
				t.Fatal("progress projection wrote business rows before caller persistence")
			}
			original, err := source.Select(t.Context(), MetadataRecordScope{Fields: []string{"projects"}}, 64<<20)
			if err != nil || !reflect.DeepEqual(original.Projects, data.Projects) {
				t.Fatal("progress rewrite mutated its frozen source", err)
			}
			held := view.MetadataWorkUsedForTesting()
			cancelled, cancel := context.WithCancel(t.Context())
			injected := false
			cancelled = WithSQLStatementObserver(cancelled, func(query string) {
				if strings.Contains(query, "FROM issue_records i LEFT JOIN") {
					injected = true
					cancel()
				}
			})
			discarded, err := plan.Rebuild(cancelled, tx, workspace, source, now)
			cancel()
			if discarded != nil || !errors.Is(err, context.Canceled) || !injected {
				t.Fatal("SQL progress cancellation did not stop selected input loading", err, injected)
			}
			if view.MetadataWorkUsedForTesting() != held {
				t.Fatal("cancelled SQL progress group retained work")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			plan.Close()
			if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("SQL progress rebuild leaked work or hydrated metadata")
			}
		})
	}
}

func TestScaleP21SW119LegacyProgressBoundsCancellationRelease(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	peaks := []int64{}
	for _, size := range []int{64 << 10, 640 << 10} {
		source, _ := scaleP21SW119Source(t, scaleP21SW119Data(f.data.Workspace, size), false)
		var peak atomic.Int64
		ctx := scaleP21HotWebhookContext{Context: t.Context(), check: func() {
			budget := view.metadataResidency.work
			if !budget.mu.TryLock() {
				return
			}
			current := int64(budget.used)
			budget.mu.Unlock()
			for previous := peak.Load(); current > previous; previous = peak.Load() {
				if peak.CompareAndSwap(previous, current) {
					break
				}
			}
		}}
		plan, err := view.prepareLegacyProgressImage(ctx, source, now)
		if err != nil {
			t.Fatal(err)
		}
		peaks = append(peaks, peak.Load())
		cancelled, cancel := context.WithCancel(t.Context())
		calls := 0
		err = plan.ForEachProject(cancelled, func(string) error {
			calls++
			cancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatal("progress stage cursor ignored cancellation", calls, err)
		}
		plan.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("cancelled progress cursor leaked work")
		}
	}
	if peaks[0] == 0 || peaks[1] > peaks[0]+(64<<10) {
		t.Fatal("tenfold unrelated bodies increased admitted project work", peaks)
	}
	source, _ := scaleP21SW119Source(t, scaleP21SW119Data(f.data.Workspace, 0), false)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if plan, err := view.prepareLegacyProgressImage(ctx, source, now); !errors.Is(err, context.Canceled) || plan != nil {
		t.Fatal("cancelled progress prepare admitted a plan", plan, err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 128 << 10
	plan, err := view.prepareLegacyProgressImage(t.Context(), source, now)
	view.metadataResidency.work.limit = limit
	if !errors.Is(err, ErrMetadataWorkBudget) || plan != nil || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("progress group was allocated beyond its admission limit", plan, err)
	}
	t.Logf("unrelated_body_bytes=65536/655360 peak_admitted_work=%d/%d", peaks[0], peaks[1])
}

func TestScaleP21SW119LegacyProgressIndexScale(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	for _, count := range []int{64, 640} {
		base := scaleP21SW119Data(f.data.Workspace, 0)
		data := domain.Bootstrap{Workspace: base.Workspace, States: base.States, TeamSettings: base.TeamSettings, Activities: map[string][]domain.ActivityEvent{}}
		for ordinal := range count {
			id := fmt.Sprintf("sw119-index-%06d", ordinal)
			data.Projects = append(data.Projects, domain.Project{ID: id})
			issue := base.Issues[0]
			issue.ID, issue.Project = id+"-issue", &domain.ProjectSummary{ID: id}
			data.Issues = append(data.Issues, issue)
			data.Activities[issue.ID] = base.Activities[base.Issues[0].ID]
		}
		source, _ := scaleP21SW119Source(t, data, false)
		ctx := metadataLogicalSourceScratchContext(t.Context(), source)
		stage, err := sw26NewMigrationStage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		index := &legacyProgressIndex{store: view, source: source, stage: stage}
		started := time.Now()
		if err := index.build(ctx, "projects", "issues", "activities", "states", "teamSettings"); err != nil {
			stage.Close()
			t.Fatal(err)
		}
		elapsed := time.Since(started)
		for field, want := range map[string]int{"projects": count, "issues": count, "activities": count, "states": 1, "teamSettings": 1} {
			var got int
			if err := stage.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+legacyProgressTable(field)).Scan(&got); err != nil || got != want {
				stage.Close()
				t.Fatal("progress index row count", field, got, want, err)
			}
		}
		groups, inputs := 0, 0
		if err := index.projects(ctx, func(_ int, node metadataLogicalNode) error {
			work := &ResourceRead{store: view}
			defer work.Close()
			value, err := work.projectNode(ctx, node, "ID")
			if err != nil {
				return err
			}
			groups++
			return index.issues(ctx, value.FieldByName("ID").String(), func(metadataLogicalNode) error {
				inputs++
				return nil
			})
		}); err != nil {
			stage.Close()
			t.Fatal(err)
		}
		if groups != count || inputs != count {
			stage.Close()
			t.Fatal("indexed group traversal repeated or omitted unrelated inputs", groups, inputs, count)
		}
		if err := stage.Close(); err != nil {
			t.Fatal(err)
		}
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("progress index pass retained workspace headers")
		}
		t.Logf("projects=%d issues=%d activity_groups=%d state_ids=1 setting_ids=1 index_elapsed=%s group_visits=%d issue_visits=%d", count, count, count, elapsed, groups, inputs)
	}
}
