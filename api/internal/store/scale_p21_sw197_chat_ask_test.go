package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW197AskCallback(input ChatRequestAskInput, fallback domain.User, clock time.Time) func(*domain.Bootstrap) (string, error) {
	return func(data *domain.Bootstrap) (string, error) {
		if slices.ContainsFunc(data.Asks, func(ask domain.Ask) bool { return ask.ID == input.ID || ask.IssueID == input.IssueID }) {
			return input.ID, ErrNoMutation
		}
		requester := fallback
		if index := slices.IndexFunc(data.Users, func(user domain.User) bool { return user.ID == fallback.ID }); index >= 0 {
			requester = data.Users[index]
		}
		data.Asks = append([]domain.Ask{{
			ID: input.ID, Title: input.Title, Body: input.Body, Source: input.Source, Requester: requester,
			TeamID: input.TeamID, TemplateID: input.TemplateID, Status: "approved", IssueID: input.IssueID,
			Approvals: []domain.AskApproval{}, CreatedAt: clock, UpdatedAt: clock,
		}}, data.Asks...)
		var bot *domain.ActorBot
		if data.Viewer.Via != nil && data.Viewer.ID != "" {
			value := *data.Viewer.Via
			bot = &value
		}
		data.AuditLog = append([]domain.AuditLogEntry{{
			ID: idgen.New("audit_"), Actor: data.Viewer, BotActor: bot, Action: "created",
			ResourceType: "ask", ResourceID: input.ID,
			Metadata: map[string]any{"source": input.Source, "issueId": input.IssueID}, CreatedAt: clock,
		}}, data.AuditLog...)
		return input.ID, nil
	}
}

