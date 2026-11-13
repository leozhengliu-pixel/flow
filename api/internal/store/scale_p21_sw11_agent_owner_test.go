package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW11AgentSessionOwnedBy(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, owner := f.data.Workspace.URLKey, f.data.Viewer.ID
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	sessions := []domain.AgentSession{
		{ID: "sw11-owned", SlugID: "sw11-old-slug", UserID: owner, Title: strings.Repeat("large owned transcript header ", 20000)},
		{ID: "sw11-other-owner", UserID: owner + "-other"},
		{ID: "sw11-deleted", UserID: owner, DeletedAt: &stamp},
		{ID: "sw11-empty-owner"},
	}
	for i := range 75 {
		sessions = append(sessions, domain.AgentSession{ID: fmt.Sprintf("sw11-unused-%d", i), UserID: owner + "-other", Title: "Unrelated"})
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "agentSessions"), workspace, "agent.session_updated", "", nil, func(data *domain.Bootstrap) error {
		data.AgentSessions = sessions
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read := func() []byte {
		t.Helper()
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='agentSessions' AND record_key='sw11-owned'`, workspace).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := read()
	if len(before) <= MetadataRecordPageMaxBytes {
		t.Fatal("ownership fixture must exceed the selected-record body budget")
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	events := 0
	view.SetRealtimeSink(func(string, domain.RealtimeEvent) { events++ })
	view.SetWebhookSink(func(string, domain.DomainEvent) { events++ })
	for _, test := range []struct {
		id, user string
		want     bool
	}{
		{"sw11-owned", owner, true},
		{"sw11-other-owner", owner, false},
		{"sw11-deleted", owner, false},
		{"sw11-empty-owner", "", true},
		{"sw11-missing", "", false},
		{"sw11-owned", strings.ToUpper(owner), false},
		{"SW11-owned", owner, false},
		{"sw11-old-slug", owner, false},
		{"", owner, false},
		{"sw11-owned\x00", owner, false},
		{strings.Repeat("x", 192), owner, false},
		{"sw11-owned' OR 1=1 --", owner, false},
	} {
		t.Run(test.id+"/"+test.user, func(t *testing.T) {
			got, err := view.AgentSessionOwnedBy(t.Context(), workspace, test.id, test.user)
			if err != nil || got != test.want {
				t.Fatalf("ownership=%v err=%v; want %v", got, err, test.want)
			}
		})
	}
	if got, err := view.AgentSessionOwnedBy(t.Context(), "", "sw11-owned", owner); err != nil || !got {
		t.Fatalf("default workspace ownership=%v err=%v", got, err)
	}
	if got, err := view.AgentSessionOwnedBy(t.Context(), workspace+"-missing", "sw11-owned", owner); got || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing workspace ownership=%v err=%v", got, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := view.AgentSessionOwnedBy(cancelled, workspace, "sw11-owned", owner); got || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ownership=%v err=%v", got, err)
	}
	if got, err := view.AgentSessionOwnedBy(t.Context(), workspace, "sw11-owned", owner); err != nil || !got {
		t.Fatalf("ownership after cancellation=%v err=%v", got, err)
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 || events != 0 {
		t.Fatal("ownership query decoded full metadata, leaked admission or published an event")
	}
	if !bytes.Equal(before, read()) {
		t.Fatal("ownership query rewrote the canonical session")
	}
}
