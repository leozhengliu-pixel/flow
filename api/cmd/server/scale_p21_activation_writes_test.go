package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"

	"github.com/alicebob/miniredis/v2"
)

type activationHTTPPeer struct {
	*httptest.Server
	service *server
}

func scaleP21ActivationHTTPPeers(t *testing.T, mode string) ([2]activationHTTPPeer, scaleP03PlatformFixture, domain.Issue, string) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_PEER_SYNC", "off")
	t.Setenv("FLOW_METADATA_CATALOG", "off")
	recordMode := "on"
	if mode == "off" {
		recordMode = "off"
	}
	t.Setenv("FLOW_RECORD_HYDRATION", recordMode)
	t.Setenv("FLOW_RECORD_JOURNAL", recordMode)
	t.Setenv("FLOW_SYNC_V2", recordMode)

	config := scaleP03DatabaseConfig(t)
	open := func() *store.SQLiteStore {
		t.Helper()
		repo, err := store.OpenDatabase(config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Close() })
		return repo
	}
	seed := open()
	f := scaleP03Fixture(t, seed)
	workspace, team := f.data.Workspace.URLKey, f.data.Teams[0].ID
	project := idgen.New("activation-project-")
	if err := seed.MutateWorkspace(t.Context(), workspace, "test.activation_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: project, Name: "Activation project", TeamIDs: []string{team}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{
		"title": "Activation issue", "teamId": team, "projectId": project, "priority": 3,
	}, workspace, http.StatusCreated)
	f.host.Close()
	if mode != "off" {
		report, err := seed.PrepareBoundedMetadata(t.Context())
		if err != nil || !report.Ready {
			t.Fatalf("prepare bounded metadata: ready=%t, error=%v", report.Ready, err)
		}
		t.Setenv("FLOW_BOUNDED_METADATA", "on")
	}
	if mode == "full" {
		t.Setenv("FLOW_RECORD_PEER_SYNC", "on")
	}

	redis := miniredis.RunT(t)
	var peers [2]activationHTTPPeer
	for index := range peers {
		repo := open()
		coordinator, err := coordination.Open(t.Context(), coordination.Config{
			Mode: "standalone", Addrs: []string{redis.Addr()}, Prefix: "activation-writes",
			ConnectTimeout: time.Second, LockTTL: 5 * time.Second, LockWait: 5 * time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = coordinator.Close() })
		repo.SetWorkspaceCoordinator(coordinator)
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		service := &server{store: repo, coordinator: coordinator, coordinationContext: ctx, uploadPath: t.TempDir()}
		peers[index] = activationHTTPPeer{httptest.NewServer(newHandler(service)), service}
		t.Cleanup(peers[index].Close)
		if err := service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	return peers, f, issue, project
}

