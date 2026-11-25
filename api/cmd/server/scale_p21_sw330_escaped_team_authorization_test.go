package main

import (
	"context"
	"fmt"
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

func TestScaleP21SW330EscapedTeamAuthorizationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "off")
	for driver, repository := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			foreign := scaleP03Fixture(t, repository)
			f := scaleP21M2Setup(t, repository)
			workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw330-")
			data, found := repository.WorkspaceMetadata(workspace)
			if !found {
				t.Fatal("missing owned M workspace")
			}
			private, child := "", ""
			for _, team := range data.Teams {
				if strings.HasPrefix(team.ID, "m2-private-") {
					private = team.ID
				}
			}
			for id, settings := range data.TeamSettings {
				if settings.ParentTeamID == private {
					child = id
				}
			}
			if private == "" || child == "" {
				t.Fatal("fixture lacks private and inherited team boundaries")
			}
			at := time.Date(2026, 11, 17, 1, 2, 3, 0, time.UTC)
			full := store.ForceFullMutationsForTesting(true)
			err := repository.MutateWorkspace(t.Context(), workspace, "scale_p21.sw330_seed", "", nil, func(data *domain.Bootstrap) error {
				for index, row := range []struct {
					id      string
					private bool
				}{
					{f.public + "/b", true},
					{private + "/b", false},
					{private + "/resources", false},
					{private + "/resource-sections", false},
					{f.public + "/resources", true},
					{f.public + "/resource-sections", true},
					{"", false},
					{"/b", false},
				} {
					access := ""
					if row.private {
						access = "private"
					}
					data.Teams = append(data.Teams, domain.Team{
						ID: row.id, Key: fmt.Sprintf("%sTEAM%d", prefix, index), Private: row.private,
					})
					data.TeamSettings[row.id] = domain.TeamSettings{TeamID: row.id, Access: access}
				}
				for _, team := range []string{f.public, private, child} {
					data.TeamResourceSections = append(data.TeamResourceSections, domain.TeamResourceSection{
						ID: prefix + "section-" + team, TeamID: team, Name: "SW330 selected section", Position: 1, CreatedAt: at, UpdatedAt: at,
					})
					data.TeamPinnedResources = append(data.TeamPinnedResources, domain.TeamPinnedResource{
						ID: prefix + "resource-" + team, TeamID: team, ResourceType: "link", Title: "SW330 selected resource",
						URL: "https://example.test/sw330", Position: 1, CreatedAt: at, UpdatedAt: at,
					})
				}
				return nil
			})
			full()
			if err != nil {
				t.Fatal(err)
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repository, workspace); err != nil {
				t.Fatal(err)
			}
			type outcome struct {
				Allowed, Handoff bool
				Status           int
				Body, Pattern    string
			}
			pairs, rowAdminAllows, contentAdminDenials, resourceNoHandoffs := 0, 0, 0, 0
			legacySQL, boundedSQL, maxBoundedSQL := 0, 0, 0
			families := map[string]int{}
			for _, row := range []struct {
				name, method, ref, tail string
				encodedFixed, foreign   bool
			}{
				{name: "plain-members", method: http.MethodGet, ref: f.public, tail: "/members"},
				{name: "public-prefix-private-full", method: http.MethodGet, ref: f.public + "/b", tail: "/members"},
				{name: "private-prefix-public-full", method: http.MethodGet, ref: private + "/b", tail: "/members"},
				{name: "injected-content-members", method: http.MethodGet, ref: private + "/resources", tail: "/members"},
				{name: "injected-sections-members", method: http.MethodHead, ref: private + "/resource-sections", tail: "/members"},
				{name: "slash-public-resource-row", method: http.MethodGet, ref: f.public + "/b", tail: "/resources"},
				{name: "slash-private-resource-row", method: http.MethodGet, ref: private + "/b", tail: "/resources"},
				{name: "injected-content-resource", method: http.MethodGet, ref: private + "/resources", tail: "/resources"},
				{name: "public-content-private-full", method: http.MethodGet, ref: f.public + "/resources", tail: "/resources"},
				{name: "plain-resource-output", method: http.MethodGet, ref: f.public, tail: "/resources"},
				{name: "plain-resource-head", method: http.MethodHead, ref: f.public, tail: "/resources"},
				{name: "inherited-members", method: http.MethodGet, ref: child, tail: "/members"},
				{name: "empty-decoded-prefix", method: http.MethodGet, ref: "/b", tail: "/members"},
				{name: "encoded-fixed-members", method: http.MethodGet, ref: f.public + "/b", tail: "/members", encodedFixed: true},
				{name: "encoded-fixed-resources", method: http.MethodGet, ref: f.public, tail: "/resources", encodedFixed: true},
				{name: "slash-team-put", method: http.MethodPut, ref: f.public + "/b", tail: "/members/" + url.PathEscape(prefix+"missing/resources")},
				{name: "slash-team-delete", method: http.MethodDelete, ref: private + "/b", tail: "/members/" + url.PathEscape(prefix+"missing/resources")},
				{name: "injected-content-sync", method: http.MethodPost, ref: private + "/resources", tail: "/members/sync"},
				{name: "slash-user-put", method: http.MethodPut, ref: f.public, tail: "/members/" + url.PathEscape(prefix+"missing/resources")},
				{name: "foreign-workspace", method: http.MethodGet, ref: f.public + "/b", tail: "/members", foreign: true},
			} {
				t.Run(row.name, func(t *testing.T) {
					f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
						var expected outcome
						for _, source := range []*store.SQLiteStore{repository, f.probe} {
							request := f.literalRequest(user, key, map[string]any{"role": "invalid", "members": "invalid"})
							request.Method = row.method
							head, tail := "/api/teams/", row.tail
							if row.encodedFixed {
								head = "/%61pi/%74eams/"
								tail = strings.Replace(tail, "/members", "/%6dembers", 1)
								tail = strings.Replace(tail, "/resources", "/%72esources", 1)
							}
							var err error
							request.URL, err = url.Parse(head + url.PathEscape(row.ref) + tail)
							if err != nil {
								t.Fatal(err)
							}
							if strings.Contains(row.ref, "/") && request.URL.RawPath == "" {
								t.Fatal("escaped-team fixture lost its RawPath")
							}
							if row.method == http.MethodDelete {
								request.URL.RawQuery = "dryRun=true"
							}
							selectedWorkspace := workspace
							if row.foreign {
								selectedWorkspace = foreign.data.Workspace.URLKey
								request.Header.Set("X-Workspace-Key", selectedWorkspace)
							}
							request, release := beginRequestResourceReads(request)
							statements := 0
							request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
							service := &server{store: source}
							if !source.BoundedMetadataEnabled() {
								if _, handled := service.boundedTeamMembershipAuthorization(request, selectedWorkspace, user.ID); handled {
									t.Fatal("membership adapter changed the literal off path")
								}
								if _, handled := service.boundedTeamResourceAuthorization(request, selectedWorkspace, user.ID); handled {
									t.Fatal("resource adapter changed the literal off path")
								}
							} else {
								memberRef, member := scaleP21SW315TeamMembershipAuthorizationRequest(request)
								resourceRef, resource := scaleP21SW307TeamResourceAuthorizationRequest(request)
								parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
								if member == resource || member && memberRef != parts[2] || resource && resourceRef != parts[2] {
									t.Fatal("escaped family failed to select its literal decoded first reference")
								}
							}
							got := outcome{Allowed: service.resourceAllowedLegacy(request, selectedWorkspace, user.ID)}
							_, got.Handoff = request.Context().Value(teamResourcesReadKey{}).(teamResourcesRead)
							parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
							wantHandoff := got.Allowed && row.method == http.MethodGet && len(parts) == 4 && parts[3] == "resources"
							if got.Handoff != wantHandoff {
								t.Fatal("registered raw suffix invented or lost a literal decoded handoff", got.Handoff, wantHandoff)
							}
							mux := http.NewServeMux()
							bind := func(pattern string, leaf http.HandlerFunc) {
								mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
									got.Pattern = r.Pattern
									if r.PathValue("id") != row.ref {
										t.Fatal("real mux lost the full escaped wildcard")
									}
									leaf(w, r)
								})
							}
							bind("GET /api/teams/{id}/members", service.listTeamMemberships)
							bind("PUT /api/teams/{id}/members/{userId}", service.putTeamMembership)
							bind("DELETE /api/teams/{id}/members/{userId}", service.deleteTeamMembership)
							bind("POST /api/teams/{id}/members/sync", service.syncTeamMemberships)
							bind("GET /api/teams/{id}/resources", service.listTeamResources)
							recorder := httptest.NewRecorder()
							if got.Allowed {
								mux.ServeHTTP(recorder, request)
							} else {
								writeError(recorder, http.StatusForbidden, "This resource is outside your teams")
							}
							got.Status, got.Body = recorder.Code, recorder.Body.String()
							if got.Allowed && got.Pattern == "" {
								t.Fatal("allowed escaped route never reached its production leaf")
							}
							if !source.BoundedMetadataEnabled() {
								expected = got
								legacySQL += statements
								if got.Allowed {
									families[row.method]++
								}
							} else {
								pairs++
								boundedSQL += statements
								maxBoundedSQL = max(maxBoundedSQL, statements)
								if !reflect.DeepEqual(expected, got) {
									t.Fatalf("escaped team route differs: literal=%+v bounded=%+v", expected, got)
								}
								unrestricted := key == nil || !apiKeyTeamRestrictionSelected(*key)
								if user.ID == f.principals[0].user.ID && unrestricted {
									if row.name == "private-prefix-public-full" || row.name == "slash-private-resource-row" {
										if !got.Allowed {
											t.Fatal("private-team outsider admin lost row access")
										}
										rowAdminAllows++
									}
									if row.name == "injected-content-members" || row.name == "injected-sections-members" {
										if got.Allowed {
											t.Fatal("escaped membership route reduced a content ACL to a row ACL")
										}
										contentAdminDenials++
									}
								}
								if strings.Contains(row.ref, "/") && strings.HasSuffix(row.tail, "/resources") {
									if got.Handoff || request.Context().Value(teamResourcesReadKey{}) != nil {
										t.Fatal("escaped resource route advertised invented output")
									}
									resourceNoHandoffs++
								}
							}
							release()
							if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
								t.Fatal("team adapter retained work or hydrated unrelated metadata")
							}
						}
					})
				})
			}
			if pairs == 0 || rowAdminAllows == 0 || contentAdminDenials == 0 || resourceNoHandoffs == 0 {
				t.Fatal("escaped team table lost row/content/handoff witnesses")
			}
			for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodPost} {
				if families[method] == 0 {
					t.Fatal("registered method family never reached a production leaf", method)
				}
			}
			t.Logf("pairs=%d row-admin=%d content-admin-denials=%d no-invented-output=%d SQL legacy=%d bounded=%d max-bounded-per-route=%d",
				pairs, rowAdminAllows, contentAdminDenials, resourceNoHandoffs, legacySQL, boundedSQL, maxBoundedSQL)
		})
	}
}