func scaleP21SW197StoredMetadata(t *testing.T, repo *SQLiteStore, workspace string) any {
	t.Helper()
	var root json.RawMessage
	if err := repo.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&root); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.db.QueryContext(t.Context(), `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,collection_order,record_key`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	records := []any{}
	for rows.Next() {
		var field, key string
		var ordinal int
		var raw json.RawMessage
		if err := rows.Scan(&field, &key, &ordinal, &raw); err != nil {
			t.Fatal(err)
		}
		records = append(records, []any{field, key, ordinal, raw})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal([]any{root, records})
	if err != nil {
		t.Fatal(err)
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestScaleP21SW197ChatAskDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw197-")
	clock := time.Date(2026, 11, 16, 2, 3, 4, 0, time.UTC)
	input := ChatRequestAskInput{
		ID: prefix + "ask", IssueID: prefix + "issue", TeamID: f.data.Teams[0].ID,
		Title: "Exact chat request", Body: "Body <>& \x00 中文", Source: "feishu", TemplateID: prefix + "template",
	}
	actor := f.data.Viewer
	actor.Name = "Fresh authenticated actor"
	actor.Via = &domain.ActorBot{ID: prefix + "bot", Name: "Chat request actor"}
	baseline, found := f.storedWorkspace(workspace)
	if !found {
		t.Fatal("owned Ask workspace missing")
	}
	payload := map[string]string{"source": input.Source, "issueId": input.IssueID}
	for _, shape := range []string{"nil", "empty", "1001-split", "1001-inline", "large-inline", "existing-id", "existing-issue", "nested-aggregate-collision"} {
		t.Run(shape, func(t *testing.T) {
			var expected any
			for _, mode := range []string{"legacy", "warm", "reloaded"} {
				t.Run(mode, func(t *testing.T) {
					f.SetWebhookSink(nil)
					f.SetRealtimeSink(nil)
					restore := ForceFullMutationsForTesting(true)
					err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw197_seed", "", nil, func(data *domain.Bootstrap) error {
						*data = cloneBootstrap(baseline)
						for index := range data.Users {
							if data.Users[index].ID == actor.ID {
								data.Users[index].Name = "Stale first stored actor"
								break
							}
						}
						switch shape {
						case "nil":
							data.Asks = nil
						case "empty":
							data.Asks = []domain.Ask{}
						case "1001-split", "1001-inline", "large-inline":
							data.Asks = make([]domain.Ask, 1001)
							for index := range data.Asks {
								data.Asks[index] = domain.Ask{ID: fmt.Sprintf("%solder-%04d", prefix, index), Title: "Retained title", Body: "Retained body", CreatedAt: clock}
							}
							if shape != "1001-split" {
								data.Asks[0].ID = ""
								data.Asks[1].ID = strings.Repeat("long", 60)
								data.Asks[2].ID = data.Asks[3].ID
							}
							if shape == "large-inline" {
								data.Asks[500].Body = strings.Repeat("legal existing Ask body ", 240<<10)
							}
						case "existing-id":
							data.Asks = []domain.Ask{{ID: input.ID, IssueID: "other-issue", Title: "Unchanged existing Ask", CreatedAt: clock}}
						case "existing-issue":
							data.Asks = []domain.Ask{{ID: "different-ask", IssueID: input.IssueID, Title: "Keep the existing Ask ID", CreatedAt: clock}}
						case "nested-aggregate-collision":
							data.Asks = []domain.Ask{}
							data.Projects = append([]domain.Project{{ID: input.ID, Name: "Earlier aggregate family", Description: "Keep aggregate precedence"}}, data.Projects...)
						}
						return nil
					})
					restore()
					if err != nil {
						t.Fatal(err)
					}
					source := f.SQLiteStore
					if mode != "legacy" {
						if mode == "reloaded" {
							if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
								t.Fatal(err)
							}
						}
						source, err = f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
						if err != nil {
							t.Fatal(err)
						}
						defer source.releaseMetadataLogicalSources()
					}
					priorAudits, err := f.ListAuditEntries(t.Context(), f.data.Workspace.ID, AuditQuery{ResourceID: input.ID})
					if err != nil {
						t.Fatal(err)
					}
					auditIDs := map[string]bool{}
					for _, entry := range priorAudits {
						auditIDs[entry.ID] = true
					}
					webhooks, realtime := []any{}, []any{}
					normalizeEvent := func(value any) any {
						raw, err := json.Marshal(value)
						if err != nil {
							t.Fatal(err)
						}
						var fields map[string]any
						if err := json.Unmarshal(raw, &fields); err != nil {
							t.Fatal(err)
						}
						delete(fields, "id")
						delete(fields, "createdAt")
						return fields
					}
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) { webhooks = append(webhooks, normalizeEvent(event)) })
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = append(realtime, normalizeEvent(event)) })
					ctx := ContextWithActor(t.Context(), actor)
					var statements int
					ctx = WithSQLStatementObserver(ctx, func(string) { statements++ })
					callback := scaleP21SW197AskCallback(input, actor, clock)
					if mode == "legacy" {
						err = source.MutateWorkspaceWithAggregate(ctx, workspace, "ask.created", payload, callback)
					} else {
						err = source.CreateChatRequestAsk(ctx, workspace, input, callback)
					}
					if err != nil {
						t.Fatal("literal Ask callback refused", err)
					}
					source.SetWebhookSink(nil)
					source.SetRealtimeSink(nil)
					if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
						t.Fatal("Ask mutation leaked work or hydrated full metadata", source.MetadataWorkUsedForTesting(), source.WorkspaceMetadataDecodes())
					}
					if source.BoundedMetadataEnabled() && shape != "existing-id" && shape != "existing-issue" {
						image, err := source.retainMetadataLogicalSource(t.Context(), workspace)
						if err != nil {
							t.Fatal(err)
						}
						var first domain.Ask
						index, _, _ := metadataLogicalField("asks")
						err = image.fields[index].Children(t.Context(), func(_ reflect.Value, node metadataLogicalNode) error {
							value, err := node.Value(t.Context(), 1<<20)
							if err != nil {
								return err
							}
							first = value.Interface().(domain.Ask)
							return ErrMetadataIterationDone
						})
						image.Release()
						if !errors.Is(err, ErrMetadataIterationDone) || first.ID != input.ID ||
							!reflect.DeepEqual(first.Requester, actor) {
							t.Fatal("Ask lost native prepend/actor attribution", err, first.ID, first.Requester)
						}
					}
					audits, err := f.ListAuditEntries(t.Context(), f.data.Workspace.ID, AuditQuery{ResourceID: input.ID})
					if err != nil {
						t.Fatal(err)
					}
					createdAudits := []domain.AuditLogEntry{}
					for _, entry := range audits {
						if !auditIDs[entry.ID] {
							entry.ID, entry.Seq, entry.Hash = "<generated-audit>", 0, ""
							createdAudits = append(createdAudits, entry)
						}
					}
					chain, err := f.VerifyAuditChain(t.Context(), f.data.Workspace.ID)
					if err != nil || !chain.Valid {
						t.Fatal("Ask audit chain invalid", err, chain)
					}
					noMutation := shape == "existing-id" || shape == "existing-issue"
					if noMutation {
						if len(createdAudits) != 0 || len(webhooks) != 0 || len(realtime) != 0 {
							t.Fatal("existing Ask emitted a mutation", createdAudits, webhooks, realtime)
						}
					} else if len(createdAudits) != 1 || len(webhooks) != 1 || len(realtime) != 1 {
						t.Fatal("Ask lost audit/event publication", len(createdAudits), len(webhooks), len(realtime))
					}
					outcome := []any{scaleP21SW197StoredMetadata(t, f.SQLiteStore, workspace), createdAudits, webhooks, realtime}
					raw, err := json.Marshal(outcome)
					if err != nil {
						t.Fatal(err)
					}
					var normalized any
					if err := json.Unmarshal(raw, &normalized); err != nil {
						t.Fatal(err)
					}
					if mode == "legacy" {
						expected = normalized
					} else if !reflect.DeepEqual(expected, normalized) {
						t.Fatalf("Ask metadata/audit/event image differs from legacy (%s/%s)", shape, mode)
					}
					t.Logf("Ask shape=%s mode=%s SQL=%d", shape, mode, statements)
				})
			}
		})
	}
}

