package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// The existing SQL fallback selects these six policy strings and intentionally
// ignores nonpolicy fields, including malformed typed nonpolicy values.
func scaleP12PolicyJSONOracle(raw []byte) (domain.TeamSettings, error) {
	var selected struct {
		ParentTeamID          string `json:"parentTeamId"`
		Access                string `json:"access"`
		MembershipRestriction string `json:"membershipRestriction"`
		SettingsPermission    string `json:"settingsPermission"`
		LabelPermission       string `json:"labelPermission"`
		MemberPermission      string `json:"memberPermission"`
	}
	err := json.Unmarshal(raw, &selected)
	return domain.TeamSettings{
		ParentTeamID: selected.ParentTeamID, Access: selected.Access, MembershipRestriction: selected.MembershipRestriction,
		SettingsPermission: selected.SettingsPermission, LabelPermission: selected.LabelPermission, MemberPermission: selected.MemberPermission,
	}, err
}

func TestScaleP12GatesCallerSnapshotTypedSettings(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	for name, fixture := range scaleP03Stores(t) {
		t.Run(name, func(t *testing.T) {
			s, workspace := fixture.SQLiteStore, fixture.data.Workspace.URLKey
			if err := s.MutateWorkspace(t.Context(), workspace, "scale_p12.policy", "", nil, func(data *domain.Bootstrap) error {
				data.Teams = append(data.Teams, domain.Team{ID: "p12-parent", Private: true}, domain.Team{ID: "p12-child"})
				data.TeamSettings["p12-parent"] = domain.TeamSettings{Access: "private"}
				data.TeamSettings["p12-child"] = domain.TeamSettings{}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			app, err := s.InstallApplication(t.Context(), workspace, domain.ApplicationInstallation{
				ClientID: idgen.New("p12-policy-"), Name: "P12 typed policy", InstalledBy: fixture.data.Viewer.ID,
				Scopes: []string{"read"}, AllPublicTeams: true, Active: true,
			}, "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := s.UninstallApplication(context.Background(), workspace, app.ID); err != nil {
					t.Errorf("uninstall owned app: %v", err)
				}
				if _, err := s.db.ExecContext(context.Background(), `DELETE FROM auth_users WHERE id=?`, app.UserID); err != nil {
					t.Errorf("delete owned app principal: %v", err)
				}
			})
			if _, err := s.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET role='member' WHERE workspace_id=? AND user_id=?`, fixture.data.Workspace.ID, fixture.data.Viewer.ID); err != nil {
				t.Fatal(err)
			}
			for _, test := range []struct {
				name, raw string
				visible   bool
			}{
				{"folded-private", `{"Access":"private"}`, false},
				{"duplicate-private", `{"access":"public","Access":"private"}`, false},
				{"duplicate-public", `{"Access":"private","access":"public"}`, true},
				{"null-keeps-private", `{"access":"private","Access":null}`, false},
				{"escaped-private", `{"\u0041ccess":"private"}`, false},
				{"unicode-folded", `{"acceſſ":"private"}`, false},
				{"folded-parent", `{"ParentTeamId":"p12-parent","access":"public"}`, false},
				{"duplicate-parent", `{"parentTeamId":"","ParentTeamID":"p12-parent"}`, false},
				{"all-policy-fields", `{"SettingsPermission":"owners","LabelPermission":"members","MemberPermission":"owners","MembershipRestriction":"members"}`, false},
				{"large-ignored", `{"agentGuidance":"` + strings.Repeat("中", 1<<18) + `","Access":"private"}`, false},
				{"invalid-ignored-private", `{"Access":"private","agentGuidance":123,"defaultPriority":"bad"}`, false},
				{"invalid-ignored-public", `{"access":"public","agentGuidance":123,"defaultPriority":"bad"}`, true},
				{"large-selected", `{"access":"` + strings.Repeat("x", 1<<20) + `"}`, true},
			} {
				t.Run(test.name, func(t *testing.T) {
					typed, err := scaleP12PolicyJSONOracle([]byte(test.raw))
					if err != nil {
						t.Fatal(err)
					}
					want := typed
					if _, err := s.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='teamSettings' AND record_key=?`, []byte(test.raw), workspace, "p12-child"); err != nil {
						t.Fatal(err)
					}
					version, err := s.RealtimeSourceVersion(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
					tx, err := s.db.BeginTx(t.Context(), metadataReadOptions(s.dialect))
					if err != nil {
						t.Fatal(err)
					}
					s.db.SetMaxOpenConns(1)
					defer tx.Rollback()
					ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: tx})
					access, err := s.ResolveAccess(ctx, workspace, fixture.data.Viewer.ID, nil)
					if err != nil {
						t.Fatal(err)
					}
					if got := access.policy.metadata.TeamSettings["p12-child"]; !reflect.DeepEqual(got, want) {
						t.Errorf("caller-owned SQL policy differs from typed metadata: got %#v want %#v", got, want)
					}
					if got := access.policy.teams.Content["p12-child"]; got != test.visible {
						t.Errorf("child content visibility=%v want %v", got, test.visible)
					}
					source, err := s.ReadEventPolicySource(ctx, workspace, version)
					if test.name == "large-selected" {
						if !errors.Is(err, ErrEventSourceBudget) {
							t.Fatalf("large selected policy changed the existing event source budget: %v", err)
						}
						t.Logf("legacy selected policy retains %d bytes; event source rejects its unchanged 256KiB limit", len(access.policy.metadata.TeamSettings["p12-child"].Access))
					} else {
						if err != nil {
							t.Fatal(err)
						}
						if got := source.metadata.TeamSettings["p12-child"]; !reflect.DeepEqual(got, want) {
							t.Errorf("caller-owned event policy differs from typed policy projection: got %#v want %#v", got, want)
						}
					}
					gotApp, err := s.ApplicationByUser(ctx, app.UserID)
					if err != nil {
						t.Fatal(err)
					}
					wantApp := app
					wantApp.ResolveTeamAccess(access.policy.metadata.Teams, access.policy.metadata.TeamSettings)
					if !reflect.DeepEqual(gotApp.AccessTeamIDs(), wantApp.AccessTeamIDs()) {
						t.Errorf("snapshot app teams=%v want %v", gotApp.AccessTeamIDs(), wantApp.AccessTeamIDs())
					}
					if slices.Contains(gotApp.AccessTeamIDs(), "p12-child") && (typed.Access == "private" || typed.ParentTeamID != "") {
						t.Error("all-public app gained a private child")
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					if s.db.Stats().MaxOpenConnections != 1 {
						t.Fatal("typed policy changed the SQL connection limit")
					}
				})
			}
		})
	}
}

