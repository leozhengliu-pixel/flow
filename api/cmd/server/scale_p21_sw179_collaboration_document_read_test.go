package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW179Result struct {
	Error    string
	Found    bool
	Conflict bool
	Role     string
	Document domain.Document
}

func scaleP21SW179Error(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, store.ErrAuthForbidden):
		return "forbidden"
	case errors.Is(err, store.ErrMetadataWorkBudget):
		return "work-budget"
	default:
		return err.Error()
	}
}

func scaleP21SW179Header(document domain.Document) domain.Document {
	header := domain.Document{
		ID: document.ID, SlugID: document.SlugID, CollaborationID: document.CollaborationID,
		Creator: domain.User{ID: document.Creator.ID}, TeamIDs: slices.Clone(document.TeamIDs),
		InheritedTeamIDs: slices.Clone(document.InheritedTeamIDs),
	}
	if document.Permissions != nil {
		header.Permissions = make([]domain.DocumentPermission, len(document.Permissions))
		for index, permission := range document.Permissions {
			header.Permissions[index] = domain.DocumentPermission{
				SubjectType: permission.SubjectType, SubjectID: permission.SubjectID, Role: permission.Role,
			}
		}
	}
	return header
}

func scaleP21SW179Legacy(t *testing.T, f scaleP21M2Fixture, ctx context.Context, workspace, userID, ref string, key *domain.APIKey) scaleP21SW179Result {
	t.Helper()
	var data domain.Bootstrap
	var err error
	if userID == "" {
		var found bool
		data, found = f.repository.WorkspaceMetadata(workspace)
		if !found {
			err = store.ErrAuthForbidden
		}
	} else {
		data, err = f.repository.PagedWorkspaceMetadata(ctx, workspace, userID)
		if err == nil {
			request := f.literalRequest(domain.User{ID: userID}, key, nil)
			credentialContext := ctx
			if key != nil {
				credentialContext = context.WithValue(ctx, apiKeyContextKey{}, *key)
			}
			filterBootstrapForAPIKey(&data, request.WithContext(credentialContext))
		}
	}
	result := scaleP21SW179Result{Error: scaleP21SW179Error(err)}
	if err != nil {
		return result
	}
	document, conflict, err := collaborationDocument(data, ref)
	if errors.Is(err, errNotFound) {
		return result
	}
	if err != nil {
		t.Fatal(err)
	}
	result.Found, result.Conflict = true, conflict
	result.Document = scaleP21SW179Header(document)
	result.Role = documentRole(&server{store: f.repository, authDisabled: userID == ""}, data, document)
	return result
}

func scaleP21SW179Pair(t *testing.T, f scaleP21M2Fixture, ctx context.Context, workspace, userID, ref string, key *domain.APIKey) scaleP21SW179Result {
	t.Helper()
	want := scaleP21SW179Legacy(t, f, ctx, workspace, userID, ref, key)
	read, err := f.probe.BeginCollaborationDocumentRead(ctx, workspace, userID, ref, key)
	got := scaleP21SW179Result{Error: scaleP21SW179Error(err)}
	if err != nil {
		if read != nil || f.probe.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("refused collaboration read retained partial headers/work", err)
		}
	} else {
		defer read.Close()
		if read == nil || f.probe.MetadataWorkUsedForTesting() == 0 {
			t.Fatal("successful collaboration read lacks its independent owner")
		}
		if _, err := read.Context(ctx); err != nil {
			t.Fatal("open collaboration context refused", err)
		}
		if len(read.Data.Documents) > 1 || read.Data.Users != nil || read.Data.Projects != nil ||
			read.Data.Issues != nil || read.Data.Comments != nil || read.Data.Activities != nil {
			t.Fatal("collaboration selector retained an unrelated directory/content collection")
		}
		got.Found, got.Conflict = read.Found, read.Conflict
		if read.Found {
			if len(read.Data.Documents) != 1 || !reflect.DeepEqual(read.Data.Documents[0], read.Document) {
				t.Fatal("selected document and role input disagree")
			}
			if !reflect.DeepEqual(read.Document, scaleP21SW179Header(read.Document)) {
				t.Fatal("collaboration reader materialized undeclared document fields")
			}
			got.Document = scaleP21SW179Header(read.Document)
			got.Role = documentRole(&server{store: f.probe, authDisabled: userID == ""}, read.Data, read.Document)
		} else if len(read.Data.Documents) != 0 || !reflect.DeepEqual(read.Document, domain.Document{}) {
			t.Fatal("absent collaboration target returned a document")
		}
		read.Close()
		read.Close()
		if !reflect.DeepEqual(read.Data, domain.Bootstrap{}) || !reflect.DeepEqual(read.Document, domain.Document{}) ||
			f.probe.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("closed collaboration read retained headers/work")
		}
		if _, err := read.Context(ctx); err == nil {
			t.Fatal("closed collaboration read supplied a context")
		}
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("collaboration selector/role differential ref=%q user=%q:\nlegacy=%#v\nbounded=%#v", ref, userID, want, got)
	}
	if f.probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("collaboration reader decoded the complete workspace")
	}
	return got
}

