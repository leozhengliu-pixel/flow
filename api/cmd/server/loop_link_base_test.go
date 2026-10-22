package main

import (
	"context"
	"testing"

	"flow/api/internal/domain"
)

// Links a background loop run writes point at the app: FLOW_APP_URL first, then the allowed origin; with neither
// (and no browser origin seen yet) they stay relative instead of naming the run's synthetic host.
func TestLoopRunLinkBase(t *testing.T) {
	owner := domain.User{ID: "usr_owner"}
	cases := []struct {
		name, appURL, allowedOrigin, want string
	}{
		{"app url", "https://flow.example.com/", "https://other.example.com", "https://flow.example.com"},
		{"allowed origin", "", "https://flow.example.com", "https://flow.example.com"},
		{"nothing configured", "", "", ""},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Setenv("FLOW_APP_URL", item.appURL)
			s := &server{allowedOrigin: item.allowedOrigin}
			request, err := s.loopRunRequest(context.Background(), "acme", owner)
			if err != nil {
				t.Fatal(err)
			}
			if got := agentLinkBaseURL(request); got != item.want {
				t.Fatalf("link base = %q, want %q", got, item.want)
			}
			url := mcpIssueURL("acme", "ENG-1", map[string]any{"__flowBaseURL": agentLinkBaseURL(request)})
			if want := item.want + "/acme/issue/ENG-1"; url != want {
				t.Fatalf("issue url = %q, want %q", url, want)
			}
		})
	}
}
