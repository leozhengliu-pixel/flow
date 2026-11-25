package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21IssuePersistenceFixture(t *testing.T, projects []domain.Project, documents []domain.Document) (scaleP09Fixture, domain.Issue) {
	t.Helper()
	f := scaleP09Database(t)
	at := utcDay(time.Now().UTC()).AddDate(0, 0, -15)
	person := domain.User{ID: "p21-person", Name: "Person", Active: true}
	team := domain.Team{ID: "p21-team", Name: "Selected private team", Key: "P21", Private: true}
	state := domain.WorkflowState{ID: "p21-started", Name: "Started", Type: "started", TeamID: team.ID}
	issue := domain.Issue{ID: "p21-issue", Identifier: "P21-1", Number: 1, Version: 1, Title: "Before", Team: team, Creator: person, State: state, CreatedAt: at, UpdatedAt: at, Project: &domain.ProjectSummary{ID: "p21-project", Name: "Selected"}}
	for index := range projects {
		if projects[index].CreatedAt.IsZero() {
			projects[index].CreatedAt = at
		}
		projects[index].Status = domain.ProjectStatus{ID: "p21-project-status", Name: "Planned", Type: "planned"}
	}
	for index := range documents {
		documents[index].CreatedAt = at
	}
	if err := f.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.issue_persistence_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Users = append(data.Users, person)
		data.Teams = append(data.Teams, team)
		data.States = append(data.States, state, domain.WorkflowState{ID: "p21-completed", Name: "Completed", Type: "completed", TeamID: team.ID})
		data.Projects = append(data.Projects, projects...)
		data.Documents = append(data.Documents, documents...)
		data.Issues = append(data.Issues, issue)
		data.Webhooks = append(data.Webhooks, domain.Webhook{ID: "p21-hook", Enabled: true, URL: "https://example.invalid"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return f, issue
}

func scaleP21IssuePersistenceView(t *testing.T, f scaleP09Fixture, bounded, reload bool) *SQLiteStore {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	if reload {
		f.mu.Lock()
		f.forgetCopyRevisionLocked(workspace)
		f.mu.Unlock()
		if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
	}
	if !bounded {
		return f.SQLiteStore
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	return view
}

type scaleP21IssuePhysicalField struct {
	Shape string
	Root  json.RawMessage
	Rows  []struct {
		Key   string
		Order int
		Raw   json.RawMessage
	}
}

func scaleP21IssuePhysical(t *testing.T, repo *SQLiteStore, workspace, name string) scaleP21IssuePhysicalField {
	t.Helper()
	var raw []byte
	if err := repo.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	var shapes map[string]string
	if err := json.Unmarshal(root[metadataCollectionsKey], &shapes); err != nil {
		t.Fatal(err)
	}
	result := scaleP21IssuePhysicalField{Shape: shapes[name], Root: root[name]}
	rows, err := repo.db.QueryContext(t.Context(), `SELECT record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field=? ORDER BY collection_order,record_key`, workspace, name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var item struct {
			Key   string
			Order int
			Raw   json.RawMessage
		}
		if err := rows.Scan(&item.Key, &item.Order, &item.Raw); err != nil {
			t.Fatal(err)
		}
		result.Rows = append(result.Rows, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21IssueNative(t *testing.T, repo *SQLiteStore, workspace, name string) any {
	t.Helper()
	if !repo.BoundedMetadataEnabled() {
		data, _ := repo.storedWorkspace(workspace)
		index, _, _ := metadataLogicalField(name)
		return reflect.ValueOf(data).Field(index).Interface()
	}
	source, err := repo.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	index, _, _ := metadataLogicalField(name)
	value, err := source.fields[index].Value(t.Context(), metadataWorkBytes)
	if err != nil {
		t.Fatal(err)
	}
	return value.Interface()
}

func scaleP21IssuePersistenceUpdate(t *testing.T, repo *SQLiteStore, workspace string, issue domain.Issue, business bool, contexts ...context.Context) domain.Issue {
	t.Helper()
	ctx := t.Context()
	if len(contexts) != 0 {
		ctx = contexts[0]
	}
	input := domain.IssueUpdateInput{Title: new("Updated"), StateID: new("p21-completed"), Estimate: new(float64(7)), ProjectMilestoneID: new("milestone")}
	updated, err := repo.UpdateIssueRecord(ctx, workspace, issue.ID, &issue.Version, IssueMutationScope{Payload: input}, func(data *domain.Bootstrap, current *domain.Issue) error {
		if !slices.ContainsFunc(data.Projects, func(project domain.Project) bool { return project.ID == current.Project.ID }) {
			t.Fatal("project callback input absent")
		}
		current.Title = *input.Title
		current.Estimate = input.Estimate
		current.ProjectMilestoneID = input.ProjectMilestoneID
		for _, state := range data.States {
			if state.ID == *input.StateID {
				current.State = state
				break
			}
		}
		if business {
			at := issue.CreatedAt.AddDate(0, 0, 1)
			data.Activities[current.ID] = append(data.Activities[current.ID], domain.ActivityEvent{ID: "p21-activity", Type: "issue.updated", Actor: issue.Creator, CreatedAt: at, Metadata: domain.ActivityMetadata{"stateBeforeId": issue.State.ID, "stateBeforeType": issue.State.Type, "estimateBefore": "1", "title": "Updated"}})
			data.Notifications = append(data.Notifications, domain.Notification{ID: "p21-notification", RecipientID: issue.Creator.ID, Actor: issue.Creator, IssueID: current.ID, SourceID: current.ID, SourceType: "issue", Type: "issue", Category: "assigned", CreatedAt: at, UpdatedAt: at})
			data.NotificationDeliveries = append(data.NotificationDeliveries, domain.NotificationDelivery{ID: "p21-delivery", NotificationID: "p21-notification", RecipientID: issue.Creator.ID, Channel: "email", Status: "pending", CreatedAt: at, UpdatedAt: at})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	updated.UpdatedAt = time.Time{}
	return updated
}

func TestScaleP21IssuePersistenceSplitBusiness(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, reload := range []bool{false, true} {
		t.Run(fmt.Sprint("reload=", reload), func(t *testing.T) {
			var physical []scaleP21IssuePhysicalField
			var native []any
			var issues []domain.Issue
			var effects []map[string][]json.RawMessage
			for _, bounded := range []bool{false, true} {
				f, issue := scaleP21IssuePersistenceFixture(t, []domain.Project{
					{ID: "p21-project", Name: "Selected", TeamIDs: []string{"p21-team"}, Milestones: []domain.ProjectMilestone{{ID: "milestone"}}},
					{ID: "unrelated-project", Description: strings.Repeat("unrelated body ", 20000)},
				}, nil)
				workspace := f.data.Workspace.URLKey
				repo := scaleP21IssuePersistenceView(t, f, bounded, reload)
				var webhook []domain.DomainEvent
				var realtime []domain.RealtimeEvent
				repo.SetWebhookSink(func(_ string, event domain.DomainEvent) { webhook = append(webhook, event) })
				repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
					if event.Type == "issue.updated" {
						realtime = append(realtime, event)
					}
				})
				prior := scaleP21IssuePhysical(t, repo, workspace, "projects").Rows[1].Raw
				issues = append(issues, scaleP21IssuePersistenceUpdate(t, repo, workspace, issue, true))
				state := scaleP21IssuePhysical(t, repo, workspace, "projects")
				if string(prior) != string(state.Rows[1].Raw) {
					t.Fatal("unrelated project row changed")
				}
				physical = append(physical, state)
				native = append(native, scaleP21IssueNative(t, repo, workspace, "projects"))
				outputs := map[string][]json.RawMessage{}
				rows, err := f.db.QueryContext(t.Context(), `SELECT kind,data FROM workspace_content_records WHERE workspace_key=? AND kind IN ('activity','notification','delivery') ORDER BY kind,id`, workspace)
				if err != nil {
					t.Fatal(err)
				}
				for rows.Next() {
					var kind string
					var raw json.RawMessage
					if err := rows.Scan(&kind, &raw); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					outputs[kind] = append(outputs[kind], raw)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					t.Fatal(err)
				}
				for _, kind := range []string{"activity", "notification", "delivery"} {
					if len(outputs[kind]) != 1 {
						t.Fatal("business side effect missing", kind, len(outputs[kind]))
					}
				}
				if len(webhook) != 1 || len(realtime) != 1 || len(webhook[0].PreviousValues) == 0 {
					t.Fatal("issue webhook/realtime/previous-values effects missing")
				}
				outputs["webhook-input"] = []json.RawMessage{webhook[0].Payload, webhook[0].PreviousValues}
				var payload map[string]json.RawMessage
				if err := json.Unmarshal(realtime[0].Payload, &payload); err != nil {
					t.Fatal(err)
				}
				var entity domain.Issue
				if err := json.Unmarshal(payload["entity"], &entity); err != nil {
					t.Fatal(err)
				}
				entity.UpdatedAt = time.Time{}
				payload["entity"], _ = json.Marshal(entity)
				encoded, _ := json.Marshal(payload)
				outputs["realtime"] = []json.RawMessage{encoded}
				var content string
				if err := f.db.QueryRowContext(t.Context(), `SELECT content FROM issue_search_documents WHERE workspace_key=? AND issue_id=?`, workspace, issue.ID).Scan(&content); err != nil {
					t.Fatal(err)
				}
				encoded, _ = json.Marshal(content)
				outputs["issue-search"] = []json.RawMessage{encoded}
				effects = append(effects, outputs)
				if bounded && (repo.WorkspaceMetadataDecodes() != 0 || repo.MetadataWorkUsedForTesting() != 0) {
					t.Fatal("bounded writer decoded full metadata or leaked work")
				}
			}
			if !reflect.DeepEqual(physical[0], physical[1]) || !reflect.DeepEqual(native[0], native[1]) || !reflect.DeepEqual(issues[0], issues[1]) || !reflect.DeepEqual(effects[0], effects[1]) {
				t.Fatal("literal/bounded split physical/native/business output differs",
					reflect.DeepEqual(physical[0], physical[1]), reflect.DeepEqual(native[0], native[1]), reflect.DeepEqual(issues[0], issues[1]), reflect.DeepEqual(effects[0], effects[1]))
			}
		})
	}
}

func TestScaleP21IssuePersistenceInlineProjectShapes(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, keepInline := range []bool{false, true} {
		t.Run(fmt.Sprint("keepInline=", keepInline), func(t *testing.T) {
			var physical []scaleP21IssuePhysicalField
			var native []any
			for _, bounded := range []bool{false, true} {
				projects := []domain.Project{{ID: "p21-project", Name: "First native"}, {ID: "p21-project", Name: "后置项目"}}
				if keepInline {
					projects = append(projects, domain.Project{ID: "sibling", Name: "Sibling one"}, domain.Project{ID: "sibling", Name: "Sibling two"})
				}
				f, issue := scaleP21IssuePersistenceFixture(t, projects, nil)
				workspace := f.data.Workspace.URLKey
				repo := scaleP21IssuePersistenceView(t, f, bounded, false)
				before := scaleP21IssuePhysical(t, repo, workspace, "projects")
				if before.Shape != "" || len(before.Rows) != 0 {
					t.Fatal("duplicate fixture did not start inline")
				}
				scaleP21IssuePersistenceUpdate(t, repo, workspace, issue, false)
				physical = append(physical, scaleP21IssuePhysical(t, repo, workspace, "projects"))
				native = append(native, scaleP21IssueNative(t, repo, workspace, "projects"))
				if bounded && (repo.WorkspaceMetadataDecodes() != 0 || repo.MetadataWorkUsedForTesting() != 0) {
					t.Fatal("inline project writer decoded full metadata or leaked work")
				}
			}
			if !reflect.DeepEqual(physical[0], physical[1]) || !reflect.DeepEqual(native[0], native[1]) {
				t.Fatal("inline physical/native state differs", physical, native)
			}
			// An inline field has no rows to rebase onto: the duplicate entries
			// stay (no first-occurrence removal) and the field stays inline.
			wantCount := 2
			if keepInline {
				wantCount = 4
			}
			if physical[1].Shape != "" || len(native[1].([]domain.Project)) != wantCount {
				t.Fatal("inline duplicates were removed or the shape changed", physical[1].Shape, native[1])
			}
		})
	}
}

func TestScaleP21IssuePersistenceDocumentChaining(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, inlineProjects := range []bool{false, true} {
		for _, inlineDocuments := range []bool{false, true} {
			t.Run(fmt.Sprintf("projectsInline=%t/documentsInline=%t", inlineProjects, inlineDocuments), func(t *testing.T) {
				var physical [][]scaleP21IssuePhysicalField
				var native [][]any
				for _, bounded := range []bool{false, true} {
					projects := []domain.Project{{ID: "p21-project", Name: "Selected"}}
					documents := []domain.Document{{ID: "p21-document", IssueID: "p21-issue", IssueTeamID: "p21-team", InheritedTeamIDs: []string{"p21-team"}, ProjectIDs: []string{"p21-project"}, Title: "文档"}}
					if inlineProjects {
						projects = append(projects, projects[0], domain.Project{ID: "sibling", Name: "A"}, domain.Project{ID: "sibling", Name: "B"})
					}
					if inlineDocuments {
						documents = append(documents, documents[0], domain.Document{ID: "sibling", Title: "A"}, domain.Document{ID: "sibling", Title: "B"})
					}
					f, issue := scaleP21IssuePersistenceFixture(t, projects, documents)
					workspace := f.data.Workspace.URLKey
					repo := scaleP21IssuePersistenceView(t, f, bounded, false)
					selected, _ := repo.storedWorkspace(workspace)
					var inputs *issueHotInputs
					if bounded {
						selected.Issues = []domain.Issue{issue}
						var err error
						inputs, err = repo.loadIssueProjectInputs(t.Context(), workspace, &selected, issue, domain.IssueUpdateInput{})
						if err != nil {
							t.Fatal(err)
						}
						defer inputs.close()
						if err := inputs.loadIssueDocuments(t.Context(), map[string]bool{issue.ID: true}); err != nil {
							t.Fatal(err)
						}
						var baseline domain.Bootstrap
						inputs.installProjects(&selected, &baseline)
					}
					var updatedDocuments []domain.Document
					for _, document := range selected.Documents {
						if document.IssueID == issue.ID {
							document.IssueTeamID = "destination"
							document.InheritedTeamIDs = []string{"destination", "p21-team"}
						}
						updatedDocuments = append(updatedDocuments, document)
					}
					updated := issue
					updated.Estimate = new(float64(7))
					repo.writeMu.Lock()
					ctx, copyWrite := repo.watchMetadataWrite(t.Context(), workspace)
					tx, err := repo.db.BeginTx(ctx, repo.recordTxOptions())
					if err != nil {
						repo.writeMu.Unlock()
						t.Fatal(err)
					}
					err = repo.writeIssueRecord(ctx, tx, workspace, updated, selected)
					var publication *metadataLogicalPublication
					var next *domain.Bootstrap
					if err == nil && bounded {
						publication, err = repo.persistSelectedIssueMetadata(ctx, tx, workspace, selected, []domain.Issue{issue}, []domain.Issue{updated}, updatedDocuments, inputs)
					} else if err == nil {
						next, err = repo.persistIssueMetadata(ctx, tx, workspace, []domain.Issue{issue}, []domain.Issue{updated}, updatedDocuments)
					}
					if err == nil {
						err = tx.Commit()
					}
					if err == nil && publication != nil {
						err = publication.Install()
					}
					if err == nil && next != nil {
						repo.installWrittenWorkspace(workspace, *next, false, copyWrite)
					}
					if publication != nil {
						publication.Close()
					}
					tx.Rollback()
					repo.writeMu.Unlock()
					if err != nil {
						t.Fatal("document/progress transaction failed", bounded, err)
					}
					physical = append(physical, []scaleP21IssuePhysicalField{scaleP21IssuePhysical(t, repo, workspace, "projects"), scaleP21IssuePhysical(t, repo, workspace, "documents")})
					native = append(native, []any{scaleP21IssueNative(t, repo, workspace, "projects"), scaleP21IssueNative(t, repo, workspace, "documents")})
					if inputs != nil {
						inputs.close()
						if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 {
							t.Fatal("document/progress owner leaked or decoded full metadata")
						}
					}
				}
				if !reflect.DeepEqual(physical[0], physical[1]) || !reflect.DeepEqual(native[0], native[1]) {
					t.Fatal("chained physical/native project/document postimages differ",
						reflect.DeepEqual(physical[0], physical[1]), reflect.DeepEqual(native[0], native[1]))
				}
			})
		}
	}
}

func TestScaleP21IssuePersistenceRollbackAndAdmission(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, mode := range []string{"callback", "cancel", "inline-cancel", "quota", "root"} {
		t.Run(mode, func(t *testing.T) {
			projects := []domain.Project{{ID: "p21-project", Name: "Selected"}}
			if mode == "root" || mode == "inline-cancel" {
				projects = append(projects, projects[0])
			}
			f, issue := scaleP21IssuePersistenceFixture(t, projects, nil)
			workspace := f.data.Workspace.URLKey
			view := scaleP21IssuePersistenceView(t, f, true, false)
			if mode == "root" {
				var raw []byte
				if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var root map[string]json.RawMessage
				var grown []domain.Project
				if err := json.Unmarshal(raw, &root); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(root["projects"], &grown); err != nil {
					t.Fatal(err)
				}
				for index := range grown {
					grown[index].Description = strings.Repeat("legal oversized inline body ", 20000)
				}
				root["projects"], _ = json.Marshal(grown)
				raw, _ = json.Marshal(root)
				if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, raw, workspace); err != nil {
					t.Fatal(err)
				}
				f.mu.Lock()
				f.forgetCopyRevisionLocked(workspace)
				f.mu.Unlock()
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
					t.Fatal(err)
				}
				t.Log("oversized inline root remains admitted until a writer-local quota failure")
			}
			prior := scaleP21IssuePhysical(t, f.SQLiteStore, workspace, "projects")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "inline-cancel" {
				ctx = WithSQLStatementObserver(ctx, func(query string) {
					if strings.HasPrefix(query, "UPDATE workspace_states SET workspace_id=") {
						cancel()
					}
				})
			}
			limit := view.metadataResidency.work.limit
			want := errors.New("callback rollback")
			_, err := view.UpdateIssueRecord(ctx, workspace, issue.ID, &issue.Version, IssueMutationScope{Payload: domain.IssueUpdateInput{ProjectID: &issue.Project.ID}}, func(data *domain.Bootstrap, current *domain.Issue) error {
				current.Title = "Must roll back"
				switch mode {
				case "callback":
					return want
				case "cancel":
					cancel()
					return ctx.Err()
				case "quota", "root":
					view.metadataResidency.work.limit = view.metadataResidency.work.used
				}
				return nil
			})
			view.metadataResidency.work.limit = limit
			switch mode {
			case "callback":
				if !errors.Is(err, want) {
					t.Fatal(err)
				}
			case "cancel", "inline-cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			default:
				if !errors.Is(err, ErrMetadataWorkBudget) {
					t.Fatal("explicit admission error missing", err)
				}
			}
			stored, readErr := f.IssueRecord(t.Context(), workspace, issue.ID)
			if readErr != nil || stored.Title != issue.Title || stored.Version != issue.Version || !reflect.DeepEqual(prior, scaleP21IssuePhysical(t, f.SQLiteStore, workspace, "projects")) || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("rollback/cancel/admission altered canonical state or leaked", err, readErr)
			}
		})
	}
}

func TestScaleP21IssuePersistenceInlineDynamicWork(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	var physical []scaleP21IssuePhysicalField
	var native []any
	var peak int
	for _, bounded := range []bool{false, true} {
		projects := []domain.Project{{ID: "p21-project", Name: "First"}, {ID: "p21-project", Name: "Last"}, {ID: "duplicate", Name: "A"}, {ID: "duplicate", Name: "B"}}
		for index := range 80 {
			projects = append(projects, domain.Project{ID: fmt.Sprint("unrelated-", index), Name: "Native header"})
		}
		f, issue := scaleP21IssuePersistenceFixture(t, projects, nil)
		workspace := f.data.Workspace.URLKey
		repo := scaleP21IssuePersistenceView(t, f, bounded, false)
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if len(raw) > MetadataRecordPageMaxBytes {
			t.Fatal("fixture exceeded explicit inline root envelope")
		}
		ctx := WithSQLStatementObserver(t.Context(), func(query string) {
			if bounded && strings.HasPrefix(query, "UPDATE workspace_states SET workspace_id=") {
				peak = max(peak, repo.MetadataWorkUsedForTesting())
			}
		})
		scaleP21IssuePersistenceUpdate(t, repo, workspace, issue, false, ctx)
		physical = append(physical, scaleP21IssuePhysical(t, repo, workspace, "projects"))
		native = append(native, scaleP21IssueNative(t, repo, workspace, "projects"))
		if bounded && repo.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("dynamic inline work leaked")
		}
	}
	if !reflect.DeepEqual(physical[0], physical[1]) || !reflect.DeepEqual(native[0], native[1]) || peak <= metadataLogicalInlineWorkLimit {
		t.Fatal("dynamic inline physical/native equality or >2MiB ownership failed", peak)
	}
	t.Logf("inline_live_admission=%d", peak)
}

func TestScaleP21IssuePersistenceInlineNativeNoop(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	nativeAt := time.Now()
	var physical []scaleP21IssuePhysicalField
	var native []any
	for _, bounded := range []bool{false, true} {
		f, issue := scaleP21IssuePersistenceFixture(t, []domain.Project{
			{ID: "p21-project", Name: "First", CreatedAt: nativeAt},
			{ID: "p21-project", Name: "Last", CreatedAt: nativeAt, ProgressHistory: []domain.ProjectProgressHistoryPoint{{Date: utcDay(nativeAt), Value: 7}}},
		}, nil)
		workspace := f.data.Workspace.URLKey
		repo := scaleP21IssuePersistenceView(t, f, bounded, false)
		prior := scaleP21IssuePhysical(t, repo, workspace, "projects")
		_, err := repo.UpdateIssueRecord(t.Context(), workspace, issue.ID, &issue.Version, IssueMutationScope{Payload: domain.IssueUpdateInput{Title: new("Title only")}}, func(_ *domain.Bootstrap, current *domain.Issue) error {
			current.Title = "Title only"
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		state := scaleP21IssuePhysical(t, repo, workspace, "projects")
		values := scaleP21IssueNative(t, repo, workspace, "projects").([]domain.Project)
		// The inline duplicates have no rows to rebase onto, so neither copy
		// is dropped: the first takes the progress refresh like any project
		// and the second keeps its native values.
		if reflect.DeepEqual(prior, state) || state.Shape != "" || len(values) != 2 || values[0].CreatedAt != nativeAt ||
			len(values[0].IssueCountHistory) == 0 || len(values[1].ProgressHistory) != 1 {
			t.Fatal("literal inline duplicate/native-time contract changed", bounded, state.Shape, len(values))
		}
		physical = append(physical, state)
		native = append(native, values)
		if bounded && repo.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("inline no-write owner leaked")
		}
	}
	if !reflect.DeepEqual(physical[0], physical[1]) || !reflect.DeepEqual(native[0], native[1]) {
		t.Fatal("inline no-write physical/native snapshots differ")
	}
}

func TestScaleP21IssuePersistenceUnknownStamp(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f, issue := scaleP21IssuePersistenceFixture(t, []domain.Project{{ID: "p21-project", Name: "First"}, {ID: "p21-project", Name: "Last"}}, nil)
	workspace := f.data.Workspace.URLKey
	view := scaleP21IssuePersistenceView(t, f, true, false)
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := source.Transform(t.Context(), func(_ string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		return metadataAggregateStoredNode{node}, false, nil
	})
	source.Release()
	if err != nil {
		t.Fatal(err)
	}
	if err := unknown.setStamp(copyStamp{workspaceID: f.data.Workspace.ID, revision: -1}); err != nil {
		unknown.Release()
		t.Fatal(err)
	}
	view.installMetadataLogicalSource(workspace, unknown)
	scaleP21IssuePersistenceUpdate(t, view, workspace, issue, false)
	source, err = view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	if source.Stamp().revision != -1 || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("unknown source revision was promoted or owner leaked", source.Stamp())
	}
	physical := scaleP21IssuePhysical(t, f.SQLiteStore, workspace, "projects")
	native := scaleP21IssueNative(t, view, workspace, "projects").([]domain.Project)
	// The inline duplicates have no rows to rebase onto: both stay, in SQL
	// and in the warm native postimage, and the field stays inline.
	var stored []domain.Project
	if err := json.Unmarshal(physical.Root, &stored); err != nil {
		t.Fatal(err)
	}
	if physical.Shape != "" || len(physical.Rows) != 0 || len(stored) != 2 || len(native) != 2 ||
		stored[0].Name != native[0].Name || stored[1].Name != native[1].Name || len(native[0].IssueCountHistory) == 0 {
		t.Fatal("unknown-stamp transaction lost inline duplicates or diverged from SQL", physical, native)
	}
}

func TestScaleP21IssuePersistenceCanonicalPeerRebase(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	var physical []scaleP21IssuePhysicalField
	var native []any
	peerAt, err := time.Parse(time.RFC3339Nano, "2026-11-14T13:22:33.123456789+05:45")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("canonical peer body ", 150000)
	var peak int
	for _, bounded := range []bool{false, true} {
		f, issue := scaleP21IssuePersistenceFixture(t, []domain.Project{{ID: "p21-project", Name: "Selected"}}, nil)
		workspace := f.data.Workspace.URLKey
		repo := scaleP21IssuePersistenceView(t, f, bounded, false)
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key='p21-project'`, workspace).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var project domain.Project
		if err := json.Unmarshal(raw, &project); err != nil {
			t.Fatal(err)
		}
		project.Name, project.Description, project.CreatedAt = "Peer rename", body, peerAt
		raw, err = json.Marshal(project)
		if err != nil {
			t.Fatal(err)
		}
		// A supported SQL producer changes this canonical row while the
		// installed native generation remains the writer's old preimage.
		if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='projects' AND record_key='p21-project'`, raw, workspace); err != nil {
			t.Fatal(err)
		}
		ctx := WithSQLStatementObserver(t.Context(), func(string) {
			if bounded {
				peak = max(peak, repo.MetadataWorkUsedForTesting())
			}
		})
		scaleP21IssuePersistenceUpdate(t, repo, workspace, issue, false, ctx)
		physical = append(physical, scaleP21IssuePhysical(t, repo, workspace, "projects"))
		records := scaleP21IssueNative(t, repo, workspace, "projects").([]domain.Project)
		native = append(native, records)
		if len(records) != 1 || records[0].Name != project.Name || records[0].Description != body || !records[0].CreatedAt.Equal(peerAt) || len(records[0].ProgressHistory) == 0 {
			t.Fatal("canonical peer fields or progress lost")
		}
		if bounded {
			source, err := repo.retainMetadataLogicalSource(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			stamp := source.Stamp()
			source.Release()
			if stamp.revision != -1 || repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("peer-rebased source stamp/ownership changed", stamp)
			}
		}
	}
	if !reflect.DeepEqual(physical[0], physical[1]) || !reflect.DeepEqual(native[0], native[1]) || peak < len(body)*8 {
		t.Fatal("peer rebase physical/native equality or row admission differs", peak)
	}
	t.Logf("selected_canonical_bytes=%d live_admission=%d", len(body), peak)
}

func TestScaleP21IssuePersistenceMixedShapeBoundary(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f, issue := scaleP21IssuePersistenceFixture(t, []domain.Project{{ID: "p21-project", Name: "Selected"}}, nil)
	workspace := f.data.Workspace.URLKey
	var raw []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	root["projects"], _ = json.Marshal([]domain.Project{{ID: "inline-prefix", Name: "Mixed native prefix"}})
	raw, _ = json.Marshal(root)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, raw, workspace); err != nil {
		t.Fatal(err)
	}
	view := scaleP21IssuePersistenceView(t, f, true, true)
	prior := scaleP21IssuePhysical(t, f.SQLiteStore, workspace, "projects")
	_, err := view.UpdateIssueRecord(t.Context(), workspace, issue.ID, &issue.Version, IssueMutationScope{Payload: domain.IssueUpdateInput{ProjectID: &issue.Project.ID}}, func(_ *domain.Bootstrap, current *domain.Issue) error {
		current.Title = "Must roll back"
		return nil
	})
	stored, readErr := f.IssueRecord(t.Context(), workspace, issue.ID)
	if !errors.Is(err, ErrBoundedMetadataCoverage) || readErr != nil || stored.Title != issue.Title ||
		!reflect.DeepEqual(prior, scaleP21IssuePhysical(t, f.SQLiteStore, workspace, "projects")) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("mixed physical shape was silently accepted or mutated", err, readErr)
	}
}

