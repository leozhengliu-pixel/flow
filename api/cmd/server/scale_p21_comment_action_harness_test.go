package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The comment-action and agent-reply writers are compared on the issue
// update fixture: legacy, warm bounded and SQL-reloaded bounded runs must
// agree byte for byte (generated IDs and clocks aside) on the response,
// every owned SQL row, the metadata rows and installed image of the fields
// these writers read or write, and every webhook and realtime publication.

var scaleP21WriterFields = []string{
	"commentActions", "users", "threadSubscriptions", "issueSlas", "slaEvents", "labels",
	"projects", "documents", "notificationPreferences", "members", "teamMembers", "issueSuggestions",
}

type scaleP21WriterOutcome struct {
	Code               int
	Body               json.RawMessage
	State              scaleP21IssueUpdateState
	Metadata           [][]any
	Webhooks, Realtime []json.RawMessage
	Installed          json.RawMessage
	Extra              any
}

func scaleP21WriterInstalled(t *testing.T, workspace string, repo *store.SQLiteStore, names []string) json.RawMessage {
	t.Helper()
	result := map[string]any{}
	if !repo.BoundedMetadataEnabled() {
		full, ok := repo.BootstrapFor(workspace)
		if !ok {
			t.Fatal("workspace missing")
		}
		raw, err := json.Marshal(full)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			var value any
			if len(fields[name]) != 0 {
				if err := json.Unmarshal(fields[name], &value); err != nil {
					t.Fatal(name, err)
				}
			}
			result[name] = value
		}
	} else {
		read, found, err := repo.BeginInstalledMetadataRead(t.Context(), workspace)
		if err != nil || !found {
			t.Fatal("installed read", err)
		}
		for _, name := range names {
			var raw strings.Builder
			if err := read.StreamFieldJSON(t.Context(), name, &raw); err != nil {
				read.Close()
				t.Fatal(name, err)
			}
			var value any
			if raw.Len() != 0 {
				if err := json.Unmarshal([]byte(raw.String()), &value); err != nil {
					read.Close()
					t.Fatal(name, err)
				}
			}
			result[name] = value
		}
		read.Close()
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// scaleP21WriterOutputFields are the installed fields these writers change;
// a source's installed image must equal its SQL reload for each of them.
// Other fields are compared through the SQL reload only: a legacy snapshot
// keeps their pre-normalization shape (label resource types, canonical
// document owners, default preferences) until reloaded.
var scaleP21WriterOutputFields = []string{
	"commentActions", "threadSubscriptions", "issueSlas", "slaEvents", "issueSuggestions",
}

// run executes call against source and records everything it may change.
func (f scaleP21IssueUpdateFixture) runWriter(t *testing.T, source string, call func(*server) (*httptest.ResponseRecorder, any)) (scaleP21WriterOutcome, time.Time) {
	t.Helper()
	started := time.Now().UTC()
	service := f.reset(t, source)
	workspace := f.data.Workspace.URLKey
	got := scaleP21WriterOutcome{}
	var mu sync.Mutex
	service.store.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasPrefix(event.Type, "scale_p21.") {
			return
		}
		got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot,
		}))
	})
	realtime := func(_ string, event domain.RealtimeEvent) {
		mu.Lock()
		defer mu.Unlock()
		// Test seeds and races run through the literal writer of the
		// shared database; they are not outputs of the writer under test.
		if event.Type == "agent_task.updated" || strings.HasPrefix(event.Type, "scale_p21.") {
			return
		}
		got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"clientId": event.ClientID, "payload": event.Payload,
		}))
	}
	service.store.SetRealtimeSink(realtime)
	f.repository.SetRealtimeSink(realtime)
	prior := map[any]bool{}
	for _, event := range f.state(t).Events {
		prior[event[0]] = true
	}
	w, extra := call(service)
	if w != nil {
		got.Code, got.Body = w.Code, json.RawMessage(w.Body.String())
	}
	got.Extra = extra
	service.store.SetWebhookSink(nil)
	service.store.SetRealtimeSink(nil)
	f.repository.SetRealtimeSink(nil)
	got.State = f.state(t)
	// created_at is RFC3339Nano text with trimmed zeros, so its SQL order
	// is not time order; order the events by their parsed time.
	stamps := map[any]time.Time{}
	for _, row := range f.rows(t, `SELECT id,created_at FROM domain_events WHERE workspace_id=?`, f.data.Workspace.ID) {
		if text, ok := row[1].(string); ok {
			stamps[row[0]], _ = time.Parse(time.RFC3339Nano, text)
		}
	}
	sort.SliceStable(got.State.Events, func(i, j int) bool {
		return stamps[got.State.Events[i][0]].Before(stamps[got.State.Events[j][0]])
	})
	var events [][]any
	for _, event := range got.State.Events {
		if kind, _ := event[1].(string); !prior[event[0]] && !strings.HasPrefix(kind, "scale_p21.") {
			events = append(events, event[1:])
		}
	}
	got.State.Events = events
	got.Metadata = f.rows(t, `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('commentActions','users','threadSubscriptions','issueSlas','slaEvents','labels','notificationPreferences','members','teamMembers') ORDER BY field,collection_order,record_key`, workspace)
	if service.store.BoundedMetadataEnabled() && (service.store.WorkspaceMetadataDecodes() != 0 || service.store.MetadataWorkUsedForTesting() != 0) {
		t.Fatalf("%s: bounded writer decoded full metadata (%d) or retained work (%d)", source,
			service.store.WorkspaceMetadataDecodes(), service.store.MetadataWorkUsedForTesting())
	}
	installed := scaleP21WriterInstalled(t, workspace, service.store, scaleP21WriterOutputFields)
	reloaded, err := store.OpenDatabase(f.reader.config)
	if err != nil {
		t.Fatal(err)
	}
	loaded := scaleP21WriterInstalled(t, workspace, reloaded, scaleP21WriterOutputFields)
	got.Installed = scaleP21WriterInstalled(t, workspace, reloaded, scaleP21WriterFields)
	if err := reloaded.Close(); err != nil {
		t.Fatal(err)
	}
	nul := strings.NewReplacer(`\u0000`, "")
	if installed, loaded := nul.Replace(string(installed)), nul.Replace(string(loaded)); installed != loaded {
		t.Fatalf("%s: installed image differs from the SQL reload: %s", source, scaleP21M2Diff(installed, loaded))
	}
	return got, started
}

