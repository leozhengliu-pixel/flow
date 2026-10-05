package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func pulseReasonTypes(reasons []pulseReason) []string {
	result := []string{}
	for _, reason := range reasons {
		result = append(result, reason.Type)
	}
	return result
}

func TestPulseForMeRules(t *testing.T) {
	me := domain.User{ID: "me", Name: "Me"}
	snapshot := &store.PulseFeedSnapshot{
		Teams: []domain.Team{{ID: "team_a", Name: "Alpha"}, {ID: "team_b", Name: "Beta"}, {ID: "team_c", Name: "Gamma"}, {ID: "team_d", Name: "Delta"}},
		Projects: []domain.Project{
			{ID: "p_member", Name: "Member", TeamIDs: []string{"team_b"}, MemberIDs: []string{"me"}},
			{ID: "p_lead", Name: "Lead", TeamIDs: []string{"team_b"}, Lead: &me},
			{ID: "p_owned", Name: "Owned", TeamIDs: []string{"team_b"}},
			{ID: "p_team", Name: "Team", TeamIDs: []string{"team_a"}},
			{ID: "p_team_optout", Name: "Team opt-out", TeamIDs: []string{"team_c"}},
			{ID: "p_team_sub", Name: "Team subscribed", TeamIDs: []string{"team_d"}},
			{ID: "p_unsub", Name: "Unsubscribed member", TeamIDs: []string{"team_b"}, MemberIDs: []string{"me"}},
			{ID: "p_sub", Name: "Subscribed", TeamIDs: []string{"team_b"}},
			{ID: "p_other", Name: "Other", TeamIDs: []string{"team_b"}},
		},
		Initiatives: []domain.Initiative{
			{ID: "i_owned", Name: "Owned initiative", Owner: &me, ProjectIDs: []string{"p_owned"}},
			{ID: "i_parent", Name: "Parent"},
			{ID: "i_child", Name: "Child", ParentInitiativeIDs: []string{"i_parent"}, ProjectIDs: []string{"p_member"}},
			{ID: "i_none", Name: "None", ProjectIDs: []string{"p_other"}},
			{ID: "i_unsub", Name: "Owned but unsubscribed", Owner: &me},
		},
		Subscriptions: []domain.Subscription{
			{UserID: "me", ResourceType: "team", ResourceID: "team_c", OptOutEvents: []string{"pulse"}},
			{UserID: "me", ResourceType: "team", ResourceID: "team_d", Events: []string{"pulse"}},
			{UserID: "me", ResourceType: "project", ResourceID: "p_unsub", OptOutEvents: []string{"pulse"}},
			{UserID: "me", ResourceType: "project", ResourceID: "p_sub", Events: []string{"pulse"}},
			{UserID: "me", ResourceType: "initiative", ResourceID: "i_unsub", OptOutEvents: []string{"pulse"}},
			{UserID: "someone", ResourceType: "project", ResourceID: "p_other", Events: []string{"pulse"}},
		},
	}
	// "me" belongs to Alpha and Gamma; Beta and Delta are public teams they can see.
	memberships := []domain.TeamMember{{TeamID: "team_a", UserID: "me"}, {TeamID: "team_c", UserID: "me"}, {TeamID: "team_a", UserID: "someone"}, {TeamID: "team_b", UserID: "someone"}}
	rules := newPulseRules(snapshot)
	viewer := newPulseViewer(snapshot, "me", "member", memberships, nil)
	cases := []struct {
		kind, source string
		author       string
		mentions     []string
		want         []string
	}{
		{"project", "p_member", "x", nil, []string{pulseReasonProjectMember}},
		{"project", "p_lead", "x", nil, []string{pulseReasonProjectMember}},
		{"project", "p_owned", "x", nil, []string{pulseReasonInitiativeOwner}},
		{"project", "p_team", "x", nil, []string{pulseReasonTeamProjectUpdates}},
		{"project", "p_team_optout", "x", nil, []string{}},
		{"project", "p_team_sub", "x", nil, []string{pulseReasonTeamProjectUpdates}},
		{"project", "p_unsub", "x", nil, []string{}},
		{"project", "p_sub", "x", nil, []string{pulseReasonSubscribed}},
		{"project", "p_other", "x", nil, []string{}},
		{"project", "p_other", "me", nil, []string{pulseReasonAuthor}},
		{"project", "p_other", "x", []string{"me"}, []string{pulseReasonMentioned}},
		// An explicit unsubscribe hides defaults but never your own words.
		{"project", "p_unsub", "me", nil, []string{pulseReasonAuthor}},
		{"initiative", "i_owned", "x", nil, []string{pulseReasonInitiativeOwner}},
		{"initiative", "i_parent", "x", nil, []string{pulseReasonInitiativeProjectMember}},
		{"initiative", "i_child", "x", nil, []string{pulseReasonInitiativeProjectMember}},
		{"initiative", "i_none", "x", nil, []string{}},
		{"initiative", "i_unsub", "x", nil, []string{}},
	}
	for _, test := range cases {
		entry := &store.PulseFeedEntry{Kind: test.kind, SourceID: test.source, UpdateID: "u", AuthorID: test.author, Mentions: test.mentions}
		reasons, _ := rules.forMeReasons(viewer, entry)
		if got := pulseReasonTypes(reasons); !slices.Equal(got, test.want) {
			t.Errorf("%s %s author=%s mentions=%v: reasons %v, want %v", test.kind, test.source, test.author, test.mentions, got, test.want)
		}
	}
	reasons, _ := rules.forMeReasons(viewer, &store.PulseFeedEntry{Kind: "project", SourceID: "p_team"})
	if len(reasons) != 1 || !slices.Equal(reasons[0].SourceIDs, []string{"team_a"}) || !slices.Equal(reasons[0].SourceNames, []string{"Alpha"}) {
		t.Fatalf("team reason = %#v", reasons)
	}
	reasons, _ = rules.forMeReasons(viewer, &store.PulseFeedEntry{Kind: "initiative", SourceID: "i_parent"})
	if !slices.Equal(reasons[0].SourceIDs, []string{"p_member"}) {
		t.Fatalf("sub-initiative project reason = %#v", reasons)
	}
	// A guest sees no initiatives and no projects without teams.
	guest := newPulseViewer(snapshot, "me", "guest", memberships, nil)
	if rules.initiativeVisible(guest, rules.initiatives["i_owned"]) || rules.projectVisible(guest, &domain.Project{}) {
		t.Fatal("guest can see Pulse sources")
	}
}

