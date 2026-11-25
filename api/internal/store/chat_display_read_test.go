package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21ChatDisplayProjection(data domain.Bootstrap, request ChatDisplayRequest) domain.Bootstrap {
	var result domain.Bootstrap
	result.WorkspaceSettings.HIPAACompliance = data.WorkspaceSettings.HIPAACompliance
	if request.Directory && data.Users != nil {
		result.Users = []domain.User{}
		for _, user := range data.Users {
			result.Users = append(result.Users, domain.User{ID: user.ID, Name: user.Name, DisplayName: user.DisplayName, Username: user.Username, Email: user.Email})
		}
	}
	if settings, found := data.UserSettings[request.UserID]; found {
		result.UserSettings = map[string]domain.UserSettings{request.UserID: {Language: settings.Language, Timezone: settings.Timezone}}
	}
	if request.Policy {
		result.Teams = make([]domain.Team, len(data.Teams))
		result.TeamSettings = map[string]domain.TeamSettings{}
		for index, team := range data.Teams {
			result.Teams[index] = domain.Team{ID: team.ID, Private: team.Private, ArchivedAt: scaleP21SW19PresenceTime(team.ArchivedAt), RetiredAt: scaleP21SW19PresenceTime(team.RetiredAt)}
		}
		for id, settings := range data.TeamSettings {
			result.TeamSettings[id] = domain.TeamSettings{ParentTeamID: settings.ParentTeamID, Access: settings.Access, MembershipRestriction: settings.MembershipRestriction}
		}
	}
	if settings, found := data.TeamSettings[request.TeamID]; found {
		if result.TeamSettings == nil {
			result.TeamSettings = map[string]domain.TeamSettings{}
		}
		selected := result.TeamSettings[request.TeamID]
		selected.TeamID, selected.Timezone = settings.TeamID, settings.Timezone
		result.TeamSettings[request.TeamID] = selected
	}
	for _, review := range data.Reviews {
		if request.ReviewRef != "" && (review.ID == request.ReviewRef || review.SlugID == request.ReviewRef) {
			result.Reviews = []domain.CodeReview{{ID: review.ID, SlugID: review.SlugID, Title: review.Title}}
			break
		}
	}
	return result
}

