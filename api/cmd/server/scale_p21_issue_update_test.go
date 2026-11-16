package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// PATCH /api/issue-records/{id} through the bounded UpdateIssueRecord and
// the bounded destination checks, compared with the legacy writer and the
// paged projection: plain edits, markdown/structured/legacy-editor
// descriptions with mentions and former-member checks against the previous
// state and queued collaboration updates, team moves (labels, states,
// documents, children, projects, numbering and notification audience),
// destination project/cycle/assignee checks, private teams, guests and
// credentials, cancellation, concurrent writers and the stale-close/archive
// automation. Legacy, warm and SQL-reloaded runs must agree byte for byte
// except generated IDs and clocks.

var scaleP21IssueUpdateCollaboration = regexp.MustCompile(`collab_\d+_[0-9a-f]{16}`)

type scaleP21IssueUpdateState struct {
	scaleP21TeamMoveState
	Collaboration [][]any
}

type scaleP21IssueUpdateOutcome struct {
	Code                int
	Body                json.RawMessage
	State               scaleP21IssueUpdateState
	Webhooks, Realtime  []json.RawMessage
	Installed, Reloaded json.RawMessage
}

type scaleP21IssueUpdateFixture struct {
	scaleP21TeamMoveFixture
	former domain.User
}

type scaleP21IssueUpdateCase struct {
	name string
	opts scaleP21TeamMoveOptions
	// edit adjusts the moved issue after the team-move seed.
	edit  func(*domain.Issue)
	setup func(*testing.T)
	user  domain.User
	key   *domain.APIKey
	issue string
	body  map[string]any
	stop  bool
	code  int
	// check inspects the legacy outcome (every source equals it).
	check func(*testing.T, scaleP21IssueUpdateOutcome)
}

func scaleP21IssueUpdateSetup(t *testing.T) scaleP21IssueUpdateFixture {
	t.Helper()
	f := scaleP21IssueUpdateFixture{scaleP21TeamMoveFixture: scaleP21TeamMoveSetup(t)}
	repo, workspace := f.repository, f.data.Workspace.URLKey
	_, f.former = scaleP03Join(t, f.scaleP03PlatformFixture, "former-member", idgen.New("iu-former-")+"@example.test", "member", f.public)
	if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, f.former.ID); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.issue_update_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Cycles = append(data.Cycles,
			domain.Cycle{ID: "iu-cycle-public", TeamID: f.public, Number: 1, Name: "Public", Status: "current", StartsAt: at, EndsAt: at.AddDate(1, 0, 0), CreatedAt: at, UpdatedAt: at},
			domain.Cycle{ID: "iu-cycle-private", TeamID: f.private, Number: 1, Name: "Private", Status: "current", StartsAt: at, EndsAt: at.AddDate(1, 0, 0), CreatedAt: at, UpdatedAt: at},
			domain.Cycle{ID: "iu-cycle-parent", TeamID: "tm-parent", Number: 1, Name: "Parent", Status: "current", StartsAt: at, EndsAt: at.AddDate(1, 0, 0), CreatedAt: at, UpdatedAt: at})
		if data.CycleSettings == nil {
			data.CycleSettings = map[string]domain.CycleSettings{}
		}
		data.CycleSettings[f.public] = domain.CycleSettings{Enabled: true}
		data.CycleSettings["tm-parent"] = domain.CycleSettings{Enabled: true, AutoAddStarted: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	full, ok := repo.BootstrapFor(workspace)
	if !ok {
		t.Fatal("issue update fixture workspace missing")
	}
	var err error
	if f.baseline, err = json.Marshal(full); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f scaleP21IssueUpdateFixture) state(t *testing.T) scaleP21IssueUpdateState {
	t.Helper()
	return scaleP21IssueUpdateState{
		scaleP21TeamMoveState: f.scaleP21TeamMoveFixture.state(t),
		Collaboration:         f.rows(t, `SELECT document_id,update_id,client_id,update_data FROM document_collaboration_updates WHERE workspace_key=? ORDER BY document_id,update_id`, f.data.Workspace.URLKey),
	}
}

