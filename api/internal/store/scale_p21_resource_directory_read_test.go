package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func resourceDirectoryLiteral(data domain.Bootstrap, kind string) []any {
	items := []any{}
	switch kind {
	case "projects":
		for _, p := range data.Projects {
			p.Comments, p.CommentAttachments, p.DescriptionRevisions = nil, nil, nil
			p.IssueCountHistory, p.ScopeHistory, p.CompletedScopeHistory, p.InProgressScopeHistory, p.ProgressHistory = nil, nil, nil, nil, nil
			p.Milestones = slices.Clone(p.Milestones)
			for i := range p.Milestones {
				p.Milestones[i].DescriptionRevisions = nil
			}
			items = append(items, p)
		}
	case "projectMilestones":
		for _, p := range data.Projects {
			for _, m := range p.Milestones {
				m.DescriptionRevisions = nil
				items = append(items, m)
			}
		}
	case "initiatives":
		for _, i := range data.Initiatives {
			i.Comments, i.DescriptionHistory = nil, nil
			items = append(items, i)
		}
	case "labels":
		for _, l := range data.Labels {
			items = append(items, l)
		}
	case "documents":
		for _, d := range data.Documents {
			d.ContentState, d.ContentData, d.Revisions, d.Permissions = "", nil, nil, nil
			items = append(items, d)
		}
	}
	return items
}

func resourceDirectoryValue(v *ResourceDirectoryValue, kind string) any {
	switch kind {
	case "projects":
		return v.Project
	case "projectMilestones":
		return v.Milestone
	case "initiatives":
		return v.Initiative
	case "labels":
		return v.Label
	default:
		return v.Document
	}
}

