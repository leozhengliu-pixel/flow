package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SlackNotificationProjection(data domain.Bootstrap, kind, id string, requests []SlackNotificationTeam) domain.Bootstrap {
	result := domain.Bootstrap{Workspace: domain.Workspace{URLKey: data.Workspace.URLKey}}
	for _, connection := range data.IntegrationConnections {
		if connection.Provider == "slack" && connection.Status == "connected" && connection.OAuthAuthorized {
			result.IntegrationConnections = []domain.IntegrationConnection{{ID: connection.ID, Provider: connection.Provider, Status: connection.Status, OAuthAuthorized: true}}
			break
		}
	}
	if kind == "project" {
		for _, item := range data.Projects {
			if item.ID == id {
				result.Projects = []domain.Project{{ID: item.ID, Name: item.Name, SlugID: item.SlugID, TeamIDs: item.TeamIDs}}
				break
			}
		}
	}
	if kind == "initiative" {
		for _, item := range data.Initiatives {
			if item.ID == id {
				result.Initiatives = []domain.Initiative{{ID: item.ID, Name: item.Name, SlugID: item.SlugID, LeadTeamID: item.LeadTeamID, ContributingTeamIDs: item.ContributingTeamIDs}}
				break
			}
		}
	}
	for _, request := range requests {
		settings, found := data.TeamSettings[request.TeamID]
		if !found {
			continue
		}
		if result.TeamSettings == nil {
			result.TeamSettings = map[string]domain.TeamSettings{}
		}
		selected := result.TeamSettings[request.TeamID]
		selected.SlackChannelID = settings.SlackChannelID
		if selected.SlackChannelID == "" {
			selected.SlackChannelName = settings.SlackChannelName
		}
		if enabled, exists := settings.SlackNotifications[request.Key]; exists {
			if selected.SlackNotifications == nil {
				selected.SlackNotifications = map[string]bool{}
			}
			selected.SlackNotifications[request.Key] = enabled
		}
		result.TeamSettings[request.TeamID] = selected
	}
	return result
}

