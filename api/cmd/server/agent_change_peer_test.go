package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Undo of one agent reply on two servers at once: the second waits for the
// first and then finds nothing left to undo, instead of both reverting the
// same change.
func TestPeerAgentChangeUndoRunsOnceAcrossServers(t *testing.T) {
	peers := newPeerServers(t)
	admin := peerAdmin(t, peers.first.URL)
	repository := peers.stores[0]
	workspace := repository.WorkspaceKeys()[0]
	data, _ := repository.WorkspaceMetadata(workspace)
	userIndex := slices.IndexFunc(data.Users, func(user domain.User) bool { return user.Email == "admin@example.test" })
	if userIndex < 0 || len(data.Projects) == 0 {
		t.Fatal("fixture lacks the admin or a project")
	}
	userID, projectID := data.Users[userIndex].ID, data.Projects[0].ID
	sessionID, messageID := "agent_peer_undo_session", "agent_peer_undo_reply"
	err := repository.MutateWorkspace(context.Background(), workspace, "agent.session_created", sessionID, nil, func(next *domain.Bootstrap) error {
		next.Projects = slices.Clone(next.Projects)
		index := slices.IndexFunc(next.Projects, func(project domain.Project) bool { return project.ID == projectID })
		next.Projects[index].Description = "Agent plan"
		now := time.Now().UTC()
		change := &domain.AgentChange{Action: "updated", EntityType: "project", EntityID: projectID, Label: next.Projects[index].Name, Fields: []string{"description"}, Before: json.RawMessage(`{"description":"Original plan"}`), After: json.RawMessage(`{"description":"Agent plan"}`), Undoable: true}
		next.AgentSessions = append(slices.Clone(next.AgentSessions), domain.AgentSession{ID: sessionID, SlugID: sessionID, UserID: userID, Title: "Peer undo", Location: "page", IssueIDs: []string{}, CreatedAt: now, UpdatedAt: now, Messages: []domain.AgentMessage{
			{ID: "agent_peer_undo_question", Role: "user", Content: "Edit the plan", CreatedAt: now},
			{ID: messageID, Role: "assistant", Content: "Done.", CreatedAt: now, Parts: []domain.AgentMessagePart{{ID: "agent_peer_undo_part", Type: "tool", ToolCall: &domain.AgentToolCall{ID: "call_1", Name: "save_project", Change: change}}}},
		}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, repo := range peers.stores {
		if err := repo.ReloadWorkspace(context.Background(), workspace); err != nil {
			t.Fatal(err)
		}
	}
	// The first Undo pauses after it chose what to revert.
	paused, release := make(chan struct{}), make(chan struct{})
	var entered atomic.Int32
	agentChangeTargetsHook = func() {
		if entered.Add(1) == 1 {
			close(paused)
			<-release
		}
	}
	t.Cleanup(func() { agentChangeTargetsHook = nil })
	undo := func(baseURL string) <-chan int {
		done := make(chan int, 1)
		go func() {
			request, _ := http.NewRequest(http.MethodPost, baseURL+"/api/agent/sessions/"+sessionID+"/messages/"+messageID+"/undo", strings.NewReader("{}"))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Workspace-Key", workspace)
			response, err := admin.Do(request)
			if err != nil {
				done <- 0
				return
			}
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			done <- response.StatusCode
		}()
		return done
	}
	first := undo(peers.first.URL)
	select {
	case <-paused:
	case <-time.After(10 * time.Second):
		t.Fatal("first undo never chose its changes")
	}
	second := undo(peers.second.URL)
	select {
	case status := <-second:
		close(release)
		<-first
		t.Fatalf("the second server's undo finished (%d) while the first server's undo of the same reply was still running", status)
	case <-time.After(time.Second):
	}
	close(release)
	if status := <-first; status != http.StatusOK {
		t.Fatalf("first undo status %d, want 200", status)
	}
	if status := <-second; status != http.StatusConflict {
		t.Fatalf("second undo status %d, want 409 (nothing left to undo)", status)
	}
	if entered.Load() != 1 {
		t.Fatalf("%d undos chose changes to revert, want 1", entered.Load())
	}
}