// prepare seeds the team-move fixture, then the case's issue edit and setup.
func (f scaleP21IssueUpdateFixture) prepare(test scaleP21IssueUpdateCase) func(*testing.T) {
	seed := f.scaleP21TeamMoveFixture.prepare(test.opts)
	return func(t *testing.T) {
		seed(t)
		repo, workspace := f.repository, f.data.Workspace.URLKey
		f.exec(t, `DELETE FROM document_collaboration_updates WHERE workspace_key=?`, workspace)
		if test.edit != nil {
			id := test.issue
			if err := repo.MutateWorkspace(store.WithIssueRecordMutations(t.Context(), id), workspace, "scale_p21.issue_update_issue", id, nil, func(data *domain.Bootstrap) error {
				issue, err := issueByID(data, id)
				if err != nil {
					return err
				}
				test.edit(issue)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		if test.setup != nil {
			test.setup(t)
		}
	}
}

func (f scaleP21IssueUpdateFixture) patch(service *server, user domain.User, key *domain.APIKey, id string, body any, cancelled bool) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPatch, "/api/issue-records/"+id, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
	r.SetPathValue("workspaceKey", f.data.Workspace.URLKey)
	r.SetPathValue("workspace", f.data.Workspace.URLKey)
	r.SetPathValue("id", id)
	ctx := context.WithValue(r.Context(), authUserContextKey{}, user)
	ctx = store.ContextWithActor(ctx, user)
	if key != nil {
		ctx = context.WithValue(ctx, apiKeyContextKey{}, *key)
	}
	if cancelled {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		cancel()
	}
	r = r.WithContext(ctx)
	r, closeOutput := beginRequestResourceReads(r)
	defer closeOutput()
	w := httptest.NewRecorder()
	service.updateIssueRecord(w, r)
	return w
}

func (f scaleP21IssueUpdateFixture) run(t *testing.T, source string, call func(*server) *httptest.ResponseRecorder) (scaleP21IssueUpdateOutcome, time.Time) {
	t.Helper()
	started := time.Now().UTC()
	service := f.reset(t, source)
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
	// A test view shares the database of the store that opened it, whose
	// commit hook publishes notification.created through its own sink.
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
		t.Fatalf("%s: bounded issue update decoded full metadata (%d) or retained work (%d)", source,
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
	if installed, reloaded := nul.Replace(string(got.Installed)), nul.Replace(string(got.Reloaded)); installed != reloaded {
		t.Fatalf("%s: installed image differs from the SQL reload: %s", source, scaleP21M2Diff(installed, reloaded))
	}
	return got, started
}

func scaleP21IssueUpdateNormalize(t *testing.T, value any, started time.Time) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	// Collaboration generations are generated IDs too.
	raw = scaleP21IssueUpdateCollaboration.ReplaceAll(raw, []byte("collab_generated"))
	return scaleP21TriageNormalize(t, json.RawMessage(raw), started)
}

// scaleP21IssueUpdateSorted orders concurrent publications by their
// normalized envelope; each envelope is still compared byte for byte.
func scaleP21IssueUpdateSorted(t *testing.T, items []json.RawMessage, started time.Time) []json.RawMessage {
	t.Helper()
	var normalized []string
	for _, item := range items {
		normalized = append(normalized, scaleP21IssueUpdateNormalize(t, item, started))
	}
	slices.Sort(normalized)
	result := []json.RawMessage{}
	for _, item := range normalized {
		result = append(result, json.RawMessage(item))
	}
	return result
}

func (f scaleP21IssueUpdateFixture) differential(t *testing.T, name string, call func(*server) *httptest.ResponseRecorder) []scaleP21IssueUpdateOutcome {
	t.Helper()
	var normalized []string
	var outcomes []scaleP21IssueUpdateOutcome
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		got, started := f.run(t, source, call)
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

func (f scaleP21IssueUpdateFixture) runCases(t *testing.T, cases []scaleP21IssueUpdateCase) {
	t.Helper()
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if test.issue == "" {
				test.issue = f.triage.ID
			}
			f.scaleP21TriageWriterFixture.prepare = f.prepare(test)
			defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
			outcomes := f.differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
				return f.patch(service, test.user, test.key, test.issue, test.body, test.stop)
			})
			legacy := outcomes[0]
			t.Logf("legacy status %d", legacy.Code)
			if dir := os.Getenv("FLOW_ISSUE_UPDATE_DUMP"); dir != "" && legacy.Code >= 500 {
				_ = os.WriteFile(dir+"/failed-"+test.name+".json", []byte(scaleP21IssueUpdateNormalize(t, legacy, time.Time{})), 0o600)
			}
			if test.code != 0 && legacy.Code != test.code {
				t.Fatalf("legacy status %d, want %d: %s", legacy.Code, test.code, legacy.Body)
			}
			if legacy.Code != http.StatusOK && len(legacy.State.Events) != 0 {
				t.Fatalf("legacy failure published %d events", len(legacy.State.Events))
			}
			if test.check != nil {
				test.check(t, legacy)
			}
			if team, move := test.body["teamId"].(string); move && legacy.Code == http.StatusOK {
				issue := scaleP21TeamMoveIssue(t, scaleP21TeamMoveOutcome{State: legacy.State.scaleP21TeamMoveState}, test.issue)
				if issue.Team.ID != team || team != f.public && (len(issue.PreviousIdentifiers) == 0 || len(legacy.State.Aliases) == 0) {
					t.Fatalf("legacy move: team %s previous %v aliases %v", issue.Team.ID, issue.PreviousIdentifiers, legacy.State.Aliases)
				}
				// The installed documents (rows, or the inline field).
				var installed struct{ Documents []domain.Document }
				if err := json.Unmarshal(legacy.Installed, &installed); err != nil {
					t.Fatal(err)
				}
				scoped := 0
				for _, document := range installed.Documents {
					if document.IssueID == test.issue && document.IssueTeamID == team {
						scoped++
					}
				}
				if team != f.public && test.issue == f.triage.ID && scoped == 0 {
					t.Fatal("legacy move did not rescope the attached documents")
				}
			}
		})
	}
}

