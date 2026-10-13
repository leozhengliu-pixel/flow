package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestIssueRecordsConnectionPagesBothWays(t *testing.T) {
	f := newPlatformFixture(t, nil)
	base := f.data.Issues[0]
	start := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	issues := make([]domain.Issue, 45)
	for i := range issues {
		issue := base
		issue.ID = fmt.Sprintf("conn-%03d", i)
		issue.Identifier = fmt.Sprintf("CONN-%d", i)
		issue.State.ID = "conn-state"
		issue.CreatedAt = start.Add(time.Duration(i/2) * time.Minute)
		issue.UpdatedAt = issue.CreatedAt
		issues[i] = issue
	}
	if err := f.repository.ImportIssues(context.Background(), f.data.Workspace.URLKey, issues); err != nil {
		t.Fatal(err)
	}
	token := f.apiKey(t, []string{"read"}, nil)
	filter := url.QueryEscape(`{"field":"status","values":["conn-state"]}`)
	get := func(query string) connection[domain.Issue] {
		t.Helper()
		_, raw := bearer(t, http.MethodGet, f.host.URL+"/api/issue-records?filter="+filter+"&"+query, token, nil, http.StatusOK)
		return decodeInto[connection[domain.Issue]](t, raw)
	}
	ids := func(page connection[domain.Issue]) []string {
		out := make([]string, len(page.Nodes))
		for i, issue := range page.Nodes {
			out[i] = issue.ID
		}
		return out
	}

	// Forward: newest first by default.
	var all []string
	page := get("first=10&includeTotal=true")
	if page.TotalCount == nil || *page.TotalCount != 45 || page.PageInfo.HasPreviousPage || !page.PageInfo.HasNextPage {
		t.Fatalf("first page = %#v", page.PageInfo)
	}
	pages := []connection[domain.Issue]{page}
	all = append(all, ids(page)...)
	for page.PageInfo.HasNextPage {
		page = get("first=10&after=" + url.QueryEscape(*page.PageInfo.EndCursor))
		pages = append(pages, page)
		all = append(all, ids(page)...)
	}
	if len(all) != 45 || len(pages) != 5 {
		t.Fatalf("forward walk: %d rows in %d pages", len(all), len(pages))
	}

	// Backward: last page first, then before=startCursor until the start.
	var back []string
	page = get("last=10")
	if page.PageInfo.HasNextPage || !page.PageInfo.HasPreviousPage || !slices.Equal(ids(page), all[35:]) {
		t.Fatalf("last page = %v %#v", ids(page), page.PageInfo)
	}
	back = append(ids(page), back...)
	for page.PageInfo.HasPreviousPage {
		page = get("last=10&before=" + url.QueryEscape(*page.PageInfo.StartCursor))
		if !page.PageInfo.HasNextPage {
			t.Fatalf("backward page forgot its successor: %#v", page.PageInfo)
		}
		back = append(ids(page), back...)
	}
	if !slices.Equal(back, all) {
		t.Fatalf("backward walk differs:\n%v\n%v", back, all)
	}
	// before alone pages backward with the default size, in the same order.
	page = get("before=" + url.QueryEscape(*pages[2].PageInfo.StartCursor) + "&last=10")
	if !slices.Equal(ids(page), ids(pages[1])) {
		t.Fatalf("page before page 3 = %v", ids(page))
	}

	// Explicit ordering, direction and the connection switch.
	asc := get("connection=1&orderBy=updatedAt&orderDirection=asc&first=3")
	desc := get("orderBy=updatedAt&first=3")
	if len(asc.Nodes) != 3 || len(desc.Nodes) != 3 || asc.Nodes[0].ID == desc.Nodes[0].ID {
		t.Fatalf("directions: %v %v", ids(asc), ids(desc))
	}

	// Argument errors use the error envelope; page sizes are capped at 100.
	for _, query := range []string{"first=101", "last=0", "first=2&last=2", "after=x&before=y", "last=2&after=x", "first=2&before=x", "orderBy=nope", "first=1&orderDirection=sideways", "first=1&archived=maybe", "after=garbage"} {
		_, raw := bearer(t, http.MethodGet, f.host.URL+"/api/issue-records?"+query, token, nil, http.StatusBadRequest)
		if envelope := decodeInto[map[string]any](t, raw); envelope["type"] != apiErrorInvalidInput {
			t.Fatalf("%s: %s", query, raw)
		}
	}
	// A cursor issued for one filter is refused for another.
	_, raw := bearer(t, http.MethodGet, f.host.URL+"/api/issue-records?last=5&before="+url.QueryEscape(*pages[1].PageInfo.StartCursor), token, nil, http.StatusBadRequest)
	_ = raw

	// Without connection arguments the legacy page shape is unchanged.
	_, raw = bearer(t, http.MethodGet, f.host.URL+"/api/issue-records?limit=5&filter="+filter, token, nil, http.StatusOK)
	legacy := decodeInto[map[string]any](t, raw)
	if _, ok := legacy["items"]; !ok || legacy["nodes"] != nil || legacy["startCursor"] != nil || legacy["hasMore"] != true {
		t.Fatalf("legacy page changed: %s", raw)
	}
}
