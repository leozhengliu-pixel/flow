package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func BenchmarkLimitedWorkspaceSearch(b *testing.B) {
	data := domain.Bootstrap{Issues: make([]domain.Issue, 50_000)}
	for index := range data.Issues {
		data.Issues[index] = domain.Issue{
			ID: fmt.Sprintf("issue_%d", index), Identifier: fmt.Sprintf("PERF-%d", index),
			Title: fmt.Sprintf("Performance audit issue %d", index),
			Team:  domain.Team{ID: "team", Name: "Performance"}, UpdatedAt: time.Unix(int64(index), 0),
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		result := buildSearchResultsLimited(data, "audit", map[string]bool{"issue": true}, 30)
		if len(result) != 30 {
			b.Fatalf("result count = %d", len(result))
		}
	}
}

func BenchmarkDashboardIssueFilter(b *testing.B) {
	data := domain.Bootstrap{Issues: make([]domain.Issue, 50_000)}
	for index := range data.Issues {
		data.Issues[index] = domain.Issue{
			ID: fmt.Sprintf("issue_%d", index), Team: domain.Team{ID: fmt.Sprintf("team_%d", index%5)},
			State: domain.WorkflowState{ID: fmt.Sprintf("state_%d", index%4)},
		}
	}
	config, _ := json.Marshal(map[string]any{"teamIds": []string{"team_1", "team_3"}, "stateIds": []string{"state_2"}})
	widget := domain.DashboardWidget{Config: config}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = widgetIssueFilter(data, widget)
	}
}

func TestWriteArrayPageBoundsAndCursor(t *testing.T) {
	values := make([]int, 140)
	request := httptest.NewRequest("GET", "/jobs?limit=1000", nil)
	recorder := httptest.NewRecorder()
	writeArrayPage(recorder, request, values)
	var first []int
	if err := json.Unmarshal(recorder.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first) != 50 || recorder.Header().Get("X-Next-Cursor") == "" || recorder.Header().Get("X-Total-Count") != "140" {
		t.Fatalf("page headers=%v length=%d", recorder.Header(), len(first))
	}
	secondRequest := httptest.NewRequest("GET", "/jobs?limit=100&cursor="+recorder.Header().Get("X-Next-Cursor"), nil)
	secondRecorder := httptest.NewRecorder()
	writeArrayPage(secondRecorder, secondRequest, values)
	var second []int
	_ = json.Unmarshal(secondRecorder.Body.Bytes(), &second)
	if len(second) != 90 {
		t.Fatalf("second page length=%d", len(second))
	}
}
