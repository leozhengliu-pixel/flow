package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW308ResourceAuthorizationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	prefix := idgen.New("sw308-")
	public, private, child := prefix+"public", prefix+"private", prefix+"child"
	target, linked := prefix+"project", prefix+"initiative"
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams", "teamSettings", "projects", "initiatives"), workspace, "scale_p21.sw308.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: public}, domain.Team{ID: private, Private: true}, domain.Team{ID: child})
		data.TeamSettings[public] = domain.TeamSettings{TeamID: public, Access: "public"}
		data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
		data.TeamSettings[child] = domain.TeamSettings{TeamID: child, ParentTeamID: private, Access: "public"}
		data.Projects = append(data.Projects,
			domain.Project{ID: target, SlugID: prefix + "current-123456789abc", PreviousSlugIDs: []string{prefix + "old"}, TeamIDs: []string{public}},
			domain.Project{ID: prefix + "secret", SlugID: prefix + "collision", TeamIDs: []string{private}},
			domain.Project{ID: prefix + "alias", PreviousSlugIDs: []string{prefix + "collision"}, TeamIDs: []string{public}},
			domain.Project{ID: prefix + "inherited", TeamIDs: []string{child}},
			domain.Project{ID: prefix + "unscoped"},
		)
		data.Initiatives = append(data.Initiatives,
			domain.Initiative{ID: linked, PreviousSlugIDs: []string{prefix + "initiative-old"}, ProjectIDs: []string{target, prefix + "secret"}},
			domain.Initiative{ID: prefix + "initiative-secret", LeadTeamID: private},
			domain.Initiative{ID: prefix + "initiative-duplicate", ProjectIDs: []string{target, target, prefix + "secret"}},
			domain.Initiative{ID: prefix + "initiative-unscoped"},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	member := func(name, role string, teams []string) domain.User {
		t.Helper()
		user, token, err := f.Register(t.Context(), name, idgen.New("sw308-user-")+"@example.test", "sw308-password")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = f.ErasePersonFromWorkspace(context.Background(), workspace, user.ID)
			_ = f.EraseAccount(context.Background(), user.ID)
		})
		if err := f.VerifyEmail(t.Context(), token); err != nil {
			t.Fatal(err)
		}
		invitation, err := f.Invite(t.Context(), workspaceID, f.data.Viewer.ID, user.Email, role, teams)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.AcceptInvitation(t.Context(), invitation.Token, user.ID); err != nil {
			t.Fatal(err)
		}
		return user
	}
	users := []domain.User{
		f.data.Viewer,
		member("Private admin", "admin", []string{private}),
		member("Public member", "member", []string{public}),
		member("Explicit guest", "guest", []string{public}),
		member("Inherited member", "member", []string{private}),
	}
	keys := []*domain.APIKey{
		nil,
		{ID: prefix + "unrestricted", Scopes: []string{"read"}},
		{ID: prefix + "selected-empty", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}},
		{ID: prefix + "selected-set", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{public}},
		{ID: prefix + "app", Actor: "app", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{public}},
	}
	refs := []struct{ kind, ref string }{
		{"projects", target}, {"projects", prefix + "old"}, {"projects", prefix + "collision"},
		{"projects", prefix + "inherited"}, {"projects", prefix + "unscoped"},
		{"projects", prefix + "duplicate"}, {"projects", "missing"}, {"projects", prefix + "other-123456789abc"},
		{"initiatives", linked}, {"initiatives", prefix + "initiative-old"},
		{"initiatives", prefix + "initiative-secret"}, {"initiatives", prefix + "initiative-duplicate"},
		{"initiatives", prefix + "initiative-unscoped"}, {"initiatives", "missing"},
	}
	// Membership writes can invalidate the owner's installed-copy revision.
	// Certify the warm generation after those fixture writes have completed.
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"warm", "sql-reloaded", "canonical"} {
		if state == "canonical" {
			f.mu.Lock()
			f.forgetCopyRevisionLocked(workspace)
			f.mu.Unlock()
		}
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		if state == "sql-reloaded" {
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), workspace, "scale_p21.sw308.reload", "", nil, func(data *domain.Bootstrap) error {
				for index := range data.Projects {
					if data.Projects[index].ID == target {
						data.Projects[index].TeamIDs = []string{private}
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before := view.metadataResidency.logical[workspace].Stamp()
			statements := 0
			if err := view.reloadResidentWorkspace(WithSQLStatementObserver(t.Context(), func(string) { statements++ }), workspace); err != nil {
				t.Fatal(err)
			}
			after := view.metadataResidency.logical[workspace].Stamp()
			if statements == 0 || after == before {
				t.Fatal("SQL-reloaded case reused its original installed generation", statements, before, after)
			}
		}
		for userIndex, user := range users {
			for keyIndex, key := range keys {
				for _, item := range refs {
					literal, err := f.ResolveAccess(t.Context(), workspace, user.ID, key)
					if err != nil {
						t.Fatal(err)
					}
					_, want, oldErr := f.ResourceReadMetadata(t.Context(), literal, item.kind, item.ref)
					access, err := view.ResolveAccess(t.Context(), workspace, user.ID, key)
					if err != nil {
						t.Fatal(err)
					}
					if state == "canonical" && view.metadataResidency.logical[workspace].Stamp() == access.policy.stamp {
						t.Fatal("canonical case reused a certified native generation")
					}
					if state != "canonical" && view.metadataResidency.logical[workspace].Stamp() != access.policy.stamp {
						t.Fatal("warm/reloaded case did not retain a certified native generation")
					}
					sqlCount := 0
					var observed []string
					read, err := view.BeginResourceAuthorizationRead(WithSQLStatementObserver(t.Context(), func(query string) {
						sqlCount++
						observed = append(observed, query)
					}), access, item.kind, item.ref)
					if err != nil || oldErr != nil || read == nil || read.Allowed != want {
						t.Fatalf("%s user=%d key=%d kind=%s ref=%q: read=%+v literal=%t bounded-error=%v literal-error=%v", state, userIndex, keyIndex, item.kind, item.ref, read, want, err, oldErr)
					}
					if state != "canonical" && sqlCount > 1 {
						t.Fatalf("installed authorization loaded detail/reference SQL: state=%s native=%t count=%d queries=%q", state, view.metadataResidency.logical[workspace].Stamp() == access.policy.stamp, sqlCount, observed)
					}
					read.Close()
					read.Close()
					if read.Allowed || view.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("authorization ownership did not release", view.MetadataWorkUsedForTesting())
					}
				}
			}
		}
		if view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("authorization hydrated the full workspace")
		}
		view.releaseMetadataLogicalSources()
	}
}

