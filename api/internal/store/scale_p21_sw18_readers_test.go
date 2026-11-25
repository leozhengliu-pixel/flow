package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW18RootCollection(t *testing.T, repo *SQLiteStore, workspace, field string, value json.RawMessage) {
	t.Helper()
	var raw []byte
	if err := repo.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	shapes := map[string]string{}
	if err := json.Unmarshal(root[metadataCollectionsKey], &shapes); err != nil {
		t.Fatal(err)
	}
	delete(shapes, field)
	root[metadataCollectionsKey], _ = json.Marshal(shapes)
	root[field] = value
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, raw, workspace); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP21SW18WebhookCredentials(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	target := domain.IntegrationConnection{
		ID: idgen.New("sw18-hook-"), Provider: "github", Status: "connected",
		Config: map[string]string{"webhookSecret": "null", "webhookSecretEnv": "FLOW_INTEGRATION_SW18_WEBHOOK", "signingSecret": "数据🔑", "signingSecretEnv": "FLOW_INTEGRATION_SW18_SLACK", "username": "MixedCase", "ignored": strings.Repeat("legal ignored config ", 20000)},
	}
	connections := []domain.IntegrationConnection{
		{ID: idgen.New("sw18-wrong-provider-"), Provider: "GitHub", Status: "connected"},
		{ID: idgen.New("sw18-wrong-status-"), Provider: "github", Status: "Connected"},
		{ID: idgen.New("sw18-disabled-"), Provider: "github", Status: "disabled"},
		target,
		{ID: idgen.New("sw18-later-"), Provider: "github", Status: "configured", Config: map[string]string{"webhookSecret": "later"}},
	}
	for range 75 {
		connections = append(connections, domain.IntegrationConnection{ID: idgen.New("sw18-other-"), Provider: "unused", Status: "connected", Config: map[string]string{"ignored": "retained"}})
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "integrationConnections"), workspace, "integration.updated", "", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections = connections
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := IntegrationWebhookCredentials{target.ID, "null", "FLOW_INTEGRATION_SW18_WEBHOOK", "数据🔑", "FLOW_INTEGRATION_SW18_SLACK", "MixedCase"}
	for _, selectedWorkspace := range []string{workspace, ""} {
		got, found, release, err := view.BeginIntegrationWebhookCredentials(t.Context(), selectedWorkspace, "github")
		if err != nil || !found || got != want {
			release()
			t.Fatalf("selected=%v found=%v err=%v", got.ID, found, err)
		}
		if view.metadataResidency.work.used == 0 {
			t.Fatal("returned credentials lost their work allowance before verification")
		}
		release()
		release()
		if view.metadataResidency.work.used != 0 {
			t.Fatal("credential lease was not released exactly once")
		}
	}
	for _, selected := range []struct{ workspace, provider string }{
		{workspace + "-missing", "github"}, {workspace, "GITHUB"}, {workspace, "missing"},
		{workspace + "\x00", "github"}, {workspace, "github\x00"},
	} {
		_, found, release, err := view.BeginIntegrationWebhookCredentials(t.Context(), selected.workspace, selected.provider)
		release()
		if err != nil || found {
			t.Fatalf("missing credential selection: found=%v err=%v", found, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, release, err := view.BeginIntegrationWebhookCredentials(ctx, workspace, "github")
	release()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled credential read: %v", err)
	}
	for _, config := range []map[string]string{nil, {}, {"webhookSecret": strings.Repeat("large selected secret 数据 ", 20000)}} {
		target.Config = config
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "integrationConnections"), workspace, "integration.updated", target.ID, nil, func(data *domain.Bootstrap) error {
			data.IntegrationConnections[3] = target
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		got, found, release, err := view.BeginIntegrationWebhookCredentials(t.Context(), workspace, "github")
		if err != nil || !found || got.WebhookSecret != config["webhookSecret"] {
			release()
			t.Fatal("complete selected scalar read failed", err)
		}
		release()
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 128 << 10
	_, _, release, err = view.BeginIntegrationWebhookCredentials(t.Context(), workspace, "github")
	release()
	view.metadataResidency.work.limit = limit
	if !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("large selected secret bypassed process work cap: %v", err)
	}
	_, found, release, err := view.BeginIntegrationWebhookCredentials(t.Context(), workspace, "github")
	release()
	if err != nil || !found || view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("credential read did not recover or decoded complete metadata", err)
	}
	scaleP21SW18RootCollection(t, f.SQLiteStore, workspace, "integrationConnections", json.RawMessage(`null`))
	_, found, release, err = view.BeginIntegrationWebhookCredentials(t.Context(), workspace, "github")
	release()
	if err != nil || found {
		t.Fatal("inactive root collection exposed stale connection rows", err)
	}
	scaleP21SW18RootCollection(t, f.SQLiteStore, workspace, "integrationConnections", json.RawMessage(`[{"id":"unsplit","provider":"github","status":"connected"}]`))
	_, _, release, err = view.BeginIntegrationWebhookCredentials(t.Context(), workspace, "github")
	release()
	if !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("unsplit nonempty connections were treated as empty state", err)
	}
}

