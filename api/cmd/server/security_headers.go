package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	appconfig "flow/api/internal/config"
)

// embedFrameOrigins are the players the editor's embed blocks load in an
// iframe (web/src/components/editor/embeds/embed-providers.ts). They are the
// only third-party frames the web app policy allows, and the only origins the
// Permissions-Policy delegates fullscreen and picture-in-picture to.
var embedFrameOrigins = []string{
	"https://www.youtube-nocookie.com",
	"https://www.loom.com",
	"https://share.descript.com",
	"https://www.tella.tv",
	"https://miro.com",
}

// webAppConnectOrigins are third-party hosts the web app calls from the
// browser: the OAuth application form checks a GitHub username on GitHub's API.
var webAppConnectOrigins = []string{"https://api.github.com"}

// securityHeaderPolicy is the one place Flow decides its response security
// headers. Handlers that set a header themselves keep their value (uploads
// keep "sandbox; default-src 'none'"); the policy only fills in what is
// missing, right before the response header is written.
type securityHeaderPolicy struct {
	config             appconfig.SecurityHeadersConfig
	forwarding         *forwardingPolicy
	httpsApp           bool
	frameAncestors     string
	frameOptions       string
	permissions        string
	hsts               string
	apiPolicy          string
	reportDirectives   string
	reportingEndpoints string
	// app is the web app document policy; nil when this server does not
	// serve the built web app (FLOW_STATIC_PATH unset).
	app *webAppPolicy
}

func newSecurityHeaderPolicy(config appconfig.SecurityHeadersConfig, appURL, staticPath string) *securityHeaderPolicy {
	if !config.Enabled {
		return nil
	}
	parsedApp, _ := url.Parse(strings.TrimSpace(appURL))
	policy := &securityHeaderPolicy{config: config, forwarding: forwardingPolicyFromConfig(config, appURL), httpsApp: parsedApp != nil && parsedApp.Scheme == "https" && parsedApp.Host != ""}
	policy.frameAncestors = "frame-ancestors " + strings.Join(config.FrameAncestors, " ")
	switch strings.Join(config.FrameAncestors, " ") {
	case "'none'":
		policy.frameOptions = "DENY"
	case "'self'":
		policy.frameOptions = "SAMEORIGIN"
	}
	// X-Frame-Options cannot name other origins; with an allow list only
	// frame-ancestors (enforced in every CSP mode) decides.
	policy.permissions = config.PermissionsPolicy
	if policy.permissions == "" {
		policy.permissions = defaultPermissionsPolicy()
	}
	if config.HSTS == appconfig.HSTSAuto && policy.httpsApp {
		policy.hsts = "max-age=" + strconv.Itoa(config.HSTSMaxAge)
		if config.HSTSIncludeSubdomains {
			policy.hsts += "; includeSubDomains"
		}
		if config.HSTSPreload {
			policy.hsts += "; preload"
		}
	}
	if config.CSPReportURI != "" {
		endpoint := config.CSPReportURI
		if strings.HasPrefix(endpoint, "/") && parsedApp != nil && parsedApp.Host != "" {
			endpoint = parsedApp.Scheme + "://" + parsedApp.Host + endpoint
		}
		policy.reportDirectives = "; report-uri " + endpoint + "; report-to csp"
		policy.reportingEndpoints = `csp="` + endpoint + `"`
	}
	policy.apiPolicy = "default-src 'none'; base-uri 'none'; form-action 'none'"
	if staticPath != "" {
		policy.app = &webAppPolicy{directives: webAppDirectives(config, parsedApp), index: inlineScriptHashes{path: filepath.Join(staticPath, "index.html")}}
	}
	return policy
}

func defaultPermissionsPolicy() string {
	embeds := "self"
	for _, origin := range embedFrameOrigins {
		embeds += ` "` + origin + `"`
	}
	// Granted: what the web app uses itself (clipboard copy, fullscreen and
	// picture in picture for videos, passkeys, notification sounds). Embedded
	// players keep the fullscreen and picture in picture they had. Everything
	// else (camera, microphone, screen capture, location, payments, devices)
	// is off for Flow and every frame in it.
	return strings.Join([]string{
		"accelerometer=()", "autoplay=(self)", "bluetooth=()", "browsing-topics=()", "camera=()", "clipboard-read=()",
		"clipboard-write=(self)", "display-capture=()", "encrypted-media=(self)", "fullscreen=(" + embeds + ")",
		"geolocation=()", "gyroscope=()", "hid=()", "idle-detection=()", "local-fonts=()", "magnetometer=()",
		"microphone=()", "midi=()", "payment=()", "picture-in-picture=(" + embeds + ")",
		"publickey-credentials-create=(self)", "publickey-credentials-get=(self)", "screen-wake-lock=()", "serial=()",
		"usb=()", "window-management=()", "xr-spatial-tracking=()",
	}, ", ")
}

