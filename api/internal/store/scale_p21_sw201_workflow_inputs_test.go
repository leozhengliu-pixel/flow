package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW201NextIssueNumber(issues []domain.Issue) int {
	next := 1
	for _, issue := range issues {
		if issue.Number >= next {
			next = issue.Number + 1
		}
	}
	return next
}

func TestScaleP21SW201WorkflowSelectedInputs(t *testing.T) {
	clock := time.Date(2026, 11, 16, 2, 3, 4, 0, time.UTC)
	for _, numbers := range [][]int{
		nil, {-4, -2}, {1, 4, 3}, {math.MaxInt}, {math.MaxInt, -10},
		{math.MaxInt, math.MinInt}, {math.MaxInt - 1}, {9, math.MaxInt, 4},
		{math.MaxInt, 3, math.MaxInt}, {math.MaxInt, 3, math.MaxInt - 1},
	} {
		for _, runCount := range []int{0, 1, 499, 500, 501, 1001} {
			t.Run(fmt.Sprintf("%v/%d", numbers, runCount), func(t *testing.T) {
				data := domain.Bootstrap{
					Workspace:       domain.Workspace{ID: "physical"},
					NextIssueNumber: 997,
					Viewer:          domain.User{ID: "viewer"}, Users: []domain.User{{ID: "viewer"}},
					Teams: []domain.Team{{ID: "team"}},
					WorkflowDefinitions: []domain.WorkflowDefinition{
						{ID: "target", Enabled: true, Actions: []domain.WorkflowAction{{Type: "createIssue", Config: map[string]string{"teamId": "team", "title": "Created"}}}},
						{ID: "target", Enabled: false, Name: "Later duplicate must not win"},
						{ID: "unrelated", Actions: []domain.WorkflowAction{{Type: "unknown", Config: map[string]string{"body": strings.Repeat("unselected ", 40000)}}}},
					},
				}
				for index, number := range numbers {
					data.Issues = append(data.Issues, domain.Issue{ID: fmt.Sprintf("issue-%d", index), Number: number})
				}
				for index := range runCount {
					workflow := "other"
					if index == 0 {
						workflow = "target"
					}
					data.WorkflowRuns = append(data.WorkflowRuns, domain.WorkflowRun{
						ID: fmt.Sprintf("old-%d", index), WorkflowID: workflow, Attempt: index + 1,
						Status: "failed", NextRetryAt: &clock, Output: map[string]string{"prior": "retained"},
					})
				}
				view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
				source, err := view.retainMetadataLogicalSource(t.Context(), "physical")
				if err != nil {
					t.Fatal(err)
				}
				defer source.Release()
				inputs, err := view.loadWorkflowRunInputs(t.Context(), source, "target", "issue", "issue-0")
				if err != nil {
					t.Fatal(err)
				}
				defer inputs.close()
				if inputs.data.NextIssueNumber != data.NextIssueNumber || len(inputs.data.WorkflowDefinitions) != 1 || inputs.data.WorkflowDefinitions[0].Enabled != true ||
					len(inputs.data.WorkflowRuns) > 500 {
					t.Fatal("selected workflow precedence or tail bound changed")
				}
				// The immediate legacy action's ordered integer fold is the
				// oracle, including multiple creates and legal SQL/import ints.
				fullIssues := append([]domain.Issue(nil), data.Issues...)
				for range 4 {
					expected := scaleP21SW201NextIssueNumber(fullIssues)
					got := scaleP21SW201NextIssueNumber(inputs.data.Issues)
					if expected != got {
						t.Fatal("selected number headers changed ordered fold", numbers, expected, got)
					}
					created := domain.Issue{ID: fmt.Sprintf("created-%d", expected), Number: expected}
					fullIssues = append([]domain.Issue{created}, fullIssues...)
					inputs.data.Issues = append([]domain.Issue{created}, inputs.data.Issues...)
				}
				for index := len(inputs.data.WorkflowRuns) - 1; index >= 0; index-- {
					if inputs.data.WorkflowRuns[index].WorkflowID == "target" {
						inputs.data.WorkflowRuns[index].NextRetryAt = nil
						break
					}
				}
				newRun := domain.WorkflowRun{ID: "new-run", WorkflowID: "target", Status: "succeeded", Output: map[string]string{}}
				inputs.data.WorkflowRuns = append(inputs.data.WorkflowRuns, newRun)
				if len(inputs.data.WorkflowRuns) > 500 {
					inputs.data.WorkflowRuns = inputs.data.WorkflowRuns[len(inputs.data.WorkflowRuns)-500:]
				}
				expectedRuns := append([]domain.WorkflowRun(nil), data.WorkflowRuns...)
				for index := len(expectedRuns) - 1; index >= 0; index-- {
					if expectedRuns[index].WorkflowID == "target" {
						expectedRuns[index].NextRetryAt = nil
						break
					}
				}
				expectedRuns = append(expectedRuns, newRun)
				if len(expectedRuns) > 500 {
					expectedRuns = expectedRuns[len(expectedRuns)-500:]
				}
				image, err := inputs.image(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer image.Release()
				index, _, _ := metadataLogicalField("workflowRuns")
				value, err := image.fields[index].Value(t.Context(), 32<<20)
				if err != nil || !reflect.DeepEqual(expectedRuns, value.Interface().([]domain.WorkflowRun)) {
					t.Fatal("selected retry predecessor changed the exact final tail", err)
				}
				index, _, _ = metadataLogicalField("issues")
				value, err = image.fields[index].Value(t.Context(), 32<<20)
				if err != nil || !reflect.DeepEqual(fullIssues, value.Interface().([]domain.Issue)) {
					t.Fatal("number-only placeholders escaped into issue publication", err)
				}
				inputs.close()
				if view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("selected input work did not release", view.MetadataWorkUsedForTesting())
				}
			})
		}
	}
}

