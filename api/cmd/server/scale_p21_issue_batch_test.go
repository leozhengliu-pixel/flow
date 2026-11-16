package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// POST /api/issue-records/batch through the bounded issue batch writer,
// compared with the legacy scoped writer: field, state (family and sort
// neighbours), label, people, description, team-move and SLA/suggestion
// updates, atomic failures of mixed-team and 1000-issue batches, team-move
// permissions, cancellation, concurrent batches and inline collections.

func (f scaleP21IssueUpdateFixture) batch(service *server, user domain.User, key *domain.APIKey, ids []string, update map[string]any, cancelled bool) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(map[string]any{"issueIds": ids, "update": update})
	r := httptest.NewRequest(http.MethodPost, "/api/issue-records/batch", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
	r.SetPathValue("workspaceKey", f.data.Workspace.URLKey)
	r.SetPathValue("workspace", f.data.Workspace.URLKey)
	ctx := context.WithValue(r.Context(), authUserContextKey{}, user)
	ctx = store.ContextWithActor(ctx, user)
	if key != nil {
		ctx = context.WithValue(ctx, apiKeyContextKey{}, *key)
	}
	// issueRecordAlias scopes the write to the request's issues.
	ctx = store.WithIssueRecordMutations(ctx, ids...)
	if cancelled {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		cancel()
	}
	r = r.WithContext(ctx)
	r, closeOutput := beginRequestResourceReads(r)
	defer closeOutput()
	w := httptest.NewRecorder()
	service.batchUpdate(w, r)
	// The legacy writer lists the other changed issues in map order.
	var items []map[string]json.RawMessage
	if w.Code == http.StatusOK && json.Unmarshal(w.Body.Bytes(), &items) == nil && len(items) > 0 {
		var affected []json.RawMessage
		if json.Unmarshal(items[0]["affectedIssues"], &affected) == nil && len(affected) > 1 {
			slices.SortFunc(affected, func(a, b json.RawMessage) int { return bytes.Compare(a, b) })
			items[0]["affectedIssues"], _ = json.Marshal(affected)
			body, _ := json.Marshal(items)
			sorted := httptest.NewRecorder()
			sorted.WriteHeader(w.Code)
			sorted.Body.Write(body)
			return sorted
		}
	}
	return w
}

type scaleP21IssueBatchCase struct {
	name   string
	opts   scaleP21TeamMoveOptions
	edit   func(*domain.Issue)
	setup  func(*testing.T)
	user   domain.User
	key    *domain.APIKey
	ids    []string
	update map[string]any
	stop   bool
	code   int
	check  func(*testing.T, scaleP21IssueUpdateOutcome)
}

func (f scaleP21IssueUpdateFixture) runBatchCases(t *testing.T, cases []scaleP21IssueBatchCase) {
	t.Helper()
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			single := scaleP21IssueUpdateCase{opts: test.opts, edit: test.edit, setup: test.setup, issue: f.triage.ID}
			f.scaleP21TriageWriterFixture.prepare = f.prepare(single)
			defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
			var before scaleP21IssueUpdateState
			outcomes := f.differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
				if !service.store.BoundedMetadataEnabled() {
					before = f.state(t)
				}
				return f.batch(service, test.user, test.key, test.ids, test.update, test.stop)
			})
			legacy := outcomes[0]
			t.Logf("legacy status %d", legacy.Code)
			if test.code != 0 && legacy.Code != test.code {
				t.Fatalf("legacy status %d, want %d: %s", legacy.Code, test.code, legacy.Body)
			}
			if legacy.Code != http.StatusOK {
				// A failed batch writes nothing at all.
				if !reflect.DeepEqual(before.Records, legacy.State.Records) || !reflect.DeepEqual(before.Content, legacy.State.Content) ||
					len(legacy.State.Events) != 0 || len(legacy.Webhooks) != 0 {
					t.Fatal("legacy failed batch was not rolled back")
				}
			}
			if test.check != nil {
				test.check(t, legacy)
			}
		})
	}
}