func TestScaleP12GatesTypedPolicyJSON(t *testing.T) {
	for _, raw := range []string{
		`{}`, `null`, `{"access":"private"}`, `{"Access":"private"}`,
		`{"access":"public","Access":"private"}`, `{"access":"private","Access":null}`,
		`{"\u0041ccess":"private","acceſſ":null}`, `{"acceſſ":"private"}`,
		`{"ParentTeamID":"parent","parentTeamId":null}`,
		`{"MembershipRestriction":"members","SettingsPermission":"owners","LabelPermission":"members","MemberPermission":"owners"}`,
		`{"access":false}`, `{"access":42}`, `{"access":{}}`, `{"access":[]}`,
		`{"access":false,"access":"public"}`, `{"Access":false,"access":"private"}`,
		`{"access":"private","Access":123}`, `{"parentTeamId":true,"ParentTeamID":"parent"}`,
		`{"access":"\ud800\udfff"}`, "{\"Access\":\"\xff\"}",
		`{"access":"private","agentGuidance":"` + strings.Repeat("中", 20<<10) + `"}`,
		`{"access":"private","unknown":{"access":true,"nested":[null,{},false]}}`,
		`{"Access":"private","agentGuidance":123,"defaultPriority":"bad"}`,
		`{"access":"public","agentGuidance":123,"defaultPriority":"bad"}`,
		`{"access":"` + strings.Repeat("x", 1<<20) + `"}`,
		`{"access":"private","` + strings.Repeat("unknown", 1000) + `":true}`,
		`{"access":"private"} trailing`, `{"access":"private",`, `[]`, `""`,
	} {
		name := raw
		if len(name) > 100 {
			name = name[:100]
		}
		t.Run(name, func(t *testing.T) {
			typed, typedErr := scaleP12PolicyJSONOracle([]byte(raw))
			got, err := readLegacyTeamPolicyJSON(t.Context(), io.NewSectionReader(bytes.NewReader([]byte(raw)), 0, int64(len(raw))))
			if (err != nil) != (typedErr != nil) {
				t.Fatalf("error differs: projected=%v typed=%v", err, typedErr)
			}
			if typedErr == nil {
				if !reflect.DeepEqual(got, typed) {
					t.Fatalf("policy fields differ: got %#v want %#v", got, typed)
				}
			}
		})
	}
}

