package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP15PermissionDifferential(t *testing.T) {
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			ctx := t.Context()
			if err := repo.MutateWorkspace(ctx, workspace, "scale_p15.seed", "", nil, func(data *domain.Bootstrap) error {
				data.Teams = append(data.Teams, domain.Team{ID: "p15-public", Key: "P15O"}, domain.Team{ID: "p15-private", Key: "P15P", Private: true}, domain.Team{ID: "p15-child", Key: "P15C"})
				data.TeamSettings["p15-public"] = domain.TeamSettings{TeamID: "p15-public", Access: "public"}
				data.TeamSettings["p15-private"] = domain.TeamSettings{TeamID: "p15-private", Access: "private"}
				data.TeamSettings["p15-child"] = domain.TeamSettings{TeamID: "p15-child", ParentTeamID: "p15-private", Access: "public"}
				data.Projects = append(data.Projects,
					domain.Project{ID: "p15-open", SlugID: "current-p15-000000000015", PreviousSlugIDs: []string{"old-p15"}, Name: "Public", Description: "Body & <text>", TeamIDs: []string{"p15-public"}, DependencyIDs: []string{"p15-secret"}, IssueCount: 99, Lead: &data.Viewer, Comments: []domain.Comment{{ID: "p15-comment", Body: "Discussion", User: data.Viewer}}, DescriptionRevisions: []domain.ProjectDescriptionRevision{{ID: "p15-version", Description: "Old body", Author: data.Viewer}}},
					domain.Project{ID: "p15-secret", Name: "Private", TeamIDs: []string{"p15-private"}},
					domain.Project{ID: "p15-inherited", TeamIDs: []string{"p15-child"}})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := repo.SetTeamMembership(ctx, f.data.Workspace.ID, "p15-private", f.data.Viewer.ID, "member", true); err != nil {
				t.Fatal(err)
			}
			member, memberUser := scaleP03Join(t, f, "P15 member", idgen.New("p15-member-")+"@example.test", "member", "p15-public")
			admin, _ := scaleP03Join(t, f, "P15 outside admin", idgen.New("p15-admin-")+"@example.test", "admin", "p15-public")
			guest, guestUser := scaleP03Join(t, f, "P15 guest", idgen.New("p15-guest-")+"@example.test", "guest", "p15-public")
			inherited, _ := scaleP03Join(t, f, "P15 inherited", idgen.New("p15-inherited-")+"@example.test", "member", "p15-private")
			issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Private granted issue", "teamId": "p15-private", "projectId": "p15-secret"}, workspace, http.StatusCreated)
			authRequest[[]domain.IssuePermission](t, f.admin, http.MethodPut, f.host.URL+"/api/issues/"+issue.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": guestUser.ID, "role": "viewer"}}}, workspace, http.StatusOK)
			unrestricted := scaleP03Key(t, f, []string{"read"}, nil)
			selected := scaleP03Key(t, f, []string{"read"}, []string{"p15-public"})
			empty := idgen.New("p15-empty-")
			if err := repo.MutateWorkspace(ctx, workspace, "scale_p15.seed", "", nil, func(data *domain.Bootstrap) error {
				data.APIKeys = append(data.APIKeys, domain.APIKey{ID: "p15-empty", Name: "Selected empty", SecretHash: secretHash(empty), CreatorID: data.Viewer.ID, Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}})
				settings, preferences := data.UserSettings[f.data.Viewer.ID], data.NotificationPreferences[f.data.Viewer.ID]
				for _, user := range f.resources.users {
					// Missing defaults get a fresh UpdatedAt on each legacy read.
					// Persist a stable fixture rather than excluding response fields.
					settings.UserID, preferences.UserID = user.ID, user.ID
					data.UserSettings[user.ID], data.NotificationPreferences[user.ID] = settings, preferences
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			other, err := repo.CreateWorkspace(store.ContextWithActor(ctx, f.data.Viewer), "P15 foreign", idgen.New("p15-other-"), "us")
			if err != nil {
				t.Fatal(err)
			}
			f.resources.workspaces = append(f.resources.workspaces, other.Workspace.URLKey)
			principals := []struct {
				name, token string
				client      *http.Client
			}{
				{"admin-member", "", f.admin}, {"admin-private-outsider", "", admin}, {"member-private-outsider", "", member},
				{"guest-explicit-grant", "", guest}, {"subteam-inheritance", "", inherited},
				{"key-unrestricted", unrestricted, nil}, {"key-selected-set", selected, nil}, {"key-selected-empty", empty, nil},
			}
			compare := func(t *testing.T, client *http.Client, token, path, key string) {
				t.Helper()
				t.Setenv("FLOW_RECORD_HYDRATION", "off")
				old := scaleP03Read(t, f, client, token, http.MethodGet, path, key)
				t.Setenv("FLOW_RECORD_HYDRATION", "on")
				got := scaleP03Read(t, f, client, token, http.MethodGet, path, key)
				var left, right any
				if err := json.Unmarshal(old.Body.Bytes(), &left); err != nil {
					t.Fatalf("legacy JSON: %v", err)
				}
				if err := json.Unmarshal(got.Body.Bytes(), &right); err != nil {
					t.Fatalf("new JSON: %v", err)
				}
				if old.Code != got.Code || !reflect.DeepEqual(left, right) {
					changed := []string{}
					if fields, ok := left.(map[string]any); ok {
						current, _ := right.(map[string]any)
						for name, value := range fields {
							if !reflect.DeepEqual(value, current[name]) {
								changed = append(changed, name)
							}
						}
					}
					slices.Sort(changed)
					t.Fatalf("permission differential for %s: legacy status=%d new status=%d fields=%v", path, old.Code, got.Code, changed)
				}
			}
			for _, principal := range principals {
				t.Run(principal.name, func(t *testing.T) {
					if principal.token != "" {
						t.Setenv("FLOW_RECORD_HYDRATION", "off")
						_ = scaleP03Read(t, f, principal.client, principal.token, http.MethodGet, "/api/bootstrap", workspace)
						deadline := time.Now().Add(2 * time.Second)
						for {
							keys, _ := repo.WorkspaceMetadataFields(workspace, "apiKeys")
							settled := slices.ContainsFunc(keys.APIKeys, func(key domain.APIKey) bool {
								return key.SecretHash == secretHash(principal.token) && key.LastUsedAt != nil
							})
							if settled {
								break
							}
							if time.Now().After(deadline) {
								t.Fatal("credential telemetry did not settle")
							}
							time.Sleep(time.Millisecond)
						}
					}
					for _, path := range []string{"/api/bootstrap", "/api/issue-records/bootstrap", "/api/projects/old-p15", "/api/projects/p15-secret", "/api/projects/p15-inherited"} {
						compare(t, principal.client, principal.token, path, workspace)
					}
					compare(t, principal.client, principal.token, "/api/projects/p15-open", other.Workspace.URLKey)
				})
			}
			if err := repo.SetTeamMembership(ctx, f.data.Workspace.ID, "p15-private", memberUser.ID, "member", true); err != nil {
				t.Fatal(err)
			}
			compare(t, member, "", "/api/bootstrap", workspace)
			request := httptest.NewRequest(http.MethodGet, "/api/realtime/events?workspace="+workspace, nil)
			request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, memberUser))
			event := domain.RealtimeEvent{ID: "p15-queued", Type: "project.updated", AggregateID: "p15-secret", Payload: json.RawMessage(`{"hiddenReference":"p15-hidden-reference","actor":"p15-hidden-actor","count":999}`)}
			subscriber, stop := f.service.realtime.subscribeTransport(workspace, "", false)
			defer stop()
			f.service.realtime.publish(workspace, event)
			queued := <-subscriber.channel
			if err := repo.SetTeamMembership(ctx, f.data.Workspace.ID, "p15-private", memberUser.ID, "member", false); err != nil {
				t.Fatal(err)
			}
			compare(t, member, "", "/api/bootstrap", workspace)
			t.Setenv("FLOW_RECORD_HYDRATION", "off")
			projected, visible, err := f.service.pagedRealtimeEvent(request, queued)
			if err != nil {
				t.Fatal(err)
			}
			want := httptest.NewRecorder()
			if visible {
				writeSSE(want, projected, f.service.realtime.node)
			}
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			got := httptest.NewRecorder()
			if !f.service.writeRealtimeBatch(got, request, subscriber, []domain.RealtimeEvent{queued}, false, 0) || !bytes.Equal(got.Body.Bytes(), want.Body.Bytes()) || bytes.Contains(got.Body.Bytes(), []byte("p15-hidden")) {
				t.Fatal("queued event reused stale permissions or exposed hidden fields")
			}
			stop()
			f.service.realtime.publish(workspace, domain.RealtimeEvent{ID: "p15-replay-cursor"})
			f.service.realtime.publish(workspace, event)
			replay, stopReplay := f.service.realtime.subscribeTransport(workspace, "p15-replay-cursor", false)
			defer stopReplay()
			replayed := <-replay.channel
			got = httptest.NewRecorder()
			if !f.service.writeRealtimeBatch(got, request, replay, []domain.RealtimeEvent{replayed}, false, 0) || !bytes.Equal(got.Body.Bytes(), want.Body.Bytes()) {
				t.Fatal("replayed event reused pre-revocation permissions")
			}
		})
	}
}

func TestScaleP15ReadSnapshotReleasedBeforeSocket(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("TMPDIR", t.TempDir())
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		released := false
		w := &scaleP15CountingWriter{header: make(http.Header), onWrite: func() error {
			if !released {
				t.Fatal("SQL read snapshot outlived the producer")
			}
			_, err := repo.AccountForUser(context.Background(), f.data.Viewer.ID)
			return err
		}}
		serializeLegacyBootstrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, release, err := repo.BeginWorkspaceRead(r.Context(), f.data.Workspace.URLKey)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { release(); released = true }()
			writeBootstrapJSON(w, f.data)
		})).ServeHTTP(w, httptest.NewRequest("GET", "/api/bootstrap", nil))
	}
}
