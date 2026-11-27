package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"flow/api/internal/domain"
)

func TestIssueRecordGroupEstimatePointsHTTP(t *testing.T) {
	f := newMCPContractFixture(t)
	issues := slices.Clone(f.data.Issues[:3])
	team := issues[0].Team.ID
	if err := f.repository.MutateWorkspace(workspaceFixtureContext(t.Context()), f.data.Workspace.URLKey, "test.estimates", "", nil, func(d *domain.Bootstrap) error {
		settings := d.TeamSettings[team]
		settings.EstimateType, settings.EstimateAllowZero, settings.EstimateCountUnestimated = "fibonacci", false, nil
		settings.InheritIssueEstimation = false
		d.TeamSettings[team] = settings
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	three, five := 3.0, 5.0
	state := issues[0].State
	for i := range issues {
		issues[i].Team, issues[i].State, issues[i].Estimate = issues[0].Team, state, nil
		issues[i].Version++
	}
	issues[0].Estimate, issues[1].Estimate = &three, &five
	if err := f.repository.ImportIssues(t.Context(), f.data.Workspace.URLKey, issues); err != nil {
		t.Fatal(err)
	}
	type group struct {
		Value          string   `json:"value"`
		Count          int64    `json:"count"`
		EstimatePoints *float64 `json:"estimatePoints"`
	}
	get := func(params url.Values) (int, []group) {
		t.Helper()
		params.Set("filter", `{"field":"id","operator":"in","values":["`+issues[0].ID+`","`+issues[1].ID+`","`+issues[2].ID+`"]}`)
		req, _ := http.NewRequest("GET", f.host.URL+"/api/issue-records/groups?"+params.Encode(), nil)
		req.Header.Set("Authorization", "Bearer "+f.secret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result struct {
			Groups []group `json:"groups"`
		}
		if resp.StatusCode == http.StatusOK {
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatal(err)
			}
		}
		return resp.StatusCode, result.Groups
	}
	status, plain := get(url.Values{"groupBy": {"status"}})
	if status != http.StatusOK || len(plain) != 1 || plain[0].Value != state.ID || plain[0].Count != 3 || plain[0].EstimatePoints != nil {
		t.Fatalf("plain groups: HTTP %d %+v", status, plain)
	}
	// 3 + 5 + 1 for the unestimated issue.
	status, totals := get(url.Values{"groupBy": {"status"}, "aggregate": {"estimatePoints"}})
	if status != http.StatusOK || len(totals) != 1 || totals[0].Count != 3 || totals[0].EstimatePoints == nil || *totals[0].EstimatePoints != 9 {
		t.Fatalf("estimate totals: HTTP %d %+v", status, totals)
	}
	if status, _ := get(url.Values{"groupBy": {"status"}, "aggregate": {"sum"}}); status != http.StatusBadRequest {
		t.Fatalf("unsupported aggregate: HTTP %d", status)
	}
}
