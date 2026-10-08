package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func customerSettingsHandler(t *testing.T) (http.Handler, *store.SQLiteStore) {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	return handler, repository
}

func TestCustomerTaxonomyCreateValidatesAndAppends(t *testing.T) {
	handler, _ := customerSettingsHandler(t)
	first := requestJSON[domain.CustomerStatus](t, handler, http.MethodPost, "/api/customer-statuses", map[string]any{"name": "  At risk ", "description": " Needs attention "}, http.StatusCreated)
	if first.Name != "At risk" || first.Description != "Needs attention" || first.Color != "#95a2b3" {
		t.Fatalf("status = %#v, want trimmed name/description and the default status colour", first)
	}
	second := requestJSON[domain.CustomerStatus](t, handler, http.MethodPost, "/api/customer-statuses", map[string]any{"name": "Renewal", "color": "#ABCDEF"}, http.StatusCreated)
	if second.Position <= first.Position || second.Color != "#abcdef" {
		t.Fatalf("new status %#v should be appended after %#v with a lowercased colour", second, first)
	}
	tier := requestJSON[domain.CustomerTier](t, handler, http.MethodPost, "/api/customer-tiers", map[string]any{"name": "Enterprise", "description": "Top accounts"}, http.StatusCreated)
	if tier.Color != "#8a8f98" || tier.Description != "Top accounts" {
		t.Fatalf("tier = %#v, want the default tier colour and description", tier)
	}
	for _, input := range []map[string]any{
		{"name": "   "},
		{"name": strings.Repeat("x", 26)},
		{"name": "Long description", "description": strings.Repeat("d", 256)},
		{"name": "at RISK"},
		{"name": "Bad colour", "color": "red"},
	} {
		requestJSON[map[string]any](t, handler, http.MethodPost, "/api/customer-statuses", input, http.StatusBadRequest)
	}
	taxonomy := requestJSON[struct {
		Statuses []domain.CustomerStatus `json:"statuses"`
	}](t, handler, http.MethodGet, "/api/customer-taxonomy", nil, http.StatusOK)
	if !slices.ContainsFunc(taxonomy.Statuses, func(item domain.CustomerStatus) bool { return item.ID == first.ID && item.Description == "Needs attention" }) {
		t.Fatalf("description was not persisted: %#v", taxonomy.Statuses)
	}
}

func TestCustomerTaxonomyRenameReorderDeleteKeepCustomersConsistent(t *testing.T) {
	handler, repository := customerSettingsHandler(t)
	active := requestJSON[domain.CustomerStatus](t, handler, http.MethodPost, "/api/customer-statuses", map[string]any{"name": "Onboarding"}, http.StatusCreated)
	fallback := requestJSON[domain.CustomerStatus](t, handler, http.MethodPost, "/api/customer-statuses", map[string]any{"name": "Steady"}, http.StatusCreated)
	tier := requestJSON[domain.CustomerTier](t, handler, http.MethodPost, "/api/customer-tiers", map[string]any{"name": "Gold"}, http.StatusCreated)
	customer := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{"name": "Acme"}, http.StatusCreated)
	other := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{"name": "Globex"}, http.StatusCreated)
	setCustomer := func(id, status, tierName string) {
		t.Helper()
		err := mutateTestWorkspace(t, repository, func(data *domain.Bootstrap) {
			for index := range data.Customers {
				if data.Customers[index].ID == id {
					data.Customers[index].Status = status
					data.Customers[index].Tier = tierName
				}
			}
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	setCustomer(customer.ID, "onboarding", "Gold")
	setCustomer(other.ID, fallback.Name, "")

	renamed := requestJSON[customerTaxonomyResult](t, handler, http.MethodPatch, "/api/customer-statuses/"+active.ID, map[string]any{"name": "Ramping", "description": "First 90 days"}, http.StatusOK)
	if renamed.Name != "Ramping" || renamed.Description != "First 90 days" {
		t.Fatalf("renamed = %#v", renamed)
	}
	if got := customerByID(t, handler, customer.ID); got.Status != "Ramping" {
		t.Fatalf("rename did not follow the customer: status=%q", got.Status)
	}

	moved := requestJSON[customerTaxonomyResult](t, handler, http.MethodPatch, "/api/customer-statuses/"+fallback.ID, map[string]any{"position": -1}, http.StatusOK)
	if moved.Position != -1 {
		t.Fatalf("position = %v, want -1", moved.Position)
	}

	archived := requestJSON[customerTaxonomyResult](t, handler, http.MethodPatch, "/api/customer-statuses/"+active.ID, map[string]any{"archived": true}, http.StatusOK)
	if archived.ArchivedAt == nil || !slices.Equal(archived.ReassignedCustomerIDs, []string{customer.ID}) {
		t.Fatalf("archive = %#v, want only %s reassigned", archived, customer.ID)
	}
	if got := customerByID(t, handler, customer.ID); got.Status != "Steady" {
		t.Fatalf("deleted status should fall back to the first remaining status, got %q", got.Status)
	}
	restored := requestJSON[customerTaxonomyResult](t, handler, http.MethodPatch, "/api/customer-statuses/"+active.ID, map[string]any{"archived": false, "restoreCustomerIds": archived.ReassignedCustomerIDs}, http.StatusOK)
	if restored.ArchivedAt != nil {
		t.Fatal("undo did not restore the status")
	}
	if got := customerByID(t, handler, customer.ID); got.Status != "Ramping" {
		t.Fatalf("undo did not restore the customer: %q", got.Status)
	}
	if got := customerByID(t, handler, other.ID); got.Status != "Steady" {
		t.Fatalf("unrelated customer changed: %q", got.Status)
	}

	requestJSON[map[string]any](t, handler, http.MethodDelete, "/api/customer-tiers/"+tier.ID, nil, http.StatusNoContent)
	if got := customerByID(t, handler, customer.ID); got.Tier != "" {
		t.Fatalf("deleting a tier should clear it on customers, got %q", got.Tier)
	}
	requestJSON[map[string]any](t, handler, http.MethodDelete, "/api/customer-statuses/missing", nil, http.StatusNotFound)
}

func TestCustomerExclusionsArchiveAndRestoreRequests(t *testing.T) {
	handler, repository := customerSettingsHandler(t)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	customer := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{"name": "Initech"}, http.StatusCreated)
	request := requestJSON[domain.CustomerRequest](t, handler, http.MethodPost, "/api/customer-requests", map[string]any{"customerId": customer.ID, "body": "Please add SSO", "issueId": bootstrap.Issues[0].ID}, http.StatusCreated)
	if err := mutateTestWorkspace(t, repository, func(data *domain.Bootstrap) {
		for index := range data.CustomerRequests {
			if data.CustomerRequests[index].ID == request.ID {
				data.CustomerRequests[index].Creator.Email = "jane@support.initech.example"
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"featureSettings": map[string]any{"customerExcludedDomains": []string{" Initech.example "}}}, http.StatusOK)
	got := customerRequestByID(t, handler, request.ID)
	if got.ArchivedAt == nil || got.ArchivedByExclusion != "initech.example" {
		t.Fatalf("excluding the sender's domain should archive the request, got %#v", got)
	}
	requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"featureSettings": map[string]any{"customerExcludedDomains": []string{}}}, http.StatusOK)
	got = customerRequestByID(t, handler, request.ID)
	if got.ArchivedAt != nil || got.ArchivedByExclusion != "" {
		t.Fatalf("removing the exclusion should restore the request, got %#v", got)
	}
	requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"featureSettings": map[string]any{"customerRevenueCurrency": "XYZ"}}, http.StatusBadRequest)
	requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"featureSettings": map[string]any{"customerRevenueFormat": "weekly"}}, http.StatusBadRequest)
	settings := requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"featureSettings": map[string]any{"customerRevenueCurrency": "JPY", "customerRevenueFormat": "monthly"}}, http.StatusOK)
	if settings.FeatureSettings.CustomerRevenueCurrency != "JPY" || settings.FeatureSettings.CustomerRevenueFormat != "monthly" {
		t.Fatalf("display options = %#v", settings.FeatureSettings)
	}
}

