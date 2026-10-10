package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// File previews back the rich-text editor's "Embed file preview" for GitHub
// (and GitLab) blob links: GET /api/integrations/file-preview?url=... returns
// the lines of the linked file (or of its #L range) so the editor can render
// them as a highlighted snippet. Only github.com blob URLs and blob URLs on a
// GitLab instance the workspace connected (or gitlab.com) are fetched, and the
// outbound request goes to a host Flow chooses (api.github.com,
// raw.githubusercontent.com or the connected GitLab API), never to the host the
// URL names, so the endpoint cannot be used to reach arbitrary addresses.

const (
	filePreviewMaxURLLength     = 2048
	filePreviewMaxBytes         = 1 << 20
	filePreviewMaxLines         = 1000
	filePreviewMaxLineLength    = 2000
	filePreviewMaxRefCandidates = 3
	filePreviewTimeout          = 8 * time.Second
	filePreviewCacheTTL         = 2 * time.Minute
	filePreviewNegativeTTL      = 30 * time.Second
	filePreviewCacheEntries     = 128
	filePreviewCacheBytes       = 16 << 20
)

var (
	githubFilePreviewAPIBase = "https://api.github.com"
	githubFilePreviewRawBase = "https://raw.githubusercontent.com"
	// filePreviewHTTPClient builds the client for GitHub requests. It only dials
	// public addresses and follows redirects on the same host; tests replace it
	// to reach a loopback fake.
	filePreviewHTTPClient = func() *http.Client {
		client := secureOutboundClient(filePreviewTimeout)
		client.CheckRedirect = filePreviewSameHostRedirect
		return client
	}
	filePreviewLineRange   = regexp.MustCompile(`^L(\d{1,7})(?:C\d{1,7})?(?:-L?(\d{1,7})(?:C\d{1,7})?)?$`)
	githubOwnerPattern     = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	githubRepoPattern      = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	gitlabNamespacePattern = regexp.MustCompile(`^[A-Za-z0-9_.+][A-Za-z0-9_.+-]{0,254}$`)
)

func filePreviewSameHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 || len(via) > 0 && !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
		return errors.New("unsafe redirect")
	}
	return nil
}

// filePreviewTarget is a validated blob URL.
type filePreviewTarget struct {
	Provider string // "github" or "gitlab"
	// Prefix is the URL up to the repository: https://github.com/owner/repo,
	// or for GitLab everything before "/-/blob/".
	Prefix    string
	Owner     string // GitHub
	Repo      string // GitHub repo name, or the GitLab project path
	Segments  []string
	StartLine int
	EndLine   int
}

type filePreviewError struct {
	Status  int
	Code    string
	Message string
}

func (e *filePreviewError) Error() string { return e.Message }

func previewError(status int, code, message string) *filePreviewError {
	return &filePreviewError{Status: status, Code: code, Message: message}
}

var (
	errFilePreviewInvalid      = previewError(http.StatusBadRequest, "invalid_url", "Only GitHub and GitLab file links can be previewed")
	errFilePreviewNotConnected = previewError(http.StatusNotFound, "not_connected", "Connect the repository integration to preview this file")
	errFilePreviewNoAccess     = previewError(http.StatusNotFound, "no_access", "The file was not found or the integration has no access to it")
	errFilePreviewTooLarge     = previewError(http.StatusRequestEntityTooLarge, "too_large", "The file is too large to preview")
	errFilePreviewUnsupported  = previewError(http.StatusUnsupportedMediaType, "unsupported", "Only text files can be previewed")
	errFilePreviewRateLimited  = previewError(http.StatusTooManyRequests, "rate_limited", "The repository host is rate limiting requests; try again shortly")
	errFilePreviewOutOfRange   = previewError(http.StatusRequestedRangeNotSatisfiable, "out_of_range", "The linked lines are outside the file")
	errFilePreviewUpstream     = previewError(http.StatusBadGateway, "upstream", "Could not load the file from the repository host")
)

// filePreviewSegment accepts one decoded path segment of a ref or file path.
func filePreviewSegment(value string) bool {
	if value == "" || value == "." || value == ".." || len(value) > 255 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return false
		}
	}
	return true
}

func splitFilePreviewPath(escaped string) ([]string, bool) {
	parts := strings.Split(strings.Trim(escaped, "/"), "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil || strings.Contains(decoded, "/") {
			return nil, false
		}
		segments = append(segments, decoded)
	}
	return segments, true
}

