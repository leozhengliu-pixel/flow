package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW23TeamPolicyNativeTypedFallback(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency()}
	view.metadataResidency.requireCoverage = false
	for _, bad := range []bool{false, true} {
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		for index := range 75 {
			body := fmt.Sprintf(`{"id":"duplicate-%d","key":"key-%03d","Private":true,"name":"ignored","archivedAt":"2026-11-13T01:02:03.%s+05:45"}`, index%7, index, strings.Repeat("7", 1000))
			if index == 31 {
				body = `{"id":"private-raw","private":false,"PRIVATE":true,"Name":"` + strings.Repeat("unused", 400000) + `"}`
			}
			if bad && index == 74 {
				body = `{"id":"bad","name":123}`
			}
			if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"teams", fmt.Sprintf("physical-%03d", index)}, metadataRecord{data: []byte(body), order: index%6 - 3}, nil); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
		}
		settings := []byte(`{"Access":"private","access":"public","parentTeamId":null,"membershipRestriction":"members","defaultPriority":"ignored-invalid"}`)
		if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"teamSettings", "private-raw"}, metadataRecord{data: settings}, nil); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
		old, oldErr := f.resourcePolicyRecords(ctx, workspace, f.data.Workspace.ID)
		native, newErr := view.readResidentResourcePolicy(ctx, workspace, f.data.Workspace.ID)
		if (oldErr != nil) != (newErr != nil) {
			tx.Rollback()
			t.Fatal("typed fallback error mismatch", oldErr, newErr)
		}
		if oldErr == nil {
			oldJSON, _ := json.Marshal(old)
			newJSON, _ := json.Marshal(native)
			if string(oldJSON) != string(newJSON) {
				tx.Rollback()
				t.Fatal("canonical alias/null/time/order fallback differs")
			}
			if teamVisibleToUser(native, "private-raw", "outsider", "member") {
				tx.Rollback()
				t.Fatal("historical private alias granted outside member")
			}
		}
		tx.Rollback()
		if view.metadataResidency.work.used != 0 {
			t.Fatal("native fallback leaked independent encoding/row admission")
		}
	}
}

func TestScaleP21SW23TeamPolicyNativeOwnedEncoding(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency()}
	view.metadataResidency.requireCoverage = false
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, key := range []string{"id", "key"} {
		body := []byte(fmt.Sprintf(`{"id":"row",%q:%q}`, key, strings.Repeat("<", 50000)))
		if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"teams", "encoded"}, metadataRecord{data: body}, nil); err != nil {
			t.Fatal(err)
		}
		ctx, releaseParent, err := view.admitMetadataWork(t.Context(), 2<<20)
		if err != nil {
			t.Fatal(err)
		}
		read, err := view.scaleP21SW23ReadTeamPolicyRows(ctx, tx, workspace)
		releaseParent()
		if read != nil || !errors.Is(err, ErrMetadataPageBudget) || view.metadataResidency.work.used != 0 {
			t.Fatal("encoded expansion exceeded page budget without complete cleanup", key, err)
		}
	}
	if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"teams", "encoded"}, metadataRecord{data: []byte(`{"id":"small","private":true}`)}, nil); err != nil {
		t.Fatal(err)
	}
	ctx, releaseParent, err := view.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	read, err := view.scaleP21SW23ReadTeamPolicyRows(ctx, tx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	releaseParent()
	if len(read.rows) == 0 || view.metadataResidency.work.used != 12<<20 {
		t.Fatal("encoded rows borrowed parent allowance")
	}
	read.Close()
	read.Close()
	if view.metadataResidency.work.used != 0 || read.rows != nil {
		t.Fatal("encoded read did not clear rows and release work")
	}
}

func TestScaleP21SW23TeamPolicyNativePreservesInstalledRecovery(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21_sw23.installed_recovery_control", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: "installed-control", Name: "must stay installed", Key: "key", Color: "red", Icon: "circle", ArchivedByID: "actor"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	old, ok := f.WorkspaceMetadataFields(workspace, "teams")
	if !ok {
		t.Fatal("legacy installed control missing")
	}
	view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency(), workspaces: map[string]domain.Bootstrap{}}
	view.metadataResidency.requireCoverage = false
	if err := view.reloadResidentWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	got, ok := view.WorkspaceMetadataFields(workspace, "teams")
	if !ok || !reflect.DeepEqual(old.Teams, got.Teams) {
		t.Fatal("policy helper changed complete installed Team recovery")
	}
	if !view.LoopTeamKeyAllowed(workspace, "must stay installed", map[string]bool{"installed-control": true}) {
		t.Fatal("Name-dependent installed consumer lost full Team fields")
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"ID":"reload-alias","Name":"alias name stays installed","Private":true,"ARCHIVEDAT":"2026-11-13T01:02:03.` + strings.Repeat("7", 1000) + `+05:45","createdAt":"2026-11-13T01:02:03Z"}`)
	if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"teams", "reload-physical"}, metadataRecord{data: raw, order: -9}, nil); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := view.reloadResidentWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	again, _ := view.WorkspaceMetadataFields(workspace, "teams")
	legacy, _ := f.WorkspaceMetadataFields(workspace, "teams")
	if !reflect.DeepEqual(again.Teams, legacy.Teams) {
		t.Fatal("ordinary completed reload Team values changed")
	}
	if len(again.Teams) == 0 || again.Teams[0].ID != "reload-alias" || again.Teams[0].Name != "alias name stays installed" ||
		again.Teams[0].ArchivedAt == nil {
		t.Fatal("full installed recovery lost aliases or long timestamp")
	}
}

func TestScaleP21SW23TeamPolicyNativeSingleConnectionSnapshot(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	f.db.SetMaxOpenConns(1)
	view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency()}
	view.metadataResidency.requireCoverage = false
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	// Direct trusted calls create and close their own repeatable snapshot.
	if _, err := view.readResidentResourcePolicy(ctx, workspace, f.data.Workspace.ID); err != nil {
		t.Fatal("direct caller deadlocked or failed on a one-connection pool", err)
	}
	tx, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	owned := context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
	if _, err := view.readResidentResourcePolicy(owned, workspace, f.data.Workspace.ID); err != nil {
		t.Fatal("borrowed snapshot used a second connection", err)
	}
	var one int
	if err := tx.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatal("borrowed transaction was closed by policy reader", err)
	}
	// The established wrapper binds ambient transactions to a database,
	// not an implicit workspace alias. Explicit trusted keys keep precedence.
	otherNamespace := context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace + "-other", reader: tx})
	old, oldErr := f.resourcePolicyRecords(otherNamespace, workspace, f.data.Workspace.ID)
	actual, err := view.readResidentResourcePolicy(otherNamespace, workspace, f.data.Workspace.ID)
	oldJSON, _ := json.Marshal(old)
	actualJSON, _ := json.Marshal(actual)
	if oldErr != nil || err != nil || string(oldJSON) != string(actualJSON) {
		t.Fatal("explicit namespace changed the legacy database-bound snapshot contract", oldErr, err)
	}
	if _, err := view.readResidentResourcePolicy(owned, workspace, "foreign-workspace-id"); !errors.Is(err, ErrResourceAccessChanged) {
		t.Fatal("explicit workspace-ID binding was lost", err)
	}
	invalid := context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace})
	if _, err := view.readResidentResourcePolicy(invalid, workspace, f.data.Workspace.ID); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("nil matching snapshot reader was not rejected", err)
	}
	if view.metadataResidency.work.used != 0 {
		t.Fatal("snapshot error or success leaked work")
	}
}
