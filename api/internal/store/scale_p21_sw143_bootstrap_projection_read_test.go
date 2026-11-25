package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW143Setup(t *testing.T) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
}

func scaleP21SW143Encode(t *testing.T, data domain.Bootstrap) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := json.NewEncoder(&output).Encode(data); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func scaleP21SW143Compare(t *testing.T, ctx context.Context, want domain.Bootstrap, read *IssueBootstrapRead) {
	t.Helper()
	// These are the intermediate reader's seven audited dead fields. The HTTP
	// handler replaces update maps and removes these transient families before
	// publication; SW145 proves that complete route boundary independently.
	want.ProjectUpdates, want.InitiativeUpdates = nil, nil
	want.ProviderJobs, want.CommentActions = nil, nil
	want.LoopRuns, want.LoopVersions, want.LoopAttachments = nil, nil, nil
	var output bytes.Buffer
	if err := read.WriteJSON(ctx, &output); err != nil {
		t.Fatal("encode bounded bootstrap", err)
	}
	expected := scaleP21SW143Encode(t, want)
	if !bytes.Equal(expected, output.Bytes()) {
		position := 0
		for position < min(len(expected), output.Len()) && expected[position] == output.Bytes()[position] {
			position++
		}
		t.Fatalf("final encoded projected header differs at byte %d, lengths %d/%d: legacy=%q bounded=%q",
			position, len(expected), output.Len(),
			expected[max(0, position-80):min(len(expected), position+140)],
			output.Bytes()[max(0, position-80):min(output.Len(), position+140)])
	}
}

