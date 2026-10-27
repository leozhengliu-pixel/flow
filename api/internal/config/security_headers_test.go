package config

import (
	"reflect"
	"strings"
	"testing"
)

var securityHeaderEnv = []string{
	"FLOW_SECURITY_HEADERS", "FLOW_SECURITY_HEADERS_SKIP", "FLOW_SECURITY_REFERRER_POLICY", "FLOW_SECURITY_PERMISSIONS_POLICY",
	"FLOW_SECURITY_FRAME_ANCESTORS", "FLOW_SECURITY_HSTS", "FLOW_SECURITY_HSTS_MAX_AGE", "FLOW_SECURITY_HSTS_INCLUDE_SUBDOMAINS",
	"FLOW_SECURITY_HSTS_PRELOAD", "FLOW_SECURITY_CSP", "FLOW_SECURITY_CSP_REPORT_URI", "FLOW_SECURITY_CSP_EXTRA_SOURCES",
	"FLOW_TRUST_PROXY_HEADERS",
}

func clearSecurityHeaderEnv(t *testing.T) {
	for _, key := range securityHeaderEnv {
		t.Setenv(key, "")
	}
}

func TestSecurityHeadersDefaults(t *testing.T) {
	clearSecurityHeaderEnv(t)
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.SecurityHeaders, DefaultSecurityHeaders()) {
		t.Fatalf("defaults %#v", loaded.SecurityHeaders)
	}
	defaults := loaded.SecurityHeaders
	if !defaults.Enabled || defaults.CSP != CSPReportOnly || defaults.HSTS != HSTSAuto || defaults.HSTSMaxAge != 31536000 || defaults.HSTSIncludeSubdomains || defaults.HSTSPreload || defaults.TrustProxyHeaders || !reflect.DeepEqual(defaults.FrameAncestors, []string{"'self'"}) {
		t.Fatalf("unexpected defaults %#v", defaults)
	}
}

func TestSecurityHeadersLoadsOverrides(t *testing.T) {
	clearSecurityHeaderEnv(t)
	t.Setenv("FLOW_SECURITY_HEADERS_SKIP", "strict-transport-security, x-frame-options")
	t.Setenv("FLOW_SECURITY_REFERRER_POLICY", "no-referrer")
	t.Setenv("FLOW_SECURITY_FRAME_ANCESTORS", "self, https://portal.example.com https://*.intranet.example.com")
	t.Setenv("FLOW_SECURITY_HSTS_MAX_AGE", "63072000")
	t.Setenv("FLOW_SECURITY_HSTS_INCLUDE_SUBDOMAINS", "true")
	t.Setenv("FLOW_SECURITY_HSTS_PRELOAD", "true")
	t.Setenv("FLOW_SECURITY_CSP", "ENFORCE")
	t.Setenv("FLOW_SECURITY_CSP_REPORT_URI", "https://reports.example.com/csp")
	t.Setenv("FLOW_SECURITY_CSP_EXTRA_SOURCES", "connect-src https://telemetry.example.com; img-src https://cdn.example.com/images/ ;")
	t.Setenv("FLOW_TRUST_PROXY_HEADERS", "true")
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.SecurityHeaders
	if !reflect.DeepEqual(got.Skip, []string{"Strict-Transport-Security", "X-Frame-Options"}) || !got.Skips("strict-transport-security") || got.Skips("Referrer-Policy") {
		t.Fatalf("skip %#v", got.Skip)
	}
	if got.ReferrerPolicy != "no-referrer" || got.CSP != CSPEnforce || got.HSTSMaxAge != 63072000 || !got.HSTSIncludeSubdomains || !got.HSTSPreload || !got.TrustProxyHeaders || got.CSPReportURI != "https://reports.example.com/csp" {
		t.Fatalf("overrides %#v", got)
	}
	if !reflect.DeepEqual(got.FrameAncestors, []string{"'self'", "https://portal.example.com", "https://*.intranet.example.com"}) {
		t.Fatalf("frame ancestors %#v", got.FrameAncestors)
	}
	if !reflect.DeepEqual(got.CSPExtraSources, map[string][]string{"connect-src": {"https://telemetry.example.com"}, "img-src": {"https://cdn.example.com/images/"}}) {
		t.Fatalf("extra sources %#v", got.CSPExtraSources)
	}
	t.Setenv("FLOW_SECURITY_HEADERS", "off")
	if loaded, err := Load(); err != nil || loaded.SecurityHeaders.Enabled {
		t.Fatalf("FLOW_SECURITY_HEADERS=off: %v %v", loaded.SecurityHeaders.Enabled, err)
	}
}

