package main

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/coding"
	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW23LoopRunReadsDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, loopID := f.data.Workspace.URLKey, idgen.New("sw23-loop-")
		stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		loops := []domain.Loop{{ID: loopID, SlugID: "sw23-old-slug", Name: "Target", Description: strings.Repeat("legal large instructions ", 16000)}}
		for range 60 {
			loops = append(loops, domain.Loop{ID: idgen.New("sw23-other-loop-"), Name: "Retained"})
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "loops"), workspace, "scale_p21_sw23.seed", "", nil, func(data *domain.Bootstrap) error {
			data.Loops = loops
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		run := domain.LoopRun{ID: idgen.New("sw23-run-"), LoopID: loopID, Status: "completed", Trigger: "manual", StartedAt: stamp, Output: "Complete"}
		for i, principal := range f.principals {
			run.Feedback = append(run.Feedback, domain.LoopRunFeedback{UserID: principal.user.ID, Rating: []string{"up", "down"}[i%2], Comment: principal.name, At: stamp})
		}
		if err := repo.CreateLoopRun(t.Context(), workspace, run, store.LoopRunLease{}); err != nil {
			t.Fatal(err)
		}
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW23 foreign", idgen.New("sw23-workspace-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		foreignRun := run
		foreignRun.ID, foreignRun.LoopID = idgen.New("sw23-foreign-run-"), idgen.New("sw23-foreign-loop-")
		if err := repo.CreateLoopRun(t.Context(), foreign.Workspace.URLKey, foreignRun, store.LoopRunLease{}); err != nil {
			t.Fatal(err)
		}
		f.probe, err = repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		before := scaleP21M3Fingerprint(t, repo, workspace, "loops")
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, test := range []struct {
				name, loop, run string
				list            bool
				status          int
			}{
				{"list", loopID, run.ID, true, http.StatusOK},
				{"get", loopID, run.ID, false, http.StatusOK},
				{"list-missing", loopID + "-missing", run.ID, true, http.StatusNotFound},
				{"list-slug", "sw23-old-slug", run.ID, true, http.StatusNotFound},
				{"list-long", strings.Repeat("x", 192), run.ID, true, http.StatusNotFound},
				{"list-nul", loopID + "\x00", run.ID, true, http.StatusNotFound},
				{"get-missing", loopID, run.ID + "-missing", false, http.StatusNotFound},
				{"get-other-loop", loopID + "-missing", run.ID, false, http.StatusNotFound},
				{"get-foreign-run", foreignRun.LoopID, foreignRun.ID, false, http.StatusNotFound},
			} {
				t.Run(test.name, func(t *testing.T) {
					var outcomes []any
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						r, w := f.request(user, key, nil), httptest.NewRecorder()
						r.SetPathValue("id", test.loop)
						r.SetPathValue("runId", test.run)
						service := &server{store: source}
						if test.list {
							service.listLoopRuns(w, r)
						} else {
							service.getLoopRun(w, r)
						}
						if w.Code != test.status {
							t.Fatalf("loop read status=%d body=%s want=%d", w.Code, w.Body.String(), test.status)
						}
						outcomes = append(outcomes, []any{w.Code, scaleP21M3JSON(t, w.Body.Bytes())})
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatalf("run read differential: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
				})
			}
		})
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			for _, list := range []bool{false, true} {
				r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
				r.Header.Set("X-Workspace-Key", foreign.Workspace.URLKey)
				r.SetPathValue("workspaceKey", foreign.Workspace.URLKey)
				r.SetPathValue("workspace", foreign.Workspace.URLKey)
				r.SetPathValue("id", loopID)
				r.SetPathValue("runId", run.ID)
				if list {
					(&server{store: source}).listLoopRuns(w, r)
				} else {
					(&server{store: source}).getLoopRun(w, r)
				}
				if w.Code != http.StatusNotFound {
					t.Fatalf("run escaped workspace binding: %d %s", w.Code, w.Body.String())
				}
			}
			for _, list := range []bool{false, true} {
				r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
				r.SetPathValue("id", loopID)
				r.SetPathValue("runId", run.ID)
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				r = r.WithContext(ctx)
				if list {
					(&server{store: source}).listLoopRuns(w, r)
					if w.Code != http.StatusInternalServerError {
						t.Fatalf("cancelled list status=%d", w.Code)
					}
				} else {
					(&server{store: source}).getLoopRun(w, r)
					if w.Code != http.StatusNotFound {
						t.Fatalf("cancelled get status=%d", w.Code)
					}
				}
			}
		}
		member := f.principals[2].user
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				r, w := f.request(member, nil, nil), httptest.NewRecorder()
				r.SetPathValue("id", loopID)
				r.SetPathValue("runId", run.ID)
				scaleP21M3Call(&server{store: source}, w, r, (*server).getLoopRun)
				if w.Code != http.StatusForbidden {
					t.Fatal("queued/replayed read bypassed live membership")
				}
			}
		}
		if before != scaleP21M3Fingerprint(t, repo, workspace, "loops") || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("run reads changed canonical loops or decoded complete metadata")
		}
	}
}