func TestScaleP21ChatDisplayNativeOwnership(t *testing.T) {
	large := strings.Repeat("unrelated ", 100000)
	at := time.Date(2026, 11, 14, 1, 2, 3, 0, time.UTC)
	data := domain.Bootstrap{
		Users: []domain.User{
			{ID: "duplicate", Name: "First", DisplayName: "First display", Email: "first@example.test", Username: "first\x00handle", JobTitle: large},
			{ID: "duplicate", Name: "Second"}, {ID: "", Name: "Empty identity"},
		},
		UserSettings: map[string]domain.UserSettings{
			"":           {Language: "zh", Timezone: "Asia/Shanghai", AgentInstructions: large},
			"user\x00id": {Language: "system", Timezone: "America/Los_Angeles", AgentInstructions: large},
		},
		Teams:        []domain.Team{{ID: "team\x00id", Private: true, RetiredAt: &at}, {ID: "team\x00id", ArchivedAt: &at}, {ID: ""}},
		TeamSettings: map[string]domain.TeamSettings{"": {Timezone: "UTC"}, "team\x00id": {TeamID: "native-id", Timezone: "Asia/Tokyo", ParentTeamID: "parent", Access: "private", MembershipRestriction: "owners", AgentGuidance: large}},
		Reviews: []domain.CodeReview{
			{ID: "first", SlugID: "current", Title: "First title", Description: large},
			{ID: "current", SlugID: "second", Title: "Later ID match"},
		},
		Documents: []domain.Document{{ID: "unrelated", Content: large}},
	}
	data.WorkspaceSettings.HIPAACompliance = true
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	view.lastWorkspaceKey = "physical"
	for _, request := range []ChatDisplayRequest{
		{Directory: true, UserID: "user\x00id", TeamID: "team\x00id", ReviewRef: "current"},
		{Directory: true, Policy: true, UserID: "user\x00id", TeamID: "team\x00id", ReviewRef: "current"},
		{Directory: true}, {UserID: "missing", TeamID: "missing", ReviewRef: "missing"},
	} {
		for _, key := range []string{"physical", ""} {
			sql := 0
			read, found, err := view.BeginChatDisplayRead(WithSQLStatementObserver(t.Context(), func(string) { sql++ }), key, request)
			if err != nil || !found {
				t.Fatal(found, err)
			}
			want := scaleP21ChatDisplayProjection(data, request)
			if !reflect.DeepEqual(read.Data, want) || sql != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("installed display projection changed", request, sql)
			}
			if view.metadataResidency.work.used == 0 {
				t.Fatal("display output has no owner")
			}
			if len(read.Data.Users) > 0 {
				read.Data.Users[0].Name = "Caller changed"
			}
			read.Close()
			read.Close()
			if !reflect.DeepEqual(read.Data, domain.Bootstrap{}) || view.metadataResidency.work.used != 0 {
				t.Fatal("display close did not release")
			}
		}
	}
	for _, key := range []string{"missing", "physical "} {
		read, found, err := view.BeginChatDisplayRead(t.Context(), key, ChatDisplayRequest{})
		if err != nil || found || read != nil {
			t.Fatal("missing registry selection changed", key, found, err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, _, err := view.BeginChatDisplayRead(cancelled, "", ChatDisplayRequest{Directory: true}); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
	limit := view.metadataResidency.work.limit
	for _, size := range []int{1, 130 << 10} {
		view.metadataResidency.work.limit = size
		if read, _, err := view.BeginChatDisplayRead(t.Context(), "", ChatDisplayRequest{Directory: true}); read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.metadataResidency.work.used != 0 {
			t.Fatal("partial projection escaped budget or leaked", size, err)
		}
	}
	view.metadataResidency.work.limit = limit
	if read, _, err := view.BeginChatDisplayRead(t.Context(), "", ChatDisplayRequest{Directory: true}); err != nil {
		t.Fatal(err)
	} else if err := read.TransferOutput(t.Context()); !errors.Is(err, ErrBoundedMetadataCoverage) || view.metadataResidency.work.used != 0 {
		t.Fatal("unowned output escaped or leaked", err)
	}
	var releases []func()
	ctx, cancel := context.WithCancel(WithMetadataOutputOwner(t.Context(), func(close func()) bool {
		releases = append(releases, close)
		return true
	}))
	read, _, err := view.BeginChatDisplayRead(ctx, "", ChatDisplayRequest{Directory: true})
	if err != nil || read.TransferOutput(ctx) != nil {
		t.Fatal("output transfer failed", err)
	}
	cancel()
	if view.metadataResidency.work.used == 0 {
		t.Fatal("cancellation released retained output")
	}
	if _, err := json.Marshal(read.Data.Users); err != nil {
		t.Fatal("retained output cannot encode", err)
	}
	for _, release := range releases {
		release()
	}
	if err := read.LoadReview(t.Context(), "current"); !errors.Is(err, errMetadataLogicalClosed) || view.metadataResidency.work.used != 0 {
		t.Fatal("transferred owner did not close", err)
	}
	var jobs sync.WaitGroup
	for range 4 {
		jobs.Go(func() {
			for range 3 {
				read, found, err := view.BeginChatDisplayRead(t.Context(), "", ChatDisplayRequest{Directory: true})
				if err != nil || !found || !reflect.DeepEqual(read.Data, scaleP21ChatDisplayProjection(data, ChatDisplayRequest{Directory: true})) {
					t.Error("concurrent installed display changed", err)
					return
				}
				read.Close()
			}
		})
	}
	jobs.Wait()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("concurrent display read leaked")
	}
}

func TestScaleP21ChatDisplayComposedGeneration(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	at := time.Date(2026, 11, 14, 1, 2, 3, 0, time.UTC)
	const projectID, initiativeID, teamID = "composed-project", "composed-initiative", "composed-team"
	seed := func(name string, private bool) {
		t.Helper()
		if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.chat_composition", "", nil, func(data *domain.Bootstrap) error {
			data.Workspace.Name = name
			data.WorkspaceSettings.HIPAACompliance = private
			for index := range data.Users {
				data.Users[index].DisplayName = name
			}
			data.Teams = []domain.Team{{ID: teamID, Private: private}}
			if private {
				data.Teams[0].RetiredAt, data.Teams[0].ArchivedAt = &at, &at
			}
			data.TeamSettings = map[string]domain.TeamSettings{teamID: {TeamID: teamID, Timezone: name, ParentTeamID: name, Access: name, MembershipRestriction: name}}
			data.UserSettings[f.data.Viewer.ID] = domain.UserSettings{Language: name, Timezone: name}
			data.Projects = []domain.Project{{ID: projectID, Name: name, TeamIDs: []string{teamID}}}
			data.Initiatives = []domain.Initiative{{ID: initiativeID, Name: name, LeadTeamID: teamID, Health: name}}
			data.ProjectUpdates[projectID] = []domain.ProjectUpdate{{ID: "composed-update", ProjectID: projectID, Body: name, CreatedAt: at}}
			data.Reviews = []domain.CodeReview{{ID: "composed-review", SlugID: "composed-slug", Title: name}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed("Original", false)
	old, _ := f.WorkspaceMetadata(workspace)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	owner, found, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer owner.Close()
	bound, err := owner.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	replace := func(name string) {
		t.Helper()
		seed(name, true)
		if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
			t.Fatal(err)
		}
	}
	replace("Replacement")
	pulse, found, err := view.BeginPulseRead(bound, workspace, false)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer pulse.Close()
	policy, found, err := view.BeginChatDisplayRead(bound, workspace, ChatDisplayRequest{Policy: true})
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer policy.Close()
	if !reflect.DeepEqual(policy.Data, scaleP21ChatDisplayProjection(old, ChatDisplayRequest{Policy: true})) {
		t.Fatal("composed policy switched installed generation")
	}
	replace("Later replacement")
	request := ChatDisplayRequest{Directory: true, UserID: f.data.Viewer.ID, TeamID: teamID}
	display, found, err := view.BeginChatDisplayRead(bound, workspace, request)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer display.Close()
	owner.Close()
	if err := pulse.LoadSource(t.Context(), "project", projectID); err != nil {
		t.Fatal(err)
	}
	if err := pulse.LoadUpdates(t.Context(), "project", projectID, []string{"composed-update"}); err != nil {
		t.Fatal(err)
	}
	if err := display.LoadReview(t.Context(), "composed-slug"); err != nil {
		t.Fatal(err)
	}
	request.ReviewRef = "composed-slug"
	if !reflect.DeepEqual(display.Data, scaleP21ChatDisplayProjection(old, request)) {
		t.Fatal("composed display switched installed generation")
	}
	scaleP21HotPulseWireEqual(t, pulse.Snapshot.Workspace, old.Workspace)
	scaleP21HotPulseWireEqual(t, pulse.Snapshot.Projects, old.Projects)
	scaleP21HotPulseWireEqual(t, pulse.Snapshot.Initiatives[0].Name, old.Initiatives[0].Name)
	scaleP21HotPulseWireEqual(t, pulse.Snapshot.ProjectUpdates[projectID], old.ProjectUpdates[projectID])
	display.Close()
	policy.Close()
	pulse.Close()
	if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("composed generation leaked work or decoded full metadata")
	}
}

func TestScaleP21ChatDisplayPinnedReview(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	seed := func(title string) {
		t.Helper()
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "reviews"), workspace, "scale_p21.chat_review_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Reviews = []domain.CodeReview{{ID: "pinned-review", SlugID: "pinned-current", Title: title}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed("Original installed title")
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	read, _, err := view.BeginChatDisplayRead(t.Context(), workspace, ChatDisplayRequest{})
	if err != nil {
		t.Fatal(err)
	}
	seed("Later title")
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	if err := read.LoadReview(t.Context(), "pinned-current"); err != nil || len(read.Data.Reviews) != 1 || read.Data.Reviews[0].Title != "Original installed title" {
		t.Fatal("deferred review selected a newer generation", err)
	}
	read.Close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("pinned review leaked")
	}
}

