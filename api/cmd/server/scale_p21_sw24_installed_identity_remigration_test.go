package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP21SW24IdentityRequest(user domain.User, key *domain.APIKey, workspace, path string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("X-Workspace-Key", workspace)
	ctx := context.WithValue(r.Context(), authUserContextKey{}, user)
	if key != nil {
		ctx = context.WithValue(ctx, apiKeyContextKey{}, *key)
	}
	return r.WithContext(ctx)
}

func scaleP21SW24IdentityOutcome(w *httptest.ResponseRecorder) any {
	return []any{w.Code, w.Body.String(), w.Header().Get("X-Total-Count"), w.Header().Get("X-Next-Cursor")}
}

func TestScaleP21SW24InstalledDefaultTaskSourceDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		if driver := scaleP03DatabaseConfig(t).Driver; driver == "postgres" || driver == "postgresql" {
			t.Skip("PostgreSQL text columns cannot retain a physical NUL key")
		}
		physical := idgen.New("sw24-default-remigration-") + "\x00tail"
		if _, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "Default remigration", physical, "us"); err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, physical)
		task := domain.AgentTask{
			ID: idgen.New("mention_"), WorkspaceKey: physical, AppUserID: f.data.Viewer.ID,
			CreatorID: f.data.Viewer.ID, ResourceType: codingAnchorAgentChat, CodingSessionID: "sw24-remigration",
		}
		if err := repo.CreateMentionTask(t.Context(), task); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		actor := f.data.Viewer
		actor.App = true
		for _, stage := range []string{"warm", "reloaded"} {
			if stage == "reloaded" {
				if err := repo.ReloadWorkspace(t.Context(), physical); err != nil {
					t.Fatal(err)
				}
				if err := probe.ReloadWorkspace(t.Context(), physical); err != nil {
					t.Fatal(err)
				}
			}
			for _, source := range []*store.SQLiteStore{repo, probe} {
				service := &server{store: source}
				for _, binding := range []string{physical, ""} {
					for _, id := range []string{task.ID, task.ID + "-missing"} {
						for _, editing := range []bool{false, true} {
							r := scaleP21SW24IdentityRequest(actor, nil, binding, "/api/application-agent-tasks")
							left, leftData, leftErr := service.scaleP21SW24OldAuthorizedAgentTask(r, id, editing)
							right, rightData, rightErr := service.authorizedAgentTask(r, id, editing)
							message := func(err error) string {
								if err != nil {
									return err.Error()
								}
								return ""
							}
							old := []any{left, leftData.Workspace.URLKey, leftData.Viewer.ID, leftData.ViewerRole, message(leftErr)}
							current := []any{right, rightData.Workspace.URLKey, rightData.Viewer.ID, rightData.ViewerRole, message(rightErr)}
							if !reflect.DeepEqual(old, current) {
								t.Fatalf("default identity differs stage=%s bounded=%v binding=%q: %s", stage, source.BoundedMetadataEnabled(), binding, scaleP21M2Diff(old, current))
							}
						}
					}
					for _, archived := range []bool{false, true} {
						r := scaleP21SW24IdentityRequest(actor, nil, binding, "/api/application-agent-tasks")
						if archived {
							r.URL.RawQuery = "includeArchived=true"
						}
						left, right := httptest.NewRecorder(), httptest.NewRecorder()
						service.scaleP21SW24OldListApplicationAgentTasks(left, r)
						service.listApplicationAgentTasks(right, r)
						if !reflect.DeepEqual(scaleP21SW24IdentityOutcome(left), scaleP21SW24IdentityOutcome(right)) {
							t.Fatalf("task list differs stage=%s bounded=%v binding=%q: old=%v new=%v", stage, source.BoundedMetadataEnabled(), binding, scaleP21SW24IdentityOutcome(left), scaleP21SW24IdentityOutcome(right))
						}
					}
				}
			}
		}
		// A same-source replacement preserves the old default's logical key;
		// it does not claim warm full versus cold resident source equality.
	}
}

