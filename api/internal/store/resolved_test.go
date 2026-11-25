package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW0ResolvedReferenceDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	projectID, userID := idgen.New("project_resolved_"), idgen.New("usr_resolved_")
	fresh := domain.User{ID: userID, Name: "Fresh lead", DisplayName: "Fresh lead", Active: true, Email: "resolved@example.test"}
	stale := fresh
	stale.Name, stale.DisplayName = "Stored old lead", "Stored old lead"
	reset := func() {
		t.Helper()
		f.SetWebhookSink(nil)
		f.SetRealtimeSink(nil)
		if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "labels", "projects", "users", "webhooks"), workspace, "issue_label.updated", "", nil, func(data *domain.Bootstrap) error {
			data.Labels = []domain.IssueLabel{{ID: projectID, Name: "Before", Scope: "workspace"}}
			data.Users = []domain.User{fresh}
			data.Projects = []domain.Project{{ID: projectID, Name: "Aggregate", Lead: &stale}}
			for range 65 {
				data.Projects = append(data.Projects, domain.Project{ID: idgen.New("project_unrelated_"), Name: "Retained", Description: strings.Repeat("unrelated ", 500)})
			}
			data.Webhooks = []domain.Webhook{{ID: idgen.New("hook_resolved_"), Enabled: true, URL: "https://example.test/hook"}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	row := func(kind, id string) []byte {
		t.Helper()
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, kind, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var events []domain.DomainEvent
	var realtime []domain.RealtimeEvent
	var outcomes [][]byte
	for _, bounded := range []bool{false, true} {
		reset()
		repo := f.SQLiteStore
		if bounded {
			var err error
			repo, err = f.BoundedMetadataViewForTesting(t.Context())
			if err != nil {
				t.Fatal(err)
			}
		}
		beforeProject := string(row("projects", projectID))
		repo.SetWebhookSink(func(_ string, event domain.DomainEvent) { events = append(events, event) })
		repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = append(realtime, event) })
		resolved := false
		scope := MetadataResolvedScope{
			MetadataRecordScope: MetadataRecordScope{
				Read:  []RecordKey{{Kind: "projects", ID: projectID}},
				Write: []RecordKey{{Kind: "labels", ID: projectID}},
			},
			ResolveRead: func(data domain.Bootstrap) ([]RecordKey, error) {
				resolved = true
				if len(data.Projects) != 1 || data.Projects[0].Lead == nil {
					t.Fatal("resolver did not receive the initial selection")
				}
				id := data.Projects[0].Lead.ID
				data.Projects[0].Name = "Planner mutation must stay private"
				return []RecordKey{{Kind: "users", ID: id}, {Kind: "users", ID: id}}, nil
			},
		}
		if err := repo.MutateWorkspaceResolved(t.Context(), workspace, "issue_label.updated", projectID, map[string]string{"name": "After"}, scope, func(data *domain.Bootstrap) error {
			if bounded && (len(data.Projects) != 1 || len(data.Users) != 1 || data.Projects[0].Name != "Aggregate" || data.Projects[0].Lead.Name != fresh.Name) {
				t.Fatal("resolved callback lost owned input or refreshed reference")
			}
			data.Labels[0].Name = "After"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if resolved != bounded || len(events) != len(outcomes)+1 || len(realtime) != len(outcomes)+1 {
			t.Fatal("legacy resolver dispatch or publication changed")
		}
		if string(row("projects", projectID)) != beforeProject {
			t.Fatal("event-only reference refresh rewrote the read-only project")
		}
		outcomes = append(outcomes, row("labels", projectID))
		if bounded && (repo.WorkspaceMetadataDecodes() != 0 || repo.metadataResidency.work.used != 0) {
			t.Fatal("resolved writer decoded full metadata or leaked work")
		}
	}
	if !reflect.DeepEqual(outcomes[0], outcomes[1]) || !jsonEqual(events[0].PreviousValues, events[1].PreviousValues) ||
		!jsonEqual(events[0].Snapshot, events[1].Snapshot) || !jsonEqual(realtime[0].Payload, realtime[1].Payload) {
		t.Fatalf("resolved canonical/event differential: previous=%s/%s snapshots=%s/%s realtime=%s/%s",
			events[0].PreviousValues, events[1].PreviousValues, events[0].Snapshot, events[1].Snapshot, realtime[0].Payload, realtime[1].Payload)
	}
	var payload struct {
		Entity domain.Project `json:"entity"`
	}
	if err := json.Unmarshal(realtime[1].Payload, &payload); err != nil || payload.Entity.Lead == nil || payload.Entity.Lead.Name != fresh.Name || len(events[1].PreviousValues) == 0 {
		t.Fatal("resolved event lost legacy previous/reference values", err)
	}
}

func TestScaleP21SW0ResolvedRejectsAndDispatch(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	id := idgen.New("project_resolved_guard_")
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.resolved_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: id, Name: "Retained"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	observed := 0
	view.SetRealtimeSink(func(string, domain.RealtimeEvent) { observed++ })
	callbackError := errors.New("resolver failure")
	for _, spec := range []struct {
		name    string
		resolve func(domain.Bootstrap) ([]RecordKey, error)
		mutate  func(*domain.Bootstrap) error
		want    error
	}{
		{"planner-error", func(domain.Bootstrap) ([]RecordKey, error) { return nil, callbackError }, func(*domain.Bootstrap) error { t.Fatal("called after resolver error"); return nil }, callbackError},
		{"invalid-key", func(domain.Bootstrap) ([]RecordKey, error) { return []RecordKey{{Kind: "invalid", ID: "x"}}, nil }, func(*domain.Bootstrap) error { t.Fatal("called after invalid scope"); return nil }, ErrMetadataRecordQuery},
		{"too-many", func(domain.Bootstrap) ([]RecordKey, error) {
			keys := []RecordKey{}
			for range MetadataRecordBatchMaxKeys {
				keys = append(keys, RecordKey{Kind: "projects", ID: idgen.New("missing_")})
			}
			return keys, nil
		}, func(*domain.Bootstrap) error { t.Fatal("called after oversized scope"); return nil }, ErrMetadataPageBudget},
		{"read-only", func(domain.Bootstrap) ([]RecordKey, error) { return nil, nil }, func(data *domain.Bootstrap) error { data.Projects[0].Name = "Forbidden"; return nil }, ErrMetadataRecordQuery},
		{"callback-error", func(domain.Bootstrap) ([]RecordKey, error) { return nil, nil }, func(*domain.Bootstrap) error { return callbackError }, callbackError},
	} {
		t.Run(spec.name, func(t *testing.T) {
			err := view.MutateWorkspaceResolved(t.Context(), workspace, "issue_label.updated", id, nil, MetadataResolvedScope{MetadataRecordScope: MetadataRecordScope{Read: []RecordKey{{Kind: "projects", ID: id}}}, ResolveRead: spec.resolve}, spec.mutate)
			if !errors.Is(err, spec.want) || observed != 0 || view.metadataResidency.work.used != 0 {
				t.Fatalf("err=%v want=%v publications=%d", err, spec.want, observed)
			}
		})
	}
	declared := MetadataResolvedScope{MetadataRecordScope: MetadataRecordScope{
		Read: []RecordKey{{Kind: "projects", ID: id}}, Write: []RecordKey{{Kind: "labels", ID: id}},
	}}
	declared.ResolveRead = func(domain.Bootstrap) ([]RecordKey, error) {
		declared.Write[0] = RecordKey{Kind: "projects", ID: id}
		return nil, nil
	}
	if err := view.MutateWorkspaceResolved(t.Context(), workspace, "issue_label.updated", id, nil, declared, func(data *domain.Bootstrap) error {
		data.Projects[0].Name = "Scope alias must not grant a write"
		return nil
	}); !errors.Is(err, ErrMetadataRecordQuery) || observed != 0 {
		t.Fatal("resolver changed the caller's snapshotted write declaration", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := view.MutateWorkspaceResolved(ctx, workspace, "issue_label.updated", id, nil, MetadataResolvedScope{}, func(*domain.Bootstrap) error { t.Fatal("cancelled callback"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	called := false
	err = f.MutateWorkspaceResolvedWithAggregate(t.Context(), workspace, "issue_label.updated", nil, MetadataResolvedScope{
		MetadataRecordScope: MetadataRecordScope{Read: []RecordKey{{Kind: "invalid", ID: "x"}}},
		ResolveRead:         func(domain.Bootstrap) ([]RecordKey, error) { t.Fatal("off-mode planner"); return nil, nil },
	}, func(data *domain.Bootstrap) (string, error) {
		called = true
		return "", ErrNoMutation
	})
	if err != nil || !called {
		t.Fatal("off-mode dispatch changed", err)
	}
	resolved := false
	err = view.MutateWorkspaceResolvedWithAggregate(t.Context(), workspace, "issue_label.updated", nil, MetadataResolvedScope{
		MetadataRecordScope: MetadataRecordScope{Write: []RecordKey{{Kind: "projects", ID: id}}},
		ResolveRead: func(domain.Bootstrap) ([]RecordKey, error) {
			resolved = true
			return nil, nil
		},
	}, func(data *domain.Bootstrap) (string, error) {
		data.Projects[0].Name = "Aggregate success after failures"
		return id, nil
	})
	if err != nil || !resolved || observed != 1 || view.metadataResidency.work.used != 0 {
		t.Fatal("bounded aggregate dispatch did not recover", err)
	}
}
