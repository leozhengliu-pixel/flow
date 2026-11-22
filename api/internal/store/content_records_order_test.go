package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestContentRecordGroupedOrderDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	ctx := t.Context()
	type content struct {
		ID string `json:"id"`
	}
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, kind := range []string{"activity", "comment"} {
		t.Run(kind, func(t *testing.T) {
			insert := func(resource, id string, hours int, raw []byte) {
				t.Helper()
				if _, err := f.db.ExecContext(ctx, `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES(?,?,?,?,?,?)`,
					workspace, kind, resource, id, stamp.Add(time.Duration(hours)*time.Hour).Format(time.RFC3339), raw); err != nil {
					t.Fatal(err)
				}
			}
			for _, row := range []struct {
				resource, id string
				hours        int
			}{
				{"Resource", "z", 2},
				{"Resource", "first", 1},
				{"Resource", "a", 2},
				{"r\u00e9source", "b", 2},
				{"other", "a", 2},
			} {
				raw, err := json.Marshal(content{row.id})
				if err != nil {
					t.Fatal(err)
				}
				insert(row.resource, row.id, row.hours, raw)
			}
			legacy, err := readContentRecordsWithBase[content](ctx, f.SQLiteStore, workspace, kind, newRecordBase())
			if err != nil {
				t.Fatal(err)
			}
			got, err := readContentRecords[content](ctx, f.SQLiteStore, workspace, kind, nil)
			if err != nil || !reflect.DeepEqual(got, legacy) {
				t.Fatalf("grouped %s = %v, %v; legacy = %v", kind, got, err, legacy)
			}
			if want := []content{{"first"}, {"a"}, {"z"}}; !reflect.DeepEqual(got["Resource"], want) || !reflect.DeepEqual(got["r\u00e9source"], []content{{"b"}}) {
				t.Fatalf("group order or SQL collation changed resource slices: %v", got)
			}
			insert("unrelated", "malformed", 0, []byte(`{`))
			defer func() {
				if _, err := f.db.ExecContext(ctx, `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind=? AND resource_id='unrelated' AND id='malformed'`, workspace, kind); err != nil {
					t.Error(err)
				}
			}()
			got, err = readContentRecords[content](ctx, f.SQLiteStore, workspace, kind, nil)
			var syntax *json.SyntaxError
			if got != nil || !errors.As(err, &syntax) {
				t.Fatalf("malformed %s returned %v, %v", kind, got, err)
			}
		})
	}
}