// pulseTestRepo opens the fixture with Pulse on, a third member who writes
// updates, and a project followed only through its team.
func pulseTestRepo(t *testing.T) *store.SQLiteStore {
	t.Helper()
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	err = repo.MutateWorkspace(t.Context(), "test-workspace", "test.pulse", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.FeatureFlags["pulse"] = true
		data.WorkspaceSettings.FeatureSettings.PulseWorkspaceSchedule = "daily"
		for id, settings := range data.TeamSettings {
			settings.Timezone = "UTC"
			data.TeamSettings[id] = settings
		}
		data.Projects = append(data.Projects, domain.Project{ID: "project_team", Name: "Team only", SlugID: "team-only", TeamIDs: []string{"team_test"}, MemberIDs: []string{}, Status: data.Projects[0].Status})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

var pulseTestAuthor = domain.User{ID: "usr_member", Name: "Test member", DisplayName: "Test member"}

func addPulseUpdates(t *testing.T, repo *store.SQLiteStore, projectID string, updates ...domain.ProjectUpdate) {
	t.Helper()
	ctx := store.WithMetadataFields(t.Context(), "projectUpdates")
	err := repo.MutateWorkspace(ctx, "test-workspace", "project.update_updated", projectID, nil, func(data *domain.Bootstrap) error {
		for _, update := range updates {
			update.ProjectID = projectID
			if update.User.ID == "" {
				update.User = pulseTestAuthor
			}
			if update.Health == "" {
				update.Health = "onTrack"
			}
			data.ProjectUpdates[projectID] = append([]domain.ProjectUpdate{update}, data.ProjectUpdates[projectID]...)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func pulseFeedPath(values url.Values) string { return "/api/pulse/feed?" + values.Encode() }

func feedIDs(response pulseFeedResponse) []string {
	result := []string{}
	for _, item := range response.Items {
		result = append(result, item.ID)
	}
	return result
}

func TestPulseFeedViewsFiltersSearchAndStableCursor(t *testing.T) {
	repo := pulseTestRepo(t)
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	base := time.Now().UTC().Add(-time.Hour)
	addPulseUpdates(t, repo, "project_aut",
		domain.ProjectUpdate{ID: "u1", Body: "First milestone", CreatedAt: base},
		domain.ProjectUpdate{ID: "u2", Body: "Second", CreatedAt: base.Add(time.Minute), Health: "atRisk"},
		domain.ProjectUpdate{ID: "u3", Body: "Third", CreatedAt: base.Add(2 * time.Minute), Reactions: map[string][]string{"🎉": {"a", "b", "c", "d"}}},
		domain.ProjectUpdate{ID: "u4", Body: "Fourth", CreatedAt: base.Add(3 * time.Minute), User: domain.User{ID: "usr_admin", Name: "Test admin", DisplayName: "Test admin"}},
		domain.ProjectUpdate{ID: "u5", Body: "Fifth", CreatedAt: base.Add(4 * time.Minute)},
	)
	page := requestJSON[pulseFeedResponse](t, handler, http.MethodGet, pulseFeedPath(url.Values{"view": {"all"}, "limit": {"2"}}), nil, http.StatusOK)
	if got := feedIDs(page); !slices.Equal(got, []string{"project:u5", "project:u4"}) || page.NextCursor == "" {
		t.Fatalf("first page = %v next=%q", got, page.NextCursor)
	}
	if page.Items[0].Source.Name != "Test project" || !strings.HasSuffix(page.Items[0].Source.URL, "/project/test-project/overview") || page.Items[0].Kind != "project" {
		t.Fatalf("item source = %#v", page.Items[0])
	}
	// A new update posted while paging does not shift the next page.
	addPulseUpdates(t, repo, "project_aut", domain.ProjectUpdate{ID: "u6", Body: "Newest", CreatedAt: base.Add(10 * time.Minute)})
	second := requestJSON[pulseFeedResponse](t, handler, http.MethodGet, pulseFeedPath(url.Values{"view": {"all"}, "limit": {"2"}, "cursor": {page.NextCursor}}), nil, http.StatusOK)
	if got := feedIDs(second); !slices.Equal(got, []string{"project:u3", "project:u2"}) {
		t.Fatalf("second page = %v", got)
	}
	third := requestJSON[pulseFeedResponse](t, handler, http.MethodGet, pulseFeedPath(url.Values{"view": {"all"}, "limit": {"2"}, "cursor": {second.NextCursor}}), nil, http.StatusOK)
	if got := feedIDs(third); !slices.Equal(got, []string{"project:u1"}) || third.NextCursor != "" {
		t.Fatalf("last page = %v next=%q", got, third.NextCursor)
	}
	requestJSON[map[string]string](t, handler, http.MethodGet, pulseFeedPath(url.Values{"view": {"popular"}, "cursor": {page.NextCursor}}), nil, http.StatusBadRequest)

	popular := requestJSON[pulseFeedResponse](t, handler, http.MethodGet, pulseFeedPath(url.Values{"view": {"popular"}}), nil, http.StatusOK)
	if got := feedIDs(popular); got[0] != "project:u3" {
		t.Fatalf("popular = %v, want the discussed update first", got)
	}
	following := requestJSON[pulseFeedResponse](t, handler, http.MethodGet, pulseFeedPath(url.Values{"view": {"following"}}), nil, http.StatusOK)
	if len(following.Items) != 6 || len(following.Items[0].Reasons) == 0 || !following.Items[0].Subscribed {
		t.Fatalf("following = %v %#v", feedIDs(following), following.Items[0].Reasons)
	}
	created := requestJSON[pulseFeedResponse](t, handler, http.MethodGet, pulseFeedPath(url.Values{"view": {"created"}}), nil, http.StatusOK)
	if got := feedIDs(created); !slices.Equal(got, []string{"project:u4"}) {
		t.Fatalf("created = %v", got)
	}
	filtered := requestJSON[pulseFeedResponse](t, handler, http.MethodGet, pulseFeedPath(url.Values{"filter": {`[{"field":"health","operator":"is","values":["atRisk"]}]`}}), nil, http.StatusOK)
	if got := feedIDs(filtered); !slices.Equal(got, []string{"project:u2"}) {
		t.Fatalf("health filter = %v", got)
	}
	notAuthor := requestJSON[pulseFeedResponse](t, handler, http.MethodGet, pulseFeedPath(url.Values{"filter": {`{"filters":[{"field":"author","operator":"isNot","values":["usr_member"]},{"field":"health","operator":"is","values":["atRisk"]}],"match":"any"}`}}), nil, http.StatusOK)
	if got := feedIDs(notAuthor); !slices.Equal(got, []string{"project:u4", "project:u2"}) {
		t.Fatalf("any-match filter = %v", got)
	}
	searched := requestJSON[pulseFeedResponse](t, handler, http.MethodGet, pulseFeedPath(url.Values{"q": {"milestone"}}), nil, http.StatusOK)
	if got := feedIDs(searched); !slices.Equal(got, []string{"project:u1"}) {
		t.Fatalf("search = %v", got)
	}
	byAuthor := requestJSON[pulseFeedResponse](t, handler, http.MethodGet, pulseFeedPath(url.Values{"q": {"test admin"}}), nil, http.StatusOK)
	if got := feedIDs(byAuthor); !slices.Equal(got, []string{"project:u4"}) {
		t.Fatalf("author search = %v", got)
	}
	bySource := requestJSON[pulseFeedResponse](t, handler, http.MethodGet, pulseFeedPath(url.Values{"q": {"TEST PROJECT"}}), nil, http.StatusOK)
	if len(bySource.Items) != 6 {
		t.Fatalf("source search = %v", feedIDs(bySource))
	}
	requestJSON[map[string]string](t, handler, http.MethodGet, pulseFeedPath(url.Values{"filter": {`[{"field":"nope","operator":"is","values":["x"]}]`}}), nil, http.StatusBadRequest)
	name, resource, view := "At risk", "pulse", "all"
	saved := requestJSON[domain.SavedView](t, handler, http.MethodPost, "/api/views", domain.SavedViewMutationInput{Name: &name, Resource: &resource, View: &view, Filters: json.RawMessage(`[{"id":"f1","field":"health","operator":"is","values":["atRisk"]}]`), Display: json.RawMessage(`{"match":"all"}`)}, http.StatusCreated)
	custom := requestJSON[pulseFeedResponse](t, handler, http.MethodGet, pulseFeedPath(url.Values{"viewId": {saved.ID}}), nil, http.StatusOK)
	if got := feedIDs(custom); !slices.Equal(got, []string{"project:u2"}) {
		t.Fatalf("custom feed = %v", got)
	}
	requestJSON[map[string]string](t, handler, http.MethodGet, pulseFeedPath(url.Values{"viewId": {"missing"}}), nil, http.StatusNotFound)

	// Archived projects drop out of every view.
	err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "projects"), "test-workspace", "project.updated", "project_aut", nil, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		data.Projects[0].ArchivedAt = &now
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	archived := requestJSON[pulseFeedResponse](t, handler, http.MethodGet, pulseFeedPath(url.Values{"view": {"all"}}), nil, http.StatusOK)
	if len(archived.Items) != 0 {
		t.Fatalf("archived project updates = %v", feedIDs(archived))
	}
}

func TestPulseSeenIsMonotonicAndUnreadExcludesOwnUpdates(t *testing.T) {
	repo := pulseTestRepo(t)
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	now := time.Now().UTC()
	seen := now.Add(-time.Hour)
	requestJSON[map[string]time.Time](t, handler, http.MethodPost, "/api/pulse/seen", map[string]time.Time{"at": seen}, http.StatusOK)
	addPulseUpdates(t, repo, "project_aut",
		domain.ProjectUpdate{ID: "before", Body: "Before", CreatedAt: seen.Add(-time.Minute)},
		domain.ProjectUpdate{ID: "theirs", Body: "Theirs", CreatedAt: seen.Add(10 * time.Minute)},
		domain.ProjectUpdate{ID: "mine", Body: "Mine", CreatedAt: seen.Add(20 * time.Minute), User: domain.User{ID: "usr_admin", Name: "Test admin"}},
	)
	type unread struct {
		Count    int        `json:"count"`
		LatestAt *time.Time `json:"latestAt"`
	}
	got := requestJSON[unread](t, handler, http.MethodGet, "/api/pulse/unread", nil, http.StatusOK)
	if got.Count != 1 || got.LatestAt == nil || !got.LatestAt.Equal(seen.Add(10*time.Minute)) {
		t.Fatalf("unread = %#v, want only the other member's newer update", got)
	}
	// Moving last seen backwards is ignored.
	back := requestJSON[map[string]time.Time](t, handler, http.MethodPost, "/api/pulse/seen", map[string]time.Time{"at": seen.Add(-24 * time.Hour)}, http.StatusOK)
	if !back["lastSeenAt"].Equal(seen) {
		t.Fatalf("seen moved backwards: %v", back)
	}
	settings, _ := repo.WorkspaceMetadataFields("test-workspace", "userSettings")
	if stored, _ := time.Parse(time.RFC3339Nano, settings.UserSettings["usr_admin"].FeedLastSeenTime); !stored.Equal(seen) {
		t.Fatalf("stored last seen = %v", settings.UserSettings["usr_admin"].FeedLastSeenTime)
	}
	requestJSON[map[string]time.Time](t, handler, http.MethodPost, "/api/pulse/seen", map[string]time.Time{"at": now}, http.StatusOK)
	if got := requestJSON[unread](t, handler, http.MethodGet, "/api/pulse/unread", nil, http.StatusOK); got.Count != 0 {
		t.Fatalf("unread after seen = %#v", got)
	}
	feed := requestJSON[pulseFeedResponse](t, handler, http.MethodGet, "/api/pulse/feed?view=following", nil, http.StatusOK)
	if feed.LastSeenAt == nil || !feed.LastSeenAt.Equal(now) || feed.UnreadCount != 0 {
		t.Fatalf("feed last seen = %v unread=%d", feed.LastSeenAt, feed.UnreadCount)
	}
}

func TestPulseSubscriptionsOverrideDefaults(t *testing.T) {
	repo := pulseTestRepo(t)
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	at := time.Now().UTC().Add(-time.Hour)
	addPulseUpdates(t, repo, "project_team", domain.ProjectUpdate{ID: "team_update", Body: "Team", CreatedAt: at})
	addPulseUpdates(t, repo, "project_aut", domain.ProjectUpdate{ID: "member_update", Body: "Member", CreatedAt: at.Add(time.Minute)})
	following := func() []string {
		return feedIDs(requestJSON[pulseFeedResponse](t, handler, http.MethodGet, "/api/pulse/feed?view=following", nil, http.StatusOK))
	}
	if got := following(); !slices.Equal(got, []string{"project:member_update", "project:team_update"}) {
		t.Fatalf("defaults = %v", got)
	}
	// Team project updates are on for your teams until you unsubscribe.
	if got := requestJSON[map[string]bool](t, handler, http.MethodDelete, "/api/pulse/subscriptions/team/team_test", nil, http.StatusOK); got["subscribed"] {
		t.Fatalf("team unsubscribe = %v", got)
	}
	if got := following(); !slices.Equal(got, []string{"project:member_update"}) {
		t.Fatalf("after team unsubscribe = %v", got)
	}
	// An explicit project unsubscribe beats project membership.
	requestJSON[map[string]bool](t, handler, http.MethodDelete, "/api/pulse/subscriptions/project/project_aut", nil, http.StatusOK)
	if got := following(); len(got) != 0 {
		t.Fatalf("after project unsubscribe = %v", got)
	}
	state := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/pulse/subscriptions/project/project_aut", nil, http.StatusOK)
	if state["subscribed"] != false || state["explicit"] != true {
		t.Fatalf("project state = %v", state)
	}
	all := requestJSON[pulseFeedResponse](t, handler, http.MethodGet, "/api/pulse/feed?view=all", nil, http.StatusOK)
	if len(all.Items) != 2 || all.Items[0].Subscribed {
		t.Fatalf("recent feed after unsubscribe = %#v", all.Items)
	}
	// The generic subscription API keeps the opt-out when removing a record.
	requestJSON[domain.Subscription](t, handler, http.MethodDelete, "/api/subscriptions/project/project_aut", nil, http.StatusOK)
	if got := following(); len(got) != 0 {
		t.Fatalf("opt-out lost by generic unsubscribe: %v", got)
	}
	requestJSON[map[string]bool](t, handler, http.MethodPut, "/api/pulse/subscriptions/team/team_test", nil, http.StatusOK)
	requestJSON[map[string]bool](t, handler, http.MethodPut, "/api/pulse/subscriptions/project/project_aut", nil, http.StatusOK)
	if got := following(); !slices.Equal(got, []string{"project:member_update", "project:team_update"}) {
		t.Fatalf("after subscribing again = %v", got)
	}
	items := requestJSON[pulseFeedResponse](t, handler, http.MethodGet, "/api/pulse/feed?view=following", nil, http.StatusOK).Items
	if got := pulseReasonTypes(items[0].Reasons); !slices.Equal(got, []string{pulseReasonProjectMember, pulseReasonSubscribed, pulseReasonTeamProjectUpdates}) {
		t.Fatalf("member project reasons = %v", got)
	}
	if got := pulseReasonTypes(items[1].Reasons); !slices.Equal(got, []string{pulseReasonTeamProjectUpdates}) || items[1].Reasons[0].SourceNames[0] != "Test team" {
		t.Fatalf("team project reasons = %#v", items[1].Reasons)
	}
	requestJSON[map[string]string](t, handler, http.MethodPut, "/api/pulse/subscriptions/cycle/x", nil, http.StatusBadRequest)
	requestJSON[map[string]string](t, handler, http.MethodPut, "/api/pulse/subscriptions/project/missing", nil, http.StatusNotFound)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	index := slices.IndexFunc(bootstrap.Subscriptions, func(item domain.Subscription) bool {
		return item.ResourceType == "project" && item.ResourceID == "project_aut"
	})
	if index < 0 || !slices.Contains(bootstrap.Subscriptions[index].Events, "pulse") || len(bootstrap.Subscriptions[index].OptOutEvents) != 0 {
		t.Fatalf("stored subscription = %#v", bootstrap.Subscriptions)
	}
}

func TestPulseFeedRespectsFeatureFlagAndPagedBootstrapOmitsUpdates(t *testing.T) {
	repo := pulseTestRepo(t)
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	addPulseUpdates(t, repo, "project_aut", domain.ProjectUpdate{ID: "u1", Body: "Hello", CreatedAt: time.Now().UTC(), Comments: []domain.Comment{{ID: "c1", Body: "Nice"}}})
	paged := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/issue-records/bootstrap", nil, http.StatusOK)
	if len(paged.ProjectUpdates) != 0 || len(paged.InitiativeUpdates) != 0 {
		t.Fatalf("paged bootstrap still ships updates: %v", paged.ProjectUpdates)
	}
	updates := requestJSON[[]domain.ProjectUpdate](t, handler, http.MethodGet, "/api/projects/project_aut/updates", nil, http.StatusOK)
	if len(updates) != 1 || len(updates[0].Comments) != 1 {
		t.Fatalf("project updates = %#v", updates)
	}
	requestJSON[map[string]string](t, handler, http.MethodGet, "/api/projects/missing/updates", nil, http.StatusNotFound)
	err := repo.MutateWorkspace(t.Context(), "test-workspace", "workspace.settings_updated", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.FeatureFlags["pulse"] = false
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/pulse/feed", "/api/pulse/unread", "/api/pulse/capabilities"} {
		requestJSON[map[string]string](t, handler, http.MethodGet, path, nil, http.StatusForbidden)
	}
}

func TestPulseDiffBetweenSnapshots(t *testing.T) {
	priority := func(value int) *int { return &value }
	progress := func(value float64) *float64 { return &value }
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	previous := &domain.PulseSnapshot{CapturedAt: at, StatusID: "planned", Status: "Planned", Priority: priority(3), PriorityLabel: "Medium", LeadID: "a", Lead: "Ann", TargetDate: "2026-10-01", Progress: progress(0.25),
		Milestones: []domain.PulseMilestoneSnapshot{{ID: "m1", Name: "Beta", Progress: 0.5}, {ID: "m2", Name: "GA", Progress: 0}}, Projects: nil}
	next := &domain.PulseSnapshot{CapturedAt: at.Add(72 * time.Hour), StatusID: "started", Status: "In Progress", Priority: priority(1), PriorityLabel: "Urgent", LeadID: "b", Lead: "Bob", StartDate: "2026-09-02", TargetDate: "2026-10-01", Progress: progress(0.5),
		Milestones: []domain.PulseMilestoneSnapshot{{ID: "m1", Name: "Beta", Progress: 1}, {ID: "m2", Name: "GA", Progress: 0}, {ID: "m3", Name: "New", Progress: 0}}}
	diff := pulseDiffBetween(previous, next)
	if diff == nil || diff.Status == nil || diff.Status.From != "Planned" || diff.Status.To != "In Progress" || diff.Priority == nil || diff.Priority.To != "Urgent" || diff.Lead == nil || diff.Lead.ToID != "b" {
		t.Fatalf("diff = %#v", diff)
	}
	if diff.StartDate == nil || diff.StartDate.From != "" || diff.StartDate.To != "2026-09-02" || diff.TargetDate != nil {
		t.Fatalf("date diff = %#v %#v", diff.StartDate, diff.TargetDate)
	}
	if diff.ProgressSince == nil || !diff.ProgressSince.Date.Equal(at) || diff.ProgressSince.From != 0.25 || diff.ProgressSince.To != 0.5 {
		t.Fatalf("progress = %#v", diff.ProgressSince)
	}
	if len(diff.Milestones) != 2 || diff.Milestones[0].ID != "m1" || diff.Milestones[0].To != 1 || !diff.Milestones[1].Added {
		t.Fatalf("milestones = %#v", diff.Milestones)
	}
	if pulseDiffBetween(nil, next) != nil || pulseDiffBetween(next, next) != nil {
		t.Fatal("missing or identical snapshots must not produce a diff")
	}
	initiativeBefore := &domain.PulseSnapshot{Status: "Active", Projects: []domain.PulseRef{{ID: "p1", Name: "One"}, {ID: "p2", Name: "Two"}}}
	initiativeAfter := &domain.PulseSnapshot{Status: "Active", Projects: []domain.PulseRef{{ID: "p2", Name: "Two"}, {ID: "p3", Name: "Three"}}}
	projects := pulseDiffBetween(initiativeBefore, initiativeAfter).Projects
	if projects == nil || len(projects.Added) != 1 || projects.Added[0].ID != "p3" || len(projects.Removed) != 1 || projects.Removed[0].ID != "p1" {
		t.Fatalf("initiative projects diff = %#v", projects)
	}
}

func TestProjectUpdatesRecordSnapshotsAndDiffs(t *testing.T) {
	repo := pulseTestRepo(t)
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	first := requestJSON[domain.ProjectUpdate](t, handler, http.MethodPost, "/api/projects/project_aut/updates", map[string]any{"body": "First"}, http.StatusCreated)
	// Snapshots stay in storage; responses carry only the diff.
	stored, _ := repo.WorkspaceMetadataFields("test-workspace", "projectUpdates")
	if first.Snapshot != nil || stored.ProjectUpdates["project_aut"][0].Snapshot == nil || stored.ProjectUpdates["project_aut"][0].Snapshot.Status != "In Progress" || first.Diff != nil {
		t.Fatalf("first update snapshot=%#v stored=%#v diff=%#v", first.Snapshot, stored.ProjectUpdates["project_aut"][0].Snapshot, first.Diff)
	}
	err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "projects"), "test-workspace", "project.updated", "project_aut", nil, func(data *domain.Bootstrap) error {
		data.Projects[0].Status = domain.ProjectStatus{ID: "ps_completed", Name: "Completed", Type: "completed"}
		data.Projects[0].Priority, data.Projects[0].PriorityLabel = 1, "Urgent"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	preview := requestJSON[struct {
		Diff *domain.PulseDiff `json:"diff"`
	}](t, handler, http.MethodGet, "/api/projects/project_aut/updates/diff-preview", nil, http.StatusOK)
	if preview.Diff == nil || preview.Diff.Status == nil || preview.Diff.Status.To != "Completed" {
		t.Fatalf("preview = %#v", preview.Diff)
	}
	second := requestJSON[domain.ProjectUpdate](t, handler, http.MethodPost, "/api/projects/project_aut/updates", map[string]any{"body": "Second"}, http.StatusCreated)
	if second.Diff == nil || second.Diff.Status == nil || second.Diff.Status.From != "In Progress" || second.Diff.Status.To != "Completed" || second.Diff.Priority == nil || second.Diff.Priority.From != "High" {
		t.Fatalf("second diff = %#v", second.Diff)
	}
	feed := requestJSON[pulseFeedResponse](t, handler, http.MethodGet, "/api/pulse/feed?view=all", nil, http.StatusOK)
	raw, _ := json.Marshal(feed.Items[0])
	if feed.Items[0].Diff == nil || feed.Items[0].Diff.Status == nil || strings.Contains(string(raw), `"snapshot"`) {
		t.Fatalf("feed item diff = %s", raw)
	}
	initiative := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{"name": "Initiative", "projectIds": []string{"project_aut"}}, http.StatusCreated)
	one := requestJSON[domain.InitiativeUpdate](t, handler, http.MethodPost, "/api/initiatives/"+initiative.ID+"/updates", map[string]any{"body": "One"}, http.StatusCreated)
	storedInitiatives, _ := repo.WorkspaceMetadataFields("test-workspace", "initiativeUpdates")
	if one.Snapshot != nil || storedInitiatives.InitiativeUpdates[initiative.ID][0].Snapshot == nil || len(storedInitiatives.InitiativeUpdates[initiative.ID][0].Snapshot.Projects) != 1 {
		t.Fatalf("initiative snapshot = %#v", storedInitiatives.InitiativeUpdates[initiative.ID])
	}
	requestJSON[domain.Initiative](t, handler, http.MethodPatch, "/api/initiatives/"+initiative.ID, map[string]any{"projectIds": []string{"project_aut", "project_cruise"}}, http.StatusOK)
	two := requestJSON[domain.InitiativeUpdate](t, handler, http.MethodPost, "/api/initiatives/"+initiative.ID+"/updates", map[string]any{"body": "Two"}, http.StatusCreated)
	if two.Diff == nil || two.Diff.Projects == nil || len(two.Diff.Projects.Added) != 1 || two.Diff.Projects.Added[0].ID != "project_cruise" {
		t.Fatalf("initiative diff = %#v", two.Diff)
	}
}

func TestPulseWindowDeliversAtSixLocalTime(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	newYork, _ := time.LoadLocation("America/New_York")
	// 05:59 in Tokyo is not due; 06:00 is, with a 24 hour window.
	if _, _, due := pulseWindow("daily", time.Date(2026, 9, 13, 20, 59, 0, 0, time.UTC), tokyo); due {
		t.Fatal("daily summary before 06:00 Tokyo")
	}
	start, end, due := pulseWindow("daily", time.Date(2026, 9, 13, 21, 0, 0, 0, time.UTC), tokyo)
	if !due || !end.Equal(time.Date(2026, 9, 13, 21, 0, 0, 0, time.UTC)) || end.Sub(start) != 24*time.Hour {
		t.Fatalf("Tokyo window = %v..%v due=%v", start, end, due)
	}
	// Weekly summaries arrive on Monday morning local time only.
	if _, _, due := pulseWindow("weekly", time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC), newYork); !due {
		t.Fatal("weekly summary not due Monday 06:00 New York")
	}
	if _, _, due := pulseWindow("weekly", time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC), newYork); due {
		t.Fatal("weekly summary due before 06:00 New York")
	}
	if _, _, due := pulseWindow("never", time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC), time.UTC); due {
		t.Fatal("never is due")
	}
	if effectivePulseSchedule("", "weekly") != "weekly" || effectivePulseSchedule("default", "daily") != "daily" || effectivePulseSchedule("never", "daily") != "never" {
		t.Fatal("personal schedule mapping")
	}
	zones := pulseZones{}
	settings := map[string]domain.TeamSettings{"t1": {Timezone: "Europe/Berlin"}}
	if zone := pulseUserZone(zones, domain.UserSettings{Timezone: "Asia/Tokyo"}, []domain.TeamMember{{TeamID: "t1"}}, settings); zone.String() != "Asia/Tokyo" {
		t.Fatalf("user zone = %v", zone)
	}
	if zone := pulseUserZone(zones, domain.UserSettings{Timezone: "Not/AZone"}, []domain.TeamMember{{TeamID: "t1"}}, settings); zone.String() != "Europe/Berlin" {
		t.Fatalf("team fallback zone = %v", zone)
	}
	if zone := pulseUserZone(zones, domain.UserSettings{}, nil, settings); zone != time.UTC {
		t.Fatalf("fallback zone = %v", zone)
	}
}

func pulseSummaries(repo *store.SQLiteStore) map[string]domain.Notification {
	result := map[string]domain.Notification{}
	for _, notification := range repo.Bootstrap().Notifications {
		if notification.Type == "pulseSummary" {
			result[notification.RecipientID] = notification
		}
	}
	return result
}

func setPulseTimezone(t *testing.T, repo *store.SQLiteStore, zones map[string]string) {
	t.Helper()
	err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "userSettings"), "test-workspace", "workspace.agent_guidance_updated", "", nil, func(data *domain.Bootstrap) error {
		for userID, zone := range zones {
			settings := data.UserSettings[userID]
			settings.UserID, settings.Timezone = userID, zone
			data.UserSettings[userID] = settings
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPulseSummariesArriveAtSixLocalAcrossTimeZones(t *testing.T) {
	repo := pulseTestRepo(t)
	s := &server{store: repo}
	setPulseTimezone(t, repo, map[string]string{"usr_admin": "Asia/Tokyo", "usr_member": "America/Los_Angeles"})
	author := domain.User{ID: "usr_writer", Name: "Writer", DisplayName: "Writer"}
	addPulseUpdates(t, repo, "project_aut", domain.ProjectUpdate{ID: "window_update", Body: "Shipped beta.", User: author, CreatedAt: time.Date(2026, 9, 13, 20, 0, 0, 0, time.UTC)})
	err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "projects"), "test-workspace", "project.updated", "project_aut", nil, func(data *domain.Bootstrap) error {
		data.Projects[0].MemberIDs = []string{"usr_admin", "usr_member"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// 12:00 UTC: 21:00 in Tokyo (due since 06:00), 05:00 in Los Angeles (not yet).
	if err := s.preparePulseSummaries(t.Context(), "test-workspace", time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	summaries := pulseSummaries(repo)
	admin, ok := summaries["usr_admin"]
	if !ok || len(summaries) != 1 {
		t.Fatalf("summaries at 12:00 UTC = %v", summaries)
	}
	if admin.Title != "Daily Pulse" || admin.Text != "Update from Test project" || admin.Category != "pulse" || admin.OccurrenceCount != 1 || admin.Payload == nil || !slices.Equal(admin.Payload.UpdateIDs, []string{"window_update"}) || admin.Payload.Schedule != "daily" {
		t.Fatalf("admin summary = %#v payload=%#v", admin, admin.Payload)
	}
	settings, _ := repo.WorkspaceMetadataFields("test-workspace", "settings")
	cursors := pulseCursors(&settings)
	if !cursors["usr_admin"].Equal(time.Date(2026, 9, 13, 21, 0, 0, 0, time.UTC)) || !cursors["usr_member"].IsZero() {
		t.Fatalf("cursors = %v", cursors)
	}
	// 13:00 UTC is 06:00 in Los Angeles.
	if err := s.preparePulseSummaries(t.Context(), "test-workspace", time.Date(2026, 9, 14, 13, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if member, ok := pulseSummaries(repo)["usr_member"]; !ok || member.Text != "Update from Test project" {
		t.Fatalf("member summary at 06:00 Los Angeles = %#v", pulseSummaries(repo))
	}
	if len(pulseSummaries(repo)) != 2 {
		t.Fatal("admin received a second summary")
	}
}

func TestPulseSummariesSkipGuestsOwnUpdatesAndDisabledCategory(t *testing.T) {
	repo := pulseTestRepo(t)
	s := &server{store: repo}
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	addPulseUpdates(t, repo, "project_aut", domain.ProjectUpdate{ID: "admin_update", Body: "Mine", User: domain.User{ID: "usr_admin", Name: "Test admin"}, CreatedAt: now.Add(-5 * time.Hour)})
	if err := repo.UpdateMemberRole(t.Context(), "workspace_test", "usr_member", "guest"); err != nil {
		t.Fatal(err)
	}
	if err := s.preparePulseSummaries(t.Context(), "test-workspace", now); err != nil {
		t.Fatal(err)
	}
	if summaries := pulseSummaries(repo); len(summaries) != 0 {
		t.Fatalf("summaries = %v, want none (own update, guest)", summaries)
	}
	settings, _ := repo.WorkspaceMetadataFields("test-workspace", "settings")
	if cursors := pulseCursors(&settings); cursors["usr_admin"].IsZero() || cursors["usr_member"].IsZero() {
		t.Fatalf("due members' cursors must still advance: %v", cursors)
	}
}

func TestPulseSummaryText(t *testing.T) {
	ref := func(source string) domain.PulseUpdateRef { return domain.PulseUpdateRef{Source: source} }
	cases := []struct {
		refs  []domain.PulseUpdateRef
		total int
		want  string
	}{
		{[]domain.PulseUpdateRef{ref("Mobile app")}, 1, "Update from Mobile app"},
		{[]domain.PulseUpdateRef{ref("Mobile app"), ref("Mobile app")}, 2, "2 updates from Mobile app"},
		{[]domain.PulseUpdateRef{ref("Mobile app"), ref("API")}, 2, "API and Mobile app"},
		{[]domain.PulseUpdateRef{ref("Mobile app"), ref("API"), ref("Billing")}, 3, "API, Billing and 1 other update"},
		{[]domain.PulseUpdateRef{ref("Mobile app"), ref("API"), ref("Billing")}, 7, "API, Billing and 5 other updates"},
		{nil, 0, ""},
	}
	for _, test := range cases {
		if got := pulseSummaryText(test.refs, test.total); got != test.want {
			t.Errorf("pulseSummaryText(%v, %d) = %q, want %q", test.refs, test.total, got, test.want)
		}
	}
	if pulseSummaryTitle("daily") != "Daily Pulse" || pulseSummaryTitle("weekly") != "Weekly Pulse" {
		t.Fatal("summary titles")
	}
	if got := extractiveSummary("## Status\nWe shipped **beta** to [customers](https://x). Next we fix bugs. Then GA."); got != "Status We shipped beta to customers. Next we fix bugs." {
		t.Fatalf("extractive summary = %q", got)
	}
	if got := readableNotificationLine(domain.Notification{Title: "Daily Pulse", Text: "Update from API"}); got != "Daily Pulse: Update from API" {
		t.Fatalf("email line = %q", got)
	}
}

// pulseSummaryFixture creates one Daily Pulse notification for the
// development viewer (usr_admin) covering one project update by a member.
func pulseSummaryFixture(t *testing.T) (*store.SQLiteStore, string) {
	t.Helper()
	repo := pulseTestRepo(t)
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	addPulseUpdates(t, repo, "project_aut", domain.ProjectUpdate{ID: "summary_update", Body: "We shipped the beta to five customers. Feedback is positive. Next we harden billing.", Health: "atRisk", CreatedAt: now.Add(-5 * time.Hour)})
	if err := (&server{store: repo}).preparePulseSummaries(t.Context(), "test-workspace", now); err != nil {
		t.Fatal(err)
	}
	notification, ok := pulseSummaries(repo)["usr_admin"]
	if !ok {
		t.Fatal("no summary notification")
	}
	return repo, notification.ID
}

func TestPulseSummaryFallsBackToExtractiveWithoutAgent(t *testing.T) {
	repo, id := pulseSummaryFixture(t)
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	summary := requestJSON[domain.PulseSummary](t, handler, http.MethodGet, "/api/pulse/summaries/"+id, nil, http.StatusOK)
	if summary.AI || summary.Title != "Daily Pulse" || len(summary.Sections) != 1 || summary.Sections[0].Kind != "project" {
		t.Fatalf("summary = %#v", summary)
	}
	item := summary.Sections[0].Items[0]
	if item.UpdateID != "summary_update" || item.SourceName != "Test project" || item.Health != "atRisk" || item.Summary != "We shipped the beta to five customers. Feedback is positive." {
		t.Fatalf("summary item = %#v", item)
	}
	if !strings.HasPrefix(summary.Text, "Daily Pulse. Project updates. Test project, at risk: We shipped") || len([]rune(summary.Text)) > pulseSummaryTextLimit {
		t.Fatalf("summary text = %q", summary.Text)
	}
	cached := requestJSON[domain.PulseSummary](t, handler, http.MethodGet, "/api/pulse/summaries/"+id, nil, http.StatusOK)
	if !cached.GeneratedAt.Equal(summary.GeneratedAt) {
		t.Fatal("summary was generated twice")
	}
	requestJSON[map[string]bool](t, handler, http.MethodPost, "/api/pulse/summaries/"+id+"/report", map[string]string{"reason": "Wrong numbers", "updateId": "summary_update"}, http.StatusOK)
	requestJSON[map[string]string](t, handler, http.MethodPost, "/api/pulse/summaries/"+id+"/report", map[string]string{"reason": " "}, http.StatusBadRequest)
	record, err := repo.NotificationRecord(t.Context(), "test-workspace", "usr_admin", id)
	if err != nil || len(record.SummaryReports) != 1 || record.SummaryReports[0].Reason != "Wrong numbers" || record.PulseSummary == nil {
		t.Fatalf("stored record = %#v (%v)", record, err)
	}
	requestJSON[map[string]string](t, handler, http.MethodGet, "/api/pulse/summaries/notification-1", nil, http.StatusNotFound)
	if got := requestJSON[map[string]bool](t, handler, http.MethodGet, "/api/pulse/capabilities", nil, http.StatusOK); got["aiSummaries"] || got["audio"] {
		t.Fatalf("capabilities = %v", got)
	}
	requestJSON[map[string]string](t, handler, http.MethodGet, "/api/pulse/summaries/"+id+"/audio", nil, http.StatusNotFound)
}

func TestPulseSummaryUsesAgentProvider(t *testing.T) {
	repo, id := pulseSummaryFixture(t)
	var prompt string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, message := range body.Messages {
			prompt += message.Role + ":" + message.Content + "\n"
		}
		reply, _ := json.Marshal(map[string]any{"summaries": []map[string]string{{"updateId": "summary_update", "summary": "Beta reached five customers; billing hardening is next."}, {"updateId": "invented", "summary": "Ignored"}}})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "```json\n" + string(reply) + "\n```"}}}})
	}))
	defer provider.Close()
	s := &server{store: repo, uploadPath: t.TempDir(), authDisabled: true}
	s.agent.Enabled, s.agent.Protocol, s.agent.BaseURL, s.agent.Model, s.agent.MaxOutputTokens, s.agent.Timeout = true, "openai-chat-completions", provider.URL, "test-model", 1024, 5*time.Second
	handler := newHandler(s)
	summary := requestJSON[domain.PulseSummary](t, handler, http.MethodGet, "/api/pulse/summaries/"+id, nil, http.StatusOK)
	if !summary.AI || summary.Sections[0].Items[0].Summary != "Beta reached five customers; billing hardening is next." {
		t.Fatalf("AI summary = %#v", summary)
	}
	if !strings.Contains(prompt, "Never add facts") || !strings.Contains(prompt, "We shipped the beta to five customers") || !strings.Contains(prompt, `"updateId":"summary_update"`) {
		t.Fatalf("provider prompt = %s", prompt)
	}
	if got := requestJSON[map[string]bool](t, handler, http.MethodGet, "/api/pulse/capabilities", nil, http.StatusOK); !got["aiSummaries"] {
		t.Fatalf("capabilities = %v", got)
	}
}

