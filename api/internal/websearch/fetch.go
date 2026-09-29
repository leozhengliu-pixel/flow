package websearch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Page is the readable text of a fetched URL.
type Page struct {
	URL         string `json:"url"`
	FinalURL    string `json:"finalUrl"`
	Title       string `json:"title,omitempty"`
	ContentType string `json:"contentType"`
	Text        string `json:"text"`
	Truncated   bool   `json:"truncated"`
}

// Fetcher downloads public web pages for the model. It refuses non-http(s)
// URLs and any connection to a private, loopback, link-local or otherwise
// internal address. The address check runs on the resolved IP at connect
// time, so DNS rebinding and redirects to internal hosts are refused too.
type Fetcher struct {
	Timeout      time.Duration // default 10 s
	MaxBytes     int64         // body bytes read, default 2 MB
	MaxRedirects int           // default 3
	MaxText      int           // characters of text returned, default 60 000
	// AllowAddr overrides which addresses may be dialed (tests); nil = public only.
	AllowAddr func(netip.Addr) bool
}

// ErrBlockedAddress is returned when a URL resolves to an internal address.
var ErrBlockedAddress = errors.New("address is not allowed")

func (f *Fetcher) timeout() time.Duration {
	if f.Timeout > 0 {
		return f.Timeout
	}
	return 10 * time.Second
}

func (f *Fetcher) maxBytes() int64 {
	if f.MaxBytes > 0 {
		return f.MaxBytes
	}
	return 2 << 20
}

func (f *Fetcher) maxRedirects() int {
	if f.MaxRedirects > 0 {
		return f.MaxRedirects
	}
	return 3
}

func (f *Fetcher) maxText() int {
	if f.MaxText > 0 {
		return f.MaxText
	}
	return 60000
}

func (f *Fetcher) allowed(addr netip.Addr) bool {
	if f.AllowAddr != nil {
		return f.AllowAddr(addr)
	}
	return PublicAddr(addr)
}

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), // NAT64 can reach IPv4-internal hosts
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"), // 6to4 embeds IPv4
}

// PublicAddr reports whether an address is a routable public unicast address.
func PublicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	if addr.Is4() && addr == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return false
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func checkURL(target *url.URL) error {
	if target.Scheme != "http" && target.Scheme != "https" {
		return fmt.Errorf("only http and https URLs can be fetched")
	}
	if target.Hostname() == "" {
		return fmt.Errorf("the URL has no host")
	}
	if target.User != nil {
		return fmt.Errorf("URLs with credentials cannot be fetched")
	}
	return nil
}

func (f *Fetcher) client() *http.Client {
	dialer := &net.Dialer{
		Timeout: f.timeout(),
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			addr, err := netip.ParseAddr(host)
			if err != nil || !f.allowed(addr) {
				return ErrBlockedAddress
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                 nil, // a proxy would dial on our behalf and skip the address check
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   f.timeout(),
		ResponseHeaderTimeout: f.timeout(),
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Timeout:   f.timeout(),
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) > f.maxRedirects() {
				return fmt.Errorf("too many redirects (more than %d)", f.maxRedirects())
			}
			return checkURL(request.URL)
		},
	}
}

