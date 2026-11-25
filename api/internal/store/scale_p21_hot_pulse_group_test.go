package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21HotPulseWireEqual(t *testing.T, got, want any) {
	t.Helper()
	actual, actualErr := json.Marshal(got)
	expected, expectedErr := json.Marshal(want)
	if (actualErr == nil) != (expectedErr == nil) || !bytes.Equal(actual, expected) {
		t.Fatalf("Pulse wire bytes/error changed: actual=%s expected=%s errors=%v/%v", actual, expected, actualErr, expectedErr)
	}
	if actualErr != nil && actualErr.Error() != expectedErr.Error() {
		t.Fatal("Pulse wire encoding error changed", actualErr, expectedErr)
	}
}

func scaleP21HotPulseCompactIndexEqual(got, want []*PulseFeedEntry) bool {
	if len(got) != len(want) {
		return false
	}
	for index, entry := range got {
		expected := *want[index]
		expected.Search = ""
		if entry.Search != "" || !reflect.DeepEqual(*entry, expected) {
			return false
		}
	}
	return true
}

func TestScaleP21HotPulseGroupNativeOwnership(t *testing.T) {
	now := time.Date(2026, 11, 14, 1, 2, 3, 4, time.UTC)
	user := domain.User{ID: "user", Name: "Native", DisplayName: "Native Person", Email: "native@example.test"}
	data := domain.Bootstrap{
		Workspace:    domain.Workspace{ID: "workspace", URLKey: "logical"},
		Projects:     []domain.Project{{ID: "p\x00id", Name: "Native", Lead: &user, TeamIDs: []string{"private"}, Description: strings.Repeat("skipped ", 300000)}, {ID: "p\x00id", Name: "Duplicate"}},
		Initiatives:  []domain.Initiative{{ID: "initiative", ProjectIDs: []string{"p\x00id"}, Owner: &user}},
		Teams:        []domain.Team{{ID: "private", Private: true}},
		TeamSettings: map[string]domain.TeamSettings{"private": {ParentTeamID: "parent", Access: "private", AgentGuidance: strings.Repeat("skipped ", 300000)}},
		UserSettings: map[string]domain.UserSettings{"user": {FeedLastSeenTime: "1730000000000", AgentInstructions: strings.Repeat("skipped ", 300000)}},
		Users:        []domain.User{user},
		ProjectUpdates: map[string][]domain.ProjectUpdate{"p\x00id": {
			{ID: "first", Body: "Hello @NativePerson", CreatedAt: now, User: user, Comments: []domain.Comment{{ID: "comment", Body: strings.Repeat("complete comment ", 50000)}}, Reactions: map[string][]string{"like": {"one", "two"}}, Snapshot: &domain.PulseSnapshot{Status: "original"}},
			{ID: "second", Body: "Body", CreatedAt: now.Add(-time.Hour), User: user, BodyData: map[string]any{"type": "userMention", "id": "user"}},
		}},
		InitiativeUpdates: map[string][]domain.InitiativeUpdate{"initiative": {{ID: "initiative-update", Body: "Initiative", User: user, CreatedAt: now.Add(-time.Minute)}}},
	}
	literal := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"physical": data}, lastWorkspaceKey: "physical"}
	view := scaleP21HotLogoStore(t, literal.workspaces)
	view.lastWorkspaceKey = "physical"
	want, _ := literal.PulseFeed("")
	read, found, err := view.BeginPulseRead(t.Context(), "", true)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if !scaleP21HotPulseCompactIndexEqual(read.Snapshot.Recent, want.Recent) || !scaleP21HotPulseCompactIndexEqual(read.Snapshot.Popular, want.Popular) {
		t.Fatal("native ordered index, mentions, author or engagement changed")
	}
	if read.Snapshot.ProjectUpdates != nil || read.Snapshot.InitiativeUpdates != nil || read.Snapshot.Projects[0].Description != "" {
		t.Fatal("source read retained full details")
	}
	if err := read.LoadUserSettings(t.Context(), "user"); err != nil || read.Snapshot.UserSettings["user"].FeedLastSeenTime != data.UserSettings["user"].FeedLastSeenTime {
		t.Fatal("last-seen installed point changed", err)
	}
	if err := read.LoadUpdates(t.Context(), "project", "p\x00id", []string{"first"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read.Snapshot.ProjectUpdates["p\x00id"], data.ProjectUpdates["p\x00id"][:1]) {
		t.Fatal("selected detail lost comments, reactions, body or snapshot")
	}
	previous, err := read.LoadLatestSnapshot(t.Context(), "project", "p\x00id")
	if err != nil || !reflect.DeepEqual(previous, data.ProjectUpdates["p\x00id"][0].Snapshot) {
		t.Fatal("composer snapshot changed", err)
	}
	if err := read.LoadSource(t.Context(), "project", "p\x00id"); err != nil || !reflect.DeepEqual(read.Snapshot.Projects, data.Projects) {
		t.Fatal("full aggregate lost duplicate/order/native detail", err)
	}
	if view.metadataResidency.work.used == 0 {
		t.Fatal("output released before encoding/use")
	}
	read.Snapshot.ProjectUpdates["p\x00id"][0].Comments[0].Body = "changed"
	read.Snapshot.Projects[0].Lead.Name = "changed"
	read.Close()
	read.Close()
	if read.Snapshot != nil || view.metadataResidency.work.used != 0 {
		t.Fatal("close did not release owned state")
	}
	again, _, err := view.BeginPulseRead(t.Context(), "physical", false)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if err := again.LoadUpdates(t.Context(), "project", "p\x00id", nil); err != nil ||
		!reflect.DeepEqual(again.Snapshot.ProjectUpdates["p\x00id"], data.ProjectUpdates["p\x00id"]) {
		t.Fatal("caller modified installed data or full history was truncated", err)
	}
	before := view.metadataResidency.work.used
	count := 0
	if err := again.VisitUpdates(t.Context(), "project", "p\x00id", func(value any) error {
		update := value.(domain.ProjectUpdate)
		if !reflect.DeepEqual(update, data.ProjectUpdates["p\x00id"][count]) || view.metadataResidency.work.used <= before {
			t.Fatal("streamed detail changed or was not charged during use")
		}
		count++
		return nil
	}); err != nil || count != 2 || view.metadataResidency.work.used != before {
		t.Fatal("streaming retained decoded bodies", count, err)
	}
	page, _, err := view.BeginPulseRead(t.Context(), "physical", false)
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	total, err := page.LoadUpdatePage(t.Context(), "project", "p\x00id", 1, 1)
	if err != nil || total != 2 || !reflect.DeepEqual(page.Snapshot.ProjectUpdates["p\x00id"], data.ProjectUpdates["p\x00id"][1:]) {
		t.Fatal("native page lost count/order or decoded other details", total, err)
	}
}

