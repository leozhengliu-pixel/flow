package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func seedCatalogTeams(t *testing.T, repo *store.SQLiteStore, n int) {
	t.Helper()
	data := repo.Bootstrap()
	base := []domain.WorkflowState{}
	for _, state := range data.States {
		if state.TeamID == "" {
			base = append(base, state)
		}
	}
	err := repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, "test.bulk_teams", "seed", nil, func(next *domain.Bootstrap) error {
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("hot-team-%04d", i)
			next.Teams = append(next.Teams, domain.Team{ID: id, Name: id, Key: fmt.Sprintf("HT%03d", i)})
			next.TeamSettings[id] = domain.TeamSettings{TeamID: id, Timezone: "Etc/UTC", EstimateType: "notUsed", Access: "public"}
			for j, state := range base {
				clone := state
				clone.ID = fmt.Sprintf("%s-state-%d", id, j)
				clone.TeamID = id
				next.States = append(next.States, clone)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRecentProjectUsesDirectoryAfterRealtimeSlim(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "recent-project.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	data := repository.Bootstrap()
	if len(data.Projects) == 0 {
		t.Fatal("fixture has no projects")
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/recent", map[string]string{"type": "project", "id": data.Projects[0].ID}, http.StatusNoContent)
}

func TestIssueListAndTeamSettingsSurviveLargeCatalog(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "hotpath-list.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	seedCatalogTeams(t, repository, 400)
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	data := repository.Bootstrap()
	page := requestJSON[issueQueryResponse](t, handler, http.MethodGet, "/api/issues?archived=all&limit=5", nil, http.StatusOK)
	if page.Total == 0 || len(page.Items) == 0 {
		t.Fatalf("issue list empty: %#v", page)
	}
	teamID := data.Teams[0].ID
	patched := requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, "/api/teams/"+teamID+"/settings", map[string]any{"timezone": "Asia/Shanghai"}, http.StatusOK)
	if patched.Timezone != "Asia/Shanghai" {
		t.Fatalf("settings patch: %#v", patched)
	}
	got := requestJSON[domain.TeamSettings](t, handler, http.MethodGet, "/api/teams/"+teamID+"/settings", nil, http.StatusOK)
	if got.Timezone != "Asia/Shanghai" {
		t.Fatalf("settings get: %#v", got)
	}
}

func TestAuthenticatedAccountAndPresenceStayFastWithLargeCatalog(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "hotpath-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	catalogSize := 800
	if raceDetector {
		catalogSize = 80
	}
	seedCatalogTeams(t, repository, catalogSize)
	host := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer host.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	start := time.Now()
	authRequest[[]domain.AccountSession](t, admin, http.MethodGet, host.URL+"/api/account/sessions", nil, "test-workspace", http.StatusOK)
	authRequest[[]domain.Passkey](t, admin, http.MethodGet, host.URL+"/api/account/passkeys", nil, "test-workspace", http.StatusOK)
	authRequest[any](t, admin, http.MethodGet, host.URL+"/api/account/signing-key", nil, "test-workspace", http.StatusOK)
	authRequest[domain.UserSettings](t, admin, http.MethodGet, host.URL+"/api/account/settings", nil, "test-workspace", http.StatusOK)
	authRequest[[]domain.Presence](t, admin, http.MethodPost, host.URL+"/api/realtime/presence", map[string]any{"clientId": "hotpath-client"}, "test-workspace", http.StatusOK)
	authRequest[[]domain.Presence](t, admin, http.MethodGet, host.URL+"/api/realtime/presence", nil, "test-workspace", http.StatusOK)
	elapsed := time.Since(start)
	if !raceDetector && elapsed > 2*time.Second {
		t.Fatalf("account/presence reads cloned the catalog: %s", elapsed)
	}
}
