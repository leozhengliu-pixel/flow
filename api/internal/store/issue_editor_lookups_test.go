package store

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21IssueEditorRead(t *testing.T, ctx context.Context, repo *SQLiteStore, workspace, workspaceID string, target domain.Issue) (*issueEditorLookups, *sqlTx) {
	t.Helper()
	source, err := repo.retainMetadataLogicalSource(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := repo.db.BeginTx(ctx, repo.recordTxOptions())
	if err != nil {
		source.Release()
		t.Fatal(err)
	}
	read, err := newIssueEditorLookups(ctx, &issueHotInputs{store: repo, source: source}, tx, workspace, workspaceID, target)
	source.Release()
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		read.close()
		tx.Rollback()
		if repo.MetadataWorkUsedForTesting() != 0 {
			t.Error("editor lookup owner leaked work", repo.MetadataWorkUsedForTesting())
		}
	})
	return read, tx
}

func scaleP21IssueEditorMembers(t *testing.T, f scaleP09Fixture, count int) []string {
	t.Helper()
	prefix := idgen.New("p21-editor-")
	var ids []string
	for index := range count {
		id := fmt.Sprintf("%s-%03d", prefix, index)
		ids = append(ids, id)
		stamp := "2026-01-02T03:04:05Z"
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO auth_users(id,email,name,display_name,avatar_url,password_hash,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			id, id+"@example.test", id, id, "", "", 1, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		status := "active"
		if index%3 == 0 {
			status = "suspended"
		} else if index%3 == 1 {
			status = "removed"
		}
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at) VALUES(?,?,?,?,?)`, f.data.Workspace.ID, id, "member", status, stamp); err != nil {
			t.Fatal(err)
		}
		if index < 3 {
			if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_departures(workspace_id,user_id,role,joined_at,left_at) VALUES(?,?,?,?,?)`, f.data.Workspace.ID, id, "member", stamp, stamp); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() {
		for _, id := range ids {
			if _, err := f.db.ExecContext(context.Background(), `DELETE FROM workspace_departures WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, id); err != nil {
				t.Error(err)
			}
			if _, err := f.db.ExecContext(context.Background(), `DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, id); err != nil {
				t.Error(err)
			}
			if _, err := f.db.ExecContext(context.Background(), `DELETE FROM auth_users WHERE id=?`, id); err != nil {
				t.Error(err)
			}
		}
	})
	return ids
}

func TestScaleP21IssueEditorFormerSQL(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, reload := range []bool{false, true} {
		t.Run(fmt.Sprint("reload=", reload), func(t *testing.T) {
			f, issue := scaleP21IssuePersistenceFixture(t, nil, nil)
			ids := scaleP21IssueEditorMembers(t, f, 84)
			workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
			if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.editor_sql_authority", "", nil, func(data *domain.Bootstrap) error {
				data.Members = append(data.Members, domain.WorkspaceMember{User: domain.User{ID: "stale-native-former"}, Status: "suspended"})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, ids[0]); err != nil {
				t.Fatal(err)
			}
			view := scaleP21IssuePersistenceView(t, f, true, reload)
			queries := 0
			ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
			read, tx := scaleP21IssueEditorRead(t, ctx, view, workspace, workspaceID, issue)
			if queries != 0 {
				t.Fatal("lazy service performed SQL before callback", queries)
			}
			data, _ := f.storedWorkspace(workspace)
			want, err := readFormerMembers(t.Context(), tx, workspaceID, data.Members)
			got := read.formerMembers()
			if err != nil || read.err != nil || len(got) <= 50 || !reflect.DeepEqual(got, want) || !got[ids[0]] || got[ids[2]] || got["stale-native-former"] {
				t.Fatal("SQL authority, departure, rejoin or full ID-only map differs", err, read.err, len(got))
			}
			beforeQueries, beforeWork := queries, view.MetadataWorkUsedForTesting()
			if !reflect.DeepEqual(read.formerMembers(), want) || queries != beforeQueries || view.MetadataWorkUsedForTesting() != beforeWork {
				t.Fatal("former lookup was not cached")
			}
			read.close()
			read.close()
			if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("SQL map owner leaked work or decoded complete metadata")
			}
			t.Logf("former_ids=%d held_work=%d", len(want), beforeWork)
		})
	}
}

