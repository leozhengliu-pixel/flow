package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Triage team moves through the shared team-move kernel (accepted team
// suggestions and auto-applied team suggestions of generation) against the
// member snapshots the legacy scoped writer and its permission check use:
// an assignee who is not on the destination team, a suspended former member
// assignee and subscribers (duplicated), a private destination team the
// actor is or is not on, team membership that differs between the stored
// image and the live rows (changed after the snapshot was taken), membership
// removed concurrently with the move, an actor suspended after the snapshot,
// duplicate team suggestion rows and duplicate stored team member rows.
// Legacy, warm and SQL-reloaded runs agree byte for byte, and no run writes
// live membership into the stored members/teamMembers image.

func (f scaleP21IssueUpdateFixture) sw401TeamMember(t *testing.T, team, user string, present bool) {
	t.Helper()
	f.exec(t, `DELETE FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`, f.data.Workspace.ID, team, user)
	if present {
		f.exec(t, `INSERT INTO team_memberships (workspace_id,team_id,user_id,role,joined_at) VALUES (?,?,?,?,?)`,
			f.data.Workspace.ID, team, user, "member", "2026-01-02T03:04:05Z")
	}
}

// sw401LeaveConcurrently removes user's team membership row from another
// connection while a writer may hold the database: SQLite busy errors are
// retried, so the removal lands before, during or after the write.
func (f scaleP21IssueUpdateFixture) sw401LeaveConcurrently(team, user string) error {
	query := `DELETE FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`
	if f.reader.config.Driver == "postgres" || f.reader.config.Driver == "postgresql" {
		query = `DELETE FROM team_memberships WHERE workspace_id=$1 AND team_id=$2 AND user_id=$3`
	}
	var err error
	for range 2000 {
		if _, err = f.reader.reader.Exec(query, f.data.Workspace.ID, team, user); err == nil || !strings.Contains(err.Error(), "SQLITE_BUSY") {
			return err
		}
		time.Sleep(time.Millisecond)
	}
	return err
}

func (f scaleP21IssueUpdateFixture) sw401Status(t *testing.T, user, status string) {
	t.Helper()
	f.exec(t, `UPDATE workspace_memberships SET status=? WHERE workspace_id=? AND user_id=?`, status, f.data.Workspace.ID, user)
}

// sw401Image edits the stored image only (no membership rows change).
func (f scaleP21IssueUpdateFixture) sw401Image(t *testing.T, edit func(*domain.Bootstrap)) {
	t.Helper()
	restore := store.ForceFullMutationsForTesting(true)
	defer restore()
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw401_image", "", nil, func(data *domain.Bootstrap) error {
		edit(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW401Private(data *domain.Bootstrap) {
	settings := data.TeamSettings["tm-parent"]
	settings.Access = "private"
	data.TeamSettings["tm-parent"] = settings
}

func TestScaleP21SW401TriageTeamMoveMemberSnapshots(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	workspace := f.data.Workspace.URLKey
	owner, member := f.data.Viewer, f.principals[2].user
	former := f.former
	reset := func(t *testing.T) {
		for _, user := range []string{owner.ID, member.ID} {
			f.sw401TeamMember(t, "tm-parent", user, false)
			f.sw401Status(t, user, "active")
		}
	}
	t.Cleanup(func() { reset(t) })
	assignee := func(user domain.User, subscribers ...string) func(*domain.Issue) {
		return func(issue *domain.Issue) {
			copy := user
			issue.Assignee = &copy
			issue.SubscriberIDs = subscribers
		}
	}
	private := func(image, live []string) func(*testing.T) {
		return func(t *testing.T) {
			reset(t)
			f.sw401Image(t, func(data *domain.Bootstrap) {
				scaleP21SW401Private(data)
				for _, user := range image {
					data.TeamMembers = append(data.TeamMembers, domain.TeamMember{TeamID: "tm-parent", UserID: user, Role: "member"})
				}
			})
			for _, user := range live {
				f.sw401TeamMember(t, "tm-parent", user, true)
			}
		}
	}
	accept := func(user domain.User, suggestion string) func(*server) *httptest.ResponseRecorder {
		return func(service *server) *httptest.ResponseRecorder {
			return f.stateRequest(service, user, nil, f.triage.ID, suggestion, true, false)
		}
	}
	generate := func(service *server) *httptest.ResponseRecorder {
		r := f.literalRequest(owner, nil, nil)
		r, closeOutput := beginRequestResourceReads(r)
		defer closeOutput()
		results, err := service.generateTriageIntelligenceForIssues(r.Context(), workspace, []string{f.triage.ID})
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
	duplicates := func(t *testing.T) {
		reset(t)
		at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		f.sw401Image(t, func(data *domain.Bootstrap) {
			for _, item := range []domain.IssueSuggestion{
				{ID: "tm-s-parent", IssueID: f.triage.ID, Type: "team", State: "active", SuggestedTeamID: "tm-other"},
				{ID: "tm-s-parent", IssueID: f.triage.ID, Type: "team", State: "dismissed", SuggestedTeamID: "tm-parent"},
			} {
				item.StateChangedAt, item.CreatedAt, item.UpdatedAt = at, at, at
				data.IssueSuggestions = append(data.IssueSuggestions, item)
			}
			data.TeamMembers = append(data.TeamMembers,
				domain.TeamMember{TeamID: "tm-parent", UserID: member.ID, Role: "member"},
				domain.TeamMember{TeamID: "tm-parent", UserID: member.ID, Role: "admin"})
		})
	}
	for _, test := range []struct {
		name  string
		opts  scaleP21TeamMoveOptions
		edit  func(*domain.Issue)
		setup func(*testing.T)
		call  func(*server) *httptest.ResponseRecorder
		code  int
		// team is the legacy issue's team after the call ("" skips the check).
		team string
	}{
		{name: "assignee-not-destination-member", edit: assignee(member, member.ID, former.ID, member.ID), setup: reset,
			call: accept(owner, "tm-s-parent"), code: http.StatusOK, team: "tm-parent"},
		{name: "former-member-assignee", edit: assignee(former, former.ID, former.ID), setup: reset,
			call: accept(owner, "tm-s-parent"), code: http.StatusOK, team: "tm-parent"},
		{name: "member-actor-former-assignee", edit: assignee(former, member.ID), setup: reset,
			call: accept(member, "tm-s-parent"), code: http.StatusOK, team: "tm-parent"},
		{name: "private-destination-member", edit: assignee(member, former.ID), setup: private([]string{owner.ID, owner.ID}, []string{owner.ID}),
			call: accept(owner, "tm-s-parent"), code: http.StatusOK, team: "tm-parent"},
		{name: "private-destination-outsider", edit: assignee(member), setup: private(nil, nil),
			call: accept(owner, "tm-s-parent"), code: http.StatusForbidden},
		{name: "private-destination-member-actor", edit: assignee(former), setup: private([]string{member.ID}, []string{member.ID}),
			call: accept(member, "tm-s-parent"), code: http.StatusOK, team: "tm-parent"},
		{name: "private-destination-left-after-snapshot", edit: assignee(member), setup: private([]string{owner.ID}, nil),
			call: accept(owner, "tm-s-parent")},
		{name: "private-destination-joined-after-snapshot", edit: assignee(member), setup: private(nil, []string{owner.ID}),
			call: accept(owner, "tm-s-parent")},
		{name: "member-actor-left-after-snapshot", edit: assignee(member), setup: private([]string{member.ID}, nil),
			call: accept(member, "tm-s-parent")},
		{name: "actor-suspended-after-snapshot", edit: assignee(former), setup: func(t *testing.T) {
			reset(t)
			f.sw401Status(t, member.ID, "suspended")
		}, call: accept(member, "tm-s-parent")},
		{name: "duplicate-suggestions-and-team-members", edit: assignee(member, member.ID, member.ID), setup: duplicates,
			call: accept(member, "tm-s-parent"), code: http.StatusOK, team: "tm-parent"},
		{name: "generate-auto-move-former-assignee", opts: scaleP21TeamMoveOptions{auto: true, triageIssue: true, project: "tm-p-cross"},
			edit: assignee(former, former.ID, member.ID, former.ID), setup: reset, call: generate, code: http.StatusOK, team: "tm-parent"},
		{name: "generate-auto-move-private-destination", opts: scaleP21TeamMoveOptions{auto: true, triageIssue: true, project: "tm-p-cross"},
			edit: assignee(member, former.ID), setup: private([]string{member.ID}, nil), call: generate, code: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			f.scaleP21TriageWriterFixture.prepare = f.prepare(scaleP21IssueUpdateCase{opts: test.opts, edit: test.edit, setup: test.setup, issue: f.triage.ID})
			defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
			defer reset(t)
			outcomes := f.differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
				before := scaleP21SW185MemberImage(t, service.store, workspace)
				w := test.call(service)
				if after := scaleP21SW185MemberImage(t, service.store, workspace); after != before {
					t.Fatalf("bounded=%t replaced the stored member snapshots: %s", service.store.BoundedMetadataEnabled(), scaleP21M2Diff(before, after))
				}
				return w
			})
			legacy := outcomes[0]
			t.Logf("legacy status %d", legacy.Code)
			if test.code != 0 && legacy.Code != test.code {
				t.Fatalf("legacy status %d, want %d: %s", legacy.Code, test.code, legacy.Body)
			}
			if test.team != "" {
				issue := scaleP21TeamMoveIssue(t, scaleP21TeamMoveOutcome{State: legacy.State.scaleP21TeamMoveState}, f.triage.ID)
				if issue.Team.ID != test.team || issue.Assignee == nil {
					t.Fatalf("legacy issue team %s assignee %v", issue.Team.ID, issue.Assignee)
				}
			}
		})
	}
}

// The actor's membership of the private destination team is removed while
// the accepted move runs. Every bounded outcome equals the legacy outcome
// of the removal landing before the move (refused) or after it (moved): the
// writer itself never consults destination membership.
func TestScaleP21SW401TriageTeamMoveMembershipRemovedConcurrently(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	owner := f.data.Viewer
	t.Cleanup(func() { f.sw401TeamMember(t, "tm-parent", owner.ID, false) })
	seed := func(t *testing.T) {
		f.sw401Image(t, func(data *domain.Bootstrap) {
			scaleP21SW401Private(data)
			data.TeamMembers = append(data.TeamMembers, domain.TeamMember{TeamID: "tm-parent", UserID: owner.ID, Role: "member"})
		})
		f.sw401TeamMember(t, "tm-parent", owner.ID, true)
	}
	f.scaleP21TriageWriterFixture.prepare = f.prepare(scaleP21IssueUpdateCase{setup: seed, issue: f.triage.ID})
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	accept := func(service *server) *httptest.ResponseRecorder {
		return f.stateRequest(service, owner, nil, f.triage.ID, "tm-s-parent", true, false)
	}
	expected := map[string]int{}
	for _, removedFirst := range []bool{true, false} {
		got, started := f.run(t, "legacy", func(service *server) *httptest.ResponseRecorder {
			if removedFirst {
				f.sw401TeamMember(t, "tm-parent", owner.ID, false)
				return accept(service)
			}
			w := accept(service)
			f.sw401TeamMember(t, "tm-parent", owner.ID, false)
			return w
		})
		expected[scaleP21IssueUpdateNormalize(t, got, started)] = got.Code
	}
	if len(expected) != 2 {
		t.Fatal("legacy removal orders produced one outcome")
	}
	codes := map[int]bool{}
	for _, code := range expected {
		codes[code] = true
	}
	if !codes[http.StatusOK] || !codes[http.StatusForbidden] {
		t.Fatalf("legacy outcomes %v", codes)
	}
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		for attempt := range 4 {
			got, started := f.run(t, source, func(service *server) *httptest.ResponseRecorder {
				var wait sync.WaitGroup
				wait.Add(1)
				go func() {
					defer wait.Done()
					if attempt%2 == 1 {
						time.Sleep(time.Duration(attempt) * time.Millisecond)
					}
					if err := f.sw401LeaveConcurrently("tm-parent", owner.ID); err != nil {
						t.Error(err)
					}
				}()
				w := accept(service)
				wait.Wait()
				return w
			})
			if _, ok := expected[scaleP21IssueUpdateNormalize(t, got, started)]; !ok {
				t.Fatalf("%s attempt %d: concurrent removal matches no sequential legacy order (status %d): %s", source, attempt, got.Code, got.Body)
			}
		}
	}
}
