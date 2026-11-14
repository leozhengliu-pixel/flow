package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// These ordinary-producer witnesses retain unresolved source transitions.
// The export namespace control covers its repaired physical-key handoff.
func TestScaleP21SW24ApplicationConsentSourceHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace, teamID := f.data.Workspace.URLKey, idgen.New("sw24-consent-source-")
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw24.consent_source", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams, domain.Team{ID: teamID, Name: "Consent before\x00after", Key: "SW24"})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		observe := func(source *store.SQLiteStore) string {
			r := httptest.NewRequest(http.MethodGet, "/api/oauth/application-teams?workspace="+workspace, nil)
			ctx := context.WithValue(r.Context(), authUserContextKey{}, f.data.Viewer)
			ctx = context.WithValue(ctx, oauthWorkspaceAccessKey{}, oauthWorkspaceAccess{
				workspace: workspace, userID: f.data.Viewer.ID,
				data: domain.Bootstrap{Workspace: f.data.Workspace, Viewer: f.data.Viewer, ViewerRole: "admin"},
			})
			w := httptest.NewRecorder()
			(&server{store: source}).applicationConsentTeams(w, r.WithContext(ctx))
			if w.Code != http.StatusOK {
				t.Fatalf("consent directory status=%d body=%s", w.Code, w.Body.String())
			}
			var teams []domain.Team
			if err := json.Unmarshal(w.Body.Bytes(), &teams); err != nil {
				t.Fatal(err)
			}
			for _, team := range teams {
				if team.ID == teamID {
					return team.Name
				}
			}
			t.Fatal("consent team missing")
			return ""
		}
		for range 2 {
			if left, right := observe(repo), observe(probe); left != "Consent before\x00after" || right != "Consent beforeafter" {
				t.Fatalf("source hold disappeared: legacy=%q bounded=%q; re-review consumer before changing this assertion", left, right)
			}
		}
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if got := observe(repo); got != "Consent beforeafter" {
			t.Fatalf("canonical reload control=%q", got)
		}
	}
}

func TestScaleP21SW24ApplicationDefaultSourceHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		if driver := scaleP03DatabaseConfig(t).Driver; driver == "postgres" || driver == "postgresql" {
			t.Skip("PostgreSQL text columns cannot contain physical NUL workspace keys")
		}
		physical := idgen.New("sw24-default-source-") + "\x00tail"
		if _, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "Default source", physical, "us"); err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, physical)
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		left, ok := repo.WorkspaceMetadata("")
		right, found, err := probe.WorkspaceMetadataScoped(t.Context(), "", store.MetadataRecordScope{})
		if !ok || !found || err != nil || left.Workspace.URLKey != physical || right.Workspace.URLKey != physical {
			t.Fatalf("default core old=%q/%v bounded=%q/%v err=%v", left.Workspace.URLKey, ok, right.Workspace.URLKey, found, err)
		}
		canonical, err := probe.HydrateWorkspaceMetadata(t.Context(), physical, store.MetadataRecordScope{})
		if err != nil || canonical.Data.Workspace.URLKey != strings.ReplaceAll(physical, "\x00", "") {
			t.Fatal("canonical physical-key transformation changed", err)
		}
		task := domain.AgentTask{
			ID: idgen.New("mention_"), WorkspaceKey: physical, AppUserID: f.data.Viewer.ID,
			ResourceType: codingAnchorAgentChat, CreatorID: f.data.Viewer.ID, CodingSessionID: "sw24-source",
		}
		if err := repo.CreateMentionTask(t.Context(), task); err != nil {
			t.Fatal(err)
		}
		actor := f.data.Viewer
		actor.App = true
		observe := func(source *store.SQLiteStore) (bool, int) {
			r := httptest.NewRequest(http.MethodGet, "/api/application-agent-tasks", nil)
			r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, actor))
			service := &server{store: source}
			actual, _, err := service.authorizedAgentTask(r, task.ID, false)
			authorized := err == nil && actual.ID == task.ID
			w := httptest.NewRecorder()
			service.listApplicationAgentTasks(w, r)
			var tasks []domain.AgentTask
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &tasks) != nil {
				t.Fatalf("default list status=%d body=%s", w.Code, w.Body.String())
			}
			return authorized, len(tasks)
		}
		for range 2 {
			oldAllowed, oldSize := observe(repo)
			newAllowed, newSize := observe(probe)
			if !oldAllowed || newAllowed || !reflect.DeepEqual([]int{oldSize, newSize}, []int{1, 0}) {
				t.Fatalf("installed identity helper hold disappeared: authorized old/new=%v/%v list sizes=%d/%d; re-review its consumer before certification", oldAllowed, newAllowed, oldSize, newSize)
			}
		}
	}
}