func TestScaleP21SW308ResourceAuthorizationNativeDuplicates(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	workspace, team := f.data.Workspace.URLKey, f.data.Teams[0].ID
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	access, err := view.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, &domain.APIKey{ID: "sw308-native-key", TeamRestriction: "selected", TeamIDs: []string{team}, Scopes: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	data := domain.Bootstrap{
		Projects: []domain.Project{
			{ID: "duplicate", SlugID: "alias", TeamIDs: []string{}},
			{ID: "selected", PreviousSlugIDs: []string{"alias"}, TeamIDs: []string{team}},
			{ID: "duplicate", TeamIDs: []string{team}},
		},
		Initiatives: []domain.Initiative{{ID: "parent", ProjectIDs: []string{"duplicate"}}},
	}
	source, err := captureMetadataLogicalSource(t.Context(), data, access.policy.stamp)
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource(workspace, source)
	for _, item := range []struct{ kind, ref string }{{"projects", "alias"}, {"projects", "duplicate"}, {"initiatives", "parent"}} {
		_, want, err := resourceReadMetadata(access.policy, data, item.kind, item.ref)
		if err != nil {
			t.Fatal(err)
		}
		read, err := view.BeginResourceAuthorizationRead(t.Context(), access, item.kind, item.ref)
		if err != nil || read.Allowed != want {
			t.Fatal("native duplicate selection changed", item, err, want)
		}
		read.Close()
	}
}

func TestScaleP21SW308CanonicalHeadersAndLargeOwnership(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	for _, large := range []bool{false, true} {
		t.Run(fmt.Sprintf("large=%t", large), func(t *testing.T) {
			body := "small"
			if large {
				body = strings.Repeat("body", MetadataRecordPageMaxBytes)
			}
			id := idgen.New("sw308-header-")
			raw := []byte(`{"id":"` + id + `","teamIds":["first"],"TEAMIDS":null,"TeamIds":["last"],"slugId":"keep","SLUGID":null,"previousSlugIds":["old"],"PreviousSlugIds":null,"description":"` + body + `"}`)
			var literal domain.Project
			if err := json.Unmarshal(raw, &literal); err != nil {
				t.Fatal(err)
			}
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'projects',?,999,?)`, workspace, id, raw); err != nil {
				t.Fatal(err)
			}
			owner := &ResourceAuthorizationRead{owner: &ResourceRead{store: view}}
			defer owner.Close()
			statements := 0
			ctx := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
			value, err := owner.header(ctx, tx, workspace, "projects", id, reflect.TypeFor[domain.Project](), resourceAuthorizationProjectFields)
			if err != nil {
				t.Fatal(err)
			}
			got := value.Interface().(domain.Project)
			literal.Description = ""
			if !reflect.DeepEqual(got, literal) {
				t.Fatal("header changed repeated/folded/null typed decoding", got, literal)
			}
			wantSQL := 1
			if large {
				wantSQL += (len(raw) + metadataRecordChunkBytes - 1) / metadataRecordChunkBytes
			}
			if statements != wantSQL {
				t.Fatalf("small/large row query bound changed: got=%d want=%d", statements, wantSQL)
			}
			held := view.MetadataWorkUsedForTesting()
			if held == 0 || held > 1<<20 {
				t.Fatal("body escaped into retained authorization work", held)
			}
			owner.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("header retained work after Close")
			}
			if large {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				ctx = WithSQLStatementObserver(ctx, func(query string) {
					if strings.Contains(query, "SUBSTR(") {
						cancel()
					}
				})
				cancelled := &ResourceAuthorizationRead{owner: &ResourceRead{store: view}}
				if _, err := cancelled.header(ctx, tx, workspace, "projects", id, reflect.TypeFor[domain.Project](), resourceAuthorizationProjectFields); !errors.Is(err, context.Canceled) {
					t.Fatal("large header ignored cancellation", err)
				}
				cancelled.Close()
				if view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("cancelled header retained work")
				}
			}
		})
	}
}

func TestScaleP21SW308ResourceAuthorizationReleaseAndAuthority(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	target := idgen.New("sw308-owned-")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), workspace, "scale_p21.sw308.owned", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: target, TeamIDs: []string{data.Teams[0].ID}, Description: strings.Repeat("body", 300000)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	access, err := view.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := view.BeginResourceAuthorizationRead(t.Context(), access, "projects", target)
	if err != nil || !first.Allowed {
		t.Fatal("first owned authorization failed", err)
	}
	firstWork := view.MetadataWorkUsedForTesting()
	second, err := view.BeginResourceAuthorizationRead(t.Context(), access, "projects", target)
	if err != nil || !second.Allowed {
		t.Fatal("second owned authorization failed", err)
	}
	if view.MetadataWorkUsedForTesting() <= firstWork {
		t.Fatal("authorization readers did not independently own admission")
	}
	var group sync.WaitGroup
	for range 4 {
		group.Go(first.Close)
	}
	group.Wait()
	if !second.Allowed || view.MetadataWorkUsedForTesting() != firstWork {
		t.Fatal("closing first read affected the second", view.MetadataWorkUsedForTesting(), firstWork)
	}
	second.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := view.BeginResourceAuthorizationRead(ctx, access, "projects", target); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled authorization was not cancelled", err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	if _, err := view.BeginResourceAuthorizationRead(t.Context(), access, "projects", target); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("authorization ignored admission", err)
	}
	view.metadataResidency.work.limit = limit
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = f.db.ExecContext(context.Background(), `UPDATE workspace_memberships SET status='active' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID)
	}()
	for _, stage := range []string{"queued", "replayed"} {
		if _, err := view.BeginResourceAuthorizationRead(t.Context(), access, "projects", target); !errors.Is(err, ErrResourceAccessChanged) || !errors.Is(err, ErrAuthForbidden) {
			t.Fatal("stale authorization survived "+stage, err)
		}
	}
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("authorization failure leaked work or hydrated workspace")
	}
}

