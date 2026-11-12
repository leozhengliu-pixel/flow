package main

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP14RecipientPermissionDifferential(t *testing.T) {
	config := scaleP03DatabaseConfig(t)
	if config.Driver == "sqlite" {
		config.MaxOpenConns = 1
	}
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	for driver, repo := range map[string]*store.SQLiteStore{config.Driver: repo} {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			private, child := idgen.New("p14-private-"), idgen.New("p14-child-")
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p14.seeded", "", nil, func(data *domain.Bootstrap) error {
				data.Teams = append(data.Teams, domain.Team{ID: private, Key: "P14", Private: true}, domain.Team{ID: child, Key: "P14C"})
				data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
				data.TeamSettings[child] = domain.TeamSettings{TeamID: child, Access: "public", ParentTeamID: private}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, private, f.data.Viewer.ID, "member", true); err != nil {
				t.Fatal(err)
			}
			type principal struct {
				name   string
				client *http.Client
				user   domain.User
				token  string
			}
			principals := []principal{{name: "admin", client: f.admin, user: f.data.Viewer}}
			for _, item := range []struct {
				name, role string
				teams      []string
			}{
				{"admin-private-outsider", "admin", nil}, {"member-private-outsider", "member", nil},
				{"guest-explicit-grant", "guest", []string{private}}, {"sub-team-inheritance", "member", []string{child}},
			} {
				client, user := scaleP03Join(t, f, item.name, idgen.New("p14-")+"@example.test", item.role, item.teams...)
				principals = append(principals, principal{name: item.name, client: client, user: user})
			}
			unrestricted := scaleP03Key(t, f, []string{"read"}, nil)
			selected := scaleP03Key(t, f, []string{"read"}, []string{private})
			empty := idgen.New("p14-empty-")
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p14.seeded", "", nil, func(data *domain.Bootstrap) error {
				data.APIKeys = append(data.APIKeys, domain.APIKey{ID: idgen.New("p14-key-"), SecretHash: secretHash(empty), CreatorID: f.data.Viewer.ID, Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}})
				now := time.Now().UTC()
				for _, p := range principals {
					data.Notifications = append(data.Notifications, domain.Notification{ID: idgen.New("p14-notification-"), RecipientID: p.user.ID, Actor: domain.User{ID: "hidden-actor", Name: "Hidden actor"}, SourceID: "old-private-slug", IssueID: "hidden-reference", CreatedAt: now, UpdatedAt: now})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, item := range []struct{ name, token string }{{"key-unrestricted", unrestricted}, {"key-selected-empty", empty}, {"key-selected-set", selected}} {
				principals = append(principals, principal{name: item.name, token: item.token, user: f.data.Viewer})
			}
			for _, p := range principals {
				t.Run(p.name, func(t *testing.T) {
					legacy := scaleP03Read(t, f, p.client, p.token, http.MethodGet, "/api/notifications?limit=1", workspace)
					next := scaleP14StateRequest(t, f, p.client, p.token, workspace)
					if next.Code != legacy.Code {
						t.Fatalf("status changed: legacy=%d state=%d body=%s", legacy.Code, next.Code, next.Body)
					}
					if next.Code != http.StatusOK {
						t.Fatalf("fixture principal denied: %s", next.Body)
					}
					var page store.NotificationPage
					var state store.NotificationState
					if err := json.Unmarshal(legacy.Body.Bytes(), &page); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(next.Body.Bytes(), &state); err != nil {
						t.Fatal(err)
					}
					want := 1
					if p.name == "key-selected-empty" || p.name == "key-selected-set" {
						want = 0
					}
					if state.UnreadCount != page.UnreadCount || state.UnreadCount != want {
						t.Fatalf("legacy=%d state=%+v", page.UnreadCount, state)
					}
					if bytes.Contains(next.Body.Bytes(), []byte("hidden")) || bytes.Contains(next.Body.Bytes(), []byte("old-private-slug")) || len(state.ChangedIDs) > 0 {
						t.Fatalf("overlay leaked content: %s", next.Body)
					}
					if next.Header().Get("Cache-Control") != "no-store" {
						t.Fatal("personal count may be cached")
					}
				})
			}
			other, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "P14 other", idgen.New("p14-other-"), "us")
			if err != nil {
				t.Fatal(err)
			}
			f.resources.workspaces = append(f.resources.workspaces, other.Workspace.URLKey)
			outside := scaleP14StateRequest(t, f, principals[1].client, "", other.Workspace.URLKey)
			if outside.Code != http.StatusForbidden {
				t.Fatalf("cross-workspace read=%d", outside.Code)
			}
			for _, phase := range []string{"queued", "replayed"} {
				t.Run("revocation-"+phase, func(t *testing.T) {
					access, err := repo.ResolveAccess(t.Context(), workspace, principals[2].user.ID, nil)
					if err != nil {
						t.Fatal(err)
					}
					if err := repo.RemoveMember(t.Context(), f.data.Workspace.ID, principals[2].user.ID); err != nil {
						t.Fatal(err)
					}
					_, visible, err := f.service.projectNotificationState(t.Context(), access, f.data.Workspace.ID, principals[2].user.ID, store.NotificationState{Revision: "1", UnreadCount: 1})
					if visible || err == nil {
						t.Fatalf("revoked envelope visible=%v error=%v", visible, err)
					}
					if err := repo.EnsureWorkspaceMembership(t.Context(), f.data.Workspace.ID, principals[2].user.ID); err != nil {
						t.Fatal(err)
					}
				})
			}
			t.Run("team-grant-revocation", func(t *testing.T) {
				user := principals[3].user
				access, err := repo.ResolveAccess(t.Context(), workspace, user.ID, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, private, user.ID, "member", false); err != nil {
					t.Fatal(err)
				}
				_, visible, err := f.service.projectNotificationState(t.Context(), access, f.data.Workspace.ID, user.ID, store.NotificationState{Revision: "1", UnreadCount: 1})
				if visible || err == nil {
					t.Fatalf("old grant projection survived revocation: %v %v", visible, err)
				}
			})
			t.Run("credential-revocation", func(t *testing.T) {
				r := httptest.NewRequest(http.MethodGet, "/api/sync/notification-state", nil)
				r.Header.Set("Authorization", "Bearer "+selected)
				r.Header.Set("X-Workspace-Key", workspace)
				user, key, _ := f.service.authenticateAPIKey(r)
				if key == nil {
					t.Fatal("fixture credential not authenticated")
				}
				access, err := repo.ResolveAccess(t.Context(), workspace, user.ID, key)
				if err != nil {
					t.Fatal(err)
				}
				authRequest[any](t, f.admin, http.MethodDelete, f.host.URL+"/api/api-keys/"+key.ID, nil, workspace, http.StatusNoContent)
				_, visible, err := f.service.projectNotificationState(t.Context(), access, f.data.Workspace.ID, user.ID, store.NotificationState{Revision: "1", UnreadCount: 1})
				if visible || err == nil {
					t.Fatalf("old credential projection survived revocation: %v %v", visible, err)
				}
				if got := scaleP14StateRequest(t, f, nil, selected, workspace).Code; got != http.StatusUnauthorized {
					t.Fatalf("revoked credential GET = %d", got)
				}
			})
			access, err := repo.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			queries := 0
			ctx := store.WithSQLStatementObserver(t.Context(), func(string) { queries++ })
			for _, recipient := range []string{"", "someone-else"} {
				_, visible, err := f.service.projectNotificationState(ctx, access, f.data.Workspace.ID, recipient, store.NotificationState{Revision: "1", UnreadCount: 99})
				if visible || err != nil {
					t.Fatalf("other recipient accepted: %v %v", visible, err)
				}
			}
			_, visible, err := f.service.projectNotificationState(ctx, access, other.Workspace.ID, access.PrincipalID, store.NotificationState{Revision: "1", UnreadCount: 99})
			if visible || err != nil || queries != 0 {
				t.Fatalf("nonrecipient/cross-workspace queried SQL: %d %v %v", queries, visible, err)
			}
			state := store.NotificationState{Revision: "1", UnreadCount: 1, ChangedIDs: []string{"own-notification"}}
			if projected, visible, err := f.service.projectNotificationState(t.Context(), access, f.data.Workspace.ID, access.PrincipalID, state); err != nil || !visible || !reflect.DeepEqual(projected, state) {
				t.Fatalf("recipient projection: %+v %v %v", projected, visible, err)
			}
		})
	}
}

