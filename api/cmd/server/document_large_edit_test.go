package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// largeDocumentMarkdown returns a Markdown body of at least size bytes with
// multi-byte text, quotes, backslashes and markup, so it reads back equal
// only when nothing was cut or re-encoded.
func largeDocumentMarkdown(label string, size int) string {
	var builder strings.Builder
	for line := 0; builder.Len() < size; line++ {
		fmt.Fprintf(&builder, "## %s section %d\n\nPlan 计划 ✅ \"quoted\" \\path\\ <tag> & text for line %d.\n\n", label, line, line)
	}
	return builder.String()
}

// Documents over 64 KiB are edited through every write path while the
// workspace has an enabled webhook, so each edit records the pre-edit body
// as the event's previous values (a MySQL TEXT column used to reject it and
// the PATCH answered 500). Runs on SQLite and on the database named by
// FLOW_TEST_DATABASE_DRIVER / FLOW_TEST_DATABASE_URL.
func TestDocumentLargeEditAcrossWritePaths(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	for name, repo := range favoriteTabTestStores(t) {
		t.Run(name, func(t *testing.T) { testDocumentLargeEditAcrossWritePaths(t, repo) })
	}
}

func testDocumentLargeEditAcrossWritePaths(t *testing.T, repo *store.SQLiteStore) {
	ctx := t.Context()
	const workspace = "test-workspace"
	service := &server{store: repo, uploadPath: t.TempDir()}
	host := httptest.NewServer(newHandler(service))
	defer host.Close()
	var sinkMu sync.Mutex
	published := map[string]json.RawMessage{}
	repo.SetWebhookSink(func(workspace string, event domain.DomainEvent) {
		sinkMu.Lock()
		published[event.ID] = event.PreviousValues
		sinkMu.Unlock()
		service.dispatchDomainEvent(workspace, event)
	})
	// The stored previous values read back as what webhooks were given.
	sameAsPublished := func(event domain.DomainEvent) {
		t.Helper()
		sinkMu.Lock()
		want := published[event.ID]
		sinkMu.Unlock()
		loaded, err := repo.LoadOffloadedJSON(ctx, workspace, event.PreviousValues)
		var got, expected any
		if err == nil {
			err = errors.Join(json.Unmarshal(loaded, &got), json.Unmarshal(want, &expected))
		}
		if err != nil || len(want) == 0 || !reflect.DeepEqual(got, expected) {
			t.Fatalf("event %s: stored previous values (%d bytes, %d loaded) differ from the published ones (%d bytes): %v", event.ID, len(event.PreviousValues), len(loaded), len(want), err)
		}
	}
	defer repo.SetWebhookSink(nil)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	webhookID := "large-document-webhook-" + suffix
	if err := repo.MutateWorkspace(ctx, workspace, "webhook.created", webhookID, nil, func(data *domain.Bootstrap) error {
		data.Webhooks = append(data.Webhooks, domain.Webhook{ID: webhookID, Name: "Large documents", URL: "https://hooks.example.test/large-document", Enabled: true, ResourceTypes: []string{"Document"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	key := authRequest[struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}](t, admin, http.MethodPost, host.URL+"/api/api-keys", map[string]any{"name": "Large documents " + suffix, "scopes": []string{"read", "write"}}, workspace, http.StatusCreated)
	keySecret := key.Secret
	defer func() {
		_ = repo.MutateWorkspace(ctx, workspace, "test.cleanup", webhookID, nil, func(data *domain.Bootstrap) error {
			data.Webhooks = slices.DeleteFunc(data.Webhooks, func(item domain.Webhook) bool { return item.ID == webhookID })
			data.APIKeys = slices.DeleteFunc(data.APIKeys, func(item domain.APIKey) bool { return item.ID == key.ID })
			return nil
		})
	}()
	documentURL := func(id string) string { return host.URL + "/api/documents/" + id }
	read := func(id string) domain.Document {
		return authRequest[domain.Document](t, admin, http.MethodGet, documentURL(id), nil, workspace, http.StatusOK)
	}
	updateEvents := func(id string) []domain.DomainEvent {
		events, err := repo.Events(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return slices.DeleteFunc(events, func(event domain.DomainEvent) bool { return event.Type != "document.updated" })
	}

	// Admin session: create, then a > 64 KiB edit guarded by the version.
	original, updated := largeDocumentMarkdown("original", 173<<10), largeDocumentMarkdown("updated", 173<<10)
	created := authRequest[domain.Document](t, admin, http.MethodPost, host.URL+"/api/documents", map[string]any{"title": "Large document " + suffix, "content": original}, workspace, http.StatusCreated)
	if created.Content != original || created.Version != 0 {
		t.Fatalf("created: version %d, content equal %v", created.Version, created.Content == original)
	}
	edited := authRequest[domain.Document](t, admin, http.MethodPatch, documentURL(created.ID), map[string]any{"content": updated, "expectedVersion": 0}, workspace, http.StatusOK)
	if edited.Content != updated || edited.Version != 1 {
		t.Fatalf("edited: version %d, content equal %v", edited.Version, edited.Content == updated)
	}
	if got := read(created.ID); got.Content != updated || got.Version != 1 {
		t.Fatalf("read back: version %d, content equal %v", got.Version, got.Content == updated)
	}
	events := updateEvents(created.ID)
	if len(events) != 1 {
		t.Fatalf("update events: %d", len(events))
	}
	t.Logf("domain_events bytes for one %d byte document edit: payload %d, previous_values %d", len(updated), len(events[0].Payload), len(events[0].PreviousValues))
	sameAsPublished(events[0])
	previous, err := repo.LoadOffloadedJSON(ctx, workspace, events[0].PreviousValues)
	if err != nil {
		t.Fatal(err)
	}
	var before domain.Document
	if err := json.Unmarshal(previous, &before); err != nil || before.Content != original {
		t.Fatalf("previous values: content equal %v, %v", before.Content == original, err)
	}

	// History keeps both bodies whole.
	history := authRequest[struct {
		Nodes []domain.DocumentRevision `json:"nodes"`
	}](t, admin, http.MethodGet, documentURL(created.ID)+"/history?limit=50", nil, workspace, http.StatusOK).Nodes
	contents := []string{}
	for _, revision := range history {
		contents = append(contents, revision.Content)
	}
	if len(history) != 2 || contents[0] != updated || contents[1] != original {
		t.Fatalf("history: %d versions, newest equal %v, baseline equal %v", len(history), len(contents) > 0 && contents[0] == updated, len(contents) > 1 && contents[1] == original)
	}

	// A stale expectedVersion is still a conflict, not a server error.
	authRequest[map[string]any](t, admin, http.MethodPatch, documentURL(created.ID), map[string]any{"content": largeDocumentMarkdown("stale", 100<<10), "expectedVersion": 0}, workspace, http.StatusConflict)
	if got := read(created.ID); got.Content != updated || got.Version != 1 {
		t.Fatalf("after a conflict: version %d, content equal %v", got.Version, got.Content == updated)
	}

	// MCP save_document with an API key.
	mcp := &mcpContractFixture{repository: repo, service: service, host: host, data: repo.Bootstrap(), secret: keySecret}
	viaMCP := largeDocumentMarkdown("mcp", 200<<10)
	mcpObject(t, mcp, "save_document", map[string]any{"id": created.ID, "content": viaMCP})
	if got := read(created.ID); got.Content != viaMCP || got.Version != 2 {
		t.Fatalf("after save_document: version %d, content equal %v", got.Version, got.Content == viaMCP)
	}
	events = updateEvents(created.ID)
	if len(events) != 2 {
		t.Fatalf("update events after save_document: %d", len(events))
	}
	sinkMu.Lock()
	inline := len(published[events[1].ID])
	sinkMu.Unlock()
	t.Logf("domain_events bytes for a following %d byte edit (the document has 2 versions): payload %d, previous_values %d (%d inline)", len(viaMCP), len(events[1].Payload), len(events[1].PreviousValues), inline)
	sameAsPublished(events[1])
	// The versions' bodies are references; the pre-edit body stays inline.
	if len(events[1].PreviousValues) >= inline || len(events[1].PreviousValues) > 2*len(updated) {
		t.Fatalf("stored previous values hold the version bodies: %d of %d bytes", len(events[1].PreviousValues), inline)
	}
	// The same content again changes nothing: no version, no event.
	eventsBefore := len(updateEvents(created.ID))
	if unchanged := mcpObject(t, mcp, "save_document", map[string]any{"id": created.ID, "content": viaMCP}); unchanged["unchanged"] != true {
		t.Fatalf("repeated save_document: %v", unchanged["unchanged"])
	}
	if got := read(created.ID); got.Version != 2 || len(updateEvents(created.ID)) != eventsBefore {
		t.Fatalf("repeated save_document wrote: version %d, events %d -> %d", got.Version, eventsBefore, len(updateEvents(created.ID)))
	}

	// The REST API with the API key.
	viaKey := largeDocumentMarkdown("api key", 150<<10)
	_, raw := bearer(t, http.MethodPatch, documentURL(created.ID), keySecret, map[string]any{"content": viaKey, "expectedVersion": 2}, http.StatusOK)
	if got := decodeInto[domain.Document](t, raw); got.Content != viaKey || got.Version != 3 {
		t.Fatalf("API key edit: version %d, content equal %v", got.Version, got.Content == viaKey)
	}

	// An application's own token (client credentials) creates and edits.
	app := authRequest[domain.OAuthApplication](t, admin, http.MethodPost, host.URL+"/api/oauth-applications", map[string]any{"name": "Large documents " + suffix, "scopes": []string{"read", "write"}, "supportsClientCredentials": true}, workspace, http.StatusCreated)
	token := applicationFormToken(t, host, app, "read write", http.StatusOK)
	appOriginal, appUpdated := largeDocumentMarkdown("app original", 90<<10), largeDocumentMarkdown("app updated", 180<<10)
	body, _ := json.Marshal(map[string]any{"title": "App document " + suffix, "content": appOriginal})
	var appDocument domain.Document
	if err := json.Unmarshal(applicationBearerRequest(t, host, token.Token, http.MethodPost, "/api/documents", string(body), http.StatusCreated), &appDocument); err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(map[string]any{"content": appUpdated, "expectedVersion": appDocument.Version})
	var appEdited domain.Document
	if err := json.Unmarshal(applicationBearerRequest(t, host, token.Token, http.MethodPatch, "/api/documents/"+appDocument.ID, string(body), http.StatusOK), &appEdited); err != nil {
		t.Fatal(err)
	}
	if got := read(appDocument.ID); got.Content != appUpdated || got.Version != appDocument.Version+1 {
		t.Fatalf("application edit: version %d, content equal %v", got.Version, got.Content == appUpdated)
	}
	if events := updateEvents(appDocument.ID); len(events) != 1 || len(events[0].PreviousValues) < len(appOriginal) {
		t.Fatalf("application edit events: %d", len(events))
	}
}
