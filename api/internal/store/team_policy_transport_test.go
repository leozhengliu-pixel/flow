package store

import (
	"bytes"
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

const scaleP21SW310Cutoff = (256 << 10) / 32

type scaleP21SW310Row struct {
	key   string
	order int
	raw   []byte
	blob  bool
	text  bool
}

func scaleP21SW310Raw(t *testing.T, object string, size int, unicode bool) []byte {
	t.Helper()
	prefix := strings.TrimSuffix(object, "}") + `,"transportPadding":"`
	padding := size - len(prefix) - len(`"}`)
	if padding < 0 || !strings.HasSuffix(object, "}") {
		t.Fatal("invalid exact-width transport fixture")
	}
	fill := strings.Repeat("x", padding)
	if unicode {
		fill = strings.Repeat("\u754c", padding/3) + strings.Repeat("x", padding%3)
	}
	raw := []byte(prefix + fill + `"}`)
	if len(raw) != size || !json.Valid(raw) {
		t.Fatal("transport fixture lost byte width or JSON syntax")
	}
	return raw
}

func scaleP21SW310Seed(t *testing.T, tx *sqlTx, workspace string, rows []scaleP21SW310Row) {
	t.Helper()
	if _, err := tx.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='teams'`, workspace); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		key, raw := any(row.key), any(row.raw)
		if tx.dialect == "mysql" || row.blob {
			key = []byte(row.key)
		}
		if row.text {
			raw = string(row.raw)
		}
		if _, err := tx.ExecContext(t.Context(),
			`INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'teams',?,?,?)`,
			workspace, key, row.order, raw); err != nil {
			t.Fatal(err)
		}
	}
}

// Use the literal typed decoder and the existing SW23 policy projection.
// The SQL order and physical locator stay in the oracle's complete rows.
func scaleP21SW310Oracle(ctx context.Context, tx *sqlTx, workspace string) ([]metadataCacheRow, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field='teams' ORDER BY collection_order,record_key`,
		workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []metadataCacheRow
	for rows.Next() {
		var key string
		var order int
		var raw []byte
		if err := rows.Scan(&key, &order, &raw); err != nil {
			return nil, err
		}
		var team domain.Team
		if err := json.Unmarshal(raw, &team); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(scaleP21SW23TeamPolicyOnly(team))
		if err != nil {
			return nil, err
		}
		result = append(result, metadataCacheRow{Field: "teams", Key: key, Order: order, Data: encoded})
	}
	return result, rows.Err()
}

func scaleP21SW310Header(query string) bool {
	return strings.HasPrefix(query, "SELECT record_key,") &&
		strings.Contains(query, "FROM workspace_metadata_records") &&
		strings.Contains(query, "field='teams'") &&
		strings.Contains(query, "ORDER BY collection_order,record_key LIMIT 32")
}

func scaleP21SW310Chunk(query string) bool {
	return strings.HasPrefix(query, "SELECT SUBSTR(") &&
		strings.Contains(query, "FROM workspace_metadata_records") &&
		strings.Contains(query, "field='teams'")
}

func scaleP21SW310Closed(t *testing.T, view *SQLiteStore, read *scaleP21SW23TeamPolicyRowsRead) {
	t.Helper()
	read.Close()
	read.Close()
	if read.rows != nil || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("policy transport retained rows/work or decoded full metadata")
	}
}

