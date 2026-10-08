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
			{ID: "r1", IssueID: "issue_1", CustomerID: "c1"}, {ID: "r2", IssueID: "issue_1", CustomerID: "c1", Priority: 1}, {ID: "r3", IssueID: "issue_1", CustomerID: "c2"},
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

func TestCustomerNumberFiltersAndOrdering(t *testing.T) {
	repo, q := customerFilterFixture(t)
	ids := func(filter IssueFilter) []string {
		t.Helper()
		query := q
		query.Filter = IssueFilter{And: []IssueFilter{q.Filter, filter}}
		query.IncludeTotal = true
		page, err := repo.QueryIssueRecords(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, item := range page.Items {
			got = append(got, item.ID)
		}
		slices.Sort(got)
		return got
	}
	for _, test := range []struct {
		operator, value string
		want            []string
	}{
		{"is", "customer-count:gte:2", []string{"issue_1"}},
		{"is", "customer-count:eq:1", []string{"issue_2", "issue_33"}},
		{"is", "customer-count:lte:0", []string{"issue_53156"}},
		{"gte", "customer-count:2", []string{"issue_1"}},
		{"is", "customer-important-count:gte:1", []string{"issue_1"}},
		{"is", "customer-important-count:eq:0", []string{"issue_2", "issue_33", "issue_53156"}},
		{"is", "customer-revenue:gte:500", []string{"issue_1"}},
		{"is", "customer-revenue:lte:0", []string{"issue_1", "issue_2"}},
		{"is", "customer-size:gte:10", []string{"issue_1"}},
		{"neq", "customer-size:12", []string{"issue_1", "issue_2"}},
	} {
		got := ids(IssueFilter{Field: "customerId", Operator: test.operator, Values: []string{test.value}})
		if !slices.Equal(got, test.want) {
			t.Fatalf("%s %s: got %v want %v", test.operator, test.value, got, test.want)
		}
	}
	if got := ids(IssueFilter{Not: &IssueFilter{Field: "customerId", Values: []string{"customer-count:gte:2"}}}); !slices.Equal(got, []string{"issue_2", "issue_33", "issue_53156"}) {
		t.Fatalf("negated number filter: %v", got)
	}
	for _, node := range []IssueFilter{{Field: "customerId", Values: []string{"customer-count:gt:2"}}, {Field: "customerId", Values: []string{"customer-count:gte:many"}}, {Field: "customerId", Operator: "gte", Values: []string{"customer:c1"}}} {
		if err := ValidateIssueFilter(node); !errors.Is(err, ErrIssueQuery) {
			t.Fatalf("invalid number filter accepted: %+v", node)
		}
	}
	for _, test := range []struct {
		sort, direction string
		want            []string
	}{
		{"customerCount", "desc", []string{"issue_1", "issue_33", "issue_2", "issue_53156"}},
		{"customerRevenue", "desc", []string{"issue_1", "issue_53156", "issue_33", "issue_2"}},
		{"customerImportantCount", "asc", []string{"issue_2", "issue_33", "issue_53156", "issue_1"}},
	} {
		query := q
		query.Sort, query.Direction, query.Limit = test.sort, test.direction, 1
		got := []string{}
		for len(got) < 8 {
			page, err := repo.QueryIssueRecords(t.Context(), query)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range page.Items {
				got = append(got, item.ID)
			}
			if !page.HasMore {
				break
			}
			query.Cursor = page.NextCursor
		}
		if !slices.Equal(got, test.want) {
			t.Fatalf("%s %s: got %v want %v", test.sort, test.direction, got, test.want)
		}
	}
}

func TestCustomerGroupingIsMultiMembership(t *testing.T) {
	repo, q := customerFilterFixture(t)
	q.GroupBy = "customer"
	groups, err := repo.QueryIssueGroups(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, group := range groups {
		got[group.Value] = group.Count
	}
	want := map[string]int64{"c1": 1, "c2": 2, UnknownCustomerGroup: 1, "": 1}
	if len(got) != len(want) {
		t.Fatalf("groups %v want %v", got, want)
	}
	for value, count := range want {
		if got[value] != count {
			t.Fatalf("groups %v want %v", got, want)
		}
	}
	for value, ids := range map[string][]string{"c2": {"issue_1", "issue_2"}, UnknownCustomerGroup: {"issue_33"}, "": {"issue_53156"}} {
		query := q
		group := value
		query.GroupValue = &group
		page, err := repo.QueryIssueRecords(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		items := []string{}
		for _, item := range page.Items {
			items = append(items, item.ID)
		}
		slices.Sort(items)
		if !slices.Equal(items, ids) {
			t.Fatalf("group %q: got %v want %v", value, items, ids)
		}
	}
}

func TestProjectCustomerSummariesAndFilters(t *testing.T) {
	repo, q := customerFilterFixture(t)
	summaries, err := repo.ProjectCustomerSummaries(t.Context(), q.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	direct := summaries["project_aut"]
	if direct == nil || !slices.ContainsFunc(direct.Customers, func(customer ProjectCustomer) bool { return customer.ID == "c1" && customer.Revenue == 1000 && customer.OwnerID == "owner" }) {
		t.Fatalf("project request missing from summary: %+v", direct)
	}
	summary := &ProjectCustomerSummary{Customers: []ProjectCustomer{{ID: "c1", Important: true, OwnerID: "owner", Status: "active", Revenue: 1000, Size: 12}, {ID: "c2", Tier: "silver"}}, UnknownCustomer: true}
	for _, test := range []struct {
		operator string
		values   []string
		want     bool
	}{
		{"is", []string{"customer:c2"}, true}, {"isNot", []string{"customer:c2"}, false}, {"is", []string{"customer:"}, true},
		{"is", []string{"customer-owner:owner"}, true}, {"is", []string{"customer-status:trial"}, false}, {"is", []string{"customer-tier:silver"}, true},
		{"gte", []string{"customer-count:3"}, true}, {"eq", []string{"customer-important-count:1"}, true}, {"lte", []string{"customer-revenue:0"}, true}, {"gte", []string{"customer-size:13"}, false},
	} {
		got, err := MatchProjectCustomerFilter(summary, test.operator, test.values)
		if err != nil || got != test.want {
			t.Fatalf("%s %v: got %v err %v", test.operator, test.values, got, err)
		}
	}
	if matched, _ := MatchProjectCustomerFilter(nil, "eq", []string{"customer-count:0"}); !matched {
		t.Fatal("projects without requests have zero customers")
	}
	for _, values := range [][]string{{"customer-count:many"}, {"nope"}} {
		if _, err := MatchProjectCustomerFilter(nil, "gte", values); err == nil {
			t.Fatalf("invalid values accepted: %v", values)
		}
	}
	page, err := repo.QueryProjectDirectory(t.Context(), ProjectRecordQuery{Workspace: q.Workspace, Admin: true, Limit: 100, Filters: []ProjectDirectoryFilter{{Field: "customers", Operator: "is", Values: []string{"customer:c1"}}}, CustomerSummaries: summaries})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(page.Items, func(project domain.Project) bool { return project.ID == "project_aut" }) {
		t.Fatal("customer filter dropped the requesting project")
	}
	for _, project := range page.Items {
		if matched, _ := MatchProjectCustomerFilter(summaries[project.ID], "is", []string{"customer:c1"}); !matched {
			t.Fatalf("project %s does not request c1", project.ID)
		}
	}
}
