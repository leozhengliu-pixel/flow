package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestMCPSaveViewAdvancedFiltersKeepTheTree(t *testing.T) {
	f := newMCPContractFixture(t)
	created := mcpObject(t, f, "save_view", map[string]any{
		"name": "Advanced", "team": "TST",
		"filters": []map[string]any{{"field": "labels", "operator": "includesAll", "values": []string{"Defect"}}},
		"advancedFilters": []map[string]any{{"conjunction": "or", "items": []map[string]any{
			{"field": "priority", "values": []string{"Urgent"}},
			{"conjunction": "and", "items": []map[string]any{{"field": "status", "values": []string{"started"}}}},
		}}},
	})
	stored := func() []map[string]any {
		for _, item := range f.repository.Bootstrap().SavedViews {
			if item.ID == created["id"] {
				var filters []map[string]any
				if err := json.Unmarshal(item.Filters, &filters); err != nil {
					t.Fatal(err)
				}
				return filters
			}
		}
		t.Fatal("view not stored")
		return nil
	}
	filters := stored()
	if len(filters) != 2 || filters[0]["operator"] != "includesAll" || filters[1]["field"] != "advanced" {
		t.Fatalf("filters: %v", filters)
	}
	tree := filters[1]["tree"].(map[string]any)
	items := tree["items"].([]any)
	if tree["conjunction"] != "or" || len(items) != 2 || items[0].(map[string]any)["values"].([]any)[0].(map[string]any)["value"] != "1" || items[1].(map[string]any)["conjunction"] != "and" {
		t.Fatalf("tree: %v", tree)
	}
	// Replacing only the plain filters keeps the advanced chip.
	mcpObject(t, f, "save_view", map[string]any{"id": created["id"], "filters": []map[string]any{}})
	if filters = stored(); len(filters) != 1 || filters[0]["field"] != "advanced" {
		t.Fatalf("advanced chip was not preserved: %v", filters)
	}
	if message := mcpToolError(t, f, "save_view", map[string]any{"id": created["id"], "advancedFilters": []map[string]any{{"items": []map[string]any{{"items": []map[string]any{{"items": []map[string]any{{"items": []map[string]any{{"field": "priority", "values": []string{"Urgent"}}}}}}}}}}}}); !strings.Contains(message, "three levels") {
		t.Fatalf("depth error: %s", message)
	}
	if message := mcpToolError(t, f, "save_view", map[string]any{"id": created["id"], "filters": []map[string]any{{"field": "priority", "operator": "includesAll", "values": []string{"Urgent"}}}}); !strings.Contains(message, "labels") {
		t.Fatalf("operator error: %s", message)
	}
}

func TestValidSavedViewFiltersBoundsTrees(t *testing.T) {
	for _, raw := range []string{`[]`, `null`, `{"match":"all"}`, `[{"field":"status","values":["s"]}]`, `[{"field":"advanced","tree":{"conjunction":"and","items":[{"field":"status"},{"conjunction":"or","items":[{"conjunction":"and","items":[{"field":"priority"}]}]}]}}]`} {
		if !validSavedViewFilters(json.RawMessage(raw)) {
			t.Fatalf("rejected %s", raw)
		}
	}
	for _, raw := range []string{
		`[{"field":"advanced","tree":{"items":[{"items":[{"items":[{"items":[{"field":"status"}]}]}]}]}}]`,
		`[{"field":"advanced","tree":{"conjunction":"xor","items":[]}}]`,
		`[{"field":"advanced","tree":{"items":[{"operator":"is"}]}}]`,
	} {
		if validSavedViewFilters(json.RawMessage(raw)) {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestLegacyIssueQueryIncludesAllLabels(t *testing.T) {
	both := domain.Issue{ID: "a", Labels: []domain.IssueLabel{{ID: "l1"}, {ID: "l2"}}, CreatedAt: time.Now()}
	one := domain.Issue{ID: "b", Labels: []domain.IssueLabel{{ID: "l1"}}, CreatedAt: time.Now()}
	none := domain.Issue{ID: "c", CreatedAt: time.Now()}
	root, err := decodeIssueQuery(`{"or":[{"field":"labels","operator":"includesAll","values":["l1","l2"]},{"not":{"field":"labels","operator":"excludesAll","values":[""]}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	for issue, want := range map[*domain.Issue]bool{&both: true, &one: false, &none: true} {
		if got := root.matches(*issue, domain.Bootstrap{}); got != want {
			t.Fatalf("%s: got %v want %v", issue.ID, got, want)
		}
	}
}
