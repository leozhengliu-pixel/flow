package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW11AgentRunOwnerDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw11-owner-")
		deletedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		sessions := []domain.AgentSession{}
		for _, principal := range f.principals {
			id := prefix + principal.user.ID
			sessions = append(sessions,
				domain.AgentSession{ID: id, SlugID: id + "-old-slug", UserID: principal.user.ID, Title: strings.Repeat("legal large session title ", 16000)},
				domain.AgentSession{ID: id + "-deleted", UserID: principal.user.ID, DeletedAt: &deletedAt},
			)
		}
		for range 60 {
			sessions = append(sessions, domain.AgentSession{ID: idgen.New("sw11-unrelated-"), UserID: prefix + "-unrelated", Title: "Unrelated"})
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "agentSessions"), workspace, "agent.session_updated", "", nil, func(data *domain.Bootstrap) error {
			data.AgentSessions = sessions
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		before := scaleP21SW6RecordHash(t, repo, workspace, "agentSessions", sessions[0].ID)
		off := &server{store: repo}
		on := &server{store: f.probe}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			id := prefix + user.ID
			for _, selected := range []struct {
				id, viewer, workspace string
				want                  bool
			}{
				{id, user.ID, workspace, true},
				{id, user.ID + "-foreign", workspace, false},
				{id + "-deleted", user.ID, workspace, false},
				{id + "-old-slug", user.ID, workspace, false},
				{"sw11-missing", user.ID, workspace, false},
				{id + "\x00", user.ID, workspace, false},
				{strings.Repeat("x", 192), user.ID, workspace, false},
				{id, user.ID, workspace + "-missing", false},
			} {
				// This peer-owned stop predicate deliberately does not inspect the
				// request credential or private-team content. Preserve that boundary.
				_ = f.request(user, key, nil)
				old, next := off.agentRunOwnedBy(selected.workspace, selected.id, selected.viewer), on.agentRunOwnedBy(selected.workspace, selected.id, selected.viewer)
				if old != selected.want || next != old {
					t.Fatalf("owner differential old=%v bounded=%v want=%v id=%q", old, next, selected.want, selected.id)
				}
			}
			for _, service := range []*server{off, on} {
				cancelled := false
				unregister := service.registerAgentRun(workspace, id, func() { cancelled = true })
				payload, _ := json.Marshal(agentForwardedAnswer{Kind: "stop", SessionID: id, UserID: user.ID + "-foreign"})
				service.receiveForwardedAgentAnswer(workspace, domain.RealtimeEvent{Payload: payload})
				if cancelled {
					t.Fatal("foreign queued stop cancelled the run")
				}
				payload, _ = json.Marshal(agentForwardedAnswer{Kind: "stop", SessionID: id, UserID: user.ID})
				service.receiveForwardedAgentAnswer(workspace, domain.RealtimeEvent{Payload: payload})
				if !cancelled {
					t.Fatal("owner queued stop did not cancel the oversized session")
				}
				unregister()
			}
		})
		if f.probe.WorkspaceMetadataDecodes() != 0 || before != scaleP21SW6RecordHash(t, repo, workspace, "agentSessions", sessions[0].ID) {
			t.Fatal("owner reads decoded full metadata or changed the oversized session")
		}
		// Ownership revocation is checked when the queued stop is applied.
		target, originalOwner := sessions[0].ID, sessions[0].UserID
		payload, _ := json.Marshal(agentForwardedAnswer{Kind: "stop", SessionID: target, UserID: originalOwner})
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "agentSessions"), workspace, "agent.session_updated", target, nil, func(data *domain.Bootstrap) error {
			for i := range data.AgentSessions {
				if data.AgentSessions[i].ID == target {
					data.AgentSessions[i].UserID = originalOwner + "-transferred"
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, service := range []*server{off, on} {
			cancelled := false
			unregister := service.registerAgentRun(workspace, target, func() { cancelled = true })
			service.receiveForwardedAgentAnswer(workspace, domain.RealtimeEvent{Payload: payload})
			service.receiveForwardedAgentAnswer(workspace, domain.RealtimeEvent{Payload: payload})
			unregister()
			if cancelled || service.agentRunOwnedBy(workspace, target, originalOwner) {
				t.Fatal("queued or replayed stop retained revoked ownership")
			}
		}
		if _, err := f.probe.AgentSessionOwnedBy(context.Background(), workspace, target, originalOwner+"-transferred"); err != nil {
			t.Fatal(err)
		}
	}
}