// cspDirective is one directive of the web app policy; script-src gets the
// inline script hashes of index.html appended at request time.
type cspDirective struct {
	name    string
	sources []string
}

func webAppDirectives(config appconfig.SecurityHeadersConfig, app *url.URL) []cspDirective {
	connect := []string{"'self'"}
	if app != nil && app.Host != "" {
		// 'self' covers same-origin WebSockets in current browsers; the
		// explicit origin keeps realtime working in older ones.
		if app.Scheme == "https" {
			connect = append(connect, "wss://"+app.Host)
		} else {
			connect = append(connect, "ws://"+app.Host)
		}
	}
	connect = append(connect, webAppConnectOrigins...)
	directives := []cspDirective{
		{"default-src", []string{"'self'"}},
		{"script-src", []string{"'self'"}},
		// Rendered markdown math (KaTeX), diagrams (Mermaid), the editor, the
		// terminal and toasts write inline styles at runtime; scripts stay strict.
		{"style-src", []string{"'self'", "'unsafe-inline'"}},
		// Avatars, logos, link previews and markdown images come from any
		// https host; pasted and drawn images are data: and blob: URLs.
		{"img-src", []string{"'self'", "data:", "blob:", "https:"}},
		{"media-src", []string{"'self'", "blob:"}},
		{"font-src", []string{"'self'"}},
		{"connect-src", connect},
		// blob: is the hidden frame PDF printing uses.
		{"frame-src", append([]string{"blob:"}, embedFrameOrigins...)},
		{"worker-src", []string{"'self'"}},
		{"manifest-src", []string{"'self'"}},
		{"object-src", []string{"'none'"}},
		{"base-uri", []string{"'self'"}},
		{"form-action", []string{"'self'"}},
	}
	for index := range directives {
		for _, source := range config.CSPExtraSources[directives[index].name] {
			if !containsString(directives[index].sources, source) {
				directives[index].sources = append(directives[index].sources, source)
			}
		}
	}
	return directives
}

type webAppPolicy struct {
	directives []cspDirective
	index      inlineScriptHashes
	mu         sync.Mutex
	key        string
	value      string
}

// policy is the web app policy without frame-ancestors, with the hashes of
// index.html's current inline scripts.
func (p *webAppPolicy) policy() string {
	hashes := p.index.current()
	key := strings.Join(hashes, " ")
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.value != "" && p.key == key {
		return p.value
	}
	parts := make([]string, 0, len(p.directives))
	for _, directive := range p.directives {
		sources := directive.sources
		if directive.name == "script-src" {
			// 'self', then the inline script hashes, then operator extras.
			sources = make([]string, 0, len(directive.sources)+len(hashes))
			sources = append(append(append(sources, directive.sources[0]), hashes...), directive.sources[1:]...)
		}
		parts = append(parts, directive.name+" "+strings.Join(sources, " "))
	}
	p.key, p.value = key, strings.Join(parts, "; ")
	return p.value
}