func TestScaleP21SW330EscapedTeamFamilyBoundary(t *testing.T) {
	for _, row := range []struct{ method, path string }{
		{http.MethodGet, "/api/teams/a%2Fresources/members"},
		{http.MethodHead, "/api/teams/a%2Fresource-sections/members"},
		{http.MethodPut, "/api/teams/a%2Fb/members/user%2Fresources"},
		{http.MethodDelete, "/api/teams/a%2Fb/members/user%2Fresources"},
		{http.MethodPost, "/api/teams/a%2Fresources/members/sync"},
		{http.MethodGet, "/api/teams/a%2Fb/resources"},
		{http.MethodHead, "/api/teams/%2Fb/resources"},
	} {
		request := httptest.NewRequest(row.method, row.path, nil)
		_, member := scaleP21SW315TeamMembershipAuthorizationRequest(request)
		_, resource := scaleP21SW307TeamResourceAuthorizationRequest(request)
		if member == resource {
			t.Fatal("escaped registered route was not selected exactly once", row)
		}
	}
	for _, row := range []struct{ method, path string }{
		{http.MethodGet, "/api%2Fteams/a/members"},
		{http.MethodGet, "/api/teams/a%2Fb/members/user"},
		{http.MethodPost, "/api/teams/a%2Fb/resources"},
		{http.MethodPost, "/api/teams/a%2Fb/members/user"},
		{http.MethodGet, "/api/teams/a%2Fb/resources/extra"},
		{http.MethodGet, "/api/teams//members"},
		{http.MethodPut, "/api/teams/a%2Fb/members/"},
	} {
		request := httptest.NewRequest(row.method, row.path, nil).WithContext(context.Background())
		if _, selected := scaleP21SW315TeamMembershipAuthorizationRequest(request); selected {
			t.Fatal("membership adapter captured another escaped family", row)
		}
		if _, selected := scaleP21SW307TeamResourceAuthorizationRequest(request); selected {
			t.Fatal("resource adapter captured another escaped family", row)
		}
	}
}
