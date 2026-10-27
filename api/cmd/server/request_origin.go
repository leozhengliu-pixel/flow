package main

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"

	"github.com/coder/websocket"

	appconfig "flow/api/internal/config"
)

// forwardingPolicy decides which forwarding headers Flow believes when it
// works out how a browser reached it: the client address (rate limits, login
// alerts), the scheme (HSTS, Secure cookies, links) and the host (links in
// API responses, OAuth and SCIM metadata, signed file URLs).
//
// X-Forwarded-For, X-Forwarded-Proto and X-Forwarded-Host are read only when
// the operator trusts the proxy in front of Flow (FLOW_TRUST_PROXY_HEADERS)
// and, with FLOW_TRUSTED_PROXIES, only from a peer in that list. Each proxy
// appends to these headers, so the values a client sends come first and the
// one the nearest proxy wrote comes last: Flow reads from the right. The
// RFC 7239 Forwarded header is not read. Without trust Flow uses the
// connection itself: TLS on its listener, the Host header and the peer
// address.
type forwardingPolicy struct {
	trust bool
	// proxies are the trusted proxy ranges; empty trusts the direct peer as
	// the one proxy.
	proxies []netip.Prefix
	// appScheme and appHost are FLOW_APP_URL's: a request for the configured
	// host that did not say its scheme is taken to use the configured one, so
	// links stay https behind a TLS-terminating proxy Flow does not trust.
	appScheme string
	appHost   string
}

func newForwardingPolicy(trust bool, proxies []netip.Prefix, appURL string) *forwardingPolicy {
	policy := &forwardingPolicy{trust: trust, proxies: proxies}
	if parsed, err := url.Parse(strings.TrimSpace(appURL)); err == nil && parsed.Host != "" && (parsed.Scheme == "https" || parsed.Scheme == "http") {
		policy.appScheme, policy.appHost = parsed.Scheme, strings.ToLower(parsed.Host)
	}
	return policy
}

// forwardingPolicyFromConfig builds the policy from the validated security
// header settings (FLOW_TRUST_PROXY_HEADERS, FLOW_TRUSTED_PROXIES).
func forwardingPolicyFromConfig(config appconfig.SecurityHeadersConfig, appURL string) *forwardingPolicy {
	proxies, _ := config.TrustedProxyPrefixes()
	return newForwardingPolicy(config.TrustProxyHeaders, proxies, appURL)
}

// forwardingPolicyFromEnv is the policy for requests that did not pass
// through the server's handler chain (background runs, direct handler calls).
func forwardingPolicyFromEnv() *forwardingPolicy {
	proxies, _ := appconfig.ParseTrustedProxies(strings.Split(os.Getenv("FLOW_TRUSTED_PROXIES"), ","))
	return newForwardingPolicy(strings.EqualFold(strings.TrimSpace(os.Getenv("FLOW_TRUST_PROXY_HEADERS")), "true"), proxies, os.Getenv("FLOW_APP_URL"))
}

type forwardingPolicyKey struct{}

// withForwardingPolicy makes the policy available to every handler.
func withForwardingPolicy(policy *forwardingPolicy, next http.Handler) http.Handler {
	if policy == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), forwardingPolicyKey{}, policy)))
	})
}

func requestForwarding(r *http.Request) *forwardingPolicy {
	if policy, ok := r.Context().Value(forwardingPolicyKey{}).(*forwardingPolicy); ok && policy != nil {
		return policy
	}
	return forwardingPolicyFromEnv()
}

// fromTrustedProxy reports whether this request's forwarding headers were
// written by a proxy Flow trusts.
func (p *forwardingPolicy) fromTrustedProxy(r *http.Request) bool {
	if !p.trust {
		return false
	}
	if len(p.proxies) == 0 {
		return true
	}
	peer, ok := parseForwardedAddress(r.RemoteAddr)
	return ok && p.trustedHop(peer)
}

func (p *forwardingPolicy) trustedHop(addr netip.Addr) bool {
	for _, prefix := range p.proxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// lastForwardedValue is the right-most item across every line of a
// comma-separated forwarding header: the one the nearest proxy wrote.
func lastForwardedValue(header http.Header, name string) string {
	values := header.Values(name)
	for index := len(values) - 1; index >= 0; index-- {
		items := strings.Split(values[index], ",")
		if item := strings.TrimSpace(items[len(items)-1]); item != "" {
			return item
		}
	}
	return ""
}

// overHTTPS reports whether the request reached Flow over HTTPS as far as
// Flow can confirm it: TLS on this listener, or a trusted proxy's
// X-Forwarded-Proto. HSTS uses it.
func (p *forwardingPolicy) overHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return p.fromTrustedProxy(r) && strings.EqualFold(lastForwardedValue(r.Header, "X-Forwarded-Proto"), "https")
}

