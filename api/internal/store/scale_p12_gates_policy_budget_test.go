package store

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP12GatesPolicyReader(t *testing.T, fixture scaleP03StoreFixture, raw string, writable bool) (*legacyTeamPolicyReaderAt, *sqlTx, *atomic.Int64) {
	t.Helper()
	s, workspace := fixture.SQLiteStore, fixture.data.Workspace.URLKey
	key := idgen.New("p12-policy-budget-")
	if err := s.MutateWorkspace(t.Context(), workspace, "scale_p12.policy_budget", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: key, Name: "Policy budget"})
		data.TeamSettings[key] = domain.TeamSettings{TeamID: key}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='teamSettings' AND record_key=?`, []byte(raw), workspace, key); err != nil {
		t.Fatal(err)
	}
	var queries atomic.Int64
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.HasPrefix(query, "SELECT SUBSTR(") {
			queries.Add(1)
		}
	})
	s.db.SetMaxOpenConns(1)
	options := metadataReadOptions(s.dialect)
	options.ReadOnly = !writable
	tx, err := s.db.BeginTx(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: tx})
	return &legacyTeamPolicyReaderAt{
		ctx: ctx, store: s, workspace: workspace, key: key, size: int64(len(raw)), blob: s.dialect == "mysql",
	}, tx, &queries
}

func TestScaleP12GatesPolicyReadChunkBudget(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	policy := `{"Access":"public","access":"private","ParentTeamID":"","SettingsPermission":"owners","LabelPermission":"members","MemberPermission":"owners","MembershipRestriction":"members","memberPermission":null}`
	for name, fixture := range scaleP03Stores(t) {
		t.Run(name, func(t *testing.T) {
			for _, test := range []struct {
				name, raw string
				maxReads  int64
			}{
				{"one-page", policy, 1},
				{"ignored-guidance", `{"agentGuidance":"` + strings.Repeat("x", 4*metadataRecordChunkBytes) + `",` + policy[1:], 8},
			} {
				t.Run(test.name, func(t *testing.T) {
					reader, _, queries := scaleP12GatesPolicyReader(t, fixture, test.raw, false)
					got, err := readLegacyTeamPolicyJSON(reader.ctx, io.NewSectionReader(reader, 0, reader.size))
					if err != nil {
						t.Fatal(err)
					}
					want, err := scaleP12PolicyJSONOracle([]byte(test.raw))
					if err != nil || !reflect.DeepEqual(got, want) {
						t.Fatalf("typed policy differs: got %#v want %#v err=%v", got, want, err)
					}
					if got := queries.Load(); got < 1 || got > test.maxReads {
						t.Fatalf("policy body SQL=%d, budget=%d", got, test.maxReads)
					}
					if len(reader.chunk) > metadataRecordChunkBytes {
						t.Fatalf("retained SQL chunk=%d exceeds existing %d-byte bound", len(reader.chunk), metadataRecordChunkBytes)
					}
					t.Logf("body bytes=%d body SQL=%d retained chunk=%d", len(test.raw), queries.Load(), len(reader.chunk))
				})
			}
		})
	}
}

func TestScaleP12GatesPolicyChunkInvalidatedBySQLWrite(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	for name, fixture := range scaleP03Stores(t) {
		t.Run(name, func(t *testing.T) {
			raw := `{"access":"public"}`
			reader, tx, queries := scaleP12GatesPolicyReader(t, fixture, raw, true)
			first := make([]byte, len(raw))
			if _, err := reader.ReadAt(first, 0); err != nil || string(first) != raw {
				t.Fatalf("first policy=%q err=%v", first, err)
			}
			updated := `{"access":"secret"}`
			if _, err := tx.ExecContext(reader.ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='teamSettings' AND record_key=?`, []byte(updated), reader.workspace, reader.key); err != nil {
				t.Fatal(err)
			}
			next := make([]byte, len(updated))
			if _, err := reader.ReadAt(next, 0); err != nil || string(next) != updated {
				t.Fatalf("retained pre-write policy=%q want %q err=%v", next, updated, err)
			}
			if queries.Load() != 2 {
				t.Fatalf("one SQL read on either side of a write: got %d", queries.Load())
			}
		})
	}
}

