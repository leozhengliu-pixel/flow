package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func customerRequestsHandler(t *testing.T) http.Handler {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	return newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
}

// The composer accepts a request with any one of a customer, a body or a source,
// and its deferred "Create new customer" option creates the customer in the same write.
func TestCustomerRequestCreateAcceptsComposerShapes(t *testing.T) {
	handler := customerRequestsHandler(t)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	issueID := bootstrap.Issues[0].ID

	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/customer-requests", map[string]any{"issueId": issueID, "body": "  "}, http.StatusBadRequest)

	unknown := requestJSON[domain.CustomerRequest](t, handler, http.MethodPost, "/api/customer-requests", map[string]any{"issueId": issueID, "body": "  Needs SSO  "}, http.StatusCreated)
	if unknown.CustomerID != "" || unknown.Body != "Needs SSO" || unknown.IssueID != issueID || unknown.Source != "manual" {
		t.Fatalf("request without a customer = %#v", unknown)
	}
	sourceOnly := requestJSON[domain.CustomerRequest](t, handler, http.MethodPost, "/api/customer-requests", map[string]any{"issueId": issueID, "sourceUrl": " https://example.com/ticket/1 "}, http.StatusCreated)
	if sourceOnly.SourceURL != "https://example.com/ticket/1" || sourceOnly.Body != "" {
		t.Fatalf("source-only request = %#v", sourceOnly)
	}

	created := requestJSON[domain.CustomerRequest](t, handler, http.MethodPost, "/api/customer-requests", map[string]any{"issueId": issueID, "customerName": " Acme ", "priority": 1}, http.StatusCreated)
	if created.CustomerID == "" || created.Priority != 1 {
		t.Fatalf("request with a new customer = %#v", created)
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	index := slices.IndexFunc(bootstrap.Customers, func(item domain.Customer) bool { return item.ID == created.CustomerID })
	if index < 0 || bootstrap.Customers[index].Name != "Acme" {
		t.Fatalf("inline customer was not created: %#v", bootstrap.Customers)
	}

	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/customer-requests", map[string]any{"customerId": "missing", "body": "x"}, http.StatusNotFound)
}

// Row menu actions: change or remove the customer, mark important, and move the
// request between an issue and a project (a request belongs to one of them).
func TestCustomerRequestUpdateChangesCustomerAndMoves(t *testing.T) {
	handler := customerRequestsHandler(t)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Issues) < 2 || len(bootstrap.Projects) == 0 {
		t.Skip("fixture needs two issues and a project")
	}
	first, second, project := bootstrap.Issues[0].ID, bootstrap.Issues[1].ID, bootstrap.Projects[0].ID
	customer := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{"name": "Initech"}, http.StatusCreated)
	request := requestJSON[domain.CustomerRequest](t, handler, http.MethodPost, "/api/customer-requests", map[string]any{"issueId": first, "body": "Audit log"}, http.StatusCreated)

	path := "/api/customer-requests/" + request.ID
	updated := requestJSON[domain.CustomerRequest](t, handler, http.MethodPatch, path, map[string]any{"customerId": customer.ID, "priority": 1}, http.StatusOK)
	if updated.CustomerID != customer.ID || updated.Priority != 1 {
		t.Fatalf("change customer = %#v", updated)
	}
	updated = requestJSON[domain.CustomerRequest](t, handler, http.MethodPatch, path, map[string]any{"issueId": second}, http.StatusOK)
	if updated.IssueID != second || updated.ProjectID != "" {
		t.Fatalf("move to issue = %#v", updated)
	}
	updated = requestJSON[domain.CustomerRequest](t, handler, http.MethodPatch, path, map[string]any{"projectId": project}, http.StatusOK)
	if updated.ProjectID != project || updated.IssueID != "" {
		t.Fatalf("move to project = %#v", updated)
	}
	updated = requestJSON[domain.CustomerRequest](t, handler, http.MethodPatch, path, map[string]any{"customerId": "", "priority": 0}, http.StatusOK)
	if updated.CustomerID != "" || updated.Priority != 0 || updated.Body != "Audit log" {
		t.Fatalf("remove customer = %#v", updated)
	}
	// Removing the last of customer, body and source is rejected.
	requestJSON[map[string]any](t, handler, http.MethodPatch, path, map[string]any{"body": ""}, http.StatusBadRequest)
	requestJSON[map[string]any](t, handler, http.MethodPatch, path, map[string]any{"issueId": "missing"}, http.StatusBadRequest)
}

// With sign-in, issue-linked requests stay writable: authorization reads the paged metadata
// projection, which keeps the requests of visible issues (and requests without a customer).
func TestCustomerRequestWritesAuthorizedInPagedMode(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "requests.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	defer host.Close()
	client := authClient(t)
	authRequest[domain.AuthSession](t, client, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	page := authRequest[struct {
		Items []domain.Issue `json:"items"`
	}](t, client, "GET", host.URL+"/api/issue-records?limit=1", nil, "test-workspace", http.StatusOK)
	if len(page.Items) == 0 {
		t.Skip("fixture has no issues")
	}
	issueID := page.Items[0].ID
	customer := authRequest[domain.Customer](t, client, "POST", host.URL+"/api/customers", map[string]string{"name": "Paged customer"}, "test-workspace", http.StatusCreated)
	request := authRequest[domain.CustomerRequest](t, client, "POST", host.URL+"/api/customer-requests", map[string]any{"customerId": customer.ID, "issueId": issueID, "body": "Paged"}, "test-workspace", http.StatusCreated)
	updated := authRequest[domain.CustomerRequest](t, client, "PATCH", host.URL+"/api/customer-requests/"+request.ID, map[string]any{"priority": 1}, "test-workspace", http.StatusOK)
	if updated.Priority != 1 {
		t.Fatalf("priority = %v", updated.Priority)
	}
	unknown := authRequest[domain.CustomerRequest](t, client, "POST", host.URL+"/api/customer-requests", map[string]any{"issueId": issueID, "body": "No customer yet"}, "test-workspace", http.StatusCreated)
	authRequest[domain.CustomerRequest](t, client, "PATCH", host.URL+"/api/customer-requests/"+unknown.ID, map[string]any{"customerId": customer.ID}, "test-workspace", http.StatusOK)
	authRequest[any](t, client, "PATCH", host.URL+"/api/customer-requests/"+unknown.ID, map[string]any{"customerId": "customer_missing"}, "test-workspace", http.StatusForbidden)
	authRequest[any](t, client, "DELETE", host.URL+"/api/customer-requests/"+request.ID, nil, "test-workspace", http.StatusNoContent)
}
