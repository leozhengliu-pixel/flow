package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scaleP21TriageWriterFixture struct {
	scaleP21TriageFixture
	reader   *scaleP04Database
	baseline []byte
	// prepare runs after each reset, before publications are captured.
	prepare func(*testing.T)
}

type scaleP21TriageState struct {
	Issues, Content, Suggestions, Projects, Events [][]any
}

type scaleP21TriageOutcome struct {
	Code               int
	Body               json.RawMessage
	State              scaleP21TriageState
	Webhooks, Realtime []json.RawMessage
}

var (
	scaleP21TriageStamp = regexp.MustCompile(`\d{19}_[0-9a-z]{10}`)
	scaleP21TriageClock = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)
)

func scaleP21TriageWriterSetup(t *testing.T) scaleP21TriageWriterFixture {
	t.Helper()
	sw := scaleP21SW10Setup(t)
	f := scaleP21TriageWriterFixture{scaleP21TriageFixture: scaleP21TriageSeed(t, sw.scaleP21M2Fixture), reader: sw.reader}
	repo, workspace := f.repository, f.data.Workspace.URLKey
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 2, 3, 4, 5, 6, 7, time.UTC)
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.triage_writer_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Labels = append(data.Labels, domain.IssueLabel{ID: "triage-l-public", Name: "Preview crash", Description: "Marketplace image preview crash", Scope: "workspace"},
			domain.IssueLabel{ID: "triage-l-private", Name: "Private", Scope: f.private})
		for _, extra := range []domain.IssueSuggestion{
			{ID: "triage-s-label-real", Type: "label", SuggestedLabelID: "triage-l-public"},
			{ID: "triage-s-label-private", Type: "label", SuggestedLabelID: "triage-l-private"},
			{ID: "triage-s-related-active", Type: "relatedIssue", SuggestedIssueID: f.visible.ID},
		} {
			extra.IssueID, extra.State, extra.StateChangedAt, extra.CreatedAt, extra.UpdatedAt = f.triage.ID, "active", stamp, stamp, stamp
			extra.Metadata = map[string]any{"rank": 2, "reasons": []string{"seeded"}}
			data.IssueSuggestions = append(data.IssueSuggestions, extra)
		}
		// A later issue's row keeps its position behind the scoped rows.
		data.IssueSuggestions = append(data.IssueSuggestions, domain.IssueSuggestion{
			ID: "triage-s-tail", IssueID: f.visible.ID, Type: "project", State: "active", SuggestedProjectID: "triage-p-public",
			StateChangedAt: stamp, CreatedAt: stamp, UpdatedAt: stamp,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	full, ok := repo.BootstrapFor(workspace)
	if !ok {
		t.Fatal("triage writer fixture workspace missing")
	}
	var err error
	if f.baseline, err = json.Marshal(full); err != nil {
		t.Fatal(err)
	}
	return f
}

// reset restores the complete fixture through the literal full writer and
// returns the service for source: the legacy store, its warm bounded view, or
// a bounded view freshly loaded from canonical SQL.
func (f *scaleP21TriageWriterFixture) reset(t *testing.T, source string) *server {
	t.Helper()
	repo, workspace := f.repository, f.data.Workspace.URLKey
	var data domain.Bootstrap
	if err := json.Unmarshal(f.baseline, &data); err != nil {
		t.Fatal(err)
	}
	for _, view := range []*store.SQLiteStore{repo, f.probe} {
		view.SetWebhookSink(nil)
		view.SetRealtimeSink(nil)
	}
	restore := store.ForceFullMutationsForTesting(true)
	err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.triage_writer_reset", "", nil, func(next *domain.Bootstrap) error {
		*next = data
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	if f.prepare != nil {
		f.prepare(t)
	}
	switch source {
	case "legacy":
		return &server{store: repo}
	case "warm":
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		return &server{store: f.probe}
	}
	reloaded, err := repo.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return &server{store: reloaded}
}

func (f *scaleP21TriageWriterFixture) rows(t *testing.T, query string, arg any) [][]any {
	t.Helper()
	rows := f.reader.query(t, query, arg)
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

// state reads every SQL row the writers own, the complete suggestion list in
// canonical collection order and all domain events.
func (f *scaleP21TriageWriterFixture) state(t *testing.T) scaleP21TriageState {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	return scaleP21TriageState{
		Issues:      f.rows(t, `SELECT id,version,collection_order,data FROM issue_records WHERE workspace_key=? ORDER BY collection_order,id`, workspace),
		Content:     f.rows(t, `SELECT kind,resource_id,created_at,data FROM workspace_content_records WHERE workspace_key=? ORDER BY kind,resource_id,created_at,data`, workspace),
		Suggestions: f.rows(t, `SELECT record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field='issueSuggestions' ORDER BY collection_order,record_key`, workspace),
		Projects:    f.rows(t, `SELECT record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' ORDER BY collection_order,record_key`, workspace),
		Events:      f.rows(t, `SELECT id,event_type,aggregate_id,payload,previous_values FROM domain_events WHERE workspace_id=? ORDER BY created_at,id`, f.data.Workspace.ID),
	}
}

// run executes one writer call against source and records the response,
// the complete owned SQL state and every publication.
func (f *scaleP21TriageWriterFixture) run(t *testing.T, source string, call func(*server) *httptest.ResponseRecorder) (scaleP21TriageOutcome, time.Time) {
	t.Helper()
	service := f.reset(t, source)
	got := scaleP21TriageOutcome{}
	service.store.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot,
		}))
	})
	service.store.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"clientId": event.ClientID, "payload": event.Payload,
		}))
	})
	prior := map[any]bool{}
	for _, event := range f.state(t).Events {
		prior[event[0]] = true
	}
	started := time.Now().UTC()
	if w := call(service); w != nil {
		got.Code, got.Body = w.Code, json.RawMessage(w.Body.String())
	}
	service.store.SetWebhookSink(nil)
	service.store.SetRealtimeSink(nil)
	got.State = f.state(t)
	var events [][]any
	for _, event := range got.State.Events {
		if !prior[event[0]] {
			events = append(events, event[1:])
		}
	}
	got.State.Events = events
	if service.store.BoundedMetadataEnabled() && (service.store.WorkspaceMetadataDecodes() != 0 || service.store.MetadataWorkUsedForTesting() != 0) {
		t.Fatal("bounded triage writer decoded full metadata or retained work")
	}
	return got, started
}

