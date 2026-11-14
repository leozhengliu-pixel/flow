package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// Canonical witnesses deliberately bypass the installed logical source. They
// continue testing why an arbitrary SQL substitution cannot certify a reader.
func scaleP21SW30CanonicalMetadataWitness(ctx context.Context, s *SQLiteStore, workspace string, scope MetadataRecordScope) (domain.Bootstrap, bool, error) {
	hydrated, err := s.HydrateWorkspaceMetadata(ctx, workspace, scope)
	return hydrated.Data, err == nil, err
}

func TestScaleP21SW30LogicalReloadLifecycle(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	id := idgen.New("sw30_emoji_")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "customEmojis"), workspace, "application_policy.updated", id, nil, func(data *domain.Bootstrap) error {
		data.CustomEmojis = []domain.CustomEmoji{{ID: id, Name: "warm\x00name"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	before, ok, err := view.WorkspaceMetadataScoped(t.Context(), workspace, MetadataRecordScope{Fields: []string{"customEmojis"}})
	if err != nil || !ok || before.CustomEmojis[0].Name != "warm\x00name" {
		t.Fatalf("warm source lost accepted value: %#v %v", before.CustomEmojis, err)
	}
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := view.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	expected, _ := f.WorkspaceMetadataFields(workspace, "customEmojis", "teams", "teamSettings", "workspaceSettings")
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	actual, err := source.Select(t.Context(), MetadataRecordScope{
		Fields: []string{"customEmojis", "teams", "teamSettings", "workspaceSettings"},
	}, metadataPolicyBytes)
	if err != nil {
		t.Fatal(err)
	}
	expected.Viewer, expected.ViewerRole = actual.Viewer, actual.ViewerRole
	if !reflect.DeepEqual(expected, actual) {
		t.Fatal("bounded reload differs from literal old normalized reload")
	}
	stamp, err := readMetadataStamp(t.Context(), view.db, workspace)
	if err != nil || source.Stamp() != stamp {
		t.Fatalf("cold source stamp=%+v actual=%+v err=%v", source.Stamp(), stamp, err)
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("bounded reload decoded complete metadata or leaked work")
	}
}

func TestScaleP21SW30LogicalWriterDefaultWorkspace(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, policy := range []bool{false, true} {
		t.Run(map[bool]string{false: "record", true: "policy"}[policy], func(t *testing.T) {
			f := scaleP09Database(t)
			workspace := f.data.Workspace.URLKey
			second := idgen.New("sw30_default_")
			if _, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "Second default workspace", second, "us"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := f.DeleteWorkspace(context.Background(), second); err != nil {
					t.Error(err)
				}
			})
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace, second)
			if err != nil {
				t.Fatal(err)
			}
			scope := MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "sw30-default"}}}
			callback := func(data *domain.Bootstrap) error {
				data.Settings["sw30-default"] = "changed"
				return nil
			}
			if policy {
				scope = MetadataRecordScope{Write: []RecordKey{{Kind: "workspaceSettings", ID: "fiscalMonth"}}}
				callback = func(data *domain.Bootstrap) error {
					data.WorkspaceSettings.FiscalMonth = "October"
					return nil
				}
			}
			for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
				if err := source.MutateWorkspaceScoped(t.Context(), workspace, "application_policy.updated", "", nil, scope, callback); err != nil {
					t.Fatal(err)
				}
				source.mu.RLock()
				selected := source.lastWorkspaceKey
				source.mu.RUnlock()
				if selected != workspace {
					t.Fatalf("committed write left default workspace %q, want %q", selected, workspace)
				}
			}
			actual, found, err := view.WorkspaceMetadataScoped(t.Context(), "", MetadataRecordScope{})
			if err != nil || !found || actual.Workspace.URLKey != workspace {
				t.Fatalf("default accessor selected wrong logical source: %q %v", actual.Workspace.URLKey, err)
			}
		})
	}
}

