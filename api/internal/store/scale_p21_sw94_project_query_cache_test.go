package store

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"

	"github.com/alicebob/miniredis/v2"
)

func scaleP21SW94Cached(t *testing.T) (scaleP09Fixture, *SQLiteStore, *coordination.Redis, *miniredis.Miniredis) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	server := miniredis.RunT(t)
	cache, err := coordination.Open(t.Context(), coordination.Config{
		Mode: "standalone", Addrs: []string{server.Addr()}, Prefix: "scale-p21-sw94",
		ConnectTimeout: time.Second, LockTTL: time.Second, LockWait: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.RLock()
	prior := f.coordinator
	f.mu.RUnlock()
	var view *SQLiteStore
	var priorView WorkspaceCoordinator
	t.Cleanup(func() {
		f.SetWorkspaceCoordinator(prior)
		if view != nil {
			view.SetWorkspaceCoordinator(priorView)
		}
		_ = cache.Close()
	})
	f.SetWorkspaceCoordinator(cache)
	view, err = f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	view.mu.RLock()
	priorView = view.coordinator
	view.mu.RUnlock()
	view.SetWorkspaceCoordinator(cache)
	return f, view, cache, server
}

func TestScaleP21SW94ProjectQueryCacheDifferential(t *testing.T) {
	f, view, cache, server := scaleP21SW94Cached(t)
	query := ProjectRecordQuery{
		Workspace: f.data.Workspace.URLKey, Limit: 13, IncludeTotal: true, Search: "literal <query>",
		TeamIDs: []string{"team-one", "team-two"}, AllowedTeamIDs: []string{},
		Filters: []ProjectDirectoryFilter{{Field: "name", Operator: "contains", Values: []string{"one"}}},
	}
	for _, generation := range []string{"", "17", "-7", "9223372036854775807", "not-a-number", "9223372036854775808"} {
		t.Run("generation-"+generation, func(t *testing.T) {
			genKey := f.projectQueryGenKey(query.Workspace)
			server.Del(genKey)
			if generation != "" {
				if err := server.Set(genKey, generation); err != nil {
					t.Fatal(err)
				}
			}
			_, legacyKey, _ := f.cacheGetProjectQuery(t.Context(), query)
			if legacyKey == "" {
				t.Fatal("literal query cache key missing")
			}
			for _, raw := range [][]byte{
				nil, []byte{}, []byte("null"), []byte(`{}`), []byte(`{"items":null,"total":-1}`),
				[]byte(`{"items":[],"hasMore":true,"nextCursor":"opaque","total":77}`),
				[]byte(`{"items":[{"id":"project","name":"Full cached project","description":"literal body","previousSlugIds":["old"],"createdAt":"2026-11-14T01:02:03Z"}],"total":1}`),
				[]byte(`{"items":`), []byte(" \n\t"), []byte(`[]`),
				[]byte(`{"items":[{"createdAt":"invalid-time"}]}`), []byte(`{"items":42}`),
			} {
				server.Del(legacyKey)
				if raw != nil {
					if err := cache.CacheSet(t.Context(), legacyKey, raw, time.Minute); err != nil {
						t.Fatal(err)
					}
				}
				want, wantKey, wantHit := f.cacheGetProjectQuery(t.Context(), query)
				owner := &ResourceRead{store: view}
				got, gotKey, gotHit, err := view.cacheGetProjectQueryOwned(t.Context(), query, owner)
				if err != nil || gotKey != wantKey || gotHit != wantHit || !reflect.DeepEqual(got, want) {
					owner.Close()
					t.Fatalf("raw=%q query cache differs: page=%+v/%+v key=%s/%s hit=%t/%t err=%v", raw, got, want, gotKey, wantKey, gotHit, wantHit, err)
				}
				if view.MetadataWorkUsedForTesting() == 0 {
					owner.Close()
					t.Fatal("cache key/page work was not held by the caller")
				}
				owner.Close()
				if view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("closed cache output retained work")
				}
			}
		})
	}
	t.Run("set-warm-fresh-and-output-ownership", func(t *testing.T) {
		server.Del(f.projectQueryGenKey(query.Workspace))
		_, key, _ := f.cacheGetProjectQuery(t.Context(), query)
		page := ProjectRecordPage{
			Items: []domain.Project{{ID: "project", Name: "Original", Description: strings.Repeat("Full owned cache body. ", 2000),
				PreviousSlugIDs: []string{"old"}, LabelIDs: []string{}, CreatedAt: time.Date(2026, 11, 14, 1, 2, 3, 0, time.UTC)}},
			NextCursor: "next", HasMore: true, Total: 51,
		}
		f.cacheSetProjectQuery(t.Context(), key, page)
		legacyRaw, err := cache.CacheGet(t.Context(), key)
		if err != nil {
			t.Fatal(err)
		}
		owner := &ResourceRead{store: view}
		if err := view.cacheSetProjectQueryOwned(t.Context(), key, page, owner); err != nil {
			owner.Close()
			t.Fatal(err)
		}
		ownedRaw, err := cache.CacheGet(t.Context(), key)
		if err != nil || !bytes.Equal(legacyRaw, ownedRaw) || view.MetadataWorkUsedForTesting() == 0 {
			owner.Close()
			t.Fatal("cache setter changed literal bytes or did not own encoding", err)
		}
		owner.Close()
		first := &ResourceRead{store: view}
		got, gotKey, hit, err := view.cacheGetProjectQueryOwned(t.Context(), query, first)
		if err != nil || !hit || gotKey != key || !reflect.DeepEqual(got, page) {
			first.Close()
			t.Fatal("fresh owned cache read differs", err)
		}
		got.Items[0].Name = "Caller mutation"
		got.Items[0].PreviousSlugIDs[0] = "Caller alias"
		second := &ResourceRead{store: view}
		again, _, hit, err := view.cacheGetProjectQueryOwned(t.Context(), query, second)
		if err != nil || !hit || !reflect.DeepEqual(again, page) {
			second.Close()
			first.Close()
			t.Fatal("warm cache shared mutable caller output", err)
		}
		second.Close()
		first.Close()
		if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("cache reads retained work or decoded full metadata")
		}
	})
}

