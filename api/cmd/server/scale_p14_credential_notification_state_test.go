package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP14CredentialScopedNotificationState(t *testing.T) {
	config := scaleP03DatabaseConfig(t)
	if config.Driver == "sqlite" {
		config.MaxOpenConns = 1
	}
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := apiKeyScopeFixture(t, repo)
	workspace := f.data.Workspace.URLKey
	for _, actor := range []string{"user", "app"} {
		for _, spec := range []struct {
			name, restriction string
			teams             []string
			want              int
		}{
			{"selected-set", "selected", []string{"scope-public"}, 2},
			{"selected-empty", "selected", []string{}, 0},
			{"legacy-selected", "", []string{"scope-public"}, 2},
			{"selected-private", "selected", []string{"scope-private"}, 1},
			{"unrestricted", "all", nil, 3},
		} {
			t.Run(actor+"/"+spec.name, func(t *testing.T) {
				secret, key := apiKeyScopeCredential(t, f, actor, spec.restriction, spec.teams, []string{"read", "initiative:read"})
				principal := key.SubjectID()
				ids := []string{"scope-notification", "scope-hidden-notification", "scope-notification-child"}
				if actor == "app" {
					for i := range ids {
						ids[i] += "-" + principal
					}
					if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p14.app_notifications", "", nil, func(data *domain.Bootstrap) error {
						now := time.Now().UTC()
						for i, issue := range []string{"scope-issue", "scope-hidden-issue", "scope-child"} {
							data.Notifications = append(data.Notifications, domain.Notification{ID: ids[i], RecipientID: principal, IssueID: issue, SourceType: "issue", SourceID: issue, CreatedAt: now, UpdatedAt: now})
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				legacy := scaleP03Read(t, f, nil, secret, http.MethodGet, "/api/notifications?limit=1", workspace)
				response := scaleP14StateRequest(t, f, nil, secret, workspace)
				var page store.NotificationPage
				var state store.NotificationState
				if legacy.Code != http.StatusOK || response.Code != http.StatusOK || json.Unmarshal(legacy.Body.Bytes(), &page) != nil || json.Unmarshal(response.Body.Bytes(), &state) != nil {
					t.Fatalf("notification reads: list=%d state=%d", legacy.Code, response.Code)
				}
				if state.UnreadCount != spec.want || state.UnreadCount != page.UnreadCount {
					t.Fatalf("scoped count: state=%d list=%d want=%d", state.UnreadCount, page.UnreadCount, spec.want)
				}
				// Authenticate queues a metadata telemetry write. Let it commit
				// before constructing the separate queued-event access fixture.
				for deadline := time.Now().Add(3 * time.Second); ; {
					current, err := repo.AuthenticateAPIKeyRecord(t.Context(), workspace, secretHash(secret), nil)
					if err != nil {
						t.Fatal(err)
					}
					if current.Key.LastUsedAt != nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("credential telemetry did not commit")
					}
					time.Sleep(5 * time.Millisecond)
				}
				access, err := repo.ResolveAccess(t.Context(), workspace, principal, &key)
				if err != nil {
					t.Fatal(err)
				}
				source, err := repo.NotificationState(t.Context(), workspace, principal)
				if err != nil || source.UnreadCount != 3 {
					t.Fatalf("recipient source count=%d error=%v", source.UnreadCount, err)
				}
				source.ChangedIDs = ids
				var queries []string
				ctx := store.WithSQLStatementObserver(t.Context(), func(query string) { queries = append(queries, query) })
				projected, visible, err := f.service.projectNotificationState(ctx, access, f.data.Workspace.ID, principal, source)
				if err != nil || !visible || projected.UnreadCount != spec.want || projected.Invalidated {
					t.Fatalf("scoped push=%+v visible=%v error=%v", projected, visible, err)
				}
				var wantIDs []string
				for i, team := range []string{"scope-public", "scope-private", "scope-public"} {
					if spec.restriction == "all" || slices.Contains(spec.teams, team) {
						wantIDs = append(wantIDs, ids[i])
					}
				}
				if !reflect.DeepEqual(projected.ChangedIDs, wantIDs) {
					t.Fatalf("push changed IDs=%v want=%v", projected.ChangedIDs, wantIDs)
				}
				for _, query := range queries {
					if strings.Contains(query, "COUNT(*) FROM workspace_content_records") {
						t.Fatal("credential projection read the recipient-wide unread count")
					}
				}
				if spec.restriction != "all" {
					source.ChangedIDs = []string{"removed-notification"}
					projected, visible, err = f.service.projectNotificationState(t.Context(), access, f.data.Workspace.ID, principal, source)
					if err != nil || !visible || !projected.Invalidated || len(projected.ChangedIDs) != 0 || projected.UnreadCount != spec.want {
						t.Fatalf("removed notification push=%+v visible=%v error=%v", projected, visible, err)
					}
				}
			})
		}
	}
}

func TestScaleP14CountReauthenticatesAfterMetadataDrift(t *testing.T) {
	config := scaleP03DatabaseConfig(t)
	if config.Driver == "sqlite" {
		config.MaxOpenConns = 1
	}
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP03Fixture(t, repo)
	workspace, team := f.data.Workspace.URLKey, f.data.Teams[0].ID
	for _, mode := range []string{"telemetry", "narrowed", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			secret, key := apiKeyScopeCredential(t, f, "user", "selected", []string{team}, []string{"read"})
			pageRead, changed, authentications := false, false, 0
			ctx := store.WithSQLStatementObserver(t.Context(), func(query string) {
				if strings.Contains(query, "FROM api_key_lookup locator") {
					authentications++
				}
				if strings.HasPrefix(query, "SELECT data,created_at,id FROM workspace_content_records") {
					pageRead = true
				}
				if changed || !pageRead || !strings.HasPrefix(query, "SELECT workspace_id,(SELECT COALESCE(SUM(revision)") {
					return
				}
				changed = true
				write, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				if err := repo.MutateWorkspace(store.ContextWithActor(write, f.data.Viewer), workspace, "scale_p14.credential_changed", key.ID, nil, func(data *domain.Bootstrap) error {
					for i := range data.APIKeys {
						if data.APIKeys[i].ID != key.ID {
							continue
						}
						now := time.Now().UTC()
						data.APIKeys[i].LastUsedAt = &now
						if mode == "narrowed" {
							data.APIKeys[i].TeamIDs = []string{}
						}
						if mode == "revoked" {
							data.APIKeys[i].RevokedAt = &now
						}
					}
					return nil
				}); err != nil {
					t.Errorf("change after read snapshot: %v", err)
				}
			})
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p14.notice", "", nil, func(data *domain.Bootstrap) error {
				now := time.Now().UTC()
				data.Notifications = append(data.Notifications, domain.Notification{ID: idgen.New("p14-race-"), RecipientID: f.data.Viewer.ID, SourceType: "team", SourceID: team, CreatedAt: now, UpdatedAt: now})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			response := scaleP14StateRequestContext(t, ctx, f, nil, secret, workspace)
			if !changed || authentications < 2 {
				t.Fatalf("read did not reauthenticate after epoch drift: changed=%v auth=%d", changed, authentications)
			}
			if mode == "revoked" {
				if response.Code != http.StatusUnauthorized {
					t.Fatalf("revoked credential returned %d", response.Code)
				}
				return
			}
			var state store.NotificationState
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &state) != nil {
				t.Fatalf("credential state after %s: status=%d body=%s", mode, response.Code, response.Body)
			}
			if mode == "narrowed" && state.UnreadCount != 0 || mode == "telemetry" && state.UnreadCount == 0 {
				t.Fatalf("count ignored current credential scope: mode=%s count=%d", mode, state.UnreadCount)
			}
		})
	}
}
