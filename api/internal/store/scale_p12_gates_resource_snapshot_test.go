package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type scaleP12ResourceSnapshotFixture struct {
	*SQLiteStore
	legacy                    domain.Bootstrap
	member, app               domain.User
	project, initiative, team string
}

type scaleP12WireCount struct{ calls *int }

func (value scaleP12WireCount) MarshalJSON() ([]byte, error) {
	*value.calls++
	return []byte(`"counted"`), nil
}

type scaleP12WireJSONByte uint8

func (scaleP12WireJSONByte) MarshalJSON() ([]byte, error) {
	return []byte(`"custom\u003c\u0026\u003e"`), nil
}

type scaleP12WireJSONPointerByte uint8

func (*scaleP12WireJSONPointerByte) MarshalJSON() ([]byte, error) {
	return []byte(`"custom\u003c\u0026\u003e"`), nil
}

type scaleP12WireTextByte uint8

func (scaleP12WireTextByte) MarshalText() ([]byte, error) {
	return []byte("custom<&>"), nil
}

type scaleP12WireTextPointerByte uint8

func (*scaleP12WireTextPointerByte) MarshalText() ([]byte, error) {
	return []byte("custom<&>"), nil
}

type scaleP12WirePlainByte uint8
type scaleP12WirePlainBytes []byte

func TestScaleP12GatesInheritedResourceSinglePool(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, fixture := range scaleP03Stores(t) {
		s := fixture.SQLiteStore
		workspace := fixture.data.Workspace.URLKey
		id := idgen.New("p12-inherited-")
		if err := s.MutateWorkspace(t.Context(), workspace, "scale_p12.inherited_seed", id, nil, func(data *domain.Bootstrap) error {
			data.Projects = append(data.Projects, domain.Project{ID: id, Name: "Inherited target"})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		s.db.SetMaxOpenConns(1)
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		read, release, err := s.BeginWorkspaceRead(ctx, workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		access, err := s.ResolveAccess(read, workspace, fixture.data.Viewer.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		locked, writerDone := make(chan struct{}), make(chan error, 1)
		go func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			close(locked)
			var one int
			writerDone <- s.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
		}()
		<-locked
		_, _, err = s.ResourceReadMetadata(read, access, "projects", id)
		release()
		writerErr := <-writerDone
		if ctx.Err() != nil || writerErr != nil {
			t.Fatalf("inherited resource read deadlocked: context=%v writer=%v", ctx.Err(), writerErr)
		}
		if !errors.Is(err, ErrRecordJournalUnavailable) {
			t.Fatalf("sole connection changed live-epoch fail-closed result: %v", err)
		}
	}
}

func TestScaleP12GatesResourceSnapshotPublicationGap(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, publication := range []string{"local", "external"} {
		for _, shape := range []string{"full", "scoped"} {
			for _, known := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/%s/known=%v", publication, shape, known), func(t *testing.T) {
					for _, fixture := range scaleP03Stores(t) {
						s, workspace := fixture.SQLiteStore, fixture.data.Workspace.URLKey
						actor, token, err := s.Register(t.Context(), "Publication writer", idgen.New("p12-publication-")+"@example.test", "publication-fixture-password")
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() {
							if _, err := s.db.ExecContext(context.Background(), `DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, fixture.data.Workspace.ID, actor.ID); err != nil {
								t.Errorf("remove owned publication membership: %v", err)
							}
							if err := s.EraseAccount(context.Background(), actor.ID); err != nil {
								t.Errorf("erase owned publication writer: %v", err)
							}
						})
						if err := s.VerifyEmail(t.Context(), token); err != nil {
							t.Fatal(err)
						}
						if err := s.EnsureWorkspaceMembership(t.Context(), fixture.data.Workspace.ID, actor.ID); err != nil {
							t.Fatal(err)
						}
						id := idgen.New("p12-publication-initiative-")
						item := domain.Initiative{ID: id, Name: "Publication target", Description: "Before publication", LeadTeamID: fixture.data.Teams[0].ID}
						if err := s.MutateWorkspace(ContextWithActor(t.Context(), fixture.data.Viewer), workspace, "scale_p12.publication_seed", id, nil, func(data *domain.Bootstrap) error {
							data.Initiatives = append(data.Initiatives, item)
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						if !known {
							s.mu.Lock()
							s.forgetCopyRevisionLocked(workspace)
							s.mu.Unlock()
						}
						ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
						defer cancel()
						held, resume := make(chan struct{}), make(chan struct{})
						var once sync.Once
						readCtx := WithSQLStatementObserver(ctx, func(query string) {
							if strings.HasPrefix(query, "SELECT "+metadataStampColumns) {
								once.Do(func() {
									close(held)
									<-resume
								})
							}
						})
						if shape == "scoped" {
							readCtx = WithMetadataRecords(readCtx, MetadataRecordScope{Fields: []string{"teams", "teamSettings"}, Read: []RecordKey{{Kind: "initiatives", ID: id}}})
						}
						type result struct {
							ctx     context.Context
							release func()
							err     error
						}
						done := make(chan result, 1)
						go func() {
							read, release, err := s.BeginWorkspaceRead(readCtx, workspace)
							done <- result{read, release, err}
						}()
						unblock := sync.OnceFunc(func() { close(resume) })
						defer unblock()
						select {
						case <-held:
						case <-ctx.Done():
							t.Fatal("reader did not reach its first SQL stamp")
						}
						item.Description = "After publication"
						if publication == "local" {
							err = s.MutateWorkspace(ContextWithActor(ctx, actor), workspace, "scale_p12.publication_write", id, nil, func(data *domain.Bootstrap) error {
								for index := range data.Initiatives {
									if data.Initiatives[index].ID == id {
										data.Initiatives[index].Description = item.Description
										data.Initiatives[index].Creator = domain.User{}
									}
								}
								return nil
							})
						} else {
							var tx *sqlTx
							tx, err = s.db.BeginTx(ctx, nil)
							if err == nil {
								defer tx.Rollback()
								var raw []byte
								raw, err = json.Marshal(item)
								if err == nil {
									_, err = tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='initiatives' AND record_key=?`, raw, workspace, id)
								}
								if err == nil {
									err = tx.Commit()
								}
							}
						}
						unblock()
						read := <-done
						if read.release != nil {
							defer read.release()
						}
						if err != nil || read.err != nil || ctx.Err() != nil {
							t.Fatalf("publication gap failed: writer=%v reader=%v context=%v", err, read.err, ctx.Err())
						}
						oracle, err := s.ResolveAccess(ctx, workspace, fixture.data.Viewer.ID, nil)
						if err != nil {
							t.Fatal(err)
						}
						want, allowed, err := s.ResourceReadMetadata(ctx, oracle, "initiatives", id)
						if err != nil || !allowed || len(want.Initiatives) != 1 {
							t.Fatalf("legacy publication oracle: allowed=%v err=%v", allowed, err)
						}
						access, err := s.ResolveAccess(read.ctx, workspace, fixture.data.Viewer.ID, nil)
						if err != nil {
							t.Fatal(err)
						}
						got, gotAllowed, err := s.ResourceReadMetadata(read.ctx, access, "initiatives", id)
						left, _ := json.Marshal(want)
						right, _ := json.Marshal(got)
						if err != nil || gotAllowed != allowed || !bytes.Equal(left, right) {
							t.Fatalf("publication gap changed complete legacy body: err=%v allowed=%v/%v\nwant=%s\ngot=%s", err, allowed, gotAllowed, left, right)
						}
						if publication == "local" && want.Initiatives[0].Creator.ID != "" || publication == "external" && want.Initiatives[0].Creator.ID != fixture.data.Viewer.ID {
							t.Fatal("fixture did not distinguish fresh local publication from external normalization")
						}
						if s.db.Stats().MaxOpenConnections != 4 {
							t.Fatal("publication probe changed the existing fixture pool")
						}
					}
				})
			}
		}
	}
}

