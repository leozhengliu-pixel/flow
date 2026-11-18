package main

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
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
	for _, event := range f.state(t).Events {
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
	reloaded, err := store.OpenDatabase(f.reader.config)
	if err != nil {
		t.Fatal(err)
	}
	got.Reloaded = f.installed(t, reloaded)
	if err := reloaded.Close(); err != nil {
		t.Fatal(err)
	}
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
	var normalized []string
	var outcomes []scaleP21IssueUpdateOutcome
	for _, source := range sources {
		got, started := f.sw460Run(t, source, peer, call, fresh)
		outcomes = append(outcomes, got)
		normalized = append(normalized, scaleP21IssueUpdateNormalize(t, got, started))
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
