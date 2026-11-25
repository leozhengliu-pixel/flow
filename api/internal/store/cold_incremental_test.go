package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// coldIncrementalFixture is a bounded store (a production-coverage view of a
// journaled database) whose installed image is a fresh SQL reload, with a
// workspace holding many metadata collections.
func coldIncrementalFixture(t *testing.T) (scaleP09Fixture, *SQLiteStore, string) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	at := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.cold_incremental_seed", "", nil, func(data *domain.Bootstrap) error {
		for i := range 4 {
			data.Labels = append(data.Labels, domain.IssueLabel{ID: fmt.Sprintf("ci-label-%d", i), Name: fmt.Sprintf("Label %d", i), Color: "#aabbcc", CreatedAt: at})
		}
		status := domain.ProjectStatus{}
		if len(data.ProjectStatuses) > 0 {
			status = data.ProjectStatuses[0]
		}
		for i := range 12 {
			project := domain.Project{ID: fmt.Sprintf("ci-project-%02d", i), Name: fmt.Sprintf("Project %d", i), SlugID: fmt.Sprintf("project-%d-%012x", i, i),
				Color: "#112233", Status: status, LabelIDs: []string{fmt.Sprintf("ci-label-%d", i%4)}, CreatedAt: at, UpdatedAt: at}
			// Leads and creators: a user change reaches only these projects.
			if len(data.Users) > 0 && i%3 == 0 {
				lead := data.Users[i%len(data.Users)]
				project.Lead = &lead
			}
			if len(data.Users) > 0 && i%4 == 1 {
				creator := data.Users[(i+1)%len(data.Users)]
				project.Creator = &creator
			}
			if i == 5 {
				// An escaped character: the row is read whatever user changed.
				project.Description = "R&D <notes>"
			}
			data.Projects = append(data.Projects, project)
		}
		for i := range 5 {
			data.SavedViews = append(data.SavedViews, domain.SavedView{ID: fmt.Sprintf("ci-view-%d", i), Name: fmt.Sprintf("View %d", i), Resource: "issues", Scope: "workspace"})
			data.APIKeys = append(data.APIKeys, domain.APIKey{ID: fmt.Sprintf("ci-key-%d", i), Name: fmt.Sprintf("Key %d", i), Prefix: fmt.Sprintf("flw_%d", i), Scopes: []string{"read"}, TeamIDs: []string{}, CreatedAt: at})
			data.Webhooks = append(data.Webhooks, domain.Webhook{ID: fmt.Sprintf("ci-hook-%d", i), Name: fmt.Sprintf("Hook %d", i), URL: "https://example.test/hook", ResourceTypes: []string{"Issue"}, TeamIDs: []string{}, Enabled: true})
			data.WorkflowDefinitions = append(data.WorkflowDefinitions, domain.WorkflowDefinition{ID: fmt.Sprintf("ci-flow-%d", i), Name: fmt.Sprintf("Flow %d", i), Enabled: true})
			data.ImportJobs = append(data.ImportJobs, domain.ImportJob{ID: fmt.Sprintf("ci-import-%d", i), Filename: "a.csv", Format: "csv", Status: "pending", Headers: []string{"title"}, Errors: []string{}, CreatedAt: at, UpdatedAt: at})
			data.Trash = append(data.Trash, domain.TrashEntry{ID: fmt.Sprintf("ci-trash-%d", i), ResourceType: "document", ResourceID: fmt.Sprintf("gone-%d", i), Title: "Gone", Payload: json.RawMessage(`{"a":1}`), DeletedAt: at, ExpiresAt: at.Add(720 * time.Hour)})
			data.Documents = append(data.Documents, domain.Document{ID: fmt.Sprintf("ci-doc-%d", i), SlugID: fmt.Sprintf("doc-%d", i), Title: fmt.Sprintf("Doc %d", i), Content: "body", CreatedAt: at, UpdatedAt: at})
			data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{ID: fmt.Sprintf("ci-app-%d", i), Name: fmt.Sprintf("App %d", i), ClientID: fmt.Sprintf("client-%d", i), RedirectURIs: []string{"https://example.test/cb"}, Scopes: []string{"read"}, CreatedAt: at})
		}
		data.Settings["ci-setting"] = map[string]any{"value": "before", "n": 1.0}
		if len(data.Teams) > 0 {
			for i, kind := range []string{"unstarted", "started"} {
				data.States = append(data.States, domain.WorkflowState{ID: fmt.Sprintf("ci-state-%d", i), TeamID: data.Teams[0].ID, Name: fmt.Sprintf("State %d", i), Type: kind, Color: "#445566", Position: float64(10 + i)})
			}
		}
		data.Initiatives = append(data.Initiatives,
			domain.Initiative{ID: "ci-initiative-0", Name: "Viewer created", LabelIDs: []string{"ci-label-1"}},
			domain.Initiative{ID: "ci-initiative-1", Name: "Owned", Creator: data.Viewer, LabelIDs: []string{}})
		for i := range 2 {
			loop := fmt.Sprintf("ci-loop-%d", i)
			data.Loops = append(data.Loops, domain.Loop{ID: loop, Name: fmt.Sprintf("Loop %d", i), Instructions: "instructions", ConnectorIDs: []string{}, TriggerConfig: map[string]any{"every": "day"}})
			data.LoopVersions = append(data.LoopVersions, domain.LoopVersion{ID: loop + "-v1", LoopID: loop, Version: 1, Current: true})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	scaleP21CompleteWorkspaceCatalog(t, f, workspace)
	f.db.boundedCatalog.Store(true)
	t.Cleanup(func() { f.db.boundedCatalog.Store(false) })
	view := coldIncrementalReloadedView(t, f, workspace)
	return f, view, workspace
}

// coldIncrementalReloadedView is a production-coverage bounded view whose
// installed image of workspace is a fresh SQL reload.
func coldIncrementalReloadedView(t *testing.T, f scaleP09Fixture, workspace string) *SQLiteStore {
	t.Helper()
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	view.metadataResidency.requireCoverage = true
	view.writeMu.Lock()
	view.dropResidentWorkspace(workspace)
	err = view.reloadResidentWorkspaceLogical(t.Context(), workspace, true)
	view.writeMu.Unlock()
	if err != nil {
		t.Fatal("SQL reload", err)
	}
	return view
}

// coldIncrementalAssertExact compares view's installed image of workspace
// with a fresh SQL reload: the decoded image, the stamp and the policy.
func coldIncrementalAssertExact(t *testing.T, f scaleP09Fixture, view *SQLiteStore, workspace, label string) domain.Bootstrap {
	t.Helper()
	got := scaleP21ColdTeamNative(t, view, workspace)
	fresh := coldIncrementalReloadedView(t, f, workspace)
	want := scaleP21ColdTeamNative(t, fresh, workspace)
	if !reflect.DeepEqual(got, want) {
		gv, wv := reflect.ValueOf(got), reflect.ValueOf(want)
		for i := 0; i < gv.NumField(); i++ {
			g, w := gv.Field(i), wv.Field(i)
			if reflect.DeepEqual(g.Interface(), w.Interface()) {
				continue
			}
			if g.Kind() == reflect.Slice && w.Kind() == reflect.Slice {
				t.Errorf("%s: field %s differs from a fresh reload (len %d, want %d)", label, gv.Type().Field(i).Name, g.Len(), w.Len())
				for j := 0; j < min(g.Len(), w.Len()); j++ {
					if !reflect.DeepEqual(g.Index(j).Interface(), w.Index(j).Interface()) {
						t.Errorf("  item %d:\n got  %#v\n want %#v", j, g.Index(j).Interface(), w.Index(j).Interface())
						break
					}
				}
				continue
			}
			t.Errorf("%s: field %s differs from a fresh reload:\n got  %#v\n want %#v", label, gv.Type().Field(i).Name, g.Interface(), w.Interface())
		}
		t.FailNow()
	}
	stamp, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil {
		t.Fatal(err)
	}
	// (The journal head a writer's own exact publication records is its
	// own: installColdFullWriteLocked records 0.)
	view.metadataResidency.mu.Lock()
	installed := view.metadataResidency.logical[workspace].Stamp()
	policy := view.metadataResidency.policy[workspace]
	view.metadataResidency.mu.Unlock()
	fresh.metadataResidency.mu.Lock()
	freshPolicy := fresh.metadataResidency.policy[workspace]
	fresh.metadataResidency.mu.Unlock()
	if installed != stamp || policy != freshPolicy {
		t.Fatalf("%s: installed stamp %v policy %d; SQL stamp %v, reload policy %d", label, installed, policy, stamp, freshPolicy)
	}
	view.mu.RLock()
	resident := view.workspaces[workspace]
	view.mu.RUnlock()
	fresh.mu.RLock()
	freshResident := fresh.workspaces[workspace]
	fresh.mu.RUnlock()
	if !reflect.DeepEqual(resident, freshResident) {
		t.Fatalf("%s: resident policy copy differs from a fresh reload", label)
	}
	return got
}

