package main

import (
	"net/http"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// requestStatus sends a request and returns its status.
func requestStatus(t *testing.T, client *http.Client, method, url, workspace string) int {
	t.Helper()
	request, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if workspace != "" {
		request.Header.Set("X-Workspace-Key", workspace)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response.StatusCode
}

// A server keeps its copy of the workspace while the stored metadata
// revision is unchanged; access changes committed on another server still
// apply to this server's next request.
func TestPeerAccessChangeAppliesToTheNextRequestDespiteKeptCopies(t *testing.T) {
	peers := newPeerServers(t)
	first, second := peers.first.URL, peers.second.URL
	admin := peerAdmin(t, first)
	teamsURL := first + "/api/workspaces/test-workspace/teams"
	team := authRequest[domain.Team](t, admin, http.MethodPost, teamsURL, map[string]any{"name": "Revision Team", "key": "RVT"}, "", http.StatusCreated)
	member, memberUser := joinByInvitation(t, first, admin, "Revision Member", "revision-member@example.com", "member", team.ID)
	leaver, leaverUser := joinByInvitation(t, first, admin, "Revision Leaver", "revision-leaver@example.com", "member", team.ID)
	issue := authRequest[domain.Issue](t, admin, http.MethodPost, first+"/api/issues", map[string]any{"title": "Revision issue", "teamId": team.ID}, "test-workspace", http.StatusCreated)
	issueURL := second + "/api/issue-records/" + issue.ID
	for _, client := range []*http.Client{member, leaver} {
		authRequest[domain.Issue](t, client, http.MethodGet, issueURL, nil, "test-workspace", http.StatusOK)
	}

	// Issue edits change no workspace metadata: the other server's peer
	// events and requests keep its copy instead of decoding it again.
	decodes := store.WorkspaceMetadataDecodes()
	for index := range 5 {
		authRequest[domain.Issue](t, admin, http.MethodPatch, first+"/api/issues/"+issue.ID, map[string]any{"title": "Revision issue " + string(rune('a'+index))}, "test-workspace", http.StatusOK)
		authRequest[domain.Issue](t, member, http.MethodGet, issueURL, nil, "test-workspace", http.StatusOK)
	}
	time.Sleep(200 * time.Millisecond) // the peer events are applied
	if n := store.WorkspaceMetadataDecodes() - decodes; n != 0 {
		t.Errorf("issue edits decoded the workspace metadata %d times", n)
	}

	// The team made private, then the member taken off it, on the first
	// server: the second server stops serving the member at once.
	authRequest[domain.Team](t, admin, http.MethodPatch, teamsURL+"/"+team.ID, map[string]any{"private": true}, "", http.StatusOK)
	authRequest[domain.Issue](t, member, http.MethodGet, issueURL, nil, "test-workspace", http.StatusOK)
	authRequest[any](t, admin, http.MethodPut, teamsURL+"/"+team.ID+"/members/"+memberUser.ID, map[string]any{"member": false}, "", http.StatusNoContent)
	if status := requestStatus(t, member, http.MethodGet, issueURL, "test-workspace"); status == http.StatusOK {
		t.Fatal("the second server still served the private team's issue to a member taken off the team")
	}
	authRequest[domain.Issue](t, leaver, http.MethodGet, issueURL, nil, "test-workspace", http.StatusOK)

	// A member removed on the first server loses the workspace on the
	// second at once.
	authRequest[any](t, admin, http.MethodDelete, first+"/api/workspaces/test-workspace/members/"+leaverUser.ID, nil, "", http.StatusNoContent)
	if status := requestStatus(t, leaver, http.MethodGet, issueURL, "test-workspace"); status == http.StatusOK {
		t.Fatal("the second server still served a removed member")
	}
	if status := requestStatus(t, leaver, http.MethodGet, second+"/api/bootstrap", "test-workspace"); status == http.StatusOK {
		t.Fatal("the second server still bootstrapped a removed member")
	}

	// A metadata change made on the second server after the first one's is
	// written onto the current copy there.
	renamed := authRequest[domain.Team](t, admin, http.MethodPatch, second+"/api/workspaces/test-workspace/teams/"+team.ID, map[string]any{"name": "Revision Team Renamed"}, "", http.StatusOK)
	if !renamed.Private || renamed.Name != "Revision Team Renamed" {
		t.Fatalf("team after both servers' changes = %+v", renamed)
	}
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, first+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	for _, item := range bootstrap.Teams {
		if item.ID == team.ID && (!item.Private || item.Name != "Revision Team Renamed") {
			t.Fatalf("first server's team = %+v", item)
		}
	}
}
