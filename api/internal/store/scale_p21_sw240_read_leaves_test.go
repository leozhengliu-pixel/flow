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

func scaleP21SW240SharedProjection(data domain.Bootstrap, token string) any {
	for _, item := range data.Issues {
		if item.ShareToken != token {
			continue
		}
		item.ShareToken, item.SharedAt, item.Team, item.Assignee, item.Delegate = "", nil, domain.Team{}, nil, nil
		item.Creator.Email = ""
		comments := slices.Clone(data.Comments[item.ID])
		if comments == nil {
			comments = []domain.Comment{}
		}
		for index := range comments {
			comments[index].User.Email = ""
		}
		return map[string]any{"issue": item, "comments": comments, "workspace": data.Workspace}
	}
	return nil
}

func scaleP21SW240ReadProjection(read *SharedIssueRead) any {
	if !read.Found {
		return nil
	}
	return scaleP21SW240SharedProjection(domain.Bootstrap{
		Workspace: read.Workspace, Issues: []domain.Issue{read.Issue},
		Comments: map[string][]domain.Comment{read.Issue.ID: read.Comments},
	}, read.Issue.ShareToken)
}

func TestScaleP21SW240SharedIssueSourcesAndLargeOwnership(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, native := range []bool{false, true} {
		for _, reload := range []bool{false, true} {
			t.Run(fmt.Sprintf("native=%t/reload=%t", native, reload), func(t *testing.T) {
				f, issue := scaleP21IssuePersistenceFixture(t, nil, nil)
				workspace := f.data.Workspace.URLKey
				at := time.Date(2026, 11, 16, 8, 9, 10, 11, time.UTC)
				scaleP21SW63Seed(t, f, issue, at)
				token := "sw240-public-token"
				if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw240_seed", "", nil, func(data *domain.Bootstrap) error {
					label := domain.IssueLabel{ID: "sw240-label", Name: "First label"}
					data.Labels = append(data.Labels, label)
					label.Name = "Last label"
					data.Labels = append(data.Labels, label)
					for index := range data.Issues {
						if data.Issues[index].ID == issue.ID {
							data.Issues[index].ShareToken, data.Issues[index].SharedAt = token, &at
							data.Issues[index].Description = strings.Repeat("complete public body ", 200000)
							data.Issues[index].Labels = []domain.IssueLabel{{ID: label.ID}, {ID: label.ID}}
							data.Issues[index].Assignee, data.Issues[index].Delegate = &data.Viewer, &data.Viewer
						}
					}
					other := issue
					other.ID, other.Identifier, other.ShareToken = issue.ID+"-second", "P21-200", token
					other.Labels = []domain.IssueLabel{{ID: label.ID}}
					data.Issues = append(data.Issues, other)
					data.Projects = append(data.Projects, domain.Project{ID: "sw240-count-project", Name: "Counted project", LabelIDs: []string{label.ID, label.ID}})
					data.Initiatives = append(data.Initiatives, domain.Initiative{ID: "sw240-count-initiative", Name: "Counted initiative", LabelIDs: []string{label.ID, label.ID}})
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if reload {
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				if native {
					data, found := f.BootstrapFor(workspace)
					if !found {
						t.Fatal("literal native source")
					}
					var first domain.Issue
					for _, item := range data.Issues {
						if item.ID == issue.ID {
							first = item
						}
					}
					first.ID, first.Title, first.ShareToken = "sw240-native-first", "Native first occurrence", token
					data.Issues = append([]domain.Issue{first}, data.Issues...)
					data.Comments[first.ID] = []domain.Comment{
						{ID: "", Body: "Native empty-ID comment", User: data.Viewer},
						{ID: "", Body: "Native duplicate-ID comment", User: data.Viewer},
					}
					f.mu.Lock()
					f.setWorkspaceLocked(workspace, data)
					f.mu.Unlock()
				}
				view := scaleP21IssuePersistenceView(t, f, true, false)
				literal, found := f.BootstrapFor(workspace)
				if !found {
					t.Fatal("literal shared source")
				}
				read, err := view.BeginSharedIssueRead(t.Context(), workspace, token)
				if err != nil || read == nil || !read.Found {
					t.Fatal("bounded shared detail", err)
				}
				expected, err := json.Marshal(scaleP21SW240SharedProjection(literal, token))
				if err != nil {
					t.Fatal(err)
				}
				got, err := json.Marshal(scaleP21SW240ReadProjection(read))
				if err != nil || string(expected) != string(got) {
					t.Fatal("complete shared detail differs", err)
				}
				if view.MetadataWorkUsedForTesting() == 0 {
					t.Fatal("large shared output escaped its owner")
				}
				read.Comments[0].Body = "Caller-owned edit"
				read.Close()
				read.Close()
				if !reflect.DeepEqual(read.Issue, domain.Issue{}) || read.Comments != nil ||
					view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("shared output close retained data/work or decoded metadata")
				}
				again, err := view.BeginSharedIssueRead(t.Context(), workspace, token)
				if err != nil || again.Comments[0].Body == "Caller-owned edit" {
					t.Fatal("shared output aliased its installed input", err)
				}
				again.Close()
				for _, missing := range []string{"unknown-token", token + " "} {
					read, err := view.BeginSharedIssueRead(t.Context(), workspace, missing)
					if err != nil || read.Found {
						t.Fatal("token first-match/equality changed", missing, err)
					}
					read.Close()
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if read, err := view.BeginSharedIssueRead(ctx, workspace, token); !errors.Is(err, context.Canceled) || read != nil {
					t.Fatal("shared cancellation", err)
				}
				limit := view.metadataResidency.work.limit
				view.metadataResidency.work.limit = 1
				_, err = view.BeginSharedIssueRead(t.Context(), workspace, token)
				view.metadataResidency.work.limit = limit
				if !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("shared admission failure leaked", err)
				}
			})
		}
	}
}