func TestScaleP12GatesPolicyChunkConcurrentReadAt(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	for name, fixture := range scaleP03Stores(t) {
		t.Run(name, func(t *testing.T) {
			raw := `{"Access":"private","parentTeamId":"parent","memberPermission":null}`
			reader, _, queries := scaleP12GatesPolicyReader(t, fixture, raw, false)
			var group sync.WaitGroup
			for range 8 {
				group.Go(func() {
					for offset := range len(raw) {
						part := make([]byte, len(raw)-offset)
						if _, err := reader.ReadAt(part, int64(offset)); err != nil || !bytes.Equal(part, []byte(raw[offset:])) {
							t.Errorf("policy at offset=%d: got %q err=%v", offset, part, err)
							return
						}
					}
				})
			}
			group.Wait()
			if queries.Load() != 1 {
				t.Fatalf("concurrent one-page token reads used %d SQL queries", queries.Load())
			}
		})
	}
}

func TestScaleP12GatesPolicyPrefetchBoundaries(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	for _, size := range []int{legacyTeamPolicyPrefixBytes - 1, legacyTeamPolicyPrefixBytes, legacyTeamPolicyPrefixBytes + 1, metadataRecordChunkBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			for name, fixture := range scaleP03Stores(t) {
				t.Run(name, func(t *testing.T) {
					start, end := `{"agentGuidance":"`, `","Access":"private","access":null}`
					raw := start + strings.Repeat("x", size-len(start)-len(end)) + end
					reader, tx, _ := scaleP12GatesPolicyReader(t, fixture, raw, true)
					if _, err := tx.ExecContext(reader.ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='teamSettings' AND record_key<>?`, []byte(`{}`), reader.workspace, reader.key); err != nil {
						t.Fatal(err)
					}
					headers, bodies := 0, 0
					ctx := WithSQLStatementObserver(reader.ctx, func(query string) {
						if strings.HasPrefix(query, "SELECT r.record_key,") {
							headers++
						}
						if strings.HasPrefix(query, "SELECT SUBSTR(") {
							bodies++
						}
					})
					got := map[string]domain.TeamSettings{}
					if err := fixture.readLegacyTeamPolicySettings(ctx, reader.workspace, got); err != nil {
						t.Fatal(err)
					}
					want, err := scaleP12PolicyJSONOracle([]byte(raw))
					if err != nil || !reflect.DeepEqual(got[reader.key], want) {
						t.Fatalf("policy differs at bytes=%d: got %#v want %#v err=%v", size, got[reader.key], want, err)
					}
					if headers != 1 || bodies > 6 || size <= legacyTeamPolicyPrefixBytes && bodies != 0 {
						t.Fatalf("bytes=%d header SQL=%d body SQL=%d", size, headers, bodies)
					}
					t.Logf("body bytes=%d header SQL=%d body SQL=%d", size, headers, bodies)
				})
			}
		})
	}
}

func TestScaleP12GatesPolicyPrefetchInvalidatedBeforeRead(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	for name, fixture := range scaleP03Stores(t) {
		t.Run(name, func(t *testing.T) {
			before, after := `{"access":"public" }`, `{"Access":"private"}`
			reader, tx, _ := scaleP12GatesPolicyReader(t, fixture, before, true)
			large := `{"access":"public","agentGuidance":"` + strings.Repeat("x", 2*metadataRecordChunkBytes) + `"}`
			if _, err := tx.ExecContext(reader.ctx, `UPDATE workspace_metadata_records SET collection_order=0,data=? WHERE workspace_key=? AND field='teamSettings' AND record_key<>?`, []byte(large), reader.workspace, reader.key); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(reader.ctx, `UPDATE workspace_metadata_records SET collection_order=100000 WHERE workspace_key=? AND field='teamSettings' AND record_key=?`, reader.workspace, reader.key); err != nil {
				t.Fatal(err)
			}
			var changed atomic.Bool
			var mutationErr error
			var ctx context.Context
			ctx = WithSQLStatementObserver(reader.ctx, func(query string) {
				if strings.HasPrefix(query, "SELECT SUBSTR(") && changed.CompareAndSwap(false, true) {
					_, mutationErr = tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='teamSettings' AND record_key=?`, []byte(after), reader.workspace, reader.key)
				}
			})
			got := map[string]domain.TeamSettings{}
			if err := fixture.readLegacyTeamPolicySettings(ctx, reader.workspace, got); err != nil {
				t.Fatal(err)
			}
			if !changed.Load() || mutationErr != nil {
				t.Fatalf("controlled post-header write ran=%v err=%v", changed.Load(), mutationErr)
			}
			want, err := scaleP12PolicyJSONOracle([]byte(after))
			if err != nil || !reflect.DeepEqual(got[reader.key], want) {
				t.Fatalf("prefetch retained pre-write policy: got %#v want %#v err=%v", got[reader.key], want, err)
			}
		})
	}
}
