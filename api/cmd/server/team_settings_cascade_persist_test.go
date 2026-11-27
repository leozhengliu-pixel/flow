package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// PATCH /api/teams/{id}/settings cascades beyond the team's own settings:
// "apply to sub-teams" rewrites descendants' settings, a default status
// clears the flag on the other statuses (and materializes the team's own
// statuses, remapping its issues and issue templates), inheriting cycles
// completes and deletes the team's cycles (clearing the issues' cycle), a new
// parent remaps the issues to the inherited statuses and private access
// removes outsiders from the team's issues. Each case runs with bounded
// metadata off (the legacy image) and on (the route runs COLD) on copies of
// one seeded database: the responses and the stored rows must agree, and in
// both modes a fresh process reopening the database (legacy and bounded) must
// load the image the writing process holds (labels are compared as rows: the
// legacy image counts their issues).

const teamCascadeWorkspace = "test-workspace"

var teamCascadeFields = []string{"teams", "teamSettings", "states", "cycles", "cycleSettings", "issueTemplates"}

func teamCascadeSettings(id, parent string) domain.TeamSettings {
	return domain.TeamSettings{
		TeamID: id, ParentTeamID: parent, Timezone: "Etc/UTC", EstimateType: "notUsed", Access: "public",
		MembershipRestriction: "open", SettingsPermission: "allMembers", LabelPermission: "allMembers",
		TemplatePermission: "allMembers", AgentSkillPermission: "allMembers", LoopPermission: "allMembers",
		MemberPermission: "allMembers", StaleMonths: 6, AutoArchiveMonths: 6, ProgressOrder: "first", TriageAction: "none",
		ResolvedSummaries: true, InheritWorkflowStatuses: parent != "", InheritCycles: false,
		SlackNotifications: map[string]bool{}, PRAutomations: map[string]string{}, ReleaseAutomations: []domain.TeamAutomationRule{},
		TriageRules: []domain.TeamAutomationRule{}, AgentSkills: []domain.TeamAgentSkill{}, AgentConnectors: []domain.TeamAgentConnector{},
	}
}

