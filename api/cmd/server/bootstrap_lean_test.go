package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func getWithHeaders(t *testing.T, target, token string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status %d: %s", target, response.StatusCode, raw)
	}
	return response, raw
}

// seedLeanIssues imports count issues alternating between the first two teams.
// Every third issue has a description; the rest have none.
func seedLeanIssues(t *testing.T, f platformFixture, count, bodySize int) (teams []domain.Team, described map[string]bool) {
	t.Helper()
	second := authRequest[domain.Team](t, f.admin, http.MethodPost, f.host.URL+"/api/workspaces/"+f.data.Workspace.URLKey+"/teams", map[string]any{"name": "Lean second", "key": "LSN"}, "", http.StatusCreated)
	teams = []domain.Team{f.data.Teams[0], second}
	described = map[string]bool{}
	base := f.data.Issues[0]
	for start := 0; start < count; start += 500 {
		batch := []domain.Issue{}
		for i := start; i < min(start+500, count); i++ {
			issue := base
			issue.ID = fmt.Sprintf("lean-%05d", i)
			issue.Identifier = fmt.Sprintf("LEAN-%d", i)
			issue.Number = i + 1000
			issue.Title = fmt.Sprintf("Lean record %d", i)
			issue.Team = teams[i%2]
			issue.Description = ""
			if i%3 == 0 {
				issue.Description = fmt.Sprintf("needle%d ", i) + strings.Repeat("Long description paragraph with details. ", bodySize/40)
				described[issue.ID] = true
			}
			issue.SubscriberIDs = []string{"user-a", "user-b"}
			issue.Reactions = map[string][]string{"thumbsup": {"user-a", "user-b"}}
			issue.CreatedAt = time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC)
			issue.UpdatedAt = issue.CreatedAt
			batch = append(batch, issue)
		}
		if err := f.repository.ImportIssues(context.Background(), f.data.Workspace.URLKey, batch); err != nil {
			t.Fatal(err)
		}
	}
	return teams, described
}