func TestSecurityHeadersRejectsUnsafeOrAmbiguousSettings(t *testing.T) {
	cases := map[string][2]string{
		"unknown skipped header":           {"FLOW_SECURITY_HEADERS_SKIP", "X-XSS-Protection"},
		"unknown referrer policy":          {"FLOW_SECURITY_REFERRER_POLICY", "everything"},
		"bare wildcard ancestor":           {"FLOW_SECURITY_FRAME_ANCESTORS", "*"},
		"plain-http ancestor":              {"FLOW_SECURITY_FRAME_ANCESTORS", "http://portal.example.com"},
		"ancestor with a path":             {"FLOW_SECURITY_FRAME_ANCESTORS", "https://portal.example.com/app"},
		"none mixed with origins":          {"FLOW_SECURITY_FRAME_ANCESTORS", "none https://portal.example.com"},
		"unknown HSTS mode":                {"FLOW_SECURITY_HSTS", "always"},
		"negative max-age":                 {"FLOW_SECURITY_HSTS_MAX_AGE", "-1"},
		"preload without subdomains":       {"FLOW_SECURITY_HSTS_PRELOAD", "true"},
		"unknown CSP mode":                 {"FLOW_SECURITY_CSP", "strict"},
		"report URI over plain http":       {"FLOW_SECURITY_CSP_REPORT_URI", "http://reports.example.com/csp"},
		"protocol-relative report URI":     {"FLOW_SECURITY_CSP_REPORT_URI", "//reports.example.com/csp"},
		"unknown extra directive":          {"FLOW_SECURITY_CSP_EXTRA_SOURCES", "default-src https://cdn.example.com"},
		"bare wildcard source":             {"FLOW_SECURITY_CSP_EXTRA_SOURCES", "img-src *"},
		"unsafe-eval":                      {"FLOW_SECURITY_CSP_EXTRA_SOURCES", "script-src 'unsafe-eval'"},
		"unsafe-inline scripts":            {"FLOW_SECURITY_CSP_EXTRA_SOURCES", "script-src 'unsafe-inline'"},
		"scripts from any https host":      {"FLOW_SECURITY_CSP_EXTRA_SOURCES", "script-src https:"},
		"unknown keyword":                  {"FLOW_SECURITY_CSP_EXTRA_SOURCES", "connect-src 'strict-dynamic'"},
		"source with a separator injected": {"FLOW_SECURITY_CSP_EXTRA_SOURCES", "img-src https://cdn.example.com,https://x.example"},
	}
	for name, setting := range cases {
		t.Run(name, func(t *testing.T) {
			clearSecurityHeaderEnv(t)
			t.Setenv(setting[0], setting[1])
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FLOW_SECURITY_") {
				t.Fatalf("%s=%q accepted: %v", setting[0], setting[1], err)
			}
		})
	}
}

func TestSecurityHeadersAcceptsDocumentedEscapeHatches(t *testing.T) {
	for _, setting := range [][2]string{
		{"FLOW_SECURITY_FRAME_ANCESTORS", "none"},
		{"FLOW_SECURITY_FRAME_ANCESTORS", "'self' http://localhost:3000"},
		{"FLOW_SECURITY_CSP_REPORT_URI", "/csp-reports"},
		{"FLOW_SECURITY_CSP_EXTRA_SOURCES", "script-src https://cdn.example.com 'sha256-abc='; img-src http://127.0.0.1:9000; connect-src https:; style-src 'unsafe-inline'; worker-src blob:"},
		{"FLOW_SECURITY_CSP", "off"},
		{"FLOW_SECURITY_HSTS", "off"},
	} {
		clearSecurityHeaderEnv(t)
		t.Setenv(setting[0], setting[1])
		if _, err := Load(); err != nil {
			t.Fatalf("%s=%q: %v", setting[0], setting[1], err)
		}
	}
}