func scaleP21IssueUpdateNotifications(outcome scaleP21IssueUpdateOutcome) map[string]bool {
	recipients := map[string]bool{}
	for _, row := range outcome.State.Content {
		if row[0] != "notification" {
			continue
		}
		var notification domain.Notification
		if json.Unmarshal([]byte(row[3].(string)), &notification) == nil {
			recipients[notification.RecipientID] = true
		}
	}
	return recipients
}

func TestScaleP21IssueUpdateFieldsDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	owner, admin, member, guest, sub := f.data.Viewer, f.principals[1].user, f.principals[2].user, f.principals[3].user, f.principals[4].user
	selected := &domain.APIKey{ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}
	subscribe := func(issue *domain.Issue) {
		issue.SubscriberIDs = []string{admin.ID, member.ID, guest.ID, sub.ID, f.former.ID}
	}
	previousState := mentioningCollaborationState(f.former.ID)
	withState := func(issue *domain.Issue) {
		subscribe(issue)
		issue.DocumentContent = &domain.DocumentContent{ID: "document_content_" + issue.ID, Version: 2, Content: "Earlier", ContentState: previousState}
	}
	queued := func(t *testing.T) {
		update, _ := base64.StdEncoding.DecodeString(mentioningCollaborationState(f.former.ID))
		if _, err := f.repository.AppendDocumentCollaborationUpdate(t.Context(), f.data.Workspace.URLKey, store.DocumentCollaborationUpdate{
			ID: "iu-update-1", DocumentID: "document_content_" + f.triage.ID, ClientID: "iu-client", Data: update, CreatedAt: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatal(err)
		}
	}
	editorState := func(ids ...string) string {
		raw, _ := json.Marshal(mentioningDocument("Editor ", ids...))
		return string(raw)
	}
	f.runCases(t, []scaleP21IssueUpdateCase{
		{name: "plain-fields", edit: subscribe, user: owner, code: http.StatusOK,
			body: map[string]any{"title": "Retitled", "priority": 2, "estimate": 5, "dueDate": "2026-12-01", "sortOrder": 7.5, "icon": "Bug"}},
		{name: "plain-fields-member", edit: subscribe, user: member, code: http.StatusOK, body: map[string]any{"title": "Member edit", "priority": 1}},
		{name: "noop", user: owner, code: http.StatusOK, body: map[string]any{"title": "Triage list bounded differential"}},
		{name: "description-markdown-mentions", edit: subscribe, user: owner, code: http.StatusOK,
			body: map[string]any{"description": "Ping @" + member.Name + " and @" + strings.ReplaceAll(admin.DisplayName, " ", "") + " and @" + f.former.Email}},
		{name: "description-data-mention", edit: subscribe, user: owner, code: http.StatusOK,
			body: map[string]any{"description": "Rich", "descriptionData": mentioningDocument("Rich ", member.ID, sub.ID)}},
		{name: "description-data-former-new", edit: subscribe, user: owner, code: http.StatusBadRequest,
			body: map[string]any{"description": "Rich", "descriptionData": mentioningDocument("Rich ", f.former.ID)}},
		{name: "description-state", edit: subscribe, user: owner, code: http.StatusOK,
			body: map[string]any{"descriptionState": editorState(member.ID)}},
		{name: "description-state-former-new", edit: subscribe, user: owner, code: http.StatusBadRequest,
			body: map[string]any{"descriptionState": editorState(f.former.ID)}},
		{name: "description-state-former-previous-state", edit: withState, user: owner, code: http.StatusOK,
			body: map[string]any{"descriptionState": editorState(f.former.ID, member.ID)}},
		{name: "description-state-former-queued-update", edit: subscribe, setup: queued, user: owner, code: http.StatusOK,
			body: map[string]any{"descriptionState": editorState(f.former.ID)}},
		{name: "description-and-state-with-document-updates", edit: withState, setup: queued, user: owner, code: http.StatusOK,
			body: map[string]any{"description": "Both", "descriptionState": editorState(member.ID), "documentUpdateIds": []string{"iu-update-1"}}},
		{name: "description-state-invalid-json", edit: subscribe, user: owner, code: http.StatusOK,
			body: map[string]any{"descriptionState": "{not json"}},
		{name: "assignee-member", edit: subscribe, user: owner, code: http.StatusOK, body: map[string]any{"assigneeId": member.ID}},
		{name: "assignee-outside-workspace", user: owner, code: http.StatusForbidden, body: map[string]any{"assigneeId": "iu-nobody"}},
		{name: "assignee-former", user: owner, code: http.StatusBadRequest, body: map[string]any{"assigneeId": f.former.ID}},
		{name: "assignee-guest-sees-team", user: guest, code: 0, body: map[string]any{"assigneeId": member.ID}},
		{name: "subscribers-with-former", user: owner, code: http.StatusBadRequest, body: map[string]any{"subscriberIds": []string{member.ID, f.former.ID}}},
		{name: "project-valid", edit: subscribe, user: owner, code: http.StatusOK, body: map[string]any{"projectId": "triage-p-public"}},
		{name: "project-private-member", user: member, code: http.StatusForbidden, body: map[string]any{"projectId": "triage-p-private"}},
		{name: "project-missing", user: owner, code: http.StatusForbidden, body: map[string]any{"projectId": "iu-missing-project"}},
		{name: "project-selected-key-outside", user: owner, key: selected, code: http.StatusForbidden, body: map[string]any{"projectId": "triage-p-private"}},
		{name: "project-selected-key-inside", user: owner, key: selected, code: 0, body: map[string]any{"projectId": "triage-p-public"}},
		{name: "project-clear", opts: scaleP21TeamMoveOptions{project: "triage-p-public"}, user: owner, code: http.StatusOK, body: map[string]any{"projectId": ""}},
		{name: "cycle-valid", edit: subscribe, user: member, code: http.StatusOK, body: map[string]any{"cycleId": "iu-cycle-public"}},
		{name: "cycle-private-member", user: member, code: http.StatusForbidden, body: map[string]any{"cycleId": "iu-cycle-private"}},
		{name: "cycle-private-admin-member", user: admin, code: 0, body: map[string]any{"cycleId": "iu-cycle-private"}},
		{name: "cycle-missing", user: owner, code: http.StatusForbidden, body: map[string]any{"cycleId": "iu-missing-cycle"}},
		{name: "cycle-other-team", user: owner, code: http.StatusBadRequest, body: map[string]any{"cycleId": "iu-cycle-parent"}},
		{name: "cycle-selected-key", user: owner, key: selected, code: 0, body: map[string]any{"cycleId": "iu-cycle-public"}},
		{name: "labels", edit: subscribe, user: owner, code: http.StatusOK, body: map[string]any{"labelIds": []string{"triage-l-public", "tm-l-area"}}},
		{name: "state-with-family", edit: subscribe, user: owner, code: http.StatusOK, body: map[string]any{"stateId": f.triage.State.ID, "priority": 3}},
		{name: "parent", user: owner, code: 0, body: map[string]any{"parentId": f.hidden.ID}},
		{name: "guest-edit", user: guest, code: 0, body: map[string]any{"title": "Guest edit"}},
		{name: "outsider-private-issue", user: member, issue: f.hidden.ID, code: 0, body: map[string]any{"title": "Hidden edit"}},
		{name: "selected-key-edit", user: owner, key: selected, code: 0, body: map[string]any{"title": "Key edit"}},
		{name: "expected-version", user: owner, code: 0, body: map[string]any{"title": "Versioned", "expectedVersion": 1}},
		{name: "expected-document-version-conflict", user: owner, code: http.StatusConflict, body: map[string]any{"description": "Stale", "expectedDocumentVersion": 7}},
		{name: "cancelled", user: owner, stop: true, code: 0, body: map[string]any{"title": "Cancelled"}},
		{name: "missing-issue", user: owner, issue: "iu-missing-issue", code: http.StatusNotFound, body: map[string]any{"title": "Missing"}},
		{name: "by-identifier", user: owner, issue: f.triage.Identifier, code: 0, body: map[string]any{"title": "By identifier"}},
	})
}

func TestScaleP21IssueUpdateTeamMoveDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	owner, admin, member, guest, sub := f.data.Viewer, f.principals[1].user, f.principals[2].user, f.principals[3].user, f.principals[4].user
	selected := &domain.APIKey{ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}
	subscribe := func(issue *domain.Issue) {
		issue.SubscriberIDs = []string{owner.ID, admin.ID, member.ID, guest.ID, sub.ID, f.former.ID}
		issue.Assignee = &member
	}
	onTriage := func(t *testing.T) {
		if err := f.repository.SetTeamMembership(t.Context(), f.data.Workspace.ID, "tm-triage", owner.ID, "member", true); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := f.repository.SetTeamMembership(context.Background(), f.data.Workspace.ID, "tm-triage", owner.ID, "member", false); err != nil {
				t.Error(err)
			}
		})
	}
	// The destination's SLA rule wins over a workspace rule whose overdue
	// SLA the issue holds: no breach is recorded for the moved issue.
	sla := func(t *testing.T) {
		due := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
		if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.issue_update_sla", "", nil, func(data *domain.Bootstrap) error {
			data.Settings["sla"] = map[string]any{"enabled": true}
			data.SLARules = append(data.SLARules,
				domain.SLARule{ID: "iu-sla-parent", Name: "Parent", Enabled: true, TeamIDs: []string{"tm-parent"}, TargetMinutes: 60},
				domain.SLARule{ID: "iu-sla-workspace", Name: "Workspace", Enabled: true, TargetMinutes: 60})
			data.IssueSLAs = append(data.IssueSLAs, domain.IssueSLA{ID: "iu-issue-sla", IssueID: f.triage.ID, RuleID: "iu-sla-workspace", StartedAt: due.Add(-time.Hour), DueAt: due, Status: "active"})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	breaches := func(want bool) func(*testing.T, scaleP21IssueUpdateOutcome) {
		return func(t *testing.T, outcome scaleP21IssueUpdateOutcome) {
			found := false
			for _, row := range outcome.State.Content {
				found = found || row[0] == "activity" && strings.Contains(row[3].(string), `"issue.sla_breached"`)
			}
			if found != want {
				t.Fatalf("legacy SLA breach recorded %t, want %t", found, want)
			}
		}
	}
	cases := []scaleP21IssueUpdateCase{
		{name: "sla-destination-rule", edit: subscribe, setup: sla, user: owner, code: http.StatusOK, body: map[string]any{"teamId": "tm-parent"}, check: breaches(false)},
		{name: "sla-same-team-breach", edit: subscribe, setup: sla, user: owner, code: http.StatusOK, body: map[string]any{"title": "Breached"}, check: breaches(true)},
		{name: "parent-team-incompatible-project", opts: scaleP21TeamMoveOptions{project: "triage-p-public"}, edit: subscribe, user: owner, code: http.StatusOK,
			body: map[string]any{"teamId": "tm-parent"}},
		{name: "sub-team-compatible-project", opts: scaleP21TeamMoveOptions{project: "tm-p-cross"}, edit: subscribe, user: owner, code: http.StatusOK,
			body: map[string]any{"teamId": "tm-sub"}},
		{name: "team-without-settings-or-labels", edit: subscribe, user: owner, code: http.StatusOK, body: map[string]any{"teamId": "tm-bare"}},
		{name: "triage-target-viewer-outside", opts: scaleP21TeamMoveOptions{triageTarget: true}, edit: subscribe, user: owner, code: http.StatusOK,
			body: map[string]any{"teamId": "tm-triage"}},
		{name: "triage-target-viewer-on-team", opts: scaleP21TeamMoveOptions{triageTarget: true}, edit: subscribe, setup: onTriage, user: owner, code: http.StatusOK,
			body: map[string]any{"teamId": "tm-triage"}},
		{name: "merged-update-activity", opts: scaleP21TeamMoveOptions{mergeAt: time.Now().UTC(), project: "triage-p-public"}, edit: subscribe, user: owner, code: http.StatusOK,
			body: map[string]any{"teamId": "tm-parent"}},
		{name: "move-with-state-labels-cycle", edit: subscribe, user: owner, code: http.StatusOK,
			body: map[string]any{"teamId": "tm-parent", "stateId": "tmp-started", "labelIds": []string{"tm-l-parent-bug", "tm-l-shared"}, "cycleId": "iu-cycle-parent", "title": "Moved and edited"}},
		{name: "move-with-description", edit: subscribe, user: owner, code: http.StatusOK,
			body: map[string]any{"teamId": "tm-parent", "description": "Moved @" + member.Name}},
		{name: "move-with-description-state", edit: subscribe, user: owner, code: http.StatusOK,
			body: map[string]any{"teamId": "tm-parent", "descriptionState": `{"type":"doc","content":[]}`}},
		{name: "move-into-private-by-member", edit: subscribe, user: admin, code: http.StatusOK, body: map[string]any{"teamId": f.private}},
		{name: "move-into-private-sub-team", edit: subscribe, user: sub, code: 0, body: map[string]any{"teamId": f.child}},
		{name: "move-into-private-by-outsider", edit: subscribe, user: owner, code: http.StatusForbidden, body: map[string]any{"teamId": f.private}},
		{name: "move-by-member", edit: subscribe, user: member, code: http.StatusOK, body: map[string]any{"teamId": "tm-parent"}},
		{name: "move-by-guest", user: guest, code: 0, body: map[string]any{"teamId": "tm-parent"}},
		{name: "move-selected-key", user: owner, key: selected, code: http.StatusForbidden, body: map[string]any{"teamId": "tm-parent"}},
		{name: "move-ghost-team", user: owner, code: http.StatusForbidden, body: map[string]any{"teamId": "iu-ghost-team"}},
		{name: "same-team", edit: subscribe, user: owner, code: http.StatusOK, body: map[string]any{"teamId": f.public, "title": "Same team"}},
		{name: "move-with-family-state", edit: subscribe, user: owner, code: http.StatusOK,
			body: map[string]any{"teamId": "tm-parent", "stateId": "tmp-done"}},
		{name: "move-the-child", user: owner, issue: f.visible.ID, code: http.StatusOK, body: map[string]any{"teamId": "tm-parent"}},
		{name: "move-cancelled", user: owner, stop: true, code: 0, body: map[string]any{"teamId": "tm-parent"}},
	}
	f.runCases(t, cases)
	// The audience of a move: only subscribers who can open the issue in its
	// new team hear about it (checked on the legacy outcome above through
	// the differential; here the private move excludes the outsiders).
	t.Run("private-move-audience", func(t *testing.T) {
		test := scaleP21IssueUpdateCase{name: "audience", edit: subscribe, user: admin, issue: f.triage.ID, body: map[string]any{"teamId": f.private}}
		f.scaleP21TriageWriterFixture.prepare = f.prepare(test)
		defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
		outcomes := f.differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
			return f.patch(service, test.user, nil, test.issue, test.body, false)
		})
		recipients := scaleP21IssueUpdateNotifications(outcomes[0])
		if outcomes[0].Code != http.StatusOK || recipients[guest.ID] || recipients[f.former.ID] || !recipients[sub.ID] {
			t.Fatalf("legacy private move audience %d %v", outcomes[0].Code, recipients)
		}
	})
}

