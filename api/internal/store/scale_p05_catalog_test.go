package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP05Database(t *testing.T) (*SQLiteStore, string) {
	t.Helper()
	driver, dsn := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" {
		driver, dsn = "sqlite", sqliteDSN(filepath.Join(t.TempDir(), "catalog.db"))
	}
	s := scaleP05Connect(t, driver, dsn)
	workspace := fmt.Sprintf("scale-p05-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		for _, table := range []string{"metadata_catalog_relations", "metadata_catalog", "metadata_catalog_collections", "workspace_metadata_records"} {
			if _, err := s.db.ExecContext(context.Background(), `DELETE FROM `+table+` WHERE workspace_key=?`, workspace); err != nil {
				t.Error(err)
			}
		}
		_ = s.Close()
	})
	return s, workspace
}

func scaleP05Connect(t *testing.T, dialect, dsn string) *SQLiteStore {
	t.Helper()
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	driver := dialect
	if dialect == "postgres" {
		driver = "pgx"
	} else if dialect == "mysql" {
		var err error
		dsn, err = mysqlDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &SQLiteStore{db: &sqlDatabase{DB: db, dialect: dialect}, dialect: dialect}
	if _, err := s.db.ExecContext(t.Context(), `CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY,name VARCHAR(191) NOT NULL,applied_at VARCHAR(40) NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureWorkspaceMetadataRecords(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureMetadataCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s
}

func scaleP05Put(t *testing.T, s *SQLiteStore, workspace, kind, id string, order int, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,?,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET collection_order=excluded.collection_order,data=excluded.data`, workspace, kind, id, order, raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func scaleP05Finish(t *testing.T, s *SQLiteStore, workspace, kind string) {
	t.Helper()
	for range 100 {
		progress, err := s.BackfillMetadataCatalog(t.Context(), workspace, kind)
		if err != nil {
			t.Fatal(err)
		}
		if progress.Processed > 128 || progress.Pruned > 128 {
			t.Fatalf("unbounded batch: %+v", progress)
		}
		if progress.Complete {
			return
		}
	}
	t.Fatal("backfill did not finish")
}

func scaleP05Projects(t *testing.T, s *SQLiteStore, workspace string, count int) []domain.Project {
	t.Helper()
	projects := make([]domain.Project, 0, count)
	for i := range count {
		created := time.Date(2026, 1, 2, 3, 4, 5, i%3, time.FixedZone("offset", 3600))
		project := domain.Project{
			ID: fmt.Sprintf("p-%03d", i), Name: fmt.Sprintf("Project %03d", i), Summary: "Summary %_! case", SlugID: domain.SlugWithSuffix("Current", fmt.Sprintf("%012x", i)), PreviousSlugIDs: []string{fmt.Sprintf("legacy-%03d", i)},
			Description: strings.Repeat("body\\\"\n", 4000), Priority: i % 5, Position: float64(i) / 10, Health: []string{"onTrack", "at_risk", "off-track"}[i%3],
			Status:    domain.ProjectStatus{ID: fmt.Sprintf("s%d", i%3), Name: "Status", Type: "started"},
			CreatedAt: created, UpdatedAt: created.Add(time.Duration(i%5) * time.Second),
			MemberIDs: []string{fmt.Sprintf("u%d", i%3)}, DependencyIDs: []string{"private-project"}, Initiatives: []string{"initiative"}, Customers: []string{"customer"},
			Creator: &domain.User{ID: "actor", Email: "hidden@example.test"}, IssueCount: i,
			Milestones: []domain.ProjectMilestone{{ID: "m", Name: "Milestone", Description: strings.Repeat("history", 9000), DescriptionRevisions: []domain.ProjectDescriptionRevision{{ID: "revision", Description: "old"}}}},
		}
		switch i % 4 {
		case 0:
			project.TeamIDs = nil
		case 1:
			project.TeamIDs = []string{}
		case 2:
			project.TeamIDs = []string{"private"}
		case 3:
			project.TeamIDs = []string{"public", "child"}
		}
		if i%2 == 0 {
			project.Lead = &domain.User{ID: "lead"}
			project.LabelIDs = []string{"label"}
			date := "2000-01-01"
			project.TargetDate = &date
		}
		if i%9 == 0 {
			project.ArchivedAt = &created
		}
		if i%7 == 0 {
			project.UpdatedAt = time.Time{}
		}
		scaleP05Put(t, s, workspace, "projects", project.ID, i/2, project)
		projects = append(projects, project)
	}
	return projects
}

func scaleP05ReadPages(t *testing.T, s *SQLiteStore, workspace string, q PageRequest) []domain.Project {
	t.Helper()
	all := []domain.Project{}
	for range 100 {
		page, err := s.ReadMetadataCatalogPage(t.Context(), workspace, q)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(page)
		if err != nil || len(raw) > MetadataRecordPageMaxBytes || q.MaxBytes > 0 && len(raw) > q.MaxBytes {
			t.Fatalf("page bytes=%d err=%v", len(raw), err)
		}
		for _, item := range page.Items {
			var project domain.Project
			if err := json.Unmarshal(item.Data, &project); err != nil {
				t.Fatal(err)
			}
			all = append(all, project)
		}
		if !page.HasMore {
			return all
		}
		if page.Next == "" || page.Next == q.Cursor {
			t.Fatal("pagination made no progress")
		}
		q.Cursor = page.Next
	}
	t.Fatal("too many pages")
	return nil
}

func TestScaleP05CatalogDifferentialFiltersOrdersAndSlugs(t *testing.T) {
	s, workspace := scaleP05Database(t)
	projects := scaleP05Projects(t, s, workspace, 141)
	scaleP05Finish(t, s, workspace, "projects")
	queries := []ProjectRecordQuery{
		{Archived: "all", Admin: true}, {Admin: true}, {Archived: "true", Admin: true},
		{AllowedTeamIDs: []string{}}, {AllowedTeamIDs: []string{"public"}}, {TeamIDs: []string{"child"}, Admin: true},
		{Search: "SUMMARY %_!", Admin: true}, {Search: "Project 139", Admin: true}, {Search: "no match", Admin: true},
	}
	for _, filter := range []ProjectDirectoryFilter{
		{Field: "status", Values: []string{"s1"}}, {Field: "status", Values: []string{"Status"}}, {Field: "status", Values: []string{"started"}},
		{Field: "priority", Values: []string{"urgent"}}, {Field: "lead", Values: []string{"lead"}}, {Field: "lead", Values: []string{""}},
		{Field: "members", Values: []string{"u1"}}, {Field: "health", Values: []string{"on-track"}}, {Field: "health", Values: []string{"at-risk"}},
		{Field: "dates", Values: []string{"overdue"}}, {Field: "dates", Values: []string{"has-target"}}, {Field: "dates", Values: []string{"no-target"}},
		{Field: "milestones", Values: []string{"m"}}, {Field: "milestones", Values: []string{"Milestone"}},
		{Field: "labels", Values: []string{""}}, {Field: "labels", Values: []string{"label"}}, {Field: "teams", Values: []string{"private"}},
		{Field: "project", Values: []string{"p-001"}}, {Field: "project", Values: []string{"legacy-001"}}, {Field: "project", Values: []string{"stale-000000000001"}}, {Field: "project", Values: []string{"000000000001"}},
		{Field: "customers", Values: []string{"customer:none"}},
	} {
		for _, op := range []string{"is", "isNot"} {
			filter.Operator = op
			queries = append(queries, ProjectRecordQuery{Admin: true, Archived: "all", Filters: []ProjectDirectoryFilter{filter}})
		}
	}
	for i, query := range queries {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			query.Limit, query.IncludeTotal = 100, true
			expected := filterProjectDirectory(projects, query).Items
			if len(expected) == 100 {
				query.Cursor = encodeProjectCursor(100)
				expected = append(expected, filterProjectDirectory(projects, query).Items...)
				query.Cursor = ""
			}
			filter, _ := json.Marshal(query)
			actual := scaleP05ReadPages(t, s, workspace, PageRequest{Kind: "projects", Filter: string(filter), Limit: 7})
			wantJSON, _ := json.Marshal(expected)
			gotJSON, _ := json.Marshal(actual)
			if !reflect.DeepEqual(wantJSON, gotJSON) {
				t.Fatalf("canonical/catalog differ for query %s: canonical=%d catalog=%d", filter, len(expected), len(actual))
			}
		})
	}
	for _, order := range []string{"createdAt", "updatedAt"} {
		actual := scaleP05ReadPages(t, s, workspace, PageRequest{Kind: "projects", Filter: `{"Archived":"all","Admin":true}`, Order: order, Limit: 13})
		expected := slices.Clone(projects)
		slices.SortFunc(expected, func(a, b domain.Project) int {
			left, right := a.CreatedAt, b.CreatedAt
			if order == "updatedAt" {
				if !a.UpdatedAt.IsZero() {
					left = a.UpdatedAt
				}
				if !b.UpdatedAt.IsZero() {
					right = b.UpdatedAt
				}
			}
			if cmp := right.Compare(left); cmp != 0 {
				return cmp
			}
			return strings.Compare(b.ID, a.ID)
		})
		for i := range expected {
			if expected[i].ID != actual[i].ID {
				t.Fatalf("%s order at %d: canonical=%s catalog=%s", order, i, expected[i].ID, actual[i].ID)
			}
		}
	}
}

func TestScaleP05ResumeDeleteAndHalfBackfill(t *testing.T) {
	s, workspace := scaleP05Database(t)
	scaleP05Projects(t, s, workspace, 257)
	first, err := s.BackfillMetadataCatalog(t.Context(), workspace, "projects")
	if err != nil || first.Processed != 128 || first.Complete {
		t.Fatalf("first batch=%+v err=%v", first, err)
	}
	if _, err := s.ReadMetadataCatalogPage(t.Context(), workspace, PageRequest{Kind: "projects"}); !errors.Is(err, ErrMetadataCatalogGap) {
		t.Fatalf("half backfill served as complete: %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.BackfillMetadataCatalog(canceled, workspace, "projects"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	var last string
	if err := s.db.QueryRowContext(t.Context(), `SELECT last_record_key FROM metadata_catalog_collections WHERE workspace_key=? AND field='projects'`, workspace).Scan(&last); err != nil || last != first.Cursor {
		t.Fatalf("checkpoint changed on cancellation: %s %v", last, err)
	}
	// A second handle resumes the persisted checkpoint, as a restarted replica.
	var second *SQLiteStore
	if s.dialect == "sqlite" {
		var sequence int
		var name, path string
		if err := s.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
			t.Fatal(err)
		}
		second = scaleP05Connect(t, "sqlite", sqliteDSN(path))
	} else {
		second = scaleP05Connect(t, s.dialect, os.Getenv("FLOW_TEST_DATABASE_URL"))
	}
	defer second.Close()
	progress, err := second.BackfillMetadataCatalog(t.Context(), workspace, "projects")
	if err != nil || progress.Processed != 128 || progress.Cursor != "p-255" {
		t.Fatalf("resumed batch=%+v err=%v", progress, err)
	}
	scaleP05Finish(t, second, workspace, "projects")
	status, err := second.MetadataCatalogStatus(t.Context(), workspace, "projects")
	if err != nil || !status.Complete || !status.Shadow || status.Ready || status.Revision == "" {
		t.Fatalf("unsafe coverage: %+v %v", status, err)
	}
	// Old-writer edits below the cursor and deletes are repaired on the next
	// pass. Until then the sidecar remains explicitly non-authoritative.
	scaleP05Put(t, s, workspace, "projects", "p-000", 0, domain.Project{ID: "p-000", Name: "Renamed", SlugID: "new-123456789abc", PreviousSlugIDs: []string{"legacy"}})
	if _, err := s.db.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key='p-001'`, workspace); err != nil {
		t.Fatal(err)
	}
	if err := second.ResetMetadataCatalogBackfill(t.Context(), workspace, "projects"); err != nil {
		t.Fatal(err)
	}
	scaleP05Finish(t, second, workspace, "projects")
	var count int
	for _, table := range []string{"metadata_catalog", "metadata_catalog_relations"} {
		if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table+` WHERE workspace_key=? AND field='projects' AND record_key='p-001'`, workspace).Scan(&count); err != nil || count != 0 {
			t.Fatalf("stale delete in %s: %d %v", table, count, err)
		}
	}
	var name []byte
	if err := s.db.QueryRowContext(t.Context(), `SELECT summary FROM metadata_catalog WHERE workspace_key=? AND field='projects' AND record_key='p-000'`, workspace).Scan(&name); err != nil || !strings.Contains(string(name), "Renamed") {
		t.Fatalf("old writer update missing: %s %v", name, err)
	}
}

func TestScaleP05CustomerFilterDifferential(t *testing.T) {
	s, workspace := scaleP05Database(t)
	projects := scaleP05Projects(t, s, workspace, 5)
	scaleP05Finish(t, s, workspace, "projects")
	customers := map[string]*ProjectCustomerSummary{
		"p-001": {Customers: []ProjectCustomer{{ID: "c1", OwnerID: "owner", Status: "active", Tier: "tier", Revenue: 100, Size: 20, Important: true}}},
		"p-002": {UnknownCustomer: true},
	}
	for _, values := range [][]string{{"customer:c1"}, {"customer-owner:owner"}, {"customer-status:active"}, {"customer-tier:tier"}, {"customer-count:1"}, {"customer-revenue:100"}, {"customer-size:20"}, {"customer-important-count:1"}} {
		for _, operator := range []string{"is", "isNot", "gte", "lte", "eq", "neq"} {
			query := ProjectRecordQuery{Workspace: workspace, Archived: "all", Admin: true, CustomerSummaries: customers, Filters: []ProjectDirectoryFilter{{Field: "customers", Operator: operator, Values: values}}}
			expected := filterProjectDirectory(projects, query)
			page, err := s.ReadMetadataCatalogProjectPage(t.Context(), query, PageRequest{})
			if err != nil {
				t.Fatal(err)
			}
			actual := []string{}
			for _, item := range page.Items {
				actual = append(actual, item.Key.ID)
			}
			wanted := []string{}
			for _, project := range expected.Items {
				wanted = append(wanted, project.ID)
			}
			if !reflect.DeepEqual(actual, wanted) {
				t.Fatalf("customer filter %s %v: canonical=%v catalog=%v", operator, values, wanted, actual)
			}
		}
	}
}

func TestScaleP05FailedBatchRollsBackCheckpointAndRelations(t *testing.T) {
	s, workspace := scaleP05Database(t)
	scaleP05Projects(t, s, workspace, 3)
	if _, err := s.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'projects','z-bad',4,?)`, workspace, []byte(`{"id":"z-bad","createdAt":"private-record-value"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BackfillMetadataCatalog(t.Context(), workspace, "projects"); err == nil {
		t.Fatal("invalid canonical timestamp accepted")
	} else if strings.Contains(err.Error(), "private-record-value") {
		t.Fatal("backfill error disclosed canonical content")
	}
	if s.dialect == "sqlite" {
		if _, err := s.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='projects' AND record_key='z-bad'`, []byte(`{"id":"z-bad","description":"private-record-value",`), workspace); err != nil {
			t.Fatal(err)
		}
		if _, err := s.BackfillMetadataCatalog(t.Context(), workspace, "projects"); err == nil || strings.Contains(err.Error(), "private-record-value") {
			t.Fatal("invalid JSON was accepted or its contents disclosed")
		}
	}
	for _, table := range []string{"metadata_catalog", "metadata_catalog_relations", "metadata_catalog_collections"} {
		var count int
		if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table+` WHERE workspace_key=?`, workspace).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed batch left %s rows=%d err=%v", table, count, err)
		}
	}
	scaleP05Put(t, s, workspace, "projects", "z-bad", 4, domain.Project{ID: "z-bad"})
	hooks := commitHooks{beforeCommit: func(tx *sqlTx) { _ = tx.Tx.Rollback() }}
	s.db.commitHooks.Store(&hooks)
	if _, err := s.BackfillMetadataCatalog(t.Context(), workspace, "projects"); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("commit fault: %v", err)
	}
	s.db.commitHooks.Store(nil)
	for _, table := range []string{"metadata_catalog", "metadata_catalog_relations", "metadata_catalog_collections"} {
		var count int
		if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table+` WHERE workspace_key=?`, workspace).Scan(&count); err != nil || count != 0 {
			t.Fatalf("commit fault left %s rows=%d err=%v", table, count, err)
		}
	}
	scaleP05Finish(t, s, workspace, "projects")
}