type coldIncrementalWrite struct {
	name   string
	ctx    func(context.Context) context.Context
	event  string
	mutate func(*domain.Bootstrap) error
	// aggregate and payload select the special legacy writers (loop output
	// contract, feature flags, user settings).
	aggregate string
	payload   any
}

// coldIncrementalWrites are COLD legacy writers over the collections the
// default-COLD routes write: settings, admin (members, users, team and
// workspace settings), deletes, OAuth applications and API keys, webhooks,
// workflows, imports, trash, projects, labels, views and documents.
func coldIncrementalWrites(rng *rand.Rand, step int) []coldIncrementalWrite {
	at := time.Date(2026, 11, 19, 9, 0, step, 0, time.UTC)
	id := func(prefix string) string { return fmt.Sprintf("%s-%d-%d", prefix, step, rng.IntN(1<<20)) }
	pick := func(n int) int {
		if n == 0 {
			return 0
		}
		return rng.IntN(n)
	}
	plain := func(ctx context.Context) context.Context { return ctx }
	fields := func(names ...string) func(context.Context) context.Context {
		return func(ctx context.Context) context.Context { return WithMetadataFields(ctx, names...) }
	}
	scoped := func(ctx context.Context) context.Context { return WithMutationScope(ctx, MutationScope{}) }
	return []coldIncrementalWrite{
		{"settings", plain, "application_policy.updated", func(data *domain.Bootstrap) error {
			data.Settings["ci-setting"] = map[string]any{"value": id("v"), "n": float64(step)}
			return nil
		}, "", nil},
		{"settings-new-key", fields("settings"), "application_policy.updated", func(data *domain.Bootstrap) error {
			data.Settings[id("ci-extra")] = map[string]any{"on": true}
			return nil
		}, "", nil},
		{"api-key-create", plain, "api_key.created", func(data *domain.Bootstrap) error {
			data.APIKeys = append(data.APIKeys, domain.APIKey{ID: id("key"), Name: "New key", Prefix: "flw_new", Scopes: []string{"read", "write"}, TeamIDs: []string{}, CreatedAt: at})
			return nil
		}, "", nil},
		{"api-key-revoke", scoped, "api_key.revoked", func(data *domain.Bootstrap) error {
			if len(data.APIKeys) > 0 {
				data.APIKeys[pick(len(data.APIKeys))].RevokedAt = &at
			}
			return nil
		}, "", nil},
		{"api-key-delete", plain, "api_key.deleted", func(data *domain.Bootstrap) error {
			if len(data.APIKeys) > 1 {
				i := pick(len(data.APIKeys))
				data.APIKeys = slices.Delete(data.APIKeys, i, i+1)
			}
			return nil
		}, "", nil},
		{"oauth-app-update", fields("oauthApplications"), "oauth_application.updated", func(data *domain.Bootstrap) error {
			if len(data.OAuthApplications) > 0 {
				data.OAuthApplications[pick(len(data.OAuthApplications))].Description = id("desc")
			}
			return nil
		}, "", nil},
		{"oauth-app-create", plain, "oauth_application.created", func(data *domain.Bootstrap) error {
			data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{ID: id("app"), Name: "Created", ClientID: id("client"), RedirectURIs: []string{}, Scopes: []string{"read"}, CreatedAt: at})
			return nil
		}, "", nil},
		{"webhook-delete", plain, "webhook.deleted", func(data *domain.Bootstrap) error {
			if len(data.Webhooks) > 1 {
				i := pick(len(data.Webhooks))
				data.Webhooks = slices.Delete(data.Webhooks, i, i+1)
			}
			return nil
		}, "", nil},
		{"webhook-create", scoped, "webhook.created", func(data *domain.Bootstrap) error {
			data.Webhooks = append(data.Webhooks, domain.Webhook{ID: id("hook"), Name: "Created", URL: "https://example.test/new", ResourceTypes: []string{"Project"}, TeamIDs: []string{}, Enabled: rng.IntN(2) == 0})
			return nil
		}, "", nil},
		{"workflow-update", fields("workflowDefinitions"), "workflow.updated", func(data *domain.Bootstrap) error {
			if len(data.WorkflowDefinitions) > 0 {
				i := pick(len(data.WorkflowDefinitions))
				data.WorkflowDefinitions[i].Name = id("flow")
				data.WorkflowDefinitions[i].Enabled = !data.WorkflowDefinitions[i].Enabled
			}
			return nil
		}, "", nil},
		{"workflow-create-delete", plain, "workflow.created", func(data *domain.Bootstrap) error {
			data.WorkflowDefinitions = append(data.WorkflowDefinitions, domain.WorkflowDefinition{ID: id("flow"), Name: "New flow", Enabled: true})
			if len(data.WorkflowDefinitions) > 3 {
				data.WorkflowDefinitions = slices.Delete(data.WorkflowDefinitions, 0, 1)
			}
			return nil
		}, "", nil},
		{"import-progress", plain, "import.updated", func(data *domain.Bootstrap) error {
			if len(data.ImportJobs) > 0 {
				i := pick(len(data.ImportJobs))
				data.ImportJobs[i].Status, data.ImportJobs[i].Imported, data.ImportJobs[i].UpdatedAt = "completed", step, at
				data.ImportJobs[i].Rows = []map[string]string{{"title": id("row")}}
			}
			return nil
		}, "", nil},
		{"import-create", plain, "import.created", func(data *domain.Bootstrap) error {
			data.ImportJobs = append(data.ImportJobs, domain.ImportJob{ID: id("import"), Filename: "b.csv", Format: "csv", Status: "pending", Headers: []string{"title"}, Errors: []string{}, CreatedAt: at, UpdatedAt: at})
			return nil
		}, "", nil},
		{"trash-add-restore", plain, "trash.updated", func(data *domain.Bootstrap) error {
			if len(data.Trash) > 2 {
				i := pick(len(data.Trash))
				data.Trash = slices.Delete(data.Trash, i, i+1)
			}
			data.Trash = append(data.Trash, domain.TrashEntry{ID: id("trash"), ResourceType: "view", ResourceID: id("view"), Title: "Deleted view", Payload: json.RawMessage(`{"b":2}`), DeletedAt: at, ExpiresAt: at.Add(720 * time.Hour)})
			return nil
		}, "", nil},
		{"project-update", plain, "project.updated", func(data *domain.Bootstrap) error {
			if len(data.Projects) > 0 {
				i := pick(len(data.Projects))
				data.Projects[i].Description, data.Projects[i].UpdatedAt = id("description"), at
			}
			return nil
		}, "", nil},
		{"project-labels", fields("projects"), "project.updated", func(data *domain.Bootstrap) error {
			if len(data.Projects) > 0 && len(data.Labels) > 0 {
				i := pick(len(data.Projects))
				data.Projects[i].LabelIDs = append(data.Projects[i].LabelIDs, data.Labels[pick(len(data.Labels))].ID)
			}
			return nil
		}, "", nil},
		{"project-archive", plain, "project.archived", func(data *domain.Bootstrap) error {
			if len(data.Projects) > 0 {
				i := pick(len(data.Projects))
				if data.Projects[i].ArchivedAt == nil {
					data.Projects[i].ArchivedAt = &at
				} else {
					data.Projects[i].ArchivedAt = nil
				}
			}
			return nil
		}, "", nil},
		{"project-create", plain, "project.created", func(data *domain.Bootstrap) error {
			project := domain.Project{ID: id("project"), Name: "Created", SlugID: id("created"), Color: "#000000", CreatedAt: at, UpdatedAt: at}
			if len(data.ProjectStatuses) > 0 {
				project.Status = data.ProjectStatuses[0]
			}
			data.Projects = append(data.Projects, project)
			return nil
		}, "", nil},
		{"label-create", plain, "label.created", func(data *domain.Bootstrap) error {
			data.Labels = append(data.Labels, domain.IssueLabel{ID: id("label"), Name: id("Label"), Color: "#ff0000", CreatedAt: at})
			return nil
		}, "", nil},
		{"label-rename", fields("labels"), "label.updated", func(data *domain.Bootstrap) error {
			if len(data.Labels) > 0 {
				data.Labels[pick(len(data.Labels))].Description = id("label description")
			}
			return nil
		}, "", nil},
		{"view-create", plain, "view.created", func(data *domain.Bootstrap) error {
			data.SavedViews = append(data.SavedViews, domain.SavedView{ID: id("view"), Name: "Created", Resource: "issues", Scope: "personal"})
			return nil
		}, "", nil},
		{"view-delete", plain, "view.deleted", func(data *domain.Bootstrap) error {
			if len(data.SavedViews) > 1 {
				i := pick(len(data.SavedViews))
				data.SavedViews = slices.Delete(data.SavedViews, i, i+1)
			}
			return nil
		}, "", nil},
		{"document-create", plain, "document.created", func(data *domain.Bootstrap) error {
			data.Documents = append(data.Documents, domain.Document{ID: id("doc"), SlugID: id("slug"), Title: "Created", Content: "new body", CreatedAt: at, UpdatedAt: at})
			return nil
		}, "", nil},
		{"document-delete", plain, "document.deleted", func(data *domain.Bootstrap) error {
			if len(data.Documents) > 1 {
				i := pick(len(data.Documents))
				data.Documents = slices.Delete(data.Documents, i, i+1)
			}
			return nil
		}, "", nil},
		{"member-role", plain, "member.role_updated", func(data *domain.Bootstrap) error {
			for i := range data.Members {
				if data.Members[i].Role != "owner" {
					data.Members[i].Role = []string{"admin", "member"}[pick(2)]
				}
			}
			return nil
		}, "", nil},
		{"team-settings", plain, "team.settings_updated", func(data *domain.Bootstrap) error {
			for key, value := range data.TeamSettings {
				value.Description = id("team description")
				data.TeamSettings[key] = value
				break
			}
			return nil
		}, "", nil},
		{"workspace-settings", plain, "workspace_settings.updated", func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.WelcomeMessage = id("Welcome")
			return nil
		}, "", nil},
		{"several", plain, "application_policy.updated", func(data *domain.Bootstrap) error {
			data.Settings["ci-setting"] = map[string]any{"value": id("many")}
			data.APIKeys = append(data.APIKeys, domain.APIKey{ID: id("key"), Name: "Several", Prefix: "flw_s", Scopes: []string{}, TeamIDs: []string{}, CreatedAt: at})
			if len(data.SavedViews) > 1 {
				data.SavedViews = data.SavedViews[1:]
			}
			if len(data.Projects) > 0 {
				data.Projects[0].Name = id("Renamed")
			}
			return nil
		}, "", nil},
		{"user-rename", plain, "user.updated", func(data *domain.Bootstrap) error {
			if len(data.Users) > 0 {
				data.Users[pick(len(data.Users))].DisplayName = id("Person")
			}
			return nil
		}, "", nil},
		{"user-add", plain, "user.created", func(data *domain.Bootstrap) error {
			// With stored settings: a synthesized default carries the
			// normalization's clock, which no two reloads share.
			user := id("user")
			data.Users = append(data.Users, domain.User{ID: user, Name: "Added", DisplayName: id("Added"), Email: id("added") + "@example.test"})
			settings, preferences := defaultUserSettings(user), defaultNotificationPreferences(user)
			settings.UpdatedAt, preferences.UpdatedAt = at, at
			if data.UserSettings == nil {
				data.UserSettings = map[string]domain.UserSettings{}
			}
			if data.NotificationPreferences == nil {
				data.NotificationPreferences = map[string]domain.NotificationPreferences{}
			}
			data.UserSettings[user], data.NotificationPreferences[user] = settings, preferences
			return nil
		}, "", nil},
		{"user-and-project", plain, "user.updated", func(data *domain.Bootstrap) error {
			if len(data.Users) > 0 && len(data.Projects) > 0 {
				user := &data.Users[pick(len(data.Users))]
				user.DisplayName = id("Both")
				project := &data.Projects[pick(len(data.Projects))]
				lead := *user
				project.Lead, project.UpdatedAt = &lead, at
			}
			return nil
		}, "", nil},
		{"team-rename", plain, "team.updated", func(data *domain.Bootstrap) error {
			if len(data.Teams) > 0 {
				data.Teams[pick(len(data.Teams))].Color = id("#team")
			}
			return nil
		}, "", nil},
		{"project-status", plain, "project_status.updated", func(data *domain.Bootstrap) error {
			if len(data.ProjectStatuses) > 0 {
				data.ProjectStatuses[pick(len(data.ProjectStatuses))].Description = id("status")
			}
			return nil
		}, "", nil},
		{"viewer", plain, "application_policy.updated", func(data *domain.Bootstrap) error {
			data.Viewer.DisplayName = id("Viewer")
			return nil
		}, "", nil},
		{"initiative-update", plain, "initiative.updated", func(data *domain.Bootstrap) error {
			if len(data.Initiatives) > 0 {
				data.Initiatives[pick(len(data.Initiatives))].Description = id("initiative")
			}
			return nil
		}, "", nil},
		{"no-op", plain, "application_policy.updated", func(data *domain.Bootstrap) error { return nil }, "", nil},
		{"loop-contract", plain, "loop.output_contract", func(data *domain.Bootstrap) error {
			for i := range data.Loops {
				if data.Loops[i].ID == "ci-loop-0" {
					data.Loops[i].OutputContract = &domain.LoopOutputContract{Source: "model", ReadOnly: true, InstructionsHash: id("hash"), Outputs: []domain.LoopContractOutput{}, ExtractedAt: &at}
				}
			}
			return nil
		}, "ci-loop-0", map[string]any{"source": "model"}},
		{"feature-flags", plain, "workspace_preferences.updated", func(data *domain.Bootstrap) error {
			if data.WorkspaceSettings.FeatureFlags == nil {
				data.WorkspaceSettings.FeatureFlags = map[string]bool{}
			}
			data.WorkspaceSettings.FeatureFlags["ci-flag"] = rng.IntN(2) == 0
			return nil
		}, "", map[string]any{"featureFlags": map[string]bool{"ci-flag": true}}},
		{"user-settings", plain, "user_settings.updated", func(data *domain.Bootstrap) error {
			for key, value := range data.UserSettings {
				value.InterfaceTheme = []string{"dark", "light"}[pick(2)]
				data.UserSettings[key] = value
			}
			return nil
		}, "", map[string]any{"interfaceTheme": "dark"}},
	}
}