func TestScaleP21IssuePersistenceCanonicalIdentityBoundary(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f, issue := scaleP21IssuePersistenceFixture(t, []domain.Project{{ID: "p21-project", Name: "Selected"}}, nil)
	workspace := f.data.Workspace.URLKey
	view := scaleP21IssuePersistenceView(t, f, true, false)
	var raw []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key='p21-project'`, workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var project domain.Project
	if err := json.Unmarshal(raw, &project); err != nil {
		t.Fatal(err)
	}
	project.ID = "different-native-id"
	raw, _ = json.Marshal(project)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='projects' AND record_key='p21-project'`, raw, workspace); err != nil {
		t.Fatal(err)
	}
	prior := scaleP21IssuePhysical(t, f.SQLiteStore, workspace, "projects")
	_, err := view.UpdateIssueRecord(t.Context(), workspace, issue.ID, &issue.Version, IssueMutationScope{Payload: domain.IssueUpdateInput{ProjectID: &issue.Project.ID}}, func(_ *domain.Bootstrap, current *domain.Issue) error {
		current.Title = "Must roll back"
		return nil
	})
	stored, readErr := f.IssueRecord(t.Context(), workspace, issue.ID)
	if !errors.Is(err, ErrBoundedMetadataCoverage) || readErr != nil || stored.Title != issue.Title ||
		!reflect.DeepEqual(prior, scaleP21IssuePhysical(t, f.SQLiteStore, workspace, "projects")) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("canonical identity mismatch was silently accepted or mutated", err, readErr)
	}
}
