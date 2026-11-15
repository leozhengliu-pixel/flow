package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21SW151BootstrapDerivedLiteralMarker(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	literal := "Keep \x00flow-p21-bootstrap-body:" + strings.Repeat("a", 32) + ":7 as ordinary text"
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21SW145Seed(t, repo)
		restore := store.ForceFullMutationsForTesting(true)
		err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw151_seed", "", nil, func(data *domain.Bootstrap) error {
			for index := range data.Loops {
				if data.Loops[index].ID == f.loop {
					data.Loops[index].Instructions = literal
					data.Loops[index].Description = ""
					data.Loops[index].InstructionsData = nil
				}
			}
			return nil
		})
		restore()
		if err != nil {
			t.Fatal(err)
		}
		view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		for _, signed := range []bool{false, true} {
			test := scaleP21SW145Case{
				workspace: f.data.Workspace.URLKey, development: true,
				status: http.StatusOK, spool: 4 << 20, signed: signed,
			}
			old, _ := scaleP21SW145Serve(t, f, repo, test)
			bounded, _ := scaleP21SW145Serve(t, f, view, test)
			if old.status != http.StatusOK || bounded.status != old.status ||
				!reflect.DeepEqual(old.header, bounded.header) || !bytes.Equal(old.body, bounded.body) {
				t.Fatal("literal instruction-derived marker changed the registered response", old.status, bounded.status)
			}
			var data domain.Bootstrap
			if err := json.Unmarshal(old.body, &data); err != nil {
				t.Fatal(err)
			}
			witness := false
			for _, loop := range data.Loops {
				if loop.ID == f.loop && strings.Contains(loop.Description, literal) {
					witness = true
				}
			}
			if !witness {
				t.Fatal("legacy loop presenter did not derive the literal marker witness")
			}
		}
	}
}