func TestScaleP21ResourceDirectorySourcesAndOwnership(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("resource-directory-")
	at := time.Date(2025, 2, 3, 4, 5, 6, 7, time.FixedZone("native-directory", 3600))
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.resource_directory", "", nil, func(data *domain.Bootstrap) error {
		team := data.Teams[0].ID
		data.Projects = []domain.Project{
			{ID: prefix + "project", Name: "Native project", Description: "actual description", TeamIDs: []string{team}, Lead: &data.Viewer, LabelIDs: []string{prefix + "label", prefix + "label"}, IssueCount: 90, CreatedAt: at, UpdatedAt: at,
				Resources:            []domain.ProjectResource{{ID: "resource", Title: "Keep resource", URL: "https://example.test", PinnedTeamIDs: []string{team}}},
				Milestones:           []domain.ProjectMilestone{{ID: prefix + "milestone", ProjectID: prefix + "project", Name: "Native milestone", Description: "milestone description", CreatedAt: at, UpdatedAt: at, DescriptionRevisions: []domain.ProjectDescriptionRevision{{Description: "excluded milestone history"}}}},
				DescriptionRevisions: []domain.ProjectDescriptionRevision{{Description: "excluded history"}}, Comments: []domain.Comment{{Body: "excluded comment"}}},
			{ID: prefix + "empty", Name: "Native empty", Milestones: []domain.ProjectMilestone{}, CreatedAt: at},
		}
		data.Initiatives = []domain.Initiative{{ID: prefix + "initiative", Name: "Native initiative", Description: "initiative description", Creator: data.Viewer, Owner: &data.Viewer, ProjectIDs: []string{prefix + "project"}, LabelIDs: []string{prefix + "label"}, Resources: []domain.InitiativeResource{{ID: "initiative-resource", Title: "Keep"}}, CommentAttachments: []domain.Attachment{{ID: "keep-attachment"}}, Comments: []domain.Comment{{Body: "excluded"}}, DescriptionHistory: []domain.InitiativeDescriptionRevision{{Description: "excluded"}}, CreatedAt: at, UpdatedAt: at}}
		data.Labels = []domain.IssueLabel{{ID: prefix + "label", Name: "Native label", Description: "label description", Scope: team, GroupID: prefix + "group", IssueCount: 80, CreatedAt: at}}
		data.LabelGroups = []domain.LabelGroup{{ID: prefix + "group", Scope: team}}
		data.Documents = []domain.Document{{ID: prefix + "document", Title: "Native document", Content: "actual content", ContentState: "excluded state", ContentData: map[string]any{"native": int64(9)}, Creator: data.Viewer, TeamIDs: []string{team}, ProjectIDs: []string{prefix + "project"}, Attachments: []domain.Attachment{{ID: "document-attachment"}}, Revisions: []domain.DocumentRevision{{Content: "excluded"}}, CreatedAt: at, UpdatedAt: at}}
		data.Favorites = append(data.Favorites, domain.Favorite{ID: prefix + "favorite", UserID: data.Viewer.ID, ResourceType: "initiative", ResourceID: prefix + "initiative"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"warm", "reloaded", "unknown", "duplicates"} {
		t.Run(state, func(t *testing.T) {
			switch state {
			case "reloaded":
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			case "unknown":
				f.mu.Lock()
				f.forgetCopyRevisionLocked(workspace)
				f.mu.Unlock()
			case "duplicates":
				f.mu.Lock()
				data := f.workspaces[workspace]
				p, i, l, d := data.Projects[0], data.Initiatives[0], data.Labels[0], data.Documents[0]
				p.Name, p.Description, p.Milestones = "Last project name", "last project body", nil
				i.Name, i.Description = "Last initiative name", "last initiative body"
				l.Name, l.Description = "Last label name", "last label body"
				d.Title, d.Content = "Last document title", "last document body"
				data.Projects, data.Initiatives = append(slices.Clone(data.Projects), p), append(slices.Clone(data.Initiatives), i)
				data.Labels, data.Documents = append(slices.Clone(data.Labels), l), append(slices.Clone(data.Documents), d)
				f.workspaces[workspace] = data
				f.mu.Unlock()
			}
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			for _, principal := range []string{"", f.data.Viewer.ID} {
				var literal domain.Bootstrap
				if principal == "" {
					literal, _ = f.WorkspaceMetadata(workspace)
				} else {
					literal, err = f.PagedWorkspaceMetadata(t.Context(), workspace, principal)
					if err != nil {
						t.Fatal(err)
					}
				}
				for _, kind := range []string{"projects", "projectMilestones", "initiatives", "labels", "documents"} {
					read, err := view.BeginResourceDirectoryRead(t.Context(), workspace, principal, nil, kind, nil)
					if err != nil {
						t.Fatal(kind, err)
					}
					want, got := resourceDirectoryLiteral(literal, kind), []any{}
					base := view.MetadataWorkUsedForTesting()
					err = read.Visit(t.Context(), func(header *ResourceDirectoryValue) error {
						if kind == "projectMilestones" && header.Token.Child < 0 {
							return nil
						}
						if header.Project.Description != "" || header.Initiative.Description != "" || header.Document.Content != "" || header.Label.Description != "" || header.Milestone.Description != "" {
							t.Fatal("filter header retained output bodies")
						}
						value, err := read.Load(t.Context(), header.Token)
						if err != nil {
							return err
						}
						got = append(got, resourceDirectoryValue(value, kind))
						if view.MetadataWorkUsedForTesting() <= base {
							t.Fatal("output escaped admission")
						}
						value.Close()
						return nil
					})
					if err != nil || scaleP21DirectoryJSON(t, want) != scaleP21DirectoryJSON(t, got) {
						t.Fatalf("%s principal=%q state=%s differs: %v\nwant=%s\ngot=%s", kind, principal, state, err, scaleP21DirectoryJSON(t, want), scaleP21DirectoryJSON(t, got))
					}
					if view.MetadataWorkUsedForTesting() != base {
						t.Fatal("per-record owner did not release")
					}
					read.Close()
					if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
						t.Fatal("directory leaked or decoded full metadata")
					}
				}
			}
		})
	}
}

