package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// sw460Reassign reassigns the SW440 document's issue team through writer:
// the canonical store (this legacy server, or another server's write that a
// bounded view has not installed), or a second legacy server.
func (f scaleP21IssueUpdateFixture) sw460Reassign(t *testing.T, writer *store.SQLiteStore, team string) {
	t.Helper()
	at := time.Date(2026, 1, 4, 5, 6, 7, 0, time.UTC)
	if err := writer.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw441_conflict", "", nil, func(data *domain.Bootstrap) error {
		for index := range data.Documents {
			if document := &data.Documents[index]; document.ID == "sw440-doc" {
				document.IssueTeamID, document.UpdatedAt = team, at
				return nil
			}
		}
		return errors.New("conflict document missing")
	}); err != nil {
		t.Fatal(err)
	}
}

// sw460Run is scaleP21IssueUpdateFixture.run with another server's write
// between the reset (which installs the bounded views' images) and the call,
// before any publication is captured. With fresh, the installed image must
// equal the SQL reload.
func (f scaleP21IssueUpdateFixture) sw460Run(t *testing.T, source string, peer func(*testing.T), call func(*server) *httptest.ResponseRecorder, fresh bool) (scaleP21IssueUpdateOutcome, time.Time) {
	t.Helper()
	started := time.Now().UTC()
	service := f.reset(t, source)
	peer(t)
	got := scaleP21IssueUpdateOutcome{}
	var mu sync.Mutex
	service.store.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		mu.Lock()
		defer mu.Unlock()
		got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot,
		}))
	})
	realtime := func(_ string, event domain.RealtimeEvent) {
		mu.Lock()
		defer mu.Unlock()
		got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"clientId": event.ClientID, "payload": event.Payload,
		}))
	}
	service.store.SetRealtimeSink(realtime)
	f.repository.SetRealtimeSink(realtime)
	prior := map[any]bool{}
	for _, event := range f.events(t) {
		prior[event[0]] = true
	}
	if w := call(service); w != nil {
		got.Code, got.Body = w.Code, json.RawMessage(w.Body.String())
	}
	service.store.SetWebhookSink(nil)
	service.store.SetRealtimeSink(nil)
	f.repository.SetRealtimeSink(nil)
	got.State = f.state(t)
	var events [][]any
	for _, event := range got.State.Events {
		if !prior[event[0]] {
			events = append(events, event[1:])
		}
	}
	got.State.Events = events
	if service.store.BoundedMetadataEnabled() && (service.store.WorkspaceMetadataDecodes() != 0 || service.store.MetadataWorkUsedForTesting() != 0) {
		t.Fatalf("%s: bounded generation decoded full metadata (%d) or retained work (%d)", source,
			service.store.WorkspaceMetadataDecodes(), service.store.MetadataWorkUsedForTesting())
	}
	got.Installed = f.installed(t, service.store)
	got.Reloaded = f.reloaded(t)
	nul := strings.NewReplacer(`\u0000`, "")
	if installed, reloaded := nul.Replace(string(got.Installed)), nul.Replace(string(got.Reloaded)); fresh && installed != reloaded {
		t.Fatalf("%s: installed image differs from the SQL reload: %s", source, scaleP21M2Diff(installed, reloaded))
	}
	return got, started
}

// sw460Differential runs sources in order and requires each to equal the
// first byte for byte (stamps and clocks of the call normalized).
func (f scaleP21IssueUpdateFixture) sw460Differential(t *testing.T, sources []string, peer func(*testing.T), call func(*server) *httptest.ResponseRecorder, fresh bool) []scaleP21IssueUpdateOutcome {
	t.Helper()
	return scaleP21SW460Compare(t, sources, fresh, func(source string) (scaleP21IssueUpdateOutcome, time.Time) {
		return f.sw460Run(t, source, peer, call, fresh)
	})
}

