package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestCycleResourcesAndCalendarFeed(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	cycle := bootstrap.Cycles[0]
	link := requestJSON[domain.CycleResource](t, handler, http.MethodPost, "/api/cycles/"+cycle.ID+"/resources", map[string]string{"type": "link", "title": "Runbook", "url": "https://example.com/runbook"}, http.StatusCreated)
	if link.Title != "Runbook" || link.URL == "" {
		t.Fatalf("cycle link=%#v", link)
	}
	if len(bootstrap.Documents) > 0 {
		document := requestJSON[domain.CycleResource](t, handler, http.MethodPost, "/api/cycles/"+cycle.ID+"/resources", map[string]string{"type": "document", "documentId": bootstrap.Documents[0].ID}, http.StatusCreated)
		if document.DocumentID != bootstrap.Documents[0].ID || !strings.Contains(document.URL, "/document/") {
			t.Fatalf("cycle document=%#v", document)
		}
	}
	feed := requestJSON[map[string]string](t, handler, http.MethodPost, "/api/cycles/"+cycle.ID+"/calendar-token", nil, http.StatusOK)
	if !strings.Contains(feed["url"], ".ics?token=") {
		t.Fatalf("calendar feed=%#v", feed)
	}
	request := httptest.NewRequest(http.MethodGet, feed["url"], nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "BEGIN:VCALENDAR") || response.Header().Get("Content-Type") != "text/calendar; charset=utf-8" {
		t.Fatalf("calendar status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	public := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if public.Cycles[0].CalendarToken != "" {
		t.Fatal("bootstrap leaked cycle calendar token")
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/cycles/"+cycle.ID+"/resources/"+link.ID, nil, http.StatusNoContent)
}

func TestCycleCapacityMatrixLifecycle(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	cycle := bootstrap.Cycles[0]
	var memberID string
	for _, member := range bootstrap.TeamMembers {
		if member.TeamID == cycle.TeamID {
			memberID = member.UserID
			break
		}
	}
	if memberID == "" {
		memberID = bootstrap.Viewer.ID
		if err := repository.MutateWorkspace(t.Context(), bootstrap.Workspace.URLKey, "test.cycle_member", cycle.TeamID, nil, func(data *domain.Bootstrap) error {
			data.TeamMembers = append(data.TeamMembers, domain.TeamMember{TeamID: cycle.TeamID, UserID: memberID, Role: "member", JoinedAt: time.Now().UTC()})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	updated := requestJSON[domain.Cycle](t, handler, http.MethodPut, "/api/cycles/"+cycle.ID+"/capacity", map[string]any{
		"capacity":         24,
		"capacityByMember": map[string]any{memberID: map[string]int{"mon": 6, "tue": 5}},
	}, http.StatusOK)
	if updated.Capacity != 24 || updated.CapacityByMember[memberID]["mon"] != 6 || updated.CapacityByMember[memberID]["tue"] != 5 {
		t.Fatalf("updated capacity=%#v", updated)
	}
	result := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/cycles/"+cycle.ID+"/capacity", nil, http.StatusOK)
	if result["cycleId"] != cycle.ID {
		t.Fatalf("capacity response=%#v", result)
	}
}
