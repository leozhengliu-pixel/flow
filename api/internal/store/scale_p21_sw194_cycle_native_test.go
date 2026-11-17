package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW194CycleCloneNativeOwnership(t *testing.T) {
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
	viewer := domain.User{ID: "sw194-viewer", AppScopes: []string{"read"}, Via: &domain.ActorBot{ID: "bot", Name: "Original"}}
	data := domain.Bootstrap{
		Viewer: viewer, Users: []domain.User{viewer}, ViewerTeamIDs: []string{"team"},
		Issues:   []domain.Issue{{ID: "issue", SuggestedLabelIDs: []string{"label"}}},
		Settings: map[string]any{"nested": []string{"original"}},
		Activities: map[string][]domain.ActivityEvent{
			"issue": {{ID: "event", Metadata: domain.ActivityMetadata{"cycle": "original"}}},
		},
		ActivityHistoryHeads: map[string]domain.ActivityEvent{"issue": {ID: "head", Metadata: domain.ActivityMetadata{"cycle": "head"}}},
	}
	cost, err := cycleScheduleCloneValueWork(t.Context(), reflect.ValueOf(data), false, 0)
	if err != nil || cost == 0 {
		t.Fatal("missing clone admission", cost, err)
	}
	view := &SQLiteStore{metadataResidency: newMetadataResidency()}
	owner := &ResourceRead{store: view}
	if err := reserveCycleScheduleClone(t.Context(), owner, data); err != nil {
		t.Fatal(err)
	}
	cloned := cloneCallbackInput(data)
	cloned.Viewer.Via.Name = "Changed viewer"
	cloned.Users[0].Via.Name = "Changed user"
	cloned.Users[0].AppScopes[0] = "write"
	cloned.ViewerTeamIDs[0] = "other"
	cloned.Issues[0].SuggestedLabelIDs[0] = "other-label"
	cloned.Settings["nested"].([]string)[0] = "changed"
	cloned.Activities["issue"][0].Metadata["cycle"] = "changed"
	head := cloned.ActivityHistoryHeads["issue"]
	head.Metadata["cycle"] = "changed"
	if data.Viewer.Via.Name != "Original" || data.Users[0].AppScopes[0] != "read" ||
		data.ViewerTeamIDs[0] != "team" || data.Issues[0].SuggestedLabelIDs[0] != "label" ||
		data.Settings["nested"].([]string)[0] != "original" ||
		data.Activities["issue"][0].Metadata["cycle"] != "original" ||
		data.ActivityHistoryHeads["issue"].Metadata["cycle"] != "head" {
		t.Fatal("native clone admission replaced the original deep-clone behavior")
	}
	t.Logf("native clone owns %d bytes including callback extras", view.metadataResidency.work.used)
	owner.Close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("clone allowance leaked")
	}
}

func TestScaleP21SW194CycleCloneSharesImmutableBodies(t *testing.T) {
	var prior uint64
	for _, size := range []int{32, 5 << 20} {
		body := strings.Repeat("x", size)
		data := domain.Bootstrap{
			Issues:     []domain.Issue{{ID: "issue", Description: body}},
			Settings:   map[string]any{"body": body},
			Comments:   map[string][]domain.Comment{"issue": {{ID: "comment", Body: body}}},
			Activities: map[string][]domain.ActivityEvent{"issue": {{Metadata: domain.ActivityMetadata{"descriptionBefore": body}}}},
		}
		cost, err := cycleScheduleCloneValueWork(t.Context(), reflect.ValueOf(data), false, 0)
		if err != nil {
			t.Fatal(err)
		}
		if prior != 0 && cost != prior {
			t.Fatalf("immutable text entered mutable clone allowance: before=%d after=%d", prior, cost)
		}
		cloned := cloneCallbackInput(data)
		if cloned.Issues[0].Description != body || cloned.Comments["issue"][0].Body != body ||
			cloned.Activities["issue"][0].Metadata["descriptionBefore"] != body {
			t.Fatal("body values changed")
		}
		prior = cost
		t.Logf("body=%d bytes; mutable clone allowance=%d", size, cost)
	}
}

func TestScaleP21SW194CycleHeadersIgnoreBodies(t *testing.T) {
	var prior int
	for _, size := range []int{32, 5 << 20} {
		at := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
		data := domain.Bootstrap{
			Teams: []domain.Team{{ID: "team"}},
			Cycles: []domain.Cycle{
				{ID: "duplicate", TeamID: "team", Number: 1, Status: "current", StartsAt: at, EndsAt: at, Description: strings.Repeat("x", size)},
				{ID: "duplicate", TeamID: "team", Number: 2, Status: "upcoming", StartsAt: at.AddDate(0, 0, 1), EndsAt: at.AddDate(0, 0, 14), Name: "Opaque name"},
			},
			CycleSettings: map[string]domain.CycleSettings{"team": {Enabled: true, AutoCreate: true, AutoMigrate: true, UpcomingCount: 2}},
		}
		source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: "sw194"})
		if err != nil {
			t.Fatal(err)
		}
		view := &SQLiteStore{metadataResidency: newMetadataResidency()}
		owner := &ResourceRead{store: view}
		got, err := loadCycleScheduleMetadata(t.Context(), owner, source, false)
		if err != nil {
			owner.Close()
			source.Release()
			t.Fatal(err)
		}
		cost := view.metadataResidency.work.used
		if len(got.Cycles) != 2 || got.Cycles[0].ID != "duplicate" || got.Cycles[1].Number != 2 ||
			got.Cycles[0].Description != "" || got.Cycles[1].Name != "" ||
			!got.Cycles[0].EndsAt.Equal(at) || !got.CycleSettings["team"].AutoMigrate {
			t.Fatal("native headers changed occurrence order, selected values or body omission")
		}
		if prior != 0 && cost != prior {
			t.Fatalf("opaque cycle body entered header allowance: before=%d after=%d", prior, cost)
		}
		prior = cost
		t.Logf("cycle body=%d bytes; header allowance=%d", size, cost)
		owner.Close()
		source.Release()
		if view.metadataResidency.work.used != 0 {
			t.Fatal("header allowance leaked")
		}
	}
}

func TestScaleP21SW194CycleCloneRefusal(t *testing.T) {
	view := &SQLiteStore{metadataResidency: newMetadataResidency()}
	data := domain.Bootstrap{Issues: []domain.Issue{{ID: "issue"}}}
	view.metadataResidency.work.limit = 1
	owner := &ResourceRead{store: view}
	if err := reserveCycleScheduleClone(t.Context(), owner, data); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("quota did not reject before clone", err)
	}
	owner.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	owner = &ResourceRead{store: view}
	if err := reserveCycleScheduleClone(ctx, owner, data); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled clone lost context", err)
	}
	owner.Close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("refused clone retained allowance")
	}
}