// teamCascadeSeed writes the cascade fixture and returns the database path.
func teamCascadeSeed(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "seed.db")
	repo, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	created := day.Add(-30 * 24 * time.Hour)
	if err := repo.MutateWorkspace(t.Context(), teamCascadeWorkspace, "test.team_cascade_seed", "", nil, func(data *domain.Bootstrap) error {
		admin, member := data.Users[0], data.Users[1]
		team := func(id, key string) domain.Team {
			return domain.Team{ID: id, Name: key + " team", Key: key, CreatedAt: &created, UpdatedAt: &created}
		}
		retired := team("team_ret", "RET")
		retired.RetiredAt = &created
		data.Teams = append(data.Teams, team("team_par", "PAR"), team("team_chi", "CHI"), team("team_sol", "SOL"), team("team_cyc", "CYC"), team("team_pen", "PEN"), team("team_cye", "CYE"), retired)
		data.TeamSettings["team_ret"] = teamCascadeSettings("team_ret", "team_chi")
		data.TeamSettings["team_test"] = teamCascadeSettings("team_test", "")
		data.TeamSettings["team_par"] = teamCascadeSettings("team_par", "")
		data.TeamSettings["team_chi"] = teamCascadeSettings("team_chi", "team_par")
		data.TeamSettings["team_sol"] = teamCascadeSettings("team_sol", "")
		data.TeamSettings["team_cyc"] = teamCascadeSettings("team_cyc", "team_par")
		data.TeamSettings["team_pen"] = teamCascadeSettings("team_pen", "")
		data.TeamSettings["team_cye"] = teamCascadeSettings("team_cye", "team_pen")
		for _, owned := range []string{"team_par", "team_sol", "team_pen"} {
			prefix := strings.TrimPrefix(owned, "team_")
			data.States = append(data.States,
				domain.WorkflowState{ID: prefix + "_backlog", TeamID: owned, Name: "Backlog", Color: "#bbb", Type: "backlog", Position: 0, Default: true},
				domain.WorkflowState{ID: prefix + "_todo", TeamID: owned, Name: "Todo", Color: "#ccc", Type: "unstarted", Position: 1},
				domain.WorkflowState{ID: prefix + "_doing", TeamID: owned, Name: "Doing", Color: "#ddd", Type: "started", Position: 2},
				domain.WorkflowState{ID: prefix + "_done", TeamID: owned, Name: "Done", Color: "#eee", Type: "completed", Position: 3},
			)
		}
		data.CycleSettings["team_par"] = domain.CycleSettings{Enabled: false, DurationWeeks: 2, Capacity: 3}
		data.CycleSettings["team_cyc"] = domain.CycleSettings{Enabled: true, DurationWeeks: 1, Capacity: 3}
		data.CycleSettings["team_pen"] = domain.CycleSettings{Enabled: true, DurationWeeks: 1, UpcomingCount: 1, Capacity: 3}
		data.CycleSettings["team_cye"] = domain.CycleSettings{Enabled: true, DurationWeeks: 2, Capacity: 3}
		cycle := func(id, teamID string, number int, starts time.Time, status string) domain.Cycle {
			return domain.Cycle{ID: id, Number: number, Name: fmt.Sprint("Cycle ", number), TeamID: teamID, StartsAt: starts, EndsAt: starts.Add(week), Status: status, CreatedAt: created, UpdatedAt: created}
		}
		data.Cycles = append(data.Cycles,
			cycle("cyc_current", "team_cyc", 4, day, "current"),
			cycle("cyc_upcoming", "team_cyc", 5, day.Add(week), "upcoming"),
			cycle("cyc_done", "team_cyc", 3, day.Add(-week), "completed"),
			cycle("pen_current", "team_pen", 8, day, "current"),
			cycle("pen_upcoming", "team_pen", 9, day.Add(week), "upcoming"),
			cycle("cye_current", "team_cye", 2, day, "current"),
			cycle("cye_stray", "team_cye", 3, day.Add(3*week), "upcoming"),
		)
		data.IssueTemplates = append(data.IssueTemplates,
			domain.IssueTemplate{ID: "template_tst", TeamID: "team_test", Name: "Bug", StateID: "state_todo", LabelIDs: []string{}, Creator: admin, CreatedAt: created, UpdatedAt: created},
		)
		data.TeamMembers = append(data.TeamMembers, domain.TeamMember{TeamID: "team_test", UserID: admin.ID, Role: "owner", JoinedAt: created})
		globalState := func(id string) domain.WorkflowState {
			for _, state := range data.States {
				if state.ID == id {
					return state
				}
			}
			t.Fatalf("state %s missing", id)
			return domain.WorkflowState{}
		}
		teamOf := func(id string) domain.Team {
			for _, item := range data.Teams {
				if item.ID == id {
					return item
				}
			}
			t.Fatalf("team %s missing", id)
			return domain.Team{}
		}
		number := 100
		issue := func(teamID, stateID string, change func(*domain.Issue)) {
			number++
			owner := teamOf(teamID)
			value := domain.Issue{
				ID: fmt.Sprintf("issue_cascade_%d", number), Identifier: fmt.Sprintf("%s-%d", owner.Key, number), Number: number,
				Title: fmt.Sprint("Cascade issue ", number), Team: owner, State: globalState(stateID), Creator: admin,
				CreatedAt: created, UpdatedAt: created, SubscriberIDs: []string{}, Labels: []domain.IssueLabel{},
			}
			if change != nil {
				change(&value)
			}
			data.Issues = append(data.Issues, value)
		}
		issue("team_test", "state_todo", func(issue *domain.Issue) {
			issue.Assignee = &member
			issue.SubscriberIDs = []string{admin.ID, member.ID}
		})
		issue("team_test", "state_progress", func(issue *domain.Issue) { issue.Assignee = &admin })
		issue("team_sol", "sol_todo", nil)
		issue("team_sol", "sol_doing", nil)
		issue("team_cyc", "par_todo", func(issue *domain.Issue) { id := "cyc_upcoming"; issue.CycleID = &id })
		issue("team_cyc", "par_todo", func(issue *domain.Issue) { id := "cyc_current"; issue.CycleID = &id })
		issue("team_cye", "pen_todo", func(issue *domain.Issue) { id := "cye_stray"; issue.CycleID = &id })
		issue("team_par", "par_todo", nil)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// teamCascadeCopy copies the seeded database for one run.
func teamCascadeCopy(t *testing.T, seed string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "flow.db")
	db, err := sql.Open("sqlite", seed)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`VACUUM INTO ?`, target); err != nil {
		t.Fatal(err)
	}
	return target
}

