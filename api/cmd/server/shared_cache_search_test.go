package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
	"flow/api/internal/store"

	"github.com/alicebob/miniredis/v2"
)

// Two cached replicas and uncached references in both flag modes compare
// search, viewer policy and sync against one database and one Redis.
func TestSharedCacheDifferential(t *testing.T) {
	for _, flag := range []string{"off", "on"} {
		t.Run("bounded-"+flag, func(t *testing.T) { testSharedCacheDifferential(t, flag) })
	}
}

type sharedCacheReplica struct {
	url   string
	store *store.SQLiteStore
}

func sharedCacheReplicas(t *testing.T, flag string, opTimeout time.Duration) (*miniredis.Miniredis, []sharedCacheReplica, string) {
	t.Helper()
	// A complete legacy seed, as bounded startup requires (rtSeed sets the
	// journal environment).
	seed := rtSeed(t)
	path := filepath.Join(t.TempDir(), "shared-cache.db")
	rtCopy(t, seed, path)
	t.Setenv("FLOW_BOUNDED_METADATA", flag)
	t.Setenv("FLOW_SYNC_V2", "on")
	redisServer := miniredis.RunT(t)
	uploads := t.TempDir()
	count := 3
	if flag == "on" {
		// Compare both readers against writes that maintain bounded coverage.
		count++
	}
	replicas := make([]sharedCacheReplica, count)
	for index := range replicas {
		wantBounded := flag == "on"
		if index == 3 {
			wantBounded = false
			t.Setenv("FLOW_BOUNDED_METADATA", "off")
		}
		repository, err := store.OpenDatabase(rtConfig(path))
		if err != nil {
			t.Fatal(err)
		}
		if repository.BoundedMetadataEnabled() != wantBounded {
			t.Fatalf("replica %d: bounded mode = %v, want %v", index, repository.BoundedMetadataEnabled(), wantBounded)
		}
		// Both flag states have an uncached reference.
		coordinator, err := coordination.Open(context.Background(), coordination.Config{Mode: "standalone", Addrs: []string{redisServer.Addr()}, Prefix: "shared-cache", ConnectTimeout: time.Second, LockTTL: 5 * time.Second, LockWait: 5 * time.Second, Cache: coordination.SharedCacheConfig{Disabled: index >= 2, Viewer: true, SyncPage: true, OpTimeout: opTimeout}})
		if err != nil {
			t.Fatal(err)
		}
		repository.SetWorkspaceCoordinator(coordinator)
		instance := &server{store: repository, uploadPath: uploads, coordinator: coordinator, coordinationContext: t.Context()}
		disableBackgroundWorkersForTest(instance)
		// Require requests to observe peer writes before any subscriber refresh.
		instance.coordinationStarted.Store(true)
		httpServer := httptest.NewServer(newHandler(instance))
		t.Cleanup(func() {
			httpServer.Close()
			_ = coordinator.Close()
			_ = repository.Close()
		})
		replicas[index] = sharedCacheReplica{url: httpServer.URL, store: repository}
	}
	t.Setenv("FLOW_BOUNDED_METADATA", flag)
	return redisServer, replicas, path
}

