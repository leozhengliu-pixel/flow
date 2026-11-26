package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/objectstore"
	"flow/api/internal/outbound"
	"flow/api/internal/store"
)

func testLinkFaviconPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	img.SetNRGBA(8, 8, color.NRGBA{R: 40, G: 130, B: 120, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newLinkFaviconTestSite(t *testing.T, handler http.Handler) string {
	t.Helper()
	site := httptest.NewServer(handler)
	t.Cleanup(site.Close)
	const host = "favicon.example.test"
	const ip = "93.184.216.34"
	localAddress := site.Listener.Addr().String()
	publicAddress := strings.Replace(localAddress, "127.0.0.1", ip, 1)
	previous := outboundPolicy
	outboundPolicy = outbound.Default().WithResolver(migrationFetchFakeDNS{host: ip}).
		WithDialer(func(ctx context.Context, network, address string) (net.Conn, error) {
			if address == publicAddress {
				address = localAddress
			}
			// A loopback request would reach its fixture if the policy let
			// it through, so the refusal tests check the real policy.
			if !strings.HasPrefix(address, "127.0.0.1:") {
				return nil, io.ErrUnexpectedEOF
			}
			return (&net.Dialer{}).DialContext(ctx, network, address)
		})
	t.Cleanup(func() { outboundPolicy = previous })
	return strings.Replace(site.URL, "127.0.0.1", host, 1)
}

func faviconForTest(t *testing.T, raw string) linkFaviconResult {
	t.Helper()
	site, ok := linkFaviconSite(raw)
	if !ok {
		t.Fatalf("linkFaviconSite(%q) refused", raw)
	}
	return linkFaviconBytes(context.Background(), site)
}

func TestLinkFaviconUsesDeclaredIcon(t *testing.T) {
	pngBytes := testLinkFaviconPNG(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Error("saved link query reached favicon discovery")
		}
		http.Redirect(w, r, "/docs/page", http.StatusFound)
	})
	mux.HandleFunc("/docs/page", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><link rel="mask-icon" href="/mask.svg"><link rel="shortcut icon" href="../assets/icon.png"></head><body></body></html>`))
	})
	mux.HandleFunc("/assets/icon.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(pngBytes)
	})
	site := newLinkFaviconTestSite(t, mux)
	result := faviconForTest(t, site+"/example/document?token=fixture")
	if !bytes.Equal(result.Icon, pngBytes) || result.ContentType != "image/png" {
		t.Fatalf("declared icon = %d bytes with type %q", len(result.Icon), result.ContentType)
	}
}

func TestLinkFaviconFallsBackToFaviconICO(t *testing.T) {
	ico := append([]byte{0, 0, 1, 0, 1, 0}, make([]byte, 32)...)
	site := newLinkFaviconTestSite(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			_, _ = w.Write(ico)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head></head><body></body></html>`))
	}))
	result := faviconForTest(t, site)
	if result.ContentType != "image/x-icon" {
		t.Fatalf("favicon.ico type = %q", result.ContentType)
	}
}

func TestLinkFaviconRejectsOversizedAndNonImages(t *testing.T) {
	for name, body := range map[string][]byte{
		"oversized": append(testLinkFaviconPNG(t), make([]byte, linkFaviconMaxIconBytes)...),
		"svg":       []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"html":      []byte(`<html><body>not found</body></html>`),
	} {
		t.Run(name, func(t *testing.T) {
			site := newLinkFaviconTestSite(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/favicon.ico" {
					w.Header().Set("Content-Type", "image/png")
					_, _ = w.Write(body)
					return
				}
				http.NotFound(w, r)
			}))
			if result := faviconForTest(t, site); len(result.Icon) != 0 {
				t.Fatal("rejected bytes returned an icon")
			}
		})
	}
}

