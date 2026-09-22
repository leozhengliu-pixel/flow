package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestAsksWebSettingsCRUDAndPages(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "asks-web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	created := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/asks-web-settings", map[string]any{
		"title":        "Acme Asks",
		"hostname":     "asks.acme.test",
		"emailAddress": "notify@acme.test",
	}, http.StatusCreated)
	settings := created["settings"].(map[string]any)
	id := settings["id"].(string)
	if settings["asksUrl"] != "https://asks.acme.test" {
		t.Fatalf("asksUrl=%v", settings["asksUrl"])
	}
	dns := created["dnsRecord"].(map[string]any)
	if dns["type"] != "TXT" || dns["name"] != "_flow-asks.asks.acme.test" {
		t.Fatalf("dns=%v", dns)
	}

	listed := requestJSON[[]domain.AsksWebSettings](t, handler, http.MethodGet, "/api/asks-web-settings", nil, http.StatusOK)
	if len(listed) != 1 || listed[0].ID != id {
		t.Fatalf("listed=%v", listed)
	}

	verified := requestJSON[domain.AsksWebSettings](t, handler, http.MethodPost, "/api/asks-web-settings/"+id+"/dns-check", map[string]string{
		"txtValue": dns["value"].(string),
	}, http.StatusOK)
	if !verified.DnsVerified || verified.HostingStatus != "configured" {
		t.Fatalf("verified=%+v", verified)
	}

	page := requestJSON[domain.AsksWebPage](t, handler, http.MethodPost, "/api/asks-web-settings/"+id+"/pages", map[string]any{
		"slug":  "support",
		"title": "Support",
	}, http.StatusCreated)
	if page.Slug != "support" || page.SettingsID != id {
		t.Fatalf("page=%+v", page)
	}

	updatedPage := requestJSON[domain.AsksWebPage](t, handler, http.MethodPatch, "/api/asks-web-settings/"+id+"/pages/"+page.ID, map[string]any{
		"title":               "Support desk",
		"emailRepliesEnabled": true,
		"autoReplyCreated":    "Thanks, we got it.",
		"autoReplyCompleted":  "Resolved.",
		"autoReplyCanceled":   "Canceled.",
	}, http.StatusOK)
	if updatedPage.Title != "Support desk" || !updatedPage.EmailRepliesEnabled {
		t.Fatalf("updatedPage=%+v", updatedPage)
	}

	got := requestJSON[domain.AsksWebSettings](t, handler, http.MethodGet, "/api/asks-web-settings/"+id, nil, http.StatusOK)
	if len(got.Pages) != 1 || got.Pages[0].Title != "Support desk" {
		t.Fatalf("got=%+v", got)
	}

	requestJSON[any](t, handler, http.MethodDelete, "/api/asks-web-settings/"+id+"/pages/"+page.ID, nil, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodDelete, "/api/asks-web-settings/"+id, nil, http.StatusNoContent)
	empty := requestJSON[[]domain.AsksWebSettings](t, handler, http.MethodGet, "/api/asks-web-settings", nil, http.StatusOK)
	if len(empty) != 0 {
		t.Fatalf("expected empty, got %v", empty)
	}
}

func TestAsksWebPageLimitConstant(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "asks-web-limit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	created := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/asks-web-settings", map[string]any{
		"title":    "Limit",
		"hostname": "asks.limit.test",
	}, http.StatusCreated)
	id := created["settings"].(map[string]any)["id"].(string)
	for i := 0; i < maxAsksWebPagesPerConfiguration; i++ {
		requestJSON[domain.AsksWebPage](t, handler, http.MethodPost, "/api/asks-web-settings/"+id+"/pages", map[string]any{
			"slug":  fmt.Sprintf("page-%d", i+1),
			"title": "Page",
		}, http.StatusCreated)
	}
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/asks-web-settings/"+id+"/pages", map[string]any{
		"slug":  "overflow",
		"title": "Overflow",
	}, http.StatusBadRequest)
}
