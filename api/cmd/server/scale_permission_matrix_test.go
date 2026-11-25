package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The full bootstrap is the frozen semantic oracle for later packages. Compare
// serialized fields without normalizing null/empty or dropping unknown fields.
func scaleP01EqualJSON(t *testing.T, expected, actual any) {
	t.Helper()
	want, got := scaleP01ComparableJSON(t, expected), scaleP01ComparableJSON(t, actual)
	if !reflect.DeepEqual(want, got) {
		fields := []string{}
		if a, ok := want.(map[string]any); ok {
			b, _ := got.(map[string]any)
			for key, value := range a {
				if !reflect.DeepEqual(value, b[key]) {
					fields = append(fields, key)
				}
			}
		}
		slices.Sort(fields)
		t.Fatalf("permission differential fields=%v (payloads suppressed)", fields)
	}
}

func scaleP01ComparableJSON(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// Frozen adapter for the existing REST list shape; details keep these fields.
func scaleP01LegacyProjectList(project domain.Project) domain.Project {
	project.Comments, project.CommentAttachments, project.DescriptionRevisions = nil, nil, nil
	project.IssueCountHistory, project.ScopeHistory, project.CompletedScopeHistory, project.InProgressScopeHistory, project.ProgressHistory = nil, nil, nil, nil, nil
	project.Milestones = slices.Clone(project.Milestones)
	for i := range project.Milestones {
		project.Milestones[i].DescriptionRevisions = nil
	}
	return project
}

type scaleP01PermissionViewer struct {
	name            string
	client          *http.Client
	secret          string
	private, shared bool
}

func scaleP01TestDatabaseConfig(t *testing.T, config store.DatabaseConfig, externalConnections int) store.DatabaseConfig {
	t.Helper()
	driver := strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_TEST_DATABASE_DRIVER")))
	if driver == "" {
		return config
	}
	config.Driver, config.URL = driver, os.Getenv("FLOW_TEST_DATABASE_URL")
	if config.URL == "" {
		t.Fatal("FLOW_TEST_DATABASE_URL is required")
	}
	config.MaxOpenConns = externalConnections
	if driver == "sqlite" {
		config.Path = filepath.Clean(config.URL)
		if !filepath.IsAbs(config.Path) || (!strings.HasPrefix(config.Path, "/tmp/codex-scale-p01/") && !strings.HasPrefix(config.Path, "/private/tmp/codex-scale-p01/") && !strings.HasPrefix(config.Path, "/tmp/codex-scale-key-leak/") && !strings.HasPrefix(config.Path, "/private/tmp/codex-scale-key-leak/") && !strings.HasPrefix(config.Path, "/tmp/codex-scale-p14/") && !strings.HasPrefix(config.Path, "/private/tmp/codex-scale-p14/")) {
			t.Fatal("explicit SQLite test paths must be inside a P01, P14 or scale-key-leak scratch directory")
		}
		config.MaxOpenConns = 1
	}
	return config
}

func scaleP01PermissionRead(t *testing.T, viewer scaleP01PermissionViewer, base, workspace, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Workspace-Key", workspace)
	if viewer.secret != "" {
		req.Header.Set("Authorization", "Bearer "+viewer.secret)
	}
	response, err := viewer.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, raw
}

func TestScaleP01PermissionMatrix(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	for name, config := range scaleP01PermissionConfigs(t) {
		t.Run(name, func(t *testing.T) { scaleP01PermissionMatrix(t, config) })
	}
}

func scaleP01PermissionMatrix(t *testing.T, config store.DatabaseConfig) {
	t.Helper()
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	srv, host, admin, data, owned := scaleP01OwnedWorkspace(t, repo)
	base, workspace := host.URL, data.Workspace.URLKey
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	teamsURL := base + "/api/workspaces/" + workspace + "/teams"
	public := authRequest[domain.Team](t, admin, "POST", teamsURL, map[string]any{"name": "P01 public " + suffix, "key": "P1A" + suffix[len(suffix)-4:]}, "", http.StatusCreated)
	private := authRequest[domain.Team](t, admin, "POST", teamsURL, map[string]any{"name": "P01 private " + suffix, "key": "P1B" + suffix[len(suffix)-4:], "private": true}, "", http.StatusCreated)
	child := authRequest[domain.Team](t, admin, "POST", teamsURL, map[string]any{"name": "P01 child " + suffix, "key": "P1C" + suffix[len(suffix)-4:], "parentTeamId": private.ID}, "", http.StatusCreated)
	outsideAdmin, _ := scaleP01Join(t, owned, base, workspace, admin, "P01 outside admin", "admin", public.ID)
	member, memberUser := scaleP01Join(t, owned, base, workspace, admin, "P01 member", "member", public.ID, private.ID)
	guest, guestUser := scaleP01Join(t, owned, base, workspace, admin, "P01 guest", "guest", public.ID)
	outsider, _ := scaleP01Join(t, owned, base, workspace, admin, "P01 outsider", "member", public.ID)
	projectID, hiddenID, childID := "p01-shared-"+suffix, "p01-hidden-"+suffix, "p01-child-"+suffix
	if err := repo.MutateWorkspace(store.ContextWithActor(t.Context(), data.Viewer), workspace, "test.p01_fixture", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects,
			domain.Project{ID: hiddenID, SlugID: "hidden-000000000001", Name: "P01 hidden", TeamIDs: []string{private.ID}},
			domain.Project{ID: childID, SlugID: "child-000000000003", Name: "P01 inherited", TeamIDs: []string{child.ID}},
			domain.Project{ID: projectID, SlugID: "current-000000000002", PreviousSlugIDs: []string{"old-p01-" + suffix}, Name: "P01 shared", TeamIDs: []string{public.ID, private.ID}, DependencyIDs: []string{hiddenID}, IssueCount: 42, Resources: []domain.ProjectResource{}, Comments: []domain.Comment{}},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	issue := authRequest[domain.Issue](t, admin, "POST", base+"/api/issues", map[string]any{"title": "P01 explicitly granted", "teamId": private.ID, "projectId": projectID}, workspace, http.StatusCreated)
	authRequest[[]domain.IssuePermission](t, admin, "PUT", base+"/api/issues/"+issue.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": guestUser.ID, "role": "viewer"}}}, workspace, http.StatusOK)
	viewers := []scaleP01PermissionViewer{
		{"admin", admin, "", true, true}, {"admin-outside-private", outsideAdmin, "", false, true},
		{"member-and-subteam-inheritance", member, "", true, true}, {"guest-explicit-grant", guest, "", false, true}, {"private-outsider", outsider, "", false, true},
	}
	for _, spec := range []struct {
		name, restriction string
		teams             []string
		private, shared   bool
	}{
		{"unrestricted", "all", nil, true, true}, {"selected-empty", "selected", []string{}, false, false}, {"selected-set", "selected", []string{public.ID}, false, true},
	} {
		teams := spec.teams
		if spec.name == "selected-empty" {
			teams = []string{public.ID}
		}
		key := authRequest[struct {
			Secret string        `json:"secret"`
			Key    domain.APIKey `json:"key"`
		}](t, admin, "POST", base+"/api/api-keys", map[string]any{"name": "P01 " + spec.name, "scopes": []string{"read"}, "teamRestriction": spec.restriction, "teamIds": teams}, workspace, http.StatusCreated)
		if spec.name == "selected-empty" {
			// Creation rejects this state; persisted keys can reach it after
			// their selected teams disappear. It must remain deny-all.
			if err := repo.MutateWorkspace(t.Context(), workspace, "api_key.updated", key.Key.ID, nil, func(data *domain.Bootstrap) error {
				for i := range data.APIKeys {
					if data.APIKeys[i].ID == key.Key.ID {
						data.APIKeys[i].TeamIDs = []string{}
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		viewers = append(viewers, scaleP01PermissionViewer{"key-" + spec.name, &http.Client{Timeout: 10 * time.Second}, key.Secret, spec.private, spec.shared})
	}
	for _, viewer := range viewers {
		t.Run(viewer.name, func(t *testing.T) {
			code, raw := scaleP01PermissionRead(t, viewer, base, workspace, "/api/bootstrap")
			if code != http.StatusOK {
				t.Fatalf("bootstrap status=%d", code)
			}
			var oracle domain.Bootstrap
			if err := json.Unmarshal(raw, &oracle); err != nil {
				t.Fatal(err)
			}
			// REST list nodes and detail records share their field contract.
			// Bootstrap's scoped-key team reference shape is a separate legacy
			// contract, so it remains a visibility oracle only.
			code, raw = scaleP01PermissionRead(t, viewer, base, workspace, "/api/projects?first=100")
			var listed connection[json.RawMessage]
			if code != http.StatusOK || json.Unmarshal(raw, &listed) != nil {
				t.Fatal("project list unavailable")
			}
			for _, target := range []struct {
				id      string
				allowed bool
			}{{hiddenID, viewer.private}, {childID, viewer.private}, {projectID, viewer.shared}} {
				index := slices.IndexFunc(oracle.Projects, func(project domain.Project) bool { return project.ID == target.id })
				if (index >= 0) != target.allowed {
					t.Fatalf("oracle visibility for %s=%v, want %v", target.id, index >= 0, target.allowed)
				}
				refs := []string{target.id}
				if target.id == projectID {
					refs = append(refs, "old-p01-"+suffix, "stale-prefix-000000000002")
				}
				for _, ref := range refs {
					code, raw := scaleP01PermissionRead(t, viewer, base, workspace, "/api/projects/"+ref)
					if !target.allowed {
						if code != http.StatusNotFound && code != http.StatusForbidden {
							t.Fatalf("hidden record status=%d", code)
						}
						continue
					}
					if code != http.StatusOK {
						t.Fatalf("record status=%d", code)
					}
					var actual domain.Project
					if err := json.Unmarshal(raw, &actual); err != nil {
						t.Fatal(err)
					}
					var expected json.RawMessage
					for _, node := range listed.Nodes {
						var item struct {
							ID string `json:"id"`
						}
						if json.Unmarshal(node, &item) == nil && item.ID == target.id {
							expected = node
							break
						}
					}
					if expected == nil {
						t.Fatal("visible record absent from REST list")
					}
					scaleP01EqualJSON(t, expected, scaleP01LegacyProjectList(actual))
					wantDetail := oracle.Projects[index]
					// The existing detail/list routes retain the stored aggregate
					// zero; full bootstrap derives its count from visible issues.
					wantDetail.IssueCount = 0
					if viewer.name == "key-selected-set" {
						wantDetail.TeamIDs = []string{public.ID}
					}
					scaleP01EqualJSON(t, wantDetail, json.RawMessage(raw))
					visibleCount := 0
					for _, item := range oracle.Issues {
						if item.Project != nil && item.Project.ID == target.id {
							visibleCount++
						}
					}
					bootstrapCountLeak := oracle.Projects[index].IssueCount > visibleCount
					if viewer.name == "admin" && target.id == projectID && oracle.Projects[index].IssueCount != 1 {
						t.Fatal("hidden-count fixture has no nonzero control")
					}
					leaks := !viewer.private && (slices.Contains(actual.TeamIDs, private.ID) || slices.Contains(actual.DependencyIDs, hiddenID) || actual.IssueCount > visibleCount || bootstrapCountLeak)
					if leaks {
						t.Fatal("hidden reference or count leaked")
					}
				}
			}
			// Explicit issue grants are independent of private project visibility.
			issueIndex := slices.IndexFunc(oracle.Issues, func(item domain.Issue) bool { return item.ID == issue.ID })
			code, _ = scaleP01PermissionRead(t, viewer, base, workspace, "/api/issue-records/"+issue.ID)
			if (code == http.StatusOK) != (issueIndex >= 0) {
				t.Fatalf("issue grant differs: bootstrap=%v record=%d", issueIndex >= 0, code)
			}
			if viewer.name == "guest-explicit-grant" && issueIndex < 0 {
				t.Fatal("explicit guest grant absent")
			}
		})
	}
	// A queued frame is projected again against current access. The old
	// projected payload must not survive revocation, including its actor.
	queued := domain.RealtimeEvent{ID: "p01-queued", Type: "project.updated", AggregateID: childID, ActorID: data.Viewer.ID, Payload: json.RawMessage(`{"entity":{"id":"` + childID + `","description":"P01 private body"}}`)}
	request := httptest.NewRequest("GET", "/api/realtime/events", nil)
	request.Header.Set("X-Workspace-Key", workspace)
	request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, memberUser))
	projected, visible, err := srv.pagedRealtimeEvent(request, queued)
	if err != nil || !visible || !bytes.Contains(projected.Payload, []byte(childID)) {
		t.Fatal("inherited project not visible before revocation")
	}
	head := authRequest[changesResponse](t, member, "GET", base+"/api/realtime/changes?since=0", nil, workspace, http.StatusOK).Head
	if err := repo.MutateWorkspace(store.ContextWithActor(t.Context(), data.Viewer), workspace, "project.updated", childID, map[string]string{"name": "queued change"}, func(data *domain.Bootstrap) error { return nil }); err != nil {
		t.Fatal(err)
	}
	authRequest[any](t, admin, "DELETE", base+"/api/teams/"+private.ID+"/members/"+memberUser.ID, nil, workspace, http.StatusOK)
	after, visible, err := srv.pagedRealtimeEvent(request, projected)
	if err != nil {
		t.Fatal(err)
	}
	if visible && (after.AggregateID != "" || after.ActorID != "" || len(after.Payload) != 0) {
		t.Fatal("queued projection/actor survived revocation")
	}
	page := authRequest[changesResponse](t, member, "GET", base+fmt.Sprintf("/api/realtime/changes?since=%d", head), nil, workspace, http.StatusOK)
	if page.Next <= head {
		t.Fatal("filtered replay did not advance scanned cursor")
	}
	for _, event := range page.Events {
		if event.AggregateID == childID || strings.Contains(string(event.Payload), childID) {
			t.Fatal("revoked project leaked during replay")
		}
	}
	otherKey := "p01-other-" + suffix
	authRequest[domain.Bootstrap](t, admin, "POST", base+"/api/workspaces", map[string]string{"name": "P01 other", "urlKey": otherKey}, "", http.StatusCreated)
	owned.workspaces = append(owned.workspaces, otherKey)
	for _, viewer := range viewers {
		code, raw := scaleP01PermissionRead(t, viewer, base, otherKey, "/api/projects/"+projectID)
		if code == http.StatusOK || bytes.Contains(raw, []byte(projectID)) {
			t.Fatalf("cross-workspace access by %s", viewer.name)
		}
	}
}

func TestScaleP01DifferentialPreservesEmptyAndNull(t *testing.T) {
	if reflect.DeepEqual(scaleP01ComparableJSON(t, []string{}), scaleP01ComparableJSON(t, []string(nil))) {
		t.Fatal("differential normalized empty and null collections")
	}
	if reflect.DeepEqual(scaleP01ComparableJSON(t, map[string]any{}), scaleP01ComparableJSON(t, map[string]any{"extra": nil})) {
		t.Fatal("differential discarded an additional field")
	}
	scaleP01EqualJSON(t, map[string]any{"list": []string{}, "nullable": nil}, map[string]any{"list": []string{}, "nullable": nil})
}
