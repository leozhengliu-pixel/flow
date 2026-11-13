package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW16AgentSessionDraftLoop(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, userID := f.data.Workspace.URLKey, f.data.Viewer.ID
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	sessions := []domain.AgentSession{
		{ID: "sw16-session", UserID: userID, SlugID: "sw16-old-slug", Title: strings.Repeat("large session ", 24000), LoopIDs: []string{"sw16-published", "sw16-draft"}},
		{ID: "sw16-deleted", UserID: userID, DeletedAt: &stamp, LoopIDs: []string{"sw16-draft"}},
		{ID: "sw16-no-loops", UserID: userID},
		{ID: "sw16-missing-loop", UserID: userID, LoopIDs: []string{"sw16-missing"}},
		{ID: "sw16-case-loop", UserID: userID, LoopIDs: []string{"SW16-draft"}},
		{ID: "sw16-capital-status", UserID: userID, LoopIDs: []string{"sw16-capital"}},
		{ID: "sw16-blank-status", UserID: userID, LoopIDs: []string{"sw16-blank"}},
		{ID: "sw16-text-null", UserID: "null", LoopIDs: []string{"null"}},
		{ID: "sw16-blank-owner", LoopIDs: []string{"sw16-draft"}},
	}
	loops := []domain.Loop{
		{ID: "sw16-published", Status: "published"},
		{ID: "sw16-draft", Status: "draft", Instructions: strings.Repeat("large unused loop body ", 18000)},
		{ID: "sw16-capital", Status: "Draft"},
		{ID: "sw16-blank"},
		{ID: "null", Status: "draft"},
	}
	for index := range 75 {
		id := fmt.Sprintf("sw16-unused-%d", index)
		sessions = append(sessions, domain.AgentSession{ID: id, UserID: userID, Title: "Retained"})
		loops = append(loops, domain.Loop{ID: id, Status: "draft"})
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "agentSessions", "loops"), workspace, "agent.session_updated", "", nil, func(data *domain.Bootstrap) error {
		data.AgentSessions, data.Loops = sessions, loops
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fingerprint := func() [32]byte {
		t.Helper()
		hash := sha256.New()
		rows, err := f.db.QueryContext(t.Context(), `SELECT field,record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('agentSessions','loops') ORDER BY field,collection_order,record_key`, workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var field, key string
			var raw []byte
			if err := rows.Scan(&field, &key, &raw); err != nil {
				t.Fatal(err)
			}
			_, _ = fmt.Fprintf(hash, "%s:%s:", field, key)
			_, _ = hash.Write(raw)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return [32]byte(hash.Sum(nil))
	}
	before := fingerprint()
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	published := 0
	view.SetRealtimeSink(func(string, domain.RealtimeEvent) { published++ })
	view.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
	for _, test := range []struct {
		id, owner string
		want      bool
	}{
		{"sw16-session", userID, true}, {"sw16-deleted", userID, true},
		{"sw16-session", userID + "-other", false}, {"sw16-session", strings.ToUpper(userID), false},
		{"sw16-old-slug", userID, false}, {"SW16-session", userID, false},
		{"sw16-no-loops", userID, false}, {"sw16-missing-loop", userID, false},
		{"sw16-case-loop", userID, false}, {"sw16-capital-status", userID, false},
		{"sw16-blank-status", userID, false}, {"sw16-text-null", "null", true},
		{"sw16-text-null", "", false}, {"sw16-blank-owner", "", true},
		{"", userID, false}, {strings.Repeat("s", 192), userID, false},
		{"sw16-session\x00", userID, false}, {"sw16-session' OR 1=1 --", userID, false},
	} {
		t.Run(test.id+"/"+test.owner, func(t *testing.T) {
			for _, repo := range []*SQLiteStore{f.SQLiteStore, view} {
				got, err := repo.AgentSessionHasDraftLoop(t.Context(), workspace, test.id, test.owner)
				if err != nil || got != test.want {
					t.Fatalf("draft=%t err=%v want=%t", got, err, test.want)
				}
			}
		})
	}
	for _, key := range []string{"", workspace + "-missing", strings.ToUpper(workspace), workspace + " "} {
		got, err := view.AgentSessionHasDraftLoop(t.Context(), key, "sw16-session", userID)
		if err != nil || got != (key == "") {
			t.Fatalf("workspace %q draft=%t err=%v", key, got, err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := view.AgentSessionHasDraftLoop(cancelled, workspace, "sw16-session", userID); got || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled scalar draft=%t err=%v", got, err)
	}
	if got, err := view.AgentSessionHasDraftLoop(t.Context(), workspace, "sw16-session", userID); err != nil || !got {
		t.Fatalf("scalar admission after cancellation: %t %v", got, err)
	}
	if published != 0 || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 || fingerprint() != before {
		t.Fatal("scalar query published, decoded metadata, leaked work or changed rows")
	}
}
