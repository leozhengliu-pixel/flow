package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type capturedWebhook struct {
	header http.Header
	body   []byte
}

// webhookReceiver records deliveries and answers with status().
type webhookReceiver struct {
	mu       sync.Mutex
	received []capturedWebhook
	status   func() int
	notify   chan struct{}
	server   *httptest.Server
}

func newWebhookReceiver(t *testing.T, status int) *webhookReceiver {
	t.Helper()
	receiver := &webhookReceiver{notify: make(chan struct{}, 64)}
	receiver.status = func() int { return status }
	receiver.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receiver.mu.Lock()
		receiver.received = append(receiver.received, capturedWebhook{header: r.Header.Clone(), body: body})
		code := receiver.status()
		receiver.mu.Unlock()
		w.WriteHeader(code)
		_, _ = w.Write([]byte("receiver says " + strconv.Itoa(code)))
		select {
		case receiver.notify <- struct{}{}:
		default:
		}
	}))
	t.Cleanup(receiver.server.Close)
	return receiver
}

func (receiver *webhookReceiver) setStatus(status int) {
	receiver.mu.Lock()
	receiver.status = func() int { return status }
	receiver.mu.Unlock()
}

// waitFor waits until a delivery matches.
func (receiver *webhookReceiver) waitFor(t *testing.T, match func(map[string]any, http.Header) bool) (map[string]any, capturedWebhook) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		receiver.mu.Lock()
		for _, item := range receiver.received {
			var envelope map[string]any
			if json.Unmarshal(item.body, &envelope) == nil && match(envelope, item.header) {
				receiver.mu.Unlock()
				return envelope, item
			}
		}
		receiver.mu.Unlock()
		select {
		case <-receiver.notify:
		case <-deadline:
			t.Fatal("timed out waiting for webhook delivery")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (receiver *webhookReceiver) count() int {
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	return len(receiver.received)
}

func webhookTestServer(t *testing.T) (*server, http.Handler, domain.Bootstrap, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "webhooks.db")
	repository, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	service := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, allowedOrigin: "https://flow.example"}
	handler := newHandler(service)
	seed, _ := repository.BootstrapFor("test-workspace")
	return service, handler, seed, path
}

