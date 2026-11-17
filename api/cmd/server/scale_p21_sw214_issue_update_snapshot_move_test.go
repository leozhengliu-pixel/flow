package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scaleP21SW214Member struct {
	ID, Role, Status string
}

func scaleP21SW214ReloadSource(t *testing.T, f scaleP21IssueUpdateFixture, repo *store.SQLiteStore) {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	name := func() string {
		t.Helper()
		if !repo.BoundedMetadataEnabled() {
			data, found := repo.WorkspaceSettingsMetadata(workspace)
			if !found {
				t.Fatal("reload workspace missing")
			}
			return data.Workspace.Name
		}
		read, found, err := repo.BeginInstalledMetadataRead(t.Context(), workspace)
		if err != nil || !found {
			t.Fatal("reload installed source", err)
		}
		defer read.Close()
		var raw strings.Builder
		if err := read.StreamFieldJSON(t.Context(), "workspace", &raw); err != nil {
			t.Fatal(err)
		}
		var data domain.Workspace
		if err := json.Unmarshal([]byte(raw.String()), &data); err != nil {
			t.Fatal(err)
		}
		return data.Name
	}
	before := name()
	rows := f.rows(t, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace)
	if len(rows) != 1 {
		t.Fatal("reload root row missing")
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rows[0][0].(string)), &root); err != nil {
		t.Fatal(err)
	}
	var identity domain.Workspace
	if err := json.Unmarshal(root["workspace"], &identity); err != nil {
		t.Fatal(err)
	}
	identity.Name = "SW214 SQL reload " + workspace
	if identity.Name == before {
		t.Fatal("reload witness did not change the installed source")
	}
	var err error
	if root["workspace"], err = json.Marshal(identity); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE workspace_states SET data=? WHERE workspace_key=?`, raw, workspace)
	f.exec(t, `UPDATE workspace_cache_revisions SET revision=revision+1 WHERE workspace_key=? AND kind='metadata'`, workspace)
	decodes := repo.WorkspaceMetadataDecodes()
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal("actual SQL reload", err)
	}
	if name() != identity.Name {
		t.Fatal("actual SQL reload did not replace the installed logical source")
	}
	if repo.BoundedMetadataEnabled() && repo.WorkspaceMetadataDecodes() != decodes {
		t.Fatal("bounded SQL reload used full hydration")
	}
	if !repo.BoundedMetadataEnabled() && repo.WorkspaceMetadataDecodes() <= decodes {
		t.Fatal("literal SQL reload did not run its decoder")
	}
}

func scaleP21SW214Apply(data *domain.Bootstrap, issue *domain.Issue, input domain.IssueUpdateInput) error {
	changes, err := applyUpdate(data, issue, input)
	if err != nil {
		return err
	}
	issue.UpdatedAt = time.Now().UTC()
	applySLARules(data, issue, issue.UpdatedAt)
	activity := appendIssueUpdateActivity(data, issue.ID, changes)
	appendIssueNotifications(data, *issue, activity, nil)
	return nil
}

func scaleP21SW214Prepare(t *testing.T, f scaleP21IssueUpdateFixture) {
	t.Helper()
	owner, recipient := f.data.Viewer, f.principals[2].user
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw214_snapshots", "", nil, func(data *domain.Bootstrap) error {
		data.NextIssueNumber = 9000
		state := domain.WorkflowState{ID: "sw214-source-todo", TeamID: f.public, Name: "Todo", Type: "unstarted", Position: 2}
		data.States = append(data.States, state)
		// SQL still has the recipient active and no tm-triage membership
		// for this viewer. The literal no-actor/unmarked branch keeps this.
		data.Members = []domain.WorkspaceMember{
			{User: recipient, Role: "member", Status: "suspended", JoinedAt: at},
			{User: recipient, Role: "admin", Status: "active", JoinedAt: at},
			{User: owner, Role: "admin", Status: "active", JoinedAt: at},
		}
		data.TeamMembers = []domain.TeamMember{
			{UserID: owner.ID, TeamID: "tm-triage", Role: "member", JoinedAt: at, Origin: "manual"},
			{UserID: recipient.ID, TeamID: "tm-triage", Role: "lead", JoinedAt: at, Managed: true, ManagedSource: "stored snapshot", Origin: "import"},
		}
		for index := range data.Issues {
			if data.Issues[index].ID == f.triage.ID {
				data.Issues[index].SubscriberIDs = []string{owner.ID, recipient.ID}
				data.Issues[index].State = state
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP21SW214IssueUpdateStoredMoveAudienceDifferential(t *testing.T) {
	for _, reload := range []bool{false, true} {
		name := "installed"
		if reload {
			name = "canonical-reloaded"
		}
		t.Run(name, func(t *testing.T) { scaleP21SW214StoredMoveDifferential(t, reload) })
	}
}

func scaleP21SW214StoredMoveDifferential(t *testing.T, reload bool) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	owner, recipient := f.data.Viewer, f.principals[2].user
	test := scaleP21IssueUpdateCase{
		opts:  scaleP21TeamMoveOptions{triageTarget: true, project: "tm-p-cross"},
		setup: func(t *testing.T) { scaleP21SW214Prepare(t, f) },
	}
	prepare := f.prepare(test)
	f.scaleP21TriageWriterFixture.prepare = func(t *testing.T) {
		prepare(t)
		if reload {
			if err := f.repository.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
				t.Fatal(err)
			}
		}
	}
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	for _, mode := range []struct {
		name          string
		actor, marker bool
	}{
		{"no-actor-renumbering", false, true},
		{"no-actor-unlocked", false, false},
		{"actor-unlocked", true, false},
	} {
		t.Run(mode.name, func(t *testing.T) {
			outcomes := f.differential(t, mode.name, func(service *server) *httptest.ResponseRecorder {
				if reload {
					scaleP21SW214ReloadSource(t, f, service.store)
				}
				ctx := t.Context()
				if mode.actor {
					ctx = store.ContextWithActor(ctx, owner)
				}
				if mode.marker {
					ctx = store.WithIssueRenumbering(ctx)
				}
				if service.store.BoundedMetadataEnabled() {
					read, err := service.store.BeginMetadataJSONRead(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer read.Close()
					ctx = read.Context(ctx)
				}
				team := "tm-triage"
				input := domain.IssueUpdateInput{TeamID: &team}
				var captured []scaleP21SW214Member
				var capturedTeams []domain.TeamMember
				next := 0
				updated, err := service.store.UpdateIssueRecord(ctx, f.data.Workspace.URLKey, f.triage.ID, nil,
					store.IssueMutationScope{Payload: input}, func(data *domain.Bootstrap, issue *domain.Issue) error {
						next = data.NextIssueNumber
						captured, capturedTeams = nil, nil
						for _, member := range data.Members {
							if member.User.ID == owner.ID || member.User.ID == recipient.ID {
								captured = append(captured, scaleP21SW214Member{member.User.ID, member.Role, member.Status})
							}
						}
						for _, member := range data.TeamMembers {
							if member.UserID == owner.ID || member.UserID == recipient.ID {
								capturedTeams = append(capturedTeams, member)
							}
						}
						return scaleP21SW214Apply(data, issue, input)
					})
				w := httptest.NewRecorder()
				if err != nil {
					issueRecordsError(w, err)
				} else {
					writeJSON(w, http.StatusOK, map[string]any{"issue": updated, "members": captured, "teams": capturedTeams, "next": next})
				}
				return w
			})
			var result struct {
				Issue   domain.Issue          `json:"issue"`
				Members []scaleP21SW214Member `json:"members"`
				Teams   []domain.TeamMember   `json:"teams"`
				Next    int                   `json:"next"`
			}
			if err := json.Unmarshal(outcomes[0].Body, &result); err != nil {
				t.Fatal(err)
			}
			if outcomes[0].Code != http.StatusOK || result.Issue.Team.ID != "tm-triage" ||
				!reload && (result.Next != 9000 || result.Issue.Number != 9000) ||
				result.Issue.Number < result.Next || result.Issue.Number <= 0 {
				t.Fatalf("legacy stored move=%d next=%d issue=%+v body=%s", outcomes[0].Code, result.Next, result.Issue, outcomes[0].Body)
			}
			if len(result.Members) != 3 || result.Members[0].Status != "suspended" || result.Members[1].Status != "active" ||
				!slices.ContainsFunc(result.Teams, func(member domain.TeamMember) bool { return member.UserID == owner.ID && member.TeamID == "tm-triage" }) {
				t.Fatal("fixture did not preserve stale stored member snapshots", result.Members, result.Teams)
			}
			if result.Issue.State.ID == "tmt-triage" || scaleP21IssueUpdateNotifications(outcomes[0])[recipient.ID] {
				t.Fatal("legacy did not use stored triage membership and first suspended-member precedence")
			}
			sequence := scaleP21TeamMoveInt(t, outcomes[0].State.Sequence[0][0])
			if !mode.marker && sequence != f.sequence || mode.marker && sequence != int64(result.Issue.Number) {
				t.Fatal("unlocked/renumbered move changed sequence semantics", sequence, mode.marker)
			}
		})
	}
}

func TestScaleP21SW214IssueUpdateStalePeerRouteDifferential(t *testing.T) {
	for _, reload := range []bool{false, true} {
		name := "installed"
		if reload {
			name = "canonical-reloaded"
		}
		t.Run(name, func(t *testing.T) { scaleP21SW214StalePeerRouteDifferential(t, reload) })
	}
}

func scaleP21SW214StalePeerRouteDifferential(t *testing.T, reload bool) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	prepare := f.prepare(scaleP21IssueUpdateCase{
		opts:  scaleP21TeamMoveOptions{project: "tm-p-cross"},
		setup: func(t *testing.T) { scaleP21SW214Prepare(t, f) },
	})
	f.scaleP21TriageWriterFixture.prepare = func(t *testing.T) {
		prepare(t)
		if reload {
			if err := f.repository.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
				t.Fatal(err)
			}
		}
	}
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	owner := f.data.Viewer
	// A peer can leave the literal installed documents stale relative to SQL.
	// Compare both images to their corresponding oracle, without equating them.
	outcomes := f.scaleP21TriageWriterFixture.differential(t, "stale-peer-after-handler-read", func(service *server) *httptest.ResponseRecorder {
		if reload {
			scaleP21SW214ReloadSource(t, f, service.store)
		}
		priorEvents := map[any][]any{}
		for _, event := range f.state(t).Events {
			priorEvents[event[0]] = event
		}
		peer, err := store.OpenDatabase(f.reader.config)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		seenPage, moved, sequenceSQL := false, false, 0
		peerNumber := int(f.sequence) + 100
		if peerNumber >= 9000 {
			t.Fatal("stale peer fixture no longer separates snapshot and sequence numbers")
		}
		request := f.literalRequest(owner, nil, map[string]any{"teamId": f.public, "title": "SW214 stale same-team request"})
		request.Method, request.URL.Path = http.MethodPatch, "/api/issue-records/"+f.triage.ID
		request.Header.Set("Content-Type", "application/json")
		request.SetPathValue("id", f.triage.ID)
		request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(query string) {
			if strings.Contains(query, "issue_number_sequences") {
				sequenceSQL++
			}
			if !seenPage && strings.Contains(query, "SELECT i.data,") && strings.Contains(query, "FROM issue_records i") {
				seenPage = true
				return
			}
			if moved || !seenPage || !strings.Contains(query, "workspace_memberships") {
				return
			}
			// The page has been read; the handler will keep its same-team
			// decision. A different same-version replica moves the row.
			moved = true
			if err := peer.MutateWorkspace(t.Context(), f.data.Workspace.URLKey,
				"scale_p21.sw214_peer_number", "", nil, func(data *domain.Bootstrap) error {
					data.NextIssueNumber = peerNumber
					return nil
				}); err != nil {
				t.Fatal(err)
			}
			peerTeam := "tm-parent"
			input := domain.IssueUpdateInput{TeamID: &peerTeam}
			updated, err := peer.UpdateIssueRecord(store.WithIssueRenumbering(store.ContextWithActor(t.Context(), owner)),
				f.data.Workspace.URLKey, f.triage.ID, nil, store.IssueMutationScope{Payload: input},
				func(data *domain.Bootstrap, issue *domain.Issue) error { return scaleP21SW214Apply(data, issue, input) })
			if err != nil || updated.Team.ID != peerTeam || updated.Number != peerNumber {
				t.Fatal("same-version peer did not move the row", updated, err)
			}
		}))
		request, closeOutput := beginRequestResourceReads(request)
		defer closeOutput()
		w := httptest.NewRecorder()
		service.updateIssueRecord(w, request)
		if !seenPage || !moved {
			t.Fatal("stale-peer witness did not run strictly after the route's issue page")
		}
		if sequenceSQL != 0 {
			t.Fatal("incoming unmarked move touched or locked the issue sequence", sequenceSQL)
		}
		installed := f.installed(t, service.store)
		reloaded, err := store.OpenDatabase(f.reader.config)
		if err != nil {
			t.Fatal(err)
		}
		reloadedImage := f.installed(t, reloaded)
		if err := reloaded.Close(); err != nil {
			t.Fatal(err)
		}
		state := f.state(t)
		var events [][]any
		for _, event := range state.Events {
			if previous, found := priorEvents[event[0]]; found {
				if !reflect.DeepEqual(previous, event) {
					t.Fatal("stale-peer route changed a preexisting event")
				}
				delete(priorEvents, event[0])
			} else {
				events = append(events, event[1:])
			}
		}
		if len(priorEvents) != 0 {
			t.Fatal("stale-peer route deleted a preexisting event")
		}
		// Historical clocks precede this leg's interval. Compare those rows
		// literally above; only this leg's new events enter the differential.
		state.Events = events
		result := httptest.NewRecorder()
		writeJSON(result, w.Code, struct {
			Body                json.RawMessage
			State               scaleP21IssueUpdateState
			Installed, Reloaded json.RawMessage
		}{json.RawMessage(w.Body.Bytes()), state, installed, reloadedImage})
		return result
	})
	var snapshot struct {
		Body  domain.Issue
		State struct{ Sequence [][]int64 }
	}
	if err := json.Unmarshal(outcomes[0].Body, &snapshot); err != nil {
		t.Fatal(err)
	}
	issue := snapshot.Body
	wantNumber := 9000
	if reload {
		wantNumber = int(f.sequence) + 101
	}
	if outcomes[0].Code != http.StatusOK || issue.Team.ID != f.public || issue.Number != wantNumber ||
		issue.Title != "SW214 stale same-team request" || !slices.Contains(issue.PreviousIdentifiers, "TMP-"+strconv.Itoa(int(f.sequence)+100)) {
		t.Fatal("legacy stale request did not retain its unlocked snapshot move", outcomes[0].Code, issue, string(outcomes[0].Body))
	}
	if sequence := scaleP21TeamMoveInt(t, snapshot.State.Sequence[0][0]); sequence != f.sequence+100 {
		t.Fatal("incoming stale request advanced the peer's sequence", sequence)
	}
}