// Every COLD legacy writer's publication equals a fresh SQL reload: the
// incremental one, and the full reload it falls back to. The sequence mixes
// the writers at random (seeded), so later writes patch generations earlier
// incremental publications installed.
func TestColdIncrementalPublicationDifferential(t *testing.T) {
	f, view, workspace := coldIncrementalFixture(t)
	rng := rand.New(rand.NewPCG(20261119, 1))
	incremental, full, unchanged := ColdPublishCountsForTesting()
	userReaders := ColdPublishUserReaderPatchesForTesting()
	ran := map[string]int{}
	for step := range 160 {
		writes := coldIncrementalWrites(rng, step)
		write := writes[rng.IntN(len(writes))]
		if step < len(writes) {
			write = writes[step]
		}
		ran[write.name]++
		if step%5 == 4 {
			// A HOT writer's generation in between (every other one marked
			// behind, as a conflict refresh would): the next publication
			// derives from the last publication's generation.
			var closes []func()
			output := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
				closes = append(closes, close)
				return true
			})
			err := view.MutateWorkflowRun(output, workspace, "ci-flow-0", "", "", nil, func(data *domain.Bootstrap) error {
				data.WorkflowRuns = append(data.WorkflowRuns, domain.WorkflowRun{ID: fmt.Sprintf("hot-run-%d", step), WorkflowID: "ci-flow-0", Trigger: "manual", Status: "succeeded", Attempt: 1})
				return nil
			})
			for _, close := range closes {
				close()
			}
			if err != nil && !errors.Is(err, ErrNoMutation) {
				t.Logf("step %d HOT run: %v", step, err)
			}
			if step%10 == 9 {
				view.metadataResidency.mu.Lock()
				view.metadataResidency.logical[workspace].behind.Store(true)
				view.metadataResidency.mu.Unlock()
			}
		}
		ctx := write.ctx(WithColdWork(t.Context(), "cold-incremental-test"))
		if step%7 == 3 {
			// The legacy writer stores the request actor's account over its
			// user row: a change of that user alone.
			current := scaleP21ColdTeamNative(t, view, workspace)
			if len(current.Users) > 0 {
				actor := current.Users[rng.IntN(len(current.Users))]
				actor.DisplayName = fmt.Sprintf("Actor %d", step)
				ctx = ContextWithActor(ctx, actor)
			}
		}
		var err error
		if write.aggregate != "" || write.payload != nil {
			// The production dispatch (the special legacy writers select on
			// the event's aggregate and payload).
			err = view.MutateWorkspace(ctx, workspace, write.event, write.aggregate, write.payload, write.mutate)
		} else {
			err = view.coldLegacyMutation(ctx, workspace, write.event, nil, func(data *domain.Bootstrap) (string, error) {
				return "", write.mutate(data)
			})
		}
		if err != nil {
			t.Fatalf("step %d %s: %v", step, write.name, err)
		}
		coldIncrementalAssertExact(t, f, view, workspace, fmt.Sprintf("step %d %s", step, write.name))
	}
	afterIncremental, afterFull, afterUnchanged := ColdPublishCountsForTesting()
	t.Logf("publications: incremental %d, full %d, unchanged %d; fallbacks %v; writers %v",
		afterIncremental-incremental, afterFull-full, afterUnchanged-unchanged, ColdPublishFallbacksForTesting(), ran)
	if afterIncremental-incremental < 60 {
		t.Fatalf("only %d of 160 publications were incremental", afterIncremental-incremental)
	}
	plans := ColdPublishFieldPlansForTesting()
	t.Logf("field plans (patched, rebuilt): %v; user readers patched %d", plans, ColdPublishUserReaderPatchesForTesting()-userReaders)
	if ColdPublishUserReaderPatchesForTesting() == userReaders {
		t.Error("no user-reading field was patched in the items holding a changed user")
	}
	for _, name := range []string{"apiKeys", "savedViews", "projects", "documents", "webhooks", "workflowDefinitions", "importJobs", "trash", "oauthApplications", "labels", "settings", "users", "teams"} {
		if plans[name][0] == 0 {
			t.Errorf("%s was never patched", name)
		}
	}
	// (Members hold no canonical rows here: a user change, patched in the
	// items holding the user, leaves them as installed.)
	for _, name := range []string{"labels", "issues", "projects", "projectStatuses", "teamSettings", "initiatives", "viewer"} {
		if plans[name][1] == 0 {
			t.Errorf("%s was never rebuilt", name)
		}
	}
}

