package store

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21M1WorkspaceColorDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: "m1-unrelated", Description: strings.Repeat("unrelated ", 40000)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	row := func() []byte {
		t.Helper()
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key='m1-unrelated'`, workspace).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	unrelated := row()
	for _, color := range []string{legacyWorkspaceColor, "  #5e6ad2  ", "#FF0000", ""} {
		var identities []domain.Workspace
		var events [][]domain.DomainEvent
		var realtime [][]domain.RealtimeEvent
		for _, bounded := range []bool{false, true} {
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "workspace"), workspace, "scale_p21.reset", "", nil, func(data *domain.Bootstrap) error {
				data.Workspace.Color = color
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			data, _ := f.WorkspaceMetadata(workspace)
			// Startup's migration enumerates the store's catalog. These views
			// expose only the fixture's owned workspace on persistent databases.
			source := &SQLiteStore{db: f.db, dialect: f.dialect, maxStateBytes: f.maxStateBytes, viewer: f.viewer, lastWorkspaceKey: workspace, workspaces: map[string]domain.Bootstrap{workspace: data}}
			if bounded {
				var err error
				source, err = f.BoundedMetadataViewForTesting(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				source.workspaces = map[string]domain.Bootstrap{workspace: source.workspaces[workspace]}
			}
			var emitted []domain.DomainEvent
			var delivered []domain.RealtimeEvent
			source.SetWebhookSink(func(_ string, event domain.DomainEvent) { emitted = append(emitted, event) })
			source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { delivered = append(delivered, event) })
			for range 2 {
				if err := source.migrateWorkspaceInitialsColor(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			for i := range emitted {
				emitted[i].ID, emitted[i].CreatedAt = "", time.Time{}
			}
			for i := range delivered {
				delivered[i].ID, delivered[i].Seq, delivered[i].CreatedAt = "", 0, time.Time{}
			}
			result, ok := source.WorkspaceSettingsMetadata(workspace)
			if !ok {
				t.Fatal("migration lost workspace identity")
			}
			identities = append(identities, result.Workspace)
			events, realtime = append(events, emitted), append(realtime, delivered)
			if bounded && (source.WorkspaceMetadataDecodes() != 0 || source.metadataResidency.work.used != 0) {
				t.Fatal("color migration decoded complete metadata or retained admission")
			}
		}
		if !reflect.DeepEqual(identities[0], identities[1]) || !reflect.DeepEqual(events[0], events[1]) || !reflect.DeepEqual(realtime[0], realtime[1]) || !bytes.Equal(unrelated, row()) {
			t.Fatalf("color migration differs for %q: identity=%#v events=%#v realtime=%#v", color, identities, events, realtime)
		}
		want := 0
		if strings.EqualFold(strings.TrimSpace(color), legacyWorkspaceColor) {
			want = 1
		}
		if len(events[1]) != want {
			t.Fatalf("color migration/idempotence: events=%d want=%d", len(events[1]), want)
		}
	}
}