func scaleP21SW143Seed(t *testing.T, f scaleP09Fixture, prefix, body string) {
	t.Helper()
	scaleP21SW112Seed(t, f, prefix, body)
	if err := f.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw143_seed", "", nil, func(data *domain.Bootstrap) error {
		actor := data.Viewer
		actor.Name = "Body actor <>&"
		data.Projects[0].Comments = []domain.Comment{{ID: prefix + "project-comment", Body: body, BodyData: map[string]any{"text": body}, User: actor}}
		data.Projects[0].Resources[0].Title = "Resource header"
		data.Projects[0].Milestones[0].Description = "Milestone header"
		data.Projects[0].Milestones[0].DescriptionRevisions[0].Description = "Milestone revision header"
		data.Initiatives[0].Comments = []domain.Comment{{ID: prefix + "initiative-comment", Body: body, User: actor}}
		data.Initiatives[0].Resources[0].Title = "Initiative resource header"
		data.Initiatives[0].CommentAttachments[0].Title = "Attachment header"
		data.Loops = []domain.Loop{{ID: prefix + "loop", Name: "Loop", Creator: actor,
			Instructions: body, Description: body, InstructionsData: map[string]any{"text": body}}}
		data.Trash = []domain.TrashEntry{{ID: prefix + "trash", ResourceType: "document", ResourceID: prefix + "document",
			TeamIDs: []string{data.Teams[0].ID}, Payload: json.RawMessage(`{"body":"<>&","number":1e30,"duplicate":1,"duplicate":null}`), DeletedBy: actor}}
		data.Settings[prefix+"native"] = map[string]any{
			"integer": int64(9007199254740993), "number": json.Number("1e30"),
			"nil": []string(nil), "empty": []string{}, "raw": json.RawMessage(` {"raw":"<>&"} `),
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP21SW143BootstrapProjectedHeaderJSONWarmReloadGrowth(t *testing.T) {
	scaleP21SW143Setup(t)
	for _, reloaded := range []bool{false, true} {
		t.Run(fmt.Sprint("reloaded-", reloaded), func(t *testing.T) {
			f := scaleP09Database(t)
			workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw143-body-")
			held := 0
			// Reuse identities and SQL directory timestamps: their admitted
			// policy/header bytes must not vary with an unrelated fixture.
			for _, copies := range []int{1000, 10000} {
				body := strings.Repeat("Legal bootstrap body <>&\u2028\xff. ", copies)
				scaleP21SW143Seed(t, f, prefix, body)
				if reloaded {
					f.mu.Lock()
					f.forgetCopyRevisionLocked(workspace)
					f.mu.Unlock()
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				defer view.releaseMetadataLogicalSources()
				want, err := f.PagedWorkspaceMetadata(t.Context(), workspace, f.data.Viewer.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
					t.Fatal(err)
				}
				oldSQL, newSQL := 0, 0
				if _, err := f.PagedWorkspaceMetadata(WithSQLStatementObserver(t.Context(), func(string) { oldSQL++ }), workspace, f.data.Viewer.ID); err != nil {
					t.Fatal(err)
				}
				observed := WithSQLStatementObserver(t.Context(), func(string) { newSQL++ })
				read, err := view.BeginIssueBootstrapRead(observed, workspace, f.data.Viewer.ID, nil)
				if err != nil {
					t.Fatal(err)
				}
				work := view.MetadataWorkUsedForTesting()
				if copies == 1000 {
					held = work
				} else if work != held {
					t.Fatal("10x opaque body growth changed retained admission", reloaded, held, work)
				}
				scaleP21SW143Compare(t, observed, want, read)
				var streamed bytes.Buffer
				if err := read.StreamString(t.Context(), read.Data.Loops[0].Instructions, func(chunk []byte) error {
					_, err := streamed.Write(chunk)
					return err
				}); err != nil || streamed.String() != want.Loops[0].Instructions {
					t.Fatal("body string stream changed", err)
				}
				t.Logf("body_copies=%d reloaded=%t admitted_work=%d legacy_SQL=%d bounded_SQL=%d", copies, reloaded, work, oldSQL, newSQL)
				read.Close()
				read.Close()
				if !reflect.DeepEqual(read.Data, domain.Bootstrap{}) || read.access != (AccessContext{}) || read.principal != "" ||
					view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("bootstrap retained output/work or decoded a full workspace")
				}
			}
		})
	}
}

func TestScaleP21SW143BootstrapExplicitHandoffAndDefaults(t *testing.T) {
	scaleP21SW143Setup(t)
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw143-handoff-")
	scaleP21SW143Seed(t, f, prefix, "Explicit handed body")
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	poolLimit := f.db.Stats().MaxOpenConnections
	f.db.SetMaxOpenConns(1)
	defer f.db.SetMaxOpenConns(poolLimit)
	tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	handed, found := f.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("fixture workspace missing")
	}
	at := time.Date(2026, 11, 15, 1, 2, 3, 4, time.UTC)
	handed.Issues = []domain.Issue{{ID: prefix + "issue", Title: "Explicit issue", Team: handed.Teams[0], Creator: handed.Viewer}}
	handed.Comments = map[string][]domain.Comment{prefix + "issue": {{ID: prefix + "comment", Body: "Explicit comment", User: handed.Viewer}}}
	handed.Activities = map[string][]domain.ActivityEvent{prefix + "issue": {{ID: prefix + "activity", CreatedAt: at}}}
	handed.Notifications = []domain.Notification{{ID: prefix + "notification", RecipientID: handed.Viewer.ID}}
	handed.NotificationDeliveries = []domain.NotificationDelivery{{ID: prefix + "delivery", RecipientID: handed.Viewer.ID}}
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx, metadata: handed})
	want, err := f.PagedWorkspaceMetadata(ctx, workspace, handed.Viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	read, err := view.BeginIssueBootstrapRead(ctx, workspace, handed.Viewer.ID, nil)
	if err != nil {
		legacyRole, legacyStatus, legacyRoleErr := f.WorkspaceRole(ctx, handed.Workspace.ID, handed.Viewer.ID)
		_, accessErr := view.ResolveAccess(ctx, workspace, handed.Viewer.ID, nil)
		t.Fatalf("handoff: %v; installed viewer equals fixture owner=%t role=%s/%s roleErr=%v directAccessErr=%v",
			err, handed.Viewer.ID == f.data.Viewer.ID, legacyRole, legacyStatus, legacyRoleErr, accessErr)
	}
	scaleP21SW143Compare(t, ctx, want, read)
	read.Close()
	for _, mismatch := range []bool{false, true} {
		borrowed := ctx
		if mismatch {
			borrowed = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{
				database: f.db, workspace: prefix + "other", reader: tx,
				metadata: domain.Bootstrap{Workspace: domain.Workspace{ID: "poison"}},
			})
		}
		for _, userID := range []string{f.data.Viewer.ID, ""} {
			t.Run(fmt.Sprint("borrowed/", mismatch, "/", userID == ""), func(t *testing.T) {
				var expected domain.Bootstrap
				if userID == "" {
					var found bool
					expected, found = f.WorkspaceMetadataForRead(borrowed, workspace)
					if !found {
						t.Fatal("development borrowed oracle missing")
					}
					expected.ViewerRole = "admin"
					if err := f.ApplyTeamDefaultFavorites(borrowed, &expected, expected.Viewer.ID); err != nil {
						t.Fatal(err)
					}
				} else {
					var err error
					expected, err = f.PagedWorkspaceMetadata(borrowed, workspace, userID)
					if err != nil {
						t.Fatal(err)
					}
				}
				read, err := view.BeginIssueBootstrapRead(borrowed, workspace, userID, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer read.Close()
				scaleP21SW143Compare(t, borrowed, expected, read)
			})
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal("reader closed caller snapshot", err)
	}
	for _, key := range []string{workspace, ""} {
		for _, userID := range []string{f.data.Viewer.ID, ""} {
			t.Run(fmt.Sprint(key == "", "/", userID == ""), func(t *testing.T) {
				var want domain.Bootstrap
				if userID == "" {
					want, found = f.WorkspaceMetadataForRead(t.Context(), key)
					if !found {
						t.Fatal("development oracle missing")
					}
					want.ViewerRole = "admin"
					if err := f.ApplyTeamDefaultFavorites(t.Context(), &want, want.Viewer.ID); err != nil {
						t.Fatal(err)
					}
				} else {
					want, err = f.PagedWorkspaceMetadata(t.Context(), key, userID)
					if err != nil {
						t.Fatal(err)
					}
				}
				read, err := view.BeginIssueBootstrapRead(t.Context(), key, userID, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer read.Close()
				scaleP21SW143Compare(t, t.Context(), want, read)
			})
		}
	}
	for _, userID := range []string{f.data.Viewer.ID, ""} {
		read, err := view.BeginIssueBootstrapRead(t.Context(), prefix+"unknown", userID, nil)
		wantErr := ErrAuthForbidden
		if userID == "" {
			wantErr = sql.ErrNoRows
		}
		if read != nil || !errors.Is(err, wantErr) {
			t.Fatal("unknown workspace changed", err)
		}
	}
	if read, err := f.BeginIssueBootstrapRead(t.Context(), workspace, f.data.Viewer.ID, nil); read != nil || !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("flag-off constructor activated", err)
	}
}

func scaleP21SW143Join(t *testing.T, f scaleP09Fixture, role string, teams []string) domain.User {
	t.Helper()
	user, token, err := f.Register(t.Context(), "SW143 "+role, idgen.New("sw143-person-")+"@example.test", "sw143-password")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.ErasePersonFromWorkspace(context.Background(), f.data.Workspace.URLKey, user.ID); err != nil {
			t.Error(err)
		}
		if err := f.EraseAccount(context.Background(), user.ID); err != nil {
			t.Error(err)
		}
	})
	if err := f.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	invitation, err := f.Invite(t.Context(), f.data.Workspace.ID, f.data.Viewer.ID, user.Email, role, teams)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.AcceptInvitation(t.Context(), invitation.Token, user.ID); err != nil {
		t.Fatal(err)
	}
	return user
}

