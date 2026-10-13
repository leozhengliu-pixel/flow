package main

import (
	"net/http"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestSingleRecordReadsMatchListsAndVisibility(t *testing.T) {
	f := newPlatformFixture(t, nil)
	if len(f.data.Teams) < 2 {
		t.Skip("fixture needs two teams")
	}
	token := f.apiKey(t, []string{"read"}, nil)
	for _, resource := range []string{"teams", "users", "workflow-states", "issue-labels", "cycles", "projects", "initiatives", "customers"} {
		_, raw := bearer(t, http.MethodGet, f.host.URL+"/api/"+resource+"?first=250", token, nil, http.StatusOK)
		list := decodeInto[connection[map[string]any]](t, raw)
		if len(list.Nodes) == 0 {
			t.Logf("%s: fixture has none", resource)
			continue
		}
		for _, node := range list.Nodes[:min(3, len(list.Nodes))] {
			id, _ := node["id"].(string)
			_, raw := bearer(t, http.MethodGet, f.host.URL+"/api/"+resource+"/"+id, token, nil, http.StatusOK)
			record := decodeInto[map[string]any](t, raw)
			if record["id"] != id {
				t.Fatalf("%s/%s returned %s", resource, id, raw)
			}
		}
		_, raw = bearer(t, http.MethodGet, f.host.URL+"/api/"+resource+"/does-not-exist", token, nil, http.StatusNotFound)
		if envelope := decodeInto[map[string]any](t, raw); envelope["type"] != apiErrorNotFound {
			t.Fatalf("%s: %s", resource, raw)
		}
	}
	// A team by key, the caller as "me", a project by slug.
	team := f.data.Teams[0]
	_, raw := bearer(t, http.MethodGet, f.host.URL+"/api/teams/"+strings.ToLower(team.Key), token, nil, http.StatusOK)
	if got := decodeInto[domain.Team](t, raw); got.ID != team.ID {
		t.Fatalf("team by key = %s", raw)
	}
	_, raw = bearer(t, http.MethodGet, f.host.URL+"/api/users/me", token, nil, http.StatusOK)
	if got := decodeInto[domain.User](t, raw); got.ID != f.data.Viewer.ID {
		t.Fatalf("me = %s", raw)
	}
	if len(f.data.Projects) > 0 {
		_, raw = bearer(t, http.MethodGet, f.host.URL+"/api/projects/"+f.data.Projects[0].SlugID, token, nil, http.StatusOK)
		if got := decodeInto[domain.Project](t, raw); got.ID != f.data.Projects[0].ID {
			t.Fatalf("project by slug = %s", raw)
		}
	}

	// A team-restricted key sees only its team's records.
	restricted := f.apiKey(t, []string{"read"}, []string{team.ID})
	other := f.data.Teams[1]
	bearer(t, http.MethodGet, f.host.URL+"/api/teams/"+other.ID, restricted, nil, http.StatusNotFound)
	bearer(t, http.MethodGet, f.host.URL+"/api/teams/"+team.ID, restricted, nil, http.StatusOK)
	for _, cycle := range f.data.Cycles {
		want := http.StatusNotFound
		if cycle.TeamID == team.ID {
			want = http.StatusOK
		}
		bearer(t, http.MethodGet, f.host.URL+"/api/cycles/"+cycle.ID, restricted, nil, want)
	}
	// Reads need credentials.
	bearer(t, http.MethodGet, f.host.URL+"/api/teams/"+team.ID, "", nil, http.StatusUnauthorized)
}
