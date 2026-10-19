package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"flow/api/internal/codehost"
)

const testSecret = "flow_glwh_secret"

func hookHeader(event, token string) http.Header {
	header := http.Header{}
	if event != "" {
		header.Set("X-Gitlab-Event", event)
	}
	if token != "" {
		header.Set("X-Gitlab-Token", token)
	}
	header.Set("X-Gitlab-Event-UUID", "uuid-1")
	return header
}

const projectJSON = `"project":{"id":7,"path_with_namespace":"acme/platform/api","web_url":"https://gitlab.example.com/acme/platform/api"}`

func mrHook(action, state string) string {
	return fmt.Sprintf(`{"object_kind":"merge_request","user":{"username":"alice"},%s,"object_attributes":{"id":44001,"iid":27,"title":"Draft: Fix checkout","url":"https://gitlab.example.com/acme/platform/api/-/merge_requests/27","state":%q,"action":%q,"source_branch":"flow/fix","target_branch":"main","source_project_id":7,"target_project_id":7,"last_commit":{"id":"abc"}}}`, projectJSON, state, action)
}

const mrObject = `"merge_request":{"id":44001,"iid":27,"title":"Fix checkout","url":"https://gitlab.example.com/acme/platform/api/-/merge_requests/27","state":"opened","source_branch":"flow/fix","target_branch":"main","last_commit":{"id":"abc"}}`

func TestParseWebhook(t *testing.T) {
	host, _ := New(codehost.Dependencies{})
	ctx := WithWebhookVerification(context.Background(), WebhookVerification{Secrets: []string{testSecret}, ConnectionID: "integration_gitlab_1", SelfUsernames: []string{"flow-bot"}})
	cases := []struct {
		name   string
		event  string
		body   string
		check  func(t *testing.T, event codehost.WebhookEvent)
		typ    codehost.WebhookEventType
		author string
	}{
		{name: "mr opened", event: HookMergeRequest, body: mrHook("open", "opened"), typ: codehost.EventChangeRequestOpened, check: func(t *testing.T, e codehost.WebhookEvent) {
			cr := e.ChangeRequest
			if cr == nil || cr.Number != 27 || cr.ExternalID != "44001" || cr.HeadBranch != "flow/fix" || cr.BaseBranch != "main" || cr.HeadSHA != "abc" || cr.State != "open" || !cr.Draft || cr.Repo.FullName != "acme/platform/api" || cr.Repo.ID != "7" {
				t.Fatalf("change request: %#v", cr)
			}
			if e.HostName != "gitlab.example.com" || e.InstallationID != "integration_gitlab_1" || e.DeliveryID != "uuid-1" || e.AuthorLogin != "alice" || e.AuthorIsBot || e.CodeHost != "gitlab" {
				t.Fatalf("event: %#v", e)
			}
		}},
		{name: "mr reopened", event: HookMergeRequest, body: mrHook("reopen", "opened"), typ: codehost.EventChangeRequestOpened},
		{name: "mr merged", event: HookMergeRequest, body: mrHook("merge", "merged"), typ: codehost.EventChangeRequestMerged, check: func(t *testing.T, e codehost.WebhookEvent) {
			if e.ChangeRequest.State != "merged" {
				t.Fatalf("state: %s", e.ChangeRequest.State)
			}
		}},
		{name: "mr closed", event: HookMergeRequest, body: mrHook("close", "closed"), typ: codehost.EventChangeRequestClosed},
		{name: "mr push", event: HookMergeRequest, body: mrHook("update", "opened"), typ: codehost.EventChangeRequestUpdate},
		{name: "mr approved", event: HookMergeRequest, body: mrHook("approved", "opened"), typ: codehost.EventReview, check: func(t *testing.T, e codehost.WebhookEvent) {
			if e.ReviewState != "approved" {
				t.Fatalf("review state: %q", e.ReviewState)
			}
		}},
		{name: "mr without action", event: HookMergeRequest, body: mrHook("", "merged"), typ: codehost.EventChangeRequestMerged},
		{name: "mr comment", event: HookNote, body: `{"object_kind":"note","user":{"username":"bob"},` + projectJSON + `,"object_attributes":{"id":9001,"note":"@flow please add tests","noteable_type":"MergeRequest","system":false,"type":null},` + mrObject + `}`, typ: codehost.EventComment, check: func(t *testing.T, e codehost.WebhookEvent) {
			if e.CommentID != "9001" || e.CommentBody != "@flow please add tests" || e.ChangeRequest == nil || e.ChangeRequest.Number != 27 || e.AuthorLogin != "bob" || e.CommentPath != "" {
				t.Fatalf("comment: %#v", e)
			}
		}},
		{name: "diff note", event: HookNote, body: `{"object_kind":"note","user":{"username":"bob"},` + projectJSON + `,"object_attributes":{"id":9002,"note":"@flow rename this","noteable_type":"MergeRequest","type":"DiffNote","position":{"new_path":"src/app.ts","old_path":"src/app.ts","new_line":42,"old_line":null}},` + mrObject + `}`, typ: codehost.EventReviewComment, check: func(t *testing.T, e codehost.WebhookEvent) {
			if e.CommentPath != "src/app.ts" || e.CommentLine != 42 {
				t.Fatalf("diff note: %#v", e)
			}
		}},
		{name: "removed-line diff note", event: HookNote, body: `{"object_kind":"note","user":{"username":"bob"},` + projectJSON + `,"object_attributes":{"id":9003,"note":"why?","noteable_type":"MergeRequest","type":"DiffNote","position":{"new_path":"","old_path":"old.go","new_line":null,"old_line":7}},` + mrObject + `}`, typ: codehost.EventReviewComment, check: func(t *testing.T, e codehost.WebhookEvent) {
			if e.CommentPath != "old.go" || e.CommentLine != 7 {
				t.Fatalf("old-line note: %#v", e)
			}
		}},
		{name: "flow's own note", event: HookNote, body: `{"object_kind":"note","user":{"username":"flow-bot"},` + projectJSON + `,"object_attributes":{"id":9004,"note":"Session finished","noteable_type":"MergeRequest"},` + mrObject + `}`, typ: codehost.EventComment, check: func(t *testing.T, e codehost.WebhookEvent) {
			if !e.AuthorIsBot {
				t.Fatal("Flow's own note must be marked as bot")
			}
		}},
		{name: "project token bot note", event: HookNote, body: `{"object_kind":"note","user":{"username":"project_7_bot_3f2a"},` + projectJSON + `,"object_attributes":{"id":9005,"note":"glab reply","noteable_type":"MergeRequest"},` + mrObject + `}`, typ: codehost.EventComment, check: func(t *testing.T, e codehost.WebhookEvent) {
			if !e.AuthorIsBot {
				t.Fatal("project access token bot must be marked as bot")
			}
		}},
		{name: "system note", event: HookNote, body: `{"object_kind":"note","user":{"username":"bob"},` + projectJSON + `,"object_attributes":{"id":9006,"note":"added 1 commit","noteable_type":"MergeRequest","system":true},` + mrObject + `}`, typ: codehost.EventIgnored},
		{name: "issue note", event: HookNote, body: `{"object_kind":"note","user":{"username":"bob"},` + projectJSON + `,"object_attributes":{"id":9007,"note":"@flow","noteable_type":"Issue"}}`, typ: codehost.EventIgnored},
		{name: "pipeline failed on MR", event: HookPipeline, body: `{"object_kind":"pipeline","user":{"username":"bob"},` + projectJSON + `,"object_attributes":{"id":501,"ref":"flow/fix","sha":"def","status":"failed"},` + mrObject + `,"builds":[{"name":"unit","stage":"test","status":"failed"},{"name":"build","stage":"build","status":"success"}]}`, typ: codehost.EventCIStatus, check: func(t *testing.T, e codehost.WebhookEvent) {
			if e.CI == nil || e.CI.State != "failure" || e.CI.Summary != "Pipeline #501 failed: failed jobs test/unit" || e.CI.URL != "https://gitlab.example.com/acme/platform/api/-/pipelines/501" {
				t.Fatalf("ci: %#v", e.CI)
			}
			if e.ChangeRequest == nil || e.ChangeRequest.Number != 27 || e.ChangeRequest.HeadSHA != "def" {
				t.Fatalf("pipeline MR: %#v", e.ChangeRequest)
			}
		}},
		{name: "branch pipeline succeeded", event: HookPipeline, body: `{"object_kind":"pipeline",` + projectJSON + `,"object_attributes":{"id":502,"ref":"flow/other","sha":"123","status":"success"}}`, typ: codehost.EventCIStatus, check: func(t *testing.T, e codehost.WebhookEvent) {
			if e.ChangeRequest == nil || e.ChangeRequest.Number != 0 || e.ChangeRequest.HeadBranch != "flow/other" || e.CI.State != "success" {
				t.Fatalf("branch pipeline: %#v", e)
			}
		}},
		{name: "running pipeline", event: HookPipeline, body: `{"object_kind":"pipeline",` + projectJSON + `,"object_attributes":{"id":503,"ref":"flow/fix","status":"running"}}`, typ: codehost.EventIgnored},
		{name: "push hook", event: "Push Hook", body: `{"object_kind":"push",` + projectJSON + `}`, typ: codehost.EventIgnored},
		{name: "object kind fallback", event: "", body: mrHook("open", "opened"), typ: codehost.EventChangeRequestOpened},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			event, err := host.ParseWebhook(ctx, hookHeader(c.event, testSecret), []byte(c.body))
			if err != nil {
				t.Fatal(err)
			}
			if event.Type != c.typ {
				t.Fatalf("type = %s, want %s", event.Type, c.typ)
			}
			if c.check != nil {
				c.check(t, event)
			}
		})
	}
}