func TestScaleP21SW23InternalConversationIdentityDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, sessionID := f.data.Workspace.URLKey, idgen.New("sw23-session-")
		seed := func(owner string) {
			t.Helper()
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "agentSessions"), workspace, "scale_p21_sw23.seed", "", nil, func(data *domain.Bootstrap) error {
				data.AgentSessions = []domain.AgentSession{
					{ID: sessionID, SlugID: "sw23-session-old", UserID: owner, Messages: []domain.AgentMessage{{ID: "sw23-message", Role: "user", Content: "Complete request"}}},
					{ID: sessionID + "-unrelated", UserID: owner, Title: strings.Repeat("unrelated large session ", 16000)},
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			seed(user.ID)
			for _, ref := range []string{sessionID, sessionID + "-missing", "sw23-session-old"} {
				var outcomes []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					session, err := (&server{store: source}).internalRequestConversation(f.request(user, key, nil), ref)
					message := ""
					if err != nil {
						message = err.Error()
					}
					outcomes = append(outcomes, []any{session, message})
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("conversation differential: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			}
		})
		seed(f.data.Viewer.ID)
		var outcomes []any
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			request := f.request(domain.User{}, nil, nil)
			session, err := (&server{store: source, authDisabled: true}).internalRequestConversation(request, sessionID)
			if err != nil || session.ID != sessionID || session.UserID != f.data.Viewer.ID {
				t.Fatal("stored-viewer fallback changed", err)
			}
			outcomes = append(outcomes, session)
			ctx, cancel := context.WithCancel(request.Context())
			cancel()
			if _, err := (&server{store: source, authDisabled: true}).internalRequestConversation(request.WithContext(ctx), sessionID); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled conversation identity lookup: %v", err)
			}
		}
		if !reflect.DeepEqual(outcomes[0], outcomes[1]) || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("stored-viewer fallback differs or decoded complete metadata")
		}
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW23 conversation foreign", idgen.New("sw23-conversation-workspace-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		f.probe, err = repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			request := f.request(f.data.Viewer, nil, nil)
			request.Header.Set("X-Workspace-Key", foreign.Workspace.URLKey)
			request.SetPathValue("workspaceKey", foreign.Workspace.URLKey)
			request.SetPathValue("workspace", foreign.Workspace.URLKey)
			if _, err := (&server{store: source}).internalRequestConversation(request, sessionID); err == nil {
				t.Fatal("conversation escaped its workspace")
			}
		}
		member := f.principals[2].user
		seed(member.ID)
		queued := []*http.Request{}
		for _, credential := range scaleP21M3Credentials() {
			queued = append(queued, f.request(member, credential.key, nil))
		}
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			for _, request := range queued {
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					if _, err := (&server{store: source}).internalRequestConversation(request, sessionID); err == nil {
						t.Fatal("queued/replayed conversation read retained revoked authority")
					}
				}
			}
		}
	}
}

type scaleP21SW23CodingStore struct {
	*fakeCodingStore
	keys  []string
	calls int
}

func (f *scaleP21SW23CodingStore) WorkspacesForInstallation(context.Context, string, string, string) ([]string, error) {
	return append([]string(nil), f.keys...), nil
}

func (f *scaleP21SW23CodingStore) SessionsForChangeRequest(context.Context, string, string, string, int) ([]domain.CodingSession, error) {
	f.calls++
	return nil, nil
}

