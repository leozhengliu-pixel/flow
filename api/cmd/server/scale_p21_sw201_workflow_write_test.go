package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"

	"github.com/Deln0r/ygo"
)

func scaleP21SW201EditorProducerIssue(t *testing.T, data domain.Bootstrap, id string) []byte {
	t.Helper()
	var issue domain.Issue
	found := false
	for _, item := range data.Issues {
		if item.ID != id {
			continue
		}
		raw, err := json.Marshal(item)
		if err != nil || json.Unmarshal(raw, &issue) != nil {
			t.Fatal("editor source issue", err)
		}
		found = true
		break
	}
	if !found {
		t.Fatal("editor source issue missing")
	}
	description := strings.Repeat("x", 4<<20)
	documentJSON, err := json.Marshal(mentioningDocument(description))
	if err != nil {
		t.Fatal(err)
	}
	doc := ygo.NewDocWithOptions(ygo.Options{ClientID: 201})
	fragment := ygo.NewXmlFragment(doc, "prosemirror")
	tx := doc.WriteTxn()
	paragraph := fragment.InsertXmlElement(tx, 0, "paragraph")
	if err := paragraph.InsertXmlText(tx, 0).Insert(tx, 0, description); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	state := base64.StdEncoding.EncodeToString(ygo.EncodeStateAsUpdate(doc))
	descriptionState := string(documentJSON)
	input := domain.IssueUpdateInput{
		Description: &description, DescriptionState: &descriptionState, ContentState: &state,
	}
	request, err := json.Marshal(input)
	if err != nil || len(request) > maxJSONRequestBytes {
		t.Fatal("supported editor update exceeds the existing HTTP request limit", err, len(request))
	}
	// This is the actual issue-record route's update producer. Capture its
	// generated timestamp once so all three fixture legs have one preimage.
	if _, err := applyUpdate(&data, &issue, input); err != nil {
		t.Fatal("supported editor update", err)
	}
	if issue.DocumentContent == nil || issue.DocumentContent.Content != description ||
		issue.DocumentContent.ContentState != state || issue.DescriptionState != descriptionState {
		t.Fatal("actual editor producer did not retain its projections")
	}
	raw, err := json.Marshal(issue)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("description_bytes=%d description_state_bytes=%d content_state_bytes=%d request_bytes=%d issue_row_bytes=%d",
		len(description), len(descriptionState), len(state), len(request), len(raw))
	return raw
}

func scaleP21SW201Metadata(t *testing.T, database *scaleP04Database) any {
	t.Helper()
	rootRows := database.query(t, `SELECT data FROM workspace_states WHERE workspace_key=?`, database.key)
	var root json.RawMessage
	if !rootRows.Next() {
		t.Fatal("owned workflow root missing")
	}
	if err := rootRows.Scan(&root); err != nil {
		t.Fatal(err)
	}
	rootRows.Close()
	rows := database.query(t, `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,collection_order,record_key`, database.key)
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
	return []any{root, records}
}

func scaleP21SW201Normalize(t *testing.T, value any, runID string, started, finished time.Time) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	var walk func(any, string) any
	walk = func(value any, key string) any {
		switch value := value.(type) {
		case string:
			if runID != "" && value == runID {
				return "<generated-workflow-run>"
			}
			if clock, err := time.Parse(time.RFC3339Nano, value); err == nil {
				offsets := []time.Duration{}
				switch strings.ToLower(key) {
				case "createdat", "updatedat", "scheduledat", "startedat", "completedat", "lastrunat":
					offsets = []time.Duration{0}
				case "nextretryat":
					offsets = []time.Duration{2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 32 * time.Minute, 64 * time.Minute}
				case "nextrunat":
					offsets = []time.Duration{15 * time.Minute}
				}
				for _, offset := range offsets {
					base := clock.Add(-offset)
					if !base.Before(started) && !base.After(finished) {
						return fmt.Sprintf("<generated-clock+%s>", offset)
					}
				}
			}
		case map[string]any:
			for key, child := range value {
				value[key] = walk(child, key)
			}
		case []any:
			for index, child := range value {
				value[index] = walk(child, key)
			}
		}
		return value
	}
	return walk(result, "")
}