func scaleP21SW460Compare(t *testing.T, sources []string, fresh bool, run func(string) (scaleP21IssueUpdateOutcome, time.Time)) []scaleP21IssueUpdateOutcome {
	t.Helper()
	var normalized []string
	var outcomes []scaleP21IssueUpdateOutcome
	for _, source := range sources {
		got, started := run(source)
		outcomes = append(outcomes, got)
		compared := got
		if fresh {
			// Every source's image equals the SQL reload up to NUL (sw460Run).
			// A bounded writer that lost the race to the other server
			// installs that reload (installedGenerationBehind), whose rows
			// had NUL stripped when stored; a legacy server keeps its copy.
			compared.Installed = json.RawMessage(strings.ReplaceAll(string(got.Installed), `\u0000`, ""))
		}
		normalized = append(normalized, scaleP21IssueUpdateNormalize(t, compared, started))
		if len(normalized) > 1 && normalized[len(normalized)-1] != normalized[0] {
			t.Fatalf("%s changed: %s", source, scaleP21M2Diff(normalized[0], normalized[len(normalized)-1]))
		}
	}
	return outcomes
}

// Another server reassigns the team of a document attached to an issue the
// generation does not touch, after this server installed its image (the SW441
// interleaving recorded on 2026-11-17). No writer refreshes a record it does
// not change: a legacy server that has not received that server's peer event
// yet (mutateIssueScope on its copy) and the bounded writer on its installed
// image commit the same rows and keep the document in their image as it was
// before the other write. The bounded warm and SQL-reloaded views equal that
// legacy server byte for byte (response, rows, events, webhooks, realtime and
// the installed documents and suggestions), and their images differ from the
// SQL reload only by that document.
func TestScaleP21SW460TriageUntouchedPeerWriteMatchesLegacyServer(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f, ids := scaleP21SW440Setup(t, 100)
	f.scaleP21TriageWriterFixture.prepare = f.prepare(scaleP21IssueUpdateCase{issue: f.triage.ID})
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	peer := func(t *testing.T) {
		other, err := store.OpenDatabase(f.reader.config)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		f.sw460Reassign(t, other, "sw440-plain")
	}
	outcomes := f.sw460Differential(t, []string{"legacy", "warm", "reloaded"}, peer, f.sw440Generate(t, ids), false)
	legacy := outcomes[0]
	if legacy.Code != 200 || len(legacy.State.Events) != 1 {
		t.Fatalf("legacy status %d with %d events: %s", legacy.Code, len(legacy.State.Events), legacy.Body)
	}
	// The SQL rows strip NUL (writeMetadataRecordRow); images keep it.
	nul := strings.NewReplacer(`\u0000`, "")
	image, reload := nul.Replace(string(legacy.Installed)), nul.Replace(string(legacy.Reloaded))
	stale := `"issueId":"sw400-bulk-0000","issueTeamId":"sw400-team"`
	fresh := `"issueId":"sw400-bulk-0000","issueTeamId":"sw440-plain"`
	if !strings.Contains(image, stale) || !strings.Contains(reload, fresh) {
		t.Fatal("the legacy server's copy is not behind the other server's document write")
	}
	image = strings.Replace(image, stale, fresh, 1)
	image = strings.Replace(image, `"updatedAt":"2026-01-03T04:05:06Z","revisions"`, `"updatedAt":"2026-01-04T05:06:07Z","revisions"`, 1)
	if image != reload {
		t.Fatalf("the image differs from the SQL reload beyond the other server's document write: %s", scaleP21M2Diff(reload, image))
	}
}

// scaleP21SW461Existing is the number of other issues' suggestions the SW461
// workspace holds; a measurement run sets FLOW_SW461_EXISTING.
func scaleP21SW461Existing(t *testing.T) int {
	if raw := os.Getenv("FLOW_SW461_EXISTING"); raw != "" {
		count, err := strconv.Atoi(raw)
		if err != nil || count < 0 {
			t.Fatalf("FLOW_SW461_EXISTING=%q", raw)
		}
		return count
	}
	return 20000
}

