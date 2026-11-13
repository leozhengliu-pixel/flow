package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP11PermissionDifferentialAndQueuedRevocation(t *testing.T) {
	for _, mode := range []string{"shadow", "on", "off"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_PEER_SYNC", mode)
			t.Setenv("FLOW_RECORD_JOURNAL", "on")
			peers, config := scaleP11HTTPPeers(t)
			for i := range 2 {
				peers.stopListening(i)
			}
			f := scaleP03CreateFixture(t, peers.stores[0], peers.servers[0], peers.first)
			workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
			suffix := idgen.New("p11-")
			public, private, child := "p11-public", "p11-private", "p11-child"
			ctx := store.ContextWithActor(t.Context(), f.data.Viewer)
			if err := f.repository.MutateWorkspace(ctx, workspace, "test.p11_matrix", "", nil, func(data *domain.Bootstrap) error {
				data.Teams = append(data.Teams, domain.Team{ID: public, Key: "P11A"}, domain.Team{ID: private, Key: "P11B", Private: true}, domain.Team{ID: child, Key: "P11C"})
				data.TeamSettings[public] = domain.TeamSettings{TeamID: public, Access: "public"}
				data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
				data.TeamSettings[child] = domain.TeamSettings{TeamID: child, ParentTeamID: private, Access: "public"}
				data.Projects = append(data.Projects,
					domain.Project{ID: "p11-open", Name: "Open", TeamIDs: []string{public}, SlugID: "current-000000000011", PreviousSlugIDs: []string{"old-p11"}, DependencyIDs: []string{"p11-secret"}, IssueCount: 42, Comments: []domain.Comment{{ID: "p11-comment", Body: "visible discussion", User: data.Viewer}}},
					domain.Project{ID: "p11-secret", Name: "Secret", TeamIDs: []string{private}, Description: "private body"},
					domain.Project{ID: "p11-inherited", Name: "Inherited", TeamIDs: []string{child}},
				)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.repository.SetTeamMembership(ctx, workspaceID, private, f.data.Viewer.ID, "member", true); err != nil {
				t.Fatal(err)
			}
			outsideAdmin, _ := scaleP03Join(t, f, "P11 outside admin", suffix+"-admin@example.test", "admin", public)
			member, memberUser := scaleP03Join(t, f, "P11 member", suffix+"-member@example.test", "member", public, private)
			guest, guestUser := scaleP03Join(t, f, "P11 guest", suffix+"-guest@example.test", "guest", public)
			outsider, _ := scaleP03Join(t, f, "P11 outsider", suffix+"-outsider@example.test", "member", public)
			// Persist personal defaults once. Their existing lazy defaults
			// contain time.Now(), which differs between two legacy reloads.
			for _, client := range []*http.Client{f.admin, outsideAdmin, member, guest, outsider} {
				authRequest[domain.UserSettings](t, client, "PATCH", f.host.URL+"/api/account/settings", map[string]string{"timezone": "Etc/UTC"}, workspace, http.StatusOK)
				authRequest[domain.NotificationPreferences](t, client, "PATCH", f.host.URL+"/api/notification-preferences", map[string]string{"emailFormat": "immediate"}, workspace, http.StatusOK)
			}
			issue := authRequest[domain.Issue](t, f.admin, "POST", f.host.URL+"/api/issue-records", map[string]any{"title": "P11 grant", "teamId": private, "projectId": "p11-secret"}, workspace, http.StatusCreated)
			authRequest[[]domain.IssuePermission](t, f.admin, "PUT", f.host.URL+"/api/issues/"+issue.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": guestUser.ID, "role": "viewer"}}}, workspace, http.StatusOK)
			unrestricted := scaleP03Key(t, f, []string{"read"}, nil)
			selected := scaleP03Key(t, f, []string{"read"}, []string{public})
			empty := idgen.New("p11-selected-empty-")
			if err := f.repository.MutateWorkspace(ctx, workspace, "test.p11_matrix", "", nil, func(data *domain.Bootstrap) error {
				data.APIKeys = append(data.APIKeys, domain.APIKey{ID: "p11-empty", Name: "P11 empty", SecretHash: secretHash(empty), CreatorID: data.Viewer.ID, Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			other, err := f.repository.CreateWorkspace(ctx, "P11 other", idgen.New("p11-other-"), "us")
			if err != nil {
				t.Fatal(err)
			}
			f.resources.workspaces = append(f.resources.workspaces, other.Workspace.URLKey)
			if err := peers.stores[1].ReloadWorkspace(ctx, workspace); err != nil {
				t.Fatal(err)
			}
			if mode != "off" {
				if err := peers.servers[1].syncPeerWorkspace(ctx, workspace); err != nil {
					t.Fatal(err)
				}
			}
			// No notification or Redis counter bump follows the remaining writes.
			f.repository.SetRealtimeSink(nil)
			if err := f.repository.MutateWorkspace(ctx, workspace, "project.updated", "p11-open", map[string]string{"name": "P11 durable rename"}, func(data *domain.Bootstrap) error {
				for i := range data.Projects {
					if data.Projects[i].ID == "p11-open" {
						data.Projects[i].Name = "P11 durable rename"
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if mode == "off" {
				if err := peers.stores[1].ReloadWorkspace(ctx, workspace); err != nil {
					t.Fatal(err)
				}
			}
			remote := f
			remote.host, remote.service, remote.repository = peers.second, peers.servers[1], peers.stores[1]
			// Loading/normalizing the compatibility snapshot is part of the
			// existing peer contract. Use a fresh legacy reader as the oracle.
			oracleRepo, err := store.OpenDatabase(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = oracleRepo.Close() })
			oracleService := &server{store: oracleRepo, uploadPath: t.TempDir()}
			oracleHost := httptest.NewServer(newHandler(oracleService))
			t.Cleanup(oracleHost.Close)
			if err := oracleService.stopDeliveryScheduler(t.Context()); err != nil {
				t.Fatal(err)
			}
			oracle := f
			oracle.host, oracle.service, oracle.repository = oracleHost, oracleService, oracleRepo
			// Settle the asynchronous per-replica credential telemetry before
			// comparing complete bootstrap bodies, including lastUsedAt.
			for _, repo := range []*store.SQLiteStore{oracleRepo, remote.repository} {
				for _, secret := range []string{unrestricted, selected, empty} {
					scaleP11WarmCredential(t, repo, workspace, secret)
				}
				if err := repo.ReloadWorkspace(ctx, workspace); err != nil {
					t.Fatal(err)
				}
			}
			principals := []struct {
				name, token string
				client      *http.Client
			}{
				{"admin", "", f.admin}, {"admin-outside-private", "", outsideAdmin},
				{"member-subteam-inheritance", "", member}, {"guest-explicit-grant", "", guest}, {"private-outsider", "", outsider},
				{"key-unrestricted", unrestricted, nil}, {"key-selected-empty", empty, nil}, {"key-selected-set", selected, nil},
			}
			paths := []string{"/api/bootstrap", "/api/projects/p11-open", "/api/projects/old-p11", "/api/projects/stale-000000000011", "/api/projects/p11-secret", "/api/projects/p11-inherited", "/api/issue-records/" + issue.ID}
			for _, principal := range principals {
				t.Run(principal.name, func(t *testing.T) {
					for _, path := range paths {
						want := scaleP03Read(t, oracle, principal.client, principal.token, "GET", path, workspace)
						got := scaleP03Read(t, remote, principal.client, principal.token, "GET", path, workspace)
						if want.Code != got.Code {
							t.Fatalf("%s status=%d, oracle=%d", path, got.Code, want.Code)
						}
						scaleP01EqualJSON(t, json.RawMessage(want.Body.Bytes()), json.RawMessage(got.Body.Bytes()))
					}
					want := scaleP03Read(t, oracle, principal.client, principal.token, "GET", "/api/projects/p11-open", other.Workspace.URLKey)
					got := scaleP03Read(t, remote, principal.client, principal.token, "GET", "/api/projects/p11-open", other.Workspace.URLKey)
					if want.Code != got.Code || got.Code == http.StatusOK {
						t.Fatal("cross-workspace access differs")
					}
				})
			}
			// The queued projection contains private data and an actor before
			// revocation. Neither may survive rechecking/replay on the peer.
			request := httptest.NewRequest("GET", "/api/realtime/events", nil)
			request.Header.Set("X-Workspace-Key", workspace)
			request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, memberUser))
			queued := domain.RealtimeEvent{ID: "p11-queued", Type: "project.updated", AggregateID: "p11-inherited", ActorID: f.data.Viewer.ID}
			projected, visible, err := remote.service.pagedRealtimeEvent(request, queued)
			if err != nil || !visible {
				t.Fatal("inherited record was not visible before revocation")
			}
			head, err := f.repository.WorkspaceChangeHead(ctx, workspace)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.repository.MutateWorkspace(ctx, workspace, "project.updated", "p11-inherited", map[string]string{"name": "queued"}, func(data *domain.Bootstrap) error { return nil }); err != nil {
				t.Fatal(err)
			}
			if err := f.repository.SetTeamMembership(ctx, workspaceID, private, memberUser.ID, "member", false); err != nil {
				t.Fatal(err)
			}
			if mode == "off" {
				if err := remote.repository.ReloadWorkspace(ctx, workspace); err != nil {
					t.Fatal(err)
				}
			} else if err := remote.service.ensureCurrentWorkspace(ctx, workspace); err != nil {
				t.Fatal(err)
			}
			after, visible, err := remote.service.pagedRealtimeEvent(request, projected)
			if err != nil || visible && (after.ActorID != "" || after.AggregateID != "" || len(after.Payload) != 0) {
				t.Fatal("queued private payload or actor survived revocation")
			}
			page := authRequest[changesResponse](t, member, "GET", remote.host.URL+fmt.Sprintf("/api/realtime/changes?since=%d", head), nil, workspace, http.StatusOK)
			if page.Next <= head || slices.ContainsFunc(page.Events, func(event domain.RealtimeEvent) bool {
				return event.AggregateID == "p11-inherited" || event.ActorID != "" && event.Type == "project.updated"
			}) {
				t.Fatal("revoked replay leaked a private actor/record or failed to advance")
			}
		})
	}
}

func scaleP11HTTPPeers(t *testing.T) (peerServers, store.DatabaseConfig) {
	t.Helper()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	f := scaleP11NewFixture(t, 0)
	var peers peerServers
	for i, repo := range []*store.SQLiteStore{f.writer, f.peer} {
		ctx, cancel := context.WithCancel(t.Context())
		service := &server{store: repo, uploadPath: t.TempDir(), coordinator: f.service.coordinator, coordinationContext: ctx}
		host := httptest.NewServer(newHandler(service))
		if err := service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { host.Close(); cancel() })
		peers.stores[i], peers.servers[i], peers.deafen[i] = repo, service, cancel
		if i == 0 {
			peers.first = host
		} else {
			peers.second = host
		}
	}
	return peers, f.config
}

func scaleP11WarmCredential(t *testing.T, repo *store.SQLiteStore, workspace, secret string) {
	t.Helper()
	auth, err := repo.AuthenticateAPIKeyRecord(t.Context(), workspace, secretHash(secret), applicationApproved)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	// A use recorded under a minute ago (for example by the fixture) makes the
	// next RecordAPIKeyUse a throttled no-op, so there is no write to wait for.
	var settled *time.Time
	if data, ok := repo.WorkspaceMetadata(workspace); ok {
		for _, key := range data.APIKeys {
			if key.ID == auth.Key.ID && key.LastUsedAt != nil && started.Sub(*key.LastUsedAt) < 50*time.Second {
				settled = key.LastUsedAt
			}
		}
	}
	if err := repo.RecordAPIKeyUse(t.Context(), workspace, auth.Key.ID, auth.Key.AuthorizationID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, ok := repo.WorkspaceMetadata(workspace)
		if ok && slices.ContainsFunc(data.APIKeys, func(key domain.APIKey) bool {
			return key.ID == auth.Key.ID && key.LastUsedAt != nil && (settled != nil || !key.LastUsedAt.Before(started))
		}) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("credential telemetry did not finish before the differential")
}
