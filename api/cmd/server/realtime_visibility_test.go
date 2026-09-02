package main

import (
	"testing"

	"flow/api/internal/domain"
)

func TestRealtimeVisibilityFiltersHiddenResources(t *testing.T) {
	data := domain.Bootstrap{
		Issues:    []domain.Issue{{ID: "visible"}},
		Documents: []domain.Document{{ID: "doc-visible"}},
	}
	values := []domain.Presence{
		{ClientID: "a", IssueID: "visible"},
		{ClientID: "b", IssueID: "hidden"},
		{ClientID: "c", DocumentID: "doc-visible"},
		{ClientID: "d", DocumentID: "doc-hidden"},
	}
	filtered := filterPresenceForViewer(data, values)
	if len(filtered) != 2 || filtered[0].ClientID != "a" || filtered[1].ClientID != "c" {
		t.Fatalf("filtered presence=%#v", filtered)
	}
	if !realtimeEventVisible(data, domain.RealtimeEvent{Type: "issue.updated", AggregateID: "visible"}) {
		t.Fatal("visible issue event was filtered")
	}
	if realtimeEventVisible(data, domain.RealtimeEvent{Type: "issue.updated", AggregateID: "hidden"}) {
		t.Fatal("hidden issue event leaked")
	}
}
