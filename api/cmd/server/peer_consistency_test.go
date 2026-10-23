package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
	"flow/api/internal/store"

	"github.com/alicebob/miniredis/v2"
)

// peerServers runs two API servers on one database with Redis coordination,
// like a two-replica deployment.
type peerServers struct {
	first, second *httptest.Server
	stores        [2]*store.SQLiteStore
	servers       [2]*server
	// deafen stops a server's peer event subscription: it then learns of the
	// other server's changes only from the database (the worst case of a slow
	// or lost peer event).
	deafen [2]context.CancelFunc
}

func (p peerServers) stopListening(index int) {
	p.deafen[index]()
	// Listen returns once it sees the cancellation.
	time.Sleep(50 * time.Millisecond)
}

func newPeerServers(t *testing.T) peerServers {
	t.Helper()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	redisServer := miniredis.RunT(t)
	path := filepath.Join(t.TempDir(), "peers.db")
	var peers peerServers
	uploads := t.TempDir()
	for index := range 2 {
		repository, err := store.OpenSQLiteTestFixture(path)
		if err != nil {
			t.Fatal(err)
		}
		coordinator, err := coordination.Open(context.Background(), coordination.Config{Mode: "standalone", Addrs: []string{redisServer.Addr()}, Prefix: "peers", ConnectTimeout: time.Second, LockTTL: 5 * time.Second, LockWait: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		repository.SetWorkspaceCoordinator(coordinator)
		ctx, cancel := context.WithCancel(context.Background())
		instance := &server{store: repository, uploadPath: uploads, coordinator: coordinator, coordinationContext: ctx}
		httpServer := httptest.NewServer(newHandler(instance))
		t.Cleanup(func() {
			httpServer.Close()
			cancel()
			_ = coordinator.Close()
			_ = repository.Close()
		})
		peers.stores[index], peers.servers[index], peers.deafen[index] = repository, instance, cancel
		if index == 0 {
			peers.first = httpServer
		} else {
			peers.second = httpServer
		}
	}
	return peers
}

func peerAdmin(t *testing.T, baseURL string) *http.Client {
	t.Helper()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, baseURL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	return admin
}

// A workspace created on one server can be joined through the other at once,
// before that server has applied the peer's change.
func TestPeerServerAcceptsInvitationToAWorkspaceCreatedOnAnotherServer(t *testing.T) {
	peers := newPeerServers(t)
	owner := peerAdmin(t, peers.second.URL)
	peers.stopListening(0)
	for attempt := range 3 {
		key := "peer-space-" + string(rune('a'+attempt))
		invitee, user := verifiedAuthClient(t, peers.first.URL, "Peer Invitee", "peer-invitee-"+key+"@example.com")
		authRequest[domain.Bootstrap](t, owner, http.MethodPost, peers.second.URL+"/api/workspaces", map[string]string{"name": "Peer Space", "urlKey": key}, "", http.StatusCreated)
		invitations := authRequest[[]domain.Invitation](t, owner, http.MethodPost, peers.second.URL+"/api/workspaces/"+key+"/invitations", map[string]any{"emails": []string{user.Email}, "role": "member"}, "", http.StatusCreated)
		membership := authRequest[domain.WorkspaceMembership](t, invitee, http.MethodPost, peers.first.URL+"/api/invitations/accept", map[string]string{"token": invitations[0].Token}, "", http.StatusOK)
		if membership.Workspace.URLKey != key {
			t.Fatalf("accepted workspace = %#v", membership.Workspace)
		}
		authRequest[domain.Bootstrap](t, invitee, http.MethodGet, peers.first.URL+"/api/bootstrap", nil, key, http.StatusOK)
	}
}

// Entities created and permissions changed on one server are usable on the
// other at once, even when that server never receives the peer events.
func TestPeerServerServesChangesMadeOnAnotherServer(t *testing.T) {
	peers := newPeerServers(t)
	admin := peerAdmin(t, peers.second.URL)
	peers.stopListening(0)
	first, second := peers.first.URL, peers.second.URL

	// A new team.
	team := authRequest[domain.Team](t, admin, http.MethodPost, second+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Peer team", "key": "PEER"}, "", http.StatusCreated)
	authRequest[domain.Issue](t, admin, http.MethodPost, first+"/api/issues", map[string]any{"title": "In the new team", "teamId": team.ID}, "test-workspace", http.StatusCreated)

	// A new member (invited here, accepted on the other server) in the new team.
	member, user := verifiedAuthClient(t, first, "Peer Member", "peer-member@example.com")
	invitations := authRequest[[]domain.Invitation](t, admin, http.MethodPost, first+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{user.Email}, "role": "member", "teamIds": []string{team.ID}}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, member, http.MethodPost, second+"/api/invitations/accept", map[string]string{"token": invitations[0].Token}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, member, http.MethodGet, first+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	if !slices.ContainsFunc(bootstrap.Teams, func(item domain.Team) bool { return item.ID == team.ID }) {
		t.Fatalf("new member does not see the new team: %#v", bootstrap.Teams)
	}

	// A permission change: the member becomes an admin.
	authRequest[any](t, member, http.MethodPost, first+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{"peer-next@example.com"}}, "", http.StatusForbidden)
	authRequest[domain.WorkspaceMember](t, admin, http.MethodPatch, second+"/api/workspaces/test-workspace/members/"+user.ID, map[string]string{"role": "admin"}, "", http.StatusOK)
	authRequest[[]domain.Invitation](t, member, http.MethodPost, first+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{"peer-next@example.com"}, "role": "member"}, "", http.StatusCreated)

	// A new API key, scoped to the new team.
	key := authRequest[struct {
		Secret string `json:"secret"`
	}](t, admin, http.MethodPost, second+"/api/api-keys", map[string]any{"name": "Peer key", "scopes": []string{"read"}, "teamIds": []string{team.ID}}, "test-workspace", http.StatusCreated)
	request, err := http.NewRequest(http.MethodGet, first+"/api/bootstrap", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key.Secret)
	request.Header.Set("X-Workspace-Key", "test-workspace")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var projected domain.Bootstrap
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&projected) != nil || len(projected.Teams) != 1 || projected.Teams[0].ID != team.ID {
		t.Fatalf("new API key on the other server: status %d teams %#v", response.StatusCode, projected.Teams)
	}
}