func TestScaleP21SW24WorkerDefinitionSourceHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		service := &server{store: repo, authDisabled: true}
		r := httptest.NewRequest(http.MethodPost, "/api/loops?workspace="+f.data.Workspace.URLKey,
			strings.NewReader(`{"name":"Worker source","instructions":"Inspect before\u0000after","status":"draft"}`))
		r.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
		w := httptest.NewRecorder()
		service.createLoop(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("ordinary loop create: %d %s", w.Code, w.Body.String())
		}
		var created domain.Loop
		if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.ID == "" || created.Instructions != "Inspect beforeafter" {
			t.Fatal("HTTP NUL stripping control", created.Instructions, err)
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "loops"), f.data.Workspace.URLKey, "scale_p21_sw24.worker_source", created.ID, nil, func(data *domain.Bootstrap) error {
			for index := range data.Loops {
				if data.Loops[index].ID == created.ID {
					data.Loops[index].Instructions = "Inspect before\x00after"
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := probe.HydrateWorkspaceMetadata(t.Context(), f.data.Workspace.URLKey, store.MetadataRecordScope{
			Read: []store.RecordKey{{Kind: "loops", ID: created.ID}},
		})
		if err != nil || len(canonical.Data.Loops) != 1 || canonical.Data.Loops[0].Instructions != "Inspect beforeafter" {
			t.Fatal("canonical loop transformation changed", err)
		}
		for range 2 {
			left, ok, err := service.workerLoopDefinition(t.Context(), f.data.Workspace.URLKey, created.ID)
			if err != nil || !ok || left == nil {
				t.Fatal("legacy worker input", err)
			}
			right, ok, err := (&server{store: probe}).workerLoopDefinition(t.Context(), f.data.Workspace.URLKey, created.ID)
			if err != nil || !ok || right == nil {
				t.Fatal("bounded worker input", err)
			}
			if left.Instructions != "Inspect before\x00after" || !reflect.DeepEqual(left, right) ||
				loopModelInstructions(*left) != loopModelInstructions(*right) || loopContractHashOf(*left) != loopContractHashOf(*right) {
				t.Fatal("installed worker definition differs")
			}
		}
		if err := repo.RefreshMetadataRecord(t.Context(), f.data.Workspace.URLKey, "loops", created.ID); err != nil {
			t.Fatal(err)
		}
		if err := repo.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		reloaded, ok, err := service.workerLoopDefinition(t.Context(), f.data.Workspace.URLKey, created.ID)
		if err != nil || !ok || reloaded == nil || reloaded.Instructions != "Inspect beforeafter" {
			t.Fatal("canonical reload control", err)
		}
	}
}

func TestScaleP21SW24ExportNamespaceSourceDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		if driver := scaleP03DatabaseConfig(t).Driver; driver == "postgres" || driver == "postgresql" {
			t.Skip("PostgreSQL text columns cannot contain physical NUL workspace keys")
		}
		physical, jobID := idgen.New("sw24-export-source-")+"\x00tail", idgen.New("sw24-export-")
		if _, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "Export source", physical, "us"); err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, physical)
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "exportJobs"), physical, "scale_p21_sw24.export_source", "", nil, func(data *domain.Bootstrap) error {
			data.ExportJobs = append(data.ExportJobs, domain.ExportJob{ID: jobID, UserID: f.data.Viewer.ID, Status: "failed", Format: "json"})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			var codes, sizes []int
			var totals []string
			for _, source := range []*store.SQLiteStore{repo, probe} {
				service := &server{store: source, authDisabled: true}
				r := httptest.NewRequest(http.MethodGet, "/api/exports", nil)
				r.Header.Set("X-Workspace-Key", physical)
				r.SetPathValue("id", jobID)
				w := httptest.NewRecorder()
				service.getExport(w, r)
				codes = append(codes, w.Code)
				w = httptest.NewRecorder()
				service.listExports(w, r)
				var jobs []domain.ExportJob
				if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &jobs) != nil {
					t.Fatalf("export list status=%d body=%s", w.Code, w.Body.String())
				}
				sizes, totals = append(sizes, len(jobs)), append(totals, w.Header().Get("X-Total-Count"))
			}
			if !reflect.DeepEqual(codes, []int{http.StatusOK, http.StatusOK}) ||
				!reflect.DeepEqual(sizes, []int{1, 1}) || !reflect.DeepEqual(totals, []string{"1", "1"}) {
				t.Fatalf("physical export namespace differs: get=%v sizes=%v totals=%v", codes, sizes, totals)
			}
		}
	}
}