func TestScaleP21SW201WorkflowRunDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	f, database, issue := scaleP21SW44ChatFixture(t)
	repo, workspace := database.repo, database.key
	prefix := idgen.New("sw201-")
	workflowID := prefix + "workflow"
	baseline, found := repo.BootstrapFor(workspace)
	if !found {
		t.Fatal("owned workflow fixture missing")
	}
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	sqlFixture := scaleP21SW10Fixture{reader: database}
	sqlFixture.data.Workspace.URLKey = workspace
	for _, test := range []string{
		"success", "partial-action-failure", "detached-issue-pointer", "duplicate-definition",
		"1001-run-tail-old-predecessor", "retry-not-due", "missing-definition", "disabled-definition",
		"schedule-not-due", "schedule-success", "legal-large-issue", "legal-large-editor-state",
		"legal-large-stale-project", "legal-large-retained-history",
	} {
		t.Run(test, func(t *testing.T) {
			var expected any
			var editorProducerIssue []byte
			if test == "legal-large-editor-state" {
				editorProducerIssue = scaleP21SW201EditorProducerIssue(t, baseline, issue.ID)
			}
			future := time.Now().UTC().Add(time.Hour)
			for _, mode := range []string{"legacy", "warm", "reloaded"} {
				t.Run(mode, func(t *testing.T) {
					repo.SetWebhookSink(nil)
					repo.SetRealtimeSink(nil)
					trigger, dueOnly := "manual", false
					if strings.HasPrefix(test, "schedule-") || test == "disabled-definition" {
						trigger, dueOnly = "schedule", true
					}
					if strings.HasPrefix(test, "retry-") || test == "1001-run-tail-old-predecessor" || test == "legal-large-retained-history" {
						trigger, dueOnly = "retry", true
					}
					clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
					past := clock
					restore := store.ForceFullMutationsForTesting(true)
					err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw201_fixture", "", nil, func(data *domain.Bootstrap) error {
						var original domain.Bootstrap
						if err := json.Unmarshal(saved, &original); err != nil {
							return err
						}
						*data = original
						definition := domain.WorkflowDefinition{
							ID: workflowID, Name: "Exact workflow", Trigger: "manual", Enabled: true,
							MaxAttempts: 8, CreatorID: baseline.Viewer.ID, CreatedAt: clock, UpdatedAt: clock,
							Actions: []domain.WorkflowAction{
								{Type: "setIssuePriority", Config: map[string]string{"priority": "1"}},
								{Type: "notify", Config: map[string]string{}},
							},
						}
						switch test {
						case "partial-action-failure":
							definition.Actions = append(definition.Actions, domain.WorkflowAction{Type: "assignIssue", Config: map[string]string{"userId": "missing-user"}})
						case "detached-issue-pointer":
							definition.Actions = []domain.WorkflowAction{
								{Type: "createIssue", Config: map[string]string{"teamId": issue.Team.ID, "title": "Workflow child"}},
								{Type: "setIssuePriority", Config: map[string]string{"priority": "1"}},
							}
						case "disabled-definition":
							definition.Enabled = false
						case "schedule-not-due", "schedule-success":
							definition.Trigger, definition.Schedule, definition.NextRunAt = "schedule", "@every 15m", &past
							if test == "schedule-not-due" {
								definition.NextRunAt = &future
							}
						}
						data.WorkflowDefinitions = []domain.WorkflowDefinition{definition}
						if test == "duplicate-definition" {
							second := definition
							second.Name, second.Actions = "Later duplicate", []domain.WorkflowAction{{Type: "assignIssue", Config: map[string]string{"userId": "missing-user"}}}
							data.WorkflowDefinitions = append(data.WorkflowDefinitions, second)
						}
						if test == "missing-definition" {
							data.WorkflowDefinitions = nil
						}
						data.WorkflowRuns = []domain.WorkflowRun{}
						if test == "1001-run-tail-old-predecessor" {
							for index := range 1001 {
								run := domain.WorkflowRun{ID: fmt.Sprintf("%srun-%04d", prefix, index), WorkflowID: "other", Attempt: 1, Status: "succeeded", ScheduledAt: clock, StartedAt: clock}
								if index == 0 {
									run.WorkflowID, run.Status, run.Attempt, run.NextRetryAt = workflowID, "failed", 4, &past
								}
								data.WorkflowRuns = append(data.WorkflowRuns, run)
							}
						}
						if test == "retry-not-due" {
							data.WorkflowRuns = []domain.WorkflowRun{{ID: prefix + "prior", WorkflowID: workflowID, Status: "failed", Attempt: 3, NextRetryAt: &future}}
						}
						for index := range data.Issues {
							if data.Issues[index].ID == issue.ID {
								if test == "legal-large-editor-state" {
									if err := json.Unmarshal(editorProducerIssue, &data.Issues[index]); err != nil {
										return err
									}
								}
								data.Issues[index].Priority, data.Issues[index].PriorityLabel = 4, priorityLabel(4)
								if test == "legal-large-issue" {
									data.Issues[index].Description = strings.Repeat("x", 4<<20)
								}
							}
						}
						if test == "legal-large-stale-project" {
							data.Projects = append(data.Projects, domain.Project{
								ID: prefix + "large-project", Name: "Unrelated stale lead",
								Description: strings.Repeat("x", 5<<20),
								Lead:        &domain.User{ID: baseline.Viewer.ID, Name: "Old actor snapshot"},
							})
						}
						return nil
					})
					restore()
					if err != nil {
						t.Fatal(err)
					}
					if test == "legal-large-retained-history" {
						// Build the legal history through individual ordinary
						// writes; its total exceeds one transaction's 32 MiB.
						body := strings.Repeat("x", 5<<20)
						for index := range 4 {
							restore := store.ForceFullMutationsForTesting(true)
							err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "workflowRuns"), workspace,
								"workflow_definition.updated", workflowID, nil, func(data *domain.Bootstrap) error {
									data.WorkflowRuns = append(data.WorkflowRuns, domain.WorkflowRun{
										ID: fmt.Sprintf("%sretained-%d", prefix, index), WorkflowID: workflowID,
										Status: "failed", Attempt: index + 1, NextRetryAt: &past,
										Error: body, Output: map[string]string{"retained": body},
									})
									return nil
								})
							restore()
							if err != nil {
								t.Fatal(err)
							}
						}
					}
					scaleP21SW201Exec(t, database, `UPDATE issue_number_sequences SET last_number=? WHERE workspace_key=?`, baseline.NextIssueNumber-1, workspace)
					source := repo
					if mode != "legacy" {
						if mode == "reloaded" {
							scaleP21SW201ReloadSource(t, database, repo)
						}
						source, err = repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
						if err != nil {
							t.Fatal(err)
						}
						if mode == "reloaded" {
							// The constructor captures the owner's native
							// generation. Force the bounded SQL path afterward.
							scaleP21SW201ReloadSource(t, database, source)
						}
					}
					f.s.store = source
					before := scaleP21SW59SQL(t, sqlFixture)
					prior, err := repo.Events(t.Context(), workflowID)
					if err != nil {
						t.Fatal(err)
					}
					priorIDs := map[string]bool{}
					for _, event := range prior {
						priorIDs[event.ID] = true
					}
					got := scaleP21SW59Outcome{Header: make(http.Header)}
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, event))
					})
					captureRealtime := func(_ string, event domain.RealtimeEvent) {
						got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, event))
					}
					source.SetRealtimeSink(captureRealtime)
					if source != repo {
						repo.SetRealtimeSink(captureRealtime)
					}
					started := time.Now().UTC()
					var run domain.WorkflowRun
					var runErr error
					statements := 0
					peakWork := 0
					lastStatement := ""
					func() {
						ctx, closeOutput := beginMetadataOutputReads(t.Context())
						defer closeOutput()
						ctx = store.WithSQLStatementObserver(ctx, func(query string) {
							statements++
							peakWork = max(peakWork, source.MetadataWorkUsedForTesting())
							lastStatement = query
						})
						run, runErr = f.s.runWorkflow(ctx, workspace, workflowID, trigger, "issue", issue.ID, dueOnly)
					}()
					finished := time.Now().UTC()
					switch test {
					case "success", "detached-issue-pointer", "duplicate-definition", "1001-run-tail-old-predecessor",
						"schedule-success", "legal-large-issue", "legal-large-editor-state",
						"legal-large-stale-project", "legal-large-retained-history":
						if runErr != nil || run.Status != "succeeded" {
							t.Logf("workflow failure SQL=%d peak-work=%d last-statement=%s", statements, peakWork, lastStatement)
							t.Fatalf("required workflow success refused (%s/%s): status=%q error=%v", test, mode, run.Status, runErr)
						}
					}
					source.SetWebhookSink(nil)
					source.SetRealtimeSink(nil)
					repo.SetRealtimeSink(nil)
					if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
						t.Fatal("workflow run leaked work or hydrated full metadata", source.MetadataWorkUsedForTesting(), source.WorkspaceMetadataDecodes())
					}
					got.Rows = scaleP21SW59SQL(t, sqlFixture)
					events, err := repo.Events(t.Context(), workflowID)
					if err != nil {
						t.Fatal(err)
					}
					for _, event := range events {
						if !priorIDs[event.ID] {
							got.Events = append(got.Events, event)
						}
					}
					got.Response, err = json.Marshal(map[string]any{"run": run, "error": scaleP21ChatPointError(runErr), "metadata": scaleP21SW201Metadata(t, database)})
					if err != nil {
						t.Fatal(err)
					}
					normalized := scaleP21SW201Normalize(t, scaleP21SW59Normalized(t, got, before, started, finished), run.ID, started, finished)
					if mode == "legacy" {
						expected = normalized
					} else if !reflect.DeepEqual(expected, normalized) {
						t.Fatalf("workflow response/rows/atomic side effects differ from literal (%s/%s), run error=%v", test, mode, runErr)
					}
					if test == "partial-action-failure" && (run.Status != "failed" || run.NextRetryAt == nil || len(got.Events) != 1) {
						t.Fatal("failed action rolled back prior successful actions", run, len(got.Events))
					}
					if test == "detached-issue-pointer" {
						after, err := repo.IssueRecord(t.Context(), workspace, issue.ID)
						if err != nil || after.Priority != 4 || run.Output["issueId"] == "" {
							t.Fatal("actions after prepend changed the legacy detached-pointer behavior", err, after.Priority, run.Output)
						}
					}
					t.Logf("workflow case=%s mode=%s SQL=%d peak-work=%d", test, mode, statements, peakWork)
				})
			}
		})
	}
}

func TestScaleP21SW201WorkflowActionClosureGuard(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "automation_intake.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "applyWorkflowActions" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			switchNode, ok := node.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			selector, ok := switchNode.Tag.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Type" {
				return true
			}
			for _, clause := range switchNode.Body.List {
				for _, expression := range clause.(*ast.CaseClause).List {
					literal, ok := expression.(*ast.BasicLit)
					if !ok {
						t.Fatal("workflow action selector is no longer a literal closure")
					}
					var action string
					if err := json.Unmarshal([]byte(literal.Value), &action); err != nil {
						t.Fatal(err)
					}
					actions = append(actions, action)
				}
			}
			return false
		})
	}
	if !reflect.DeepEqual(actions, []string{"notify", "assignIssue", "setIssuePriority", "addIssueLabel", "createIssue"}) {
		t.Fatal("workflow action loader/persistence closure needs a Tier A extension", actions)
	}
}
