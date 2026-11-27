package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleResidentFixture struct {
	policyDifferentialFixture
	workspace, plain, linked, project string
	tokens                            map[string]string
}

// scaleResidentSetup seeds two documents (one linked to a project, so its
// title edits rewrite the project's resource) and logs every principal in.
func scaleResidentSetup(t *testing.T, repo *store.SQLiteStore) scaleResidentFixture {
	t.Helper()
	f := setupPolicyDifferential(t, repo)
	r := scaleResidentFixture{policyDifferentialFixture: f, workspace: f.data.Workspace.URLKey, tokens: map[string]string{}}
	prefix := idgen.New("resident-")
	r.plain, r.linked, r.project = prefix+"plain", prefix+"linked", prefix+"project"
	if err := repo.MutateWorkspace(t.Context(), r.workspace, "scale_p21.resident_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents,
			domain.Document{ID: r.plain, Title: "Plain", Content: strings.Repeat("plain body ", 4000), Creator: data.Viewer, Version: 1, TeamIDs: []string{f.public},
				Revisions: []domain.DocumentRevision{{ID: prefix + "rev", Title: "Plain", Content: strings.Repeat("old body ", 3000), Author: data.Viewer}}},
			domain.Document{ID: r.linked, Title: "Linked", Content: "linked body", Creator: data.Viewer, Version: 1, TeamIDs: []string{f.public}, ProjectIDs: []string{r.project}})
		data.Projects = append(data.Projects, domain.Project{ID: r.project, Name: "Resident project", TeamIDs: []string{f.public}, Summary: "project",
			Resources: []domain.ProjectResource{{ID: r.linked, ProjectID: r.project, Type: "document", Title: "Linked", PinnedTeamIDs: []string{}}}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, r.workspace); err != nil {
		t.Fatal(err)
	}
	// A view captured from the flag-off copy does not know its stored
	// stamp; a replica's installed generation does after its load.
	if err := f.probe.ReloadWorkspace(t.Context(), r.workspace); err != nil {
		t.Fatal(err)
	}
	for _, principal := range f.principals {
		_, token, err := repo.Login(t.Context(), principal.user.Email, "p03-fixture-password")
		if err != nil {
			t.Fatal(err)
		}
		r.tokens[principal.user.ID] = token
	}
	return r
}

func (r scaleResidentFixture) edit(source *store.SQLiteStore, user domain.User, key *domain.APIKey, id string, body map[string]any) scaleDocRoutesOutcome {
	return scaleDocRoutesServe(r.policyDifferentialFixture, source, r.tokens[user.ID], user, key, http.MethodPatch, "/api/documents/"+id, body)
}

func scaleResidentStats() (made, verified int64, fallbacks map[string]int64) {
	stats := store.ResidentWriteStatsForTesting()
	fallbacks = map[string]int64{}
	for key, value := range stats {
		if reason, ok := strings.CutPrefix(key, "fallback:"); ok {
			fallbacks[reason] = value
		}
	}
	return stats["made"], stats["verified"], fallbacks
}

// TestScaleResidentDocumentWrite: with bounded metadata on, sequential
// edits of a document by one principal are made from the resident image
// once the installed generation is current, and every one of them is
// verified against the scoped image writer (callback input, previous values,
// realtime payload, stored rows and next generation identical; see
// resident_write_verify.go). Title, content and archive edits,
// edits that rewrite a linked project's resource, and API key edits are
// covered; the stored documents are the edits'.
func TestScaleResidentDocumentWrite(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	for _, repo := range scaleServerTestStores(t) {
		r := scaleResidentSetup(t, repo)
		admin, member := r.data.Viewer, r.principals[1].user
		key := &domain.APIKey{ID: idgen.New("resident-key-"), Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"}
		madeBefore, verifiedBefore, _ := scaleResidentStats()
		steps := []struct {
			user domain.User
			key  *domain.APIKey
			id   string
			body map[string]any
		}{
			{admin, nil, r.plain, map[string]any{"title": "Plain 1"}},
			{admin, nil, r.plain, map[string]any{"title": "Plain 2"}},
			{admin, nil, r.plain, map[string]any{"content": strings.Repeat("new body ", 4000)}},
			// Another principal: the stored viewer changes with each edit.
			{member, nil, r.plain, map[string]any{"title": "Plain by member"}},
			{admin, nil, r.plain, map[string]any{"title": "Plain 3", "icon": "book"}},
			{member, nil, r.linked, map[string]any{"title": "Linked by member"}},
			{admin, nil, r.linked, map[string]any{"title": "Linked 1"}},
			{admin, nil, r.linked, map[string]any{"title": "Linked 2"}},
			{admin, nil, r.plain, map[string]any{"archived": true}},
			{admin, nil, r.plain, map[string]any{"archived": false}},
			{admin, key, r.plain, map[string]any{"title": "Plain by key"}},
			{admin, key, r.plain, map[string]any{"title": "Plain by key again"}},
			{admin, nil, r.plain, map[string]any{"title": "Plain 4", "expectedVersion": 1}},
		}
		for index, step := range steps {
			outcome := r.edit(r.probe, step.user, step.key, step.id, step.body)
			want := http.StatusOK
			if _, conflict := step.body["expectedVersion"]; conflict {
				want = http.StatusConflict
			}
			if outcome.code != want {
				t.Fatalf("step %d %v = %d %s, want %d", index, step.body, outcome.code, outcome.body, want)
			}
		}
		made, verified, fallbacks := scaleResidentStats()
		t.Logf("resident writes made=%d verified=%d fallbacks=%v images=%v decode=%v", made-madeBefore, verified-verifiedBefore, fallbacks,
			store.ResidentImageStatsForTesting(), store.ColdDecodeCacheStatsForTesting())
		// Every successful edit is resident (the conflict makes none).
		if made-madeBefore < int64(len(steps))-1 || verified-verifiedBefore < made-madeBefore {
			t.Fatalf("resident writes made=%d verified=%d of %d edits (fallbacks %v)", made-madeBefore, verified-verifiedBefore, len(steps), fallbacks)
		}
		plain := mustDocument(t, repo, r.workspace, r.plain)
		if plain.Title != "Plain by key again" || plain.ArchivedAt != nil || plain.Icon != "book" || !strings.HasPrefix(plain.Content, "new body") {
			t.Fatalf("plain document after the edits: title %q archived %v icon %q", plain.Title, plain.ArchivedAt, plain.Icon)
		}
		if linked := mustDocument(t, repo, r.workspace, r.linked); linked.Title != "Linked 2" {
			t.Fatalf("linked document title %q", linked.Title)
		}
		project := mustProject(t, repo, r.workspace, r.project)
		if len(project.Resources) != 1 || project.Resources[0].Title != "Linked 2" {
			t.Fatalf("linked project resources %+v", project.Resources)
		}
		if cached, err := r.probe.ResidentImageCheckForTesting(t.Context(), r.workspace); err != nil {
			t.Fatalf("resident image after the edits (cached=%v): %v", cached, err)
		}
	}
}

// TestScaleResidentDocumentWriteStale: a write another server committed
// after the generation was installed (before the edit, or between the edit's
// preparation and its commit) sends the edit to the scoped writer, which
// makes it on the current state: both writes are kept.
func TestScaleResidentDocumentWriteStale(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	for _, repo := range scaleServerTestStores(t) {
		r := scaleResidentSetup(t, repo)
		admin := r.data.Viewer
		for _, title := range []string{"Warm 1", "Warm 2"} {
			if outcome := r.edit(r.probe, admin, nil, r.plain, map[string]any{"title": title}); outcome.code != http.StatusOK {
				t.Fatalf("warm edit = %d %s", outcome.code, outcome.body)
			}
		}
		// Another server edits the linked document; this one's generation
		// is now behind the stored stamp.
		if outcome := r.edit(repo, admin, nil, r.linked, map[string]any{"title": "Linked elsewhere"}); outcome.code != http.StatusOK {
			t.Fatalf("other server edit = %d %s", outcome.code, outcome.body)
		}
		_, _, before := scaleResidentStats()
		if outcome := r.edit(r.probe, admin, nil, r.plain, map[string]any{"title": "After a stale generation"}); outcome.code != http.StatusOK {
			t.Fatalf("edit on a stale generation = %d %s", outcome.code, outcome.body)
		}
		_, _, after := scaleResidentStats()
		if after["generation behind the stored stamp"] <= before["generation behind the stored stamp"] {
			t.Fatalf("the edit on a stale generation did not fall back: %v", after)
		}
		// The other server commits between this edit's preparation and its
		// commit.
		for _, title := range []string{"Warm 3", "Warm 4"} {
			if outcome := r.edit(r.probe, admin, nil, r.plain, map[string]any{"title": title}); outcome.code != http.StatusOK {
				t.Fatalf("warm edit = %d %s", outcome.code, outcome.body)
			}
		}
		raced := false
		restore := store.SetResidentWriteBeforeCommitForTesting(func() {
			if raced {
				return
			}
			raced = true
			if outcome := r.edit(repo, admin, nil, r.linked, map[string]any{"title": "Linked in the window"}); outcome.code != http.StatusOK {
				t.Errorf("other server edit in the window = %d %s", outcome.code, outcome.body)
			}
		})
		_, _, before = scaleResidentStats()
		outcome := r.edit(r.probe, admin, nil, r.plain, map[string]any{"title": "After the window"})
		restore()
		if outcome.code != http.StatusOK {
			t.Fatalf("edit raced by another server = %d %s", outcome.code, outcome.body)
		}
		_, _, after = scaleResidentStats()
		if !raced || after["stored stamp moved"] <= before["stored stamp moved"] {
			t.Fatalf("the raced edit did not fall back at its commit (raced=%v): %v", raced, after)
		}
		if plain := mustDocument(t, repo, r.workspace, r.plain); plain.Title != "After the window" {
			t.Fatalf("plain title %q", plain.Title)
		}
		if linked := mustDocument(t, repo, r.workspace, r.linked); linked.Title != "Linked in the window" {
			t.Fatalf("linked title %q", linked.Title)
		}
		// The next edit sees both writes.
		if outcome := r.edit(r.probe, admin, nil, r.linked, map[string]any{"title": "Linked last"}); outcome.code != http.StatusOK {
			t.Fatalf("edit after the race = %d %s", outcome.code, outcome.body)
		}
		if project := mustProject(t, repo, r.workspace, r.project); len(project.Resources) != 1 || project.Resources[0].Title != "Linked last" {
			t.Fatalf("project resources %+v", project.Resources)
		}
	}
}

// TestScaleResidentDocumentWriteConcurrent: concurrent edits of two
// documents through one replica all land: every edit answers 200, each
// document's version counts its edits, and the installed generation equals
// a fresh decode. Run with -race.
func TestScaleResidentDocumentWriteConcurrent(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	for _, repo := range scaleServerTestStores(t) {
		r := scaleResidentSetup(t, repo)
		admin := r.data.Viewer
		if outcome := r.edit(r.probe, admin, nil, r.plain, map[string]any{"title": "Warm"}); outcome.code != http.StatusOK {
			t.Fatalf("warm edit = %d %s", outcome.code, outcome.body)
		}
		start := map[string]int64{r.plain: mustDocument(t, repo, r.workspace, r.plain).Version, r.linked: mustDocument(t, repo, r.workspace, r.linked).Version}
		const workers, edits = 4, 4
		var wait sync.WaitGroup
		failures := make(chan string, workers*edits)
		for worker := range workers {
			wait.Add(1)
			go func() {
				defer wait.Done()
				id := r.plain
				if worker%2 == 1 {
					id = r.linked
				}
				for edit := range edits {
					if outcome := r.edit(r.probe, admin, nil, id, map[string]any{"title": fmt.Sprintf("worker %d edit %d", worker, edit)}); outcome.code != http.StatusOK {
						failures <- fmt.Sprintf("worker %d edit %d = %d %s", worker, edit, outcome.code, outcome.body)
					}
				}
			}()
		}
		wait.Wait()
		close(failures)
		for failure := range failures {
			t.Error(failure)
		}
		for id, version := range start {
			if got := mustDocument(t, repo, r.workspace, id).Version; got != version+workers/2*edits {
				t.Errorf("document %s version %d after %d edits from %d", id, got, workers/2*edits, version)
			}
		}
		if cached, err := r.probe.ResidentImageCheckForTesting(t.Context(), r.workspace); err != nil {
			t.Fatalf("resident image after concurrent edits (cached=%v): %v", cached, err)
		}
	}
}
