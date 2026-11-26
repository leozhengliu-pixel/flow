package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"

	"github.com/alicebob/miniredis/v2"
)

func TestIssueReadFreshnessDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_PEER_SYNC", "off")
	t.Setenv("FLOW_METADATA_CATALOG", "off")
	config := scaleP03DatabaseConfig(t)
	open := func(t *testing.T) *store.SQLiteStore {
		t.Helper()
		repo, err := store.OpenDatabase(config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Close() })
		return repo
	}
	seed := open(t)
	f := scaleP03Fixture(t, seed)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	public, private := f.data.Teams[0].ID, idgen.New("freshness-team-")
	ctx := store.ContextWithActor(t.Context(), f.data.Viewer)
	if err := seed.MutateWorkspace(ctx, workspace, "test.freshness_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: private, Key: "FRESH", Name: "Freshness Team", Private: true})
		data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := seed.SetTeamMembership(ctx, workspaceID, private, f.data.Viewer.ID, "owner", true); err != nil {
		t.Fatal(err)
	}
	member, user := scaleP03Join(t, f, "Freshness member", idgen.New("freshness-")+"@example.test", "member", public, private)
	key := scaleP03Key(t, f, []string{"read"}, []string{public})
	post := func(team string) domain.Issue {
		return authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
			map[string]string{"title": "Freshness witness", "teamId": team}, workspace, http.StatusCreated)
	}
	visible, restricted := post(public), post(private)
	f.host.Close()
	pinCreatedIssueAges(t, seed, workspace, false, visible.ID)
	report, err := seed.PrepareBoundedMetadata(t.Context())
	if err != nil || !report.Ready {
		t.Fatalf("metadata preparation: ready=%t, err=%v", report.Ready, err)
	}

	for _, flag := range []string{"off", "on"} {
		t.Run(flag, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", flag)
			redis := miniredis.RunT(t)
			coordinator, err := coordination.Open(t.Context(), coordination.Config{
				Mode: "standalone", Addrs: []string{redis.Addr()}, Prefix: idgen.New("freshness"), ConnectTimeout: time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = coordinator.Close() })
			newPeer := func() (*server, *httptest.Server) {
				service := &server{store: open(t), coordinator: coordinator, uploadPath: t.TempDir()}
				// Let Redis receive versions, while both replicas miss pubsub.
				service.coordinationStarted.Store(true)
				host := httptest.NewServer(newHandler(service))
				t.Cleanup(host.Close)
				if err := service.stopDeliveryScheduler(t.Context()); err != nil {
					t.Fatal(err)
				}
				return service, host
			}
			writer, first := newPeer()
			peer, second := newPeer()
			if peer.store.BoundedMetadataEnabled() != (flag == "on") {
				t.Fatal("incorrect metadata mode")
			}
			detail := "/api/issue-records/" + visible.Identifier
			title := fmt.Sprintf("Writer change %s", flag)
			result := authRequest[domain.Issue](t, f.admin, http.MethodPatch, first.URL+detail,
				map[string]string{"title": title}, workspace, http.StatusOK)
			read := func(url string) {
				t.Helper()
				got := authRequest[domain.Issue](t, f.admin, http.MethodGet, url+detail, nil, workspace, http.StatusOK)
				if got.ID != result.ID || got.Title != title || got.Version != result.Version {
					t.Fatal("read did not include the committed issue edit")
				}
			}
			read(first.URL)

			// Force a metadata lag unrelated to the issue edit.
			if err := seed.MutateWorkspace(ctx, workspace, "test.freshness_metadata", "", nil, func(data *domain.Bootstrap) error {
				data.Workspace.Name = "Freshness workspace " + flag
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := coordinator.CacheIncr(t.Context(), peer.workspaceVersionKey(workspace)); err != nil {
				t.Fatal(err)
			}
			if state, err := peer.store.WorkspacePeerState(t.Context(), workspace); err != nil || state.Current {
				t.Fatalf("peer must be behind before the read: current=%t, err=%v", state.Current, err)
			}
			if flag == "on" {
				peer.workspaceVersions.mu.Lock()
				peer.workspaceVersions.loading = map[string]*workspaceReload{workspace: {done: make(chan struct{})}}
				peer.workspaceVersions.mu.Unlock()
			}
			read(second.URL)
			if flag == "on" {
				state, err := peer.store.WorkspacePeerState(t.Context(), workspace)
				if err != nil || state.Current {
					t.Fatalf("issue read should not wait for the installed image: current=%t, err=%v", state.Current, err)
				}
			}

			getWithKey := func(path string) *http.Response {
				t.Helper()
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, second.URL+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("X-Workspace-Key", workspace)
				req.Header.Set("Authorization", "Bearer "+key)
				response, err := second.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = response.Body.Close() })
				return response
			}
			if response := getWithKey("/api/issue-records/" + restricted.ID); response.StatusCode != http.StatusNotFound {
				t.Fatalf("selected API key read returned %d", response.StatusCode)
			}
			page := authRequest[store.IssueRecordPage](t, member, http.MethodGet, second.URL+"/api/issue-records", nil, workspace, http.StatusOK)
			if !slices.ContainsFunc(page.Items, func(issue domain.Issue) bool { return issue.ID == restricted.ID }) {
				t.Fatal("member did not see their private-team issue")
			}
			if flag == "on" {
				testIssueReadDuringChange(t, peer, second.URL, workspace, restricted.ID, member, http.StatusOK, func() error {
					return seed.MutateWorkspace(ctx, workspace, "test.freshness_metadata", "", nil, func(data *domain.Bootstrap) error {
						data.Workspace.Name = "Freshness workspace edited"
						return nil
					})
				})
				testIssueReadDuringChange(t, peer, second.URL, workspace, restricted.ID, member, http.StatusNotFound, func() error {
					return writer.store.SetTeamMembership(t.Context(), workspaceID, private, user.ID, "member", false)
				})
			}
		})
	}
}

func testIssueReadDuringChange(t *testing.T, peer *server, peerURL, workspace, issueID string, member *http.Client, status int, change func() error) {
	t.Helper()
	mux := http.NewServeMux()
	attempts := 0
	mux.HandleFunc("GET /api/issue-records/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Value(issueReadSnapshotKey{}).(*issueReadAttempt); !ok {
			t.Error("expected the read snapshot after authorization")
			return
		}
		attempts++
		if attempts == 1 {
			if err := change(); err != nil {
				t.Error(err)
				return
			}
		}
		peer.getIssueRecord(w, r)
	})
	handler := peer.withColdMetadataRouteMux(mux, peer.authenticate(mux))
	req := httptest.NewRequest(http.MethodGet, peerURL+"/api/issue-records/"+issueID, nil)
	req.Header.Set("X-Workspace-Key", workspace)
	for _, cookie := range member.Jar.Cookies(req.URL) {
		req.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != status || attempts != 2 {
		t.Fatalf("read during change returned %d after %d attempts, want %d after 2", response.Code, attempts, status)
	}
}
