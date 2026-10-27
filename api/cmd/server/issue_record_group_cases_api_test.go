package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// groupValues (one status group of several workflows) and groupBy=cases with
// groupCases (Focus buckets) on the paged issue endpoints.
func TestIssueRecordsGroupValuesAndCases(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	host := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer host.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", 200)
	data := repository.Bootstrap()
	team := data.Teams[0]
	var states []domain.WorkflowState
	for _, state := range data.States {
		if state.TeamID == team.ID || state.TeamID == "" {
			states = append(states, state)
		}
	}
	if len(states) < 2 {
		t.Fatalf("fixture states %v", states)
	}
	first := authRequest[domain.Issue](t, admin, "POST", host.URL+"/api/issue-records", map[string]any{"title": "Grouped first", "teamId": team.ID, "stateId": states[0].ID, "priority": 1}, "test-workspace", 201)
	second := authRequest[domain.Issue](t, admin, "POST", host.URL+"/api/issue-records", map[string]any{"title": "Grouped second", "teamId": team.ID, "stateId": states[1].ID}, "test-workspace", 201)
	scope := "&filter=" + url.QueryEscape(`{"field":"id","values":["`+first.ID+`","`+second.ID+`"]}`)
	ids := func(items []domain.Issue) []string {
		result := []string{}
		for _, item := range items {
			result = append(result, item.ID)
		}
		slices.Sort(result)
		return result
	}
	sorted := func(values ...string) []string { slices.Sort(values); return values }
	page := authRequest[store.IssueRecordPage](t, admin, "GET", host.URL+"/api/issue-records?projection=list&groupBy=status&groupValues="+states[0].ID+","+states[1].ID+scope, nil, "test-workspace", 200)
	if got := ids(page.Items); !slices.Equal(got, sorted(first.ID, second.ID)) {
		t.Fatalf("status group of two values = %v", got)
	}
	authRequest[map[string]any](t, admin, "GET", host.URL+"/api/issue-records?projection=list&groupBy=label&groupValues=x"+scope, nil, "test-workspace", 400)

	cases := url.QueryEscape(`[{"value":"focus-urgent","filter":{"field":"priority","values":["1"]}},{"value":"focus-active","filter":{"and":[]}}]`)
	type groups struct {
		Groups []store.IssueRecordGroup `json:"groups"`
	}
	got := authRequest[groups](t, admin, "GET", host.URL+"/api/issue-records/groups?groupBy=cases&groupCases="+cases+scope, nil, "test-workspace", 200).Groups
	if !slices.Equal(got, []store.IssueRecordGroup{{Value: "focus-urgent", Count: 1}, {Value: "focus-active", Count: 1}}) {
		t.Fatalf("case groups = %v", got)
	}
	page = authRequest[store.IssueRecordPage](t, admin, "GET", host.URL+"/api/issue-records?projection=list&groupBy=cases&groupValue=focus-active&groupCases="+cases+scope, nil, "test-workspace", 200)
	if got := ids(page.Items); !slices.Equal(got, []string{second.ID}) {
		t.Fatalf("active case page = %v", got)
	}
	for _, bad := range []string{"", url.QueryEscape(`[]`), url.QueryEscape(`{"value":"x"}`), url.QueryEscape(`[{"value":"x","filter":{"field":"nope","values":["1"]}}]`)} {
		authRequest[map[string]any](t, admin, "GET", host.URL+"/api/issue-records/groups?groupBy=cases&groupCases="+bad+scope, nil, "test-workspace", 400)
	}
}