func coldIncrementalRun(t *testing.T, view *SQLiteStore, workspace, event string, mutate func(*domain.Bootstrap) error) {
	t.Helper()
	if err := view.coldLegacyMutation(WithColdWork(t.Context(), "cold-incremental-test"), workspace, event, nil, func(data *domain.Bootstrap) (string, error) {
		return "", mutate(data)
	}); err != nil {
		t.Fatal(event, err)
	}
}

func coldIncrementalFallbackDelta(before map[string]int64, reason string) int64 {
	return ColdPublishFallbacksForTesting()[reason] - before[reason]
}

// Each case the comparison cannot show exact reloads in full (and still
// equals a fresh reload); a change committed beside the write while it held
// the lock is part of the incremental publication.
func TestColdIncrementalPublicationFallbacks(t *testing.T) {
	f, view, workspace := coldIncrementalFixture(t)
	at := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	addView := func(id string) func(*domain.Bootstrap) error {
		return func(data *domain.Bootstrap) error {
			data.SavedViews = append(data.SavedViews, domain.SavedView{ID: id, Name: id, Resource: "issues", Scope: "workspace"})
			return nil
		}
	}
	directRecord := func(field, key, raw string) {
		t.Helper()
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field=? AND record_key=?`, []byte(raw), workspace, field, key); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	type outcome struct{ incremental, full int64 }
	run := func(label, reason string, want outcome, write func()) {
		t.Helper()
		fallbacks := ColdPublishFallbacksForTesting()
		incremental, full, _ := ColdPublishCountsForTesting()
		write()
		afterIncremental, afterFull, _ := ColdPublishCountsForTesting()
		got := outcome{afterIncremental - incremental, afterFull - full}
		if got != want {
			t.Fatalf("%s: publications %+v, want %+v (fallbacks %v)", label, got, want, ColdPublishFallbacksForTesting())
		}
		if reason != "" && coldIncrementalFallbackDelta(fallbacks, reason) < 1 {
			t.Fatalf("%s: no %q fallback (fallbacks %v)", label, reason, ColdPublishFallbacksForTesting())
		}
		coldIncrementalAssertExact(t, f, view, workspace, label)
	}

	run("incremental baseline", "", outcome{1, 0}, func() {
		coldIncrementalRun(t, view, workspace, "view.created", addView("fb-view-1"))
	})
	// A peer's (or an untracked writer's) commit after a publication's own
	// generation: the publication's state is the base, so the comparison
	// includes the commit.
	run("commit after a publication", "", outcome{1, 0}, func() {
		directRecord("apiKeys", "ci-key-1", `{"id":"ci-key-1","name":"Peer renamed","prefix":"flw_1","creatorId":"","scopes":["read"],"teamIds":[]}`)
		coldIncrementalRun(t, view, workspace, "view.created", addView("fb-view-2"))
	})
	hotRun := func(id string) {
		t.Helper()
		var closes []func()
		output := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
			closes = append(closes, close)
			return true
		})
		err := view.MutateWorkflowRun(output, workspace, "ci-flow-1", "", "", nil, func(data *domain.Bootstrap) error {
			data.WorkflowRuns = append(data.WorkflowRuns, domain.WorkflowRun{ID: id, WorkflowID: "ci-flow-1", Trigger: "manual", Status: "succeeded", Attempt: 1, ScheduledAt: at, StartedAt: at})
			return nil
		})
		for _, close := range closes {
			close()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	// The same after a HOT writer's generation, whose inputs nobody
	// recorded: the last publication's generation (retained) and state are
	// the base, and the comparison includes the HOT write too.
	run("commit after a HOT generation", "", outcome{1, 0}, func() {
		hotRun("fb-run-1")
		directRecord("apiKeys", "ci-key-2", `{"id":"ci-key-2","name":"Peer renamed","prefix":"flw_2","creatorId":"","scopes":["read"],"teamIds":[]}`)
		coldIncrementalRun(t, view, workspace, "view.created", addView("fb-view-2b"))
	})
	// A HOT generation that is not exact (marked behind) is replaced from
	// the last publication's generation as well.
	run("inexact HOT generation", "", outcome{1, 0}, func() {
		hotRun("fb-run-2")
		view.metadataResidency.mu.Lock()
		view.metadataResidency.logical[workspace].behind.Store(true)
		view.metadataResidency.mu.Unlock()
		coldIncrementalRun(t, view, workspace, "view.created", addView("fb-view-2c"))
	})
	// Without a known publication (a restart), the image behind SQL when
	// the write takes the lock reloads in full.
	run("commit after a HOT generation, nothing known", "installed generation behind", outcome{0, 1}, func() {
		forgetColdPublications(view.metadataResidency, workspace)
		hotRun("fb-run-3")
		directRecord("apiKeys", "ci-key-3", `{"id":"ci-key-3","name":"Peer renamed","prefix":"flw_3","creatorId":"","scopes":["read"],"teamIds":[]}`)
		coldIncrementalRun(t, view, workspace, "view.created", addView("fb-view-2d"))
	})
	run("incremental after the reload", "", outcome{1, 0}, func() {
		coldIncrementalRun(t, view, workspace, "view.created", addView("fb-view-2e"))
	})
	// A behind marker (a conflict refresh patched only some records) on the
	// installed generation: the known publication's generation is exact at
	// its state whatever was committed since, so it stays the base.
	run("behind marker", "", outcome{1, 0}, func() {
		view.metadataResidency.mu.Lock()
		view.metadataResidency.logical[workspace].behind.Store(true)
		view.metadataResidency.mu.Unlock()
		coldIncrementalRun(t, view, workspace, "view.created", addView("fb-view-3"))
	})
	// A commit beside the write while it holds the lock (a peer, or a
	// writer that takes no lock): the comparison sees its records.
	run("commit beside the write", "", outcome{1, 0}, func() {
		coldIncrementalRun(t, view, workspace, "view.created", func(data *domain.Bootstrap) error {
			directRecord("webhooks", "ci-hook-1", `{"id":"ci-hook-1","name":"Beside","url":"https://example.test/beside","resourceTypes":["Issue"],"teamIds":[],"enabled":false}`)
			return addView("fb-view-4")(data)
		})
	})
	// The journal changed generation while the write held the lock (a peer
	// turned it off and on): versions are no longer comparable.
	run("journal generation changed", "journal generation changed", outcome{0, 1}, func() {
		coldIncrementalRun(t, view, workspace, "view.created", func(data *domain.Bootstrap) error {
			if _, err := f.db.ExecContext(t.Context(), `UPDATE record_journal_mode SET generation=? WHERE id=1`, "rjg-test-"+at.Format(time.RFC3339Nano)); err != nil {
				return err
			}
			return addView("fb-view-5")(data)
		})
	})
	// The normalizer's base changed: workspaceSettings shapes no other
	// field, so only it is rebuilt.
	run("base field", "", outcome{1, 0}, func() {
		coldIncrementalRun(t, view, workspace, "workspace_settings.updated", func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.WelcomeMessage = "changed"
			return nil
		})
	})
	// Disabled.
	run("disabled", "disabled", outcome{0, 1}, func() {
		SetColdIncrementalPublicationForTesting(false)
		defer SetColdIncrementalPublicationForTesting(true)
		coldIncrementalRun(t, view, workspace, "view.created", addView("fb-view-6"))
	})
	run("incremental again", "", outcome{1, 0}, func() {
		coldIncrementalRun(t, view, workspace, "view.created", addView("fb-view-7"))
	})
}

// A project change that keeps every project's labels leaves the labels (and
// their counts) as installed; one that changes a project's labels rebuilds
// them with the new counts. Both equal a fresh reload.
func TestColdIncrementalPublicationLabelCounts(t *testing.T) {
	f, view, workspace := coldIncrementalFixture(t)
	plans := func() map[string][2]int64 { return ColdPublishFieldPlansForTesting() }
	labelsRebuilt := func(before map[string][2]int64) int64 { return plans()["labels"][1] - before["labels"][1] }
	projectsPatched := func(before map[string][2]int64) int64 { return plans()["projects"][0] - before["projects"][0] }
	// The first project write learns every project's labels.
	coldIncrementalRun(t, view, workspace, "project.updated", func(data *domain.Bootstrap) error {
		data.Projects[3].Description = "first"
		return nil
	})
	coldIncrementalAssertExact(t, f, view, workspace, "first project write")
	before := plans()
	coldIncrementalRun(t, view, workspace, "project.updated", func(data *domain.Bootstrap) error {
		data.Projects[5].Description = "labels kept"
		return nil
	})
	got := coldIncrementalAssertExact(t, f, view, workspace, "labels kept")
	if labelsRebuilt(before) != 0 || projectsPatched(before) != 1 {
		t.Fatalf("labels kept: labels rebuilt %d, projects patched %d", labelsRebuilt(before), projectsPatched(before))
	}
	counts := map[string]int{}
	for _, label := range got.Labels {
		counts[label.ID] = label.IssueCount
	}
	before = plans()
	coldIncrementalRun(t, view, workspace, "project.updated", func(data *domain.Bootstrap) error {
		data.Projects[5].LabelIDs = []string{"ci-label-0", "ci-label-2"}
		return nil
	})
	got = coldIncrementalAssertExact(t, f, view, workspace, "labels changed")
	if labelsRebuilt(before) != 1 || projectsPatched(before) != 1 {
		t.Fatalf("labels changed: labels rebuilt %d, projects patched %d", labelsRebuilt(before), projectsPatched(before))
	}
	for _, label := range got.Labels {
		if label.ID == "ci-label-0" && label.IssueCount != counts[label.ID]+1 {
			t.Fatalf("label count %d, want %d", label.IssueCount, counts[label.ID]+1)
		}
	}
	// Archiving takes a project's labels out of the counts.
	before = plans()
	coldIncrementalRun(t, view, workspace, "project.archived", func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		data.Projects[5].ArchivedAt = &now
		return nil
	})
	coldIncrementalAssertExact(t, f, view, workspace, "archived")
	if labelsRebuilt(before) != 1 {
		t.Fatalf("archived: labels rebuilt %d", labelsRebuilt(before))
	}
}

// HOT (bounded point) writers install their own exact generations between
// COLD writes: a workflow run and a label deletion (which also rewrites the
// projects that carried the label). The incremental COLD publications that
// follow derive from those generations and still equal a fresh reload.
func TestColdIncrementalPublicationAfterHotWriters(t *testing.T) {
	f, view, workspace := coldIncrementalFixture(t)
	at := time.Date(2026, 11, 19, 11, 0, 0, 0, time.UTC)
	incremental, _, _ := ColdPublishCountsForTesting()
	for i := range 3 {
		var closes []func()
		output := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
			closes = append(closes, close)
			return true
		})
		run := domain.WorkflowRun{ID: fmt.Sprintf("hot-run-%d", i), WorkflowID: "ci-flow-1", Trigger: "manual", Status: "succeeded", Attempt: 1, ScheduledAt: at, StartedAt: at}
		err := view.MutateWorkflowRun(output, workspace, "ci-flow-1", "", "", nil, func(data *domain.Bootstrap) error {
			data.WorkflowRuns = append(data.WorkflowRuns, run)
			return nil
		})
		for _, close := range closes {
			close()
		}
		if err != nil {
			t.Fatal("HOT workflow run", err)
		}
		if err := view.MutateLabelDeletion(t.Context(), workspace, "label.deleted", fmt.Sprintf("ci-label-%d", i), false, func(*domain.Bootstrap) error { return nil }); err != nil {
			t.Fatal("HOT label deletion", err)
		}
		coldIncrementalAssertExact(t, f, view, workspace, fmt.Sprintf("after HOT writes %d", i))
		coldIncrementalRun(t, view, workspace, "project.updated", func(data *domain.Bootstrap) error {
			data.Projects[i].Description = fmt.Sprintf("after hot %d", i)
			data.SavedViews = append(data.SavedViews, domain.SavedView{ID: fmt.Sprintf("hot-view-%d", i), Name: "After HOT", Resource: "issues", Scope: "workspace"})
			return nil
		})
		coldIncrementalAssertExact(t, f, view, workspace, fmt.Sprintf("after COLD write %d", i))
	}
	if after, _, _ := ColdPublishCountsForTesting(); after-incremental != 3 {
		t.Fatalf("%d incremental publications, want 3 (fallbacks %v)", after-incremental, ColdPublishFallbacksForTesting())
	}
}

// A generation installed after the COLD facade decoded its base (here a HOT
// write between the facade's creation and its write lock) is decoded
// outside the shared lock: Lock refreshes the facade and takes the lock
// again, so onLock finds it current.
func TestColdIncrementalStaleFacadeRefreshesOutsideTheLock(t *testing.T) {
	f, view, workspace := coldIncrementalFixture(t)
	at := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	refreshes := coldWriteRefreshes.Load()
	syncs := coldWriteSyncNanos.Load()
	err := view.coldLegacyWrite(WithColdWork(t.Context(), "cold-incremental-test"), workspace, func(ctx context.Context, facade *SQLiteStore) error {
		var closes []func()
		output := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
			closes = append(closes, close)
			return true
		})
		err := view.MutateWorkflowRun(output, workspace, "ci-flow-2", "", "", nil, func(data *domain.Bootstrap) error {
			data.WorkflowRuns = append(data.WorkflowRuns, domain.WorkflowRun{ID: "stale-run", WorkflowID: "ci-flow-2", Trigger: "manual", Status: "succeeded", Attempt: 1, ScheduledAt: at, StartedAt: at})
			return nil
		})
		for _, close := range closes {
			close()
		}
		if err != nil {
			return err
		}
		if !facade.coldFacadeStale(ctx, workspace) {
			t.Error("the HOT write left the facade current")
		}
		return facade.dispatchMutation(ctx, workspace, "view.created", nil, func(data *domain.Bootstrap) (string, error) {
			if len(data.WorkflowRuns) == 0 || data.WorkflowRuns[len(data.WorkflowRuns)-1].ID != "stale-run" {
				t.Error("the COLD writer's base lacks the HOT write")
			}
			data.SavedViews = append(data.SavedViews, domain.SavedView{ID: "stale-view", Name: "Stale", Resource: "issues", Scope: "workspace"})
			return "", nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if coldWriteRefreshes.Load()-refreshes != 1 {
		t.Fatalf("%d refreshes outside the lock, want 1", coldWriteRefreshes.Load()-refreshes)
	}
	t.Logf("sync under the lock %v", time.Duration(coldWriteSyncNanos.Load()-syncs))
	got := coldIncrementalAssertExact(t, f, view, workspace, "stale facade")
	if !slices.ContainsFunc(got.SavedViews, func(view domain.SavedView) bool { return view.ID == "stale-view" }) {
		t.Fatal("the COLD write is missing")
	}
}

// A workflow-state deletion (a COLD route's store writer outside the COLD
// facade) publishes the installed image: the deleted state leaves the
// installed states, which equal a fresh reload.
func TestColdIncrementalWorkflowStateDeletionPublishes(t *testing.T) {
	f, view, workspace := coldIncrementalFixture(t)
	before := coldIncrementalAssertExact(t, f, view, workspace, "before")
	var target domain.WorkflowState
	for _, state := range before.States {
		if state.ID == "ci-state-1" {
			target = state
			break
		}
	}
	if target.ID == "" {
		t.Fatal("fixture has no deletable team state")
	}
	incremental, full, _ := ColdPublishCountsForTesting()
	if err := view.DeleteWorkflowStateRecords(t.Context(), workspace, target.TeamID, func(data *domain.Bootstrap, inUse bool) (domain.WorkflowState, *domain.WorkflowState, error) {
		for _, state := range data.States {
			if state.ID == target.ID {
				return state, nil, nil
			}
		}
		return domain.WorkflowState{}, nil, fmt.Errorf("state %s missing", target.ID)
	}); err != nil {
		t.Fatal(err)
	}
	got := coldIncrementalAssertExact(t, f, view, workspace, "after deletion")
	if slices.ContainsFunc(got.States, func(state domain.WorkflowState) bool { return state.ID == target.ID }) {
		t.Fatal("the installed image still holds the deleted state")
	}
	afterIncremental, afterFull, _ := ColdPublishCountsForTesting()
	if afterIncremental-incremental != 1 || afterFull != full {
		t.Fatalf("publications: incremental %d, full %d (fallbacks %v)", afterIncremental-incremental, afterFull-full, ColdPublishFallbacksForTesting())
	}
}

// Reloads of the installed generation (a peer's commit, ReloadWorkspace)
// derive from the known publication and the records changed since, as COLD
// publications do; a reload in full keeps its generation as the known one.
// Every reload equals a fresh one.
func TestColdIncrementalReloadDerivesFromKnown(t *testing.T) {
	f, view, workspace := coldIncrementalFixture(t)
	peer := func(field, key, raw string) {
		t.Helper()
		if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field=? AND record_key=?`, []byte(raw), workspace, field, key); err != nil {
			t.Fatal(err)
		}
	}
	reload := func(label string, incremental, full int64) {
		t.Helper()
		beforeIncremental, beforeFull := residentReloadIncrementals.Load(), residentReloadFulls.Load()
		const stampSelect = "SELECT " + metadataStampColumns
		var snapshotReads, publicationChecks int
		ctx := WithSQLStatementObserver(t.Context(), func(query string) {
			switch {
			case query == stampSelect+" FROM workspace_states WHERE workspace_key=?":
				snapshotReads++
			case strings.HasPrefix(query, stampSelect+",COALESCE((SELECT MAX(head) FROM workspace_record_journal_heads "):
				publicationChecks++
			}
		})
		if err := view.ReloadWorkspace(ctx, workspace); err != nil {
			t.Fatal(label, err)
		}
		if got := [2]int64{residentReloadIncrementals.Load() - beforeIncremental, residentReloadFulls.Load() - beforeFull}; got != [2]int64{incremental, full} {
			t.Fatalf("%s: reloads (incremental, full) %v, want %v (fallbacks %v)", label, got, [2]int64{incremental, full}, ColdPublishFallbacksForTesting())
		}
		// Preflight and state capture share a stamp. Raw recovery and its root
		// cursor each read one, then installation rechecks under writeMu.
		if incremental == 1 && (snapshotReads != 3 || publicationChecks != 1) {
			t.Fatalf("%s: stamp queries (snapshot, publication) = (%d, %d), want (3, 1)", label, snapshotReads, publicationChecks)
		}
		coldIncrementalAssertExact(t, f, view, workspace, label)
	}
	// The fixture's full reload is the known publication.
	peer("apiKeys", "ci-key-1", `{"id":"ci-key-1","name":"Peer one","prefix":"flw_1","creatorId":"","scopes":["read"],"teamIds":[]}`)
	reload("peer commit after a full reload", 1, 0)
	reload("nothing changed", 0, 0)
	coldIncrementalRun(t, view, workspace, "view.created", func(data *domain.Bootstrap) error {
		data.SavedViews = append(data.SavedViews, domain.SavedView{ID: "reload-view", Name: "Reload", Resource: "issues", Scope: "workspace"})
		return nil
	})
	users := scaleP21ColdTeamNative(t, view, workspace).Users
	if len(users) == 0 {
		t.Fatal("fixture has no users")
	}
	user := users[0]
	user.DisplayName = "Peer renamed user"
	raw, err := json.Marshal(user)
	if err != nil {
		t.Fatal(err)
	}
	peer("users", user.ID, string(raw))
	peer("webhooks", "ci-hook-2", `{"id":"ci-hook-2","name":"Peer hook","url":"https://example.test/peer","resourceTypes":["Issue"],"teamIds":[],"enabled":true}`)
	reload("peer commit after a COLD publication", 1, 0)
	forgetColdPublications(view.metadataResidency, workspace)
	peer("apiKeys", "ci-key-2", `{"id":"ci-key-2","name":"Peer two","prefix":"flw_2","creatorId":"","scopes":["read"],"teamIds":[]}`)
	reload("nothing known", 0, 1)
	peer("apiKeys", "ci-key-3", `{"id":"ci-key-3","name":"Peer three","prefix":"flw_3","creatorId":"","scopes":["read"],"teamIds":[]}`)
	reload("after the full reload", 1, 0)
	SetColdIncrementalPublicationForTesting(false)
	peer("apiKeys", "ci-key-4", `{"id":"ci-key-4","name":"Peer four","prefix":"flw_4","creatorId":"","scopes":["read"],"teamIds":[]}`)
	reload("disabled", 0, 1)
	SetColdIncrementalPublicationForTesting(true)
}

