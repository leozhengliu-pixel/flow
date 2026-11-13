package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW22ApplicationWebhookCount(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	client := idgen.New("sw22-client-")
	second, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "SW22 second", idgen.New("sw22-second-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.DeleteWorkspace(context.Background(), second.Workspace.URLKey); err != nil {
			t.Error(err)
		}
	})
	for _, key := range []string{workspace, second.Workspace.URLKey} {
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "webhooks"), key, "webhook.updated", "", nil, func(data *domain.Bootstrap) error {
			data.Webhooks = []domain.Webhook{
				{ID: "sw22-match-1", ManagedBy: "application", ApplicationClientID: client, Name: strings.Repeat("legal large webhook name ", 16000)},
				{ID: "sw22-match-2", ManagedBy: "application", ApplicationClientID: client, Enabled: false},
				{ID: "sw22-other-manager", ManagedBy: "Application", ApplicationClientID: client},
				{ID: "sw22-other-case", ManagedBy: "application", ApplicationClientID: strings.ToUpper(client)},
				{ID: "sw22-unmanaged", ApplicationClientID: client},
				{ID: "sw22-empty-client", ManagedBy: "application"},
			}
			for index := range 75 {
				data.Webhooks = append(data.Webhooks, domain.Webhook{ID: fmt.Sprintf("sw22-unrelated-%d", index), ManagedBy: "application", ApplicationClientID: client + "-other"})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	hash := func() [32]byte {
		t.Helper()
		sum := sha256.New()
		for _, key := range []string{workspace, second.Workspace.URLKey} {
			if err := f.StreamMetadataRecords(t.Context(), key, "webhooks", func(record RecordKey, body io.Reader) error {
				_, _ = io.WriteString(sum, record.ID)
				_, err := io.Copy(sum, body)
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
		return [32]byte(sum.Sum(nil))
	}
	before := hash()
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []struct {
		client string
		want   int
	}{
		{client, 2}, {strings.ToUpper(client), 2}, {client + "-other", 2},
		{client + "-missing", 0}, {"", 2}, {client + "\x00", 0}, {client + "' OR 1=1 --", 0},
	} {
		legacy := 0
		for _, key := range f.WorkspaceKeys() {
			data, _ := f.WorkspaceMetadataFields(key, "webhooks")
			for _, hook := range data.Webhooks {
				if hook.ManagedBy == "application" && hook.ApplicationClientID == spec.client {
					legacy++
					break
				}
			}
		}
		for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
			count, err := source.ApplicationWebhookWorkspaceCount(t.Context(), spec.client)
			if err != nil || count != legacy || count != spec.want {
				t.Fatalf("count=%d legacy=%d want=%d err=%v", count, legacy, spec.want, err)
			}
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := view.ApplicationWebhookWorkspaceCount(cancelled, client); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled count: %v", err)
	}
	if count, err := view.ApplicationWebhookWorkspaceCount(t.Context(), client); err != nil || count != 2 {
		t.Fatal("count did not recover after cancellation", count, err)
	}
	if before != hash() || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("count changed canonical state, decoded full metadata or leaked admission")
	}
}

func TestScaleP21SW22ImportIssuesIdentityDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21_sw22.seed", "", nil, func(data *domain.Bootstrap) error {
		for index := range 70 {
			data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprintf("sw22-project-%d", index)})
		}
		data.Projects[len(data.Projects)-1].Description = strings.Repeat("unrelated project ", 20000)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	original := domain.Issue{
		ID: idgen.New("issue_sw22_"), Identifier: "SW22-1", Number: 1, Title: "Before", Version: 1,
		Team: f.data.Teams[0], Creator: f.data.Viewer, State: f.data.States[0], CreatedAt: at, UpdatedAt: at,
		Labels: []domain.IssueLabel{{ID: "sw22-label-original", Name: "Before"}},
	}
	next := original
	next.Title, next.Priority, next.Labels = "After", 2, []domain.IssueLabel{{ID: "sw22-label-next", Name: "After"}}
	snapshot := func() any {
		t.Helper()
		var detail, list []byte
		var version, order, total, number int64
		if err := f.db.QueryRowContext(t.Context(), `SELECT data,list_data,version,collection_order FROM issue_records WHERE workspace_key=? AND id=?`, workspace, original.ID).Scan(&detail, &list, &version, &order); err != nil {
			t.Fatal(err)
		}
		if err := f.db.QueryRowContext(t.Context(), `SELECT total FROM issue_collection_counts WHERE workspace_key=?`, workspace).Scan(&total); err != nil {
			t.Fatal(err)
		}
		if err := f.db.QueryRowContext(t.Context(), `SELECT last_number FROM issue_number_sequences WHERE workspace_key=?`, workspace).Scan(&number); err != nil {
			t.Fatal(err)
		}
		rows, err := f.db.QueryContext(t.Context(), `SELECT label_id FROM issue_label_records WHERE workspace_key=? AND issue_id=? ORDER BY label_id`, workspace, original.ID)
		if err != nil {
			t.Fatal(err)
		}
		labels := []string{}
		for rows.Next() {
			var label string
			if err := rows.Scan(&label); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			labels = append(labels, label)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		return []any{json.RawMessage(detail), json.RawMessage(list), version, order, total, number, labels}
	}
	var outcomes []any
	publishes := 0
	for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
		source.SetWebhookSink(func(string, domain.DomainEvent) { publishes++ })
		source.SetRealtimeSink(func(string, domain.RealtimeEvent) { publishes++ })
		if err := f.ImportIssues(t.Context(), workspace, []domain.Issue{original}); err != nil {
			t.Fatal(err)
		}
		if err := source.ImportIssues(t.Context(), workspace, []domain.Issue{next}); err != nil {
			t.Fatal(err)
		}
		after := snapshot()
		if err := source.ImportIssues(t.Context(), workspace, []domain.Issue{next}); err != nil || !reflect.DeepEqual(after, snapshot()) {
			t.Fatal("identical import changed canonical state", err)
		}
		outcomes = append(outcomes, after)
	}
	if !reflect.DeepEqual(outcomes[0], outcomes[1]) || publishes != 0 {
		t.Fatal("import final SQL/index/count/sequence state or publication behavior changed")
	}
	before, _ := json.Marshal(snapshot())
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := view.ImportIssues(cancelled, workspace, []domain.Issue{original}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled import: %v", err)
	}
	for _, invalid := range [][]domain.Issue{
		{next, next}, {{Title: "missing ID"}}, make([]domain.Issue, 1001),
		{{ID: "sw22-large", Description: strings.Repeat("large issue ", 100000)}},
	} {
		if err := view.ImportIssues(t.Context(), workspace, invalid); err == nil {
			t.Fatal("invalid or oversized import was accepted")
		}
	}
	if err := view.ImportIssues(t.Context(), workspace+"-missing", []domain.Issue{next}); err == nil || err.Error() != "workspace not found" {
		t.Fatalf("missing import workspace: %v", err)
	}
	after, _ := json.Marshal(snapshot())
	if !bytes.Equal(before, after) || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("failed import changed SQL state, decoded full metadata or leaked admission")
	}
}

