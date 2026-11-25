package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21LoopTickEqual(got, want []domain.Loop) bool {
	left, leftErr := json.Marshal(got)
	right, rightErr := json.Marshal(want)
	if leftErr != nil || rightErr != nil || !bytes.Equal(left, right) || len(got) != len(want) {
		return false
	}
	got, want = slices.Clone(got), slices.Clone(want)
	for index := range got {
		if !got[index].CreatedAt.Equal(want[index].CreatedAt) || (got[index].NextRunAt == nil) != (want[index].NextRunAt == nil) {
			return false
		}
		if got[index].NextRunAt != nil && !got[index].NextRunAt.Equal(*want[index].NextRunAt) {
			return false
		}
		got[index].CreatedAt, want[index].CreatedAt = time.Time{}, time.Time{}
		got[index].NextRunAt, want[index].NextRunAt = nil, nil
	}
	return reflect.DeepEqual(got, want)
}

func scaleP21LoopTickProjection(data domain.Bootstrap, mode string) []domain.Loop {
	var result []domain.Loop
	for _, item := range data.Loops {
		if !item.Enabled || item.Status == "draft" || (item.TriggerType == "schedule") != (mode == "schedule") {
			continue
		}
		value := domain.Loop{ID: item.ID, Enabled: item.Enabled, Status: item.Status, TriggerType: item.TriggerType}
		names := strings.Fields("startDate starting interval unit time weekdays timezone")
		if mode == "schedule" {
			value.CreatedAt, value.NextRunAt = item.CreatedAt, item.NextRunAt
		} else {
			value.TeamID, value.TeamAccess, value.Level, value.TrustedSourceKeys = item.TeamID, item.TeamAccess, item.Level, item.TrustedSourceKeys
			names = strings.Fields("event action includeCreated value filters filter filterField filterOperator filterValue teamIds")
		}
		for name, item := range item.TriggerConfig {
			if slices.Contains(names, name) {
				if value.TriggerConfig == nil {
					value.TriggerConfig = map[string]any{}
				}
				value.TriggerConfig[name] = item
			}
		}
		result = append(result, value)
	}
	return result
}

