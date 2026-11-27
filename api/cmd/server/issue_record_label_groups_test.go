package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestIssueRecordLabelGroupsHTTP(t *testing.T) {
	f := newMCPContractFixture(t)
	issues := slices.Clone(f.data.Issues[:3])
	team := issues[0].Team.ID
	if err := f.repository.MutateWorkspace(workspaceFixtureContext(t.Context()), f.data.Workspace.URLKey, "test.labels", "", nil, func(d *domain.Bootstrap) error {
		d.LabelGroups = append(d.LabelGroups, domain.LabelGroup{ID: "lg-http", Name: "Http group", ResourceType: "issue"})
		d.Labels = append(d.Labels,
			domain.IssueLabel{ID: "lg-http-b", Name: "beta", GroupID: "lg-http"}, domain.IssueLabel{ID: "lg-http-a", Name: "Alpha", GroupID: "lg-http"},
			domain.IssueLabel{ID: "lg-http-team", Name: "Zed", Scope: team}, domain.IssueLabel{ID: "lg-http-project", Name: "Project", GroupID: "lg-http", ResourceType: "project"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	issues[0].Labels = []domain.IssueLabel{{ID: "lg-http-a"}, {ID: "lg-http-b"}}
	issues[1].Labels = []domain.IssueLabel{{ID: "lg-http-a"}, {ID: "lg-http-team"}}
	issues[2].Labels = nil
	for i := range issues {
		issues[i].Version++
	}
	if err := f.repository.ImportIssues(t.Context(), f.data.Workspace.URLKey, issues); err != nil {
		t.Fatal(err)
	}
	get := func(path string, target any) int {
		t.Helper()
		req, _ := http.NewRequest("GET", f.host.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+f.secret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
				t.Fatal(err)
			}
		}
		return resp.StatusCode
	}
	filter := `{"field":"id","operator":"in","values":["` + issues[0].ID + `","` + issues[1].ID + `","` + issues[2].ID + `"]}`
	groups := func(params url.Values) []store.IssueRecordGroup {
		t.Helper()
		params.Set("filter", filter)
		var result struct {
			Groups []store.IssueRecordGroup `json:"groups"`
		}
		if status := get("/api/issue-records/groups?"+params.Encode(), &result); status != http.StatusOK {
			t.Fatalf("groups %v: HTTP %d", params, status)
		}
		return result.Groups
	}
	rows := func(params url.Values, value string) []string {
		t.Helper()
		params.Set("filter", filter)
		params.Set("groupValue", value)
		params.Set("projection", "list")
		var page store.IssueRecordPage
		if status := get("/api/issue-records?"+params.Encode(), &page); status != http.StatusOK {
			t.Fatalf("rows %v: HTTP %d", params, status)
		}
		ids := []string{}
		for _, item := range page.Items {
			ids = append(ids, item.ID)
		}
		return ids
	}

	// Label: every label of an issue, team labels first, grouped labels by group then name, "No label" last.
	if got, want := groups(url.Values{"groupBy": {"labels"}}), []store.IssueRecordGroup{{Value: "lg-http-team", Count: 1}, {Value: "lg-http-a", Count: 2}, {Value: "lg-http-b", Count: 1}, {Value: "", Count: 1}}; !slices.Equal(got, want) {
		t.Fatalf("label groups %+v want %+v", got, want)
	}
	// Label group: its issue labels by name; an issue with none of them is in "No label".
	group := url.Values{"groupBy": {"labelGroup"}, "labelGroupId": {"lg-http"}}
	if got, want := groups(group), []store.IssueRecordGroup{{Value: "lg-http-a", Count: 2}, {Value: "lg-http-b", Count: 1}, {Value: "", Count: 1}}; !slices.Equal(got, want) {
		t.Fatalf("label group groups %+v want %+v", got, want)
	}
	if got := rows(group, ""); !slices.Equal(got, []string{issues[2].ID}) {
		t.Fatalf("label group none rows %v", got)
	}
	if got := rows(group, "lg-http-b"); !slices.Equal(got, []string{issues[0].ID}) {
		t.Fatalf("label group rows %v", got)
	}
	// An unknown group puts every issue in "No label".
	if got := groups(url.Values{"groupBy": {"labelGroup"}, "labelGroupId": {"missing"}}); len(got) != 1 || got[0].Value != "" || got[0].Count != 3 {
		t.Fatalf("unknown label group %+v", got)
	}
	// Status type: started first.
	types := groups(url.Values{"groupBy": {"statusType"}})
	for i := 1; i < len(types); i++ {
		if slices.Index(statusTypeGroupOrder, types[i-1].Value) > slices.Index(statusTypeGroupOrder, types[i].Value) {
			t.Fatalf("status type order %+v", types)
		}
	}
}

func TestIssueRecordLabelGroupsOrder(t *testing.T) {
	labels := map[string]store.IssueGroupLabel{
		"plain": {Name: "plain"}, "bug": {Name: "Bug", Scope: "workspace"},
		"stage-b": {Name: "Beta", GroupID: "g-stage", GroupName: "Stage"}, "stage-a": {Name: "alpha", GroupID: "g-stage", GroupName: "Stage"},
		"area-z": {Name: "Zed", GroupID: "g-area", GroupName: "Area"}, "team": {Name: "Team label", Scope: "team-1"},
	}
	values := func(groups []store.IssueRecordGroup) []string {
		result := []string{}
		for _, group := range groups {
			result = append(result, group.Value)
		}
		return result
	}
	groups := func(values ...string) []store.IssueRecordGroup {
		result := []store.IssueRecordGroup{}
		for _, value := range values {
			result = append(result, store.IssueRecordGroup{Value: value, Count: 1})
		}
		return result
	}

	// Label: team labels, ungrouped labels by name, grouped labels by group then name, "No label" last.
	all := groups("", "stage-b", "plain", "area-z", "team", "bug", "stage-a")
	sortIssueRecordGroups(labels, store.IssueRecordQuery{GroupBy: "labels"}, all)
	if got, want := values(all), []string{"team", "bug", "plain", "area-z", "stage-a", "stage-b", ""}; !slices.Equal(got, want) {
		t.Fatalf("label order %v want %v", got, want)
	}
	// Label group: its labels by name.
	members := groups("", "stage-b", "stage-a")
	sortIssueRecordGroups(labels, store.IssueRecordQuery{GroupBy: "labels", GroupLabelIDs: []string{"stage-a", "stage-b"}}, members)
	if got, want := values(members), []string{"stage-a", "stage-b", ""}; !slices.Equal(got, want) {
		t.Fatalf("label group order %v want %v", got, want)
	}
	// Status type: started, unstarted, backlog, triage, completed, canceled.
	types := groups("canceled", "backlog", "completed", "started", "triage", "unstarted")
	sortIssueRecordGroups(nil, store.IssueRecordQuery{GroupBy: "statusType"}, types)
	if got := values(types); !slices.Equal(got, statusTypeGroupOrder) {
		t.Fatalf("status type order %v", got)
	}
}
