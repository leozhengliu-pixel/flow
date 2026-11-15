package main

import (
	"bytes"
	"encoding/base64"
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

func TestScaleP21PulseHistoryDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{37}, 32)))
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		workspace := f.data.Workspace.URLKey
		prefix := idgen.New("pulse-history-")
		full, mixed, initiative := prefix+"full", prefix+"mixed", prefix+"initiative"
		publicRef, privateRef, childRef := prefix+"public-ref", prefix+"private-ref", prefix+"child-ref"
		unscopedRef, archivedRef := prefix+"unscoped-ref", prefix+"archived-ref"
		visibleInitiative, hiddenInitiative, unlinkedInitiative := prefix+"visible-initiative", prefix+"hidden-initiative", prefix+"unlinked-initiative"
		at := time.Date(2026, 11, 14, 6, 0, 0, 0, time.UTC)
		body := strings.Repeat("Complete history body. ", 14000)
		var private, child string
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.pulse_history_fixture", "", nil, func(data *domain.Bootstrap) error {
			for _, project := range data.Projects {
				if project.ID == "m2-unrelated" {
					private = project.TeamIDs[0]
				}
			}
			for _, team := range data.Teams {
				if data.TeamSettings[team.ID].ParentTeamID == private {
					child = team.ID
				}
			}
			if private == "" || child == "" {
				t.Fatal("history fixture lacks private/child teams")
			}
			data.Projects = append(data.Projects,
				domain.Project{ID: full, Name: "Full source", TeamIDs: []string{f.public}, Description: strings.Repeat("unrelated source body ", 30000)},
				domain.Project{ID: mixed, Name: "Mixed source", TeamIDs: []string{f.public, private}},
				domain.Project{ID: publicRef, Name: "Visible reference", TeamIDs: []string{f.public}, PreviousSlugIDs: []string{publicRef + "-old"}},
				domain.Project{ID: privateRef, Name: "Hidden reference", TeamIDs: []string{private}},
				domain.Project{ID: childRef, Name: "Inherited reference", TeamIDs: []string{child}},
				domain.Project{ID: unscopedRef, Name: "Unscoped reference"},
				domain.Project{ID: archivedRef, Name: "Archived reference", TeamIDs: []string{f.public}, ArchivedAt: &at},
				domain.Project{ID: prefix + "empty-project", Name: "Empty project", TeamIDs: []string{f.public}},
			)
			data.Initiatives = append(data.Initiatives,
				domain.Initiative{ID: initiative, Name: "Initiative source", ProjectIDs: []string{publicRef, privateRef}},
				domain.Initiative{ID: visibleInitiative, Name: "Visible initiative", ProjectIDs: []string{publicRef}},
				domain.Initiative{ID: hiddenInitiative, Name: "Hidden initiative", ProjectIDs: []string{privateRef}},
				domain.Initiative{ID: unlinkedInitiative, Name: "Unlinked initiative"},
				domain.Initiative{ID: prefix + "empty-initiative", Name: "Empty initiative", ProjectIDs: []string{publicRef}},
			)
			actor := domain.User{ID: prefix + "actor", Name: "Application actor", App: true, OAuthClientID: "history-client",
				AppTeamIDs: []string{f.public, private, child, f.public}}
			diff := &domain.PulseDiff{
				Status: &domain.PulseValueChange{From: "Planned", To: "Active"},
				Projects: &domain.PulseProjectsChange{
					Added:   []domain.PulseRef{{ID: publicRef}, {ID: privateRef}, {ID: childRef}, {ID: unscopedRef}, {ID: archivedRef}, {ID: publicRef + "-old"}, {ID: "missing"}, {ID: publicRef}},
					Removed: []domain.PulseRef{{ID: privateRef}, {ID: publicRef}},
				},
				Initiatives: &domain.PulseProjectsChange{
					Added: []domain.PulseRef{{ID: visibleInitiative}, {ID: hiddenInitiative}, {ID: unlinkedInitiative}, {ID: "missing"}},
				},
				ProgressSince: &domain.PulseProgressChange{Date: at, From: 15, To: 75},
				Milestones:    []domain.PulseMilestoneChange{{ID: "milestone", Name: "Complete milestone", To: 100, Completed: true}},
			}
			for _, id := range []string{full, mixed} {
				data.ProjectUpdates[id] = []domain.ProjectUpdate{
					{ID: "history-first", ProjectID: id, Body: body, User: actor, CreatedAt: at, Diff: diff, Snapshot: &domain.PulseSnapshot{Status: "internal"},
						Comments: []domain.Comment{{ID: "complete-comment", Body: "Complete comment", User: actor}}, Reactions: map[string][]string{"like": {actor.ID}}},
					{ID: "history-second", ProjectID: id, Body: "Second update", User: actor, CreatedAt: at.Add(-time.Hour),
						Diff: &domain.PulseDiff{Projects: &domain.PulseProjectsChange{Added: nil, Removed: []domain.PulseRef{}}}},
					{ID: "history-third", ProjectID: id, Body: "Third update", User: actor, CreatedAt: at.Add(-2 * time.Hour)},
				}
			}
			data.InitiativeUpdates[initiative] = []domain.InitiativeUpdate{
				{ID: "history-first", InitiativeID: initiative, Body: body, User: actor, CreatedAt: at, Diff: diff, Snapshot: &domain.PulseSnapshot{Status: "internal"},
					Comments: []domain.Comment{{ID: "complete-comment", Body: "Complete comment", User: actor}}, Reactions: map[string][]string{"like": {actor.ID}}},
				{ID: "history-second", InitiativeID: initiative, Body: "Second update", User: actor, CreatedAt: at.Add(-time.Hour),
					Diff: &domain.PulseDiff{Initiatives: &domain.PulseProjectsChange{Added: []domain.PulseRef{}, Removed: nil}}},
				{ID: "history-third", InitiativeID: initiative, Body: "Third update", User: actor, CreatedAt: at.Add(-2 * time.Hour)},
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		type route struct {
			name, id string
			handler  func(*server, http.ResponseWriter, *http.Request)
		}
		routes := []route{
			{"project-full", full, (*server).listProjectUpdates},
			{"project-partial", mixed, (*server).listProjectUpdates},
			{"initiative", initiative, (*server).listInitiativeUpdates},
		}
		type outcome struct {
			Code   int
			Header http.Header
			Body   any
		}
		call := func(t *testing.T, source *store.SQLiteStore, user domain.User, key *domain.APIKey, target route, query url.Values, development bool) (outcome, string) {
			t.Helper()
			r := f.literalRequest(user, key, nil)
			r.Method, r.URL.Path, r.URL.RawQuery = http.MethodGet, "/api/projects/"+target.id+"/updates", query.Encode()
			if target.handler == nil {
				t.Fatal("missing history handler")
			}
			if strings.HasPrefix(target.name, "initiative") {
				r.URL.Path = "/api/initiatives/" + target.id + "/updates"
			}
			r.SetPathValue("id", target.id)
			w := httptest.NewRecorder()
			scaleP21CallWithOutputOwner(&server{store: source, authDisabled: development}, w, r, target.handler)
			var value any
			if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
				t.Fatal("history response is not JSON", w.Code, err)
			}
			next := ""
			if object, ok := value.(map[string]any); ok {
				next, _ = object["nextCursor"].(string)
				if next != "" {
					// Encryption nonces vary; both modes consume the literal
					// legacy token on subsequent requests below.
					object["nextCursor"] = "<cursor>"
				}
			}
			if source == f.probe && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
				t.Fatal("bounded history leaked work or decoded complete metadata")
			}
			return outcome{w.Code, w.Header().Clone(), value}, next
		}
		pair := func(t *testing.T, user domain.User, key *domain.APIKey, target route, query url.Values, development bool) (outcome, string) {
			t.Helper()
			legacy, next := call(t, repo, user, key, target, query, development)
			bounded, _ := call(t, f.probe, user, key, target, query, development)
			if !reflect.DeepEqual(legacy, bounded) {
				t.Fatalf("%s query=%s differential: %s", target.name, query.Encode(), scaleP21M2Diff(legacy, bounded))
			}
			return legacy, next
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, target := range routes {
				t.Run(target.name, func(t *testing.T) {
					plain, _ := pair(t, user, key, target, url.Values{}, false)
					if plain.Code != http.StatusOK {
						t.Fatal("history fixture failed", plain)
					}
					items := plain.Body.([]any)
					if len(items) != 3 {
						t.Fatal("history fixture lost update order or membership")
					}
					first := items[0].(map[string]any)
					if first["body"] != body || first["snapshot"] != nil || len(first["comments"].([]any)) != 1 {
						t.Fatal("history lost complete body, comments, order or snapshot stripping")
					}
					if key != nil && apiKeyTeamRestrictionSelected(*key) {
						change := first["diff"].(map[string]any)
						wantProgress := target.name == "project-full" && len(key.TeamIDs) != 0
						if (change["progressSince"] != nil) != wantProgress || (change["milestones"] != nil) != wantProgress {
							t.Fatal("partial history aggregate redaction changed", target.name, change)
						}
						teams, _ := first["user"].(map[string]any)["appTeamIds"].([]any)
						for _, team := range teams {
							if team != f.public {
								t.Fatal("actor retained a hidden team", teams)
							}
						}
					}
					query := url.Values{"window": {"1"}, "limit": {"1"}}
					for page := 0; ; page++ {
						got, next := pair(t, user, key, target, query, false)
						if got.Code != http.StatusOK || page > 2 {
							t.Fatal("history window did not terminate", got)
						}
						if next == "" {
							break
						}
						query.Set("cursor", next)
					}
				})
			}
		})
		selected := &domain.APIKey{ID: "history-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}
		for _, target := range routes {
			query := url.Values{"window": {"1"}, "update": {"history-first"}}
			first, next := pair(t, f.data.Viewer, selected, target, query, false)
			if first.Code != http.StatusOK || next == "" {
				t.Fatal("large history detail did not issue a continuation", first)
			}
			query.Set("cursor", next)
			if nextPage, _ := pair(t, f.data.Viewer, selected, target, query, false); nextPage.Code != http.StatusOK {
				t.Fatal("legacy detail cursor failed across modes", nextPage)
			}
			if got, _ := pair(t, f.data.Viewer, selected, target, url.Values{}, true); got.Code != http.StatusOK {
				t.Fatal("development history failed", got)
			}
		}
		for _, target := range []route{
			{"project-empty", prefix + "empty-project", (*server).listProjectUpdates},
			{"initiative-empty", prefix + "empty-initiative", (*server).listInitiativeUpdates},
			{"project-missing", prefix + "missing", (*server).listProjectUpdates},
			{"initiative-missing", prefix + "missing", (*server).listInitiativeUpdates},
		} {
			for _, query := range []url.Values{{}, {"window": {"1"}, "limit": {"1"}}} {
				pair(t, f.data.Viewer, selected, target, query, false)
			}
		}
	}
}
