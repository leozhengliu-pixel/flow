package domain

import (
	"slices"
	"testing"
)

func TestResourceSlugs(t *testing.T) {
	if got := SlugWithSuffix("QA spike: resp export revamp", "0123456789ab"); got != "qa-spike-resp-export-revamp-0123456789ab" {
		t.Fatalf("SlugWithSuffix = %q", got)
	}
	if got := SlugWithSuffix("🚀", "0123456789ab"); got != "untitled-0123456789ab" {
		t.Fatalf("empty prefix = %q", got)
	}
	if suffix := NewSlugSuffix(); SlugSuffix("x-"+suffix) != suffix || NewSlugSuffix() == suffix {
		t.Fatalf("new suffix %q not a distinct 12-hex suffix", suffix)
	}
	for slug, want := range map[string]string{"compare-test-566151d57d0a": "566151d57d0a", "566151d57d0a": "566151d57d0a", "qa-spike": "", "imported-a1b2c3": "", "x-566151D57D0A": "566151d57d0a", "": ""} {
		if got := SlugSuffix(slug); got != want {
			t.Fatalf("SlugSuffix(%q) = %q, want %q", slug, got, want)
		}
	}

	// A rename keeps the suffix and remembers the old slug; renaming back drops it from previous.
	next, previous := RenamedSlug("compare-test-566151d57d0a", nil, "Compare v2")
	if next != "compare-v2-566151d57d0a" || !slices.Equal(previous, []string{"compare-test-566151d57d0a"}) {
		t.Fatalf("rename = %q %v", next, previous)
	}
	back, previous := RenamedSlug(next, previous, "Compare test")
	if back != "compare-test-566151d57d0a" || !slices.Equal(previous, []string{"compare-v2-566151d57d0a"}) {
		t.Fatalf("rename back = %q %v", back, previous)
	}
	// A legacy slug gets a suffix on rename.
	if next, previous := RenamedSlug("legacy", nil, "Legacy two"); SlugSuffix(next) == "" || !slices.Equal(previous, []string{"legacy"}) {
		t.Fatalf("legacy rename = %q %v", next, previous)
	}

	// StableSlug is deterministic and leaves suffixed slugs alone.
	if next, previous, changed := StableSlug("project_old", "Old project", "old-project", nil); !changed || next != "old-project-"+DerivedSlugSuffix("project_old") || !slices.Equal(previous, []string{"old-project"}) {
		t.Fatalf("StableSlug = %q %v %v", next, previous, changed)
	}
	if next, _, changed := StableSlug("p", "New", "new-566151d57d0a", nil); changed || next != "new-566151d57d0a" {
		t.Fatalf("suffixed slug changed: %q", next)
	}

	projects := []Project{
		{ID: "a", SlugID: "same-name-aaaaaaaaaaaa", PreviousSlugIDs: []string{"same-name"}},
		{ID: "b", SlugID: "same-name-bbbbbbbbbbbb", PreviousSlugIDs: []string{"same-name"}},
		{ID: "c", SlugID: "renamed-cccccccccccc", PreviousSlugIDs: []string{"original-cccccccccccc"}},
	}
	for ref, want := range map[string]int{
		"a": 0, "same-name-bbbbbbbbbbbb": 1, "stale-bbbbbbbbbbbb": 1, "bbbbbbbbbbbb": 1,
		"same-name": 0, "original-cccccccccccc": 2, "missing": -1, "missing-dddddddddddd": -1, "": -1,
	} {
		if got := ProjectIndexBySlugRef(projects, ref); got != want {
			t.Fatalf("ProjectIndexBySlugRef(%q) = %d, want %d", ref, got, want)
		}
	}
}
