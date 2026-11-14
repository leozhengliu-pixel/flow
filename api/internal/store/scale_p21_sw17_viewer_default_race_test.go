package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

type sw17ViewerDefaultHookContext struct {
	context.Context
	once sync.Once
	hook func()
}

func (c *sw17ViewerDefaultHookContext) Err() error {
	c.once.Do(c.hook)
	return c.Context.Err()
}

func sw17ViewerDefaultSwitchStore() *SQLiteStore {
	return &SQLiteStore{
		lastWorkspaceKey: "old\x00physical",
		workspaces: map[string]domain.Bootstrap{
			"old\x00physical": {Workspace: domain.Workspace{ID: "old-id", URLKey: "old-logical"}, Viewer: domain.User{ID: "old-viewer", Name: "before"}},
			"new\x00physical": {Workspace: domain.Workspace{ID: "new-id", URLKey: "new-logical"}, Viewer: domain.User{ID: "new-viewer", Name: "after"}},
		},
		metadataResidency: newMetadataResidency(),
	}
}

func TestScaleP21SW17ViewerDefaultSwitchBetweenLocks(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "explicit"}[explicit], func(t *testing.T) {
			repo := sw17ViewerDefaultSwitchStore()
			before, found := repo.WorkspaceMetadata("")
			if !found || before.Viewer.Name != "before" {
				t.Fatal("old default oracle missing")
			}
			workspace := ""
			if explicit {
				workspace = "old\x00physical"
			}
			var expected domain.User
			hook := &sw17ViewerDefaultHookContext{Context: t.Context(), hook: func() {
				repo.mu.Lock()
				repo.lastWorkspaceKey = "new\x00physical"
				old := repo.workspaces["old\x00physical"]
				old.Viewer.Name = "changed-after-switch"
				repo.workspaces["old\x00physical"] = old
				repo.mu.Unlock()
				legacy, found := repo.WorkspaceMetadata(workspace)
				if !found {
					t.Fatal("post-switch oracle missing")
				}
				expected = legacy.Viewer
			}}
			// tryAdmitAdditionalMetadataWork calls Err after the first
			// preflight lock is released and before the copy lock.
			read, found, err := repo.BeginInstalledViewerRead(hook, workspace)
			if err != nil || !found || read == nil {
				t.Fatal("viewer selection failed", found, err)
			}
			defer read.Close()
			if !reflect.DeepEqual(read.Viewer(), expected) {
				t.Fatalf("viewer combined an obsolete default with its later value: explicit=%v got=%+v expected=%+v", explicit, read.Viewer(), expected)
			}
			if !explicit && (read.Viewer().ID != "new-viewer" || read.Viewer().Name != "after") {
				t.Fatal("default lookup returned a viewer no single-lock default accessor observed")
			}
			if explicit && read.Viewer().Name != "changed-after-switch" {
				t.Fatal("explicit physical key followed the default switch")
			}
			read.Close()
			read.Close()
			if repo.metadataResidency.work.used != 0 || repo.metadataResidency.work.waiting != 0 {
				t.Fatal("default switch leaked owned viewer allowance")
			}
		})
	}
}

func TestScaleP21SW17ViewerDefaultSwitchCostRetryAndLifetime(t *testing.T) {
	repo := sw17ViewerDefaultSwitchStore()
	larger := domain.User{ID: "new-viewer", Name: strings.Repeat("large new viewer ", 10000)}
	expectedCost, err := scaleP21SW20ViewerCost(larger)
	if err != nil {
		t.Fatal(err)
	}
	parent, releaseParent, err := repo.admitMetadataWork(t.Context(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	hook := &sw17ViewerDefaultHookContext{Context: parent, hook: func() {
		repo.mu.Lock()
		repo.lastWorkspaceKey = "new\x00physical"
		next := repo.workspaces["new\x00physical"]
		next.Viewer = larger
		repo.workspaces["new\x00physical"] = next
		old := repo.workspaces["old\x00physical"]
		old.Viewer.Name = "changed-after-switch"
		repo.workspaces["old\x00physical"] = old
		repo.mu.Unlock()
	}}
	read, found, err := repo.BeginInstalledViewerRead(hook, "")
	if err != nil || !found || read == nil || !reflect.DeepEqual(read.Viewer(), larger) {
		t.Fatal("larger switched default failed admitted retry", found, err)
	}
	if repo.metadataResidency.work.used != 8<<20+expectedCost {
		t.Fatal("retry retained the failed small allowance or reused its parent", repo.metadataResidency.work.used, expectedCost)
	}
	releaseParent()
	if repo.metadataResidency.work.used != expectedCost {
		t.Fatal("parent release uncharged the result")
	}
	if read.Viewer().Name != larger.Name {
		t.Fatal("returned viewer did not remain alive after parent release")
	}
	read.Close()
	if repo.metadataResidency.work.used != 0 {
		t.Fatal("retry handle close leaked admission")
	}

	repo = sw17ViewerDefaultSwitchStore()
	hook = &sw17ViewerDefaultHookContext{Context: t.Context(), hook: func() {
		repo.mu.Lock()
		repo.lastWorkspaceKey = "new\x00physical"
		next := repo.workspaces["new\x00physical"]
		next.Viewer.Name = strings.Repeat("x", scaleP21SW20ViewerMaxWork/33+1)
		repo.workspaces["new\x00physical"] = next
		repo.mu.Unlock()
	}}
	read, found, err = repo.BeginInstalledViewerRead(hook, "")
	if read != nil || !found || !errors.Is(err, ErrMetadataWorkBudget) || repo.metadataResidency.work.used != 0 || repo.metadataResidency.work.waiting != 0 {
		t.Fatal("oversized switched default was returned or leaked allowance", found, err)
	}
}
