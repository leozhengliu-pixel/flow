package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW31InlineMembersRefreshDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, fieldScoped := range []bool{false, true} {
		for _, large := range []bool{false, true} {
			name := "full"
			if fieldScoped {
				name = "legacy-field-scoped"
			}
			if large {
				name += "-large-member"
			}
			t.Run(name, func(t *testing.T) {
				f := scaleP09Database(t)
				workspace := f.data.Workspace.URLKey
				personID := idgen.New("sw31-person-")
				fresh := domain.User{ID: personID, Name: "Fresh member", DisplayName: "Fresh display", Active: true}
				if large {
					fresh.Name, fresh.DisplayName = strings.Repeat("fresh ", 1400), strings.Repeat("display ", 1000)
				}
				stale := fresh
				stale.Name, stale.DisplayName = "Old member", "Old display"
				reset := func() {
					t.Helper()
					f.SetRealtimeSink(nil)
					if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "users", "members", "settings"), workspace, "application_policy.updated", "", nil,
						func(data *domain.Bootstrap) error {
							data.Users = append(slices.DeleteFunc(data.Users, func(user domain.User) bool { return user.ID == personID }), fresh)
							data.Members = []domain.WorkspaceMember{{User: stale, Role: "member", Status: "active", JoinedAt: time.Date(2026, 11, 13, 1, 2, 3, 0, time.UTC)}}
							data.Settings["sw31-target"] = "before"
							data.Settings["sw31-untouched"] = map[string]any{"integer": 7, "nil": nil}
							return nil
						}); err != nil {
						t.Fatal(err)
					}
				}
				type outcome struct {
					root       map[string]json.RawMessage
					members    []domain.WorkspaceMember
					realtime   json.RawMessage
					finalStamp copyStamp
					statements int
				}
				var results []outcome
				for _, bounded := range []bool{false, true} {
					reset()
					repo := f.SQLiteStore
					var initialStamp copyStamp
					if bounded {
						var err error
						repo, err = f.BoundedMetadataViewForTesting(t.Context())
						if err != nil {
							t.Fatal(err)
						}
						source, err := repo.retainMetadataLogicalSource(t.Context(), workspace)
						if err != nil {
							t.Fatal(err)
						}
						initialStamp = source.Stamp()
						source.Release()
					}
					got := outcome{}
					repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { got.realtime = slices.Clone(event.Payload) })
					ctx := t.Context()
					if fieldScoped {
						ctx = WithMetadataFields(ctx, "settings")
					}
					ctx = WithSQLStatementObserver(ctx, func(string) { got.statements++ })
					err := repo.MutateWorkspaceScoped(ctx, workspace, "application_policy.updated", "sw31-absent-aggregate", map[string]string{"value": "after"},
						MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "sw31-target"}}},
						func(data *domain.Bootstrap) error {
							data.Settings["sw31-target"] = "after"
							return nil
						})
					if err != nil {
						t.Fatal(err)
					}
					var raw []byte
					if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&raw); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(raw, &got.root); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(got.root["members"], &got.members); err != nil {
						t.Fatal(err)
					}
					got.finalStamp, err = readMetadataStamp(t.Context(), f.db, workspace)
					if err != nil {
						t.Fatal(err)
					}
					if bounded {
						source, err := repo.retainMetadataLogicalSource(t.Context(), workspace)
						if err != nil {
							t.Fatal(err)
						}
						stamp := source.Stamp()
						if stamp.workspaceID != got.finalStamp.workspaceID ||
							initialStamp.revision >= 0 && stamp != got.finalStamp ||
							initialStamp.revision < 0 && stamp.revision != -1 {
							source.Release()
							t.Fatal("inline root write invented or lost commit stamp certification")
						}
						installed, err := source.Select(t.Context(), MetadataRecordScope{Fields: []string{"members"}}, 2<<20)
						source.Release()
						if err != nil || !reflect.DeepEqual(installed.Members, got.members) {
							t.Fatalf("inline canonical and installed members diverged: %v", err)
						}
						if repo.WorkspaceMetadataDecodes() != 0 || repo.metadataResidency.work.used != 0 {
							t.Fatal("inline writer decoded the full workspace or retained work admission")
						}
					}
					results = append(results, got)
				}
				want := fresh.Name
				if fieldScoped {
					want = stale.Name
				}
				if len(results[0].members) != 1 || results[0].members[0].User.Name != want {
					t.Fatal("old inline refresh control lost its field boundary")
				}
				if !reflect.DeepEqual(results[0].root, results[1].root) ||
					!reflect.DeepEqual(results[0].members, results[1].members) ||
					!jsonEqual(results[0].realtime, results[1].realtime) {
					t.Fatal("inline members refresh changed canonical root or published effects")
				}
				t.Logf("mutation SQL statements: old=%d bounded=%d; inline field root <=%d bytes and admitted work <=%d bytes",
					results[0].statements, results[1].statements, MetadataRecordPageMaxBytes, metadataLogicalInlineWorkLimit)
			})
		}
	}
}

func TestScaleP21SW31InlineMembersBudgetAndCancellation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var originalRoot []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&originalRoot); err != nil {
		t.Fatal(err)
	}
	before := domain.Bootstrap{Members: []domain.WorkspaceMember{{User: domain.User{ID: "sw31-user", Name: "before"}}}}
	after := before
	after.Members = []domain.WorkspaceMember{{User: domain.User{ID: "sw31-user", Name: strings.Repeat("large member ", 100000)}}}
	a, err := captureMetadataLogicalSource(t.Context(), before, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b, err := captureMetadataLogicalSource(t.Context(), after, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Release()
	fieldNode := func(source *metadataLogicalSource) metadataLogicalNode {
		var result metadataLogicalNode
		if err := source.VisitFields(t.Context(), func(name string, node metadataLogicalNode) error {
			if name == "members" {
				result = node
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return result
	}
	aNode, bNode := fieldNode(a), fieldNode(b)
	for _, canceled := range []bool{false, true} {
		ctx := t.Context()
		if canceled {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		err = view.persistMetadataLogicalInlineField(ctx, tx, workspace, "members", aNode, bNode)
		_ = tx.Rollback()
		if canceled {
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation changed to a different error: %v", err)
			}
		} else if !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatalf("oversized inline field exceeded explicit admission: %v", err)
		}
		if view.metadataResidency.work.used != 0 {
			t.Fatal("failed inline field retained work admission")
		}
		var unchanged []byte
		if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&unchanged); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(originalRoot, unchanged) {
			t.Fatal("failed inline field changed canonical root bytes")
		}
	}
}
