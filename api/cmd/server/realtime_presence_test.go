package main

import (
	"context"
	"encoding/json"
	"errors"
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

func scaleP21SW62PresenceJSON(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	var normalize func(any)
	normalize = func(value any) {
		switch item := value.(type) {
		case map[string]any:
			for key, value := range item {
				if key == "lastSeenAt" {
					item[key] = "<presence-clock>"
				} else {
					normalize(value)
				}
			}
		case []any:
			for _, value := range item {
				normalize(value)
			}
		}
	}
	normalize(decoded)
	return decoded
}

func TestScaleP21SW62PresenceDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_PRESENCE_MINIMAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw62-presence-")
		public, private, inherited := prefix+"public", prefix+"private", prefix+"inherited"
		userGrant, workspaceGrant, missingTeam := prefix+"user-grant", prefix+"workspace-grant", prefix+"missing-team"
		currentSlug, oldSlug := prefix+"current", prefix+"previous"
		issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
			map[string]any{"title": "Presence issue", "teamId": f.public}, workspace, http.StatusCreated)
		privateTeam, childTeam := "", ""
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw62_presence_fixture", "", nil, func(data *domain.Bootstrap) error {
			for _, project := range data.Projects {
				if project.ID == "m2-unrelated" {
					privateTeam = project.TeamIDs[0]
				}
			}
			for id, settings := range data.TeamSettings {
				if settings.ParentTeamID == privateTeam {
					childTeam = id
				}
			}
			if privateTeam == "" || childTeam == "" {
				t.Fatal("private/inherited presence teams missing")
			}
			data.Projects = append(data.Projects,
				domain.Project{ID: prefix + "private-project", TeamIDs: []string{privateTeam}},
				domain.Project{ID: prefix + "child-project", TeamIDs: []string{childTeam}},
				domain.Project{ID: prefix + "missing-project", TeamIDs: []string{prefix + "missing-team-id"}},
			)
			wide := strings.Repeat("Unused presence document body ", 10000)
			data.Documents = append(data.Documents,
				domain.Document{ID: public, SlugID: currentSlug, PreviousSlugIDs: []string{oldSlug}, Content: wide},
				domain.Document{ID: private, SlugID: private + "-slug", TeamIDs: []string{privateTeam}, Content: wide},
				domain.Document{ID: inherited, SlugID: inherited + "-slug", ProjectIDs: []string{prefix + "child-project"}, InheritedTeamIDs: []string{childTeam}, Content: wide},
				domain.Document{ID: userGrant, SlugID: userGrant + "-slug", ProjectIDs: []string{prefix + "private-project"}, InheritedTeamIDs: []string{privateTeam},
					Permissions: []domain.DocumentPermission{{SubjectType: "user", SubjectID: f.principals[3].user.ID, Role: "viewer"}}, Content: wide},
				domain.Document{ID: workspaceGrant, SlugID: workspaceGrant + "-slug", ProjectIDs: []string{prefix + "private-project"}, InheritedTeamIDs: []string{privateTeam},
					Permissions: []domain.DocumentPermission{{SubjectType: "workspace", SubjectID: data.Workspace.ID, Role: "viewer"}}, Content: wide},
				domain.Document{ID: missingTeam, SlugID: missingTeam + "-slug", ProjectIDs: []string{prefix + "missing-project"},
					InheritedTeamIDs: []string{prefix + "missing-team-id"}, Content: wide},
			)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		page, err := repo.QueryIssueRecords(t.Context(), store.IssueRecordQuery{Workspace: workspace, Archived: "all", Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		privateIssue := ""
		for _, item := range page.Items {
			if item.Team.ID == privateTeam {
				privateIssue = item.ID
				break
			}
		}
		if privateIssue == "" {
			t.Fatal("private presence issue missing")
		}
		roster := []domain.Presence{
			{ClientID: "idle", User: f.data.Viewer, Route: "/idle"},
			{ClientID: "public", User: f.data.Viewer, DocumentID: public, Route: "/documents"},
			{ClientID: "current-slug", User: f.data.Viewer, DocumentID: currentSlug, Route: "/documents"},
			{ClientID: "old-slug", User: f.data.Viewer, DocumentID: oldSlug, Route: "/documents"},
			{ClientID: "private", User: f.data.Viewer, DocumentID: private, Route: "/documents"},
			{ClientID: "inherited", User: f.data.Viewer, DocumentID: inherited, Route: "/documents"},
			{ClientID: "user-grant", User: f.data.Viewer, DocumentID: userGrant, Route: "/documents"},
			{ClientID: "workspace-grant", User: f.data.Viewer, DocumentID: workspaceGrant, Route: "/documents"},
			{ClientID: "missing-team", User: f.data.Viewer, DocumentID: missingTeam, Route: "/documents"},
			{ClientID: "missing-document", User: f.data.Viewer, DocumentID: "missing", Route: "/documents"},
			{ClientID: "public-issue", User: f.data.Viewer, IssueID: issue.ID, Route: "/issues"},
			{ClientID: "private-issue", User: f.data.Viewer, IssueID: privateIssue, Route: "/issues"},
		}
		routes := []struct {
			name, method string
			query        url.Values
			input        map[string]any
			minimal      bool
		}{
			{"list-roster", http.MethodGet, nil, nil, false},
			{"list-private-document", http.MethodGet, url.Values{"documentId": {private}}, nil, false},
			{"list-explicit-user-document", http.MethodGet, url.Values{"documentId": {userGrant}}, nil, false},
			{"list-previous-slug", http.MethodGet, url.Values{"documentId": {oldSlug}}, nil, false},
			{"list-private-issue", http.MethodGet, url.Values{"issueId": {privateIssue}}, nil, false},
			{"list-route", http.MethodGet, url.Values{"route": {"/documents"}}, nil, false},
			{"list-malformed-document-filter", http.MethodGet, url.Values{"documentId": {public}, "filter": {"{"}}, nil, false},
			{"update-current-slug", http.MethodPost, nil, map[string]any{"clientId": "new-client", "documentId": currentSlug, "route": "/documents"}, false},
			{"update-private-document", http.MethodPost, nil, map[string]any{"clientId": "new-client", "documentId": private, "route": "/documents"}, false},
			{"update-minimal", http.MethodPost, nil, map[string]any{"clientId": "new-client", "documentId": public, "route": "/documents"}, true},
			{"remove-full", http.MethodPost, nil, map[string]any{"clientId": "public", "active": false, "documentId": "missing"}, false},
			{"remove-minimal", http.MethodPost, nil, map[string]any{"clientId": "public", "active": false, "documentId": "missing"}, true},
			{"invalid-client", http.MethodPost, nil, map[string]any{"clientId": "", "documentId": public}, false},
		}
		for _, reload := range []bool{false, true} {
			if reload {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
				for _, route := range routes {
					t.Run(route.name, func(t *testing.T) {
						var outputs []any
						for _, source := range []*store.SQLiteStore{repo, f.probe} {
							service := &server{store: source, realtime: newRealtimeHub()}
							seedAt := time.Now().UTC()
							service.realtime.presence[workspace] = map[string]domain.Presence{}
							for index, item := range roster {
								item.LastSeenAt = seedAt.Add(time.Duration(index-len(roster)) * time.Millisecond)
								service.realtime.presence[workspace][item.ClientID] = item
							}
							r := f.literalRequest(user, key, route.input)
							r.Method, r.URL.Path, r.URL.RawQuery = route.method, "/api/realtime/presence", route.query.Encode()
							if route.minimal {
								r.Header.Set("X-Flow-Presence", "minimal")
							}
							w := httptest.NewRecorder()
							handler := (*server).listPresence
							if route.method == http.MethodPost {
								handler = (*server).updatePresence
							}
							scaleP21CallWithOutputOwner(service, w, r, handler)
							var body any
							if w.Body.Len() != 0 {
								if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
									t.Fatal("presence response is not JSON", w.Code, err)
								}
							}
							outputs = append(outputs, scaleP21SW62PresenceJSON(t, []any{w.Code, w.Header(), body, service.realtime.snapshotPresence(workspace)}))
							if source.MetadataWorkUsedForTesting() != 0 {
								t.Fatal("presence operation leaked owned document headers")
							}
						}
						if !reflect.DeepEqual(outputs[0], outputs[1]) {
							t.Fatalf("reload=%t presence differential: %s", reload, scaleP21M2Diff(outputs[0], outputs[1]))
						}
					})
				}
			})
		}
		// Supplied policy and each document lookup retain separate generations.
		r := f.literalRequest(f.principals[2].user, nil, nil)
		r.Method, r.URL.Path = http.MethodGet, "/api/realtime/presence"
		service := &server{store: f.probe, realtime: newRealtimeHub()}
		data, query, err := service.pagedRealtimeMetadata(r)
		if err != nil {
			t.Fatal(err)
		}
		before := scaleP21SW62PresenceJSON(t, roster)
		if _, err := service.pagedPresenceFor(r, data, query, roster); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, scaleP21SW62PresenceJSON(t, roster)) {
			t.Fatal("presence projection mutated shared roster")
		}
		cancelled, cancel := context.WithCancel(r.Context())
		cancel()
		if _, err := service.pagedPresenceFor(r.WithContext(cancelled), data, query, []domain.Presence{{DocumentID: public}}); !errors.Is(err, context.Canceled) {
			t.Fatal("document roster read failure became a visibility decision", err)
		}
		r.URL.RawQuery = url.Values{"documentId": {public}}.Encode()
		w := httptest.NewRecorder()
		scaleP21CallWithOutputOwner(service, w, r.WithContext(cancelled), (*server).listPresence)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatal("presence read failure became permission denial or success", w.Code, w.Body.String())
		}
		if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("presence adapters leaked work or decoded complete metadata")
		}
	}
}
