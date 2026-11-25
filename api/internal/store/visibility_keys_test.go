package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW22VisibilityKeysDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	prefix := idgen.New("sw22-visibility-")
	public, private, child := prefix+"public", prefix+"private", prefix+"child"
	retired, archived := prefix+"retired", prefix+"archived"
	at := time.Now().UTC()
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams", "teamSettings", "projects"), workspace, "scale_p21_sw22.visibility_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams,
			domain.Team{ID: public, Key: "V22P"},
			domain.Team{ID: private, Key: "V22S", Private: true},
			domain.Team{ID: child, Key: "V22C"},
			domain.Team{ID: retired, Key: "V22R", RetiredAt: &at},
			domain.Team{ID: archived, Key: "V22A", ArchivedAt: &at})
		data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private", AgentGuidance: strings.Repeat("unrelated guidance ", 6000)}
		data.TeamSettings[child] = domain.TeamSettings{TeamID: child, ParentTeamID: private, Access: "public"}
		data.Projects = append(data.Projects, domain.Project{ID: idgen.New("sw22-visibility-project-"), Description: strings.Repeat("unrelated project ", 50000)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	addMember := func(name, role string, teamIDs []string) domain.User {
		t.Helper()
		user, token, err := f.Register(t.Context(), name, idgen.New("sw22-visibility-user-")+"@example.test", "sw22-visibility-password")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := f.ErasePersonFromWorkspace(ctx, workspace, user.ID); err != nil {
				t.Error(err)
			}
			if err := f.EraseAccount(ctx, user.ID); err != nil {
				t.Error(err)
			}
		})
		if err := f.VerifyEmail(t.Context(), token); err != nil {
			t.Fatal(err)
		}
		invite, err := f.Invite(t.Context(), workspaceID, f.data.Viewer.ID, user.Email, role, teamIDs)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.AcceptInvitation(t.Context(), invite.Token, user.ID); err != nil {
			t.Fatal(err)
		}
		return user
	}
	member := addMember("Member private outsider", "member", []string{public})
	guest := addMember("Guest explicit membership", "guest", []string{public})
	inherited := addMember("Inherited parent member", "member", []string{private})
	admin := addMember("Private admin", "admin", []string{private})
	directory := addMember("Directory member", "member", []string{retired})
	if err := f.SetTeamMembership(t.Context(), workspaceID, archived, directory.ID, "member", true); err != nil {
		t.Fatal(err)
	}
	var extra []string
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams", "teamSettings"), workspace, "scale_p21_sw22.visibility_headers", "", nil, func(data *domain.Bootstrap) error {
		extra = nil
		for index := range 140 {
			id := idgen.New("sw22-visibility-many-")
			extra = append(extra, id)
			data.Teams = append(data.Teams, domain.Team{ID: id, Private: index%2 == 0})
			data.TeamSettings[id] = domain.TeamSettings{TeamID: id, Access: "public"}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	membershipTx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer membershipTx.Rollback()
		for _, team := range extra {
			for _, user := range []domain.User{f.data.Viewer, admin, member, guest, inherited, directory} {
				if _, err := membershipTx.ExecContext(t.Context(), `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,'member',?)`, workspaceID, team, user.ID, at.Format(time.RFC3339Nano)); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := membershipTx.Commit(); err != nil {
			t.Fatal(err)
		}
	}()
	other, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "SW22 visibility other", idgen.New("sw22-visibility-other-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.DeleteWorkspace(context.Background(), other.Workspace.URLKey); err != nil {
			t.Error(err)
		}
	})
	app, err := f.InstallApplication(t.Context(), workspace, domain.ApplicationInstallation{
		ClientID: idgen.New("sw22_visibility_vendor_"), Name: "ZZ Visibility app", Active: true,
		AllPublicTeams: true, InstalledBy: f.data.Viewer.ID, Scopes: []string{"read"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		for _, statement := range []string{
			`DELETE FROM team_memberships WHERE user_id=?`,
			`DELETE FROM workspace_memberships WHERE user_id=?`,
			`DELETE FROM application_installations WHERE user_id=?`,
			`DELETE FROM auth_users WHERE id=?`,
		} {
			if _, err := f.db.ExecContext(ctx, statement, app.UserID); err != nil {
				t.Error(err)
			}
		}
	})
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	compare := func(ctx context.Context, key string, check func(map[string]string)) {
		t.Helper()
		want, wantErr := f.ViewerVisibilityKeys(ctx, key)
		read, gotErr := view.BeginViewerVisibilityKeysRead(ctx, key)
		var got map[string]string
		if read != nil {
			defer read.Close()
			got = read.Keys
		}
		if !reflect.DeepEqual(want, got) || (wantErr == nil) != (gotErr == nil) {
			t.Fatalf("visibility key=%q: got=%v/%v want=%v/%v", key, got, gotErr, want, wantErr)
		}
		if wantErr != nil && wantErr.Error() != gotErr.Error() {
			t.Fatalf("visibility error changed: got=%v want=%v", gotErr, wantErr)
		}
		if check != nil {
			check(got)
		}
	}
	compare(t.Context(), workspace, func(keys map[string]string) {
		for _, user := range []domain.User{f.data.Viewer, admin, member, guest, inherited, directory} {
			if keys[user.ID] == "" {
				t.Fatal("active principal missing from global visibility map", user.ID)
			}
		}
		if keys[f.data.Viewer.ID] == keys[admin.ID] || strings.Contains(keys[member.ID], private) || strings.Contains(keys[guest.ID], private) {
			t.Fatal("fixture did not distinguish private content and directory rows")
		}
		adminParts := strings.Split(keys[f.data.Viewer.ID], "|")
		if len(adminParts) != 3 || !strings.Contains(adminParts[1], private) || strings.Contains(adminParts[2], private) {
			t.Fatal("private outsider admin's row/content keys were collapsed", keys[f.data.Viewer.ID])
		}
		if !strings.Contains(keys[inherited.ID], child) || !strings.Contains(keys[directory.ID], retired) || strings.Contains(keys[directory.ID], archived) {
			t.Fatal("inherited, retired or archived team policy changed")
		}
	})
	func() {
		// The named-field legacy read has a zero Viewer even if the
		// installed base viewer is an app with implicit public-team access.
		f.mu.Lock()
		oldLegacy := f.workspaces[workspace]
		appLegacy := oldLegacy
		appLegacy.Viewer = app.User()
		f.workspaces[workspace] = appLegacy
		f.mu.Unlock()
		view.mu.Lock()
		oldNative := view.workspaces[workspace]
		appNative := oldNative
		appNative.Viewer = app.User()
		view.workspaces[workspace] = appNative
		view.mu.Unlock()
		defer func() {
			f.mu.Lock()
			f.workspaces[workspace] = oldLegacy
			f.mu.Unlock()
			view.mu.Lock()
			view.workspaces[workspace] = oldNative
			view.mu.Unlock()
		}()
		compare(t.Context(), workspace, func(keys map[string]string) {
			if keys[app.UserID] != "app|" {
				t.Fatal("named-field read acquired the base app viewer's grants", keys[app.UserID])
			}
		})
	}()
	compare(t.Context(), other.Workspace.URLKey, func(keys map[string]string) {
		if _, leaked := keys[member.ID]; leaked {
			t.Fatal("visibility keys crossed workspace membership")
		}
	})
	compare(t.Context(), "", nil)
	for _, alias := range []string{workspace + "\x00", strings.ToUpper(workspace), workspace + "-missing"} {
		compare(t.Context(), alias, nil)
	}
	for _, status := range []string{"suspended", "active", "suspended", "active"} {
		if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status=? WHERE workspace_id=? AND user_id=?`, status, workspaceID, guest.ID); err != nil {
			t.Fatal(err)
		}
		compare(t.Context(), workspace, func(got map[string]string) {
			_, exists := got[guest.ID]
			if exists != (status == "active") {
				t.Fatal("queued/replayed visibility retained revoked membership", status, got[guest.ID])
			}
		})
	}
	accountTx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer accountTx.Rollback()
		if _, err := accountTx.ExecContext(t.Context(), `UPDATE auth_users SET active=0,deleted_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), member.ID); err != nil {
			t.Fatal(err)
		}
		ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: accountTx})
		compare(ctx, workspace, func(got map[string]string) {
			if got[member.ID] == "" {
				t.Fatal("active membership incorrectly inherited account-active filtering")
			}
		})
	}()
	for _, raw := range [][]byte{
		[]byte(`{"active":"wrong"}`),
		[]byte(`{"userId":`),
		[]byte(`null`),
		{},
		nil,
	} {
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer tx.Rollback()
			if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, workspaceID, app.UserID); err != nil {
				t.Fatal(err)
			}
			if raw == nil {
				_, err = tx.ExecContext(t.Context(), `DELETE FROM application_installations WHERE user_id=?`, app.UserID)
			} else {
				_, err = tx.ExecContext(t.Context(), `UPDATE application_installations SET data=? WHERE user_id=?`, raw, app.UserID)
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
			compare(ctx, workspace, nil)
		}()
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer tx.Rollback()
		// Decorated installation UserID can differ from the lookup column in
		// imported data. Keep legacy last-key-wins and membership lookup.
		copy := app
		copy.UserID = f.data.Viewer.ID
		raw, err := json.Marshal(copy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), `UPDATE application_installations SET data=? WHERE user_id=?`, raw, app.UserID); err != nil {
			t.Fatal(err)
		}
		ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
		compare(ctx, workspace, nil)
	}()
	appBudgetTx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer appBudgetTx.Rollback()
		large, err := json.Marshal(map[string]string{"userId": app.UserID, "unrelated": strings.Repeat("\u4e2d", scaleP21SW22VisibilityApp/2)})
		if err != nil {
			t.Fatal(err)
		}
		update := `UPDATE application_installations SET data=? WHERE user_id=?`
		if f.dialect == "sqlite" {
			update = `UPDATE application_installations SET data=CAST(? AS TEXT) WHERE user_id=?`
		}
		if _, err := appBudgetTx.ExecContext(t.Context(), update, large, app.UserID); err != nil {
			t.Fatal(err)
		}
		ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: appBudgetTx})
		if _, err := view.BeginViewerVisibilityKeysRead(ctx, workspace); !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatal("oversized app body transfer was not preflighted", err)
		}
	}()
	if f.dialect == "sqlite" {
		textTx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer textTx.Rollback()
			medium, err := json.Marshal(map[string]string{"userId": app.UserID, "unrelated": strings.Repeat("\u4e2d", 100000)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := textTx.ExecContext(t.Context(), `UPDATE application_installations SET data=CAST(? AS TEXT) WHERE user_id=?`, medium, app.UserID); err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: textTx})
			compare(ctx, workspace, nil)
		}()
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := view.BeginViewerVisibilityKeysRead(cancelled, workspace); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled visibility scan", err)
	}
	f.db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	compare(ctx, workspace, nil)
	if view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("visibility key helper decoded full metadata")
	}
}