func TestScaleP21SW308ResourceAuthorizationBorrowedSnapshot(t *testing.T) {
	for _, journal := range []string{"off", "on"} {
		t.Run(journal, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", "off")
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			t.Setenv("FLOW_RECORD_JOURNAL", journal)
			f := scaleP09Database(t)
			workspace, target := f.data.Workspace.URLKey, idgen.New("sw308-snapshot-")
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), workspace, "scale_p21.sw308.snapshot", "", nil, func(data *domain.Bootstrap) error {
				data.Projects = append(data.Projects, domain.Project{ID: target, TeamIDs: []string{data.Teams[0].ID}})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			f.forgetCopyRevisionLocked(workspace)
			f.mu.Unlock()
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			f.db.SetMaxOpenConns(1)
			tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
			access, err := view.ResolveAccess(ctx, workspace, f.data.Viewer.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			literal, err := f.ResolveAccess(ctx, workspace, f.data.Viewer.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, want, oldErr := f.ResourceReadMetadata(ctx, literal, "projects", target)
			read, err := view.BeginResourceAuthorizationRead(ctx, access, "projects", target)
			if journal == "on" {
				if !errors.Is(oldErr, ErrRecordJournalUnavailable) || !errors.Is(err, oldErr) || read != nil {
					t.Fatal("pool-one live authority boundary changed", err, oldErr)
				}
			} else {
				if err != nil || oldErr != nil || read == nil || read.Allowed != want {
					t.Fatal("borrowed canonical authorization changed", err, oldErr, want)
				}
				read.Close()
			}
			var alive int
			if err := tx.QueryRowContext(t.Context(), "SELECT 1").Scan(&alive); err != nil || alive != 1 {
				t.Fatal("authorization closed caller's transaction", err)
			}
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("borrowed authorization retained work")
			}
		})
	}
}
