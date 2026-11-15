package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW110Root(comments []domain.Comment, id string) (string, bool) {
	for depth := 0; depth < 16; depth++ {
		index := slices.IndexFunc(comments, func(comment domain.Comment) bool { return comment.ID == id })
		if index < 0 {
			return "", false
		}
		if comments[index].ParentID == nil || *comments[index].ParentID == "" {
			return comments[index].ID, true
		}
		id = *comments[index].ParentID
	}
	return "", false
}

func scaleP21SW110Seed(t *testing.T, f scaleP09Fixture, issue domain.Issue, at time.Time, mode string) {
	t.Helper()
	scaleP21SW63Seed(t, f, issue, at)
	if err := f.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw110_seed", "", nil, func(data *domain.Bootstrap) error {
		first := data.ThreadSubscriptions[0]
		duplicate := first
		duplicate.ID, duplicate.State = issue.ID+"-duplicate", "muted"
		if mode == "duplicate-id" || mode == "duplicate-id-append" {
			duplicate.ID = first.ID
			duplicate.State = first.State
			duplicate.UpdatedAt = first.UpdatedAt.Add(time.Second)
		}
		if mode == "append" || mode == "duplicate-id-append" || mode == "clear-missing" {
			first.UserID, duplicate.UserID = "other-user", "other-user"
		}
		foreign := data.ThreadSubscriptions[1]
		foreign.State = "unchanged-native\x00state"
		last := foreign
		last.ID += "-last"
		data.ThreadSubscriptions = []domain.ThreadSubscription{foreign, first, duplicate, last}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP21SW110ThreadWriterDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	for _, scoped := range []bool{false, true} {
		for _, reload := range []bool{false, true} {
			for _, mode := range []string{"update-first", "append", "noop", "clear", "clear-missing", "duplicate-id", "duplicate-id-append", "cancel-before", "cancel-callback"} {
				t.Run(fmt.Sprintf("scoped=%t/reload=%t/%s", scoped, reload, mode), func(t *testing.T) {
					f, issue := scaleP21IssuePersistenceFixture(t, nil, nil)
					workspace := f.data.Workspace.URLKey
					metadata, _ := f.WorkspaceMetadata(workspace)
					at := time.Date(2026, 11, 15, 2, 3, 4, 567, time.UTC)
					type outcome struct {
						Business                   scaleP21SW63Business
						Installed                  domain.Bootstrap
						Events, Webhooks, Realtime []any
						Saved                      domain.ThreadSubscription
						Calls                      int
						ErrorType, ErrorText       string
					}
					var results []any
					var counts []int64
					for _, bounded := range []bool{false, true} {
						writeFullMetadata(t, f.SQLiteStore, workspace, cloneBootstrap(metadata))
						scaleP21SW110Seed(t, f, issue, at, mode)
						repo := scaleP21IssuePersistenceView(t, f, bounded, reload)
						before := scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)
						prior := scaleP21SW63EventIDs(t, repo, workspace)
						ctx := ContextWithActor(t.Context(), f.data.Viewer)
						if scoped {
							ctx = WithIssueDiscussionMutation(ctx, issue.ID, issue.ID+"-reply")
						}
						var owner *MetadataJSONRead
						if bounded {
							var err error
							owner, err = repo.BeginMetadataJSONRead(ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer owner.Close()
							ctx = owner.Context(ctx)
						}
						ctx, cancel := context.WithCancel(ctx)
						defer cancel()
						if mode == "cancel-before" {
							cancel()
						}
						got := outcome{}
						repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
							got.Webhooks = append(got.Webhooks, []any{event.Type, event.AggregateID, event.Payload, event.PreviousValues, event.Snapshot})
						})
						repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
							got.Realtime = append(got.Realtime, []any{event.Type, event.AggregateID, event.ActorID, event.Payload})
						})
						var captured *domain.Bootstrap
						state := "muted"
						if mode == "noop" {
							state = "subscribed"
						} else if mode == "clear" || mode == "clear-missing" {
							state = ""
						}
						var statements atomic.Int64
						var observing atomic.Bool
						observing.Store(true)
						mutationCtx := WithSQLStatementObserver(ctx, func(string) {
							if observing.Load() {
								statements.Add(1)
							}
						})
						err := repo.MutateWorkspace(mutationCtx, workspace, "comment.thread_subscription", issue.ID, map[string]string{"commentId": issue.ID + "-reply", "state": state}, func(data *domain.Bootstrap) error {
							got.Calls++
							captured = data
							root, ok := scaleP21SW110Root(data.Comments[issue.ID], issue.ID+"-reply")
							if !ok || root != issue.ID+"-root" {
								t.Fatal("thread callback lost its exact ancestor chain")
							}
							if mode == "clear" || mode == "clear-missing" {
								data.ThreadSubscriptions = slices.DeleteFunc(data.ThreadSubscriptions, func(thread domain.ThreadSubscription) bool {
									return thread.UserID == data.Viewer.ID && thread.IssueID == issue.ID && thread.CommentID == root
								})
							} else {
								index := slices.IndexFunc(data.ThreadSubscriptions, func(thread domain.ThreadSubscription) bool {
									return thread.UserID == data.Viewer.ID && thread.IssueID == issue.ID && thread.CommentID == root
								})
								if index >= 0 {
									got.Saved = data.ThreadSubscriptions[index]
									if got.Saved.State == state {
										return ErrNoMutation
									}
									data.ThreadSubscriptions[index].State, data.ThreadSubscriptions[index].UpdatedAt = state, at.Add(time.Hour)
									got.Saved = data.ThreadSubscriptions[index]
								} else {
									got.Saved = domain.ThreadSubscription{ID: issue.ID + "-created-thread", UserID: data.Viewer.ID, IssueID: issue.ID, CommentID: root, State: state, CreatedAt: at.Add(time.Hour), UpdatedAt: at.Add(time.Hour)}
									data.ThreadSubscriptions = append(data.ThreadSubscriptions, got.Saved)
								}
							}
							if mode == "cancel-callback" {
								cancel()
							}
							return nil
						})
						observing.Store(false)
						counts = append(counts, statements.Load())
						repo.SetWebhookSink(nil)
						repo.SetRealtimeSink(nil)
						if err != nil {
							got.ErrorType, got.ErrorText = fmt.Sprintf("%T", err), err.Error()
						}
						failed := mode == "cancel-before" || mode == "cancel-callback"
						if failed && err == nil || !failed && err != nil {
							t.Fatal("thread callback success/error changed", "bounded", bounded, err)
						}
						got.Business = scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)
						got.Installed = scaleP21SW98Installed(t, repo, workspace)
						got.Events = scaleP21SW63NewEvents(t, repo, workspace, prior)
						events := 1
						if failed || mode == "noop" {
							events = 0
							if !reflect.DeepEqual(got.Business, before) {
								t.Fatal("cancelled/no-op thread operation changed business rows")
							}
						}
						if len(got.Events) != events || len(got.Webhooks) != events || len(got.Realtime) != events {
							t.Fatal("thread event/publication count changed", len(got.Events), len(got.Webhooks), len(got.Realtime), events)
						}
						results = append(results, scaleP21SW63JSON(t, got))
						if bounded {
							if captured != nil && (repo.MetadataWorkUsedForTesting() == 0 || reflect.DeepEqual(*captured, domain.Bootstrap{})) {
								t.Fatal("thread callback output escaped its response owner")
							}
							owner.Close()
							owner.Close()
							if captured != nil && !reflect.DeepEqual(*captured, domain.Bootstrap{}) {
								t.Fatal("thread callback output survived owner release")
							}
							if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 {
								t.Fatal("thread writer leaked work or hydrated full metadata")
							}
						}
					}
					if !reflect.DeepEqual(results[0], results[1]) {
						t.Fatal("literal/bounded thread writer differs", scaleP21SW98FirstDiff(results[0], results[1], "thread"))
					}
					t.Logf("scoped=%t reload=%t operation=%s writer_SQL_old=%d writer_SQL_bounded=%d", scoped, reload, mode, counts[0], counts[1])
				})
			}
		}
	}
}