// scaleP21SW461Suggestions adds existing suggestions to the SW440 baseline: a
// dismissed suggestion of every tenth scoped issue first (generation moves it
// behind the regenerated ones), then existing suggestions of two issues outside
// the scope, active and dismissed, of the size real generations store.
func scaleP21SW461Suggestions(data *domain.Bootstrap, ids []string, existing int) ([]domain.IssueSuggestion, error) {
	at := time.Date(2026, 1, 5, 6, 7, 8, 0, time.UTC)
	scoped := map[string]bool{}
	suggestions := make([]domain.IssueSuggestion, 0, (len(ids)+9)/10+existing+len(data.IssueSuggestions))
	for index, id := range ids {
		scoped[id] = true
		if index%10 == 0 {
			suggestions = append(suggestions, domain.IssueSuggestion{ID: fmt.Sprintf("sw461-dismissed-%04d", index), IssueID: id, Type: "label", State: "dismissed",
				SuggestedLabelID: "sw461-label", StateChangedAt: at, CreatedAt: at, UpdatedAt: at,
				Metadata: map[string]any{"rank": 1, "reasons": []string{"Dismissed earlier"}, "score": 0.5}})
		}
	}
	var others []string
	for _, issue := range data.Issues {
		if !scoped[issue.ID] && len(others) < 2 {
			others = append(others, issue.ID)
		}
	}
	if len(others) != 2 {
		return nil, fmt.Errorf("sw461 needs two issues outside the scope, found %v", others)
	}
	for index := range existing {
		state := "active"
		if index%4 == 3 {
			state = "dismissed"
		}
		suggestions = append(suggestions, domain.IssueSuggestion{ID: fmt.Sprintf("sw461-existing-%06d", index), IssueID: others[index%2], Type: "relatedIssue", State: state,
			SuggestedIssueID: fmt.Sprintf("sw400-bulk-%04d", index%len(ids)), StateChangedAt: at, CreatedAt: at, UpdatedAt: at,
			Metadata: map[string]any{"rank": index%5 + 1, "reasons": []string{"Similar title", fmt.Sprintf("Shares %d words", index%7+2)}, "score": 0.25 + float64(index%50)/100}})
	}
	return append(suggestions, scaleP21SW461Unique(data.IssueSuggestions)...), nil
}

// Repeated IDs keep the field inline. Keep the first occurrence so the
// seeded list is one canonical row per suggestion.
func scaleP21SW461Unique(list []domain.IssueSuggestion) []domain.IssueSuggestion {
	seen := map[string]bool{}
	unique := make([]domain.IssueSuggestion, 0, len(list))
	for _, item := range list {
		if !seen[item.ID] {
			seen[item.ID] = true
			unique = append(unique, item)
		}
	}
	return unique
}

