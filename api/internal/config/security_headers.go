package config

import (
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// Content Security Policy modes (FLOW_SECURITY_CSP).
const (
	CSPReportOnly = "report-only"
	CSPEnforce    = "enforce"
	CSPOff        = "off"
)

// HSTS modes (FLOW_SECURITY_HSTS).
const (
	HSTSAuto = "auto"
	HSTSOff  = "off"
)

// DefaultHSTSMaxAge is one year, in seconds.
const DefaultHSTSMaxAge = 31536000

// SecurityHeaderNames are the response headers the security header policy
// sets, in the canonical form FLOW_SECURITY_HEADERS_SKIP accepts.
var SecurityHeaderNames = []string{
	"Content-Security-Policy",
	"Permissions-Policy",
	"Referrer-Policy",
	"Strict-Transport-Security",
	"X-Content-Type-Options",
	"X-Frame-Options",
}

// SecurityHeadersConfig is the response security header policy
// (docs/configuration.md "Security response headers"). A header a handler sets
// itself always wins over these defaults.
type SecurityHeadersConfig struct {
	// Enabled is FLOW_SECURITY_HEADERS (on by default); off sends none of the
	// headers below, for a gateway that owns them all or as a rollback.
	Enabled bool
	// Skip is FLOW_SECURITY_HEADERS_SKIP: headers (SecurityHeaderNames) Flow
	// leaves to the gateway in front of it. Skipping Content-Security-Policy
	// also drops the report-only policy and frame-ancestors.
	Skip []string
	// ReferrerPolicy is FLOW_SECURITY_REFERRER_POLICY.
	ReferrerPolicy string
	// PermissionsPolicy is FLOW_SECURITY_PERMISSIONS_POLICY; empty uses the
	// built-in policy matched to the web app's features.
	PermissionsPolicy string
	// FrameAncestors is FLOW_SECURITY_FRAME_ANCESTORS: who may frame Flow,
	// as CSP sources ('self', 'none' or https origins). Default 'self'.
	FrameAncestors []string
	// HSTS is FLOW_SECURITY_HSTS: auto sends Strict-Transport-Security when
	// FLOW_APP_URL is https and the request arrived over HTTPS (TLS on this
	// listener, or X-Forwarded-Proto from a trusted proxy); off never sends it.
	HSTS                  string
	HSTSMaxAge            int
	HSTSIncludeSubdomains bool
	HSTSPreload           bool
	// CSP is FLOW_SECURITY_CSP: report-only (default), enforce or off.
	CSP string
	// CSPReportURI is FLOW_SECURITY_CSP_REPORT_URI, where browsers send
	// violation reports (report-uri and the Reporting API); empty sends none.
	CSPReportURI string
	// CSPExtraSources is FLOW_SECURITY_CSP_EXTRA_SOURCES: sources added to
	// fetch directives of the web app policy, e.g.
	// "connect-src https://telemetry.example.com; img-src https://cdn.example.com".
	CSPExtraSources map[string][]string
	// TrustProxyHeaders is FLOW_TRUST_PROXY_HEADERS: X-Forwarded-For,
	// X-Forwarded-Proto and X-Forwarded-Host come from a proxy the operator
	// trusts (client address for rate limits, HTTPS detection for HSTS and
	// cookies, the scheme and host of links Flow builds). Off by default:
	// clients can forge them.
	TrustProxyHeaders bool
	// TrustedProxies is FLOW_TRUSTED_PROXIES: the addresses (IPs or CIDR
	// ranges) of the proxies in front of Flow. When set, forwarding headers
	// are read only from a peer in the list, and the client address is the
	// right-most X-Forwarded-For entry outside it. Empty: the direct peer is
	// the one trusted proxy, so the right-most entry is the client.
	TrustedProxies []string
}

// DefaultSecurityHeaders is the policy with every variable unset.
func DefaultSecurityHeaders() SecurityHeadersConfig {
	return SecurityHeadersConfig{
		Enabled:         true,
		Skip:            []string{},
		ReferrerPolicy:  "strict-origin-when-cross-origin",
		FrameAncestors:  []string{"'self'"},
		HSTS:            HSTSAuto,
		HSTSMaxAge:      DefaultHSTSMaxAge,
		CSP:             CSPReportOnly,
		CSPExtraSources: map[string][]string{},
	}
}

func loadSecurityHeaders() SecurityHeadersConfig {
	config := DefaultSecurityHeaders()
	config.Enabled = !switchedOff("FLOW_SECURITY_HEADERS")
	for _, name := range csvRaw(value("FLOW_SECURITY_HEADERS_SKIP", "")) {
		config.Skip = append(config.Skip, http.CanonicalHeaderKey(name))
	}
	config.ReferrerPolicy = strings.ToLower(value("FLOW_SECURITY_REFERRER_POLICY", config.ReferrerPolicy))
	config.PermissionsPolicy = value("FLOW_SECURITY_PERMISSIONS_POLICY", "")
	if raw := value("FLOW_SECURITY_FRAME_ANCESTORS", ""); raw != "" {
		config.FrameAncestors = cspSourceList(raw)
	}
	config.HSTS = strings.ToLower(value("FLOW_SECURITY_HSTS", config.HSTS))
	config.HSTSMaxAge = integer("FLOW_SECURITY_HSTS_MAX_AGE", config.HSTSMaxAge)
	config.HSTSIncludeSubdomains = boolean("FLOW_SECURITY_HSTS_INCLUDE_SUBDOMAINS", false)
	config.HSTSPreload = boolean("FLOW_SECURITY_HSTS_PRELOAD", false)
	config.CSP = strings.ToLower(value("FLOW_SECURITY_CSP", config.CSP))
	config.CSPReportURI = value("FLOW_SECURITY_CSP_REPORT_URI", "")
	config.CSPExtraSources = parseCSPExtraSources(value("FLOW_SECURITY_CSP_EXTRA_SOURCES", ""))
	config.TrustProxyHeaders = boolean("FLOW_TRUST_PROXY_HEADERS", false)
	if proxies := csvRaw(value("FLOW_TRUSTED_PROXIES", "")); len(proxies) > 0 {
		config.TrustedProxies = proxies
	}
	return config
}

// TrustedProxyPrefixes parses TrustedProxies: each entry is an IP address
// (a single host) or a CIDR range, IPv4 or IPv6.
func (c SecurityHeadersConfig) TrustedProxyPrefixes() ([]netip.Prefix, error) {
	return ParseTrustedProxies(c.TrustedProxies)
}

// ParseTrustedProxies parses IP addresses and CIDR ranges. IPv4-mapped IPv6
// entries are read as IPv4, as client addresses are.
func ParseTrustedProxies(entries []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			prefix, err := netip.ParsePrefix(entry)
			if err != nil {
				return nil, fmt.Errorf("FLOW_TRUSTED_PROXIES: %q is not an IP address or CIDR range", entry)
			}
			if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
				prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
			}
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(entry)
		if err != nil || addr.Zone() != "" {
			return nil, fmt.Errorf("FLOW_TRUSTED_PROXIES: %q is not an IP address or CIDR range", entry)
		}
		addr = addr.Unmap()
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes, nil
}

