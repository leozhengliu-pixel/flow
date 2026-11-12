package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21BoundedActivationFailsBeforeOpeningDatabase(t *testing.T) {
	for _, flag := range []string{"on", "true", "1"} {
		t.Run(flag, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", flag)
			path := filepath.Join(t.TempDir(), "must-not-create.db")
			repo, err := OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: path})
			if repo != nil || !errors.Is(err, ErrBoundedMetadataCoverage) {
				t.Fatalf("incomplete coverage enabled bounded residency: %v", err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("activation failure modified database: %v", err)
			}
		})
	}
}

func scaleP21ResidentProbe(repo *SQLiteStore) *SQLiteStore {
	probe := &SQLiteStore{db: repo.db, dialect: repo.dialect, maxStateBytes: repo.maxStateBytes, workspaces: map[string]domain.Bootstrap{}, metadataResidency: newMetadataResidency()}
	// Exercise the storage primitive independently of the production gate.
	// Existing business consumers cannot run against this partial snapshot.
	probe.metadataResidency.requireCoverage = false
	return probe
}

func TestScaleP21ResidentRecoveryKeepsPolicyAndEvictsRecords(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.seed", "", nil, func(data *domain.Bootstrap) error {
		for i := 0; i < 300; i++ {
			id := fmt.Sprintf("p21-policy-%04d", i)
			data.Teams = append(data.Teams, domain.Team{ID: id, Key: id, Private: i%3 == 0})
			data.TeamSettings[id] = domain.TeamSettings{TeamID: id, Access: "private", ParentTeamID: data.Teams[0].ID}
		}
		data.Projects = append(data.Projects, domain.Project{ID: "p21-large-project", Description: strings.Repeat("body", 200000)})
		data.Projects = append(data.Projects, domain.Project{ID: "p21-small-project", Description: "owned small detail"})
		data.Trash = append(data.Trash, domain.TrashEntry{ID: "p21-large-trash", Payload: []byte(`{"body":"` + strings.Repeat("x", 800000) + `"}`)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	legacy, ok := f.WorkspaceMetadata(workspace)
	if !ok {
		t.Fatal("owned workspace missing")
	}
	probe := scaleP21ResidentProbe(f.SQLiteStore)
	probe.metadataResidency.cacheLimit = 2048
	if err := probe.loadResidentWorkspaces(t.Context()); err != nil {
		t.Fatal(err)
	}
	resident, ok := probe.storedWorkspace(workspace)
	if !ok {
		t.Fatal("resident workspace missing")
	}
	if !reflect.DeepEqual(resourcePolicyMetadata(legacy), resourcePolicyMetadata(resident)) {
		t.Fatal("resident policy changed private or inherited team semantics")
	}
	if len(resident.Projects) != 0 || len(resident.Documents) != 0 || len(resident.Trash) != 0 || len(resident.Users) != 0 {
		t.Fatal("recovery retained record collections")
	}
	if probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("resident recovery decoded full workspace")
	}
	if probe.metadataResidency.cacheBytes > probe.metadataResidency.cacheLimit {
		t.Fatal("record cache exceeded byte budget")
	}
	changedTeam := resident.Teams[1].ID
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams", "teamSettings"), workspace, "team.updated", changedTeam, map[string]string{"name": "P21 policy change"}, func(data *domain.Bootstrap) error {
		for i := range data.Teams {
			if data.Teams[i].ID == changedTeam {
				data.Teams[i].Private = !data.Teams[i].Private
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := probe.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	updated, _ := probe.storedWorkspace(workspace)
	if updated.Teams[1].Private == resident.Teams[1].Private {
		t.Fatal("record cache reused a stale policy revision or changed an earlier snapshot")
	}
	if err := probe.ReloadAllWorkspaces(t.Context()); err != nil {
		t.Fatal(err)
	}
	if probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("reload decoded full workspace")
	}
	// Scoped snapshots cannot mistake the resident policy for a complete copy.
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	small, err := probe.HydrateWorkspaceMetadata(t.Context(), workspace, MetadataRecordScope{Read: []RecordKey{{"projects", "p21-small-project"}}})
	if err != nil || len(small.Data.Projects) != 1 || small.Data.Projects[0].Description != "owned small detail" {
		t.Fatalf("bounded detail did not read its canonical record: %v", err)
	}
	hydrated, err := probe.HydrateWorkspaceMetadata(t.Context(), workspace, MetadataRecordScope{Read: []RecordKey{{"projects", "p21-large-project"}}})
	if !errors.Is(err, ErrMetadataPageBudget) || len(hydrated.Data.Projects) != 0 {
		t.Fatalf("oversized detail bypassed record budget: %v", err)
	}
	if probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("scoped canonical detail took the full decoder path")
	}
	if _, _, err := probe.BeginWorkspaceRead(t.Context(), workspace); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatalf("unregistered read silently used partial residency: %v", err)
	}
	called := false
	if err := probe.mutateFull(t.Context(), workspace, "unregistered.new_handler", nil, func(*domain.Bootstrap) (string, error) { called = true; return "", nil }); !errors.Is(err, ErrBoundedMetadataCoverage) || called {
		t.Fatalf("unregistered full mutation ran: called=%v error=%v", called, err)
	}
}