func TestScaleP21ResourceDirectoryBodyGrowthAndFailure(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	held := 0
	for _, size := range []int{8 << 10, 80 << 10} {
		if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.resource_directory_growth", "", nil, func(data *domain.Bootstrap) error {
			data.Projects = []domain.Project{{ID: "stable", Name: "Stable", Description: strings.Repeat("x", size), DescriptionRevisions: []domain.ProjectDescriptionRevision{{Description: strings.Repeat("y", size)}}, Milestones: []domain.ProjectMilestone{{ID: "stable-milestone", Description: strings.Repeat("m", size), DescriptionRevisions: []domain.ProjectDescriptionRevision{{Description: strings.Repeat("h", size)}}}}}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		read, err := view.BeginResourceDirectoryRead(t.Context(), workspace, f.data.Viewer.ID, nil, "projects", nil)
		if err != nil {
			t.Fatal(err)
		}
		base := view.MetadataWorkUsedForTesting()
		if held != 0 && held != base {
			t.Fatal("policy allocation grew with excluded bodies", held, base)
		}
		held = base
		token := ResourceDirectoryToken{Ordinal: 0, Child: -1}
		value, err := read.Load(t.Context(), token)
		if err != nil || len(value.Project.Description) != size || len(value.Project.Milestones[0].Description) != size || value.Project.DescriptionRevisions != nil || value.Project.Milestones[0].DescriptionRevisions != nil {
			t.Fatal("output projection differs", err)
		}
		t.Logf("body_bytes=%d policy_work=%d output_work=%d", size, base, view.MetadataWorkUsedForTesting()-base)
		value.Project.Milestones[0].Name = "changed owned value"
		value.Close()
		again, err := read.Load(t.Context(), token)
		if err != nil || again.Project.Milestones[0].Name != "" {
			t.Fatal("mutable output changed immutable source", err)
		}
		again.Close()
		limit := view.metadataResidency.work.limit
		view.metadataResidency.work.limit = base + 1
		if _, err := read.Load(t.Context(), token); !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != base {
			t.Fatal("failed output admission leaked", err)
		}
		view.metadataResidency.work.limit = limit
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := read.Load(cancelled, token); !errors.Is(err, context.Canceled) {
			t.Fatal("ignored cancellation", err)
		}
		value, err = read.Load(t.Context(), token)
		if err != nil {
			t.Fatal(err)
		}
		read.Close()
		if view.MetadataWorkUsedForTesting() == 0 || len(value.Project.Description) != size {
			t.Fatal("closing directory released live output")
		}
		value.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("output owner leaked")
		}
		view.releaseMetadataLogicalSources()
	}
}

func TestScaleP21ResourceDirectorySnapshotAuthorityAndAdmission(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, idgen.New("directory-snapshot-")
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.directory_snapshot", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = []domain.Project{{ID: target, Name: "Original", TeamIDs: []string{data.Teams[0].ID}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, release, err := f.BeginWorkspaceRead(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.directory_later", "", nil, func(data *domain.Bootstrap) error {
		data.Projects[0].Name = "Later"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	f.db.SetMaxOpenConns(1)
	read, err := view.BeginResourceDirectoryRead(ctx, workspace, f.data.Viewer.ID, nil, "projects", nil)
	if err != nil {
		t.Fatal(err)
	}
	token := ResourceDirectoryToken{Ordinal: 0, Child: -1}
	value, err := read.Load(ctx, token)
	if err != nil || value.Project.Name != "Original" {
		t.Fatal("borrowed directory escaped its caller snapshot", err)
	}
	value.Close()
	read.Close()
	var one int
	if err := ctx.Value(workspaceReadKey{}).(workspaceRead).reader.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatal("directory closed caller transaction", err)
	}
	release()
	f.db.SetMaxOpenConns(4)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := view.BeginResourceDirectoryRead(cancelled, workspace, f.data.Viewer.ID, nil, "projects", nil); !errors.Is(err, context.Canceled) || read != nil || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("cancelled constructor leaked work", err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	_, err = view.BeginResourceDirectoryRead(t.Context(), workspace, f.data.Viewer.ID, nil, "projects", nil)
	view.metadataResidency.work.limit = limit
	if !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("failed constructor leaked admission", err)
	}
	read, err = view.BeginResourceDirectoryRead(t.Context(), workspace, f.data.Viewer.ID, nil, "projects", nil)
	if err != nil {
		t.Fatal(err)
	}
	base := view.MetadataWorkUsedForTesting()
	selection, err := read.RetainSelection(t.Context(), 1024)
	if err != nil || view.MetadataWorkUsedForTesting() != base+1024 {
		t.Fatal("page key did not acquire independent admission", err)
	}
	selection()
	selection()
	if view.MetadataWorkUsedForTesting() != base {
		t.Fatal("displaced page key leaked admission")
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := read.Load(t.Context(), token); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("queued directory retained revoked authority", err)
	}
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("revoked directory leaked work")
	}
}

