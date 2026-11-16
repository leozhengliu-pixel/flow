package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW196DiscussionOrdinalScopeDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	for _, reload := range []bool{false, true} {
		for _, shape := range []string{"empty-id", "overlong-id", "many-keys"} {
			for _, mode := range []string{"changed", "rollback", "canceled"} {
				t.Run(fmt.Sprintf("reload=%t/%s/%s", reload, shape, mode), func(t *testing.T) {
					f, issue := scaleP21IssuePersistenceFixture(t, nil, nil)
					workspace := f.data.Workspace.URLKey
					at := time.Date(2026, 11, 16, 4, 5, 6, 7, time.UTC)
					type outcome struct {
						Business                   scaleP21SW63Business
						Installed                  []domain.ThreadSubscription
						Events, Webhooks, Realtime []any
						Calls                      int
						Error                      string
					}
					var results []any
					for _, bounded := range []bool{false, true} {
						scaleP21SW63Seed(t, f, issue, at)
						if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw196_seed", "", nil, func(data *domain.Bootstrap) error {
							selected, foreign := data.ThreadSubscriptions[0], data.ThreadSubscriptions[1]
							count := 1
							if shape == "many-keys" {
								count = MetadataRecordBatchMaxKeys + 1
							}
							data.ThreadSubscriptions = []domain.ThreadSubscription{foreign}
							for index := range count {
								item := selected
								item.ID = fmt.Sprintf("%s-selected-%d", issue.ID, index)
								if shape == "empty-id" {
									item.ID = ""
								} else if shape == "overlong-id" {
									item.ID = strings.Repeat("long-subscription-id", 32)
								}
								data.ThreadSubscriptions = append(data.ThreadSubscriptions, item)
							}
							foreign.ID += "-last"
							data.ThreadSubscriptions = append(data.ThreadSubscriptions, foreign)
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						repo := scaleP21IssuePersistenceView(t, f, bounded, reload)
						before := scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)
						prior := scaleP21SW63EventIDs(t, repo, workspace)
						var closes []func()
						closeOutput := func() {
							for _, close := range closes {
								close()
								close()
							}
						}
						t.Cleanup(closeOutput)
						ctx := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
							closes = append(closes, close)
							return true
						})
						ctx = ContextWithActor(WithIssueDiscussionMutation(ctx, issue.ID, issue.ID+"-root"), f.data.Viewer)
						ctx, cancel := context.WithCancel(ctx)
						defer cancel()
						got := outcome{}
						repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
							got.Webhooks = append(got.Webhooks, []any{event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues, event.Snapshot})
						})
						repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
							got.Realtime = append(got.Realtime, []any{event.Type, event.AggregateID, event.ActorID, event.Payload})
						})
						var captured *domain.Bootstrap
						callback := func(data *domain.Bootstrap) (string, error) {
							got.Calls++
							captured = data
							metadataBefore := domain.Bootstrap{ThreadSubscriptions: append([]domain.ThreadSubscription(nil), data.ThreadSubscriptions...)}
							changed := 0
							for index := range data.ThreadSubscriptions {
								item := &data.ThreadSubscriptions[index]
								if item.IssueID == issue.ID {
									item.State, item.UpdatedAt = "muted", at.Add(time.Hour)
									changed++
								}
							}
							want := 1
							if shape == "many-keys" {
								want = MetadataRecordBatchMaxKeys + 1
							}
							if changed != want {
								t.Fatalf("selected subscription count=%d want=%d", changed, want)
							}
							metadataNext := domain.Bootstrap{ThreadSubscriptions: data.ThreadSubscriptions}
							scope, scopeErr := boundedDiscussionMetadataScope(metadataBefore, metadataNext)
							if shape != "empty-id" && (scopeErr == nil || !discussionNeedsOrdinalScope(metadataBefore, metadataNext, issue.ID, scope, scopeErr)) {
								t.Fatal("fixture did not require the ordinal fallback", scopeErr)
							}
							comment := &data.Comments[issue.ID][0]
							comment.Body, comment.Version = "After ordinal scope", comment.Version+1
							comment.EditedAt = new(at.Add(time.Hour))
							if mode == "rollback" {
								return issue.ID, ErrIssueQuery
							}
							if mode == "canceled" {
								cancel()
							}
							return issue.ID, nil
						}
						payload := map[string]string{"commentId": issue.ID + "-root", "body": "After ordinal scope"}
						var err error
						if bounded {
							err = repo.mutateBoundedIssueDiscussion(ctx, workspace, "comment.updated", payload, callback)
						} else {
							restore := ForceFullMutationsForTesting(true)
							err = repo.mutateIssueScope(ctx, workspace, "comment.updated", payload, callback)
							restore()
						}
						repo.SetWebhookSink(nil)
						repo.SetRealtimeSink(nil)
						if err != nil {
							got.Error = err.Error()
						}
						if mode == "changed" && err != nil ||
							mode == "rollback" && !errors.Is(err, ErrIssueQuery) ||
							mode == "canceled" && !errors.Is(err, context.Canceled) {
							t.Fatal("discussion result", bounded, mode, err)
						}
						if got.Calls != 1 {
							t.Fatal("callback count", got.Calls)
						}
						got.Business = scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)
						got.Installed = scaleP21SW98Installed(t, repo, workspace).ThreadSubscriptions
						got.Events = scaleP21SW63NewEvents(t, repo, workspace, prior)
						wantEvents := 1
						if mode != "changed" {
							wantEvents = 0
							if !reflect.DeepEqual(before, got.Business) {
								t.Fatal("failed discussion changed durable rows")
							}
						}
						if len(got.Events) != wantEvents || len(got.Webhooks) != wantEvents || len(got.Realtime) != wantEvents {
							t.Fatal("discussion publication counts", len(got.Events), len(got.Webhooks), len(got.Realtime))
						}
						results = append(results, scaleP21SW63JSON(t, got))
						if bounded && (len(closes) != 1 || repo.MetadataWorkUsedForTesting() == 0 || captured == nil) {
							t.Fatal("discussion output lost response ownership")
						}
						closeOutput()
						if bounded && (!reflect.DeepEqual(*captured, domain.Bootstrap{}) ||
							repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0) {
							t.Fatal("discussion output leaked work or fully hydrated metadata")
						}
					}
					if !reflect.DeepEqual(results[0], results[1]) {
						t.Fatal("literal/bounded discussion differs", scaleP21SW98FirstDiff(results[0], results[1], "discussion"))
					}
				})
			}
		}
	}
}
