package store

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW28LogicalNormalizationAdversarial(t *testing.T) {
	stamp := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	current := domain.User{ID: "usr_sw28_current", Name: "Fresh", DisplayName: "Fresh display", AppTeamIDs: []string{"team_private"}}
	stale := current
	stale.Name, stale.DisplayName, stale.AppTeamIDs = "Old", "Old display", []string{"team_old"}
	parentID, childID, missingID, selfID := "issue_sw28_parent", "issue_sw28_child", "issue_sw28_missing", "issue_sw28_self"
	fixtures := []struct {
		name string
		data domain.Bootstrap
	}{
		{
			name: "cross-field-defaults-and-orphans",
			data: domain.Bootstrap{
				Viewer: current,
				Users:  []domain.User{current},
				Teams:  []domain.Team{{ID: "team_one"}, {ID: "team_two"}},
				States: []domain.WorkflowState{
					{ID: "state_foreign_default", TeamID: "team_other", Default: true},
					{ID: "state_backlog", Type: "backlog"},
					{ID: "state_team_two", TeamID: "team_two", Default: true},
				},
				UserSettings: map[string]domain.UserSettings{
					"orphan": {UserID: "orphan", UpdatedAt: stamp},
				},
				TeamSettings: map[string]domain.TeamSettings{
					"team_two": {TeamID: "", Access: "private", StaleMonths: 99},
					"orphan":   {Access: "private", StaleMonths: 99},
				},
				CycleSettings: map[string]domain.CycleSettings{
					"team_one": {DurationWeeks: 2},
					"orphan":   {DurationWeeks: 2},
				},
				Initiatives: []domain.Initiative{{ID: "initiative_sw28_missing_creator"}},
			},
		},
		{
			name: "duplicate-directory-and-status-ids-last-wins",
			data: domain.Bootstrap{
				Viewer: current,
				Users:  []domain.User{stale, current},
				ProjectStatuses: []domain.ProjectStatus{
					{ID: "status_sw28", Name: "First"},
					{ID: "status_sw28", Name: "Last"},
				},
				Members: []domain.WorkspaceMember{{User: stale}},
				Projects: []domain.Project{{
					ID: "project_sw28_duplicate", Lead: &stale, Creator: &stale,
					Status: domain.ProjectStatus{ID: "status_sw28", Name: "Embedded old"},
				}},
				Initiatives: []domain.Initiative{{ID: "initiative_sw28_duplicate", Creator: stale, Owner: &stale}},
			},
		},
		{
			name: "external-parent-links-counts-and-label-projection",
			data: domain.Bootstrap{
				Viewer: current, Users: []domain.User{current},
				Labels: []domain.IssueLabel{{ID: "label_sw28", Name: "Current label", IssueCount: 91}},
				Projects: []domain.Project{
					{ID: "project_sw28", LabelIDs: []string{"label_sw28", "label_sw28", ""}, IssueCount: 92},
					{ID: "project_sw28_archived", ArchivedAt: &stamp, LabelIDs: []string{"label_sw28"}, IssueCount: 93},
				},
				Initiatives: []domain.Initiative{{ID: "initiative_sw28", LabelIDs: []string{"label_sw28", "label_sw28"}}},
				Issues: []domain.Issue{
					{ID: parentID, Title: "Parent", Labels: []domain.IssueLabel{{ID: "label_sw28", Name: "Old label"}}, Project: &domain.ProjectSummary{ID: "project_sw28"}},
					{ID: childID, Title: "Child", ParentID: &parentID, Labels: []domain.IssueLabel{{ID: "label_sw28"}, {ID: "label_sw28"}}, Project: &domain.ProjectSummary{ID: "project_sw28"}},
					{ID: "issue_sw28_orphan", ParentID: &missingID},
					{ID: selfID, ParentID: &selfID},
					{ID: "issue_sw28_archived", ArchivedAt: &stamp, Labels: []domain.IssueLabel{{ID: "label_sw28"}}, Project: &domain.ProjectSummary{ID: "project_sw28"}},
				},
			},
		},
		{
			name: "duplicate-parent-ids-preserve-slice-order",
			data: domain.Bootstrap{Issues: []domain.Issue{
				{ID: parentID, Title: "First duplicate", SubIssueIDs: []string{"existing"}},
				{ID: parentID, Title: "Last duplicate"},
				{ID: childID, ParentID: &parentID},
			}},
		},
		{
			name: "external-content-actors-use-current-directory",
			data: domain.Bootstrap{
				Viewer: current, Users: []domain.User{stale, current},
				Comments: map[string][]domain.Comment{
					parentID: {{ID: "comment_sw28", User: stale}},
				},
				Activities: map[string][]domain.ActivityEvent{
					parentID: {{ID: "activity_sw28", Actor: stale}},
				},
				Notifications: []domain.Notification{{ID: "notification_sw28", RecipientID: current.ID, Actor: stale}},
			},
		},
		{
			name: "nil-empty-update-map-values",
			data: domain.Bootstrap{
				Viewer: current, Users: []domain.User{current},
				ProjectUpdates: map[string][]domain.ProjectUpdate{
					"project_nil": nil, "project_empty": {},
					"project_nonempty": {{ID: "project_update_sw28", User: stale}},
				},
				InitiativeUpdates: map[string][]domain.InitiativeUpdate{
					"initiative_nil": nil, "initiative_empty": {},
					"initiative_nonempty": {{ID: "initiative_update_sw28", User: stale}},
				},
				Comments: map[string][]domain.Comment{"issue_nil": nil, "issue_empty": {}},
			},
		},
		{
			name: "existing-empty-vs-nil-collections",
			data: domain.Bootstrap{
				Users: []domain.User{}, Teams: []domain.Team{}, Documents: []domain.Document{},
				Issues: []domain.Issue{}, Activities: map[string][]domain.ActivityEvent{},
				Comments: map[string][]domain.Comment{}, Notifications: []domain.Notification{},
				NotificationDeliveries: []domain.NotificationDelivery{},
				ProjectUpdates:         map[string][]domain.ProjectUpdate{},
				InitiativeUpdates:      map[string][]domain.InitiativeUpdate{},
			},
		},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			original := cloneBootstrap(fixture.data)
			original.WorkspaceSettings = domain.WorkspaceSettings{SessionDurationDays: 30, UpdatedAt: stamp}
			original.CustomerStatuses = []domain.CustomerStatus{}
			// Generated clock values are tested separately. Seed every present
			// identity's timestamp to make all other fields compare literally.
			if original.UserSettings == nil {
				original.UserSettings = map[string]domain.UserSettings{}
			}
			if original.NotificationPreferences == nil {
				original.NotificationPreferences = map[string]domain.NotificationPreferences{}
			}
			for _, user := range original.Users {
				if _, exists := original.UserSettings[user.ID]; !exists {
					original.UserSettings[user.ID] = domain.UserSettings{UserID: user.ID, UpdatedAt: stamp}
				}
				original.NotificationPreferences[user.ID] = domain.NotificationPreferences{UserID: user.ID, UpdatedAt: stamp}
			}
			want := cloneBootstrap(original)
			normalizeStoredMetadata(&want)
			raw, err := captureMetadataLogicalSource(t.Context(), original, copyStamp{workspaceID: "sw28-normalize", revision: 4})
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Release()
			repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
			normalized, err := repo.normalizeMetadataLogicalSource(t.Context(), raw)
			if err != nil {
				t.Fatal(err)
			}
			defer normalized.Release()
			got := scaleP21SW28DecodeLogicalSource(t, normalized)
			scaleP21SW28CompareNormalized(t, want, got)
			if normalized.Stamp() != raw.Stamp() || repo.metadataResidency.work.used != 0 {
				t.Fatal("normalization changed generation or leaked admission")
			}
		})
	}
}