// A CRDT snapshot keeps the bounded writer's coverage boundary: the write
// is refused before any change, with a typed error. Cancellation writes
// nothing in either mode.
func TestScaleP21IssueUpdateStoreBoundaries(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	state := mentioningCollaborationState(f.principals[2].user.ID)
	f.runCases(t, []scaleP21IssueUpdateCase{
		{name: "content-state", user: f.data.Viewer, issue: f.triage.ID, body: map[string]any{"contentState": state}, code: http.StatusOK},
	})
	// A cancelled bounded move fails like the legacy one, writing nothing.
	team := "tm-parent"
	var errs []string
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		service := f.reset(t, source)
		before := f.state(t)
		ctx, cancel := context.WithCancel(store.WithIssueRenumbering(store.ContextWithActor(t.Context(), f.data.Viewer)))
		cancel()
		_, err := service.store.UpdateIssueRecord(ctx, f.data.Workspace.URLKey, f.triage.ID, nil,
			store.IssueMutationScope{Payload: domain.IssueUpdateInput{TeamID: &team}}, func(*domain.Bootstrap, *domain.Issue) error {
				t.Fatal("callback ran")
				return nil
			})
		if err == nil {
			t.Fatalf("%s: cancelled move succeeded", source)
		}
		if errs = append(errs, err.Error()); errs[len(errs)-1] != errs[0] {
			t.Fatalf("%s: cancelled move error %q, legacy %q", source, errs[len(errs)-1], errs[0])
		}
		if after := f.state(t); !reflect.DeepEqual(before, after) || service.store.MetadataWorkUsedForTesting() != 0 {
			t.Fatalf("%s: cancelled move changed state or retained work", source)
		}
	}
}