func TestScaleP12GatesOwnedSnapshotPolicy(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	for _, fixture := range scaleP03Stores(t) {
		s, workspace := fixture.SQLiteStore, fixture.data.Workspace.URLKey
		s.db.SetMaxOpenConns(1)
		ctx, release, err := s.BeginWorkspaceRead(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		snapshot := ctx.Value(workspaceReadKey{}).(workspaceRead)
		witness := ctx.Value(resourcePolicySnapshotKey{}).(resourcePolicySnapshot)
		wrongStamp, wrongRevision := witness, witness
		wrongStamp.stamp.workspaceID = "other"
		wrongRevision.stamp.revision++
		if _, ok := s.ownedResourcePolicy(ctx, workspace, fixture.data.Workspace.ID); !ok {
			t.Fatal("complete stamped metadata was not reusable")
		}
		for _, test := range []struct {
			name     string
			snapshot workspaceRead
			witness  resourcePolicySnapshot
		}{
			{"core-only", workspaceRead{database: s.db, workspace: workspace, reader: snapshot.reader}, witness},
			{"wrong-workspace", workspaceRead{database: s.db, workspace: "other", reader: snapshot.reader, metadata: snapshot.metadata}, witness},
			{"wrong-reader", workspaceRead{database: s.db, workspace: workspace, metadata: snapshot.metadata}, witness},
			{"wrong-stamp", snapshot, wrongStamp},
			{"wrong-revision", snapshot, wrongRevision},
			{"no-witness", snapshot, resourcePolicySnapshot{}},
		} {
			t.Run(test.name, func(t *testing.T) {
				ctx := context.WithValue(ctx, workspaceReadKey{}, test.snapshot)
				ctx = context.WithValue(ctx, resourcePolicySnapshotKey{}, test.witness)
				if _, ok := s.ownedResourcePolicy(ctx, workspace, fixture.data.Workspace.ID); ok {
					t.Fatal("incomplete or mismatched policy witness was accepted")
				}
			})
		}
		policyQueries := 0
		ctx = WithSQLStatementObserver(ctx, func(query string) {
			if strings.Contains(query, "workspace_metadata_records") && (strings.Contains(query, "'teamSettings'") || strings.Contains(query, "'teams'")) {
				policyQueries++
			}
		})
		// A writer can own mu while waiting for our sole connection.
		s.mu.Lock()
		access, err := s.ResolveAccess(ctx, workspace, fixture.data.Viewer.ID, nil)
		s.mu.Unlock()
		if err != nil || access.WorkspaceID != fixture.data.Workspace.ID {
			t.Fatalf("complete owned policy failed under the writer mutex: %v", err)
		}
		if policyQueries != 0 {
			t.Fatalf("complete owned snapshot reread policy %d times", policyQueries)
		}
		if s.dialect == "sqlite" {
			t.Run("in-transaction-write", func(t *testing.T) {
				if _, err := snapshot.reader.ExecContext(ctx, `UPDATE workspace_metadata_records SET collection_order=collection_order+1 WHERE workspace_key=? AND field='teamSettings'`, workspace); err != nil {
					t.Fatal(err)
				}
				if _, ok := s.ownedResourcePolicy(ctx, workspace, fixture.data.Workspace.ID); ok {
					t.Fatal("policy witness survived an in-transaction write before revision flush")
				}
			})
		}
		release()
	}
}

func TestScaleP12GatesPolicyPhysicalMapOrder(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	for _, fixture := range scaleP03Stores(t) {
		s, workspace := fixture.SQLiteStore, fixture.data.Workspace.URLKey
		if err := s.MutateWorkspace(t.Context(), workspace, "scale_p12.policy_map", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams, domain.Team{ID: "p12-alias-d"})
			data.TeamSettings["p12-alias-d"] = domain.TeamSettings{}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetTeamMembership(t.Context(), fixture.data.Workspace.ID, "p12-alias-d", fixture.data.Viewer.ID, "member", true); err != nil {
			t.Fatal(err)
		}
		s.db.SetMaxOpenConns(1)
		tx, err := s.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for index := range 75 {
			key := fmt.Sprintf("p12-order-%03d", index)
			raw := []byte(`{"access":"public"}`)
			if index%2 == 0 {
				raw = []byte(`{"Access":"private"}`)
			}
			if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"teamSettings", key}, metadataRecord{data: raw, order: index%6 - 3}, nil); err != nil {
				t.Fatal(err)
			}
		}
		if s.dialect == "sqlite" {
			for index, item := range []struct {
				key   any
				order int
				raw   string
			}{
				{"p12-alias-a", 9, `{"Access":"private"}`},
				{[]byte("p12-alias-a"), -9, `{"access":"public"}`},
				{"p12-alias-b", -9, `{"access":"public"}`},
				{[]byte("p12-alias-b"), 9, `{"Access":"private"}`},
				{[]byte("p12-alias-d"), -9, `{"access":"public"}`},
			} {
				if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'teamSettings',?,?,?)`, workspace, item.key, item.order, []byte(item.raw)); err != nil {
					t.Fatalf("physical alias %d: %v", index, err)
				}
			}
			if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET collection_order=9,data=? WHERE workspace_key=? AND field='teamSettings' AND record_key=?`, []byte(`{"Access":"private"}`), workspace, "p12-alias-d"); err != nil {
				t.Fatal(err)
			}
		}
		ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: tx})
		var root []byte
		if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&root); err != nil {
			t.Fatal(err)
		}
		complete, err := s.decodeWorkspaceMetadata(ctx, workspace, root, tx)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.resourcePolicyRecords(ctx, workspace, fixture.data.Workspace.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := resourcePolicyMetadata(complete)
		if !reflect.DeepEqual(got.TeamSettings, want.TeamSettings) {
			t.Fatal("paged policy map differs from complete legacy metadata order")
		}
		if s.dialect == "sqlite" {
			for _, key := range []string{"p12-alias-a", "p12-alias-b", "p12-alias-d"} {
				if got.TeamSettings[key].Access != "private" {
					t.Fatalf("physical map order widened %s: %#v", key, got.TeamSettings[key])
				}
			}
		}
		access, err := s.ResolveAccess(ctx, workspace, fixture.data.Viewer.ID, nil)
		if err != nil || !access.policy.teams.Content["p12-alias-d"] {
			t.Fatalf("ordered map aliases rejected an explicitly authorized principal: %v", err)
		}
		if s.dialect == "sqlite" {
			// Current legacy decoding validates every map row before
			// overwriting an equal Go key, including a losing alias.
			for _, test := range []struct {
				name string
				key  any
				raw  string
			}{
				{"losing-invalid", []byte("p12-alias-d"), `{"access":"public"}`},
				{"winning-invalid", "p12-alias-d", `{"Access":"private"}`},
			} {
				if _, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='teamSettings' AND record_key=?`, []byte(`{"access":false}`), workspace, test.key); err != nil {
					t.Fatal(err)
				}
				if _, err := s.decodeWorkspaceMetadata(ctx, workspace, root, tx); err == nil {
					t.Fatalf("complete legacy oracle accepted %s policy", test.name)
				}
				if _, err := s.resourcePolicyRecords(ctx, workspace, fixture.data.Workspace.ID); err == nil {
					t.Fatalf("streamed policy accepted %s policy", test.name)
				}
				if _, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='teamSettings' AND record_key=?`, []byte(test.raw), workspace, test.key); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestScaleP12GatesScopedSnapshotPolicyAuthority(t *testing.T) {
	// The current journal writer requires textual identities. These physical
	// BLOB aliases belong to the historical journal-off database source.
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	for _, fixture := range scaleP03Stores(t) {
		s, workspace := fixture.SQLiteStore, fixture.data.Workspace.URLKey
		const teamID = "p12-scoped-alias"
		if err := s.MutateWorkspace(t.Context(), workspace, "scale_p12.scoped_policy", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams, domain.Team{ID: teamID})
			data.TeamSettings[teamID] = domain.TeamSettings{}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET role='member' WHERE workspace_id=? AND user_id=?`, fixture.data.Workspace.ID, fixture.data.Viewer.ID); err != nil {
			t.Fatal(err)
		}
		s.db.SetMaxOpenConns(1)
		privateCopies := 0
		for _, test := range []struct{ name, text, blob string }{
			{"private-text", "private", "public"},
			{"private-blob", "public", "private"},
		} {
			t.Run(test.name, func(t *testing.T) {
				tx, err := s.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"teamSettings", teamID}, metadataRecord{data: []byte(fmt.Sprintf(`{"teamId":%q,"access":%q}`, teamID, test.text))}, nil); err != nil {
					t.Fatal(err)
				}
				if s.dialect == "sqlite" {
					if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'teamSettings',?,0,?)
						ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`, workspace, []byte(teamID), []byte(fmt.Sprintf(`{"teamId":%q,"access":%q}`, teamID, test.blob))); err != nil {
						t.Fatal(err)
					}
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if err := s.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				// Equal-order physical aliases follow the actual installed
				// full reader, whose global unstable sort has its own tie
				// order. Do not substitute the SQL policy cursor's winner.
				want, err := s.ResolveAccess(t.Context(), workspace, fixture.data.Viewer.ID, nil)
				if err != nil {
					t.Fatal(err)
				}
				private := want.policy.metadata.TeamSettings[teamID].Access == "private"
				if private {
					privateCopies++
				}
				if want.policy.teams.Content[teamID] == private {
					t.Fatal("installed policy did not enforce the private boundary")
				}
				version, err := s.RealtimeSourceVersion(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				scoped := WithMetadataRecords(t.Context(), MetadataRecordScope{Fields: []string{"projectStatuses"}})
				read, release, err := s.BeginWorkspaceRead(scoped, workspace)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				snapshot := read.Value(workspaceReadKey{}).(workspaceRead)
				if len(snapshot.metadata.Teams) != 0 || len(snapshot.metadata.TeamSettings) != 0 {
					t.Fatal("fixture must omit policy from its scoped metadata")
				}
				if _, ok := s.ownedResourcePolicy(read, workspace, fixture.data.Workspace.ID); !ok {
					t.Fatal("matching pre-SQL scoped policy was not certified")
				}
				policyQueries := 0
				read = WithSQLStatementObserver(read, func(query string) {
					if strings.Contains(query, "workspace_metadata_records") && (strings.Contains(query, "'teams'") || strings.Contains(query, "'teamSettings'")) {
						policyQueries++
					}
				})
				s.mu.Lock()
				got, accessErr := s.ResolveAccess(read, workspace, fixture.data.Viewer.ID, nil)
				source, sourceErr := s.ReadEventPolicySource(read, workspace, version)
				s.mu.Unlock()
				release()
				if accessErr != nil || sourceErr != nil {
					t.Fatalf("scoped installed policy failed while writer mutex was held: access=%v source=%v", accessErr, sourceErr)
				}
				if got.policy.teams.Content[teamID] != want.policy.teams.Content[teamID] ||
					!reflect.DeepEqual(resourcePolicyMetadata(got.policy.metadata), resourcePolicyMetadata(want.policy.metadata)) ||
					!reflect.DeepEqual(source.metadata, resourcePolicyMetadata(want.policy.metadata)) {
					t.Fatal("scoped SQL read changed actual installed full-reader policy")
				}
				if policyQueries != 0 {
					t.Fatalf("scoped owned policy reread policy SQL %d times", policyQueries)
				}
			})
		}
		if privateCopies != 1 {
			t.Fatalf("opposite physical values must exercise one private and one public installed copy, got %d private copies", privateCopies)
		}
	}
}
