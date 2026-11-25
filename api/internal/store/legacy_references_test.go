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

func scaleP21SW81Data(repetitions int) domain.Bootstrap {
	at, native := time.Date(2026, 11, 14, 1, 2, 3, 456, time.UTC), time.Now()
	body := strings.Repeat("Complete unrelated reference body. ", repetitions)
	stale := domain.User{ID: "user", Name: "Stale embedded user", AppScopes: nil, AppTeamIDs: []string{}}
	empty, missing, via := stale, stale, stale
	empty.ID, missing.ID, via.ID = "", "missing-user", "via-user"
	return domain.Bootstrap{
		Workspace: domain.Workspace{ID: "native", URLKey: "native"}, Viewer: stale, ViewerRole: "unchanged-role",
		Users: []domain.User{
			{ID: "user", Name: "Earlier duplicate user"},
			{ID: "", Name: "Earlier empty user"},
			{ID: "user", Name: "Last duplicate user", DisplayName: "Full current display", JobTitle: "Full current user body", AppScopes: []string{"read"}, AppTeamIDs: []string{}, OutOfOfficeUntil: &native},
			{ID: "", Name: "Last empty user", JobTitle: "Full empty-ID body", AppScopes: []string{}},
			{ID: "unreferenced-user", Name: "Unreferenced user", JobTitle: body},
		},
		Teams:   []domain.Team{{ID: "team", Name: "Earlier team"}, {ID: "", Name: "Earlier empty team"}, {ID: "team", Name: "Last team", Key: "LAST", Private: true, CreatedAt: &at}, {ID: "", Name: "Last empty team"}},
		States:  []domain.WorkflowState{{ID: "state", Name: "Earlier state"}, {ID: "", Name: "Earlier empty state"}, {ID: "state", Name: "Last state", Description: "Full state body", Type: "completed"}, {ID: "", Name: "Last empty state", Type: "started"}},
		Labels:  []domain.IssueLabel{{ID: "label", Name: "Earlier label", IssueCount: 901}, {ID: "", Name: "Earlier empty label"}, {ID: "label", Name: "Last label", Description: "Full label body", IssueCount: 902, LastAppliedAt: &at}, {ID: "", Name: "Last empty label", IssueCount: 903}},
		Members: []domain.WorkspaceMember{{User: stale, Role: "member", JoinedAt: at, LastSeenAt: &native}, {User: empty}, {User: missing}, {User: via}},
		Projects: []domain.Project{
			{ID: "project", Name: "Earlier project", Description: body, Lead: &stale, Creator: &empty, Comments: []domain.Comment{{ID: "nested-project-comment", User: stale}}, IssueCount: 801},
			{ID: "", Name: "Earlier empty project", Creator: &missing},
			{ID: "project", Name: "Last project", Icon: "last-icon", Color: "#123456", Description: body, Lead: &via, Creator: &stale, IssueCount: 802},
			{ID: "", Name: "Last empty project", Color: "#abcdef", Lead: nil, Creator: nil, IssueCount: 803},
		},
		Initiatives:       []domain.Initiative{{ID: "initiative", Creator: stale, Owner: &empty, Description: body, Comments: []domain.Comment{{ID: "nested-initiative-comment", User: stale}}}, {ID: "nil-owner", Creator: missing}, {ID: "via-owner", Creator: via, Owner: &via}},
		ProjectUpdates:    map[string][]domain.ProjectUpdate{"nil": nil, "empty": {}, "project": {{ID: "project-update", User: stale, Body: body, CreatedAt: at, Comments: []domain.Comment{{ID: "nested-project-update-comment", User: stale}}}}},
		InitiativeUpdates: map[string][]domain.InitiativeUpdate{"nil": nil, "empty": {}, "initiative": {{ID: "initiative-update", User: empty, Body: body, CreatedAt: at, Comments: []domain.Comment{{ID: "nested-initiative-update-comment", User: stale}}}}},
		Comments:          map[string][]domain.Comment{"nil": nil, "empty": {}, "group": {{ID: "comment", User: stale, Body: body, CreatedAt: at, EditedAt: &native}, {ID: "via-comment", User: via}, {ID: "missing-comment", User: missing}}},
		Activities:        map[string][]domain.ActivityEvent{"nil": nil, "empty": {}, "group": {{ID: "activity", Actor: empty, CreatedAt: at, Metadata: domain.ActivityMetadata{"body": body}}, {ID: "via-activity", Actor: via}}},
		Notifications:     []domain.Notification{{ID: "notification", Actor: stale, Title: body, CreatedAt: at}, {ID: "via-notification", Actor: via}},
		Issues: []domain.Issue{
			{ID: "issue", Title: body, Description: body, Team: domain.Team{ID: "team", Name: "Stale team"}, State: domain.WorkflowState{ID: "state", Name: "Stale state"}, Creator: stale, Assignee: &empty, Delegate: &via,
				Project: &domain.ProjectSummary{ID: "project", Name: "Stale project"}, Labels: []domain.IssueLabel{{ID: "label", Name: "Stale label", IssueCount: 999}, {ID: "", Name: "Stale empty label"}, {ID: "missing-label", Name: "Preserve absent label"}}, CreatedAt: at, Reactions: map[string][]string{"like": {"user", "user"}}},
			{ID: "empty-refs", Team: domain.Team{ID: ""}, State: domain.WorkflowState{ID: ""}, Creator: empty, Project: &domain.ProjectSummary{ID: ""}, Labels: []domain.IssueLabel{}},
			{ID: "missing-refs", Team: domain.Team{ID: "missing-team"}, State: domain.WorkflowState{ID: "missing-state"}, Creator: missing, Assignee: &missing, Project: &domain.ProjectSummary{ID: "missing-project"}, Labels: nil},
		},
		NotificationDeliveries: []domain.NotificationDelivery{{ID: "unchanged-delivery", RecipientID: "user", Error: body, CreatedAt: at}},
		Documents:              []domain.Document{{ID: "unchanged-document", Content: body}},
		Settings:               map[string]any{"native": map[string]any{"integer": int64(9007199254740993), "bytes": []byte{0, 255}, "nil": []string(nil), "empty": []string{}, "time": native}},
	}
}

