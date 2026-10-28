package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Two servers share the Redis hot cache. Server A's write is held after its
// commit, before it drops the cached copies it retired; meanwhile server B,
// which cached the old counts, page and metadata just before, reads them
// again and must see the write: the caches are keyed by revisions the
// writing transaction moved on, not only by what the writer invalidates
// after commit.
func TestHotCacheNeverServesDataOlderThanACommittedWrite(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			writer, reader := pair[0], pair[1]
			workspace, template := peerWorkspace(t, writer)
			data, _ := writer.WorkspaceMetadata(workspace)
			states := []domain.WorkflowState{}
			for _, state := range data.States {
				if state.TeamID == template.Team.ID || state.TeamID == "" {
					states = append(states, state)
				}
			}
			if len(states) < 2 || len(data.Labels) == 0 {
				t.Fatalf("fixture needs two states and a label: %d, %d", len(states), len(data.Labels))
			}
			stamp := time.Now().UnixNano()
			template.Team.ID = fmt.Sprintf("window-team-%d", stamp)
			template.State = states[0]
			issue := createPeerIssue(t, writer, workspace, fmt.Sprintf("window-%d", stamp), template)

			// held runs write on A with its commit held after it committed
			// (and numbered its events) but before it returned to drop the
			// cached copies, runs check on B meanwhile, then lets A finish.
			held := func(write func(context.Context) error, check func()) {
				t.Helper()
				mark, reached, release := pauseCommits(t, pair[:], "sequenced")
				var done sync.WaitGroup
				var writeErr error
				done.Add(1)
				go func() {
					defer done.Done()
					writeErr = write(mark(ctx))
				}()
				select {
				case <-reached:
				case <-time.After(10 * time.Second):
					close(release)
					t.Fatal("the write never reached its commit")
				}
				check()
				close(release)
				done.Wait()
				if writeErr != nil {
					t.Fatal(writeErr)
				}
			}
			cached := func(read func() string, want string) {
				t.Helper()
				// The second read is served from the shared cache.
				for range 2 {
					if got := read(); got != want {
						t.Fatalf("read %q before the write, want %q", got, want)
					}
				}
			}

			// Group counts.
			counts := IssueRecordQuery{Workspace: workspace, Archived: "false", GroupBy: "status", TeamIDs: []string{template.Team.ID}}
			moved := func() string {
				groups, err := reader.QueryIssueGroups(ctx, counts)
				if err != nil {
					t.Fatal(err)
				}
				for _, group := range groups {
					if group.Value == states[1].ID {
						return fmt.Sprint(group.Count)
					}
				}
				return "0"
			}
			cached(moved, "0")
			held(func(ctx context.Context) error {
				_, err := writer.UpdateIssueRecord(ctx, workspace, issue.ID, nil, IssueMutationScope{}, func(_ *domain.Bootstrap, issue *domain.Issue) error {
					issue.State = states[1]
					return nil
				})
				return err
			}, func() {
				if got := moved(); got != "1" {
					t.Fatalf("B counted %s moved issues after the move committed", got)
				}
			})

			// An issue query page.
			page := IssueRecordQuery{Workspace: workspace, Archived: "false", TeamIDs: []string{template.Team.ID}, Limit: 50}
			title := func() string {
				result, err := reader.QueryIssueRecords(ctx, page)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range result.Items {
					if item.ID == issue.ID {
						return item.Title
					}
				}
				return "(missing)"
			}
			cached(title, issue.Title)
			held(func(ctx context.Context) error {
				_, err := writer.UpdateIssueRecord(ctx, workspace, issue.ID, nil, IssueMutationScope{}, func(_ *domain.Bootstrap, issue *domain.Issue) error {
					issue.Title = "renamed after the cache filled"
					return nil
				})
				return err
			}, func() {
				if got := title(); got != "renamed after the cache filled" {
					t.Fatalf("B's page shows title %q after the rename committed", got)
				}
			})

			// The metadata record copy, read through a read snapshot.
			label := data.Labels[0]
			labelName := func() string {
				readCtx, release, err := reader.BeginWorkspaceRead(ctx, workspace)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				for _, item := range readCtx.Value(workspaceReadKey{}).(workspaceRead).metadata.Labels {
					if item.ID == label.ID {
						return item.Name
					}
				}
				return "(missing)"
			}
			cached(labelName, label.Name)
			if ready, _ := reader.hotCache().CacheGet(ctx, reader.metadataReadyKey(workspace)); len(ready) == 0 {
				t.Fatal("the metadata copy was not cached")
			}
			renamed := fmt.Sprintf("label renamed %d", stamp)
			held(func(ctx context.Context) error {
				return writer.MutateWorkspace(ctx, workspace, "label.updated", label.ID, map[string]string{"name": renamed}, func(next *domain.Bootstrap) error {
					for index := range next.Labels {
						if next.Labels[index].ID == label.ID {
							next.Labels[index].Name = renamed
						}
					}
					return nil
				})
			}, func() {
				if got := labelName(); got != renamed {
					t.Fatalf("B's metadata shows label %q after the rename committed", got)
				}
			})
		})
	}
}