type scaleP21SW94CacheHook struct {
	*coordination.Redis
	read func(context.Context, string, int64) ([]byte, int64, error)
}

func (cache *scaleP21SW94CacheHook) CacheGetBounded(ctx context.Context, key string, maxBytes int64) ([]byte, int64, error) {
	return cache.read(ctx, key, maxBytes)
}

type scaleP21SW94LegacyCache struct {
	WorkspaceCoordinator
	hotCache
}

func TestScaleP21SW94ProjectQueryCacheReadGuards(t *testing.T) {
	f, view, cache, server := scaleP21SW94Cached(t)
	query := ProjectRecordQuery{Workspace: f.data.Workspace.URLKey, Limit: 10}
	raw := []byte(`{"items":[{"id":"project","name":"Cached"}],"total":1}`)
	_, key, _ := f.cacheGetProjectQuery(t.Context(), query)
	for _, mode := range []string{"growth", "oversize-header", "negative-header", "preflight-error", "fetch-error", "disappeared"} {
		t.Run(mode, func(t *testing.T) {
			if err := cache.CacheSet(t.Context(), key, raw, time.Minute); err != nil {
				t.Fatal(err)
			}
			pageCalls := 0
			hook := &scaleP21SW94CacheHook{Redis: cache}
			hook.read = func(ctx context.Context, actualKey string, maxBytes int64) ([]byte, int64, error) {
				if actualKey != key {
					return cache.CacheGetBounded(ctx, actualKey, maxBytes)
				}
				pageCalls++
				if pageCalls == 1 {
					switch mode {
					case "oversize-header":
						return nil, metadataWorkBytes/32 + 1, nil
					case "negative-header":
						return nil, -1, nil
					case "preflight-error":
						return nil, 0, errors.New("cache preflight failed")
					}
					return cache.CacheGetBounded(ctx, actualKey, maxBytes)
				}
				switch mode {
				case "growth":
					if err := server.Set(key, strings.Repeat("grown payload ", 100)); err != nil {
						t.Fatal(err)
					}
				case "fetch-error":
					return nil, 0, errors.New("cache fetch failed")
				case "disappeared":
					server.Del(key)
				}
				return cache.CacheGetBounded(ctx, actualKey, maxBytes)
			}
			view.SetWorkspaceCoordinator(hook)
			defer view.SetWorkspaceCoordinator(cache)
			owner := &ResourceRead{store: view}
			page, gotKey, hit, err := view.cacheGetProjectQueryOwned(t.Context(), query, owner)
			budget := mode == "growth" || mode == "oversize-header" || mode == "negative-header"
			if hit || gotKey != key || !reflect.DeepEqual(page, ProjectRecordPage{}) ||
				budget && !errors.Is(err, ErrMetadataWorkBudget) || !budget && err != nil {
				owner.Close()
				t.Fatal("cache guard changed miss/budget semantics", mode, page, gotKey, hit, err)
			}
			if (mode == "oversize-header" || mode == "negative-header") && pageCalls != 1 {
				owner.Close()
				t.Fatal("oversized header fetched an unadmitted payload")
			}
			owner.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("cache guard retained parent work")
			}
		})
	}
	t.Run("cancel-and-budget", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		owner := &ResourceRead{store: view}
		_, _, hit, err := view.cacheGetProjectQueryOwned(cancelled, query, owner)
		owner.Close()
		if hit || err != nil && !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("canceled cache read hit or retained work", err)
		}
		limit := view.metadataResidency.work.limit
		view.metadataResidency.work.limit = 1
		owner = &ResourceRead{store: view}
		_, _, hit, err = view.cacheGetProjectQueryOwned(t.Context(), query, owner)
		owner.Close()
		view.metadataResidency.work.limit = limit
		if hit || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("cache read admission did not fail cleanly", err)
		}
		owner = &ResourceRead{store: view}
		view.metadataResidency.work.limit = 1
		err = view.cacheSetProjectQueryOwned(t.Context(), key, ProjectRecordPage{Items: []domain.Project{{ID: "project"}}}, owner)
		owner.Close()
		view.metadataResidency.work.limit = limit
		if !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("cache encoding admission did not fail cleanly", err)
		}
	})
	t.Run("unsupported-adapter-and-wrong-owner", func(t *testing.T) {
		view.SetWorkspaceCoordinator(&scaleP21SW94LegacyCache{WorkspaceCoordinator: cache, hotCache: cache})
		owner := &ResourceRead{store: view}
		_, _, _, err := view.cacheGetProjectQueryOwned(t.Context(), query, owner)
		owner.Close()
		view.SetWorkspaceCoordinator(cache)
		if !errors.Is(err, ErrBoundedMetadataCoverage) {
			t.Fatal("unbounded cache adapter was accepted", err)
		}
		_, _, _, err = view.cacheGetProjectQueryOwned(t.Context(), query, &ResourceRead{store: f.SQLiteStore})
		if !errors.Is(err, ErrBoundedMetadataCoverage) {
			t.Fatal("cache output accepted another store's owner", err)
		}
	})
	t.Run("no-cache", func(t *testing.T) {
		view.SetWorkspaceCoordinator(nil)
		defer view.SetWorkspaceCoordinator(cache)
		page, key, hit, err := view.cacheGetProjectQueryOwned(t.Context(), query, nil)
		if err != nil || key != "" || hit || !reflect.DeepEqual(page, ProjectRecordPage{}) {
			t.Fatal("disabled cache did not retain its literal miss", err)
		}
		if err := view.cacheSetProjectQueryOwned(t.Context(), "unused", ProjectRecordPage{}, nil); err != nil {
			t.Fatal("disabled cache setter required an owner", err)
		}
	})
	t.Logf("cache generation key and page guard cases checked for limit %d", query.Limit)
}