// normalize replaces only generated stamps (IDs) and clocks read during the
// call; every other byte, order and count is compared.
func scaleP21TriageNormalize(t *testing.T, value any, started time.Time) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	text := scaleP21TriageStamp.ReplaceAllString(string(raw), "<stamp>")
	return scaleP21TriageClock.ReplaceAllStringFunc(text, func(clock string) string {
		if at, err := time.Parse(time.RFC3339Nano, clock); err == nil && !at.Before(started) {
			return "<now>"
		}
		return clock
	})
}

func (f *scaleP21TriageWriterFixture) differential(t *testing.T, name string, call func(*server) *httptest.ResponseRecorder) []scaleP21TriageOutcome {
	t.Helper()
	var normalized []string
	var outcomes []scaleP21TriageOutcome
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		got, started := f.run(t, source, call)
		outcomes = append(outcomes, got)
		normalized = append(normalized, scaleP21TriageNormalize(t, got, started))
		if len(normalized) > 1 && normalized[len(normalized)-1] != normalized[0] {
			t.Fatalf("%s: %s changed: %s", name, source, scaleP21M2Diff(normalized[0], normalized[len(normalized)-1]))
		}
	}
	return outcomes
}

func (f *scaleP21TriageWriterFixture) stateRequest(service *server, user domain.User, key *domain.APIKey, issueID, suggestionID string, accept, cancelled bool) *httptest.ResponseRecorder {
	r := f.literalRequest(user, key, nil)
	r.SetPathValue("id", issueID)
	r.SetPathValue("suggestionId", suggestionID)
	if cancelled {
		ctx, cancel := context.WithCancel(r.Context())
		cancel()
		r = r.WithContext(ctx)
	}
	w := httptest.NewRecorder()
	r, closeOutput := beginRequestResourceReads(r)
	defer closeOutput()
	service.updateIssueSuggestionState(w, r, accept)
	return w
}

func TestScaleP21TriageSuggestionStateWriterDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TriageWriterSetup(t)
	owner, member := f.data.Viewer, f.principals[2].user
	selected := &domain.APIKey{ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}
	cases := []struct {
		name         string
		user         domain.User
		key          *domain.APIKey
		issue, id    string
		accept, stop bool
		code         int
	}{
		{"dismiss-project", owner, nil, f.triage.ID, "triage-s-project-triage-p-public", false, false, http.StatusOK},
		{"dismiss-unscoped-project-selected-key", owner, selected, f.triage.ID, "triage-s-project-triage-p-unscoped", false, false, http.StatusForbidden},
		{"accept-project", owner, nil, f.triage.ID, "triage-s-project-triage-p-public", true, false, http.StatusOK},
		{"accept-archived-project", owner, nil, f.triage.ID, "triage-s-project-triage-p-archived", true, false, http.StatusOK},
		{"accept-private-project-outsider", member, nil, f.triage.ID, "triage-s-project-triage-p-private", true, false, http.StatusForbidden},
		{"accept-assignee", owner, nil, f.triage.ID, "triage-s-assignee", true, false, http.StatusOK},
		{"accept-label", member, nil, f.triage.ID, "triage-s-label-real", true, false, http.StatusOK},
		{"accept-private-label-outsider", member, nil, f.triage.ID, "triage-s-label-private", true, false, http.StatusForbidden},
		{"accept-missing-label", owner, nil, f.triage.ID, "triage-s-label", true, false, http.StatusForbidden},
		{"accept-duplicate-relation", owner, nil, f.triage.ID, "triage-s-similar-" + f.visible.ID, true, false, http.StatusOK},
		{"accept-related-relation", member, nil, f.triage.ID, "triage-s-related-active", true, false, http.StatusOK},
		{"accept-missing-issue-target", owner, nil, f.triage.ID, "triage-s-similar-" + f.gone.ID, true, false, http.StatusForbidden},
		{"accept-self-relation", owner, nil, f.triage.ID, "triage-s-similar-" + f.triage.ID, true, false, http.StatusOK},
		{"dismiss-team", owner, nil, f.triage.ID, "triage-s-team-" + f.public, false, false, http.StatusOK},
		{"dismiss-accepted", owner, nil, f.triage.ID, "triage-s-related-" + f.visible.ID, false, false, http.StatusNotFound},
		{"dismiss-other-issue-row", owner, nil, f.triage.ID, "triage-s-other", false, false, http.StatusNotFound},
		{"dismiss-missing", owner, nil, f.triage.ID, "triage-s-missing", false, false, http.StatusNotFound},
		{"dismiss-cancelled", owner, nil, f.triage.ID, "triage-s-project-triage-p-public", false, true, 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			outcomes := f.differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
				return f.stateRequest(service, test.user, test.key, test.issue, test.id, test.accept, test.stop)
			})
			if test.code != 0 && outcomes[0].Code != test.code {
				t.Fatalf("legacy status %d, want %d: %s", outcomes[0].Code, test.code, outcomes[0].Body)
			}
			published := 0
			if test.code == http.StatusOK {
				published = 1
			}
			if got := outcomes[0]; len(got.State.Events) != published || len(got.Webhooks) != published || len(got.Realtime) != published {
				t.Fatalf("legacy events/webhooks/realtime %d/%d/%d, want %d", len(got.State.Events), len(got.Webhooks), len(got.Realtime), published)
			}
		})
	}
	// Team acceptance moves and renumbers the issue. The bounded writer
	// refuses it before any read-write work; nothing is written or published.
	t.Run("accept-team-refused", func(t *testing.T) {
		legacy, _ := f.run(t, "legacy", func(service *server) *httptest.ResponseRecorder {
			return f.stateRequest(service, owner, nil, f.triage.ID, "triage-s-team-"+f.public, true, false)
		})
		if legacy.Code != http.StatusOK {
			t.Fatalf("legacy team acceptance: %d %s", legacy.Code, legacy.Body)
		}
		for _, source := range []string{"warm", "reloaded"} {
			service := f.reset(t, source)
			before := f.state(t)
			w := f.stateRequest(service, owner, nil, f.triage.ID, "triage-s-team-"+f.public, true, false)
			if w.Code == http.StatusOK || !reflect.DeepEqual(before, f.state(t)) {
				t.Fatalf("%s team acceptance was not refused cleanly: %d %s", source, w.Code, w.Body.String())
			}
		}
		owned, closeOwned := beginIndependentMetadataOutputReads(t.Context())
		defer closeOwned()
		ctx := store.ContextWithIssueSuggestionWrite(store.WithIssueRenumbering(store.WithIssueRecordMutations(owned, f.triage.ID)), store.IssueSuggestionWrite{})
		err := f.probe.MutateWorkspace(ctx, f.data.Workspace.URLKey, "issue.suggestion_accepted", "x", nil, func(*domain.Bootstrap) error {
			t.Fatal("refused callback ran")
			return nil
		})
		if !errors.Is(err, store.ErrIssueSuggestionWriteRefused) || !errors.Is(err, store.ErrBoundedMetadataCoverage) {
			t.Fatal("renumbering was not a typed refusal", err)
		}
	})
}

