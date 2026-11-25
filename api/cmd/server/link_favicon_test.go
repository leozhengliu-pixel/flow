package main

import (
	"context"
	"io"
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
	"flow/api/internal/store"
)

// pngBytes is a 1x1 PNG.
var pngBytes = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89}

func withPlainFaviconClient(t *testing.T) {
	t.Helper()
	previous := linkFaviconHTTPClient
	linkFaviconHTTPClient = func() *http.Client { return http.DefaultClient }
	t.Cleanup(func() { linkFaviconHTTPClient = previous })
}

func faviconOf(page string) (string, string) {
	parsed, _ := url.Parse(page)
	icon, contentType := linkFaviconBytes(context.Background(), parsed)
	return string(icon), contentType
}

func TestLinkFaviconUsesTheDeclaredIcon(t *testing.T) {
	withPlainFaviconClient(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/docs/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><link rel="mask-icon" href="/mask.svg"><link rel="shortcut icon" href="../assets/icon.png"></head><body></body></html>`))
	})
	mux.HandleFunc("/assets/icon.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(pngBytes)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	if icon, contentType := faviconOf(server.URL + "/docs/page"); icon != string(pngBytes) || contentType != "image/png" {
		t.Fatalf("declared icon not used: %q %q", icon, contentType)
	}
}

func TestLinkFaviconFallsBackToFaviconICO(t *testing.T) {
	withPlainFaviconClient(t)
	ico := append([]byte{0, 0, 1, 0, 1, 0}, make([]byte, 32)...)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			_, _ = w.Write(ico)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>x</title></head><body></body></html>`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	if _, contentType := faviconOf(server.URL + "/"); contentType != "image/x-icon" {
		t.Fatalf("favicon.ico fallback not used: %q", contentType)
	}
}

func TestLinkFaviconRejectsOversizedAndNonImages(t *testing.T) {
	withPlainFaviconClient(t)
	icons := map[string][]byte{
		"oversized": append(append([]byte{}, pngBytes...), make([]byte, linkFaviconMaxIconBytes)...),
		"svg":       []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"html":      []byte(`<html><body>not found</body></html>`),
	}
	for name, body := range icons {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/favicon.ico" {
				_, _ = w.Write(body)
				return
			}
			http.NotFound(w, r)
		}))
		icon, _ := faviconOf(server.URL + "/page")
		server.Close()
		if icon != "" {
			t.Fatalf("%s: unexpected icon", name)
		}
	}
}

func TestLinkFaviconNeverReachesPrivateAddresses(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write(pngBytes)
	}))
	defer server.Close()
	if icon, _ := faviconOf(server.URL + "/page"); icon != "" || hits != 0 {
		t.Fatalf("a loopback site was fetched (hits=%d)", hits)
	}
}

func TestLinkFaviconSkipsBrandedSitesAndOtherSchemes(t *testing.T) {
	withPlainFaviconClient(t)
	for _, raw := range []string{"https://github.com/a/b/issues/1", "https://www.figma.com/file/x", "https://acme.slack.com/archives/C1/p1", "ftp://example.com/x", "javascript:alert(1)"} {
		if icon, _ := faviconOf(raw); icon != "" {
			t.Fatalf("%s: unexpected icon", raw)
		}
	}
}

func TestLinkFaviconObjectNames(t *testing.T) {
	sum := strings.Repeat("a", 64)
	for name, want := range map[string]bool{"linkicon_" + sum + ".png": true, "linkicon_" + sum + ".ico": true, "linkicon_" + sum + ".svg": false, "linkicon_abc.png": false, "../linkicon_" + sum + ".png": false, "attachment_x_" + sum + ".png": false} {
		if isLinkFaviconObject(name) != want {
			t.Fatalf("%s: want %v", name, want)
		}
	}
}

// countingStore counts the objects written to the upload storage.
type countingStore struct {
	objectstore.Store
	puts atomic.Int32
}

func (c *countingStore) Put(ctx context.Context, name string, body io.Reader, contentType string) (int64, error) {
	c.puts.Add(1)
	return c.Store.Put(ctx, name, body, contentType)
}

type faviconFixture struct {
	handler http.Handler
	repo    *store.SQLiteStore
	storage *countingStore
	data    domain.Bootstrap
}