func TestScaleP21SW22IssueSequenceMigrationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	issues := []domain.Issue{}
	for index := range 120 {
		issues = append(issues, domain.Issue{
			ID: idgen.New("issue_sw22_seq_"), Identifier: fmt.Sprintf("SW22SEQ-%d", index+1), Number: index + 1, Title: "Retained",
			Version: 1, Team: f.data.Teams[0], Creator: f.data.Viewer, State: f.data.States[0], CreatedAt: at, UpdatedAt: at,
		})
	}
	issues[len(issues)-1].Number = 5000
	if err := f.ImportIssues(t.Context(), workspace, issues); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Startup scans only catalog keys. Keep this test's catalogs isolated from
	// other packages sharing the persistent external database.
	f.mu.Lock()
	owned := f.workspaces[workspace]
	previous := f.workspaces
	f.workspaces = map[string]domain.Bootstrap{workspace: owned}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.workspaces = previous
		f.mu.Unlock()
	}()
	view.workspaces = map[string]domain.Bootstrap{workspace: view.workspaces[workspace]}
	for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
		if _, err := f.db.ExecContext(t.Context(), `DELETE FROM issue_number_sequences WHERE workspace_key=?`, workspace); err != nil {
			t.Fatal(err)
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if err := source.migrateIssueSequences(cancelled); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled sequence migration: %v", err)
		}
		if err := source.migrateIssueSequences(t.Context()); err != nil {
			t.Fatal(err)
		}
		var number int
		if err := f.db.QueryRowContext(t.Context(), `SELECT last_number FROM issue_number_sequences WHERE workspace_key=?`, workspace).Scan(&number); err != nil || number != 5000 {
			t.Fatalf("sequence=%d want=5000 err=%v", number, err)
		}
		if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_number_sequences SET last_number=7000 WHERE workspace_key=?`, workspace); err != nil {
			t.Fatal(err)
		}
		if err := source.migrateIssueSequences(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := f.db.QueryRowContext(t.Context(), `SELECT last_number FROM issue_number_sequences WHERE workspace_key=?`, workspace).Scan(&number); err != nil || number != 7000 {
			t.Fatalf("repeat migration overwrote existing sequence=%d err=%v", number, err)
		}
	}
	if view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("sequence migration decoded complete metadata")
	}
}