func TestLinkFaviconBoundsHTMLDiscovery(t *testing.T) {
	site := newLinkFaviconTestSite(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<head><!--`+strings.Repeat("x", linkFaviconMaxPageBytes)+`--><link rel="icon" href="/too-late.png"></head>`)
		case "/too-late.png":
			t.Error("discovered a declaration beyond the page size limit")
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	if result := faviconForTest(t, site); len(result.Icon) != 0 {
		t.Fatal("oversized HTML returned an icon")
	}
}

func TestLinkFaviconNeverReachesPrivateAddresses(t *testing.T) {
	pngBytes := testLinkFaviconPNG(t)
	var hits atomic.Int32
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(pngBytes)
	}))
	defer private.Close()
	var redirects atomic.Int32
	public := newLinkFaviconTestSite(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirects.Add(1)
		http.Redirect(w, r, private.URL+"/internal.png", http.StatusFound)
	}))
	for _, site := range []string{private.URL, public} {
		if result := faviconForTest(t, site); len(result.Icon) != 0 || hits.Load() != 0 {
			t.Fatalf("loopback fetched icon=%d hits=%d", len(result.Icon), hits.Load())
		}
	}
	if redirects.Load() == 0 {
		t.Fatal("fixture did not exercise redirects")
	}
}

func TestLinkFaviconSkipsBrandedSitesAndOtherSchemes(t *testing.T) {
	for _, raw := range []string{"https://github.com/a/b/issues/1", "https://www.figma.com/file/x", "https://acme.slack.com/archives/C1/p1", "ftp://example.com/x", "https://user:password@example.test/", "javascript:alert(1)"} {
		if _, ok := linkFaviconSite(raw); ok {
			t.Fatalf("accepted disallowed link %q", raw)
		}
	}
}

type countingLinkFaviconStore struct {
	objectstore.Store
	puts atomic.Int32
}

func (s *countingLinkFaviconStore) Put(ctx context.Context, name string, body io.Reader, contentType string) (int64, error) {
	s.puts.Add(1)
	return s.Store.Put(ctx, name, body, contentType)
}

type linkFaviconFixture struct {
	handler http.Handler
	storage *countingLinkFaviconStore
	data    domain.Bootstrap
}

func newLinkFaviconFixture(t *testing.T) linkFaviconFixture {
	t.Helper()
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "favicon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	inner, err := objectstore.Open(context.Background(), objectstore.Config{Driver: "local", LocalPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	storage := &countingLinkFaviconStore{Store: inner}
	service := &server{store: repo, uploadPath: t.TempDir(), objectStore: storage, authDisabled: true}
	return linkFaviconFixture{handler: newHandler(service), storage: storage, data: repo.Bootstrap()}
}

func (f linkFaviconFixture) issue(t *testing.T) domain.Issue {
	t.Helper()
	return requestJSON[domain.Issue](t, f.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Favicon", "teamId": f.data.Teams[0].ID}, http.StatusCreated)
}

func (f linkFaviconFixture) link(t *testing.T, issueID, raw string) domain.Attachment {
	t.Helper()
	return requestJSON[domain.Attachment](t, f.handler, http.MethodPost, "/api/issues/"+issueID+"/links", map[string]string{"url": raw}, http.StatusCreated)
}

func (f linkFaviconFixture) icon(issueID, attachmentID string) *httptest.ResponseRecorder {
	query := url.Values{"workspace": {f.data.Workspace.URLKey}, "issueId": {issueID}, "attachmentId": {attachmentID}}
	request := httptest.NewRequest(http.MethodGet, "/api/link-icon?"+query.Encode(), nil)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func TestLinkIconCachesSuccessfulFetchInStorage(t *testing.T) {
	f := newLinkFaviconFixture(t)
	pngBytes := testLinkFaviconPNG(t)
	var hits atomic.Int32
	site := newLinkFaviconTestSite(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/favicon.ico" {
			_, _ = w.Write(pngBytes)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head></head><body>page</body></html>`))
	}))
	issue := f.issue(t)
	link := f.link(t, issue.ID, site+"/page")
	first := f.icon(issue.ID, link.ID)
	if first.Code != http.StatusOK || first.Header().Get("Content-Type") != "image/png" || !bytes.Equal(first.Body.Bytes(), pngBytes) {
		t.Fatalf("first response = %d %q %q", first.Code, first.Header().Get("Content-Type"), first.Body.String())
	}
	if first.Header().Get("Cache-Control") != "private, max-age=604800" || !strings.Contains(strings.Join(first.Header().Values("Vary"), ","), "Cookie") {
		t.Fatalf("cache header = %q", first.Header().Get("Cache-Control"))
	}
	otherLink := f.link(t, issue.ID, site+"/other-page")
	second := f.icon(issue.ID, otherLink.ID)
	if second.Code != http.StatusOK || !bytes.Equal(second.Body.Bytes(), pngBytes) {
		t.Fatalf("second response = %d %q", second.Code, second.Body.String())
	}
	if hits.Load() != 2 {
		t.Fatalf("outbound requests = %d, want 2 on the first fetch only", hits.Load())
	}
	if f.storage.puts.Load() != 2 {
		t.Fatalf("stored objects = %d, want icon and index", f.storage.puts.Load())
	}
	stored := requestJSON[domain.Attachment](t, f.handler, http.MethodGet, "/api/attachments/"+link.ID, nil, http.StatusOK)
	if stored.IconURL != "" {
		t.Fatal("favicon read updated the saved attachment")
	}
}

