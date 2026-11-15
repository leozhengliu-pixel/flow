package store

import (
	"context"
	"database/sql"
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

type scaleP21SW98Capture struct {
	Viewer            domain.User
	Via               *domain.ActorBot
	Role              string
	TeamIDs           []string
	Issue             domain.Issue
	BeforeCountLabels []domain.IssueLabel
	Comments          []domain.Comment
	CommentNumbers    []string
}

type scaleP21SW98Outcome struct {
	Business                   scaleP21SW63Business
	Metadata                   scaleP21SW76Metadata
	Indexes                    map[string][][]any
	Events, Webhooks, Realtime []any
	Callback                   []scaleP21SW98Capture
	Installed                  domain.Bootstrap
	InstalledVia               *domain.ActorBot
	ErrorType, ErrorText       string
	IgnoredLookupType          string
	IgnoredLookupText          string
	Calls                      int
}

func scaleP21SW98FirstDiff(want, got any, path string) string {
	if reflect.DeepEqual(want, got) {
		return ""
	}
	switch old := want.(type) {
	case map[string]any:
		if next, ok := got.(map[string]any); ok {
			keys := []string{}
			for key := range old {
				keys = append(keys, key)
			}
			for key := range next {
				if _, exists := old[key]; !exists {
					keys = append(keys, key)
				}
			}
			slices.Sort(keys)
			for _, key := range keys {
				a, foundA := old[key]
				b, foundB := next[key]
				child := path + "." + key
				if foundA != foundB {
					return fmt.Sprintf("%s presence old=%t bounded=%t", child, foundA, foundB)
				}
				if diff := scaleP21SW98FirstDiff(a, b, child); diff != "" {
					return diff
				}
			}
		}
	case []any:
		if next, ok := got.([]any); ok {
			if len(old) != len(next) {
				return fmt.Sprintf("%s length old=%d bounded=%d", path, len(old), len(next))
			}
			for index := range old {
				if diff := scaleP21SW98FirstDiff(old[index], next[index], fmt.Sprintf("%s[%d]", path, index)); diff != "" {
					return diff
				}
			}
		}
	}
	preview := func(value any) string {
		text := fmt.Sprintf("%T %v", value, value)
		if len(text) > 120 {
			text = text[:120] + "..."
		}
		return text
	}
	return fmt.Sprintf("%s old=%s bounded=%s", path, preview(want), preview(got))
}

func scaleP21SW98Indexes(t *testing.T, repo *SQLiteStore, workspace string) map[string][][]any {
	t.Helper()
	result := map[string][][]any{}
	for name, query := range map[string]string{
		"issue-physical":   `SELECT id,identifier,team_id,state_id,state_type,project_id,assignee_id,creator_id,cycle_id,parent_id,priority,sort_order,title,created_at,updated_at,archived,version,collection_order,data FROM issue_records WHERE workspace_key=? ORDER BY collection_order,id`,
		"content-physical": `SELECT kind,resource_id,id,created_at,data FROM workspace_content_records WHERE workspace_key=? ORDER BY kind,resource_id,id`,
		"labels":           `SELECT issue_id,label_id FROM issue_label_records WHERE workspace_key=? ORDER BY issue_id,label_id`,
		"subscribers":      `SELECT issue_id,user_id FROM issue_subscriber_records WHERE workspace_key=? ORDER BY issue_id,user_id`,
		"actors":           `SELECT issue_id,user_id,last_at FROM issue_actor_records WHERE workspace_key=? ORDER BY issue_id,user_id`,
		"attributes":       `SELECT issue_id,field,value FROM issue_attribute_records WHERE workspace_key=? ORDER BY issue_id,field,value`,
		"search":           `SELECT issue_id,content FROM issue_search_documents WHERE workspace_key=? ORDER BY issue_id`,
		"catalog":          `SELECT field,record_key,collection_order,body_bytes,summary,omitted FROM metadata_catalog WHERE workspace_key=? ORDER BY field,record_key`,
		"catalog-refs":     `SELECT field,record_key,relation,value_hash,value FROM metadata_catalog_relations WHERE workspace_key=? ORDER BY field,record_key,relation,value_hash`,
	} {
		rows, err := repo.db.QueryContext(t.Context(), query, workspace)
		if err != nil {
			t.Fatal(name, err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		result[name] = [][]any{}
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for index := range values {
				targets[index] = &values[index]
			}
			if err := rows.Scan(targets...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			for index, value := range values {
				if raw, ok := value.([]byte); ok {
					values[index] = slices.Clone(raw)
				}
			}
			result[name] = append(result[name], values)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func scaleP21SW98Installed(t *testing.T, repo *SQLiteStore, workspace string) domain.Bootstrap {
	t.Helper()
	scope := MetadataRecordScope{Fields: []string{
		"users", "members", "teams", "teamSettings", "workspaceSettings", "labels", "projects",
		"initiatives", "projectUpdates", "initiativeUpdates", "threadSubscriptions", "settings",
	}}
	if !repo.BoundedMetadataEnabled() {
		data, found := repo.storedWorkspace(workspace)
		if !found {
			t.Fatal("literal installed metadata missing")
		}
		result := domain.Bootstrap{Workspace: data.Workspace, Viewer: data.Viewer, ViewerRole: data.ViewerRole}
		source, target := reflect.ValueOf(data), reflect.ValueOf(&result).Elem()
		for _, field := range persistedMetadataFields() {
			if slices.Contains(scope.Fields, field.name) {
				target.Field(field.index).Set(source.Field(field.index))
			}
		}
		return result
	}
	source, err := repo.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	data, err := source.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	policy, found := repo.storedWorkspace(workspace)
	if !found || !reflect.DeepEqual(policy.Viewer, data.Viewer) || policy.ViewerRole != data.ViewerRole {
		t.Fatal("ordinary writer installed a mismatched native policy/source pair")
	}
	return data
}

func TestScaleP21SW98OrdinaryLegacyDiscussionDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	if fullMutationsForced.Load() {
		t.Fatal("ordinary full-writer oracle must not be forced")
	}
	for _, reload := range []bool{false, true} {
		for _, test := range []struct {
			name, operation, role, status, peer        string
			unbound, inactive, ignoredLookup, snapshot bool
			failure                                    string
		}{
			{name: "create-member", operation: "create", role: "member", status: "active"},
			{name: "update-guest", operation: "update", role: "guest", status: "active"},
			{name: "delete-thread", operation: "delete", role: "member", status: "active"},
			{name: "reaction-add", operation: "reaction-add", role: "member", status: "active"},
			{name: "reaction-remove", operation: "reaction-remove", role: "guest", status: "active"},
			{name: "inactive-member-no-refusal", operation: "update", role: "guest", status: "suspended", inactive: true},
			{name: "unbound-actor-no-refusal", operation: "update", unbound: true},
			{name: "canonical-actor-merge", operation: "update", role: "member", status: "active", peer: "actor-merged"},
			{name: "canonical-actor-drop", operation: "update", role: "member", status: "active", peer: "actor-deleted-write-directory"},
			{name: "ignored-ordinary-lookup-error", operation: "update", role: "member", status: "active", ignoredLookup: true},
			{name: "no-mutation", operation: "update", role: "member", status: "active", failure: "noop"},
			{name: "cancel-before", operation: "update", role: "member", status: "active", failure: "cancel-before"},
			{name: "cancel-callback", operation: "update", role: "member", status: "active", failure: "cancel-callback"},
			{name: "cancel-persist-rolls-back", operation: "update", role: "member", status: "active", failure: "cancel-persist"},
			{name: "callback-error", operation: "update", role: "member", status: "active", failure: "callback-error"},
			{name: "snapshot-noop-precedence", operation: "update", role: "member", status: "active", failure: "noop", snapshot: true},
			{name: "snapshot-callback-error-precedence", operation: "update", role: "member", status: "active", failure: "callback-error", snapshot: true},
			{name: "snapshot-success-late-write-refusal", operation: "update", role: "member", status: "active", failure: "snapshot-write", snapshot: true},
		} {
			t.Run(fmt.Sprintf("reload=%t/%s", reload, test.name), func(t *testing.T) {
				f, issue := scaleP21IssuePersistenceFixture(t, []domain.Project{{ID: "p21-project", Name: "Selected project"}}, nil)
				if test.ignoredLookup && f.db.journalStop != nil {
					f.db.journalStop()
					f.db.journalWorkers.Wait()
				}
				issue.Labels = []domain.IssueLabel{{ID: "sw98-count-label", Name: "SW98 count label", Scope: issue.Team.ID}}
				workspace := f.data.Workspace.URLKey
				at := time.Date(2026, 11, 1, 12, 0, 0, 123, time.UTC)
				actorBase := scaleP21SW69OwnedActor(f.data.Viewer)
				actor := scaleP21SW69OwnedActor(actorBase)
				actor.Name, actor.Active = "SW98 live actor", !test.inactive
				actor.Via = &domain.ActorBot{ID: "sw98-request-app", Name: "Ordinary request attribution", Type: domain.ActorBotTypeOAuthApplication}
				if test.unbound {
					actor.ID = "sw98-unbound-actor"
				}
				metadataSeed, found := f.WorkspaceMetadata(workspace)
				if !found {
					t.Fatal("ordinary metadata reset fixture missing")
				}
				var outputs []any
				var installedOutputs []domain.Bootstrap
				var installedVia []*domain.ActorBot
				var statementCounts []int64
				for _, bounded := range []bool{false, true} {
					writeFullMetadata(t, f.SQLiteStore, workspace, cloneBootstrap(metadataSeed))
					scaleP21SW63Seed(t, f, issue, at)
					if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw98_seed", "", nil, func(data *domain.Bootstrap) error {
						data.Viewer = actorBase
						if index := slices.IndexFunc(data.Users, func(user domain.User) bool { return user.ID == actorBase.ID }); index >= 0 {
							data.Users[index] = actorBase
						} else {
							data.Users = append(data.Users, actorBase)
						}
						data.Users = slices.DeleteFunc(data.Users, func(user domain.User) bool { return user.ID == "sw98-unbound-actor" })
						for index := range data.Comments[issue.ID] {
							data.Comments[issue.ID][index].User = actorBase
						}
						data.Comments[issue.ID][0].Reactions = map[string][]string{}
						if test.operation == "reaction-remove" {
							data.Comments[issue.ID][0].Reactions["like"] = []string{actor.ID}
						}
						data.Notifications[0].Actor = actorBase
						data.Notifications[0].GroupKey = actorBase.ID + ":" + issue.ID + ":comments"
						if !slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool { return label.ID == "sw98-count-label" }) {
							data.Labels = append(data.Labels, issue.Labels[0])
						}
						for index := range data.Projects {
							if data.Projects[index].ID == "p21-project" {
								stale := scaleP21SW69OwnedActor(actorBase)
								stale.Name = "SW98 stale embedded lead"
								data.Projects[index].Lead = &stale
							}
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if !test.unbound {
						if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET role=?,status=? WHERE workspace_id=? AND user_id=?`, test.role, test.status, f.data.Workspace.ID, actor.ID); err != nil {
							t.Fatal(err)
						}
					}
					repo := scaleP21IssuePersistenceView(t, f, bounded, reload)
					nativeBefore, found := f.WorkspaceMetadata(workspace)
					if !found {
						t.Fatal("ordinary native preimage missing")
					}
					peerBefore := cloneBootstrap(nativeBefore)
					if index := slices.IndexFunc(peerBefore.Users, func(user domain.User) bool { return user.ID == actorBase.ID }); index >= 0 {
						peerBefore.Users[0], peerBefore.Users[index] = peerBefore.Users[index], peerBefore.Users[0]
					} else {
						t.Fatal("canonical actor peer fixture missing")
					}
					before := scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)
					beforeMetadata := scaleP21SW76MetadataImage(t, repo.db, workspace)
					beforeIndexes := scaleP21SW98Indexes(t, repo, workspace)
					prior := scaleP21SW63EventIDs(t, repo, workspace)
					ctx := ContextWithActor(t.Context(), actor)
					var snapshot *sqlTx
					if test.snapshot {
						var err error
						snapshot, err = repo.db.BeginTx(ctx, metadataReadOptions(repo.dialect))
						if err != nil {
							t.Fatal(err)
						}
						defer snapshot.Rollback()
						ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{
							database: repo.db, workspace: workspace, reader: snapshot, metadata: nativeBefore,
						})
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
					if test.failure == "cancel-before" {
						cancel()
					}
					persistCancelled := false
					if test.failure == "cancel-persist" {
						ctx = WithSQLStatementObserver(ctx, func(query string) {
							if strings.Contains(strings.ToLower(query), "insert into domain_events") {
								persistCancelled = true
								cancel()
							}
						})
					}
					if UsesIssueRecordMutations(ctx) || IsIssueDiscussionMutation(ctx) {
						t.Fatal("ordinary differential accidentally selected a record/discussion callback context")
					}
					got := scaleP21SW98Outcome{}
					var captured []*domain.Bootstrap
					repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						if bounded && repo.MetadataWorkUsedForTesting() <= outerWork {
							t.Error("ordinary webhook lost its owned native result")
						}
						got.Webhooks = append(got.Webhooks, []any{event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues, event.Snapshot})
					})
					repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
						if bounded && repo.MetadataWorkUsedForTesting() <= outerWork {
							t.Error("ordinary realtime lost its owned native result")
						}
						got.Realtime = append(got.Realtime, []any{event.Type, event.AggregateID, event.ActorID, event.ClientID, event.Payload})
					})
					event := "comment.updated"
					switch test.operation {
					case "create":
						event = "comment.created"
					case "delete":
						event = "comment.deleted"
					case "reaction-add", "reaction-remove":
						event = "comment.reaction_toggled"
					}
					payload := map[string]any{"commentId": issue.ID + "-root", "body": "SW98 changed body"}
					var statements atomic.Int64
					var observing atomic.Bool
					observing.Store(true)
					priorObserver, _ := ctx.Value(sqlStatementObserverKey{}).(func(string))
					mutationCtx := WithSQLStatementObserver(ctx, func(query string) {
						if observing.Load() {
							statements.Add(1)
						}
						if priorObserver != nil {
							priorObserver(query)
						}
					})
					err := repo.MutateWorkspace(mutationCtx, workspace, event, issue.ID, payload, func(data *domain.Bootstrap) error {
						got.Calls++
						captured = append(captured, data)
						index := slices.IndexFunc(data.Issues, func(value domain.Issue) bool { return value.ID == issue.ID })
						if index < 0 || data.Viewer.ID != actor.ID || !reflect.DeepEqual(data.Viewer.Via, actor.Via) {
							t.Fatal("ordinary callback lost its issue or live actor")
						}
						role := nativeBefore.ViewerRole
						if !test.unbound && test.status == "active" {
							role = test.role
						}
						if data.ViewerRole != role {
							t.Fatal("ordinary callback changed active/inactive role semantics", data.ViewerRole, role)
						}
						if test.ignoredLookup {
							closedPool, err := sql.Open("sqlite", ":memory:")
							if err != nil {
								t.Fatal(err)
							}
							if err := closedPool.Close(); err != nil {
								t.Fatal(err)
							}
							lookupErr := func() error {
								database, livePool := repo.db, repo.db.DB
								database.DB = closedPool
								defer func() { database.DB = livePool }()
								_, err := data.CollaborationUpdates("document_content_" + issue.ID)
								return err
							}()
							if lookupErr == nil {
								t.Fatal("ordinary lookup fault injection did not fail")
							}
							got.IgnoredLookupType = fmt.Sprintf("%T", lookupErr)
							got.IgnoredLookupText = lookupErr.Error()
						}
						if test.failure != "noop" {
							switch test.operation {
							case "create":
								data.Comments[issue.ID] = append(data.Comments[issue.ID], domain.Comment{ID: issue.ID + "-created", Version: 1, Body: "SW98 created body", BodyData: map[string]any{"native": int64(9)}, User: data.Viewer, CreatedAt: at.Add(3 * time.Second), Reactions: map[string][]string{}})
							case "delete":
								data.Comments[issue.ID] = slices.DeleteFunc(data.Comments[issue.ID], func(comment domain.Comment) bool {
									return comment.ID == issue.ID+"-root" || comment.ParentID != nil && *comment.ParentID == issue.ID+"-root"
								})
								data.ThreadSubscriptions = slices.DeleteFunc(data.ThreadSubscriptions, func(thread domain.ThreadSubscription) bool {
									return thread.IssueID == issue.ID && thread.CommentID == issue.ID+"-root"
								})
							case "reaction-add":
								data.Comments[issue.ID][0].Reactions["like"] = []string{data.Viewer.ID}
							case "reaction-remove":
								delete(data.Comments[issue.ID][0].Reactions, "like")
							default:
								data.Comments[issue.ID][0].Body = "SW98 changed body"
								data.Comments[issue.ID][0].Version++
								data.Comments[issue.ID][0].EditedAt = new(at.Add(3 * time.Second))
							}
							data.Activities[issue.ID] = append(data.Activities[issue.ID], domain.ActivityEvent{ID: issue.ID + "-activity", Type: event, Actor: data.Viewer, CreatedAt: at.Add(3 * time.Second), Metadata: domain.ActivityMetadata{"commentId": issue.ID + "-root"}})
						}
						result := scaleP21SW98Capture{Viewer: data.Viewer, Via: data.Viewer.Via, Role: data.ViewerRole, TeamIDs: slices.Clone(data.ViewerTeamIDs), Issue: data.Issues[index], Comments: data.Comments[issue.ID]}
						result.BeforeCountLabels = slices.Clone(result.Issue.Labels)
						for _, comment := range result.Comments {
							result.CommentNumbers = append(result.CommentNumbers, fmt.Sprintf("%T", comment.BodyData["native"]))
						}
						got.Callback = append(got.Callback, result)
						if test.peer != "" {
							tx := scaleP21SW76Begin(t, f)
							scaleP21SW83Peer(t, tx, workspace, test.peer, peerBefore)
							if err := tx.Commit(); err != nil {
								t.Fatal(err)
							}
						}
						switch test.failure {
						case "noop":
							return ErrNoMutation
						case "cancel-callback":
							cancel()
						case "callback-error":
							return ErrIssueQuery
						}
						return nil
					})
					observing.Store(false)
					statementCounts = append(statementCounts, statements.Load())
					repo.SetWebhookSink(nil)
					repo.SetRealtimeSink(nil)
					if snapshot != nil {
						scaleP21SW70Usable(t, snapshot)
						if err := snapshot.Rollback(); err != nil {
							t.Fatal("ordinary callback consumed the caller's read snapshot", err)
						}
					}
					if err != nil {
						got.ErrorType, got.ErrorText = fmt.Sprintf("%T", err), err.Error()
					}
					failed := test.failure == "cancel-before" || test.failure == "cancel-callback" || test.failure == "cancel-persist" || test.failure == "callback-error" || test.failure == "snapshot-write"
					wantCalls, wantEvents := 1, 1
					if test.failure == "cancel-before" {
						wantCalls = 0
					}
					if failed || test.failure == "noop" {
						wantEvents = 0
					}
					if got.Calls != wantCalls || len(got.Webhooks) != wantEvents || len(got.Realtime) != wantEvents {
						t.Fatal("ordinary callback/event count changed", got.Calls, len(got.Webhooks), len(got.Realtime))
					}
					if failed && err == nil || !failed && err != nil {
						t.Fatal("ordinary callback changed success/failure behavior", err)
					}
					if test.failure == "callback-error" && !errors.Is(err, ErrIssueQuery) ||
						(test.failure == "cancel-before" || test.failure == "cancel-callback" || test.failure == "cancel-persist") && !errors.Is(err, context.Canceled) {
						t.Fatal("ordinary mutation error changed", err)
					}
					if test.failure == "cancel-persist" && !persistCancelled {
						t.Fatal("ordinary rollback injection did not reach the event write after business SQL")
					}
					if test.failure == "snapshot-write" && (err == nil || err.Error() != "cannot start a transaction inside a workspace read snapshot") {
						t.Fatal("ordinary snapshot rejected the successful callback before its eventual write", err)
					}
					got.Business = scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)
					got.Metadata = scaleP21SW76MetadataImage(t, repo.db, workspace)
					got.Indexes = scaleP21SW98Indexes(t, repo, workspace)
					got.Events = scaleP21SW63NewEvents(t, repo, workspace, prior)
					got.Installed = scaleP21SW98Installed(t, repo, workspace)
					got.InstalledVia = got.Installed.Viewer.Via
					if got.Installed.ViewerRole != nativeBefore.ViewerRole {
						t.Fatal("ordinary writer persisted callback-only actor role")
					}
					if test.peer != "" {
						actorIndex := slices.IndexFunc(got.Installed.Users, func(user domain.User) bool { return user.ID == actorBase.ID })
						if test.peer == "actor-deleted-write-directory" && actorIndex >= 0 ||
							test.peer == "actor-merged" && (actorIndex < 0 || got.Installed.Users[actorIndex].JobTitle != "Canonical actor job title") {
							t.Fatal("ordinary metadata union did not adopt the canonical actor merge/drop")
						}
					}
					if test.operation == "create" && !failed {
						var persisted, preimage domain.Issue
						if err := json.Unmarshal(got.Business.Issue, &persisted); err != nil {
							t.Fatal(err)
						}
						if err := json.Unmarshal(before.Issue, &preimage); err != nil {
							t.Fatal(err)
						}
						if len(got.Callback) != 1 || !reflect.DeepEqual(persisted.Labels, preimage.Labels) {
							t.Fatal("ordinary discussion changed the display-only issue row label state")
						}
						beforeCounts := got.Callback[0].Issue
						beforeCounts.Labels = got.Callback[0].BeforeCountLabels
						expectedPrevious := aggregatePreviousValues(domain.Bootstrap{Issues: []domain.Issue{preimage}},
							domain.Bootstrap{Issues: []domain.Issue{beforeCounts}}, issue.ID)
						webhook := got.Webhooks[0].([]any)
						if !reflect.DeepEqual(scaleP21SW63JSON(t, webhook[4]), scaleP21SW63JSON(t, expectedPrevious)) {
							t.Fatal("ordinary PreviousValues was calculated after refreshing resource counts")
						}
						realtime := got.Realtime[0].([]any)
						var envelope struct {
							Entity domain.Issue `json:"entity"`
						}
						if err := json.Unmarshal(realtime[4].(json.RawMessage), &envelope); err != nil ||
							len(envelope.Entity.Labels) != 1 || envelope.Entity.Labels[0].IssueCount != 1 {
							t.Fatal("ordinary realtime entity lost post-count state", err)
						}
						labelIndex := slices.IndexFunc(got.Installed.Labels, func(label domain.IssueLabel) bool { return label.ID == "sw98-count-label" })
						if labelIndex < 0 || got.Installed.Labels[labelIndex].IssueCount != 1 {
							t.Fatal("ordinary installed metadata lost post-count label state")
						}
						if len(got.Callback[0].Issue.Labels) != 1 || got.Callback[0].Issue.Labels[0].IssueCount != 1 {
							before, after := -1, -1
							if len(got.Callback[0].BeforeCountLabels) == 1 {
								before = got.Callback[0].BeforeCountLabels[0].IssueCount
							}
							if len(got.Callback[0].Issue.Labels) == 1 {
								after = got.Callback[0].Issue.Labels[0].IssueCount
							}
							t.Fatalf("ordinary callback alias lost post-count observation: before=%d after=%d", before, after)
						}
					}
					if len(got.Events) != wantEvents || (failed || test.failure == "noop") &&
						(!reflect.DeepEqual(got.Business, before) || !reflect.DeepEqual(got.Metadata, beforeMetadata) || !reflect.DeepEqual(got.Indexes, beforeIndexes)) {
						t.Fatal("failed/no-op ordinary callback changed SQL, root, index, order, or events")
					}
					// Comment routes discard issue locals on error; version conflicts
					// return a comment. Their failed issue observation ends inside
					// the callback, while success/noop keeps the real shallow alias.
					if err != nil {
						for index := range got.Callback {
							got.Callback[index].Issue.Labels = slices.Clone(got.Callback[index].BeforeCountLabels)
						}
					}
					// The captured values are encoded after MutateWorkspace returns,
					// while the containing response still owns every callback attempt.
					if bounded && got.Calls > 0 {
						if repo.MetadataWorkUsedForTesting() <= outerWork {
							t.Fatal("ordinary response lost callback ownership before last use")
						}
						for _, data := range captured {
							if reflect.DeepEqual(*data, domain.Bootstrap{}) {
								t.Fatal("ordinary callback data was cleared before caller last use")
							}
						}
						if err := owner.ReserveEncoding(t.Context(), got.Callback); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := json.Marshal(got.Callback); err != nil {
						t.Fatal(err)
					}
					outputs = append(outputs, scaleP21SW63JSON(t, got))
					installedOutputs = append(installedOutputs, got.Installed)
					installedVia = append(installedVia, got.InstalledVia)
					if bounded {
						owner.Close()
						owner.Close()
						for _, data := range captured {
							if !reflect.DeepEqual(*data, domain.Bootstrap{}) {
								t.Fatal("closed ordinary response retained callback data")
							}
						}
						if !reflect.DeepEqual(scaleP21SW98Installed(t, repo, workspace), got.Installed) ||
							repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 {
							t.Fatal("ordinary response close damaged installed source or leaked/hydrated metadata")
						}
					}
				}
				if !reflect.DeepEqual(installedOutputs[0], installedOutputs[1]) ||
					!reflect.DeepEqual(installedVia[0], installedVia[1]) ||
					!reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatalf("ordinary literal/bounded differential: nativeFields=%v nativeViaEqual=%t first=%s",
						changedMetadataFields(installedOutputs[0], installedOutputs[1]),
						reflect.DeepEqual(installedVia[0], installedVia[1]),
						scaleP21SW98FirstDiff(outputs[0], outputs[1], "$"))
				}
				if test.failure == "" {
					t.Logf("ordinary synchronous SQL reload=%t scenario=%s old=%d bounded=%d delta=%+d",
						reload, test.name, statementCounts[0], statementCounts[1], statementCounts[1]-statementCounts[0])
				}
			})
		}
	}
}