func activationRequest[T any](ctx context.Context, client *http.Client, method, url, workspace string, input any) (status int, result T, err error) {
	body, err := json.Marshal(input)
	if err != nil {
		return 0, result, err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return 0, result, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workspace-Key", workspace)
	res, err := client.Do(req)
	if err != nil {
		return 0, result, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		err = json.NewDecoder(res.Body).Decode(&result)
	}
	return res.StatusCode, result, err
}

func TestScaleP21ActivationIssueWritesDifferential(t *testing.T) {
	for _, mode := range []string{"off", "current", "full"} {
		t.Run(mode, func(t *testing.T) {
			peers, f, issue, _ := scaleP21ActivationHTTPPeers(t, mode)
			workspace := f.data.Workspace.URLKey
			route := "/api/issue-records/" + issue.ID
			var comments [2]domain.Comment
			var commentStatus [2]int
			var commentErr [2]error
			var updated domain.Issue
			var updateStatus int
			var updateErr error
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			start := make(chan struct{})
			var writers sync.WaitGroup
			for index := range peers {
				writers.Go(func() {
					<-start
					base := peers[index].URL + route
					commentStatus[index], comments[index], commentErr[index] = activationRequest[domain.Comment](ctx,
						f.admin, http.MethodPost, base+"/comments", workspace, map[string]string{"body": "Peer comment"})
					if index == 0 {
						updateStatus, updated, updateErr = activationRequest[domain.Issue](ctx,
							f.admin, http.MethodPatch, base, workspace, map[string]int{"priority": 2})
					}
				})
			}
			close(start)
			writers.Wait()
			for index := range comments {
				if commentStatus[index] != http.StatusCreated || commentErr[index] != nil {
					t.Fatalf("replica %d comment failed: status=%d error=%t", index, commentStatus[index], commentErr[index] != nil)
				}
			}
			if updateStatus != http.StatusOK || updateErr != nil {
				t.Fatalf("issue update failed: status=%d error=%t", updateStatus, updateErr != nil)
			}
			if updated.Priority != 2 || updated.Version <= issue.Version || comments[0].ID == "" || comments[1].ID == "" || comments[0].ID == comments[1].ID {
				t.Fatal("cross-replica writes did not return the committed issue and two distinct comments")
			}
			for _, peer := range peers {
				reloaded := authRequest[domain.Issue](t, f.admin, http.MethodGet, peer.URL+route, nil, workspace, http.StatusOK)
				history := authRequest[store.IssueHistory](t, f.admin, http.MethodGet, peer.URL+route+"/history", nil, workspace, http.StatusOK)
				if reloaded.Priority != updated.Priority || reloaded.Version != updated.Version || len(history.Comments) != len(comments) {
					t.Fatal("peer did not return the final issue and both comments")
				}
			}
		})
	}
}

func TestScaleP21ActivationIssueWriteBudget(t *testing.T) {
	for _, mode := range []string{"off", "current", "full"} {
		t.Run(mode, func(t *testing.T) {
			peers, f, issue, project := scaleP21ActivationHTTPPeers(t, mode)
			service := peers[0].service
			repo, workspace := service.store, f.data.Workspace.URLKey
			actor := store.ContextWithActor(t.Context(), f.data.Viewer)
			writeCreate := func() *httptest.ResponseRecorder {
				input := domain.IssueCreateInput{Title: "Budget test issue", TeamID: issue.Team.ID, ProjectID: &project}
				body, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(http.MethodPost, "/api/issue-records", bytes.NewReader(body))
				request.Header.Set("X-Workspace-Key", workspace)
				request.Header.Set("Content-Type", "application/json")
				ctx, closeOutput := beginMetadataOutputReads(store.WithIssueRecordMutations(actor))
				defer closeOutput()
				response := httptest.NewRecorder()
				service.createIssue(response, request.WithContext(ctx))
				return response
			}
			writeComment := func() *httptest.ResponseRecorder {
				request := httptest.NewRequest(http.MethodPost, "/api/issue-records/"+issue.ID+"/comments", strings.NewReader(`{"body":"Budget test comment"}`))
				request.Header.Set("X-Workspace-Key", workspace)
				request.SetPathValue("id", issue.ID)
				ctx, closeOutput := beginMetadataOutputReads(store.WithIssueDiscussionMutation(actor, issue.ID, ""))
				defer closeOutput()
				response := httptest.NewRecorder()
				service.createComment(response, request.WithContext(ctx))
				return response
			}
			writePriority := func() *httptest.ResponseRecorder {
				newPriority := 1
				scope := store.IssueMutationScope{Payload: domain.IssueUpdateInput{Priority: &newPriority}}
				result, err := repo.UpdateIssueRecord(actor, workspace, issue.ID, nil, scope, func(_ *domain.Bootstrap, issue *domain.Issue) error {
					issue.Priority = newPriority
					return nil
				})
				response := httptest.NewRecorder()
				respondMutation(response, err, http.StatusOK, result)
				return response
			}
			events := func() int64 {
				t.Helper()
				head, err := repo.WorkspaceChangeHead(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				return head
			}
			hold := func() func() {
				t.Helper()
				room := repo.MetadataWorkLimitForTesting() - repo.MetadataWorkUsedForTesting()
				release, err := repo.HoldMetadataWorkForTesting(room - 32<<10)
				if err != nil {
					t.Fatal("could not hold the issue writer's metadata budget")
				}
				t.Cleanup(release)
				return release
			}
			for _, write := range []struct {
				name string
				run  func() *httptest.ResponseRecorder
				want int
			}{
				{"create", writeCreate, http.StatusCreated},
				{"comment", writeComment, http.StatusCreated},
				{"priority", writePriority, http.StatusOK},
			} {
				t.Run(write.name, func(t *testing.T) {
					before := events()
					if mode != "off" {
						restore := store.SetMetadataWorkRetryWaitForTesting(50 * time.Millisecond)
						release := hold()
						response := write.run()
						release()
						restore()
						switch {
						case response.Code == http.StatusServiceUnavailable && response.Header().Get("Retry-After") != "" && events() == before:
							// Refused before any write.
						case response.Code == write.want && events() == before+1:
							// A point write (MySQL and PostgreSQL issue edits) needs no
							// metadata work, so the held budget does not refuse it.
							before++
						default:
							t.Fatalf("held budget returned %d, want 503 with Retry-After and no write, or a complete point write", response.Code)
						}
						release = hold()
						timer := time.AfterFunc(150*time.Millisecond, release)
						defer timer.Stop()
						defer release()
					}
					if response := write.run(); response.Code != write.want || events() != before+1 {
						t.Fatalf("write returned %d, want %d and one event", response.Code, write.want)
					}
				})
			}
		})
	}
}
