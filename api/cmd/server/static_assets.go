package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	appconfig "flow/api/internal/config"
)

// During a rolling update (or a rollback) replicas of two web app builds
// serve side by side. A page from one build asks for content-hashed files
// under /assets/ that only that build has, and the load balancer may send the
// request to a replica of the other build. assetFallback answers such a miss
// from, in order:
//
//  1. local directories: the previous releases' assets baked into the image
//     (FLOW_ASSET_PREVIOUS_PATH) and a directory every release publishes to
//     (FLOW_ASSET_SHARED_PATH);
//  2. the other replicas (FLOW_ASSET_PEERS, a headless Service);
//  3. base URLs where releases publish their assets (FLOW_ASSET_FALLBACK_URL).
//
// A file found nowhere is a 404 with Cache-Control: no-store, never the app.

// assetPeerHeader marks a request one replica sends another. The receiving
// replica answers from its disk only, so a miss never bounces between peers.
const assetPeerHeader = "X-Flow-Asset-Peer"

// assetSourceHeader says where a fallback answer came from (previous, shared,
// peer or url), for operators reading responses.
const assetSourceHeader = "X-Flow-Asset-Source"

const (
	assetManifestName   = "asset-manifest.json"
	assetMaxBytes       = 32 << 20
	assetCacheBytes     = 64 << 20
	assetMissTTL        = 10 * time.Second
	assetFetchTimeout   = 5 * time.Second
	assetMaxPeers       = 16
	assetPublishGrace   = time.Hour
	assetImmutableCache = "public, max-age=31536000, immutable"
)