func scaleP21SW179Setup(t *testing.T) (scaleP21SW10Fixture, string, []string) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21SW10Setup(t)
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw179-")
	if err := f.repository.MutateWorkspace(t.Context(), workspace, "scale_p21.sw179_seed", "", nil, func(data *domain.Bootstrap) error {
		private, child := "", ""
		for _, project := range data.Projects {
			if project.ID == "m2-unrelated" && len(project.TeamIDs) != 0 {
				private = project.TeamIDs[0]
			}
		}
		for id, settings := range data.TeamSettings {
			if settings.ParentTeamID == private {
				child = id
			}
		}
		if private == "" || child == "" {
			t.Fatal("private/inherited collaboration fixture teams missing")
		}
		creator, guest := f.principals[1].user, f.principals[3].user
		data.Projects = append(data.Projects, domain.Project{ID: prefix + "child-project", TeamIDs: []string{child}})
		documents := []domain.Document{
			{ID: prefix + "legacy", SlugID: prefix + "legacy-slug", Creator: creator},
			{ID: prefix + "rotated", SlugID: prefix + "current", PreviousSlugIDs: []string{prefix + "old"},
				CollaborationID: prefix + "generation", Creator: creator, TeamIDs: []string{f.public}},
			{ID: prefix + "collision", SlugID: prefix + "collision-slug", CollaborationID: prefix + "first-generation", Creator: creator},
			{ID: prefix + "later-generation", CollaborationID: prefix + "collision", Creator: creator},
			{ID: prefix + "private-first", CollaborationID: prefix + "hidden-first", Creator: creator, TeamIDs: []string{private}},
			{ID: prefix + "public-second", CollaborationID: prefix + "hidden-first", Creator: creator, TeamIDs: []string{f.public}},
			{ID: prefix + "inherited", Creator: creator, ProjectIDs: []string{prefix + "child-project"}, InheritedTeamIDs: []string{child}},
			{ID: prefix + "user-grant", Creator: creator, ProjectIDs: []string{"m2-unrelated"}, InheritedTeamIDs: []string{private},
				Permissions: []domain.DocumentPermission{{SubjectType: "user", SubjectID: guest.ID, Role: "editor"}}},
			{ID: prefix + "workspace-grant", Creator: creator, ProjectIDs: []string{"m2-unrelated"}, InheritedTeamIDs: []string{private},
				Permissions: []domain.DocumentPermission{{SubjectType: "workspace", SubjectID: data.Workspace.ID, Role: "commenter"}}},
			{ID: prefix + "team-grant", Creator: creator, TeamIDs: []string{f.public},
				Permissions: []domain.DocumentPermission{{SubjectType: "team", SubjectID: f.public, Role: "editor"},
					{SubjectType: "user", SubjectID: guest.ID, Role: "viewer"}, {SubjectType: "team", SubjectID: f.public, Role: "commenter"}}},
			{ID: prefix + "nil", Creator: creator},
			{ID: prefix + "empty", Creator: creator, TeamIDs: []string{}, InheritedTeamIDs: []string{}, Permissions: []domain.DocumentPermission{}},
		}
		for index := range documents {
			documents[index].Title = "Unused collaboration title"
			documents[index].Content = strings.Repeat("Unused legal collaboration body <>&. ", 1000)
			documents[index].ContentState = "Unused Yjs base state"
			documents[index].ContentData = map[string]any{"native": []any{int64(9007199254740993), nil, []string{}}}
			documents[index].Revisions = []domain.DocumentRevision{{Content: documents[index].Content}}
		}
		data.Documents = append(data.Documents, documents...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	refs := []string{"legacy", "legacy-slug", "rotated", "current", "old", "generation", "collision", "collision-slug",
		"first-generation", "hidden-first", "inherited", "user-grant", "workspace-grant", "team-grant", "nil", "empty", "missing"}
	for index := range refs {
		refs[index] = prefix + refs[index]
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), f.repository, workspace); err != nil {
		t.Fatal(err)
	}
	return f, prefix, refs
}

func TestScaleP21SW179CollaborationDocumentSelectionMatrix(t *testing.T) {
	f, prefix, refs := scaleP21SW179Setup(t)
	workspace := f.data.Workspace.URLKey
	nilACL := scaleP21SW179Pair(t, f.scaleP21M2Fixture, t.Context(), workspace, f.data.Viewer.ID, prefix+"nil", nil)
	emptyACL := scaleP21SW179Pair(t, f.scaleP21M2Fixture, t.Context(), workspace, f.data.Viewer.ID, prefix+"empty", nil)
	if nilACL.Document.Permissions != nil || emptyACL.Document.Permissions == nil || nilACL.Document.TeamIDs != nil || emptyACL.Document.TeamIDs == nil {
		t.Fatal("native nil/empty header shapes were collapsed")
	}
	grant := scaleP21SW179Pair(t, f.scaleP21M2Fixture, t.Context(), workspace, f.principals[3].user.ID, prefix+"user-grant", nil)
	if !grant.Found || grant.Role != "editor" {
		t.Fatal("explicit guest ACL witness did not survive inherited private scope", grant)
	}
	teamGrant := scaleP21SW179Pair(t, f.scaleP21M2Fixture, t.Context(), workspace, f.principals[2].user.ID, prefix+"team-grant", nil)
	if !teamGrant.Found || teamGrant.Role != "editor" || len(teamGrant.Document.Permissions) != 3 ||
		teamGrant.Document.Permissions[0].Role != "editor" || teamGrant.Document.Permissions[2].Role != "commenter" {
		t.Fatal("ordered native team grants were not exercised", teamGrant)
	}
	for _, reloaded := range []bool{false, true} {
		t.Run(fmt.Sprint("reloaded=", reloaded), func(t *testing.T) {
			if reloaded {
				if err := f.repository.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), f.repository, workspace); err != nil {
				t.Fatal(err)
			}
			f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
				for _, ref := range refs {
					scaleP21SW179Pair(t, f.scaleP21M2Fixture, t.Context(), workspace, user.ID, ref, key)
				}
			})
			for _, ref := range refs {
				scaleP21SW179Pair(t, f.scaleP21M2Fixture, t.Context(), workspace, "", ref, nil)
			}
		})
	}
	for _, spec := range []struct {
		ref      string
		id       string
		conflict bool
		found    bool
	}{
		{"legacy", "legacy", false, true},
		{"legacy-slug", "legacy", true, true},
		{"generation", "rotated", false, true},
		{"rotated", "rotated", true, true},
		{"current", "rotated", true, true},
		{"old", "", false, false},
		{"collision", "collision", true, true},
	} {
		result := scaleP21SW179Pair(t, f.scaleP21M2Fixture, t.Context(), workspace, f.data.Viewer.ID, prefix+spec.ref, nil)
		if result.Found != spec.found || result.Conflict != spec.conflict || spec.found && result.Document.ID != prefix+spec.id {
			t.Fatal("nonempty generation/order witness did not reach its branch", spec, result)
		}
	}
	member := f.principals[2].user
	hidden := scaleP21SW179Pair(t, f.scaleP21M2Fixture, t.Context(), workspace, member.ID, prefix+"hidden-first", nil)
	if !hidden.Found || hidden.Document.ID != prefix+"public-second" || hidden.Conflict {
		t.Fatal("hidden earlier occurrence suppressed later visible generation", hidden)
	}
}