func TestPulseSummaryFallsBackWhenAgentFails(t *testing.T) {
	repo, id := pulseSummaryFixture(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"overloaded"}}`, http.StatusServiceUnavailable)
	}))
	defer provider.Close()
	s := &server{store: repo, uploadPath: t.TempDir(), authDisabled: true}
	s.agent.Enabled, s.agent.Protocol, s.agent.BaseURL, s.agent.Model, s.agent.MaxOutputTokens = true, "openai-chat-completions", provider.URL, "test-model", 1024
	summary := requestJSON[domain.PulseSummary](t, newHandler(s), http.MethodGet, "/api/pulse/summaries/"+id, nil, http.StatusOK)
	if summary.AI || summary.Sections[0].Items[0].Summary == "" {
		t.Fatalf("fallback summary = %#v", summary)
	}
}

func TestPulseSummaryAudioIsSynthesizedOnceAndCached(t *testing.T) {
	repo, id := pulseSummaryFixture(t)
	calls := 0
	var request map[string]string
	var authorization string
	speech := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		authorization = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/audio/speech" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3-fake-mp3"))
	}))
	defer speech.Close()
	s := &server{store: repo, uploadPath: t.TempDir(), authDisabled: true}
	s.tts.Enabled, s.tts.BaseURL, s.tts.APIKey, s.tts.Model, s.tts.Voice = true, speech.URL+"/v1", "speech-key", "gpt-4o-mini-tts", "alloy"
	handler := newHandler(s)
	for range 2 {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/pulse/summaries/"+id+"/audio", nil))
		if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "audio/mpeg" || recorder.Body.String() != "ID3-fake-mp3" {
			t.Fatalf("audio response %d %q %q", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("speech provider called %d times, want 1 (cached)", calls)
	}
	if authorization != "Bearer speech-key" || request["model"] != "gpt-4o-mini-tts" || request["voice"] != "alloy" || !strings.HasPrefix(request["input"], "Daily Pulse.") {
		t.Fatalf("speech request = %v auth=%q", request, authorization)
	}
	if got := requestJSON[map[string]bool](t, handler, http.MethodGet, "/api/pulse/capabilities", nil, http.StatusOK); !got["audio"] {
		t.Fatalf("capabilities = %v", got)
	}
}

func TestStatusUpdatesMCPFiltersAndSorts(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	edited := now.Add(-time.Hour)
	ann := domain.User{ID: "ann", Name: "ann", DisplayName: "Ann", Email: "ann@example.test"}
	bob := domain.User{ID: "bob", Name: "bob", DisplayName: "Bob"}
	archived := now
	data := domain.Bootstrap{
		Viewer:      ann,
		Projects:    []domain.Project{{ID: "p1", Name: "Alpha", Initiatives: []string{"i1"}}, {ID: "p2", Name: "Beta"}, {ID: "p3", Name: "Gone", ArchivedAt: &archived}},
		Initiatives: []domain.Initiative{{ID: "i1", Name: "Growth", ProjectIDs: []string{"p1"}}},
		ProjectUpdates: map[string][]domain.ProjectUpdate{
			"p1": {{ID: "a2", User: ann, CreatedAt: now.Add(-2 * time.Hour), EditedAt: &edited}, {ID: "a1", User: bob, CreatedAt: now.Add(-48 * time.Hour)}},
			"p2": {{ID: "b1", User: bob, CreatedAt: now.Add(-3 * time.Hour)}, {ID: "b0", User: bob, CreatedAt: now.Add(-3 * time.Hour)}},
			"p3": {{ID: "c1", User: ann, CreatedAt: now.Add(-time.Minute)}},
		},
		InitiativeUpdates: map[string][]domain.InitiativeUpdate{"i1": {{ID: "g1", User: ann, CreatedAt: now.Add(-time.Hour)}}},
	}
	list := func(args map[string]any) []string {
		t.Helper()
		result, err := statusUpdates(data, args)
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, item := range result.(map[string]any)["items"].([]mcpStatusUpdate) {
			ids = append(ids, item.ID)
		}
		return ids
	}
	if got := list(map[string]any{"type": "project"}); !slices.Equal(got, []string{"a2", "b0", "b1", "a1"}) {
		t.Fatalf("default order = %v", got)
	}
	if got := list(map[string]any{"type": "project", "includeArchived": true}); got[0] != "c1" {
		t.Fatalf("includeArchived = %v", got)
	}
	if got := list(map[string]any{"type": "project", "project": "Beta"}); !slices.Equal(got, []string{"b0", "b1"}) {
		t.Fatalf("project filter = %v", got)
	}
	if got := list(map[string]any{"type": "project", "initiative": "Growth"}); !slices.Equal(got, []string{"a2", "a1"}) {
		t.Fatalf("initiative filter = %v", got)
	}
	if got := list(map[string]any{"type": "project", "user": "me"}); !slices.Equal(got, []string{"a2"}) {
		t.Fatalf("user filter = %v", got)
	}
	if got := list(map[string]any{"type": "project", "user": "Bob", "createdAt": now.Add(-24 * time.Hour).Format(time.RFC3339)}); !slices.Equal(got, []string{"b0", "b1"}) {
		t.Fatalf("createdAt filter = %v", got)
	}
	if got := list(map[string]any{"type": "project", "updatedAt": "-P1D", "orderBy": "updatedAt"}); len(got) == 0 || got[0] != "a2" {
		t.Fatalf("updatedAt order = %v", got)
	}
	if got := list(map[string]any{"type": "initiative", "initiative": "i1"}); !slices.Equal(got, []string{"g1"}) {
		t.Fatalf("initiative updates = %v", got)
	}
	if _, err := statusUpdates(data, map[string]any{"type": "project", "orderBy": "health"}); err == nil {
		t.Fatal("invalid orderBy accepted")
	}
	if _, err := statusUpdates(data, map[string]any{"type": "project", "createdAt": "yesterday"}); err == nil {
		t.Fatal("invalid date accepted")
	}
}

func TestPulseUpdateAuthorChecksAndAuthorNotifications(t *testing.T) {
	repo := pulseTestRepo(t)
	key := "test-workspace"
	s := &server{store: repo, uploadPath: t.TempDir()}
	host := httptest.NewServer(newHandler(s))
	defer host.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	authRequest[domain.WorkspaceMember](t, admin, "PATCH", host.URL+"/api/workspaces/"+key+"/members/usr_member", map[string]string{"role": "member"}, "", http.StatusOK)
	member := authClient(t)
	authRequest[domain.AuthSession](t, member, "POST", host.URL+"/api/auth/login", map[string]string{"email": "member@example.test", "password": "test-password"}, "", http.StatusOK)

	update := authRequest[domain.ProjectUpdate](t, admin, "POST", host.URL+"/api/projects/project_aut/updates", map[string]any{"body": "Admin update"}, key, http.StatusCreated)
	path := host.URL + "/api/projects/project_aut/updates/" + update.ID
	authRequest[map[string]string](t, member, "PATCH", path, map[string]any{"body": "Hijacked"}, key, http.StatusForbidden)
	authRequest[map[string]string](t, member, "DELETE", path, nil, key, http.StatusForbidden)
	own := authRequest[domain.ProjectUpdate](t, member, "POST", host.URL+"/api/projects/project_aut/updates", map[string]any{"body": "Member update"}, key, http.StatusCreated)
	authRequest[domain.ProjectUpdate](t, member, "PATCH", host.URL+"/api/projects/project_aut/updates/"+own.ID, map[string]any{"body": "Edited by author"}, key, http.StatusOK)
	// Admins may edit anyone's update.
	authRequest[domain.ProjectUpdate](t, admin, "PATCH", host.URL+"/api/projects/project_aut/updates/"+own.ID, map[string]any{"body": "Edited by admin"}, key, http.StatusOK)

	initiative := authRequest[domain.Initiative](t, admin, "POST", host.URL+"/api/initiatives", map[string]any{"name": "Initiative"}, key, http.StatusCreated)
	initiativeUpdate := authRequest[domain.InitiativeUpdate](t, admin, "POST", host.URL+"/api/initiatives/"+initiative.ID+"/updates", map[string]any{"body": "Initiative update"}, key, http.StatusCreated)
	authRequest[map[string]string](t, member, "PATCH", host.URL+"/api/initiatives/"+initiative.ID+"/updates/"+initiativeUpdate.ID, map[string]any{"body": "x"}, key, http.StatusForbidden)
	authRequest[map[string]string](t, member, "DELETE", host.URL+"/api/initiatives/"+initiative.ID+"/updates/"+initiativeUpdate.ID, nil, key, http.StatusForbidden)
	authRequest[map[string]string](t, member, "DELETE", path+"/attachments/missing", nil, key, http.StatusForbidden)

	// Comments and reactions on an update notify its author, never the actor.
	authRequest[domain.ProjectUpdate](t, member, "POST", path+"/comments", map[string]any{"body": "Great work", "bodyData": map[string]any{"type": "doc"}}, key, http.StatusCreated)
	authRequest[domain.ProjectUpdate](t, member, "POST", path+"/reactions", map[string]any{"emoji": "🎉"}, key, http.StatusOK)
	authRequest[domain.ProjectUpdate](t, admin, "POST", path+"/comments", map[string]any{"body": "Thanks"}, key, http.StatusCreated)
	authRequest[domain.InitiativeUpdate](t, member, "POST", host.URL+"/api/initiatives/"+initiative.ID+"/updates/"+initiativeUpdate.ID+"/comments", map[string]any{"body": "Noted"}, key, http.StatusCreated)
	inbox := authRequest[store.NotificationPage](t, admin, "GET", host.URL+"/api/notifications", nil, key, http.StatusOK)
	types := map[string]int{}
	for _, notification := range inbox.Notifications {
		if notification.Category == "updates" && strings.HasPrefix(notification.Type, "update") {
			types[notification.Type]++
			if notification.Actor.ID != "usr_member" || notification.Title == "" || notification.Text == "" {
				t.Fatalf("author notification = %#v", notification)
			}
		}
	}
	if types[notificationUpdateComment] != 2 || types[notificationUpdateReaction] != 1 {
		t.Fatalf("admin notifications = %v", types)
	}
	memberInbox := authRequest[store.NotificationPage](t, member, "GET", host.URL+"/api/notifications", nil, key, http.StatusOK)
	for _, notification := range memberInbox.Notifications {
		if strings.HasPrefix(notification.Type, "update") {
			t.Fatalf("member notified about own activity: %#v", notification)
		}
	}
	updates := authRequest[[]domain.ProjectUpdate](t, admin, "GET", host.URL+"/api/projects/project_aut/updates", nil, key, http.StatusOK)
	index := slices.IndexFunc(updates, func(item domain.ProjectUpdate) bool { return item.ID == update.ID })
	if index < 0 || len(updates[index].Comments) != 2 || updates[index].Comments[0].BodyData["type"] != "doc" {
		t.Fatalf("update comments = %#v", updates)
	}

	// Guests never get Pulse.
	authRequest[domain.WorkspaceMember](t, admin, "PATCH", host.URL+"/api/workspaces/"+key+"/members/usr_member", map[string]string{"role": "guest"}, "", http.StatusOK)
	authRequest[map[string]string](t, member, "GET", host.URL+"/api/pulse/feed", nil, key, http.StatusForbidden)
	feed := authRequest[pulseFeedResponse](t, admin, "GET", host.URL+"/api/pulse/feed?view=all", nil, key, http.StatusOK)
	if len(feed.Items) < 3 {
		t.Fatalf("admin feed = %v", feedIDs(feed))
	}
}

func TestPulseFeedHidesPrivateTeamUpdates(t *testing.T) {
	repo := pulseTestRepo(t)
	key := "test-workspace"
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	defer host.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	authRequest[domain.WorkspaceMember](t, admin, "PATCH", host.URL+"/api/workspaces/"+key+"/members/usr_member", map[string]string{"role": "member"}, "", http.StatusOK)
	private := authRequest[domain.Team](t, admin, "POST", host.URL+"/api/workspaces/"+key+"/teams", map[string]any{"name": "Private", "key": "PRV", "private": true}, "", http.StatusCreated)
	err := repo.MutateWorkspace(context.Background(), key, "test.pulse", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: "project_private", Name: "Private project", TeamIDs: []string{private.ID}, MemberIDs: []string{"usr_member"}})
		data.ProjectUpdates["project_private"] = []domain.ProjectUpdate{{ID: "secret", ProjectID: "project_private", Body: "Secret", User: domain.User{ID: "usr_admin"}, CreatedAt: time.Now().UTC()}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	member := authClient(t)
	authRequest[domain.AuthSession](t, member, "POST", host.URL+"/api/auth/login", map[string]string{"email": "member@example.test", "password": "test-password"}, "", http.StatusOK)
	for _, view := range []string{"all", "following", "popular"} {
		feed := authRequest[pulseFeedResponse](t, member, "GET", host.URL+"/api/pulse/feed?view="+view, nil, key, http.StatusOK)
		if slices.Contains(feedIDs(feed), "project:secret") {
			t.Fatalf("%s feed leaked a private team update", view)
		}
	}
	if adminFeed := authRequest[pulseFeedResponse](t, admin, "GET", host.URL+"/api/pulse/feed?view=all", nil, key, http.StatusOK); !slices.Contains(feedIDs(adminFeed), "project:secret") {
		t.Fatal("admin cannot see the private update")
	}
	authRequest[map[string]string](t, member, "PUT", host.URL+"/api/pulse/subscriptions/project/project_private", nil, key, http.StatusNotFound)
}