func waitForDelivery(t *testing.T, s *server, workspace, webhookID string, match func(domain.WebhookDelivery) bool) domain.WebhookDelivery {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		page, err := s.store.WebhookDeliveries(context.Background(), store.WebhookDeliveryQuery{Workspace: workspace, WebhookID: webhookID, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if match(item) {
				return item
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("timed out waiting for delivery state")
	return domain.WebhookDelivery{}
}

func TestWebhookSignatureVerificationAndTimestamp(t *testing.T) {
	payload := json.RawMessage(`{"action":"create","data":{"id":"a","nested":{"x":1}}}`)
	now := time.UnixMilli(1_700_000_000_000)
	body := webhookBodyWithTimestamp(payload, now.UnixMilli())
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("timestamped body is not JSON: %s", body)
	}
	if decoded["webhookTimestamp"] != float64(now.UnixMilli()) || decoded["data"].(map[string]any)["nested"] == nil {
		t.Fatalf("timestamp injection broke the body: %s", body)
	}
	if string(webhookBodyWithTimestamp(json.RawMessage(`{}`), 5)) != `{"webhookTimestamp":5}` {
		t.Fatal("empty object timestamp")
	}
	signature := webhookSignature("secret", body)
	if !verifyWebhookSignature("secret", body, signature, now.Add(30*time.Second), time.Minute) {
		t.Fatal("valid signature rejected")
	}
	if verifyWebhookSignature("other", body, signature, now, time.Minute) {
		t.Fatal("wrong secret accepted")
	}
	if verifyWebhookSignature("secret", append(body, ' '), signature, now, time.Minute) {
		t.Fatal("modified body accepted")
	}
	if verifyWebhookSignature("secret", body, signature, now.Add(2*time.Minute), time.Minute) {
		t.Fatal("replayed delivery outside the window accepted")
	}
	if verifyWebhookSignature("secret", body, "zz", now, time.Minute) {
		t.Fatal("malformed signature accepted")
	}
}

func TestWebhookURLMustBeHTTPS(t *testing.T) {
	production := &server{}
	for _, value := range []string{"http://example.com/hook", "https://localhost/hook", "https://127.0.0.1/hook", "https://10.0.0.1/hook", "ftp://example.com", "https://user:pw@example.com/"} {
		if _, err := production.validWebhookURL(value); err == nil {
			t.Fatalf("%s accepted", value)
		}
	}
	if _, err := production.validWebhookURL("https://hooks.example.com/flow"); err != nil {
		t.Fatal(err)
	}
	development := &server{authDisabled: true}
	if _, err := development.validWebhookURL("http://127.0.0.1:9999/hook"); err != nil {
		t.Fatalf("development loopback rejected: %v", err)
	}
	if _, err := development.validWebhookURL("http://example.com/hook"); err == nil {
		t.Fatal("development accepted public http")
	}
}

func TestWebhookDeliveryIsSignedWithFlowShapedEnvelope(t *testing.T) {
	_, handler, seed, _ := webhookTestServer(t)
	receiver := newWebhookReceiver(t, http.StatusOK)
	created := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/webhooks?workspace=test-workspace", map[string]any{
		"label": "Issue events", "url": receiver.server.URL, "resourceTypes": []string{"Issue", "comments", "reactions"},
	}, http.StatusCreated)
	secret, _ := created["secret"].(string)
	webhookID, _ := created["id"].(string)
	if secret == "" || created["payloadVersion"] != float64(2) || created["signingStatus"] != "active" || created["name"] != "Issue events" {
		t.Fatalf("create response: %#v", created)
	}
	if types := created["resourceTypes"].([]any); len(types) != 3 || types[0] != "issues" {
		t.Fatalf("resource types not normalised: %#v", types)
	}
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace=test-workspace", map[string]any{"title": "Signed delivery", "teamId": seed.Teams[0].ID}, http.StatusCreated)
	envelope, raw := receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool {
		return envelope["type"] == "Issue" && envelope["action"] == "create"
	})
	if raw.header.Get("Flow-Signature") != webhookSignature(secret, raw.body) || raw.header.Get("X-Flow-Signature") != raw.header.Get("Flow-Signature") {
		t.Fatalf("signature mismatch: %s", raw.header.Get("Flow-Signature"))
	}
	if !verifyWebhookSignature(secret, raw.body, raw.header.Get("Flow-Signature"), time.Now(), time.Minute) {
		t.Fatal("receiver-side verification failed")
	}
	if raw.header.Get("Flow-Event") != "Issue" || raw.header.Get("User-Agent") != "Flow-Webhook" || raw.header.Get("Flow-Delivery") == "" || raw.header.Get("Flow-Timestamp") == "" {
		t.Fatalf("headers: %#v", raw.header)
	}
	if strconv.FormatInt(int64(envelope["webhookTimestamp"].(float64)), 10) != raw.header.Get("Flow-Timestamp") {
		t.Fatal("body and header timestamps differ")
	}
	data, _ := envelope["data"].(map[string]any)
	if data["id"] != issue.ID || data["identifier"] != issue.Identifier || envelope["webhookId"] != webhookID || envelope["organizationId"] != seed.Workspace.ID {
		t.Fatalf("envelope: %#v", envelope)
	}
	if url, _ := envelope["url"].(string); !strings.HasPrefix(url, "https://flow.example/test-workspace/issue/"+issue.Identifier) {
		t.Fatalf("url: %v", envelope["url"])
	}
	if _, ok := envelope["createdAt"].(string); !ok {
		t.Fatal("createdAt missing")
	}
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID+"?workspace=test-workspace", map[string]any{"title": "Renamed"}, http.StatusOK)
	updated, _ := receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool {
		return envelope["type"] == "Issue" && envelope["action"] == "update"
	})
	if from, _ := updated["updatedFrom"].(map[string]any); from["title"] != "Signed delivery" {
		t.Fatalf("updatedFrom: %#v", updated["updatedFrom"])
	}
	comment := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/comments?workspace=test-workspace", map[string]any{"body": "Looks good"}, http.StatusCreated)
	commentEvent, _ := receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool { return envelope["type"] == "Comment" })
	commentData, _ := commentEvent["data"].(map[string]any)
	if commentEvent["action"] != "create" || commentData["id"] != comment.ID || commentData["body"] != "Looks good" || commentData["issueId"] != issue.ID {
		t.Fatalf("comment envelope: %#v", commentEvent)
	}
	if url, _ := commentEvent["url"].(string); !strings.HasSuffix(url, "#comment-"+comment.ID) {
		t.Fatalf("comment url: %v", commentEvent["url"])
	}
	requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/comments/"+comment.ID+"/reactions?workspace=test-workspace", map[string]any{"emoji": "👍"}, http.StatusOK)
	reaction, _ := receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool {
		return envelope["type"] == "Reaction" && envelope["action"] == "create"
	})
	if reactionData, _ := reaction["data"].(map[string]any); reactionData["emoji"] != "👍" || reactionData["commentId"] != comment.ID {
		t.Fatalf("reaction: %#v", reaction)
	}
	requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/comments/"+comment.ID+"/reactions?workspace=test-workspace", map[string]any{"emoji": "👍"}, http.StatusOK)
	receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool {
		return envelope["type"] == "Reaction" && envelope["action"] == "remove"
	})

	// The delivery log records each delivery with its response.
	page := requestJSON[store.WebhookDeliveryPage](t, handler, http.MethodGet, "/api/webhooks/"+webhookID+"/deliveries?workspace=test-workspace", nil, http.StatusOK)
	if len(page.Items) < 4 {
		t.Fatalf("delivery log: %#v", page.Items)
	}
	first := page.Items[len(page.Items)-1]
	if first.Status != domain.WebhookDeliverySucceeded || first.ResponseStatus != http.StatusOK || first.Attempt != 1 || first.ResponseBody != "receiver says 200" || len(first.Attempts) != 1 || first.Payload != nil {
		t.Fatalf("logged delivery: %#v", first)
	}
	detail := requestJSON[domain.WebhookDelivery](t, handler, http.MethodGet, "/api/webhooks/"+webhookID+"/deliveries/"+first.ID+"?workspace=test-workspace", nil, http.StatusOK)
	if len(detail.Payload) == 0 || strings.Contains(string(detail.Payload), "webhookTimestamp") {
		t.Fatalf("stored payload: %s", detail.Payload)
	}
}