func TestScaleP21SW179CollaborationDocumentGrowthAndSource(t *testing.T) {
	for _, reloaded := range []bool{false, true} {
		t.Run(fmt.Sprint("reloaded=", reloaded), func(t *testing.T) {
			scaleP21SW179GrowthAndSource(t, reloaded)
		})
	}
}

func scaleP21SW179GrowthAndSource(t *testing.T, reloaded bool) {
	t.Helper()
	f, prefix, _ := scaleP21SW179Setup(t)
	workspace, held := f.data.Workspace.URLKey, 0
	for _, copies := range []int{1000, 10000} {
		body := strings.Repeat("Unused legal selected/unrelated body <>&. ", copies)
		if err := f.repository.MutateWorkspace(t.Context(), workspace, "scale_p21.sw179_growth", "", nil, func(data *domain.Bootstrap) error {
			data.Documents = slices.DeleteFunc(data.Documents, func(document domain.Document) bool { return strings.HasPrefix(document.ID, prefix+"noise-") })
			for index := range data.Documents {
				if data.Documents[index].ID == prefix+"rotated" {
					data.Documents[index].Content, data.Documents[index].ContentState = body, body
					data.Documents[index].ContentData = map[string]any{"body": body}
					data.Documents[index].Revisions = []domain.DocumentRevision{{Content: body, ContentState: body}}
				}
			}
			for index := 0; index < copies/50; index++ {
				data.Documents = append(data.Documents, domain.Document{ID: fmt.Sprintf("%snoise-%04d", prefix, index), Creator: f.data.Viewer, Content: "Unrelated"})
			}
			data.Documents[len(data.Documents)-1].Content = body
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if reloaded {
			if err := f.repository.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), f.repository, workspace); err != nil {
			t.Fatal(err)
		}
		read, err := f.probe.BeginCollaborationDocumentRead(t.Context(), workspace, f.data.Viewer.ID, prefix+"generation", nil)
		if err != nil {
			t.Fatal(err)
		}
		work := f.probe.MetadataWorkUsedForTesting()
		if work == 0 {
			t.Fatal("growth result escaped its owned admission")
		}
		if copies == 1000 {
			held = work
		} else if work != held {
			t.Fatal("10x selected body/directory growth increased retained work", held, work)
		}
		if !read.Found || len(read.Data.Documents) != 1 || !reflect.DeepEqual(read.Document, scaleP21SW179Header(read.Document)) {
			t.Fatal("growth read retained bodies or lost its selected header")
		}
		t.Logf("body_copies=%d reloaded=%t admitted_work=%d", copies, reloaded, work)
		read.Close()
		scaleP21SW179Pair(t, f.scaleP21M2Fixture, t.Context(), workspace, f.data.Viewer.ID, prefix+"generation", nil)
	}
	// Keep the probe's old installed generation. Open the literal SQL snapshot
	// after the write so no write needs to upgrade an existing read transaction.
	if err := f.repository.MutateWorkspace(t.Context(), workspace, "scale_p21.sw179_handed", "", nil, func(data *domain.Bootstrap) error {
		for index := range data.Documents {
			if data.Documents[index].ID == prefix+"rotated" {
				data.Documents[index].CollaborationID = prefix + "handed-generation"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, release, err := f.repository.BeginWorkspaceRead(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	result := scaleP21SW179Pair(t, f.scaleP21M2Fixture, ctx, workspace, f.data.Viewer.ID, prefix+"handed-generation", nil)
	if !result.Found || result.Document.ID != prefix+"rotated" {
		t.Fatal("handed generation was replaced by stale installed metadata")
	}
	release()
	if err := f.repository.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), f.repository, workspace); err != nil {
		t.Fatal(err)
	}
	scaleP21SW179Pair(t, f.scaleP21M2Fixture, t.Context(), workspace, f.data.Viewer.ID, prefix+"handed-generation", nil)
}

func TestScaleP21SW179CollaborationDocumentHiddenMatchSQL(t *testing.T) {
	f, prefix, _ := scaleP21SW179Setup(t)
	workspace, member := f.data.Workspace.URLKey, f.principals[2].user
	private := ""
	if data, found := f.repository.WorkspaceMetadataFields(workspace, "projects"); found {
		for _, project := range data.Projects {
			if project.ID == "m2-unrelated" && len(project.TeamIDs) != 0 {
				private = project.TeamIDs[0]
			}
		}
	}
	if private == "" {
		t.Fatal("hidden-match fixture private team missing")
	}
	key := &domain.APIKey{ID: prefix + "sql-key", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}
	ref, baselineSQL, baselineWork := prefix+"sql-generation", 0, 0
	var baseline scaleP21SW179Result
	for _, hidden := range []int{1, 100} {
		if err := f.repository.MutateWorkspace(t.Context(), workspace, "scale_p21.sw179_hidden_matches", "", nil, func(data *domain.Bootstrap) error {
			data.Documents = slices.DeleteFunc(data.Documents, func(document domain.Document) bool {
				return strings.HasPrefix(document.ID, prefix+"sql-")
			})
			for index := 0; index < hidden; index++ {
				data.Documents = append(data.Documents, domain.Document{
					ID: fmt.Sprintf("%ssql-hidden-%03d", prefix, index), CollaborationID: ref,
					Creator: f.principals[1].user, TeamIDs: []string{private},
				})
			}
			data.Documents = append(data.Documents, domain.Document{
				ID: prefix + "sql-visible", CollaborationID: ref, Creator: f.principals[1].user, TeamIDs: []string{f.public},
			})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), f.repository, workspace); err != nil {
			t.Fatal(err)
		}
		legacyQueries := 0
		legacyContext := store.WithSQLStatementObserver(t.Context(), func(string) { legacyQueries++ })
		want := scaleP21SW179Legacy(t, f.scaleP21M2Fixture, legacyContext, workspace, member.ID, ref, key)
		queries := 0
		ctx := store.WithSQLStatementObserver(t.Context(), func(string) { queries++ })
		read, err := f.probe.BeginCollaborationDocumentRead(ctx, workspace, member.ID, ref, key)
		if err != nil {
			t.Fatal(err)
		}
		got := scaleP21SW179Result{
			Found: read.Found, Conflict: read.Conflict, Document: scaleP21SW179Header(read.Document),
			Role: documentRole(&server{store: f.probe}, read.Data, read.Document),
		}
		work := f.probe.MetadataWorkUsedForTesting()
		if queries == 0 || !got.Found || got.Document.ID != prefix+"sql-visible" || !reflect.DeepEqual(want, got) {
			t.Fatal("hidden-match SQL sample lost exact visible selection/role", hidden, queries, want, got)
		}
		if hidden == 1 {
			baselineSQL, baselineWork, baseline = queries, work, got
		} else if queries != baselineSQL || work != baselineWork || !reflect.DeepEqual(got, baseline) {
			t.Fatal("hidden matching candidates amplified SQL or retained work", baselineSQL, queries, baselineWork, work)
		}
		read.Close()
		if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("hidden-match sample retained work or decoded the full workspace")
		}
		t.Logf("hidden_matching_candidates=%d legacy_SQL=%d bounded_SQL=%d admitted_work=%d", hidden, legacyQueries, queries, work)
	}
}

func TestScaleP21SW179CollaborationDocumentAuthorityAndLifetime(t *testing.T) {
	f, prefix, _ := scaleP21SW179Setup(t)
	workspace, member := f.data.Workspace.URLKey, f.principals[2].user
	app, err := f.repository.InstallApplication(store.ContextWithActor(t.Context(), f.data.Viewer), workspace, domain.ApplicationInstallation{
		ClientID: idgen.New("sw179-app-"), Name: "Collaboration reader application", Active: true,
		InstalledBy: f.data.Viewer.ID, TeamIDs: []string{f.public}, Scopes: []string{"read"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), f.repository, workspace); err != nil {
		t.Fatal(err)
	}
	for _, key := range []*domain.APIKey{
		nil,
		{ID: prefix + "read-key", Scopes: []string{"read"}, TeamRestriction: "all"},
		{ID: prefix + "app-key", Actor: "app", Scopes: []string{"document:read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}},
		{ID: prefix + "empty-app-key", Actor: "app", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}},
	} {
		for _, ref := range []string{"legacy", "generation", "inherited", "team-grant"} {
			scaleP21SW179Pair(t, f.scaleP21M2Fixture, t.Context(), workspace, app.UserID, prefix+ref, key)
		}
	}
	appVisible := scaleP21SW179Pair(t, f.scaleP21M2Fixture, t.Context(), workspace, app.UserID, prefix+"generation", nil)
	if !appVisible.Found || appVisible.Role == "none" {
		t.Fatal("installed application team visibility witness was empty", appVisible)
	}
	if err := f.repository.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	result := scaleP21SW179Pair(t, f.scaleP21M2Fixture, t.Context(), workspace, member.ID, prefix+"generation", nil)
	if result.Error == "" {
		t.Fatal("stale installed viewer survived live membership suspension")
	}
	if err := f.repository.ResumeMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	scaleP21SW179Pair(t, f.scaleP21M2Fixture, t.Context(), workspace, member.ID, prefix+"generation", nil)
	if _, err := f.repository.UninstallApplication(store.ContextWithActor(t.Context(), f.data.Viewer), workspace, app.ID); err != nil {
		t.Fatal(err)
	}
	result = scaleP21SW179Pair(t, f.scaleP21M2Fixture, t.Context(), workspace, app.UserID, prefix+"generation", nil)
	if result.Error == "" {
		t.Fatal("stale installed application survived live uninstall")
	}
	scaleP21SW179Pair(t, f.scaleP21M2Fixture, t.Context(), workspace+"-missing", f.data.Viewer.ID, prefix+"generation", nil)
	scaleP21SW179Pair(t, f.scaleP21M2Fixture, t.Context(), workspace, prefix+"missing-user", prefix+"generation", nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := f.probe.BeginCollaborationDocumentRead(ctx, workspace, f.data.Viewer.ID, prefix+"generation", nil); read != nil ||
		!errors.Is(err, context.Canceled) || f.probe.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("canceled construction returned partial output/work", err)
	}
	if read, err := f.probe.BeginCollaborationDocumentRead(t.Context(), workspace, f.data.Viewer.ID, strings.Repeat("x", 40<<20), nil); read != nil ||
		!errors.Is(err, store.ErrMetadataWorkBudget) || f.probe.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("oversized selection input escaped admission or leaked work", err)
	}
	read, err := f.probe.BeginCollaborationDocumentRead(t.Context(), workspace, f.data.Viewer.ID, prefix+"generation", nil)
	if err != nil {
		t.Fatal(err)
	}
	caller, cancelCaller := context.WithCancel(t.Context())
	owned, err := read.Context(caller)
	if err != nil {
		t.Fatal(err)
	}
	cancelCaller()
	if !errors.Is(owned.Err(), context.Canceled) {
		t.Fatal("collaboration context discarded caller cancellation")
	}
	read.Close()
	if f.probe.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("canceled caller retained collaboration work after close")
	}
	for _, transfer := range []string{"missing-owner", "closed-owner", "open-owner"} {
		t.Run(transfer, func(t *testing.T) {
			ctx := t.Context()
			closeOutput := func() {}
			if transfer != "missing-owner" {
				ctx, closeOutput = beginMetadataOutputReads(ctx)
			}
			defer closeOutput()
			if transfer == "closed-owner" {
				closeOutput()
			}
			read, err := f.probe.BeginCollaborationDocumentRead(t.Context(), workspace, f.data.Viewer.ID, prefix+"generation", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close()
			err = read.TransferOutput(ctx)
			if transfer == "open-owner" {
				if err != nil || f.probe.MetadataWorkUsedForTesting() == 0 || !read.Found {
					t.Fatal("output transfer lost its independently owned selection", err)
				}
			} else if !errors.Is(err, store.ErrBoundedMetadataCoverage) {
				t.Fatal("unowned selection transfer escaped", err)
			}
			closeOutput()
			if f.probe.MetadataWorkUsedForTesting() != 0 || !reflect.DeepEqual(read.Data, domain.Bootstrap{}) {
				t.Fatal("output close/refusal retained collaboration input")
			}
		})
	}
}
