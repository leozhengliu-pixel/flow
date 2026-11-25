package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW20TeamPolicyPhysicalKeyOrderDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	type row struct {
		key      any
		bodyID   string
		marker   string
		archived bool
	}
	type scenario struct {
		name       string
		rows       []row
		mixedTypes bool
	}
	configured := scenario{name: "configured-column-collation"}
	// Compare the actual configured database ordering. No locale, case or
	// Unicode normalization order is assumed, including on PostgreSQL.
	for index, key := range []string{
		"A", "a", "Z", "z", "\u00c4", "\u00e4", "\u00c5", "\u00e5",
		"\u00e9", "e", "e\u0301", "\u00df", "ss", "\u0130", "i", "\u0131",
		"\u03a3", "\u03c3", "\u03c2", "\u4e2d", "\u6587", "\u00e9clair",
		"apple", "Apple", "apple-", "apple_", "apple0",
	} {
		id := fmt.Sprintf("sw20-order-unicode-%03d", index)
		if key == "A" || key == "a" {
			id = "sw20-order-duplicate-body"
		}
		configured.rows = append(configured.rows, row{
			key: key, bodyID: id, marker: fmt.Sprintf("unicode-%03d", index), archived: key == "A",
		})
	}
	for index := range 45 {
		configured.rows = append(configured.rows, row{
			key: fmt.Sprintf("filler-%03d", index), bodyID: fmt.Sprintf("sw20-order-filler-%03d", index),
			marker: fmt.Sprintf("filler-%03d", index),
		})
	}
	scenarios := []scenario{configured}
	if f.dialect == "sqlite" {
		mixed := scenario{name: "sqlite-physical-text-and-blob", mixedTypes: true}
		for index := range 36 {
			mixed.rows = append(mixed.rows,
				row{key: fmt.Sprintf("txt-%03d", index), bodyID: fmt.Sprintf("sw20-order-text-%03d", index), marker: fmt.Sprintf("text-%03d", index)},
				row{key: []byte(fmt.Sprintf("bin-%03d", index)), bodyID: fmt.Sprintf("sw20-order-blob-%03d", index), marker: fmt.Sprintf("blob-%03d", index)},
			)
		}
		mixed.rows = append(mixed.rows,
			row{key: "same-physical-bytes", bodyID: "sw20-order-same-text", marker: "same-text"},
			row{key: []byte("same-physical-bytes"), bodyID: "sw20-order-same-blob", marker: "same-blob"},
			row{key: "z-archived-first", bodyID: "sw20-order-duplicate-body", marker: "archived-text", archived: true},
			row{key: []byte("a-active-later"), bodyID: "sw20-order-duplicate-body", marker: "active-blob"},
		)
		scenarios = append(scenarios, mixed)
	}
	for _, spec := range scenarios {
		t.Run(spec.name, func(t *testing.T) {
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='teams'`, workspace); err != nil {
				t.Fatal(err)
			}
			archived := time.Date(2026, 11, 13, 1, 2, 3, 0, time.UTC)
			for _, item := range spec.rows {
				team := domain.Team{ID: item.bodyID, Key: item.marker, Name: "ignored name"}
				if item.archived {
					team.ArchivedAt = &archived
				}
				body, err := json.Marshal(team)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'teams',?,0,?)`, workspace, item.key, body); err != nil {
					t.Fatal(err)
				}
			}
			if spec.mixedTypes {
				var physicalDuplicates int
				if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='teams' AND CAST(record_key AS BLOB)=CAST(? AS BLOB)`, workspace, "same-physical-bytes").Scan(&physicalDuplicates); err != nil || physicalDuplicates != 2 {
					t.Fatal("fixture lost distinct TEXT/BLOB rows with identical bytes", physicalDuplicates, err)
				}
			}
			ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
			// This is the literal old SQL/typed implementation, not a Go or
			// forced-C re-sort used as a substitute oracle.
			old, err := f.resourcePolicyRecords(ctx, workspace, f.data.Workspace.ID)
			if err != nil || len(old.Teams) != len(spec.rows) {
				t.Fatal("legacy physical-row oracle", len(old.Teams), err)
			}
			view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency()}
			view.metadataResidency.requireCoverage = false
			read, err := view.scaleP21SW23ReadTeamPolicyRows(ctx, tx, workspace)
			if err != nil {
				t.Fatal("policy header scan rejected legal physical rows", err)
			}
			projected := domain.Bootstrap{}
			for _, item := range read.rows {
				if err := appendResidentMetadataRow(&projected, item, "array"); err != nil {
					read.Close()
					t.Fatal(err)
				}
			}
			expected, err := json.Marshal(old.Teams)
			if err != nil {
				read.Close()
				t.Fatal(err)
			}
			actual, err := json.Marshal(projected.Teams)
			if err != nil || string(actual) != string(expected) {
				read.Close()
				t.Fatalf("physical collection order differs: dialect=%s old=%s new=%s err=%v", f.dialect, expected, actual, err)
			}
			oldVisible := teamVisibleToUser(domain.Bootstrap{Teams: old.Teams}, "sw20-order-duplicate-body", "outside", "member")
			newVisible := teamVisibleToUser(projected, "sw20-order-duplicate-body", "outside", "member")
			if oldVisible != newVisible || spec.mixedTypes && oldVisible {
				read.Close()
				t.Fatal("first duplicate body's archived state changed visibility", oldVisible, newVisible)
			}
			read.Close()
			native, err := view.readResidentResourcePolicy(ctx, workspace, f.data.Workspace.ID)
			if err != nil {
				t.Fatal("native policy integration", err)
			}
			expectedPolicy, err := json.Marshal(old)
			if err != nil {
				t.Fatal(err)
			}
			actualPolicy, err := json.Marshal(native)
			if err != nil || string(actualPolicy) != string(expectedPolicy) {
				t.Fatal("native policy integration differs from literal configured SQL order", err)
			}
			if view.metadataResidency.work.used != 0 {
				t.Fatal("physical key regression leaked retained work")
			}
			t.Logf("dialect=%s physical-rows=%d duplicate-body-visible=%v", f.dialect, len(spec.rows), oldVisible)
		})
	}
}

func TestScaleP21SW20TeamPolicyNamespacePredicateDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	if f.dialect != "mysql" {
		t.Skip("MySQL workspace collation and binary point-predicate control")
	}
	workspace := f.data.Workspace.URLKey
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	body := []byte(`{"id":"sw20-namespace-team","key":"namespace-control"}`)
	if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"teams", "sw20-namespace-team"}, metadataRecord{data: body, order: -999}, nil); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
	matched := 0
	for _, alias := range []string{strings.ToUpper(workspace), workspace + " "} {
		if alias == workspace {
			continue
		}
		stamp, err := readMetadataStamp(ctx, tx, alias)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil || stamp.workspaceID != f.data.Workspace.ID {
			t.Fatal("configured namespace collation control", err)
		}
		matched++
		old, err := f.resourcePolicyRecords(ctx, alias, f.data.Workspace.ID)
		if err != nil || len(old.Teams) == 0 {
			t.Fatal("literal legacy namespace oracle", err)
		}
		view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency()}
		view.metadataResidency.requireCoverage = false
		actual, err := view.readResidentResourcePolicy(ctx, alias, f.data.Workspace.ID)
		if err != nil {
			t.Fatal("new Team point predicate rejected a legacy-selected namespace", err)
		}
		expectedJSON, err := json.Marshal(old)
		if err != nil {
			t.Fatal(err)
		}
		actualJSON, err := json.Marshal(actual)
		if err != nil || string(expectedJSON) != string(actualJSON) {
			t.Fatal("configured namespace collation changed policy output", err)
		}
		if view.metadataResidency.work.used != 0 {
			t.Fatal("namespace predicate control leaked retained work")
		}
	}
	if matched == 0 {
		t.Skip("configured collation has no tested case or space equivalent alias")
	}
	t.Logf("configured MySQL namespace aliases checked: %d", matched)
}