func TestWebhookPayloadClassification(t *testing.T) {
	cases := map[string][2]string{
		"issue.created":                       {"issues", "create"},
		"issue.updated":                       {"issues", "update"},
		"issue.archived":                      {"issues", "update"},
		"issue.deleted":                       {"issues", "remove"},
		"issue.relation_added":                {"relations", "create"},
		"comment.created":                     {"comments", "create"},
		"comment.resolved":                    {"comments", "update"},
		"comment.reaction_toggled":            {"reactions", "create"},
		"issue.reaction_toggled":              {"reactions", "create"},
		"project.update_created":              {"project_updates", "create"},
		"project.update_commented":            {"comments", "create"},
		"project.milestone_deleted":           {"milestones", "remove"},
		"project.created":                     {"projects", "create"},
		"project.description_edited":          {"projects", "update"},
		"initiative.update_created":           {"initiative_updates", "create"},
		"initiative.updated":                  {"initiatives", "update"},
		"document.comment_created":            {"comments", "create"},
		"document.created":                    {"documents", "create"},
		"cycle.started":                       {"cycles", "update"},
		"label.created":                       {"labels", "create"},
		"customer_request.created":            {"customer_requests", "create"},
		"customer.merged":                     {"customers", "update"},
		"release.note_created":                {"release_notes", "create"},
		"release.created":                     {"releases", "create"},
		"attachment.deleted":                  {"attachments", "remove"},
		"user.updated":                        {"users", "update"},
		"document.awareness":                  {"", ""},
		"document.draft_created":              {"", ""},
		"issue.suggestions_generated":         {"", ""},
		"comment.thread_subscription":         {"", ""},
		"cycle.calendar_token_requested":      {"", ""},
		"project.comment_thread_subscription": {"", ""},
	}
	for event, want := range cases {
		resource, action := classifyWebhookEvent(event)
		if resource != want[0] || action != want[1] {
			t.Errorf("%s = %s/%s, want %s/%s", event, resource, action, want[0], want[1])
		}
		if resource != "" && webhookTypeName(resource) == "" {
			t.Errorf("%s has no payload type", resource)
		}
	}
	for _, value := range []string{"Issue", "IssueLabel", "CustomerNeed", "ProjectUpdate", "issues", "AgentSessionEvent"} {
		if _, ok := canonicalWebhookResourceType(value); !ok {
			t.Errorf("%s not accepted", value)
		}
	}
	if _, err := normalizeWebhookResourceTypes([]string{"AgentSessionEvent"}, false); err == nil {
		t.Error("workspace webhook accepted an application-only type")
	}
}

