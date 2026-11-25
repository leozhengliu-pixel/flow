package main

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP21SW43DirectoryCacheKey(repo *store.SQLiteStore, workspaceID string) string {
	return fmt.Sprintf("%p:%s", repo, workspaceID)
}

func TestScaleP21SW43MemberDirectoryDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
		old, bounded := &server{store: repo}, &server{store: f.probe}
		keys := []string{
			scaleP21SW43DirectoryCacheKey(repo, workspaceID),
			scaleP21SW43DirectoryCacheKey(f.probe, workspaceID),
		}
		clear := func() {
			for _, key := range keys {
				memberDirectories.Delete(key)
			}
		}
		t.Cleanup(clear)
		compare := func(ctx context.Context, ref string) memberDirectory {
			t.Helper()
			left := old.workspaceMemberDirectory(ctx, ref)
			right := bounded.workspaceMemberDirectory(ctx, ref)
			if !reflect.DeepEqual(left, right) {
				t.Fatalf("member directory changed for exact reference %q: old=%#v bounded=%#v", ref, left, right)
			}
			return right
		}
		f.matrix(t, func(t *testing.T, actor domain.User, key *domain.APIKey) {
			clear()
			ctx := f.literalRequest(actor, key, nil).Context()
			queries := 0
			ctx = store.WithSQLStatementObserver(ctx, func(query string) {
				if strings.Contains(query, "FROM workspace_memberships m JOIN auth_users") {
					queries++
				}
			})
			directory := compare(ctx, workspace)
			if len(directory.users) != len(f.principals) || directory.roles[actor.ID] == "" || queries != 2 {
				t.Fatalf("trusted directory/principal independence changed: users=%d role=%q SQL=%d", len(directory.users), directory.roles[actor.ID], queries)
			}
			compare(ctx, workspace)
			if queries != 2 {
				t.Fatal("cached directory performed another membership read")
			}
			// The original metadata identity lookup ignores cancellation; an
			// already cached directory is still returned to this caller.
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if got := compare(canceled, workspace); !reflect.DeepEqual(got, directory) {
				t.Fatal("cancellation changed the existing cache-hit contract")
			}
		})

		clear()
		target := f.principals[2].user.ID
		before := compare(t.Context(), workspace)
		if _, ok := before.users[target]; !ok {
			t.Fatal("fixture member absent before suspension")
		}
		if err := repo.SuspendMember(t.Context(), workspaceID, target); err != nil {
			t.Fatal(err)
		}
		// Preserve the existing notification-directory TTL, including queued
		// work seeing the same cache until it expires.
		for _, phase := range []string{"queued", "replay"} {
			if got := compare(t.Context(), workspace); !reflect.DeepEqual(got, before) {
				t.Fatalf("%s changed the existing cache lifetime", phase)
			}
		}
		for _, key := range keys {
			cached, ok := memberDirectories.Load(key)
			if !ok {
				t.Fatal("owned cache entry disappeared")
			}
			entry := cached.(cachedMemberDirectory)
			entry.loadedAt = time.Now().Add(-2 * memberDirectoryTTL)
			memberDirectories.Store(key, entry)
		}
		after := compare(t.Context(), workspace)
		if _, ok := after.users[target]; ok || after.roles[target] != "" {
			t.Fatal("expired directory retained a suspended member")
		}
		if err := repo.ResumeMember(t.Context(), workspaceID, target); err != nil {
			t.Fatal(err)
		}
		clear()
		if _, ok := compare(t.Context(), workspace).users[target]; !ok {
			t.Fatal("resumed member missing after the normal refresh")
		}

		for _, ref := range []string{workspace + "-missing", strings.ToUpper(workspace), " "} {
			directory := compare(t.Context(), ref)
			if len(directory.users) != 0 || len(directory.roles) != 0 {
				t.Fatal("exact missing workspace reference gained membership")
			}
		}
		clear()
		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		if got := compare(canceled, workspace); len(got.users) != 0 || len(got.roles) != 0 {
			t.Fatal("uncached canceled SQL read returned a directory")
		}
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW43 foreign", idgen.New("sw43-directory-foreign-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, foreign.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		foreignKeys := []string{
			scaleP21SW43DirectoryCacheKey(repo, foreign.Workspace.ID),
			scaleP21SW43DirectoryCacheKey(f.probe, foreign.Workspace.ID),
		}
		t.Cleanup(func() {
			for _, key := range foreignKeys {
				memberDirectories.Delete(key)
			}
		})
		if got := compare(t.Context(), foreign.Workspace.URLKey); len(got.users) != 1 || got.users[f.data.Viewer.ID].ID == "" || got.users[target].ID != "" {
			t.Fatal("directory crossed a workspace boundary")
		}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			service := &server{store: source, authDisabled: true}
			if got := service.workspaceMemberDirectory(t.Context(), workspace); len(got.users) != 0 || len(got.roles) != 0 {
				t.Fatal("auth-disabled directory changed")
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("bounded identity read decoded complete metadata")
		}
	}
}

func TestScaleP21SW43MemberDirectoryAllocationSample(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []*store.SQLiteStore{repo, view} {
			key := scaleP21SW43DirectoryCacheKey(source, f.data.Workspace.ID)
			t.Cleanup(func() { memberDirectories.Delete(key) })
			service := &server{store: source}
			service.workspaceMemberDirectory(t.Context(), f.data.Workspace.URLKey)
			allocations := testing.AllocsPerRun(20, func() {
				if len(service.workspaceMemberDirectory(t.Context(), f.data.Workspace.URLKey).users) != 1 {
					t.Fatal("cached directory sample lost its owner")
				}
			})
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for range 20 {
				service.workspaceMemberDirectory(t.Context(), f.data.Workspace.URLKey)
			}
			runtime.ReadMemStats(&after)
			t.Logf("bounded=%t cached-directory allocs/op=%.0f bytes/op=%d", source == view, allocations, (after.TotalAlloc-before.TotalAlloc)/20)
		}
	}
}
