package main

import (
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestProjectAndInitiativeTimestampSort(t *testing.T) {
	second := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	early := second.Add(371 * time.Millisecond)
	late := early.Add(600 * time.Microsecond)
	same := early.In(time.FixedZone("UTC-5", -5*60*60))
	data := domain.Bootstrap{
		Projects: []domain.Project{
			{ID: "late", CreatedAt: late},
			{ID: "same", CreatedAt: same},
			{ID: "early", CreatedAt: early},
			{ID: "whole", CreatedAt: second},
		},
		Initiatives: []domain.Initiative{
			{ID: "late", CreatedAt: late, UpdatedAt: late},
			{ID: "same", CreatedAt: same, UpdatedAt: same},
			{ID: "early", CreatedAt: early, UpdatedAt: early},
			{ID: "whole", CreatedAt: second, UpdatedAt: second},
		},
	}
	var latest = map[string]time.Time{"late": late, "same": same, "early": early, "whole": second}
	want := []string{"whole", "same", "early", "late"}
	for _, ordering := range []string{"Created", "Updated"} {
		for _, desc := range []bool{false, true} {
			projects := slices.Clone(data.Projects)
			q := scaleP19Query{Display: scaleP19Display{Ordering: ordering}}
			expected := slices.Clone(want)
			if desc {
				q.Display.OrderingDirection = "desc"
				slices.Reverse(expected)
			}
			scaleP19SortProjects(projects, q, data, nil, latest)
			var got []string
			for _, project := range projects {
				got = append(got, project.ID)
			}
			if !slices.Equal(got, expected) {
				t.Errorf("%s desc=%t: %v, want %v", ordering, desc, got, expected)
			}
		}
	}
	for _, sort := range []string{"created", "updated"} {
		page, err := scaleP19Initiatives(data, scaleP19Query{Sort: sort}, scaleP19WindowPage{}, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, item := range page.Items.([]domain.Initiative) {
			got = append(got, item.ID)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", sort, got, want)
		}
	}
}