func TestLegacyWebhookKeepsVersionOneEnvelope(t *testing.T) {
	s, handler, seed, _ := webhookTestServer(t)
	receiver := newWebhookReceiver(t, http.StatusOK)
	created := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/webhooks?workspace=test-workspace", map[string]any{
		"name": "Legacy", "url": receiver.server.URL, "resourceTypes": []string{"issues"}, "payloadVersion": 1,
	}, http.StatusCreated)
	if created["payloadVersion"] != float64(1) {
		t.Fatalf("payload version: %#v", created)
	}
	requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace=test-workspace", map[string]any{"title": "Legacy", "teamId": seed.Teams[0].ID}, http.StatusCreated)
	envelope, raw := receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool { return envelope["type"] == "issues" })
	if envelope["action"] != "created" || envelope["id"] == "" || envelope["organizationId"] != seed.Workspace.ID || envelope["webhookTimestamp"] == nil || raw.header.Get("X-Flow-Event") != "Issue" {
		t.Fatalf("legacy envelope: %#v", envelope)
	}
	_ = s
}

func TestWebhookRetriesAfterOneMinuteOneHourAndSixHours(t *testing.T) {
	s, handler, seed, _ := webhookTestServer(t)
	receiver := newWebhookReceiver(t, http.StatusInternalServerError)
	created := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/webhooks?workspace=test-workspace", map[string]any{
		"name": "Failing", "url": receiver.server.URL, "resourceTypes": []string{"issues"},
	}, http.StatusCreated)
	webhookID := created["id"].(string)
	requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace=test-workspace", map[string]any{"title": "Retry me", "teamId": seed.Teams[0].ID}, http.StatusCreated)
	delivery := waitForDelivery(t, s, "test-workspace", webhookID, func(item domain.WebhookDelivery) bool { return item.Status == domain.WebhookDeliveryRetrying })
	if delivery.Attempt != 1 || delivery.NextAttemptAt == nil {
		t.Fatalf("first failure: %#v", delivery)
	}
	first := delivery.Attempts[0].At
	if gap := delivery.NextAttemptAt.Sub(first); gap < 59*time.Second || gap > 61*time.Second {
		t.Fatalf("first retry after %s", gap)
	}
	ctx := context.Background()
	// Not due yet: nothing is sent.
	before := receiver.count()
	s.processDueWebhookDeliveries(ctx, first.Add(30*time.Second))
	if receiver.count() != before {
		t.Fatal("retry sent before it was due")
	}
	expect := []time.Duration{time.Hour, 6 * time.Hour}
	now := *delivery.NextAttemptAt
	for index, gap := range expect {
		s.processDueWebhookDeliveries(ctx, now)
		delivery = waitForDelivery(t, s, "test-workspace", webhookID, func(item domain.WebhookDelivery) bool { return item.Attempt == index+2 })
		if delivery.Status != domain.WebhookDeliveryRetrying || delivery.NextAttemptAt == nil || delivery.NextAttemptAt.Sub(now).Round(time.Second) != gap {
			t.Fatalf("attempt %d: %#v", index+2, delivery)
		}
		now = *delivery.NextAttemptAt
	}
	s.processDueWebhookDeliveries(ctx, now)
	delivery = waitForDelivery(t, s, "test-workspace", webhookID, func(item domain.WebhookDelivery) bool { return item.Attempt == 4 })
	if delivery.Status != domain.WebhookDeliveryFailed || delivery.NextAttemptAt != nil || len(delivery.Attempts) != 4 || delivery.ResponseStatus != 500 {
		t.Fatalf("final failure: %#v", delivery)
	}
	if receiver.count() != before+3 {
		t.Fatalf("attempts sent: %d", receiver.count()-before)
	}
	// Each attempt carries its attempt number and a fresh signature.
	receiver.mu.Lock()
	attempts := []string{}
	for _, item := range receiver.received {
		attempts = append(attempts, item.header.Get("Flow-Attempt"))
	}
	receiver.mu.Unlock()
	if !slices.Equal(attempts, []string{"1", "2", "3", "4"}) {
		t.Fatalf("attempt headers: %v", attempts)
	}
	failures := requestJSON[[]domain.WebhookFailureEvent](t, handler, http.MethodGet, "/api/webhooks/"+webhookID+"/failures?workspace=test-workspace", nil, http.StatusOK)
	if len(failures) != 4 || failures[0].HTTPStatus == nil || *failures[0].HTTPStatus != 500 {
		t.Fatalf("failures: %#v", failures)
	}
	hooks, _ := s.store.WorkspaceMetadataFields("test-workspace", "webhooks")
	index := slices.IndexFunc(hooks.Webhooks, func(item domain.Webhook) bool { return item.ID == webhookID })
	if hooks.Webhooks[index].FailingSince == nil || !hooks.Webhooks[index].Enabled {
		t.Fatalf("failing state: %#v", hooks.Webhooks[index])
	}
}

