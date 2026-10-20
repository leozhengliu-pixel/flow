// Package outbound is Flow's single policy for server-side requests to URLs
// that workspace members, admins or remote servers supply (MCP connectors,
// webhooks, identity providers, self-hosted code hosts, …).
//
// By default only public HTTPS destinations are reachable: every DNS answer
// must be a public unicast address. An operator can open specific intranet
// hosts with two lists that must both match (FLOW_TRUSTED_PRIVATE_ENDPOINTS
// for the host name, FLOW_TRUSTED_PRIVATE_NETWORKS for every address it
// resolves to). Loopback, link-local, cloud metadata, unspecified, multicast
// and broadcast addresses stay blocked whatever is configured.
//
// The same policy runs when a URL is saved (Check) and on every connection
// (Client's dialer): the dialer resolves the host, checks every answer and
// dials the checked address, so a DNS answer that changes between the save
// and the request (DNS rebinding) is re-checked rather than trusted. TLS
// keeps the URL's host name for SNI and certificate verification.
package outbound

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"
)

// Access says whether a caller honours the operator's trusted private
// endpoints. Each caller chooses explicitly (see docs/configuration.md).
type Access int

const (
	// PublicOnly reaches public addresses only: arbitrary URLs a member
	// pastes (link previews, attachments from URLs, file previews).
	PublicOnly Access = iota
	// TrustedPrivate also reaches operator-trusted private endpoints: MCP
	// connectors, webhooks, identity providers, self-hosted code hosts.
	TrustedPrivate
)

// Environment variable names, quoted in error messages and docs.
const (
	EnvTrustedEndpoints = "FLOW_TRUSTED_PRIVATE_ENDPOINTS"
	EnvTrustedNetworks  = "FLOW_TRUSTED_PRIVATE_NETWORKS"
	EnvTrustedCAFile    = "FLOW_TRUSTED_CA_FILE"
)

// Config is the operator's configuration (environment / Helm only).
type Config struct {
	// TrustedHosts are exact host names ("hsb.example.net") or "*." suffix
	// wildcards ("*.corp.example.com", which matches any subdomain but not
	// the apex). A bare "*" is refused.
	TrustedHosts []string
	// TrustedNetworks are CIDRs ("10.218.0.0/16") trusted hosts may resolve to.
	TrustedNetworks []string
	// CAFile is a PEM bundle added to the system roots for enterprise PKI.
	CAFile string
}

// LoadConfig reads the configuration from the environment.
func LoadConfig() Config {
	return Config{
		TrustedHosts:    splitList(os.Getenv(EnvTrustedEndpoints)),
		TrustedNetworks: splitList(os.Getenv(EnvTrustedNetworks)),
		CAFile:          strings.TrimSpace(os.Getenv(EnvTrustedCAFile)),
	}
}

