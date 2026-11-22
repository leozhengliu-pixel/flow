package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// TestSyncChangesPulsePagesDifferential reads sync change pages with several
// project, initiative and document changes per page through a flag-off and a
// bounded server and requires identical responses for every principal and a
// team-restricted key: old history (records that moved on), current records,
// hidden projects and an initiative only a hidden project links to, a deleted
// project, and a commit by a peer replica between pages, read both before and
// after this replica applies it.
func TestSyncChangesPulsePagesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_SYNC_V2", "on")
	config := scaleP03DatabaseConfig(t)
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	peer, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	f := scaleP21M2Setup(t, repo)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	visible, renamed, hidden, removed := idgen.New("sync-pulse-visible-"), idgen.New("sync-pulse-renamed-"), idgen.New("sync-pulse-hidden-"), idgen.New("sync-pulse-removed-")
	// linked has no teams and no projects of its own: only the hidden project
	// links to it (from the project's side), which hides it from outsiders.
	linked := idgen.New("sync-pulse-linked-")
	document, privateDocument := idgen.New("sync-pulse-document-"), idgen.New("sync-pulse-private-document-")
	initiative, parent := idgen.New("sync-pulse-initiative-"), idgen.New("sync-pulse-parent-")
	private := ""
	mutate := func(target *store.SQLiteStore, name string, change func(*domain.Bootstrap) error) {
		t.Helper()
		if err := target.MutateWorkspace(t.Context(), workspace, name, "", nil, change); err != nil {
			t.Fatal(err)
		}
	}
	project := func(data *domain.Bootstrap, id string) *domain.Project {
		for index := range data.Projects {
			if data.Projects[index].ID == id {
				return &data.Projects[index]
			}
		}
		t.Fatalf("project %s is missing", id)
		return nil
	}
	mutate(repo, "sync_pulse.seed", func(data *domain.Bootstrap) error {
		for _, item := range data.Projects {
			if item.ID == "m2-unrelated" && len(item.TeamIDs) != 0 {
				private = item.TeamIDs[0]
			}
		}
		if private == "" {
			return errors.New("fixture private team is missing")
		}
		data.Projects = append(data.Projects,
			domain.Project{ID: visible, Name: "Visible project", TeamIDs: []string{f.public}, DependencyIDs: []string{hidden}, Initiatives: []string{initiative}, Lead: &data.Viewer},
			domain.Project{ID: renamed, Name: "Renamed project", TeamIDs: []string{f.public}},
			domain.Project{ID: hidden, Name: "Hidden project secret", TeamIDs: []string{private}, Initiatives: []string{initiative, linked}},
			domain.Project{ID: removed, Name: "Removed project secret", TeamIDs: []string{private}},
		)
		data.Initiatives = append(data.Initiatives,
			domain.Initiative{ID: initiative, Name: "Mixed initiative", ProjectIDs: []string{visible}, ContributingTeamIDs: []string{f.public, private}, Owner: &data.Viewer},
			domain.Initiative{ID: parent, Name: "Parent initiative", LeadTeamID: private},
			domain.Initiative{ID: linked, Name: "Linked initiative secret"},
		)
		data.InitiativeRelations = append(data.InitiativeRelations, domain.InitiativeRelation{ID: idgen.New("sync-pulse-relation-"), Type: "parent", InitiativeID: initiative, RelatedInitiativeID: parent})
		data.Documents = append(data.Documents,
			domain.Document{ID: document, SlugID: document + "-slug", Title: "Shared document", Content: "Shared body", TeamIDs: []string{f.public}, Creator: data.Viewer},
			domain.Document{ID: privateDocument, SlugID: privateDocument + "-slug", Title: "Private document secret", Content: "Private body", TeamIDs: []string{private}, Creator: data.Viewer},
		)
		return nil
	})
	start, err := repo.RecordJournalHead(t.Context(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	// Several metadata changes, most of them later superseded: a page holds
	// stale and current records of the same and of different projects.
	for round := range 3 {
		mutate(repo, "sync_pulse.rename", func(data *domain.Bootstrap) error {
			project(data, renamed).Name = "Renamed project " + strings.Repeat("v", round+1)
			project(data, hidden).Name = "Hidden project secret " + strings.Repeat("h", round+1)
			for index := range data.Documents {
				if data.Documents[index].ID == document {
					data.Documents[index].Title += " edited"
				}
			}
			return nil
		})
	}
	mutate(repo, "sync_pulse.links", func(data *domain.Bootstrap) error {
		project(data, visible).Name = "Visible project linked"
		for index := range data.Documents {
			if data.Documents[index].ID == privateDocument {
				data.Documents[index].Title += " edited"
			}
		}
		for index := range data.Initiatives {
			if data.Initiatives[index].ID == initiative {
				data.Initiatives[index].Name = "Mixed initiative renamed"
				data.Initiatives[index].ProjectIDs = append(data.Initiatives[index].ProjectIDs, hidden)
			}
			if data.Initiatives[index].ID == linked {
				data.Initiatives[index].Name = "Linked initiative secret renamed"
			}
		}
		return nil
	})
	mutate(repo, "sync_pulse.delete", func(data *domain.Bootstrap) error {
		for index := range data.Projects {
			if data.Projects[index].ID == removed {
				data.Projects = append(data.Projects[:index], data.Projects[index+1:]...)
				return nil
			}
		}
		return errors.New("removed project is missing")
	})
	// Every principal's session, and the owner through an API key restricted
	// to the public team.
	scaleP03Key(t, f.scaleP03PlatformFixture, []string{"read"}, []string{f.public})
	data, ok := repo.BootstrapFor(workspace)
	if !ok || len(data.APIKeys) == 0 {
		t.Fatal("restricted key is missing")
	}
	restricted := data.APIKeys[len(data.APIKeys)-1]
	if restricted.TeamRestriction != "selected" {
		t.Fatalf("key is not team restricted: %+v", restricted.TeamRestriction)
	}
	matrix := func(t *testing.T, visit func(*testing.T, domain.User, *domain.APIKey)) {
		for _, principal := range f.principals {
			t.Run(principal.name, func(t *testing.T) { visit(t, principal.user, nil) })
		}
		t.Run("restricted-key", func(t *testing.T) { visit(t, f.data.Viewer, &restricted) })
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
		t.Fatal(err)
	}
	type page struct {
		code int
		body string
	}
	read := func(service *server, journal committedChangeReader, user domain.User, key *domain.APIKey, after string) (page, syncV2ChangesResponse) {
		t.Helper()
		r := f.literalRequest(user, key, nil)
		r.Method = http.MethodGet
		r.URL.Path = "/api/sync/changes"
		r.URL.RawQuery = "limit=3&after=" + after
		w := httptest.NewRecorder()
		service.syncChangesWithJournal(w, r, journal)
		var result syncV2ChangesResponse
		if w.Code == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return page{w.Code, w.Body.String()}, result
	}
	// drain reads every page from start through both servers, comparing each.
	drain := func(t *testing.T, user domain.User, key *domain.APIKey) []string {
		t.Helper()
		old := &server{store: repo, realtime: newRealtimeHub()}
		bounded := &server{store: f.probe, realtime: newRealtimeHub()}
		bodies := []string{}
		after := start
		for pages := 0; ; pages++ {
			if pages > 50 {
				t.Fatal("changes did not reach the head")
			}
			want, result := read(old, repo, user, key, after)
			got, _ := read(bounded, f.probe, user, key, after)
			if want != got {
				t.Fatalf("page after %s differs:\nflag off: %d %s\nbounded:  %d %s", after, want.code, want.body, got.code, got.body)
			}
			if want.code != http.StatusOK {
				t.Fatalf("page after %s: %d %s", after, want.code, want.body)
			}
			bodies = append(bodies, want.body)
			if !result.HasMore {
				break
			}
			after = result.Next
		}
		if f.probe.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("bounded sync changes retained metadata work")
		}
		return bodies
	}
	check := func(t *testing.T, user domain.User, key *domain.APIKey) {
		bodies := strings.Join(drain(t, user, key), "\n")
		name := ""
		for _, principal := range f.principals {
			if principal.user.ID == user.ID {
				name = principal.name
			}
		}
		// The owner's restricted key is held to its teams like the outsiders:
		// the creator grant of its owner does not widen it.
		if (name == "member-private-outsider" || name == "guest-explicit" || key != nil) && strings.Contains(bodies, privateDocument) {
			t.Fatalf("a private document reached an outsider: %s", bodies)
		}
		if (key != nil || name == "member-private-outsider" || name == "guest-explicit") && (strings.Contains(bodies, hidden) || strings.Contains(bodies, removed) || strings.Contains(bodies, linked) || strings.Contains(bodies, "project secret") || strings.Contains(bodies, "initiative secret")) {
			t.Fatalf("a hidden record reached an outsider: %s", bodies)
		}
		if strings.Contains(bodies, "Removed project secret") {
			t.Fatalf("a deleted project's record was sent: %s", bodies)
		}
		// A private team member gets the current records as upserts, not
		// only invalidations: the comparison is not between two empty pages.
		if key == nil && name == "admin" && (!strings.Contains(bodies, `{"key":{"kind":"projects"`) || !strings.Contains(bodies, `{"key":{"kind":"initiatives"`) || !strings.Contains(bodies, "Linked initiative secret renamed") || !strings.Contains(bodies, "Shared document edited edited edited") || !strings.Contains(bodies, "Private document secret edited")) {
			t.Fatalf("current project and initiative records were not applied directly: %s", bodies)
		}
	}
	matrix(t, check)

	// A peer replica commits between pages; this replica reads it before and
	// after applying it.
	if err := peer.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	mutate(peer, "sync_pulse.peer", func(data *domain.Bootstrap) error {
		project(data, visible).Name = "Visible project from peer"
		project(data, hidden).TeamIDs = []string{f.public}
		return nil
	})
	matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) { drain(t, user, key) })
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
		t.Fatal(err)
	}
	matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
		bodies := strings.Join(drain(t, user, key), "\n")
		if key == nil && user.ID == f.data.Viewer.ID && !strings.Contains(bodies, "Visible project from peer") {
			t.Fatalf("the peer commit was not applied: %s", bodies)
		}
	})
}

