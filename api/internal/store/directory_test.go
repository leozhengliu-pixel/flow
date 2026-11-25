package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func scaleP10Equal(t *testing.T, want, got any) {
	t.Helper()
	a, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(got)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("wire differential: want=%s got=%s err=%v", a, b, err)
	}
}

func TestScaleP10DirectoryDifferential(t *testing.T) {
	s, workspace := scaleP05Database(t)
	scaleP05Projects(t, s, workspace, 21)
	scaleP05Finish(t, s, workspace, "projects")
	queries := []ProjectRecordQuery{
		{}, {Admin: true}, {AllowedTeamIDs: nil}, {AllowedTeamIDs: []string{}},
		{AllowedTeamIDs: []string{"public"}}, {AllowedTeamIDs: []string{"private"}},
		{Admin: true, TeamIDs: []string{"public"}}, {Admin: true, Search: "%_! CASE"},
		{Admin: true, Search: "legacy"}, {Admin: true, Archived: "true"}, {Admin: true, Archived: "all"},
	}
	for _, filter := range []ProjectDirectoryFilter{
		{Field: "status", Values: []string{"s1"}}, {Field: "status", Values: []string{"Status"}}, {Field: "status", Values: []string{"started"}},
		{Field: "priority", Values: []string{"urgent"}}, {Field: "lead", Values: []string{"lead"}}, {Field: "lead", Values: []string{""}},
		{Field: "members", Values: []string{"u1"}}, {Field: "health", Values: []string{"on-track"}}, {Field: "health", Values: []string{"at-risk"}},
		{Field: "dates", Values: []string{"overdue"}}, {Field: "dates", Values: []string{"has-target"}}, {Field: "dates", Values: []string{"no-target"}},
		{Field: "milestones", Values: []string{"m"}}, {Field: "milestones", Values: []string{"Milestone"}},
		{Field: "labels", Values: []string{""}}, {Field: "labels", Values: []string{"label"}}, {Field: "teams", Values: []string{"private"}},
		{Field: "project", Values: []string{"p-001"}}, {Field: "project", Values: []string{"legacy-001"}}, {Field: "project", Values: []string{"stale-000000000001"}},
		{Field: "customers", Values: []string{"customer:"}}, {Field: "customers", Values: []string{"customer:c1"}},
		{Field: "customers", Values: []string{"customer-owner:owner"}}, {Field: "customers", Values: []string{"customer-status:active"}}, {Field: "customers", Values: []string{"customer-tier:gold"}},
	} {
		for _, op := range []string{"is", "isNot"} {
			filter.Operator = op
			queries = append(queries, ProjectRecordQuery{Admin: true, Archived: "all", Filters: []ProjectDirectoryFilter{filter}})
		}
	}
	for _, op := range []string{"gte", "lte", "eq", "neq"} {
		for _, value := range []string{"customer-revenue:100", "customer-size:10", "customer-count:1", "customer-important-count:0"} {
			queries = append(queries, ProjectRecordQuery{Admin: true, Filters: []ProjectDirectoryFilter{{Field: "customers", Operator: op, Values: []string{value}}}})
		}
	}
	for i, query := range queries {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			query.Workspace, query.Limit = workspace, 3
			query.CustomerSummaries = map[string]*ProjectCustomerSummary{
				"p-001": {Customers: []ProjectCustomer{{ID: "c1", OwnerID: "owner", Status: "active", Tier: "gold", Revenue: 100, Size: 10}}},
				"p-003": {UnknownCustomer: true},
			}
			for _, total := range []bool{false, true} {
				query.IncludeTotal = total
				for _, cursor := range []string{"", "broken", encodeProjectCursor(3), encodeProjectCursor(1000)} {
					query.Cursor = cursor
					want, err := s.queryProjectDirectoryLegacy(t.Context(), query)
					if err != nil {
						t.Fatal(err)
					}
					got, err := s.readProjectDirectoryCatalog(t.Context(), query)
					if err != nil {
						t.Fatal(err)
					}
					scaleP10Equal(t, want, got)
				}
			}
		})
	}
}

func TestScaleP10DirectoryUnreadyFallback(t *testing.T) {
	s, workspace := scaleP05Database(t)
	projects := scaleP05Projects(t, s, workspace, 3)
	scaleP05Finish(t, s, workspace, "projects")
	projects[0].Name = "Canonical update from an old writer"
	scaleP05Put(t, s, workspace, "projects", projects[0].ID, 0, projects[0])
	q := ProjectRecordQuery{Workspace: workspace, Admin: true, Archived: "all", IncludeTotal: true}
	want, err := s.queryProjectDirectoryLegacy(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"", "off", "on", "shadow", "invalid"} {
		t.Setenv("FLOW_INDEXED_READS", mode)
		var bodies int
		ctx := WithSQLStatementObserver(t.Context(), func(query string) {
			if strings.Contains(query, "SELECT summary FROM metadata_catalog ") {
				bodies++
			}
		})
		got, err := s.QueryProjectDirectory(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		scaleP10Equal(t, want, got)
		if mode != "shadow" && bodies != 0 {
			t.Fatal("unready catalog was consulted for a served directory")
		}
	}
	if err := s.ResetMetadataCatalogBackfill(t.Context(), workspace, "projects"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_INDEXED_READS", "on")
	got, err := s.QueryProjectDirectory(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	scaleP10Equal(t, want, got)
}

func BenchmarkScaleP10CatalogDirectory(b *testing.B) {
	for _, scale := range []int{1, 10} {
		b.Run(fmt.Sprintf("body%d", scale), func(b *testing.B) {
			s := scaleP05BenchmarkStore(b, scale)
			ctx := context.Background()
			if _, err := s.db.ExecContext(ctx, `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,name TEXT NOT NULL,applied_at TEXT NOT NULL)`); err != nil {
				b.Fatal(err)
			}
			if err := s.ensureMetadataCatalog(ctx); err != nil {
				b.Fatal(err)
			}
			for {
				progress, err := s.BackfillMetadataCatalog(ctx, "bench", "projects")
				if err != nil {
					b.Fatal(err)
				}
				if progress.Complete {
					break
				}
			}
			q := ProjectRecordQuery{Workspace: "bench", Admin: true, Limit: 100, IncludeTotal: true}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				page, err := s.readProjectDirectoryCatalog(ctx, q)
				if err != nil || len(page.Items) != 100 || page.Total != 205 {
					b.Fatalf("page=%d total=%d err=%v", len(page.Items), page.Total, err)
				}
			}
		})
	}
}
