package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"flow/api/internal/codehost"
)

func signedHeader(event, delivery string, body []byte, secret string) http.Header {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	header := http.Header{}
	header.Set("X-GitHub-Event", event)
	header.Set("X-GitHub-Delivery", delivery)
	header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return header
}

const prPayload = `{"action":"%s","installation":{"id":42},"repository":{"id":1,"full_name":"acme/app"},"sender":{"login":"alice","type":"User"},
"pull_request":{"id":1010,"number":10,"html_url":"https://github.com/acme/app/pull/10","title":"Fix","state":"%s","draft":false,"merged":%s,
"user":{"login":"flow-dev[bot]"},"head":{"ref":"flow/fix","sha":"abc"},"base":{"ref":"main","repo":{"full_name":"acme/app"}}}}`

func TestWebhookSignature(t *testing.T) {
	fake := newFakeGitHub(t)
	host, _ := newTestHost(t, fake, nil)
	body := []byte(`{"zen":"hi"}`)
	if _, err := host.ParseWebhook(t.Context(), signedHeader("ping", "d", body, "wrong"), body); !errors.Is(err, codehost.ErrBadSignature) {
		t.Fatalf("wrong secret: %v", err)
	}
	header := signedHeader("ping", "d", body, "whsec")
	if _, err := host.ParseWebhook(t.Context(), header, []byte(`{"zen":"tampered"}`)); !errors.Is(err, codehost.ErrBadSignature) {
		t.Fatalf("tampered body: %v", err)
	}
	header.Del("X-Hub-Signature-256")
	if _, err := host.ParseWebhook(t.Context(), header, body); !errors.Is(err, codehost.ErrBadSignature) {
		t.Fatalf("missing signature: %v", err)
	}
	header.Set("X-Hub-Signature-256", "sha256=zz")
	if _, err := host.ParseWebhook(t.Context(), header, body); !errors.Is(err, codehost.ErrBadSignature) {
		t.Fatalf("malformed signature: %v", err)
	}
	event, err := host.ParseWebhook(t.Context(), signedHeader("ping", "d1", body, "whsec"), body)
	if err != nil || event.Type != codehost.EventIgnored || event.DeliveryID != "d1" {
		t.Fatalf("ping: %+v %v", event, err)
	}
}