func TestColdIncrementalReloadPreservesConcurrentLocalWrite(t *testing.T) {
	f, view, workspace := coldIncrementalFixture(t)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='apiKeys' AND record_key='ci-key-1'`,
		[]byte(`{"id":"ci-key-1","name":"Peer","prefix":"flw_1","scopes":["read"],"teamIds":[]}`), workspace); err != nil {
		t.Fatal(err)
	}
	prepared, resume := make(chan struct{}), make(chan struct{})
	var pause sync.Once
	hook := func() { pause.Do(func() { close(prepared); <-resume }) }
	residentReloadBeforeInstall.Store(&hook)
	defer residentReloadBeforeInstall.Store(nil)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	reloaded := make(chan error, 1)
	go func() { reloaded <- view.ReloadWorkspace(ctx, workspace) }()
	select {
	case <-prepared:
	case err := <-reloaded:
		close(resume)
		t.Fatalf("reload skipped preparation: %v", err)
	case <-ctx.Done():
		close(resume)
		t.Fatal("reload did not prepare", <-reloaded)
	}
	if !view.writeMu.TryLock() {
		close(resume)
		t.Fatal("preparation held the write lock", <-reloaded)
	}
	view.writeMu.Unlock()
	const id = "ci-project-05"
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "projects", ID: id}}}
	err := view.MutateWorkspaceScoped(WithScopedSelectionWrite(ctx), workspace, "project.updated", id, nil, scope, func(next *domain.Bootstrap) error {
		next.Projects[0].Description = "Local edit during reload"
		return nil
	})
	close(resume)
	reloadErr := <-reloaded
	if err != nil || reloadErr != nil {
		t.Fatalf("local write: %v, reload: %v", err, reloadErr)
	}
	source, err := view.retainMetadataLogicalSource(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	selected, err := source.Select(ctx, scope, 2<<20)
	if err != nil || len(selected.Projects) != 1 || selected.Projects[0].Description != "Local edit during reload" {
		t.Fatalf("local edit was lost: %v", err)
	}
	coldIncrementalAssertExact(t, f, view, workspace, "after raced reload")
}

func TestOptimisticResidentReloadPreservesConcurrentLocalWrite(t *testing.T) {
	f, view, workspace := coldIncrementalFixture(t)
	forgetColdPublications(view.metadataResidency, workspace)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='apiKeys' AND record_key='ci-key-1'`,
		[]byte(`{"id":"ci-key-1","name":"Peer","prefix":"flw_1","scopes":["read"],"teamIds":[]}`), workspace); err != nil {
		t.Fatal(err)
	}
	prepared, resume := make(chan struct{}), make(chan struct{})
	var pause sync.Once
	hook := func() { pause.Do(func() { close(prepared); <-resume }) }
	residentReloadBeforeInstall.Store(&hook)
	defer residentReloadBeforeInstall.Store(nil)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	reloaded := make(chan error, 1)
	go func() { reloaded <- view.ReloadWorkspace(ctx, workspace) }()
	select {
	case <-prepared:
	case err := <-reloaded:
		close(resume)
		t.Fatalf("reload skipped optimistic preparation: %v", err)
	case <-ctx.Done():
		close(resume)
		t.Fatal("reload did not prepare", <-reloaded)
	}
	if !view.writeMu.TryLock() {
		close(resume)
		t.Fatal("optimistic preparation held the write lock", <-reloaded)
	}
	view.writeMu.Unlock()
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "projects", ID: "ci-project-05"}}}
	err := view.MutateWorkspaceScoped(WithScopedSelectionWrite(ctx), workspace, "project.updated", "ci-project-05", nil, scope, func(next *domain.Bootstrap) error {
		next.Projects[0].Description = "Local edit during optimistic reload"
		return nil
	})
	close(resume)
	reloadErr := <-reloaded
	if err != nil || reloadErr != nil {
		t.Fatalf("local write: %v, reload: %v", err, reloadErr)
	}
	source, err := view.retainMetadataLogicalSource(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	selected, err := source.Select(ctx, scope, 2<<20)
	if err != nil || len(selected.Projects) != 1 || selected.Projects[0].Description != "Local edit during optimistic reload" {
		t.Fatalf("local edit was lost: %v", err)
	}
	coldIncrementalAssertExact(t, f, view, workspace, "after raced optimistic reload")
}

