package store

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP12GatesAccessPolicyLockOrder(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, fixture := range scaleP03Stores(t) {
		s := fixture.SQLiteStore
		workspace := fixture.data.Workspace.URLKey
		app, err := s.InstallApplication(t.Context(), workspace, domain.ApplicationInstallation{ClientID: idgen.New("scale-p12-gates-"), Name: "Policy lock fixture", InstalledBy: fixture.data.Viewer.ID, Scopes: []string{"read"}, AllPublicTeams: true, Active: true}, "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := s.UninstallApplication(context.Background(), workspace, app.ID); err != nil {
				t.Errorf("uninstall owned application: %v", err)
			}
			if _, err := s.db.ExecContext(context.Background(), `DELETE FROM auth_users WHERE id=?`, app.UserID); err != nil {
				t.Errorf("clean up owned app principal: %v", err)
			}
		})
		s.db.SetMaxOpenConns(1)
		for name, principal := range map[string]string{"member": fixture.data.Viewer.ID, "app": app.UserID} {
			for _, mode := range []string{"owned-snapshot", "caller-snapshot", "event-source-snapshot"} {
				t.Run(name+"/"+mode, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					version, err := s.RealtimeSourceVersion(ctx, workspace)
					if err != nil {
						t.Fatal(err)
					}
					readHeld, resumeRead := make(chan struct{}), make(chan struct{})
					var pause sync.Once
					readCtx := WithSQLStatementObserver(ctx, func(query string) {
						if strings.Contains(query, "workspace_states CROSS JOIN record_journal_mode") {
							pause.Do(func() {
								close(readHeld)
								<-resumeRead
							})
						}
					})
					release := func() {}
					if mode != "owned-snapshot" {
						tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
						if err != nil {
							t.Fatal(err)
						}
						readCtx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: tx})
						release = func() { _ = tx.Rollback() }
						close(readHeld)
					}
					defer release()
					result := make(chan error, 1)
					resolve := func() {
						if mode == "event-source-snapshot" {
							_, err := s.ReadEventPolicySource(readCtx, workspace, version)
							result <- err
							return
						}
						access, err := s.ResolveAccess(readCtx, workspace, principal, nil)
						if err == nil && (access.PrincipalID != principal || access.WorkspaceID != fixture.data.Workspace.ID || access.AuthzEpoch == "") {
							err = fmt.Errorf("resolved access lost its binding")
						}
						result <- err
					}
					if mode == "owned-snapshot" {
						go resolve()
					}
					select {
					case <-readHeld:
					case <-ctx.Done():
						t.Fatal("reader did not acquire the only SQL connection")
					}
					writerLocked, writerDone := make(chan struct{}), make(chan error, 1)
					go func() {
						s.mu.Lock()
						defer s.mu.Unlock()
						close(writerLocked)
						var one int
						writerDone <- s.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
					}()
					<-writerLocked
					if mode == "owned-snapshot" {
						close(resumeRead)
					} else {
						go resolve()
					}
					err = <-result
					release()
					writerErr := <-writerDone
					if err != nil {
						t.Fatalf("policy read waited for the SQL-blocked writer: %v", err)
					}
					if writerErr != nil {
						t.Fatalf("writer did not resume after policy read: %v", writerErr)
					}
					if s.db.Stats().MaxOpenConnections != 1 {
						t.Fatal("lock-order check changed the connection limit")
					}
				})
			}
		}
	}
}

func TestScaleP12GatesSnapshotApplicationPolicy(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, fixture := range scaleP03Stores(t) {
		s := fixture.SQLiteStore
		workspace := fixture.data.Workspace.URLKey
		clientID := idgen.New("flow_client_scale_p12_")
		if err := s.MutateWorkspace(t.Context(), workspace, "scale_p12.application", clientID, nil, func(data *domain.Bootstrap) error {
			data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{ID: clientID, ClientID: clientID, Name: "Live policy name", LogoURL: "/policy.png", Scopes: []string{"read"}})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		app, err := s.InstallApplication(t.Context(), workspace, domain.ApplicationInstallation{ClientID: clientID, Name: "Installed name", InstalledBy: fixture.data.Viewer.ID, Scopes: []string{"read", "write"}, AllPublicTeams: true, Active: true}, "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := s.UninstallApplication(context.Background(), workspace, app.ID); err != nil {
				t.Errorf("uninstall owned application: %v", err)
			}
			if _, err := s.db.ExecContext(context.Background(), `DELETE FROM auth_users WHERE id=?`, app.UserID); err != nil {
				t.Errorf("clean up owned app principal: %v", err)
			}
		})
		want, err := s.ApplicationByUser(t.Context(), app.UserID)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := s.db.BeginTx(t.Context(), metadataReadOptions(s.dialect))
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: tx})
		got, err := s.ApplicationByUser(ctx, app.UserID)
		_ = tx.Rollback()
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("snapshot application policy differs from legacy: %v", err)
		}
		if got.Name != "Live policy name" || len(got.Scopes) != 1 || got.Scopes[0] != "read" {
			t.Fatal("snapshot application lost canonical name or narrowed scopes")
		}
	}
}