func TestScaleP21IssueEditorFormerNative(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, reload := range []bool{false, true} {
		t.Run(fmt.Sprint("reload=", reload), func(t *testing.T) {
			var held int
			for _, bodyCopies := range []int{1, 10} {
				f, issue := scaleP21IssuePersistenceFixture(t, nil, nil)
				workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
				if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_memberships WHERE workspace_id=?`, workspaceID); err != nil {
					t.Fatal(err)
				}
				if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.editor_members", "", nil, func(data *domain.Bootstrap) error {
					data.Members = []domain.WorkspaceMember{{User: domain.User{ID: ""}, Status: "suspended"}}
					for index := range 82 {
						id := fmt.Sprint("native-former-", index)
						status := "suspended"
						if index == 81 {
							status = "removed"
						}
						data.Members = append(data.Members, domain.WorkspaceMember{
							User:   domain.User{ID: id, AvatarURL: strings.Repeat("unrelated avatar ", 5*bodyCopies)},
							Status: status, Role: strings.Repeat("unrelated role ", 5*bodyCopies),
						})
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				view := scaleP21IssuePersistenceView(t, f, true, reload)
				read, tx := scaleP21IssueEditorRead(t, t.Context(), view, workspace, workspaceID, issue)
				data, _ := f.storedWorkspace(workspace)
				want, err := readFormerMembers(t.Context(), tx, workspaceID, data.Members)
				got := read.formerMembers()
				if err != nil || read.err != nil || len(got) != 82 || !got[""] || got["native-former-81"] || !reflect.DeepEqual(got, want) {
					t.Fatal("native full-ID map or exact suspended/empty-ID behavior differs", err, read.err, len(got))
				}
				work := view.MetadataWorkUsedForTesting()
				if held == 0 {
					held = work
				}
				if work != held {
					t.Fatal("unrelated member/user body growth changed admission", held, work)
				}
				read.close()
				tx.Rollback()
				if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("native former owner leaked or decoded full metadata")
				}
			}
			t.Logf("native_former_ids=82 held_work=%d", held)
		})
	}
}

func TestScaleP21IssueEditorNativeBodyFraming(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f, issue := scaleP21IssuePersistenceFixture(t, nil, nil)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_memberships WHERE workspace_id=?`, workspaceID); err != nil {
		t.Fatal(err)
	}
	var held int
	for _, copies := range []int{1, 10} {
		data := domain.Bootstrap{
			Workspace: f.data.Workspace,
			Members: []domain.WorkspaceMember{
				{User: domain.User{ID: "former", AvatarURL: strings.Repeat("avatar ", copies*100000)}, Status: "suspended"},
				{User: domain.User{ID: "active", AvatarURL: strings.Repeat("active ", copies*100000)}, Status: "active"},
			},
		}
		view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{workspace: data})
		view.db, view.dialect = f.db, f.dialect
		read, tx := scaleP21IssueEditorRead(t, t.Context(), view, workspace, workspaceID, issue)
		if got := read.formerMembers(); read.err != nil || !maps.Equal(got, map[string]bool{"former": true}) {
			t.Fatal("large native bodies prevented ID-only fallback", got, read.err)
		}
		work := view.MetadataWorkUsedForTesting()
		if held == 0 {
			held = work
		}
		if work != held {
			t.Fatal("tenfold native member body growth changed lookup admission", held, work)
		}
		read.close()
		tx.Rollback()
	}
	t.Logf("native_body_bytes=1400000..14000000 held_work=%d", held)
}

func TestScaleP21IssueEditorNativeDynamicAdmission(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f, issue := scaleP21IssuePersistenceFixture(t, nil, nil)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_memberships WHERE workspace_id=?`, workspaceID); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("large-native-identity-", 200000)
	data := domain.Bootstrap{Workspace: f.data.Workspace, Members: []domain.WorkspaceMember{{User: domain.User{ID: id}, Status: "suspended"}}}
	for _, quota := range []bool{false, true} {
		view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{workspace: data})
		view.db, view.dialect = f.db, f.dialect
		read, tx := scaleP21IssueEditorRead(t, t.Context(), view, workspace, workspaceID, issue)
		limit := view.metadataResidency.work.limit
		if quota {
			view.metadataResidency.work.limit = view.metadataResidency.work.used + 128<<10
		}
		got := read.formerMembers()
		if quota {
			if !errors.Is(read.err, ErrMetadataWorkBudget) || len(got) != 0 {
				t.Fatal("native large-ID quota copied output before admission", read.err, len(got))
			}
		} else if read.err != nil || len(got) != 1 || !got[id] || view.MetadataWorkUsedForTesting() <= 16<<20 {
			t.Fatal("legal selected native ID did not receive dynamic admission", read.err, len(got), view.MetadataWorkUsedForTesting())
		}
		view.metadataResidency.work.limit = limit
		read.close()
		tx.Rollback()
	}
}

func TestScaleP21IssueEditorCollaboration(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint("explicit=", explicit), func(t *testing.T) {
			f, issue := scaleP21IssuePersistenceFixture(t, nil, nil)
			documentID := "document_content_" + issue.ID
			if explicit {
				documentID = "explicit-generation"
				issue.DocumentContent = &domain.DocumentContent{ID: documentID}
			}
			for _, item := range []struct {
				id, document, at string
				data             []byte
			}{
				{"update-c", documentID, "2026-11-14T00:00:01Z", []byte("last")},
				{"update-b", documentID, "2026-11-14T00:00:00Z", []byte("tie-last")},
				{"update-a", documentID, "2026-11-14T00:00:00Z", []byte("first")},
				{"update-empty", documentID, "2026-11-14T00:00:02Z", []byte{}},
				{"unrelated", "other-document", "2026-11-14T00:00:00Z", []byte(strings.Repeat("unrelated ", 300000))},
			} {
				if _, err := f.db.ExecContext(t.Context(), `INSERT INTO document_collaboration_updates(update_id,workspace_key,document_id,client_id,update_data,created_at) VALUES(?,?,?,?,?,?)`,
					idgen.New(item.id), f.data.Workspace.URLKey, item.document, "client", item.data, item.at); err != nil {
					t.Fatal(err)
				}
			}
			view := scaleP21IssuePersistenceView(t, f, true, true)
			queries := []string{}
			ctx := WithSQLStatementObserver(t.Context(), func(query string) { queries = append(queries, query) })
			read, tx := scaleP21IssueEditorRead(t, ctx, view, f.data.Workspace.URLKey, f.data.Workspace.ID, issue)
			if len(queries) != 0 {
				t.Fatal("collaboration queried eagerly")
			}
			want, err := collaborationUpdatesLookup(t.Context(), tx, f.data.Workspace.URLKey)(documentID)
			got, gotErr := read.collaborationUpdates(documentID)
			if err != nil || gotErr != nil || len(got) != 4 || !reflect.DeepEqual(got, want) || string(got[0]) != "first" || string(got[1]) != "tie-last" || string(got[2]) != "last" {
				t.Fatal("selected collaboration blob order/empty shape changed", err, gotErr)
			}
			if len(queries) != 2 || !strings.Contains(queries[0], "COUNT(*)") || !strings.HasPrefix(queries[1], "SELECT update_data") {
				t.Fatal("driver byte preflight did not precede blob reads", queries)
			}
			got[0][0] = 'X'
			got[1] = nil
			before := view.MetadataWorkUsedForTesting()
			again, err := read.collaborationUpdates(documentID)
			if err != nil || !reflect.DeepEqual(again, want) || len(queries) != 2 || view.MetadataWorkUsedForTesting() <= before {
				t.Fatal("independent callback blobs alias retained output or lack ownership", err)
			}
			read.close()
			if _, err := read.collaborationUpdates(documentID); !errors.Is(err, ErrMetadataRecordQuery) {
				t.Fatal("closed service still returned retained data", err)
			}
			tx.Rollback()
		})
	}
}

func TestScaleP21IssueEditorDefaultsAndFailure(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, mode := range []string{"empty-workspace", "no-updates", "foreign-document", "sql-failure", "former-quota", "updates-quota", "former-cancel", "updates-cancel"} {
		t.Run(mode, func(t *testing.T) {
			f, issue := scaleP21IssuePersistenceFixture(t, nil, nil)
			view := scaleP21IssuePersistenceView(t, f, true, false)
			workspaceID := f.data.Workspace.ID
			if mode == "empty-workspace" {
				workspaceID = ""
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			queries := []string{}
			ctx = WithSQLStatementObserver(ctx, func(query string) { queries = append(queries, query) })
			read, tx := scaleP21IssueEditorRead(t, ctx, view, f.data.Workspace.URLKey, workspaceID, issue)
			limit := view.metadataResidency.work.limit
			t.Cleanup(func() { view.metadataResidency.work.limit = limit })
			switch mode {
			case "empty-workspace":
				if got := read.formerMembers(); got == nil || len(got) != 0 || read.err != nil || len(queries) != 0 {
					t.Fatal("empty workspace default/SQL laziness changed", got, read.err, queries)
				}
			case "no-updates":
				got, err := read.collaborationUpdates(read.documentID)
				if got != nil || err != nil {
					t.Fatal("no rows lost nil output shape", got, err)
				}
			case "foreign-document":
				_, err := read.collaborationUpdates("undeclared")
				if !errors.Is(err, ErrMetadataRecordQuery) || !errors.Is(read.err, err) || len(queries) != 0 {
					t.Fatal("undeclared lookup silently returned empty", err, read.err, queries)
				}
			case "sql-failure":
				tx.Rollback()
				if got := read.formerMembers(); got == nil || len(got) != 0 || read.err != nil {
					t.Fatal("ordinary SQL failure lost legacy empty-map/warning behavior", got, read.err)
				}
			default:
				if strings.HasSuffix(mode, "cancel") {
					cancel()
				} else {
					view.metadataResidency.work.limit = view.metadataResidency.work.used
				}
				if strings.HasPrefix(mode, "former") {
					_ = read.formerMembers()
				} else {
					_, _ = read.collaborationUpdates(read.documentID)
				}
				want := ErrMetadataWorkBudget
				if strings.HasSuffix(mode, "cancel") {
					want = context.Canceled
				}
				if !errors.Is(read.err, want) {
					t.Fatal("explicit owner failure was swallowed", mode, read.err)
				}
				if slices.ContainsFunc(queries, func(query string) bool {
					return strings.HasPrefix(query, "SELECT update_data") || query == formerMembersQuery
				}) {
					t.Fatal("quota/cancellation copied driver output before admission", queries)
				}
			}
			view.metadataResidency.work.limit = limit
			read.close()
			tx.Rollback()
		})
	}
}

func TestScaleP21IssueEditorVerifierOwnedLookups(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f, issue := scaleP21IssuePersistenceFixture(t, nil, nil)
	ids := scaleP21IssueEditorMembers(t, f, 3)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	if _, err := f.AppendDocumentCollaborationUpdate(t.Context(), workspace, DocumentCollaborationUpdate{
		ID: idgen.New("p21-editor-update"), DocumentID: "document_content_" + issue.ID, Data: []byte("same"), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	view := scaleP21IssuePersistenceView(t, f, true, false)
	read, tx := scaleP21IssueEditorRead(t, t.Context(), view, workspace, workspaceID, issue)
	ctx := withMutationCallbackLookups(t.Context(), view, tx, read.formerMembers, read.collaborationUpdates)
	stored := domain.Bootstrap{Workspace: f.data.Workspace, Issues: []domain.Issue{issue}}
	data := cloneBootstrap(stored)
	calls := 0
	held := 0
	_, err := view.compareMutationCallback(ctx, tx, "issue", stored, &data, nil, nil, func(data *domain.Bootstrap) (string, error) {
		calls++
		if !maps.Equal(data.FormerMembers(), map[string]bool{ids[0]: true, ids[1]: true}) {
			t.Fatal("verifier replaced ID map service")
		}
		updates, err := data.CollaborationUpdates(read.documentID)
		if err != nil || string(updates[0]) != "same" {
			t.Fatal("independent verifier callback lost original blob", err)
		}
		held = view.MetadataWorkUsedForTesting()
		if held <= 128<<10 {
			t.Fatal("lookup output was released before callback use", held)
		}
		limit := view.metadataResidency.work.limit
		view.metadataResidency.work.limit = held
		_, release, admissionErr := view.tryAdmitAdditionalMetadataWork(t.Context(), 1)
		if release != nil {
			release()
		}
		view.metadataResidency.work.limit = limit
		if !errors.Is(admissionErr, ErrMetadataWorkBudget) {
			t.Fatal("callback output did not compete for shared process work", admissionErr)
		}
		updates[0][0] = 'X'
		data.Issues[0].Title = "after"
		return issue.ID, nil
	}, true, false)
	if err != nil || read.err != nil || calls != 2 || data.Issues[0].Title != "after" || stored.Issues[0].Title != issue.Title {
		t.Fatal("real owner/verifier independence failed", err, read.err, calls)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if view.MetadataWorkUsedForTesting() != held || read.check() != nil {
		t.Fatal("callback output was released before commit completed", view.MetadataWorkUsedForTesting(), held, read.err)
	}
	read.close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("committed callback owner leaked output")
	}
}