// parseFilePreviewLines reads a #L10, #L10-L20 (GitHub) or #L10-20 (GitLab)
// fragment. Zero values mean "the whole file".
func parseFilePreviewLines(fragment string) (int, int) {
	match := filePreviewLineRange.FindStringSubmatch(fragment)
	if match == nil {
		return 0, 0
	}
	start, _ := strconv.Atoi(match[1])
	end := start
	if match[2] != "" {
		end, _ = strconv.Atoi(match[2])
	}
	if start < 1 {
		return 0, 0
	}
	if end < start {
		start, end = end, start
		if start < 1 {
			start = 1
		}
	}
	return start, end
}

// parseFilePreviewURL validates a GitHub or GitLab blob URL.
func parseFilePreviewURL(raw string) (filePreviewTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > filePreviewMaxURLLength {
		return filePreviewTarget{}, errFilePreviewInvalid
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Hostname() == "" || u.Opaque != "" {
		return filePreviewTarget{}, errFilePreviewInvalid
	}
	host := strings.ToLower(u.Hostname())
	start, end := parseFilePreviewLines(u.Fragment)
	escaped := u.EscapedPath()
	if host == "github.com" || host == "www.github.com" {
		segments, ok := splitFilePreviewPath(escaped)
		if !ok || u.Port() != "" || len(segments) < 5 || segments[2] != "blob" || !githubOwnerPattern.MatchString(segments[0]) || !githubRepoPattern.MatchString(segments[1]) || segments[1] == "." || segments[1] == ".." {
			return filePreviewTarget{}, errFilePreviewInvalid
		}
		rest := segments[3:]
		for _, segment := range rest {
			if !filePreviewSegment(segment) {
				return filePreviewTarget{}, errFilePreviewInvalid
			}
		}
		return filePreviewTarget{Provider: "github", Prefix: "https://github.com/" + segments[0] + "/" + segments[1], Owner: segments[0], Repo: segments[1], Segments: rest, StartLine: start, EndLine: end}, nil
	}
	before, after, found := strings.Cut(escaped, "/-/blob/")
	if !found || u.Scheme != "https" && !(u.Scheme == "http" && safeLocalDevelopmentURL(raw)) {
		return filePreviewTarget{}, errFilePreviewInvalid
	}
	project, ok := splitFilePreviewPath(before)
	if !ok || len(project) < 2 {
		return filePreviewTarget{}, errFilePreviewInvalid
	}
	for _, segment := range project {
		if !gitlabNamespacePattern.MatchString(segment) || segment == "." || segment == ".." {
			return filePreviewTarget{}, errFilePreviewInvalid
		}
	}
	rest, ok := splitFilePreviewPath(after)
	if !ok || len(rest) < 2 {
		return filePreviewTarget{}, errFilePreviewInvalid
	}
	for _, segment := range rest {
		if !filePreviewSegment(segment) {
			return filePreviewTarget{}, errFilePreviewInvalid
		}
	}
	prefix := u.Scheme + "://" + strings.ToLower(u.Host) + strings.TrimRight(before, "/")
	return filePreviewTarget{Provider: "gitlab", Prefix: prefix, Segments: rest, StartLine: start, EndLine: end}, nil
}

func escapeFilePreviewPath(segments []string) string {
	escaped := make([]string, len(segments))
	for index, segment := range segments {
		escaped[index] = url.PathEscape(segment)
	}
	return strings.Join(escaped, "/")
}

// filePreviewHTMLURL is the canonical link back to the file on the host.
func filePreviewHTMLURL(target filePreviewTarget, ref, filePath string, start, end int) string {
	separator := "/blob/"
	if target.Provider == "gitlab" {
		separator = "/-/blob/"
	}
	link := target.Prefix + separator + escapeFilePreviewPath(strings.Split(ref, "/")) + "/" + escapeFilePreviewPath(strings.Split(filePath, "/"))
	if start > 0 {
		link += "#L" + strconv.Itoa(start)
		if end > start {
			if target.Provider == "gitlab" {
				link += "-" + strconv.Itoa(end)
			} else {
				link += "-L" + strconv.Itoa(end)
			}
		}
	}
	return link
}

var filePreviewLanguages = map[string]string{
	".go": "go", ".js": "javascript", ".mjs": "javascript", ".cjs": "javascript", ".jsx": "javascript", ".ts": "typescript", ".tsx": "typescript", ".mts": "typescript", ".cts": "typescript",
	".py": "python", ".rb": "ruby", ".rs": "rust", ".java": "java", ".kt": "kotlin", ".kts": "kotlin", ".swift": "swift", ".c": "c", ".h": "c",
	".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".hpp": "cpp", ".hh": "cpp", ".cs": "csharp", ".css": "css", ".scss": "scss", ".sass": "scss", ".less": "less",
	".html": "xml", ".htm": "xml", ".xml": "xml", ".svg": "xml", ".vue": "xml", ".json": "json", ".jsonc": "json", ".yaml": "yaml", ".yml": "yaml", ".toml": "ini", ".ini": "ini", ".cfg": "ini",
	".md": "markdown", ".markdown": "markdown", ".mdx": "markdown", ".sh": "bash", ".bash": "bash", ".zsh": "bash", ".sql": "sql", ".php": "php", ".pl": "perl", ".pm": "perl",
	".lua": "lua", ".r": "r", ".scala": "scala", ".dart": "dart", ".ex": "elixir", ".exs": "elixir", ".hs": "haskell", ".clj": "clojure", ".cljs": "clojure",
	".proto": "protobuf", ".ps1": "powershell", ".graphql": "graphql", ".gql": "graphql", ".diff": "diff", ".patch": "diff", ".m": "objectivec", ".mm": "objectivec", ".vb": "vbnet",
	".txt": "plaintext",
}

// filePreviewLanguage maps a file name to the code block highlighter's
// language id; empty lets the client auto-detect.
func filePreviewLanguage(filePath string) string {
	name := strings.ToLower(path.Base(filePath))
	switch {
	case name == "dockerfile" || strings.HasPrefix(name, "dockerfile.") || strings.HasSuffix(name, ".dockerfile"):
		return "dockerfile"
	case name == "makefile" || name == "gnumakefile" || strings.HasSuffix(name, ".mk"):
		return "makefile"
	case name == "nginx.conf":
		return "nginx"
	case name == "go.mod" || name == "go.sum":
		return "plaintext"
	}
	return filePreviewLanguages[path.Ext(name)]
}

type filePreviewResponse struct {
	Provider   string   `json:"provider"`
	Repo       string   `json:"repo"`
	Path       string   `json:"path"`
	Ref        string   `json:"ref"`
	Language   string   `json:"language"`
	Lines      []string `json:"lines"`
	StartLine  int      `json:"startLine"`
	EndLine    int      `json:"endLine"`
	TotalLines int      `json:"totalLines"`
	Truncated  bool     `json:"truncated"`
	HTMLURL    string   `json:"htmlUrl"`
}

// filePreviewFile is a fetched file (or the error fetching it), as cached.
type filePreviewFile struct {
	content   string
	ref, path string
	err       *filePreviewError
	expires   time.Time
	seq       uint64
}

type filePreviewCacheKey struct {
	key string
	seq uint64
}

type filePreviewCall struct {
	done chan struct{}
	file filePreviewFile
}

type filePreviewCacheStore struct {
	mu       sync.Mutex
	entries  map[string]filePreviewFile
	order    []filePreviewCacheKey
	bytes    int
	seq      uint64
	inflight map[string]*filePreviewCall
}

var filePreviewCache = &filePreviewCacheStore{entries: map[string]filePreviewFile{}, inflight: map[string]*filePreviewCall{}}

func (c *filePreviewCacheStore) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries, c.order, c.bytes, c.inflight = map[string]filePreviewFile{}, nil, 0, map[string]*filePreviewCall{}
}