func TestScaleP21ChatDisplayWarmReloadBodyBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	request := ChatDisplayRequest{Directory: true, UserID: f.data.Viewer.ID, TeamID: "display-team", ReviewRef: "display-review"}
	expectedWork := map[bool]int{}
	expectedPolicyWork := map[bool]int{}
	for _, size := range []int{10000, 100000} {
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "users", "userSettings", "teamSettings", "reviews"), workspace, "scale_p21.chat_display_seed", "", nil, func(data *domain.Bootstrap) error {
			for index := range data.Users {
				data.Users[index].JobTitle = strings.Repeat("unused profile ", size)
			}
			data.UserSettings[request.UserID] = domain.UserSettings{Language: "zh-CN", Timezone: "Asia/Shanghai", AgentInstructions: strings.Repeat("unused user setting ", size)}
			data.TeamSettings[request.TeamID] = domain.TeamSettings{TeamID: request.TeamID, Timezone: "America/Los_Angeles", AgentGuidance: strings.Repeat("unused team setting ", size)}
			data.Reviews = []domain.CodeReview{{ID: request.ReviewRef, SlugID: "display-current-slug", Title: "Display review", Description: strings.Repeat("unused review ", size)}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			if reload {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			data, _ := f.WorkspaceMetadata(workspace)
			read, found, err := view.BeginChatDisplayRead(t.Context(), workspace, request)
			if err != nil || !found || !reflect.DeepEqual(read.Data, scaleP21ChatDisplayProjection(data, request)) {
				t.Fatal("warm/reloaded display changed", size, reload, err)
			}
			held := view.metadataResidency.work.used
			if expectedWork[reload] == 0 {
				expectedWork[reload] = held
			}
			if held != expectedWork[reload] || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("unrelated bodies increased display admission", size, reload, held, expectedWork)
			}
			policyRequest := ChatDisplayRequest{Policy: true, TeamID: request.TeamID}
			policy, found, err := view.BeginChatDisplayRead(t.Context(), workspace, policyRequest)
			if err != nil || !found || !reflect.DeepEqual(policy.Data, scaleP21ChatDisplayProjection(data, policyRequest)) {
				t.Fatal("warm/reloaded policy changed", size, reload, err)
			}
			policyHeld := view.metadataResidency.work.used - held
			if expectedPolicyWork[reload] == 0 {
				expectedPolicyWork[reload] = policyHeld
			}
			if policyHeld != expectedPolicyWork[reload] {
				t.Fatal("unrelated bodies increased policy admission", size, reload, policyHeld, expectedPolicyWork)
			}
			policy.Close()
			read.Close()
			if view.metadataResidency.work.used != 0 {
				t.Fatal("warm/reloaded display leaked")
			}
			t.Logf("body_repetitions=%d reload=%t display_work=%d policy_work=%d", size, reload, held, policyHeld)
		}
	}
}