func TestScaleP21ResourceDirectoryFilterActorAndSearchOwnership(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.directory_search", "", nil, func(data *domain.Bootstrap) error {
		actor := data.Viewer
		actor.AppTeamIDs = []string{strings.Repeat("not-a-filter-field", 1000)}
		data.Documents = []domain.Document{{ID: "search-target", Title: "Actual title", Content: strings.Repeat("search body\n", 2000), Creator: actor, TeamIDs: []string{data.Teams[0].ID}, Revisions: []domain.DocumentRevision{{Content: strings.Repeat("excluded history", 3000)}}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	read, err := view.BeginResourceDirectoryRead(t.Context(), workspace, f.data.Viewer.ID, nil, "documents", nil)
	if err != nil {
		t.Fatal(err)
	}
	base := view.MetadataWorkUsedForTesting()
	token := ResourceDirectoryToken{Ordinal: 0, Child: -1}
	header, err := read.LoadHeader(t.Context(), token)
	if err != nil || header.Document.Title != "Actual title" || header.Document.Creator.ID != f.data.Viewer.ID || header.Document.Creator.AppTeamIDs != nil || header.Document.Content != "" {
		t.Fatal("filter header exposed marker/body/non-filter actor fields", err)
	}
	header.Close()
	search, err := read.SearchDocument(t.Context(), token)
	if err != nil || len(search.Document.Content) != 24000 || search.Document.Revisions != nil || view.MetadataWorkUsedForTesting() <= base+24000*32 {
		t.Fatal("search body or scratch escaped admission", err)
	}
	search.Close()
	if view.MetadataWorkUsedForTesting() != base {
		t.Fatal("search retained body work")
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = base + 50000
	_, err = read.SearchDocument(t.Context(), token)
	view.metadataResidency.work.limit = limit
	if !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != base {
		t.Fatal("failed search admission leaked work", err)
	}
	value, err := read.Load(t.Context(), token)
	if err != nil || len(value.Document.Creator.AppTeamIDs) != 1 {
		t.Fatal("complete output lost stored actor fields", err)
	}
	read.Close()
	if view.MetadataWorkUsedForTesting() == 0 || value.Document.Content == "" {
		t.Fatal("directory close released live search/output body")
	}
	value.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("search/output ownership leaked")
	}
}

func TestScaleP21ResourceDirectoryProjectedHandoff(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.directory_handoff", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = []domain.Project{{ID: "handoff-project", Name: "Installed name", TeamIDs: []string{data.Teams[0].ID}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	handed, err := f.PagedWorkspaceMetadata(t.Context(), workspace, f.data.Viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	handed.Projects[0].Name = "Exact handed name"
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	read, err := view.BeginResourceDirectoryRead(t.Context(), workspace, f.data.Viewer.ID, nil, "projects", &handed)
	if err != nil {
		t.Fatal(err)
	}
	value, err := read.Load(t.Context(), ResourceDirectoryToken{Ordinal: 0, Child: -1})
	if err != nil || value.Project.Name != "Exact handed name" {
		t.Fatal("handoff was replaced or reprojected from installed records", err)
	}
	value.Close()
	read.Close()
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	read, err = view.BeginResourceDirectoryRead(t.Context(), workspace, f.data.Viewer.ID, nil, "projects", &handed)
	if !errors.Is(err, ErrAuthForbidden) || read != nil || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("projected handoff bypassed live membership or leaked work", err)
	}
}

func TestScaleP21ResourceDirectoryTrustedSource(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.directory_trusted", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = []domain.Project{{ID: "trusted-project", Name: "Exact installed name"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	statements := 0
	ctx := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
	read, err := view.BeginResourceDirectoryRead(ctx, workspace, "", nil, "projects", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.directory_trusted_later", "", nil, func(data *domain.Bootstrap) error {
		data.Projects[0].Name = "Later generation"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	value, err := read.Load(ctx, ResourceDirectoryToken{Ordinal: 0, Child: -1})
	if err != nil || value.Project.Name != "Exact installed name" || statements != 0 {
		t.Fatal("trusted source performed SQL or refreshed its captured generation", statements, err)
	}
	value.Close()
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("trusted source leaked or decoded full metadata")
	}
}

func TestScaleP21ResourceDirectoryMemoSourcePrecedence(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	for _, source := range []string{"installed", "event"} {
		t.Run(source, func(t *testing.T) {
			tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			owner := &ResourceRead{store: view}
			defer owner.Close()
			ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
			ctx = owner.metadataOutputContext(ctx)
			ctx, closeMemo := withResourcePolicyMemo(ctx, owner)
			defer closeMemo()
			canonicalReads, principals := 0, 0
			ctx = WithSQLStatementObserver(ctx, func(query string) {
				if strings.Contains(query, "field='teams'") && strings.Contains(query, ">191 LIMIT 1") {
					canonicalReads++
				}
				if strings.HasPrefix(query, "SELECT u.id,u.email") && strings.Contains(query, "LEFT JOIN team_memberships") {
					principals++
				}
			})
			before, err := view.ResolveAccess(ctx, workspace, f.data.Viewer.ID, nil)
			if err != nil || len(before.policy.metadata.Teams) == 0 || canonicalReads != 1 {
				t.Fatal("first resolution did not read canonical policy exactly once", canonicalReads, err)
			}
			canonicalKey := before.policy.metadata.Teams[0].Key
			again, err := view.ResolveAccess(ctx, workspace, f.data.Viewer.ID, nil)
			if err != nil || canonicalReads != 1 || principals != 2 || again.policy.metadata.Teams[0].Key != canonicalKey {
				t.Fatal("same-operation canonical reuse skipped authority or reread policy", canonicalReads, principals, err)
			}
			view.mu.Lock()
			original := view.workspaces[workspace]
			replacement := resourcePolicyMetadata(before.policy.metadata)
			replacement.Teams[0].Key = "new-certified-installed-policy"
			view.workspaces[workspace] = replacement
			view.setCopyRevisionLocked(workspace, before.policy.stamp)
			view.mu.Unlock()
			defer func() {
				view.mu.Lock()
				view.workspaces[workspace] = original
				view.forgetCopyRevisionLocked(workspace)
				view.mu.Unlock()
			}()
			want := replacement.Teams[0].Key
			if source == "event" {
				event := resourcePolicyMetadata(before.policy.metadata)
				event.Teams[0].Key = "new-event-policy"
				ctx = context.WithValue(ctx, eventPolicySourceKey{}, &EventPolicySource{
					store: view, workspace: workspace,
					version:  EventSourceVersion{WorkspaceID: before.policy.stamp.workspaceID, Metadata: before.policy.stamp.revision},
					metadata: event,
				})
				want = event.Teams[0].Key
			}
			after, err := view.ResolveAccess(ctx, workspace, f.data.Viewer.ID, nil)
			if err != nil || after.policy.metadata.Teams[0].Key != want || canonicalReads != 1 || principals != 3 {
				t.Fatal("memo overrode the higher-priority source or skipped principal query", source, canonicalReads, principals, err)
			}
			closeMemo()
			owner.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("source precedence test retained memo work")
			}
		})
	}
}

