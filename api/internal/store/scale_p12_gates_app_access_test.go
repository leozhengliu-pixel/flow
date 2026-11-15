package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP12GatesSnapshotApplicationMissingWorkspace(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	for name, fixture := range scaleP03Stores(t) {
		t.Run(name, func(t *testing.T) {
			s, workspace := fixture.SQLiteStore, fixture.data.Workspace.URLKey
			other, err := s.CreateWorkspace(ContextWithActor(t.Context(), fixture.data.Viewer), "P12 application catalog", idgen.New("p12-app-catalog-"), "us")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.DeleteWorkspace(context.Background(), other.Workspace.URLKey); err != nil {
					t.Error(err)
				}
			})
			client := idgen.New("flow_client_p12_snapshot_")
			if err := s.MutateWorkspace(t.Context(), other.Workspace.URLKey, "scale_p12.application_catalog", client, nil, func(data *domain.Bootstrap) error {
				data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{
					ID: client, ClientID: client, Name: "Current catalog name", LogoURL: "/p12-current.png", Scopes: []string{"read"},
				})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			app, err := s.InstallApplication(t.Context(), workspace, domain.ApplicationInstallation{
				ClientID: idgen.New("p12_snapshot_vendor_"), Name: "Installed snapshot name", InstalledBy: fixture.data.Viewer.ID,
				Scopes: []string{"read", "write"}, AllPublicTeams: true, Active: true,
			}, "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				if _, err := s.UninstallApplication(ctx, workspace, app.ID); err != nil {
					t.Error(err)
				}
				if _, err := s.db.ExecContext(ctx, `DELETE FROM auth_users WHERE id=?`, app.UserID); err != nil {
					t.Error(err)
				}
			})
			missing := idgen.New("p12-app-missing-")
			unregistered := idgen.New("flow_client_p12_unregistered_")
			for _, test := range []struct {
				name, workspace, client, literal string
				deleted                          bool
			}{
				{name: "null", literal: "null"},
				{name: "empty-object", literal: "{}"},
				{name: "existing-workspace", workspace: workspace, client: app.ClientID},
				{name: "missing-workspace", workspace: missing, client: app.ClientID},
				{name: "empty-workspace", client: app.ClientID},
				{name: "global-refresh-missing-workspace", workspace: missing, client: client},
				{name: "global-refresh-empty-workspace", client: client},
				{name: "global-refresh-existing-workspace", workspace: workspace, client: client},
				{name: "deactivated-missing-workspace", workspace: missing, client: unregistered},
				{name: "deactivated-existing-workspace", workspace: workspace, client: unregistered},
				{name: "malformed-field", literal: `{"active":"wrong"}`},
				{name: "malformed-json", literal: `{"userId":`},
				{name: "missing-installation", deleted: true},
			} {
				t.Run(test.name, func(t *testing.T) {
					value := app
					value.WorkspaceKey, value.ClientID = test.workspace, test.client
					raw, err := json.Marshal(value)
					if err != nil {
						t.Fatal(err)
					}
					if test.literal != "" {
						raw = []byte(test.literal)
					}
					// This is ApplicationByUser's original decoration policy,
					// evaluated before taking the SQL connection.
					var want domain.ApplicationInstallation
					wantErr := json.Unmarshal(raw, &want)
					if test.deleted {
						want, wantErr = domain.ApplicationInstallation{}, sql.ErrNoRows
					} else if wantErr == nil {
						s.resolveApplicationAccess(&want)
					}
					tx, err := s.db.BeginTx(t.Context(), metadataReadOptions(s.dialect))
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					if test.deleted {
						_, err = tx.ExecContext(t.Context(), `DELETE FROM application_installations WHERE user_id=?`, app.UserID)
					} else {
						_, err = tx.ExecContext(t.Context(), `UPDATE application_installations SET data=? WHERE user_id=?`, raw, app.UserID)
					}
					if err != nil {
						t.Fatal(err)
					}
					s.db.SetMaxOpenConns(1)
					ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: tx})
					got, gotErr := s.ApplicationByUser(ctx, app.UserID)
					if (gotErr == nil) != (wantErr == nil) || gotErr != nil && (reflect.TypeOf(gotErr) != reflect.TypeOf(wantErr) || gotErr.Error() != wantErr.Error()) {
						t.Fatalf("application error changed: got=%v want=%v", gotErr, wantErr)
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("snapshot application differs: got=%#v want=%#v", got, want)
					}
					if gotErr == nil {
						if test.client == client && (got.Name != "Current catalog name" || got.AvatarURL != "/p12-current.png" || !reflect.DeepEqual(got.Scopes, []string{"read"}) || !got.Active) {
							t.Fatal("global OAuth refresh changed", got)
						}
						if test.client == unregistered && got.Active {
							t.Fatal("missing built-in OAuth client remained active")
						}
						if test.workspace != workspace && len(got.AccessTeamIDs()) != 0 {
							t.Fatal("missing workspace acquired snapshot workspace grants")
						}
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					if s.db.Stats().MaxOpenConnections != 1 {
						t.Fatal("snapshot application changed the connection limit")
					}
				})
			}
		})
	}
}

func TestScaleP12GatesSnapshotApplicationLookupErrors(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for name, fixture := range scaleP03Stores(t) {
		t.Run(name, func(t *testing.T) {
			s, workspace := fixture.SQLiteStore, fixture.data.Workspace.URLKey
			tx, err := s.db.BeginTx(t.Context(), metadataReadOptions(s.dialect))
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			s.db.SetMaxOpenConns(1)
			ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: tx})
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			app := domain.ApplicationInstallation{WorkspaceKey: workspace}
			if err := s.resolveSnapshotApplicationAccess(cancelled, &app); !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled workspace lookup error changed", err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := s.resolveSnapshotApplicationAccess(ctx, &app); !errors.Is(err, sql.ErrTxDone) {
				t.Fatal("closed snapshot lookup error changed", err)
			}
		})
	}
}