// sharedCacheSearch returns the search response without the caller's search
// history, which every search appends to.
func sharedCacheSearch(t *testing.T, client *http.Client, bearer, base, query string) string {
	t.Helper()
	status, raw := sharedCacheGET(t, client, bearer, base, "/api/search?"+query)
	if status != http.StatusOK {
		t.Fatalf("search %s on %s: status %d: %s", query, base, status, raw)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	delete(body, "history")
	canonical, _ := json.Marshal(body)
	return string(canonical)
}

func sharedCacheGET(t *testing.T, client *http.Client, bearer, base, path string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Workspace-Key", "test-workspace")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, raw
}

func sharedCacheValues(t *testing.T, redisServer *miniredis.Miniredis, kind string) []string {
	t.Helper()
	var values []string
	for _, key := range redisServer.Keys() {
		if !strings.Contains(key, ":sc:{result-") {
			continue
		}
		fields, err := redisServer.HKeys(key)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range fields {
			if strings.HasPrefix(field, kind+":") {
				values = append(values, redisServer.HGet(key, field))
			}
		}
	}
	return values
}

func evictSharedCache(redisServer *miniredis.Miniredis) {
	for _, key := range redisServer.Keys() {
		if strings.HasPrefix(key, "shared-cache:sc:{result-") {
			redisServer.Del(key)
		}
	}
}

// Model a journal page from before issue field history accompanied edits, so
// the same production sync path also exercises per-viewer upserts.
func sharedCacheReplayWithoutHistory(t *testing.T, path, workspaceID, after string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result, err := db.ExecContext(t.Context(), `UPDATE record_journal SET payload=CAST(json_set(payload,'$.records',
		json((SELECT json_group_array(json(value)) FROM json_each(payload,'$.records') WHERE json_extract(value,'$.key.Kind')='issues'))) AS BLOB)
		WHERE workspace_id=? AND seq>?`, workspaceID, after)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 2 {
		t.Fatalf("replay fixture updated %d journal rows: %v", rows, err)
	}
}

func testSharedCacheDifferential(t *testing.T, flag string) {
	redisServer, replicas, dbPath := sharedCacheReplicas(t, flag, time.Second)
	first, second, reference := replicas[0].url, replicas[1].url, replicas[2].url
	admin := peerAdmin(t, first)
	teams := first + "/api/workspaces/test-workspace/teams"
	open := authRequest[domain.Team](t, admin, http.MethodPost, teams, map[string]any{"name": "Zephyr open", "key": "ZOP"}, "", http.StatusCreated)
	vault := authRequest[domain.Team](t, admin, http.MethodPost, teams, map[string]any{"name": "Zephyr vault", "key": "ZVT", "private": true}, "", http.StatusCreated)
	authRequest[domain.Project](t, admin, http.MethodPost, second+"/api/projects", map[string]any{"name": "Zephyr launch", "summary": "open zephyr work", "teamIds": []string{open.ID}}, "test-workspace", http.StatusCreated)
	secret := authRequest[domain.Project](t, admin, http.MethodPost, first+"/api/projects", map[string]any{"name": "Zephyr vault plan", "summary": "private zephyr work", "teamIds": []string{vault.ID}}, "test-workspace", http.StatusCreated)
	authRequest[domain.Document](t, admin, http.MethodPost, second+"/api/documents", map[string]any{"title": "Zephyr handbook", "content": "zephyr notes for everyone", "teamIds": []string{open.ID}}, "test-workspace", http.StatusCreated)
	authRequest[domain.Document](t, admin, http.MethodPost, first+"/api/documents", map[string]any{"title": "Zephyr vault memo", "content": "zephyr secrets", "teamIds": []string{vault.ID}}, "test-workspace", http.StatusCreated)

	member, memberUser := verifiedAuthClient(t, first, "Zephyr Member", "zephyr-member@example.com")
	invitations := authRequest[[]domain.Invitation](t, admin, http.MethodPost, first+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{memberUser.Email}, "role": "member", "teamIds": []string{open.ID}}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, member, http.MethodPost, second+"/api/invitations/accept", map[string]string{"token": invitations[0].Token}, "", http.StatusOK)
	key := authRequest[struct {
		Key    domain.APIKey `json:"key"`
		Secret string        `json:"secret"`
	}](t, admin, http.MethodPost, second+"/api/api-keys", map[string]any{"name": "Zephyr key", "scopes": []string{"read"}, "teamIds": []string{open.ID}}, "test-workspace", http.StatusCreated)

	type principal struct {
		name   string
		client *http.Client
		bearer string
	}
	principals := []principal{{"admin", admin, ""}, {"member", member, ""}, {"key", nil, key.Secret}}
	queries := []string{
		"q=zephyr", "q=Zephyr&types=project", "q=zephyr&types=document&sort=title", "q=vault&includeArchived=true",
		"q=zephyr&limit=1", "q=" + url.QueryEscape("zephyr work"), "q=nothing-matches-this",
	}
	policy, ok := replicas[0].store.IssueAccessMetadata(t.Context(), "test-workspace")
	if !ok {
		t.Fatal("missing workspace")
	}
	publicIssue := authRequest[domain.Issue](t, admin, http.MethodPost, first+"/api/issue-records", map[string]any{"title": "Public sync fixture", "teamId": open.ID}, "test-workspace", http.StatusCreated)
	privateIssue := authRequest[domain.Issue](t, admin, http.MethodPost, first+"/api/issue-records", map[string]any{"title": "Private sync fixture", "teamId": vault.ID}, "test-workspace", http.StatusCreated)
	// Mark the key used before recording the stable cursor for the page.
	sharedCacheSearch(t, nil, key.Secret, first, "q=zephyr&types=issue")
	after, err := replicas[0].store.RecordJournalHead(t.Context(), policy.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	syncPath := "/api/sync/changes?limit=2&after=" + after
	authRequest[domain.Issue](t, admin, http.MethodPatch, second+"/api/issue-records/"+publicIssue.ID, map[string]any{"title": "Public sync beacon"}, "test-workspace", http.StatusOK)
	authRequest[domain.Issue](t, admin, http.MethodPatch, first+"/api/issue-records/"+privateIssue.ID, map[string]any{"title": "Private sync beacon"}, "test-workspace", http.StatusOK)
	page, err := replicas[0].store.ReadCommittedChanges(t.Context(), policy.Workspace.ID, after, 2, syncV2DeltaBytes)
	if err != nil || len(page.Transactions) != 2 || !cacheableSyncPage(page) {
		t.Fatalf("expected two cacheable issue edits, got %d transactions (err=%v)", len(page.Transactions), err)
	}
	refs := []string{first, second, reference}
	if len(replicas) > 3 {
		refs = append(refs, replicas[3].url)
	}
	compare := func(phase string, hasHistory, memberInVault bool) {
		t.Helper()
		for _, who := range principals {
			for _, query := range queries {
				// The first replica may compute, the second reads its entry.
				want := sharedCacheSearch(t, who.client, who.bearer, reference, query)
				for index, base := range refs {
					if got := sharedCacheSearch(t, who.client, who.bearer, base, query); got != want {
						t.Fatalf("%s: %s %s on replica %d differs:\n got %s\nwant %s", phase, who.name, query, index, got, want)
					}
				}
			}
			status, want := sharedCacheGET(t, who.client, who.bearer, reference, syncPath)
			expected := "Public sync beacon"
			if hasHistory {
				expected = `"invalidations":["issue_field_history","issues"]`
			}
			if status != http.StatusOK || !bytes.Contains(want, []byte(expected)) {
				t.Fatalf("%s: %s sync reference returned %d: %s", phase, who.name, status, want)
			}
			if !hasHistory && bytes.Contains(want, []byte("Private sync beacon")) != (who.name == "admin" || who.name == "member" && memberInVault) {
				t.Fatalf("%s: %s has unexpected private issue visibility", phase, who.name)
			}
			for index, base := range refs {
				if base == reference {
					continue
				}
				if status, got := sharedCacheGET(t, who.client, who.bearer, base, syncPath); status != http.StatusOK || !bytes.Equal(got, want) {
					t.Fatalf("%s: %s sync on replica %d returned %d:\n got %s\nwant %s", phase, who.name, index, status, got, want)
				}
			}
		}
	}
	visible := func(client *http.Client, base, name string) bool {
		return strings.Contains(sharedCacheSearch(t, client, "", base, "q=zephyr"), name)
	}

	evictSharedCache(redisServer)
	for range 3 {
		sharedCacheSearch(t, member, "", first, "q=vault&types=project,document")
	}
	memberEntries := sharedCacheValues(t, redisServer, "search-meta")
	if len(memberEntries) != 2 {
		t.Fatalf("expected two scoped search entries, got %d", len(memberEntries))
	}
	for _, value := range memberEntries {
		if value != "F\x00{}" {
			t.Fatal("a search with only hidden matches stored data for the member")
		}
	}
	// Each replica observes reuse before the first stores the result and the
	// second reads it. The peer and uncached references still compare below.
	for _, base := range []string{first, second} {
		for range 2 {
			sharedCacheSearch(t, admin, "", base, "q=zephyr")
		}
	}
	hits := coordination.SharedCacheCounters()
	compare("cold with issue history", true, false)
	for _, mode := range []string{"Search", "Viewer", "Sync"} {
		counter := "redisCache" + mode + "Hits"
		if coordination.SharedCacheCounters()[counter] == hits[counter] {
			t.Fatalf("%s did not read shared entries", mode)
		}
	}
	if visible(member, first, "Zephyr vault plan") {
		t.Fatal("the member sees a private team's project")
	}
	sharedCacheReplayWithoutHistory(t, dbPath, policy.Workspace.ID, after)
	compare("projected upserts", false, false)
	compare("warm projected upserts", false, false)

	// A write on one replica is searched on the other at once.
	authRequest[domain.Project](t, admin, http.MethodPatch, second+"/api/projects/"+secret.ID, map[string]any{"name": "Zephyr vault renamed"}, "test-workspace", http.StatusOK)
	if !visible(admin, first, "Zephyr vault renamed") || visible(admin, first, "Zephyr vault plan") {
		t.Fatal("the first replica served a search from before the rename")
	}
	compare("after rename", false, false)

	// A permission grant and its revocation take effect at once.
	authRequest[any](t, admin, http.MethodPut, first+"/api/workspaces/test-workspace/teams/"+vault.ID+"/members/"+memberUser.ID, map[string]any{"member": true}, "", http.StatusNoContent)
	if !visible(member, second, "Zephyr vault renamed") {
		t.Fatal("the member does not see the team they joined")
	}
	compare("after grant", false, true)
	authRequest[any](t, admin, http.MethodPut, second+"/api/workspaces/test-workspace/teams/"+vault.ID+"/members/"+memberUser.ID, map[string]any{"member": false}, "", http.StatusNoContent)
	if visible(member, first, "Zephyr vault renamed") {
		t.Fatal("the member still sees the team they left")
	}
	compare("after revocation", false, false)

	// Evict result shards while coordination continues normally.
	evictSharedCache(redisServer)
	compare("after eviction", false, false)
	authRequest[domain.Document](t, admin, http.MethodPost, first+"/api/documents", map[string]any{"title": "Zephyr after flush", "content": "zephyr again", "teamIds": []string{open.ID}}, "test-workspace", http.StatusCreated)
	compare("after eviction and write", false, false)

	// A peer update retires the already-cached replay of the old issue version.
	authRequest[domain.Issue](t, admin, http.MethodPatch, second+"/api/issue-records/"+publicIssue.ID, map[string]any{"title": "Public sync moved on"}, "test-workspace", http.StatusOK)
	status, want := sharedCacheGET(t, admin, "", reference, syncPath)
	if status != http.StatusOK || bytes.Contains(want, []byte("Public sync beacon")) || !bytes.Contains(want, []byte(`"invalidations":["issues"]`)) {
		t.Fatalf("sync reference after peer update returned %d: %s", status, want)
	}
	for index, base := range refs {
		if status, got := sharedCacheGET(t, admin, "", base, syncPath); status != http.StatusOK || !bytes.Equal(got, want) {
			t.Fatalf("sync after peer update on replica %d returned %d:\n got %s\nwant %s", index, status, got, want)
		}
	}

	authRequest[any](t, admin, http.MethodDelete, second+"/api/api-keys/"+key.Key.ID, nil, "test-workspace", http.StatusNoContent)
	for _, path := range []string{"/api/search?q=zephyr", syncPath} {
		for index, base := range refs {
			status, _ := sharedCacheGET(t, nil, key.Secret, base, path)
			if status != http.StatusUnauthorized && status != http.StatusForbidden {
				t.Fatalf("revoked key reached %s on replica %d with status %d", path, index, status)
			}
		}
	}
}

// Search pages read through a cache whose Redis is gone compute locally and
// return the same result.
func TestSharedCacheSearchWithoutRedis(t *testing.T) {
	redisServer, replicas, _ := sharedCacheReplicas(t, "off", 50*time.Millisecond)
	admin := peerAdmin(t, replicas[0].url)
	authRequest[domain.Project](t, admin, http.MethodPost, replicas[0].url+"/api/projects", map[string]any{"name": "Zephyr offline", "teamIds": []string{}}, "test-workspace", http.StatusCreated)
	ctx := context.Background()
	search := func(repository *store.SQLiteStore) []byte {
		t.Helper()
		viewer := repository.Account().Viewer
		policy, access, err := repository.IssueQueryAccess(ctx, "test-workspace", viewer.ID)
		if err != nil {
			t.Fatal(err)
		}
		query := store.IssueRecordQuery{Workspace: "test-workspace", Access: &access, Archived: "false", Direction: "desc"}
		data, err := repository.SearchMetadata(ctx, policy, store.SearchMetadataQuery{Scope: query, Types: searchTypes(""), Terms: []string{"zephyr"}, Limit: 40})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(data)
		return raw
	}
	want := search(replicas[2].store)
	if got := search(replicas[0].store); !bytes.Equal(got, want) {
		t.Fatalf("cached search differs:\n%s\n%s", got, want)
	}
	if got := search(replicas[1].store); !bytes.Equal(got, want) {
		t.Fatalf("search from the shared entry differs:\n%s\n%s", got, want)
	}
	redisServer.Close()
	started := time.Now()
	if got := search(replicas[0].store); !bytes.Equal(got, want) {
		t.Fatalf("search without Redis differs:\n%s\n%s", got, want)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("search without Redis took %s", elapsed)
	}
}
