package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"flow/api/internal/outbound"
)

// Outbound requests to URLs that members, admins or remote servers supply go
// through one policy (api/internal/outbound): public HTTPS by default, plus
// the private hosts an operator trusts with FLOW_TRUSTED_PRIVATE_ENDPOINTS
// and FLOW_TRUSTED_PRIVATE_NETWORKS (and FLOW_TRUSTED_CA_FILE for their
// certificates). The same policy checks a URL when it is saved and every
// connection made to it.
//
// Every caller picks its access explicitly:
//
//	outboundTrustedPrivate (an operator may open intranet hosts):
//	  MCP connectors (install, OAuth discovery, runtime transport, the
//	  coding-session MCP gateway, URL elicitations), webhooks and the audit
//	  log stream, integration deliveries and integration OAuth token/revoke
//	  endpoints, enterprise identity providers (OIDC discovery, SAML
//	  metadata), self-hosted GitLab / GitHub Enterprise and the review
//	  provider APIs.
//	outboundPublicOnly (never the intranet, whatever is configured):
//	  link previews, attachments imported from a URL, file previews: any
//	  member can paste those URLs, so they must not become an intranet probe.
type outboundAccess = outbound.Access

const (
	outboundPublicOnly     = outbound.PublicOnly
	outboundTrustedPrivate = outbound.TrustedPrivate
)

// outboundPolicy is set from the configuration at startup (main); tests
// replace it to resolve and dial fake intranet hosts.
var outboundPolicy = outbound.Default()

// outboundEndpointError checks raw for access. The error is safe to show to
// any member (it never names resolved addresses); the detail is logged.
func outboundEndpointError(ctx context.Context, raw string, access outboundAccess) error {
	_, err := outboundPolicy.Check(ctx, raw, access)
	var refusal *outbound.Error
	if errors.As(err, &refusal) && refusal.Reason != outbound.ReasonInvalidURL {
		slog.InfoContext(ctx, "outbound endpoint refused", "detail", refusal.Detail())
	}
	return err
}

func safeOutboundHTTPS(ctx context.Context, raw string, access outboundAccess) bool {
	return outboundEndpointError(ctx, raw, access) == nil
}

// secureOutboundClient is an HTTP client whose every connection (and
// redirect) is checked against the policy for access.
func secureOutboundClient(timeout time.Duration, access outboundAccess) *http.Client {
	return outboundPolicy.Client(timeout, access)
}

// operatorCATransport is the default transport verifying TLS against the
// system roots plus FLOW_TRUSTED_CA_FILE, for destinations the deployment
// itself configured (FLOW_INTEGRATION_GITLAB_HOST) rather than a member.
func operatorCATransport() http.RoundTripper {
	if outboundPolicy.RootCAs() == nil {
		return http.DefaultTransport
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: outboundPolicy.RootCAs()}
	return transport
}

// integrationEndpointError is outboundEndpointError that also accepts
// plain-HTTP loopback URLs in local development (allowLocal).
func integrationEndpointError(ctx context.Context, raw string, allowLocal bool, access outboundAccess) error {
	if allowLocal && safeLocalDevelopmentURL(raw) {
		return nil
	}
	return outboundEndpointError(ctx, raw, access)
}

func integrationEndpointSafe(ctx context.Context, raw string, allowLocal bool, access outboundAccess) bool {
	return integrationEndpointError(ctx, raw, allowLocal, access) == nil
}
