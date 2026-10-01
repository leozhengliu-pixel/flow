package store

import (
	"testing"
	"time"
)

// A bound on one source's payload scan must never narrow another source's.
func TestMutationScopeMergeKeepsWidestDataScanBound(t *testing.T) {
	early, late := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		base, extra MutationScope
		want        time.Time
	}{
		{"bound only on extra", MutationScope{}, MutationScope{IssueDataContains: []string{"b"}, IssueDataUpdatedSince: late}, late},
		{"unbounded base wins", MutationScope{IssueDataContains: []string{"a"}}, MutationScope{IssueDataContains: []string{"b"}, IssueDataUpdatedSince: late}, time.Time{}},
		{"unbounded extra wins", MutationScope{IssueDataContains: []string{"a"}, IssueDataUpdatedSince: late}, MutationScope{IssueDataContains: []string{"b"}}, time.Time{}},
		{"earliest bound", MutationScope{IssueDataContains: []string{"a"}, IssueDataUpdatedSince: late}, MutationScope{IssueDataContains: []string{"b"}, IssueDataUpdatedSince: early}, early},
		{"extra without scans keeps base", MutationScope{IssueDataContains: []string{"a"}, IssueDataUpdatedSince: late}, MutationScope{IssueIDs: []string{"x"}}, late},
	}
	for _, test := range cases {
		if got := test.base.merge(test.extra).IssueDataUpdatedSince; !got.Equal(test.want) {
			t.Errorf("%s: bound = %v, want %v", test.name, got, test.want)
		}
	}
}