func TestScaleP21HotLoopScheduleInstalledProjection(t *testing.T) {
	now := time.Date(2026, 11, 14, 1, 2, 3, 4, time.FixedZone("native", 7*3600+123))
	wide := strings.Repeat("ignored loop body ", 300000)
	data := domain.Bootstrap{
		Workspace: domain.Workspace{URLKey: "logical"},
		Loops: []domain.Loop{
			{ID: "ignored-draft", Enabled: true, Status: "draft", TriggerType: "schedule", Instructions: wide},
			{ID: "ignored-paused", Status: "published", TriggerType: "issue", Instructions: wide},
			{ID: "duplicate", Enabled: true, Status: "published", TriggerType: "schedule", CreatedAt: now, NextRunAt: &now, TriggerConfig: map[string]any{"interval": int(3), "weekdays": []string{"mon"}, "starting": "2026-11-01", "opaque": json.RawMessage("not-json")}, Instructions: wide},
			{ID: "duplicate", Enabled: true, Status: "dra\x00ft", TriggerType: "schedule", TriggerConfig: map[string]any{"interval": int64(9007199254740993)}},
			{ID: "", Enabled: true, TriggerType: "issue", TriggerConfig: map[string]any{"event": "triage", "value": nil, "filters": []any{map[string]any{"field": "priority", "value": int(3)}}, "teamIds": []string{"private"}, "opaque": wide}, Instructions: wide},
			{ID: "event\x00id", Enabled: true, Status: "published", TriggerType: "", TrustedSourceKeys: []string{"integration:slack"}},
		},
		Teams: []domain.Team{{ID: "private", Name: "Private", Private: true}, {ID: "public", Name: "Public"}},
		TeamSettings: map[string]domain.TeamSettings{
			"private":                               {TeamID: "private", Access: "private", TriageEnabled: true, ParentTeamID: "orphan", InheritWorkflowStatuses: true, AgentGuidance: wide},
			"public":                                {TeamID: "", TriageEnabled: true},
			"orphan":                                {TeamID: "orphan", InheritWorkflowStatuses: true, ParentTeamID: "last-parent"},
			"last-parent":                           {TeamID: "last-parent", AgentGuidance: wide},
			strings.Repeat("unrelated key", 300000): {TeamID: "unrelated", AgentGuidance: wide},
		},
		States:            []domain.WorkflowState{{ID: "state", TeamID: "last-parent", Name: "Native", Position: 2, Default: true}},
		WorkspaceSettings: domain.WorkspaceSettings{ExternalLoopTriggers: true, TrustedSourcesMode: "allowlist", TrustedSourcesAllowlist: []string{"integration:slack"}, AgentInstructions: wide},
		CustomerRequests:  []domain.CustomerRequest{{ID: "request", IssueID: "first"}, {ID: "other", IssueID: "skip"}, {ID: "request", IssueID: "issue"}},
		Projects:          []domain.Project{{ID: "target", Name: "First", TeamIDs: []string{"private"}, Description: wide}, {ID: "target", Name: "Second", TeamIDs: []string{"public"}}, {ID: "other", Description: wide}},
		Asks:              []domain.Ask{{IssueID: "other", Source: wide}, {IssueID: "issue", Source: "web"}, {IssueID: "issue", Source: "slack"}},
		Members:           []domain.WorkspaceMember{{User: domain.User{ID: "creator", Name: wide}, Role: "app"}, {User: domain.User{ID: "other", Name: wide}}},
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	view.lastWorkspaceKey = "physical"
	for _, key := range []string{"physical", ""} {
		for _, mode := range []string{"schedule", "event"} {
			queries := 0
			read, found, err := view.BeginLoopScheduleRead(WithSQLStatementObserver(t.Context(), func(string) { queries++ }), key, mode)
			if err != nil || !found {
				t.Fatal("installed loop selection failed", key, mode, found, err)
			}
			if !scaleP21LoopTickEqual(read.Data.Loops, scaleP21LoopTickProjection(data, mode)) {
				t.Fatal("native loop fields/order/config values changed", mode, read.Data.Loops)
			}
			if mode == "event" {
				if err := read.LoadTriggerEntity(t.Context(), domain.DomainEvent{Type: "customer_request.created", AggregateID: "request"}); err != nil {
					t.Fatal(err)
				}
				if len(read.Data.CustomerRequests) != 2 || read.Data.CustomerRequests[1].IssueID != "issue" {
					t.Fatal("customer request duplicate order changed", read.Data.CustomerRequests)
				}
				if err := read.LoadTriggerEntity(t.Context(), domain.DomainEvent{Type: "project.updated", AggregateID: "target"}); err != nil {
					t.Fatal(err)
				}
				if len(read.Data.Projects) != 2 || read.Data.Projects[0].Name != "First" || read.Data.Projects[0].Description != "" {
					t.Fatal("target entity order/body projection changed", read.Data.Projects)
				}
				if err := read.LoadTriggerContext(t.Context(), &domain.Issue{ID: "issue", Team: domain.Team{ID: "private"}, Creator: domain.User{ID: "creator"}}, "created", nil); err != nil {
					t.Fatal(err)
				}
				if len(read.Data.TeamSettings) != 4 || read.Data.TeamSettings["private"].AgentGuidance != "" || read.Data.TeamSettings["last-parent"].TeamID != "last-parent" || read.Data.TeamSettings["public"].TeamID != "" {
					t.Fatal("native map keys/parent closure changed", read.Data.TeamSettings)
				}
				if len(read.Data.Asks) != 2 || read.Data.Asks[0].Source != "web" || read.Data.Asks[1].Source != "slack" || len(read.Data.Members) != 1 || read.Data.Members[0].User.Name != "" || read.Data.Members[0].Role != "app" {
					t.Fatal("matching source references changed", read.Data.Asks, read.Data.Members)
				}
				if !read.Data.WorkspaceSettings.ExternalLoopTriggers || len(read.Data.WorkspaceSettings.TrustedSourcesAllowlist) != 1 || read.Data.WorkspaceSettings.AgentInstructions != "" {
					t.Fatal("source trust fields changed")
				}
			}
			if queries != 0 || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used == 0 {
				t.Fatal("installed projection used SQL/full decode or lacks ownership", queries)
			}
			read.Close()
			read.Close()
			if view.metadataResidency.work.used != 0 || !reflect.DeepEqual(read.Data, domain.Bootstrap{}) {
				t.Fatal("loop read did not clear/release")
			}
		}
	}
	probe, found, err := view.BeginLoopScheduleRead(t.Context(), "physical", "event-probe")
	if err != nil || !found || !probe.HasLoops || probe.Data.Loops != nil {
		t.Fatal("event-loop predicate changed", found, err)
	}
	probe.Close()
	for _, key := range []string{"logical", "missing"} {
		if _, found, err := view.BeginLoopScheduleRead(t.Context(), key, "event"); err != nil || found {
			t.Fatal("physical/missing workspace selection changed", key, found, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := view.BeginLoopScheduleRead(ctx, "physical", "schedule"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation not propagated", err)
	}
	oldLimit := view.metadataResidency.work.limit
	for _, limit := range []int{1, 140 << 10} {
		view.metadataResidency.work.limit = limit
		if _, _, err := view.BeginLoopScheduleRead(t.Context(), "physical", "event"); !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatal("partial extraction exceeded quota", limit, err)
		}
		if view.metadataResidency.work.used != 0 {
			t.Fatal("failed extraction leaked quota", limit)
		}
	}
	view.metadataResidency.work.limit = oldLimit
}

func TestScaleP21HotLoopScheduleWarmReloadBodyBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	expectedWork := map[bool]int{}
	for _, size := range []int{10000, 100000} {
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "loops"), workspace, "scale_p21.loop_tick_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Loops = []domain.Loop{
				{ID: "schedule", Enabled: true, Status: "published", TriggerType: "schedule", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Instructions: strings.Repeat("unrelated ", size), TriggerConfig: map[string]any{"interval": int(2), "unit": "day", "unknown": strings.Repeat("opaque ", size)}},
				{ID: "event", Enabled: true, Status: "published", TriggerType: "issue", TriggerConfig: map[string]any{"event": "created"}, Description: strings.Repeat("unrelated ", size)},
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			if reload {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			want, _ := f.WorkspaceMetadataFields(workspace, "loops")
			queries := 0
			read, found, err := view.BeginLoopScheduleRead(WithSQLStatementObserver(t.Context(), func(string) { queries++ }), workspace, "schedule")
			if err != nil || !found || !scaleP21LoopTickEqual(read.Data.Loops, scaleP21LoopTickProjection(want, "schedule")) {
				t.Fatal("warm/reloaded scalar inputs changed", reload, found, err)
			}
			held := view.metadataResidency.work.used
			if expectedWork[reload] == 0 {
				expectedWork[reload] = held
			}
			if held != expectedWork[reload] || queries != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("unrelated bodies changed read cost", size, reload, held, expectedWork, queries)
			}
			read.Close()
			if view.metadataResidency.work.used != 0 {
				t.Fatal("read quota leaked")
			}
			t.Logf("body_repetitions=%d reload=%t held_work=%d SQL=%d", size, reload, held, queries)
		}
	}
}