func TestScaleP21HotPulseGroupBoundsAndInstalledGeneration(t *testing.T) {
	data := domain.Bootstrap{Workspace: domain.Workspace{URLKey: "logical"}, Projects: []domain.Project{{ID: "p"}},
		ProjectUpdates: map[string][]domain.ProjectUpdate{"p": {{ID: "old", Body: "original"}}}}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	read, found, err := view.BeginPulseRead(t.Context(), "physical", false)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	limit, held := view.metadataResidency.work.limit, view.metadataResidency.work.used
	view.metadataResidency.work.limit = held + 1
	if err := read.LoadUpdates(t.Context(), "project", "p", nil); !errors.Is(err, ErrMetadataWorkBudget) || view.metadataResidency.work.used != held {
		t.Fatal("detail quota error leaked work or returned a partial update", err)
	}
	view.metadataResidency.work.limit = limit
	data.ProjectUpdates = map[string][]domain.ProjectUpdate{"p": {{ID: "new", Body: "new"}}}
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource("physical", source)
	if err := read.LoadUpdates(t.Context(), "project", "p", nil); err != nil || read.Snapshot.ProjectUpdates["p"][0].ID != "old" {
		t.Fatal("read followed a new installed generation", err)
	}
	read.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := view.BeginPulseRead(ctx, "physical", true); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	limit = view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	if _, found, err := view.BeginPulseRead(t.Context(), "physical", true); !found || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("quota became a missing workspace", found, err)
	}
	if view.metadataResidency.work.used != 0 {
		t.Fatal("quota error leaked work")
	}
	view.metadataResidency.work.limit = limit
	if _, found, err := view.BeginPulseRead(t.Context(), "logical", true); err != nil || found {
		t.Fatal("logical alias substituted for physical installed key", found, err)
	}
	if view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("pulse decoded complete metadata")
	}
}