func TestScaleP21SW240FacetVisitorCancellationAndRelease(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f, _ := scaleP21IssuePersistenceFixture(t, nil, nil)
	workspace := f.data.Workspace.URLKey
	view := scaleP21IssuePersistenceView(t, f, true, false)
	for _, cancelDuring := range []bool{false, true} {
		read, err := view.BeginIssueFacetRead(t.Context(), workspace, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		if !cancelDuring {
			cancel()
		}
		visits := 0
		err = read.Visit(ctx, func(_, _, _ string) error {
			visits++
			if view.MetadataWorkUsedForTesting() == 0 {
				t.Fatal("facet visited without ownership")
			}
			cancel()
			return ctx.Err()
		})
		cancel()
		if !errors.Is(err, context.Canceled) || cancelDuring && visits != 1 || !cancelDuring && visits != 0 {
			t.Fatal("facet cancellation boundary", err, visits)
		}
		read.Close()
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("facet close leaked work or hydrated full metadata")
		}
	}
}

func TestScaleP21SW240SharedIssueCancellationStages(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f, issue := scaleP21IssuePersistenceFixture(t, nil, nil)
	workspace := f.data.Workspace.URLKey
	scaleP21SW63Seed(t, f, issue, time.Date(2026, 11, 16, 1, 2, 3, 4, time.UTC))
	token := "sw240-cancel-token"
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw240_cancel", "", nil, func(data *domain.Bootstrap) error {
		for index := range data.Issues {
			if data.Issues[index].ID == issue.ID {
				data.Issues[index].ShareToken = token
				data.Issues[index].Labels = []domain.IssueLabel{{ID: "sw240-count-label"}}
			}
		}
		data.Labels = append(data.Labels, domain.IssueLabel{ID: "sw240-count-label", Name: "Selected count"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view := scaleP21IssuePersistenceView(t, f, true, false)
	for _, stage := range []string{"issue", "comment"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			hit := false
			ctx = WithSQLStatementObserver(ctx, func(statement string) {
				match := stage == "issue" && strings.Contains(statement, " AS row_bytes FROM issue_records") ||
					stage == "comment" && strings.Contains(statement, " AS row_bytes FROM workspace_content_records")
				if match && !hit {
					hit = true
					cancel()
				}
			})
			read, err := view.BeginSharedIssueRead(ctx, workspace, token)
			if !hit || !errors.Is(err, context.Canceled) || read != nil || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("shared staged cancellation changed output or leaked ownership", stage, hit, err)
			}
		})
	}
}

