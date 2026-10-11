package main

import (
	"testing"
	"time"

	"flow/api/internal/domain"
)

// The in-memory query walk (/api/issues/query with a filter) reads Releases ▸
// Released date and the release membership fields like the SQL store: any of
// the issue's releases matches, "isEmpty" means none released.
func TestLegacyIssueQueryReleasedDate(t *testing.T) {
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	later := at.Add(48 * time.Hour)
	data := domain.Bootstrap{Releases: []domain.Release{
		{ID: "r1", PipelineID: "p1", Status: "released", IssueIDs: []string{"a", "b"}, ReleasedAt: &at},
		{ID: "r2", PipelineID: "p2", Status: "released", IssueIDs: []string{"b"}, ReleasedAt: &later},
		{ID: "r3", PipelineID: "p1", Status: "planned", IssueIDs: []string{"c"}},
	}}
	issues := []domain.Issue{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	for _, test := range []struct {
		query string
		want  []string
	}{
		{`{"field":"releasedAt","operator":"after","values":["2026-10-01T11:59:59Z"]}`, []string{"a", "b"}},
		{`{"field":"releasedAt","operator":"after","values":["2026-10-01T20:00:00+08:00"]}`, []string{"b"}},
		{`{"field":"releasedAt","operator":"before","values":["2026-10-02T00:00:00Z"]}`, []string{"a", "b"}},
		{`{"field":"releasedAt","operator":"isEmpty"}`, []string{"c", "d"}},
		{`{"field":"releasePipeline","operator":"in","values":["p2"]}`, []string{"b"}},
		{`{"field":"release","operator":"isEmpty"}`, []string{"d"}},
		{`{"field":"releaseStatus","operator":"in","values":["planned"]}`, []string{"c"}},
	} {
		root, err := decodeIssueQuery(test.query)
		if err != nil {
			t.Fatal(err)
		}
		env := newIssueMatchEnv(data, root)
		got := []string{}
		for _, issue := range issues {
			if root.matchesIn(issue, env) {
				got = append(got, issue.ID)
			}
		}
		if len(got) != len(test.want) {
			t.Fatalf("%s: got %v want %v", test.query, got, test.want)
		}
		for i := range got {
			if got[i] != test.want[i] {
				t.Fatalf("%s: got %v want %v", test.query, got, test.want)
			}
		}
	}
}