func TestScaleP12GatesResourceReadCaptureOwnership(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	source := domain.Bootstrap{Projects: make([]domain.Project, (1<<20)/int(reflect.TypeFor[domain.Project]().Size())+1)}
	source.Projects[0].ID = "capture-source"
	ctx, release := BeginHydratedSnapshotReuse(t.Context())
	defer release()
	parent := ctx.Value(hydrationSnapshotReuseKey{}).(*hydrationSnapshotReuse)
	for range 8 {
		capture, finish := beginWorkspaceReadCapture(ctx)
		owned := CloneHydratedReadSnapshot(capture, source)
		child := capture.Value(hydrationSnapshotReuseKey{}).(*hydrationSnapshotReuse)
		if child == parent || len(child.buffers) != 1 || len(parent.buffers) != 0 {
			t.Fatal("attempt capture retained storage in the parent")
		}
		finish(false)
		finish(false)
		if len(parent.buffers) != 0 || len(child.buffers) != 0 || owned.Projects[0].ID != "" {
			t.Fatal("discarded attempt retained or failed to clear its owned storage")
		}
	}
	capture, finish := beginWorkspaceReadCapture(ctx)
	owned := CloneHydratedReadSnapshot(capture, source)
	finish(true)
	finish(false)
	if len(parent.buffers) != 1 || owned.Projects[0].ID != source.Projects[0].ID || ctx.Value(hydrationSnapshotReuseKey{}) != parent {
		t.Fatal("validated capture did not transfer storage to the original parent")
	}
	release()
	if owned.Projects[0].ID != "" || source.Projects[0].ID != "capture-source" {
		t.Fatal("parent release did not clear only its owned capture")
	}
	closedCtx, closeParent := BeginHydratedSnapshotReuse(t.Context())
	capture, finish = beginWorkspaceReadCapture(closedCtx)
	live := CloneHydratedReadSnapshot(capture, source)
	closeParent()
	finish(true)
	closed := closedCtx.Value(hydrationSnapshotReuseKey{}).(*hydrationSnapshotReuse)
	if len(closed.buffers) != 0 || live.Projects[0].ID != source.Projects[0].ID {
		t.Fatal("closed parent cleared or retained a successful live capture")
	}
}

func TestScaleP12GatesResourceSnapshotRetryCancellation(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%v", cancelled), func(t *testing.T) {
			for _, fixture := range scaleP03Stores(t) {
				s, workspace := fixture.SQLiteStore, fixture.data.Workspace.URLKey
				s.mu.Lock()
				s.forgetCopyRevisionLocked(workspace)
				s.mu.Unlock()
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				deadline, _ := ctx.Deadline()
				ctx, releaseParent := BeginHydratedSnapshotReuse(ctx)
				defer releaseParent()
				parent := ctx.Value(hydrationSnapshotReuseKey{}).(*hydrationSnapshotReuse)
				attempts := 0
				ctx = WithSQLStatementObserver(ctx, func(query string) {
					if !strings.HasPrefix(query, "SELECT "+metadataStampColumns) {
						return
					}
					attempts++
					if attempts <= 8 {
						s.mu.Lock()
						s.forgetCopyRevisionLocked(workspace)
						s.mu.Unlock()
						if cancelled && attempts == 8 {
							cancel()
						}
					}
				})
				read, release, err := s.BeginWorkspaceRead(ctx, workspace)
				if release != nil {
					release()
					release()
				}
				if read.Value(hydrationSnapshotReuseKey{}) != parent {
					t.Fatal("snapshot returned the closed attempt lease instead of its parent")
				}
				if cancelled {
					if !errors.Is(err, context.Canceled) || release != nil || attempts != 8 {
						t.Fatalf("retry ignored cancellation: attempts=%d release=%v err=%v", attempts, release != nil, err)
					}
				} else {
					if err != nil || attempts != 9 {
						t.Fatalf("retry did not recover after publication churn: attempts=%d err=%v", attempts, err)
					}
					source := domain.Bootstrap{Projects: make([]domain.Project, (1<<20)/int(reflect.TypeFor[domain.Project]().Size())+1)}
					source.Projects[0].ID = "nested-capture"
					nested := CloneHydratedReadSnapshot(read, source)
					if len(parent.buffers) != 1 {
						t.Fatal("returned context lost nested snapshot reuse")
					}
					releaseParent()
					if nested.Projects[0].ID != "" || source.Projects[0].ID != "nested-capture" {
						t.Fatal("nested snapshot escaped its original parent lifetime")
					}
				}
				if len(parent.buffers) != 0 {
					t.Fatal("retry retained discarded attempt storage")
				}
				// database/sql may already have claimed a cancelled Tx while
				// its asynchronous rollback is still returning the connection.
				// Owning every existing slot proves reclamation within the
				// original deadline, without starting another timeout window.
				started := time.Now()
				initial := s.db.Stats()
				if initial.MaxOpenConnections != 4 {
					t.Fatal("retry probe changed the existing fixture pool")
				}
				cleanupCtx, stopCleanup := context.WithDeadline(t.Context(), deadline)
				connections := make([]*sql.Conn, 0, initial.MaxOpenConnections)
				closeConnections := func() {
					for _, connection := range connections {
						_ = connection.Close()
					}
				}
				for range initial.MaxOpenConnections {
					connection, err := s.db.DB.Conn(cleanupCtx)
					if err != nil {
						closeConnections()
						stopCleanup()
						t.Fatalf("SQL cleanup missed the original deadline: initialInUse=%d elapsed=%s err=%v", initial.InUse, time.Since(started), err)
					}
					connections = append(connections, connection)
					var one int
					if err := connection.QueryRowContext(cleanupCtx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
						closeConnections()
						stopCleanup()
						t.Fatalf("reclaimed SQL connection was unusable: %v", err)
					}
				}
				closeConnections()
				stopCleanup()
				t.Logf("SQL cleanup: initialInUse=%d ownedSlots=%d elapsed=%s", initial.InUse, len(connections), time.Since(started))
				if s.db.Stats().InUse != 0 || len(parent.buffers) != 0 {
					t.Fatal("retry retained a SQL connection or owned attempt storage after reclamation")
				}
			}
		})
	}
	t.Run("nested-read", func(t *testing.T) {
		for _, fixture := range scaleP03Stores(t) {
			s, workspace := fixture.SQLiteStore, fixture.data.Workspace.URLKey
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			read, release, err := s.BeginWorkspaceRead(ctx, workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			s.mu.Lock()
			done := make(chan error, 1)
			go func() {
				_, nestedRelease, err := s.BeginWorkspaceRead(read, workspace)
				if nestedRelease != nil {
					nestedRelease()
				}
				done <- err
			}()
			select {
			case err := <-done:
				s.mu.Unlock()
				if err == nil || err.Error() != "cannot start a transaction inside a workspace read snapshot" {
					t.Fatalf("nested snapshot changed its existing transaction error: %v", err)
				}
			case <-ctx.Done():
				s.mu.Unlock()
				<-done
				t.Fatal("nested snapshot waited for the SQL-held store mutex")
			}
			var one int
			if err := s.db.QueryRowContext(read, `SELECT 1`).Scan(&one); err != nil || one != 1 {
				t.Fatalf("nested rejection released the caller's snapshot: %v", err)
			}
		}
	})
}