func TestScaleP21SW28LogicalNormalizationSynthesizedDefaults(t *testing.T) {
	user := domain.User{ID: "usr_sw28_default", Name: "Defaults", DisplayName: "Defaults"}
	original := domain.Bootstrap{
		Viewer: user, Users: []domain.User{user},
		Teams:     []domain.Team{{ID: "team_sw28_default"}},
		Documents: []domain.Document{{ID: "document_sw28_default", Creator: user}},
	}
	start := time.Now().UTC()
	want := cloneBootstrap(original)
	normalizeStoredMetadata(&want)
	source, err := captureMetadataLogicalSource(t.Context(), original, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
	normalized, err := repo.normalizeMetadataLogicalSource(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	defer normalized.Release()
	got := scaleP21SW28DecodeLogicalSource(t, normalized)
	finish := time.Now().UTC()
	clock := func(name string, expected, actual time.Time) {
		t.Helper()
		if expected.Before(start) || expected.After(finish) || actual.Before(start) || actual.After(finish) {
			t.Fatalf("%s synthesized timestamp outside execution: old=%v new=%v interval=%v..%v", name, expected, actual, start, finish)
		}
	}
	clock("workspace", want.WorkspaceSettings.UpdatedAt, got.WorkspaceSettings.UpdatedAt)
	want.WorkspaceSettings.UpdatedAt = got.WorkspaceSettings.UpdatedAt
	for id, expected := range want.UserSettings {
		actual, exists := got.UserSettings[id]
		if !exists {
			t.Fatalf("missing synthesized user settings %s", id)
		}
		clock("userSettings/"+id, expected.UpdatedAt, actual.UpdatedAt)
		expected.UpdatedAt = actual.UpdatedAt
		want.UserSettings[id] = expected
	}
	for id, expected := range want.NotificationPreferences {
		actual, exists := got.NotificationPreferences[id]
		if !exists {
			t.Fatalf("missing synthesized notification settings %s", id)
		}
		clock("notificationPreferences/"+id, expected.UpdatedAt, actual.UpdatedAt)
		expected.UpdatedAt = actual.UpdatedAt
		want.NotificationPreferences[id] = expected
	}
	if len(want.CustomerStatuses) != len(got.CustomerStatuses) {
		t.Fatal("synthesized customer statuses changed length")
	}
	for i := range want.CustomerStatuses {
		clock("customerStatus.created", want.CustomerStatuses[i].CreatedAt, got.CustomerStatuses[i].CreatedAt)
		clock("customerStatus.updated", want.CustomerStatuses[i].UpdatedAt, got.CustomerStatuses[i].UpdatedAt)
		want.CustomerStatuses[i].CreatedAt = got.CustomerStatuses[i].CreatedAt
		want.CustomerStatuses[i].UpdatedAt = got.CustomerStatuses[i].UpdatedAt
	}
	if len(want.Documents) != 1 || len(got.Documents) != 1 ||
		len(want.Documents[0].Permissions) != 1 || len(got.Documents[0].Permissions) != 1 {
		t.Fatal("synthesized document creator permission missing")
	}
	clock("documentPermission.created", want.Documents[0].Permissions[0].CreatedAt, got.Documents[0].Permissions[0].CreatedAt)
	clock("documentPermission.updated", want.Documents[0].Permissions[0].UpdatedAt, got.Documents[0].Permissions[0].UpdatedAt)
	want.Documents[0].Permissions[0].CreatedAt = got.Documents[0].Permissions[0].CreatedAt
	want.Documents[0].Permissions[0].UpdatedAt = got.Documents[0].Permissions[0].UpdatedAt
	scaleP21SW28CompareNormalized(t, want, got)
	if repo.metadataResidency.work.used != 0 {
		t.Fatal("synthesized defaults leaked work admission")
	}
}

func scaleP21SW28DecodeLogicalSource(t *testing.T, source *metadataLogicalSource) domain.Bootstrap {
	t.Helper()
	var result domain.Bootstrap
	target := reflect.ValueOf(&result).Elem()
	if err := source.VisitFields(t.Context(), func(name string, node metadataLogicalNode) error {
		if name == "-" {
			return nil
		}
		index, _, ok := metadataLogicalField(name)
		if !ok {
			t.Fatalf("unregistered logical field %q", name)
		}
		value, err := node.Value(t.Context(), metadataWorkBytes)
		if err == nil {
			target.Field(index).Set(value)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21SW28CompareNormalized(t *testing.T, want, got domain.Bootstrap) {
	t.Helper()
	expected, actual := reflect.ValueOf(want), reflect.ValueOf(got)
	for index := 0; index < actual.NumField(); index++ {
		a, b := expected.Field(index).Interface(), actual.Field(index).Interface()
		if !reflect.DeepEqual(a, b) {
			wantJSON, _ := json.Marshal(a)
			gotJSON, _ := json.Marshal(b)
			t.Errorf("%s normalization differs\nold=%s\nnew=%s", actual.Type().Field(index).Name, wantJSON, gotJSON)
		}
	}
}
