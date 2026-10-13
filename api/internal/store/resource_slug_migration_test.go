package store

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// The slug migration gives name-only project and initiative slugs a stable
// suffix (distinct for records that shared a name), keeps the old slug
// resolvable, leaves suffixed slugs alone and is idempotent.
func TestResourceSlugMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "flow.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	project := func(id, name, slugID string) domain.Project {
		return domain.Project{ID: id, Name: name, SlugID: slugID, TeamIDs: []string{}, MemberIDs: []string{}, CreatedAt: at, UpdatedAt: at}
	}
	initiative := func(id, name, slugID string) domain.Initiative {
		return domain.Initiative{ID: id, Name: name, SlugID: slugID, CreatedAt: at, UpdatedAt: at}
	}
	if err := repo.MutateWorkspace(ctx, accessWorkspace, "test.seeded", "workspace", nil, func(next *domain.Bootstrap) error {
		next.Projects = append(next.Projects,
			project("project_dup_a", "QA spike: resp export revamp", "qa-spike-resp-export-revamp"),
			project("project_dup_b", "QA spike: resp export revamp", "qa-spike-resp-export-revamp"),
			project("project_kept", "Kept", "kept-0123456789ab"),
			project("project_clash", "Clash", "clash-0123456789ab"),
			project("project_import", "Imported", "imported-a1b2c3"),
		)
		next.Initiatives = append(next.Initiatives,
			initiative("initiative_dup_a", "Roadmap", "roadmap"),
			initiative("initiative_dup_b", "Roadmap", "roadmap"),
			initiative("initiative_kept", "Kept", "kept-fedcba987654"),
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=?`, resourceSlugMigrationVersion); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}

	snapshot := func(repo *SQLiteStore) ([]domain.Project, []domain.Initiative) {
		t.Helper()
		data, ok := repo.storedWorkspace(accessWorkspace)
		if !ok {
			t.Fatal("workspace missing")
		}
		return data.Projects, data.Initiatives
	}
	check := func(stage string, repo *SQLiteStore) ([]domain.Project, []domain.Initiative) {
		t.Helper()
		projects, initiatives := snapshot(repo)
		suffixes := map[string]string{}
		for _, item := range projects {
			suffix := domain.SlugSuffix(item.SlugID)
			if suffix == "" {
				t.Fatalf("%s: project %s slug %q has no stable suffix", stage, item.ID, item.SlugID)
			}
			if other, dup := suffixes[suffix]; dup {
				t.Fatalf("%s: projects %s and %s share suffix %s", stage, other, item.ID, suffix)
			}
			suffixes[suffix] = item.ID
			if !item.UpdatedAt.IsZero() && item.ID != "project_aut" && item.ID != "project_cruise" && !item.UpdatedAt.Equal(at) {
				t.Fatalf("%s: project %s updatedAt moved to %v", stage, item.ID, item.UpdatedAt)
			}
		}
		for _, item := range initiatives {
			if domain.SlugSuffix(item.SlugID) == "" {
				t.Fatalf("%s: initiative %s slug %q has no stable suffix", stage, item.ID, item.SlugID)
			}
		}
		find := func(id string) domain.Project {
			index := slices.IndexFunc(projects, func(item domain.Project) bool { return item.ID == id })
			if index < 0 {
				t.Fatalf("%s: project %s missing", stage, id)
			}
			return projects[index]
		}
		a, b := find("project_dup_a"), find("project_dup_b")
		if a.SlugID == b.SlugID || !slices.Contains(a.PreviousSlugIDs, "qa-spike-resp-export-revamp") || !slices.Contains(b.PreviousSlugIDs, "qa-spike-resp-export-revamp") {
			t.Fatalf("%s: duplicate-name projects = %q %v / %q %v", stage, a.SlugID, a.PreviousSlugIDs, b.SlugID, b.PreviousSlugIDs)
		}
		if want := "qa-spike-resp-export-revamp-" + domain.DerivedSlugSuffix("project_dup_a"); a.SlugID != want {
			t.Fatalf("%s: slug = %q, want %q", stage, a.SlugID, want)
		}
		// The legacy link keeps naming the record it opened before (the first).
		if index := domain.ProjectIndexBySlugRef(projects, "qa-spike-resp-export-revamp"); index < 0 || projects[index].ID != a.ID {
			t.Fatalf("%s: legacy slug resolves to %d", stage, index)
		}
		// Each new URL names its own project.
		if index := domain.ProjectIndexBySlugRef(projects, b.SlugID); projects[index].ID != b.ID {
			t.Fatalf("%s: %q resolves to %s", stage, b.SlugID, projects[index].ID)
		}
		if kept := find("project_kept"); kept.SlugID != "kept-0123456789ab" || len(kept.PreviousSlugIDs) != 0 {
			t.Fatalf("%s: suffixed slug changed: %q %v", stage, kept.SlugID, kept.PreviousSlugIDs)
		}
		if clash := find("project_clash"); clash.SlugID == "clash-0123456789ab" || !slices.Contains(clash.PreviousSlugIDs, "clash-0123456789ab") {
			t.Fatalf("%s: clashing suffix kept: %q %v", stage, clash.SlugID, clash.PreviousSlugIDs)
		}
		if imported := find("project_import"); !slices.Contains(imported.PreviousSlugIDs, "imported-a1b2c3") {
			t.Fatalf("%s: short-suffix import slug not migrated: %q %v", stage, imported.SlugID, imported.PreviousSlugIDs)
		}
		ia := initiatives[slices.IndexFunc(initiatives, func(item domain.Initiative) bool { return item.ID == "initiative_dup_a" })]
		ib := initiatives[slices.IndexFunc(initiatives, func(item domain.Initiative) bool { return item.ID == "initiative_dup_b" })]
		ik := initiatives[slices.IndexFunc(initiatives, func(item domain.Initiative) bool { return item.ID == "initiative_kept" })]
		if ia.SlugID == ib.SlugID || !slices.Contains(ib.PreviousSlugIDs, "roadmap") || ik.SlugID != "kept-fedcba987654" {
			t.Fatalf("%s: initiatives = %q %q %q", stage, ia.SlugID, ib.SlugID, ik.SlugID)
		}
		if index := domain.InitiativeIndexBySlugRef(initiatives, "roadmap"); initiatives[index].ID != ia.ID {
			t.Fatalf("%s: legacy initiative slug resolves to %s", stage, initiatives[index].ID)
		}
		return projects, initiatives
	}

	repo, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	migratedProjects, migratedInitiatives := check("migrated", repo)
	if applied, err := repo.migrationApplied(ctx, resourceSlugMigrationVersion); err != nil || !applied {
		t.Fatalf("migration not recorded: %v %v", applied, err)
	}
	// Running it again changes nothing.
	if err := repo.migrateResourceSlugs(ctx); err != nil {
		t.Fatal(err)
	}
	again, againInitiatives := snapshot(repo)
	for index := range again {
		if again[index].SlugID != migratedProjects[index].SlugID || !slices.Equal(again[index].PreviousSlugIDs, migratedProjects[index].PreviousSlugIDs) {
			t.Fatalf("rerun changed %s: %q -> %q", again[index].ID, migratedProjects[index].SlugID, again[index].SlugID)
		}
	}
	for index := range againInitiatives {
		if againInitiatives[index].SlugID != migratedInitiatives[index].SlugID {
			t.Fatalf("rerun changed %s", againInitiatives[index].ID)
		}
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	// The result is stored: a reopen reads the same slugs.
	repo, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	reopened, _ := check("reopened", repo)
	for index := range reopened {
		if reopened[index].SlugID != migratedProjects[index].SlugID {
			t.Fatalf("reopen changed %s: %q -> %q", reopened[index].ID, migratedProjects[index].SlugID, reopened[index].SlugID)
		}
	}
}