func scaleP21SW461Seed(t *testing.T, f *scaleP21IssueUpdateFixture, ids []string, existing int) {
	t.Helper()
	ctx, workspace := t.Context(), f.data.Workspace.URLKey
	if err := f.repository.MutateWorkspace(ctx, workspace, "scale_p21.sw461_suggestions", "", nil, func(data *domain.Bootstrap) error {
		data.IssueSuggestions = scaleP21SW461Unique(data.IssueSuggestions)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	full, ok := f.repository.BootstrapFor(workspace)
	if !ok {
		t.Fatal("workspace missing")
	}
	suggestions, err := scaleP21SW461Suggestions(&full, ids, existing)
	if err != nil {
		t.Fatal(err)
	}

	// Seed the exact order cheaply. The requests still have to persist and
	// publish the full reorder through the production writers.
	f.reader.query(t, `SELECT 1 FROM workspace_states WHERE workspace_key=?`, workspace).Close()
	dialect := f.reader.config.Driver
	if dialect == "postgresql" {
		dialect = "postgres"
	}
	db := scaleDB{DB: f.reader.reader, driver: dialect}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, db.rebind(`DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='issueSuggestions'`), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != int64(len(full.IssueSuggestions)) {
		t.Fatalf("suggestion seed is not record-backed: deleted %d of %d rows: %v", count, len(full.IssueSuggestions), err)
	}
	const batchSize = 200
	for start := 0; start < len(suggestions); start += batchSize {
		batch := suggestions[start:min(start+batchSize, len(suggestions))]
		args := make([]any, 0, 4*len(batch))
		for index, item := range batch {
			raw, err := json.Marshal(item)
			if err != nil {
				t.Fatal(err)
			}
			args = append(args, workspace, item.ID, start+index, store.StripJSONNUL(raw))
		}
		query := `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES ` +
			strings.Repeat(`(?,'issueSuggestions',?,?,?),`, len(batch)-1) + `(?,'issueSuggestions',?,?,?)`
		if _, err := tx.ExecContext(ctx, db.rebind(query), args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := f.repository.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
		t.Fatal(err)
	}
	if err := f.repository.ReloadWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	full, ok = f.repository.BootstrapFor(workspace)
	if !ok || len(full.IssueSuggestions) != len(suggestions) {
		t.Fatalf("suggestion seed reloaded %d of %d rows", len(full.IssueSuggestions), len(suggestions))
	}
}

// sw461Copy prepares an independent SQLite preimage for the bounded run.
// Resetting a database after the legacy request would renumber 20,000 rows
// a second time just to restore the starting order.
func (f scaleP21IssueUpdateFixture) sw461Copy(t *testing.T) scaleP21IssueUpdateFixture {
	t.Helper()
	writer := *f.scaleP21TriageWriterFixture
	f.scaleP21TriageWriterFixture = &writer
	config := writer.reader.config
	config.Path = teamCascadeCopy(t, config.Path)
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := repo.Close(); err != nil {
			t.Errorf("close SW461 fixture copy: %v", err)
		}
	})
	f.reader = &scaleP04Database{config: config}
	t.Cleanup(func() {
		if f.reader.reader != nil {
			if err := f.reader.reader.Close(); err != nil {
				t.Errorf("close SW461 fixture reader: %v", err)
			}
		}
	})
	f.repository = repo
	f.probe, err = repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	f.reloadOracle = nil
	f.scaleP21TriageWriterFixture.prepare = f.resetTriageSideTables
	return f
}

// A workspace with many existing suggestions: triage generation over one
// issue and over 1000 issues moves the scoped issues' dismissed suggestions
// behind their regenerated ones, so legacy renumbers the whole suggestion
// list. From an empty 256 MB work budget the bounded writer equals legacy
// byte for byte (warm and SQL-reloaded: response, every suggestion row with
// its collection order, issue, history and document rows, events, sinks and
// the installed image). Each bounded source has its own legacy oracle so
// the CI planner can shard the comparisons. With FLOW_SW461_MEASURE=1 only
// the bounded source runs, logging its peak work under an unlimited budget. With
// FLOW_SW461_MEASURE=budget its status under the 256 MB budget.
func TestScaleP21SW461TriageSuggestionListReorderDifferential(t *testing.T) {
	scaleP21SW461Reorder(t, false, "warm")
}

func TestScaleP21SW461TriageSuggestionListReorderReloadedDifferential(t *testing.T) {
	scaleP21SW461Reorder(t, false, "reloaded")
}

func TestScaleP21SW461TriageSuggestionListReorderBulkDifferential(t *testing.T) {
	scaleP21SW461Reorder(t, true, "warm")
}

func TestScaleP21SW461TriageSuggestionListReorderBulkReloadedDifferential(t *testing.T) {
	scaleP21SW461Reorder(t, true, "reloaded")
}

func scaleP21SW461Reorder(t *testing.T, bulk bool, source string) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f, ids := scaleP21SW440Setup(t, scaleP21SW440Size(t))
	existing := scaleP21SW461Existing(t)
	measure := os.Getenv("FLOW_SW461_MEASURE") != ""
	scope := ids[:1]
	if bulk {
		scope = ids
	}
	t.Run(fmt.Sprintf("scope-%d", len(scope)), func(t *testing.T) {
		began := time.Now()
		scaleP21SW461Seed(t, &f, ids, existing)
		t.Logf("suggestion seed took %s", time.Since(began))
		f.prepareTriageReset(t, f.prepare(scaleP21IssueUpdateCase{issue: f.triage.ID}))
		generate := f.sw440Generate(t, scope)
		peak := 0
		call := func(service *server) *httptest.ResponseRecorder {
			began := time.Now()
			defer func() {
				t.Logf("bounded=%t generation took %s", service.store.BoundedMetadataEnabled(), time.Since(began))
			}()
			if !service.store.BoundedMetadataEnabled() {
				return generate(service)
			}
			if measure && os.Getenv("FLOW_SW461_MEASURE") != "budget" {
				restore := service.store.SetMetadataWorkLimitForTesting(1 << 40)
				defer restore()
			}
			service.store.MetadataWorkPeakForTesting()
			w := generate(service)
			peak = max(peak, service.store.MetadataWorkPeakForTesting())
			return w
		}
		noPeer := func(*testing.T) {}
		if measure {
			got, _ := f.sw460Run(t, source, noPeer, call, true)
			t.Logf("%d existing suggestions, %d scoped issues: status %d, bounded peak work %.1f MB", existing, len(scope), got.Code, float64(peak)/(1<<20))
			return
		}
		bounded := f
		// Only copy the per-test SQLite file. A configured database can
		// contain other workspaces, so restore just the owned rows there.
		if os.Getenv("FLOW_TEST_DATABASE_DRIVER") == "" {
			began := time.Now()
			bounded = f.sw461Copy(t)
			t.Logf("bounded preimage copy took %s", time.Since(began))
		}
		outcomes := scaleP21SW460Compare(t, []string{"legacy", source}, true, func(mode string) (scaleP21IssueUpdateOutcome, time.Time) {
			fixture := f
			if mode != "legacy" {
				fixture = bounded
			}
			return fixture.sw460Run(t, mode, noPeer, call, true)
		})
		t.Logf("%d existing suggestions, %d scoped issues: %s bounded peak work %.1f MB of 256 MB", existing, len(scope), source, float64(peak)/(1<<20))
		legacy := outcomes[0]
		if legacy.Code != http.StatusOK || len(legacy.State.Events) != 1 {
			t.Fatalf("legacy status %d with %d events: %.300s", legacy.Code, len(legacy.State.Events), legacy.Body)
		}
		// Legacy renumbered the existing rows, which followed the
		// dismissed suggestions of every tenth bulk issue.
		dismissed := (len(ids) + 9) / 10
		moved, renumbered := false, 0
		for index, row := range legacy.State.Suggestions {
			key := row[0].(string)
			if key == "sw461-dismissed-0000" && index > existing {
				moved = true
			}
			if strings.HasPrefix(key, "sw461-existing-") {
				var order int
				fmt.Sscan(fmt.Sprint(row[1]), &order)
				number, _ := strconv.Atoi(strings.TrimPrefix(key, "sw461-existing-"))
				if order != dismissed+number {
					renumbered++
				}
			}
		}
		if !moved || existing > 0 && renumbered != existing {
			t.Fatalf("legacy kept the dismissed suggestion in place (%t) or did not renumber the existing rows (%d of %d)", moved, renumbered, existing)
		}
	})
}