func scaleP21SW81Compare(t *testing.T, view *SQLiteStore, source *metadataLogicalSource, releaseInput func()) {
	t.Helper()
	scope := scaleP21SW72FullScope(t, source)
	before, err := source.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	want, err := source.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	refreshDisplayReferences(&want)
	refreshIssueReferences(&want)
	queries := 0
	ctx, cancel := context.WithCancel(WithSQLStatementObserver(t.Context(), func(string) { queries++ }))
	defer cancel()
	result, err := view.refreshLegacyDisplayImage(ctx, source)
	if err != nil || result == nil {
		t.Fatal("legacy display composition failed", err)
	}
	t.Cleanup(result.Release)
	scaleP21SW72Compare(t, result, scope, want)
	scaleP21SW72Compare(t, source, scope, before)
	for index, node := range source.fields {
		name := reflect.TypeFor[domain.Bootstrap]().Field(index).Tag.Get("json")
		switch name {
		case "-", "members", "projects", "initiatives", "projectUpdates", "initiativeUpdates", "comments", "activities", "notifications", "issues":
			continue
		}
		if result.fields[index] != node {
			t.Fatal("display composition rewrote another source field", name)
		}
	}
	if result.Stamp() != source.Stamp() || queries != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("display composition changed generation or read SQL/metadata")
	}
	releaseInput()
	cancel()
	scaleP21SW72Compare(t, result, scope, want)
	result.Release()
	result.Release()
	if _, err := result.Select(t.Context(), scope, 64<<20); !errors.Is(err, errMetadataLogicalClosed) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("released display image remained usable or retained work", err)
	}
}

