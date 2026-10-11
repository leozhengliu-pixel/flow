package store

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

var releaseFilterIssues = []string{"issue_1", "issue_2", "issue_33", "issue_53156"}

// Releases: r1 (pipeline p1, released at T) holds issue_1 and issue_2, r2
// (p2, released T+48h) holds issue_2, r3 (p1, planned) holds issue_33.
// issue_53156 is in no release.
var releaseFilterT = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func releaseFilterFixture(t *testing.T) (*SQLiteStore, IssueRecordQuery) {
	t.Helper()
	repo, q, _ := issuePointReadFixture(t)
	q.Access = nil
	released, later := releaseFilterT, releaseFilterT.Add(48*time.Hour)
	if err := repo.MutateWorkspace(t.Context(), q.Workspace, "release.created", "", nil, func(d *domain.Bootstrap) error {
		d.Releases = append(d.Releases,
			domain.Release{ID: "r1", SlugID: "r1", Name: "1.0", Status: "released", PipelineID: "p1", Stage: "Released", IssueIDs: []string{"issue_1", "issue_2"}, ReleasedAt: &released},
			domain.Release{ID: "r2", SlugID: "r2", Name: "1.1", Status: "released", PipelineID: "p2", Stage: "Released", IssueIDs: []string{"issue_2"}, ReleasedAt: &later},
			domain.Release{ID: "r3", SlugID: "r3", Name: "2.0", Status: "planned", PipelineID: "p1", Stage: "Planned", IssueIDs: []string{"issue_33", ""}},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return repo, q
}

func releaseFilterMatches(t *testing.T, repo *SQLiteStore, q IssueRecordQuery, node IssueFilter) []string {
	t.Helper()
	q.Filter = IssueFilter{And: []IssueFilter{{Field: "id", Values: releaseFilterIssues}, node}}
	q.Limit, q.IncludeTotal = 50, true
	page, err := repo.QueryIssueRecords(t.Context(), q)
	if err != nil {
		t.Fatalf("%+v: %v", node, err)
	}
	got := []string{}
	for _, item := range page.Items {
		got = append(got, item.ID)
	}
	slices.Sort(got)
	if page.Total != int64(len(got)) {
		t.Fatalf("%+v: total %d for %v", node, page.Total, got)
	}
	return got
}

func TestReleasedDateFilterBoundariesAndTimezones(t *testing.T) {
	repo, q := releaseFilterFixture(t)
	instant := func(value time.Time) string { return value.Format(time.RFC3339Nano) }
	shanghai := time.FixedZone("UTC+8", 8*3600)
	for _, test := range []struct {
		name string
		node IssueFilter
		want []string
	}{
		{"after just before r1", IssueFilter{Field: "releasedAt", Operator: "after", Values: []string{instant(releaseFilterT.Add(-time.Millisecond))}}, []string{"issue_1", "issue_2"}},
		// "after" is strict: a release exactly at the threshold does not match.
		{"after exactly r1", IssueFilter{Field: "releasedAt", Operator: "after", Values: []string{instant(releaseFilterT)}}, []string{"issue_2"}},
		{"gte exactly r1", IssueFilter{Field: "releasedAt", Operator: "gte", Values: []string{instant(releaseFilterT)}}, []string{"issue_1", "issue_2"}},
		{"after r2", IssueFilter{Field: "releasedAt", Operator: "after", Values: []string{instant(releaseFilterT.Add(48 * time.Hour))}}, []string{}},
		// "before" matches when any release is earlier (issue_2 has r1 before and r2 after).
		{"before r2", IssueFilter{Field: "releasedAt", Operator: "before", Values: []string{instant(releaseFilterT.Add(time.Hour))}}, []string{"issue_1", "issue_2"}},
		{"before exactly r1", IssueFilter{Field: "releasedAt", Operator: "before", Values: []string{instant(releaseFilterT)}}, []string{}},
		// The same instant written with an offset compares in UTC.
		{"offset instant", IssueFilter{Field: "releasedAt", Operator: "after", Values: []string{releaseFilterT.In(shanghai).Add(time.Hour).Format(time.RFC3339)}}, []string{"issue_2"}},
		// 20:00 in UTC+8 is r1's instant (12:00Z): strict "before" excludes it, 20:00Z does not.
		{"offset wall clock", IssueFilter{Field: "releasedAt", Operator: "before", Values: []string{"2026-10-01T20:00:00+08:00"}}, []string{}},
		{"utc wall clock", IssueFilter{Field: "releasedAt", Operator: "before", Values: []string{"2026-10-01T20:00:00Z"}}, []string{"issue_1", "issue_2"}},
		{"calendar day", IssueFilter{Field: "releasedAt", Operator: "after", Values: []string{"2026-10-02"}}, []string{"issue_2"}},
		{"no released date", IssueFilter{Field: "releasedAt", Operator: "isEmpty"}, []string{"issue_33", "issue_53156"}},
		{"has released date", IssueFilter{Field: "releasedAt", Operator: "isNotEmpty"}, []string{"issue_1", "issue_2"}},
		{"not after", IssueFilter{Not: &IssueFilter{Field: "releasedAt", Operator: "after", Values: []string{instant(releaseFilterT)}}}, []string{"issue_1", "issue_33", "issue_53156"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := releaseFilterMatches(t, repo, q, test.node); !slices.Equal(got, test.want) {
				t.Fatalf("got %v want %v", got, test.want)
			}
		})
	}
}

func TestReleaseMembershipFilters(t *testing.T) {
	repo, q := releaseFilterFixture(t)
	for _, test := range []struct {
		node IssueFilter
		want []string
	}{
		{IssueFilter{Field: "release", Values: []string{"r1"}}, []string{"issue_1", "issue_2"}},
		{IssueFilter{Field: "release", Operator: "isNot", Values: []string{"r1"}}, []string{"issue_33", "issue_53156"}},
		{IssueFilter{Field: "release", Values: []string{"r3", ""}}, []string{"issue_33", "issue_53156"}},
		{IssueFilter{Field: "releasePipeline", Values: []string{"p2"}}, []string{"issue_2"}},
		{IssueFilter{Field: "releasePipeline", Values: []string{"p1"}}, []string{"issue_1", "issue_2", "issue_33"}},
		{IssueFilter{Field: "releaseStage", Values: []string{"Planned"}}, []string{"issue_33"}},
		{IssueFilter{Field: "releaseStatus", Values: []string{"released"}}, []string{"issue_1", "issue_2"}},
		{IssueFilter{Field: "release", Operator: "isEmpty"}, []string{"issue_53156"}},
		{IssueFilter{Field: "release", Operator: "isNotEmpty"}, []string{"issue_1", "issue_2", "issue_33"}},
	} {
		if got := releaseFilterMatches(t, repo, q, test.node); !slices.Equal(got, test.want) {
			t.Fatalf("%+v: got %v want %v", test.node, got, test.want)
		}
	}
	for _, node := range []IssueFilter{
		{Field: "releasedAt", Values: []string{"r1"}},
		{Field: "releasedAt", Operator: "after", Values: []string{"yesterday"}},
		{Field: "releasedAt", Operator: "after", Values: []string{"2026-10-01", "2026-10-02"}},
		{Field: "release", Operator: "after", Values: []string{"2026-10-01"}},
		{Field: "release", Operator: "contains", Values: []string{"r"}},
	} {
		if err := ValidateIssueFilter(node); !errors.Is(err, ErrIssueQuery) {
			t.Fatalf("invalid release filter accepted: %+v", node)
		}
	}
}

func TestReleaseIssueIndexLifecycleAndMigration(t *testing.T) {
	repo, q := releaseFilterFixture(t)
	has := IssueFilter{Field: "releasedAt", Operator: "isNotEmpty"}
	// Releasing r3 indexes its date for its issues.
	if err := repo.MutateWorkspace(t.Context(), q.Workspace, "release.updated", "r3", nil, func(d *domain.Bootstrap) error {
		for i := range d.Releases {
			if d.Releases[i].ID == "r3" {
				now := releaseFilterT.Add(72 * time.Hour)
				d.Releases[i].Status, d.Releases[i].ReleasedAt = "released", &now
				d.Releases[i].IssueIDs = []string{"issue_33", "issue_53156"}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := releaseFilterMatches(t, repo, q, has); !slices.Equal(got, releaseFilterIssues) {
		t.Fatalf("release edit left a stale index: %v", got)
	}
	// Removing an issue from r1 and deleting r2 drops their rows.
	if err := repo.MutateWorkspace(t.Context(), q.Workspace, "release.deleted", "r2", nil, func(d *domain.Bootstrap) error {
		d.Releases = slices.DeleteFunc(d.Releases, func(release domain.Release) bool { return release.ID == "r2" })
		for i := range d.Releases {
			if d.Releases[i].ID == "r1" {
				d.Releases[i].IssueIDs = []string{"issue_1"}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := releaseFilterMatches(t, repo, q, IssueFilter{Field: "release", Operator: "isEmpty"}); !slices.Equal(got, []string{"issue_2"}) {
		t.Fatalf("deleted release still indexed: %v", got)
	}
	// A failed mutation leaves the index untouched.
	if err := repo.MutateWorkspace(t.Context(), q.Workspace, "release.updated", "", nil, func(d *domain.Bootstrap) error { d.Releases = nil; return errors.New("abort") }); err == nil {
		t.Fatal("failed mutation committed")
	}
	want := releaseFilterMatches(t, repo, q, has)
	// Existing databases are backfilled from the release records.
	for _, table := range []string{"release_issue_filter_records", "release_issue_filter_migrations"} {
		if _, err := repo.db.ExecContext(t.Context(), "DELETE FROM "+table); err != nil {
			t.Fatal(err)
		}
	}
	if got := releaseFilterMatches(t, repo, q, has); len(got) != 0 {
		t.Fatalf("index not cleared: %v", got)
	}
	for range 2 {
		if err := repo.migrateReleaseIssueIndex(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if got := releaseFilterMatches(t, repo, q, has); !slices.Equal(got, want) {
		t.Fatalf("migration got %v want %v", got, want)
	}
}

// The filter SQL is dialect-neutral: placeholders rebind for Postgres and the
// index writes' conflict clause becomes INSERT IGNORE on MySQL.
func TestReleaseFilterSQLAcrossDialects(t *testing.T) {
	clause, args, err := compileReleaseFilter(IssueFilter{Field: "releasedAt", Operator: "after", Values: []string{"2026-10-01T08:00:00+08:00"}})
	if err != nil || len(args) != 1 || args[0] != "2026-10-01T00:00:00.000000000Z" {
		t.Fatalf("clause=%q args=%v err=%v", clause, args, err)
	}
	if !strings.Contains(clause, "r.released_at<>''") || !strings.Contains(clause, "r.issue_id=i.id") {
		t.Fatalf("released-at clause is not an indexed per-issue lookup: %q", clause)
	}
	if postgres := rewriteSQL("SELECT 1 FROM issue_records i WHERE "+clause, "postgres"); !strings.Contains(postgres, "r.released_at>$1") {
		t.Fatalf("postgres rewrite = %q", postgres)
	}
	insert := rewriteSQL(`INSERT INTO release_issue_filter_records(workspace_key,release_id,issue_id,pipeline_id,stage,status,released_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, "mysql")
	if !strings.HasPrefix(insert, "INSERT IGNORE INTO release_issue_filter_records") {
		t.Fatalf("mysql rewrite = %q", insert)
	}
}
