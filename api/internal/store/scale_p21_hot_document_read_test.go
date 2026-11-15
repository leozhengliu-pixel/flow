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

func TestScaleP21HotDocumentReadOwnershipAndFallback(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, idgen.New("p21-document-")
	at := time.Date(2025, 3, 4, 5, 6, 7, 8, time.UTC)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams", "documents", "projects"), workspace, "scale_p21.document_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents, domain.Document{ID: target, SlugID: target + "-current", PreviousSlugIDs: []string{target + "-old"}, Title: "Owned", Creator: data.Viewer, CreatedAt: at, UpdatedAt: at, Content: strings.Repeat("body ", 140000), ContentData: map[string]any{"native": int64(7), "at": at, "mutable": []any{"original"}}, TeamIDs: []string{data.Teams[0].ID}, Revisions: []domain.DocumentRevision{{ID: target + "-revision", Content: "old", Author: data.Viewer}}})
		data.Projects = append(data.Projects, domain.Project{ID: target + "-project", TeamIDs: []string{data.Teams[0].ID}, Resources: []domain.ProjectResource{{ID: target}}})
		for range 100 {
			data.Documents = append(data.Documents, domain.Document{ID: idgen.New("unrelated-document-"), Content: strings.Repeat("unrelated", 1024), Creator: data.Viewer})
			data.Projects = append(data.Projects, domain.Project{ID: idgen.New("unrelated-project-"), Description: strings.Repeat("unrelated", 1024)})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"warm", "reloaded", "canonical"} {
		t.Run(state, func(t *testing.T) {
			if state == "reloaded" {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if state == "canonical" {
				f.mu.Lock()
				f.forgetCopyRevisionLocked(workspace)
				f.mu.Unlock()
			}
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			canonical, _ := readMetadataStamp(t.Context(), f.db, workspace)
			known, exact := f.copyRevisionOf(workspace)
			t.Logf("state=%s source=%+v SQL=%+v owner=%+v exact=%t", state, view.metadataResidency.logical[workspace].Stamp(), canonical, known, exact)
			request := DocumentReadRequest{Ref: target + "-old", ReadOnly: state != "warm", ProjectionFallback: true}
			want, err := f.BeginDocumentRead(t.Context(), workspace, f.data.Viewer.ID, nil, request)
			if errors.Is(err, ErrResourceAccessChanged) {
				data, fallbackErr := f.PagedWorkspaceMetadata(t.Context(), workspace, f.data.Viewer.ID)
				err = fallbackErr
				want = &DocumentRead{}
				for _, item := range data.Documents {
					if item.ID == target {
						want.Data.Documents = []domain.Document{item}
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			read, err := view.BeginDocumentRead(t.Context(), workspace, f.data.Viewer.ID, nil, request)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(want.Data.Documents)
			after, _ := json.Marshal(read.Data.Documents)
			if string(before) != string(after) || len(read.Data.Projects) != 1 || len(read.Data.Documents[0].Content) != 700000 {
				for index := range want.Data.Documents {
					want.Data.Documents[index].Content, want.Data.Documents[index].ContentData = "", nil
				}
				for index := range read.Data.Documents {
					read.Data.Documents[index].Content, read.Data.Documents[index].ContentData = "", nil
				}
				t.Fatalf("selection mismatch state=%s projects=%d\nwant=%+v\n got=%+v", state, len(read.Data.Projects), want.Data.Documents, read.Data.Documents)
			}
			if state == "warm" {
				if _, ok := read.Data.Documents[0].ContentData["native"].(int64); !ok {
					t.Fatal("installed native mutation input became wire JSON")
				}
				if value, ok := read.Data.Documents[0].ContentData["at"].(time.Time); !ok || value.Location() != at.Location() {
					t.Fatal("installed native timestamp identity lost")
				}
			}
			used := view.MetadataWorkUsedForTesting()
			if used == 0 || !read.owner.SourceRetainedForTesting() {
				t.Fatal("document was not owned through processing")
			}
			read.Data.Documents[0].ContentData["mutable"].([]any)[0] = "changed"
			read.Close()
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 || !reflect.DeepEqual(read.Data, domain.Bootstrap{}) {
				t.Fatal("document read leaked admission/output")
			}
			again, err := view.BeginDocumentRead(t.Context(), workspace, f.data.Viewer.ID, nil, request)
			if err != nil || again.Data.Documents[0].ContentData["mutable"].([]any)[0] != "original" {
				t.Fatal("mutable document escaped source ownership", err)
			}
			again.Close()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := view.BeginDocumentRead(ctx, workspace, f.data.Viewer.ID, nil, request); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			limit := view.metadataResidency.work.limit
			view.metadataResidency.work.limit = 1
			if _, err := view.BeginDocumentRead(t.Context(), workspace, f.data.Viewer.ID, nil, request); !errors.Is(err, ErrMetadataWorkBudget) {
				t.Fatal("document ignored work budget", err)
			}
			view.metadataResidency.work.limit = limit
			if view.WorkspaceMetadataDecodes() != 0 || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("document decoded complete metadata or leaked failed work")
			}
			t.Logf("state=%s held_work=%d", state, used)
		})
	}
}

func TestScaleP21HotDocumentReadUnrelatedCount(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, idgen.New("p21-document-count-")
	var held uint64
	for _, count := range []int{100, 1000} {
		if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.document_count", "", nil, func(data *domain.Bootstrap) error {
			data.Documents = []domain.Document{{ID: target, Creator: data.Viewer}}
			data.Projects = nil
			for range count {
				data.Documents = append(data.Documents, domain.Document{ID: idgen.New("other-doc-"), Content: strings.Repeat("body", 128), Creator: data.Viewer})
				data.Projects = append(data.Projects, domain.Project{ID: idgen.New("other-project-"), Description: strings.Repeat("body", 128)})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		read, err := view.BeginDocumentRead(t.Context(), workspace, f.data.Viewer.ID, nil, DocumentReadRequest{Ref: target, ProjectionFallback: true})
		if err != nil {
			t.Fatal(err)
		}
		used := uint64(view.MetadataWorkUsedForTesting())
		if held != 0 && used != held {
			t.Fatalf("unrelated retained work grew: %d -> %d", held, used)
		}
		held = used
		t.Logf("unrelated=%d held_work=%d", count, held)
		read.Close()
		view.releaseMetadataLogicalSources()
	}
}

func TestScaleP21HotDocumentReadCallerSnapshotPoolOne(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, idgen.New("p21-document-caller-")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "documents"), workspace, "scale_p21.document_caller_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents, domain.Document{ID: target, Title: "Caller snapshot", Content: strings.Repeat("body ", 70000), Creator: data.Viewer, CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	snapshot, release, err := f.BeginWorkspaceRead(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "documents"), workspace, "scale_p21.document_caller_change", "", nil, func(data *domain.Bootstrap) error {
		for index := range data.Documents {
			if data.Documents[index].ID == target {
				data.Documents[index].Title = "Later installed generation"
			}
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
	f.db.SetMaxOpenConns(1)
	defer f.db.SetMaxOpenConns(0)
	legacy, err := f.PagedWorkspaceMetadata(snapshot, workspace, f.data.Viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	var expected domain.Document
	for _, item := range legacy.Documents {
		if item.ID == target {
			expected = item
		}
	}
	read, err := view.BeginDocumentRead(snapshot, workspace, f.data.Viewer.ID, nil, DocumentReadRequest{Ref: target, ReadOnly: true, ProjectionFallback: true})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(expected)
	got, _ := json.Marshal(read.Data.Documents[0])
	if string(want) != string(got) || read.Data.Documents[0].Title != "Caller snapshot" {
		t.Fatal("fallback escaped caller's repeatable read snapshot")
	}
	read.Close()
	held := snapshot.Value(workspaceReadKey{}).(workspaceRead)
	var one int
	if err := held.reader.QueryRowContext(snapshot, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatal("document reader closed caller transaction", err)
	}
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("caller snapshot leaked work or decoded full metadata")
	}
}

func TestScaleP21HotDocumentReadInstalledApplication(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, client, target := f.data.Workspace.URLKey, idgen.New("flow_client_document_"), idgen.New("p21-document-app-")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "oauthApplications"), workspace, "scale_p21.document_app_definition", "", nil, func(data *domain.Bootstrap) error {
		data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{ID: client + "-definition", ClientID: client, Name: "Global application name", LogoURL: "https://example.test/current-document-app.png", Scopes: []string{"read", "app:mentionable"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app, err := f.InstallApplication(t.Context(), workspace, domain.ApplicationInstallation{ClientID: client, Name: "Stale installation name", AvatarURL: "https://example.test/stale-document-app.png", Scopes: []string{"read", "write", "app:mentionable"}, TeamIDs: []string{f.data.Teams[0].ID}, InstalledBy: f.data.Viewer.ID, Active: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := f.ErasePersonFromWorkspace(ctx, workspace, app.UserID); err != nil {
			t.Error(err)
		}
		if err := f.EraseAccount(ctx, app.UserID); err != nil {
			t.Error(err)
		}
	})
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "documents"), workspace, "scale_p21.document_app_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents, domain.Document{ID: target, Title: "Installed app read", Content: "Current body", Creator: data.Viewer, TeamIDs: []string{f.data.Teams[0].ID}, SubscriberIDs: []string{app.UserID}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	request := DocumentReadRequest{Ref: target, ReadOnly: true, ProjectionFallback: true}
	literal, err := f.BeginDocumentRead(t.Context(), workspace, app.UserID, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	defer literal.Close()
	if literal.Data.Viewer.Name != "Global application name" || slices.Contains(literal.Data.Viewer.AppScopes, "write") || len(literal.Data.Documents) != 1 {
		t.Fatalf("fixture did not resolve the actual global app definition: viewer=%+v documents=%d", literal.Data.Viewer, len(literal.Data.Documents))
	}
	read, err := view.BeginDocumentRead(t.Context(), workspace, app.UserID, nil, request)
	if err != nil {
		t.Fatal("installed app projection failed", err)
	}
	defer read.Close()
	type identity struct {
		Viewer  domain.User
		Users   []domain.User
		Members []domain.WorkspaceMember
	}
	want, _ := json.Marshal(identity{literal.Data.Viewer, literal.Data.Users, literal.Data.Members})
	got, _ := json.Marshal(identity{read.Data.Viewer, read.Data.Users, read.Data.Members})
	if !reflect.DeepEqual(identity{literal.Data.Viewer, literal.Data.Users, literal.Data.Members}, identity{read.Data.Viewer, read.Data.Users, read.Data.Members}) {
		t.Fatalf("installed app name/scope projection differs\nliteral=%s\nbounded=%s\nliteral viewer=%+v\nbounded viewer=%+v", want, got, literal.Data.Viewer, read.Data.Viewer)
	}
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("installed application output owner leaked admission")
	}
	// A human viewer reaches app decoration through ListMembers, whose SQL
	// errors the old projector deliberately ignores. Admission errors must
	// escape that boundary instead of producing a successful empty directory.
	owner := &ResourceRead{store: view}
	ctx := owner.metadataOutputContext(t.Context())
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	_, _, err = view.projectBootstrapForUser(ctx, domain.Bootstrap{Workspace: f.data.Workspace}, f.data.Viewer.ID)
	view.metadataResidency.work.limit = limit
	owner.Close()
	if !errors.Is(err, ErrMetadataWorkBudget) || !errors.Is(metadataOutputError(ctx), ErrMetadataWorkBudget) {
		t.Fatal("ignored member-list error swallowed application admission failure", err)
	}
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("failed member app projection leaked admission")
	}
}

func TestScaleP21HotDocumentReadLiveProjectionAdmission(t *testing.T) {
	for _, kind := range []string{"member", "identity", "team-member", "invitation", "departure", "inactive-app", "default-favorite", "favorite-override"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", "off")
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			t.Setenv("FLOW_RECORD_JOURNAL", "on")
			f := scaleP09Database(t)
			if kind != "invitation" && f.dialect != "sqlite" {
				t.Skip("large SQL VARCHAR scalar is legal on SQLite; invitation JSON is tested on every dialect")
			}
			workspace, userID, workspaceID := f.data.Workspace.URLKey, f.data.Viewer.ID, f.data.Workspace.ID
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			request := DocumentReadRequest{ReadOnly: true, ProjectionFallback: true}
			baseline, err := view.BeginDocumentRead(t.Context(), workspace, userID, nil, request)
			if err != nil {
				t.Fatal(err)
			}
			baselineWork := view.MetadataWorkUsedForTesting()
			baseline.Close()
			large, at := strings.Repeat("x", 64<<10), "2025-03-04T05:06:07Z"
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := f.db.ExecContext(t.Context(), query, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "member":
				exec(`UPDATE auth_users SET name=?,display_name=?,avatar_url=? WHERE id=?`, large, large, large, userID)
			case "identity":
				id := idgen.New("p21-document-identity-")
				exec(`INSERT INTO auth_identities(id,user_id,provider,issuer,subject,identity_key,username,claims_json,created_at,last_login_at) VALUES(?,?,'oidc','test',?,?,'','{}',?,?)`, id, userID, large, id, at, at)
			case "team-member":
				exec(`UPDATE team_memberships SET origin=? WHERE workspace_id=? AND user_id=?`, large, workspaceID, userID)
			case "invitation":
				id := idgen.New("p21-document-invite-")
				teamIDs := []byte("[" + strings.Repeat(`"",`, 22000) + `""]`)
				exec(`INSERT INTO workspace_invitations(id,workspace_id,email,role,team_ids,token_hash,inviter_id,status,expires_at,created_at) VALUES(?,?,?,'member',?,?,?,'pending',?,?)`, id, workspaceID, id+"@example.test", teamIDs, id, userID, at, at)
			case "departure", "inactive-app":
				person := domain.User{ID: idgen.New("app_p21_document_departed_")}
				exec(`INSERT INTO auth_users(id,email,name,display_name,avatar_url,password_hash,active,created_at,updated_at) VALUES(?,?,?,?,?,'',0,?,?)`, person.ID, person.ID+"@example.test", large, large, large, at, at)
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
					defer cancel()
					if _, err := f.db.ExecContext(ctx, `DELETE FROM auth_users WHERE id=?`, person.ID); err != nil {
						t.Error(err)
					}
				})
				if kind == "departure" {
					exec(`INSERT INTO workspace_departures(workspace_id,user_id,role,joined_at,left_at) VALUES(?,?,'member',?,?)`, workspaceID, person.ID, at, at)
				} else {
					// The compatibility branch is keyed by the canonical record
					// key's app_ prefix, not a decoded User.App value.
					exec(`INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'users',?,9999,?)`, workspace, person.ID, []byte(`{}`))
				}
			case "default-favorite":
				exec(`INSERT INTO team_default_favorites(id,workspace_key,team_id,resource_type,resource_id,position,created_at,updated_at) VALUES(?,?,?,'document',?,0,?,?)`, idgen.New("p21-document-default-"), workspace, f.data.Teams[0].ID, large, at, at)
			case "favorite-override":
				exec(`INSERT INTO team_default_favorite_overrides(workspace_key,user_id,resource_type,resource_id,hidden,updated_at) VALUES(?,?,'document',?,1,?)`, workspace, userID, large, at)
			}
			literal, err := f.BeginDocumentRead(t.Context(), workspace, userID, nil, request)
			if err != nil {
				t.Fatal(err)
			}
			defer literal.Close()
			read, err := view.BeginDocumentRead(t.Context(), workspace, userID, nil, request)
			if err != nil {
				t.Fatal(err)
			}
			type directory struct {
				Viewer      domain.User
				Users       []domain.User
				Members     []domain.WorkspaceMember
				TeamMembers []domain.TeamMember
				Invitations []domain.Invitation
				Departed    []domain.WorkspaceMember
				Favorites   []domain.Favorite
			}
			output := func(data domain.Bootstrap) directory {
				return directory{data.Viewer, data.Users, data.Members, data.TeamMembers, data.Invitations, data.DepartedMembers, data.Favorites}
			}
			want, got := output(literal.Data), output(read.Data)
			if !reflect.DeepEqual(want, got) {
				t.Fatal("live projection differs from literal directory output")
			}
			used := view.MetadataWorkUsedForTesting()
			read.Close()
			if used <= baselineWork+(1<<20) {
				t.Fatalf("large live SQL input was not admitted: baseline=%d large=%d", baselineWork, used)
			}
			limit := view.metadataResidency.work.limit
			view.metadataResidency.work.limit = baselineWork + (1 << 20)
			_, err = view.BeginDocumentRead(t.Context(), workspace, userID, nil, request)
			view.metadataResidency.work.limit = limit
			if !errors.Is(err, ErrMetadataWorkBudget) {
				t.Fatal("large live projection bypassed admission", err)
			}
			if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("live projection failure leaked work or decoded full metadata")
			}
			t.Logf("source=%s baseline_work=%d large_work=%d", kind, baselineWork, used)
		})
	}
}