// Fetch downloads a URL and returns its readable text.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (Page, error) {
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return Page{}, fmt.Errorf("invalid URL")
	}
	if err := checkURL(target); err != nil {
		return Page{}, err
	}
	if addr, err := netip.ParseAddr(strings.Trim(target.Hostname(), "[]")); err == nil && !f.allowed(addr) {
		return Page{}, fmt.Errorf("%s: %w", target.Hostname(), ErrBlockedAddress)
	}
	ctx, cancel := context.WithTimeout(ctx, f.timeout())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return Page{}, fmt.Errorf("invalid URL")
	}
	request.Header.Set("User-Agent", "FlowAgent/1.0 (+web fetch)")
	request.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,application/json;q=0.8,*/*;q=0.1")
	response, err := f.client().Do(request)
	if err != nil {
		if errors.Is(err, ErrBlockedAddress) {
			return Page{}, fmt.Errorf("the URL points to an internal address, which cannot be fetched")
		}
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return Page{}, fmt.Errorf("could not fetch the URL: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return Page{}, fmt.Errorf("the page returned HTTP %d", response.StatusCode)
	}
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	mediaType = strings.ToLower(mediaType)
	body, err := io.ReadAll(io.LimitReader(response.Body, f.maxBytes()))
	if err != nil {
		return Page{}, fmt.Errorf("could not read the page: %v", err)
	}
	if mediaType == "" {
		mediaType = strings.ToLower(strings.Split(http.DetectContentType(body), ";")[0])
	}
	page := Page{URL: rawURL, FinalURL: response.Request.URL.String(), ContentType: mediaType}
	switch {
	case mediaType == "text/html" || mediaType == "application/xhtml+xml":
		page.Title, page.Text = ReadableText(body)
	case strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") || mediaType == "application/xml" || strings.HasSuffix(mediaType, "+xml"):
		page.Text = strings.TrimSpace(strings.ToValidUTF8(string(body), ""))
	default:
		return Page{}, fmt.Errorf("the page is %s, which cannot be read as text", mediaType)
	}
	if int64(len(body)) >= f.maxBytes() {
		page.Truncated = true
	}
	if utf8.RuneCountInString(page.Text) > f.maxText() {
		page.Text = string([]rune(page.Text)[:f.maxText()])
		page.Truncated = true
	}
	return page, nil
}

var skippedElements = map[string]bool{"script": true, "style": true, "noscript": true, "template": true, "svg": true, "iframe": true, "nav": true, "footer": true, "form": true, "button": true, "select": true, "canvas": true}

var blockElements = map[string]bool{"p": true, "div": true, "section": true, "article": true, "main": true, "br": true, "li": true, "ul": true, "ol": true, "tr": true, "table": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "pre": true, "blockquote": true, "header": true, "aside": true, "dt": true, "dd": true, "hr": true, "figcaption": true}

// ReadableText reduces an HTML document to its title and visible text.
func ReadableText(document []byte) (string, string) {
	tokenizer := html.NewTokenizer(strings.NewReader(strings.ToValidUTF8(string(document), "")))
	var text strings.Builder
	title := ""
	inTitle := false
	skipDepth := 0
	for {
		kind := tokenizer.Next()
		switch kind {
		case html.ErrorToken:
			return strings.TrimSpace(collapseSpace(title)), tidyLines(text.String())
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := tokenizer.TagName()
			tag := string(name)
			if tag == "title" && kind == html.StartTagToken {
				inTitle = true
			}
			if skippedElements[tag] && kind == html.StartTagToken {
				skipDepth++
			}
			if blockElements[tag] {
				text.WriteString("\n")
				if tag == "li" && skipDepth == 0 {
					text.WriteString("- ")
				}
			}
		case html.EndTagToken:
			name, _ := tokenizer.TagName()
			tag := string(name)
			if tag == "title" {
				inTitle = false
			}
			if skippedElements[tag] && skipDepth > 0 {
				skipDepth--
			}
			if blockElements[tag] {
				text.WriteString("\n")
			}
		case html.TextToken:
			value := string(tokenizer.Text())
			if inTitle {
				title += value
				continue
			}
			if skipDepth == 0 {
				text.WriteString(collapseSpace(value))
			}
		}
	}
}

func collapseSpace(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		if value != "" {
			return " "
		}
		return ""
	}
	result := strings.Join(fields, " ")
	if strings.TrimLeft(value[:1], " \t\r\n") == "" {
		result = " " + result
	}
	if strings.TrimRight(value[len(value)-1:], " \t\r\n") == "" {
		result += " "
	}
	return result
}

func tidyLines(value string) string {
	lines := []string{}
	blank := false
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "-" {
			if !blank && len(lines) > 0 {
				lines = append(lines, "")
			}
			blank = true
			continue
		}
		blank = false
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// stripTags removes inline markup (e.g. <strong>) from provider snippets.
func stripTags(value string) string {
	if !strings.Contains(value, "<") && !strings.Contains(value, "&") {
		return strings.TrimSpace(value)
	}
	tokenizer := html.NewTokenizer(strings.NewReader(value))
	var text strings.Builder
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return strings.TrimSpace(strings.Join(strings.Fields(text.String()), " "))
		case html.TextToken:
			text.Write(tokenizer.Text())
		}
	}
}