func scaleP21SW240FacetOccurrences(data domain.Bootstrap) [][3]string {
	result := [][3]string{}
	add := func(field, id, label string) { result = append(result, [3]string{field, id, label}) }
	for _, issue := range data.Issues {
		add("team", issue.Team.ID, issue.Team.Name)
		add("status", issue.State.ID, issue.State.Name)
		if issue.Assignee != nil {
			add("assignee", issue.Assignee.ID, issue.Assignee.DisplayName)
		}
		for _, label := range issue.Labels {
			add("label", label.ID, label.Name)
		}
		if issue.Project != nil {
			add("project", issue.Project.ID, issue.Project.Name)
		}
		if issue.CycleID != nil {
			for _, cycle := range data.Cycles {
				if cycle.ID == *issue.CycleID {
					add("cycle", cycle.ID, cycle.Name)
					break
				}
			}
		}
	}
	return result
}

func TestScaleP21SW240FacetNativeFirstParentPermissions(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21IssueAuthorizationSeed(t)
	workspace := f.data.Workspace.URLKey
	data, found := f.BootstrapFor(workspace)
	if !found {
		t.Fatal("native fixture")
	}
	parent := domain.Issue{
		ID: "sw240-duplicate-parent", Team: domain.Team{ID: f.private},
		Permissions: []domain.IssuePermission{{SubjectType: "user", SubjectID: f.data.Viewer.ID, Role: "viewer"}},
	}
	later := parent
	later.Permissions = []domain.IssuePermission{{SubjectType: "user", SubjectID: f.guest.ID, Role: "viewer"}}
	child := domain.Issue{ID: "sw240-first-parent-child", Team: domain.Team{ID: f.private}, ParentID: &parent.ID}
	data.Issues = append([]domain.Issue{parent, later, child}, data.Issues...)
	f.mu.Lock()
	f.setWorkspaceLocked(workspace, data)
	f.mu.Unlock()
	view := scaleP21IssuePersistenceView(t, f.scaleP09Fixture, true, false)
	for _, user := range []domain.User{f.data.Viewer, f.guest} {
		literal, found, err := f.BootstrapForUser(t.Context(), workspace, user.ID)
		if err != nil || !found {
			t.Fatal("literal native authority", err)
		}
		read, err := view.BeginIssueFacetRead(t.Context(), workspace, user.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		var got [][3]string
		err = read.Visit(t.Context(), func(field, value, label string) error {
			got = append(got, [3]string{field, value, label})
			return nil
		})
		if err != nil || !reflect.DeepEqual(scaleP21SW240FacetOccurrences(literal), got) {
			t.Fatal("native first-parent authority or facet order differs", user.ID, err)
		}
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("native facet read leaked")
		}
	}
}