func TestScaleP21SlackNotificationNativeOwnership(t *testing.T) {
	wide := strings.Repeat("Unrelated Slack metadata ", 40000)
	data := domain.Bootstrap{
		Workspace: domain.Workspace{URLKey: "logical\x00workspace", Name: wide},
		IntegrationConnections: []domain.IntegrationConnection{
			{ID: wide, Provider: "Slack", Status: "connected", OAuthAuthorized: true},
			{ID: wide, Provider: "slack", Status: "Connected", OAuthAuthorized: true},
			{ID: wide, Provider: "slack", Status: "connected"},
			{ID: "first\x00connection", Provider: "slack", Status: "connected", OAuthAuthorized: true, Config: map[string]string{"unused": wide}},
			{ID: "later", Provider: "slack", Status: "connected", OAuthAuthorized: true},
		},
		Projects: []domain.Project{
			{ID: "unrelated", SlugID: "duplicate", Description: wide},
			{ID: "duplicate", Name: "First project", TeamIDs: []string{"team\x00id", "", "team\x00id"}, Description: wide},
			{ID: "duplicate", Name: "Later duplicate"},
			{ID: "", Name: "Empty project"},
			{ID: "nul\x00id", Name: "NUL project", SlugID: "native\x00slug"},
		},
		Initiatives: []domain.Initiative{
			{ID: "unrelated", SlugID: "duplicate", Summary: wide},
			{ID: "duplicate", Name: "First initiative", LeadTeamID: "team\x00id", ContributingTeamIDs: []string{"", "other", "team\x00id", "other"}, Summary: wide},
			{ID: "duplicate", Name: "Later initiative"},
		},
		TeamSettings: map[string]domain.TeamSettings{
			"team\x00id": {SlackChannelID: " C-native\x00 ", SlackChannelName: wide, SlackNotifications: map[string]bool{"projectUpdates": true, "issueCreated": false, wide: true}, AgentGuidance: wide},
			"":           {SlackChannelName: " \t#empty-key\u2003 ", SlackNotifications: map[string]bool{"commentCreated": true}},
			"other":      {SlackChannelName: "#other", SlackNotifications: map[string]bool{}},
			wide:         {SlackChannelID: wide, SlackNotifications: map[string]bool{"projectUpdates": true}},
		},
	}
	requests := []SlackNotificationTeam{{"team\x00id", "projectUpdates"}, {"team\x00id", "issueCreated"}, {"", "commentCreated"}, {"other", "projectUpdates"}, {"missing", "projectUpdates"}, {"team\x00id", "projectUpdates"}}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	view.lastWorkspaceKey = "physical"
	for _, test := range []struct{ kind, id string }{{"project", "duplicate"}, {"initiative", "duplicate"}, {"project", ""}, {"project", "nul\x00id"}, {"project", "missing"}, {"project", "native\x00slug"}} {
		for _, key := range []string{"physical", ""} {
			statements := 0
			read, found, err := view.BeginSlackNotificationRead(WithSQLStatementObserver(t.Context(), func(string) { statements++ }), key)
			if err != nil || !found {
				t.Fatal(found, err)
			}
			if err := read.LoadTarget(t.Context(), test.kind, test.id); err != nil {
				t.Fatal(err)
			}
			if err := read.LoadTeams(t.Context(), requests); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(read.Data, scaleP21SlackNotificationProjection(data, test.kind, test.id, requests)) || statements != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("Slack projection/order/exact-ID semantics changed", test, statements)
			}
			if view.metadataResidency.work.used == 0 {
				t.Fatal("Slack output has no owner")
			}
			read.Data.TeamSettings["team\x00id"].SlackNotifications["projectUpdates"] = false
			read.Close()
			read.Close()
			if !reflect.DeepEqual(read.Data, domain.Bootstrap{}) || view.metadataResidency.work.used != 0 {
				t.Fatal("Slack close did not clear/release")
			}
			if !errors.Is(read.LoadTarget(t.Context(), "project", "duplicate"), errMetadataLogicalClosed) ||
				!errors.Is(read.LoadTeams(t.Context(), requests), errMetadataLogicalClosed) ||
				!errors.Is(read.ReservePosts(t.Context(), 1, 1), errMetadataLogicalClosed) {
				t.Fatal("closed Slack read accepted reuse")
			}
		}
	}
	if read, found, err := view.BeginSlackNotificationRead(t.Context(), "logical\x00workspace"); err != nil || found || read != nil {
		t.Fatal("logical key replaced physical registry key", found, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, _, err := view.BeginSlackNotificationRead(cancelled, ""); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("Slack begin ignored cancellation", err)
	}
	read, _, err := view.BeginSlackNotificationRead(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(read.LoadTarget(cancelled, "project", "duplicate"), context.Canceled) ||
		!errors.Is(read.LoadTeams(cancelled, requests), context.Canceled) {
		t.Fatal("Slack stages ignored cancellation")
	}
	read.Close()
	limit := view.metadataResidency.work.limit
	for _, budget := range []int{1, 130 << 10} {
		view.metadataResidency.work.limit = budget
		if read, _, err := view.BeginSlackNotificationRead(t.Context(), ""); read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.metadataResidency.work.used != 0 {
			t.Fatal("partial Slack begin escaped budget or leaked", budget, err)
		}
	}
	view.metadataResidency.work.limit = limit
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for range 3 {
				read, found, err := view.BeginSlackNotificationRead(t.Context(), "")
				if err != nil || !found {
					t.Error(found, err)
					return
				}
				if err := read.LoadTarget(t.Context(), "project", "duplicate"); err != nil {
					t.Error(err)
				}
				if err := read.LoadTeams(t.Context(), requests); err != nil {
					t.Error(err)
				}
				if !reflect.DeepEqual(read.Data, scaleP21SlackNotificationProjection(data, "project", "duplicate", requests)) {
					t.Error("concurrent Slack source changed")
				}
				read.Close()
			}
		})
	}
	readers.Wait()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("concurrent Slack read leaked")
	}
}

