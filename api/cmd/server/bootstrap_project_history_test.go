package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// scaleP21BootstrapHistoryProjects is enough project progress history that
// charging every header frame's cost for the whole read (the bounded
// bootstrap's former admission) exceeded the process work budget, which
// refused every bootstrap of the workspace with 503 while the flag-off
// bootstrap answered 200. The headers themselves take a few MiB.
const (
	scaleP21BootstrapHistoryProjects = 160
	scaleP21BootstrapHistoryPoints   = 40
)

func scaleP21BootstrapHistorySeed(t *testing.T, repo *store.SQLiteStore) scaleP21SW145Fixture {
	t.Helper()
	f := scaleP21SW145Seed(t, repo)
	stamp := time.Date(2026, time.November, 19, 9, 10, 11, 0, time.UTC)
	restore := store.ForceFullMutationsForTesting(true)
	defer restore()
	err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.bootstrap_history_seed", "", nil, func(data *domain.Bootstrap) error {
		private := ""
		for _, team := range data.Teams {
			if team.Private {
				private = team.ID
			}
		}
		owner := data.Viewer
		for index := range scaleP21BootstrapHistoryProjects {
			history := func(offset float64) []domain.ProjectProgressHistoryPoint {
				points := make([]domain.ProjectProgressHistoryPoint, scaleP21BootstrapHistoryPoints)
				for day := range points {
					points[day] = domain.ProjectProgressHistoryPoint{
						Date: stamp.AddDate(0, 0, -day), Value: offset + float64(day)/7, ScopeEstimate: float64(index + day),
						ScopeCount: index + day, CompletedIssueCount: day, StartedIssueCount: day % 3, AddedEstimateToday: float64(day % 5),
					}
				}
				return points
			}
			team := f.public
			if index%3 == 0 {
				team = private
			}
			id := fmt.Sprintf("history-project-%03d", index)
			data.Projects = append(data.Projects, domain.Project{
				ID: id, Name: fmt.Sprintf("History project %d", index), SlugID: fmt.Sprintf("history-project-%03d-%012x", index, index),
				TeamIDs: []string{team}, MemberIDs: []string{owner.ID}, Health: "onTrack",
				Description: fmt.Sprintf("Project %d body /uploads/history-%d.png ", index, index) + strings.Repeat("progress ", 200),
				Comments: []domain.Comment{{
					ID: id + "-comment", Body: "History comment /uploads/history-comment.png", User: owner, CreatedAt: stamp, Version: 1,
				}},
				IssueCountHistory: history(1), ScopeHistory: history(2), CompletedScopeHistory: history(3),
				InProgressScopeHistory: history(4), ProgressHistory: history(5),
				Creator: &owner, Lead: &owner, CreatedAt: stamp, UpdatedAt: stamp,
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

type scaleP21BootstrapHistoryResponse struct {
	Status                  int
	RetryAfter, ContentType string
	Body                    string
}

func scaleP21BootstrapHistoryServe(t *testing.T, f scaleP21SW145Fixture, source *store.SQLiteStore, test scaleP21SW145Case) scaleP21BootstrapHistoryResponse {
	return scaleP21BootstrapHistoryServeWith(t, f, &server{store: source, authDisabled: test.development}, test)
}

func scaleP21BootstrapHistoryServeWith(t *testing.T, f scaleP21SW145Fixture, service *server, test scaleP21SW145Case) scaleP21BootstrapHistoryResponse {
	t.Helper()
	request := f.literalRequest(test.user, test.key, nil)
	request.Method, request.URL.Path = http.MethodGet, "/api/issue-records/bootstrap"
	request.Header.Set("X-Flow-Projection", test.projection)
	request, closeOutput := beginRequestResourceReads(request)
	defer closeOutput()
	writer := httptest.NewRecorder()
	service.signedFileURLs(http.HandlerFunc(service.issueRecordsBootstrap)).ServeHTTP(writer, request)
	return scaleP21BootstrapHistoryResponse{writer.Code, writer.Header().Get("Retry-After"), writer.Header().Get("Content-Type"), writer.Body.String()}
}

// TestScaleP21IssueBootstrapLargeProjectHistory compares the paged bootstrap
// flag off and on for a workspace whose project headers are large, for every
// principal shape and projection: the bounded read answers the same bytes
// instead of refusing, and releases all of its work.
func TestScaleP21IssueBootstrapLargeProjectHistory(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP21BootstrapHistorySeed(t, repo)
			workspace := f.data.Workspace.URLKey
			view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace, f.other)
			if err != nil {
				t.Fatal(err)
			}
			cases := []scaleP21SW145Case{
				{name: "admin-private-outsider", user: f.principals[0].user},
				{name: "private-admin", user: f.principals[1].user},
				{name: "member", user: f.principals[2].user},
				{name: "guest-explicit", user: f.principals[3].user},
				{name: "inherited-selected", user: f.principals[4].user, key: &domain.APIKey{ID: "history-selected", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
				{name: "development", user: f.data.Viewer, development: true},
			}
			histories := 0
			for _, test := range cases {
				for _, projection := range []string{"", "project-list", "issue-detail"} {
					test.workspace, test.projection = workspace, projection
					t.Run(fmt.Sprintf("%s/projection=%s", test.name, projection), func(t *testing.T) {
						legacy := scaleP21BootstrapHistoryServe(t, f, repo, test)
						if legacy.Status != http.StatusOK {
							t.Fatal("flag-off bootstrap failed", legacy.Status, legacy.Body)
						}
						histories = max(histories, strings.Count(legacy.Body, `"scopeCount"`))
						decodes := view.WorkspaceMetadataDecodes()
						view.MetadataWorkPeakForTesting()
						bounded := scaleP21BootstrapHistoryServe(t, f, view, test)
						peak := view.MetadataWorkPeakForTesting()
						if bounded.Status != legacy.Status || bounded.RetryAfter != legacy.RetryAfter || bounded.ContentType != legacy.ContentType {
							t.Fatalf("flag-on bootstrap answered %d (Retry-After %q) where flag-off answered %d: %.200s", bounded.Status, bounded.RetryAfter, legacy.Status, bounded.Body)
						}
						if bounded.Body != legacy.Body {
							t.Fatalf("flag-on bootstrap body differs: %d/%d bytes", len(bounded.Body), len(legacy.Body))
						}
						if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != decodes {
							t.Fatal("bounded bootstrap retained work or decoded the workspace", view.MetadataWorkUsedForTesting())
						}
						if peak <= 0 || peak > 128<<20 {
							t.Fatal("bounded bootstrap admitted an unexpected amount of work", peak)
						}
					})
				}
			}
			// The admin projections carry every project's histories.
			if want := scaleP21BootstrapHistoryProjects * 4 / 3 * scaleP21BootstrapHistoryPoints; histories < want {
				t.Fatal("the fixture's histories were not served", histories, want)
			}
		})
	}
}

// TestScaleP21IssueBootstrapAccessChangedDuringRead moves the workspace's
// authorization epoch while a bounded bootstrap holds its access (another
// member made admin and member again). The flag-off
// bootstrap reads one consistent workspace and answers 200; the bounded one
// reads again and answers the same bytes. Authorization that keeps changing
// under every read is a retryable 503, never a 403.
func TestScaleP21IssueBootstrapAccessChangedDuringRead(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP21SW145Seed(t, repo)
			workspace := f.data.Workspace.URLKey
			view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace, f.other)
			if err != nil {
				t.Fatal(err)
			}
			edits := 0
			// A member made admin and member again: the epoch moves twice and
			// no response changes.
			bystander := f.principals[4].user
			if role, _, err := repo.WorkspaceRole(t.Context(), f.data.Workspace.ID, bystander.ID); err != nil || role != "member" {
				t.Fatal("the bystander is not a member", role, err)
			}
			edit := func() {
				edits++
				for _, next := range []string{"admin", "member"} {
					if err := repo.UpdateMemberRole(t.Context(), f.data.Workspace.ID, bystander.ID, next); err != nil {
						t.Error(err)
					}
				}
			}
			for _, test := range []scaleP21SW145Case{
				{name: "member", user: f.principals[2].user},
				{name: "admin", user: f.principals[1].user, projection: "project-list"},
			} {
				test.workspace = workspace
				t.Run(test.name, func(t *testing.T) {
					edits = 0
					// The first read's trailing check (its third epoch read) finds
					// the epoch moved; the next read is undisturbed.
					serve := func(source *store.SQLiteStore, observe func(string)) scaleP21BootstrapHistoryResponse {
						service := &server{store: source}
						request := f.literalRequest(test.user, test.key, nil)
						request.Method, request.URL.Path = http.MethodGet, "/api/issue-records/bootstrap"
						request.Header.Set("X-Flow-Projection", test.projection)
						if observe != nil {
							request = request.WithContext(store.WithSQLStatementObserver(request.Context(), observe))
						}
						request, closeOutput := beginRequestResourceReads(request)
						defer closeOutput()
						writer := httptest.NewRecorder()
						service.issueRecordsBootstrap(writer, request)
						return scaleP21BootstrapHistoryResponse{writer.Code, writer.Header().Get("Retry-After"), writer.Header().Get("Content-Type"), writer.Body.String()}
					}
					epochRead := func(query string) bool {
						return strings.Contains(query, "workspace_authz_epochs") && strings.Contains(query, "record_journal_mode")
					}
					reads := 0
					bounded := serve(view, func(query string) {
						if epochRead(query) {
							if reads++; reads == 3 {
								edit()
							}
						}
					})
					legacy := serve(repo, nil)
					if legacy.Status != http.StatusOK || bounded != legacy {
						t.Fatalf("flag-on answered %d (%d bytes), flag-off %d (%d bytes): %.200s", bounded.Status, len(bounded.Body), legacy.Status, len(legacy.Body), bounded.Body)
					}
					if edits != 1 || reads <= 3 || view.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("the bootstrap was not read again after the epoch moved, or kept work", edits, reads)
					}
					// Authorization that never settles.
					refused := serve(view, func(query string) {
						if epochRead(query) {
							edit()
						}
					})
					if refused.Status != http.StatusServiceUnavailable || refused.RetryAfter != "2" || view.MetadataWorkUsedForTesting() != 0 {
						t.Fatalf("unsettled authorization answered %d (Retry-After %q): %.200s", refused.Status, refused.RetryAfter, refused.Body)
					}
				})
			}
		})
	}
}