// After this server installed its image, another server adds a suggestion in
// front of the list, deletes an existing one and edits another, all of other
// issues. The generation then renumbers the list (dismissed suggestions move
// behind the regenerated ones) over records it knows and stored rows it does
// not: a legacy server that has not received the other server's peer event
// yet keeps the added row and its order, does not write the deleted one again
// and only moves the edited one. The bounded warm and SQL-reloaded views
// equal that legacy server byte for byte.
func TestScaleP21SW462TriageSuggestionReorderOverPeerRows(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f, ids := scaleP21SW440Setup(t, 100)
	workspace := f.data.Workspace.URLKey
	// The peer case observes the installed legacy image before it reloads
	// from SQL, so seed it through the writer to keep the literal values.
	if err := f.repository.MutateWorkspace(t.Context(), workspace, "scale_p21.sw461_suggestions", "", nil, func(data *domain.Bootstrap) error {
		suggestions, err := scaleP21SW461Suggestions(data, ids, 400)
		data.IssueSuggestions = suggestions
		return err
	}); err != nil {
		t.Fatal(err)
	}
	full, ok := f.repository.BootstrapFor(workspace)
	if !ok {
		t.Fatal("workspace missing")
	}
	var err error
	if f.baseline, err = json.Marshal(full); err != nil {
		t.Fatal(err)
	}
	f.scaleP21TriageWriterFixture.prepare = f.prepare(scaleP21IssueUpdateCase{issue: f.triage.ID})
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	at := time.Date(2026, 1, 6, 7, 8, 9, 0, time.UTC)
	peer := func(t *testing.T) {
		other, err := store.OpenDatabase(f.reader.config)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		if err := other.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw462_peer", "", nil, func(data *domain.Bootstrap) error {
			kept := data.IssueSuggestions[:0]
			for _, item := range data.IssueSuggestions {
				switch item.ID {
				case "sw461-existing-000007":
					continue
				case "sw461-existing-000012":
					item.State, item.StateChangedAt, item.UpdatedAt = "accepted", at, at
				}
				kept = append(kept, item)
			}
			added := domain.IssueSuggestion{ID: "sw462-added", IssueID: data.IssueSuggestions[len(data.IssueSuggestions)-1].IssueID, Type: "relatedIssue", State: "active", SuggestedIssueID: ids[3],
				StateChangedAt: at, CreatedAt: at, UpdatedAt: at, Metadata: map[string]any{"rank": 1, "reasons": []string{"Added elsewhere"}, "score": 0.5}}
			data.IssueSuggestions = append([]domain.IssueSuggestion{added}, kept...)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	outcomes := f.sw460Differential(t, []string{"legacy", "warm", "reloaded"}, peer, f.sw440Generate(t, ids), false)
	legacy := outcomes[0]
	if legacy.Code != http.StatusOK || len(legacy.State.Events) != 1 {
		t.Fatalf("legacy status %d with %d events: %.300s", legacy.Code, len(legacy.State.Events), legacy.Body)
	}
	found := map[string]bool{}
	for _, row := range legacy.State.Suggestions {
		key := row[0].(string)
		found[key] = true
		if key == "sw461-existing-000012" && !strings.Contains(row[2].(string), `"state":"accepted"`) {
			t.Fatal("legacy lost the other server's edit")
		}
	}
	if !found["sw462-added"] || found["sw461-existing-000007"] || !found["sw461-dismissed-0000"] {
		t.Fatalf("legacy rows: added %t, deleted kept %t, dismissed %t", found["sw462-added"], found["sw461-existing-000007"], found["sw461-dismissed-0000"])
	}
}

// A legacy server whose copy is behind (flag off) generates triage after
// another server reassigned the team of a document attached to an issue the
// team auto-move moves: its metadata rebase loses the race on that document,
// refreshes it and runs the callback again. The response lists every scoped
// issue as often as the scope names it (before 2026-11-18 the rerun appended
// a second copy of every result), and the bounded warm and SQL-reloaded views equal that legacy
// server byte for byte, the moved document included.
func TestScaleP21SW463TriageRerunReturnsOneResultPerIssue(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f, ids := scaleP21SW440Setup(t, 100)
	f.scaleP21TriageWriterFixture.prepare = scaleP21SW441Attach(f, f.prepare(scaleP21IssueUpdateCase{issue: f.triage.ID, setup: scaleP21SW400Actions(f, func(settings *domain.TriageIntelligenceSettings) {
		settings.TeamAction, settings.LabelAction = "auto", "auto"
	})}))
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	peer := func(t *testing.T) {
		other, err := store.OpenDatabase(f.reader.config)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		f.sw460Reassign(t, other, "sw440-plain")
	}
	outcomes := f.sw460Differential(t, []string{"legacy", "warm", "reloaded"}, peer, func(service *server) *httptest.ResponseRecorder {
		w := f.sw440Generate(t, ids)(service)
		var results []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
			t.Fatalf("status %d: %.300s", w.Code, w.Body.String())
		}
		// The scope repeats one ID (scaleP21SW440Setup), which every
		// attempt lists as often as the scope names it.
		allowed := map[string]int{}
		for _, id := range ids {
			allowed[id]++
		}
		for _, result := range results {
			if allowed[result.ID]--; allowed[result.ID] < 0 {
				t.Fatalf("bounded=%t: %s listed more often than the scope names it among %d results", service.store.BoundedMetadataEnabled(), result.ID, len(results))
			}
		}
		return w
	}, true)
	legacy := outcomes[0]
	if legacy.Code != http.StatusOK || len(legacy.State.Events) != 1 {
		t.Fatalf("legacy status %d with %d events: %.300s", legacy.Code, len(legacy.State.Events), legacy.Body)
	}
	for _, row := range legacy.State.Documents {
		if row[0] != "sw440-doc" {
			continue
		}
		var document domain.Document
		if err := json.Unmarshal([]byte(row[2].(string)), &document); err != nil {
			t.Fatal(err)
		}
		// The move applied over the other server's write: the legacy server
		// refreshed the document and ran the generation again.
		if document.IssueID != scaleP21SW441Moved || document.IssueTeamID == "sw440-plain" || document.IssueTeamID == scaleP21SW400Team ||
			!document.UpdatedAt.Equal(time.Date(2026, 1, 4, 5, 6, 7, 0, time.UTC)) {
			t.Fatalf("legacy did not move the reassigned document: %s", row[2])
		}
		return
	}
	t.Fatal("document row missing")
}