func TestScaleP21SW110ThreadWriterRejectsOtherEdits(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	for _, scoped := range []bool{false, true} {
		for _, mode := range []string{"invalid-state", "identity", "owner", "issue", "comment", "created", "reorder", "append-foreign", "append-invalid"} {
			t.Run(fmt.Sprintf("scoped=%t/%s", scoped, mode), func(t *testing.T) {
				f, issue := scaleP21IssuePersistenceFixture(t, nil, nil)
				at := time.Date(2026, 11, 15, 2, 3, 4, 567, time.UTC)
				scaleP21SW110Seed(t, f, issue, at, "")
				workspace := f.data.Workspace.URLKey
				repo := scaleP21IssuePersistenceView(t, f, true, false)
				ctx := ContextWithActor(t.Context(), f.data.Viewer)
				if scoped {
					ctx = WithIssueDiscussionMutation(ctx, issue.ID, issue.ID+"-reply")
				}
				owner, err := repo.BeginMetadataJSONRead(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close()
				before := scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)
				prior := scaleP21SW63EventIDs(t, repo, workspace)
				err = repo.MutateWorkspace(owner.Context(ctx), workspace, "comment.thread_subscription", issue.ID, nil, func(data *domain.Bootstrap) error {
					index := slices.IndexFunc(data.ThreadSubscriptions, func(thread domain.ThreadSubscription) bool { return thread.IssueID == issue.ID })
					item := &data.ThreadSubscriptions[index]
					switch mode {
					case "invalid-state":
						item.State = "invalid"
					case "identity":
						item.ID += "-changed"
					case "owner":
						item.UserID += "-changed"
					case "issue":
						item.IssueID += "-changed"
					case "comment":
						item.CommentID += "-changed"
					case "created":
						item.CreatedAt = item.CreatedAt.Add(time.Hour)
					case "reorder":
						data.ThreadSubscriptions[index], data.ThreadSubscriptions[index+1] = data.ThreadSubscriptions[index+1], data.ThreadSubscriptions[index]
					case "append-foreign", "append-invalid":
						added := *item
						added.ID += "-new"
						if mode == "append-foreign" {
							added.IssueID += "-foreign"
						} else {
							added.State = "invalid"
						}
						data.ThreadSubscriptions = append(data.ThreadSubscriptions, added)
					}
					return nil
				})
				if !errors.Is(err, ErrBoundedMetadataCoverage) {
					t.Fatal("unsupported thread edit accepted", mode, err)
				}
				if !reflect.DeepEqual(before, scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)) || len(scaleP21SW63NewEvents(t, repo, workspace, prior)) != 0 {
					t.Fatal("unsupported thread edit committed partial SQL/events")
				}
				owner.Close()
				if repo.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("unsupported thread edit leaked work")
				}
			})
		}
	}
}
