package store

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP18ConversationTargetsAndRecovery(t *testing.T) {
	repo, _ := scaleP04OwnedStore(t)
	data := repo.Bootstrap()
	workspace, owner := data.Workspace.URLKey, data.Viewer.ID
	body := strings.Repeat("complete reply and tool result ", 1000)
	session := domain.AgentSession{ID: "p18-session", SlugID: "p18-slug", UserID: owner, Messages: []domain.AgentMessage{{ID: "p18-reply", Role: "assistant", Content: body, Status: "completed", Outcome: &domain.AgentReplyOutcome{Status: "completed"}}}}
	conversation := domain.AIConversation{ID: "p18-ai", UserID: owner, Context: map[string]any{"history": body, "preserved": true}}
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p18.seeded", "", nil, func(next *domain.Bootstrap) error {
		next.AgentSessions = append(next.AgentSessions, session, domain.AgentSession{ID: "p18-other-owner", UserID: "someone-else", Messages: session.Messages})
		next.AIConversations = append(next.AIConversations, conversation, domain.AIConversation{ID: "p18-other-ai", UserID: "someone-else", Context: conversation.Context})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	access, err := repo.ResolveAccess(t.Context(), workspace, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{session.ID, session.SlugID} {
		loaded, err := repo.AgentConversationRecord(t.Context(), access, ref, true)
		if err != nil || len(loaded.Messages) != 1 || loaded.Messages[0].Content != body || !reflect.DeepEqual(loaded.Messages[0].Outcome, session.Messages[0].Outcome) {
			t.Fatalf("transcript/recovery outcome changed: ref=%s err=%v", ref, err)
		}
		loaded.Messages[0].Content = "caller edit"
	}
	loaded, err := repo.AIConversationRecord(t.Context(), access, conversation.ID)
	if err != nil || !reflect.DeepEqual(loaded.Context, conversation.Context) {
		t.Fatalf("long AI context changed: %v", err)
	}
	if _, err := repo.AgentConversationRecord(t.Context(), access, "p18-other-owner", true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("other owner's session: %v", err)
	}
	if _, err := repo.AIConversationRecord(t.Context(), access, "p18-other-ai"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("other owner's AI context: %v", err)
	}
	// A retained/queued context is not authorization after revocation.
	for _, user := range data.Users {
		if user.ID != owner {
			if err := repo.SetTeamMembership(t.Context(), data.Workspace.ID, data.Teams[0].ID, user.ID, "owner", true); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if err := repo.SetTeamMembership(t.Context(), data.Workspace.ID, data.Teams[0].ID, owner, "member", false); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AgentConversationRecord(t.Context(), access, session.ID, false); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("queued context survived epoch change: %v", err)
	}
}

func TestScaleP18ConversationScopePreservesUnloadedContexts(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, _ := scaleP04OwnedStore(t)
	data := repo.Bootstrap()
	workspace := data.Workspace.URLKey
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p18.seeded", "", nil, func(next *domain.Bootstrap) error {
		next.AIConversations = []domain.AIConversation{{ID: "p18-target", UserID: data.Viewer.ID, Context: map[string]any{"keep": "target"}}, {ID: "p18-unrelated", UserID: data.Viewer.ID, Context: map[string]any{"keep": strings.Repeat("long context ", 10000)}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx := ContextWithActor(WithMetadataRecords(t.Context(), MetadataRecordScope{Write: []RecordKey{{Kind: "aiConversations", ID: "p18-target"}}}), data.Viewer)
	if err := repo.MutateWorkspace(ctx, workspace, "ai.conversation_updated", "p18-target", nil, func(next *domain.Bootstrap) error {
		if len(next.AIConversations) != 1 || next.AIConversations[0].ID != "p18-target" {
			t.Fatalf("hydrated unrelated conversations: %d", len(next.AIConversations))
		}
		next.AIConversations[0].Title = "Edited title"
		next.AIConversations[0].UpdatedAt = time.Now().UTC()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	access, err := repo.ResolveAccess(t.Context(), workspace, data.Viewer.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"p18-target", "p18-unrelated"} {
		item, err := repo.AIConversationRecord(t.Context(), access, id)
		if err != nil || item.Context["keep"] == "" {
			t.Fatalf("unloaded context lost: %s %v", id, err)
		}
	}
}
