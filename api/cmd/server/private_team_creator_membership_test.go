package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"flow/api/internal/domain"
)

// A private team's creator must be one of its members, so the issues they file in
// it stay readable to them (REST and MCP), with and without auth.
func TestPrivateTeamCreatorIsAMemberAndReadsItsIssuesOverMCP(t *testing.T) {
	for _, mode := range []struct {
		name         string
		authDisabled bool
	}{{"auth enabled", false}, {"auth disabled", true}} {
		t.Run(mode.name, func(t *testing.T) {
			f := newMCPContractFixture(t)
			f.service.authDisabled = mode.authDisabled
			post := func(path string, body any, want int) []byte {
				t.Helper()
				raw, _ := json.Marshal(body)
				request, err := http.NewRequest(http.MethodPost, f.host.URL+path, bytes.NewReader(raw))
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Authorization", "Bearer "+f.secret)
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				var buffer bytes.Buffer
				_, _ = buffer.ReadFrom(response.Body)
				if response.StatusCode != want {
					t.Fatalf("POST %s = %d, want %d: %s", path, response.StatusCode, want, buffer.String())
				}
				return buffer.Bytes()
			}
			var team domain.Team
			if err := json.Unmarshal(post("/api/workspaces/"+f.data.Workspace.URLKey+"/teams", map[string]any{"name": "Secret ops", "key": "SOP", "private": true}, http.StatusCreated), &team); err != nil {
				t.Fatal(err)
			}
			members, err := f.repository.ListTeamMembers(t.Context(), f.data.Workspace.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(members, func(member domain.TeamMember) bool {
				return member.TeamID == team.ID && member.UserID == f.data.Viewer.ID
			}) {
				t.Fatalf("the creator has no team_memberships row in the private team: %+v", members)
			}
			var issue domain.Issue
			if err := json.Unmarshal(post("/api/issue-records", map[string]any{"title": "Private work", "teamId": team.ID}, http.StatusCreated), &issue); err != nil {
				t.Fatal(err)
			}
			got := mcpObject(t, f, "get_issue", map[string]any{"id": issue.ID})
			if got["title"] != "Private work" {
				t.Fatalf("MCP get_issue = %v", got)
			}
		})
	}
}