func TestApplyCustomerExclusionChangesKeepsOtherRequests(t *testing.T) {
	now := time.Now().UTC()
	manual := now.Add(-time.Hour)
	data := &domain.Bootstrap{CustomerRequests: []domain.CustomerRequest{
		{ID: "email", Creator: domain.User{Email: "ops@acme.test"}},
		{ID: "other", Creator: domain.User{Email: "ops@globex.test"}},
		{ID: "anonymous"},
		{ID: "manually-archived", Creator: domain.User{Email: "ceo@acme.test"}, ArchivedAt: &manual},
	}}
	applyCustomerExclusionChanges(data, nil, []string{"acme.test", "ops@globex.test"}, now)
	archived := map[string]string{}
	for _, item := range data.CustomerRequests {
		if item.ArchivedAt != nil {
			archived[item.ID] = item.ArchivedByExclusion
		}
	}
	if archived["email"] != "acme.test" || archived["other"] != "ops@globex.test" || archived["manually-archived"] != "" || len(archived) != 3 {
		t.Fatalf("archived = %#v", archived)
	}
	applyCustomerExclusionChanges(data, []string{"acme.test", "ops@globex.test"}, []string{"ops@globex.test"}, now)
	if data.CustomerRequests[0].ArchivedAt != nil || data.CustomerRequests[1].ArchivedAt == nil || data.CustomerRequests[3].ArchivedAt == nil {
		t.Fatalf("only the request archived by the removed entry should be restored: %#v", data.CustomerRequests)
	}
}

func mutateTestWorkspace(t *testing.T, repository *store.SQLiteStore, mutate func(*domain.Bootstrap)) error {
	t.Helper()
	return repository.Mutate(t.Context(), "test.fixture_updated", "fixture", nil, func(data *domain.Bootstrap) error {
		mutate(data)
		return nil
	})
}

func customerByID(t *testing.T, handler http.Handler, id string) domain.Customer {
	t.Helper()
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	index := slices.IndexFunc(bootstrap.Customers, func(item domain.Customer) bool { return item.ID == id })
	if index < 0 {
		t.Fatalf("customer %s not found", id)
	}
	return bootstrap.Customers[index]
}

func customerRequestByID(t *testing.T, handler http.Handler, id string) domain.CustomerRequest {
	t.Helper()
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	index := slices.IndexFunc(bootstrap.CustomerRequests, func(item domain.CustomerRequest) bool { return item.ID == id })
	if index < 0 {
		t.Fatalf("customer request %s not found", id)
	}
	return bootstrap.CustomerRequests[index]
}
