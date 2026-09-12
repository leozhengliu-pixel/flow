package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestCustomerFilterHTTPGroupedPagination(t *testing.T) {
	f := newMCPContractFixture(t)
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "test.customers", "", nil, func(d *domain.Bootstrap) error {
		d.Customers = []domain.Customer{{ID: "customer-contract", Name: "Customer", AnnualRevenue: 100}}
		d.CustomerRequests = []domain.CustomerRequest{{ID: "customer-request-1", IssueID: d.Issues[0].ID, CustomerID: "customer-contract"}, {ID: "customer-request-2", IssueID: d.Issues[1].ID, CustomerID: "customer-contract"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	filter := `{"and":[{"field":"customerId","operator":"is","values":["customer:customer-contract"]}]}`
	get := func(path string, target any) {
		t.Helper()
		req, _ := http.NewRequest("GET", f.host.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+f.secret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("customer filter returned HTTP %d", resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			t.Fatal(err)
		}
	}
	params := url.Values{"filter": {filter}, "groupBy": {"status"}, "limit": {"1"}, "includeTotal": {"true"}, "projection": {"list"}, "projectId": {f.data.Projects[0].ID}}
	var page store.IssueRecordPage
	get("/api/issue-records?"+params.Encode(), &page)
	if page.Total != 2 || len(page.Items) != 1 || !page.HasMore {
		t.Fatalf("wrong page: %+v", page)
	}
	first := page.Items[0].ID
	params.Set("cursor", page.NextCursor)
	get("/api/issue-records?"+params.Encode(), &page)
	if len(page.Items) != 1 || page.Items[0].ID == first || page.HasMore {
		t.Fatal("cursor duplicated or lost customer issues")
	}
	params.Del("cursor")
	var grouped struct {
		Groups []store.IssueRecordGroup `json:"groups"`
	}
	get("/api/issue-records/groups?"+params.Encode(), &grouped)
	var count int64
	for _, group := range grouped.Groups {
		count += group.Count
	}
	if count != 2 {
		t.Fatalf("wrong group counts: %+v", grouped)
	}
}