func TestScaleP21TriagePruneWriterDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TriageWriterSetup(t)
	for _, test := range []struct {
		name  string
		stale func(domain.IssueSuggestion) bool
	}{
		{"project-across-issues", func(item domain.IssueSuggestion) bool {
			return item.Type == "project" && (item.SuggestedProjectID == "triage-p-public" || item.SuggestedProjectID == "triage-p-mixed")
		}},
		{"assignee", func(item domain.IssueSuggestion) bool { return item.Type == "assignee" }},
		{"label-and-team", func(item domain.IssueSuggestion) bool {
			return item.Type == "label" || item.Type == "team" && item.SuggestedTeamID == f.private
		}},
		{"dismissed-only", func(item domain.IssueSuggestion) bool { return item.State == "dismissed" }},
		{"nothing", func(domain.IssueSuggestion) bool { return false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f.differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
				service.pruneIssueSuggestionTargets(t.Context(), f.data.Workspace.URLKey, test.stale)
				return nil
			})
		})
	}
	t.Run("missing-workspace", func(t *testing.T) {
		f.differential(t, "missing-workspace", func(service *server) *httptest.ResponseRecorder {
			service.pruneIssueSuggestionTargets(t.Context(), "triage-missing-workspace", func(domain.IssueSuggestion) bool { return true })
			return nil
		})
	})
}