func TestScaleP21SW240FacetNativeAuthorityLargePopulation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	data, found := f.BootstrapFor(workspace)
	if !found {
		t.Fatal("native population fixture")
	}
	team := domain.Team{ID: "sw240-native-population-team", Key: "POP", Name: "Population team"}
	data.Teams = append(data.Teams, team)
	const population = 1501
	data.Issues = make([]domain.Issue, population)
	for index := range data.Issues {
		data.Issues[index] = domain.Issue{
			ID: fmt.Sprintf("sw240-native-population-%04d", index), Team: team,
			State: domain.WorkflowState{ID: "sw240-native-state", Name: "Population state"}, Creator: data.Viewer,
			Description: strings.Repeat("body excluded from authority ", 30),
		}
		if index > 0 {
			data.Issues[index].ParentID = &data.Issues[index-1].ID
		}
	}
	f.mu.Lock()
	f.setWorkspaceLocked(workspace, data)
	f.mu.Unlock()
	view := scaleP21IssuePersistenceView(t, f, true, false)
	// Fixed 128 KiB retained for every row would exceed this after 128 rows.
	// Compact authority must admit the whole population under this limit.
	view.metadataResidency.work.limit = 16 << 20
	literal, found, err := f.BootstrapForUser(t.Context(), workspace, f.data.Viewer.ID)
	if err != nil || !found {
		t.Fatal("literal large native population", err)
	}
	read, err := view.BeginIssueFacetRead(t.Context(), workspace, f.data.Viewer.ID, nil)
	if err != nil {
		t.Fatal("compact native authority rejected legal population", err)
	}
	peak := view.MetadataWorkUsedForTesting()
	var got [][3]string
	err = read.Visit(t.Context(), func(field, id, label string) error {
		peak = max(peak, view.MetadataWorkUsedForTesting())
		got = append(got, [3]string{field, id, label})
		return nil
	})
	if err != nil || !reflect.DeepEqual(got, scaleP21SW240FacetOccurrences(literal)) {
		t.Fatal("large native facet output/order differs", err, len(got))
	}
	if len(read.visible) != population || read.sqlIssues || peak >= 16<<20 {
		t.Fatal("native authority retained whole rows or fixed per-row scratch", len(read.visible), read.sqlIssues, peak)
	}
	t.Logf("native_authority_rows=%d peak_admitted_bytes=%d limit_bytes=%d", population, peak, 16<<20)
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("large native facet owner leaked")
	}
}

func scaleP21SW240FacetVisit(t *testing.T, view *SQLiteStore, workspace, userID string, native bool) [][3]string {
	t.Helper()
	read, err := view.BeginIssueFacetRead(t.Context(), workspace, userID, nil)
	if err != nil {
		t.Fatal("bounded facet read", userID, err)
	}
	defer read.Close()
	if read.sqlIssues == native {
		t.Fatal("facet read did not use the intended issue source", native)
	}
	got := [][3]string{}
	if err := read.Visit(t.Context(), func(field, value, label string) error {
		if view.MetadataWorkUsedForTesting() == 0 {
			t.Fatal("facet visited without ownership")
		}
		got = append(got, [3]string{field, value, label})
		return nil
	}); err != nil {
		t.Fatal("bounded facet visit", userID, err)
	}
	return got
}