func splitList(value string) []string {
	out := []string{}
	for _, item := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// Resolver is the DNS lookup the policy uses (net.DefaultResolver in production).
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// Policy is a parsed Config. The zero value is not usable; use New or Default.
type Policy struct {
	hosts    []string // lower-case exact hosts and "*.suffix" patterns
	networks []netip.Prefix
	roots    *x509.CertPool // nil: system roots
	resolver Resolver
	dial     func(ctx context.Context, network, address string) (net.Conn, error)
}

// Default is the policy without trusted endpoints (public HTTPS only).
func Default() *Policy {
	policy, _ := New(Config{})
	return policy
}

// New parses and validates a configuration.
func New(cfg Config) (*Policy, error) {
	hosts, err := ParseHosts(cfg.TrustedHosts)
	if err != nil {
		return nil, err
	}
	networks, err := ParseNetworks(cfg.TrustedNetworks)
	if err != nil {
		return nil, err
	}
	if len(hosts) > 0 && len(networks) == 0 {
		return nil, fmt.Errorf("%s is set but %s is empty: trusted hosts must also name the networks they resolve to", EnvTrustedEndpoints, EnvTrustedNetworks)
	}
	policy := &Policy{hosts: hosts, networks: networks, resolver: net.DefaultResolver}
	if cfg.CAFile != "" {
		if policy.roots, err = LoadCAFile(cfg.CAFile); err != nil {
			return nil, err
		}
	}
	dialer := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
	policy.dial = dialer.DialContext
	return policy, nil
}

// Validate reports whether cfg would load (startup configuration check).
func Validate(cfg Config) error {
	_, err := New(cfg)
	return err
}

// ParseHosts validates host patterns: exact host names or IP literals, or
// "*." followed by a domain of at least two labels.
func ParseHosts(values []string) ([]string, error) {
	out := []string{}
	for _, raw := range values {
		value := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
		if value == "" {
			continue
		}
		name := value
		if suffix, ok := strings.CutPrefix(value, "*."); ok {
			name = suffix
			if !strings.Contains(suffix, ".") {
				return nil, fmt.Errorf("%s: %q is too broad; a wildcard needs a domain of at least two labels (for example *.corp.example.com)", EnvTrustedEndpoints, raw)
			}
		}
		if strings.Contains(name, "*") {
			return nil, fmt.Errorf("%s: %q is not allowed; use exact hosts or a leading *. wildcard", EnvTrustedEndpoints, raw)
		}
		if _, err := netip.ParseAddr(name); err != nil && !validHostName(name) {
			return nil, fmt.Errorf("%s: %q is not a host name", EnvTrustedEndpoints, raw)
		}
		if blockedHostName(name) {
			return nil, fmt.Errorf("%s: %q is a metadata or loopback host and can never be trusted", EnvTrustedEndpoints, raw)
		}
		out = append(out, value)
	}
	return out, nil
}

func validHostName(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return false
			}
		}
	}
	return true
}

// ParseNetworks validates CIDRs. A /0 is refused: it would trust every address.
func ParseNetworks(values []string) ([]netip.Prefix, error) {
	out := []netip.Prefix{}
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			addr, addrErr := netip.ParseAddr(value)
			if addrErr != nil {
				return nil, fmt.Errorf("%s: %q is not a CIDR", EnvTrustedNetworks, raw)
			}
			prefix = netip.PrefixFrom(addr, addr.BitLen())
		}
		prefix = prefix.Masked()
		if prefix.Bits() == 0 {
			return nil, fmt.Errorf("%s: %q would trust every address", EnvTrustedNetworks, raw)
		}
		out = append(out, prefix)
	}
	return out, nil
}

// LoadCAFile returns the system roots plus the PEM certificates in path.
func LoadCAFile(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", EnvTrustedCAFile, err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s: %s holds no PEM certificates", EnvTrustedCAFile, path)
	}
	return pool, nil
}

// WithResolver returns a copy using resolver for DNS (tests).
func (p *Policy) WithResolver(resolver Resolver) *Policy {
	copy := *p
	copy.resolver = resolver
	return &copy
}

// WithDialer returns a copy that opens connections with dial (tests: route
// a checked private address to an httptest server).
func (p *Policy) WithDialer(dial func(ctx context.Context, network, address string) (net.Conn, error)) *Policy {
	copy := *p
	copy.dial = dial
	return &copy
}

// WithRoots returns a copy that verifies TLS against roots (tests).
func (p *Policy) WithRoots(roots *x509.CertPool) *Policy {
	copy := *p
	copy.roots = roots
	return &copy
}

// RootCAs is the certificate pool for TLS verification (nil: system roots).
func (p *Policy) RootCAs() *x509.CertPool { return p.roots }

// HasTrustedEndpoints reports whether the operator trusted any private host.
func (p *Policy) HasTrustedEndpoints() bool { return len(p.hosts) > 0 }

// HostTrusted reports whether host matches a trusted host pattern.
func (p *Policy) HostTrusted(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if host == "" {
		return false
	}
	for _, pattern := range p.hosts {
		if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
		} else if host == pattern {
			return true
		}
	}
	return false
}