func TestWebhookMapping(t *testing.T) {
	fake := newFakeGitHub(t)
	host, _ := newTestHost(t, fake, nil)
	sprintf := fmt.Sprintf
	cases := []struct {
		name  string
		event string
		body  string
		want  codehost.WebhookEventType
		check func(t *testing.T, event codehost.WebhookEvent)
	}{
		{name: "opened", event: "pull_request", body: sprintf(prPayload, "opened", "open", "false"), want: codehost.EventChangeRequestOpened, check: func(t *testing.T, e codehost.WebhookEvent) {
			if e.ChangeRequest.Number != 10 || e.ChangeRequest.HeadBranch != "flow/fix" || e.InstallationID != "42" || e.Repo.FullName != "acme/app" || e.Repo.HostName != "github.com" || e.AuthorLogin != "alice" || e.Action != "opened" {
				t.Fatalf("%+v %+v", e, e.ChangeRequest)
			}
		}},
		{name: "reopened", event: "pull_request", body: sprintf(prPayload, "reopened", "open", "false"), want: codehost.EventChangeRequestOpened},
		{name: "merged", event: "pull_request", body: sprintf(prPayload, "closed", "closed", "true"), want: codehost.EventChangeRequestMerged, check: func(t *testing.T, e codehost.WebhookEvent) {
			if e.ChangeRequest.State != "merged" {
				t.Fatalf("state %s", e.ChangeRequest.State)
			}
		}},
		{name: "closed", event: "pull_request", body: sprintf(prPayload, "closed", "closed", "false"), want: codehost.EventChangeRequestClosed},
		{name: "ready for review", event: "pull_request", body: sprintf(prPayload, "ready_for_review", "open", "false"), want: codehost.EventChangeRequestUpdate},
		{name: "synchronize", event: "pull_request", body: sprintf(prPayload, "synchronize", "open", "false"), want: codehost.EventChangeRequestUpdate},
		{name: "labeled is ignored", event: "pull_request", body: sprintf(prPayload, "labeled", "open", "false"), want: codehost.EventIgnored},
		{name: "PR comment", event: "issue_comment", body: `{"action":"created","installation":{"id":42},"repository":{"full_name":"acme/app"},"sender":{"login":"alice"},"issue":{"number":10,"title":"Fix","state":"open","pull_request":{"url":"x"}},"comment":{"id":5,"body":"@flow fix the test","user":{"login":"alice","type":"User"}}}`, want: codehost.EventComment, check: func(t *testing.T, e codehost.WebhookEvent) {
			if e.ChangeRequest.Number != 10 || e.CommentID != "5" || e.CommentBody != "@flow fix the test" || e.AuthorIsBot {
				t.Fatalf("%+v", e)
			}
		}},
		{name: "issue comment is ignored", event: "issue_comment", body: `{"action":"created","issue":{"number":3},"comment":{"id":5,"body":"x"}}`, want: codehost.EventIgnored},
		{name: "edited comment is ignored", event: "issue_comment", body: `{"action":"edited","issue":{"number":3,"pull_request":{}},"comment":{"id":5,"body":"x"}}`, want: codehost.EventIgnored},
		{name: "review comment", event: "pull_request_review_comment", body: `{"action":"created","installation":{"id":42},"repository":{"full_name":"acme/app"},"pull_request":{"number":10,"head":{"ref":"flow/fix"},"base":{"ref":"main"}},"comment":{"id":6,"body":"nit","path":"a.go","line":12,"diff_hunk":"@@ -1 +1 @@","html_url":"https://github.com/acme/app/pull/10#discussion_r6","user":{"login":"ci[bot]","type":"Bot","email":"ci@example.com"}}}`, want: codehost.EventReviewComment, check: func(t *testing.T, e codehost.WebhookEvent) {
			if e.CommentPath != "a.go" || e.CommentLine != 12 || !e.AuthorIsBot || e.AuthorLogin != "ci[bot]" {
				t.Fatalf("%+v", e)
			}
			if e.CommentDiffHunk != "@@ -1 +1 @@" || e.CommentURL != "https://github.com/acme/app/pull/10#discussion_r6" || e.AuthorEmail != "ci@example.com" {
				t.Fatalf("%+v", e)
			}
		}},
		{name: "review", event: "pull_request_review", body: `{"action":"submitted","installation":{"id":42},"repository":{"full_name":"acme/app"},"pull_request":{"number":10,"head":{"ref":"flow/fix"},"base":{"ref":"main"}},"review":{"id":8,"body":"please fix","state":"CHANGES_REQUESTED","user":{"login":"bob"}}}`, want: codehost.EventReview, check: func(t *testing.T, e codehost.WebhookEvent) {
			if e.ReviewState != "changes_requested" || e.AuthorLogin != "bob" || e.CommentBody != "please fix" {
				t.Fatalf("%+v", e)
			}
		}},
		{name: "check suite failure", event: "check_suite", body: `{"action":"completed","installation":{"id":42},"repository":{"full_name":"acme/app"},"check_suite":{"head_branch":"flow/fix","head_sha":"abc","status":"completed","conclusion":"failure","pull_requests":[{"number":10,"head":{"ref":"flow/fix","sha":"abc"},"base":{"ref":"main"}}]}}`, want: codehost.EventCIStatus, check: func(t *testing.T, e codehost.WebhookEvent) {
			if e.CI.State != "failure" || e.ChangeRequest.Number != 10 {
				t.Fatalf("%+v %+v", e.CI, e.ChangeRequest)
			}
		}},
		{name: "check suite success without PR", event: "check_suite", body: `{"action":"completed","check_suite":{"head_branch":"main","head_sha":"abc","conclusion":"success"}}`, want: codehost.EventCIStatus, check: func(t *testing.T, e codehost.WebhookEvent) {
			if e.CI.State != "success" || e.ChangeRequest.HeadBranch != "main" {
				t.Fatalf("%+v", e)
			}
		}},
		{name: "installation suspended", event: "installation", body: `{"action":"suspend","installation":{"id":42,"account":{"login":"acme","type":"Organization"},"repository_selection":"all"}}`, want: codehost.EventInstallation, check: func(t *testing.T, e codehost.WebhookEvent) {
			if e.Installation == nil || e.Installation.SuspendedAt == nil || e.Installation.AccountLogin != "acme" || e.Action != "suspend" || e.InstallationID != "42" {
				t.Fatalf("%+v", e.Installation)
			}
		}},
		{name: "installation unsuspended", event: "installation", body: `{"action":"unsuspend","installation":{"id":42,"account":{"login":"acme"},"suspended_at":"2026-01-01T00:00:00Z"}}`, want: codehost.EventInstallation, check: func(t *testing.T, e codehost.WebhookEvent) {
			if e.Installation.SuspendedAt != nil {
				t.Fatal("unsuspend clears suspension")
			}
		}},
		{name: "installation deleted", event: "installation", body: `{"action":"deleted","installation":{"id":42,"account":{"login":"acme"}}}`, want: codehost.EventInstallation},
		{name: "repositories changed", event: "installation_repositories", body: `{"action":"added","installation":{"id":42,"account":{"login":"acme"},"repository_selection":"selected"}}`, want: codehost.EventInstallation, check: func(t *testing.T, e codehost.WebhookEvent) {
			if e.Installation.RepositorySelection != "selected" || e.Action != "added" {
				t.Fatalf("%+v", e)
			}
		}},
		{name: "push is ignored", event: "push", body: `{"ref":"refs/heads/main"}`, want: codehost.EventIgnored},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := []byte(c.body)
			event, err := host.ParseWebhook(t.Context(), signedHeader(c.event, "delivery-1", body, "whsec"), body)
			if err != nil {
				t.Fatal(err)
			}
			if event.Type != c.want || event.DeliveryID != "delivery-1" || event.CodeHost != "github" {
				t.Fatalf("got %s (%+v), want %s", event.Type, event, c.want)
			}
			if c.check != nil {
				c.check(t, event)
			}
		})
	}
	body := []byte(`not json`)
	if _, err := host.ParseWebhook(t.Context(), signedHeader("pull_request", "d", body, "whsec"), body); err == nil {
		t.Fatal("invalid JSON must fail")
	}
}