// The stale-close and archive automation passes an untyped payload: its
// state change, activity and subscriber notifications, the closed issue's
// project progress and the archive read the same closure as a typed update.
func TestScaleP21IssueUpdateSchedulerDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	owner, admin, member, guest := f.data.Viewer, f.principals[1].user, f.principals[2].user, f.principals[3].user
	old := time.Date(2026, 1, 5, 6, 7, 8, 0, time.UTC)
	settings := func(stale bool) func(*testing.T) {
		return func(t *testing.T) {
			if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.issue_update_automation", "", nil, func(data *domain.Bootstrap) error {
				current := data.TeamSettings[f.public]
				current.TeamID, current.AutoCloseStale, current.StaleMonths, current.AutoArchiveMonths = f.public, stale, 1, 1
				data.TeamSettings[f.public] = current
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			// The archive candidate: the completed sub-issue.
			id := f.visible.ID
			if err := f.repository.MutateWorkspace(store.WithIssueRecordMutations(t.Context(), id), f.data.Workspace.URLKey, "scale_p21.issue_update_archive", id, nil, func(data *domain.Bootstrap) error {
				issue, err := issueByID(data, id)
				if err != nil {
					return err
				}
				issue.State = domain.WorkflowState{ID: "state_done", Name: "Done", Type: "completed"}
				issue.CompletedAt, issue.UpdatedAt = &old, old
				issue.SubscriberIDs = []string{owner.ID, member.ID}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	stale := func(issue *domain.Issue) {
		issue.UpdatedAt = old
		issue.SubscriberIDs = []string{owner.ID, admin.ID, member.ID, guest.ID, f.former.ID}
		issue.Assignee = &member
	}
	for _, test := range []scaleP21IssueUpdateCase{
		{name: "stale-close-and-archive", opts: scaleP21TeamMoveOptions{project: "triage-p-public"}, edit: stale, setup: settings(true)},
		{name: "archive-only", edit: stale, setup: settings(false)},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.issue = f.triage.ID
			f.scaleP21TriageWriterFixture.prepare = f.prepare(test)
			defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
			now := time.Now().UTC()
			outcomes := f.differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				if err := service.maintainTeamSettings(t.Context(), f.data.Workspace.URLKey, now); err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return w
				}
				writeJSON(w, http.StatusNoContent, map[string]bool{"done": true})
				return w
			})
			legacy := outcomes[0]
			if legacy.Code != http.StatusNoContent || len(legacy.State.Events) == 0 {
				t.Fatalf("legacy automation %d events %d: %s", legacy.Code, len(legacy.State.Events), legacy.Body)
			}
			if test.name == "stale-close-and-archive" && len(scaleP21IssueUpdateNotifications(legacy)) == 0 {
				t.Fatal("legacy stale close notified nobody")
			}
		})
	}
}

