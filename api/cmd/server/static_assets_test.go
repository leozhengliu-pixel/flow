package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
)

// webBuild writes a built web app: index.html loading entry, and each asset.
func webBuild(t *testing.T, entry string, assets ...string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range assets {
		if err := os.WriteFile(filepath.Join(dir, "assets", name), []byte("/* "+name+" */ export default 1"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	html := `<!doctype html><html><body><script type="module" src="/assets/` + entry + `"></script></body></html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(releaseManifest{Release: releaseID(assets), Assets: assets})
	if err := os.WriteFile(filepath.Join(dir, assetManifestName), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// carried is an image's /app/web-previous: the assets of earlier builds.
func carried(t *testing.T, builds ...string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, build := range builds {
		entries, err := os.ReadDir(filepath.Join(build, "assets"))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			data, err := os.ReadFile(filepath.Join(build, "assets", entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "assets", entry.Name()), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

// replica is one running server process of a build, on a real listener.
type replica struct {
	*httptest.Server
	requests atomic.Int64
}

func startReplica(t *testing.T, staticPath string, config appconfig.AssetsConfig) *replica {
	t.Helper()
	if config.SharedKeepReleases == 0 {
		config.SharedKeepReleases = appconfig.DefaultAssetSharedKeepReleases
	}
	r := &replica{}
	handler := (&server{staticPath: staticPath, assets: newAssetFallback(config)}).withStaticFiles(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "api", http.StatusTeapot)
	}))
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.requests.Add(1)
		handler.ServeHTTP(w, req)
	}))
	t.Cleanup(r.Close)
	return r
}

// unstartedPeer reserves an address so two replicas can name each other.
func unstartedPeer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)
	return server, server.Listener.Addr().String()
}

func startReplicaOn(t *testing.T, listener *httptest.Server, staticPath string, config appconfig.AssetsConfig) *replica {
	t.Helper()
	if config.SharedKeepReleases == 0 {
		config.SharedKeepReleases = appconfig.DefaultAssetSharedKeepReleases
	}
	r := &replica{Server: listener}
	handler := (&server{staticPath: staticPath, assets: newAssetFallback(config)}).withStaticFiles(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "api", http.StatusTeapot)
	}))
	listener.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.requests.Add(1)
		handler.ServeHTTP(w, req)
	})
	listener.Start()
	return r
}

func fetch(t *testing.T, base, path string, header ...string) (*http.Response, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(header); i += 2 {
		request.Header.Set(header[i], header[i+1])
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response, string(body)
}

func expectScript(t *testing.T, base, path, source string) {
	t.Helper()
	response, body := fetch(t, base, path)
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "javascript") || !strings.Contains(body, filepath.Base(path)) {
		t.Fatalf("%s from %s: status=%d type=%q body=%q", path, base, response.StatusCode, response.Header.Get("Content-Type"), body)
	}
	if response.Header.Get("Cache-Control") != assetImmutableCache {
		t.Fatalf("%s cache-control=%q", path, response.Header.Get("Cache-Control"))
	}
	if got := response.Header.Get(assetSourceHeader); got != source {
		t.Fatalf("%s source=%q want %q", path, got, source)
	}
}

func expectMissing(t *testing.T, base, path string) {
	t.Helper()
	response, body := fetch(t, base, path)
	if response.StatusCode != http.StatusNotFound || response.Header.Get("Cache-Control") != "no-store" || strings.Contains(response.Header.Get("Content-Type"), "text/html") || strings.Contains(body, "<html") {
		t.Fatalf("%s: status=%d cache-control=%q type=%q body=%q", path, response.StatusCode, response.Header.Get("Cache-Control"), response.Header.Get("Content-Type"), body)
	}
}

// Release v1 (old) and v2 (new); v2's image carries v1's assets.
func twoReleases(t *testing.T) (v1, v2, v2Previous string) {
	v1 = webBuild(t, "index-AAAA1111.js", "index-AAAA1111.js", "route-AAAA2222.js", "index-AAAA3333.css")
	v2 = webBuild(t, "index-BBBB1111.js", "index-BBBB1111.js", "route-BBBB2222.js", "index-BBBB3333.css")
	return v1, v2, carried(t, v1)
}

func TestRollingUpdateRefreshLoadsNewAssetsFromOldReplica(t *testing.T) {
	v1, v2, v2Previous := twoReleases(t)
	oldListener, oldAddr := unstartedPeer(t)
	newListener, newAddr := unstartedPeer(t)
	oldReplica := startReplicaOn(t, oldListener, v1, appconfig.AssetsConfig{Peers: []string{newAddr}})
	newReplica := startReplicaOn(t, newListener, v2, appconfig.AssetsConfig{PreviousPath: v2Previous, Peers: []string{oldAddr}})

	// The refresh lands on a new replica...
	response, html := fetch(t, newReplica.URL, "/acme/issues")
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-cache" || !strings.Contains(html, "/assets/index-BBBB1111.js") {
		t.Fatalf("new HTML status=%d cache-control=%q body=%q", response.StatusCode, response.Header.Get("Cache-Control"), html)
	}
	// ...and its entry, styles and route chunk go to an old one.
	expectScript(t, oldReplica.URL, "/assets/index-BBBB1111.js", "peer")
	expectScript(t, oldReplica.URL, "/assets/route-BBBB2222.js", "peer")
	css, _ := fetch(t, oldReplica.URL, "/assets/index-BBBB3333.css")
	if css.StatusCode != http.StatusOK || !strings.HasPrefix(css.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("css status=%d type=%q", css.StatusCode, css.Header.Get("Content-Type"))
	}
	// A page the old replica served loads its chunks from the new one.
	expectScript(t, newReplica.URL, "/assets/route-AAAA2222.js", "previous")
	// The old replica's own files never go to a peer.
	expectScript(t, oldReplica.URL, "/assets/route-AAAA2222.js", "")
	// Fetched once, then from memory.
	before := newReplica.requests.Load()
	expectScript(t, oldReplica.URL, "/assets/index-BBBB1111.js", "peer")
	if newReplica.requests.Load() != before {
		t.Fatal("a cached peer asset was fetched again")
	}
}

func TestPageFromBeforeTheDeployLoadsItsChunksAfterEveryReplicaIsNew(t *testing.T) {
	_, v2, v2Previous := twoReleases(t)
	// No old replica is left and nothing else is configured: the image's
	// copy of the previous release answers.
	newReplica := startReplica(t, v2, appconfig.AssetsConfig{PreviousPath: v2Previous})
	expectScript(t, newReplica.URL, "/assets/route-AAAA2222.js", "previous")
	expectScript(t, newReplica.URL, "/assets/index-AAAA1111.js", "previous")
	expectMissing(t, newReplica.URL, "/assets/route-ZZZZ9999.js")
}

func TestRollbackServesBothReleasesBothWays(t *testing.T) {
	v1, v2, v2Previous := twoReleases(t)
	shared := t.TempDir()
	// v2 ran first and published to the shared directory.
	if _, err := publishSharedAssets(v1, shared, 5, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := publishSharedAssets(v2, shared, 5, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Mid-rollback: v1 replicas come back beside v2 ones.
	oldListener, oldAddr := unstartedPeer(t)
	newListener, newAddr := unstartedPeer(t)
	rolledBack := startReplicaOn(t, oldListener, v1, appconfig.AssetsConfig{Peers: []string{newAddr}})
	stillNew := startReplicaOn(t, newListener, v2, appconfig.AssetsConfig{PreviousPath: v2Previous, Peers: []string{oldAddr}})
	expectScript(t, rolledBack.URL, "/assets/route-BBBB2222.js", "peer")   // v2 page, v1 replica
	expectScript(t, stillNew.URL, "/assets/route-AAAA2222.js", "previous") // v1 page, v2 replica
	expectScript(t, rolledBack.URL, "/assets/index-AAAA1111.js", "")       // own files
	expectScript(t, stillNew.URL, "/assets/index-BBBB1111.js", "")         // own files
	expectMissing(t, rolledBack.URL, "/assets/route-CCCC2222.js")          // nobody built it

	// Rollback finished: only v1 runs. A v2 page still open lazy-loads a
	// chunk; the v1 image cannot carry a later build, the shared directory has it.
	onlyOld := startReplica(t, v1, appconfig.AssetsConfig{SharedPath: shared})
	expectScript(t, onlyOld.URL, "/assets/route-BBBB2222.js", "shared")
	// Without shared storage the request is an honest 404 (the client then reloads once).
	bare := startReplica(t, v1, appconfig.AssetsConfig{})
	expectMissing(t, bare.URL, "/assets/route-BBBB2222.js")
}

func TestMissingAssetNeverAnswersHTML(t *testing.T) {
	v1, _, _ := twoReleases(t)
	// A replica of a build without this fallback answers misses with the app.
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><html>app</html>")
	}))
	defer legacy.Close()
	peer404 := httptest.NewServer(http.NotFoundHandler())
	defer peer404.Close()
	replica := startReplica(t, v1, appconfig.AssetsConfig{
		PreviousPath: t.TempDir(),
		SharedPath:   t.TempDir(),
		Peers:        []string{strings.TrimPrefix(legacy.URL, "http://"), strings.TrimPrefix(peer404.URL, "http://")},
		FallbackURLs: []string{legacy.URL, peer404.URL},
	})
	for _, path := range []string{"/assets/index-GONE1111.js", "/assets/index-GONE1111.css", "/assets/logo-GONE1111.svg", "/assets/font-GONE1111.woff2"} {
		expectMissing(t, replica.URL, path)
	}
	// A plain 404 replica also answers no-store.
	expectMissing(t, startReplica(t, v1, appconfig.AssetsConfig{}).URL, "/assets/index-GONE1111.js")
	// Pages still get the app.
	response, body := fetch(t, replica.URL, "/assets/issue/AST-1")
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "index-AAAA1111.js") {
		t.Fatalf("route under a workspace keyed assets: status=%d body=%q", response.StatusCode, body)
	}
}

func TestPeerRequestsAnswerFromDiskOnly(t *testing.T) {
	v1, v2, _ := twoReleases(t)
	far := startReplica(t, v2, appconfig.AssetsConfig{})
	near := startReplica(t, v1, appconfig.AssetsConfig{Peers: []string{strings.TrimPrefix(far.URL, "http://")}})
	response, _ := fetch(t, near.URL, "/assets/index-BBBB1111.js", assetPeerHeader, "1")
	if response.StatusCode != http.StatusNotFound || far.requests.Load() != 0 {
		t.Fatalf("peer request status=%d forwarded=%d", response.StatusCode, far.requests.Load())
	}
	// Two replicas naming each other do not loop on a file neither has.
	aListener, aAddr := unstartedPeer(t)
	bListener, bAddr := unstartedPeer(t)
	a := startReplicaOn(t, aListener, v1, appconfig.AssetsConfig{Peers: []string{bAddr}})
	b := startReplicaOn(t, bListener, v2, appconfig.AssetsConfig{Peers: []string{aAddr}})
	expectMissing(t, a.URL, "/assets/route-NONE0000.js")
	if b.requests.Load() != 1 {
		t.Fatalf("peer saw %d requests, want 1", b.requests.Load())
	}
	// The miss is remembered briefly instead of asking again.
	expectMissing(t, a.URL, "/assets/route-NONE0000.js")
	if b.requests.Load() != 1 {
		t.Fatalf("peer saw %d requests after a remembered miss, want 1", b.requests.Load())
	}
}

func TestAssetFallbackURLServesPublishedAssets(t *testing.T) {
	v1, _, _ := twoReleases(t)
	var hits atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/flow-assets/assets/route-BBBB2222.js" {
			http.NotFound(w, r)
			return
		}
		// Object stores often label files generically.
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, "/* route-BBBB2222.js */")
	}))
	defer origin.Close()
	replica := startReplica(t, v1, appconfig.AssetsConfig{FallbackURLs: []string{origin.URL + "/flow-assets/"}})
	expectScript(t, replica.URL, "/assets/route-BBBB2222.js", "url")
	expectScript(t, replica.URL, "/assets/route-BBBB2222.js", "url")
	if hits.Load() != 1 {
		t.Fatalf("origin hits=%d, want 1 (cached)", hits.Load())
	}
	head, err := http.Head(replica.URL + "/assets/route-BBBB2222.js")
	if err != nil {
		t.Fatal(err)
	}
	head.Body.Close()
	if head.StatusCode != http.StatusOK {
		t.Fatalf("HEAD status=%d", head.StatusCode)
	}
}

func TestSharedAssetsKeepRecentReleases(t *testing.T) {
	shared := t.TempDir()
	start := time.Now().Add(-48 * time.Hour)
	var builds []string
	for i, prefix := range []string{"AAAA", "BBBB", "CCCC", "DDDD"} {
		build := webBuild(t, "index-"+prefix+"1111.js", "index-"+prefix+"1111.js", "shared-00001111.js")
		builds = append(builds, build)
		if _, err := publishSharedAssets(build, shared, 2, start.Add(time.Duration(i)*3*time.Hour)); err != nil {
			t.Fatal(err)
		}
		// Assets carry the time they were published, as on a real volume.
		for _, name := range []string{"index-" + prefix + "1111.js", "shared-00001111.js"} {
			when := start.Add(time.Duration(i) * 3 * time.Hour)
			_ = os.Chtimes(filepath.Join(shared, "assets", name), when, when)
		}
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(shared, "assets", name))
		return err == nil
	}
	// The two newest releases stay; older ones are gone, a file they share stays.
	if exists("index-AAAA1111.js") || exists("index-BBBB1111.js") || !exists("index-CCCC1111.js") || !exists("index-DDDD1111.js") || !exists("shared-00001111.js") {
		entries, _ := os.ReadDir(filepath.Join(shared, "assets"))
		t.Fatalf("after pruning: %v", entries)
	}
	// Rolling back to C publishes it again, so it counts as newest; D stays as the one before.
	if _, err := publishSharedAssets(builds[2], shared, 2, start.Add(12*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !exists("index-CCCC1111.js") || !exists("index-DDDD1111.js") {
		t.Fatal("rollback dropped a kept release")
	}
	// A file a replica is still writing (no manifest yet, recent) is kept.
	if err := os.WriteFile(filepath.Join(shared, "assets", "index-EEEE1111.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pruneSharedAssets(shared, 2, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !exists("index-EEEE1111.js") {
		t.Fatal("a just-written file was pruned")
	}
	// No temporary files are left behind.
	entries, _ := os.ReadDir(filepath.Join(shared, "assets"))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Fatalf("temporary file left: %s", entry.Name())
		}
	}
}

func TestReleaseWithoutManifestIsListed(t *testing.T) {
	build := webBuild(t, "index-AAAA1111.js", "index-AAAA1111.js", "route-AAAA2222.js")
	if err := os.Remove(filepath.Join(build, assetManifestName)); err != nil {
		t.Fatal(err)
	}
	manifest, err := readRelease(build)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Release != releaseID([]string{"route-AAAA2222.js", "index-AAAA1111.js"}) || len(manifest.Assets) != 2 {
		t.Fatalf("manifest=%+v", manifest)
	}
}

func TestHashedAssetName(t *testing.T) {
	for path, want := range map[string]bool{
		"/assets/index-AB12cd34.js": true,
		"/assets/a.b-c_d.woff2":     true,
		"/assets/../index.html":     false,
		"/assets/sub/x.js":          false,
		"/assets/issue":             false,
		"/assets/.env":              false,
		"/fonts/x.woff2":            false,
		"/assets/x.html":            false,
	} {
		if _, got := hashedAssetName(path); got != want {
			t.Errorf("%s: got %v want %v", path, got, want)
		}
	}
}