// Vite's output file names: one path segment, letters, digits, '.', '_' and '-'.
var assetNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*$`)

// hashedAssetName returns the file name of a /assets/<name> request.
func hashedAssetName(path string) (string, bool) {
	name, ok := strings.CutPrefix(path, "/assets/")
	if !ok || !assetNamePattern.MatchString(name) || strings.Contains(name, "..") {
		return "", false
	}
	return name, staticAssetExtensions[strings.ToLower(filepath.Ext(name))]
}

type assetDir struct {
	source string // previous or shared
	path   string // holds assets/<name>
}

type assetFallback struct {
	dirs  []assetDir
	peers []string
	urls  []string
	// lookupHost resolves a peer host to the replicas' addresses.
	lookupHost func(ctx context.Context, host string) ([]string, error)
	peerClient *http.Client
	urlClient  *http.Client

	mu       sync.Mutex
	cached   map[string]*fetchedAsset
	order    []string
	bytes    int
	misses   map[string]time.Time
	inflight map[string]*assetFetch
}

type fetchedAsset struct {
	body        []byte
	contentType string
	source      string
}

type assetFetch struct {
	done  chan struct{}
	asset *fetchedAsset
}

// newAssetFallback returns nil when nothing is configured.
func newAssetFallback(config appconfig.AssetsConfig) *assetFallback {
	fallback := &assetFallback{peers: config.Peers, urls: config.FallbackURLs}
	if config.PreviousPath != "" {
		fallback.dirs = append(fallback.dirs, assetDir{source: "previous", path: config.PreviousPath})
	}
	if config.SharedPath != "" {
		fallback.dirs = append(fallback.dirs, assetDir{source: "shared", path: config.SharedPath})
	}
	if len(fallback.dirs) == 0 && len(fallback.peers) == 0 && len(fallback.urls) == 0 {
		return nil
	}
	resolver := &net.Resolver{}
	fallback.lookupHost = resolver.LookupHost
	// Peers are in-cluster Pod addresses: never through an outbound proxy.
	fallback.peerClient = &http.Client{Timeout: assetFetchTimeout, Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second}, CheckRedirect: noRedirects}
	fallback.urlClient = &http.Client{Timeout: assetFetchTimeout, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConnsPerHost: 4, IdleConnTimeout: 90 * time.Second}}
	fallback.cached = map[string]*fetchedAsset{}
	fallback.misses = map[string]time.Time{}
	fallback.inflight = map[string]*assetFetch{}
	return fallback
}

func noRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// serve answers r with another release's copy of name; false when no source
// has it (the caller answers 404).
func (f *assetFallback) serve(w http.ResponseWriter, r *http.Request, name string) bool {
	for _, dir := range f.dirs {
		file, err := os.Open(filepath.Join(dir.path, "assets", name))
		if err != nil {
			continue
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			file.Close()
			continue
		}
		writeAssetHeaders(w, name, "", dir.source)
		http.ServeContent(w, r, name, info.ModTime(), file)
		file.Close()
		return true
	}
	if r.Header.Get(assetPeerHeader) != "" {
		return false
	}
	asset := f.fetch(r.Context(), name)
	if asset == nil {
		return false
	}
	writeAssetHeaders(w, name, asset.contentType, asset.source)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(asset.body))
	return true
}

func writeAssetHeaders(w http.ResponseWriter, name, contentType, source string) {
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(name))
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", assetImmutableCache)
	w.Header().Set(assetSourceHeader, source)
}

// fetch asks the peers, then the fallback URLs. Concurrent requests for one
// file share a fetch; found files stay in a bounded memory cache and misses
// are remembered briefly so a burst of stale requests does not fan out.
func (f *assetFallback) fetch(ctx context.Context, name string) *fetchedAsset {
	if len(f.peers) == 0 && len(f.urls) == 0 {
		return nil
	}
	f.mu.Lock()
	if asset := f.cached[name]; asset != nil {
		f.mu.Unlock()
		return asset
	}
	if until, ok := f.misses[name]; ok && time.Now().Before(until) {
		f.mu.Unlock()
		return nil
	}
	if call := f.inflight[name]; call != nil {
		f.mu.Unlock()
		select {
		case <-call.done:
			return call.asset
		case <-ctx.Done():
			return nil
		}
	}
	call := &assetFetch{done: make(chan struct{})}
	f.inflight[name] = call
	f.mu.Unlock()

	// The fetch outlives a client that gives up, so the others waiting on it
	// still get the file.
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*assetFetchTimeout)
	asset := f.fetchFromPeers(fetchCtx, name)
	if asset == nil {
		asset = f.fetchFromURLs(fetchCtx, name)
	}
	cancel()

	f.mu.Lock()
	delete(f.inflight, name)
	if asset != nil {
		f.remember(name, asset)
	} else {
		f.misses[name] = time.Now().Add(assetMissTTL)
		for key, until := range f.misses {
			if time.Now().After(until) {
				delete(f.misses, key)
			}
		}
	}
	call.asset = asset
	close(call.done)
	f.mu.Unlock()
	return asset
}

// remember keeps asset in the cache, evicting the oldest entries past the
// byte budget. Callers hold f.mu.
func (f *assetFallback) remember(name string, asset *fetchedAsset) {
	if len(asset.body) > assetCacheBytes {
		return
	}
	f.cached[name] = asset
	f.order = append(f.order, name)
	f.bytes += len(asset.body)
	for f.bytes > assetCacheBytes && len(f.order) > 0 {
		oldest := f.order[0]
		f.order = f.order[1:]
		if entry := f.cached[oldest]; entry != nil {
			f.bytes -= len(entry.body)
			delete(f.cached, oldest)
		}
	}
}

func (f *assetFallback) fetchFromPeers(ctx context.Context, name string) *fetchedAsset {
	var targets []string
	for _, peer := range f.peers {
		host, port, err := net.SplitHostPort(peer)
		if err != nil {
			continue
		}
		addresses, err := f.lookupHost(ctx, host)
		if err != nil {
			log.Printf("asset fallback: resolve peers %s: %v", host, err)
			continue
		}
		for _, address := range addresses {
			if len(targets) < assetMaxPeers {
				targets = append(targets, "http://"+net.JoinHostPort(address, port))
			}
		}
	}
	if len(targets) == 0 {
		return nil
	}
	// Ask every replica at once; the first that has the file answers.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan *fetchedAsset, len(targets))
	for _, target := range targets {
		go func(target string) {
			results <- f.get(ctx, f.peerClient, target+"/assets/"+name, "peer", true)
		}(target)
	}
	for range targets {
		if asset := <-results; asset != nil {
			return asset
		}
	}
	return nil
}

func (f *assetFallback) fetchFromURLs(ctx context.Context, name string) *fetchedAsset {
	for _, base := range f.urls {
		if asset := f.get(ctx, f.urlClient, strings.TrimRight(base, "/")+"/assets/"+name, "url", false); asset != nil {
			return asset
		}
	}
	return nil
}

// get accepts only a complete 200 that is not an HTML page: a server without
// this fallback (or a misconfigured origin) may answer a missing file with
// the app's index.html, which must never be served as a script.
func (f *assetFallback) get(ctx context.Context, client *http.Client, target, source string, peer bool) *fetchedAsset {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil
	}
	if peer {
		request.Header.Set(assetPeerHeader, "1")
	}
	response, err := client.Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return nil
	}
	contentType := response.Header.Get("Content-Type")
	if media, _, _ := mime.ParseMediaType(contentType); media == "text/html" {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, assetMaxBytes+1))
	if err != nil || len(body) > assetMaxBytes {
		return nil
	}
	if response.ContentLength >= 0 && int64(len(body)) != response.ContentLength {
		return nil
	}
	if contentType == "" || strings.HasPrefix(contentType, "application/octet-stream") || strings.HasPrefix(contentType, "text/plain") {
		// Object stores often label everything generically; the extension decides.
		contentType = ""
	}
	return &fetchedAsset{body: body, contentType: contentType, source: source}
}

// releaseManifest is dist/asset-manifest.json, written by the web build
// (web/release-manifest-plugin.ts): the build's id and its assets/ files.
type releaseManifest struct {
	Release string   `json:"release"`
	Assets  []string `json:"assets"`
}

// releaseID is the id of a release with these asset names, the same value
// the web build writes (sha256 of the sorted names joined by newlines).
func releaseID(names []string) string {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(sum[:8])
}

// readRelease describes the build in staticPath: its manifest, or a listing
// of its assets/ directory when the build has none.
func readRelease(staticPath string) (releaseManifest, error) {
	var manifest releaseManifest
	if data, err := os.ReadFile(filepath.Join(staticPath, assetManifestName)); err == nil {
		if err := json.Unmarshal(data, &manifest); err != nil {
			return manifest, fmt.Errorf("read %s: %w", assetManifestName, err)
		}
	} else {
		entries, err := os.ReadDir(filepath.Join(staticPath, "assets"))
		if err != nil {
			return manifest, err
		}
		for _, entry := range entries {
			if entry.Type().IsRegular() {
				manifest.Assets = append(manifest.Assets, entry.Name())
			}
		}
	}
	valid := manifest.Assets[:0]
	for _, name := range manifest.Assets {
		if assetNamePattern.MatchString(name) && !strings.Contains(name, "..") {
			valid = append(valid, name)
		}
	}
	manifest.Assets = valid
	if manifest.Release == "" {
		manifest.Release = releaseID(manifest.Assets)
	}
	if !assetNamePattern.MatchString(manifest.Release) {
		return manifest, fmt.Errorf("invalid release id %q", manifest.Release)
	}
	return manifest, nil
}

// publishSharedAssets copies this build's assets into the shared directory,
// then prunes releases beyond the newest keep. The release's manifest is
// written first, so a replica pruning concurrently keeps its files; files are
// written to a temporary name and renamed, so a reader never sees half a file.
// Assets are never changed in place: a name is a content hash.
func publishSharedAssets(staticPath, sharedPath string, keep int, now time.Time) (releaseManifest, error) {
	manifest, err := readRelease(staticPath)
	if err != nil {
		return manifest, err
	}
	assets := filepath.Join(sharedPath, "assets")
	releases := filepath.Join(sharedPath, "releases")
	for _, dir := range []string{assets, releases} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return manifest, err
		}
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return manifest, err
	}
	if err := writeFileAtomic(filepath.Join(releases, manifest.Release+".json"), bytes.NewReader(data)); err != nil {
		return manifest, err
	}
	_ = os.Chtimes(filepath.Join(releases, manifest.Release+".json"), now, now)
	for _, name := range manifest.Assets {
		target := filepath.Join(assets, name)
		source := filepath.Join(staticPath, "assets", name)
		sourceInfo, err := os.Stat(source)
		if err != nil {
			return manifest, err
		}
		if info, err := os.Stat(target); err == nil && info.Mode().IsRegular() && info.Size() == sourceInfo.Size() {
			_ = os.Chtimes(target, now, now)
			continue
		}
		file, err := os.Open(source)
		if err != nil {
			return manifest, err
		}
		err = writeFileAtomic(target, file)
		file.Close()
		if err != nil {
			return manifest, err
		}
	}
	return manifest, pruneSharedAssets(sharedPath, keep, now)
}

// pruneSharedAssets keeps the files of the newest keep releases (by the time
// each last published) and anything written in the last hour.
func pruneSharedAssets(sharedPath string, keep int, now time.Time) error {
	releasesDir := filepath.Join(sharedPath, "releases")
	entries, err := os.ReadDir(releasesDir)
	if err != nil {
		return err
	}
	type release struct {
		path     string
		modified time.Time
	}
	var releases []release
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		releases = append(releases, release{path: filepath.Join(releasesDir, entry.Name()), modified: info.ModTime()})
	}
	sort.Slice(releases, func(i, j int) bool { return releases[i].modified.After(releases[j].modified) })
	referenced := map[string]bool{}
	for i, item := range releases {
		if i >= keep && now.Sub(item.modified) > assetPublishGrace {
			_ = os.Remove(item.path)
			continue
		}
		data, err := os.ReadFile(item.path)
		var manifest releaseManifest
		if err == nil {
			err = json.Unmarshal(data, &manifest)
		}
		if err != nil {
			// An unreadable manifest of a kept release: delete nothing.
			return fmt.Errorf("read %s: %w", item.path, err)
		}
		for _, name := range manifest.Assets {
			referenced[name] = true
		}
	}
	assetEntries, err := os.ReadDir(filepath.Join(sharedPath, "assets"))
	if err != nil {
		return err
	}
	for _, entry := range assetEntries {
		if referenced[entry.Name()] {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) <= assetPublishGrace {
			continue
		}
		_ = os.Remove(filepath.Join(sharedPath, "assets", entry.Name()))
	}
	return nil
}

func writeFileAtomic(target string, body io.Reader) error {
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	temp := filepath.Join(filepath.Dir(target), ".tmp-"+filepath.Base(target)+"-"+hex.EncodeToString(suffix))
	file, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(file, body)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp, target)
	}
	if err != nil {
		_ = os.Remove(temp)
	}
	return err
}

// startSharedAssetPublish publishes this build's assets to the shared
// directory before the server takes traffic, waiting at most wait so a slow
// volume cannot hold startup; publishing then finishes in the background.
func startSharedAssetPublish(staticPath string, config appconfig.AssetsConfig, wait time.Duration) {
	if staticPath == "" || config.SharedPath == "" {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		manifest, err := publishSharedAssets(staticPath, config.SharedPath, config.SharedKeepReleases, time.Now())
		if err != nil {
			log.Printf("WARNING: asset publish to FLOW_ASSET_SHARED_PATH: %v", err)
			return
		}
		log.Printf("published web assets release=%s files=%d to %s", manifest.Release, len(manifest.Assets), config.SharedPath)
	}()
	select {
	case <-done:
	case <-time.After(wait):
		log.Printf("WARNING: asset publish to FLOW_ASSET_SHARED_PATH still running after %s; serving meanwhile", wait)
	}
}