// TestSyncChangesReprojectsAPageRacedByAWrite: a write while a page is
// projected moves the workspace's source version and used to fail the read
// with a 503. The page is now projected again (bounded), flag off and on
// alike; an authorization change still fails it without a retry.
func TestSyncChangesReprojectsAPageRacedByAWrite(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_SYNC_V2", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
		project := idgen.New("sync-race-project-")
		rename := func(name string) {
			t.Helper()
			if err := repo.MutateWorkspace(t.Context(), workspace, "sync_race.rename", "", nil, func(data *domain.Bootstrap) error {
				for index := range data.Projects {
					if data.Projects[index].ID == project {
						data.Projects[index].Name = name
						return nil
					}
				}
				data.Projects = append(data.Projects, domain.Project{ID: project, Name: name, TeamIDs: []string{f.public}})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		rename("Race project")
		start, err := repo.RecordJournalHead(t.Context(), workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		rename("Race project renamed")
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		writeIssue := func() {
			t.Helper()
			authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Race write", "teamId": f.public}, workspace, http.StatusCreated)
		}
		const requestedLimit = 100
		for _, bounded := range []bool{false, true} {
			target := repo
			if bounded {
				target = f.probe
			}
			read := func(cursor string, during func(call, transactions int)) (int, []int, string) {
				t.Helper()
				var limits []int
				journal := scaleP12JournalFunc(func(ctx context.Context, id, after string, limit, maxBytes int) (store.CommittedPage, error) {
					if id != workspaceID || after != cursor || maxBytes != syncV2DeltaBytes {
						t.Fatalf("journal scope changed on retry: workspace %q, after %q, maxBytes %d", id, after, maxBytes)
					}
					limits = append(limits, limit)
					page, err := target.ReadCommittedChanges(ctx, id, after, limit, maxBytes)
					if err == nil && during != nil {
						during(len(limits), len(page.Transactions))
					}
					return page, err
				})
				r := f.literalRequest(f.data.Viewer, nil, nil)
				r.Method = http.MethodGet
				r.URL.Path = "/api/sync/changes"
				r.URL.RawQuery = "after=" + cursor + "&limit=" + strconv.Itoa(requestedLimit)
				w := httptest.NewRecorder()
				(&server{store: target, realtime: newRealtimeHub()}).syncChangesWithJournal(w, r, journal)
				return w.Code, limits, w.Body.String()
			}
			// An issue write during the first projection only.
			code, limits, body := read(start, func(call, _ int) {
				if call == 1 {
					writeIssue()
				}
			})
			if code != http.StatusOK || !slices.Equal(limits, []int{requestedLimit, requestedLimit}) || !strings.Contains(body, "Race project renamed") {
				t.Fatalf("bounded=%v: raced page was not projected again: %d after limits %v %s", bounded, code, limits, body)
			}
			if _, _, again := read(start, nil); again != body {
				t.Fatalf("bounded=%v: the projected-again page differs from a later read:\n%s\n%s", bounded, body, again)
			}
			// A large page loses its proof on each full-size attempt. Read one
			// transaction, then resume from its actual cursor and compare with a
			// quiet full read to detect skipped, duplicated, or split changes.
			code, limits, body = read(start, func(_, transactions int) {
				if transactions > 1 {
					writeIssue()
				}
			})
			if code != http.StatusOK || !slices.Equal(limits, []int{requestedLimit, requestedLimit, 1}) {
				t.Fatalf("bounded=%v: short retry: %d after limits %v %s", bounded, code, limits, body)
			}
			var prefix, suffix syncV2ChangesResponse
			if err := json.Unmarshal([]byte(body), &prefix); err != nil {
				t.Fatal(err)
			}
			if prefix.ResyncRequired || !prefix.HasMore || len(prefix.Transactions) != 1 || prefix.Next != prefix.Transactions[0].Seq || prefix.Next == start || prefix.Next == prefix.Head {
				t.Fatalf("bounded=%v: short retry returned the wrong cursor or transaction: %s", bounded, body)
			}
			code, _, body = read(prefix.Next, nil)
			if err := json.Unmarshal([]byte(body), &suffix); err != nil {
				t.Fatal(err)
			}
			if code != http.StatusOK || suffix.ResyncRequired || suffix.HasMore || len(suffix.Transactions) == 0 || suffix.Head != prefix.Head || suffix.AuthzEpoch != prefix.AuthzEpoch {
				t.Fatalf("bounded=%v: wrong continuation: %d %s", bounded, code, body)
			}
			suffix.Transactions = append(prefix.Transactions, suffix.Transactions...)
			combined, err := json.Marshal(suffix)
			if err != nil {
				t.Fatal(err)
			}
			if code, _, full := read(start, nil); code != http.StatusOK || full != string(combined) {
				t.Fatalf("bounded=%v: paged retry differs from a quiet full read:\n%s\n%s", bounded, combined, full)
			}
			const futureCursor = "9007199254740993"
			code, limits, body = read(futureCursor, nil)
			var reset syncV2ChangesResponse
			if err := json.Unmarshal([]byte(body), &reset); err != nil {
				t.Fatal(err)
			}
			if code != http.StatusOK || len(limits) != 1 || !reset.ResyncRequired || reset.Next != futureCursor || reset.Head != prefix.Head || reset.AuthzEpoch != prefix.AuthzEpoch || reset.HasMore || len(reset.Transactions) != 0 {
				t.Fatalf("bounded=%v: invalid resync response: %d after limits %v %s", bounded, code, limits, body)
			}
			// A write during every projection: the retries are bounded.
			code, limits, body = read(start, func(int, int) { writeIssue() })
			if code != http.StatusServiceUnavailable || len(limits) != syncChangesAttempts || strings.Contains(body, "transactions") {
				t.Fatalf("bounded=%v: unbounded retries or partial response: %d after limits %v %s", bounded, code, limits, body)
			}
			// An authorization change (team membership) is not retried.
			code, limits, body = read(start, func(call, _ int) {
				if call == 1 {
					if err := repo.SetTeamMembership(t.Context(), workspaceID, f.public, f.principals[2].user.ID, "member", false); err != nil {
						t.Fatal(err)
					}
					if err := repo.SetTeamMembership(t.Context(), workspaceID, f.public, f.principals[2].user.ID, "member", true); err != nil {
						t.Fatal(err)
					}
				}
			})
			if code != http.StatusServiceUnavailable || len(limits) != 1 || strings.Contains(body, "transactions") {
				t.Fatalf("bounded=%v: an authorization change was retried or delivered: %d after limits %v %s", bounded, code, limits, body)
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
		}
	}
}