func TestScaleP21ResidentRecoveryRejectsUnknownFieldsAndPreservesInstalledCopy(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	probe := scaleP21ResidentProbe(f.SQLiteStore)
	workspace := f.data.Workspace.URLKey
	if err := probe.loadResidentWorkspaces(t.Context()); err != nil {
		t.Fatal(err)
	}
	before, _ := probe.storedWorkspace(workspace)
	// Corrupt only the owned workspace's root; the failed recovery must not
	// publish any of the newly read workspaces or ledger reservations.
	var raw []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = f.db.ExecContext(context.Background(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, raw, workspace)
	}()
	unknown := append([]byte(`{"newUnregisteredCollection":[],`), raw[1:]...)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, unknown, workspace); err != nil {
		t.Fatal(err)
	}
	if err := probe.ReloadAllWorkspaces(t.Context()); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatalf("unknown field was silently ignored: %v", err)
	}
	after, _ := probe.storedWorkspace(workspace)
	if !reflect.DeepEqual(before, after) || probe.metadataResidency.work.used != 0 {
		t.Fatal("failed recovery changed installed state or leaked work lease")
	}
}

func TestScaleP21PolicyBudgetIsSharedAcrossWorkspaces(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	other, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "P21 additional owned workspace", idgen.New("scale-p21-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.DeleteWorkspace(context.Background(), other.Workspace.URLKey); err != nil {
			t.Errorf("clean up additional owned workspace: %v", err)
		}
	})
	probe := scaleP21ResidentProbe(f.SQLiteStore)
	if err := probe.loadResidentWorkspaces(t.Context()); err != nil {
		t.Fatal(err)
	}
	probe.metadataResidency.mu.Lock()
	used := probe.metadataResidency.policyBytes
	if probe.metadataResidency.policy[other.Workspace.URLKey] == 0 || probe.metadataResidency.policy[f.data.Workspace.URLKey] == 0 {
		t.Fatal("both workspaces were not charged")
	}
	probe.metadataResidency.mu.Unlock()
	probe.metadataResidency.policyLimit = used - 1
	before := probe.workspaces
	if err := probe.ReloadAllWorkspaces(t.Context()); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("recovery crossed aggregate policy budget: %v", err)
	}
	if reflect.ValueOf(before).Pointer() != reflect.ValueOf(probe.workspaces).Pointer() {
		t.Fatal("budget failure partially replaced installed workspaces")
	}
}

func TestScaleP21RecoveryRejectsCanonicalFieldsOutsideManifest(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	probe := scaleP21ResidentProbe(f.SQLiteStore)
	if err := probe.loadResidentWorkspaces(t.Context()); err != nil {
		t.Fatal(err)
	}
	before, _ := probe.storedWorkspace(workspace)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	scaleP21SeedRow(t, tx, workspace, "unregisteredP21Field", "new-record", 0, map[string]string{"body": strings.Repeat("x", 800000)})
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := probe.ReloadWorkspace(t.Context(), workspace); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatalf("canonical field outside manifest was silently ignored: %v", err)
	}
	after, _ := probe.storedWorkspace(workspace)
	if !reflect.DeepEqual(before, after) || probe.metadataResidency.work.used != 0 {
		t.Fatal("rejected canonical field changed installed state or leaked a lease")
	}
}

func TestScaleP21RecoveryRejectsUnknownPolicySetting(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	scaleP21SeedRow(t, tx, workspace, "workspaceSettings", "unregisteredP21Policy", 0, true)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	probe := scaleP21ResidentProbe(f.SQLiteStore)
	if err := probe.loadResidentWorkspaces(t.Context()); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatalf("unknown policy setting was silently ignored: %v", err)
	}
}

func TestScaleP21InternalWorkAdmissionCancelsAndDoesNotLeak(t *testing.T) {
	budget := newMetadataWorkBudget(100)
	ctx, release, err := budget.acquire(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	_, nestedRelease, err := budget.acquire(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	nestedRelease()
	if _, _, err := budget.acquire(ctx, 101); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("nested operation enlarged its lease")
	}
	var wait sync.WaitGroup
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			cancelled, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			if _, _, err := budget.acquire(cancelled, 1); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("waiting work ignored cancellation: %v", err)
			}
		}()
	}
	wait.Wait()
	release()
	release()
	if budget.used != 0 || budget.waiting != 0 {
		t.Fatalf("leaked admission: used=%d waiting=%d", budget.used, budget.waiting)
	}
}

func TestScaleP21ResidentBackfillCoverageRejectsOldWriterGaps(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	// Limit this owned fixture to policy collections. Scalar catalog parsing
	// is a separate P05 concern; readiness must still cover every row present.
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field NOT IN ('teams','teamSettings')`, workspace); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	check := func() error {
		tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
		if err != nil {
			return err
		}
		defer tx.Rollback()
		return f.validateResidentBackfill(t.Context(), tx, workspace)
	}
	if err := check(); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatalf("incomplete catalog was accepted: %v", err)
	}
	rows, err := f.db.QueryContext(t.Context(), `SELECT DISTINCT field FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	fields := []string{}
	for rows.Next() {
		var field string
		if err := rows.Scan(&field); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		fields = append(fields, field)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range fields {
		for {
			progress, err := f.BackfillMetadataCatalog(t.Context(), workspace, field)
			if err != nil {
				t.Fatal(err)
			}
			if progress.Complete {
				break
			}
		}
	}
	if err := check(); err != nil {
		t.Fatalf("completed canonical backfill rejected: %v", err)
	}
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET collection_order=collection_order+1 WHERE workspace_key=? AND field=?`, workspace, fields[0]); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	if err := check(); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatalf("old-writer gap was accepted: %v", err)
	}
}
