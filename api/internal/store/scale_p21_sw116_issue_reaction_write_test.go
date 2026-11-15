package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type scaleP21SW116Capture struct {
	Viewer       domain.User
	Via          *domain.ActorBot
	Role         string
	Saved        domain.Issue
	BeforeLabels []domain.IssueLabel
}

type scaleP21SW116Outcome struct {
	Business                   scaleP21SW63Business
	Metadata                   scaleP21SW76Metadata
	Indexes                    map[string][][]any
	Installed                  domain.Bootstrap
	InstalledVia               *domain.ActorBot
	InstalledImportJobs        []domain.ImportJob
	InstalledMigrationJobs     []domain.MigrationJob
	Events, Webhooks, Realtime []any
	Callback                   []scaleP21SW116Capture
	Updated                    domain.Issue
	Input                      domain.ReactionInput
	ErrorType, ErrorText       string
	Calls                      int
}

func scaleP21SW116Seed(t *testing.T, f scaleP09Fixture, issue domain.Issue, at time.Time, mode string) {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	issue.Labels = []domain.IssueLabel{{ID: "sw116-label", Name: "Reaction count alias", Scope: issue.Team.ID}}
	issue.Reactions = nil
	switch mode {
	case "empty-map":
		issue.Reactions = map[string][]string{}
	case "nil-list":
		issue.Reactions = map[string][]string{"like": nil, "untouched": {}}
	case "empty-list":
		issue.Reactions = map[string][]string{"like": {}, "untouched": nil}
	case "remove-all":
		issue.Reactions = map[string][]string{"like": {f.data.Viewer.ID, f.data.Viewer.ID}, "untouched": {"other"}}
	case "remove-mixed":
		issue.Reactions = map[string][]string{"like": {f.data.Viewer.ID, "other", f.data.Viewer.ID, "other"}, "untouched": nil}
	case "append-duplicates":
		issue.Reactions = map[string][]string{"like": {"other", "other"}, "untouched": {}}
	}
	scaleP21SW63Seed(t, f, issue, at)
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw116_seed", "", nil, func(data *domain.Bootstrap) error {
		if !slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool { return label.ID == "sw116-label" }) {
			data.Labels = append(data.Labels, issue.Labels[0])
		}
		data.Issues = slices.DeleteFunc(data.Issues, func(value domain.Issue) bool { return value.ID == "sw116-sibling" })
		sibling := issue
		sibling.ID, sibling.Identifier, sibling.Number = "sw116-sibling", "P21-116", 116
		sibling.Title, sibling.Labels, sibling.Reactions = "Untouched progress sibling", nil, nil
		if mode == "nested-aggregate" {
			// An earlier issue's nested creator wins the generic aggregate walk.
			// The issue-record topology only loads the target, so it has a
			// different literal entity and is compared independently below.
			sibling.Creator = domain.User{ID: issue.ID, Name: "Nested aggregate before target", Active: true}
		}
		data.Issues = append([]domain.Issue{sibling}, data.Issues...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET collection_order=-116 WHERE workspace_key=? AND id=?`, workspace, "sw116-sibling"); err != nil {
		t.Fatal(err)
	}
	native, found := f.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("reaction fixture metadata missing")
	}
	for index := range native.Labels {
		if native.Labels[index].ID == "sw116-label" {
			native.Labels[index].IssueCount = 99
		}
	}
	for index := range native.Projects {
		if native.Projects[index].ID != "p21-project" {
			continue
		}
		native.Projects[index].IssueCountHistory = nil
		native.Projects[index].ScopeHistory = nil
		native.Projects[index].CompletedScopeHistory = nil
		native.Projects[index].InProgressScopeHistory = nil
		native.Projects[index].ProgressHistory = nil
		if mode == "stale-progress" {
			native.Projects[index].ProgressHistory = []domain.ProjectProgressHistoryPoint{{Date: at, Value: 91, ScopeCount: 91}}
		}
	}
	if mode == "warm-import-inputs" {
		native.ImportJobs = append(native.ImportJobs, domain.ImportJob{
			ID: "sw116-import", UserID: f.data.Viewer.ID, Status: "pending",
			Rows:      []map[string]string{{"title": "Keep uploaded source out of installed metadata"}},
			CreatedAt: at, UpdatedAt: at,
		})
		native.MigrationJobs = append(native.MigrationJobs, domain.MigrationJob{
			ID: "sw116-migration", UserID: f.data.Viewer.ID, Status: "pending",
			Bundle:    json.RawMessage(`{"version":1,"source":"flow","issues":[]}`),
			CreatedAt: at, UpdatedAt: at,
		})
	}
	// Seed stale display counts without running another count-refresh pass.
	// They are a legal installed metadata state and expose shallow aliases.
	writeFullMetadata(t, f.SQLiteStore, workspace, native)
	var occurrences []domain.Issue
	if mode == "native-duplicates" {
		var err error
		occurrences, err = f.readIssueRecords(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		for index := range occurrences {
			if occurrences[index].ID == issue.ID {
				duplicate := occurrences[index]
				duplicate.Labels = slices.Clone(duplicate.Labels)
				occurrences = slices.Insert(occurrences, index+1, duplicate)
				break
			}
		}
	}
	f.mu.Lock()
	installed := f.workspaces[workspace]
	installed.Labels, installed.Projects = native.Labels, native.Projects
	if mode == "warm-import-inputs" {
		installed.ImportJobs, installed.MigrationJobs = native.ImportJobs, native.MigrationJobs
	}
	if mode == "native-duplicates" {
		installed.Issues = occurrences
	}
	f.workspaces[workspace] = installed
	f.mu.Unlock()
}

func TestScaleP21SW116IssueReactionWriterDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	// Disable the test-only P04 double-callback audit without selecting a
	// different dispatcher or replacing either literal writer.
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	if fullMutationsForced.Load() {
		t.Fatal("reaction differential must use both ordinary literal topologies")
	}
	for _, scoped := range []bool{false, true} {
		for _, reload := range []bool{false, true} {
			for _, mode := range []string{
				"nil-map", "empty-map", "nil-list", "empty-list", "remove-all", "remove-mixed",
				"append-duplicates", "emoji-space", "nested-aggregate", "stale-progress",
				"native-duplicates", "warm-import-inputs", "retry", "cancel-before", "cancel-callback", "cancel-persist", "callback-error",
			} {
				// Record-backed reloads intentionally do not retain native
				// duplicate issue occurrences. Prove that warm source separately.
				if reload && mode == "native-duplicates" {
					continue
				}
				t.Run(fmt.Sprintf("scoped=%t/reload=%t/%s", scoped, reload, mode), func(t *testing.T) {
					f, issue := scaleP21IssuePersistenceFixture(t, []domain.Project{{ID: "p21-project", Name: "Reaction progress"}}, nil)
					workspace := f.data.Workspace.URLKey
					metadata, found := f.WorkspaceMetadata(workspace)
					if !found {
						t.Fatal("reaction metadata reset missing")
					}
					at := time.Date(2026, 11, 15, 2, 3, 4, 567, time.UTC)
					if metadata.UserSettings == nil {
						metadata.UserSettings = map[string]domain.UserSettings{}
					}
					if metadata.NotificationPreferences == nil {
						metadata.NotificationPreferences = map[string]domain.NotificationPreferences{}
					}
					for _, user := range metadata.Users {
						if _, found := metadata.UserSettings[user.ID]; !found {
							settings := defaultUserSettings(user.ID)
							settings.UpdatedAt = at
							metadata.UserSettings[user.ID] = settings
						}
						if _, found := metadata.NotificationPreferences[user.ID]; !found {
							preferences := defaultNotificationPreferences(user.ID)
							preferences.UpdatedAt = at
							metadata.NotificationPreferences[user.ID] = preferences
						}
					}
					actor := scaleP21SW69OwnedActor(f.data.Viewer)
					actor.Via = &domain.ActorBot{ID: "sw116-request-app", Name: "Reaction request attribution", Type: domain.ActorBotTypeOAuthApplication}
					var outputs []any
					var statementCounts []int64
					for _, bounded := range []bool{false, true} {
						writeFullMetadata(t, f.SQLiteStore, workspace, cloneBootstrap(metadata))
						scaleP21SW116Seed(t, f, issue, at, mode)
						repo := scaleP21IssuePersistenceView(t, f, bounded, reload)
						if mode == "native-duplicates" {
							f.mu.RLock()
							duplicates := 0
							for _, value := range f.workspaces[workspace].Issues {
								if value.ID == issue.ID && value.Version == issue.Version {
									duplicates++
								}
							}
							f.mu.RUnlock()
							if duplicates != 2 {
								t.Fatal("native duplicate fixture lost an original occurrence", duplicates)
							}
						}
						before := scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)
						beforeMetadata := scaleP21SW76MetadataImage(t, repo.db, workspace)
						beforeIndexes := scaleP21SW98Indexes(t, repo, workspace)
						prior := scaleP21SW63EventIDs(t, repo, workspace)
						ctx := ContextWithActor(t.Context(), actor)
						if scoped {
							ctx = WithIssueRecordMutations(ctx, issue.ID)
						}
						if IsIssueDiscussionMutation(ctx) || UsesIssueRecordMutations(ctx) != scoped {
							t.Fatal("reaction differential changed callback topology")
						}
						var owner *MetadataJSONRead
						outerWork := 0
						if bounded {
							var err error
							owner, err = repo.BeginMetadataJSONRead(ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer owner.Close()
							ctx = owner.Context(ctx)
							outerWork = repo.MetadataWorkUsedForTesting()
						}
						ctx, cancel := context.WithCancel(ctx)
						defer cancel()
						if mode == "cancel-before" {
							cancel()
						}
						got := scaleP21SW116Outcome{}
						var captured []*domain.Bootstrap
						checkOwner := func() {
							t.Helper()
							if bounded && repo.MetadataWorkUsedForTesting() <= outerWork {
								t.Error("reaction publication lost callback output ownership")
							}
						}
						repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
							checkOwner()
							got.Webhooks = append(got.Webhooks, []any{event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues, event.Snapshot})
						})
						repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
							checkOwner()
							got.Realtime = append(got.Realtime, []any{event.Type, event.AggregateID, event.ActorID, event.ClientID, event.Payload})
						})
						input := domain.ReactionInput{Emoji: "like"}
						if mode == "emoji-space" {
							input.Emoji = " like "
						}
						persistCancelled := false
						var statements atomic.Int64
						var observing atomic.Bool
						observing.Store(true)
						mutationCtx := WithSQLStatementObserver(ctx, func(query string) {
							if observing.Load() {
								statements.Add(1)
								if mode == "cancel-persist" && strings.Contains(strings.ToLower(query), "insert into domain_events") {
									persistCancelled = true
									cancel()
								}
							}
						})
						// This is toggleIssueReaction's callback, with only its
						// wall clock fixed so all persisted business fields
						// and the captured shallow result compare exactly.
						err := repo.MutateWorkspace(mutationCtx, workspace, "issue.reaction_toggled", issue.ID, &input, func(data *domain.Bootstrap) error {
							got.Calls++
							captured = append(captured, data)
							index := slices.IndexFunc(data.Issues, func(value domain.Issue) bool { return value.ID == issue.ID })
							if index < 0 {
								t.Fatal("reaction callback lost target issue")
							}
							target := &data.Issues[index]
							if target.Reactions == nil {
								target.Reactions = map[string][]string{}
							}
							users := target.Reactions[input.Emoji]
							added := !slices.Contains(users, data.Viewer.ID)
							input.Added = &added
							if !added {
								users = slices.DeleteFunc(users, func(value string) bool { return value == data.Viewer.ID })
							} else {
								users = append(users, data.Viewer.ID)
							}
							if len(users) == 0 {
								delete(target.Reactions, input.Emoji)
							} else {
								target.Reactions[input.Emoji] = users
							}
							target.Version++
							target.UpdatedAt = at.Add(time.Hour)
							got.Updated = *target
							capture := scaleP21SW116Capture{
								Viewer: data.Viewer, Via: data.Viewer.Via, Role: data.ViewerRole,
								Saved: *target, BeforeLabels: slices.Clone(target.Labels),
							}
							// The real handler selects only the first issueByID
							// match. The bounded callback may prune later native
							// duplicates; the immutable image, count pass, SQL,
							// and installed metadata must still preserve their
							// literal effects, compared outside this callback.
							got.Callback = append(got.Callback, capture)
							if mode == "retry" && got.Calls == 1 {
								return ErrConcurrentWrite
							}
							if mode == "cancel-callback" {
								cancel()
							}
							if mode == "callback-error" {
								return ErrIssueQuery
							}
							return nil
						})
						observing.Store(false)
						statementCounts = append(statementCounts, statements.Load())
						repo.SetWebhookSink(nil)
						repo.SetRealtimeSink(nil)
						if err != nil {
							got.ErrorType, got.ErrorText = fmt.Sprintf("%T", err), err.Error()
						}
						warmConflict := scoped && !reload && mode == "warm-import-inputs"
						failed := mode == "cancel-before" || mode == "cancel-callback" || mode == "cancel-persist" || mode == "callback-error" || warmConflict
						wantCalls, wantEvents := 1, 1
						if mode == "cancel-before" {
							wantCalls = 0
						}
						if mode == "retry" {
							wantCalls = 2
						}
						if warmConflict {
							wantCalls = fullMutationAttempts
						}
						if failed {
							wantEvents = 0
						}
						if failed && err == nil || !failed && err != nil {
							keys, _ := conflictedMetadataKeys(err)
							t.Fatal("reaction success/error changed", bounded, got.Calls, fmt.Sprintf("%T", err), keys, err)
						}
						if mode == "callback-error" && !errors.Is(err, ErrIssueQuery) ||
							(mode == "cancel-callback" || mode == "cancel-persist") && !errors.Is(err, context.Canceled) {
							t.Fatal("reaction callback/cancellation error changed", err)
						}
						if warmConflict {
							keys, ok := conflictedMetadataKeys(err)
							if !ok || !reflect.DeepEqual(keys, []metadataRecordKey{{"importJobs", "sw116-import"}}) {
								t.Fatal("scoped warm import literal conflict changed", bounded, keys, err)
							}
						}
						if mode == "cancel-persist" && !persistCancelled {
							t.Fatal("reaction cancellation never reached event persistence")
						}
						got.Business = scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)
						got.Metadata = scaleP21SW76MetadataImage(t, repo.db, workspace)
						got.Indexes = scaleP21SW98Indexes(t, repo, workspace)
						got.Installed = scaleP21SW98Installed(t, repo, workspace)
						got.InstalledVia = got.Installed.Viewer.Via
						if mode == "warm-import-inputs" {
							var imports domain.Bootstrap
							if bounded {
								source, err := repo.retainMetadataLogicalSource(t.Context(), workspace)
								if err != nil {
									t.Fatal(err)
								}
								imports, err = source.Select(t.Context(), MetadataRecordScope{Fields: []string{"importJobs", "migrationJobs"}}, 64<<20)
								source.Release()
								if err != nil {
									t.Fatal(err)
								}
							} else {
								var found bool
								imports, found = repo.storedWorkspace(workspace)
								if !found {
									t.Fatal("reaction import compaction workspace missing")
								}
							}
							got.InstalledImportJobs, got.InstalledMigrationJobs = imports.ImportJobs, imports.MigrationJobs
							if len(imports.ImportJobs) == 0 || len(imports.MigrationJobs) == 0 {
								t.Fatal("reaction discarded import or migration job metadata")
							}
						}
						got.Events = scaleP21SW63NewEvents(t, repo, workspace, prior)
						if got.Calls != wantCalls || len(got.Events) != wantEvents || len(got.Webhooks) != wantEvents || len(got.Realtime) != wantEvents {
							t.Fatal("reaction callback/publication count changed", bounded, got.Calls, len(got.Events), len(got.Webhooks), len(got.Realtime))
						}
						if failed {
							if !reflect.DeepEqual(got.Business, before) || !reflect.DeepEqual(got.Metadata, beforeMetadata) || !reflect.DeepEqual(got.Indexes, beforeIndexes) {
								t.Fatal("failed reaction changed business, metadata, or index rows")
							}
							// The route discards its result on error. Observe
							// failed callback labels at their real last use,
							// inside the callback, before the count pass.
							got.Updated = domain.Issue{}
							for index := range got.Callback {
								got.Callback[index].Saved.Labels = slices.Clone(got.Callback[index].BeforeLabels)
							}
						} else {
							count := 1
							if scoped {
								last := got.Callback[len(got.Callback)-1]
								if len(last.BeforeLabels) != 1 {
									t.Fatal("scoped reaction fixture lost its label")
								}
								count = last.BeforeLabels[0].IssueCount
							} else if mode == "native-duplicates" {
								count = 2
							}
							if got.Updated.Version != issue.Version+1 || len(got.Updated.Labels) != 1 || got.Updated.Labels[0].IssueCount != count {
								t.Fatal("reaction shallow result lost literal version/count alias", bounded, got.Updated.Version, got.Updated.Labels, count)
							}
							var persisted domain.Issue
							if err := json.Unmarshal(got.Business.Issue, &persisted); err != nil {
								t.Fatal(err)
							}
							if persisted.Version != issue.Version+1 || !persisted.UpdatedAt.Equal(at.Add(time.Hour)) || !reflect.DeepEqual(persisted.Reactions, got.Updated.Reactions) {
								t.Fatal("reaction row lost exact version/clock/reactions")
							}
							wantAdded := mode != "remove-all" && mode != "remove-mixed"
							if input.Added == nil || *input.Added != wantAdded || input.CommentID != "" {
								t.Fatal("reaction input lost add/remove decision")
							}
							if mode == "nested-aggregate" {
								realtime := got.Realtime[0].([]any)
								var envelope struct {
									Entity map[string]any `json:"entity"`
								}
								if err := json.Unmarshal(realtime[4].(json.RawMessage), &envelope); err != nil {
									t.Fatal(err)
								}
								_, hasVersion := envelope.Entity["version"]
								if hasVersion != scoped || !scoped && envelope.Entity["name"] != "Nested aggregate before target" {
									t.Fatal("reaction nested aggregate precedence changed", scoped, envelope.Entity)
								}
							}
							if scoped && len(got.Webhooks[0].([]any)[4].(json.RawMessage)) != 0 {
								t.Fatal("issue-record reaction added ordinary PreviousValues")
							}
							project := slices.IndexFunc(got.Installed.Projects, func(value domain.Project) bool { return value.ID == "p21-project" })
							if project < 0 || len(got.Installed.Projects[project].ProgressHistory) == 0 {
								t.Fatal("reaction lost its project progress side effect")
							}
							if mode == "stale-progress" {
								stale := got.Installed.Projects[project].ProgressHistory[0].Value == 91
								if stale != scoped {
									t.Fatal("reaction changed full/scoped progress rebuild topology")
								}
							}
						}
						got.Input = input
						if bounded && got.Calls > 0 {
							if repo.MetadataWorkUsedForTesting() <= outerWork {
								t.Fatal("reaction callback output lost owner before caller last use")
							}
							for _, data := range captured {
								if reflect.DeepEqual(*data, domain.Bootstrap{}) {
									t.Fatal("reaction callback cleared before response encoding")
								}
							}
							if err := owner.ReserveEncoding(t.Context(), []any{got.Updated, got.Callback}); err != nil {
								t.Fatal(err)
							}
						}
						outputs = append(outputs, scaleP21SW63JSON(t, got))
						if bounded {
							owner.Close()
							owner.Close()
							for _, data := range captured {
								if !reflect.DeepEqual(*data, domain.Bootstrap{}) {
									t.Fatal("reaction callback survived owner release")
								}
							}
							if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 {
								t.Fatal("reaction writer leaked work or hydrated whole metadata")
							}
						}
					}
					if !reflect.DeepEqual(outputs[0], outputs[1]) {
						t.Fatal("literal/bounded issue reaction differs", scaleP21SW98FirstDiff(outputs[0], outputs[1], "reaction"))
					}
					t.Logf("scoped=%t reload=%t operation=%s writer_SQL_old=%d writer_SQL_bounded=%d", scoped, reload, mode, statementCounts[0], statementCounts[1])
				})
			}
		}
	}
}