func TestScaleP21SW30LogicalNormalizationDifferential(t *testing.T) {
	when := time.Date(2026, 11, 13, 1, 0, 0, 0, time.UTC)
	current := domain.User{ID: "usr_current", Name: "Current", DisplayName: "Current name"}
	stale := current
	stale.Name, stale.DisplayName = "Stale", "Stale name"
	fixtures := []struct {
		name string
		data domain.Bootstrap
	}{
		{"nil-defaults", domain.Bootstrap{Viewer: current, Users: []domain.User{current}, Teams: []domain.Team{{ID: "team_one"}}}},
		{"references-counts-and-order", domain.Bootstrap{
			Viewer: current, Users: []domain.User{stale, current},
			States: []domain.WorkflowState{
				{ID: "state_later", TeamID: "team_two", Default: true},
				{ID: "state_backlog", Type: "backlog"},
				{ID: "state_team", TeamID: "team_one", Default: true},
			},
			Teams: []domain.Team{{ID: "team_one"}, {ID: "team_two"}},
			TeamSettings: map[string]domain.TeamSettings{
				"team_one": {TeamID: "team_one"},
				"orphan":   {Access: "unchanged"},
			},
			CycleSettings: map[string]domain.CycleSettings{"team_one": {DurationWeeks: 2}},
			Labels: []domain.IssueLabel{
				{ID: "label_one", IssueCount: 99},
				{ID: "label_two", ResourceType: "project", IssueCount: 11},
			},
			ProjectStatuses: []domain.ProjectStatus{{ID: "status_one", Name: "First"}, {ID: "status_two", Name: "Second"}},
			Projects: []domain.Project{{
				ID: "project_one", Lead: &stale, Creator: &stale, Status: domain.ProjectStatus{ID: "status_two", Name: "Stale"},
				LabelIDs: []string{"label_one", "label_one", ""}, IssueCount: 19,
			}},
			Initiatives: []domain.Initiative{{ID: "initiative_one", Owner: &stale, LabelIDs: []string{"label_one", "label_two"}}},
			ProjectUpdates: map[string][]domain.ProjectUpdate{
				"project_one": {{ID: "update_one", User: stale}},
			},
			InitiativeUpdates: map[string][]domain.InitiativeUpdate{
				"initiative_one": {{ID: "initiative_update", User: stale}},
			},
			Members: []domain.WorkspaceMember{{User: stale}},
		}},
		{"pipelines-and-record-defaults", domain.Bootstrap{
			Viewer: current,
			ReleasePipelines: []domain.ReleasePipeline{
				{ID: "pipeline_one", Name: "Same", Stages: []string{"In progress", "Released"}},
				{ID: "pipeline_two", Name: "Same"},
				{ID: "pipeline_three"},
				{ID: "pipeline_four", SlugID: "same-3"},
				{ID: "pipeline_five", Name: "Same"},
			},
			Releases: []domain.Release{{ID: "release_one", Name: "Release name", CreatedAt: when}},
			Documents: []domain.Document{{
				ID: "document_one", Creator: current, CreatedAt: when, Content: "legal body",
			}},
			IssueTemplates:         []domain.IssueTemplate{{ID: "template_one"}},
			IntegrationConnections: []domain.IntegrationConnection{{ID: "connection_one"}},
			APIKeys:                []domain.APIKey{{ID: "key_one", TeamIDs: []string{"team_one"}}},
			Webhooks:               []domain.Webhook{{ID: "webhook_one", TeamIDs: []string{"team_one"}}},
		}},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			original := cloneBootstrap(fixture.data)
			started := time.Now().UTC()
			expected := cloneBootstrap(original)
			normalizeStoredMetadata(&expected)
			raw, err := captureMetadataLogicalSource(t.Context(), original, copyStamp{workspaceID: "normalization", revision: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Release()
			store := &SQLiteStore{metadataResidency: newMetadataResidency()}
			normalized, err := store.normalizeMetadataLogicalSource(t.Context(), raw)
			if err != nil {
				t.Fatal(err)
			}
			defer normalized.Release()
			var actual domain.Bootstrap
			out := reflect.ValueOf(&actual).Elem()
			if err := normalized.VisitFields(t.Context(), func(name string, node metadataLogicalNode) error {
				if name == "-" {
					return nil
				}
				index, _, _ := metadataLogicalField(name)
				value, err := node.Value(t.Context(), metadataWorkBytes)
				if err == nil {
					out.Field(index).Set(value)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			// These timestamps are generated independently by the literal
			// normalizer. All fields around them remain in the comparison.
			if original.WorkspaceSettings.SessionDurationDays == 0 {
				expected.WorkspaceSettings.UpdatedAt, actual.WorkspaceSettings.UpdatedAt = time.Time{}, time.Time{}
			}
			for id := range expected.UserSettings {
				if _, existed := original.UserSettings[id]; !existed {
					a, b := expected.UserSettings[id], actual.UserSettings[id]
					a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
					expected.UserSettings[id], actual.UserSettings[id] = a, b
				}
			}
			for id := range expected.NotificationPreferences {
				if _, existed := original.NotificationPreferences[id]; !existed {
					a, b := expected.NotificationPreferences[id], actual.NotificationPreferences[id]
					if a.UpdatedAt.Before(started) || b.UpdatedAt.Before(started) || a.UpdatedAt.After(time.Now()) || b.UpdatedAt.After(time.Now()) {
						t.Fatal("generated notification preference timestamp escaped the operation")
					}
					a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
					expected.NotificationPreferences[id], actual.NotificationPreferences[id] = a, b
				}
			}
			if original.CustomerStatuses == nil {
				for i := range expected.CustomerStatuses {
					expected.CustomerStatuses[i].CreatedAt, actual.CustomerStatuses[i].CreatedAt = time.Time{}, time.Time{}
					expected.CustomerStatuses[i].UpdatedAt, actual.CustomerStatuses[i].UpdatedAt = time.Time{}, time.Time{}
				}
			}
			for i := 0; i < out.NumField(); i++ {
				a, b := reflect.ValueOf(expected).Field(i), out.Field(i)
				if !reflect.DeepEqual(a.Interface(), b.Interface()) {
					want, _ := json.Marshal(a.Interface())
					got, _ := json.Marshal(b.Interface())
					t.Errorf("%s differs\nold=%s\nnew=%s", out.Type().Field(i).Name, want, got)
				}
			}
			if store.metadataResidency.work.used != 0 {
				t.Fatal("normalization leaked work admission")
			}
		})
	}
}
