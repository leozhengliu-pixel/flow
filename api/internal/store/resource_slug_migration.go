package store

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"

	"flow/api/internal/domain"
)

// resourceSlugMigrationVersion records the one-time move of project and
// initiative slugs to `<name-slug>-<12 hex>`.
const resourceSlugMigrationVersion = 1003

// migrateResourceSlugs gives every project and initiative whose slug is still
// name-only (so two records with one name shared a URL, and a rename broke old
// links) a stable 12-hex suffix, as new records get on create. The old slug is
// kept in previousSlugIds, so links made with it keep resolving (the web app
// then redirects to the new slug). The suffix is derived from the record id,
// so concurrent instances write the same value, and records that already have
// a unique suffix are left alone, which makes the migration idempotent.
func (s *SQLiteStore) migrateResourceSlugs(ctx context.Context) error {
	projects, initiatives := 0, 0
	for _, key := range s.WorkspaceKeys() {
		p, i, err := s.migrateWorkspaceResourceSlugs(ctx, key)
		if err != nil {
			return fmt.Errorf("workspace %q: %w", key, err)
		}
		projects += p
		initiatives += i
	}
	if projects+initiatives > 0 {
		slog.Info("gave project and initiative slugs a stable suffix", "projects", projects, "initiatives", initiatives)
	}
	return nil
}

func (s *SQLiteStore) migrateWorkspaceResourceSlugs(ctx context.Context, key string) (int, int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	before, ok := s.storedWorkspace(key)
	if !ok {
		return 0, 0, nil
	}
	after := before
	projects := stableResourceSlugs(before.Projects, domain.ProjectSlugKey, func(project domain.Project) string { return project.Name })
	initiatives := stableResourceSlugs(before.Initiatives, domain.InitiativeSlugKey, func(initiative domain.Initiative) string { return initiative.Name })
	if len(projects) > 0 {
		after.Projects = slices.Clone(before.Projects)
		for index, slug := range projects {
			after.Projects[index].SlugID, after.Projects[index].PreviousSlugIDs = slug.next, slug.previous
		}
	}
	if len(initiatives) > 0 {
		after.Initiatives = slices.Clone(before.Initiatives)
		for index, slug := range initiatives {
			after.Initiatives[index].SlugID, after.Initiatives[index].PreviousSlugIDs = slug.next, slug.previous
		}
	}
	if len(projects)+len(initiatives) == 0 {
		return 0, 0, nil
	}
	// Only the metadata records are written (UpdatedAt stays: a slug move
	// is not an edit).
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	if err := s.persistMetadataTx(ctx, tx, key, before, after); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	s.installWorkspace(key, after, false)
	s.dropMetadataCache(ctx, key)
	s.mu.Lock()
	if !slices.Contains(s.startupCacheDrops, key) {
		s.startupCacheDrops = append(s.startupCacheDrops, key)
	}
	s.mu.Unlock()
	return len(projects), len(initiatives), nil
}

type migratedSlug struct {
	next     string
	previous []string
}

// stableResourceSlugs returns, by index, the records whose slug needs a
// stable suffix: a name-only slug, or a suffix another record already uses.
func stableResourceSlugs[T any](items []T, key func(T) (string, string, []string), name func(T) string) map[int]migratedSlug {
	used := map[string]bool{}
	for _, item := range items {
		_, slugID, _ := key(item)
		if suffix := domain.SlugSuffix(slugID); suffix != "" {
			used[suffix] = true
		}
	}
	seen := map[string]bool{}
	changed := map[int]migratedSlug{}
	for index, item := range items {
		id, slugID, previous := key(item)
		suffix := domain.SlugSuffix(slugID)
		if suffix != "" && !seen[suffix] {
			seen[suffix] = true
			continue
		}
		next := domain.DerivedSlugSuffix(id)
		for attempt := 1; used[next] || seen[next]; attempt++ {
			next = domain.DerivedSlugSuffix(id + "#" + strconv.Itoa(attempt))
		}
		seen[next], used[next] = true, true
		slug := domain.SlugWithSuffix(name(item), next)
		changed[index] = migratedSlug{next: slug, previous: domain.RememberSlug(previous, slugID, slug)}
	}
	return changed
}