func TestScaleP21SW310TeamPolicySmallRowTransport(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	// SW23's fixture creates unique account/workspace identities and honors
	// FLOW_TEST_DATABASE_DRIVER/FLOW_TEST_DATABASE_URL, including persistence.
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency()}
	view.metadataResidency.requireCoverage = false
	if metadataRecoveryPageRows != 32 || MetadataRecordPageMaxBytes != 256<<10 || metadataWorkBytes != 256<<20 {
		t.Fatal("transport regression must retain the production limits")
	}

	type scenario struct {
		name     string
		count    int
		size     int
		unicode  bool
		physical bool
		bad      bool
		object   string
	}
	for _, test := range []scenario{
		{name: "empty"},
		{name: "below-cutoff", count: 1, size: scaleP21SW310Cutoff - 1},
		{name: "at-cutoff", count: 1, size: scaleP21SW310Cutoff},
		{name: "above-cutoff", count: 1, size: scaleP21SW310Cutoff + 1},
		{name: "unicode-at-byte-cutoff", count: 1, size: scaleP21SW310Cutoff, unicode: true},
		{name: "unicode-above-byte-cutoff", count: 1, size: scaleP21SW310Cutoff + 1, unicode: true},
		{name: "32-full-small-rows", count: 32, size: scaleP21SW310Cutoff},
		{name: "33-small-rows", count: 33, size: 256},
		{name: "physical-key-cursor", count: 33, size: 256, physical: true},
		{name: "folded-repeated-small", count: 1, size: scaleP21SW310Cutoff,
			object: `{"id":"first","ID":null,"\u212aey":"folded","key":"last","externalSource":"old","external\u017fource":"new","private":false,"PRIVATE":true,"Private":null,"archivedAt":"2026-11-17T01:02:03+05:45","ARCHIVEDAT":null,"retiredAt":"2026-11-17T01:02:03.123456789Z","unknown":{"private":"ignored"}}`},
		{name: "folded-repeated-large", count: 1, size: scaleP21SW310Cutoff + 1,
			object: `{"ID":"first","id":"last","private":true,"PRIVATE":false,"name":"ignored","createdAt":"2026-11-17T01:02:03Z","createdAt":null}`},
		{name: "malformed-known-small", count: 1, size: scaleP21SW310Cutoff, bad: true,
			object: `{"id":"bad","name":7}`},
		{name: "malformed-known-large", count: 1, size: scaleP21SW310Cutoff + 1, bad: true,
			object: `{"id":"bad","private":"true"}`},
		{name: "earlier-type-error-survives-repeat", count: 1, size: 256, bad: true,
			object: `{"id":{},"id":"later"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			var rows []scaleP21SW310Row
			for index := range test.count {
				key := fmt.Sprintf("physical-%03d", index)
				object := test.object
				if object == "" {
					object = fmt.Sprintf(`{"id":"native-%03d","key":"K%03d","private":%t}`, index%7, index, index%2 == 0)
				}
				row := scaleP21SW310Row{key: key, order: index%5 - 2,
					raw: scaleP21SW310Raw(t, object, test.size, test.unicode), text: test.unicode && f.dialect == "sqlite"}
				if test.physical {
					row.order = -7
					switch index {
					case 0:
						row.key = ""
					case 1:
						row.key = "alias"
					case 2:
						if f.dialect == "sqlite" {
							row.key = "physical\x00key"
						}
					case 31, 32:
						if f.dialect == "sqlite" {
							row.key, row.blob = "", true
							if index == 32 {
								row.key = "alias"
							}
						}
					}
				}
				rows = append(rows, row)
			}
			scaleP21SW310Seed(t, tx, workspace, rows)
			want, oldErr := scaleP21SW310Oracle(t.Context(), tx, workspace)
			if (oldErr != nil) != test.bad {
				t.Fatal("literal typed fixture lost its success/error witness", oldErr)
			}
			headers, chunks := 0, 0
			ctx := WithSQLStatementObserver(t.Context(), func(query string) {
				if scaleP21SW310Header(query) {
					headers++
				}
				if scaleP21SW310Chunk(query) {
					chunks++
				}
			})
			read, err := view.scaleP21SW23ReadTeamPolicyRows(ctx, tx, workspace)
			if test.bad {
				if err == nil || read != nil || view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("malformed typed row succeeded or retained its partial output/owners", err)
				}
			} else {
				if err != nil || read == nil {
					t.Fatal("legal transport fixture must succeed", err)
				}
				defer read.Close()
				if !reflect.DeepEqual(read.rows, want) || len(read.rows) != test.count {
					t.Fatal("typed policy output, physical key order or cursor paging differs", len(read.rows), len(want))
				}
				if view.MetadataWorkUsedForTesting() != 12<<20 {
					t.Fatal("retained policy output lost its unchanged independent 12 MiB owner")
				}
				if headers != (test.count+31)/32+1 {
					t.Fatal("32-row keyset pagination changed", headers, test.count)
				}
				if test.physical && f.dialect == "sqlite" {
					if read.rows[31].Key != "" || read.rows[32].Key != "alias" {
						t.Fatal("fixture did not cross the SQLite BLOB cursor boundary")
					}
				}
				scaleP21SW310Closed(t, view, read)
			}
			if test.count > 0 && test.size > scaleP21SW310Cutoff {
				if chunks == 0 {
					t.Fatal("large row did not use the existing SQL ReaderAt fallback")
				}
			} else if chunks != 0 {
				t.Fatal("small row still issued SQL SUBSTR body reads", chunks)
			}
			if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("transport success/error leaked work or hydrated full metadata")
			}
			t.Logf("driver=%s rows=%d bytes_per_row=%d header_SELECTs=%d SUBSTR=%d typed_error=%t",
				f.dialect, test.count, test.size, headers, chunks, test.bad)
		})
	}

	t.Run("cancel-partial-pages-and-fallback", func(t *testing.T) {
		for _, large := range []bool{false, true} {
			t.Run(fmt.Sprintf("large=%t", large), func(t *testing.T) {
				tx, err := f.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				count, size := 33, 256
				if large {
					count, size = 1, 64<<10
				}
				rows := make([]scaleP21SW310Row, count)
				for index := range rows {
					rows[index] = scaleP21SW310Row{key: fmt.Sprintf("cancel-%03d", index), order: index,
						raw: scaleP21SW310Raw(t, `{"id":"cancel","private":true}`, size, false)}
				}
				scaleP21SW310Seed(t, tx, workspace, rows)
				for _, before := range []bool{true, false} {
					ctx, cancel := context.WithCancel(t.Context())
					headers, chunks := 0, 0
					ctx = WithSQLStatementObserver(ctx, func(query string) {
						if scaleP21SW310Header(query) {
							headers++
							if !large && headers == 2 {
								cancel()
							}
						}
						if scaleP21SW310Chunk(query) {
							chunks++
							if large && chunks == 2 {
								cancel()
							}
						}
					})
					if before {
						cancel()
					}
					read, err := view.scaleP21SW23ReadTeamPolicyRows(ctx, tx, workspace)
					cancel()
					if !errors.Is(err, context.Canceled) || read != nil || view.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("cancelled transport retained rows/projector/parent allowance", before, err)
					}
					if !before && (large && chunks < 2 || !large && headers < 2) {
						t.Fatal("cancellation never reached partially retained transport")
					}
				}
				// A fresh context on the caller-owned transaction still works.
				read, err := view.scaleP21SW23ReadTeamPolicyRows(t.Context(), tx, workspace)
				if err != nil || read == nil || len(read.rows) != count {
					t.Fatal("cancelled child closed caller transaction or prevented recovery", err)
				}
				scaleP21SW310Closed(t, view, read)
			})
		}
	})

	t.Run("small-and-chunked-rows-share-caller-snapshot", func(t *testing.T) {
		seed, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer seed.Rollback()
		rows := make([]scaleP21SW310Row, 33)
		for index := range rows {
			rows[index] = scaleP21SW310Row{key: fmt.Sprintf("snapshot-%03d", index), order: index,
				raw: scaleP21SW310Raw(t, fmt.Sprintf(`{"id":"before-%03d","private":true}`, index), 256, false)}
		}
		rows[0].raw = scaleP21SW310Raw(t, `{"id":"before-000","private":true}`, 64<<10, false)
		scaleP21SW310Seed(t, seed, workspace, rows)
		if err := seed.Commit(); err != nil {
			t.Fatal(err)
		}
		tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		want, err := scaleP21SW310Oracle(t.Context(), tx, workspace)
		if err != nil {
			t.Fatal(err)
		}
		chunks, headers := 0, 0
		changed := false
		ctx := WithSQLStatementObserver(t.Context(), func(query string) {
			if scaleP21SW310Header(query) {
				headers++
			}
			if !scaleP21SW310Chunk(query) {
				return
			}
			chunks++
			if chunks != 2 {
				return
			}
			peerCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			peer, err := f.db.BeginTx(peerCtx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Rollback()
			for _, index := range []int{0, 32} {
				replacement := bytes.ReplaceAll(rows[index].raw, []byte("before-"), []byte("after--"))
				if len(replacement) != len(rows[index].raw) {
					t.Fatal("snapshot peer edit changed raw byte width")
				}
				if _, err := peer.ExecContext(peerCtx,
					`UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='teams' AND record_key=?`,
					replacement, workspace, rows[index].key); err != nil {
					t.Fatal(err)
				}
			}
			if err := peer.Commit(); err != nil {
				t.Fatal(err)
			}
			changed = true
		})
		read, err := view.scaleP21SW23ReadTeamPolicyRows(ctx, tx, workspace)
		if err != nil || read == nil {
			t.Fatal("same-snapshot transport must succeed after a genuine peer commit", err)
		}
		defer read.Close()
		if !changed || chunks < 2 || headers != 3 || !reflect.DeepEqual(read.rows, want) {
			t.Fatal("small later-page row or chunked body mixed peer generations", changed, chunks, headers)
		}
		scaleP21SW310Closed(t, view, read)
		for _, index := range []int{0, 32} {
			var raw []byte
			if err := f.db.QueryRowContext(t.Context(),
				`SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='teams' AND record_key=?`,
				workspace, rows[index].key).Scan(&raw); err != nil || !bytes.Contains(raw, []byte("after--")) {
				t.Fatal("independent peer commit was absent or overwritten", index, err)
			}
		}
	})
}