func TestScaleP21SW18TeamResourceVisitors(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, teamID := f.data.Workspace.URLKey, idgen.New("team_sw18_")
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	sections := []domain.TeamResourceSection{}
	resources := []domain.TeamPinnedResource{}
	for i := range 125 {
		position := float64(i % 4)
		if i%3 == 0 {
			position = -position
		}
		if i > 60 && i < 65 {
			position = math.Nextafter(1, 2)
		}
		sections = append(sections, domain.TeamResourceSection{ID: fmt.Sprintf("sw18-section-%03d", i), TeamID: teamID, Name: fmt.Sprint(i), Position: position, CreatedAt: at, UpdatedAt: at})
		resources = append(resources, domain.TeamPinnedResource{ID: fmt.Sprintf("sw18-resource-%03d", i), TeamID: teamID, SectionID: sections[i].ID, ResourceType: "link", ResourceID: "private-reference", URL: "https://example.test/<value>?a=1&b=2", Title: fmt.Sprint(i), Position: position, CreatedAt: at, UpdatedAt: at})
	}
	sections[20].Name = strings.Repeat("legal selected section 数据 <&> ", 25000)
	resources[40].Title = strings.Repeat("legal selected resource 数据 <&> ", 25000)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams", "teamResourceSections", "teamPinnedResources"), workspace, "team.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: teamID, Key: "SW18"})
		data.TeamResourceSections = append(slices.Clone(sections), domain.TeamResourceSection{ID: "sw18-hidden-section", TeamID: "other", Name: strings.Repeat("unrelated ", 30000)})
		data.TeamPinnedResources = append(slices.Clone(resources), domain.TeamPinnedResource{ID: "sw18-hidden-resource", TeamID: "other", Title: strings.Repeat("unrelated ", 30000)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	slices.SortStableFunc(sections, func(a, b domain.TeamResourceSection) int {
		if a.Position < b.Position {
			return -1
		}
		if a.Position > b.Position {
			return 1
		}
		return 0
	})
	slices.SortStableFunc(resources, func(a, b domain.TeamPinnedResource) int {
		if a.Position < b.Position {
			return -1
		}
		if a.Position > b.Position {
			return 1
		}
		return 0
	})
	read, workspaceFound, teamFound, err := view.BeginTeamResourceRead(t.Context(), workspace, teamID)
	if err != nil || !workspaceFound || !teamFound {
		t.Fatal("begin team resource read", err)
	}
	if view.metadataResidency.work.used < 3<<20 {
		t.Fatal("large selected rows did not receive an adaptive work allowance")
	}
	gotSections := []domain.TeamResourceSection{}
	gotResources := []domain.TeamPinnedResource{}
	if err := read.VisitSections(func(value domain.TeamResourceSection) error {
		gotSections = append(gotSections, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := read.VisitResources(func(value domain.TeamPinnedResource) error {
		gotResources = append(gotResources, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := read.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sections, gotSections) || !reflect.DeepEqual(resources, gotResources) {
		t.Fatal("typed visitors changed row values or stable position ordering")
	}
	read, _, _, err = view.BeginTeamResourceRead(t.Context(), workspace, teamID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teamPinnedResources"), workspace, "team.resource_updated", "", nil, func(data *domain.Bootstrap) error {
		data.TeamPinnedResources[0].Title = "Changed after read snapshot"
		return nil
	}); err != nil {
		_ = read.Close()
		t.Fatal("update beside read snapshot", err)
	}
	snapshotResources := []domain.TeamPinnedResource{}
	if err := read.VisitResources(func(value domain.TeamPinnedResource) error {
		snapshotResources = append(snapshotResources, value)
		return nil
	}); err != nil {
		_ = read.Close()
		t.Fatal(err)
	}
	_ = read.Close()
	if !reflect.DeepEqual(snapshotResources, resources) {
		t.Fatal("resource visitor mixed revisions after a concurrent canonical update")
	}
	for _, selected := range []struct {
		workspace, team string
		workspaceFound  bool
	}{
		{workspace + "-missing", teamID, false},
		{workspace + "\x00", teamID, false},
		{workspace, teamID + "-missing", true},
		{workspace, strings.ToUpper(teamID), true},
		{workspace, teamID + "\x00", true},
	} {
		read, workspaceFound, teamFound, err := view.BeginTeamResourceRead(t.Context(), selected.workspace, selected.team)
		if err != nil || read != nil || workspaceFound != selected.workspaceFound || teamFound {
			t.Fatalf("missing resource identity: workspace=%v team=%v err=%v", workspaceFound, teamFound, err)
		}
	}
	read, _, _, err = view.BeginTeamResourceRead(t.Context(), "", teamID)
	if err != nil || read == nil {
		t.Fatal("default workspace read", err)
	}
	sentinel := errors.New("writer stopped")
	if err := read.VisitSections(func(domain.TeamResourceSection) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal("visitor error was lost", err)
	}
	_ = read.Close()
	foreign, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "SW18 other team resources", idgen.New("sw18-resource-workspace-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.DeleteWorkspace(context.Background(), foreign.Workspace.URLKey); err != nil {
			t.Error(err)
		}
	})
	read, workspaceFound, teamFound, err = view.BeginTeamResourceRead(t.Context(), foreign.Workspace.URLKey, teamID)
	if err != nil || read != nil || !workspaceFound || teamFound {
		t.Fatal("team ID escaped its workspace", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	read, _, _, err = view.BeginTeamResourceRead(ctx, workspace, teamID)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := read.VisitSections(func(domain.TeamResourceSection) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled visitor continued", err)
	}
	_ = read.Close()
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 3 << 20
	read, _, _, err = view.BeginTeamResourceRead(t.Context(), workspace, teamID)
	view.metadataResidency.work.limit = limit
	if read != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("large selected row was silently omitted instead of rejected", err)
	}
	read, _, _, err = view.BeginTeamResourceRead(t.Context(), workspace, teamID)
	if err != nil {
		t.Fatal("read after budget rejection", err)
	}
	_ = read.Close()
	if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("team resource visitors leaked work or decoded complete metadata")
	}
	scaleP21SW18RootCollection(t, f.SQLiteStore, workspace, "teamResourceSections", json.RawMessage(`[]`))
	scaleP21SW18RootCollection(t, f.SQLiteStore, workspace, "teamPinnedResources", json.RawMessage(`null`))
	read, _, _, err = view.BeginTeamResourceRead(t.Context(), workspace, teamID)
	if err != nil {
		t.Fatal(err)
	}
	stale := 0
	if err := read.VisitSections(func(domain.TeamResourceSection) error { stale++; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := read.VisitResources(func(domain.TeamPinnedResource) error { stale++; return nil }); err != nil {
		t.Fatal(err)
	}
	_ = read.Close()
	if stale != 0 {
		t.Fatal("inactive root collections exposed stale resource rows")
	}
	scaleP21SW18RootCollection(t, f.SQLiteStore, workspace, "teamResourceSections", json.RawMessage(`[{"id":"unsplit"}]`))
	read, _, _, err = view.BeginTeamResourceRead(t.Context(), workspace, teamID)
	if read != nil || !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("unsplit nonempty resource collection became empty state", err)
	}
}

func TestScaleP21SW18TeamResourceTypedDefaults(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, teamID, sectionID := f.data.Workspace.URLKey, idgen.New("team_sw18_"), idgen.New("sw18-schema-section-")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams", "teamResourceSections"), workspace, "team.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: teamID, Key: "SW18"})
		data.TeamResourceSections = []domain.TeamResourceSection{{ID: sectionID, TeamID: teamID}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"id": sectionID, "teamId": teamID,
		"futureField": map[string]any{"reference": "not-a-current-field", "body": strings.Repeat("legal future data ", 20000)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='teamResourceSections' AND record_key=?`, raw, workspace, sectionID); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	read, workspaceFound, teamFound, err := view.BeginTeamResourceRead(t.Context(), workspace, teamID)
	if err != nil || !workspaceFound || !teamFound {
		t.Fatal(err)
	}
	count := 0
	if err := read.VisitSections(func(value domain.TeamResourceSection) error {
		count++
		if value != (domain.TeamResourceSection{ID: sectionID, TeamID: teamID}) {
			t.Fatal("mixed-version source changed the typed field defaults")
		}
		encoded, err := json.Marshal(value)
		if err != nil || strings.Contains(string(encoded), "futureField") || strings.Contains(string(encoded), "not-a-current-field") {
			t.Fatal("visitor exposed unknown canonical fields", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = read.Close()
	if count != 1 || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("typed mixed-version visitor omitted its record, decoded metadata or leaked work")
	}
}