func TestLeanBootstrapIsListRecordsWithIdReferences(t *testing.T) {
	f := newPlatformFixture(t, nil)
	teams, described := seedLeanIssues(t, f, 30, 400)
	token := f.apiKey(t, []string{"read"}, nil)
	_, raw := getWithHeaders(t, f.host.URL+"/api/bootstrap", token, map[string]string{"X-Flow-Projection": "lean"})
	if bytes.Contains(raw, []byte("Long description paragraph")) {
		t.Fatal("lean bootstrap carried a description body")
	}
	var lean struct {
		Issues      []map[string]json.RawMessage `json:"issues"`
		Comments    map[string]json.RawMessage   `json:"comments"`
		Activities  map[string]json.RawMessage   `json:"activities"`
		Teams       []domain.Team                `json:"teams"`
		States      []domain.WorkflowState       `json:"states"`
		Labels      []domain.IssueLabel          `json:"labels"`
		Users       []domain.User                `json:"users"`
		Projects    []domain.Project             `json:"projects"`
		Cycles      []domain.Cycle               `json:"cycles"`
		Notifs      []json.RawMessage            `json:"notifications"`
		IssueTotals json.RawMessage              `json:"issueCollectionPaged"`
	}
	if err := json.Unmarshal(raw, &lean); err != nil {
		t.Fatal(err)
	}
	if len(lean.Comments) != 0 || len(lean.Activities) != 0 {
		t.Fatal("lean bootstrap carried comments or activities")
	}
	banned := []string{"description", "descriptionState", "documentContent", "team", "state", "creator", "assignee", "delegate", "labels", "project", "attachments", "permissions", "reactions", "botActor", "isSummary"}
	teamIDs, stateIDs, userIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, item := range lean.Teams {
		teamIDs[item.ID] = true
	}
	for _, item := range lean.States {
		stateIDs[item.ID] = true
	}
	for _, item := range lean.Users {
		userIDs[item.ID] = true
	}
	_, defaultRaw := getWithHeaders(t, f.host.URL+"/api/bootstrap", token, nil)
	var full struct {
		Issues []domain.Issue `json:"issues"`
	}
	if err := json.Unmarshal(defaultRaw, &full); err != nil {
		t.Fatal(err)
	}
	if len(full.Issues) != len(lean.Issues) {
		t.Fatalf("lean has %d issues, default %d", len(lean.Issues), len(full.Issues))
	}
	fullByID := map[string]domain.Issue{}
	for _, issue := range full.Issues {
		fullByID[issue.ID] = issue
	}
	for _, row := range lean.Issues {
		for _, key := range banned {
			if _, ok := row[key]; ok {
				t.Fatalf("lean issue carries %q", key)
			}
		}
		var id string
		var teamID, stateID, creatorID string
		var hasContent bool
		var labelIDs []string
		var subscribers []string
		var reactions map[string]int
		for key, target := range map[string]any{"id": &id, "teamId": &teamID, "stateId": &stateID, "creatorId": &creatorID, "hasContent": &hasContent, "labelIds": &labelIDs, "subscriberIds": &subscribers, "reactionCounts": &reactions} {
			if row[key] == nil && key == "reactionCounts" {
				continue
			}
			if err := json.Unmarshal(row[key], target); err != nil {
				t.Fatalf("lean issue %s: %v", key, err)
			}
		}
		original := fullByID[id]
		if teamID != original.Team.ID || stateID != original.State.ID || creatorID != original.Creator.ID || len(labelIDs) != len(original.Labels) {
			t.Fatalf("lean references differ for %s", id)
		}
		if hasContent != (original.Description != "") || strings.HasPrefix(id, "lean-") && hasContent != described[id] {
			t.Fatalf("hasContent for %s = %v", id, hasContent)
		}
		if strings.HasPrefix(id, "lean-") && (len(subscribers) != 2 || reactions["thumbsup"] != 2) {
			t.Fatalf("subscribers/reactions lost for %s: %v %v", id, subscribers, reactions)
		}
		if !teamIDs[teamID] || !stateIDs[stateID] || !userIDs[creatorID] && creatorID != "" {
			t.Fatalf("reference of %s does not resolve in the same response: %s %s %s", id, teamID, stateID, creatorID)
		}
	}

	// A key limited to one team sees only that team's rows, in both shapes.
	limited := f.apiKey(t, []string{"read"}, []string{teams[0].ID})
	response, raw := getWithHeaders(t, f.host.URL+"/api/bootstrap", limited, map[string]string{"X-Flow-Projection": "lean"})
	if !strings.HasPrefix(response.Header.Get("Server-Timing"), "lean;dur=") {
		t.Fatalf("Server-Timing = %q", response.Header.Get("Server-Timing"))
	}
	var restricted struct {
		Issues []struct {
			ID     string `json:"id"`
			TeamID string `json:"teamId"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(raw, &restricted); err != nil {
		t.Fatal(err)
	}
	_, limitedFull := getWithHeaders(t, f.host.URL+"/api/bootstrap", limited, nil)
	var limitedFullIssues struct {
		Issues []domain.Issue `json:"issues"`
	}
	if err := json.Unmarshal(limitedFull, &limitedFullIssues); err != nil {
		t.Fatal(err)
	}
	if len(restricted.Issues) == 0 || len(restricted.Issues) != len(limitedFullIssues.Issues) {
		t.Fatalf("visibility differs: lean %d default %d", len(restricted.Issues), len(limitedFullIssues.Issues))
	}
	for _, issue := range restricted.Issues {
		if issue.TeamID != teams[0].ID || !slices.ContainsFunc(limitedFullIssues.Issues, func(item domain.Issue) bool { return item.ID == issue.ID }) {
			t.Fatalf("lean exposed %s of team %s", issue.ID, issue.TeamID)
		}
	}
}

func TestDefaultBootstrapIsUnchangedByLeanProjection(t *testing.T) {
	f := newPlatformFixture(t, nil)
	seedLeanIssues(t, f, 12, 400)
	token := f.apiKey(t, []string{"read"}, nil)
	_, first := getWithHeaders(t, f.host.URL+"/api/bootstrap", token, nil)
	_, other := getWithHeaders(t, f.host.URL+"/api/bootstrap", token, map[string]string{"X-Flow-Projection": "unknown"})
	var a, b struct {
		Issues []json.RawMessage `json:"issues"`
	}
	if json.Unmarshal(first, &a) != nil || json.Unmarshal(other, &b) != nil || len(a.Issues) != 12+len(f.data.Issues) {
		t.Fatalf("unexpected default issues: %d", len(a.Issues))
	}
	for i := range a.Issues {
		if !bytes.Equal(a.Issues[i], b.Issues[i]) {
			t.Fatalf("issue %d differs between plain and unknown projection", i)
		}
		for _, key := range []string{`"hasContent"`, `"reactionCounts"`, `"isSummary"`, `"teamId"`} {
			if bytes.Contains(a.Issues[i], []byte(key)) {
				t.Fatalf("default issue %d gained %s", i, key)
			}
		}
		for _, key := range []string{`"description"`, `"team"`, `"state"`, `"labels"`, `"subscriberIds"`, `"reactions"`} {
			if !bytes.Contains(a.Issues[i], []byte(key)) {
				t.Fatalf("default issue %d lost %s", i, key)
			}
		}
	}
}

func TestIssueSearchIDsMatchDescriptionsWithinVisibility(t *testing.T) {
	f := newPlatformFixture(t, nil)
	teams, described := seedLeanIssues(t, f, 30, 400)
	token := f.apiKey(t, []string{"read"}, nil)
	search := func(token, query string, extra ...string) (ids []string, truncated bool) {
		t.Helper()
		_, raw := getWithHeaders(t, f.host.URL+"/api/issue-records/search-ids?q="+url.QueryEscape(query)+strings.Join(extra, ""), token, nil)
		var result struct {
			IDs       []string `json:"ids"`
			Truncated bool     `json:"truncated"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result.IDs, result.Truncated
	}
	// needle6 sits in the description of lean-00006 (team 0) only.
	if ids, _ := search(token, "needle6 "); !slices.Equal(ids, []string{"lean-00006"}) {
		t.Fatalf("description match = %v", ids)
	}
	if ids, _ := search(token, "long description paragraph"); len(ids) != len(described) {
		t.Fatalf("matched %d of %d described issues", len(ids), len(described))
	}
	if ids, _ := search(token, "Lean record 7"); !slices.Contains(ids, "lean-00007") {
		t.Fatalf("title match = %v", ids)
	}
	if ids, _ := search(token, "no-such-text-anywhere"); len(ids) != 0 {
		t.Fatalf("unexpected matches %v", ids)
	}
	// lean-00003 is team 1 and described: a team-0 key must not see it.
	limited := f.apiKey(t, []string{"read"}, []string{teams[0].ID})
	if ids, _ := search(token, "needle3 "); !slices.Equal(ids, []string{"lean-00003"}) {
		t.Fatalf("unrestricted key missed the issue: %v", ids)
	}
	if ids, _ := search(limited, "needle3 "); len(ids) != 0 {
		t.Fatalf("restricted key matched a hidden team's description: %v", ids)
	}
	ids, _ := search(limited, "long description paragraph")
	for _, id := range ids {
		if !strings.HasPrefix(id, "lean-") || described[id] && teamOfLean(id) != 0 {
			t.Fatalf("restricted search leaked %s", id)
		}
	}
	if len(ids) == 0 {
		t.Fatal("restricted search found nothing")
	}
	if ids, truncated := search(token, "long description paragraph", "&limit=2"); len(ids) != 2 || !truncated {
		t.Fatalf("limit not applied: %v %v", ids, truncated)
	}
}

func teamOfLean(id string) int {
	var n int
	fmt.Sscanf(strings.TrimPrefix(id, "lean-"), "%d", &n)
	return n % 2
}

// Reports the lean/default size and build time on a 2,400-issue workspace.
func TestLeanBootstrapSizeAndBuildTime(t *testing.T) {
	f := newPlatformFixture(t, nil)
	seedLeanIssues(t, f, 2400, 2000)
	token := f.apiKey(t, []string{"read"}, nil)
	measure := func(headers map[string]string) (raw, gz int, best time.Duration) {
		for run := 0; run < 4; run++ {
			started := time.Now()
			_, body := getWithHeaders(t, f.host.URL+"/api/bootstrap", token, headers)
			if elapsed := time.Since(started); run == 0 || elapsed < best {
				best = elapsed
			}
			raw = len(body)
			var buffer bytes.Buffer
			writer := gzip.NewWriter(&buffer)
			writer.Write(body)
			writer.Close()
			gz = buffer.Len()
		}
		return
	}
	fullRaw, fullGz, fullTime := measure(nil)
	leanRaw, leanGz, leanTime := measure(map[string]string{"X-Flow-Projection": "lean"})
	t.Logf("2400 issues: default %d bytes (%d gzip) in %v; lean %d bytes (%d gzip) in %v", fullRaw, fullGz, fullTime, leanRaw, leanGz, leanTime)
	if leanRaw >= fullRaw {
		t.Fatalf("lean is not smaller: %d vs %d", leanRaw, fullRaw)
	}
}