func teamCascadeRows(t *testing.T, path, query string, args ...any) [][]any {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(query, args...)
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

type teamCascadeOutcome struct {
	Code     int
	Body     json.RawMessage
	Metadata [][]any
	Issues   [][]any
	Events   [][]any
	Image    map[string]json.RawMessage
}

// teamCascadeRun patches the copy's team with bounded metadata off or on and
// checks that a fresh process (legacy and bounded) loads the image the
// writing process holds.
func teamCascadeRun(t *testing.T, seed, bounded, teamID string, body map[string]any) teamCascadeOutcome {
	t.Helper()
	path := teamCascadeCopy(t, seed)
	owner, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	service := &server{store: owner, uploadPath: t.TempDir(), authDisabled: true}
	if bounded == "on" {
		view, err := owner.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		service.store = view
	}
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPatch, "/api/teams/"+teamID+"/settings?workspace="+teamCascadeWorkspace, strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Workspace-Key", teamCascadeWorkspace)
	w := httptest.NewRecorder()
	newHandler(service).ServeHTTP(w, r)
	got := teamCascadeOutcome{Code: w.Code, Body: json.RawMessage(w.Body.String())}
	got.Image = oauthMetadataImage(t, service.store, teamCascadeWorkspace, teamCascadeFields...)
	got.Metadata = teamCascadeRows(t, path, `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('teams','teamSettings','states','cycles','cycleSettings','issueTemplates','labels') ORDER BY field,collection_order,record_key`, teamCascadeWorkspace)
	got.Issues = teamCascadeRows(t, path, `SELECT r.id,r.team_id,r.state_id,r.state_type,r.assignee_id,r.cycle_id,r.version,r.data,(SELECT GROUP_CONCAT(user_id) FROM (SELECT user_id FROM issue_subscriber_records s WHERE s.workspace_key=r.workspace_key AND s.issue_id=r.id ORDER BY user_id)) FROM issue_records r WHERE r.workspace_key=? ORDER BY r.id`, teamCascadeWorkspace)
	got.Events = teamCascadeRows(t, path, `SELECT event_type,aggregate_id,payload FROM domain_events WHERE event_type='team.settings_updated' ORDER BY created_at,id`)
	for _, reload := range []string{"legacy", "bounded"} {
		fresh, err := store.OpenSQLite(path)
		if err != nil {
			t.Fatal(err)
		}
		reader := fresh
		if reload == "bounded" {
			if reader, err = fresh.BoundedMetadataViewForTesting(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		image := oauthMetadataImage(t, reader, teamCascadeWorkspace, teamCascadeFields...)
		_ = fresh.Close()
		for _, name := range teamCascadeFields {
			if want, have := teamCascadeNormalize(t, got.Image[name]), teamCascadeNormalize(t, image[name]); want != have {
				t.Errorf("bounded %s: %s reload of %s differs from the writing process: %s", bounded, reload, name, policyFirstDifference(want, have))
			}
		}
	}
	return got
}

// teamCascadeNormalize replaces the clocks a write stamps.
func teamCascadeNormalize(t *testing.T, value any) string {
	t.Helper()
	return scaleTriageNormalize(t, value, time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC))
}

func teamCascadeIssue(t *testing.T, outcome teamCascadeOutcome, id string) domain.Issue {
	t.Helper()
	for _, row := range outcome.Issues {
		if row[0] == id {
			var issue domain.Issue
			if err := json.Unmarshal([]byte(fmt.Sprint(row[7])), &issue); err != nil {
				t.Fatal(err)
			}
			issue.SubscriberIDs = nil
			if subscribers, ok := row[8].(string); ok && subscribers != "" {
				issue.SubscriberIDs = strings.Split(subscribers, ",")
			}
			return issue
		}
	}
	t.Fatalf("issue %s missing", id)
	return domain.Issue{}
}

func teamCascadeRecord[T any](t *testing.T, outcome teamCascadeOutcome, field, key string) (T, bool) {
	t.Helper()
	var value T
	for _, row := range outcome.Metadata {
		if row[0] == field && row[1] == key {
			if err := json.Unmarshal([]byte(fmt.Sprint(row[3])), &value); err != nil {
				t.Fatal(err)
			}
			return value, true
		}
	}
	return value, false
}

func TestTeamSettingsCascadesPersist(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	seed := teamCascadeSeed(t)
	seeded := map[any][]any{}
	for _, row := range teamCascadeRows(t, seed, `SELECT id,state_id,assignee_id,cycle_id,version,(SELECT GROUP_CONCAT(user_id) FROM issue_subscriber_records s WHERE s.workspace_key=r.workspace_key AND s.issue_id=r.id) FROM issue_records r WHERE workspace_key=?`, teamCascadeWorkspace) {
		seeded[row[0]] = row
	}
	for _, test := range []struct {
		name  string
		team  string
		body  map[string]any
		check func(*testing.T, teamCascadeOutcome)
	}{
		{name: "apply-to-sub-teams", team: "team_par", body: map[string]any{"settingsPermission": "owners", "labelPermission": "teamMembers", "applyToSubTeams": true}, check: func(t *testing.T, got teamCascadeOutcome) {
			for _, id := range []string{"team_par", "team_chi", "team_cyc", "team_ret"} {
				settings, _ := teamCascadeRecord[domain.TeamSettings](t, got, "teamSettings", id)
				if settings.SettingsPermission != "owners" || settings.LabelPermission != "teamMembers" {
					t.Errorf("%s settings not stored: %+v", id, settings)
				}
			}
		}},
		{name: "default-state-existing", team: "team_sol", body: map[string]any{"defaultStateId": "sol_todo"}, check: func(t *testing.T, got teamCascadeOutcome) {
			backlog, _ := teamCascadeRecord[domain.WorkflowState](t, got, "states", "sol_backlog")
			todo, _ := teamCascadeRecord[domain.WorkflowState](t, got, "states", "sol_todo")
			if backlog.Default || !todo.Default {
				t.Errorf("default flags not stored: backlog %v todo %v", backlog.Default, todo.Default)
			}
		}},
		{name: "default-state-materialized", team: "team_test", body: map[string]any{"defaultStateId": "state_progress"}, check: func(t *testing.T, got teamCascadeOutcome) {
			if state, ok := teamCascadeRecord[domain.WorkflowState](t, got, "states", "team_test_state_progress"); !ok || !state.Default {
				t.Errorf("materialized default not stored: %+v", state)
			}
			if template, _ := teamCascadeRecord[domain.IssueTemplate](t, got, "issueTemplates", "template_tst"); template.StateID != "team_test_state_todo" {
				t.Errorf("template status not remapped: %s", template.StateID)
			}
			if issue := teamCascadeIssue(t, got, "issue_cascade_101"); issue.State.ID != "team_test_state_todo" {
				t.Errorf("issue status not remapped: %s", issue.State.ID)
			}
			if issue := teamCascadeIssue(t, got, "issue_cascade_102"); issue.State.ID != "team_test_state_progress" {
				t.Errorf("issue status not remapped: %s", issue.State.ID)
			}
		}},
		{name: "inherit-cycles-disabled-parent", team: "team_cyc", body: map[string]any{"inheritCycles": true}, check: func(t *testing.T, got teamCascadeOutcome) {
			if current, _ := teamCascadeRecord[domain.Cycle](t, got, "cycles", "cyc_current"); current.Status != "completed" {
				t.Errorf("current cycle not completed: %s", current.Status)
			}
			if _, ok := teamCascadeRecord[domain.Cycle](t, got, "cycles", "cyc_upcoming"); ok {
				t.Error("upcoming cycle not deleted")
			}
			if issue := teamCascadeIssue(t, got, "issue_cascade_105"); issue.CycleID != nil {
				t.Errorf("deleted cycle kept on issue: %v", *issue.CycleID)
			}
			if issue := teamCascadeIssue(t, got, "issue_cascade_106"); issue.CycleID == nil || *issue.CycleID != "cyc_current" {
				t.Error("completed cycle dropped from issue")
			}
			if settings, _ := teamCascadeRecord[domain.CycleSettings](t, got, "cycleSettings", "team_cyc"); settings.Enabled {
				t.Error("inherited cycle settings not stored")
			}
		}},
		{name: "inherit-cycles-enabled-parent", team: "team_cye", body: map[string]any{"inheritCycles": true}, check: func(t *testing.T, got teamCascadeOutcome) {
			if current, _ := teamCascadeRecord[domain.Cycle](t, got, "cycles", "cye_current"); current.Number != 8 {
				t.Errorf("matching cycle not updated: %d", current.Number)
			}
			if _, ok := teamCascadeRecord[domain.Cycle](t, got, "cycles", "cye_stray"); ok {
				t.Error("unmatched cycle not deleted")
			}
			if issue := teamCascadeIssue(t, got, "issue_cascade_107"); issue.CycleID != nil {
				t.Error("deleted cycle kept on issue")
			}
			clones := 0
			for _, row := range got.Metadata {
				var cycle domain.Cycle
				if row[0] == "cycles" && json.Unmarshal([]byte(fmt.Sprint(row[3])), &cycle) == nil && cycle.TeamID == "team_cye" && cycle.Number == 9 {
					clones++
				}
			}
			if clones != 1 {
				t.Errorf("parent upcoming cycle cloned %d times", clones)
			}
		}},
		{name: "parent-change-remap", team: "team_sol", body: map[string]any{"parentTeamId": "team_par"}, check: func(t *testing.T, got teamCascadeOutcome) {
			if issue := teamCascadeIssue(t, got, "issue_cascade_103"); issue.State.ID != "par_todo" {
				t.Errorf("issue status not remapped: %s", issue.State.ID)
			}
			if issue := teamCascadeIssue(t, got, "issue_cascade_104"); issue.State.ID != "par_doing" {
				t.Errorf("issue status not remapped: %s", issue.State.ID)
			}
		}},
		{name: "access-private", team: "team_test", body: map[string]any{"access": "private"}, check: func(t *testing.T, got teamCascadeOutcome) {
			issue := teamCascadeIssue(t, got, "issue_cascade_101")
			if issue.Assignee != nil || strings.Join(issue.SubscriberIDs, ",") != "usr_admin" {
				t.Errorf("outsiders kept on private team issue: assignee %v subscribers %v", issue.Assignee, issue.SubscriberIDs)
			}
			if kept := teamCascadeIssue(t, got, "issue_cascade_102"); kept.Assignee == nil || kept.Assignee.ID != "usr_admin" {
				t.Error("member assignee removed")
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			legacy := teamCascadeRun(t, seed, "off", test.team, test.body)
			if legacy.Code != http.StatusOK {
				t.Fatalf("legacy status %d: %s", legacy.Code, legacy.Body)
			}
			test.check(t, legacy)
			// A record the cascade changed moved to its next version; the
			// others kept theirs.
			changed := 0
			for _, row := range legacy.Issues {
				before := seeded[row[0]]
				key := func(state, assignee, cycle, subscribers any) string {
					ids := strings.Split(fmt.Sprint(subscribers), ",")
					slices.Sort(ids)
					return fmt.Sprint(state, "|", assignee, "|", cycle, "|", ids)
				}
				moved := key(before[1], before[2], before[3], before[5]) != key(row[2], row[4], row[5], row[8])
				want := before[4].(int64)
				if moved {
					changed++
					want++
				}
				if row[6].(int64) != want {
					t.Errorf("issue %s version %v, want %d", row[0], row[6], want)
				}
			}
			if issues := test.name != "apply-to-sub-teams" && test.name != "default-state-existing"; issues != (changed > 0) {
				t.Errorf("%d issues changed", changed)
			}
			bounded := teamCascadeRun(t, seed, "on", test.team, test.body)
			if want, have := teamCascadeNormalize(t, legacy), teamCascadeNormalize(t, bounded); want != have {
				t.Fatalf("bounded (COLD) outcome differs from legacy: %s", policyFirstDifference(want, have))
			}
		})
	}
}
