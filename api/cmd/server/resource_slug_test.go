package main

import (
	"context"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

var stableSlugPattern = regexp.MustCompile(`^[\p{L}\p{N}-]+-[0-9a-f]{12}$`)

// Projects and initiatives get `<name>-<12 hex>` slugs: same-name records get
// distinct URLs, a rename keeps the suffix (old links resolve), and stale or
// legacy slugs still find the record.
func TestProjectAndInitiativeSlugsAreUniqueAndRenameStable(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	first := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "QA spike: resp export revamp"}, http.StatusCreated)
	second := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "QA spike: resp export revamp"}, http.StatusCreated)
	for _, project := range []domain.Project{first, second} {
		if !strings.HasPrefix(project.SlugID, "qa-spike-resp-export-revamp-") || !stableSlugPattern.MatchString(project.SlugID) {
			t.Fatalf("slug = %q, want qa-spike-resp-export-revamp-<12 hex>", project.SlugID)
		}
	}
	if first.SlugID == second.SlugID {
		t.Fatalf("same-name projects share slug %q", first.SlugID)
	}
	for _, project := range []domain.Project{first, second} {
		if got := requestJSON[domain.Project](t, handler, http.MethodGet, "/api/projects/"+project.SlugID, nil, http.StatusOK); got.ID != project.ID {
			t.Fatalf("GET %s = %s, want %s", project.SlugID, got.ID, project.ID)
		}
	}

	// Rename: the prefix follows the name, the suffix stays.
	renamed := requestJSON[domain.Project](t, handler, http.MethodPatch, "/api/projects/"+first.ID, map[string]any{"name": "Export revamp v2"}, http.StatusOK)
	suffix := domain.SlugSuffix(first.SlugID)
	if renamed.SlugID != "export-revamp-v2-"+suffix || !slices.Contains(renamed.PreviousSlugIDs, first.SlugID) {
		t.Fatalf("renamed slug = %q previous %v, want export-revamp-v2-%s with %q kept", renamed.SlugID, renamed.PreviousSlugIDs, suffix, first.SlugID)
	}
	// A save that keeps the name changes nothing.
	same := requestJSON[domain.Project](t, handler, http.MethodPatch, "/api/projects/"+first.ID, map[string]any{"name": "Export revamp v2"}, http.StatusOK)
	if same.SlugID != renamed.SlugID || !slices.Equal(same.PreviousSlugIDs, renamed.PreviousSlugIDs) {
		t.Fatalf("unchanged name moved slug: %q %v", same.SlugID, same.PreviousSlugIDs)
	}
	for _, ref := range []string{first.SlugID, "anything-" + suffix, suffix, renamed.SlugID, first.ID} {
		if got := requestJSON[domain.Project](t, handler, http.MethodGet, "/api/projects/"+ref, nil, http.StatusOK); got.ID != first.ID {
			t.Fatalf("GET %s = %s, want %s", ref, got.ID, first.ID)
		}
	}
	requestJSON[map[string]any](t, handler, http.MethodGet, "/api/projects/qa-spike-resp-export-revamp", nil, http.StatusNotFound)

	// Initiatives follow the same rules.
	roadmapA := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{"name": "Roadmap"}, http.StatusCreated)
	roadmapB := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{"name": "Roadmap"}, http.StatusCreated)
	if roadmapA.SlugID == roadmapB.SlugID || !stableSlugPattern.MatchString(roadmapA.SlugID) || !strings.HasPrefix(roadmapB.SlugID, "roadmap-") {
		t.Fatalf("initiative slugs = %q %q", roadmapA.SlugID, roadmapB.SlugID)
	}
	renamedInitiative := requestJSON[domain.Initiative](t, handler, http.MethodPatch, "/api/initiatives/"+roadmapB.ID, map[string]any{"name": "2027 Roadmap"}, http.StatusOK)
	if renamedInitiative.SlugID != "2027-roadmap-"+domain.SlugSuffix(roadmapB.SlugID) || !slices.Contains(renamedInitiative.PreviousSlugIDs, roadmapB.SlugID) {
		t.Fatalf("renamed initiative slug = %q %v", renamedInitiative.SlugID, renamedInitiative.PreviousSlugIDs)
	}
	if got := requestJSON[domain.Initiative](t, handler, http.MethodGet, "/api/initiatives/"+roadmapB.SlugID, nil, http.StatusOK); got.ID != roadmapB.ID {
		t.Fatalf("GET old initiative slug = %s, want %s", got.ID, roadmapB.ID)
	}

	// A legacy name-only slug (kept by the migration) still resolves.
	if err := repository.MutateWorkspace(context.Background(), "test-workspace", "test.seeded", second.ID, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Projects, func(item domain.Project) bool { return item.ID == second.ID })
		data.Projects[index].PreviousSlugIDs = []string{"legacy-export"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := requestJSON[domain.Project](t, handler, http.MethodGet, "/api/projects/legacy-export", nil, http.StatusOK); got.ID != second.ID {
		t.Fatalf("legacy slug = %s, want %s", got.ID, second.ID)
	}

	data := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	// MCP finders take a stale slug, a legacy slug or a full project URL.
	for query, want := range map[string]string{
		"stale-prefix-" + suffix: first.ID,
		"legacy-export":          second.ID,
		"https://flow.example/test-workspace/project/old-name-" + suffix + "/overview": first.ID,
	} {
		if found, err := mcpFindProject(data, query); err != nil || found.ID != want {
			t.Fatalf("mcpFindProject(%q) = %s %v, want %s", query, found.ID, err, want)
		}
	}
	if found, err := mcpFindInitiative(data, roadmapB.SlugID); err != nil || found.ID != roadmapB.ID {
		t.Fatalf("mcpFindInitiative(old slug) = %s %v", found.ID, err)
	}
	// A pasted pre-rename link becomes a mention of the project, linking to its current URL.
	resolver := newMentionResolver(&data, nil)
	attrs, ok := resolver.urlTarget("/test-workspace/project/"+first.SlugID+"/overview", "", true)
	if !ok || attrs.ID != first.ID || !strings.Contains(attrs.Href, "/project/"+renamed.SlugID+"/") {
		t.Fatalf("mention of old project link = %#v %v", attrs, ok)
	}
	attrs, ok = resolver.urlTarget("/test-workspace/initiative/"+roadmapB.SlugID+"/overview", "", true)
	if !ok || attrs.ID != roadmapB.ID || !strings.Contains(attrs.Href, "/initiative/"+renamedInitiative.SlugID+"/") {
		t.Fatalf("mention of old initiative link = %#v %v", attrs, ok)
	}
}
