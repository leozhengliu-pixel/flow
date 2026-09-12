package store

import (
	"errors"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func customerFilterFixture(t *testing.T) (*SQLiteStore, IssueRecordQuery) {
	t.Helper()
	repo, q, _ := issuePointReadFixture(t)
	q.Access = nil
	if err := repo.MutateWorkspace(t.Context(), q.Workspace, "test.customers", "", nil, func(d *domain.Bootstrap) error {
		d.Customers = []domain.Customer{{ID: "c1", OwnerID: "owner", Status: "active", Tier: "gold", AnnualRevenue: 1000, Size: 12}, {ID: "c2", Status: "trial", Tier: "silver"}}
		now := time.Now()
		d.CustomerRequests = []domain.CustomerRequest{
			{ID: "r1", IssueID: "issue_1", CustomerID: "c1"}, {ID: "r2", IssueID: "issue_1", CustomerID: "c1"}, {ID: "r3", IssueID: "issue_1", CustomerID: "c2"},
			{ID: "r4", IssueID: "issue_2", CustomerID: "c2"}, {ID: "r5", IssueID: "issue_33", CustomerID: "missing"}, {ID: "r6", IssueID: "issue_33", CustomerID: "missing2"},
			{ID: "r7", IssueID: "issue_53156", CustomerID: "c1", ArchivedAt: &now}, {ID: "r8", ProjectID: "project_aut", CustomerID: "c1"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	q.Filter = IssueFilter{Field: "id", Values: []string{"issue_1", "issue_2", "issue_33", "issue_53156"}}
	return repo, q
}

func TestCustomerFilterQueryGroupsAndNegation(t *testing.T) {
	repo, q := customerFilterFixture(t)
	for _, test := range []struct {
		value string
		want  []string
	}{
		{"customer:c1", []string{"issue_1"}}, {"customer:c2", []string{"issue_1", "issue_2"}},
		{"customer:", []string{"issue_33"}}, {"customer-count:0", []string{"issue_53156"}},
		{"customer-count:1", []string{"issue_2", "issue_33"}}, {"customer-count:2+", []string{"issue_1"}},
		{"customer-owner:", []string{"issue_2"}}, {"customer-owner:owner", []string{"issue_1"}},
		{"customer-status:active", []string{"issue_1"}}, {"customer-tier:silver", []string{"issue_1", "issue_2"}},
		{"customer-revenue:", []string{"issue_2"}}, {"customer-revenue:any", []string{"issue_1"}},
		{"customer-size:", []string{"issue_2"}}, {"customer-size:any", []string{"issue_1"}},
	} {
		t.Run(test.value, func(t *testing.T) {
			for _, negative := range []bool{false, true} {
				query := q
				operator := "is"
				want := slices.Clone(test.want)
				if negative {
					operator = "isNot"
					want = nil
					for _, id := range q.Filter.Values {
						if !slices.Contains(test.want, id) {
							want = append(want, id)
						}
					}
				}
				query.Filter = IssueFilter{And: []IssueFilter{q.Filter, {Field: "customerId", Operator: operator, Values: []string{test.value}}}}
				query.Limit, query.IncludeTotal, query.GroupBy = 1, true, "status"
				groups, err := repo.QueryIssueGroups(t.Context(), query)
				if err != nil {
					t.Fatal(err)
				}
				var count int64
				for _, group := range groups {
					count += group.Count
				}
				if count != int64(len(want)) {
					t.Fatalf("group count %d != %d", count, len(want))
				}
				got := []string{}
				for {
					page, err := repo.QueryIssueRecords(t.Context(), query)
					if err != nil {
						t.Fatal(err)
					}
					if page.Total != int64(len(want)) {
						t.Fatal("wrong total")
					}
					for _, item := range page.Items {
						got = append(got, item.ID)
					}
					if !page.HasMore {
						break
					}
					query.Cursor = page.NextCursor
					if len(got) > 4 {
						t.Fatal("cursor did not advance")
					}
				}
				slices.Sort(got)
				slices.Sort(want)
				if !slices.Equal(got, want) {
					t.Fatalf("negative=%v got=%v want=%v", negative, got, want)
				}
			}
		})
	}
	q.Filter = IssueFilter{And: []IssueFilter{q.Filter, {Field: "customerId", Values: []string{"customer:c1", "customer:"}}}}
	q.AllowedTeamIDs = []string{}
	page, err := repo.QueryIssueRecords(t.Context(), q)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("customer predicate bypassed team scope", err)
	}
	for _, node := range []IssueFilter{{Field: "customerId", Values: []string{"unknown"}}, {Field: "customerId", Operator: "contains", Values: []string{"customer:c1"}}} {
		if err := ValidateIssueFilter(node); !errors.Is(err, ErrIssueQuery) {
			t.Fatal("invalid customer filter accepted")
		}
	}
}

func TestCustomerIndexLifecycleMigrationAndRollback(t *testing.T) {
	repo, q := customerFilterFixture(t)
	query := func(value string) []domain.Issue {
		t.Helper()
		q.Filter = IssueFilter{Field: "customerId", Values: []string{value}}
		page, err := repo.QueryIssueRecords(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		return page.Items
	}
	if err := repo.MutateWorkspace(t.Context(), q.Workspace, "test.customer.update", "c2", nil, func(d *domain.Bootstrap) error { d.Customers[1].AnnualRevenue = 100; return nil }); err != nil {
		t.Fatal(err)
	}
	if len(query("customer-revenue:any")) != 2 {
		t.Fatal("customer edit left stale index")
	}
	if err := repo.MutateWorkspace(t.Context(), q.Workspace, "test.request.archive", "r4", nil, func(d *domain.Bootstrap) error {
		now := time.Now()
		d.CustomerRequests[3].ArchivedAt = &now
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(query("customer:c2")) != 1 {
		t.Fatal("archived request still matched")
	}
	if err := repo.MutateWorkspace(t.Context(), q.Workspace, "test.request.rollback", "", nil, func(d *domain.Bootstrap) error { d.CustomerRequests = nil; return errors.New("abort") }); err == nil {
		t.Fatal("failed mutation committed")
	}
	if len(query("customer:c1")) != 1 {
		t.Fatal("rollback changed index")
	}
	for _, table := range []string{"customer_filter_records", "customer_request_filter_records", "customer_filter_migrations"} {
		if _, err := repo.db.ExecContext(t.Context(), "DELETE FROM "+table); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.migrateCustomerFilterIndex(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := repo.migrateCustomerFilterIndex(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(query("customer:c1")) != 1 {
		t.Fatal("migration did not restore index")
	}
	if err := repo.MutateWorkspace(t.Context(), q.Workspace, "test.customer.delete", "c1", nil, func(d *domain.Bootstrap) error {
		d.Customers = slices.DeleteFunc(d.Customers, func(c domain.Customer) bool { return c.ID == "c1" })
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(query("customer:")) != 2 {
		t.Fatal("deleted customer should be unknown")
	}
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE issue_records SET data='invalid-json',list_data='invalid-json' WHERE id='issue_53156'`); err != nil {
		t.Fatal(err)
	}
	if len(query("customer:c2")) != 1 {
		t.Fatal("query decoded an unrelated issue")
	}
}