func TestScaleP21HotPulseGroupScheduleProjection(t *testing.T) {
	cursor := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	data := domain.Bootstrap{Workspace: domain.Workspace{URLKey: "logical"},
		Settings:     map[string]any{"pulseDeliveryCursors": map[string]time.Time{"user": cursor}, "unrelated": strings.Repeat("skipped ", 500000)},
		UserSettings: map[string]domain.UserSettings{"user": {UserID: "different", PulseSchedule: "weekly", Timezone: "Asia/Tokyo", AgentInstructions: strings.Repeat("skipped ", 500000)}},
		TeamSettings: map[string]domain.TeamSettings{"team": {Timezone: "Europe/London", AgentGuidance: strings.Repeat("skipped ", 500000)}},
		Projects:     []domain.Project{{ID: "p", Description: strings.Repeat("skipped ", 500000)}},
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	read, found, err := view.BeginPulseScheduleRead(t.Context(), "physical")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer read.Close()
	if len(read.Snapshot.UserSettings) != 0 || len(read.Snapshot.Projects) != 0 {
		t.Fatal("scheduler feature probe loaded unrelated collections")
	}
	if err := read.LoadSchedule(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read.Snapshot.Settings["pulseDeliveryCursors"], data.Settings["pulseDeliveryCursors"]) ||
		read.Snapshot.UserSettings["user"].PulseSchedule != "weekly" ||
		read.Snapshot.UserSettings["user"].Timezone != "Asia/Tokyo" ||
		read.Snapshot.TeamSettings["team"].Timezone != "Europe/London" ||
		read.Snapshot.UserSettings["user"].AgentInstructions != "" || len(read.Snapshot.Projects) != 0 {
		t.Fatal("scheduler scalar/native cursor projection changed")
	}
	held := view.metadataResidency.work.used
	if held > 512<<10 {
		t.Fatal("ignored scheduler bodies affected held work", held)
	}
	read.Close()
	settings := data.UserSettings["user"]
	settings.AgentInstructions = strings.Repeat("skipped ", 50000)
	data.UserSettings["user"] = settings
	teamSettings := data.TeamSettings["team"]
	teamSettings.AgentGuidance = strings.Repeat("skipped ", 50000)
	data.TeamSettings["team"] = teamSettings
	data.Settings["unrelated"] = strings.Repeat("skipped ", 50000)
	data.Projects[0].Description = strings.Repeat("skipped ", 50000)
	small := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	smallRead, _, err := small.BeginPulseScheduleRead(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	defer smallRead.Close()
	if err := smallRead.LoadSchedule(t.Context()); err != nil || small.metadataResidency.work.used != held {
		t.Fatal("10x unrelated body scaling changed scheduling work", err, small.metadataResidency.work.used, held)
	}
	t.Logf("scheduler ignored-body 1x/10x held_work=%d/%d", small.metadataResidency.work.used, held)
}

func TestScaleP21HotPulseGroupWarmReload(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, id := f.data.Workspace.URLKey, idgen.New("pulse-group-")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects", "projectUpdates", "userSettings"), workspace, "scale_p21.pulse_group_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: id, Name: "Owned", Description: strings.Repeat("unrelated ", 300000)})
		data.ProjectUpdates[id] = []domain.ProjectUpdate{{ID: id + "-update", ProjectID: id, User: data.Viewer, Body: "Full body", Comments: []domain.Comment{{ID: "comment", Body: "Full comment"}}, Reactions: map[string][]string{"like": {data.Viewer.ID}}, CreatedAt: time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)}}
		settings := data.UserSettings[data.Viewer.ID]
		settings.FeedLastSeenTime = "1730000000000"
		data.UserSettings[data.Viewer.ID] = settings
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
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := f.PulseFeed(workspace)
		read, found, err := view.BeginPulseRead(t.Context(), workspace, true)
		if err != nil || !found {
			t.Fatal(found, err)
		}
		if !scaleP21HotPulseCompactIndexEqual(read.Snapshot.Recent, want.Recent) || !scaleP21HotPulseCompactIndexEqual(read.Snapshot.Popular, want.Popular) {
			t.Fatal("warm/reloaded index differs", reload)
		}
		if err := read.LoadUpdates(t.Context(), "project", id, nil); err != nil || !reflect.DeepEqual(read.Snapshot.ProjectUpdates[id], want.ProjectUpdates[id]) {
			t.Fatal("warm/reloaded full update differs", reload, err)
		}
		read.Close()
		if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("persistent pulse leaked work or decoded full metadata")
		}
		view.releaseMetadataLogicalSources()
	}
}