// Concurrent writers on one issue serialize: the bounded outcome equals one of
// the legacy sequential orders, with no lost suggestion or activity update.
func TestScaleP21TriageWriterConcurrentSameIssue(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TriageWriterSetup(t)
	owner := f.data.Viewer
	first := func(service *server) *httptest.ResponseRecorder {
		return f.stateRequest(service, owner, nil, f.triage.ID, "triage-s-assignee", true, false)
	}
	second := func(service *server) *httptest.ResponseRecorder {
		return f.stateRequest(service, owner, nil, f.triage.ID, "triage-s-project-triage-p-public", false, false)
	}
	expected := map[string]bool{}
	for _, order := range [][2]func(*server) *httptest.ResponseRecorder{{first, second}, {second, first}} {
		got, started := f.run(t, "legacy", func(service *server) *httptest.ResponseRecorder {
			order[0](service)
			order[1](service)
			return nil
		})
		expected[scaleP21TriageNormalize(t, got.State, started)] = true
	}
	for _, source := range []string{"warm", "reloaded"} {
		for attempt := range 3 {
			got, started := f.run(t, source, func(service *server) *httptest.ResponseRecorder {
				var wait sync.WaitGroup
				codes := make([]int, 2)
				for index, call := range []func(*server) *httptest.ResponseRecorder{first, second} {
					wait.Add(1)
					go func() {
						defer wait.Done()
						codes[index] = call(service).Code
					}()
				}
				wait.Wait()
				if codes[0] != http.StatusOK || codes[1] != http.StatusOK {
					t.Errorf("concurrent writers failed: %v", codes)
				}
				return nil
			})
			if state := scaleP21TriageNormalize(t, got.State, started); !expected[state] {
				t.Fatalf("%s attempt %d: concurrent state matches no sequential legacy order", source, attempt)
			}
		}
	}
}

// The regeneration shape (runTriageIntelligence / generate): the literal
// storeTriageSuggestions callback removes an issue's rows and re-appends its
// dismissed rows and new suggestions, while other scoped issues' rows keep
// their positions. Generated IDs are fixed so stored rows compare exactly.
func TestScaleP21TriageRegenerationWriterDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TriageWriterSetup(t)
	at := time.Date(2026, 4, 5, 6, 7, 8, 9, time.UTC)
	generated := func(issueID, prefix string) []domain.IssueSuggestion {
		var items []domain.IssueSuggestion
		for index, target := range []struct{ kind, id string }{
			{"project", "triage-p-public"}, {"project", "triage-p-unscoped"}, {"label", "triage-l-public"},
			{"relatedIssue", f.visible.ID}, {"assignee", f.data.Viewer.ID},
		} {
			item := domain.IssueSuggestion{ID: prefix + target.id, IssueID: issueID, Type: target.kind, State: "active",
				StateChangedAt: at, CreatedAt: at, UpdatedAt: at, Metadata: map[string]any{"rank": index + 1, "source": triageSourceHeuristic}}
			switch target.kind {
			case "project":
				item.SuggestedProjectID = target.id
			case "label":
				item.SuggestedLabelID = target.id
			case "assignee":
				item.SuggestedUserID = target.id
			default:
				item.SuggestedIssueID = target.id
			}
			items = append(items, item)
		}
		return items
	}
	for _, test := range []struct {
		name        string
		scope       []string
		regenerated []string
		auto        bool
	}{
		{"single-issue", []string{f.triage.ID}, []string{f.triage.ID}, false},
		{"single-issue-auto-actions", []string{f.triage.ID, f.visible.ID}, []string{f.triage.ID}, true},
		{"other-scoped-issue-keeps-position", []string{f.visible.ID, f.triage.ID}, []string{f.triage.ID}, false},
		{"two-issues", []string{f.triage.ID, f.visible.ID}, []string{f.visible.ID, f.triage.ID}, false},
		{"no-rows-issue", []string{f.hidden.ID}, []string{f.hidden.ID}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			f.prepare = nil
			if test.auto {
				f.prepare = func(t *testing.T) {
					if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.triage_writer_auto", "", nil, func(data *domain.Bootstrap) error {
						settings := &data.WorkspaceSettings.FeatureSettings.TriageIntelligence
						settings.LabelAction, settings.RelatedAction, settings.AssigneeAction, settings.ProjectAction = "auto", "auto", "auto", "auto"
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
			}
			defer func() { f.prepare = nil }()
			f.differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
				ctx, closeOwned := beginIndependentMetadataOutputReads(t.Context())
				defer closeOwned()
				ctx = store.ContextWithActor(ctx, f.data.Viewer)
				ctx = store.WithIssueRecordMutations(ctx, test.scope...)
				if service.store.BoundedMetadataEnabled() {
					// Callers declare the targets they generated before the write.
					var input store.IssueSuggestionWrite
					for _, id := range test.regenerated {
						for _, item := range generated(id, "") {
							input.Users = append(input.Users, item.SuggestedUserID)
							input.Projects = append(input.Projects, item.SuggestedProjectID)
							input.Labels = append(input.Labels, item.SuggestedLabelID)
						}
					}
					ctx = store.ContextWithIssueSuggestionWrite(ctx, input)
				}
				err := service.store.MutateWorkspace(ctx, f.data.Workspace.URLKey, "issue.suggestions_refreshed", test.regenerated[0], nil, func(data *domain.Bootstrap) error {
					for _, id := range test.regenerated {
						issue, err := issueByID(data, id)
						if err != nil {
							return err
						}
						storeTriageSuggestions(data, issue, generated(id, "regen-"+id+"-"), at, triageSourceHeuristic, "thinking")
						issue.SuggestionsGeneratedAt = &at
						issue.Version++
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				return nil
			})
		})
	}
}