func TestWebhookAutoDisablesAfterSustainedFailuresAndNotifies(t *testing.T) {
	s, handler, seed, _ := webhookTestServer(t)
	receiver := newWebhookReceiver(t, http.StatusBadGateway)
	created := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/webhooks?workspace=test-workspace", map[string]any{
		"name": "Dead endpoint", "url": receiver.server.URL, "resourceTypes": []string{"issues"},
	}, http.StatusCreated)
	webhookID := created["id"].(string)
	longAgo := time.Now().Add(-25 * time.Hour)
	s.updateWebhookHealth("test-workspace", webhookID, func(item *domain.Webhook) bool { item.FailingSince = &longAgo; return true })
	ctx := context.Background()
	delivery := domain.WebhookDelivery{ID: "whd_manual", Kind: domain.WebhookDeliveryKindEvent, EventID: "evt_manual", EventType: "Issue", Action: "create", Payload: json.RawMessage(`{"type":"Issue"}`), MaxAttempts: 1}
	hooks, _ := s.store.WorkspaceMetadataFields("test-workspace", "webhooks")
	hook := hooks.Webhooks[slices.IndexFunc(hooks.Webhooks, func(item domain.Webhook) bool { return item.ID == webhookID })]
	s.queueWebhookDelivery(ctx, "test-workspace", hook, delivery)
	waitForDelivery(t, s, "test-workspace", webhookID, func(item domain.WebhookDelivery) bool {
		return item.ID == "whd_manual" && item.Status == domain.WebhookDeliveryFailed
	})
	deadline := time.Now().Add(3 * time.Second)
	var disabled domain.Webhook
	for time.Now().Before(deadline) {
		hooks, _ = s.store.WorkspaceMetadataFields("test-workspace", "webhooks")
		disabled = hooks.Webhooks[slices.IndexFunc(hooks.Webhooks, func(item domain.Webhook) bool { return item.ID == webhookID })]
		if !disabled.Enabled {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if disabled.Enabled || disabled.DisabledReason != "deliveryFailures" || disabled.DisabledAt == nil {
		t.Fatalf("webhook not disabled: %#v", disabled)
	}
	page, err := s.store.QueryNotifications(ctx, store.NotificationQuery{Workspace: "test-workspace", UserID: seed.Viewer.ID, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(page.Notifications, func(item domain.Notification) bool {
		return item.SourceType == "webhook" && item.SourceID == webhookID && strings.Contains(item.Title, "Dead endpoint")
	}) {
		t.Fatalf("no disable notification: %#v", page.Notifications)
	}
	// Disabled webhooks receive nothing; re-enabling clears the failure state.
	before := receiver.count()
	requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace=test-workspace", map[string]any{"title": "Ignored", "teamId": seed.Teams[0].ID}, http.StatusCreated)
	time.Sleep(100 * time.Millisecond)
	if receiver.count() != before {
		t.Fatal("disabled webhook received a delivery")
	}
	enabled := requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/webhooks/"+webhookID+"?workspace=test-workspace", map[string]any{"enabled": true}, http.StatusOK)
	if enabled["disabledReason"] != nil || enabled["failingSince"] != nil || enabled["enabled"] != true {
		t.Fatalf("re-enabled: %#v", enabled)
	}
}

func TestWebhookQueueSurvivesRestartAndClaimsOnce(t *testing.T) {
	s, handler, seed, path := webhookTestServer(t)
	receiver := newWebhookReceiver(t, http.StatusServiceUnavailable)
	created := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/webhooks?workspace=test-workspace", map[string]any{
		"name": "Restart", "url": receiver.server.URL, "resourceTypes": []string{"issues"},
	}, http.StatusCreated)
	webhookID := created["id"].(string)
	requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace=test-workspace", map[string]any{"title": "Survive", "teamId": seed.Teams[0].ID}, http.StatusCreated)
	pending := waitForDelivery(t, s, "test-workspace", webhookID, func(item domain.WebhookDelivery) bool { return item.Status == domain.WebhookDeliveryRetrying })
	if err := s.store.Close(); err != nil {
		t.Fatal(err)
	}
	// A new process opens the same database: the retry is still queued.
	reopened, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted := &server{store: reopened, uploadPath: t.TempDir(), authDisabled: true}
	receiver.setStatus(http.StatusOK)
	due := pending.NextAttemptAt.Add(time.Second)
	ids, err := reopened.DueWebhookDeliveries(context.Background(), due, 10)
	if err != nil || !slices.Contains(ids, pending.ID) {
		t.Fatalf("due after restart: %v %v", ids, err)
	}
	// Two instances race for the delivery: one claim wins.
	claimed, ok, err := reopened.ClaimWebhookDelivery(context.Background(), pending.ID, due, time.Minute)
	if err != nil || !ok {
		t.Fatalf("first claim: %v %v", ok, err)
	}
	if _, again, _ := reopened.ClaimWebhookDelivery(context.Background(), pending.ID, due, time.Minute); again {
		t.Fatal("delivery claimed twice")
	}
	// The claimant crashed: its lease expires and the delivery is due again.
	afterLease := due.Add(2 * time.Minute)
	restarted.processDueWebhookDeliveries(context.Background(), afterLease)
	final, err := reopened.WebhookDelivery(context.Background(), "test-workspace", claimed.ID)
	if err != nil || final.Status != domain.WebhookDeliverySucceeded || final.Attempt != 2 {
		t.Fatalf("after restart: %#v %v", final, err)
	}
}

func TestWebhookTestEventAndRedeliver(t *testing.T) {
	s, handler, seed, _ := webhookTestServer(t)
	receiver := newWebhookReceiver(t, http.StatusOK)
	created := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/webhooks?workspace=test-workspace", map[string]any{
		"name": "Test", "url": receiver.server.URL, "resourceTypes": []string{"issues"},
	}, http.StatusCreated)
	webhookID, secret := created["id"].(string), created["secret"].(string)
	result := requestJSON[domain.WebhookDelivery](t, handler, http.MethodPost, "/api/webhooks/"+webhookID+"/test?workspace=test-workspace", nil, http.StatusOK)
	if result.Status != domain.WebhookDeliverySucceeded || result.ResponseStatus != 200 || result.Kind != domain.WebhookDeliveryKindTest || result.EventType != "WebhookTest" {
		t.Fatalf("test event: %#v", result)
	}
	envelope, raw := receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool { return envelope["type"] == "WebhookTest" })
	if envelope["action"] != "test" || raw.header.Get("Flow-Signature") != webhookSignature(secret, raw.body) {
		t.Fatalf("test envelope: %#v", envelope)
	}
	receiver.setStatus(http.StatusInternalServerError)
	requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace=test-workspace", map[string]any{"title": "Redeliver", "teamId": seed.Teams[0].ID}, http.StatusCreated)
	failed := waitForDelivery(t, s, "test-workspace", webhookID, func(item domain.WebhookDelivery) bool {
		return item.EventType == "Issue" && item.Status == domain.WebhookDeliveryRetrying
	})
	receiver.setStatus(http.StatusOK)
	redelivered := requestJSON[domain.WebhookDelivery](t, handler, http.MethodPost, "/api/webhooks/"+webhookID+"/deliveries/"+failed.ID+"/redeliver?workspace=test-workspace", nil, http.StatusOK)
	if redelivered.ID == failed.ID || redelivered.RedeliveryOf != failed.ID || redelivered.Status != domain.WebhookDeliverySucceeded || redelivered.EventID != failed.EventID {
		t.Fatalf("redelivery: %#v", redelivered)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/webhooks/"+webhookID+"/deliveries/missing/redeliver?workspace=test-workspace", nil, http.StatusNotFound)
}