func newFaviconFixture(t *testing.T) faviconFixture {
	t.Helper()
	withPlainFaviconClient(t)
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "favicon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	inner, err := objectstore.Open(context.Background(), objectstore.Config{Driver: "local", LocalPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	counting := &countingStore{Store: inner}
	return faviconFixture{handler: newHandler(&server{store: repo, uploadPath: t.TempDir(), objectStore: counting, authDisabled: true}), repo: repo, storage: counting, data: repo.Bootstrap()}
}

func (f faviconFixture) issue(t *testing.T, title string) domain.Issue {
	t.Helper()
	return requestJSON[domain.Issue](t, f.handler, http.MethodPost, "/api/issues", map[string]any{"title": title, "teamId": f.data.Teams[0].ID}, http.StatusCreated)
}

func (f faviconFixture) attachments(t *testing.T, issueID string) []domain.Attachment {
	t.Helper()
	return requestJSON[connection[domain.Attachment]](t, f.handler, http.MethodGet, "/api/attachments?issueId="+issueID, nil, http.StatusOK).Nodes
}

func waitUntil(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func faviconSite(t *testing.T, hits *atomic.Int32, gate <-chan struct{}) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if gate != nil {
			<-gate
		}
		if r.URL.Path == "/favicon.ico" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head></head><body>page</body></html>`))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestCreatedLinkGetsItsFaviconInTheBackground(t *testing.T) {
	f := newFaviconFixture(t)
	var hits atomic.Int32
	site := faviconSite(t, &hits, nil)
	issue := f.issue(t, "Favicon")
	link := requestJSON[domain.Attachment](t, f.handler, http.MethodPost, "/api/issues/"+issue.ID+"/links", map[string]string{"url": site.URL + "/page"}, http.StatusCreated)
	if link.IconURL != "" {
		t.Fatalf("the response waited for the icon: %q", link.IconURL)
	}
	var icon string
	waitUntil(t, "the favicon", func() bool {
		for _, attachment := range f.attachments(t, issue.ID) {
			if attachment.ID == link.ID {
				icon = attachment.IconURL
			}
		}
		return icon != ""
	})
	if !strings.HasPrefix(icon, "/uploads/linkicon_") || strings.HasPrefix(icon, "data:") {
		t.Fatalf("icon is not a stored object: %q", icon)
	}
	request := httptest.NewRecorder()
	f.handler.ServeHTTP(request, httptest.NewRequest(http.MethodGet, icon, nil))
	if request.Code != http.StatusOK || request.Header().Get("Content-Type") != "image/png" || request.Body.String() != string(pngBytes) {
		t.Fatalf("stored icon is not served: %d %q", request.Code, request.Header().Get("Content-Type"))
	}
}

func TestSlowSiteDoesNotDelayTheLinkAndAnUpdateNeedsTheLinkToExist(t *testing.T) {
	f := newFaviconFixture(t)
	var hits atomic.Int32
	gate := make(chan struct{})
	site := faviconSite(t, &hits, gate)
	issue := f.issue(t, "Slow favicon")
	started := time.Now()
	link := requestJSON[domain.Attachment](t, f.handler, http.MethodPost, "/api/issues/"+issue.ID+"/links", map[string]string{"url": site.URL + "/page"}, http.StatusCreated)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("adding a link waited %v for a slow site", elapsed)
	}
	waitUntil(t, "the background fetch to start", func() bool { return hits.Load() > 0 })
	// The link is removed while the site is still answering: nothing is set afterwards.
	requestJSON[any](t, f.handler, http.MethodDelete, "/api/issues/"+issue.ID+"/attachments/"+link.ID, nil, http.StatusNoContent)
	close(gate)
	waitUntil(t, "the icon to be stored", func() bool { return f.storage.puts.Load() > 0 })
	time.Sleep(200 * time.Millisecond)
	if left := f.attachments(t, issue.ID); len(left) != 0 {
		t.Fatalf("a removed link came back: %+v", left)
	}
}

func TestTheSameIconIsStoredOnce(t *testing.T) {
	f := newFaviconFixture(t)
	var hits atomic.Int32
	first, second := faviconSite(t, &hits, nil), faviconSite(t, &hits, nil)
	issue := f.issue(t, "Duplicate icons")
	links := []domain.Attachment{
		requestJSON[domain.Attachment](t, f.handler, http.MethodPost, "/api/issues/"+issue.ID+"/links", map[string]string{"url": first.URL + "/a"}, http.StatusCreated),
		requestJSON[domain.Attachment](t, f.handler, http.MethodPost, "/api/issues/"+issue.ID+"/links", map[string]string{"url": second.URL + "/b"}, http.StatusCreated),
	}
	icons := map[string]string{}
	waitUntil(t, "both icons", func() bool {
		for _, attachment := range f.attachments(t, issue.ID) {
			if attachment.IconURL != "" {
				icons[attachment.ID] = attachment.IconURL
			}
		}
		return len(icons) == len(links)
	})
	if icons[links[0].ID] != icons[links[1].ID] {
		t.Fatalf("identical icons got different objects: %v", icons)
	}
	if puts := f.storage.puts.Load(); puts != 1 {
		t.Fatalf("the icon was stored %d times, want once", puts)
	}
}

func TestRefusedLinkMakesNoOutboundRequest(t *testing.T) {
	f := newFaviconFixture(t)
	var hits atomic.Int32
	site := faviconSite(t, &hits, nil)
	requestJSON[any](t, f.handler, http.MethodPost, "/api/issues/missing-issue/links", map[string]string{"url": site.URL + "/page"}, http.StatusNotFound)
	requestJSON[any](t, f.handler, http.MethodPost, "/api/issues/missing-issue/links", map[string]string{"url": "not a url"}, http.StatusBadRequest)
	time.Sleep(300 * time.Millisecond)
	if hits.Load() != 0 {
		t.Fatalf("a refused link made %d outbound requests", hits.Load())
	}
}

// A viewer of an issue cannot add a link, so no site is contacted for the attempt; once an editor adds one, anyone signed
// in can read the stored icon, and a request without a session cannot.
func TestUnauthorizedLinkMakesNoOutboundRequestAndSignedInUsersReadTheIcon(t *testing.T) {
	withPlainFaviconClient(t)
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "favicon-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	api := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer api.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, api.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	member, user := verifiedAuthClient(t, api.URL, "Viewer", "favicon-viewer@example.com")
	invites := authRequest[[]domain.Invitation](t, admin, http.MethodPost, api.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{"favicon-viewer@example.com"}, "role": "member", "teamIds": []string{bootstrap.Teams[0].ID}}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, member, http.MethodPost, api.URL+"/api/invitations/accept", map[string]string{"token": invites[0].Token}, "", http.StatusOK)
	issue := authRequest[domain.Issue](t, admin, http.MethodPost, api.URL+"/api/issues", map[string]any{"title": "Favicon permissions", "teamId": bootstrap.Teams[0].ID}, "test-workspace", http.StatusCreated)
	authRequest[[]domain.IssuePermission](t, admin, http.MethodPut, api.URL+"/api/issues/"+issue.ID+"/permissions", map[string]any{
		"permissions": []map[string]string{{"subjectType": "user", "subjectId": user.ID, "role": "viewer"}},
	}, "test-workspace", http.StatusOK)

	var hits atomic.Int32
	site := faviconSite(t, &hits, nil)
	if status := authRequestStatus(t, member, http.MethodPost, api.URL+"/api/issues/"+issue.ID+"/links", map[string]string{"url": site.URL + "/denied"}, "test-workspace"); status != http.StatusForbidden {
		t.Fatalf("a viewer adding a link = %d, want 403", status)
	}
	if status := authRequestStatus(t, member, http.MethodPost, api.URL+"/api/issues/"+issue.ID+"/link-attachments", map[string]string{"url": site.URL + "/denied"}, "test-workspace"); status != http.StatusForbidden {
		t.Fatalf("a viewer saving a link attachment = %d, want 403", status)
	}
	time.Sleep(300 * time.Millisecond)
	if hits.Load() != 0 {
		t.Fatalf("a refused request made %d outbound requests", hits.Load())
	}

	link := authRequest[domain.Attachment](t, admin, http.MethodPost, api.URL+"/api/issues/"+issue.ID+"/links", map[string]string{"url": site.URL + "/allowed"}, "test-workspace", http.StatusCreated)
	var icon string
	waitUntil(t, "the favicon", func() bool {
		read := authRequest[connection[domain.Attachment]](t, member, http.MethodGet, api.URL+"/api/attachments?issueId="+issue.ID, nil, "test-workspace", http.StatusOK)
		for _, attachment := range read.Nodes {
			if attachment.ID == link.ID {
				icon = attachment.IconURL
			}
		}
		return icon != ""
	})
	if status := authRequestStatus(t, member, http.MethodGet, api.URL+icon, nil, "test-workspace"); status != http.StatusOK {
		t.Fatalf("a signed-in user reading the icon = %d, want 200", status)
	}
	response, err := http.Get(api.URL + icon)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == http.StatusOK {
		t.Fatal("the icon was served without a session")
	}
}