func (p *Policy) inTrustedNetworks(addr netip.Addr) bool {
	for _, prefix := range p.networks {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// Reason classifies a refusal.
type Reason string

const (
	ReasonInvalidURL   Reason = "invalidUrl"
	ReasonUnresolvable Reason = "unresolvable"
	ReasonBlocked      Reason = "blocked"
	ReasonPrivate      Reason = "private"
)

// Error is a refused destination. Error() never contains resolved
// addresses, so it is safe to show to any member; Detail() adds them for
// server logs.
type Error struct {
	Reason  Reason
	Host    string
	Access  Access
	Trusted bool // the host matched a trusted pattern
	addrs   []netip.Addr
}

func (e *Error) Error() string {
	host := e.Host
	if host == "" {
		host = "the endpoint"
	}
	switch e.Reason {
	case ReasonInvalidURL:
		return "the endpoint must be an https:// URL without credentials"
	case ReasonUnresolvable:
		return host + " could not be resolved"
	case ReasonBlocked:
		return host + " resolves to a loopback, link-local, metadata or other reserved address, which is never allowed"
	}
	if e.Access == PublicOnly {
		return host + " resolves to a private address, which is not allowed here (only public HTTPS endpoints)"
	}
	if e.Trusted {
		return host + " is a trusted private endpoint but resolves outside " + EnvTrustedNetworks + "; an operator can add its network there"
	}
	return host + " resolves to a private address; an operator can allow it with " + EnvTrustedEndpoints + " and " + EnvTrustedNetworks
}

// Detail is Error() plus the resolved addresses (logs and operators only).
func (e *Error) Detail() string {
	if len(e.addrs) == 0 {
		return e.Error()
	}
	parts := make([]string, len(e.addrs))
	for i, addr := range e.addrs {
		parts[i] = addr.String()
	}
	return e.Error() + " (resolved: " + strings.Join(parts, ", ") + ")"
}

// IsRefusal reports whether err is (or wraps) a policy refusal.
func IsRefusal(err error) bool {
	var refusal *Error
	return errors.As(err, &refusal)
}

// metadataPrefixes are cloud metadata services outside link-local space.
var metadataPrefixes = []netip.Prefix{
	netip.MustParsePrefix("fd00:ec2::/32"),          // AWS IMDS / DNS / NTP over IPv6
	netip.MustParsePrefix("100.100.100.200/32"),     // Alibaba Cloud metadata
	netip.MustParsePrefix("0.0.0.0/8"),              // "this network"
	netip.MustParsePrefix("255.255.255.255/32"),     // limited broadcast
	netip.MustParsePrefix("fe80::/10"),              // link-local (also IsLinkLocalUnicast)
	netip.MustParsePrefix("169.254.0.0/16"),         // link-local incl. 169.254.169.254
	netip.MustParsePrefix("::ffff:169.254.0.0/112"), // mapped form, belt and braces
}

// blockedHostNames resolve to metadata services on some clouds.
var blockedHostNames = []string{"metadata.google.internal", "metadata.goog", "metadata", "instance-data", "instance-data.ec2.internal", "metadata.azure.com", "localhost"}

func blockedHostName(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, name := range blockedHostNames {
		if host == name || strings.HasSuffix(host, "."+name) && name == "localhost" {
			return true
		}
	}
	return false
}

// AlwaysBlocked reports addresses no configuration can open.
func AlwaysBlocked(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLoopback() || addr.IsUnspecified() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() {
		return true
	}
	for _, prefix := range metadataPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// reservedPrefixes are special-purpose ranges treated as non-public.
var reservedPrefixes = []netip.Prefix{
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

// Public reports whether addr is a routable public unicast address.
func Public(addr netip.Addr) bool {
	addr = addr.Unmap()
	if AlwaysBlocked(addr) || addr.IsPrivate() {
		return false
	}
	for _, prefix := range reservedPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// allowed checks one resolved address for host.
func (p *Policy) allowed(host string, addr netip.Addr, access Access) error {
	addr = addr.Unmap()
	trusted := access == TrustedPrivate && p.HostTrusted(host)
	switch {
	case AlwaysBlocked(addr):
		return &Error{Reason: ReasonBlocked, Host: host, Access: access, Trusted: trusted, addrs: []netip.Addr{addr}}
	case Public(addr):
		return nil
	case trusted && p.inTrustedNetworks(addr):
		return nil
	}
	return &Error{Reason: ReasonPrivate, Host: host, Access: access, Trusted: trusted, addrs: []netip.Addr{addr}}
}

// resolve returns the addresses of host (an IP literal resolves to itself).
func (p *Policy) resolve(ctx context.Context, host string, access Access) ([]netip.Addr, error) {
	if blockedHostName(host) {
		return nil, &Error{Reason: ReasonBlocked, Host: host, Access: access}
	}
	if addr, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return []netip.Addr{addr.Unmap()}, nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	answers, err := p.resolver.LookupIPAddr(lookupCtx, host)
	if err != nil || len(answers) == 0 {
		return nil, &Error{Reason: ReasonUnresolvable, Host: host, Access: access}
	}
	addrs := make([]netip.Addr, 0, len(answers))
	for _, answer := range answers {
		addr, ok := netip.AddrFromSlice(answer.IP)
		if !ok {
			return nil, &Error{Reason: ReasonUnresolvable, Host: host, Access: access}
		}
		addrs = append(addrs, addr.Unmap())
	}
	return addrs, nil
}

// checkHost resolves host and checks every answer; all must be allowed.
func (p *Policy) checkHost(ctx context.Context, host string, access Access) ([]netip.Addr, error) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	addrs, err := p.resolve(ctx, host, access)
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		if err := p.allowed(host, addr, access); err != nil {
			refusal := err.(*Error)
			refusal.addrs = addrs
			return nil, refusal
		}
	}
	return addrs, nil
}

// Check validates raw as an outbound HTTPS endpoint: https, no userinfo, and
// every address the host resolves to allowed for access. It returns the
// checked addresses.
func (p *Policy) Check(ctx context.Context, raw string, access Access) ([]netip.Addr, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" || u.User != nil {
		host := ""
		if err == nil {
			host = u.Hostname()
		}
		return nil, &Error{Reason: ReasonInvalidURL, Host: host, Access: access}
	}
	return p.checkHost(ctx, u.Hostname(), access)
}

// DialContext is a dialer for access: it resolves the host, refuses the
// connection unless every answer is allowed, and dials a checked address
// (never resolving again), so DNS rebinding cannot swap the destination.
func (p *Policy) DialContext(access Access) func(ctx context.Context, network, address string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addrs, err := p.checkHost(ctx, host, access)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, addr := range addrs {
			conn, dialErr := p.dial(ctx, network, net.JoinHostPort(addr.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, lastErr
	}
}

// Transport is an http.Transport whose every connection goes through
// DialContext(access), without environment proxies (they would resolve the
// destination themselves), verifying TLS against the policy's roots.
func (p *Policy) Transport(access Access) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = p.DialContext(access)
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: p.roots}
	return transport
}

// Client is an HTTP client for access. Redirects (at most three) are
// re-checked against the policy, and a request that started at a trusted
// private host may only be redirected to another trusted host.
func (p *Policy) Client(timeout time.Duration, access Access) *http.Client {
	return &http.Client{Timeout: timeout, Transport: p.Transport(access), CheckRedirect: p.CheckRedirect(access)}
}

// CheckRedirect is Client's redirect rule, for clients that build their own.
func (p *Policy) CheckRedirect(access Access) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		if _, err := p.Check(req.Context(), req.URL.String(), access); err != nil {
			return fmt.Errorf("unsafe redirect: %w", err)
		}
		if len(via) > 0 && access == TrustedPrivate && p.HostTrusted(via[0].URL.Hostname()) && !p.HostTrusted(req.URL.Hostname()) {
			return errors.New("unsafe redirect: a trusted private endpoint may only redirect to another trusted endpoint")
		}
		return nil
	}
}