func TestScaleP21SW197ChatAskCancellationAndBudget(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	input := ChatRequestAskInput{ID: idgen.New("sw197-ask-"), IssueID: idgen.New("sw197-issue-"), Source: "lark"}
	before := scaleP21SW197StoredMetadata(t, f.SQLiteStore, f.data.Workspace.URLKey)
	callback := scaleP21SW197AskCallback(input, f.data.Viewer, time.Now().UTC())
	cancelled, cancel := context.WithCancel(ContextWithActor(t.Context(), f.data.Viewer))
	cancel()
	if err := view.CreateChatRequestAsk(cancelled, f.data.Workspace.URLKey, input, callback); !errors.Is(err, context.Canceled) {
		t.Fatal("Ask ignored cancellation", err)
	}
	_, release, err := view.tryAdmitAdditionalMetadataWork(t.Context(), metadataWorkBytes)
	if err != nil {
		t.Fatal(err)
	}
	err = view.CreateChatRequestAsk(ContextWithActor(t.Context(), f.data.Viewer), f.data.Workspace.URLKey, input, callback)
	release()
	if !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("Ask budget refusal leaked work", err, view.MetadataWorkUsedForTesting())
	}
	if !reflect.DeepEqual(before, scaleP21SW197StoredMetadata(t, f.SQLiteStore, f.data.Workspace.URLKey)) {
		t.Fatal("refused Ask changed persistent metadata")
	}
	sentinel := errors.New("rolled back Ask callback")
	err = view.CreateChatRequestAsk(ContextWithActor(t.Context(), f.data.Viewer), f.data.Workspace.URLKey, input, func(data *domain.Bootstrap) (string, error) {
		_, _ = callback(data)
		return "", sentinel
	})
	if !errors.Is(err, sentinel) || view.MetadataWorkUsedForTesting() != 0 ||
		!reflect.DeepEqual(before, scaleP21SW197StoredMetadata(t, f.SQLiteStore, f.data.Workspace.URLKey)) {
		t.Fatal("failed callback escaped rollback or leaked", err, view.MetadataWorkUsedForTesting())
	}
	t.Run("cancel-during-persistence", func(t *testing.T) {
		ctx, cancel := context.WithCancel(ContextWithActor(t.Context(), f.data.Viewer))
		defer cancel()
		writes := 0
		ctx = WithSQLStatementObserver(ctx, func(query string) {
			if strings.HasPrefix(query, "INSERT INTO workspace_metadata_records") {
				writes++
				cancel()
			}
		})
		err := view.CreateChatRequestAsk(ctx, f.data.Workspace.URLKey, input, callback)
		if !errors.Is(err, context.Canceled) || writes == 0 || view.MetadataWorkUsedForTesting() != 0 ||
			!reflect.DeepEqual(before, scaleP21SW197StoredMetadata(t, f.SQLiteStore, f.data.Workspace.URLKey)) {
			t.Fatal("cancellation during metadata persistence escaped rollback", err, writes, view.MetadataWorkUsedForTesting())
		}
	})
	t.Run("same-replica-retry", func(t *testing.T) {
		var workers sync.WaitGroup
		for range 4 {
			workers.Go(func() {
				if err := view.CreateChatRequestAsk(ContextWithActor(t.Context(), f.data.Viewer), f.data.Workspace.URLKey, input, callback); err != nil {
					t.Error(err)
				}
			})
		}
		workers.Wait()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("concurrent retries retained work", view.MetadataWorkUsedForTesting())
		}
		events, err := f.Events(t.Context(), input.ID)
		if err != nil || len(events) != 1 {
			t.Fatal("concurrent same-replica retries duplicated creation", err, len(events))
		}
		audits, err := f.ListAuditEntries(t.Context(), f.data.Workspace.ID, AuditQuery{ResourceID: input.ID})
		if err != nil || len(audits) != 1 {
			t.Fatal("concurrent same-replica retries duplicated audit", err, len(audits))
		}
	})
}