func TestScaleP21SW143BootstrapPermissionProjectedHeaderJSON(t *testing.T) {
	scaleP21SW143Setup(t)
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw143-M-")
	public, private, child := prefix+"public", prefix+"private", prefix+"child"
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw143_teams", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: public, Key: public}, domain.Team{ID: private, Key: private, Private: true}, domain.Team{ID: child, Key: child})
		data.TeamSettings[public] = domain.TeamSettings{TeamID: public, Access: "public"}
		data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
		data.TeamSettings[child] = domain.TeamSettings{TeamID: child, Access: "public", ParentTeamID: private}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	admin := scaleP21SW143Join(t, f, "admin", []string{private})
	member := scaleP21SW143Join(t, f, "member", []string{public})
	guest := scaleP21SW143Join(t, f, "guest", []string{public})
	inherited := scaleP21SW143Join(t, f, "member", []string{private})
	principals := []struct {
		name string
		user domain.User
	}{
		{"admin", admin}, {"admin-private-outsider", f.data.Viewer}, {"member-private-outsider", member},
		{"guest-explicit-grant", guest}, {"sub-team-inheritance", inherited},
	}
	keys := map[string][]domain.APIKey{}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw143_policy", "", nil, func(data *domain.Bootstrap) error {
		for _, principal := range principals {
			data.UserSettings[principal.user.ID] = defaultUserSettings(principal.user.ID)
			for _, restriction := range []string{"all", "empty", "set"} {
				id := idgen.New("sw143-key-")
				key := domain.APIKey{ID: id, CreatorID: principal.user.ID, SecretHash: fmt.Sprintf("%x", sha256.Sum256([]byte(id))), Scopes: []string{"read"}, TeamRestriction: "all"}
				if restriction != "all" {
					key.TeamRestriction, key.TeamIDs = "selected", []string{}
				}
				if restriction == "set" {
					key.TeamIDs = []string{public}
				}
				keys[principal.user.ID] = append(keys[principal.user.ID], key)
				data.APIKeys = append(data.APIKeys, key)
			}
		}
		data.Projects = []domain.Project{
			{ID: prefix + "shared", Name: "Shared", SlugID: prefix + "current", PreviousSlugIDs: []string{prefix + "old"},
				TeamIDs: []string{public, private}, DependencyIDs: []string{prefix + "hidden"}, IssueCount: 97, Creator: &admin, Lead: &admin,
				Description: "Shared <>&", Comments: []domain.Comment{{ID: prefix + "comment", Body: "Actor comment", User: admin}}},
			{ID: prefix + "hidden", Name: "Hidden", TeamIDs: []string{private}, Description: "Private", Creator: &admin},
			{ID: prefix + "inherited", Name: "Inherited", TeamIDs: []string{child}, Description: "Child"},
		}
		data.Initiatives = []domain.Initiative{{ID: prefix + "initiative", Name: "Hierarchy", Creator: admin,
			Description: "Hierarchy body", ProjectIDs: []string{prefix + "shared", prefix + "hidden"}}}
		data.Documents = []domain.Document{{ID: prefix + "document", SlugID: prefix + "document-current", PreviousSlugIDs: []string{prefix + "document-old"},
			Title: "Guest grant", Creator: admin, TeamIDs: []string{private}, ProjectIDs: []string{prefix + "hidden"}, Content: "Document <>&",
			ContentData: map[string]any{"text": "Document"}, Permissions: []domain.DocumentPermission{{ID: prefix + "grant", SubjectType: "user", SubjectID: guest.ID, Role: "viewer"}}}}
		data.Favorites = append(data.Favorites, domain.Favorite{ID: prefix + "old-favorite", UserID: member.ID, ResourceType: "project", ResourceID: prefix + "old"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	for _, reloaded := range []bool{false, true} {
		if reloaded {
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
		}
		for _, principal := range principals {
			credentials := []*domain.APIKey{nil}
			for i := range keys[principal.user.ID] {
				credentials = append(credentials, &keys[principal.user.ID][i])
			}
			for index, key := range credentials {
				t.Run(fmt.Sprint(reloaded, "/", principal.name, "/", index), func(t *testing.T) {
					want, err := f.PagedWorkspaceMetadata(t.Context(), workspace, principal.user.ID)
					if err != nil {
						t.Fatal(err)
					}
					read, err := view.BeginIssueBootstrapRead(t.Context(), workspace, principal.user.ID, key)
					if err != nil {
						t.Fatal(err)
					}
					defer read.Close()
					scaleP21SW143Compare(t, t.Context(), want, read)
					read.Close()
					if view.MetadataWorkUsedForTesting() != 0 || read.access != (AccessContext{}) || read.principal != "" {
						t.Fatal("permission read retained work")
					}
				})
			}
		}
	}
	queued, err := view.BeginIssueBootstrapRead(t.Context(), workspace, inherited.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer queued.Close()
	authority, err := f.ResolveAccess(t.Context(), workspace, inherited.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.SetTeamMembership(t.Context(), f.data.Workspace.ID, private, inherited.ID, "member", false); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"queued", "replayed"} {
		var output bytes.Buffer
		if !errors.Is(f.ValidateAccess(t.Context(), authority), ErrAuthForbidden) ||
			!errors.Is(queued.WriteJSON(t.Context(), &output), ErrAuthForbidden) || output.Len() != 0 {
			t.Fatal("revoked output escaped", phase)
		}
	}
	queued.Close()
	other, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "SW143 other", idgen.New("sw143-other-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.DeleteWorkspace(context.Background(), other.Workspace.URLKey) })
	otherView, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), other.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	defer otherView.releaseMetadataLogicalSources()
	_, legacyErr := f.PagedWorkspaceMetadata(t.Context(), other.Workspace.URLKey, member.ID)
	cross, boundedErr := otherView.BeginIssueBootstrapRead(t.Context(), other.Workspace.URLKey, member.ID, nil)
	if cross != nil || !errors.Is(legacyErr, ErrAuthForbidden) || !errors.Is(boundedErr, ErrAuthForbidden) {
		t.Fatal("cross-workspace refusal changed", legacyErr, boundedErr)
	}
	if view.MetadataWorkUsedForTesting() != 0 || otherView.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("permission read leaked or decoded full metadata")
	}
}

func scaleP21SW143LargeDirectory(t *testing.T, f scaleP09Fixture) {
	t.Helper()
	ids := make([]string, 0, 512)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		tx, err := f.db.BeginTx(ctx, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback()
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `DELETE FROM auth_users WHERE id=?`, id); err != nil {
				t.Error(err)
				return
			}
		}
		if err := tx.Commit(); err != nil {
			t.Error(err)
		}
	})
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	at := time.Now().UTC().Format(time.RFC3339Nano)
	for range cap(ids) {
		id := idgen.New("sw143-directory-")
		ids = append(ids, id)
		// All strings fit the same VARCHAR limits on all three databases.
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO auth_users(id,email,name,display_name,avatar_url,password_hash,email_verified_at,active,created_at,updated_at)
			SELECT ?,?,?,?,?,password_hash,email_verified_at,active,created_at,updated_at FROM auth_users WHERE id=?`,
			id, id+"@example.test", strings.Repeat("n", 320), strings.Repeat("d", 320),
			"https://example.test/"+strings.Repeat("a", 2000), f.data.Viewer.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,'member','active',?,?)`,
			f.data.Workspace.ID, id, at, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP21SW143BootstrapCancellationQuotaAndLifetime(t *testing.T) {
	scaleP21SW143Setup(t)
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw143-lifetime-")
	scaleP21SW143Seed(t, f, prefix, strings.Repeat("Legal body <>& ", 20000))
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := view.BeginIssueBootstrapRead(canceled, workspace, "", nil); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled constructor escaped", err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	read, err := view.BeginIssueBootstrapRead(t.Context(), workspace, "", nil)
	view.metadataResidency.work.limit = limit
	if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("constructor quota escaped or leaked", err)
	}
	t.Run("large-live-directory-quota", func(t *testing.T) {
		view.metadataResidency.work.limit = 32 << 20
		defer func() { view.metadataResidency.work.limit = limit }()
		probe, err := view.BeginIssueBootstrapRead(t.Context(), workspace, f.data.Viewer.ID, nil)
		if err != nil {
			t.Fatal("small directory did not fit comparison budget", err)
		}
		probe.Close()
		scaleP21SW143LargeDirectory(t, f)
		read, err := view.BeginIssueBootstrapRead(t.Context(), workspace, f.data.Viewer.ID, nil)
		view.metadataResidency.work.limit = limit
		if read != nil {
			read.Close()
		}
		if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("large live directory crossed admission or leaked", err)
		}
	})
	for _, mode := range []string{"cancel", "writer-error", "short-write", "quota", "source-replaced"} {
		t.Run(mode, func(t *testing.T) {
			read, err := view.BeginIssueBootstrapRead(t.Context(), workspace, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close()
			base := view.MetadataWorkUsedForTesting()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sentinel := errors.New("destination refused")
			calls := 0
			writer := &scaleP21SW88Writer{write: func(raw []byte) (int, error) {
				calls++
				switch mode {
				case "cancel":
					cancel()
				case "writer-error":
					return 0, sentinel
				case "short-write":
					return len(raw) - 1, nil
				}
				return len(raw), nil
			}}
			if mode == "quota" {
				view.metadataResidency.work.limit = base
			}
			if mode == "source-replaced" {
				view.releaseMetadataLogicalSources()
			}
			err = read.WriteJSON(ctx, writer)
			view.metadataResidency.work.limit = limit
			switch mode {
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatal("publication cancellation lost", calls, err)
				}
			case "writer-error":
				if !errors.Is(err, sentinel) {
					t.Fatal("writer failure changed", err)
				}
			case "short-write":
				if !errors.Is(err, io.ErrShortWrite) {
					t.Fatal("short write accepted", err)
				}
			case "quota":
				if !errors.Is(err, ErrMetadataWorkBudget) || calls != 0 {
					t.Fatal("unadmitted encoding published", calls, err)
				}
			case "source-replaced":
				if err != nil || calls == 0 {
					t.Fatal("reader borrowed installed source lifetime", err)
				}
			}
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("failed encoding leaked work")
			}
		})
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	read, err = view.BeginIssueBootstrapRead(t.Context(), workspace, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := read.TransferOutput(t.Context()); !errors.Is(err, ErrBoundedMetadataCoverage) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("unowned output transfer escaped", err)
	}
	read, err = view.BeginIssueBootstrapRead(t.Context(), workspace, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := view.BeginMetadataJSONRead(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := read.TransferOutput(owner.Context(t.Context())); err != nil {
		t.Fatal(err)
	}
	if view.MetadataWorkUsedForTesting() == 0 {
		t.Fatal("transferred output released early")
	}
	owner.Close()
	if view.MetadataWorkUsedForTesting() != 0 || !reflect.DeepEqual(read.Data, domain.Bootstrap{}) {
		t.Fatal("containing operation retained transferred output")
	}
}
