package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type scaleP03StoreFixture struct {
	*SQLiteStore
	data domain.Bootstrap
}

func scaleP03Stores(t *testing.T) map[string]scaleP03StoreFixture {
	t.Helper()
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if (driver == "") != (databaseURL == "") {
		t.Fatal("P03 external database tests require both FLOW_TEST_DATABASE_DRIVER and FLOW_TEST_DATABASE_URL")
	}
	if driver != "" && driver != "sqlite" && driver != "mysql" && driver != "postgres" && driver != "postgresql" {
		t.Fatalf("P03 external database driver %q is unsupported", driver)
	}
	config := DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4}
	if driver == "" {
		config.Driver, config.Path = "sqlite", filepath.Join(t.TempDir(), "p03.db")
	} else if driver == "sqlite" {
		config.Path = databaseURL
	}
	stores := map[string]scaleP03StoreFixture{}
	for name, config := range map[string]DatabaseConfig{config.Driver: config} {
		repo, err := OpenDatabase(config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Close() })
		user, token, err := repo.Register(t.Context(), "P03 owner", idgen.New("scale-p03-store-")+"@example.test", "p03-fixture-password")
		if err != nil {
			t.Fatal(err)
		}
		workspace := ""
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if workspace != "" {
				if err := repo.DeleteWorkspace(ctx, workspace); err != nil {
					t.Errorf("clean up owned workspace: %v", err)
				}
			}
			if err := repo.EraseAccount(ctx, user.ID); err != nil {
				t.Errorf("clean up owned account: %v", err)
			}
		})
		if err := repo.VerifyEmail(t.Context(), token); err != nil {
			t.Fatal(err)
		}
		user.EmailVerified = true
		data, err := repo.CreateWorkspace(ContextWithActor(t.Context(), user), "P03 isolated", idgen.New("scale-p03-store-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		workspace = data.Workspace.URLKey
		stores[name] = scaleP03StoreFixture{repo, data}
	}
	return stores
}