func TestScaleP21SW24InstalledExportNamespaceDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		if driver := scaleP03DatabaseConfig(t).Driver; driver == "postgres" || driver == "postgresql" {
			t.Skip("PostgreSQL text columns cannot retain a physical NUL key")
		}
		physical, id := idgen.New("sw24-export-remigration-")+"\x00tail", idgen.New("sw24-job-")
		if _, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "Export remigration", physical, "us"); err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, physical)
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "exportJobs"), physical, "scale_p21_sw24.namespace_remigration", "", nil, func(data *domain.Bootstrap) error {
			data.ExportJobs = append(data.ExportJobs, domain.ExportJob{ID: id, UserID: f.data.Viewer.ID, Status: "failed", Format: "json"})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, stage := range []string{"warm", "reloaded"} {
			if stage == "reloaded" {
				if err := repo.ReloadWorkspace(t.Context(), physical); err != nil {
					t.Fatal(err)
				}
				if err := probe.ReloadWorkspace(t.Context(), physical); err != nil {
					t.Fatal(err)
				}
			}
			for _, development := range []bool{false, true} {
				for _, key := range []*domain.APIKey{nil, {Scopes: []string{"read"}}, {Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}} {
					for _, binding := range []string{physical, "", physical + "-missing", strings.ReplaceAll(physical, "\x00", "")} {
						for _, operation := range []func(*server, http.ResponseWriter, *http.Request){(*server).getExport, (*server).listExports} {
							var outcomes []any
							for _, source := range []*store.SQLiteStore{repo, probe} {
								service := &server{store: source, authDisabled: development}
								r := scaleP21SW24IdentityRequest(f.data.Viewer, key, binding, "/api/exports")
								r.SetPathValue("id", id)
								w := httptest.NewRecorder()
								operation(service, w, r)
								outcomes = append(outcomes, scaleP21SW24IdentityOutcome(w))
							}
							if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
								t.Fatalf("export namespace differs stage=%s development=%v binding=%q: %s", stage, development, binding, scaleP21M2Diff(outcomes[0], outcomes[1]))
							}
						}
					}
				}
			}
		}
	}
}

func TestScaleP21SW24InstalledExportIdentityLifetime(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		service := &server{store: probe, authDisabled: true}
		r := scaleP21SW24IdentityRequest(f.data.Viewer, nil, f.data.Workspace.URLKey, "/api/exports")
		workspace, _, _, read, err := service.boundedExportJobAccess(r)
		if err != nil || read == nil || workspace != f.data.Workspace.URLKey || read.Identity.RegistryKey != workspace {
			t.Fatal("identity lifetime was not transferred to the caller", err)
		}
		if _, _, err := probe.ExportJobPage(t.Context(), workspace, "", true, 0, 1); err != nil || read.Identity.ID != f.data.Workspace.ID {
			t.Fatal("identity was cleared before downstream use", err)
		}
		if err := read.Close(); err != nil || read.Identity != (store.InstalledWorkspaceIdentity{}) {
			t.Fatal("caller close did not end the owned identity lifetime", err)
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		_, _, _, read, err = service.boundedExportJobAccess(r.WithContext(cancelled))
		if err == nil || read != nil {
			t.Fatal("cancelled helper returned a live identity")
		}
	}
}

