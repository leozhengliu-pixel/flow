package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// peerRealtimeStream follows a signed-in person's realtime stream on one server.
func peerRealtimeStream(t *testing.T, client *http.Client, baseURL string) <-chan string {
	t.Helper()
	response, err := client.Get(baseURL + "/api/realtime/events?workspace=" + archiveWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	if response.StatusCode != http.StatusOK {
		t.Fatalf("realtime stream on %s: %d", baseURL, response.StatusCode)
	}
	lines := make(chan string, 256)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	select {
	case line := <-lines:
		if !strings.Contains(line, "connected") {
			t.Fatalf("handshake: %s", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no realtime handshake")
	}
	return lines
}

// nextArchivePush waits for the agent_task.updated push that archives taskID.
func nextArchivePush(lines <-chan string, taskID string, wait time.Duration) (domain.RealtimeEvent, bool) {
	deadline := time.After(wait)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				return domain.RealtimeEvent{}, false
			}
			data, isData := strings.CutPrefix(line, "data: ")
			if !isData || !strings.Contains(data, `"taskId":"`+taskID+`"`) || !strings.Contains(data, `"archived":true`) {
				continue
			}
			var event domain.RealtimeEvent
			if json.Unmarshal([]byte(data), &event) == nil && event.Type == "agent_task.updated" {
				return event, true
			}
		case <-deadline:
			return domain.RealtimeEvent{}, false
		}
	}
}