func TestScaleP21SW81LegacyReferencesLiteral(t *testing.T) {
	for _, variant := range []string{"last-id-winners", "native-via", "nil-directories", "empty-directories", "nil-collections", "empty-collections", "large-bodies"} {
		t.Run(variant, func(t *testing.T) {
			data := scaleP21SW81Data(1000)
			if variant == "large-bodies" {
				data = scaleP21SW81Data(20000)
			}
			switch variant {
			case "nil-directories":
				data.Users, data.Teams, data.States, data.Labels, data.Projects = nil, nil, nil, nil, nil
			case "empty-directories":
				data.Users, data.Teams, data.States, data.Labels, data.Projects = []domain.User{}, []domain.Team{}, []domain.WorkflowState{}, []domain.IssueLabel{}, []domain.Project{}
			case "nil-collections":
				data.Members, data.Projects, data.Initiatives, data.ProjectUpdates, data.InitiativeUpdates, data.Comments, data.Activities, data.Notifications, data.Issues = nil, nil, nil, nil, nil, nil, nil, nil, nil
			case "empty-collections":
				data.Members, data.Projects, data.Initiatives = []domain.WorkspaceMember{}, []domain.Project{}, []domain.Initiative{}
				data.ProjectUpdates, data.InitiativeUpdates = map[string][]domain.ProjectUpdate{}, map[string][]domain.InitiativeUpdate{}
				data.Comments, data.Activities = map[string][]domain.Comment{}, map[string][]domain.ActivityEvent{}
				data.Notifications, data.Issues = []domain.Notification{}, []domain.Issue{}
			}
			view := &SQLiteStore{metadataResidency: newMetadataResidency()}
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			base, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: "native", revision: -1})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(base.Release)
			source, release := base, func() { base.Release() }
			if variant == "native-via" {
				native := time.Now()
				actor := domain.User{ID: "via-user", Name: "Native Via winner", AppScopes: []string{}, OutOfOfficeUntil: &native,
					Via: &domain.ActorBot{ID: "native-application", Type: domain.ActorBotTypeOAuthApplication, Name: "Preserve native Via", OnBehalfOfID: "person"}}
				image, err := view.prepareLegacyActorImage(t.Context(), base, &actor)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(image.Close)
				source, release = image.Source(), func() { image.Close(); base.Release() }
				users, err := source.Select(t.Context(), MetadataRecordScope{Fields: []string{"users"}}, 4<<20)
				if err != nil || users.Users[len(users.Users)-1].Via == nil {
					t.Fatal("native Via fixture was not installed", err)
				}
			}
			scaleP21SW81Compare(t, view, source, release)
			if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatal("released reference composition leaked storage")
			}
		})
	}
}

func TestScaleP21SW81LegacyReferencesWarmReload(t *testing.T) {
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
	for _, size := range []int{2000, 20000} {
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "users", "projects", "members", "documents"), workspace, "scale_p21.sw81_seed", "", nil, func(data *domain.Bootstrap) error {
			user := data.Viewer
			user.Name = "Current directory name"
			stale := user
			stale.Name = "Stale embedded name"
			body := strings.Repeat("Unrelated warm reference body. ", size)
			data.Users = []domain.User{user, {ID: "warm-unreferenced-user", JobTitle: body}}
			data.Projects = []domain.Project{{ID: "warm-project", Name: "Warm project", Description: body, Lead: &stale, Creator: &stale}}
			data.Members = []domain.WorkspaceMember{{User: stale, Role: "member"}}
			data.Documents = []domain.Document{{ID: "warm-document", Content: body}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			t.Run(fmt.Sprintf("body-%d/reload-%t", size, reload), func(t *testing.T) {
				if reload {
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
					t.Fatal(err)
				}
				source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				owned := true
				t.Cleanup(func() {
					if owned {
						source.Release()
					}
				})
				disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
				scaleP21SW81Compare(t, view, source, func() { source.Release(); owned = false })
				if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
					t.Fatal("warm reference composition retained temporary storage")
				}
			})
		}
	}
}

func TestScaleP21SW81LegacyReferencesCancellation(t *testing.T) {
	view := &SQLiteStore{metadataResidency: newMetadataResidency()}
	source, err := captureMetadataLogicalSource(t.Context(), scaleP21SW81Data(1000), copyStamp{workspaceID: "native", revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	scope := scaleP21SW72FullScope(t, source)
	before, err := source.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	result, err := view.refreshLegacyDisplayImage(canceled, source)
	if result != nil {
		result.Release()
	}
	if result != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("canceled display composition escaped or leaked", err)
	}
	scaleP21SW72Compare(t, source, scope, before)
	source.Release()
	if result, err := view.refreshLegacyDisplayImage(t.Context(), source); result != nil || !errors.Is(err, errMetadataLogicalClosed) {
		if result != nil {
			result.Release()
		}
		t.Fatal("released source accepted display composition", err)
	}
}