func (f scaleP21IssueUpdateFixture) writerDifferential(t *testing.T, name string, call func(*server) (*httptest.ResponseRecorder, any)) []scaleP21WriterOutcome {
	t.Helper()
	var normalized []string
	var outcomes []scaleP21WriterOutcome
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		got, started := f.runWriter(t, source, call)
		outcomes = append(outcomes, got)
		normalized = append(normalized, scaleP21IssueUpdateNormalize(t, got, started))
		if len(normalized) > 1 && normalized[len(normalized)-1] != normalized[0] {
			if dir := os.Getenv("FLOW_ISSUE_UPDATE_DUMP"); dir != "" {
				_ = os.WriteFile(dir+"/legacy.json", []byte(normalized[0]), 0o600)
				_ = os.WriteFile(dir+"/"+source+".json", []byte(normalized[len(normalized)-1]), 0o600)
			}
			t.Fatalf("%s: %s changed: %s", name, source, scaleP21M2Diff(normalized[0], normalized[len(normalized)-1]))
		}
	}
	return outcomes
}

// seed applies change through the literal full writer after each reset.
func (f scaleP21IssueUpdateFixture) seedWriter(t *testing.T, change func(*domain.Bootstrap)) {
	t.Helper()
	restore := store.ForceFullMutationsForTesting(true)
	err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.writer_seed", "", nil, func(data *domain.Bootstrap) error {
		change(data)
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
}

func scaleP21WriterContent(outcome scaleP21WriterOutcome, kind string) []map[string]any {
	var result []map[string]any
	for _, row := range outcome.State.Content {
		if row[0] != kind {
			continue
		}
		var value map[string]any
		if json.Unmarshal([]byte(row[3].(string)), &value) == nil {
			result = append(result, value)
		}
	}
	return result
}