func scaleP12ResourceSnapshotFixtures(t *testing.T) []scaleP12ResourceSnapshotFixture {
	t.Helper()
	result := []scaleP12ResourceSnapshotFixture{}
	for _, fixture := range scaleP03Stores(t) {
		s := fixture.SQLiteStore
		workspace := fixture.data.Workspace.URLKey
		member, token, err := s.Register(t.Context(), "Snapshot principal", idgen.New("scale-p12-resource-")+"@example.test", "snapshot-fixture-password")
		if err != nil {
			t.Fatalf("register principal: %v", err)
		}
		if err := s.VerifyEmail(t.Context(), token); err != nil {
			t.Fatalf("verify principal: %v", err)
		}
		if err := s.EnsureWorkspaceMembership(t.Context(), fixture.data.Workspace.ID, member.ID); err != nil {
			t.Fatalf("create principal membership: %v", err)
		}
		t.Cleanup(func() {
			ctx := context.Background()
			if _, err := s.db.ExecContext(ctx, `DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, fixture.data.Workspace.ID, member.ID); err != nil {
				t.Errorf("remove owned membership: %v", err)
			}
			if err := s.EraseAccount(ctx, member.ID); err != nil {
				t.Errorf("erase owned principal: %v", err)
			}
		})
		member, err = s.UserByID(t.Context(), member.ID)
		if err != nil {
			t.Fatalf("load principal: %v", err)
		}
		var ownerRole, ownerStatus string
		if err := s.db.QueryRowContext(t.Context(), `SELECT role,status FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, fixture.data.Workspace.ID, fixture.data.Viewer.ID).Scan(&ownerRole, &ownerStatus); err != nil || !isWorkspaceAdminRole(ownerRole) || ownerStatus != "active" {
			t.Fatalf("fixture owner lost admin membership: role=%q status=%q err=%v", ownerRole, ownerStatus, err)
		}
		installed, ok := s.BootstrapFor(workspace)
		if !ok || installed.Workspace.ID != fixture.data.Workspace.ID {
			t.Fatalf("fixture installed workspace identity changed: exists=%v same-id=%v", ok, installed.Workspace.ID == fixture.data.Workspace.ID)
		}
		app, err := s.InstallApplication(t.Context(), workspace, domain.ApplicationInstallation{ClientID: idgen.New("scale-p12-resource-app-"), Name: "Snapshot app", InstalledBy: fixture.data.Viewer.ID, AllPublicTeams: true, Scopes: []string{"read", "initiative:read"}, Active: true}, "")
		if err != nil {
			t.Fatalf("install app: %v", err)
		}
		t.Cleanup(func() {
			ctx := context.Background()
			if _, err := s.UninstallApplication(ctx, workspace, app.ID); err != nil {
				t.Errorf("uninstall owned app: %v", err)
			}
			if _, err := s.db.ExecContext(ctx, `DELETE FROM auth_users WHERE id=?`, app.UserID); err != nil {
				t.Errorf("remove owned app principal: %v", err)
			}
		})
		appUser, err := s.UserByID(t.Context(), app.UserID)
		if err != nil {
			t.Fatalf("load app: %v", err)
		}
		projectID, initiativeID := idgen.New("p12-project-"), idgen.New("p12-initiative-")
		privateTeam := idgen.New("p12-private-")
		team := fixture.data.Teams[0].ID
		status := fixture.data.ProjectStatuses[0]
		stale := member
		stale.Name, stale.DisplayName = "Stale actor", "Stale actor"
		project := domain.Project{ID: projectID, Name: "Snapshot project", SlugID: "new-" + projectID, PreviousSlugIDs: []string{"old-" + projectID}, Description: strings.Repeat("Complete project body ", 1024), TeamIDs: []string{team}, Status: status, Lead: &stale, Creator: &stale, DependencyIDs: []string{projectID + "-hidden"}, Initiatives: []string{initiativeID}, Resources: []domain.ProjectResource{{ID: projectID + "-resource", Title: "Full resource", URL: "/uploads/resource.png"}}, Comments: []domain.Comment{{ID: projectID + "-comment", Body: "Full project comment", User: stale}}, DescriptionRevisions: []domain.ProjectDescriptionRevision{{ID: projectID + "-history", Description: "Complete project history", Author: stale}}}
		initiative := domain.Initiative{ID: initiativeID, Name: "Snapshot initiative", SlugID: "new-" + initiativeID, PreviousSlugIDs: []string{"old-" + initiativeID}, Description: strings.Repeat("Complete initiative body ", 1024), LeadTeamID: team, ProjectIDs: []string{projectID, projectID + "-hidden"}, Owner: &stale, Comments: []domain.Comment{{ID: initiativeID + "-comment", Body: "Full initiative comment", User: stale}}, DescriptionHistory: []domain.InitiativeDescriptionRevision{{ID: initiativeID + "-history", Description: "Complete initiative history", Editor: stale}}}
		rawSeeds := map[string][]byte{}
		for kind, value := range map[string]any{"projects": project, "initiatives": initiative} {
			rawSeeds[kind], err = json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
		}
		err = s.MutateWorkspace(ContextWithActor(t.Context(), fixture.data.Viewer), workspace, "scale_p12.snapshot_seed", projectID, nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams, domain.Team{ID: privateTeam, Private: true})
			data.TeamSettings[privateTeam] = domain.TeamSettings{TeamID: privateTeam, Access: "private"}
			data.Projects = append(data.Projects, project, domain.Project{ID: projectID + "-hidden", Name: "Hidden project", TeamIDs: []string{privateTeam}})
			data.Initiatives = append(data.Initiatives, initiative)
			return nil
		})
		if err != nil {
			t.Fatalf("seed resource records: %v", err)
		}
		// Canonical legacy rows can precede load-time normalization. Keep
		// these intentionally unnormalized while the installed oracle is
		// normalized on reload.
		for kind, raw := range rawSeeds {
			id := projectID
			if kind == "initiatives" {
				id = initiativeID
			}
			if _, err := s.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field=? AND record_key=?`, raw, workspace, kind, id); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatalf("reload normalized oracle: %v", err)
		}
		legacy, ok := s.BootstrapFor(workspace)
		if !ok || legacy.Viewer.ID == member.ID || legacy.Viewer.ID == appUser.ID {
			t.Fatal("fixture must retain an installed viewer different from both principals")
		}
		s.db.SetMaxOpenConns(1)
		result = append(result, scaleP12ResourceSnapshotFixture{s, legacy, member, appUser, projectID, initiativeID, team})
	}
	return result
}

func scaleP12ResourceSnapshot(t *testing.T, f scaleP12ResourceSnapshotFixture, ctx context.Context, shape, kind, id string) (context.Context, func()) {
	t.Helper()
	if shape == "scoped" || shape == "scoped-undeclared" {
		scope := MetadataRecordScope{Fields: []string{"teams", "teamSettings"}}
		if shape == "scoped" {
			scope.Read = []RecordKey{{Kind: kind, ID: id}}
		}
		ctx = WithMetadataRecords(ctx, scope)
	}
	read, release, err := f.BeginWorkspaceRead(ctx, f.legacy.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := read.Value(workspaceReadKey{}).(workspaceRead)
	switch shape {
	case "empty":
		snapshot.metadata = domain.Bootstrap{}
	case "summary":
		snapshot.metadata = domain.Bootstrap{Workspace: snapshot.metadata.Workspace, Viewer: snapshot.metadata.Viewer, Projects: []domain.Project{{ID: f.project, Name: "Summary project"}}, Initiatives: []domain.Initiative{{ID: f.initiative, Name: "Summary initiative"}}}
	}
	return context.WithValue(read, workspaceReadKey{}, snapshot), release
}

func scaleP12ReadResourceSnapshot(ctx context.Context, f scaleP12ResourceSnapshotFixture, access AccessContext, path, kind, id string) (domain.Bootstrap, bool, error) {
	if path == "public" {
		return f.ResourceReadMetadata(ctx, access, kind, id)
	}
	source, err := f.resourceReadRecords(ctx, access.policy, kind, id)
	if err != nil {
		return domain.Bootstrap{}, false, err
	}
	return resourceReadMetadata(access.policy, source, kind, id)
}

func TestScaleP12GatesInheritedResourceLockOrder(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, journal := range []string{"on", "off"} {
		t.Run(journal, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_JOURNAL", journal)
			for _, f := range scaleP12ResourceSnapshotFixtures(t) {
				for _, path := range []string{"public", "internal"} {
					for _, kind := range []string{"projects", "initiatives"} {
						id := f.project
						if kind == "initiatives" {
							id = f.initiative
						}
						for _, shape := range []string{"full", "empty", "scoped", "summary"} {
							t.Run(path+"/"+kind+"/"+shape, func(t *testing.T) {
								ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
								defer cancel()
								read, release := scaleP12ResourceSnapshot(t, f, ctx, shape, kind, id)
								defer release()
								access, err := f.ResolveAccess(read, f.legacy.Workspace.URLKey, f.member.ID, nil)
								if err != nil {
									t.Fatal(err)
								}
								locked, writerDone := make(chan struct{}), make(chan error, 1)
								go func() {
									f.mu.Lock()
									defer f.mu.Unlock()
									close(locked)
									var one int
									writerDone <- f.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
								}()
								<-locked
								_, _, readErr := scaleP12ReadResourceSnapshot(read, f, access, path, kind, id)
								var one int
								snapshotErr := f.db.QueryRowContext(read, `SELECT 1`).Scan(&one)
								release()
								writerErr := <-writerDone
								if ctx.Err() != nil || writerErr != nil || snapshotErr != nil {
									t.Fatalf("inherited read waited for the SQL-blocked writer or released the caller snapshot: context=%v writer=%v snapshot=%v", ctx.Err(), writerErr, snapshotErr)
								}
								if path == "public" && journal == "on" {
									if !errors.Is(readErr, ErrRecordJournalUnavailable) {
										t.Fatalf("sole connection must retain live-epoch fail-closed behavior: %v", readErr)
									}
								} else if readErr != nil {
									t.Fatal(readErr)
								}
								if f.db.Stats().MaxOpenConnections != 1 {
									t.Fatal("inherited read changed the pool limit")
								}
							})
						}
					}
				}
			}
		})
	}
}

func TestScaleP12GatesResourceSnapshotLegacyBodies(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, journal := range []string{"on", "off"} {
		t.Run(journal, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_JOURNAL", journal)
			for _, f := range scaleP12ResourceSnapshotFixtures(t) {
				for _, principal := range []struct {
					name string
					user domain.User
					key  *domain.APIKey
				}{
					{"member", f.member, nil},
					{"unrestricted", f.member, &domain.APIKey{ID: "unrestricted", TeamRestriction: "all"}},
					{"selected-empty", f.member, &domain.APIKey{ID: "selected-empty", TeamRestriction: "selected", TeamIDs: []string{}}},
					{"selected-set", f.member, &domain.APIKey{ID: "selected-set", TeamRestriction: "selected", TeamIDs: []string{f.team}}},
					{"app", f.app, &domain.APIKey{ID: "app", Actor: "app", Scopes: []string{"read", "initiative:read"}, TeamRestriction: "application"}},
				} {
					for _, kind := range []string{"projects", "initiatives"} {
						id := f.project
						if kind == "initiatives" {
							id = f.initiative
						}
						for _, ref := range []string{id, "old-" + id} {
							for _, shape := range []string{"full", "empty", "scoped", "summary"} {
								t.Run(fmt.Sprintf("%s/%s/%s/%s", principal.name, kind, ref, shape), func(t *testing.T) {
									read, release := scaleP12ResourceSnapshot(t, f, t.Context(), shape, kind, id)
									defer release()
									access, err := f.ResolveAccess(read, f.legacy.Workspace.URLKey, principal.user.ID, principal.key)
									if err != nil {
										t.Fatal(err)
									}
									want, allowed, err := resourceReadMetadata(access.policy, f.legacy, kind, ref)
									if err != nil {
										t.Fatal(err)
									}
									got, gotAllowed, err := scaleP12ReadResourceSnapshot(read, f, access, "internal", kind, ref)
									if err != nil {
										t.Fatal(err)
									}
									left, _ := json.Marshal(want)
									right, _ := json.Marshal(got)
									if gotAllowed != allowed || !bytes.Equal(left, right) {
										t.Fatalf("complete legacy body changed for %s: allowed=%v/%v\nwant=%s\ngot=%s", shape, allowed, gotAllowed, left, right)
									}
									if allowed && kind == "initiatives" && len(got.Initiatives) == 1 && got.Initiatives[0].Creator.ID != f.legacy.Viewer.ID {
										t.Fatal("missing creator defaulted to the principal instead of the installed viewer")
									}
									if allowed && kind == "projects" && len(got.Projects) == 1 && slices.Contains(got.Projects[0].DependencyIDs, f.project+"-hidden") {
										t.Fatal("hidden reference escaped the selected resource body")
									}
									if journal == "off" {
										actual, actualAllowed, err := scaleP12ReadResourceSnapshot(read, f, access, "public", kind, ref)
										raw, _ := json.Marshal(actual)
										if err != nil || actualAllowed != allowed || !bytes.Equal(left, raw) {
											t.Fatalf("journal-off inherited public read changed legacy body: %v", err)
										}
									}
								})
							}
						}
					}
				}
			}
		})
	}
}

func TestScaleP12GatesFreshResourceSnapshotBodies(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	for _, f := range scaleP12ResourceSnapshotFixtures(t) {
		err := f.MutateWorkspace(t.Context(), f.legacy.Workspace.URLKey, "scale_p12.fresh_snapshot", f.project, nil, func(data *domain.Bootstrap) error {
			for i := range data.Projects {
				if data.Projects[i].ID == f.project {
					data.Projects[i].Resources[0].PinnedTeamIDs = nil
				}
			}
			for i := range data.Initiatives {
				if data.Initiatives[i].ID == f.initiative {
					data.Initiatives[i].Creator = domain.User{}
					data.Initiatives[i].UpdateSchedule = domain.InitiativeUpdateSchedule{}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"projects", "initiatives"} {
			id := f.project
			if kind == "initiatives" {
				id = f.initiative
			}
			access, err := f.ResolveAccess(t.Context(), f.legacy.Workspace.URLKey, f.member.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			want, allowed, err := f.ResourceReadMetadata(t.Context(), access, kind, id)
			if err != nil {
				t.Fatal(err)
			}
			for _, shape := range []string{"full", "scoped", "scoped-undeclared", "empty", "summary"} {
				for _, unknown := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/unknown=%v", kind, shape, unknown), func(t *testing.T) {
						if unknown {
							f.mu.Lock()
							saved, known := f.copyRevisions[f.legacy.Workspace.URLKey]
							f.forgetCopyRevisionLocked(f.legacy.Workspace.URLKey)
							f.mu.Unlock()
							defer func() {
								if known {
									f.mu.Lock()
									f.copyRevisions[f.legacy.Workspace.URLKey] = saved
									f.mu.Unlock()
								}
							}()
						}
						read, release := scaleP12ResourceSnapshot(t, f, t.Context(), shape, kind, id)
						defer release()
						access, err := f.ResolveAccess(read, f.legacy.Workspace.URLKey, f.member.ID, nil)
						if err != nil {
							t.Fatal(err)
						}
						got, gotAllowed, err := f.ResourceReadMetadata(read, access, kind, id)
						left, _ := json.Marshal(want)
						right, _ := json.Marshal(got)
						if err != nil || gotAllowed != allowed || !bytes.Equal(left, right) {
							t.Fatalf("freshly written %s changed through %s snapshot: %v", kind, shape, err)
						}
					})
				}
			}
		}
	}
}

func TestScaleP12GatesLargeFreshResourceSnapshotBodies(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	for _, f := range scaleP12ResourceSnapshotFixtures(t) {
		blocks := make([]any, 11000)
		for index := range blocks {
			blocks[index] = map[string]any{"type": "p"}
		}
		if err := f.MutateWorkspace(t.Context(), f.legacy.Workspace.URLKey, "scale_p12.large_fresh", f.project, nil, func(data *domain.Bootstrap) error {
			for index := range data.Projects {
				if data.Projects[index].ID == f.project {
					data.Projects[index].Resources[0].PinnedTeamIDs = nil
					data.Projects[index].Comments[0].BodyData = map[string]any{"blocks": blocks}
				}
			}
			for index := range data.Initiatives {
				if data.Initiatives[index].ID == f.initiative {
					data.Initiatives[index].Creator = domain.User{}
					data.Initiatives[index].Comments[0].BodyData = map[string]any{"blocks": blocks}
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"projects", "initiatives"} {
			id := f.project
			if kind == "initiatives" {
				id = f.initiative
			}
			access, err := f.ResolveAccess(t.Context(), f.legacy.Workspace.URLKey, f.member.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			want, allowed, err := f.ResourceReadMetadata(t.Context(), access, kind, id)
			if err != nil || !allowed {
				t.Fatalf("large legacy oracle: allowed=%v err=%v", allowed, err)
			}
			if reservation := HydratedSnapshotReservation(want) / 4; reservation <= 2<<20 {
				t.Fatalf("fixture must exceed 2MiB of mutable graph, got %d", reservation)
			}
			for _, shape := range []string{"full", "empty", "summary", "scoped-undeclared"} {
				t.Run(kind+"/"+shape, func(t *testing.T) {
					read, release := scaleP12ResourceSnapshot(t, f, t.Context(), shape, kind, id)
					defer release()
					access, err := f.ResolveAccess(read, f.legacy.Workspace.URLKey, f.member.ID, nil)
					if err != nil {
						t.Fatal(err)
					}
					got, gotAllowed, err := f.ResourceReadMetadata(read, access, kind, "old-"+id)
					left, _ := json.Marshal(want)
					right, _ := json.Marshal(got)
					if err != nil || gotAllowed != allowed || !bytes.Equal(left, right) {
						t.Fatalf("large fresh old-slug target changed through %s: %v", shape, err)
					}
				})
			}
		}
	}
}

func TestScaleP12GatesResourceWireDigest(t *testing.T) {
	raw := json.RawMessage(` { "b": "<&>` + "\u2028\u2029" + `", "a" : [1, null] } `)
	bigMap := make(map[string]any, 400)
	for index := range 400 {
		bigMap[fmt.Sprintf("key-%04d", 399-index)] = []any{index, json.Number("1e-9"), nil, index%2 == 0}
	}
	bigMap["\xff<&>\u2028"] = strings.Repeat("a", 4095) + "中\xff" + strings.Repeat("b", 4095) + "\x80"
	text := strings.Repeat("body<&>\u2028\u2029\xff", 8192)
	project := domain.Project{ID: "digest", Description: text, CreatedAt: time.Date(2026, 11, 13, 1, 2, 3, 999, time.FixedZone("offset", -7*3600)),
		Resources: []domain.ProjectResource{{ID: "raw", PinnedTeamIDs: nil}, {ID: "empty", PinnedTeamIDs: []string{}}},
		Comments:  []domain.Comment{{BodyData: bigMap, Reactions: map[string][]string{"<&>": nil, "empty": {}}}},
	}
	type wrapped struct {
		Value string `json:"value,string"`
	}
	type zero struct {
		Value int `json:"value,omitzero"`
	}
	duplicate := reflect.New(reflect.StructOf([]reflect.StructField{
		{Name: "One", Type: reflect.TypeFor[string](), Tag: `json:"same"`},
		{Name: "Two", Type: reflect.TypeFor[string](), Tag: `json:"same"`},
	})).Elem()
	duplicate.Field(0).SetString("one")
	duplicate.Field(1).SetString("two")
	type nested struct {
		Time   time.Time               `json:"time"`
		Custom domain.ActivityMetadata `json:"custom"`
	}
	var deep any = "leaf"
	for range 1100 {
		deep = []any{deep}
	}
	for _, value := range []any{project, domain.Initiative{ID: "digest", Description: text}, bigMap, raw, &raw, json.RawMessage(nil),
		[]byte{1, 2, 3, 255}, []byte(nil), []byte{}, []any{float64(1e-9), math.Copysign(0, -1), float32(1e20)}, wrapped{Value: "<body>"},
		zero{}, zero{Value: 1}, duplicate.Interface(), nested{Time: project.CreatedAt, Custom: domain.ActivityMetadata{"key": "value"}}, deep} {
		want, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := resourceWireDigest(t.Context(), value)
		if err != nil || got != sha256.Sum256(want) {
			t.Fatalf("%T wire digest changed: %v", value, err)
		}
	}
	for _, value := range []any{
		[]scaleP12WireJSONByte{1}, []scaleP12WireJSONPointerByte{1},
		[]scaleP12WireTextByte{1}, []scaleP12WireTextPointerByte{1},
		[]scaleP12WireJSONByte{}, []scaleP12WireJSONPointerByte{},
		[]scaleP12WireTextByte{}, []scaleP12WireTextPointerByte{},
		[]scaleP12WireJSONByte(nil), []scaleP12WireJSONPointerByte(nil),
		[]scaleP12WireTextByte(nil), []scaleP12WireTextPointerByte(nil),
		[]scaleP12WirePlainByte{1}, scaleP12WirePlainBytes{1},
		[1]scaleP12WireJSONByte{1}, [1]scaleP12WireJSONPointerByte{1},
		[1]scaleP12WireTextByte{1}, [1]scaleP12WireTextPointerByte{1},
		new([1]scaleP12WireJSONPointerByte), new([1]scaleP12WireTextPointerByte),
	} {
		for _, wrapped := range []any{value, domain.Project{
			ID: "byte-digest", Comments: []domain.Comment{{BodyData: map[string]any{"bytes": value}}},
		}} {
			want, err := json.Marshal(wrapped)
			if err != nil {
				t.Fatal(err)
			}
			got, err := resourceWireDigest(t.Context(), wrapped)
			if err != nil || got != sha256.Sum256(want) {
				t.Fatalf("%T byte collection wire digest changed in %T: %v", value, wrapped, err)
			}
		}
	}
	cycle := []any{nil}
	cycle[0] = cycle
	for _, value := range []any{math.NaN(), json.RawMessage(`{`), cycle} {
		if _, err := resourceWireDigest(t.Context(), value); err == nil {
			t.Fatalf("invalid %T must not produce a witness", value)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := resourceWireDigest(ctx, project); !errors.Is(err, context.Canceled) {
		t.Fatalf("digest ignored cancellation: %v", err)
	}
	if !reflect.DeepEqual(bigMap["key-0000"], []any{399, json.Number("1e-9"), nil, false}) {
		t.Fatal("wire digest mutated rich-text input")
	}
}

func TestScaleP12GatesResourceSnapshotOverlappingCatalogWriter(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	for _, f := range scaleP12ResourceSnapshotFixtures(t) {
		if err := f.MutateWorkspace(t.Context(), f.legacy.Workspace.URLKey, "scale_p12.fresh_writer", f.project, nil, func(data *domain.Bootstrap) error {
			for index := range data.Projects {
				if data.Projects[index].ID == f.project {
					data.Projects[index].Resources[0].PinnedTeamIDs = nil
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, event := range []string{"alm.projects_imported", "team.deleted"} {
			t.Run(event, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				access, err := f.ResolveAccess(ctx, f.legacy.Workspace.URLKey, f.member.ID, nil)
				if err != nil {
					t.Fatal(err)
				}
				want, allowed, err := f.ResourceReadMetadata(ctx, access, "projects", f.project)
				if err != nil || !allowed {
					t.Fatalf("fresh oracle: allowed=%v err=%v", allowed, err)
				}
				read, release := scaleP12ResourceSnapshot(t, f, ctx, "scoped-undeclared", "projects", f.project)
				defer release()
				access, err = f.ResolveAccess(read, f.legacy.Workspace.URLKey, f.member.ID, nil)
				if err != nil {
					t.Fatal(err)
				}
				changed, finish, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
				stop := errors.New("rollback overlapping catalog writer")
				go func() {
					writer := withMutationAggregateID(ctx, f.team)
					done <- f.mutateTeamMetadata(writer, f.legacy.Workspace.URLKey, event, nil, func(data *domain.Bootstrap) (string, error) {
						if event == "team.deleted" {
							if err := ApplyTeamDeletion(data, f.team); err != nil {
								return "", err
							}
						} else {
							for index := range data.Projects {
								if data.Projects[index].ID == f.project {
									data.Projects[index].Name = "Import in progress"
									data.Projects[index].Resources[0].PinnedTeamIDs = []string{f.team}
								}
							}
						}
						close(changed)
						select {
						case <-finish:
						case <-ctx.Done():
						}
						return f.team, stop
					})
				}()
				select {
				case <-changed:
				case err := <-done:
					t.Fatalf("catalog writer did not reach callback: %v", err)
				case <-ctx.Done():
					t.Fatal("catalog writer did not reach callback")
				}
				got, gotAllowed, err := f.ResourceReadMetadata(read, access, "projects", "old-"+f.project)
				release()
				close(finish)
				writerErr := <-done
				left, _ := json.Marshal(want)
				right, _ := json.Marshal(got)
				if err != nil || ctx.Err() != nil || !errors.Is(writerErr, stop) || gotAllowed != allowed || !bytes.Equal(left, right) {
					t.Fatalf("catalog writer changed captured resource: read=%v writer=%v context=%v", err, writerErr, ctx.Err())
				}
			})
		}
	}
}

func TestScaleP12GatesResourceSnapshotWireCache(t *testing.T) {
	calls := 0
	project := domain.Project{ID: "cache-project", Description: "old", Comments: []domain.Comment{{BodyData: map[string]any{"counter": scaleP12WireCount{&calls}}}}}
	plain := project
	plain.Comments = []domain.Comment{{BodyData: map[string]any{"counter": "counted"}}}
	workspace := "cache-workspace"
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: "cache-workspace-id"}, Projects: []domain.Project{project}}
	s := &SQLiteStore{workspaces: map[string]domain.Bootstrap{workspace: data}}
	capture := func(workspace string) resourceReadCompatibilitySnapshot {
		t.Helper()
		s.mu.RLock()
		source := s.workspaces[workspace]
		digests, err := s.resourceReadCompatibilityDigestsLocked(t.Context(), workspace, source)
		s.mu.RUnlock()
		if err != nil {
			t.Fatal(err)
		}
		return resourceReadCompatibilitySnapshot{data: domain.Bootstrap{Workspace: source.Workspace}, digests: digests}
	}
	old := capture(workspace)
	capture(workspace)
	if calls != 1 {
		t.Fatalf("unchanged publication rebuilt witness %d times", calls)
	}
	s.mu.Lock()
	s.setCopyRevisionLocked(workspace, copyStamp{workspaceID: data.Workspace.ID, revision: 1})
	proof, _ := s.copyRevisionLocked(workspace)
	s.mu.Unlock()
	capture(workspace)
	if calls != 2 {
		t.Fatal("known publication did not invalidate witness")
	}
	s.mu.Lock()
	fresh := data
	fresh.Projects = slices.Clone(data.Projects)
	fresh.Projects[0].Description = "fresh unknown"
	s.setWorkspaceLocked(workspace, fresh)
	_, known := s.copyRevisionLocked(workspace)
	s.mu.Unlock()
	current := capture(workspace)
	if known || calls != 3 {
		t.Fatal("unknown publication reused a stale witness or certified a revision")
	}
	oldPlain := domain.Bootstrap{Projects: []domain.Project{plain}}
	freshPlain := oldPlain
	freshPlain.Projects = slices.Clone(oldPlain.Projects)
	freshPlain.Projects[0].Description = "fresh unknown"
	if matches, err := old.matches(t.Context(), oldPlain); err != nil || !matches {
		t.Fatalf("new publication mutated retained old witness: %v", err)
	}
	if matches, err := old.matches(t.Context(), freshPlain); err != nil || matches {
		t.Fatalf("retained old witness accepted a fresh body: %v", err)
	}
	if matches, err := current.matches(t.Context(), freshPlain); err != nil || !matches {
		t.Fatalf("new witness rejected fresh body: %v", err)
	}
	s.mu.Lock()
	s.forgetCopyRevisionLocked(workspace)
	s.mu.Unlock()
	capture(workspace)
	if calls != 4 {
		t.Fatal("rollback invalidation reused old wire identities")
	}
	s.mu.Lock()
	s.setCopyRevisionLocked(workspace, copyStamp{workspaceID: data.Workspace.ID, revision: 1})
	restored, _ := s.copyRevisionLocked(workspace)
	s.mu.Unlock()
	if restored.workspaceID != proof.workspaceID || restored.revision != proof.revision || restored.epoch <= proof.epoch {
		t.Fatal("publication invalidation changed the SQL revision proof")
	}
	capture(workspace)
	other := fresh
	other.Workspace.ID = "different-workspace-id"
	other.Projects = slices.Clone(fresh.Projects)
	other.Projects[0].Description = "other workspace"
	s.mu.Lock()
	// Deliberately hold the publication epoch constant: both identity
	// components must independently distinguish the last-workspace cache.
	s.workspaces["other-workspace"] = other
	s.mu.Unlock()
	capture("other-workspace")
	if calls != 6 || s.resourceWireCache.workspace != "other-workspace" {
		t.Fatal("different workspace reused cached identity set")
	}
	s.mu.Lock()
	other.Workspace.ID = "recreated-workspace-id"
	s.workspaces["other-workspace"] = other
	s.mu.Unlock()
	capture("other-workspace")
	if calls != 7 || s.resourceWireCache.workspaceID != other.Workspace.ID {
		t.Fatal("recreated workspace reused cached identity set")
	}
	// The fixed cache ceiling never changes read results. Oversized
	// identities remain request-owned and are recomputed on the next read.
	other.Projects = slices.Clone(other.Projects)
	other.Projects[0].ID = strings.Repeat("i", resourceReadWireCacheMaxBytes/2)
	s.mu.Lock()
	s.setWorkspaceLocked("oversized", other)
	s.mu.Unlock()
	oversized := capture("oversized")
	capture("oversized")
	if calls != 9 || s.resourceWireCache.digests != nil {
		t.Fatal("oversized identity set escaped the cache ceiling")
	}
	largePlain := other
	largePlain.Projects = slices.Clone(other.Projects)
	largePlain.Projects[0].Comments = plain.Comments
	if matches, err := oversized.matches(t.Context(), largePlain); err != nil || !matches {
		t.Fatalf("uncached oversized witness changed exact comparison: %v", err)
	}
	duplicates := other
	nearLimit := (resourceReadWireCacheMaxBytes-256-256)/2 - len("projects")
	duplicate := other.Projects[0]
	duplicate.ID = duplicate.ID[:nearLimit]
	duplicates.Projects = []domain.Project{duplicate, duplicate}
	s.mu.Lock()
	s.setWorkspaceLocked("duplicates", duplicates)
	s.mu.Unlock()
	duplicated := capture("duplicates")
	if len(duplicated.digests) != 1 || s.resourceWireCache.digests != nil || calls != 11 {
		t.Fatal("deduplicated identities hid preallocated bucket capacity from the ceiling")
	}
	duplicate.Comments = plain.Comments
	if matches, err := duplicated.matches(t.Context(), domain.Bootstrap{Projects: []domain.Project{duplicate}}); err != nil || !matches {
		t.Fatalf("uncached duplicate-cap witness changed exact comparison: %v", err)
	}
}

func TestScaleP12GatesResourceSnapshotStatusOrdinal(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	for _, f := range scaleP12ResourceSnapshotFixtures(t) {
		workspace := f.legacy.Workspace.URLKey
		suffix := idgen.New("p12-status-ordinal-")
		earlier := f.legacy.ProjectStatuses[0]
		earlier.ID, earlier.Name, earlier.Position = "a-"+suffix, "Earlier status", 0
		target := earlier
		target.ID, target.Name = "b-"+suffix, "Target status"
		if err := f.MutateWorkspace(t.Context(), workspace, "scale_p12.status_ordinal", f.project, nil, func(data *domain.Bootstrap) error {
			data.ProjectStatuses = append(data.ProjectStatuses, earlier, target)
			for index := range data.Projects {
				if data.Projects[index].ID == f.project {
					data.Projects[index].Status = target
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, status := range []domain.ProjectStatus{earlier, target} {
			raw, err := json.Marshal(status)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=?,collection_order=500 WHERE workspace_key=? AND field='projectStatuses' AND `+f.metadataRecordKeyEquals("record_key", "?"), raw, workspace, status.ID); err != nil {
				t.Fatal(err)
			}
		}
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND `+f.metadataRecordKeyEquals("record_key", "?"), workspace, f.project).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var project domain.Project
		if err := json.Unmarshal(raw, &project); err != nil {
			t.Fatal(err)
		}
		project.Status = target
		raw, err := json.Marshal(project)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='projects' AND `+f.metadataRecordKeyEquals("record_key", "?"), raw, workspace, f.project); err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		f.forgetCopyRevisionLocked(workspace)
		f.mu.Unlock()
		if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		legacy, ok := f.BootstrapFor(workspace)
		if !ok {
			t.Fatal("legacy ordinal oracle is missing")
		}
		index := slices.IndexFunc(legacy.ProjectStatuses, func(status domain.ProjectStatus) bool { return status.ID == target.ID })
		if index <= 0 || legacy.ProjectStatuses[index].Position != float64(index) || index == 500 {
			t.Fatal("fixture must normalize sparse, tied collection order to a dense global ordinal")
		}
		access, err := f.ResolveAccess(t.Context(), workspace, f.member.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		want, allowed, err := f.ResourceReadMetadata(t.Context(), access, "projects", f.project)
		if err != nil || !allowed || len(want.Projects) != 1 || want.Projects[0].Status.Position != float64(index) {
			t.Fatalf("legacy resource ordinal oracle: allowed=%v err=%v", allowed, err)
		}
		for _, shape := range []string{"full", "empty", "scoped", "scoped-undeclared", "summary"} {
			for _, ref := range []string{f.project, "old-" + f.project} {
				t.Run(shape+"/"+ref, func(t *testing.T) {
					read, release := scaleP12ResourceSnapshot(t, f, t.Context(), shape, "projects", f.project)
					defer release()
					access, err := f.ResolveAccess(read, workspace, f.member.ID, nil)
					if err != nil {
						t.Fatal(err)
					}
					got, gotAllowed, err := f.ResourceReadMetadata(read, access, "projects", ref)
					if err != nil || gotAllowed != allowed || len(got.Projects) != 1 {
						t.Fatalf("inherited ordinal resource read: allowed=%v err=%v", gotAllowed, err)
					}
					left, _ := json.Marshal(want)
					right, _ := json.Marshal(got)
					if !bytes.Equal(left, right) {
						t.Fatalf("global status ordinal changed: want=%g got=%g", want.Projects[0].Status.Position, got.Projects[0].Status.Position)
					}
				})
			}
		}
	}
}

