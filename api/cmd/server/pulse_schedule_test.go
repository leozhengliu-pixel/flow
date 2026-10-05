package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// B4: an invited member who never wrote anything (so is not in the metadata
// user list) still receives Pulse summaries.
func TestPulseSummariesReachInvitedMembersWhoNeverWrote(t *testing.T) {
	repo := pulseTestRepo(t)
	s := &server{store: repo}
	invited, _, err := repo.Register(t.Context(), "Invited reader", "invited-reader@example.test", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.EnsureWorkspaceMembership(t.Context(), "workspace_test", invited.ID); err != nil {
		t.Fatal(err)
	}
	// Keep the member out of the metadata user list, as on a server where
	// they never made a scoped write.
	err = repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "users", "projects"), "test-workspace", "workspace.agent_guidance_updated", "", nil, func(data *domain.Bootstrap) error {
		data.Users = slices.DeleteFunc(data.Users, func(user domain.User) bool { return user.ID == invited.ID })
		for index := range data.Projects {
			if data.Projects[index].ID == "project_aut" {
				data.Projects[index].MemberIDs = append(data.Projects[index].MemberIDs, invited.ID)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if users, _ := repo.WorkspaceMetadataFields("test-workspace", "users"); slices.ContainsFunc(users.Users, func(user domain.User) bool { return user.ID == invited.ID }) {
		t.Fatal("fixture: invited member is in the metadata user list")
	}
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	addPulseUpdates(t, repo, "project_aut", domain.ProjectUpdate{ID: "for_invited", Body: "Shipped.", User: domain.User{ID: "usr_writer", Name: "Writer"}, CreatedAt: now.Add(-5 * time.Hour)})
	if err := s.preparePulseSummaries(t.Context(), "test-workspace", now); err != nil {
		t.Fatal(err)
	}
	summary, ok := pulseSummaries(repo)[invited.ID]
	if !ok || summary.Payload == nil || !slices.Equal(summary.Payload.UpdateIDs, []string{"for_invited"}) {
		t.Fatalf("invited member summary = %#v (all %v)", summary, pulseSummaries(repo))
	}
}

// B5: a member who switches from daily to weekly on a Wednesday gets every
// update since their last daily summary in the next Monday's weekly summary.
func TestPulseDailyToWeeklySwitchDropsNothing(t *testing.T) {
	repo := pulseTestRepo(t)
	s := &server{store: repo}
	writer := domain.User{ID: "usr_writer", Name: "Writer"}
	wednesday := time.Date(2026, 9, 16, 7, 0, 0, 0, time.UTC) // 2026-09-16 is a Wednesday
	if wednesday.Weekday() != time.Wednesday {
		t.Fatal("fixture date is not a Wednesday")
	}
	if err := s.preparePulseSummaries(t.Context(), "test-workspace", wednesday); err != nil {
		t.Fatal(err)
	}
	settings, _ := repo.WorkspaceMetadataFields("test-workspace", "settings")
	if cursor := pulseCursors(&settings)["usr_admin"]; !cursor.Equal(time.Date(2026, 9, 16, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("daily cursor = %v", cursor)
	}
	err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "userSettings"), "test-workspace", "user_settings.updated", "usr_admin", nil, func(data *domain.Bootstrap) error {
		value := data.UserSettings["usr_admin"]
		value.UserID, value.PulseSchedule = "usr_admin", "weekly"
		data.UserSettings["usr_admin"] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	addPulseUpdates(t, repo, "project_aut",
		domain.ProjectUpdate{ID: "wednesday", Body: "Wednesday.", User: writer, CreatedAt: time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)},
		domain.ProjectUpdate{ID: "friday", Body: "Friday.", User: writer, CreatedAt: time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)},
		domain.ProjectUpdate{ID: "sunday", Body: "Sunday.", User: writer, CreatedAt: time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)},
	)
	// Days between are not weekly delivery days.
	if err := s.preparePulseSummaries(t.Context(), "test-workspace", time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if summary, ok := pulseSummaries(repo)["usr_admin"]; ok {
		t.Fatalf("weekly member got a summary on Friday: %#v", summary)
	}
	if err := s.preparePulseSummaries(t.Context(), "test-workspace", time.Date(2026, 9, 21, 7, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	summary, ok := pulseSummaries(repo)["usr_admin"]
	if !ok || summary.Title != "Weekly Pulse" || summary.Payload == nil || summary.Payload.Total != 3 || !slices.Equal(summary.Payload.UpdateIDs, []string{"sunday", "friday", "wednesday"}) {
		t.Fatalf("Monday weekly summary = %#v payload %#v", summary, summary.Payload)
	}
	if !summary.Payload.WindowStart.Equal(time.Date(2026, 9, 16, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("weekly window starts %v, want the last daily delivery", summary.Payload.WindowStart)
	}
}

// B6: projects linked to an initiative from the project side
// (project.Initiatives) count like initiative.ProjectIDs in the For-me rules,
// visibility, the "project" filter and the initiative snapshot.
func TestPulseInitiativeProjectsUseBothLinkDirections(t *testing.T) {
	snapshot := &store.PulseFeedSnapshot{
		Teams:        []domain.Team{{ID: "team_a", Name: "Alpha"}, {ID: "team_private", Name: "Private"}},
		TeamSettings: map[string]domain.TeamSettings{"team_private": {TeamID: "team_private", Access: "private"}},
		Projects: []domain.Project{
			{ID: "p_side", Name: "Side linked", TeamIDs: []string{"team_a"}, MemberIDs: []string{"me"}, Initiatives: []string{"i_side"}},
			{ID: "p_hidden", Name: "Hidden", TeamIDs: []string{"team_private"}, Initiatives: []string{"i_hidden"}},
		},
		Initiatives: []domain.Initiative{
			{ID: "i_side", Name: "Side initiative"},
			{ID: "i_parent", Name: "Parent"},
			{ID: "i_hidden", Name: "Hidden initiative"},
		},
		InitiativeRelations: []domain.InitiativeRelation{},
	}
	snapshot.Initiatives[0].ParentInitiativeIDs = []string{"i_parent"}
	rules := newPulseRules(snapshot)
	viewer := newPulseViewer(snapshot, "me", "member", []domain.TeamMember{{TeamID: "team_a", UserID: "me"}}, nil)
	for _, id := range []string{"i_side", "i_parent"} {
		reasons, _ := rules.forMeReasons(viewer, &store.PulseFeedEntry{Kind: "initiative", SourceID: id, UpdateID: "u", AuthorID: "x"})
		if got := pulseReasonTypes(reasons); !slices.Equal(got, []string{pulseReasonInitiativeProjectMember}) || !slices.Equal(reasons[0].SourceIDs, []string{"p_side"}) {
			t.Errorf("%s reasons = %#v", id, reasons)
		}
	}
	if values := rules.pulseFilterValues(&store.PulseFeedEntry{Kind: "initiative", SourceID: "i_side"}, "project", time.Now()); !slices.Equal(values, []string{"p_side"}) {
		t.Fatalf("initiative project filter values = %v", values)
	}
	if values := rules.pulseFilterValues(&store.PulseFeedEntry{Kind: "initiative", SourceID: "i_side"}, "projectMember", time.Now()); !slices.Contains(values, "me") {
		t.Fatalf("initiative project member filter values = %v", values)
	}
	// An initiative whose only project is in a team the viewer cannot see is
	// not workspace-wide.
	if rules.initiativeVisible(viewer, rules.initiatives["i_hidden"]) {
		t.Fatal("initiative linked only to a hidden project is visible")
	}
	if !rules.initiativeVisible(viewer, rules.initiatives["i_side"]) {
		t.Fatal("initiative linked to a visible project is hidden")
	}
	data := &domain.Bootstrap{Projects: snapshot.Projects}
	if pulse := initiativePulseSnapshot(data, snapshot.Initiatives[0], time.Now()); len(pulse.Projects) != 1 || pulse.Projects[0].ID != "p_side" || pulse.Projects[0].Name != "Side linked" {
		t.Fatalf("initiative snapshot projects = %#v", pulse.Projects)
	}
}

func viewerSubscription(repo *store.SQLiteStore, kind, id string) (domain.Subscription, bool) {
	data, _ := repo.WorkspaceMetadataFields("test-workspace", "subscriptions")
	for _, item := range data.Subscriptions {
		if item.UserID == "usr_admin" && item.ResourceType == kind && item.ResourceID == id {
			return item, true
		}
	}
	return domain.Subscription{}, false
}

// B7: the generic unsubscribe keeps the Pulse part of a record (an explicit
// Pulse subscribe or opt-out) and returns the kept record; a generic
// subscribe on an opt-out-only record makes it an active subscription again.
func TestGenericSubscriptionDeleteKeepsPulseChoice(t *testing.T) {
	repo := pulseTestRepo(t)
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	// Subscribed to project events and to Pulse.
	requestJSON[domain.Subscription](t, handler, "PUT", "/api/subscriptions/project/project_aut", map[string]any{"events": []string{"projectUpdates", "pulse"}}, 200)
	kept := requestJSON[domain.Subscription](t, handler, "DELETE", "/api/subscriptions/project/project_aut", nil, 200)
	if !slices.Equal(kept.Events, []string{"pulse"}) {
		t.Fatalf("kept record = %#v", kept)
	}
	if stored, ok := viewerSubscription(repo, "project", "project_aut"); !ok || !slices.Equal(stored.Events, []string{"pulse"}) {
		t.Fatalf("stored record after generic delete = %#v %v", stored, ok)
	}
	if got := requestJSON[map[string]any](t, handler, "GET", "/api/pulse/subscriptions/project/project_aut", nil, 200); got["explicitSubscribed"] != true {
		t.Fatalf("explicit Pulse subscribe lost: %v", got)
	}
	// Pulse opt-out only: the generic delete keeps the opt-out record.
	requestJSON[map[string]bool](t, handler, "DELETE", "/api/pulse/subscriptions/project/project_aut", nil, 200)
	kept = requestJSON[domain.Subscription](t, handler, "DELETE", "/api/subscriptions/project/project_aut", nil, 200)
	if len(kept.Events) != 0 || !slices.Equal(kept.OptOutEvents, []string{"pulse"}) {
		t.Fatalf("kept opt-out record = %#v", kept)
	}
	// A generic subscribe on the opt-out-only record is a real subscription.
	created := requestJSON[domain.Subscription](t, handler, "PUT", "/api/subscriptions/project/project_aut", nil, 200)
	if len(created.OptOutEvents) != 0 {
		t.Fatalf("generic subscribe left an opt-out-only record: %#v", created)
	}
	// A plain subscription is removed outright.
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("DELETE", "/api/subscriptions/project/project_aut", nil))
	if recorder.Code != 204 {
		t.Fatalf("plain delete status %d: %s", recorder.Code, recorder.Body.String())
	}
	if _, ok := viewerSubscription(repo, "project", "project_aut"); ok {
		t.Fatal("plain subscription survived the generic delete")
	}
}

// B11: milestone changes carry completed and omit from for new milestones;
// initiatives diff their sub-initiatives (an empty stored project list still
// diffs against the next one).
func TestPulseDiffMilestoneCompletionAndSubInitiatives(t *testing.T) {
	progress := func(value float64) *float64 { return &value }
	previous := &domain.PulseSnapshot{Progress: progress(0.2), Milestones: []domain.PulseMilestoneSnapshot{{ID: "m1", Name: "Beta", Progress: 0.5}}}
	next := &domain.PulseSnapshot{Progress: progress(0.6), Milestones: []domain.PulseMilestoneSnapshot{{ID: "m1", Name: "Beta", Progress: 1}, {ID: "m2", Name: "GA", Progress: 0.1, TargetDate: "2026-12-01"}}}
	diff := pulseDiffBetween(previous, next)
	if diff == nil || len(diff.Milestones) != 2 {
		t.Fatalf("diff = %#v", diff)
	}
	done, added := diff.Milestones[0], diff.Milestones[1]
	if !done.Completed || done.From == nil || *done.From != 0.5 || done.Added {
		t.Fatalf("completed milestone = %#v", done)
	}
	if !added.Added || added.From != nil || added.Completed || added.TargetDate != "2026-12-01" {
		t.Fatalf("added milestone = %#v", added)
	}
	raw, _ := json.Marshal(added)
	if strings.Contains(string(raw), `"from"`) {
		t.Fatalf("added milestone JSON has from: %s", raw)
	}
	none, one := []domain.PulseRef{}, []domain.PulseRef{{ID: "child", Name: "Child"}}
	before := &domain.PulseSnapshot{Status: "active", Initiatives: &none}
	after := &domain.PulseSnapshot{Status: "active", Projects: []domain.PulseRef{{ID: "p1", Name: "One"}}, Initiatives: &one}
	// Round-trip the stored snapshot: empty lists are omitted.
	stored, _ := json.Marshal(before)
	var reloaded domain.PulseSnapshot
	if err := json.Unmarshal(stored, &reloaded); err != nil {
		t.Fatal(err)
	}
	diff = pulseDiffBetween(&reloaded, after)
	if diff == nil || diff.Initiatives == nil || len(diff.Initiatives.Added) != 1 || diff.Initiatives.Added[0].ID != "child" || diff.Projects == nil || len(diff.Projects.Added) != 1 {
		t.Fatalf("initiative diff = %#v", diff)
	}
	// Snapshots from before sub-initiative capture produce no initiatives row.
	if diff := pulseDiffBetween(&domain.PulseSnapshot{Status: "active"}, after); diff != nil && diff.Initiatives != nil {
		t.Fatalf("legacy snapshot initiatives diff = %#v", diff.Initiatives)
	}
	// initiativePulseSnapshot records sub-initiatives (parent links and relations).
	data := &domain.Bootstrap{Initiatives: []domain.Initiative{{ID: "parent", Name: "Parent"}, {ID: "child", Name: "Child", ParentInitiativeIDs: []string{"parent"}}, {ID: "related", Name: "Related"}}, InitiativeRelations: []domain.InitiativeRelation{{InitiativeID: "related", RelatedInitiativeID: "parent", Type: "parent"}}}
	snapshot := initiativePulseSnapshot(data, data.Initiatives[0], time.Now())
	if snapshot.Initiatives == nil || len(*snapshot.Initiatives) != 2 || (*snapshot.Initiatives)[0].ID != "child" || (*snapshot.Initiatives)[1].ID != "related" {
		t.Fatalf("sub-initiatives = %#v", snapshot.Initiatives)
	}
}

func rawRequest(t *testing.T, handler http.Handler, method, path string, input any) (int, string) {
	t.Helper()
	var body io.Reader
	if input != nil {
		raw, _ := json.Marshal(input)
		body = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

// B12: stored snapshots never leave the server: not in create/edit/comment/
// reaction responses nor in the non-paged bootstrap, while the store keeps
// them for the next update's diff.
func TestPulseSnapshotsStayInStorage(t *testing.T) {
	repo := pulseTestRepo(t)
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	initiative := requestJSON[domain.Initiative](t, handler, "POST", "/api/initiatives", map[string]any{"name": "Snapshot initiative"}, http.StatusCreated)
	var created domain.ProjectUpdate
	for _, request := range []struct {
		method, path string
		input        any
	}{
		{"POST", "/api/projects/project_aut/updates", map[string]any{"body": "First"}},
		{"POST", "/api/initiatives/" + initiative.ID + "/updates", map[string]any{"body": "Initiative"}},
	} {
		status, body := rawRequest(t, handler, request.method, request.path, request.input)
		if status != http.StatusCreated || strings.Contains(body, `"snapshot"`) {
			t.Fatalf("%s %s = %d %s", request.method, request.path, status, body)
		}
		if strings.Contains(request.path, "projects") {
			_ = json.Unmarshal([]byte(body), &created)
		}
	}
	for _, request := range []struct {
		method, path string
		input        any
	}{
		{"PATCH", "/api/projects/project_aut/updates/" + created.ID, map[string]any{"body": "Edited"}},
		{"POST", "/api/projects/project_aut/updates/" + created.ID + "/comments", map[string]any{"body": "Comment"}},
		{"POST", "/api/projects/project_aut/updates/" + created.ID + "/reactions", map[string]any{"emoji": "+1"}},
		{"GET", "/api/bootstrap", nil},
		{"GET", "/api/projects/project_aut/updates", nil},
	} {
		status, body := rawRequest(t, handler, request.method, request.path, request.input)
		if status >= 300 || strings.Contains(body, `"snapshot"`) || strings.Contains(body, `"capturedAt"`) {
			t.Fatalf("%s %s = %d leaks a snapshot: %.400s", request.method, request.path, status, body)
		}
	}
	data, _ := repo.WorkspaceMetadataFields("test-workspace", "projectUpdates")
	if updates := data.ProjectUpdates["project_aut"]; len(updates) == 0 || updates[0].Snapshot == nil {
		t.Fatal("the stored update lost its snapshot")
	}
}

// B14: "Subscribe to {initiative}'s project updates" follows every project of
// the initiative and its sub-initiatives (linked from either side) in For me,
// without reading as a subscription to the initiative itself.
func TestInitiativeProjectUpdatesSubscription(t *testing.T) {
	repo := pulseTestRepo(t)
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	now := time.Now().UTC()
	err := repo.MutateWorkspace(t.Context(), "test-workspace", "test.pulse", "", nil, func(data *domain.Bootstrap) error {
		status := data.Projects[0].Status
		data.Projects = append(data.Projects,
			domain.Project{ID: "p_listed", Name: "Listed", TeamIDs: []string{"team_test"}, MemberIDs: []string{}, Status: status},
			domain.Project{ID: "p_side", Name: "Side", TeamIDs: []string{"team_test"}, MemberIDs: []string{}, Initiatives: []string{"i_child"}, Status: status},
			domain.Project{ID: "p_other", Name: "Other", TeamIDs: []string{"team_test"}, MemberIDs: []string{}, Status: status})
		data.Initiatives = append(data.Initiatives,
			domain.Initiative{ID: "i_parent", Name: "Parent", ProjectIDs: []string{"p_listed"}, ContributingTeamIDs: []string{}, LabelIDs: []string{}, ParentInitiativeIDs: []string{}},
			domain.Initiative{ID: "i_child", Name: "Child", ProjectIDs: []string{}, ContributingTeamIDs: []string{}, LabelIDs: []string{}, ParentInitiativeIDs: []string{"i_parent"}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Opt out of the team default so only the initiative subscription counts.
	requestJSON[map[string]bool](t, handler, "DELETE", "/api/pulse/subscriptions/team/team_test", nil, http.StatusOK)
	writer := domain.User{ID: "usr_writer", Name: "Writer"}
	for index, project := range []string{"p_listed", "p_side", "p_other"} {
		addPulseUpdates(t, repo, project, domain.ProjectUpdate{ID: "update_" + project, Body: "Update", User: writer, CreatedAt: now.Add(-time.Duration(index+1) * time.Minute)})
	}
	following := func() []string {
		feed := requestJSON[pulseFeedResponse](t, handler, "GET", "/api/pulse/feed?view=following", nil, http.StatusOK)
		return feedIDs(feed)
	}
	if got := requestJSON[map[string]bool](t, handler, "GET", "/api/pulse/subscriptions/initiative/i_parent/project-updates", nil, http.StatusOK); got["subscribed"] {
		t.Fatalf("default state = %v", got)
	}
	if got := following(); slices.Contains(got, "project:update_p_listed") {
		t.Fatalf("followed before subscribing: %v", got)
	}
	if got := requestJSON[map[string]bool](t, handler, "PUT", "/api/pulse/subscriptions/initiative/i_parent/project-updates", nil, http.StatusOK); !got["subscribed"] {
		t.Fatalf("PUT = %v", got)
	}
	got := following()
	if !slices.Contains(got, "project:update_p_listed") || !slices.Contains(got, "project:update_p_side") || slices.Contains(got, "project:update_p_other") {
		t.Fatalf("following after subscribing to the initiative's project updates = %v", got)
	}
	feed := requestJSON[pulseFeedResponse](t, handler, "GET", "/api/pulse/feed?view=following", nil, http.StatusOK)
	for _, item := range feed.Items {
		if item.ID == "project:update_p_side" {
			if len(item.Reasons) != 1 || item.Reasons[0].Type != pulseReasonTeamProjectUpdates || !slices.Equal(item.Reasons[0].InitiativeIDs, []string{"i_parent"}) || !slices.Equal(item.Reasons[0].InitiativeNames, []string{"Parent"}) {
				t.Fatalf("reason = %#v", item.Reasons)
			}
		}
	}
	if got := requestJSON[map[string]bool](t, handler, "GET", "/api/pulse/subscriptions/initiative/i_parent/project-updates", nil, http.StatusOK); !got["subscribed"] {
		t.Fatalf("state after PUT = %v", got)
	}
	// It is not a subscription to the initiative itself.
	if state := requestJSON[map[string]any](t, handler, "GET", "/api/pulse/subscriptions/initiative/i_parent", nil, http.StatusOK); state["explicit"] == true {
		t.Fatalf("initiative subscription state = %v", state)
	}
	if _, ok := viewerSubscription(repo, "initiative", "i_parent"); ok {
		t.Fatal("project-updates subscription created an initiative subscription record")
	}
	// Initiative feed items carry the state for the card menu.
	addPulseInitiativeUpdate(t, repo, "i_parent", now)
	all := requestJSON[pulseFeedResponse](t, handler, "GET", "/api/pulse/feed?view=all", nil, http.StatusOK)
	for _, item := range all.Items {
		if item.Kind == "initiative" && (item.ProjectUpdatesSubscribed == nil || !*item.ProjectUpdatesSubscribed) {
			t.Fatalf("initiative item projectUpdatesSubscribed = %v", item.ProjectUpdatesSubscribed)
		}
	}
	requestJSON[map[string]bool](t, handler, "DELETE", "/api/pulse/subscriptions/initiative/i_parent/project-updates", nil, http.StatusOK)
	if got := following(); slices.Contains(got, "project:update_p_listed") {
		t.Fatalf("still following after unsubscribing: %v", got)
	}
	requestJSON[map[string]string](t, handler, "PUT", "/api/pulse/subscriptions/initiative/missing/project-updates", nil, http.StatusNotFound)
}

func addPulseInitiativeUpdate(t *testing.T, repo *store.SQLiteStore, initiativeID string, at time.Time) {
	t.Helper()
	err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "initiativeUpdates"), "test-workspace", "initiative.update_updated", initiativeID, nil, func(data *domain.Bootstrap) error {
		data.InitiativeUpdates[initiativeID] = append([]domain.InitiativeUpdate{{ID: "initiative_update_" + initiativeID, InitiativeID: initiativeID, Body: "Initiative", Health: "onTrack", CreatedAt: at, User: domain.User{ID: "usr_writer", Name: "Writer"}, Comments: []domain.Comment{}, Reactions: map[string][]string{}}}, data.InitiativeUpdates[initiativeID]...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
