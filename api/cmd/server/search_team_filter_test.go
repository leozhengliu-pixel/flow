package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestSearchTeamPredicates(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	handler := newHandler(&server{store: repo, authDisabled: true})
	other := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/"+data.Workspace.URLKey+"/teams", map[string]any{"name": "Other", "key": "OTH"}, http.StatusCreated)
	data.Teams = append(data.Teams, other)
	for i, team := range data.Teams[:2] {
		issue := data.Issues[0]
		issue.ID, issue.Identifier, issue.Title, issue.Team = []string{"team-hit-a", "team-hit-b"}[i], []string{"TMA-1", "TMB-1"}[i], "TeamScopeNeedle", team
		if err := repo.ImportIssues(context.Background(), data.Workspace.URLKey, []domain.Issue{issue}); err != nil {
			t.Fatal(err)
		}
	}
	for _, endpoint := range []string{"/api/search", "/api/search/semantic", "/api/issue-records"} {
		for _, test := range []struct {
			op        string
			ids, want []string
		}{
			{"is", []string{data.Teams[0].ID}, []string{"team-hit-a"}},
			{"isNot", []string{data.Teams[0].ID}, []string{"team-hit-b"}},
			{"in", []string{data.Teams[0].ID, data.Teams[1].ID}, []string{"team-hit-a", "team-hit-b"}},
			{"notIn", []string{data.Teams[0].ID, data.Teams[1].ID}, []string{}},
		} {
			filter, _ := json.Marshal(store.IssueFilter{Field: "teamId", Operator: test.op, Values: test.ids})
			params := url.Values{"q": {"TeamScopeNeedle"}, "types": {"issue"}, "filter": {string(filter)}}
			got := []string{}
			if endpoint == "/api/issue-records" {
				response := requestJSON[store.IssueRecordPage](t, handler, http.MethodGet, endpoint+"?"+params.Encode(), nil, http.StatusOK)
				for _, item := range response.Items {
					got = append(got, item.ID)
				}
			} else {
				response := requestJSON[domain.SearchResponse](t, handler, http.MethodGet, endpoint+"?"+params.Encode(), nil, http.StatusOK)
				for _, item := range response.Results {
					got = append(got, item.ID)
				}
			}
			slices.Sort(got)
			if !slices.Equal(got, test.want) {
				t.Fatalf("%s %s: got %v want %v", endpoint, test.op, got, test.want)
			}
		}
		params := url.Values{"q": {"TeamScopeNeedle"}, "filter": {`{"field":"team","operator":"contains","values":["a"]}`}}
		requestJSON[map[string]any](t, handler, http.MethodGet, endpoint+"?"+params.Encode(), nil, http.StatusBadRequest)
	}
}

func TestMCPTeamFilterTree(t *testing.T) {
	f := newMCPContractFixture(t)
	other := mcpObject(t, f, "save_team", map[string]any{"name": "Other", "key": "OTH"})
	f.data.Teams = append(f.data.Teams, domain.Team{ID: other["id"].(string), Key: "OTH", Name: "Other"})
	a := mcpObject(t, f, "save_issue", map[string]any{"title": "TeamASTNeedle", "team": f.data.Teams[0].ID})
	b := mcpObject(t, f, "save_issue", map[string]any{"title": "TeamASTNeedle", "team": f.data.Teams[1].ID})
	for _, test := range []struct {
		op   string
		ids  []string
		want []string
	}{
		{"is", []string{f.data.Teams[0].ID}, []string{a["id"].(string)}},
		{"isNot", []string{f.data.Teams[0].ID}, []string{b["id"].(string)}},
		{"in", []string{f.data.Teams[0].ID, f.data.Teams[1].ID}, []string{a["id"].(string), b["id"].(string)}},
		{"notIn", []string{f.data.Teams[0].ID, f.data.Teams[1].ID}, []string{}},
	} {
		filter := map[string]any{"field": "team", "operator": test.op, "values": test.ids}
		for _, tool := range []string{"list_issues", "search_issues", "count_issues"} {
			args := map[string]any{"filter": filter, "query": "TeamASTNeedle"}
			if tool == "search_issues" {
				delete(args, "query")
				args["queries"] = []string{"TeamASTNeedle"}
			}
			result := mcpObject(t, f, tool, args)
			if tool == "count_issues" {
				if result["count"] != float64(len(test.want)) {
					t.Fatalf("count: %v", result)
				}
				continue
			}
			got := []string{}
			for _, item := range mcpItems(t, result) {
				got = append(got, item["id"].(string))
			}
			slices.Sort(got)
			slices.Sort(test.want)
			if !slices.Equal(got, test.want) {
				t.Fatalf("%s %s: %v want %v", tool, test.op, got, test.want)
			}
		}
	}
	for _, tool := range []string{"list_issues", "count_issues", "search_issues"} {
		args := map[string]any{"filter": map[string]any{"field": "team", "operator": "contains", "values": []string{"a"}}}
		if tool == "search_issues" {
			args["queries"] = []string{"needle"}
		}
		mcpToolError(t, f, tool, args)
	}
}