func TestScaleP21HotPulseGroupUpdateBodyScaling(t *testing.T) {
	var held, summaryHeld int
	for _, repeat := range []int{32768, 327680} {
		body := "@mention " + strings.Repeat("padding ", repeat) + "FinalΣ"
		user := domain.User{ID: "person", Username: "mention", Name: "Author\xffName", DisplayName: "DisplayΣ"}
		data := domain.Bootstrap{
			Workspace:         domain.Workspace{ID: "workspace", URLKey: "logical"},
			Projects:          []domain.Project{{ID: "project", Name: "Source"}},
			Initiatives:       []domain.Initiative{{ID: "initiative", Name: "Initiative"}},
			Users:             []domain.User{user},
			ProjectUpdates:    map[string][]domain.ProjectUpdate{},
			InitiativeUpdates: map[string][]domain.InitiativeUpdate{},
		}
		for index := range 12 {
			at := time.Date(2026, 11, 14, 1, index, 0, 0, time.UTC)
			bodyData := map[string]any{"type": "doc", "text": body, "content": []any{map[string]any{"type": "userMention", "id": user.ID}}}
			if index%2 == 0 {
				data.ProjectUpdates["project"] = append(data.ProjectUpdates["project"], domain.ProjectUpdate{
					ID: fmt.Sprintf("project-%02d", index), ProjectID: "project", Body: body, BodyData: bodyData, User: user, CreatedAt: at,
				})
			} else {
				data.InitiativeUpdates["initiative"] = append(data.InitiativeUpdates["initiative"], domain.InitiativeUpdate{
					ID: fmt.Sprintf("initiative-%02d", index), InitiativeID: "initiative", Body: body, BodyData: bodyData, User: user, CreatedAt: at,
				})
			}
		}
		view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
		read, found, err := view.BeginPulseRead(t.Context(), "physical", true)
		if err != nil || !found || len(read.Snapshot.Recent) != 12 {
			t.Fatal("body scaling failed ordinary feed/unread index", repeat, found, err)
		}
		current := view.metadataResidency.work.used
		if held != 0 && held != current {
			t.Fatal("10x update Body/BodyData changed retained index work", held, current)
		}
		held = current
		target := read.Snapshot.Recent[0]
		workLimit := view.metadataResidency.work.limit
		view.metadataResidency.work.limit = held + 1
		if _, err := read.MatchSearch(t.Context(), target, "needle"); !errors.Is(err, ErrMetadataWorkBudget) || view.metadataResidency.work.used != held {
			t.Fatal("streamed-search quota error leaked work", err)
		}
		view.metadataResidency.work.limit = workLimit
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := read.MatchSearch(cancelled, target, "needle"); !errors.Is(err, context.Canceled) || view.metadataResidency.work.used != held {
			t.Fatal("streamed-search cancellation leaked work", err)
		}
		expected := strings.ToLower(body + "\n" + user.DisplayName + "\n" + user.Name)
		for _, entry := range read.Snapshot.Recent {
			if entry.Search != "" || !reflect.DeepEqual(entry.Mentions, []string{user.ID}) {
				t.Fatal("full search text retained or transient mention extraction changed")
			}
			for _, query := range []string{"FinalΣ", "σ\nDisplayΣ", "DisplayΣ\nAuthor\xffName", "padding padding", "absent", "\xff"} {
				search := strings.ToLower(query)
				got, err := read.MatchSearch(t.Context(), entry, search)
				if err != nil || got != strings.Contains(expected, search) {
					t.Fatalf("streamed search differs repeat=%d query=%q got=%t err=%v", repeat, query, got, err)
				}
				if view.metadataResidency.work.used != held {
					t.Fatal("search retained its parser/query allowance")
				}
			}
		}
		if err := read.LoadUpdates(t.Context(), target.Kind, target.SourceID, []string{target.UpdateID}); err != nil {
			t.Fatal("complete selected update failed after compact index", err)
		}
		update := read.Snapshot.InitiativeUpdates[target.SourceID][0]
		if update.Body != body || update.BodyData["text"] != body || view.metadataResidency.work.used <= held {
			t.Fatal("selected update was truncated or not charged through use")
		}
		read.Close()
		if view.metadataResidency.work.used != 0 {
			t.Fatal("selected body scaling leaked work")
		}
		summary, _, err := view.BeginPulseScheduleRead(t.Context(), "physical")
		if err != nil {
			t.Fatal(err)
		}
		if err := summary.LoadSchedule(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := summary.LoadFeed(t.Context()); err != nil {
			t.Fatal("body scaling failed summary index", err)
		}
		currentSummary := view.metadataResidency.work.used
		if summaryHeld != 0 && summaryHeld != currentSummary {
			t.Fatal("10x update Body/BodyData changed retained summary work", summaryHeld, currentSummary)
		}
		summaryHeld = currentSummary
		summary.Close()
		if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("summary body scaling leaked work or decoded full metadata")
		}
		view.releaseMetadataLogicalSources()
		t.Logf("update_body_bytes=%d updates=12 retained_feed_unread_work=%d retained_summary_work=%d", len(body), held, summaryHeld)
	}
}