func TestLinkIconNegativeCacheAvoidsRepeatedFetches(t *testing.T) {
	f := newLinkFaviconFixture(t)
	var hits atomic.Int32
	site := newLinkFaviconTestSite(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/favicon.ico" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head></head><body>page</body></html>`))
	}))
	issue := f.issue(t)
	link := f.link(t, issue.ID, site+"/page")
	if response := f.icon(issue.ID, link.ID); response.Code != http.StatusNotFound {
		t.Fatalf("first miss = %d", response.Code)
	}
	if response := f.icon(issue.ID, link.ID); response.Code != http.StatusNotFound {
		t.Fatalf("second miss = %d", response.Code)
	}
	if hits.Load() != 2 {
		t.Fatalf("outbound requests = %d, want 2 on the first miss only", hits.Load())
	}
}

func TestLinkIconRefusesMissingLinkBeforeOutboundRequest(t *testing.T) {
	f := newLinkFaviconFixture(t)
	var hits atomic.Int32
	site := newLinkFaviconTestSite(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.NotFound(w, r)
	}))
	issue := f.issue(t)
	other := f.issue(t)
	link := f.link(t, other.ID, site+"/page")
	if response := f.icon(issue.ID, link.ID); response.Code != http.StatusNotFound {
		t.Fatalf("link from a different issue = %d", response.Code)
	}
	if response := f.icon("missing-issue", link.ID); response.Code != http.StatusNotFound {
		t.Fatalf("missing issue = %d", response.Code)
	}
	if hits.Load() != 0 {
		t.Fatalf("refused requests made %d outbound requests", hits.Load())
	}
}

func TestLinkIconViewerWithoutIssueAccessCannotTriggerFetch(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "favicon-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	api := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer api.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	private := authRequest[domain.Team](t, admin, http.MethodPost, api.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Favicon private", "key": "FAV", "private": true}, "", http.StatusCreated)
	issue := authRequest[domain.Issue](t, admin, http.MethodPost, api.URL+"/api/issues", map[string]any{"title": "Private favicon", "teamId": private.ID}, "test-workspace", http.StatusCreated)
	pngBytes := testLinkFaviconPNG(t)
	var hits atomic.Int32
	site := newLinkFaviconTestSite(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(pngBytes)
	}))
	link := authRequest[domain.Attachment](t, admin, http.MethodPost, api.URL+"/api/issues/"+issue.ID+"/links", map[string]string{"url": site + "/page"}, "test-workspace", http.StatusCreated)
	member, _ := verifiedAuthClient(t, api.URL, "Favicon viewer", "favicon-no-access@example.com")
	invites := authRequest[[]domain.Invitation](t, admin, http.MethodPost, api.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{"favicon-no-access@example.com"}, "role": "member", "teamIds": []string{}}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, member, http.MethodPost, api.URL+"/api/invitations/accept", map[string]string{"token": invites[0].Token}, "", http.StatusOK)
	query := url.Values{"workspace": {"test-workspace"}, "issueId": {issue.ID}, "attachmentId": {link.ID}}
	iconURL := api.URL + "/api/link-icon?" + query.Encode()
	if status := authRequestStatus(t, member, http.MethodGet, iconURL, nil, ""); status != http.StatusNotFound {
		t.Fatalf("unauthorized issue icon = %d, want 404", status)
	}
	if hits.Load() != 0 {
		t.Fatalf("unauthorized issue made %d outbound requests", hits.Load())
	}
	if status := authRequestStatus(t, admin, http.MethodGet, iconURL, nil, ""); status != http.StatusOK || hits.Load() == 0 {
		t.Fatalf("authorized issue icon = %d, outbound hits = %d", status, hits.Load())
	}
	if status := authRequestStatus(t, member, http.MethodGet, iconURL, nil, ""); status != http.StatusNotFound {
		t.Fatalf("unauthorized cached issue icon = %d, want 404", status)
	}
}

func TestLinkIconRateLimitIsPerUser(t *testing.T) {
	s := &server{apiRateLimiter: newAPIRateLimiter(apiRateLimitConfig{Period: time.Hour}, nil)}
	check := func(userID string) bool {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/link-icon", nil)
		r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, domain.User{ID: userID}))
		return s.allowLinkFaviconRequest(httptest.NewRecorder(), r)
	}
	for range linkFaviconRateRequests {
		if !check("viewer-1") {
			t.Fatal("first viewer was refused before spending their budget")
		}
	}
	if check("viewer-1") || !check("viewer-2") {
		t.Fatal("first viewer's exhausted budget did not stay isolated")
	}
}