func TestLegacyHashedSecretRequiresRotationToSign(t *testing.T) {
	s, handler, seed, _ := webhookTestServer(t)
	receiver := newWebhookReceiver(t, http.StatusOK)
	created := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/webhooks?workspace=test-workspace", map[string]any{
		"name": "Old", "url": receiver.server.URL, "resourceTypes": []string{"issues"},
	}, http.StatusCreated)
	webhookID := created["id"].(string)
	// Simulate a webhook created before signing: only the hash was kept.
	if err := s.store.DeleteWebhookSigningSecret(context.Background(), store.WebhookSecretRef(webhookID)); err != nil {
		t.Fatal(err)
	}
	s.updateWebhookHealth("test-workspace", webhookID, func(item *domain.Webhook) bool { item.SigningEnabled = false; return true })
	listed := requestJSON[[]map[string]any](t, handler, http.MethodGet, "/api/webhooks?workspace=test-workspace", nil, http.StatusOK)
	if listed[0]["signingStatus"] != "rotationRequired" {
		t.Fatalf("legacy status: %#v", listed[0])
	}
	requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace=test-workspace", map[string]any{"title": "Unsigned", "teamId": seed.Teams[0].ID}, http.StatusCreated)
	_, raw := receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool { return envelope["type"] == "Issue" })
	if raw.header.Get("Flow-Signature") != "" || raw.header.Get("Flow-Signature-Status") != "unsigned" {
		t.Fatalf("legacy delivery headers: %#v", raw.header)
	}
	rotated := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/webhooks/"+webhookID+"/rotate-secret?workspace=test-workspace", nil, http.StatusOK)
	secret, _ := rotated["secret"].(string)
	if secret == "" || rotated["signingStatus"] != "active" {
		t.Fatalf("rotation: %#v", rotated)
	}
	requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace=test-workspace", map[string]any{"title": "Signed now", "teamId": seed.Teams[0].ID}, http.StatusCreated)
	_, raw = receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool {
		data, _ := envelope["data"].(map[string]any)
		return data != nil && data["title"] == "Signed now"
	})
	if raw.header.Get("Flow-Signature") != webhookSignature(secret, raw.body) {
		t.Fatal("rotated secret not used")
	}
}

func TestIssueDeletedWebhookPayloadOmitsRealtimeOnlyFields(t *testing.T) {
	payload := json.RawMessage(`{"id":"issue_1","issueIds":["issue_1","issue_2"],"issueAccess":[{"id":"issue_2","teamId":"team_private","grants":[{"userId":"user_9"}]}]}`)
	stripped := string(withoutRealtimeOnlyFields("issue.deleted", payload))
	if strings.Contains(stripped, "issueAccess") || strings.Contains(stripped, "issue_2") || strings.Contains(stripped, "user_9") || !strings.Contains(stripped, `"id":"issue_1"`) {
		t.Fatalf("webhook payload = %s", stripped)
	}
	if other := json.RawMessage(`{"issueIds":["a"]}`); string(withoutRealtimeOnlyFields("issue.batch_updated", other)) != string(other) {
		t.Fatal("other events must keep their payload")
	}
}