func TestScaleP21IssueBatchDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	owner, admin, member, guest, sub := f.data.Viewer, f.principals[1].user, f.principals[2].user, f.principals[3].user, f.principals[4].user
	selected := &domain.APIKey{ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}
	pair := []string{f.triage.ID, f.visible.ID}
	mixed := []string{f.triage.ID, f.hidden.ID}
	subscribe := func(issue *domain.Issue) {
		issue.SubscriberIDs = []string{owner.ID, admin.ID, member.ID, guest.ID, sub.ID, f.former.ID}
	}
	// SLA rules for the public team, three earlier SLA events (the next is
	// numbered 3) and triage on the public team.
	sla := func(t *testing.T) {
		at := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
		if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.issue_batch_sla", "", nil, func(data *domain.Bootstrap) error {
			data.Settings["sla"] = map[string]any{"enabled": true}
			data.SLARules = append(data.SLARules,
				domain.SLARule{ID: "ib-sla-public", Name: "Public", Enabled: true, TeamIDs: []string{f.public}, TargetMinutes: 60, PauseStatuses: []string{"backlog"}},
				domain.SLARule{ID: "ib-sla-parent", Name: "Parent", Enabled: true, TeamIDs: []string{"tm-parent"}, TargetMinutes: 30})
			data.IssueSLAs = append(data.IssueSLAs, domain.IssueSLA{ID: "ib-issue-sla", IssueID: f.visible.ID, RuleID: "ib-sla-public", StartedAt: at, DueAt: at.Add(time.Hour), Status: "active"})
			for index := range 3 {
				data.SLAEvents = append(data.SLAEvents, domain.SLAEvent{ID: fmt.Sprintf("ib-event-%d", index), IssueID: "ib-other", SLAID: "ib-other", Type: "started", CreatedAt: at})
			}
			settings := data.TeamSettings[f.public]
			settings.TeamID, settings.TriageEnabled = f.public, true
			data.TeamSettings[f.public] = settings
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	// One fixed update: a new CRDT document has a random client ID.
	update, _ := base64.StdEncoding.DecodeString(mentioningCollaborationState(f.former.ID))
	queued := func(t *testing.T) {
		if _, err := f.repository.AppendDocumentCollaborationUpdate(t.Context(), f.data.Workspace.URLKey, store.DocumentCollaborationUpdate{
			ID: "ib-update-1", DocumentID: "document_content_" + f.visible.ID, ClientID: "ib-client", Data: update, CreatedAt: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatal(err)
		}
	}
	editorState := func(ids ...string) string {
		raw, _ := json.Marshal(mentioningDocument("Batch ", ids...))
		return string(raw)
	}
	// The new SLA's started event follows the three stored ones.
	slaEvents := func(t *testing.T, outcome scaleP21IssueUpdateOutcome) {
		rows := f.rows(t, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='slaEvents' ORDER BY collection_order`, f.data.Workspace.URLKey)
		numbered := false
		for _, row := range rows {
			var event domain.SLAEvent
			if json.Unmarshal([]byte(row[0].(string)), &event) == nil && strings.HasSuffix(event.ID, "_3") && strings.HasPrefix(event.ID, "sla_event_") {
				numbered = true
			}
		}
		if !numbered {
			t.Fatalf("legacy SLA event numbering not observed in %d rows", len(rows))
		}
	}
	f.runBatchCases(t, []scaleP21IssueBatchCase{
		{name: "priority-pair", edit: subscribe, user: owner, ids: pair, update: map[string]any{"priority": 2, "estimate": 3}, code: http.StatusOK},
		{name: "duplicate-ids", user: owner, ids: []string{f.triage.ID, f.triage.ID}, update: map[string]any{"priority": 1}, code: http.StatusOK},
		{name: "noop", user: owner, ids: pair, update: map[string]any{"priority": 0}, code: 0},
		{name: "state-with-family", edit: subscribe, user: owner, ids: pair, update: map[string]any{"stateId": "state_todo"}, code: http.StatusOK},
		{name: "state-done-auto-close", edit: subscribe, user: owner, ids: []string{f.triage.ID}, update: map[string]any{"stateId": "state_done"}, code: 0},
		{name: "triage-and-sla", edit: subscribe, setup: sla, user: owner, ids: pair, update: map[string]any{"stateId": "state_todo", "priority": 1}, code: http.StatusOK, check: slaEvents},
		{name: "sla-priority-only", setup: sla, user: owner, ids: pair, update: map[string]any{"priority": 3}, code: http.StatusOK},
		{name: "labels", edit: subscribe, user: owner, ids: pair, update: map[string]any{"labelIds": []string{"triage-l-public"}}, code: http.StatusOK},
		{name: "assignee-and-subscribers", user: owner, ids: pair, update: map[string]any{"assigneeId": member.ID, "subscriberIds": []string{admin.ID, sub.ID}}, code: http.StatusOK},
		{name: "former-assignee-rolls-back", user: owner, ids: pair, update: map[string]any{"assigneeId": f.former.ID}, code: http.StatusBadRequest},
		{name: "description-mentions", edit: subscribe, user: owner, ids: pair, update: map[string]any{"description": "Batch @" + member.Name}, code: http.StatusOK},
		{name: "description-state-former-new", user: owner, ids: pair, update: map[string]any{"descriptionState": editorState(f.former.ID)}, code: http.StatusBadRequest},
		{name: "description-state-former-queued-second", setup: queued, user: owner, ids: []string{f.visible.ID}, update: map[string]any{"descriptionState": editorState(f.former.ID)}, code: http.StatusOK},
		{name: "description-state-former-queued-one-of-two", setup: queued, user: owner, ids: pair, update: map[string]any{"descriptionState": editorState(f.former.ID)}, code: http.StatusBadRequest},
		{name: "parent", user: owner, ids: []string{f.visible.ID}, update: map[string]any{"parentId": ""}, code: http.StatusOK},
		{name: "cycle", user: owner, ids: pair, update: map[string]any{"cycleId": "iu-cycle-public"}, code: http.StatusOK},
		{name: "project", user: owner, ids: pair, update: map[string]any{"projectId": "triage-p-public"}, code: http.StatusOK},
		{name: "archive", user: owner, ids: pair, update: map[string]any{"archived": true}, code: http.StatusOK},
		{name: "mixed-team-label-rolls-back", user: owner, ids: mixed, update: map[string]any{"labelIds": []string{"tm-l-bug"}}, code: http.StatusBadRequest},
		{name: "mixed-team-priority", user: owner, ids: mixed, update: map[string]any{"priority": 4}, code: http.StatusOK},
		{name: "move-pair-parent", opts: scaleP21TeamMoveOptions{project: "triage-p-public"}, edit: subscribe, user: owner, ids: pair, update: map[string]any{"teamId": "tm-parent"}, code: http.StatusOK},
		{name: "move-mixed-sub-team", opts: scaleP21TeamMoveOptions{project: "tm-p-cross"}, edit: subscribe, user: admin, ids: mixed, update: map[string]any{"teamId": "tm-sub", "stateId": "tmp-started"}, code: 0},
		{name: "move-triage-target", opts: scaleP21TeamMoveOptions{triageTarget: true}, edit: subscribe, user: owner, ids: pair, update: map[string]any{"teamId": "tm-triage"}, code: http.StatusOK},
		{name: "move-into-private-by-member", edit: subscribe, user: admin, ids: pair, update: map[string]any{"teamId": f.private}, code: http.StatusOK},
		{name: "move-into-private-by-outsider", user: owner, ids: pair, update: map[string]any{"teamId": f.private}, code: http.StatusForbidden},
		{name: "move-by-guest", user: guest, ids: pair, update: map[string]any{"teamId": "tm-parent"}, code: http.StatusForbidden},
		{name: "move-selected-key", user: owner, key: selected, ids: pair, update: map[string]any{"teamId": "tm-parent"}, code: http.StatusForbidden},
		{name: "move-with-sla", setup: sla, edit: subscribe, user: owner, ids: pair, update: map[string]any{"teamId": "tm-parent", "priority": 2}, code: http.StatusOK},
		{name: "missing-issue-rolls-back", user: owner, ids: []string{f.triage.ID, "ib-missing"}, update: map[string]any{"priority": 1}, code: 0},
		{name: "cancelled", user: owner, ids: pair, update: map[string]any{"priority": 1}, stop: true, code: 0},
	})
}

func TestScaleP21IssueBatchContentStateDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	state := mentioningCollaborationState(f.principals[2].user.ID)
	f.runBatchCases(t, []scaleP21IssueBatchCase{
		{name: "content-state", user: f.data.Viewer, ids: []string{f.triage.ID, f.visible.ID}, update: map[string]any{"contentState": state}, code: http.StatusOK},
	})
}

// Concurrent batches over overlapping issues serialize: each bounded
// outcome equals one sequential legacy order.
func TestScaleP21IssueBatchConcurrent(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	owner, admin, member := f.data.Viewer, f.principals[1].user, f.principals[2].user
	triageOff := func(t *testing.T) {
		if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.issue_batch_triage_off", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.FeatureFlags["triage-intelligence"] = false
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	test := scaleP21IssueUpdateCase{issue: f.triage.ID, setup: triageOff, edit: func(issue *domain.Issue) { issue.SubscriberIDs = []string{member.ID} }}
	f.scaleP21TriageWriterFixture.prepare = f.prepare(test)
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	first := func(service *server) *httptest.ResponseRecorder {
		return f.batch(service, owner, nil, []string{f.triage.ID, f.visible.ID}, map[string]any{"teamId": "tm-parent"}, false)
	}
	second := func(service *server) *httptest.ResponseRecorder {
		return f.batch(service, admin, nil, []string{f.visible.ID, f.triage.ID}, map[string]any{"priority": 2, "assigneeId": member.ID}, false)
	}
	expected := map[string]bool{}
	for _, order := range [][2]func(*server) *httptest.ResponseRecorder{{first, second}, {second, first}} {
		got, started := f.run(t, "legacy", func(service *server) *httptest.ResponseRecorder {
			for _, call := range order {
				if w := call(service); w.Code != http.StatusOK {
					t.Fatalf("legacy batch: %d %s", w.Code, w.Body.String())
				}
			}
			return nil
		})
		got.Webhooks, got.Realtime = scaleP21IssueUpdateSorted(t, got.Webhooks, started), scaleP21IssueUpdateSorted(t, got.Realtime, started)
		expected[scaleP21IssueUpdateNormalize(t, got, started)] = true
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
					t.Errorf("concurrent batches failed: %v", codes)
				}
				return nil
			})
			got.Webhooks, got.Realtime = scaleP21IssueUpdateSorted(t, got.Webhooks, started), scaleP21IssueUpdateSorted(t, got.Realtime, started)
			if !expected[scaleP21IssueUpdateNormalize(t, got, started)] {
				t.Fatalf("%s attempt %d: concurrent batches match no sequential legacy order", source, attempt)
			}
		}
	}
}

// Inline labels and documents (duplicate IDs): a batch move remaps labels
// by first match and rescopes every attached copy.
func TestScaleP21IssueBatchInlineCollections(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	owner := f.data.Viewer
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	inline := func(t *testing.T) {
		restore := store.ForceFullMutationsForTesting(true)
		defer restore()
		if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.issue_batch_inline", "", nil, func(data *domain.Bootstrap) error {
			data.Labels = append(data.Labels,
				domain.IssueLabel{ID: "ib-dup-label", Name: "crash", Scope: "tm-parent", CreatedAt: at},
				domain.IssueLabel{ID: "ib-dup-label", Name: "Crash copy", Scope: "tm-parent", CreatedAt: at})
			for index := range data.Documents {
				if data.Documents[index].ID == "tm-doc-b" {
					data.Documents = append(data.Documents, data.Documents[index])
					break
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"labels", "documents"} {
			if rows := f.rows(t, `SELECT record_key FROM workspace_metadata_records WHERE workspace_key=? AND field='`+field+`'`, f.data.Workspace.URLKey); len(rows) != 0 {
				t.Fatalf("%s not stored inline: %d rows", field, len(rows))
			}
		}
	}
	f.runBatchCases(t, []scaleP21IssueBatchCase{
		{name: "inline-move", setup: inline, user: owner, ids: []string{f.triage.ID, f.visible.ID}, update: map[string]any{"teamId": "tm-parent"}, code: http.StatusOK},
		{name: "inline-priority", setup: inline, user: owner, ids: []string{f.triage.ID}, update: map[string]any{"priority": 2}, code: http.StatusOK},
	})
}

// scaleP21IssueBatchBulk adds issues to the fixture's public team so a
// batch names size issues (with the triage and visible issues), and
// returns their IDs. Triage runs are off: the batch writer is measured.
func scaleP21IssueBatchBulk(t *testing.T, f scaleP21IssueUpdateFixture, size int) []string {
	t.Helper()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ids := []string{f.triage.ID, f.visible.ID}
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.issue_batch_bulk", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.FeatureFlags["triage-intelligence"] = false
		team := domain.Team{ID: f.public, Key: f.public}
		for _, existing := range data.Teams {
			if existing.ID == f.public {
				team = existing
			}
		}
		for index := range size - len(ids) {
			id := fmt.Sprintf("ib-bulk-%04d", index)
			ids = append(ids, id)
			data.Issues = append(data.Issues, domain.Issue{ID: id, Identifier: fmt.Sprintf("%s-%d", team.Key, 5000+index), Number: 5000 + index, Version: 1,
				Title: "Bulk " + id, Team: team, State: domain.WorkflowState{ID: "state_backlog", Name: "Backlog", Type: "backlog"}, Creator: f.data.Viewer,
				SubscriberIDs: []string{f.principals[2].user.ID}, CreatedAt: at, UpdatedAt: at})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	full, ok := f.repository.BootstrapFor(f.data.Workspace.URLKey)
	if !ok {
		t.Fatal("workspace missing")
	}
	var err error
	if f.baseline, err = json.Marshal(full); err != nil {
		t.Fatal(err)
	}
	return ids
}

func scaleP21IssueBatchArchive(f scaleP21IssueUpdateFixture, id string) func(*testing.T) {
	return func(t *testing.T) {
		at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		if err := f.repository.MutateWorkspace(store.WithIssueRecordMutations(t.Context(), id), f.data.Workspace.URLKey, "scale_p21.issue_batch_archived", id, nil, func(data *domain.Bootstrap) error {
			issue, err := issueByID(data, id)
			if err != nil {
				return err
			}
			issue.ArchivedAt = &at
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// A 500-issue batch commits, and one failing issue rolls the whole batch
// back, byte for byte like the legacy writer.
func TestScaleP21IssueBatchLarge(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	ids := scaleP21IssueBatchBulk(t, f, 500)
	f.runBatchCases(t, []scaleP21IssueBatchCase{
		{name: "500-priority", user: f.data.Viewer, ids: ids, update: map[string]any{"priority": 2}, code: http.StatusOK},
		{name: "500-one-archived-rolls-back", setup: scaleP21IssueBatchArchive(f, ids[len(ids)-1]), user: f.data.Viewer, ids: ids, update: map[string]any{"priority": 2}, code: http.StatusBadRequest},
	})
}

// A full 1000-issue request preserves legacy commit and rollback behavior.
func TestScaleP21IssueBatchThousandAtomic(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	ids := scaleP21IssueBatchBulk(t, f, 1000)
	f.runBatchCases(t, []scaleP21IssueBatchCase{
		{name: "commit", user: f.data.Viewer, ids: ids, update: map[string]any{"priority": 2}, code: http.StatusOK, check: func(t *testing.T, got scaleP21IssueUpdateOutcome) {
			if len(got.State.Events) != 1 {
				t.Fatalf("committed batch published %d events", len(got.State.Events))
			}
		}},
		{name: "one-archived", setup: scaleP21IssueBatchArchive(f, ids[len(ids)-1]), user: f.data.Viewer, ids: ids, update: map[string]any{"priority": 2}, code: http.StatusBadRequest},
	})
}

// boundedLegacyIssueWrites rewrites every write under /api/issues to the
// issue records routes before dispatch: PATCH /api/issues/{id} is served by
// updateIssueRecord (whose event payload is the input; updateIssue would
// emit {changes, issue}) and POST /api/issues/batch by the scoped
// batchUpdate. updateIssue's own binding is never reached.
func TestScaleP21LegacyIssueWritesReachRecordRoutes(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(t.TempDir() + "/routes.db")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	s := &server{store: repo, uploadPath: t.TempDir()}
	host := httptest.NewServer(newHandler(s))
	defer host.Close()
	client := authClient(t)
	authRequest[domain.AuthSession](t, client, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", 200)
	issue := authRequest[domain.Issue](t, client, "POST", host.URL+"/api/issues", map[string]any{"title": "Route pin"}, "test-workspace", 201)
	var mu sync.Mutex
	var events []domain.DomainEvent
	repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	})
	authRequest[domain.Issue](t, client, "PATCH", host.URL+"/api/issues/"+issue.ID, map[string]any{"title": "Route pin edited"}, "test-workspace", http.StatusOK)
	authRequest[[]domain.Issue](t, client, "POST", host.URL+"/api/issues/batch", map[string]any{"issueIds": []string{issue.ID}, "update": map[string]any{"priority": 2}}, "test-workspace", http.StatusOK)
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 || events[0].Type != "issue.updated" || string(events[0].Payload) != `{"title":"Route pin edited"}` ||
		events[1].Type != "issue.batch_updated" || string(events[1].Payload) != fmt.Sprintf(`{"issueIds":[%q],"update":{"priority":2}}`, issue.ID) {
		for _, event := range events {
			t.Log(event.Type, string(event.Payload))
		}
		t.Fatal("legacy issue writes did not reach the issue records handlers")
	}
}