var (
	inlineScriptPattern = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script\s*>`)
	scriptSrcAttribute  = regexp.MustCompile(`(?i)(^|\s)src\s*=`)
)

// inlineScriptHashes are the CSP hashes of index.html's inline scripts (the
// theme bootstrap), re-read when the file changes so a rebuilt web app never
// needs a policy edit.
type inlineScriptHashes struct {
	path    string
	mu      sync.Mutex
	loaded  bool
	modTime time.Time
	size    int64
	hashes  []string
}

func (c *inlineScriptHashes) current() []string {
	info, err := os.Stat(c.path)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.loaded, c.hashes = false, nil
		return nil
	}
	if c.loaded && info.ModTime().Equal(c.modTime) && info.Size() == c.size {
		return c.hashes
	}
	content, err := os.ReadFile(c.path)
	if err != nil {
		return c.hashes
	}
	c.loaded, c.modTime, c.size, c.hashes = true, info.ModTime(), info.Size(), inlineScriptHashList(content)
	return c.hashes
}

func inlineScriptHashList(document []byte) []string {
	hashes := []string{}
	for _, match := range inlineScriptPattern.FindAllSubmatch(document, -1) {
		if scriptSrcAttribute.Match(match[1]) || len(match[2]) == 0 {
			continue
		}
		digest := sha256.Sum256(match[2])
		hash := "'sha256-" + base64.StdEncoding.EncodeToString(digest[:]) + "'"
		if !containsString(hashes, hash) {
			hashes = append(hashes, hash)
		}
	}
	return hashes
}

// requestOverHTTPS reports whether the browser reached Flow over HTTPS: TLS on
// this listener, or X-Forwarded-Proto set by a proxy the operator trusts
// (forwardingPolicy.overHTTPS). Without that trust the header is ignored,
// since any client can send it. With several proxies the last value is the
// one the nearest proxy wrote.
func (p *securityHeaderPolicy) requestOverHTTPS(r *http.Request) bool {
	return p.forwarding.overHTTPS(r)
}

// webAppRoute reports whether the request is served by the built web app
// (the SPA document and its static files) rather than an API route.
func (p *securityHeaderPolicy) webAppRoute(r *http.Request) bool {
	return p.app != nil && (r.Method == http.MethodGet || r.Method == http.MethodHead) && !serverRoute(r.URL.Path)
}

// consentPagePath is the OAuth consent page of the web app, where a signed-in
// user approves an application's access. It is the page a clickjacking attempt
// would target, and nothing embeds it, so no framing allow list applies to it.
const consentPagePath = "/oauth/authorize"

// framingDenied reports whether the request is for a page that no site may
// frame, whatever FLOW_SECURITY_FRAME_ANCESTORS allows for the rest of the app.
func (p *securityHeaderPolicy) framingDenied(r *http.Request) bool {
	return p.webAppRoute(r) && strings.TrimRight(r.URL.Path, "/") == consentPagePath
}

// apply fills in the security headers the handler did not set itself.
func (p *securityHeaderPolicy) apply(header http.Header, r *http.Request) {
	frameAncestors, frameOptions := p.frameAncestors, p.frameOptions
	if p.framingDenied(r) {
		frameAncestors, frameOptions = "frame-ancestors 'none'", "DENY"
	}
	set := func(name, value string) {
		if value != "" && !p.config.Skips(name) && len(header.Values(name)) == 0 {
			header.Set(name, value)
		}
	}
	set("X-Content-Type-Options", "nosniff")
	set("Referrer-Policy", p.config.ReferrerPolicy)
	set("Permissions-Policy", p.permissions)
	set("X-Frame-Options", frameOptions)
	if p.hsts != "" && p.requestOverHTTPS(r) {
		set("Strict-Transport-Security", p.hsts)
	}
	if p.config.Skips("Content-Security-Policy") {
		return
	}
	const enforced, reportOnly = "Content-Security-Policy", "Content-Security-Policy-Report-Only"
	if own := header[enforced]; len(own) > 0 || len(header[reportOnly]) > 0 {
		// The handler owns its policy (uploads, the SAML POST form, the
		// unsubscribe page). Framing stays consistent: frame-ancestors is
		// added to its enforced policy when it has none.
		for _, value := range own {
			if strings.Contains(strings.ToLower(value), "frame-ancestors") {
				return
			}
		}
		if len(own) > 0 {
			own[0] = strings.TrimRight(strings.TrimSpace(own[0]), "; ") + "; " + frameAncestors
		} else {
			header.Set(enforced, frameAncestors)
		}
		return
	}
	policy := p.apiPolicy
	if p.webAppRoute(r) {
		policy = p.app.policy()
	}
	switch p.config.CSP {
	case appconfig.CSPEnforce:
		header.Set(enforced, policy+"; "+frameAncestors+p.reportDirectives)
	case appconfig.CSPReportOnly:
		// Browsers ignore frame-ancestors in a report-only policy, so framing
		// is enforced on its own while the rest is only reported.
		header.Set(enforced, frameAncestors)
		header.Set(reportOnly, policy+p.reportDirectives)
	default:
		header.Set(enforced, frameAncestors)
		return
	}
	if p.reportingEndpoints != "" {
		set("Reporting-Endpoints", p.reportingEndpoints)
	}
}

// withSecurityHeaders applies the policy to every response, including error
// responses, static files, SSE streams and redirects. WebSocket upgrades (1xx)
// pass through untouched.
func withSecurityHeaders(policy *securityHeaderPolicy, next http.Handler) http.Handler {
	if policy == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer := &securityHeaderWriter{ResponseWriter: w, policy: policy, request: r}
		next.ServeHTTP(writer, r)
		// A handler that writes nothing gets its implicit 200 after returning.
		writer.finalize()
	})
}

type securityHeaderWriter struct {
	http.ResponseWriter
	policy  *securityHeaderPolicy
	request *http.Request
	done    bool
}

func (w *securityHeaderWriter) finalize() {
	if !w.done {
		w.done = true
		w.policy.apply(w.ResponseWriter.Header(), w.request)
	}
}

func (w *securityHeaderWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *securityHeaderWriter) WriteHeader(status int) {
	if status >= 200 {
		w.finalize()
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *securityHeaderWriter) Write(p []byte) (int, error) {
	w.finalize()
	return w.ResponseWriter.Write(p)
}

// ReadFrom keeps the sendfile path of static files and uploads.
func (w *securityHeaderWriter) ReadFrom(source io.Reader) (int64, error) {
	w.finalize()
	if from, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return from.ReadFrom(source)
	}
	return io.Copy(w.ResponseWriter, source)
}

func (w *securityHeaderWriter) Flush() {
	w.finalize()
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *securityHeaderWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}