// scheme is the scheme the browser used: TLS on this listener, else a trusted
// proxy's X-Forwarded-Proto, else FLOW_APP_URL's scheme when the request is
// for the configured host, else http.
func (p *forwardingPolicy) scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if p.fromTrustedProxy(r) {
		switch strings.ToLower(lastForwardedValue(r.Header, "X-Forwarded-Proto")) {
		case "https":
			return "https"
		case "http":
			return "http"
		}
	}
	if p.appScheme != "" && strings.EqualFold(p.host(r), p.appHost) {
		return p.appScheme
	}
	return "http"
}

// host is the host the browser asked for: a trusted proxy's X-Forwarded-Host
// when it is a plain host[:port], else the Host header.
func (p *forwardingPolicy) host(r *http.Request) string {
	if p.fromTrustedProxy(r) {
		if forwarded := lastForwardedValue(r.Header, "X-Forwarded-Host"); validForwardedHost(forwarded) {
			return forwarded
		}
	}
	return r.Host
}

// clientAddress is the address rate limits and login alerts use. Without a
// trusted proxy it is the peer. Behind one it is the right-most
// X-Forwarded-For entry that is not itself a trusted proxy: entries to the
// left of it were written by the client and are ignored. An entry that is not
// an IP address stops the walk at the last hop that reported a valid one.
func (p *forwardingPolicy) clientAddress(r *http.Request) string {
	peer, ok := parseForwardedAddress(r.RemoteAddr)
	if !ok {
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			return host
		}
		return r.RemoteAddr
	}
	if !p.fromTrustedProxy(r) {
		return peer.String()
	}
	client := peer
	values := r.Header.Values("X-Forwarded-For")
	for line := len(values) - 1; line >= 0; line-- {
		items := strings.Split(values[line], ",")
		for index := len(items) - 1; index >= 0; index-- {
			if strings.TrimSpace(items[index]) == "" {
				continue
			}
			addr, ok := parseForwardedAddress(items[index])
			if !ok {
				return client.String()
			}
			client = addr
			if !p.trustedHop(addr) {
				return client.String()
			}
		}
	}
	return client.String()
}

// parseForwardedAddress reads an address as peers and proxies write it:
// "192.0.2.1", "192.0.2.1:443", "2001:db8::1", "[2001:db8::1]:443", with
// IPv4-mapped IPv6 read as IPv4 and any zone dropped.
func parseForwardedAddress(raw string) (netip.Addr, bool) {
	raw = strings.Trim(strings.TrimSpace(raw), `"`)
	if raw == "" {
		return netip.Addr{}, false
	}
	if addrPort, err := netip.ParseAddrPort(raw); err == nil {
		return addrPort.Addr().Unmap().WithZone(""), true
	}
	addr, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap().WithZone(""), true
}

// validForwardedHost accepts a host name, IPv4 address or bracketed IPv6
// address with an optional port, and nothing that could change a URL's
// meaning (paths, credentials, spaces).
func validForwardedHost(host string) bool {
	if host == "" || len(host) > 255 {
		return false
	}
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == ':', r == '[', r == ']', r == '_':
		default:
			return false
		}
	}
	parsed, err := url.Parse("http://" + host)
	return err == nil && parsed.Host == host && parsed.Hostname() != ""
}

// externalBaseURL is the origin the browser used to reach Flow, for links
// Flow builds from the request.
func externalBaseURL(r *http.Request) string {
	policy := requestForwarding(r)
	return policy.scheme(r) + "://" + policy.host(r)
}

// webSocketAcceptOptions are the options for accepting a WebSocket from the
// web app. The library accepts an Origin only when it equals the Host header,
// which fails behind a gateway that rewrites Host to its upstream. The
// browser's Origin is also accepted when it is the origin the browser used
// according to the forwarding policy (a trusted proxy's X-Forwarded-Host and
// X-Forwarded-Proto) or FLOW_APP_URL's; any other site stays refused, so a
// page elsewhere cannot open a socket with the visitor's cookie.
func webSocketAcceptOptions(r *http.Request) *websocket.AcceptOptions {
	policy := requestForwarding(r)
	quote := strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`).Replace
	patterns := []string{quote(policy.scheme(r) + "://" + policy.host(r))}
	if policy.appHost != "" {
		patterns = append(patterns, quote(policy.appScheme+"://"+policy.appHost))
	}
	return &websocket.AcceptOptions{OriginPatterns: patterns}
}

// secureCookie reports whether cookies set on this response get Secure: the
// request reached Flow over HTTPS, or FLOW_COOKIE_SECURE forces it.
func secureCookie(r *http.Request) bool {
	return requestForwarding(r).scheme(r) == "https" || strings.EqualFold(os.Getenv("FLOW_COOKIE_SECURE"), "true")
}

// authClientAddress is the client address rate limits key on.
func authClientAddress(r *http.Request) string {
	return requestForwarding(r).clientAddress(r)
}
