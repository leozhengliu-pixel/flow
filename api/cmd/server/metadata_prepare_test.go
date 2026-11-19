package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// `server metadata-prepare` takes a database whose workspaces predate the
// record journal (every one lacks writer coverage, as on the 2520-workspace
// copy) to a state a bounded start accepts: it repairs the journal, completes
// the catalog, reports ready and exits 0; run again it has nothing to repair.
func TestMetadataPrepareCommandReadiesBoundedStart(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	t.Setenv("FLOW_METADATA_CATALOG", "off")
	path := filepath.Join(t.TempDir(), "prepare.db")
	repo, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	user, token, err := repo.Register(t.Context(), "Prepare owner", idgen.New("prepare-")+"@example.test", "prepare-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	for i := range 4 {
		if _, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), user), fmt.Sprintf("Prepare %d", i), idgen.New("prepare-"), "us"); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_DATABASE_DRIVER", "sqlite")
	t.Setenv("FLOW_DATABASE_PATH", path)

	var stdout, stderr bytes.Buffer
	if code := metadataPrepare(nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "FLOW_RECORD_JOURNAL=on") {
		t.Fatalf("journal off: exit %d, stderr %s", code, stderr.String())
	}

	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	// The flag-on start refuses before the preparation.
	t.Setenv("FLOW_BOUNDED_METADATA", "on")
	if bounded, err := store.OpenSQLite(path); err == nil {
		_ = bounded.Close()
		t.Fatal("bounded start accepted workspaces without journal coverage")
	}
	// The command prepares flag-off even when the environment says on.
	for run := range 2 {
		stdout.Reset()
		stderr.Reset()
		if code := metadataPrepare(nil, &stdout, &stderr); code != 0 {
			t.Fatalf("run %d: exit %d, stderr %s, report %s", run, code, stderr.String(), stdout.String())
		}
		var report store.BoundedMetadataPreparation
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("report %q: %v", stdout.String(), err)
		}
		if !report.Ready || report.Catalog.Collections == 0 || len(report.Catalog.Skipped) != 0 {
			t.Fatalf("run %d report %+v", run, report)
		}
		if run == 0 && report.Journal.Repaired < 4 || run == 1 && report.Journal.Workspaces != 0 {
			t.Fatalf("run %d journal %+v", run, report.Journal)
		}
	}
	t.Setenv("FLOW_BOUNDED_METADATA", "on")
	bounded, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatalf("bounded start after metadata-prepare: %v", err)
	}
	if err := bounded.Close(); err != nil {
		t.Fatal(err)
	}
}
