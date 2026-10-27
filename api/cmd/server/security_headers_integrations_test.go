package main

import (
	"net/http"
	"strings"
	"testing"
)

// Integration callbacks and webhooks: provider redirects land on these routes
// (so the browser shows them, or follows their redirect), and providers post
// events to them. Every answer, accepted or refused, is an API response with
// the full set, and a redirect carries it too.
func TestSecuritySurfaceIntegrationCallbacksAndWebhooks(t *testing.T) {
	forEachSurfaceMode(t, nil, func(t *testing.T, e *surfaceEnv) {
		html := http.Header{"Accept": {"text/html"}}
		type call struct {
			label, method, path string
			admin               bool
			header              http.Header
			body                string
			// lowest and highest acceptable status
			low, high int
		}
		calls := []call{
			{"GitHub App webhook, unsigned", http.MethodPost, "/api/integrations/github-app/webhook", false, jsonHeader(), `{}`, 400, 503},
			{"GitHub App webhook, forged signature", http.MethodPost, "/api/integrations/github-app/webhook", false, http.Header{"Content-Type": {"application/json"}, "X-Hub-Signature-256": {"sha256=00"}, "X-GitHub-Event": {"ping"}, "X-GitHub-Delivery": {"d1"}}, `{}`, 400, 503},
			{"code host webhook", http.MethodPost, "/api/integrations/github/webhook", false, jsonHeader(), `{}`, 400, 503},
			{"GitLab webhook", http.MethodPost, "/api/integrations/gitlab/webhook", false, jsonHeader(), `{}`, 400, 503},
			{"chat webhook", http.MethodPost, "/api/integrations/slack/unknown/chat/webhook", false, jsonHeader(), `{}`, 400, 404},
			{"chat webhook, GET", http.MethodGet, "/api/integrations/slack/unknown/chat/webhook", false, nil, ``, 401, 405},
			{"integration OAuth callback", http.MethodGet, "/api/integrations/github/oauth/callback?code=x&state=forged", false, html, ``, 302, 404},
			{"integration OAuth callback as JSON", http.MethodGet, "/api/integrations/github/oauth/callback?code=x&state=forged", false, http.Header{"Accept": {"application/json"}}, ``, 400, 404},
			{"integration OAuth callback, unknown provider", http.MethodGet, "/api/integrations/nope/oauth/callback?code=x", false, html, ``, 404, 404},
			{"GitHub App setup without a session", http.MethodGet, "/api/integrations/github-app/setup?installation_id=1&state=forged", false, html, ``, 401, 401},
			{"GitHub App setup", http.MethodGet, "/api/integrations/github-app/setup?installation_id=1&state=forged", true, html, ``, 302, 400},
			{"connector OAuth callback", http.MethodGet, "/api/connector-oauth/callback?code=x&state=forged", false, html, ``, 302, 400},
			{"code host account callback", http.MethodGet, "/api/account/code-host-accounts/oauth/callback?code=x&state=forged", true, html, ``, 302, 400},
			{"chat identity callback", http.MethodGet, "/api/integrations/slack/chat/identity/oauth/callback?code=x&state=forged", true, html, ``, 302, 404},
			{"e-mail intake, unknown address", http.MethodPost, "/api/email-intake/unknown-token/receive", false, jsonHeader(), `{}`, 400, 404},
			{"SCIM without a token", http.MethodGet, "/scim/v2/test-workspace/Users", false, nil, ``, 401, 401},
			{"calendar feed, unknown cycle", http.MethodGet, "/api/calendar/cycles/unknown?token=x", false, nil, ``, 401, 404},
			{"shared view, unknown", http.MethodGet, "/api/shared/views/unknown", false, nil, ``, 400, 404},
		}
		for _, item := range calls {
			request := surfaceRequest{method: item.method, path: item.path, header: item.header, body: []byte(item.body)}
			if item.admin {
				request.client = e.admin
				request.header = request.header.Clone()
				if request.header == nil {
					request.header = http.Header{}
				}
				request.header.Set("X-Workspace-Key", e.workspace)
			}
			response, _ := e.do(request)
			if response.StatusCode < item.low || response.StatusCode > item.high {
				t.Errorf("%s [%s]: status %d, want %d to %d", item.label, e.mode, response.StatusCode, item.low, item.high)
			}
			// Callbacks that carry a code or state in their URL send no
			// referrer at all, which is stricter than the default.
			if response.Header.Get("Referrer-Policy") == "no-referrer" {
				response.Header.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			}
			e.checkHeaders(item.label, response, kindAPI)
			if response.StatusCode == http.StatusFound && !strings.Contains(response.Header.Get("Location"), "/") {
				t.Errorf("%s: redirect to %q", item.label, response.Header.Get("Location"))
			}
		}
	})
}
