package store

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestBoundedSavedViewScopedCascade(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := journalScaleDatabase(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	const target, keep = "p3-cascade", "p3-keep"
	const cascades = MetadataRecordBatchMaxKeys + 10
	large := strings.Repeat("x", 900<<10)
	if err := f.MutateWorkspace(t.Context(), workspace, "view.created", target, nil, func(data *domain.Bootstrap) error {
		data.SavedViews = append(data.SavedViews,
			domain.SavedView{ID: target, SlugID: "p3-cascade-slug", ShareToken: "p3-cascade-token", Description: strings.Repeat(large, 3)},
			domain.SavedView{ID: keep, ShareToken: "p3-keep-token"},
		)
		for i := range cascades {
			id := fmt.Sprintf("p3-cascade-%d", i)
			data.Favorites = append(data.Favorites, domain.Favorite{ID: id, UserID: data.Viewer.ID, ResourceType: "view", ResourceID: target})
			data.Subscriptions = append(data.Subscriptions, domain.Subscription{ID: id, UserID: data.Viewer.ID, ResourceType: "view", ResourceID: target})
		}
		for i := range 3 {
			id := fmt.Sprintf("p3-unrelated-%d", i)
			data.SavedViews = append(data.SavedViews, domain.SavedView{ID: id, Description: large})
			data.Favorites = append(data.Favorites, domain.Favorite{ID: id, ResourceType: "view", ResourceID: keep, FolderID: large})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	peer, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.releaseMetadataLogicalSources()
	baseline := scaleHead(t, f)
	checkLookup := func(s *SQLiteStore, token string, found bool) {
		t.Helper()
		data, closeRead, err := s.BeginSharedSavedViewRead(t.Context(), workspace, token)
		if err != nil {
			t.Fatal(err)
		}
		defer closeRead()
		if got := len(data.SavedViews) == 1; got != found {
			t.Error("unexpected shared-view visibility")
		}
	}
	checkRows := func(want int) {
		t.Helper()
		var count int
		err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records
			WHERE workspace_key=? AND field IN ('favorites','subscriptions','savedViews') AND record_key LIKE ?`,
			workspace, target+"%").Scan(&count)
		if err != nil || count != want {
			t.Fatalf("selected canonical row count %d, want %d (query error %v)", count, want, err)
		}
	}
	checkLookup(view, "p3-cascade-token", true)
	checkLookup(peer, "p3-cascade-token", true)
	callbacks := 0
	write := func() error {
		return view.MutateSavedViewRecords(t.Context(), workspace, "view.deleted", "p3-cascade-slug", nil, nil, func(data *domain.Bootstrap) (string, error) {
			callbacks++
			if len(data.SavedViews) != 1 || data.SavedViews[0].ID != target || len(data.Favorites) != cascades || len(data.Subscriptions) != cascades {
				return "", errors.New("cascade callback received an unexpected record scope")
			}
			data.SavedViews = slices.DeleteFunc(data.SavedViews, func(v domain.SavedView) bool { return v.ID == target })
			data.Favorites = slices.DeleteFunc(data.Favorites, func(v domain.Favorite) bool { return v.ResourceType == "view" && v.ResourceID == target })
			data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(v domain.Subscription) bool { return v.ResourceType == "view" && v.ResourceID == target })
			return "p3-cascade-slug", nil
		})
	}
	published := 0
	view.SetRealtimeSink(func(string, domain.RealtimeEvent) { published++ })
	previous := f.db.commitHooks.Load()
	defer f.db.commitHooks.Store(previous)
	failure := errors.New("reject saved-view commit")
	f.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) {
		if tx.metadataWritten {
			tx.writeErr = failure
		}
	}})
	if err := write(); !errors.Is(err, failure) {
		t.Fatalf("cascade did not reach injected rollback after %d callbacks: %v", callbacks, err)
	}
	f.db.commitHooks.Store(previous)
	checkRows(2*cascades + 1)
	checkLookup(view, "p3-cascade-token", true)
	checkLookup(peer, "p3-cascade-token", true)
	if published != 0 || scaleHead(t, f) != baseline {
		t.Fatal("rolled-back cascade reached realtime or the journal")
	}
	attempts := 0
	callbacks = 0
	f.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) {
		if tx.metadataWritten {
			attempts++
			if attempts == 1 {
				tx.writeErr = ErrConcurrentWrite
			}
		}
	}})
	if err := write(); err != nil {
		t.Fatalf("cascade retry failed after %d callbacks and %d commit attempts: %v", callbacks, attempts, err)
	}
	f.db.commitHooks.Store(previous)
	if callbacks != 2 || attempts != 2 || published != 1 {
		t.Fatalf("wanted one publication after retry, got %d callbacks, %d commit attempts and %d publications", callbacks, attempts, published)
	}
	checkRows(0)
	checkLookup(view, "p3-cascade-token", false)
	checkLookup(view, "p3-keep-token", true)
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("scoped cascade leaked work or decoded the legacy workspace")
	}
	page, err := peer.ReadCommittedChanges(t.Context(), workspaceID, baseline, 50, 1<<20)
	if err != nil || page.ResyncRequired || len(page.Transactions) != 1 {
		t.Fatal("cascade did not produce one journal transaction", err)
	}
	deleted := 0
	for _, record := range page.Transactions[0].Records {
		if record.Deleted && strings.HasPrefix(record.Key.ID, target) {
			deleted++
		}
	}
	if deleted != 2*cascades+1 {
		t.Errorf("journal has %d cascade tombstones, want %d", deleted, 2*cascades+1)
	}
	if err := peer.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal("reload peer after cascade", err)
	}
	checkLookup(peer, "p3-cascade-token", false)
	checkLookup(peer, "p3-keep-token", true)
	statements := 0
	ctx := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
	_, release, err := peer.BeginSharedSavedViewRead(ctx, workspace, "p3-keep-token")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if statements != 1 {
		t.Errorf("warm shared-view read issued %d statements, want one stamp check", statements)
	}
}

func TestBoundedSavedViewRejectsStaleAuthorization(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := journalScaleDatabase(t)
	workspace := f.data.Workspace.URLKey
	actor := f.data.Viewer
	ctx := ContextWithActor(t.Context(), actor)
	const id, token = "p3-stale", "p3-stale-token"
	if err := f.MutateWorkspace(ctx, workspace, "view.created", id, nil, func(data *domain.Bootstrap) error {
		data.SavedViews = append(data.SavedViews, domain.SavedView{ID: id, OwnerID: actor.ID, Scope: "personal", ShareToken: token})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	access, err := view.ResolveAccess(ctx, workspace, actor.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	read, err := view.BeginRecordAuthorizationRead(ctx, access, "savedViews", id)
	if err != nil {
		t.Fatal(err)
	}
	proof := NewSavedViewAuthorization(access, id, read.Data.SavedViews)
	read.Close()
	if err := f.MutateWorkspace(ctx, workspace, "view.updated", id, nil, func(data *domain.Bootstrap) error {
		for i := range data.SavedViews {
			if data.SavedViews[i].ID == id {
				data.SavedViews[i].OwnerID = "p3-other-owner"
				data.SavedViews[i].ShareToken = ""
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err = view.MutateSavedViewRecords(ctx, workspace, "view.updated", id, domain.SavedViewMutationInput{}, proof, func(*domain.Bootstrap) (string, error) {
		t.Error("mutation ran with stale authorization")
		return id, nil
	})
	if !errors.Is(err, ErrResourceAccessChanged) {
		t.Fatal("stale authorization was not rejected", err)
	}
	data, release, err := view.BeginSharedSavedViewRead(ctx, workspace, token)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if len(data.SavedViews) != 0 {
		t.Error("revoked token remained readable")
	}
}

func TestBoundedSavedViewSQLiteCredentialChurn(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := journalScaleDatabase(t)
	if f.dialect != "sqlite" {
		t.Skip("SQLite policy epoch")
	}
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	actor := f.data.Viewer
	ctx := ContextWithActor(t.Context(), actor)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	authorize := func(key *domain.APIKey) *SavedViewAuthorization {
		t.Helper()
		access, err := view.ResolveAccess(ctx, workspace, actor.ID, key)
		if err != nil {
			t.Fatal(err)
		}
		return NewSavedViewAuthorization(access, "", nil)
	}
	session, bearer := authorize(nil), authorize(&domain.APIKey{ID: "p3-existing-key"})
	if _, err := f.db.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data)
		VALUES(?,'apiKeys','p3-unrelated-key',0,'{"id":"p3-unrelated-key"}')`, workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `DELETE FROM workspace_metadata_records
		WHERE workspace_key=? AND field='apiKeys' AND record_key='p3-unrelated-key'`, workspace); err != nil {
		t.Fatal(err)
	}
	if current := view.SessionAccessEpoch(ctx, session.Access); current == "" || current == session.Access.AuthzEpoch {
		t.Fatal("fixture did not advance the epoch through credential writes")
	}
	create := func(proof *SavedViewAuthorization, id string, allowed bool) error {
		t.Helper()
		return view.MutateSavedViewRecords(ctx, workspace, "view.created", "", domain.SavedViewMutationInput{}, proof, func(data *domain.Bootstrap) (string, error) {
			if !allowed {
				t.Error("saved view callback ran with stale authorization")
			}
			data.SavedViews = append(data.SavedViews, domain.SavedView{ID: id, OwnerID: actor.ID, Scope: "personal"})
			return id, nil
		})
	}
	if err := create(bearer, "p3-stale-bearer", false); !errors.Is(err, ErrResourceAccessChanged) {
		t.Fatalf("stale bearer proof returned %v", err)
	}
	if err := create(session, "p3-session-view", true); err != nil {
		t.Fatalf("credential churn invalidated a session saved-view write: %v", err)
	}
	session = authorize(nil)
	if _, err := f.db.ExecContext(ctx, `UPDATE workspace_memberships SET role='guest' WHERE workspace_id=? AND user_id=?`, workspaceID, actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := create(session, "p3-stale-session", false); !errors.Is(err, ErrResourceAccessChanged) {
		t.Fatalf("stale session proof after membership change returned %v", err)
	}
}

func TestBoundedSavedViewTypedShareHeaders(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := journalScaleDatabase(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "view.created", "p3-typed-view", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: "p3-typed-team", Key: "P3SHARE"})
		data.Projects = append(data.Projects, domain.Project{ID: "p3-typed-project"})
		data.SavedViews = append(data.SavedViews, domain.SavedView{ID: "p3-typed-view"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ field, id, raw string }{
		{"teams", "p3-typed-team", `{"id":"p3-typed-team","key":"P3SHARE","private":false,"Private":true}`},
		{"projects", "p3-typed-project", `{"id":"p3-typed-project","teamIds":[],"TeamIDs":["p3-typed-team"]}`},
		{"savedViews", "p3-typed-view", `{"id":"p3-typed-view","scope":"workspace","ShareToken":"p3-old","shareToken":"p3-current","ProjectID":"p3-typed-project"}`},
	} {
		if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field=? AND record_key=?`,
			[]byte(row.raw), workspace, row.field, row.id); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	data, release, err := view.BeginSharedSavedViewRead(t.Context(), workspace, "p3-current")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if len(data.SavedViews) != 1 || len(data.Projects) != 1 || len(data.Teams) != 1 || !data.Teams[0].Private {
		t.Fatal("typed token and privacy headers did not use the last matching JSON field")
	}
}