func scaleP14StateRequest(t *testing.T, f scaleP03PlatformFixture, client *http.Client, bearer, workspace string) *httptest.ResponseRecorder {
	return scaleP14StateRequestContext(t, t.Context(), f, client, bearer, workspace)
}

func scaleP14StateRequestContext(t *testing.T, ctx context.Context, f scaleP03PlatformFixture, client *http.Client, bearer, workspace string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/sync/notification-state?recipientId=someone-else", nil).WithContext(ctx)
	r.Header.Set("X-Workspace-Key", workspace)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	if client != nil {
		origin, _ := url.Parse(f.host.URL)
		for _, cookie := range client.Jar.Cookies(origin) {
			r.AddCookie(cookie)
		}
	}
	w := httptest.NewRecorder()
	f.service.authenticate(http.HandlerFunc(f.service.notificationState)).ServeHTTP(w, r)
	return w
}

// Every producer of notification records and every in-place lifecycle edit is
// listed, including the indirect mention, Agent reply and bulk producers.
// New producers fail this inventory until their writer path is reviewed.
func TestScaleP14BusinessWriterInventory(t *testing.T) {
	expected := []string{
		"disableFailingWebhook", "applyCodeReviewEvent", "slackWebhook", "notifyUpdateAuthor", "trashIssue", "restoreIssueLinks",
		"appendProjectUpdateReminders", "archiveProjectUpdateReminders", "notifyApprovers", "appendDocumentNotifications", "appendAgentReplyNotifications",
		"createDocumentReminder", "applyTriageResponsibility", "applyWorkflowActions", "appendWelcomeNotification", "appendOnboardingAnnouncement",
		"appendIssueNotifications", "aggregatableNotification", "preparePulseSummaries", "createInitiativeReminder", "createProjectUpdate", "createIssueReminder", "createProjectReminder",
	}
	var found []string
	readProjections := map[string]string{
		"captureIssueLinks":          "trash_links.go",
		"filterPreferenceIssueTeams": "preference_records.go",
		"redactAPIKeyReferences":     "api_key_projection.go",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		set := token.NewFileSet()
		parsed, err := parser.ParseFile(set, file, raw, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// These functions filter returned copies; they do not persist or
			// produce notifications. Keep exemptions tied to exact files.
			if readProjections[fn.Name.Name] == file {
				continue
			}
			body := string(raw[set.Position(fn.Body.Pos()).Offset:set.Position(fn.Body.End()).Offset])
			if strings.Contains(body, "data.Notifications =") || strings.Contains(body, "&data.Notifications[") {
				found = append(found, fn.Name.Name)
			}
		}
	}
	slices.Sort(expected)
	slices.Sort(found)
	if !reflect.DeepEqual(expected, found) {
		t.Fatalf("notification producer inventory changed:\nwant %v\ngot  %v", expected, found)
	}
}

func TestScaleP14NotificationStateWire(t *testing.T) {
	state := store.NotificationState{Revision: "9007199254740993", UnreadCount: 3}
	event := notificationStateRealtimeEvent("event", "recipient", state)
	if event.Type != store.NotificationStateEvent || event.AggregateID != "recipient" || event.ActorID != "" {
		t.Fatalf("bad envelope: %+v", event)
	}
	if string(event.Payload) != `{"revision":"9007199254740993","unreadCount":3,"invalidated":false}` {
		t.Fatalf("wire contract: %s", event.Payload)
	}
}
