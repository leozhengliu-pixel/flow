package store

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"flow/api/internal/domain"
)

// An append writer (MutateWorkspaceAppending) adding to a collection
// the stored root keeps inline (repeated or empty IDs, so no per-record rows)
// persists exactly what the literal writer persists: entries, root, rows and
// the event. It must not refuse with ErrMetadataFieldInline.
func TestAppendToInlineCollection(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	type outcome struct {
		Err      string
		Physical scaleIssuePhysicalField
		Native   any
		Event    []string
	}
	for _, shape := range []string{"duplicate", "empty-id", "rows"} {
		t.Run(shape, func(t *testing.T) {
			var outcomes []outcome
			for _, bounded := range []bool{false, true} {
				f := journalScaleDatabase(t)
				workspace := f.data.Workspace.URLKey
				labels := []domain.IssueLabel{{ID: "sw541-a", Name: "First"}, {ID: "sw541-b", Name: "Second"}}
				switch shape {
				case "duplicate":
					labels = append(labels, domain.IssueLabel{ID: "sw541-a", Name: "Repeated"})
				case "empty-id":
					labels = append(labels, domain.IssueLabel{Name: "No ID"})
				}
				if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw541_seed", "", nil, func(data *domain.Bootstrap) error {
					// Only the fixture's labels: the seeded defaults carry
					// generated IDs that differ per workspace.
					data.Labels = labels
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				inline, err := metadataFieldStoredInline(t.Context(), f.db, f.dialect, workspace, "labels")
				if err != nil || inline != (shape != "rows") {
					t.Fatal("fixture labels shape", inline, err)
				}
				repo := scaleIssuePersistenceView(t, f, bounded, false)
				created := domain.IssueLabel{ID: "sw541-new", Name: "Appended"}
				err = repo.MutateWorkspaceAppending(t.Context(), workspace, "label.created", created.ID, map[string]any{"id": created.ID, "name": created.Name},
					MetadataAppendScope{AppendKinds: []string{"labels"}}, func(data *domain.Bootstrap) error {
						data.Labels = append(data.Labels, created)
						return nil
					})
				result := outcome{Physical: scaleIssuePhysical(t, repo, workspace, "labels"), Native: scaleIssueNative(t, repo, workspace, "labels")}
				if err != nil {
					result.Err = err.Error()
				}
				var kind, aggregate string
				var payload, previous []byte
				if err := repo.db.QueryRowContext(t.Context(), `SELECT event_type,aggregate_id,payload,previous_values FROM domain_events WHERE workspace_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, f.data.Workspace.ID).Scan(&kind, &aggregate, &payload, &previous); err != nil {
					t.Fatal(err)
				}
				result.Event = []string{kind, aggregate, string(payload), string(previous)}
				if bounded && (repo.WorkspaceMetadataDecodes() != 0 || repo.MetadataWorkUsedForTesting() != 0) {
					t.Fatal("append decoded complete metadata or leaked work")
				}
				outcomes = append(outcomes, result)
			}
			if outcomes[0].Err != "" {
				t.Fatal("literal append failed", outcomes[0].Err)
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				literal, _ := json.Marshal(outcomes[0])
				bounded, _ := json.Marshal(outcomes[1])
				t.Fatal(fmt.Sprintf("append differs\nliteral=%s\nbounded=%s", literal, bounded))
			}
			native := outcomes[1].Native.([]domain.IssueLabel)
			if len(native) == 0 || native[len(native)-1].ID != "sw541-new" {
				t.Fatal("appended label missing", native)
			}
		})
	}
}