func TestScaleP21SW22VisibilityKeysBoundsAndRecovery(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	workspace := f.data.Workspace.URLKey
	view.mu.Lock()
	original := view.workspaces[workspace]
	large := original
	large.Teams = []domain.Team{{ID: "large-header", Key: strings.Repeat("x", scaleP21SW22VisibilityPolicy)}}
	view.workspaces[workspace] = large
	view.mu.Unlock()
	_, budgetErr := view.BeginViewerVisibilityKeysRead(t.Context(), workspace)
	view.mu.Lock()
	view.workspaces[workspace] = original
	view.mu.Unlock()
	if !errors.Is(budgetErr, ErrMetadataWorkBudget) {
		t.Fatal("oversized policy graph was not rejected", budgetErr)
	}
	if f.dialect == "sqlite" {
		// External drivers enforce VARCHAR(2048); SQLite needs the transfer
		// guard even for a raw row outside the validated account contract.
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer tx.Rollback()
			if _, err := tx.ExecContext(t.Context(), `UPDATE auth_users SET avatar_url=? WHERE id=?`, strings.Repeat("x", scaleP21SW22VisibilityRow+1), f.data.Viewer.ID); err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
			if _, err := view.BeginViewerVisibilityKeysRead(ctx, workspace); !errors.Is(err, ErrMetadataWorkBudget) {
				t.Fatal("account header transfer was not preflighted", err)
			}
		}()
	}
	want, err := f.ViewerVisibilityKeys(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	read, err := view.BeginViewerVisibilityKeysRead(t.Context(), workspace)
	if err != nil || read == nil || !reflect.DeepEqual(read.Keys, want) {
		t.Fatal("visibility helper did not recover after budget rejection", read, err)
	}
	defer read.Close()
	read.Keys[f.data.Viewer.ID] = "caller mutation"
	view.metadataResidency.work.mu.Lock()
	held := view.metadataResidency.work.used
	view.metadataResidency.work.mu.Unlock()
	if held != scaleP21SW22VisibilityWork {
		t.Fatal("returned visibility map is not admitted through consumption", held)
	}
	if err := read.Close(); err != nil || read.Keys != nil {
		t.Fatal("close did not invalidate borrowed keys", err)
	}
	if err := read.Close(); err != nil {
		t.Fatal("repeated close failed", err)
	}
	again, err := view.BeginViewerVisibilityKeysRead(t.Context(), workspace)
	if err != nil || again == nil || !reflect.DeepEqual(again.Keys, want) {
		t.Fatal("returned visibility map aliases another read", again, err)
	}
	defer again.Close()
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
	closed, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Rollback(); err != nil {
		t.Fatal(err)
	}
	broken := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: closed})
	if _, err := view.BeginViewerVisibilityKeysRead(broken, workspace); !errors.Is(err, sql.ErrTxDone) {
		t.Fatal("storage failure became an empty visibility map", err)
	}
	view.metadataResidency.work.mu.Lock()
	used := view.metadataResidency.work.used
	view.metadataResidency.work.mu.Unlock()
	if used != 0 {
		t.Fatal("visibility helper leaked work admission", used)
	}
	if _, err := view.ViewerVisibilityKeys(t.Context(), workspace); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("legacy map API accepted an uncharged bounded result", err)
	}
	parentCtx, releaseParent, err := view.admitMetadataWork(t.Context(), scaleP21SW22VisibilityWork)
	if err != nil {
		t.Fatal(err)
	}
	parentRead, err := view.BeginViewerVisibilityKeysRead(parentCtx, workspace)
	if err != nil {
		releaseParent()
		t.Fatal(err)
	}
	defer parentRead.Close()
	releaseParent()
	view.metadataResidency.work.mu.Lock()
	remaining := view.metadataResidency.work.used
	view.metadataResidency.work.mu.Unlock()
	if remaining != scaleP21SW22VisibilityWork || !reflect.DeepEqual(parentRead.Keys, want) {
		t.Fatal("parent release revoked an owned result reservation", remaining)
	}
	if err := parentRead.Close(); err != nil {
		t.Fatal(err)
	}
}