func TestScaleP21ResourceDirectoryMemoAuthorityBoundaries(t *testing.T) {
	for _, changed := range []string{"credential-scope", "credential-revocation", "principal-status", "epoch"} {
		t.Run(changed, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", "off")
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			t.Setenv("FLOW_RECORD_JOURNAL", "on")
			f := scaleP09Database(t)
			workspace := f.data.Workspace.URLKey
			key := &domain.APIKey{ID: "memo-credential", CreatorID: f.data.Viewer.ID, TeamRestriction: "selected", TeamIDs: []string{f.data.Teams[0].ID}, Scopes: []string{"read"}}
			if changed == "credential-revocation" {
				if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "apiKeys"), workspace, "api_key.created", key.ID, nil, func(data *domain.Bootstrap) error {
					data.APIKeys = append(data.APIKeys, *key)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			principals, canonicalReads := 0, 0
			var injected error
			ctx := WithSQLStatementObserver(t.Context(), func(query string) {
				if strings.Contains(query, "field='teams'") && strings.Contains(query, ">191 LIMIT 1") {
					canonicalReads++
				}
				if !strings.HasPrefix(query, "SELECT u.id,u.email") || !strings.Contains(query, "LEFT JOIN team_memberships") {
					return
				}
				principals++
				if principals != 2 {
					return
				}
				switch changed {
				case "credential-scope":
					key.TeamIDs = []string{}
				case "credential-revocation":
					injected = f.MutateWorkspace(WithMetadataFields(t.Context(), "apiKeys"), workspace, "api_key.revoked", key.ID, nil, func(data *domain.Bootstrap) error {
						for index := range data.APIKeys {
							if data.APIKeys[index].ID == key.ID {
								now := time.Now()
								data.APIKeys[index].RevokedAt = &now
							}
						}
						return nil
					})
				case "principal-status":
					_, injected = f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID)
				case "epoch":
					_, injected = f.db.ExecContext(t.Context(), `UPDATE workspace_authz_epochs SET epoch=epoch+1 WHERE workspace_id=?`, f.data.Workspace.ID)
				}
			})
			read, err := view.BeginResourceDirectoryRead(ctx, workspace, f.data.Viewer.ID, key, "projects", nil)
			if read != nil {
				read.Close()
			}
			if injected != nil || !errors.Is(err, ErrAuthForbidden) || principals != 2 || canonicalReads != 1 {
				t.Fatalf("%s escaped authority boundaries: principal_queries=%d canonical_reads=%d injected=%v err=%v", changed, principals, canonicalReads, injected, err)
			}
			if changed == "credential-scope" && !errors.Is(err, ErrResourceAccessChanged) {
				t.Fatal("effective credential change did not retain scope-mismatch error", err)
			}
			if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("failed memo-backed constructor leaked ownership or decoded full metadata")
			}
		})
	}
}