// Skips reports whether FLOW_SECURITY_HEADERS_SKIP leaves header to the gateway.
func (c SecurityHeadersConfig) Skips(header string) bool {
	header = http.CanonicalHeaderKey(header)
	for _, name := range c.Skip {
		if name == header {
			return true
		}
	}
	return false
}

// cspSourceList splits a space- or comma-separated source list and quotes
// the keywords self and none when they are written bare.
func cspSourceList(raw string) []string {
	sources := []string{}
	for _, item := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		switch strings.ToLower(strings.Trim(item, "'")) {
		case "self":
			item = "'self'"
		case "none":
			item = "'none'"
		}
		sources = append(sources, item)
	}
	return sources
}

// parseCSPExtraSources reads "directive source source; directive source".
// A malformed entry is kept as written so Validate can name it.
func parseCSPExtraSources(raw string) map[string][]string {
	result := map[string][]string{}
	for _, entry := range strings.Split(raw, ";") {
		parts := strings.Fields(entry)
		if len(parts) == 0 {
			continue
		}
		directive := strings.ToLower(parts[0])
		result[directive] = append(result[directive], parts[1:]...)
	}
	return result
}

// cspExtraDirectives are the web app policy directives that accept extra sources.
var cspExtraDirectives = map[string]bool{
	"script-src": true, "style-src": true, "img-src": true, "media-src": true, "font-src": true,
	"connect-src": true, "frame-src": true, "worker-src": true, "manifest-src": true,
}