func (c *filePreviewCacheStore) dropLocked(key string) {
	if entry, ok := c.entries[key]; ok {
		c.bytes -= len(entry.content)
		delete(c.entries, key)
	}
}

// load returns the cached file for key, or runs fetch once for concurrent
// callers and caches its result for a short time.
func (c *filePreviewCacheStore) load(key string, fetch func() filePreviewFile) filePreviewFile {
	now := time.Now()
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok {
		if now.Before(entry.expires) {
			c.mu.Unlock()
			return entry
		}
		c.dropLocked(key)
	}
	if call, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		<-call.done
		return call.file
	}
	call := &filePreviewCall{done: make(chan struct{})}
	c.inflight[key] = call
	c.mu.Unlock()

	file := fetch()
	ttl := filePreviewCacheTTL
	if file.err != nil {
		ttl = filePreviewNegativeTTL
	}
	file.expires = time.Now().Add(ttl)

	c.mu.Lock()
	delete(c.inflight, key)
	cacheable := file.err == nil || file.err.Code != errFilePreviewUpstream.Code && file.err.Code != errFilePreviewRateLimited.Code
	if cacheable && len(file.content) <= filePreviewCacheBytes/4 {
		c.dropLocked(key)
		c.seq++
		file.seq = c.seq
		c.entries[key] = file
		c.order = append(c.order, filePreviewCacheKey{key: key, seq: file.seq})
		c.bytes += len(file.content)
		for (len(c.entries) > filePreviewCacheEntries || c.bytes > filePreviewCacheBytes) && len(c.order) > 0 {
			oldest := c.order[0]
			c.order = c.order[1:]
			if entry, ok := c.entries[oldest.key]; ok && entry.seq == oldest.seq {
				c.dropLocked(oldest.key)
			}
		}
		if len(c.order) > 4*filePreviewCacheEntries {
			kept := c.order[:0]
			for _, item := range c.order {
				if entry, ok := c.entries[item.key]; ok && entry.seq == item.seq {
					kept = append(kept, item)
				}
			}
			c.order = kept
		}
	}
	c.mu.Unlock()
	call.file = file
	close(call.done)
	return file
}