func TestScaleP12GatesResourceSnapshotMissingStatus(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	for _, catalog := range []string{"nonempty", "empty"} {
		t.Run(catalog, func(t *testing.T) {
			for _, f := range scaleP12ResourceSnapshotFixtures(t) {
				workspace := f.legacy.Workspace.URLKey
				target := f.legacy.ProjectStatuses[0]
				target.Name, target.Description, target.Position = "Embedded status retained", "Legacy embedded details", 37
				custom := target
				custom.ID, custom.Name = idgen.New("p12-custom-status-"), "Custom status"
				if err := f.MutateWorkspace(t.Context(), workspace, "scale_p12.missing_status", f.project, nil, func(data *domain.Bootstrap) error {
					data.ProjectStatuses = []domain.ProjectStatus{}
					if catalog == "nonempty" {
						data.ProjectStatuses = []domain.ProjectStatus{custom}
					}
					for index := range data.Projects {
						if data.Projects[index].ID == f.project {
							data.Projects[index].Status = target
							data.Projects[index].Resources[0].PinnedTeamIDs = nil
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				var count int
				if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='projectStatuses'`, workspace).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if (catalog == "empty" && count != 0) || (catalog == "nonempty" && count != 1) {
					t.Fatalf("fixture status catalog changed: %s count=%d", catalog, count)
				}
				f.mu.Lock()
				f.forgetCopyRevisionLocked(workspace)
				f.mu.Unlock()
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				access, err := f.ResolveAccess(t.Context(), workspace, f.member.ID, nil)
				if err != nil {
					t.Fatal(err)
				}
				want, allowed, err := f.ResourceReadMetadata(t.Context(), access, "projects", f.project)
				if err != nil || !allowed || len(want.Projects) != 1 {
					t.Fatalf("legacy missing-status oracle: allowed=%v err=%v", allowed, err)
				}
				if retained := want.Projects[0].Status.Name == target.Name; retained != (catalog == "nonempty") {
					t.Fatal("legacy oracle must distinguish missing target from empty catalog")
				}
				for _, shape := range []string{"full", "empty", "scoped", "scoped-undeclared", "summary"} {
					for _, ref := range []string{f.project, "old-" + f.project} {
						t.Run(shape+"/"+ref, func(t *testing.T) {
							read, release := scaleP12ResourceSnapshot(t, f, t.Context(), shape, "projects", f.project)
							defer release()
							access, err := f.ResolveAccess(read, workspace, f.member.ID, nil)
							if err != nil {
								t.Fatal(err)
							}
							got, gotAllowed, err := f.ResourceReadMetadata(read, access, "projects", ref)
							if err != nil || gotAllowed != allowed || len(got.Projects) != 1 {
								t.Fatalf("inherited missing-status read: allowed=%v err=%v", gotAllowed, err)
							}
							left, _ := json.Marshal(want)
							right, _ := json.Marshal(got)
							if !bytes.Equal(left, right) {
								t.Fatalf("missing-status catalog semantics changed: want=%+v got=%+v", want.Projects[0].Status, got.Projects[0].Status)
							}
						})
					}
				}
			}
		})
	}
}
