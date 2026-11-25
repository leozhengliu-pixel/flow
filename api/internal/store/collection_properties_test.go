package store

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW0CollectionStampAndProperties(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	set := func(value bool) {
		t.Helper()
		if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.collection_properties", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.ReviewThirdPartyApplications = value
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	set(true)
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	wantStamp, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil {
		t.Fatal(err)
	}
	var root []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&root); err != nil {
		t.Fatal(err)
	}
	f.dropMetadataCache(t.Context(), workspace)
	want, err := f.decodeWorkspaceMetadata(t.Context(), workspace, root, f.db)
	if err != nil {
		t.Fatal(err)
	}
	read, err := probe.BeginMetadataCollectionRead(t.Context(), workspace, "workspaceSettings")
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	if read.stamp != wantStamp {
		t.Fatal("captured policy stamp did not belong to the source snapshot", read.stamp, wantStamp)
	}
	set(false)
	current, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil || current == read.stamp {
		t.Fatal("fixture did not change the canonical revision", err)
	}
	var got domain.WorkspaceSettings
	if err := read.VisitMap("workspaceSettings", func(_ context.Context, item MetadataMapOccurrence) error {
		// A struct's null is a no-op, unlike a map's null reset.
		if item.Reset || item.BeginObject {
			return nil
		}
		key := item.Key
		if !item.CanonicalKey {
			if err := json.NewDecoder(item.RawKey).Decode(&key); err != nil {
				return err
			}
		}
		raw, err := io.ReadAll(item.Reader)
		if err != nil {
			return err
		}
		property, err := json.Marshal(map[string]json.RawMessage{key: raw})
		if err != nil {
			return err
		}
		return json.Unmarshal(property, &got)
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want.WorkspaceSettings) || !got.ReviewThirdPartyApplications {
		t.Fatal("detached property source changed with later canonical writes")
	}
}

func TestScaleP21SW0CollectionPostgresNULWorkspace(t *testing.T) {
	// No SQL connection is needed: an impossible PostgreSQL text key must
	// not be cleaned into the name of a different, existing workspace.
	s := &SQLiteStore{dialect: "postgres", metadataResidency: newMetadataResidency()}
	read, err := s.BeginMetadataCollectionRead(t.Context(), "workspace\x00", "users")
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	shape, err := read.Shape("users")
	if err != nil || shape.WorkspaceFound {
		t.Fatal("NUL workspace aliased another canonical workspace", err)
	}
	if err := read.VisitArray("users", func(context.Context, MetadataArrayRecord) error {
		t.Fatal("missing workspace emitted a record")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
