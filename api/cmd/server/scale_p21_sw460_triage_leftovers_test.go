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

// scaleP21SW461Seed adds existing suggestions to the SW440 baseline: a
// dismissed suggestion of every tenth scoped issue first (generation moves it
// behind the regenerated ones), then existing suggestions of two issues outside
// the scope, active and dismissed, of the size real generations store.
func scaleP21SW461Seed(t *testing.T, f *scaleP21IssueUpdateFixture, ids []string, existing int) {
	t.Helper()
	at := time.Date(2026, 1, 5, 6, 7, 8, 0, time.UTC)
	dismissed := 0
	// Batches stay inside the legacy transaction write budget.
	for batch := 0; batch == 0 || batch < existing; batch += 10000 {
		if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw461_suggestions", "", nil, func(data *domain.Bootstrap) error {
			var head []domain.IssueSuggestion
			if batch == 0 {
				for index, id := range ids {
					if index%10 == 0 {
						head = append(head, domain.IssueSuggestion{ID: fmt.Sprintf("sw461-dismissed-%04d", index), IssueID: id, Type: "label", State: "dismissed",
							SuggestedLabelID: "sw461-label", StateChangedAt: at, CreatedAt: at, UpdatedAt: at,
							Metadata: map[string]any{"rank": 1, "reasons": []string{"Dismissed earlier"}, "score": 0.5}})
					}
				}
				dismissed = len(head)
			} else {
				head = append(head, data.IssueSuggestions[:dismissed+batch]...)
			}
			// Other issues: outside every generation scope.
			scoped := map[string]bool{}
			for _, id := range ids {
				scoped[id] = true
			}
			var others []string
			for _, issue := range data.Issues {
				if !scoped[issue.ID] && len(others) < 2 {
					others = append(others, issue.ID)
				}
			}
			if len(others) != 2 {
				return fmt.Errorf("sw461 needs two issues outside the scope, found %v", others)
			}
			for index := batch; index < min(batch+10000, existing); index++ {
				state := "active"
				if index%4 == 3 {
					state = "dismissed"
				}
				head = append(head, domain.IssueSuggestion{ID: fmt.Sprintf("sw461-existing-%06d", index), IssueID: others[index%2], Type: "relatedIssue", State: state,
					SuggestedIssueID: fmt.Sprintf("sw400-bulk-%04d", index%len(ids)), StateChangedAt: at, CreatedAt: at, UpdatedAt: at,
					Metadata: map[string]any{"rank": index%5 + 1, "reasons": []string{"Similar title", fmt.Sprintf("Shares %d words", index%7+2)}, "score": 0.25 + float64(index%50)/100}})
			}
			if batch != 0 {
				data.IssueSuggestions = append(head, data.IssueSuggestions[dismissed+batch:]...)
				return nil
			}
			// The list is stored one row per suggestion, as generations
			// keep it: the fixture's repeated suggestion IDs (which keep the
			// field inline in the root document) keep only their first
			// occurrence.
			seen := map[string]bool{}
			for _, item := range data.IssueSuggestions {
				if !seen[item.ID] {
					seen[item.ID] = true
					head = append(head, item)
				}
			}
			data.IssueSuggestions = head
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	full, ok := f.repository.BootstrapFor(f.data.Workspace.URLKey)
	if !ok {
		t.Fatal("workspace missing")
	}
	var err error
	if f.baseline, err = json.Marshal(full); err != nil {
		t.Fatal(err)
	}
}

// A workspace with many existing suggestions: triage generation over one
// issue and over 1000 issues moves the scoped issues' dismissed suggestions
// behind their regenerated ones, so legacy renumbers the whole suggestion
// list. From an empty 256 MB work budget the bounded writer equals legacy
// byte for byte (warm and SQL-reloaded: response, every suggestion row with
// its collection order, issue, history and document rows, events, sinks and
// the installed image). With FLOW_SW461_MEASURE=1 the bounded warm run only
// logs its peak work under an unlimited budget (the ceiling measurement), with
// FLOW_SW461_MEASURE=budget its status under the 256 MB budget.
func TestScaleP21SW461TriageSuggestionListReorderDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f, ids := scaleP21SW440Setup(t, scaleP21SW440Size(t))
	existing := scaleP21SW461Existing(t)
	scaleP21SW461Seed(t, &f, ids, existing)
	measure := os.Getenv("FLOW_SW461_MEASURE") != ""
	for _, scope := range [][]string{ids[:1], ids} {
		t.Run(fmt.Sprintf("scope-%d", len(scope)), func(t *testing.T) {
			f.scaleP21TriageWriterFixture.prepare = f.prepare(scaleP21IssueUpdateCase{issue: f.triage.ID})
			defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
			generate := f.sw440Generate(t, scope)
			peak := 0
			call := func(service *server) *httptest.ResponseRecorder {
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
				got, _ := f.sw460Run(t, "warm", noPeer, call, true)
				t.Logf("%d existing suggestions, %d scoped issues: status %d, bounded peak work %.1f MB", existing, len(scope), got.Code, float64(peak)/(1<<20))
				return
			}
			outcomes := f.sw460Differential(t, []string{"legacy", "warm", "reloaded"}, noPeer, call, true)
			t.Logf("%d existing suggestions, %d scoped issues: bounded peak work %.1f MB of 256 MB", existing, len(scope), float64(peak)/(1<<20))
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
	scaleP21SW461Seed(t, &f, ids, 400)
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
