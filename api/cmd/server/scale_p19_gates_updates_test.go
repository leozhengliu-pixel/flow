package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP19GatesUpdateBindingIgnoresUnrelatedHead(t *testing.T) {
	values := []scaleP19UpdateRecord{{id: "update", value: domain.ProjectUpdate{ID: "update", Body: "Authorized unchanged body"}}}
	before, err := scaleP19UpdateBinding("projects", "project", "100", values)
	if err != nil {
		t.Fatal(err)
	}
	after, err := scaleP19UpdateBinding("projects", "project", "101", values)
	if err != nil || before != after {
		t.Fatalf("an unrelated event expired the projected history: %q != %q: %v", before, after, err)
	}
	changed, err := scaleP19UpdateBinding("projects", "project", "101", []scaleP19UpdateRecord{{id: "update", value: domain.ProjectUpdate{ID: "update", Body: "Changed body"}}})
	if err != nil || changed == after {
		t.Fatal("a changed projected history retained its continuation", err)
	}
}

func TestScaleP19GatesUpdateContinuationAcrossPulseSchedule(t *testing.T) {
	scaleP19Options(t)
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			id := "p19-gates-updates"
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p19.seeded", "", nil, func(data *domain.Bootstrap) error {
				data.Projects = append(data.Projects, domain.Project{ID: id, Name: "Updates", TeamIDs: []string{data.Teams[0].ID}, Status: data.ProjectStatuses[0]})
				for index := range 25 {
					data.ProjectUpdates[id] = append(data.ProjectUpdates[id], domain.ProjectUpdate{ID: fmt.Sprintf("p19-gates-%02d", index), ProjectID: id, Body: fmt.Sprintf("Update %02d", index), User: data.Viewer, Health: "onTrack", CreatedAt: time.Now().UTC().Add(-time.Duration(index) * time.Hour)})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			path := "/api/projects/" + id + "/updates?window=1&limit=20"
			first := scaleP03Read(t, f, f.admin, "", http.MethodGet, path, workspace)
			if first.Code != http.StatusOK {
				t.Fatalf("initial history: %d %s", first.Code, first.Body)
			}
			var page scaleP19UpdatePage
			if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil || !page.HasMore {
				t.Fatalf("initial page %+v: %v", page, err)
			}
			ctx := store.WithMetadataFields(t.Context(), "settings")
			if err := repo.MutateWorkspace(ctx, workspace, "pulse.summary_scheduled", "", nil, func(data *domain.Bootstrap) error {
				if data.Settings == nil {
					data.Settings = map[string]any{}
				}
				data.Settings["pulseDeliveryCursors"] = map[string]string{f.data.Viewer.ID: "2026-11-13T00:00:00Z"}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			next := scaleP03Read(t, f, f.admin, "", http.MethodGet, path+"&cursor="+url.QueryEscape(page.NextCursor), workspace)
			if next.Code != http.StatusOK {
				t.Fatalf("unrelated scheduling expired an authorized continuation: %d %s", next.Code, next.Body)
			}
			var final scaleP19UpdatePage
			if err := json.Unmarshal(next.Body.Bytes(), &final); err != nil || final.Offset != 20 || len(final.Items) != 5 || final.Total != 25 || final.HasMore || final.AuthzEpoch != page.AuthzEpoch {
				t.Fatalf("continuation %+v: %v", final, err)
			}
		})
	}
}