func TestScaleP21SlackNotificationPostAdmission(t *testing.T) {
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {}})
	read, _, err := view.BeginSlackNotificationRead(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	baseline := view.metadataResidency.work.used
	if err := read.ReservePosts(t.Context(), 2, 100); err != nil || view.metadataResidency.work.used != baseline+2*(64<<10)+800 {
		t.Fatal("Slack post work was not admitted before launch", err)
	}
	read.Close()
	read, _, err = view.BeginSlackNotificationRead(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = view.metadataResidency.work.used + (64 << 10) - 1
	if err := read.ReservePosts(t.Context(), 1, 0); !errors.Is(err, ErrMetadataWorkBudget) || view.metadataResidency.work.used != baseline {
		t.Fatal("Slack post budget failure partially admitted work", err)
	}
	view.metadataResidency.work.limit = limit
	for _, test := range []struct {
		count int
		bytes uint64
	}{{-1, 0}, {metadataWorkBytes/(64<<10) + 1, 0}, {1, metadataWorkBytes}} {
		if err := read.ReservePosts(t.Context(), test.count, test.bytes); !errors.Is(err, ErrMetadataWorkBudget) || view.metadataResidency.work.used != baseline {
			t.Fatal("oversized Slack post batch escaped or partially admitted", err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := read.ReservePosts(cancelled, 1, 0); !errors.Is(err, context.Canceled) || view.metadataResidency.work.used != baseline {
		t.Fatal("canceled Slack output admission changed work", err)
	}
	read.Close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("Slack post admission leaked")
	}
}

func TestScaleP21SlackNotificationSelectedBudget(t *testing.T) {
	channel := strings.Repeat("C", 200000)
	data := domain.Bootstrap{
		IntegrationConnections: []domain.IntegrationConnection{{Provider: "slack", Status: "connected", OAuthAuthorized: true}},
		TeamSettings:           map[string]domain.TeamSettings{"team": {SlackChannelID: channel, SlackNotifications: map[string]bool{"projectUpdates": true}}},
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	read, _, err := view.BeginSlackNotificationRead(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	requests := []SlackNotificationTeam{{"team", "projectUpdates"}}
	if err := read.LoadTeams(t.Context(), requests); err != nil || read.Data.TeamSettings["team"].SlackChannelID != channel {
		t.Fatal("selected large Slack scalar changed", err)
	}
	held := view.metadataResidency.work.used
	read.Close()
	view.metadataResidency.work.limit = held - 1
	read, _, err = view.BeginSlackNotificationRead(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	if err := read.LoadTeams(t.Context(), requests); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("selected Slack scalar escaped dynamic budget", err)
	}
	read.Close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("selected Slack budget failure leaked")
	}
}

func TestScaleP21SlackNotificationWarmReloadAndGenerations(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	requests := []SlackNotificationTeam{{"team", "projectUpdates"}, {"team", "issueCreated"}}
	seed := func(size int, next bool) {
		t.Helper()
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "integrationConnections", "teamSettings", "projects", "initiatives"), workspace, "scale_p21.slack_source", "", nil, func(data *domain.Bootstrap) error {
			wide := strings.Repeat("Unused Slack metadata ", size)
			data.IntegrationConnections = []domain.IntegrationConnection{{ID: "slack-first", Provider: "slack", Status: "connected", OAuthAuthorized: true, Config: map[string]string{"unrelated": wide}}}
			data.Projects = []domain.Project{{ID: "project", Name: "Source project", SlugID: "project-slug", TeamIDs: []string{"team", "team"}, Description: wide}}
			data.Initiatives = []domain.Initiative{{ID: "initiative", Name: "Source initiative", LeadTeamID: "team", ContributingTeamIDs: []string{"team"}, Summary: wide}}
			data.TeamSettings = map[string]domain.TeamSettings{"team": {SlackChannelID: "C-first", SlackChannelName: wide, SlackNotifications: map[string]bool{"projectUpdates": true, "issueCreated": false, wide: true}, AgentGuidance: wide}}
			if next {
				data.IntegrationConnections[0].ID = "slack-second"
				data.Projects[0].Name = "Replacement project"
				settings := data.TeamSettings["team"]
				settings.SlackChannelID = "C-second"
				data.TeamSettings["team"] = settings
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	expected := map[bool]int{}
	for _, size := range []int{5000, 50000} {
		seed(size, false)
		for _, reload := range []bool{false, true} {
			if reload {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			data, _ := f.WorkspaceMetadataFields(workspace, "integrationConnections", "projects", "teamSettings")
			read, found, err := view.BeginSlackNotificationRead(t.Context(), workspace)
			if err != nil || !found {
				t.Fatal(found, err)
			}
			if err := read.LoadTarget(t.Context(), "project", "project"); err != nil {
				t.Fatal(err)
			}
			if err := read.LoadTeams(t.Context(), requests); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(read.Data, scaleP21SlackNotificationProjection(data, "project", "project", requests)) {
				t.Fatal("warm/reloaded Slack source changed")
			}
			held := view.metadataResidency.work.used
			if expected[reload] == 0 {
				expected[reload] = held
			}
			if held != expected[reload] || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("unrelated body growth increased Slack admission", held, expected)
			}
			read.Close()
			if view.metadataResidency.work.used != 0 {
				t.Fatal("warm/reloaded Slack read leaked")
			}
			t.Logf("unused_repetitions=%d reload=%t held_work=%d", size, reload, held)
		}
	}
	old, _ := f.WorkspaceMetadataFields(workspace, "integrationConnections", "projects", "teamSettings")
	preflight, _, err := view.BeginSlackNotificationRead(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	parent, _, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	seed(5000, true)
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	pinned, _, err := view.BeginSlackNotificationRead(bound, workspace)
	if err != nil {
		t.Fatal(err)
	}
	parent.Close()
	if err := pinned.LoadTarget(t.Context(), "project", "project"); err != nil {
		t.Fatal(err)
	}
	if err := pinned.LoadTeams(t.Context(), requests); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pinned.Data, scaleP21SlackNotificationProjection(old, "project", "project", requests)) {
		t.Fatal("pinned Slack child changed generation after parent closed")
	}
	second, _, err := view.BeginSlackNotificationRead(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if preflight.Data.IntegrationConnections[0].ID != "slack-first" || second.Data.IntegrationConnections[0].ID != "slack-second" {
		t.Fatal("independent Slack lookup boundaries were collapsed")
	}
	preflight.Close()
	pinned.Close()
	second.Close()
	if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("Slack generation reads leaked or hydrated metadata")
	}
}
