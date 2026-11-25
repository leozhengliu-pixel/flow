package main

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW51ChatGroupBindingsDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw51_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams,
				domain.Team{ID: "sw51-public"},
				domain.Team{ID: "sw51-public", ArchivedAt: &stamp},
				domain.Team{ID: "sw51-private", Private: true},
				domain.Team{ID: "sw51-child"},
				domain.Team{ID: "sw51-cycle"},
				domain.Team{ID: "sw51-retired", RetiredAt: &stamp})
			data.TeamSettings["sw51-child"] = domain.TeamSettings{ParentTeamID: "sw51-private"}
			data.TeamSettings["sw51-cycle"] = domain.TeamSettings{ParentTeamID: "sw51-cycle"}
			data.Projects = append(data.Projects,
				domain.Project{ID: "sw51-project", TeamIDs: []string{"sw51-public"}},
				domain.Project{ID: "sw51-project", TeamIDs: []string{"sw51-private"}, ArchivedAt: &stamp},
				domain.Project{ID: "sw51-mixed-project", TeamIDs: []string{"sw51-public", "sw51-private"}})
			data.IssueTemplates = append(data.IssueTemplates,
				domain.IssueTemplate{ID: "sw51-template", TeamID: "sw51-public", ProjectID: "sw51-project"},
				domain.IssueTemplate{ID: "sw51-duplicate"},
				domain.IssueTemplate{ID: "sw51-duplicate", FormFields: []domain.TemplateFormField{{ID: "field"}}},
				domain.IssueTemplate{ID: "sw51-foreign", VisibilityTeamID: "sw51-private"})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		record := domain.ChatIntegrationRecord{WorkspaceKey: workspace, Provider: "lark", ConnectionID: idgen.New("sw51-connection-"), AppID: "sw51-app", TenantKey: "sw51-tenant", Connected: true}
		if err := repo.PutChatIntegration(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		prefix := idgen.New("sw51-binding-")
		bindings := []domain.ChatBinding{
			{ID: prefix + "-b", ChatID: "allowed", ProjectID: "sw51-project", TemplateID: "sw51-template", ChatName: "Exact group", AllowCreate: true, Events: []string{"issue.created"}, Language: chatLanguageEnglish, CreatedAt: stamp},
			{ID: prefix + "-a", ChatID: "default"},
			{ID: prefix + "-disabled", ChatID: "disabled"},
			{ID: prefix + "-revoked", ChatID: "revoked", RevokedAt: &stamp, RevokedBy: "retained"},
			{ID: prefix + "-duplicate", ChatID: "duplicate", TemplateID: "sw51-duplicate"},
			{ID: prefix + "-missing-template", ChatID: "missing-template", TemplateID: "sw51-missing"},
			{ID: prefix + "-foreign-template", ChatID: "foreign-template", TemplateID: "sw51-foreign"},
			{ID: prefix + "-mixed-project", ChatID: "mixed-project", ProjectIDs: []string{"sw51-mixed-project"}},
			{ID: prefix + "-missing-project", ChatID: "missing-project", ProjectID: "sw51-missing"},
			{ID: prefix + "-missing-team", ChatID: "missing-team", TeamID: "sw51-missing"},
			{ID: prefix + "-retired", ChatID: "retired", TeamID: "sw51-retired"},
			{ID: prefix + "-cycle", ChatID: "cycle", TeamID: "sw51-cycle"},
			{ID: prefix + "-inherited-private", ChatID: "inherited-private", TeamID: "sw51-child"},
			{ID: prefix + "-private", ChatID: "private", TeamID: "sw51-private", Authorization: &domain.ChatBindingAuthorization{UserID: f.data.Viewer.ID, Role: domain.ChatBindingRoleWorkspaceAdmin, PrivateTeam: true, AuthorizedAt: stamp}},
			{ID: prefix + "-lost-authorizer", ChatID: "lost-authorizer", TeamID: "sw51-private", Authorization: &domain.ChatBindingAuthorization{UserID: "sw51-missing-user", PrivateTeam: true}},
		}
		for _, binding := range bindings {
			binding.ConnectionID = record.ConnectionID
			if binding.TeamID == "" {
				binding.TeamID = "sw51-public"
			}
			binding.Enabled = binding.ID != prefix+"-disabled"
			if err := repo.PutChatBinding(t.Context(), workspace, binding); err != nil {
				t.Fatal(err)
			}
		}
		if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			name     string
			wantIDs  []string
			missing  bool
			canceled bool
			hipaa    bool
		}{
			{name: "allowed", wantIDs: []string{prefix + "-b"}},
			{name: "default", wantIDs: []string{prefix + "-a"}},
			{name: "disabled"},
			{name: "revoked"},
			{name: "duplicate"},
			{name: "missing-template"},
			{name: "foreign-template"},
			{name: "mixed-project"},
			{name: "missing-project"},
			{name: "missing-team"},
			{name: "retired"},
			{name: "cycle"},
			{name: "inherited-private"},
			{name: "private", wantIDs: []string{prefix + "-private"}},
			{name: "lost-authorizer"},
			{name: "no-bindings"},
			{name: "missing-workspace", missing: true},
			{name: "canceled", canceled: true},
			{name: "hipaa", hipaa: true},
		} {
			t.Run(test.name, func(t *testing.T) {
				current := record
				chatID := test.name
				if test.missing {
					current.WorkspaceKey += "-missing"
				}
				if test.hipaa {
					chatID = "allowed"
					if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw51_hipaa", "", nil, func(data *domain.Bootstrap) error {
						data.WorkspaceSettings.HIPAACompliance = true
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
				}
				ctx := t.Context()
				if test.canceled {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				var outputs [][]domain.ChatBinding
				var messages []string
				for _, source := range []*store.SQLiteStore{repo, probe} {
					before := source.WorkspaceMetadataDecodes()
					got, err := (&server{store: source}).chatGroupBindings(ctx, current, chatID)
					if test.canceled != errors.Is(err, context.Canceled) {
						t.Fatal("cancellation changed", source.BoundedMetadataEnabled(), err)
					}
					message := ""
					if err != nil {
						message = err.Error()
					}
					if !test.canceled && err != nil {
						t.Fatal(err)
					}
					var ids []string
					for _, binding := range got {
						ids = append(ids, binding.ID)
					}
					if !slices.Equal(ids, test.wantIDs) {
						t.Fatal("binding selection/order changed", ids, test.wantIDs)
					}
					if source.MetadataWorkUsedForTesting() != 0 || source == probe && source.WorkspaceMetadataDecodes() != before {
						t.Fatal("binding read leaked work or decoded full metadata")
					}
					outputs = append(outputs, got)
					messages = append(messages, message)
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) || messages[0] != messages[1] {
					t.Fatal("actual legacy/bounded binding output differs", outputs, messages)
				}
			})
		}
	}
}