func TestScaleP21SW201WorkflowLargeRetainedRunBodies(t *testing.T) {
	now := time.Date(2026, 11, 16, 2, 3, 4, 0, time.UTC)
	body := strings.Repeat("x", 4<<20)
	data := domain.Bootstrap{
		Workspace:           domain.Workspace{ID: "physical"},
		WorkflowDefinitions: []domain.WorkflowDefinition{{ID: "target", Enabled: true}},
	}
	for index := range 3 {
		data.WorkflowRuns = append(data.WorkflowRuns, domain.WorkflowRun{
			ID: fmt.Sprintf("retained-%d", index), WorkflowID: "target",
			Status: "failed", Attempt: index + 1, NextRetryAt: &now,
			Error: body, Output: map[string]string{"retained": body},
		})
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	source, err := view.retainMetadataLogicalSource(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	inputs, err := view.loadWorkflowRunInputs(t.Context(), source, "target", "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer inputs.close()
	for _, run := range inputs.data.WorkflowRuns {
		if run.ID != "" || run.Error != "" || run.Output != nil {
			t.Fatal("retained run body entered the callback graph")
		}
	}
	inputs.data.WorkflowRuns[2].NextRetryAt = nil
	appended := domain.WorkflowRun{ID: "new", WorkflowID: "target", Status: "succeeded", Output: map[string]string{"new": "exact"}}
	inputs.data.WorkflowRuns = append(inputs.data.WorkflowRuns, appended)
	image, err := inputs.image(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer image.Release()
	index, _, _ := metadataLogicalField("workflowRuns")
	value, err := image.fields[index].Value(t.Context(), 128<<20)
	if err != nil {
		t.Fatal(err)
	}
	expected := append([]domain.WorkflowRun(nil), data.WorkflowRuns...)
	expected[2].NextRetryAt = nil
	expected = append(expected, appended)
	if !reflect.DeepEqual(value.Interface().([]domain.WorkflowRun), expected) {
		t.Fatal("source-ordinal retry patch changed retained run bodies or order")
	}
	inputs.close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("retained run header ownership did not release")
	}
}

func TestScaleP21SW201WorkflowRetainedRunPatchGuard(t *testing.T) {
	now := time.Date(2026, 11, 16, 2, 3, 4, 0, time.UTC)
	for _, mode := range []string{"old-attempt", "old-status", "old-output", "older-retry-clear", "retry-retarget", "extra-new-run", "delete", "reorder"} {
		t.Run(mode, func(t *testing.T) {
			data := domain.Bootstrap{
				WorkflowDefinitions: []domain.WorkflowDefinition{{ID: "target", Enabled: true}},
				WorkflowRuns: []domain.WorkflowRun{
					{ID: "older", WorkflowID: "target", Status: "failed", Attempt: 1, NextRetryAt: &now},
					{ID: "last", WorkflowID: "target", Status: "failed", Attempt: 2, NextRetryAt: &now},
				},
			}
			view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
			source, err := view.retainMetadataLogicalSource(t.Context(), "physical")
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			inputs, err := view.loadWorkflowRunInputs(t.Context(), source, "target", "", "")
			if err != nil {
				t.Fatal(err)
			}
			defer inputs.close()
			inputs.data.WorkflowRuns = append(inputs.data.WorkflowRuns, domain.WorkflowRun{ID: "new", WorkflowID: "target"})
			switch mode {
			case "old-attempt":
				inputs.data.WorkflowRuns[0].Attempt++
			case "old-status":
				inputs.data.WorkflowRuns[0].Status = "succeeded"
			case "old-output":
				inputs.data.WorkflowRuns[0].Output = map[string]string{"unexpected": "write"}
			case "older-retry-clear":
				inputs.data.WorkflowRuns[0].NextRetryAt = nil
			case "retry-retarget":
				later := now.Add(time.Minute)
				inputs.data.WorkflowRuns[1].NextRetryAt = &later
			case "extra-new-run":
				inputs.data.WorkflowRuns = append(inputs.data.WorkflowRuns, domain.WorkflowRun{ID: "second-new"})
			case "delete":
				inputs.data.WorkflowRuns = inputs.data.WorkflowRuns[1:]
			case "reorder":
				inputs.data.WorkflowRuns[0], inputs.data.WorkflowRuns[1] = inputs.data.WorkflowRuns[1], inputs.data.WorkflowRuns[0]
			}
			image, err := inputs.image(t.Context())
			if image != nil {
				image.Release()
			}
			if !errors.Is(err, ErrBoundedMetadataCoverage) {
				t.Fatal("retained run edit escaped its closed ordinal patch", mode, err)
			}
			inputs.close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("refused history patch retained admission")
			}
		})
	}
}

func TestScaleP21SW201WorkflowDueReadInstalledChoiceAndGeneration(t *testing.T) {
	now := time.Date(2026, 11, 16, 2, 3, 4, 0, time.UTC)
	data := func(prefix string) domain.Bootstrap {
		return domain.Bootstrap{
			Workspace:           domain.Workspace{ID: "physical"},
			WorkflowDefinitions: []domain.WorkflowDefinition{{ID: prefix + "-scheduled", Enabled: true, Trigger: "schedule", NextRunAt: &now}},
			WorkflowRuns:        []domain.WorkflowRun{{WorkflowID: prefix + "-retry", Status: "failed", NextRetryAt: &now, ResourceType: "issue", ResourceID: prefix + "-issue"}},
		}
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data("old")})
	parent, found, err := view.BeginInstalledMetadataRead(t.Context(), "physical")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	pinned, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	next, err := captureMetadataLogicalSource(t.Context(), data("selected"), copyStamp{revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource("physical", next)
	parent.Close()
	read, found, err := view.BeginWorkflowScheduleRead(pinned, "physical")
	if err != nil || !found {
		t.Fatal("literal installed lookup honored an obsolete request pin", found, err)
	}
	defer read.Close()
	count := 0
	err = read.VisitDue(t.Context(), now, func(item WorkflowScheduleItem) error {
		switch count {
		case 0:
			if item.WorkflowID != "selected-scheduled" || item.Trigger != "schedule" {
				t.Fatal("due read did not select the current installed source", item)
			}
			later, err := captureMetadataLogicalSource(t.Context(), data("later"), copyStamp{revision: -1})
			if err != nil {
				return err
			}
			view.installMetadataLogicalSource("physical", later)
		case 1:
			if item.WorkflowID != "selected-retry" || item.Trigger != "retry" || item.ResourceID != "selected-issue" {
				t.Fatal("retry loop changed source after the definition callback", item)
			}
		default:
			t.Fatal("due loops duplicated an invocation")
		}
		count++
		return nil
	})
	if err != nil || count != 2 {
		t.Fatal("installed due source/order changed", count, err)
	}
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("selected source generation retained admission")
	}
}

func TestScaleP21SW201WorkflowDueRead(t *testing.T) {
	now := time.Date(2026, 11, 16, 2, 3, 4, 0, time.UTC)
	earlier, later := now.Add(-time.Minute), now.Add(time.Minute)
	data := domain.Bootstrap{
		WorkflowDefinitions: []domain.WorkflowDefinition{
			{ID: "scheduled", Enabled: true, Trigger: "schedule", NextRunAt: &earlier},
			{ID: "scheduled", Enabled: true, Trigger: "schedule", NextRunAt: &now},
			{ID: "later", Enabled: true, Trigger: "schedule", NextRunAt: &later},
			{ID: "disabled", Trigger: "schedule", NextRunAt: &earlier},
			{ID: "manual", Enabled: true, Trigger: "manual", NextRunAt: &earlier},
			{ID: "no-clock", Enabled: true, Trigger: "schedule"},
			{ID: "", Enabled: true, Trigger: "schedule", NextRunAt: &earlier,
				Actions: []domain.WorkflowAction{{Type: "unknown", Config: map[string]string{"ignored": strings.Repeat("large configuration ", 300000)}}}},
		},
		WorkflowRuns: []domain.WorkflowRun{
			{WorkflowID: "scheduled", Status: "failed", NextRetryAt: &earlier, ResourceType: "issue", ResourceID: "issue"},
			{WorkflowID: "scheduled", Status: "failed", NextRetryAt: &now, ResourceType: "issue", ResourceID: "same-again"},
			{WorkflowID: "later", Status: "failed", NextRetryAt: &later},
			{WorkflowID: "succeeded", Status: "succeeded", NextRetryAt: &earlier},
			{WorkflowID: "no-clock", Status: "failed"},
		},
	}
	expected := []WorkflowScheduleItem{
		{WorkflowID: "scheduled", Trigger: "schedule"}, {WorkflowID: "scheduled", Trigger: "schedule"}, {Trigger: "schedule"},
		{WorkflowID: "scheduled", Trigger: "retry", ResourceType: "issue", ResourceID: "issue"},
		{WorkflowID: "scheduled", Trigger: "retry", ResourceType: "issue", ResourceID: "same-again"},
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	read, found, err := view.BeginWorkflowScheduleRead(t.Context(), "physical")
	if err != nil || !found {
		t.Fatal(err, found)
	}
	defer read.Close()
	var got []WorkflowScheduleItem
	err = read.VisitDue(t.Context(), now, func(item WorkflowScheduleItem) error {
		if view.MetadataWorkUsedForTesting() == 0 {
			t.Fatal("due item lost admission before its visitor")
		}
		got = append(got, item)
		return nil
	})
	if err != nil || !reflect.DeepEqual(expected, got) || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("due predicates/order/duplicates changed", err, got)
	}
	before := view.MetadataWorkUsedForTesting()
	sentinel := errors.New("visitor failed")
	if err := read.VisitDue(t.Context(), now, func(WorkflowScheduleItem) error { return sentinel }); !errors.Is(err, sentinel) ||
		view.MetadataWorkUsedForTesting() != before {
		t.Fatal("visitor failure leaked selected headers", err, view.MetadataWorkUsedForTesting(), before)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := read.VisitDue(cancelled, now, func(WorkflowScheduleItem) error { t.Fatal("cancelled visitor ran"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("due read ignored cancellation", err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = view.MetadataWorkUsedForTesting() + 1
	err = read.VisitDue(t.Context(), now, func(WorkflowScheduleItem) error { t.Fatal("unadmitted visitor ran"); return nil })
	view.metadataResidency.work.limit = limit
	if !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != before {
		t.Fatal("due header budget failure leaked", err)
	}
	read.Close()
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("due source did not release")
	}
	if err := read.VisitDue(t.Context(), now, func(WorkflowScheduleItem) error { return nil }); err == nil {
		t.Fatal("closed due reader remained usable")
	}
}

func TestScaleP21SW201WorkflowRunFailureBoundaries(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	workflowID := "sw201-" + f.data.Workspace.ID
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw201_seed", "", nil, func(data *domain.Bootstrap) error {
		data.WorkflowDefinitions = []domain.WorkflowDefinition{{ID: workflowID, Enabled: true, Name: "Before"}}
		data.WorkflowRuns = []domain.WorkflowRun{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	before, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil {
		t.Fatal(err)
	}
	mutate := func(data *domain.Bootstrap) error {
		data.WorkflowDefinitions[0].Name = "After"
		data.WorkflowRuns = append(data.WorkflowRuns, domain.WorkflowRun{ID: workflowID + "-run", WorkflowID: workflowID, Status: "succeeded"})
		return nil
	}
	sentinel := errors.New("failed workflow callback")
	for _, failure := range []string{"cancelled", "budget", "callback", "cancel-during-write"} {
		t.Run(failure, func(t *testing.T) {
			var closes []func()
			ctx := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
				closes = append(closes, close)
				return true
			})
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			callback := mutate
			want := context.Canceled
			release := func() {}
			writes := 0
			switch failure {
			case "cancelled":
				cancel()
			case "budget":
				want = ErrMetadataWorkBudget
				_, release, err = view.tryAdmitAdditionalMetadataWork(ctx, metadataWorkBytes)
				if err != nil {
					t.Fatal(err)
				}
			case "callback":
				want = sentinel
				callback = func(data *domain.Bootstrap) error {
					_ = mutate(data)
					return sentinel
				}
			case "cancel-during-write":
				ctx = WithSQLStatementObserver(ctx, func(query string) {
					if strings.HasPrefix(query, "INSERT INTO workspace_metadata_records") {
						writes++
						cancel()
					}
				})
			}
			got := view.MutateWorkflowRun(ctx, workspace, workflowID, "", "", map[string]string{"trigger": "manual"}, callback)
			release()
			for _, close := range closes {
				close()
				close()
			}
			after, stampErr := readMetadataStamp(t.Context(), f.db, workspace)
			if !errors.Is(got, want) || stampErr != nil || after != before || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("workflow failure escaped rollback/release", got, want, stampErr, before, after, view.MetadataWorkUsedForTesting())
			}
			if failure == "cancel-during-write" && writes == 0 {
				t.Fatal("persistence cancellation did not reach a write")
			}
		})
	}
}

func TestScaleP21SW201WorkflowCallbackNativeOwnership(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	workflowID := "sw201-native-" + f.data.Workspace.ID
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw201_native_seed", "", nil, func(data *domain.Bootstrap) error {
		data.WorkflowDefinitions = []domain.WorkflowDefinition{{ID: workflowID, Enabled: true}}
		data.WorkflowRuns = []domain.WorkflowRun{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	output := func() (context.Context, func()) {
		var closes []func()
		ctx := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
			closes = append(closes, close)
			return true
		})
		return ctx, func() {
			for _, close := range closes {
				close()
				close()
			}
			closes = nil
		}
	}
	run := domain.WorkflowRun{
		ID: workflowID + "-first", WorkflowID: workflowID, Status: "failed",
		Error: strings.Repeat("x", 4<<20), Output: map[string]string{},
	}
	write := func(ctx context.Context, run domain.WorkflowRun) error {
		return view.MutateWorkflowRun(ctx, workspace, workflowID, "", "", nil, func(data *domain.Bootstrap) error {
			data.WorkflowRuns = append(data.WorkflowRuns, run)
			return nil
		})
	}
	ctx, closeFirst := output()
	defer closeFirst()
	if err := write(ctx, run); err != nil {
		t.Fatal("native callback result did not persist", err)
	}
	native, err := metadataAggregateNativeCost(t.Context(), reflect.ValueOf([]domain.WorkflowRun{run}), 0)
	if err != nil {
		t.Fatal(err)
	}
	encoding, err := metadataAggregatePayloadEncodingWork(t.Context(), reflect.ValueOf([]domain.WorkflowRun{run}), 0)
	if err != nil {
		t.Fatal(err)
	}
	held := view.MetadataWorkUsedForTesting()
	if held < int(native) || held >= encoding {
		t.Fatal("retained result does not own its native graph independently of temporary encoding", held, native, encoding)
	}
	stamp, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil {
		t.Fatal(err)
	}
	grown := run
	grown.ID = workflowID + "-grown"
	grown.Error = strings.Repeat("y", 12<<20)
	grownNative, err := metadataAggregateNativeCost(t.Context(), reflect.ValueOf([]domain.WorkflowRun{grown}), 0)
	if err != nil {
		t.Fatal(err)
	}
	// Keep less than the new graph's native allowance available. This is
	// pressure on the real per-replica limit, not a smaller test-only limit.
	pressure := int(metadataWorkBytes) - held - (int(grownNative) - 1)
	if pressure <= 0 {
		t.Fatal("native growth fixture does not create admission pressure")
	}
	_, releasePressure, err := view.tryAdmitAdditionalMetadataWork(t.Context(), pressure)
	if err != nil {
		t.Fatal(err)
	}
	defer releasePressure()
	ctx, closeDenied := output()
	defer closeDenied()
	if err := write(ctx, grown); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("body growth escaped the still-held native output allowance", err)
	}
	closeDenied()
	current, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil || current != stamp {
		t.Fatal("denied native body growth changed durable metadata", current, stamp, err)
	}
	releasePressure()
	closeFirst()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("first native result did not release its output allowance")
	}
	ctx, closeGrown := output()
	defer closeGrown()
	if err := write(ctx, grown); err != nil {
		t.Fatal("released native output did not admit the larger streamed result", err)
	}
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	got, found := f.WorkspaceMetadataFields(workspace, "workflowRuns")
	if !found || !reflect.DeepEqual(got.WorkflowRuns, []domain.WorkflowRun{run, grown}) {
		t.Fatal("native lifetime or body growth changed persisted callback results")
	}
	closeGrown()
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("callback native lifetime leaked work or hydrated full metadata", view.MetadataWorkUsedForTesting(), view.WorkspaceMetadataDecodes())
	}
}