func TestParseWebhookVerification(t *testing.T) {
	host, _ := New(codehost.Dependencies{})
	body := []byte(mrHook("open", "opened"))
	verified := WithWebhookVerification(context.Background(), WebhookVerification{Secrets: []string{testSecret}})
	for name, c := range map[string]struct {
		ctx   context.Context
		token string
	}{
		"wrong token":   {verified, "nope"},
		"missing token": {verified, ""},
		"no secrets":    {context.Background(), testSecret},
		"empty secret":  {WithWebhookVerification(context.Background(), WebhookVerification{Secrets: []string{""}}), ""},
	} {
		if _, err := host.ParseWebhook(c.ctx, hookHeader(HookMergeRequest, c.token), body); !errors.Is(err, codehost.ErrBadSignature) {
			t.Errorf("%s: expected ErrBadSignature, got %v", name, err)
		}
	}
	if _, err := host.ParseWebhook(verified, hookHeader(HookMergeRequest, testSecret), []byte("{")); err == nil {
		t.Error("invalid JSON must fail")
	}
	header := hookHeader(HookMergeRequest, testSecret)
	header.Set("Idempotency-Key", "idem-1")
	if event, _ := host.ParseWebhook(verified, header, body); event.DeliveryID != "idem-1" {
		t.Errorf("Idempotency-Key must win: %q", event.DeliveryID)
	}
	header = http.Header{"X-Gitlab-Token": {testSecret}, "X-Gitlab-Event": {HookMergeRequest}}
	if event, _ := host.ParseWebhook(verified, header, body); len(event.DeliveryID) != 64 {
		t.Errorf("body hash fallback: %q", event.DeliveryID)
	}
}