func TestScaleP03AccessContextRevocationAndOwnership(t *testing.T) {
	for name, repo := range scaleP03Stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			base := repo.data
			workspace, principal := base.Workspace.URLKey, base.Viewer.ID
			err := repo.MutateWorkspace(ctx, workspace, "scale_p03.seeded", "", nil, func(data *domain.Bootstrap) error {
				data.Teams = append(data.Teams, domain.Team{ID: "p03-private", Private: true})
				data.TeamSettings["p03-private"] = domain.TeamSettings{TeamID: "p03-private", Access: "private"}
				data.Projects = append(data.Projects, domain.Project{ID: "p03-private-project", TeamIDs: []string{"p03-private"}, Comments: []domain.Comment{{ID: "c", Body: "unchanged", Reactions: map[string][]string{"like": {principal}}}}})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.SetTeamMembership(ctx, base.Workspace.ID, "p03-private", principal, "member", true); err != nil {
				t.Fatal(err)
			}
			access, err := repo.ResolveAccess(ctx, workspace, principal, nil)
			if err != nil {
				t.Fatal(err)
			}
			if access.WorkspaceID != base.Workspace.ID || access.PrincipalID != principal || access.CredentialScope == "" || access.AuthzEpoch == "" {
				t.Fatal("missing access contract fields")
			}
			first, err := repo.AuthorizedProjectRecord(ctx, access, "p03-private-project")
			if err != nil {
				t.Fatal(err)
			}
			first.TeamIDs[0] = "edited"
			first.Comments[0].Reactions["like"][0] = "edited"
			second, err := repo.AuthorizedProjectRecord(ctx, access, "p03-private-project")
			if err != nil || second.TeamIDs[0] != "p03-private" || second.Comments[0].Reactions["like"][0] != principal {
				t.Fatal("detail does not own its mutable fields", err)
			}
			for _, edit := range []func(*AccessContext){
				func(a *AccessContext) { a.WorkspaceID = "other" }, func(a *AccessContext) { a.PrincipalID = "other" },
				func(a *AccessContext) { a.CredentialScope = "other" }, func(a *AccessContext) { a.AuthzEpoch = "other" },
			} {
				forged := access
				edit(&forged)
				if err := repo.ValidateAccess(ctx, forged); !errors.Is(err, ErrAuthForbidden) {
					t.Fatal("edited access context accepted", err)
				}
			}
			if err := repo.ValidateAccess(ctx, AccessContext{}); !errors.Is(err, ErrAuthForbidden) {
				t.Fatal("zero access context accepted")
			}
			if _, err := repo.ResolveAccess(ctx, "", principal, nil); !errors.Is(err, ErrAuthForbidden) {
				t.Fatal("empty workspace used last-workspace fallback")
			}
			key := &domain.APIKey{ID: "p03-key", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{"p03-private"}}
			scoped, err := repo.ResolveAccess(ctx, workspace, principal, key)
			if err != nil || scoped.CredentialScope == access.CredentialScope {
				t.Fatal("credential scopes share an identity", err)
			}
			key.TeamIDs[0] = "edited"
			if scoped.MatchesCredential(key) || !scoped.MatchesCredential(&domain.APIKey{ID: "p03-key", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{"p03-private"}}) || scoped.MatchesCredential(nil) {
				t.Fatal("request reuse ignored the effective credential scope")
			}
			if err := repo.ValidateAccess(ctx, scoped); err != nil {
				t.Fatal("caller could mutate retained credential scope", err)
			}
			// Membership revocation has no metadata write. A context retained
			// in a queued/replay batch must still fail against live SQL policy.
			if _, err := repo.db.ExecContext(ctx, `DELETE FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`, base.Workspace.ID, "p03-private", principal); err != nil {
				t.Fatal(err)
			}
			for _, batch := range []string{"queued", "replay"} {
				t.Run(batch, func(t *testing.T) {
					if err := repo.ValidateAccess(ctx, access); !errors.Is(err, ErrAuthForbidden) {
						t.Fatal("retained access survived revocation", err)
					}
				})
			}
			fresh, err := repo.ResolveAccess(ctx, workspace, principal, nil)
			if err != nil || fresh.AuthzEpoch == access.AuthzEpoch {
				t.Fatal("revocation did not change epoch", err)
			}
			if _, err := repo.AuthorizedProjectRecord(ctx, fresh, "p03-private-project"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("admin outside private team read content", err)
			}
			if !fresh.policy.settingsTeams["p03-private"] || fresh.policy.teams.Content["p03-private"] {
				t.Fatal("admin settings management and content access were conflated")
			}
			if _, err := repo.db.ExecContext(ctx, `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, base.Workspace.ID, principal); err != nil {
				t.Fatal(err)
			}
			if err := repo.ValidateAccess(ctx, fresh); !errors.Is(err, ErrAuthForbidden) {
				t.Fatal("suspended membership accepted", err)
			}
			if _, err := repo.db.ExecContext(ctx, `UPDATE workspace_memberships SET status='active' WHERE workspace_id=? AND user_id=?`, base.Workspace.ID, principal); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScaleP03PolicyDoesNotRetainUnrelatedMetadata(t *testing.T) {
	for name, repo := range scaleP03Stores(t) {
		t.Run(name, func(t *testing.T) {
			base := repo.data
			workspace, principal := base.Workspace.URLKey, base.Viewer.ID
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			before, err := repo.ResolveAccess(t.Context(), workspace, principal, nil)
			if err != nil {
				t.Fatal(err)
			}
			repo.mu.Lock()
			data := repo.workspaces[workspace]
			data.Projects = append(data.Projects, domain.Project{ID: "p03-unrelated", Description: strings.Repeat("x", 2<<20)})
			data.Trash = append(data.Trash, domain.TrashEntry{ID: "p03-unrelated", Payload: []byte(strings.Repeat("x", 2<<20))})
			for id, settings := range data.TeamSettings {
				settings.AgentGuidance = strings.Repeat("x", 2<<20)
				data.TeamSettings[id] = settings
			}
			repo.workspaces[workspace] = data
			repo.mu.Unlock()
			after, err := repo.ResolveAccess(t.Context(), workspace, principal, nil)
			if err != nil {
				t.Fatal(err)
			}
			if before.AuthzEpoch != after.AuthzEpoch || len(after.policy.metadata.Projects) != 0 || len(after.policy.metadata.Trash) != 0 {
				t.Fatal("unrelated metadata entered authorization policy")
			}
			for _, settings := range after.policy.metadata.TeamSettings {
				if settings.AgentGuidance != "" {
					t.Fatal("agent instructions entered authorization policy")
				}
			}
		})
	}
}
