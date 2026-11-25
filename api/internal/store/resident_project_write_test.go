package store

import (
	"slices"
	"testing"

	"flow/api/internal/domain"
)

func TestResidentScopedProjectPreparation(t *testing.T) {
	_, view, workspace := coldIncrementalFixture(t)
	const id = "ci-project-05"
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "projects", ID: id}}}
	ctx := WithScopedSelectionWrite(t.Context())
	var seen []string
	var outsideLock []bool
	edit := func(next *domain.Bootstrap) error {
		outside := view.writeMu.TryLock()
		if outside {
			view.writeMu.Unlock()
		}
		outsideLock = append(outsideLock, outside)
		seen = append(seen, next.Projects[0].Description)
		next.Projects[0].Name = "Prepared project"
		return nil
	}
	if err := view.MutateWorkspaceScoped(ctx, workspace, "project.updated", id, nil, scope, edit); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(outsideLock, []bool{true}) {
		t.Fatalf("project did not use outside-lock preparation: %v", outsideLock)
	}
	seen, outsideLock = nil, nil
	other := MetadataRecordScope{Fields: []string{"labels"}}
	restoreOther := SetResidentWriteBeforeCommitForTesting(func() {
		if err := view.MutateWorkspaceScoped(ctx, workspace, "label.updated", "", nil, other, func(next *domain.Bootstrap) error {
			next.Labels[0].Name = "Updated label"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Cleanup(restoreOther)
	if err := view.MutateWorkspaceScoped(ctx, workspace, "project.updated", id, nil, scope, edit); err != nil {
		t.Fatal(err)
	}
	restoreOther()
	if !slices.Equal(outsideLock, []bool{true}) {
		t.Fatalf("unrelated label edit invalidated project preparation: %v", outsideLock)
	}
	seen, outsideLock = nil, nil
	restore := SetResidentWriteBeforeCommitForTesting(func() {
		if err := view.MutateWorkspaceScoped(ctx, workspace, "project.test_peer", id, nil, scope, func(next *domain.Bootstrap) error {
			next.Projects[0].Description = "Newer description"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	defer restore()
	if err := view.MutateWorkspaceScoped(ctx, workspace, "project.updated", id, nil, scope, edit); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(outsideLock, []bool{true, false}) || !slices.Equal(seen, []string{"R&D <notes>", "Newer description"}) {
		t.Fatalf("stale preparation was used: outside lock=%v descriptions=%v", outsideLock, seen)
	}
	if used := view.MetadataWorkUsedForTesting(); used != 0 {
		t.Fatalf("project edit retained %d work bytes", used)
	}
}