// The literal projection judges issues inside slices.DeleteFunc, so a parent
// that was already removed and overwritten is no longer found by a later
// child. Both layouts below must match the literal oracle, including the one
// where an archived-team parent's grant is lost.
func TestScaleP21SW240FacetDeleteFuncOrderOrphansAndLabelNames(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprintf("native=%t", native), func(t *testing.T) {
			f := scaleP21IssueAuthorizationSeed(t)
			workspace := f.data.Workspace.URLKey
			archived := ""
			data, found := f.BootstrapFor(workspace)
			if !found {
				t.Fatal("fixture")
			}
			for _, team := range data.Teams {
				if team.ArchivedAt != nil {
					archived = team.ID
				}
			}
			if archived == "" {
				t.Fatal("archived team fixture")
			}
			at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
			if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw240_order_seed", "", nil, func(data *domain.Bootstrap) error {
				data.Labels = append(data.Labels,
					domain.IssueLabel{ID: "sw240-dup-label", Name: "Visible duplicate", Scope: " Workspace "},
					domain.IssueLabel{ID: "sw240-dup-label", Name: "Hidden duplicate", Scope: f.private},
					domain.IssueLabel{ID: "sw240-private-label", Name: "Private label", Scope: f.private},
					domain.IssueLabel{ID: "sw240-public-label", Name: "Public label", Scope: f.public},
				)
				data.Projects = append(data.Projects,
					domain.Project{ID: "sw240-private-project", Name: "Private project", TeamIDs: []string{f.private}},
					domain.Project{ID: "sw240-open-project", Name: "Open project first"},
					domain.Project{ID: "sw240-open-project", Name: "Open project last", TeamIDs: []string{f.private}},
				)
				data.Cycles = append(data.Cycles,
					domain.Cycle{ID: "sw240-cycle", Name: "Private cycle", TeamID: f.private},
					domain.Cycle{ID: "sw240-cycle", Name: "Public cycle", TeamID: f.public},
				)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			labels := []domain.IssueLabel{
				{ID: "sw240-dup-label", Name: "Embedded duplicate"}, {ID: "sw240-private-label"},
				{ID: "sw240-unknown-label", Name: "Embedded unknown"}, {ID: "sw240-public-label"}, {ID: "sw240-dup-label"},
			}
			cycle := "sw240-cycle"
			issue := func(id, team string, parent *string, grant bool) domain.Issue {
				item := domain.Issue{
					ID: id, Identifier: strings.ToUpper(id), Version: 1, Title: id, Team: domain.Team{ID: team, Name: "Embedded " + team},
					State: domain.WorkflowState{ID: "sw240-state", Name: "Embedded state"}, Creator: f.data.Viewer,
					Assignee: &domain.User{ID: f.guest.ID, DisplayName: "Embedded assignee"}, Labels: labels,
					Project: &domain.ProjectSummary{ID: "sw240-private-project"}, CycleID: &cycle, ParentID: parent, CreatedAt: at, UpdatedAt: at,
				}
				if grant {
					item.Permissions = []domain.IssuePermission{
						{SubjectType: "user", SubjectID: f.guest.ID, Role: "none"},
						{SubjectType: "team", SubjectID: f.public, Role: "viewer"},
					}
				}
				return item
			}
			for offset, layout := range []struct {
				name  string
				order []string
			}{
				{"parent-lost", []string{"a", "p", "b", "c", "o", "z"}},
				{"parent-found", []string{"p", "c", "a", "b", "o", "z"}},
			} {
				t.Run(layout.name, func(t *testing.T) {
					prefix := "sw240-" + layout.name + "-"
					parent := prefix + "p"
					all := map[string]domain.Issue{
						"a": issue(prefix+"a", f.public, nil, false),
						"p": issue(parent, archived, nil, true),
						"b": issue(prefix+"b", f.public, nil, false),
						"c": issue(prefix+"c", f.private, &parent, false),
						"o": issue(prefix+"o", "sw240-orphan-team", nil, false),
						"z": issue(prefix+"z", f.public, new(""), false),
					}
					issues := []domain.Issue{}
					for _, key := range layout.order {
						issues = append(issues, all[key])
					}
					if err := f.ImportIssues(t.Context(), workspace, issues); err != nil {
						t.Fatal(err)
					}
					for index, item := range issues {
						if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET collection_order=? WHERE workspace_key=? AND id=?`, offset*100-200+index, workspace, item.ID); err != nil {
							t.Fatal(err)
						}
					}
					if native {
						f.mu.Lock()
						f.forgetCopyRevisionLocked(workspace)
						f.mu.Unlock()
						if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
						data, found := f.BootstrapFor(workspace)
						if !found {
							t.Fatal("native fixture")
						}
						f.mu.Lock()
						f.setWorkspaceLocked(workspace, data)
						f.mu.Unlock()
					}
					view := scaleP21IssuePersistenceView(t, f.scaleP09Fixture, true, !native)
					visible := map[string]bool{}
					for _, user := range []string{"", f.data.Viewer.ID, f.guest.ID} {
						var literal domain.Bootstrap
						if user == "" {
							literal, found = f.BootstrapFor(workspace)
						} else {
							var err error
							literal, found, err = f.BootstrapForUser(t.Context(), workspace, user)
							if err != nil {
								t.Fatal(err)
							}
						}
						if !found {
							t.Fatal("literal oracle", user)
						}
						if user == f.guest.ID {
							for _, item := range literal.Issues {
								visible[item.ID] = true
							}
						}
						expected := scaleP21SW240FacetOccurrences(literal)
						if got := scaleP21SW240FacetVisit(t, view, workspace, user, native); !reflect.DeepEqual(expected, got) {
							t.Fatalf("facet occurrences differ for %q:\nliteral=%v\nbounded=%v", user, expected, got)
						}
						if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
							t.Fatal("facet read leaked work or decoded the metadata graph")
						}
					}
					if visible[prefix+"c"] != (layout.name == "parent-found") || !visible[prefix+"a"] || visible[parent] || visible[prefix+"o"] {
						t.Fatal("fixture did not exercise the in-place parent lookup", visible)
					}
				})
			}
		})
	}
}
