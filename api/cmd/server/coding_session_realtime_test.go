package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// coding_session.* pushes name the session's issue, phase and state, so they
// reach only the people who may open the session: another member's private
// chat session stays out of a member's stream, an issue session reaches the
// issue's members, and a guest sees only their own sessions.
func TestCodingSessionPushesFollowSessionAccess(t *testing.T) {
	f := newCodingAccessFixture(t)
	private := f.start(coding.StartInput{Source: domain.CodingSourceAgentChat, CreatorID: "usr_admin"})
	issue := requestJSON[domain.Issue](t, f.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Flaky test", "teamId": f.workspace.Teams[0].ID}, http.StatusCreated)
	shared := f.start(coding.StartInput{Source: domain.CodingSourceIssue, IssueID: issue.ID, CreatorID: "usr_admin"})
	f.s.authDisabled = false
	defer func() { f.s.authDisabled = true }()
	host := httptest.NewServer(f.handler)
	// Cleanups run last-in first-out: the streams close before the server.
	t.Cleanup(host.Close)

	open := func(user string) <-chan string {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, host.URL+"/api/realtime/events?workspace="+f.workspace.Workspace.URLKey, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+f.secrets[user])
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s stream: %d", user, response.StatusCode)
		}
		t.Cleanup(func() { response.Body.Close() })
		lines := make(chan string, 64)
		go func() {
			defer close(lines)
			scanner := bufio.NewScanner(response.Body)
			for scanner.Scan() {
				if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
					lines <- data
				}
			}
		}()
		select {
		case line := <-lines:
			if !strings.Contains(line, `"connected"`) {
				t.Fatalf("%s handshake: %s", user, line)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: no handshake", user)
		}
		return lines
	}
	// received collects the coding session ids pushed to a stream until the
	// marker (the shared session's push, published last) arrives.
	received := func(user string, lines <-chan string) []string {
		ids := []string{}
		deadline := time.After(3 * time.Second)
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					t.Fatalf("%s: stream closed after %v", user, ids)
				}
				var event domain.RealtimeEvent
				if json.Unmarshal([]byte(line), &event) != nil || !strings.HasPrefix(event.Type, "coding_session.") {
					continue
				}
				ids = append(ids, event.AggregateID)
				if event.AggregateID == shared.ID {
					return ids
				}
			case <-deadline:
				t.Fatalf("%s: marker not delivered, got %v", user, ids)
			}
		}
	}
	push := func(session domain.CodingSession) {
		payload, _ := json.Marshal(map[string]any{"sessionId": session.ID, "agentSessionId": session.AgentSessionID, "issueId": session.IssueID, "phase": "running", "state": "active"})
		f.s.publishRealtime(f.workspace.Workspace.URLKey, domain.RealtimeEvent{ID: fmt.Sprintf("coding_%s_%d", session.ID, time.Now().UnixNano()), Type: "coding_session.updated", AggregateID: session.ID, Payload: payload, CreatedAt: time.Now().UTC()})
	}

	admin, member := open("usr_admin"), open("usr_member")
	push(private)
	push(shared)
	if got := received("usr_admin", admin); strings.Join(got, ",") != private.ID+","+shared.ID {
		t.Fatalf("admin pushes = %v", got)
	}
	if got := received("usr_member", member); strings.Join(got, ",") != shared.ID {
		t.Fatalf("member pushes = %v (another member's chat session must not be pushed)", got)
	}

	// A guest sees only their own sessions, not the issue's.
	if err := f.s.store.UpdateMemberRole(t.Context(), f.workspace.Workspace.ID, "usr_member", "guest"); err != nil {
		t.Fatal(err)
	}
	own := f.start(coding.StartInput{Source: domain.CodingSourceAgentChat, CreatorID: "usr_member"})
	guest := open("usr_member")
	push(own)
	push(shared)
	push(private)
	ids := []string{}
	deadline := time.After(time.Second)
	for done := false; !done; {
		select {
		case line := <-guest:
			var event domain.RealtimeEvent
			if json.Unmarshal([]byte(line), &event) == nil && strings.HasPrefix(event.Type, "coding_session.") {
				ids = append(ids, event.AggregateID)
			}
		case <-deadline:
			done = true
		}
	}
	if strings.Join(ids, ",") != own.ID {
		t.Fatalf("guest pushes = %v, want only %s", ids, own.ID)
	}
}
