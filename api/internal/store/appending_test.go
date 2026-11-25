package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type scaleP21SW0AppendRow struct {
	ID    string
	Order int
	Raw   []byte
}

func scaleP21SW0AppendRows(t *testing.T, repo *SQLiteStore, workspace string) []scaleP21SW0AppendRow {
	t.Helper()
	rows, err := repo.db.QueryContext(t.Context(), `SELECT record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field='aiConversations' ORDER BY collection_order,record_key`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := []scaleP21SW0AppendRow{}
	for rows.Next() {
		var row scaleP21SW0AppendRow
		if err := rows.Scan(&row.ID, &row.Order, &row.Raw); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestScaleP21SW0AppendingDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	initial := make([]domain.AIConversation, 80)
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range initial {
		initial[i] = domain.AIConversation{ID: fmt.Sprintf("existing-%03d", i), UserID: f.data.Viewer.ID, Title: "Existing", Context: map[string]any{"body": strings.Repeat("history ", 1000)}, CreatedAt: stamp, UpdatedAt: stamp}
	}
	initial[len(initial)-1].Context["body"] = strings.Repeat("unrelated ", 40000)
	reset := func() {
		t.Helper()
		if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "aiConversations", "webhooks"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
			data.AIConversations = slices.Clone(initial)
			data.Webhooks = []domain.Webhook{{ID: "sw0-append-hook", Enabled: true, URL: "https://example.test/hook"}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	first := domain.AIConversation{ID: "z-" + idgen.New("sw0-append-"), UserID: f.data.Viewer.ID, Title: "First", CreatedAt: stamp, UpdatedAt: stamp}
	second := domain.AIConversation{ID: "a-" + idgen.New("sw0-append-"), UserID: f.data.Viewer.ID, Title: "Second", CreatedAt: stamp, UpdatedAt: stamp}
	type outcome struct {
		Rows     []scaleP21SW0AppendRow
		Audits   []domain.AuditLogEntry
		Webhooks []domain.DomainEvent
		Realtime []domain.RealtimeEvent
	}
	var results []outcome
	for _, bounded := range []bool{false, true} {
		reset()
		before := scaleP21SW0AppendRows(t, f.SQLiteStore, workspace)
		unrelated := sha256.Sum256(before[len(before)-1].Raw)
		repo := f.SQLiteStore
		if bounded {
			var err error
			repo, err = f.BoundedMetadataViewForTesting(t.Context())
			if err != nil {
				t.Fatal(err)
			}
		}
		got := outcome{}
		repo.SetWebhookSink(func(_ string, event domain.DomainEvent) { got.Webhooks = append(got.Webhooks, event) })
		repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { got.Realtime = append(got.Realtime, event) })
		scope := MetadataAppendScope{MetadataRecordScope: MetadataRecordScope{Write: []RecordKey{{Kind: "aiConversations", ID: initial[0].ID}}}, AppendKinds: []string{"aiConversations"}}
		started := time.Now().UTC()
		err := repo.MutateWorkspaceAppendingWithAggregate(WithMetadataFields(t.Context(), "aiConversations", "auditLog"), workspace, "ai.conversation_created", map[string]any{"title": first.Title}, scope, func(data *domain.Bootstrap) (string, error) {
			if bounded && len(data.AIConversations) != 1 {
				t.Fatalf("loaded unrelated conversation bodies: %d", len(data.AIConversations))
			}
			for i := range data.AIConversations {
				if data.AIConversations[i].ID == initial[0].ID {
					data.AIConversations[i].Title = "Updated existing"
				}
			}
			data.AIConversations = append(data.AIConversations, first, second)
			data.AuditLog = append(data.AuditLog, domain.AuditLogEntry{ID: idgen.New("sw0-append-audit-"), Actor: data.Viewer, Action: "sw0.append", ResourceType: "ai_conversation", ResourceID: first.ID})
			return first.ID, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		got.Rows = scaleP21SW0AppendRows(t, repo, workspace)
		if len(got.Rows) != len(initial)+2 || got.Rows[len(initial)].ID != first.ID || got.Rows[len(initial)+1].ID != second.ID {
			t.Fatal("insertion did not preserve callback order")
		}
		if sha256.Sum256(got.Rows[len(initial)-1].Raw) != unrelated {
			t.Fatal("insertion rewrote an unrelated oversized record")
		}
		got.Audits, err = repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, AuditQuery{Since: started})
		if err != nil || len(got.Audits) != 1 || len(got.Webhooks) != 1 || len(got.Realtime) != 1 {
			t.Fatalf("append effects: audits=%d webhooks=%d realtime=%d err=%v", len(got.Audits), len(got.Webhooks), len(got.Realtime), err)
		}
		for i := range got.Audits {
			got.Audits[i].ID, got.Audits[i].Hash, got.Audits[i].Seq, got.Audits[i].CreatedAt = "", "", 0, time.Time{}
		}
		for i := range got.Webhooks {
			got.Webhooks[i].ID, got.Webhooks[i].CreatedAt = "", time.Time{}
		}
		for i := range got.Realtime {
			got.Realtime[i].ID, got.Realtime[i].Seq, got.Realtime[i].CreatedAt = "", 0, time.Time{}
		}
		if bounded && (repo.WorkspaceMetadataDecodes() != 0 || repo.metadataResidency.work.used != 0) {
			t.Fatal("append decoded full metadata or leaked admission")
		}
		results = append(results, got)
		repo.SetWebhookSink(nil)
		repo.SetRealtimeSink(nil)
	}
	if !reflect.DeepEqual(results[0], results[1]) {
		t.Fatal("append canonical state or audit/webhook/realtime effects differ")
	}
}

func TestScaleP21SW0AppendingRejectsAndRollsBack(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	existing := domain.AIConversation{ID: "existing", UserID: f.data.Viewer.ID, Title: "Keep"}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "aiConversations"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.AIConversations = []domain.AIConversation{existing}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	repo, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	before := scaleP21SW0AppendRows(t, repo, workspace)
	failure := errors.New("owned callback error")
	for _, spec := range []struct {
		name  string
		scope MetadataAppendScope
		want  error
		run   func(*domain.Bootstrap) error
	}{
		{"duplicate-existing", MetadataAppendScope{AppendKinds: []string{"aiConversations"}}, ErrMetadataRecordQuery, func(data *domain.Bootstrap) error {
			data.AIConversations = append(data.AIConversations, existing)
			return nil
		}},
		{"duplicate-created", MetadataAppendScope{AppendKinds: []string{"aiConversations"}}, ErrMetadataRecordQuery, func(data *domain.Bootstrap) error {
			item := domain.AIConversation{ID: "new"}
			data.AIConversations = append(data.AIConversations, item, item)
			return nil
		}},
		{"undeclared-kind", MetadataAppendScope{AppendKinds: []string{"aiConversations"}}, ErrMetadataRecordQuery, func(data *domain.Bootstrap) error {
			data.AgentActivities = append(data.AgentActivities, domain.AgentActivity{ID: "new"})
			return nil
		}},
		{"read-only", MetadataAppendScope{MetadataRecordScope: MetadataRecordScope{Read: []RecordKey{{Kind: "aiConversations", ID: existing.ID}}}, AppendKinds: []string{"aiConversations"}}, ErrMetadataRecordQuery, func(data *domain.Bootstrap) error {
			data.AIConversations[0].Title = "Forbidden"
			return nil
		}},
		{"prepend", MetadataAppendScope{MetadataRecordScope: MetadataRecordScope{Read: []RecordKey{{Kind: "aiConversations", ID: existing.ID}}}, AppendKinds: []string{"aiConversations"}}, ErrMetadataRecordQuery, func(data *domain.Bootstrap) error {
			data.AIConversations = append([]domain.AIConversation{{ID: "new"}}, data.AIConversations...)
			return nil
		}},
		{"too-many", MetadataAppendScope{AppendKinds: []string{"aiConversations"}}, ErrMetadataPageBudget, func(data *domain.Bootstrap) error {
			for i := range 51 {
				data.AIConversations = append(data.AIConversations, domain.AIConversation{ID: fmt.Sprintf("new-%d", i)})
			}
			return nil
		}},
		{"large-output", MetadataAppendScope{AppendKinds: []string{"aiConversations"}}, ErrMetadataPageBudget, func(data *domain.Bootstrap) error {
			data.AIConversations = append(data.AIConversations, domain.AIConversation{ID: "new", Title: strings.Repeat("large ", 60000)})
			return nil
		}},
		{"callback-error", MetadataAppendScope{AppendKinds: []string{"aiConversations"}}, failure, func(data *domain.Bootstrap) error {
			data.AIConversations = append(data.AIConversations, domain.AIConversation{ID: "new"})
			return failure
		}},
		{"no-mutation", MetadataAppendScope{AppendKinds: []string{"aiConversations"}}, nil, func(data *domain.Bootstrap) error {
			data.AIConversations = append(data.AIConversations, domain.AIConversation{ID: "new"})
			return ErrNoMutation
		}},
	} {
		t.Run(spec.name, func(t *testing.T) {
			published := 0
			repo.SetRealtimeSink(func(string, domain.RealtimeEvent) { published++ })
			err := repo.MutateWorkspaceAppending(t.Context(), workspace, "ai.conversation_created", "new", nil, spec.scope, spec.run)
			if !errors.Is(err, spec.want) {
				t.Fatalf("error=%v want=%v", err, spec.want)
			}
			if !reflect.DeepEqual(before, scaleP21SW0AppendRows(t, repo, workspace)) || published != 0 || repo.metadataResidency.work.used != 0 {
				t.Fatal("failed append changed canonical state, published or leaked work")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	err = repo.MutateWorkspaceAppending(ctx, workspace, "ai.conversation_created", "new", nil, MetadataAppendScope{AppendKinds: []string{"aiConversations"}}, func(*domain.Bootstrap) error {
		called = true
		return nil
	})
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancelled append called=%v err=%v", called, err)
	}
}

func TestScaleP21SW0AppendingFirstShapeAndConcurrent(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "aiConversations"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.AIConversations = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	failures := make(chan error, 2)
	for _, repo := range []*SQLiteStore{a, b} {
		group.Add(1)
		go func() {
			defer group.Done()
			first, second := "z-"+idgen.New("append-"), "a-"+idgen.New("append-")
			failures <- repo.MutateWorkspaceAppending(t.Context(), workspace, "ai.conversation_created", first, nil, MetadataAppendScope{AppendKinds: []string{"aiConversations"}}, func(data *domain.Bootstrap) error {
				data.AIConversations = append(data.AIConversations, domain.AIConversation{ID: first}, domain.AIConversation{ID: second})
				return nil
			})
		}()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows := scaleP21SW0AppendRows(t, a, workspace)
	if len(rows) != 4 {
		t.Fatalf("concurrent append lost rows: %d", len(rows))
	}
	for i, row := range rows {
		if row.Order != i || i%2 == 0 && !strings.HasPrefix(row.ID, "z-") || i%2 == 1 && !strings.HasPrefix(row.ID, "a-") {
			t.Fatal("concurrent append order or transaction grouping changed")
		}
	}
	var raw []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	var shapes map[string]string
	if json.Unmarshal(raw, &root) != nil || json.Unmarshal(root[metadataCollectionsKey], &shapes) != nil || shapes["aiConversations"] != "array" {
		t.Fatal("first append lost canonical array shape")
	}
	if a.WorkspaceMetadataDecodes() != 0 || b.WorkspaceMetadataDecodes() != 0 || a.metadataResidency.work.used != 0 || b.metadataResidency.work.used != 0 {
		t.Fatal("concurrent append decoded complete metadata or leaked work")
	}
}

func TestScaleP21SW0AppendingLegacyDispatch(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "off")
	f := scaleP09Database(t)
	called := false
	err := f.MutateWorkspaceAppending(t.Context(), f.data.Workspace.URLKey, "application_policy.updated", "", nil, MetadataAppendScope{AppendKinds: []string{"invalid"}}, func(data *domain.Bootstrap) error {
		called = true
		if len(data.Teams) != len(f.data.Teams) {
			t.Fatal("off-mode callback input was narrowed")
		}
		return ErrNoMutation
	})
	if err != nil || !called {
		t.Fatalf("off-mode dispatch called=%v err=%v", called, err)
	}
}