// filePreviewCredential is how the server may read the repository: a token
// for the provider's API (empty for anonymous access to public files).
type filePreviewCredential struct {
	key   string // cache partition: "public" or the connection id
	token string
	base  string // GitLab: the instance URL the token belongs to
}

func (s *server) githubFilePreviewCredential(data domain.Bootstrap, target filePreviewTarget) (filePreviewCredential, bool) {
	connected := false
	for _, connection := range data.IntegrationConnections {
		if connection.Provider != "github" || connection.Status == "disconnected" {
			continue
		}
		connected = true
		if org := strings.TrimSpace(connection.Config["organization"]); org != "" && !strings.EqualFold(org, target.Owner) {
			continue
		}
		token := strings.TrimSpace(connection.OAuthAccessToken)
		if token == "" {
			token = strings.TrimSpace(os.Getenv("FLOW_INTEGRATION_GITHUB_ACCESS_TOKEN"))
		}
		if token != "" {
			return filePreviewCredential{key: "github:" + connection.ID, token: token}, true
		}
	}
	return filePreviewCredential{key: "public"}, connected
}

func (s *server) gitlabFilePreviewCredential(ctx context.Context, data domain.Bootstrap, target filePreviewTarget) (filePreviewCredential, string, bool) {
	connected := false
	for _, connection := range data.IntegrationConnections {
		if connection.Provider != "gitlab" || connection.Status == "disconnected" {
			continue
		}
		token := strings.TrimSpace(connection.OAuthAccessToken)
		base, err := normalizeGitLabURL(connection.Config["host"], s.authDisabled)
		if token == "" {
			if credential, credentialErr := s.gitlabCredential(ctx, data.Workspace.URLKey, connection.ID); credentialErr == nil {
				token, base, err = credential.Token, credential.URL, nil
			}
		}
		if err != nil || !strings.HasPrefix(target.Prefix+"/", base+"/") {
			continue
		}
		connected = true
		if token != "" {
			return filePreviewCredential{key: "gitlab:" + connection.ID, token: token, base: base}, strings.TrimPrefix(target.Prefix, base+"/"), true
		}
	}
	// gitlab.com serves public projects without a token.
	if strings.HasPrefix(target.Prefix+"/", gitlabDefaultURL+"/") {
		return filePreviewCredential{key: "public", base: gitlabDefaultURL}, strings.TrimPrefix(target.Prefix, gitlabDefaultURL+"/"), connected
	}
	return filePreviewCredential{}, "", connected
}

