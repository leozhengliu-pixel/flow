package main

import (
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Dates ▸ Canceled date reads canceledAt in the in-memory query walk like the SQL store, and a custom day or
// period arrives as an after/before pair (the web client's inclusive lower and exclusive upper bound).
func TestLegacyIssueQueryCanceledDate(t *testing.T) {
	early := time.Date(2026, 11, 4, 18, 0, 0, 0, time.UTC)
	onDay := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	issues := []domain.Issue{{ID: "a", CanceledAt: &early}, {ID: "b", CanceledAt: &onDay}, {ID: "c"}}
	for _, test := range []struct {
		query string
		want  []string
	}{
		{`{"field":"canceledAt","operator":"after","values":["2026-11-01T00:00:00Z"]}`, []string{"a", "b"}},
		{`{"field":"canceledAt","operator":"isEmpty"}`, []string{"c"}},
		{`{"and":[{"field":"canceledAt","operator":"after","values":["2026-11-04T23:59:59.999Z"]},{"field":"canceledAt","operator":"before","values":["2026-11-06T00:00:00.000Z"]}]}`, []string{"b"}},
	} {
		root, err := decodeIssueQuery(test.query)
		if err != nil {
			t.Fatal(err)
		}
		env := newIssueMatchEnv(domain.Bootstrap{}, root)
		got := []string{}
		for _, issue := range issues {
			if root.matchesIn(issue, env) {
				got = append(got, issue.ID)
			}
		}
		if !slices.Equal(got, test.want) {
			t.Fatalf("%s: got %v want %v", test.query, got, test.want)
		}
	}
}