func TestScaleP21HotPulseGroupWireTimestamps(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, project, initiative := f.data.Workspace.URLKey, idgen.New("pulse-wire-project-"), idgen.New("pulse-wire-initiative-")
	at := time.Date(2026, 11, 14, 9, 17, 33, 987654321, time.FixedZone(strings.Repeat("detached-pulse-zone", 1000), 5*3600+45*60+31))
	edited, due := at.Add(time.Minute), at.Add(time.Hour)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects", "initiatives", "projectUpdates", "initiativeUpdates", "settings"), workspace, "scale_p21.pulse_wire_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: project, Name: "Wire project", CreatedAt: at, UpdatedAt: edited})
		data.Initiatives = append(data.Initiatives, domain.Initiative{ID: initiative, Name: "Wire initiative", Creator: data.Viewer, CreatedAt: at, UpdatedAt: edited})
		bodyData := map[string]any{"stamp": at, "pointer": &edited, "nested": map[string]any{"when": due},
			"content": []any{map[string]any{"type": "userMention", "id": data.Viewer.ID}}}
		data.ProjectUpdates[project] = []domain.ProjectUpdate{{ID: project + "-update", ProjectID: project,
			Body: "Wire project body", BodyData: bodyData, User: data.Viewer, CreatedAt: at, EditedAt: &edited, DueAt: &due,
			Comments:    []domain.Comment{{ID: project + "-comment", Body: "Complete comment", CreatedAt: at, EditedAt: &edited, User: data.Viewer, BodyData: bodyData}},
			Attachments: []domain.Attachment{{ID: project + "-attachment", CreatedAt: at, Creator: data.Viewer}},
			Snapshot:    &domain.PulseSnapshot{CapturedAt: at, Status: "wire"},
		}}
		data.InitiativeUpdates[initiative] = []domain.InitiativeUpdate{{ID: initiative + "-update", InitiativeID: initiative,
			Body: "Wire initiative body", BodyData: bodyData, User: data.Viewer, CreatedAt: edited, EditedAt: &edited,
			Snapshot: &domain.PulseSnapshot{CapturedAt: at, Status: "wire"},
		}}
		data.Settings["pulseDeliveryCursors"] = map[string]time.Time{data.Viewer.ID: at}
		return nil
	}); err != nil {
		t.Fatal("supported native timestamp producer failed", err)
	}
	for _, reload := range []bool{false, true} {
		if reload {
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		want, _ := f.PulseFeed(workspace)
		// The real warm producer retains typed timestamps in interface fields.
		// Reloading later uses the literal loader's JSON-decoded representation.
		if !reload {
			stamp, ok := want.ProjectUpdates[project][0].BodyData["stamp"].(time.Time)
			if !ok || !stamp.Equal(at) {
				t.Fatal("native producer fixture lost its typed BodyData time")
			}
		}
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		read, found, err := view.BeginPulseRead(t.Context(), workspace, true)
		if err != nil || !found {
			t.Fatal("detached fixed-zone read rejected", reload, found, err)
		}
		for index, entry := range read.Snapshot.Recent {
			expected := *want.Recent[index]
			expected.Search = ""
			scaleP21HotPulseWireEqual(t, entry, expected)
			if !entry.CreatedAt.Equal(expected.CreatedAt) || entry.CreatedAt.UnixNano() != expected.CreatedAt.UnixNano() || entry.Score != expected.Score {
				t.Fatal("wire projection changed ordering/score instant", reload)
			}
			match, err := read.MatchSearch(t.Context(), entry, strings.ToLower("wire"))
			if err != nil || !match {
				t.Fatal("BodyData timestamps changed search", reload, err)
			}
		}
		for _, target := range []struct{ kind, id string }{{"project", project}, {"initiative", initiative}} {
			if err := read.LoadUpdates(t.Context(), target.kind, target.id, nil); err != nil {
				t.Fatal("detached fixed-zone detail rejected", reload, err)
			}
			snapshot, err := read.LoadLatestSnapshot(t.Context(), target.kind, target.id)
			if err != nil {
				t.Fatal("detached fixed-zone composer snapshot rejected", reload, err)
			}
			if err := read.LoadSource(t.Context(), target.kind, target.id); err != nil {
				t.Fatal("detached fixed-zone source rejected", reload, err)
			}
			if target.kind == "project" {
				scaleP21HotPulseWireEqual(t, read.Snapshot.ProjectUpdates[project], want.ProjectUpdates[project])
				scaleP21HotPulseWireEqual(t, snapshot, want.ProjectUpdates[project][0].Snapshot)
				for index, source := range want.Projects {
					if source.ID == project {
						scaleP21HotPulseWireEqual(t, read.Snapshot.Projects[index], source)
					}
				}
			} else {
				scaleP21HotPulseWireEqual(t, read.Snapshot.InitiativeUpdates[initiative], want.InitiativeUpdates[initiative])
				scaleP21HotPulseWireEqual(t, snapshot, want.InitiativeUpdates[initiative][0].Snapshot)
				for index, source := range want.Initiatives {
					if source.ID == initiative {
						scaleP21HotPulseWireEqual(t, read.Snapshot.Initiatives[index], source)
					}
				}
			}
		}
		if !reload {
			stamp := read.Snapshot.ProjectUpdates[project][0].BodyData["stamp"].(time.Time)
			if !stamp.Equal(at) || stamp.Location().String() == at.Location().String() {
				t.Fatal("detached time retained a native Location graph or changed instant")
			}
		}
		read.Close()
		schedule, found, err := view.BeginPulseScheduleRead(t.Context(), workspace)
		if err != nil || !found {
			t.Fatal(found, err)
		}
		if err := schedule.LoadSchedule(t.Context()); err != nil {
			t.Fatal("detached fixed-zone cursor rejected", reload, err)
		}
		metadata, _ := f.WorkspaceMetadataFields(workspace, "settings")
		scaleP21HotPulseWireEqual(t, schedule.Snapshot.Settings["pulseDeliveryCursors"], metadata.Settings["pulseDeliveryCursors"])
		schedule.Close()
		if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("wire timestamp read leaked work or decoded full metadata", reload)
		}
		view.releaseMetadataLogicalSources()
	}
}