var referrerPolicies = map[string]bool{
	"no-referrer": true, "no-referrer-when-downgrade": true, "origin": true, "origin-when-cross-origin": true,
	"same-origin": true, "strict-origin": true, "strict-origin-when-cross-origin": true, "unsafe-url": true,
}

// Validate rejects a policy that would be ambiguous or broader than the
// documented escape hatches allow.
func (c SecurityHeadersConfig) Validate() error {
	for _, name := range c.Skip {
		known := false
		for _, header := range SecurityHeaderNames {
			known = known || header == name
		}
		if !known {
			return fmt.Errorf("FLOW_SECURITY_HEADERS_SKIP: %q is not one of %s", name, strings.Join(SecurityHeaderNames, ", "))
		}
	}
	if !referrerPolicies[c.ReferrerPolicy] {
		return fmt.Errorf("FLOW_SECURITY_REFERRER_POLICY must be a Referrer-Policy token such as strict-origin-when-cross-origin")
	}
	if strings.ContainsAny(c.PermissionsPolicy, "\r\n") {
		return fmt.Errorf("FLOW_SECURITY_PERMISSIONS_POLICY must be a single line")
	}
	if err := validFrameAncestors(c.FrameAncestors); err != nil {
		return err
	}
	switch c.HSTS {
	case HSTSAuto, HSTSOff:
	default:
		return fmt.Errorf("FLOW_SECURITY_HSTS must be auto or off")
	}
	if c.HSTSMaxAge < 0 {
		return fmt.Errorf("FLOW_SECURITY_HSTS_MAX_AGE must be 0 or more seconds")
	}
	if c.HSTSPreload && (!c.HSTSIncludeSubdomains || c.HSTSMaxAge < DefaultHSTSMaxAge) {
		return fmt.Errorf("FLOW_SECURITY_HSTS_PRELOAD needs FLOW_SECURITY_HSTS_INCLUDE_SUBDOMAINS=true and FLOW_SECURITY_HSTS_MAX_AGE of at least %d", DefaultHSTSMaxAge)
	}
	switch c.CSP {
	case CSPReportOnly, CSPEnforce, CSPOff:
	default:
		return fmt.Errorf("FLOW_SECURITY_CSP must be report-only, enforce or off")
	}
	if c.CSPReportURI != "" {
		parsed, err := url.Parse(c.CSPReportURI)
		if err != nil || strings.ContainsAny(c.CSPReportURI, " ;,\"'") || !(parsed.Scheme == "https" || parsed.Scheme == "http" && loopbackURL(c.CSPReportURI) || parsed.Scheme == "" && strings.HasPrefix(parsed.Path, "/") && !strings.HasPrefix(c.CSPReportURI, "//")) {
			return fmt.Errorf("FLOW_SECURITY_CSP_REPORT_URI must be an https URL or a path on this server such as /csp-reports")
		}
	}
	if _, err := c.TrustedProxyPrefixes(); err != nil {
		return err
	}
	if len(c.TrustedProxies) > 0 && !c.TrustProxyHeaders {
		return fmt.Errorf("FLOW_TRUSTED_PROXIES needs FLOW_TRUST_PROXY_HEADERS=true")
	}
	for directive, sources := range c.CSPExtraSources {
		if !cspExtraDirectives[directive] {
			return fmt.Errorf("FLOW_SECURITY_CSP_EXTRA_SOURCES: %q is not a directive that takes extra sources", directive)
		}
		for _, source := range sources {
			if err := validExtraSource(directive, source); err != nil {
				return err
			}
		}
	}
	return nil
}