// Concurrent updates of one issue serialize: each bounded outcome equals
// one sequential legacy order.
func TestScaleP21IssueUpdateConcurrentSameIssue(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	owner, admin, member := f.data.Viewer, f.principals[1].user, f.principals[2].user
	// The triage run after each response reads the issue as the other
	// write left it; this test compares the writers alone.
	triageOff := func(t *testing.T) {
		if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.issue_update_triage_off", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.FeatureFlags["triage-intelligence"] = false
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	test := scaleP21IssueUpdateCase{issue: f.triage.ID, opts: scaleP21TeamMoveOptions{project: "tm-p-cross"}, setup: triageOff, edit: func(issue *domain.Issue) {
		issue.SubscriberIDs = []string{member.ID}
	}}
	f.scaleP21TriageWriterFixture.prepare = f.prepare(test)
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	first := func(service *server) *httptest.ResponseRecorder {
		return f.patch(service, owner, nil, f.triage.ID, map[string]any{"teamId": "tm-parent", "title": "Moved concurrently"}, false)
	}
	second := func(service *server) *httptest.ResponseRecorder {
		return f.patch(service, admin, nil, f.triage.ID, map[string]any{"priority": 1, "description": "Concurrent @" + member.Name}, false)
	}
	expected := map[string]bool{}
	for _, order := range [][2]func(*server) *httptest.ResponseRecorder{{first, second}, {second, first}} {
		got, started := f.run(t, "legacy", func(service *server) *httptest.ResponseRecorder {
			for _, call := range order {
				if w := call(service); w.Code != http.StatusOK {
					t.Fatalf("legacy update: %d %s", w.Code, w.Body.String())
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
					t.Errorf("concurrent updates failed: %v", codes)
				}
				return nil
			})
			got.Webhooks, got.Realtime = scaleP21IssueUpdateSorted(t, got.Webhooks, started), scaleP21IssueUpdateSorted(t, got.Realtime, started)
			if state := scaleP21IssueUpdateNormalize(t, got, started); !expected[state] {
				if dir := os.Getenv("FLOW_ISSUE_UPDATE_DUMP"); dir != "" {
					index := 0
					for item := range expected {
						_ = os.WriteFile(dir+"/concurrent-legacy-"+string(rune('a'+index))+".json", []byte(item), 0o600)
						index++
					}
					_ = os.WriteFile(dir+"/concurrent-"+source+".json", []byte(state), 0o600)
				}
				t.Fatalf("%s attempt %d: concurrent updates match no sequential legacy order", source, attempt)
			}
		}
	}
}

// A workspace whose labels and documents are stored inline (duplicate
// IDs): a move remaps labels by first match and re-derives every copy of
// the attached documents' scope, persisting the inline fields exactly.
func TestScaleP21IssueUpdateInlineCollections(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	owner, member := f.data.Viewer, f.principals[2].user
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	inline := func(t *testing.T) {
		restore := store.ForceFullMutationsForTesting(true)
		defer restore()
		if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.issue_update_inline", "", nil, func(data *domain.Bootstrap) error {
			// Duplicate IDs keep both fields inline.
			data.Labels = append(data.Labels,
				domain.IssueLabel{ID: "iu-dup-label", Name: "crash", Scope: "tm-parent", CreatedAt: at},
				domain.IssueLabel{ID: "iu-dup-label", Name: "Crash copy", Scope: "tm-parent", CreatedAt: at})
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
	f.runCases(t, []scaleP21IssueUpdateCase{
		{name: "inline-move", edit: func(issue *domain.Issue) { issue.SubscriberIDs = []string{member.ID} }, setup: inline, user: owner, code: http.StatusOK,
			body: map[string]any{"teamId": "tm-parent"}},
		{name: "inline-plain", setup: inline, user: owner, code: http.StatusOK, body: map[string]any{"title": "Inline plain"}},
	})
}

// Without authentication the destination checks read the complete
// unprojected workspace: the bounded handler looks the IDs up in the
// installed collections.
func TestScaleP21IssueUpdateWithoutAuthDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	owner, member := f.data.Viewer, f.principals[2].user
	for _, test := range []scaleP21IssueUpdateCase{
		{name: "private-project", body: map[string]any{"projectId": "triage-p-private"}, code: http.StatusOK},
		{name: "missing-project", body: map[string]any{"projectId": "iu-missing-project"}, code: http.StatusForbidden},
		{name: "private-cycle", body: map[string]any{"cycleId": "iu-cycle-private"}, code: http.StatusBadRequest},
		{name: "missing-cycle", body: map[string]any{"cycleId": "iu-missing-cycle"}, code: http.StatusForbidden},
		{name: "assignee", body: map[string]any{"assigneeId": member.ID}, code: http.StatusOK},
		{name: "missing-assignee", body: map[string]any{"assigneeId": "iu-nobody"}, code: http.StatusForbidden},
		{name: "former-assignee", body: map[string]any{"assigneeId": f.former.ID}, code: http.StatusBadRequest},
		{name: "move-private", body: map[string]any{"teamId": f.private}, code: http.StatusOK},
		{name: "move-parent-with-cycle", body: map[string]any{"teamId": "tm-parent", "cycleId": "iu-cycle-parent"}, code: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.issue = f.triage.ID
			f.scaleP21TriageWriterFixture.prepare = f.prepare(test)
			defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
			outcomes := f.differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
				service.authDisabled = true
				return f.patch(service, owner, nil, test.issue, test.body, false)
			})
			if outcomes[0].Code != test.code {
				t.Fatalf("legacy status %d, want %d: %s", outcomes[0].Code, test.code, outcomes[0].Body)
			}
		})
	}
}