func TestOptimisticResidentReloadRetriesJournalHeadRace(t *testing.T) {
	f, view, workspace := coldIncrementalFixture(t)
	forgetColdPublications(view.metadataResidency, workspace)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='apiKeys' AND record_key='ci-key-1'`,
		[]byte(`{"id":"ci-key-1","name":"Peer","prefix":"flw_1","scopes":["read"],"teamIds":[]}`), workspace); err != nil {
		t.Fatal(err)
	}
	before, _, ok := view.residentStamp(workspace)
	if !ok {
		t.Fatal("fixture has no resident stamp")
	}
	_, beforeHead, _, err := view.readResidentPublicationStamp(t.Context(), workspace, f.data.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	prepared, resume := make(chan struct{}), make(chan struct{})
	var pause sync.Once
	hook := func() { pause.Do(func() { close(prepared); <-resume }) }
	residentReloadBeforeInstall.Store(&hook)
	defer residentReloadBeforeInstall.Store(nil)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	reloaded := make(chan error, 1)
	go func() { reloaded <- view.ReloadWorkspace(ctx, workspace) }()
	select {
	case <-prepared:
	case err := <-reloaded:
		close(resume)
		t.Fatalf("reload skipped optimistic preparation: %v", err)
	case <-ctx.Done():
		close(resume)
		t.Fatal("reload did not prepare", <-reloaded)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE workspace_record_journal_heads SET head=head+1 WHERE workspace_id=?`, f.data.Workspace.ID); err != nil {
		close(resume)
		t.Fatal(err)
	}
	close(resume)
	if err := <-reloaded; err != nil {
		t.Fatal(err)
	}
	after, applied, ok := view.residentStamp(workspace)
	if !ok || after == before || applied != beforeHead+1 {
		t.Fatalf("journal-head race installed stamp %v at head %d, want changed stamp and head %d (before %v)", after, applied, beforeHead+1, before)
	}
	coldIncrementalAssertExact(t, f, view, workspace, "after journal-head race")
}

