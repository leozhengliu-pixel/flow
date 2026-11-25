package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// TestScaleP21ResidentBootstrapImage serves the paged bootstrap with the flag
// on from the resident decoded image of the installed generation and checks:
// the bytes equal flag-off's for every principal shape and projection; the
// image is built once and then shared; serving never writes into it (it
// stays equal to a fresh decode of its generation); a read that pinned a
// generation keeps it after a write installs the next one; and a write's
// generation is served at once (the image follows it).
func TestScaleP21ResidentBootstrapImage(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP21BootstrapHistorySeed(t, repo)
			workspace := f.data.Workspace.URLKey
			// The projects and documents contain upload links. Add an
			// independent, cacheable field to exercise hits on the route.
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.bootstrap_encoding_seed", "", nil, func(data *domain.Bootstrap) error {
				data.Initiatives = []domain.Initiative{{ID: "resident-initiative", Name: "Resident initiative", LeadTeamID: f.public}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace, f.other)
			if err != nil {
				t.Fatal(err)
			}
			cases := []scaleP21SW145Case{
				{name: "admin", user: f.principals[0].user},
				{name: "member", user: f.principals[2].user},
				{name: "guest", user: f.principals[3].user},
				{name: "selected-key", user: f.principals[4].user, key: &domain.APIKey{ID: "resident-selected", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
				{name: "development", user: f.data.Viewer, development: true},
			}
			encodings := newBootstrapEncodingRenderer(store.BoundedBootstrapEncodingBytes())
			serve := func(t *testing.T, test scaleP21SW145Case) scaleP21BootstrapHistoryResponse {
				return scaleP21BootstrapHistoryServeWith(t, f, &server{store: view, authDisabled: test.development, bootstrapEncodings: encodings}, test)
			}
			// reference answers a case as the comparison expects it: flag-off
			// first; after bounded writes (which flag-off sees only through a
			// reload that normalizes more than the write), the same flag-on
			// read through the spool, which the route differentials pin to
			// flag-off.
			legacy := func(test scaleP21SW145Case) scaleP21BootstrapHistoryResponse {
				return scaleP21BootstrapHistoryServe(t, f, repo, test)
			}
			spool := func(test scaleP21SW145Case) scaleP21BootstrapHistoryResponse {
				t.Setenv("FLOW_COLD_DECODE_CACHE_BYTES", "0")
				defer t.Setenv("FLOW_COLD_DECODE_CACHE_BYTES", "")
				return scaleP21BootstrapHistoryServe(t, f, view, test)
			}
			compare := func(t *testing.T, label string, reference func(scaleP21SW145Case) scaleP21BootstrapHistoryResponse) {
				t.Helper()
				for _, test := range cases {
					for _, projection := range []string{"", "project-list"} {
						test.workspace, test.projection = workspace, projection
						bounded := serve(t, test)
						want := reference(test)
						if want.Status != http.StatusOK || bounded.Status != want.Status || bounded.ContentType != want.ContentType {
							t.Fatalf("%s %s/%s: resident read %d, reference %d: %.200s", label, test.name, projection, bounded.Status, want.Status, bounded.Body)
						}
						if bounded.Body != want.Body {
							t.Fatalf("%s %s/%s: resident bootstrap body differs: %d/%d bytes in %s", label, test.name, projection, len(bounded.Body), len(want.Body), scaleP21ResidentDiff(bounded.Body, want.Body))
						}
						if cached := serve(t, test); cached != bounded {
							t.Fatalf("%s %s/%s: cached bootstrap body or status differs", label, test.name, projection)
						}
						if view.MetadataWorkUsedForTesting() != 0 {
							t.Fatal("bounded bootstrap retained work", view.MetadataWorkUsedForTesting())
						}
					}
				}
			}
			before := store.ResidentImageStatsForTesting()
			encodedHits := bootstrapEncodingHits.Load()
			compare(t, "first", legacy)
			if bootstrapEncodingHits.Load() <= encodedHits {
				t.Fatal("repeat bootstraps did not reuse their encoding")
			}
			after := store.ResidentImageStatsForTesting()
			if builds := after["builds"] - before["builds"]; builds != 1 {
				t.Fatal("the resident image was not built exactly once for one generation", builds)
			}
			if after["hits"] <= before["hits"] {
				t.Fatal("later bootstraps did not share the resident image", after)
			}
			if cached, err := view.ResidentImageCheckForTesting(t.Context(), workspace); err != nil || !cached {
				t.Fatal("serving changed the shared image or dropped it", cached, err)
			}

			// A read that pinned a generation keeps it across a write.
			pinned, err := view.BeginIssueBootstrapRead(store.WithResidentBootstrap(t.Context()), workspace, f.principals[0].user.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !pinned.Resident() {
				t.Fatal("the read did not come from the resident image")
			}
			renamed := scaleP21ProjectUpdateServe(f.scaleP21M2Fixture, view, "", f.principals[0].user,
				&domain.APIKey{ID: "resident-writer", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"},
				"history-project-001", map[string]any{"name": "Renamed resident project"})
			if renamed.Code != http.StatusOK {
				t.Fatal("project rename failed", renamed.Code, renamed.Body.String())
			}
			for _, item := range pinned.Data.Projects {
				if item.Name == "Renamed resident project" {
					t.Fatal("a read holding a generation saw a later write")
				}
			}
			pinned.Close()
			built := store.ResidentImageStatsForTesting()["builds"]
			compare(t, "after write", spool)
			if store.ResidentImageStatsForTesting()["builds"] != built+1 {
				t.Fatal("the written generation was not built once")
			}
			image, err := view.ResidentImageForTesting(t.Context(), workspace)
			if err != nil || image == nil {
				t.Fatal("no image of the written generation", err)
			}
			if !strings.Contains(fmt.Sprint(projectNames(image.Projects)), "Renamed resident project") {
				t.Fatal("the image of the written generation lacks the write")
			}
			if cached, err := view.ResidentImageCheckForTesting(t.Context(), workspace); err != nil || !cached {
				t.Fatal("the written generation's image differs from a fresh decode", cached, err)
			}

			// Readers and writers together (run with -race): every read is
			// complete and the shared image stays exact.
			var wait sync.WaitGroup
			errs := make(chan error, 64)
			for reader := range 6 {
				wait.Add(1)
				go func() {
					defer wait.Done()
					test := cases[reader%len(cases)]
					test.workspace = workspace
					for range 3 {
						response := serve(t, test)
						if response.Status != http.StatusOK && response.Status != http.StatusServiceUnavailable {
							errs <- fmt.Errorf("concurrent bootstrap answered %d: %.200s", response.Status, response.Body)
						}
					}
				}()
			}
			wait.Add(1)
			go func() {
				defer wait.Done()
				for round := range 4 {
					response := scaleP21ProjectUpdateServe(f.scaleP21M2Fixture, view, "", f.principals[0].user,
						&domain.APIKey{ID: "resident-writer", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"},
						"history-project-002", map[string]any{"name": fmt.Sprintf("Concurrent rename %d", round)})
					if response.Code != http.StatusOK {
						errs <- fmt.Errorf("concurrent rename answered %d: %.200s", response.Code, response.Body.String())
					}
				}
			}()
			wait.Wait()
			close(errs)
			for err := range errs {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			compare(t, "after concurrent writes", spool)
			if cached, err := view.ResidentImageCheckForTesting(ctx, workspace); err != nil || !cached {
				t.Fatal("the image after concurrent writes differs from a fresh decode", cached, err)
			}
		})
	}
}

func projectNames(projects []domain.Project) []string {
	names := make([]string, len(projects))
	for index, project := range projects {
		names[index] = project.Name
	}
	return names
}

// scaleP21ResidentDiff names the top-level fields two bootstrap bodies
// disagree on, with the start of each side.
func scaleP21ResidentDiff(a, b string) string {
	var left, right map[string]json.RawMessage
	if json.Unmarshal([]byte(a), &left) != nil || json.Unmarshal([]byte(b), &right) != nil {
		return "unparsable bodies"
	}
	var fields []string
	for key, value := range left {
		if other := right[key]; string(other) != string(value) {
			at := 0
			for at < len(value) && at < len(other) && value[at] == other[at] {
				at++
			}
			fields = append(fields, fmt.Sprintf("%s at %d: %.160s | %.160s", key, at, value[at:], other[min(at, len(other)):]))
		}
	}
	for key := range right {
		if _, found := left[key]; !found {
			fields = append(fields, key+" missing")
		}
	}
	return strings.Join(fields, "; ")
}
