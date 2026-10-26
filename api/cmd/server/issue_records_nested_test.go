package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// nested=true on the paged issue endpoints: a sub-issue in another status is
// counted and returned with its parent, and a sub-issue in a private team the
// viewer cannot read is neither counted nor returned.
func TestIssueRecordsNestedSubIssues(t *testing.T) {
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
	member, memberUser := verifiedAuthClient(t, host.URL, "Nested reader", "nested-reader@example.test")
	invite := authRequest[[]domain.Invitation](t, admin, "POST", host.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{memberUser.Email}, "role": "member"}, "", 201)
	authRequest[domain.WorkspaceMembership](t, member, "POST", host.URL+"/api/invitations/accept", map[string]string{"token": invite[0].Token}, "", 200)
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
	private := authRequest[domain.Team](t, admin, "POST", host.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Private nested team", "key": "PNT", "private": true}, "", 201)
	parent := authRequest[domain.Issue](t, admin, "POST", host.URL+"/api/issue-records", map[string]any{"title": "Nested parent", "teamId": team.ID, "stateId": states[0].ID}, "test-workspace", 201)
	child := authRequest[domain.Issue](t, admin, "POST", host.URL+"/api/issue-records", map[string]any{"title": "Nested child", "teamId": team.ID, "stateId": states[1].ID, "parentId": parent.ID}, "test-workspace", 201)
	hidden := authRequest[domain.Issue](t, admin, "POST", host.URL+"/api/issue-records", map[string]any{"title": "Private nested child", "teamId": private.ID, "parentId": parent.ID}, "test-workspace", 201)
	scope := "&filter=" + `%7B%22field%22%3A%22id%22%2C%22values%22%3A%5B%22` + parent.ID + `%22%2C%22` + child.ID + `%22%2C%22` + hidden.ID + `%22%5D%7D`
	type groups struct {
		Groups []store.IssueRecordGroup `json:"groups"`
	}
	countsFor := func(client *http.Client, nested string) map[string]int64 {
		t.Helper()
		result := map[string]int64{}
		for _, group := range authRequest[groups](t, client, "GET", host.URL+"/api/issue-records/groups?groupBy=status"+nested+scope, nil, "test-workspace", 200).Groups {
			result[group.Value] = group.Count
		}
		return result
	}
	ids := func(items []domain.Issue) []string {
		result := []string{}
		for _, item := range items {
			result = append(result, item.ID)
		}
		slices.Sort(result)
		return result
	}
	sorted := func(values ...string) []string { slices.Sort(values); return values }
	// Old clients: every issue in its own status.
	if got := countsFor(member, ""); got[states[0].ID] != 1 || got[states[1].ID] != 1 || len(got) != 2 {
		t.Fatalf("flat member counts %v", got)
	}
	if got := countsFor(member, "&nested=true"); got[parent.State.ID] != 2 || len(got) != 1 {
		t.Fatalf("nested member counts %v", got)
	}
	page := authRequest[store.IssueRecordPage](t, member, "GET", host.URL+"/api/issue-records?projection=list&nested=true&groupBy=status&groupValue="+parent.State.ID+scope, nil, "test-workspace", 200)
	if !slices.Equal(ids(page.Items), []string{parent.ID}) || !slices.Equal(ids(page.Descendants), []string{child.ID}) {
		t.Fatalf("nested member page %v %v", ids(page.Items), ids(page.Descendants))
	}
	if other := authRequest[store.IssueRecordPage](t, member, "GET", host.URL+"/api/issue-records?projection=list&nested=true&groupBy=status&groupValue="+states[1].ID+scope, nil, "test-workspace", 200); len(other.Items) != 0 {
		t.Fatalf("nested child also listed in its own status: %v", ids(other.Items))
	}
	if got := countsFor(admin, "&nested=true"); got[parent.State.ID] != 3 {
		t.Fatalf("nested admin counts %v", got)
	}
	page = authRequest[store.IssueRecordPage](t, admin, "GET", host.URL+"/api/issue-records?projection=list&nested=true&groupBy=status&groupValue="+parent.State.ID+scope, nil, "test-workspace", 200)
	if !slices.Equal(ids(page.Descendants), sorted(child.ID, hidden.ID)) {
		t.Fatalf("nested admin descendants %v", ids(page.Descendants))
	}
}
