//go:build loadtest

package store

import (
	"os"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21JournalSurvivesLoadProbePoolReplacement(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	probe, err := f.EnableScaleP01Probe(DatabaseConfig{Driver: os.Getenv("FLOW_TEST_DATABASE_DRIVER"), URL: os.Getenv("FLOW_TEST_DATABASE_URL")})
	if err != nil {
		t.Fatal(err)
	}
	workspace, projectID := f.data.Workspace.URLKey, "p21-probe-project"
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.probe", projectID, nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: projectID, Name: "Instrumented journal", TeamIDs: []string{data.Teams[0].ID}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	version, deleted, err := f.RecordVersion(t.Context(), workspace, RecordKey{"projects", projectID})
	if err != nil || version == "" || version == "0" || deleted {
		t.Fatalf("instrumented commit lost its journal version: %q/%v/%v", version, deleted, err)
	}
	if measured := probe.SQLSnapshot(); measured.Writes == 0 || measured.Commits == 0 || measured.Errors != 0 {
		t.Fatalf("instrumented commit was not measured correctly: %+v", measured)
	}
}
