package main

import (
	"net/http"
	"testing"

	"flow/api/internal/domain"
)

// Customers take any workspace customer status (by id or name), like Linear's
// status relation; new customers start on the first status by position.
func TestCustomerStatusUsesWorkspaceStatuses(t *testing.T) {
	handler := customerRequestsHandler(t)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	var prospect domain.CustomerStatus
	for _, status := range bootstrap.CustomerStatuses {
		if status.Name == "Prospect" {
			prospect = status
		}
	}
	if prospect.ID == "" {
		t.Skipf("fixture has no Prospect status: %#v", bootstrap.CustomerStatuses)
	}

	created := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{"name": "Status default"}, http.StatusCreated)
	if created.Status != "active" {
		t.Fatalf("default status = %q, want the first status (Active, stored as active)", created.Status)
	}
	withStatus := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{"name": "Prospect co", "status": prospect.ID}, http.StatusCreated)
	if withStatus.Status != prospect.ID {
		t.Fatalf("create with status id = %q, want %q", withStatus.Status, prospect.ID)
	}
	byName := requestJSON[domain.Customer](t, handler, http.MethodPatch, "/api/customers/"+created.ID, map[string]any{"status": "prospect"}, http.StatusOK)
	if byName.Status != prospect.ID {
		t.Fatalf("update by status name = %q, want %q", byName.Status, prospect.ID)
	}
	legacy := requestJSON[domain.Customer](t, handler, http.MethodPatch, "/api/customers/"+created.ID, map[string]any{"status": "inactive"}, http.StatusOK)
	if legacy.Status != "inactive" {
		t.Fatalf("legacy status = %q", legacy.Status)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/customers/"+created.ID, map[string]any{"status": "no-such-status"}, http.StatusBadRequest)
}
