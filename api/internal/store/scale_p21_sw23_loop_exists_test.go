package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW23LoopRecordExists(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	loops := []domain.Loop{{ID: "sw23-target", SlugID: "sw23-old-slug", Description: strings.Repeat("legal large loop ", 22000)}}
	for i := range 80 {
		loops = append(loops, domain.Loop{ID: fmt.Sprintf("sw23-retained-%d", i), Name: "Retained"})
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "loops"), workspace, "scale_p21_sw23.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Loops = loops
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read := func() []byte {
		t.Helper()
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='loops' AND record_key='sw23-target'`, workspace).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := read()
	if len(before) <= MetadataRecordPageMaxBytes {
		t.Fatal("target must exceed the ordinary record budget")
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		id   string
		want bool
	}{
		{"sw23-target", true}, {"SW23-target", false}, {"sw23-old-slug", false},
		{"sw23-missing", false}, {"", false}, {"sw23-target\x00", false},
		{strings.Repeat("x", 192), false}, {"sw23-target' OR 1=1 --", false},
	} {
		got, err := view.LoopRecordExists(t.Context(), workspace, test.id)
		if err != nil || got != test.want {
			t.Fatalf("loop %q exists=%v err=%v want=%v", test.id, got, err, test.want)
		}
	}
	if got, err := view.LoopRecordExists(t.Context(), "", "sw23-target"); err != nil || !got {
		t.Fatalf("default workspace exists=%v err=%v", got, err)
	}
	if got, err := view.LoopRecordExists(t.Context(), workspace+"-missing", "sw23-target"); got || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing workspace exists=%v err=%v", got, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := view.LoopRecordExists(cancelled, workspace, "sw23-target"); got || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled lookup exists=%v err=%v", got, err)
	}
	if got, err := view.LoopRecordExists(t.Context(), workspace, "sw23-target"); err != nil || !got {
		t.Fatalf("lookup after cancellation exists=%v err=%v", got, err)
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 || !bytes.Equal(before, read()) {
		t.Fatal("existence lookup decoded full metadata, leaked work or changed its row")
	}
	// Legacy duplicate/long IDs keep the whole field inline instead of
	// generating canonical rows. The predicate must retain exact ID behavior.
	inline := []domain.Loop{{ID: "sw23-inline"}, {ID: "sw23-inline"}, {ID: strings.Repeat("x", 192)}}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "loops"), workspace, "scale_p21_sw23.inline", "", nil, func(data *domain.Bootstrap) error {
		data.Loops = inline
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		id   string
		want bool
	}{
		{"sw23-inline", true}, {"SW23-inline", false}, {strings.Repeat("x", 192), true},
		{"sw23-inline\x00", false}, {"sw23-target", false}, {"sw23-missing", false},
	} {
		got, err := view.LoopRecordExists(t.Context(), workspace, test.id)
		if err != nil || got != test.want {
			t.Fatalf("inline loop %q exists=%v err=%v want=%v", test.id, got, err, test.want)
		}
	}
}
