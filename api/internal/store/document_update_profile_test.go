package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW481DocumentProfileNativeMerge(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "physical", URLKey: "physical"},
		Viewer:    domain.User{ID: "actor", Name: "Actor", Active: true},
		Users: []domain.User{
			{ID: "actor", Name: "Actor", Email: "actor@example.test", Active: true},
			{ID: "recipient", Name: "Recipient", Email: "recipient@example.test", Active: true, AvatarURL: strings.Repeat("ignored-profile", 80000)},
		},
		Documents: []domain.Document{
			{ID: "old", PreviousSlugIDs: []string{"alias"}, Title: "Old alias must lose"},
			{ID: "target", SlugID: "alias", Title: "Selected", Content: "Complete current body",
				ContentData: map[string]any{"type": "doc", "content": []any{map[string]any{"value": int64(1<<53 + 1), "at": at}}},
				ProjectIDs:  []string{"old-project"}, Creator: domain.User{ID: "actor"},
				Revisions: []domain.DocumentRevision{{ID: "prior", Content: "Complete history", Author: domain.User{ID: "recipient"}, CreatedAt: at}}},
			{ID: "target", SlugID: "later", Title: "Later duplicate must remain"},
			{ID: "unrelated", Content: strings.Repeat("ignored document body ", 80000)},
		},
		Projects: []domain.Project{
			{ID: "old-project", Name: "Old", Description: strings.Repeat("linked project body ", 80000), TeamIDs: []string{"team"}, Resources: []domain.ProjectResource{{ID: "target", Title: "Before"}}},
			{ID: "new-project", Name: "New"},
			{ID: "resource-only", Resources: []domain.ProjectResource{{ID: "target", Title: "Unlink me"}}},
			{ID: "unrelated", Description: strings.Repeat("ignored project body ", 80000)},
		},
		Subscriptions: []domain.Subscription{
			{ID: "keep", ResourceType: "document", ResourceID: "target", UserID: "recipient"},
			{ID: "ignore", ResourceType: "document", ResourceID: "other", UserID: "outside"},
		},
		PushSubscriptions: []domain.PushSubscription{{ID: "push", UserID: "recipient", Enabled: true}},
		Favorites:         nil,
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	source, err := view.retainMetadataLogicalSource(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	owner := &ResourceRead{store: view}
	defer owner.Close()
	profile := &documentUpdateImageProfile{request: DocumentUpdateWriteRequest{Ref: "alias", ProjectIDs: []string{"new-project"}}}
	selected, err := profile.inputs(t.Context(), owner, source)
	if err != nil {
		t.Fatal(err)
	}
	if profile.documentIndex != 0 || len(selected.Documents) != 2 || selected.Documents[0].Title != "Selected" ||
		selected.Documents[1].Title != "Later duplicate must remain" || len(selected.Projects) != 3 ||
		len(selected.Subscriptions) != 1 || selected.Subscriptions[0].UserID != "recipient" {
		t.Fatal("closed document ordinal/reference selection changed")
	}
	for _, project := range selected.Projects {
		if project.Description != "" || project.ID == "old-project" && (project.Name != "Old" || !reflect.DeepEqual(project.TeamIDs, []string{"team"}) || len(project.Resources) != 1) {
			t.Fatal("linked project callback header is not exactly ID, Name, TeamIDs and Resources")
		}
	}
	if selected.Users[1].AvatarURL != "" || selected.Documents[0].Revisions[0].Content != "Complete history" ||
		selected.Documents[0].ContentData["content"].([]any)[0].(map[string]any)["value"] != int64(1<<53+1) {
		t.Fatal("native selected history/types or ignored body projection changed")
	}
	before := cloneBootstrap(selected)
	selected.Documents[0].Title = "After"
	selected.Documents[0].Revisions = append([]domain.DocumentRevision{{ID: "new", Title: "After", CreatedAt: at}}, selected.Documents[0].Revisions...)
	selected.Projects[0].Resources = nil
	selected.Projects[1].Resources = []domain.ProjectResource{{ID: "target", ProjectID: "new-project", Title: "After", PinnedTeamIDs: []string{}, CreatedAt: at}}
	selected.Favorites = []domain.Favorite{{ID: "favorite", ResourceType: "document", ResourceID: "target", UserID: "actor", CreatedAt: at}}
	if err := profile.validate(before, selected); err != nil {
		t.Fatal(err)
	}
	image, err := profile.image(t.Context(), owner, source, before, selected)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Release()
	index, _, _ := metadataLogicalField("documents")
	value, err := image.fields[index].Value(t.Context(), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	expected := append([]domain.Document(nil), data.Documents...)
	expected[1] = selected.Documents[0]
	if !reflect.DeepEqual(expected, value.Interface().([]domain.Document)) {
		t.Fatal("document replacement changed another native occurrence or type")
	}
	index, _, _ = metadataLogicalField("projects")
	value, err = image.fields[index].Value(t.Context(), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	projects := append([]domain.Project(nil), data.Projects...)
	projects[0].Resources = nil
	projects[1].Resources = selected.Projects[1].Resources
	if !reflect.DeepEqual(projects, value.Interface().([]domain.Project)) {
		t.Fatal("linked project delta changed more than Resources")
	}
	// A header-only edit outside Resources is refused, never written.
	changed := cloneBootstrap(selected)
	changed.Projects[0].Name = "Renamed"
	if err := profile.validate(before, changed); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("project edit outside Resources was admitted", err)
	}
	index, _, _ = metadataLogicalField("favorites")
	value, err = image.fields[index].Value(t.Context(), 1<<20)
	if err != nil || !reflect.DeepEqual(selected.Favorites, value.Interface().([]domain.Favorite)) {
		t.Fatal("nil favorites lost appended nonnil shape", err)
	}
	owner.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("selected document profile did not release work", view.MetadataWorkUsedForTesting())
	}
}

func TestScaleP21SW481DocumentCapturedConflictOwnership(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, documentID := f.data.Workspace.URLKey, "sw481-"+f.data.Workspace.ID
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw481_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = []domain.Document{{ID: documentID, Title: "Current", Version: 4, ContentData: map[string]any{"native": int64(1<<53 + 1)}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	var closes []func()
	ctx := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
		closes = append(closes, close)
		return true
	})
	ctx = WithMutationScope(ctx, MutationScope{Resources: []string{documentID}})
	ctx = WithDocumentUpdateWrite(ctx, DocumentUpdateWriteRequest{Ref: documentID})
	sentinel := errors.New("captured current version conflict")
	var current domain.Document
	err = view.MutateWorkspace(ctx, workspace, "document.updated", documentID, map[string]any{"expectedVersion": 3}, func(data *domain.Bootstrap) error {
		if data.CollaborationWork == nil {
			t.Fatal("document writer did not install admitted editor services")
		}
		current = data.Documents[0]
		return sentinel
	})
	if !errors.Is(err, sentinel) || len(closes) == 0 || view.MetadataWorkUsedForTesting() == 0 ||
		current.Version != 4 || current.ContentData["native"] != int64(1<<53+1) {
		t.Fatal("captured conflict lost ownership/native current document", err, len(closes), view.MetadataWorkUsedForTesting())
	}
	for _, close := range closes {
		close()
		close()
	}
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("conflict response owner did not release")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := view.MutateWorkspace(cancelled, workspace, "document.updated", documentID, nil, func(*domain.Bootstrap) error {
		t.Fatal("cancelled callback ran")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal("document writer ignored cancellation")
	}
}
