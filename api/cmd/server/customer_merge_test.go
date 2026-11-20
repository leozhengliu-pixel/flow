package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func customerMergeHandler(t *testing.T) http.Handler {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	return newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
}

// "Merge with…" moves the source's requests to the target, keeps the target's
// attributes, combines domains and deletes the source.
func TestMergeCustomerMovesRequestsAndDeletesSource(t *testing.T) {
	handler := customerMergeHandler(t)
	source := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{"name": "Acme Old", "domains": []string{"old.acme.test", "shared.acme.test"}, "annualRevenue": 5000, "size": 10}, http.StatusCreated)
	target := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{"name": "Acme", "domains": []string{"shared.acme.test", "acme.test"}, "annualRevenue": 9000}, http.StatusCreated)
	first := requestJSON[domain.CustomerRequest](t, handler, http.MethodPost, "/api/customer-requests", map[string]any{"customerId": source.ID, "body": "SSO please"}, http.StatusCreated)
	second := requestJSON[domain.CustomerRequest](t, handler, http.MethodPost, "/api/customer-requests", map[string]any{"customerId": target.ID, "body": "Audit log"}, http.StatusCreated)

	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/customers/"+source.ID+"/merge", map[string]any{"targetCustomerId": source.ID}, http.StatusBadRequest)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/customers/"+source.ID+"/merge", map[string]any{"targetCustomerId": "missing"}, http.StatusNotFound)

	merged := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers/"+source.ID+"/merge", map[string]any{"targetCustomerId": target.ID}, http.StatusOK)
	if merged.ID != target.ID || merged.Name != "Acme" || merged.AnnualRevenue != 9000 || merged.Size != 0 {
		t.Fatalf("merged customer kept the wrong attributes: %#v", merged)
	}
	if !slices.Equal(merged.Domains, []string{"acme.test", "old.acme.test", "shared.acme.test"}) {
		t.Fatalf("merged domains = %v", merged.Domains)
	}

	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(bootstrap.Customers, func(item domain.Customer) bool { return item.ID == source.ID }) {
		t.Fatal("source customer still exists after the merge")
	}
	for _, id := range []string{first.ID, second.ID} {
		index := slices.IndexFunc(bootstrap.CustomerRequests, func(item domain.CustomerRequest) bool { return item.ID == id })
		if index < 0 || bootstrap.CustomerRequests[index].CustomerID != target.ID {
			t.Fatalf("request %s was not moved to the target: %#v", id, bootstrap.CustomerRequests)
		}
	}
}

// With manual customer edits off (customers synced from elsewhere), merging
// and deleting are refused like creating and updating; nothing changes.
func TestCustomerMergeAndDeleteRefusedWhenManualEditsOff(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	source := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{"name": "Acme Old"}, http.StatusCreated)
	target := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{"name": "Acme"}, http.StatusCreated)
	workspace := repository.Bootstrap().Workspace.URLKey
	if err := repository.MutateWorkspace(t.Context(), workspace, "test.customer_settings", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.FeatureSettings.CustomerManualEdits = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/customers/"+source.ID, map[string]any{"name": "Renamed"}, http.StatusForbidden)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/customers/"+source.ID+"/merge", map[string]any{"targetCustomerId": target.ID}, http.StatusForbidden)
	requestJSON[map[string]any](t, handler, http.MethodDelete, "/api/customers/"+source.ID, nil, http.StatusForbidden)
	customers := repository.Bootstrap().Customers
	if !slices.ContainsFunc(customers, func(c domain.Customer) bool { return c.ID == source.ID }) || !slices.ContainsFunc(customers, func(c domain.Customer) bool { return c.ID == target.ID }) {
		t.Fatalf("a refused merge or delete changed the customers: %v", customers)
	}
}
