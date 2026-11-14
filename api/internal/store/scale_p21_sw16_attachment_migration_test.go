package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW16AttachmentMigrationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw16-attachment-")
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	longURL := "https://Example.test/" + strings.Repeat("\u6c49\u5b57\U0001f680/quotes\"\\?x=1&y=2 ", 14000)
	urls := []string{
		"https://Example.test/Case", "https://example.test/case", "",
		"https://example.test/\u6c49\u5b57\U0001f680?x=quoted\"&y=slash\\",
		"https://example.test/literal-null", "null",
		"https://example.test/unicode/e\u0301", "https://example.test/unicode/\u00e9",
	}
	if f.dialect == "sqlite" {
		urls = append(urls, "before\x00after")
	}
	issues := make([]domain.Issue, 80)
	expected := map[string][]string{}
	for index := range issues {
		id := fmt.Sprintf("%s-%03d", prefix, index)
		issues[index] = domain.Issue{
			ID: id, Identifier: fmt.Sprintf("SW16-%d", index), Title: "Attachment index",
			Description: strings.Repeat("unrelated issue description ", 1000),
			Team:        domain.Team{ID: prefix + "-team", Key: "SW16"}, State: domain.WorkflowState{ID: prefix + "-state", Type: "started"},
			Creator: f.data.Viewer, CreatedAt: stamp, UpdatedAt: stamp,
		}
		for item, url := range []string{urls[index%len(urls)], "https://example.test/common", urls[index%len(urls)]} {
			issues[index].Attachments = append(issues[index].Attachments, domain.Attachment{
				ID: "repeated-attachment-id", Title: fmt.Sprintf("Item %d", item), URL: url,
				Subtitle: strings.Repeat("unused attachment subtitle ", 100), Creator: f.data.Viewer, CreatedAt: stamp,
			})
			// Canonical issue JSON removes NULs before storage; migration
			// indexes that stored URL, as the legacy decoder does.
			expected[id] = append(expected[id], attachmentKey(StripNUL(url)))
		}
		slices.Sort(expected[id])
		expected[id] = slices.Compact(expected[id])
	}
	issues[1].Description = strings.Repeat("legal large issue body ", 30000)
	issues[2].Attachments = nil
	delete(expected, issues[2].ID)
	issues[3].Attachments = []domain.Attachment{}
	delete(expected, issues[3].ID)
	for range 2 {
		issues[5].Attachments = append(issues[5].Attachments, domain.Attachment{ID: "duplicate-long-url", URL: longURL})
		expected[issues[5].ID] = append(expected[issues[5].ID], attachmentKey(longURL))
	}
	slices.Sort(expected[issues[5].ID])
	expected[issues[5].ID] = slices.Compact(expected[issues[5].ID])
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21_sw16.attachments_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Issues = issues
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A null attachment has the same zero-value URL as an empty object.
	var nullIssue map[string]json.RawMessage
	raw, err := json.Marshal(issues[4])
	if err != nil || json.Unmarshal(raw, &nullIssue) != nil {
		t.Fatal("prepare null attachment fixture", err)
	}
	nullIssue["attachments"] = json.RawMessage(`[null,{},{"url":null},{"url":"null"}]`)
	raw, err = json.Marshal(nullIssue)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, raw, workspace, issues[4].ID); err != nil {
		t.Fatal(err)
	}
	expected[issues[4].ID] = []string{attachmentKey(""), attachmentKey("null")}
	slices.Sort(expected[issues[4].ID])
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Startup must touch only this fixture on persistent shared databases.
	for _, repo := range []*SQLiteStore{f.SQLiteStore, view} {
		repo.mu.Lock()
		repo.workspaces = map[string]domain.Bootstrap{workspace: repo.workspaces[workspace]}
		repo.mu.Unlock()
	}
	canonical := func() [32]byte {
		t.Helper()
		rows, err := f.db.QueryContext(t.Context(), `SELECT id,version,collection_order,data FROM issue_records WHERE workspace_key=? ORDER BY id`, workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		hash := sha256.New()
		for rows.Next() {
			var id string
			var version, order int64
			var raw []byte
			if err := rows.Scan(&id, &version, &order, &raw); err != nil {
				t.Fatal(err)
			}
			_, _ = fmt.Fprintf(hash, "%s:%d:%d:", id, version, order)
			_, _ = hash.Write(raw)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return [32]byte(hash.Sum(nil))
	}
	original := canonical()
	readIndex := func() map[string][]string {
		t.Helper()
		rows, err := f.db.QueryContext(t.Context(), `SELECT issue_id,url_hash FROM issue_attachment_records WHERE workspace_key=? ORDER BY issue_id,url_hash`, workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		index := map[string][]string{}
		for rows.Next() {
			var id, hash string
			if err := rows.Scan(&id, &hash); err != nil {
				t.Fatal(err)
			}
			index[id] = append(index[id], hash)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return index
	}
	checkpoint := func() (string, int) {
		t.Helper()
		var last string
		var complete int
		if err := f.db.QueryRowContext(t.Context(), `SELECT last_id,complete FROM issue_attachment_migrations WHERE workspace_key=?`, workspace).Scan(&last, &complete); err != nil {
			t.Fatal(err)
		}
		return last, complete
	}
	reset := func(last string, stale bool) {
		t.Helper()
		if _, err := f.db.ExecContext(t.Context(), `DELETE FROM issue_attachment_records WHERE workspace_key=?`, workspace); err != nil {
			t.Fatal(err)
		}
		if stale {
			if _, err := f.db.ExecContext(t.Context(), `INSERT INTO issue_attachment_records(workspace_key,issue_id,url_hash) VALUES(?,?,?)`, workspace, issues[0].ID, attachmentKey("stale-url")); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO issue_attachment_migrations(workspace_key,last_id,complete) VALUES(?,?,0) ON CONFLICT(workspace_key) DO UPDATE SET last_id=excluded.last_id,complete=0`, workspace, last); err != nil {
			t.Fatal(err)
		}
	}
	events := 0
	view.SetRealtimeSink(func(string, domain.RealtimeEvent) { events++ })
	view.SetWebhookSink(func(string, domain.DomainEvent) { events++ })
	for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
		reset("", true)
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		statements, unsafeReads := 0, 0
		ctx := WithSQLStatementObserver(t.Context(), func(query string) {
			statements++
			if source.BoundedMetadataEnabled() && strings.Contains(query, "SELECT data FROM issue_records") {
				unsafeReads++
			}
		})
		if err := source.migrateAttachmentIndex(ctx); err != nil {
			t.Fatal(err)
		}
		runtime.ReadMemStats(&after)
		t.Logf("bounded=%t migration allocated-bytes=%d allocations=%d SQL=%d", source.BoundedMetadataEnabled(), after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs, statements)
		if unsafeReads != 0 || !reflect.DeepEqual(readIndex(), expected) {
			t.Fatalf("attachment index differs or hydrated issues: unsafe=%d", unsafeReads)
		}
		if last, complete := checkpoint(); last != issues[len(issues)-1].ID || complete != 1 {
			t.Fatalf("checkpoint %q/%d", last, complete)
		}
		if err := source.migrateAttachmentIndex(t.Context()); err != nil || !reflect.DeepEqual(readIndex(), expected) {
			t.Fatal("completed attachment migration was not idempotent", err)
		}
		if canonical() != original {
			t.Fatal("attachment migration changed canonical issue bytes/version/order")
		}
	}
	var saved []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`, workspace, issues[65].ID).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
		reset("", false)
		var bad map[string]json.RawMessage
		if err := json.Unmarshal(saved, &bad); err != nil {
			t.Fatal(err)
		}
		bad["attachments"] = json.RawMessage(`[{"url":7}]`)
		encoded, err := json.Marshal(bad)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, encoded, workspace, issues[65].ID); err != nil {
			t.Fatal(err)
		}
		err = source.migrateAttachmentIndex(t.Context())
		if err == nil {
			t.Fatal("invalid URL type was silently indexed")
		}
		firstBatch := map[string][]string{}
		for _, issue := range issues[:64] {
			if hashes, exists := expected[issue.ID]; exists {
				firstBatch[issue.ID] = hashes
			}
		}
		if last, complete := checkpoint(); last != issues[63].ID || complete != 0 || !reflect.DeepEqual(readIndex(), firstBatch) {
			t.Fatal("failed second batch lost the first checkpoint or partially wrote its index")
		}
		if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, saved, workspace, issues[65].ID); err != nil {
			t.Fatal(err)
		}
		if err := source.migrateAttachmentIndex(t.Context()); err != nil || !reflect.DeepEqual(readIndex(), expected) {
			t.Fatal("attachment migration did not resume after repairing the source", err)
		}
	}
	for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
		reset("", true)
		priorIndex := readIndex()
		limit := f.db.maxTransactionBytes
		f.db.maxTransactionBytes = 128
		err := source.migrateAttachmentIndex(t.Context())
		f.db.maxTransactionBytes = limit
		if !errors.Is(err, ErrTransactionWriteBudget) {
			t.Fatalf("attachment migration bypassed SQL write budget: %v", err)
		}
		if last, complete := checkpoint(); last != "" || complete != 0 || !reflect.DeepEqual(readIndex(), priorIndex) {
			t.Fatal("failed attachment batch changed its checkpoint or partially committed")
		}
		if err := source.migrateAttachmentIndex(t.Context()); err != nil || !reflect.DeepEqual(readIndex(), expected) {
			t.Fatal("attachment migration did not recover after write budget", err)
		}
	}
	reset(issues[63].ID, false)
	if err := view.migrateAttachmentIndex(t.Context()); err != nil {
		t.Fatal(err)
	}
	resumed := map[string][]string{}
	for _, issue := range issues[64:] {
		if hashes, exists := expected[issue.ID]; exists {
			resumed[issue.ID] = hashes
		}
	}
	if !reflect.DeepEqual(readIndex(), resumed) {
		t.Fatal("resumed attachment migration ignored checkpoint")
	}
	reset("", true)
	prior := readIndex()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := view.migrateAttachmentIndex(ctx); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(readIndex(), prior) {
		t.Fatalf("cancelled attachment migration changed index: %v", err)
	}
	if err := view.migrateAttachmentIndex(t.Context()); err != nil || !reflect.DeepEqual(readIndex(), expected) {
		t.Fatal("attachment migration did not recover after cancellation", err)
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 || events != 0 || canonical() != original {
		t.Fatal("attachment migration decoded metadata, leaked work, published or changed issues")
	}
}
