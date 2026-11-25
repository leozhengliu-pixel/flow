package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21HotPulseGroupHandlerDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, project, initiative := f.data.Workspace.URLKey, idgen.New("pulse-group-project-"), idgen.New("pulse-group-initiative-")
		at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.pulse_group_seed", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.FeatureFlags["pulse"] = true
			data.Projects = append(data.Projects, domain.Project{ID: project, Name: "Pulse source", SlugID: project + "-slug", TeamIDs: []string{f.public}, MemberIDs: []string{f.principals[2].user.ID}, Lead: &data.Viewer, Description: strings.Repeat("unrelated body ", 300000)})
			data.Initiatives = append(data.Initiatives, domain.Initiative{ID: initiative, Name: "Pulse initiative", ProjectIDs: []string{project}, Owner: &data.Viewer})
			data.ProjectUpdates[project] = []domain.ProjectUpdate{
				{ID: project + "-first", ProjectID: project, Body: "Matching @Mention", Health: "atRisk", User: data.Viewer, CreatedAt: at, Comments: []domain.Comment{{ID: "comment", Body: "Complete comment", User: data.Viewer, Reactions: map[string][]string{"like": {data.Viewer.ID}}}}, Reactions: map[string][]string{"like": {data.Viewer.ID}}, Snapshot: &domain.PulseSnapshot{Status: "stored"}},
				{ID: project + "-second", ProjectID: project, Body: "Other update", User: f.principals[2].user, CreatedAt: at.Add(-time.Minute)},
			}
			data.InitiativeUpdates[initiative] = []domain.InitiativeUpdate{{ID: initiative + "-update", InitiativeID: initiative, Body: "Initiative update", User: f.principals[1].user, CreatedAt: at.Add(-2 * time.Minute)}}
			for _, principal := range f.principals {
				settings := data.UserSettings[principal.user.ID]
				settings.FeedLastSeenTime = at.Add(-time.Hour).Format(time.RFC3339Nano)
				data.UserSettings[principal.user.ID] = settings
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, query := range []url.Values{
				{"view": {"all"}, "limit": {"1"}},
				{"view": {"popular"}}, {"view": {"following"}}, {"view": {"created"}},
				{"q": {"matching"}}, {"filter": {`[{"field":"health","operator":"is","values":["atRisk"]}]`}},
				{"q": {"@Mention\n" + f.data.Viewer.DisplayName + "\n" + f.data.Viewer.Name}},
			} {
				var outcomes []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					r := f.literalRequest(user, key, nil)
					r.Method, r.URL.Path, r.URL.RawQuery = http.MethodGet, "/api/pulse/feed", query.Encode()
					w := httptest.NewRecorder()
					(&server{store: source}).getPulseFeed(w, r)
					outcomes = append(outcomes, []any{w.Code, scaleP21M2Response(t, w, false)})
					if source.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("feed response leaked work admission")
					}
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("query=%s differential: %s", query.Encode(), scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			}
			var unread []any
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				r := f.literalRequest(user, key, nil)
				w := httptest.NewRecorder()
				(&server{store: source}).getPulseUnread(w, r)
				unread = append(unread, []any{w.Code, scaleP21M2Response(t, w, false)})
			}
			if !reflect.DeepEqual(unread[0], unread[1]) {
				t.Fatal("unread differential", scaleP21M2Diff(unread[0], unread[1]))
			}
		})
		// The legacy history response keeps full comments/reactions and strips
		// only stored snapshots. This exercises the real handler's lifetime.
		for _, kind := range []string{"project", "initiative"} {
			var bodies []any
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				r := f.literalRequest(f.data.Viewer, nil, nil)
				r.SetPathValue("id", project)
				w := httptest.NewRecorder()
				service := &server{store: source}
				if kind == "project" {
					service.listProjectUpdates(w, r)
				} else {
					r.SetPathValue("id", initiative)
					service.listInitiativeUpdates(w, r)
				}
				var body any
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK {
					t.Fatal("history response", w.Code, w.Body.String(), err)
				}
				bodies = append(bodies, body)
				if source.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("history response leaked work admission")
				}
			}
			if !reflect.DeepEqual(bodies[0], bodies[1]) {
				t.Fatal("complete history changed", scaleP21M2Diff(bodies[0], bodies[1]))
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("bounded Pulse handler decoded full metadata")
		}
	}
}