func TestScaleP21SW24ExportCompletionSourceHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		if driver := scaleP03DatabaseConfig(t).Driver; driver == "postgres" || driver == "postgresql" {
			t.Skip("PostgreSQL text columns cannot retain a physical NUL key")
		}
		physical, id := idgen.New("sw24-export-completion-")+"\x00tail", idgen.New("sw24-job-")
		if _, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "Export completion", physical, "us"); err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, physical)
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "exportJobs"), physical, "scale_p21_sw24.completion_source", "", nil, func(data *domain.Bootstrap) error {
			data.ExportJobs = append(data.ExportJobs, domain.ExportJob{ID: id, UserID: f.data.Viewer.ID, Status: "queued", Format: "json"})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		(&server{store: repo, authDisabled: true}).completeExport(physical, id)
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, list := range []bool{false, true} {
			var names []string
			for _, source := range []*store.SQLiteStore{repo, probe} {
				service := &server{store: source, authDisabled: true}
				r := scaleP21SW24IdentityRequest(f.data.Viewer, nil, physical, "/api/exports")
				r.SetPathValue("id", id)
				w := httptest.NewRecorder()
				var jobs []domain.ExportJob
				if list {
					service.listExports(w, r)
					if json.Unmarshal(w.Body.Bytes(), &jobs) != nil {
						t.Fatal("decode completion list", w.Body.String())
					}
				} else {
					service.getExport(w, r)
					jobs = make([]domain.ExportJob, 1)
					if json.Unmarshal(w.Body.Bytes(), &jobs[0]) != nil {
						t.Fatal("decode completion point", w.Body.String())
					}
				}
				if w.Code != http.StatusOK || len(jobs) != 1 || jobs[0].Status != "completed" {
					t.Fatalf("real export completion status=%d body=%s", w.Code, w.Body.String())
				}
				names = append(names, jobs[0].Filename)
			}
			if !strings.ContainsRune(names[0], '\x00') || names[1] != strings.ReplaceAll(names[0], "\x00", "") {
				t.Fatalf("completion source hold changed: filenames=%q", names)
			}
			t.Logf("normal completeExport producer list=%v: legacy filename=%q bounded=%q", list, names[0], names[1])
		}
	}
}

// These two oracles retain the literal pre-SW20 methods' default selection.
// Their downstream permission code is unchanged by this patch.
func (s *server) scaleP21SW24OldAuthorizedAgentTask(r *http.Request, id string, editing bool) (domain.AgentTask, domain.Bootstrap, error) {
	workspace := workspaceKey(r)
	if workspace == "" {
		if metadata, ok := s.store.WorkspaceMetadata(""); ok {
			workspace = metadata.Workspace.URLKey
		}
	}
	task, err := s.store.AgentTask(r.Context(), workspace, id)
	if err != nil {
		return task, domain.Bootstrap{}, errNotFound
	}
	data, err := s.applicationTaskContext(r, task, editing)
	if err != nil {
		return task, data, errNotFound
	}
	if data.Viewer.App && task.AppUserID != data.Viewer.ID {
		return task, data, errNotFound
	}
	if task.CodingSessionID != "" && data.ViewerRole == "guest" && task.CreatorID != data.Viewer.ID {
		return task, data, errNotFound
	}
	return task, data, nil
}

func (s *server) scaleP21SW24OldListApplicationAgentTasks(w http.ResponseWriter, r *http.Request) {
	actor := requestActor(s, r)
	issueID := r.URL.Query().Get("issueId")
	resourceType := r.URL.Query().Get("resourceType")
	if resourceType == "" {
		resourceType = "issue"
	}
	if r.URL.Query().Get("resourceId") != "" {
		issueID = r.URL.Query().Get("resourceId")
	}
	if issueID == "" && !actor.App {
		writeError(w, 400, "issueId is required")
		return
	}
	if issueID != "" {
		if _, err := s.applicationTaskContext(r, domain.AgentTask{ResourceType: resourceType, ResourceID: issueID, IssueID: issueID}, false); err != nil {
			writeError(w, 404, "Issue not found")
			return
		}
	}
	appID := ""
	if actor.App {
		appID = actor.ID
	}
	workspace := workspaceKey(r)
	if workspace == "" {
		if metadata, ok := s.store.WorkspaceMetadata(""); ok {
			workspace = metadata.Workspace.URLKey
		}
	}
	list := s.store.ListAgentTasks
	if r.URL.Query().Get("includeArchived") == "true" {
		list = s.store.ListAgentTasksIncludingArchived
	}
	tasks, err := list(r.Context(), workspace, issueID, appID)
	if err != nil {
		respondMutation(w, err, 200, nil)
		return
	}
	visible := []domain.AgentTask{}
	for _, task := range tasks {
		if _, _, err := s.scaleP21SW24OldAuthorizedAgentTask(r, task.ID, false); err == nil {
			task.DeriveTimedState(time.Now())
			visible = append(visible, task)
		}
	}
	writeJSON(w, 200, visible)
}
