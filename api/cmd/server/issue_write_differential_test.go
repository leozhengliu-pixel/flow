package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// Generated ids (idgen stamps), timestamps and secrets differ between the two
// runs of a pair; everything else must match byte for byte.
var (
	issueWriteDifferentialGeneratedID = regexp.MustCompile(`[A-Za-z_-]*[0-9]{19}_[0-9a-z]{10}`)
	issueWriteDifferentialTimestamp   = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+(Z|[+-][0-9:]+)`)
	issueWriteDifferentialShareToken  = regexp.MustCompile(`issue_share_[A-Za-z0-9_-]+`)
	// Each pair runs after the previous runs: a state move takes the next
	// position at the top of the column, and the shared release lists the
	// complete run's issue before the scoped run adds its own.
	sortOrder     = regexp.MustCompile(`"sortOrder":-?[0-9.e+-]+`)
	repeatedIssue = regexp.MustCompile(`"<issue>"(,"<issue>")+`)
)

type issueWriteDifferentialPrincipal struct {
	name    string
	cookies []*http.Cookie
	bearer  string
}

type issueWriteDifferentialVariant struct {
	name string
	seed func(data *domain.Bootstrap, issue *domain.Issue)
}

type issueWriteDifferentialPair struct {
	old, new, oldIdentifier, newIdentifier string
}

func (p issueWriteDifferentialPair) normalize(raw string) string {
	for _, from := range []string{p.old, p.new} {
		raw = strings.ReplaceAll(raw, from, "<issue>")
	}
	for _, from := range []string{p.oldIdentifier, p.newIdentifier} {
		raw = strings.ReplaceAll(raw, from, "<identifier>")
	}
	raw = issueWriteDifferentialGeneratedID.ReplaceAllString(raw, "<generated>")
	raw = issueWriteDifferentialShareToken.ReplaceAllString(raw, "<share-token>")
	raw = sortOrder.ReplaceAllString(raw, `"sortOrder":<position>`)
	raw = repeatedIssue.ReplaceAllString(raw, `"<issue>"`)
	return issueWriteDifferentialTimestamp.ReplaceAllString(raw, "<time>")
}

// TestIssueWriteDifferential compares the scoped single-issue
// write reads with the complete reads they replace (completeIssueWriteReads)
// on the same routes, principals and issue shapes: status, response body,
// the stored issue row, its comments, its domain events and the
// notifications it produced.
func TestIssueWriteDifferential(t *testing.T) {
	for _, mode := range []struct {
		name               string
		hydration, journal string
		bounded            bool
	}{
		{"legacy-hydrated", "on", "on", false},
		{"legacy", "off", "off", false},
		// The same fixture served from the bounded metadata view: the
		// scoped reads never run there, and both runs must still agree.
		{"bounded", "on", "on", true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", "off")
			t.Setenv("FLOW_RECORD_HYDRATION", mode.hydration)
			t.Setenv("FLOW_RECORD_JOURNAL", mode.journal)
			for driver, repo := range scaleServerTestStores(t) {
				t.Run(driver, func(t *testing.T) { issueWriteDifferentialDifferential(t, repo, mode.bounded) })
			}
		})
	}
}

func issueWriteDifferentialDifferential(t *testing.T, repo *store.SQLiteStore, bounded bool) {
	t.Cleanup(func() { completeIssueWriteReads.Store(false) })
	f := resourceAccessScaleFixture(t, repo)
	repo.SetWebhookSink(func(string, domain.DomainEvent) {})
	workspace, owner := f.data.Workspace.URLKey, f.data.Viewer
	public := f.data.Teams[0]
	private := domain.Team{ID: idgen.New("sw244-private-"), Key: "SWP", Name: "Private", Private: true}
	closed := domain.Team{ID: idgen.New("sw244-archived-"), Key: "SWX", Name: "Archived"}
	stamp := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)
	closed.ArchivedAt = &stamp
	publicProject, privateProject := idgen.New("sw244-project-"), idgen.New("sw244-private-project-")
	publicCycle := idgen.New("sw244-cycle-")
	var stateID string
	err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw244_differential", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, private, closed)
		data.TeamSettings[private.ID] = domain.TeamSettings{TeamID: private.ID, Access: "private"}
		settings := data.TeamSettings[public.ID]
		settings.IssueSharingEnabled = true
		data.TeamSettings[public.ID] = settings
		data.WorkspaceSettings.FeatureFlags = map[string]bool{"releases": true, "loops": true, "triage-intelligence": true}
		data.Releases = append(data.Releases, domain.Release{ID: "sw244-release", Name: "Release"})
		data.Projects = append(data.Projects,
			domain.Project{ID: publicProject, Name: "Public project", TeamIDs: []string{public.ID}},
			domain.Project{ID: privateProject, Name: "Private project", TeamIDs: []string{private.ID}},
		)
		for i := range 20 {
			// Unrelated projects with progress history.
			history := make([]domain.ProjectProgressHistoryPoint, 50)
			data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprintf("sw244-unrelated-%02d", i), Name: "Unrelated", TeamIDs: []string{public.ID}, ProgressHistory: history, ScopeHistory: history})
		}
		data.Cycles = append(data.Cycles, domain.Cycle{ID: publicCycle, TeamID: public.ID, Number: 1, StartsAt: stamp, EndsAt: stamp.Add(14 * 24 * time.Hour)})
		stateID = data.States[1].ID
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	origin, err := url.Parse(f.host.URL)
	if err != nil {
		t.Fatal(err)
	}
	principals := []issueWriteDifferentialPrincipal{{name: "owner", cookies: f.admin.Jar.Cookies(origin)}}
	users := map[string]domain.User{}
	for _, spec := range []struct {
		name, role string
		teams      []string
	}{
		{"admin-private-outsider", "admin", []string{public.ID}},
		{"member", "member", []string{public.ID}},
		{"guest", "guest", []string{public.ID}},
		{"private-member", "member", []string{private.ID}},
		{"inherited-grant", "member", []string{public.ID}},
	} {
		client, user := resourceAccessScaleJoin(t, f, spec.name, idgen.New("sw244-user-")+"@example.test", spec.role, spec.teams...)
		principals = append(principals, issueWriteDifferentialPrincipal{name: spec.name, cookies: client.Jar.Cookies(origin)})
		users[spec.name] = user
	}
	type created struct {
		Key    domain.APIKey `json:"key"`
		Secret string        `json:"secret"`
	}
	key := func(name string, teams []string) created {
		input := map[string]any{"name": name + " " + idgen.New(""), "scopes": []string{"read", "write"}}
		if teams != nil {
			input["teamIds"] = teams
		}
		return authRequest[created](t, f.admin, http.MethodPost, f.host.URL+"/api/api-keys", input, workspace, http.StatusCreated)
	}
	principals = append(principals,
		issueWriteDifferentialPrincipal{name: "selected-team-key", bearer: key("selected", []string{public.ID}).Secret},
		issueWriteDifferentialPrincipal{name: "excluded-team-key", bearer: key("excluded", []string{private.ID}).Secret},
	)
	revoked := key("revoked", nil)
	request, _ := http.NewRequest(http.MethodDelete, f.host.URL+"/api/api-keys/"+revoked.Key.ID, nil)
	request.Header.Set("X-Workspace-Key", workspace)
	if response, err := f.admin.Do(request); err != nil || response.StatusCode >= 300 {
		t.Fatalf("revoke key: %v %v", err, response)
	} else {
		_ = response.Body.Close()
	}
	principals = append(principals, issueWriteDifferentialPrincipal{name: "revoked-key", bearer: revoked.Secret})

	granted := users["inherited-grant"].ID
	variants := []issueWriteDifferentialVariant{
		{"public", func(*domain.Bootstrap, *domain.Issue) {}},
		{"private-child", func(data *domain.Bootstrap, issue *domain.Issue) {
			// The parent's grant reaches the child.
			parent := domain.Issue{ID: issue.ID + "-parent", Identifier: issue.Identifier + "0", Number: issue.Number*10 + 1, Title: "Parent", Team: private, Creator: owner, State: issue.State, Version: 1, CreatedAt: stamp, UpdatedAt: stamp,
				Permissions: []domain.IssuePermission{{ID: issue.ID + "-grant", IssueID: issue.ID + "-parent", SubjectType: "user", SubjectID: granted, Role: "editor"}}}
			data.Issues = append(data.Issues, parent)
			issue.Team, issue.ParentID = private, &parent.ID
		}},
		{"archived", func(_ *domain.Bootstrap, issue *domain.Issue) { issue.ArchivedAt = &stamp }},
		{"archived-team", func(_ *domain.Bootstrap, issue *domain.Issue) { issue.Team = closed }},
		{"missing", nil},
	}
	routes := []issueWriteScopeBudgetRoute{
		{name: "comment", method: "POST", tail: "/comments", body: `{"body":"A plain comment"}`},
		{name: "priority", method: "PATCH", body: `{"priority":2}`},
		{name: "state", method: "PATCH", body: fmt.Sprintf(`{"stateId":%q}`, stateID)},
		{name: "assignee", method: "PATCH", body: fmt.Sprintf(`{"assigneeId":%q}`, users["member"].ID)},
		{name: "title", method: "PATCH", body: `{"title":"Changed title"}`},
		{name: "project", method: "PATCH", body: fmt.Sprintf(`{"projectId":%q}`, publicProject)},
		{name: "private-project", method: "PATCH", body: fmt.Sprintf(`{"projectId":%q}`, privateProject)},
		{name: "cycle", method: "PATCH", body: fmt.Sprintf(`{"cycleId":%q}`, publicCycle)},
		{name: "team-move", method: "PATCH", body: fmt.Sprintf(`{"teamId":%q}`, private.ID)},
		{name: "delete", method: "DELETE"},
		{name: "share", method: "POST", tail: "/share"},
		{name: "replace-permissions", method: "PUT", tail: "/permissions", body: `{"permissions":[{"subjectType":"workspace","role":"commenter"}]}`},
		{name: "issue-reaction", method: "POST", tail: "/reactions", body: `{"emoji":"thumbsup"}`},
		{name: "edit-comment", method: "PATCH", tail: "/comments/@comment", body: `{"body":"Edited comment"}`},
		{name: "delete-comment", method: "DELETE", tail: "/comments/@comment"},
		{name: "comment-reaction", method: "POST", tail: "/comments/@comment/reactions", body: `{"emoji":"thumbsup"}`},
		{name: "subscribe", method: "PUT", tail: "/comments/@comment/subscription", body: `{"state":"subscribed"}`},
		{name: "relation", method: "POST", tail: "/relations", body: `{"type":"related","relatedIssueId":"@target"}`},
		{name: "link", method: "POST", tail: "/links", body: `{"title":"Example","url":"https://example.test/"}`},
		{name: "delete-attachment", method: "DELETE", tail: "/attachments/@link"},
		{name: "reminder", method: "POST", tail: "/reminders", body: `{"remindAt":"2030-01-02T03:04:05Z"}`},
		{name: "releases", method: "PUT", tail: "/releases", body: `{"releaseIds":["sw244-release"]}`},
		{name: "resolve-action", method: "POST", tail: "/comment-actions/missing", body: `{"status":"dismissed"}`},
		{name: "accept-suggestion", method: "POST", tail: "/suggestions/missing/accept"},
		{name: "read-actions", method: "GET", tail: "/comment-actions"},
	}

	// Seed one issue per run: the pair differs only in ids and identifiers.
	pairs := map[string]issueWriteDifferentialPair{}
	number := 1000
	err = repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw244_differential_issues", "", nil, func(data *domain.Bootstrap) error {
		for _, variant := range variants {
			for _, route := range routes {
				for _, principal := range principals {
					number++
					name := variant.name + "/" + route.name + "/" + principal.name
					pair := issueWriteDifferentialPair{old: idgen.New("sw244-a-"), oldIdentifier: fmt.Sprintf("SWA-%d", number), newIdentifier: fmt.Sprintf("SWB-%d", number)}
					pair.new = "sw244-b-" + strings.TrimPrefix(pair.old, "sw244-a-")
					pairs[name] = pair
					if variant.seed == nil {
						continue
					}
					for _, side := range [][2]string{{pair.old, pair.oldIdentifier}, {pair.new, pair.newIdentifier}} {
						id := side[0]
						issue := domain.Issue{ID: id, Identifier: side[1], Number: number, Title: "Initial title", Team: public, Creator: owner, State: data.States[0], Version: 1, CreatedAt: stamp, UpdatedAt: stamp,
							Attachments: []domain.Attachment{{ID: id + "-link", IssueID: id, URL: "https://example.test/", CreatedAt: stamp}}}
						variant.seed(data, &issue)
						target := domain.Issue{ID: id + "-target", Identifier: side[1] + "1", Number: number*10 + 2, Title: "Target", Team: issue.Team, Creator: owner, State: data.States[0], Version: 1, CreatedAt: stamp, UpdatedAt: stamp}
						data.Issues = append(data.Issues, issue, target)
						data.Comments[id] = []domain.Comment{{ID: id + "-comment", Body: "Initial comment", User: owner, Version: 1, CreatedAt: stamp, Reactions: map[string][]string{}}}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := f.host.Config.Handler
	if bounded {
		view, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		view.SetWebhookSink(func(string, domain.DomainEvent) {})
		repo = view
		handler = newHandler(&server{store: view, uploadPath: t.TempDir()})
	}

	observe := func(pair issueWriteDifferentialPair, id string, principal issueWriteDifferentialPrincipal, route issueWriteScopeBudgetRoute) string {
		tail := strings.NewReplacer("@comment", id+"-comment", "@link", id+"-link").Replace(route.tail)
		body := strings.ReplaceAll(route.body, "@target", id+"-target")
		r := httptest.NewRequest(route.method, "/api/issues/"+id+tail, strings.NewReader(body))
		r.Header.Set("X-Workspace-Key", workspace)
		r.Header.Set("X-Request-ID", "sw244-differential")
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		for _, cookie := range principal.cookies {
			r.AddCookie(cookie)
		}
		if principal.bearer != "" {
			r.Header.Set("Authorization", "Bearer "+principal.bearer)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		parts := []string{fmt.Sprintf("status=%d", w.Code), "body=" + w.Body.String()}
		row, rowErr := repo.IssueRecord(t.Context(), workspace, id)
		parts = append(parts, fmt.Sprintf("row=%s err=%v", issueWriteDifferentialJSON(t, row), rowErr != nil))
		comments, _, commentErr := repo.ResourceCommentsPage(t.Context(), workspace, id, "", 100)
		parts = append(parts, fmt.Sprintf("comments=%s err=%v", issueWriteDifferentialJSON(t, comments), commentErr != nil))
		events, eventErr := repo.Events(t.Context(), id)
		eventView := []any{}
		for _, event := range events {
			eventView = append(eventView, []any{event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues})
		}
		parts = append(parts, fmt.Sprintf("events=%s err=%v", issueWriteDifferentialJSON(t, eventView), eventErr != nil))
		for _, recipient := range append([]string{owner.ID}, users["member"].ID) {
			page, err := repo.QueryNotifications(t.Context(), store.NotificationQuery{Workspace: workspace, UserID: recipient, Limit: 200, IncludeArchived: true, SkipUnreadCount: true})
			if err != nil {
				t.Fatal(err)
			}
			mine := slices.DeleteFunc(page.Notifications, func(item domain.Notification) bool { return item.IssueID != id })
			parts = append(parts, "notifications="+issueWriteDifferentialJSON(t, mine))
		}
		return pair.normalize(strings.Join(parts, "\n"))
	}

	compared, allowed := 0, 0
	for _, variant := range variants {
		for _, route := range routes {
			for _, principal := range principals {
				name := variant.name + "/" + route.name + "/" + principal.name
				pair := pairs[name]
				completeIssueWriteReads.Store(true)
				before := observe(pair, pair.old, principal, route)
				completeIssueWriteReads.Store(false)
				after := observe(pair, pair.new, principal, route)
				if before != after {
					t.Errorf("%s: scoped reads changed the outcome\n%s", name, issueWriteDifferentialFirstDifference(before, after))
				}
				compared++
				if strings.HasPrefix(before, "status=2") {
					allowed++
				}
			}
		}
	}
	t.Logf("compared %d request pairs, %d allowed", compared, allowed)
	if allowed < compared/6 {
		t.Fatalf("only %d of %d pairs were allowed: the differential is not exercising writes", allowed, compared)
	}
}

func issueWriteDifferentialFirstDifference(before, after string) string {
	left, right := strings.Split(before, "\n"), strings.Split(after, "\n")
	for index := range max(len(left), len(right)) {
		var a, b string
		if index < len(left) {
			a = left[index]
		}
		if index < len(right) {
			b = right[index]
		}
		if a == b {
			continue
		}
		offset := 0
		for offset < min(len(a), len(b)) && a[offset] == b[offset] {
			offset++
		}
		start := max(0, offset-120)
		return fmt.Sprintf("complete: …%s\nscoped:   …%s", a[start:min(len(a), offset+240)], b[start:min(len(b), offset+240)])
	}
	return ""
}

func issueWriteDifferentialJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