func TestColdIncrementalReloadChecksJournalHead(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(fmt.Sprintf("journal_reset=%t", reset), func(t *testing.T) {
			f, view, workspace := coldIncrementalFixture(t)
			ctx := t.Context()
			before, beforeHead, _ := view.residentStamp(workspace)
			if _, err := f.db.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='apiKeys' AND record_key='ci-key-1'`,
				[]byte(`{"id":"ci-key-1","name":"Peer","prefix":"flw_1","scopes":["read"],"teamIds":[]}`), workspace); err != nil {
				t.Fatal(err)
			}
			stamp, head, _, err := view.readResidentPublicationStamp(ctx, workspace, f.data.Workspace.ID)
			if err != nil {
				t.Fatal(err)
			}
			preparations := 0
			hook := func() {
				preparations++
				if !view.writeMu.TryLock() {
					t.Fatal("prepared reload was holding the write lock")
				}
				view.writeMu.Unlock()
				if preparations != 1 {
					return
				}
				if _, err := f.db.ExecContext(ctx, `UPDATE workspace_record_journal_heads SET head=head+1 WHERE workspace_id=?`, f.data.Workspace.ID); err != nil {
					t.Fatal(err)
				}
				if reset {
					if _, err := f.db.ExecContext(ctx, `UPDATE record_journal_mode SET generation='rjg-reload-reset' WHERE id=1`); err != nil {
						t.Fatal(err)
					}
				}
			}
			residentReloadBeforeInstall.Store(&hook)
			defer residentReloadBeforeInstall.Store(nil)
			if installed, _ := view.reloadKnownPublication(ctx, workspace); installed == reset || preparations != 1 {
				t.Fatalf("installed=%t after %d preparations", installed, preparations)
			}
			if reset {
				stamp, head = before, beforeHead
			} else {
				coldIncrementalAssertExact(t, f, view, workspace, "after the journal head advanced")
			}
			if got, applied, ok := view.residentStamp(workspace); !ok || got != stamp || applied != head {
				t.Fatalf("reload installed stamp %v at journal head %d, want %v at %d", got, applied, stamp, head)
			}
		})
	}
}

// A bounded store's startup load keeps each workspace's generation as its
// known publication: the first COLD write after a commit the installed
// generation lacks publishes incrementally instead of reloading in full.
func TestColdIncrementalStartupKeepsKnownPublication(t *testing.T) {
	f, view, workspace := coldIncrementalFixture(t)
	forgetColdPublications(view.metadataResidency, workspace)
	view.writeMu.Lock()
	err := view.loadResidentWorkspaces(t.Context())
	view.writeMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	known, ok := knownColdPublication(coldLabelCacheKey{view.metadataResidency, workspace})
	if !ok {
		t.Fatal("startup load kept no known publication")
	}
	known.source.Release()
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field=? AND record_key=?`,
		[]byte(`{"id":"ci-key-1","name":"Peer","prefix":"flw_1","creatorId":"","scopes":["read"],"teamIds":[]}`), workspace, "apiKeys", "ci-key-1"); err != nil {
		t.Fatal(err)
	}
	incremental, full, _ := ColdPublishCountsForTesting()
	coldIncrementalRun(t, view, workspace, "view.created", func(data *domain.Bootstrap) error {
		data.SavedViews = append(data.SavedViews, domain.SavedView{ID: "startup-view", Name: "Startup", Resource: "issues", Scope: "workspace"})
		return nil
	})
	if afterIncremental, afterFull, _ := ColdPublishCountsForTesting(); afterIncremental-incremental != 1 || afterFull != full {
		t.Fatalf("publications after startup: incremental %d, full %d (fallbacks %v)", afterIncremental-incremental, afterFull-full, ColdPublishFallbacksForTesting())
	}
	coldIncrementalAssertExact(t, f, view, workspace, "after startup")
}

// A COLD full write whose facade holds the parent's installed generation when
// it takes the lock (onLock brought it there) keeps that decode instead of
// decoding the workspace again under the lock; a write after another
// generation was installed still writes from that generation.
func TestColdFullWriteKeepsCurrentFacadeDecode(t *testing.T) {
	f, view, workspace := coldIncrementalFixture(t)
	decodes, skips := coldFullWriteDecodes.Load(), coldFullWriteDecodeSkips.Load()
	coldIncrementalRun(t, view, workspace, "view.created", func(data *domain.Bootstrap) error {
		data.SavedViews = append(data.SavedViews, domain.SavedView{ID: "decode-view-1", Name: "One", Resource: "issues", Scope: "workspace"})
		return nil
	})
	if got := [2]int64{coldFullWriteDecodes.Load() - decodes, coldFullWriteDecodeSkips.Load() - skips}; got != [2]int64{0, 1} {
		t.Fatalf("full write decodes under the lock, kept decodes = %v, want [0 1]", got)
	}
	coldIncrementalAssertExact(t, f, view, workspace, "first write")
	// A HOT generation installed after the facade decoded: onLock refreshes
	// the facade to it and the write keeps that refresh.
	err := view.coldLegacyWrite(WithColdWork(t.Context(), "cold-incremental-test"), workspace, func(ctx context.Context, facade *SQLiteStore) error {
		var closes []func()
		output := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
			closes = append(closes, close)
			return true
		})
		err := view.MutateWorkflowRun(output, workspace, "ci-flow-3", "", "", nil, func(data *domain.Bootstrap) error {
			data.WorkflowRuns = append(data.WorkflowRuns, domain.WorkflowRun{ID: "decode-run", WorkflowID: "ci-flow-3", Trigger: "manual", Status: "succeeded", Attempt: 1})
			return nil
		})
		for _, close := range closes {
			close()
		}
		if err != nil {
			return err
		}
		return facade.dispatchMutation(ctx, workspace, "view.created", nil, func(data *domain.Bootstrap) (string, error) {
			if !slices.ContainsFunc(data.WorkflowRuns, func(run domain.WorkflowRun) bool { return run.ID == "decode-run" }) {
				return "", errors.New("the write's base lacks the HOT generation installed before it took the lock")
			}
			data.SavedViews = append(data.SavedViews, domain.SavedView{ID: "decode-view-2", Name: "Two", Resource: "issues", Scope: "workspace"})
			return "", nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	coldIncrementalAssertExact(t, f, view, workspace, "write after a HOT generation")
}

// A registered COLD consumer's write (WithColdMetadataMutation) with a known
// publication publishes as a default COLD legacy write: incrementally, from
// the records it changed, instead of capturing the whole workspace under the
// lock; the result equals a fresh reload.
func TestColdMetadataMutationPublishesIncrementally(t *testing.T) {
	f, view, workspace := coldIncrementalFixture(t)
	const id = "registered-status"
	for _, operation := range []struct {
		consumer, event, name string
	}{
		{"server/settings_features.go:createProjectStatus", "project_status.created", "Registered"},
		{"server/settings_features.go:updateProjectStatus", "project_status.updated", "Renamed"},
	} {
		event := operation.event
		incremental, full, _ := ColdPublishCountsForTesting()
		err := view.WithColdMetadataMutation(t.Context(), operation.consumer, workspace, event, nil, func(ctx context.Context, facade *SQLiteStore) error {
			return facade.MutateWorkspace(ctx, workspace, event, id, nil, func(data *domain.Bootstrap) error {
				if event == "project_status.created" {
					data.ProjectStatuses = append(data.ProjectStatuses, domain.ProjectStatus{ID: id, Name: operation.name, Type: "planned", Position: float64(len(data.ProjectStatuses))})
					return nil
				}
				index := slices.IndexFunc(data.ProjectStatuses, func(status domain.ProjectStatus) bool { return status.ID == id })
				if index < 0 {
					return errors.New("created project status missing from the next mutation")
				}
				data.ProjectStatuses[index].Name = operation.name
				return nil
			})
		})
		if err != nil {
			t.Fatal(event, err)
		}
		if afterIncremental, afterFull, _ := ColdPublishCountsForTesting(); afterIncremental-incremental != 1 || afterFull != full {
			t.Fatalf("%s: publications incremental %d, full %d (fallbacks %v)", event, afterIncremental-incremental, afterFull-full, ColdPublishFallbacksForTesting())
		}
		got := coldIncrementalAssertExact(t, f, view, workspace, event)
		if !slices.ContainsFunc(got.ProjectStatuses, func(status domain.ProjectStatus) bool { return status.ID == id && status.Name == operation.name }) {
			t.Fatalf("%s: the installed image lacks the write", event)
		}
	}
}