// generateTriageIntelligenceForIssues: the heuristic callback scores complete
// projects, labels and users inside the bounded write. Titles share words so
// candidate search finds the related issue.
func TestScaleP21TriageGenerateWriterDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TriageWriterSetup(t)
	workspace := f.data.Workspace.URLKey
	triage := func(settings func(*domain.TriageIntelligenceSettings), triaged ...string) func(*testing.T) {
		return func(t *testing.T) {
			if err := f.repository.MutateWorkspace(t.Context(), workspace, "scale_p21.triage_generate_settings", "", nil, func(data *domain.Bootstrap) error {
				settings(&data.WorkspaceSettings.FeatureSettings.TriageIntelligence)
				data.Projects = append(data.Projects, domain.Project{ID: "triage-p-vehicle", Name: "Vehicle marketplace", Summary: "Marketplace image preview work", TeamIDs: []string{f.public}})
				data.Labels = append(data.Labels, domain.IssueLabel{ID: "triage-l-preview", Name: "Image preview", Description: "Vehicle marketplace image preview"})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for index, id := range []string{f.triage.ID, f.visible.ID} {
				if err := f.repository.MutateWorkspace(store.WithIssueRecordMutations(t.Context(), id), workspace, "scale_p21.triage_generate_issue", id, nil, func(data *domain.Bootstrap) error {
					issue, err := issueByID(data, id)
					if err != nil {
						return err
					}
					issue.Title = []string{"Vehicle marketplace image preview", "Vehicle marketplace image preview component"}[index]
					issue.Description = "Reusable vehicle marketplace image preview for listings."
					issue.SuggestionsGeneratedAt = nil
					issue.State = domain.WorkflowState{ID: "triage-started", Name: "Started", Type: "started", TeamID: f.public}
					if slices.Contains(triaged, id) {
						issue.State = domain.WorkflowState{ID: "triage-backlog", Name: "Triage", Type: "backlog", TeamID: f.public}
						issue.TriagedAt = nil
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	generate := func(ids ...string) func(*server) *httptest.ResponseRecorder {
		return func(service *server) *httptest.ResponseRecorder {
			r := f.literalRequest(f.data.Viewer, nil, nil)
			r, closeOutput := beginRequestResourceReads(r)
			defer closeOutput()
			results, err := service.generateTriageIntelligenceForIssues(r.Context(), workspace, ids)
			w := httptest.NewRecorder()
			code := http.StatusOK
			if errors.Is(err, store.ErrNoMutation) {
				code = http.StatusNoContent
			} else if err != nil {
				t.Fatalf("generate bounded=%t: %v", service.store.BoundedMetadataEnabled(), err)
			}
			writeJSON(w, code, results)
			return w
		}
	}
	suggest := func(settings *domain.TriageIntelligenceSettings) {
		settings.AssigneeAction, settings.ProjectAction, settings.LabelAction = "suggest", "suggest", "suggest"
		settings.DuplicateAction, settings.RelatedAction, settings.TeamAction = "suggest", "suggest", "hide"
	}
	auto := func(settings *domain.TriageIntelligenceSettings) {
		suggest(settings)
		settings.ProjectAction, settings.LabelAction, settings.RelatedAction, settings.DuplicateAction = "auto", "auto", "auto", "auto"
	}
	for _, test := range []struct {
		name     string
		prepare  func(*testing.T)
		ids      []string
		minimum  int
		expected int
	}{
		{"single-suggest", triage(suggest, f.triage.ID), []string{f.triage.ID}, 2, http.StatusOK},
		{"single-auto-apply", triage(auto, f.triage.ID), []string{f.triage.ID}, 2, http.StatusOK},
		{"batch-without-candidates", triage(suggest, f.triage.ID), []string{f.hidden.ID, f.triage.ID, f.gone.ID}, 2, http.StatusOK},
		{"not-triage", triage(suggest), []string{f.triage.ID}, 0, http.StatusNoContent},
		{"scoped-candidate", triage(suggest, f.triage.ID), []string{f.visible.ID, f.triage.ID}, 2, http.StatusOK},
		{"mutual-candidates", triage(suggest, f.triage.ID, f.visible.ID), []string{f.triage.ID, f.visible.ID}, 2, http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			f.prepare = test.prepare
			defer func() { f.prepare = nil }()
			outcomes := f.differential(t, test.name, generate(test.ids...))
			if outcomes[0].Code != test.expected {
				t.Fatalf("legacy status %d: %s", outcomes[0].Code, outcomes[0].Body)
			}
			if count := strings.Count(scaleP21TriageNormalize(t, outcomes[0].State.Suggestions, time.Now()), "issue_suggestion_"); count < test.minimum {
				t.Fatalf("legacy generated %d suggestions, want at least %d", count, test.minimum)
			}
		})
	}
	// Auto team moves are refused before any write: legacy would move and
	// renumber the issue.
	for _, test := range []struct {
		name    string
		prepare func(*testing.T)
		ids     []string
	}{
		{"team-auto-move", triage(func(settings *domain.TriageIntelligenceSettings) { auto(settings); settings.TeamAction = "auto" }, f.triage.ID), []string{f.triage.ID}},
	} {
		t.Run(test.name+"-refused", func(t *testing.T) {
			f.prepare = test.prepare
			defer func() { f.prepare = nil }()
			for _, source := range []string{"warm", "reloaded"} {
				service := f.reset(t, source)
				before := f.state(t)
				r := f.literalRequest(f.data.Viewer, nil, nil)
				r, closeOutput := beginRequestResourceReads(r)
				_, err := service.generateTriageIntelligenceForIssues(r.Context(), workspace, test.ids)
				closeOutput()
				if !errors.Is(err, store.ErrIssueSuggestionWriteRefused) || !reflect.DeepEqual(before, f.state(t)) {
					t.Fatalf("%s: not a clean refusal: %v", source, err)
				}
			}
		})
	}
}

// runTriageIntelligence through the "Run again" route: heuristic and
// model-backed runs (a fixed model reply over the bounded projected context)
// write the same rows, order, events and publications. The model receives a
// byte-identical prompt from every source.
func TestScaleP21TriageRunWriterDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TriageWriterSetup(t)
	workspace := f.data.Workspace.URLKey
	setup := func(settings func(*domain.TriageIntelligenceSettings), triaged bool) func(*testing.T) {
		return func(t *testing.T) {
			if err := f.repository.MutateWorkspace(t.Context(), workspace, "scale_p21.triage_run_settings", "", nil, func(data *domain.Bootstrap) error {
				settings(&data.WorkspaceSettings.FeatureSettings.TriageIntelligence)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.repository.MutateWorkspace(store.WithIssueRecordMutations(t.Context(), f.triage.ID), workspace, "scale_p21.triage_run_issue", f.triage.ID, nil, func(data *domain.Bootstrap) error {
				issue, err := issueByID(data, f.triage.ID)
				if err != nil {
					return err
				}
				issue.Title, issue.Description = "Public preview bug in marketplace", "The public marketplace image preview crashes."
				issue.State = domain.WorkflowState{ID: "triage-started", Name: "Started", Type: "started", TeamID: f.public}
				if triaged {
					issue.State = domain.WorkflowState{ID: "triage-backlog", Name: "Triage", Type: "backlog", TeamID: f.public}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	suggest := func(settings *domain.TriageIntelligenceSettings) {
		settings.AssigneeAction, settings.ProjectAction, settings.LabelAction = "suggest", "suggest", "suggest"
		settings.DuplicateAction, settings.RelatedAction, settings.TeamAction = "suggest", "suggest", "hide"
	}
	auto := func(settings *domain.TriageIntelligenceSettings) {
		suggest(settings)
		settings.ProjectAction, settings.LabelAction, settings.RelatedAction = "auto", "auto", "auto"
	}
	provider := &triageAIProvider{reply: func() string {
		return `{"duplicate":null,"related":[{"identifier":"` + f.visible.Identifier + `","reasons":["Same preview area"]}],"assignee":{"name":"` + f.data.Viewer.Name + `","reasons":["Leads it"]},"project":{"name":"Public","reasons":["Named"]},"labels":[{"name":"Bug","reasons":["A crash"]}],"team":null,"thinking":"Preview crash in the public project."}`
	}}
	upstream := httptest.NewServer(provider)
	t.Cleanup(upstream.Close)
	refresh := func(model bool) func(*server) *httptest.ResponseRecorder {
		return func(service *server) *httptest.ResponseRecorder {
			if model {
				service.agent = appconfig.AgentConfig{Enabled: true, BaseURL: upstream.URL, Model: "model", MaxOutputTokens: 800, Timeout: 5 * time.Second}
				service.agentClient = upstream.Client()
			}
			r := f.literalRequest(f.data.Viewer, nil, nil)
			r.SetPathValue("id", f.triage.ID)
			r, closeOutput := beginRequestResourceReads(r)
			defer closeOutput()
			w := httptest.NewRecorder()
			service.refreshIssueSuggestions(w, r)
			return w
		}
	}
	for _, test := range []struct {
		name    string
		prepare func(*testing.T)
		model   bool
		code    int
	}{
		{"heuristic-suggest", setup(suggest, true), false, http.StatusOK},
		{"heuristic-auto", setup(auto, true), false, http.StatusOK},
		{"model-suggest", setup(suggest, true), true, http.StatusOK},
		{"model-auto", setup(auto, true), true, http.StatusOK},
		{"not-triage", setup(suggest, false), false, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			f.prepare = test.prepare
			defer func() { f.prepare = nil }()
			provider.mu.Lock()
			provider.prompts = nil
			provider.mu.Unlock()
			outcomes := f.differential(t, test.name, refresh(test.model))
			if outcomes[0].Code != test.code {
				t.Fatalf("legacy status %d: %s", outcomes[0].Code, outcomes[0].Body)
			}
			if test.model {
				provider.mu.Lock()
				prompts := slices.Clone(provider.prompts)
				provider.mu.Unlock()
				if len(prompts) != 3 || prompts[1] != prompts[0] || prompts[2] != prompts[0] {
					t.Fatalf("model prompts differ across sources (%d calls)", len(prompts))
				}
			}
		})
	}
	t.Run("team-auto-move-refused", func(t *testing.T) {
		f.prepare = setup(func(settings *domain.TriageIntelligenceSettings) { suggest(settings); settings.TeamAction = "auto" }, true)
		defer func() { f.prepare = nil }()
		for _, source := range []string{"warm", "reloaded"} {
			service := f.reset(t, source)
			before := f.state(t)
			w := refresh(false)(service)
			if w.Code == http.StatusOK || !reflect.DeepEqual(before, f.state(t)) {
				t.Fatalf("%s: team auto-move run was not refused cleanly: %d %s", source, w.Code, w.Body.String())
			}
		}
	})
}
