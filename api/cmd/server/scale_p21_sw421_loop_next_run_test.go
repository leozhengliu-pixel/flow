package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// scaleP21SW421Body is a legal multi-byte body with characters JSON escapes.
func scaleP21SW421Body(seed string, size int) string {
	unit := seed + " <body>&é世\t ; "
	text := strings.Repeat(unit, size/len(unit)+1)[:size]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

// scaleP21SW421Peak samples a store's admitted metadata work until the
// returned stop, which reports the largest sample.
func scaleP21SW421Peak(source *store.SQLiteStore) func() int {
	done, peak := make(chan struct{}), 0
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		ticker := time.NewTicker(200 * time.Microsecond)
		defer ticker.Stop()
		for {
			peak = max(peak, source.MetadataWorkUsedForTesting())
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	return func() int {
		close(done)
		wait.Wait()
		return peak
	}
}

// SW421: the schedule runner's loop.scheduled stamp (setLoopNextRun) with
// bounded metadata against the literal stamp, byte for byte (loop rows,
// search documents, the root, events, webhooks and realtime), warm and
// SQL-reloaded: small and large loops up to the literal writer's maximum,
// rows the literal writer refuses, named time zones, duplicates, a missing
// loop and peers that commit to the loop row before the stamp.
func TestScaleP21SW421LoopNextRunDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	config := scaleP03DatabaseConfig(t)
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP03Fixture(t, repo)
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	workspace := f.data.Workspace.URLKey
	probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	reader := &scaleP04Database{config: config}
	t.Cleanup(func() {
		if reader.reader != nil {
			_ = reader.reader.Close()
		}
	})
	prefix := idgen.New("sw421-")
	target, other := prefix+"target", prefix+"other"
	at := time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
	next := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	york, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw421_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Webhooks = append(data.Webhooks, domain.Webhook{ID: prefix + "hook", Enabled: true, URL: "https://example.test/sw421"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	loop := func(id string) domain.Loop {
		return domain.Loop{ID: id, Name: "Loop " + id, Status: "published", Enabled: true, Level: "workspace", TriggerType: "schedule",
			TriggerConfig: map[string]any{"unit": "day", "interval": 1, "time": "09:00", "timezone": "UTC"}, Instructions: "Reply.",
			InstructionsData: map[string]any{"type": "doc", "native": int64(9007199254740993)}, ConnectorIDs: []string{"c"},
			Creator: f.data.Viewer, OwnerID: f.data.Viewer.ID, CreatedAt: at, UpdatedAt: at, PublishedAt: &at}
	}
	instructions := func(size int) func(*domain.Bootstrap) {
		return func(data *domain.Bootstrap) {
			data.Loops[1].Instructions = scaleP21SW421Body("legal large loop instructions", size)
		}
	}
	earlier := next.Add(-24 * time.Hour)
	cases := []struct {
		name    string
		seed    func(*domain.Bootstrap)
		loopID  string
		next    time.Time
		peer    func(*domain.Loop)
		raw     int // instructions written to the canonical row directly
		refused bool
		missing bool
		sqlite  bool
	}{
		{name: "small", next: next},
		{name: "named-time-zone", next: time.Date(2026, 11, 18, 9, 0, 0, 0, york)},
		{name: "unchanged-next-run", next: next, seed: func(data *domain.Bootstrap) { data.Loops[1].NextRunAt = &next }},
		{name: "just-above-large-row", next: next, seed: instructions(300 << 10)},
		{name: "large-4mib", next: next, seed: instructions(4 << 20)},
		{name: "large-at-max", next: next, seed: instructions(15 << 20)},
		{name: "large-time-zone-earlier", next: earlier.In(york), seed: instructions(4 << 20)},
		{name: "duplicate-occurrences", next: next, seed: func(data *domain.Bootstrap) {
			second := loop(target)
			second.Name, second.Instructions = "Second occurrence", scaleP21SW421Body("second", 300<<10)
			data.Loops = append(data.Loops, second)
			data.Loops[1].Instructions = scaleP21SW421Body("first", 2<<20)
		}},
		{name: "missing-loop", next: next, loopID: prefix + "missing", missing: true},
		{name: "peer-renamed-large", next: next, seed: instructions(4 << 20), sqlite: true,
			peer: func(item *domain.Loop) { item.Name = "Peer renamed loop" }},
		{name: "peer-scheduled-conflict-large", next: next, seed: instructions(4 << 20), sqlite: true,
			peer: func(item *domain.Loop) { item.NextRunAt = &earlier }},
		{name: "peer-scheduled-conflict-small", next: next, sqlite: true,
			peer: func(item *domain.Loop) { item.NextRunAt = &earlier }},
		{name: "over-literal-maximum", next: next, raw: 24 << 20, refused: true, sqlite: true},
	}
	query := func(t *testing.T, statement string, args ...any) [][]any {
		t.Helper()
		if reader.reader == nil {
			_ = reader.query(t, `SELECT 1 FROM workspace_states WHERE workspace_key=?`, workspace).Close()
		}
		if config.Driver == "postgres" || config.Driver == "postgresql" {
			statement = scaleDB{driver: "postgres"}.rebind(statement)
		}
		rows, err := reader.reader.QueryContext(context.Background(), statement, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var result [][]any
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			for index, value := range values {
				if raw, ok := value.([]byte); ok {
					values[index] = string(raw)
				}
			}
			result = append(result, values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	// rewrite replaces the canonical loop row's JSON as a writer in another
	// process would, after both copies were installed.
	rewrite := func(t *testing.T, edit func(*domain.Loop)) {
		t.Helper()
		rows := query(t, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='loops' AND record_key=?`, workspace, target)
		if len(rows) != 1 {
			t.Fatal("canonical loop row missing")
		}
		var item domain.Loop
		if err := json.Unmarshal([]byte(rows[0][0].(string)), &item); err != nil {
			t.Fatal(err)
		}
		edit(&item)
		raw, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reader.reader.ExecContext(context.Background(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='loops' AND record_key=?`, raw, workspace, target); err != nil {
			t.Fatal(err)
		}
	}
	for _, spec := range cases {
		for _, reload := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reload=%t", spec.name, reload), func(t *testing.T) {
				if spec.sqlite && config.Driver != "sqlite" {
					t.Skip("canonical row rewrites use SQLite placeholders; the other cases cover the configured database")
				}
				var want string
				for _, source := range []*store.SQLiteStore{repo, probe} {
					bounded := source.BoundedMetadataEnabled()
					if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "loops", "loopVersions"), workspace, "scale_p21.sw421_reset", "", nil, func(data *domain.Bootstrap) error {
						data.Loops = []domain.Loop{loop(other), loop(target)}
						data.LoopVersions = nil
						if spec.seed != nil {
							spec.seed(data)
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if spec.raw > 0 {
						rewrite(t, func(item *domain.Loop) { item.Instructions = scaleP21SW421Body("over the literal maximum", spec.raw) })
					}
					if reload || spec.raw > 0 {
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
					}
					if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
					if spec.peer != nil {
						rewrite(t, spec.peer)
					}
					prior := map[any]bool{}
					for _, event := range query(t, `SELECT id FROM domain_events WHERE workspace_id=?`, f.data.Workspace.ID) {
						prior[event[0]] = true
					}
					var hooks []domain.DomainEvent
					var live []domain.RealtimeEvent
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) { hooks = append(hooks, event) })
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { live = append(live, event) })
					decodes := source.WorkspaceMetadataDecodes()
					// setLoopNextRun logs its error; the store call returns it.
					service := &server{store: source}
					loopID := firstNonEmpty(spec.loopID, target)
					stamp := func(data *domain.Bootstrap) error {
						item := loopByID(data, loopID)
						if item == nil {
							return errNotFound
						}
						item.NextRunAt = &spec.next
						return nil
					}
					var stampErr error
					stopPeak := scaleP21SW421Peak(source)
					switch {
					case !spec.refused && !spec.missing && spec.peer == nil:
						// The server entry point (it logs its error).
						service.setLoopNextRun(workspace, loopID, spec.next)
					case bounded:
						stampErr = source.MutateLoopNextRun(context.Background(), workspace, loopID, stamp)
					default:
						stampErr = source.MutateWorkspace(store.WithMetadataFields(context.Background(), "loops"), workspace, "loop.scheduled", loopID, nil, stamp)
					}
					peak := stopPeak()
					t.Logf("%s reload=%t bounded=%t err=%v peak_work=%d", spec.name, reload, bounded, stampErr, peak)
					source.SetWebhookSink(nil)
					source.SetRealtimeSink(nil)
					var events [][]any
					for _, event := range query(t, `SELECT event_type,aggregate_id,payload,previous_values,id FROM domain_events WHERE workspace_id=? ORDER BY created_at,id`, f.data.Workspace.ID) {
						if !prior[event[4]] {
							events = append(events, event[:4])
						}
					}
					var published []any
					for _, event := range hooks {
						published = append(published, []any{event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues, event.Snapshot})
					}
					for _, event := range live {
						published = append(published, []any{event.Type, event.AggregateID, event.ActorID, event.ClientID, event.Payload})
					}
					errText := ""
					if stampErr != nil {
						errText = stampErr.Error()
					}
					encoded, err := json.Marshal(map[string]any{
						"error":  errText,
						"rows":   query(t, `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('loops','loopVersions') ORDER BY field,collection_order,record_key`, workspace),
						"search": query(t, `SELECT field,record_key,content FROM metadata_search_documents WHERE workspace_key=? AND field='loops' ORDER BY field,record_key`, workspace),
						"root":   query(t, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace),
						"events": events, "published": published,
					})
					if err != nil {
						t.Fatal(err)
					}
					got := scaleP21TriageClock.ReplaceAllStringFunc(string(encoded), func(clock string) string {
						if parsed, err := time.Parse(time.RFC3339Nano, clock); err == nil && parsed.After(at.Add(48*time.Hour)) && !parsed.Equal(next) && !parsed.Equal(spec.next) {
							return "<clock>"
						}
						return clock
					})
					switch {
					case spec.refused && !errors.Is(stampErr, store.ErrTransactionWriteBudget):
						t.Fatalf("bounded=%t over-limit stamp err=%v, want the transaction write budget", bounded, stampErr)
					case spec.missing && !errors.Is(stampErr, errNotFound):
						t.Fatalf("bounded=%t missing loop err=%v", bounded, stampErr)
					case !spec.refused && !spec.missing && stampErr != nil:
						t.Fatalf("bounded=%t stamp failed: %v", bounded, stampErr)
					}
					if !bounded {
						if !spec.refused && !spec.missing && spec.name != "unchanged-next-run" && len(events) != 1 {
							t.Fatalf("literal stamp wrote %d events", len(events))
						}
						want = got
						continue
					}
					if used := source.MetadataWorkUsedForTesting(); used != 0 {
						t.Fatalf("bounded stamp retained %d work bytes", used)
					}
					if source.WorkspaceMetadataDecodes() != decodes {
						t.Fatal("bounded stamp decoded complete workspace metadata")
					}
					if got != want {
						t.Fatalf("bounded loop.scheduled stamp differs from the literal stamp\n%s", scaleP21SW260FirstDiff(want, got))
					}
				}
			})
		}
	}
}