func TestScaleP21HotDocumentReadQueuedRevocation(t *testing.T) {
	for _, credential := range []string{"session", "selected-key"} {
		t.Run(credential, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", "off")
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			t.Setenv("FLOW_RECORD_JOURNAL", "on")
			f := scaleP09Database(t)
			workspace, userID := f.data.Workspace.URLKey, f.data.Viewer.ID
			var key *domain.APIKey
			if credential == "selected-key" {
				key = &domain.APIKey{ID: idgen.New("p21-document-key-"), TeamRestriction: "selected", TeamIDs: []string{f.data.Teams[0].ID}}
			}
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			stamp, err := readMetadataStamp(t.Context(), f.db, workspace)
			if err != nil {
				t.Fatal(err)
			}
			epoch, err := f.AuthzEpoch(t.Context(), f.data.Workspace.ID)
			if err != nil {
				t.Fatal(err)
			}
			revoked, revokeErr := false, error(nil)
			ctx := WithSQLStatementObserver(t.Context(), func(query string) {
				if revoked || !strings.HasPrefix(query, "SELECT u.id,u.email,u.name,u.display_name,u.avatar_url,u.email_verified_at,u.active,u.deleted_at,m.role,m.status,t.team_id") {
					return
				}
				revoked = true
				_, revokeErr = f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, userID)
			})
			read, err := view.BeginDocumentRead(ctx, workspace, userID, key, DocumentReadRequest{ReadOnly: true, ProjectionFallback: true})
			if read != nil {
				read.Close()
			}
			if !revoked || revokeErr != nil || !errors.Is(err, ErrAuthForbidden) || !errors.Is(err, ErrResourceAccessChanged) {
				t.Fatalf("queued read retained revoked authority: revoked=%t mutation=%v read=%v", revoked, revokeErr, err)
			}
			after, err := readMetadataStamp(t.Context(), f.db, workspace)
			if err != nil || after != stamp {
				t.Fatal("fixture changed metadata revision rather than only live authority", after, stamp, err)
			}
			current, err := f.AuthzEpoch(t.Context(), f.data.Workspace.ID)
			if err != nil || current == epoch {
				t.Fatal("fixture did not advance authoritative epoch", err)
			}
			if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("revoked document read leaked output admission or decoded full metadata")
			}
		})
	}
}
