//go:build loadtest

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

var scaleP21SW276Clock = regexp.MustCompile(`\d{4}-\d\d-\d\dT[\d:.]+Z`)

type scaleP21SW276Outcome struct {
	records, content, events, realtime, tables []string
	root, rawRoot                              string
	sizes                                      map[string]int
}

func scaleP21SW276Query(t *testing.T, repo *SQLiteStore, query string, args ...any) []string {
	t.Helper()
	rows, err := repo.db.QueryContext(t.Context(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, _ := rows.Columns()
	result := []string{}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(values))
		for i, value := range values {
			if raw, ok := value.([]byte); ok {
				value = string(raw)
			}
			parts[i] = fmt.Sprint(value)
		}
		result = append(result, strings.Join(parts, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21SW276Fixture(t *testing.T) (*SQLiteStore, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flow.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	workspace := "test-workspace"
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	// Existing records: the seed appends after them (trash after a
	// prepended entry), and an unrelated conversation stays untouched.
	if err := repo.MutateWorkspace(WithMetadataFields(t.Context(), "trash", "aiConversations"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Trash = append(data.Trash, domain.TrashEntry{ID: "sw276-trash-old", ResourceType: "document", ResourceID: "sw276-old", Title: "Old", Payload: json.RawMessage(`{"id":"sw276-old"}`), DeletedAt: at, ExpiresAt: at})
		data.AIConversations = append(data.AIConversations, domain.AIConversation{ID: "sw276-conv-old", UserID: data.Viewer.ID, Title: "Old", CreatedAt: at, UpdatedAt: at})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return repo, workspace, path
}

func scaleP21SW276Capture(t *testing.T, owner *SQLiteStore, workspace string, realtime []string) scaleP21SW276Outcome {
	t.Helper()
	result := scaleP21SW276Outcome{realtime: realtime}
	result.records = scaleP21SW276Query(t, owner, `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('projects','trash','documents','aiConversations') AND (record_key LIKE 'lt-%' OR record_key LIKE 'sw276-%') ORDER BY field,collection_order,record_key`, workspace)
	result.content = scaleP21SW276Query(t, owner, `SELECT kind,resource_id,id,record_version,data FROM workspace_content_records WHERE workspace_key=? AND id LIKE 'lt-%' ORDER BY kind,resource_id,id`, workspace)
	// The fixture's own rows carry its opening clock; only their count is compared.
	result.content = append(result.content, scaleP21SW276Query(t, owner, `SELECT kind,COUNT(*) FROM workspace_content_records WHERE workspace_key=? GROUP BY kind ORDER BY kind`, workspace)...)
	result.content = append(result.content, scaleP21SW276Query(t, owner, `SELECT kind,SUM(revision) FROM workspace_cache_revisions WHERE workspace_key=? GROUP BY kind ORDER BY kind`, workspace)...)
	result.content = append(result.content, scaleP21SW276Query(t, owner, `SELECT content FROM metadata_search_documents ORDER BY content`)...)
	result.events = scaleP21SW276Query(t, owner, `SELECT event_type,aggregate_id,payload,COALESCE(previous_values,''),COALESCE(actor_id,'') FROM domain_events WHERE event_type='loadtest.seeded' ORDER BY created_at,rowid`)
	if roots := scaleP21SW276Query(t, owner, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace); len(roots) == 1 {
		result.rawRoot = roots[0]
		result.root = scaleP21SW276Clock.ReplaceAllString(roots[0], "T")
	}
	for _, table := range scaleP21SW276Query(t, owner, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`) {
		var count int
		// FTS segment rows and random cache-revision shards are storage
		// layout; their content and per-kind sums are compared above.
		if strings.Contains(table, "_fts_") || table == "workspace_cache_revisions" {
			continue
		}
		if err := owner.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		result.tables = append(result.tables, fmt.Sprintf("%s=%d", table, count))
	}
	return result
}

// TestScaleP21SW276SyntheticSeedDifferential seeds the same synthetic
// workspace through a legacy store and through a bounded store whose resident
// copy is policy-only, with several batches per kind, existing records in the
// appended collections and notifications for several users. Stored records
// (keys, order, bytes), content records, seed events, realtime publications,
// the workspace root, every table's row count and the reported state sizes
// (warm and after a SQL reload) are identical; the bounded seed and measure
// decode no workspace and release their work.
func TestScaleP21SW276SyntheticSeedDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	clock, batch := syntheticSeedClock, syntheticSeedBatchBytes
	t.Cleanup(func() { syntheticSeedClock, syntheticSeedBatchBytes = clock, batch })
	stamp := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	syntheticSeedClock = func() time.Time { return stamp }
	syntheticSeedBatchBytes = 3000
	var outcomes []scaleP21SW276Outcome
	for _, bounded := range []bool{false, true} {
		repo, workspace, path := scaleP21SW276Fixture(t)
		data, _ := repo.storedWorkspace(workspace)
		users := []string{}
		for _, user := range data.Users {
			users = append(users, user.ID)
		}
		spec := SyntheticMetadata{
			Projects: 5, ProjectBytes: 1000, Trash: 4, TrashBytes: 1000, Documents: 3, DocumentBytes: 1500,
			Conversations: 5, ConversationBytes: 700, Notifications: 7, TeamIDs: []string{data.Teams[0].ID}, UserIDs: users, Relations: true,
		}
		target := repo
		if bounded {
			view, err := repo.BoundedMetadataViewForTesting(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			empty, err := view.WorkspaceStateBytes(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			legacyEmpty, err := repo.WorkspaceStateBytes(t.Context(), workspace)
			if err != nil || !reflect.DeepEqual(empty, legacyEmpty) {
				t.Fatalf("unseeded sizes legacy=%v bounded=%v err=%v", legacyEmpty, empty, err)
			}
			for _, store := range []*SQLiteStore{repo, view} {
				if _, err := store.WorkspaceStateBytes(t.Context(), "sw276-missing"); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("missing workspace: %v", err)
				}
			}
			target = view
		}
		var realtime []string
		// Notification signals publish through the database's owner store.
		sink := func(_ string, event domain.RealtimeEvent) {
			realtime = append(realtime, event.Type+"|"+event.AggregateID+"|"+string(event.Payload))
		}
		repo.SetRealtimeSink(sink)
		target.SetRealtimeSink(sink)
		if err := target.SeedSyntheticMetadata(t.Context(), workspace, spec); err != nil {
			t.Fatalf("bounded=%t: %v", bounded, err)
		}
		target.SetRealtimeSink(nil)
		repo.SetRealtimeSink(nil)
		outcome := scaleP21SW276Capture(t, repo, workspace, realtime)
		measured := target
		if !bounded {
			// As the load harnesses do, measure on a peer that loads the
			// seeded workspace from SQL (the writer's warm copy lacks the
			// load-time normalisation, e.g. document owner permissions).
			// It opens after the capture: startup reconciles search rows.
			peer, err := OpenSQLiteTestFixture(path)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			measured = peer
		}
		var err error
		if outcome.sizes, err = measured.WorkspaceStateBytes(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if outcome.sizes["stateRow"] != len(outcome.rawRoot) {
			t.Fatalf("bounded=%t stateRow=%d root=%d", bounded, outcome.sizes["stateRow"], len(outcome.rawRoot))
		}
		// The root embeds the fixture's opening clock, whose formatted
		// length varies; it is compared normalised above.
		delete(outcome.sizes, "stateRow")
		if bounded {
			if target.WorkspaceMetadataDecodes() != 0 || target.metadataResidency.work.used != 0 {
				t.Fatal("bounded seed decoded the workspace or leaked work")
			}
			// A second view starts from the owner's copy: bring the owner to
			// the committed rows first, then compare a fresh logical install.
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			reloaded, err := repo.BoundedMetadataViewForTesting(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer reloaded.releaseMetadataLogicalSources()
			sizes, err := reloaded.WorkspaceStateBytes(t.Context(), workspace)
			delete(sizes, "stateRow")
			if err != nil || !reflect.DeepEqual(sizes, outcome.sizes) {
				t.Fatalf("SQL-reloaded sizes %v != warm %v (%v)", sizes, outcome.sizes, err)
			}
		}
		outcomes = append(outcomes, outcome)
	}
	legacy, bounded := outcomes[0], outcomes[1]
	for name, pair := range map[string][2][]string{"records": {legacy.records, bounded.records}, "content": {legacy.content, bounded.content}, "events": {legacy.events, bounded.events}, "realtime": {legacy.realtime, bounded.realtime}, "tables": {legacy.tables, bounded.tables}} {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Errorf("%s differ:\nlegacy=%q\nbounded=%q", name, pair[0], pair[1])
		}
	}
	if legacy.root != bounded.root || !reflect.DeepEqual(legacy.sizes, bounded.sizes) {
		t.Errorf("root or sizes differ:\n%s\n%s\n%v\n%v", legacy.root, bounded.root, legacy.sizes, bounded.sizes)
	}
	if len(legacy.events) != 2+2+2+2+3 || len(legacy.records) != 5+5+3+6 || legacy.sizes["projects"] == 0 {
		t.Fatalf("fixture shape changed: events=%d records=%d sizes=%v", len(legacy.events), len(legacy.records), legacy.sizes)
	}
}
