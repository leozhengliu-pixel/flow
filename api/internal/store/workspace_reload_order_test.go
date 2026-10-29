package store

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type heldReloadKey struct{}

// A server reloads its copy of a workspace (another server's peer event, a
// request that found its copy behind) while it writes the workspace itself.
// The reload read the database before the write committed; it must not put
// that older copy in place of the one the write installed, or the server
// serves its own write as undone (and, having counted the write as applied,
// never reloads again).
func TestReloadNeverReplacesTheCopyAWriteInstalledMeanwhile(t *testing.T) {
	reloads := map[string]func(*SQLiteStore, context.Context, string) error{
		"one": func(repo *SQLiteStore, ctx context.Context, workspace string) error {
			return repo.ReloadWorkspace(ctx, workspace)
		},
		"every": func(repo *SQLiteStore, ctx context.Context, _ string) error { return repo.ReloadAllWorkspaces(ctx) },
	}
	for name, pair := range peerStoreSets(t) {
		for kind, reload := range reloads {
			t.Run(name+"/"+kind, func(t *testing.T) {
				ctx := t.Context()
				repo := pair[1]
				workspace, _ := peerWorkspace(t, repo)
				// Let the writer commit while the reload still holds a read
				// snapshot, rather than waiting on SQLite's one-connection pool.
				repo.db.SetMaxOpenConns(2)
				sessionID := fmt.Sprintf("reload-order-%s-%d", kind, time.Now().UnixNano())
				if err := repo.MutateWorkspace(WithMetadataFields(ctx, "agentSessions"), workspace, "agent.message_updated", sessionID, nil, func(data *domain.Bootstrap) error {
					data.AgentSessions = append(data.AgentSessions, domain.AgentSession{ID: sessionID, UserID: data.Viewer.ID, Messages: []domain.AgentMessage{{ID: "reply", Role: "assistant", Status: "streaming", Content: "before", WorkerCursor: &domain.AgentWorkerCursor{Run: "run", Attempt: 1, Seq: 4}}}})
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				// The reload's read transaction is held once it has read, before the copy is installed.
				read, release := make(chan struct{}, 1), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				defer unblock()
				repo.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) {
					if tx.context().Value(heldReloadKey{}) != nil {
						read <- struct{}{}
						<-release
					}
				}})
				t.Cleanup(func() { repo.db.commitHooks.Store(nil) })
				reloaded := make(chan error, 1)
				go func() { reloaded <- reload(repo, context.WithValue(ctx, heldReloadKey{}, true), workspace) }()
				select {
				case <-read:
				case <-time.After(10 * time.Second):
					t.Fatal("the reload never read the workspace")
				}

				// Both serializations are valid. If reload owns writeMu, let it
				// finish first. Otherwise force the write to install first, which
				// used to let reload roll the reply back to cursor 4.
				writeFirst := repo.writeMu.TryLock()
				if writeFirst {
					repo.writeMu.Unlock()
				}
				written := make(chan error, 1)
				go func() {
					written <- repo.MutateWorkspace(WithMetadataFields(ctx, "agentSessions"), workspace, "agent.message_updated", sessionID, nil, func(data *domain.Bootstrap) error {
						index := slices.IndexFunc(data.AgentSessions, func(item domain.AgentSession) bool { return item.ID == sessionID })
						message := &data.AgentSessions[index].Messages[0]
						message.Content = "after"
						message.WorkerCursor.Seq = 6
						return nil
					})
				}()
				if writeFirst {
					select {
					case err := <-written:
						written <- err
					case <-time.After(10 * time.Second):
						t.Fatal("the write never finished while reload was paused")
					}
				}
				unblock()
				if err := <-reloaded; err != nil {
					t.Fatal(err)
				}
				if err := <-written; err != nil {
					t.Fatal(err)
				}
				data, _ := repo.WorkspaceMetadata(workspace)
				stored, err := repo.loadWorkspaceState(ctx, workspace)
				if err != nil {
					t.Fatal(err)
				}
				for source, copy := range map[string]domain.Bootstrap{"database/cache": stored, "memory": data} {
					index := slices.IndexFunc(copy.AgentSessions, func(item domain.AgentSession) bool { return item.ID == sessionID })
					if index < 0 {
						t.Fatalf("%s lost the session", source)
					}
					message := copy.AgentSessions[index].Messages[0]
					if message.Content != "after" || message.WorkerCursor == nil || message.WorkerCursor.Seq != 6 {
						t.Errorf("%s reply after reload = content %q, cursor %+v; want after, cursor 6", source, message.Content, message.WorkerCursor)
					}
				}
			})
		}
	}
}
