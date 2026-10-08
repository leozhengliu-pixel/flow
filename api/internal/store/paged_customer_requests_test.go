package store

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Paged metadata carries no issues, so customer requests linked to an issue
// must be kept by checking the issue's visibility by id (the customer page and
// the issue's customer section read them from the metadata).
func TestPagedWorkspaceMetadataKeepsIssueLinkedCustomerRequests(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "paged-requests.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	if len(data.Issues) == 0 {
		t.Skip("fixture has no issues")
	}
	issueID := data.Issues[0].ID
	now := time.Now().UTC()
	err = repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, "test.customer_requests", "", nil, func(d *domain.Bootstrap) error {
		d.Customers = append(d.Customers, domain.Customer{ID: "customer_paged", Name: "Paged Co", Status: "active", Domains: []string{}, CreatedAt: now, UpdatedAt: now})
		d.CustomerRequests = append(d.CustomerRequests,
			domain.CustomerRequest{ID: "customer_request_issue", CustomerID: "customer_paged", Body: "on an issue", IssueID: issueID, Attachments: []domain.Attachment{}, CreatedAt: now, UpdatedAt: now},
			domain.CustomerRequest{ID: "customer_request_hidden", CustomerID: "customer_paged", Body: "on a missing issue", IssueID: "issue_missing", Attachments: []domain.Attachment{}, CreatedAt: now, UpdatedAt: now},
			domain.CustomerRequest{ID: "customer_request_unlinked", CustomerID: "customer_paged", Body: "unlinked", Attachments: []domain.Attachment{}, CreatedAt: now, UpdatedAt: now},
		)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := repo.PagedWorkspaceMetadata(t.Context(), data.Workspace.URLKey, data.Viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	has := func(id string) bool {
		return slices.ContainsFunc(metadata.CustomerRequests, func(item domain.CustomerRequest) bool { return item.ID == id })
	}
	if !has("customer_request_issue") || !has("customer_request_unlinked") {
		t.Fatalf("paged metadata dropped visible customer requests: %#v", metadata.CustomerRequests)
	}
	if has("customer_request_hidden") {
		t.Fatal("paged metadata kept a request on an issue the viewer cannot see")
	}
}