func validFrameAncestors(sources []string) error {
	if len(sources) == 0 {
		return fmt.Errorf("FLOW_SECURITY_FRAME_ANCESTORS must list 'self', 'none' or https origins")
	}
	for _, source := range sources {
		switch {
		case source == "'none'":
			if len(sources) > 1 {
				return fmt.Errorf("FLOW_SECURITY_FRAME_ANCESTORS: 'none' cannot be combined with other sources")
			}
		case source == "'self'":
		case validOriginSource(source):
		default:
			return fmt.Errorf("FLOW_SECURITY_FRAME_ANCESTORS: %q must be 'self', 'none' or an origin such as https://portal.example.com", source)
		}
	}
	return nil
}

// validOriginSource accepts https://host[:port], https://*.host, and plain-HTTP
// loopback origins for local development. No paths, no bare wildcard.
func validOriginSource(source string) bool {
	parsed, err := url.Parse(source)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return false
	}
	host := parsed.Hostname()
	if strings.HasPrefix(host, "*.") {
		host = host[2:]
	}
	if host == "" || strings.ContainsAny(host, "*'\";, ") {
		return false
	}
	return parsed.Scheme == "https" || parsed.Scheme == "wss" || (parsed.Scheme == "http" || parsed.Scheme == "ws") && loopbackURL("http://"+parsed.Host)
}

func validExtraSource(directive, source string) error {
	lower := strings.ToLower(source)
	switch {
	case source == "*" || strings.ContainsAny(source, ";,\"") || (strings.Contains(source, "'") && !strings.HasPrefix(source, "'")):
		return fmt.Errorf("FLOW_SECURITY_CSP_EXTRA_SOURCES: %s source %q is not allowed (no bare *, separators or stray quotes)", directive, source)
	case lower == "'unsafe-eval'" || (directive == "script-src" && (lower == "'unsafe-inline'" || lower == "'unsafe-hashes'")):
		return fmt.Errorf("FLOW_SECURITY_CSP_EXTRA_SOURCES: %s %s is not allowed", directive, source)
	case directive == "script-src" && (lower == "https:" || lower == "http:" || lower == "data:" || lower == "blob:"):
		return fmt.Errorf("FLOW_SECURITY_CSP_EXTRA_SOURCES: script-src %s would allow scripts from any host; list the host instead", source)
	case strings.HasPrefix(source, "'"):
		if lower == "'wasm-unsafe-eval'" || strings.HasPrefix(lower, "'sha256-") || strings.HasPrefix(lower, "'sha384-") || strings.HasPrefix(lower, "'sha512-") || (directive != "script-src" && lower == "'unsafe-inline'") {
			return nil
		}
		return fmt.Errorf("FLOW_SECURITY_CSP_EXTRA_SOURCES: %s keyword %s is not allowed", directive, source)
	case strings.HasSuffix(source, ":") && !strings.Contains(source, "/"):
		// A scheme source (https:, data:, blob:) outside script-src.
		return nil
	case validOriginSource(source) || validHostPathSource(source):
		return nil
	default:
		return fmt.Errorf("FLOW_SECURITY_CSP_EXTRA_SOURCES: %s source %q must be an https origin, a scheme such as data: or a hash", directive, source)
	}
}

// validHostPathSource accepts https://host/path sources (CSP path matching).
func validHostPathSource(source string) bool {
	parsed, err := url.Parse(source)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return validOriginSource(parsed.Scheme + "://" + parsed.Host)
}