// filePreviewGet performs one GET and maps the response onto the preview
// errors. A nil error with ok=false means "not found here" (try the next ref
// split or the anonymous fallback).
func filePreviewGet(ctx context.Context, client *http.Client, endpoint string, headers map[string]string) (string, bool, *filePreviewError) {
	ctx, cancel := context.WithTimeout(ctx, filePreviewTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", false, errFilePreviewInvalid
	}
	req.Header.Set("User-Agent", "Flow-File-Preview")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	response, err := client.Do(req)
	if err != nil {
		return "", false, errFilePreviewUpstream
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0":
		return "", false, errFilePreviewRateLimited
	case response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return "", false, nil
	case response.StatusCode < 200 || response.StatusCode >= 300:
		return "", false, errFilePreviewUpstream
	}
	if response.ContentLength > filePreviewMaxBytes {
		return "", false, errFilePreviewTooLarge
	}
	if mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type")); err == nil {
		for _, prefix := range []string{"image/", "audio/", "video/", "font/", "application/pdf", "application/zip", "application/gzip", "application/x-"} {
			if strings.HasPrefix(mediaType, prefix) {
				return "", false, errFilePreviewUnsupported
			}
		}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, filePreviewMaxBytes+1))
	if err != nil {
		return "", false, errFilePreviewUpstream
	}
	if len(body) > filePreviewMaxBytes {
		return "", false, errFilePreviewTooLarge
	}
	sniff := body
	if len(sniff) > 8000 {
		sniff = sniff[:8000]
	}
	if bytes.IndexByte(sniff, 0) >= 0 || !utf8.Valid(body) {
		return "", false, errFilePreviewUnsupported
	}
	return string(body), true, nil
}

// filePreviewRefSplits lists the (ref, path) readings of the segments after
// /blob/: branch names may contain slashes, so the first few splits are tried
// in order.
func filePreviewRefSplits(segments []string) [][2]string {
	splits := [][2]string{}
	for index := 1; index < len(segments) && index <= filePreviewMaxRefCandidates; index++ {
		splits = append(splits, [2]string{strings.Join(segments[:index], "/"), strings.Join(segments[index:], "/")})
	}
	return splits
}

func fetchGitHubFilePreview(ctx context.Context, target filePreviewTarget, credential filePreviewCredential) filePreviewFile {
	client := filePreviewHTTPClient()
	repo := url.PathEscape(target.Owner) + "/" + url.PathEscape(target.Repo)
	for _, split := range filePreviewRefSplits(target.Segments) {
		ref, filePath := split[0], split[1]
		if credential.token != "" {
			endpoint := githubFilePreviewAPIBase + "/repos/" + repo + "/contents/" + escapeFilePreviewPath(strings.Split(filePath, "/")) + "?ref=" + url.QueryEscape(ref)
			content, ok, err := filePreviewGet(ctx, client, endpoint, map[string]string{"Authorization": "Bearer " + credential.token, "Accept": "application/vnd.github.raw", "X-GitHub-Api-Version": "2022-11-28"})
			if err != nil {
				return filePreviewFile{err: err}
			}
			if ok {
				return filePreviewFile{content: content, ref: ref, path: filePath}
			}
		}
		endpoint := githubFilePreviewRawBase + "/" + repo + "/" + escapeFilePreviewPath(strings.Split(ref, "/")) + "/" + escapeFilePreviewPath(strings.Split(filePath, "/"))
		content, ok, err := filePreviewGet(ctx, client, endpoint, nil)
		if err != nil {
			return filePreviewFile{err: err}
		}
		if ok {
			return filePreviewFile{content: content, ref: ref, path: filePath}
		}
	}
	return filePreviewFile{err: errFilePreviewNoAccess}
}

func (s *server) fetchGitLabFilePreview(ctx context.Context, target filePreviewTarget, credential filePreviewCredential, project string) filePreviewFile {
	client, err := s.gitlabClient(ctx, credential.base)
	if err != nil {
		return filePreviewFile{err: errFilePreviewUpstream}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	headers := map[string]string{}
	if credential.token != "" {
		headers["PRIVATE-TOKEN"] = credential.token
	}
	for _, split := range filePreviewRefSplits(target.Segments) {
		ref, filePath := split[0], split[1]
		endpoint := credential.base + "/api/v4/projects/" + url.PathEscape(project) + "/repository/files/" + url.PathEscape(filePath) + "/raw?ref=" + url.QueryEscape(ref)
		content, ok, fetchErr := filePreviewGet(ctx, client, endpoint, headers)
		if fetchErr != nil {
			return filePreviewFile{err: fetchErr}
		}
		if ok {
			return filePreviewFile{content: content, ref: ref, path: filePath}
		}
	}
	return filePreviewFile{err: errFilePreviewNoAccess}
}

// filePreviewSlice cuts the requested line range out of the file.
func filePreviewSlice(content string, start, end int) (lines []string, first, last, total int, truncated bool, err *filePreviewError) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimSuffix(content, "\n")
	all := []string{}
	if content != "" {
		all = strings.Split(content, "\n")
	}
	total = len(all)
	if start == 0 {
		start, end = 1, total
	} else {
		if start > total {
			return nil, 0, 0, total, false, errFilePreviewOutOfRange
		}
		if end == 0 || end < start {
			end = start
		}
		end = min(end, total)
	}
	if total == 0 {
		return []string{}, 0, 0, 0, false, nil
	}
	if end-start+1 > filePreviewMaxLines {
		end = start + filePreviewMaxLines - 1
		truncated = true
	}
	lines = make([]string, 0, end-start+1)
	for _, line := range all[start-1 : end] {
		if len(line) > filePreviewMaxLineLength {
			cut := filePreviewMaxLineLength
			for cut > 0 && !utf8.RuneStart(line[cut]) {
				cut--
			}
			line = line[:cut]
			truncated = true
		}
		lines = append(lines, line)
	}
	return lines, start, end, total, truncated, nil
}