func TestScaleP21SW23CodingSettingsDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	fake := &scaleP21SW23CodingStore{fakeCodingStore: newFakeCodingStore()}
	previous := codingTriggerStore
	codingTriggerStore = func(*server) any { return fake }
	t.Cleanup(func() { codingTriggerStore = previous })
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		fake.keys = []string{workspace}
		settings := scaleP21SW3PopulatedSettings(t, f.data.WorkspaceSettings)
		settings.CodingAgentSettings = domain.CodingAgentSettings{Harness: "claude", Model: "retained-model"}
		reset := func(enabled bool) {
			t.Helper()
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "workspaceSettings"), workspace, "scale_p21_sw23.seed", "", nil, func(data *domain.Bootstrap) error {
				data.WorkspaceSettings = settings
				data.WorkspaceSettings.FeatureFlags = maps.Clone(settings.FeatureFlags)
				data.WorkspaceSettings.FeatureFlags["coding-sessions"] = enabled
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
		}
		event := codehost.WebhookEvent{Type: codehost.EventChangeRequestMerged, CodeHost: "github", HostName: "github.com", InstallationID: "sw23-installation", ChangeRequest: &codehost.ChangeRequest{Number: 1}}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, enabled := range []bool{false, true} {
				reset(enabled)
				unchanged := scaleP21M3Fingerprint(t, repo, workspace, "workspaceSettings")
				var outcomes []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					service := &server{store: source, coding: &codingServices{config: appconfig.CodingConfig{Enabled: true}}}
					triggers, ok := service.codingTriggers(workspace)
					if !ok {
						t.Fatal("coding triggers unavailable")
					}
					starter, ok := triggers.Starter.(*coding.StoreStarter)
					if !ok || starter.DefaultHarness != domain.CodingHarnessClaudeCode || starter.DefaultModel != "retained-model" {
						t.Fatal("coding starter settings changed")
					}
					dispatcher, ok := service.codingWebhookDispatcher()
					if !ok {
						t.Fatal("coding dispatcher unavailable")
					}
					config, err := dispatcher.Settings(f.request(user, key, nil).Context(), workspace)
					if err != nil || !reflect.DeepEqual(config, settings.CodingAgentSettings) {
						t.Fatal("coding dispatcher settings changed", err)
					}
					before := fake.calls
					err = service.dispatchCodingWebhook(f.request(user, key, nil).Context(), "", event)
					if err != nil || fake.calls-before != map[bool]int{false: 0, true: 1}[enabled] {
						t.Fatalf("workspace coding feature filter changed: bounded=%t enabled=%t calls=%d err=%v keys=%v", source.BoundedMetadataEnabled(), enabled, fake.calls-before, err, fake.keys)
					}
					outcomes = append(outcomes, []any{starter.DefaultHarness, starter.DefaultModel, config, fake.calls - before})
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("coding settings differential: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
				if scaleP21M3Fingerprint(t, repo, workspace, "workspaceSettings") != unchanged {
					t.Fatal("coding settings read changed canonical properties")
				}
			}
		})
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			service := &server{store: source, coding: &codingServices{config: appconfig.CodingConfig{Enabled: true}}}
			dispatcher, ok := service.codingWebhookDispatcher()
			if !ok {
				t.Fatal("coding dispatcher unavailable")
			}
			if _, err := dispatcher.Settings(t.Context(), workspace+"-missing"); !errors.Is(err, errNotFound) {
				t.Fatalf("missing coding settings workspace: %v", err)
			}
		}
		dispatcher, _ := (&server{store: f.probe, coding: &codingServices{config: appconfig.CodingConfig{Enabled: true}}}).codingWebhookDispatcher()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := dispatcher.Settings(ctx, workspace); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled bounded settings lookup: %v", err)
		}
		settings.CodingAgentSettings.Model = strings.Repeat("selected coding model ", 16000)
		reset(true)
		if _, err := dispatcher.Settings(t.Context(), workspace); !errors.Is(err, store.ErrMetadataPageBudget) {
			t.Fatalf("large selected coding settings were omitted or accepted: %v", err)
		}
		service := &server{store: f.probe, coding: &codingServices{config: appconfig.CodingConfig{Enabled: true}}}
		if _, ok := service.codingTriggers(workspace); ok {
			t.Fatal("oversized coding settings silently fell back to defaults")
		}
		settings.CodingAgentSettings.Model = "retained-model"
		reset(true)
		if config, err := dispatcher.Settings(t.Context(), workspace); err != nil || config.Model != "retained-model" {
			t.Fatal("coding settings did not recover after budget failure", err)
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("coding settings decoded complete metadata")
		}
	}
}