// An agent session archived on one API server leaves the activity of people
// connected to another server live, and a reload there agrees; archiving it
// again on the other server changes nothing and publishes nothing.
func TestAgentSessionArchiveReachesClientsOnAnotherServer(t *testing.T) {
	peers := newPeerServers(t)
	first, second := peers.first.URL, peers.second.URL
	if err := peers.stores[0].RegisterOAuthClient(t.Context(), domain.OAuthClient{ClientID: "peer-archive-agent", ClientName: "Peer archive agent", RedirectURIs: []string{"https://example.test/callback"}}); err != nil {
		t.Fatal(err)
	}
	onFirst, onSecond := peerAdmin(t, first), peerAdmin(t, second)
	team := peers.stores[0].Bootstrap().Teams[0].ID
	installed := authRequest[struct {
		Application domain.ApplicationInstallation `json:"application"`
	}](t, onFirst, http.MethodPost, first+"/api/application-installations", map[string]any{"clientId": "peer-archive-agent", "teamIds": []string{team}, "scopes": []string{"read", "write", "app:assignable", "app:mentionable"}, "active": true}, archiveWorkspace, http.StatusOK)
	issue := authRequest[domain.Issue](t, onFirst, http.MethodPost, first+"/api/issues", map[string]any{"title": "Agent work across servers", "teamId": team}, archiveWorkspace, http.StatusCreated)
	delegated := authRequest[domain.Issue](t, onFirst, http.MethodPatch, first+"/api/issue-records/"+issue.ID, map[string]any{"delegateId": installed.Application.UserID}, archiveWorkspace, http.StatusOK)
	session := delegated.AgentSessionID
	if session == "" {
		t.Fatalf("delegation started no session: %+v", delegated)
	}
	sessionsOn := func(client *http.Client, baseURL string, includeArchived bool) []domain.AgentTask {
		t.Helper()
		path := baseURL + "/api/agent-tasks?resourceType=issue&resourceId=" + issue.ID
		if includeArchived {
			path += "&includeArchived=true"
		}
		return authRequest[[]domain.AgentTask](t, client, http.MethodGet, path, nil, archiveWorkspace, http.StatusOK)
	}
	hasSession := func(tasks []domain.AgentTask) bool {
		return slices.ContainsFunc(tasks, func(task domain.AgentTask) bool { return task.ID == session })
	}
	// The second server has read the session (and its issue) before the archive.
	if !hasSession(sessionsOn(onSecond, second, false)) {
		t.Fatal("the other server does not list the session before the archive")
	}
	authRequest[domain.Issue](t, onSecond, http.MethodGet, second+"/api/issue-records/"+issue.ID, nil, archiveWorkspace, http.StatusOK)

	// Archived on the first server: the stream on the second server pushes it once.
	secondStream := peerRealtimeStream(t, onSecond, second)
	firstStream := peerRealtimeStream(t, onFirst, first)
	archived := authRequest[agentSessionArchiveResponse](t, onFirst, http.MethodPost, first+"/api/agent-tasks/"+session+"/archive", map[string]any{}, archiveWorkspace, http.StatusOK)
	if !archived.Archived || archived.AlreadyArchived || archived.Session.ArchivedAt == nil {
		t.Fatalf("archive on the first server: %+v", archived)
	}
	// The push names the issue: the web client reloads that resource's
	// sessions on it (AGENT_TASK_ACTIVITY_EVENT with the aggregate id).
	push, ok := nextArchivePush(secondStream, session, 3*time.Second)
	if !ok || push.AggregateID != issue.ID {
		t.Fatalf("the stream on the other server pushed %+v (found %v), want the archive of %s on issue %s", push, ok, session, issue.ID)
	}
	if got := archiveEvents(secondStream, session, 500*time.Millisecond); got != 0 {
		t.Fatalf("the stream on the other server got %d more archive pushes, want 1 in all", got)
	}
	if got := archiveEvents(firstStream, session, time.Second); got != 1 {
		t.Fatalf("the stream on the archiving server got %d archive pushes, want 1", got)
	}
	// A reload on the second server agrees with the push.
	if hasSession(sessionsOn(onSecond, second, false)) {
		t.Fatal("the other server still lists the archived session")
	}
	kept := sessionsOn(onSecond, second, true)
	if index := slices.IndexFunc(kept, func(task domain.AgentTask) bool { return task.ID == session }); index < 0 || kept[index].ArchivedAt == nil || kept[index].Version != archived.Session.Version {
		t.Fatalf("the other server's archived session: %+v, want version %d", kept, archived.Session.Version)
	}
	read := authRequest[struct {
		Session domain.AgentTask `json:"session"`
	}](t, onSecond, http.MethodGet, second+"/api/agent-tasks/"+session, nil, archiveWorkspace, http.StatusOK).Session
	if read.ArchivedAt == nil || read.Version != archived.Session.Version {
		t.Fatalf("the other server reads the session as %+v", read)
	}

	// Retried on the second server: a no-op, with no new push on either server
	// and no version bump.
	head, err := peers.stores[1].WorkspaceChangeHead(t.Context(), archiveWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	again := authRequest[agentSessionArchiveResponse](t, onSecond, http.MethodPost, second+"/api/agent-tasks/"+session+"/archive", map[string]any{}, archiveWorkspace, http.StatusOK)
	if !again.AlreadyArchived || again.Session.Version != archived.Session.Version || again.Session.ArchivedAt == nil {
		t.Fatalf("archive retried on the other server: %+v", again)
	}
	if stored, err := peers.stores[0].AgentTask(t.Context(), archiveWorkspace, session); err != nil || stored.Version != archived.Session.Version {
		t.Fatalf("stored session after the retry: version %d err=%v, want %d", stored.Version, err, archived.Session.Version)
	}
	if after, _ := peers.stores[1].WorkspaceChangeHead(t.Context(), archiveWorkspace); after != head {
		t.Fatalf("the retry wrote a change: %d -> %d", head, after)
	}
	if archiveEvents(secondStream, session, 700*time.Millisecond) != 0 || archiveEvents(firstStream, session, 300*time.Millisecond) != 0 {
		t.Fatal("the retry published another archive push")
	}

	// Both servers still know the installed application. The archive's audit
	// entry reloads the workspace under the coordinator; that reload used to
	// read the shared metadata cache filled before the install, drop the app
	// user, and refuse the next delegation with "unknown delegate".
	for index := range 2 {
		metadata, _ := peers.stores[index].WorkspaceMetadata(archiveWorkspace)
		if !slices.ContainsFunc(metadata.Users, func(user domain.User) bool { return user.ID == installed.Application.UserID }) {
			t.Fatalf("server %d no longer knows the installed application", index)
		}
	}

	// A server that misses the peer event still serves the archive: a reload
	// there reads the session from the database, not from a cached copy.
	authRequest[domain.Issue](t, onFirst, http.MethodPatch, first+"/api/issue-records/"+issue.ID, map[string]any{"delegateId": ""}, archiveWorkspace, http.StatusOK)
	next := authRequest[domain.Issue](t, onFirst, http.MethodPatch, first+"/api/issue-records/"+issue.ID, map[string]any{"delegateId": installed.Application.UserID}, archiveWorkspace, http.StatusOK).AgentSessionID
	if next == "" || next == session {
		t.Fatalf("second delegation session = %q", next)
	}
	if !slices.ContainsFunc(sessionsOn(onSecond, second, false), func(task domain.AgentTask) bool { return task.ID == next }) {
		t.Fatal("the other server does not list the second session")
	}
	peers.stopListening(1)
	authRequest[agentSessionArchiveResponse](t, onFirst, http.MethodPost, first+"/api/agent-tasks/"+next+"/archive", map[string]any{}, archiveWorkspace, http.StatusOK)
	if slices.ContainsFunc(sessionsOn(onSecond, second, false), func(task domain.AgentTask) bool { return task.ID == next }) {
		t.Fatal("a server that missed the peer event still lists the archived session")
	}
	if retried := authRequest[agentSessionArchiveResponse](t, onSecond, http.MethodPost, second+"/api/agent-tasks/"+next+"/archive", map[string]any{}, archiveWorkspace, http.StatusOK); !retried.AlreadyArchived {
		t.Fatalf("a retry on a server that missed the peer event: %+v", retried)
	}
}
