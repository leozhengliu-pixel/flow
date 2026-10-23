package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"

	"github.com/coder/websocket"
)

// issueAccessFixture: an admin and a signed-in member with the member's
// collaboration socket joined to an issue description.
type issueAccessFixture struct {
	admin      *http.Client
	member     *http.Client
	memberUser domain.User
	team       domain.Team
	issue      domain.Issue
	documentID string
	socket     *websocket.Conn
}

type issueAccessSetup struct {
	role        string // the member's workspace role
	privateTeam bool   // the issue's team is private
	inTeam      bool   // the member is in the issue's team
	// sharedOnly: the member is in another team and sees the issue through
	// an issue permission.
	sharedOnly bool
}

// newIssueAccessFixture signs the member in on socketURL (where its socket
// connects); the admin acts on adminURL.
func newIssueAccessFixture(t *testing.T, socketURL, adminURL string, setup issueAccessSetup) issueAccessFixture {
	t.Helper()
	admin := peerAdmin(t, adminURL)
	suffix := strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	team := authRequest[domain.Team](t, admin, http.MethodPost, adminURL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Access " + suffix, "key": teamKeyFor(suffix), "private": setup.privateTeam}, "", http.StatusCreated)
	issue := authRequest[domain.Issue](t, admin, http.MethodPost, adminURL+"/api/issues", map[string]any{"title": "Shared description", "teamId": team.ID}, "test-workspace", http.StatusCreated)
	member, user := verifiedAuthClient(t, socketURL, "Access Member", "access-"+suffix+"@example.com")
	invite := map[string]any{"emails": []string{user.Email}, "role": setup.role}
	if setup.inTeam {
		invite["teamIds"] = []string{team.ID}
	}
	if setup.sharedOnly {
		home := authRequest[domain.Team](t, admin, http.MethodPost, adminURL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Home " + suffix, "key": "H" + teamKeyFor(suffix)}, "", http.StatusCreated)
		invite["teamIds"] = []string{home.ID}
	}
	invitations := authRequest[[]domain.Invitation](t, admin, http.MethodPost, adminURL+"/api/workspaces/test-workspace/invitations", invite, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, member, http.MethodPost, socketURL+"/api/invitations/accept", map[string]string{"token": invitations[0].Token}, "", http.StatusOK)
	if setup.sharedOnly {
		authRequest[[]domain.IssuePermission](t, admin, http.MethodPut, adminURL+"/api/issues/"+issue.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": user.ID, "role": "viewer"}}}, "test-workspace", http.StatusOK)
	}
	authRequest[domain.Bootstrap](t, member, http.MethodGet, socketURL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	documentID := "document_content_" + issue.ID
	if issue.DocumentContent != nil && issue.DocumentContent.ID != "" {
		documentID = issue.DocumentContent.ID
	}
	socket := dialAuthenticatedCollaborationSocket(t, member, socketURL, "member-"+suffix)
	t.Cleanup(func() { socket.CloseNow() })
	joinDocument(t, socket, issue.ID, documentID)
	return issueAccessFixture{admin: admin, member: member, memberUser: user, team: team, issue: issue, documentID: documentID, socket: socket}
}

func teamKeyFor(name string) string {
	key := []byte{}
	for _, char := range strings.ToUpper(name) {
		if char >= 'A' && char <= 'Z' && len(key) < 4 {
			key = append(key, byte(char))
		}
	}
	return "X" + string(key) + time.Now().Format("05")
}

// expectDocumentRevoked waits for the removal notice; no edit may arrive
// before it.
func expectDocumentRevoked(t *testing.T, connection *websocket.Conn, documentID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		messageType, message, err := connection.Read(ctx)
		if err != nil {
			t.Fatalf("no document.revoked: %v", err)
		}
		if messageType != websocket.MessageText {
			t.Fatalf("received a %v frame instead of document.revoked", messageType)
		}
		var notice struct {
			Type       string `json:"type"`
			DocumentID string `json:"documentId"`
		}
		if json.Unmarshal(message, &notice) == nil && notice.Type == "document.revoked" && notice.DocumentID == documentID {
			return
		}
		t.Fatalf("unexpected message before document.revoked: %s", message)
	}
}

// expectNoEditsAccepted checks the revoked socket can neither send nor rejoin.
func expectNoEditsAccepted(t *testing.T, fixture issueAccessFixture) {
	t.Helper()
	writeSocket(t, fixture.socket, websocket.MessageBinary, encodeCollaborationFrame(collaborationUpdateFrame, fixture.documentID, "collab_after_revoke", []byte{1}))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, message, err := fixture.socket.Read(ctx)
	if err == nil && !strings.Contains(string(message), "join document before sending updates") {
		t.Fatalf("revoked member's update response = %s", message)
	}
	if err == nil {
		raw, _ := json.Marshal(map[string]string{"type": "document.join", "issueId": fixture.issue.ID, "documentId": fixture.documentID})
		writeSocket(t, fixture.socket, websocket.MessageText, raw)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, message, err := fixture.socket.Read(ctx); err == nil && !strings.Contains(string(message), "document.revoked") {
			t.Fatalf("revoked member rejoined: %s", message)
		}
	}
}

func newSingleAuthServer(t *testing.T) *httptest.Server {
	t.Helper()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "access.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	t.Cleanup(server.Close)
	return server
}

type issueAccessRevocation struct {
	name   string
	setup  issueAccessSetup
	revoke func(t *testing.T, baseURL string, fixture issueAccessFixture)
}

var issueAccessRevocations = []issueAccessRevocation{
	{"removed from the private team", issueAccessSetup{role: "member", privateTeam: true, inTeam: true}, func(t *testing.T, baseURL string, fixture issueAccessFixture) {
		authRequest[any](t, fixture.admin, http.MethodPut, baseURL+"/api/workspaces/test-workspace/teams/"+fixture.team.ID+"/members/"+fixture.memberUser.ID, map[string]any{"member": false}, "", http.StatusNoContent)
	}},
	{"team made private", issueAccessSetup{role: "member"}, func(t *testing.T, baseURL string, fixture issueAccessFixture) {
		authRequest[domain.Team](t, fixture.admin, http.MethodPatch, baseURL+"/api/workspaces/test-workspace/teams/"+fixture.team.ID, map[string]any{"private": true}, "", http.StatusOK)
	}},
	{"guest loses the shared issue", issueAccessSetup{role: "guest", privateTeam: true, sharedOnly: true}, func(t *testing.T, baseURL string, fixture issueAccessFixture) {
		authRequest[[]domain.IssuePermission](t, fixture.admin, http.MethodPut, baseURL+"/api/issues/"+fixture.issue.ID+"/permissions", map[string]any{"permissions": []map[string]string{}}, "test-workspace", http.StatusOK)
	}},
	{"member suspended", issueAccessSetup{role: "member", inTeam: true}, func(t *testing.T, baseURL string, fixture issueAccessFixture) {
		authRequest[any](t, fixture.admin, http.MethodPost, baseURL+"/api/workspaces/test-workspace/members/"+fixture.memberUser.ID+"/suspend", nil, "", http.StatusNoContent)
	}},
	{"member removed from the workspace", issueAccessSetup{role: "member", inTeam: true}, func(t *testing.T, baseURL string, fixture issueAccessFixture) {
		authRequest[any](t, fixture.admin, http.MethodDelete, baseURL+"/api/workspaces/test-workspace/members/"+fixture.memberUser.ID, nil, "", http.StatusNoContent)
	}},
	{"session signed out", issueAccessSetup{role: "member", inTeam: true}, func(t *testing.T, baseURL string, fixture issueAccessFixture) {
		authRequest[any](t, fixture.member, http.MethodPost, baseURL+"/api/auth/logout", nil, "", http.StatusNoContent)
	}},
}

// Every access change that takes an issue away from a member removes its
// description from the member's open socket at once.
func TestCollaborationIssueAccessRevocationReachesOpenSockets(t *testing.T) {
	for _, revocation := range issueAccessRevocations {
		t.Run(revocation.name, func(t *testing.T) {
			server := newSingleAuthServer(t)
			fixture := newIssueAccessFixture(t, server.URL, server.URL, revocation.setup)
			revocation.revoke(t, server.URL, fixture)
			expectDocumentRevoked(t, fixture.socket, fixture.documentID)
			expectNoEditsAccepted(t, fixture)
		})
	}
}

// The change made on another server drops the member's socket on this one.
func TestCollaborationIssueAccessRevocationReachesOtherServers(t *testing.T) {
	for _, revocation := range issueAccessRevocations {
		t.Run(revocation.name, func(t *testing.T) {
			peers := newPeerServers(t)
			fixture := newIssueAccessFixture(t, peers.first.URL, peers.second.URL, revocation.setup)
			revocation.revoke(t, peers.second.URL, fixture)
			expectDocumentRevoked(t, fixture.socket, fixture.documentID)
			expectNoEditsAccepted(t, fixture)
		})
	}
}

// Changes that leave the member's access alone keep the socket joined.
func TestCollaborationIssueAccessKeepsUnaffectedMembers(t *testing.T) {
	server := newSingleAuthServer(t)
	fixture := newIssueAccessFixture(t, server.URL, server.URL, issueAccessSetup{role: "member", privateTeam: true, inTeam: true})
	authRequest[domain.Issue](t, fixture.admin, http.MethodPatch, server.URL+"/api/issues/"+fixture.issue.ID, map[string]any{"title": "Renamed"}, "test-workspace", http.StatusOK)
	authRequest[domain.Team](t, fixture.admin, http.MethodPatch, server.URL+"/api/workspaces/test-workspace/teams/"+fixture.team.ID, map[string]any{"name": "Renamed team"}, "", http.StatusOK)
	writeSocket(t, fixture.socket, websocket.MessageBinary, encodeCollaborationFrame(collaborationUpdateFrame, fixture.documentID, "collab_still_joined", []byte{4}))
	assertUpdateFrame(t, fixture.socket, fixture.documentID, "collab_still_joined", []byte{4})
}