func writeFilePreviewError(w http.ResponseWriter, err error) {
	var preview *filePreviewError
	if !errors.As(err, &preview) {
		preview = errFilePreviewUpstream
	}
	writeJSON(w, preview.Status, map[string]string{"error": preview.Message, "code": preview.Code})
}

// filePreview serves GET /api/integrations/file-preview?url=<blob URL>.
func (s *server) filePreview(w http.ResponseWriter, r *http.Request) {
	target, err := parseFilePreviewURL(r.URL.Query().Get("url"))
	if err != nil {
		writeFilePreviewError(w, err)
		return
	}
	data := s.workspaceData(r)
	// Guests and API keys never borrow the workspace's repository credentials;
	// they still see public files.
	trusted := data.ViewerRole != "guest"
	if _, apiKey := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); apiKey {
		trusted = false
	}
	var (
		credential filePreviewCredential
		connected  bool
		project    string
	)
	switch target.Provider {
	case "github":
		credential, connected = s.githubFilePreviewCredential(data, target)
	case "gitlab":
		credential, project, connected = s.gitlabFilePreviewCredential(r.Context(), data, target)
		if credential.base == "" {
			// A self-hosted GitLab the workspace has not connected is never contacted.
			writeFilePreviewError(w, errFilePreviewNotConnected)
			return
		}
	}
	if !trusted && credential.token != "" {
		credential = filePreviewCredential{key: "public", base: credential.base}
		if target.Provider == "gitlab" && credential.base != gitlabDefaultURL {
			writeFilePreviewError(w, errFilePreviewNoAccess)
			return
		}
	}
	cacheKey := strings.Join([]string{data.Workspace.URLKey, credential.key, target.Prefix, strings.Join(target.Segments, "/")}, "\x00")
	if credential.key == "public" {
		cacheKey = strings.Join([]string{"public", target.Prefix, strings.Join(target.Segments, "/")}, "\x00")
	}
	file := filePreviewCache.load(cacheKey, func() filePreviewFile {
		// The result is shared with concurrent callers, so it does not follow this
		// request's cancellation; it has its own overall deadline.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*filePreviewTimeout)
		defer cancel()
		if target.Provider == "gitlab" {
			return s.fetchGitLabFilePreview(ctx, target, credential, project)
		}
		return fetchGitHubFilePreview(ctx, target, credential)
	})
	if file.err != nil {
		if file.err == errFilePreviewNoAccess && credential.token == "" && !connected {
			writeFilePreviewError(w, errFilePreviewNotConnected)
			return
		}
		writeFilePreviewError(w, file.err)
		return
	}
	lines, first, last, total, truncated, sliceErr := filePreviewSlice(file.content, target.StartLine, target.EndLine)
	if sliceErr != nil {
		writeFilePreviewError(w, sliceErr)
		return
	}
	repo := target.Owner + "/" + target.Repo
	if target.Provider == "gitlab" {
		repo = project
	}
	w.Header().Set("Cache-Control", "private, max-age=60")
	writeJSON(w, http.StatusOK, filePreviewResponse{
		Provider:   target.Provider,
		Repo:       repo,
		Path:       file.path,
		Ref:        file.ref,
		Language:   filePreviewLanguage(file.path),
		Lines:      lines,
		StartLine:  first,
		EndLine:    last,
		TotalLines: total,
		Truncated:  truncated,
		HTMLURL:    filePreviewHTMLURL(target, file.ref, file.path, target.StartLine, max(target.EndLine, target.StartLine)),
	})
}

// String keeps targets readable in test failures.
func (t filePreviewTarget) String() string {
	return fmt.Sprintf("%s %s %v L%d-L%d", t.Provider, t.Prefix, t.Segments, t.StartLine, t.EndLine)
}
