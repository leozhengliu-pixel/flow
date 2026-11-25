package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW182Fixture struct {
	scaleP21SW10Fixture
	prefix, team, other, project string
	base                         domain.Bootstrap
	actor                        domain.User
}

type scaleP21SW182Case struct {
	name                       string
	schedule                   string
	duplicates, upcoming, bulk int
	collision                  bool
	bodyCopies                 int
	parentOwner                bool
	background                 bool
	inherited                  bool
	rawHeaders                 int
	poisonCount                bool
	fullFallback               bool
	minimal                    bool
	stableHistory              bool
}

type scaleP21SW182MetadataRow struct {
	Field, Key string
	Order      int64
	Data       json.RawMessage
}

type scaleP21SW182Run struct {
	Normalized any
	Before     scaleP21SW59Rows
	After      scaleP21SW59Rows
	BeforeMeta []scaleP21SW182MetadataRow
	Metadata   []scaleP21SW182MetadataRow
	PriorCycle []domain.Cycle
	Cycles     []domain.Cycle
	Started    time.Time
	Finished   time.Time
	Peak       int
	Events     int
	Stopped    bool
	Writes     int
	SQL        int
}

func scaleP21SW182Setup(t *testing.T) scaleP21SW182Fixture {
	t.Helper()
	config := scaleP03DatabaseConfig(t)
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	platform := scaleP03Fixture(t, repo)
	_, actor := scaleP03Join(t, platform, "SW182 inherited actor", idgen.New("sw182-actor-")+"@example.test", "member")
	probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), platform.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	reader := &scaleP04Database{config: config}
	t.Cleanup(func() {
		if reader.reader != nil {
			_ = reader.reader.Close()
		}
	})
	base, found := repo.WorkspaceMetadata(platform.data.Workspace.URLKey)
	if !found {
		t.Fatal("missing owned ticker workspace")
	}
	role, status, err := repo.WorkspaceRole(t.Context(), platform.data.Workspace.ID, actor.ID)
	if err != nil || role != "member" || status != "active" || base.ViewerRole == role {
		t.Fatal("inherited actor fixture does not differ from the native viewer role", base.ViewerRole, role, status, err)
	}
	prefix := idgen.New("sw182-")
	return scaleP21SW182Fixture{
		scaleP21SW10Fixture: scaleP21SW10Fixture{
			scaleP21M2Fixture: scaleP21M2Fixture{scaleP03PlatformFixture: platform, probe: probe, testingT: t},
			reader:            reader,
		},
		prefix: prefix, team: prefix + "team", other: prefix + "other", project: prefix + "project", base: base, actor: actor,
	}
}

func scaleP21SW182Seed(t *testing.T, f scaleP21SW182Fixture, test scaleP21SW182Case, now time.Time, reload bool) domain.Bootstrap {
	t.Helper()
	for _, source := range []*store.SQLiteStore{f.repository, f.probe} {
		source.SetWebhookSink(nil)
		source.SetRealtimeSink(nil)
	}
	if err := f.repository.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 8, 17, 2, 3, 4, 500, time.UTC)
	today := cycleDay(now)
	duplicates, upcoming := max(1, test.duplicates), test.upcoming
	if upcoming == 0 {
		upcoming = 2
	}
	restore := store.ForceFullMutationsForTesting(true)
	err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw182_fixture", "", nil, func(data *domain.Bootstrap) error {
		data.Viewer, data.ViewerRole = f.base.Viewer, f.base.ViewerRole
		historyActor := data.Viewer
		if test.inherited {
			historyActor = f.actor
		}
		data.Teams = []domain.Team{{ID: f.other, Key: "OTHER", Name: "Other team"}}
		for range duplicates {
			data.Teams = append(data.Teams, domain.Team{ID: f.team, Key: "TICK", Name: "Ticker team"})
		}
		data.CycleSettings = map[string]domain.CycleSettings{
			f.team:  {Enabled: true, AutoCreate: true, AutoMigrate: true, DurationWeeks: 2, UpcomingCount: upcoming},
			f.other: {Enabled: false, AutoCreate: true, AutoMigrate: true, UpcomingCount: 2},
		}
		settings := data.CycleSettings[f.team]
		if test.schedule == "cooldown" {
			settings.CooldownWeeks = 2
		}
		if test.schedule == "disabled" {
			settings.Enabled = false
		}
		if test.schedule == "manual" {
			settings.AutoCreate = false
		}
		data.CycleSettings[f.team] = settings
		data.TeamSettings = map[string]domain.TeamSettings{
			f.team:  {TeamID: f.team, Access: "public", DetailedHistory: true},
			f.other: {TeamID: f.other, Access: "public"},
		}
		previous, next, future := f.prefix+"previous", f.prefix+"next", f.prefix+"future"
		cycle := func(id, team string, number int, status string, start, end time.Time) domain.Cycle {
			return domain.Cycle{
				ID: id, Number: number, TeamID: team, Status: status, StartsAt: start, EndsAt: end,
				Name: "Stored cycle " + id, Description: "Opaque cycle body\x00" + id,
				Capacity: 71, CapacityOverride: new(41), CapacityAutomatic: 29, CapacitySource: "override",
				CapacityByMember: map[string]map[string]int{data.Viewer.ID: {"points": number, "reserved": 2}},
				CalendarToken:    "native-calendar-" + id, Insight: map[string]string{"opaque": "preserve"},
				Resources: []domain.CycleResource{{ID: id + "-resource", Type: "link", Title: "Opaque resource", URL: "https://example.test/resource", CreatedAt: stamp}},
				CreatedAt: stamp, UpdatedAt: stamp,
			}
		}
		data.Cycles = []domain.Cycle{
			cycle(previous, f.team, 1, "current", today.AddDate(0, 0, -14), today.AddDate(0, 0, -1)),
			cycle(next, f.team, 2, "upcoming", today, today.AddDate(0, 0, 13)),
			cycle(future, f.team, 3, "upcoming", today.AddDate(0, 0, 14), today.AddDate(0, 0, 27)),
			cycle(f.prefix+"other-cycle", f.other, 1, "current", today.AddDate(0, 0, -14), today.AddDate(0, 0, -1)),
		}
		switch test.schedule {
		case "inclusive":
			data.Cycles[0].EndsAt = today.Add(23*time.Hour + 59*time.Minute)
		case "inclusive-zone":
			data.Cycles[0].EndsAt = today.Add(time.Hour).In(time.Local)
		case "parsed-offset":
			expected := today.Add(time.Hour)
			input := expected.In(time.FixedZone("input-only", 5*3600+120)).Format(time.RFC3339)
			parsed, err := parseCycleDate(input)
			if err != nil || parsed.Location() != time.UTC || !parsed.Equal(expected) {
				return fmt.Errorf("supported RFC3339 cycle producer changed instant: %q %v", input, err)
			}
			data.Cycles[0].EndsAt = parsed
		case "future":
			data.Cycles = data.Cycles[1:]
			data.Cycles[0].StartsAt, data.Cycles[0].EndsAt = today.AddDate(0, 0, 1), today.AddDate(0, 0, 14)
		case "cooldown":
			data.Cycles[1].StartsAt, data.Cycles[1].EndsAt = today.AddDate(0, 0, 14), today.AddDate(0, 0, 27)
			data.Cycles[2].StartsAt, data.Cycles[2].EndsAt = today.AddDate(0, 0, 42), today.AddDate(0, 0, 55)
		case "empty":
			data.Cycles = data.Cycles[3:]
		case "duplicates":
			late := cycle(previous, f.team, 91, "current", today.AddDate(0, 0, -7), today.AddDate(0, 0, 30))
			late.Name, late.Resources, late.Insight = "Later duplicate body", []domain.CycleResource{}, nil
			// Equal starts keep the first slice occurrence as the next cycle.
			tie := cycle(f.prefix+"tied-next", f.team, 4, "upcoming", today, today.AddDate(0, 0, 20))
			data.Cycles = append(data.Cycles[:2], append([]domain.Cycle{late, tie}, data.Cycles[2:]...)...)
		}
		for index := 1; index < test.rawHeaders; index++ {
			// Lean duplicate occurrences exercise the old 500-value IN
			// batching threshold without 20,000 physical issue rows.
			data.Cycles = append(data.Cycles, domain.Cycle{ID: previous, TeamID: f.team, Status: "current",
				StartsAt: today.AddDate(0, 0, -14), EndsAt: today.AddDate(0, 0, -1)})
		}
		body := strings.Repeat("large unrelated body ", max(1, test.bodyCopies)*4096)
		if test.minimal {
			body = ""
		}
		data.Projects = []domain.Project{{ID: f.project, SlugID: f.project, Name: "Exact count source", TeamIDs: []string{f.team},
			Description: body, IssueCount: 777, Progress: 0.75, Creator: &data.Viewer, Lead: &data.Viewer, CreatedAt: stamp, UpdatedAt: stamp}}
		data.Documents = []domain.Document{{ID: f.prefix + "unrelated-document", Title: "Unrelated resource", Content: body, Creator: data.Viewer, CreatedAt: stamp, UpdatedAt: stamp}}
		data.Settings["sw182-unrelated"] = map[string]any{"body": body, "number": 17}
		types := []string{"unstarted", "started", "unstarted", "started", "started", "unstarted", "completed", "canceled", "backlog", "started"}
		names := []string{"open", "merge", "delete", "comment", "recent", "cross-team", "completed", "canceled", "backlog", "unrelated"}
		data.Issues = nil
		data.Activities, data.Comments = map[string][]domain.ActivityEvent{}, map[string][]domain.Comment{}
		for index, name := range names {
			id, team, cycleID := f.prefix+name, f.team, previous
			if name == "cross-team" || name == "unrelated" {
				team = f.other
			}
			if name == "unrelated" {
				cycleID = f.prefix + "other-cycle"
			}
			created := stamp
			if name == "recent" && !test.stableHistory {
				created = now
			}
			issue := domain.Issue{
				ID: id, Identifier: fmt.Sprintf("TICK-%d", index+1), Number: index + 1, Version: 3, Title: "Exact " + name,
				Description: "Native issue body\x00" + name, Team: domain.Team{ID: team}, Creator: data.Viewer,
				State:   domain.WorkflowState{ID: f.prefix + types[index], TeamID: team, Type: types[index]},
				CycleID: &cycleID, Project: &domain.ProjectSummary{ID: f.project}, CreatedAt: created, UpdatedAt: stamp,
			}
			if name == "recent" && test.inherited {
				issue.Creator = f.actor
			}
			if name == "open" && test.collision {
				issue.Attachments = []domain.Attachment{{ID: "cycles", IssueID: id, Title: "Earlier nested aggregate collision", URL: "https://example.test/collision", Creator: data.Viewer, CreatedAt: stamp}}
			}
			data.Issues = append(data.Issues, issue)
		}
		for index := range test.bulk {
			cycleID := previous
			data.Issues = append(data.Issues, domain.Issue{
				ID: fmt.Sprintf("%sbulk-%04d", f.prefix, index), Identifier: fmt.Sprintf("BULK-%d", index+1), Number: 100 + index, Version: 3,
				Title: "Full fallback issue", Team: domain.Team{ID: f.team}, Creator: data.Viewer,
				State:   domain.WorkflowState{ID: f.prefix + "unstarted", TeamID: f.team, Type: "unstarted"},
				CycleID: &cycleID, Project: &domain.ProjectSummary{ID: f.project}, CreatedAt: stamp, UpdatedAt: stamp,
			})
		}
		historyTime := now.Add(-20 * time.Minute)
		data.Activities[f.prefix+"merge"] = []domain.ActivityEvent{{ID: f.prefix + "merge-history", Type: "issue.updated", Actor: historyActor, CreatedAt: historyTime, Metadata: domain.ActivityMetadata{"cycle": previous, "cycleBefore": f.prefix + "older"}}}
		data.Activities[f.prefix+"delete"] = []domain.ActivityEvent{{ID: f.prefix + "delete-history", Type: "issue.updated", Actor: historyActor, CreatedAt: historyTime, Metadata: domain.ActivityMetadata{"cycle": previous, "cycleBefore": next}}}
		data.Activities[f.prefix+"comment"] = []domain.ActivityEvent{{ID: f.prefix + "comment-history", Type: "issue.updated", Actor: historyActor, CreatedAt: historyTime, Metadata: domain.ActivityMetadata{"priority": "2", "priorityBefore": "1"}}}
		data.Comments[f.prefix+"comment"] = []domain.Comment{{ID: f.prefix + "boundary-comment", User: data.Viewer, Body: "The comment prevents history merging.", CreatedAt: now.Add(-10 * time.Minute)}}
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	if test.poisonCount {
		scaleP21SW182PoisonCount(t, f)
	}
	if reload {
		if err := f.repository.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), f.repository, f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	before, found := f.repository.WorkspaceMetadataFields(f.data.Workspace.URLKey, "teams", "cycles", "cycleSettings", "viewerRole")
	if !found {
		t.Fatal("missing seeded ticker metadata")
	}
	return before
}

func scaleP21SW182PoisonCount(t *testing.T, f scaleP21SW182Fixture) {
	t.Helper()
	var selected *scaleP21SW182MetadataRow
	for _, row := range scaleP21SW182Metadata(t, f) {
		if row.Field == "projects" && row.Key == f.project {
			value := row
			selected = &value
			break
		}
	}
	if selected == nil {
		t.Fatal("missing owned project for full/scoped count marker")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(selected.Data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["issueCount"] = json.RawMessage("777777")
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	query := `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='projects' AND record_key=?`
	if f.reader.config.Driver == "postgres" || f.reader.config.Driver == "postgresql" {
		for index := 1; strings.Contains(query, "?"); index++ {
			query = strings.Replace(query, "?", fmt.Sprintf("$%d", index), 1)
		}
	}
	if _, err := f.reader.reader.ExecContext(t.Context(), query, raw, f.data.Workspace.URLKey, f.project); err != nil {
		t.Fatal(err)
	}
	if err := f.repository.ForgetInstalledMetadataRevisionForTesting(f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	if err := f.repository.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW182Metadata(t *testing.T, f scaleP21SW182Fixture) []scaleP21SW182MetadataRow {
	t.Helper()
	rows := f.reader.query(t, `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,collection_order,record_key`, f.data.Workspace.URLKey)
	defer rows.Close()
	result := []scaleP21SW182MetadataRow{}
	for rows.Next() {
		var item scaleP21SW182MetadataRow
		var raw []byte
		if err := rows.Scan(&item.Field, &item.Key, &item.Order, &raw); err != nil {
			t.Fatal(err)
		}
		item.Data = json.RawMessage(raw)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21SW182Events(t *testing.T, f scaleP21SW182Fixture) []domain.DomainEvent {
	t.Helper()
	rows := f.reader.query(t, `SELECT id,event_type,aggregate_id,payload,previous_values,created_at FROM domain_events WHERE workspace_id=? ORDER BY created_at,id`, f.data.Workspace.ID)
	defer rows.Close()
	result := []domain.DomainEvent{}
	for rows.Next() {
		var event domain.DomainEvent
		var raw []byte
		var previous sql.NullString
		var created string
		if err := rows.Scan(&event.ID, &event.Type, &event.AggregateID, &raw, &previous, &created); err != nil {
			t.Fatal(err)
		}
		event.Payload = json.RawMessage(raw)
		if previous.Valid && previous.String != "" {
			event.PreviousValues = json.RawMessage(previous.String)
		}
		var err error
		event.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21SW182Native(t *testing.T, f scaleP21SW182Fixture, source *store.SQLiteStore, before domain.Bootstrap) domain.Bootstrap {
	t.Helper()
	var data domain.Bootstrap
	var closeRead func()
	if source.BoundedMetadataEnabled() {
		parent, found, err := source.BeginInstalledMetadataRead(t.Context(), f.data.Workspace.URLKey)
		if err != nil || !found {
			t.Fatal(found, err)
		}
		streamWork, err := source.BeginMetadataJSONRead(t.Context())
		if err != nil {
			parent.Close()
			t.Fatal(err)
		}
		defer streamWork.Close()
		buffer := &scaleP21SW182Stream{work: streamWork, ctx: t.Context()}
		if err := parent.StreamFieldJSON(t.Context(), "cycles", buffer); err != nil {
			parent.Close()
			t.Fatal("stream native occurrence array", err)
		}
		if err := streamWork.ReserveDecode(t.Context(), buffer.Bytes(), nil); err != nil {
			parent.Close()
			t.Fatal(err)
		}
		var streamed []domain.Cycle
		if err := json.Unmarshal(buffer.Bytes(), &streamed); err != nil {
			parent.Close()
			t.Fatal(err)
		}
		buffer.Reset()
		if err := parent.StreamFieldJSON(t.Context(), "teams", buffer); err != nil {
			parent.Close()
			t.Fatal("stream complete native team occurrences", err)
		}
		if err := streamWork.ReserveDecode(t.Context(), buffer.Bytes(), nil); err != nil {
			parent.Close()
			t.Fatal(err)
		}
		var teams []domain.Team
		if err := json.Unmarshal(buffer.Bytes(), &teams); err != nil {
			parent.Close()
			t.Fatal(err)
		}
		fields := []string{"cycleSettings", "viewerRole"}
		selection, err := parent.Select(t.Context(), store.MetadataRecordScope{Fields: fields})
		if err != nil {
			parent.Close()
			t.Fatal(err)
		}
		data, closeRead = selection.Data, parent.Close
		data.Cycles = streamed
		data.Teams = teams
	} else {
		var found bool
		data, found = source.WorkspaceMetadataFields(f.data.Workspace.URLKey, "teams", "cycles", "cycleSettings", "viewerRole")
		if !found {
			t.Fatal("missing post-ticker native metadata")
		}
		closeRead = func() {}
	}
	defer closeRead()
	// Clone only the small declared values while their selected owner is live.
	if data.ViewerRole != before.ViewerRole {
		t.Fatal("ticker persisted the callback's effective actor role instead of native role", before.ViewerRole, data.ViewerRole)
	}
	out := domain.Bootstrap{Teams: slices.Clone(data.Teams), CycleSettings: map[string]domain.CycleSettings{}, Cycles: slices.Clone(data.Cycles), ViewerRole: data.ViewerRole}
	for id, settings := range data.CycleSettings {
		out.CycleSettings[id] = settings
	}
	for index := range out.Cycles {
		cycle := &out.Cycles[index]
		cycle.Resources = slices.Clone(cycle.Resources)
		if cycle.CapacityOverride != nil {
			cycle.CapacityOverride = new(*cycle.CapacityOverride)
		}
		if cycle.Insight != nil {
			values := map[string]string{}
			for key, value := range cycle.Insight {
				values[key] = value
			}
			cycle.Insight = values
		}
		if cycle.CapacityByMember != nil {
			values := map[string]map[string]int{}
			for user, capacity := range cycle.CapacityByMember {
				values[user] = map[string]int{}
				for key, value := range capacity {
					values[user][key] = value
				}
			}
			cycle.CapacityByMember = values
		}
	}
	return out
}

type scaleP21SW182Stream struct {
	bytes.Buffer
	work *store.MetadataJSONRead
	ctx  context.Context
}

func (w *scaleP21SW182Stream) Write(value []byte) (int, error) {
	if err := w.work.ReserveEncoded(w.ctx, w.Len()+len(value)); err != nil {
		return 0, err
	}
	return w.Buffer.Write(value)
}

func (w *scaleP21SW182Stream) WriteString(value string) (int, error) {
	if err := w.work.ReserveEncoded(w.ctx, w.Len()+len(value)); err != nil {
		return 0, err
	}
	return w.Buffer.WriteString(value)
}

func scaleP21SW182Project(t *testing.T, f scaleP21SW182Fixture, source *store.SQLiteStore) any {
	t.Helper()
	if source.BoundedMetadataEnabled() {
		read, found, err := source.BeginInstalledResourcePointRead(t.Context(), f.data.Workspace.URLKey, "projects", f.project)
		if err != nil || !found || len(read.Data.Projects) != 1 {
			t.Fatal("missing native post-source project", found, err)
		}
		defer read.Close()
		return scaleP21SW10JSON(t, read.Data.Projects[0])
	}
	data, found := source.WorkspaceMetadataFields(f.data.Workspace.URLKey, "projects")
	if !found || len(data.Projects) != 1 || data.Projects[0].ID != f.project {
		t.Fatal("missing literal post-source project")
	}
	return scaleP21SW10JSON(t, data.Projects[0])
}

func scaleP21SW182CanonicalProject(t *testing.T, f scaleP21SW182Fixture) map[string]any {
	t.Helper()
	for _, row := range scaleP21SW182Metadata(t, f) {
		if row.Field == "projects" && row.Key == f.project {
			var project map[string]any
			if err := json.Unmarshal(row.Data, &project); err != nil {
				t.Fatal(err)
			}
			return project
		}
	}
	t.Fatal("missing canonical project count marker")
	return nil
}

func scaleP21SW182Normalize(t *testing.T, got scaleP21SW59Outcome, before scaleP21SW59Rows, metadata []scaleP21SW182MetadataRow, native, prior domain.Bootstrap, project any, started, finished time.Time) any {
	t.Helper()
	value := scaleP21SW59Normalized(t, got, before, started, finished).(map[string]any)
	value["Metadata"] = scaleP21SW10JSON(t, metadata)
	value["Native"] = scaleP21SW10JSON(t, native)
	value["NativeProject"] = project
	if publications, ok := value["Realtime"].([]any); ok {
		for _, publication := range publications {
			object := publication.(map[string]any)
			if cursor, ok := object["seq"].(json.Number); ok && cursor.String() != "0" {
				object["seq"] = "<committed-cursor>"
			}
		}
	}
	existing := map[string]bool{}
	for _, cycle := range prior.Cycles {
		existing[cycle.ID] = true
	}
	generated := map[string]string{}
	for _, cycle := range native.Cycles {
		if !existing[cycle.ID] {
			generated[cycle.ID] = fmt.Sprintf("<generated-cycle:%d>", len(generated))
		}
	}
	var visit func(any, string) any
	visit = func(value any, field string) any {
		switch item := value.(type) {
		case string:
			if replacement, found := generated[item]; found {
				return replacement
			}
			switch strings.ToLower(field) {
			case "createdat", "updatedat", "editedat", "nextattemptat":
				if clock, err := time.Parse(time.RFC3339Nano, item); err == nil && !clock.Before(started) && !clock.After(finished) {
					return "<generated-clock>"
				}
			}
		case map[string]any:
			for key, child := range item {
				item[key] = visit(child, key)
			}
		case []any:
			for index := range item {
				item[index] = visit(item[index], field)
			}
		}
		return value
	}
	return visit(value, "")
}

func scaleP21SW182Tick(t *testing.T, f scaleP21SW182Fixture, source *store.SQLiteStore, test scaleP21SW182Case, now time.Time, reload bool, key, failure string) scaleP21SW182Run {
	t.Helper()
	nativeBefore := scaleP21SW182Seed(t, f, test, now, reload)
	if test.poisonCount {
		// Reload normalizes native counts against its unloaded issue slice.
		// The canonical marker distinguishes writes by the actual branches.
		project := scaleP21SW182CanonicalProject(t, f)
		if project["issueCount"] != float64(777777) {
			t.Fatal("full/scoped marker was repaired during fixture construction", project["issueCount"])
		}
	}
	before, metadataBefore := scaleP21SW59SQL(t, f.scaleP21SW10Fixture), scaleP21SW182Metadata(t, f)
	priorIDs := map[string]bool{}
	for _, event := range scaleP21SW182Events(t, f) {
		priorIDs[event.ID] = true
	}
	got, result := scaleP21SW59Outcome{}, scaleP21SW182Run{Before: before, BeforeMeta: metadataBefore, PriorCycle: nativeBefore.Cycles}
	var peak, statements, writes atomic.Int64
	var stopped atomic.Bool
	var peakMu sync.Mutex
	var peakStatements []string
	tracePeaks := source.BoundedMetadataEnabled() && os.Getenv("FLOW_TEST_SW182_PEAK_TRACE") == "1"
	sample := func() bool {
		work := int64(source.MetadataWorkUsedForTesting())
		for prior := peak.Load(); work > prior; prior = peak.Load() {
			if peak.CompareAndSwap(prior, work) {
				return true
			}
		}
		return false
	}
	heldBefore := 0
	var budget *store.MetadataJSONRead
	if failure == "budget" {
		var err error
		budget, err = source.BeginMetadataJSONRead(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer budget.Close()
		// Admission only: no 64 MiB byte buffer is allocated by this call.
		if err := budget.ReserveEncoded(t.Context(), (64<<20)-(64<<10)); err != nil {
			t.Fatal("could not hold the declared aggregate work budget", err)
		}
		heldBefore = source.MetadataWorkUsedForTesting()
	}
	source.SetWebhookSink(func(workspace string, event domain.DomainEvent) {
		sample()
		if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() <= heldBefore {
			t.Error("cycle webhook outlived its admitted attempt/output owner")
		}
		raw, err := json.Marshal(map[string]any{
			"workspace": workspace, "id": event.ID, "type": event.Type, "aggregateId": event.AggregateID,
			"actorId": event.ActorID, "payload": event.Payload, "previousValues": event.PreviousValues,
			"snapshot": event.Snapshot, "createdAt": event.CreatedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		got.Webhooks = append(got.Webhooks, json.RawMessage(raw))
	})
	var realtime []domain.RealtimeEvent
	captureRealtime := func(workspace string, event domain.RealtimeEvent) {
		sample()
		if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() <= heldBefore {
			t.Error("cycle realtime publication outlived its admitted attempt/output owner")
		}
		realtime = append(realtime, event)
		// Preserve the legacy zero/nonzero cursor contract. Nonzero cursors
		// are checked against the journal below, then normalized by identity.
		raw, err := json.Marshal(map[string]any{
			"workspace": workspace, "id": event.ID, "type": event.Type, "aggregateId": event.AggregateID,
			"actorId": event.ActorID, "clientId": event.ClientID, "seq": event.Seq, "payload": event.Payload, "createdAt": event.CreatedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		got.Realtime = append(got.Realtime, json.RawMessage(raw))
	}
	source.SetRealtimeSink(captureRealtime)
	if source != f.repository {
		f.repository.SetRealtimeSink(captureRealtime)
	}
	defer func() {
		source.SetWebhookSink(nil)
		source.SetRealtimeSink(nil)
		f.repository.SetRealtimeSink(nil)
	}()
	ctx := t.Context()
	if test.background {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if test.inherited {
		ctx = store.ContextWithActor(ctx, f.actor)
		ctx = store.ContextWithRealtimeClient(ctx, "sw182-inherited-client")
	}
	parentClosed := 0
	var closeParent func()
	if test.parentOwner {
		ctx, closeParent = beginMetadataOutputReads(ctx)
		defer closeParent()
		if !registerRequestReadClose(ctx, func() { parentClosed++ }) {
			t.Fatal("missing test parent owner")
		}
	}
	if failure == "cancelled" {
		cancel()
	}
	ctx = store.WithSQLStatementObserver(ctx, func(query string) {
		statements.Add(1)
		newPeak := sample()
		if tracePeaks && newPeak {
			prefix := strings.Join(strings.Fields(query), " ")
			if len(prefix) > 160 {
				prefix = prefix[:160]
			}
			entry := fmt.Sprintf("work=%d SQL=%s", peak.Load(), prefix)
			peakMu.Lock()
			if len(peakStatements) == 10 {
				copy(peakStatements, peakStatements[1:])
				peakStatements = peakStatements[:9]
			}
			peakStatements = append(peakStatements, entry)
			peakMu.Unlock()
		}
		lower := strings.ToLower(strings.TrimSpace(query))
		if (strings.HasPrefix(lower, "insert") || strings.HasPrefix(lower, "update") || strings.HasPrefix(lower, "delete")) &&
			(strings.Contains(lower, "issue_records") || strings.Contains(lower, "workspace_metadata_records") || strings.Contains(lower, "workspace_content_records")) {
			writes.Add(1)
		}
		if failure == "rollback" && strings.Contains(lower, "insert into domain_events") {
			stopped.Store(true)
			cancel()
		}
	})
	started := time.Now().UTC()
	// Deliberately no caller-installed metadata frame or ForceFull override:
	// the original ticker's actual scope resolver is part of this contract.
	(&server{store: source}).maintainCycleSchedule(ctx, key)
	finished := time.Now().UTC()
	result.Started, result.Finished = started, finished
	result.SQL, result.Peak, result.Writes, result.Stopped = int(statements.Load()), int(peak.Load()), int(writes.Load()), stopped.Load()
	t.Logf("ticker bounded=%t SQL=%d writes=%d peak=%d failure=%s", source.BoundedMetadataEnabled(), result.SQL, result.Writes, result.Peak, failure)
	if tracePeaks {
		peakMu.Lock()
		for _, entry := range peakStatements {
			t.Log(entry)
		}
		peakMu.Unlock()
	}
	if !cycleDay(now).Equal(cycleDay(started)) || !cycleDay(started).Equal(cycleDay(finished)) {
		t.Fatalf("cycle fixture crossed UTC midnight: seeded=%s started=%s finished=%s", now, started, finished)
	}
	if parentClosed != 0 {
		t.Fatal("ticker closed its caller's metadata owner")
	}
	if closeParent != nil {
		if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() != heldBefore {
			t.Fatal("ticker left output owned by its caller's metadata frame", source.MetadataWorkUsedForTesting(), heldBefore)
		}
		closeParent()
		if parentClosed != 1 {
			t.Fatal("caller metadata frame did not close independently")
		}
	}
	if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() != heldBefore {
		t.Fatal("ticker retained preflight/attempt/output work", source.MetadataWorkUsedForTesting(), heldBefore)
	}
	if budget != nil {
		budget.Close()
		if source.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("budget refusal retained an attempt after its caller released work")
		}
	}
	got.Rows = scaleP21SW59SQL(t, f.scaleP21SW10Fixture)
	result.After, result.Metadata = got.Rows, scaleP21SW182Metadata(t, f)
	for _, event := range scaleP21SW182Events(t, f) {
		if !priorIDs[event.ID] {
			got.Events = append(got.Events, event)
		}
	}
	result.Events = len(got.Events)
	wantedEvents := 0
	if failure == "" {
		for _, team := range nativeBefore.Teams {
			if cycleScheduleDue(&nativeBefore, team.ID, now) {
				wantedEvents = 1
				break
			}
		}
	}
	if len(got.Events) != wantedEvents || len(got.Webhooks) != wantedEvents || len(got.Realtime) != wantedEvents {
		t.Fatal("ticker SQL event/publication counts differ", len(got.Events), len(got.Webhooks), len(got.Realtime), wantedEvents)
	}
	for _, event := range got.Events {
		if event.Type != "cycles.automatically_maintained" || event.AggregateID != "cycles" {
			t.Fatal("ticker changed its actual event identity", event.Type, event.AggregateID)
		}
	}
	for _, event := range realtime {
		if event.Seq != 0 {
			seq, err := f.repository.RecordJournalEventSeq(t.Context(), event.ID)
			if err != nil {
				t.Fatal(err)
			}
			number, err := strconv.ParseInt(seq, 10, 64)
			if err != nil || number != event.Seq || number <= 0 {
				t.Fatal("cycle realtime cursor did not name its committed event", seq, event.Seq, err)
			}
		}
		if !slices.ContainsFunc(got.Events, func(stored domain.DomainEvent) bool { return stored.ID == event.ID }) {
			t.Fatal("cycle realtime event was not the persisted event", event.ID)
		}
	}
	if failure == "rollback" && (!result.Stopped || result.Writes == 0) {
		t.Fatal("rollback fixture did not reach event insertion after canonical writes", result.Stopped, result.Writes)
	}
	if wantedEvents == 0 && (!reflect.DeepEqual(before, got.Rows) || !reflect.DeepEqual(metadataBefore, result.Metadata)) {
		t.Fatal("no-op/refused/rolled-back ticker changed business SQL rows")
	}
	native := scaleP21SW182Native(t, f, source, nativeBefore)
	result.Cycles = native.Cycles
	if test.collision && wantedEvents != 0 {
		var publication struct {
			Snapshot struct {
				Entity struct{ ID, Title string }
			}
		}
		if err := json.Unmarshal(got.Webhooks[0].(json.RawMessage), &publication); err != nil {
			t.Fatal(err)
		}
		if publication.Snapshot.Entity.ID != "cycles" || publication.Snapshot.Entity.Title != "Earlier nested aggregate collision" {
			t.Fatal("fixture did not preserve earlier nested aggregate lookup precedence", publication)
		}
	}
	if test.inherited && wantedEvents != 0 {
		var publication struct{ ActorID string }
		if err := json.Unmarshal(got.Webhooks[0].(json.RawMessage), &publication); err != nil || publication.ActorID != f.actor.ID {
			t.Fatal("ticker lost its inherited actor during fallback/publication", publication.ActorID, err)
		}
		if len(realtime) != 1 || realtime[0].ClientID != "sw182-inherited-client" {
			t.Fatal("ticker lost its inherited realtime client")
		}
	}
	project := scaleP21SW182Project(t, f, source)
	if test.poisonCount {
		// The scoped progress rewrite persists the normalized native zero;
		// ordinary full mutation hydrates every issue and repairs the count.
		expected := 0
		if test.fullFallback {
			expected = 10 + test.bulk
		}
		object := scaleP21SW182CanonicalProject(t, f)
		if object["issueCount"] != float64(expected) {
			t.Fatal("ticker did not take the literal scoped/full count-repair branch", test.fullFallback, expected, object)
		}
	} else if test.bulk > 1000 {
		object, ok := project.(map[string]any)
		if !ok || object["issueCount"] != float64(10+test.bulk) {
			t.Fatal("full fallback installed stale project/display counts", project)
		}
	}
	result.Normalized = scaleP21SW182Normalize(t, got, before, result.Metadata, native, nativeBefore, project, started, finished)
	if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
		t.Fatal("ticker/native verification leaked ownership or decoded a full workspace", source.MetadataWorkUsedForTesting(), source.WorkspaceMetadataDecodes())
	}
	t.Logf("bounded=%t schedule=%s key=%q failure=%s peak=%d events=%d", source.BoundedMetadataEnabled(), test.schedule, key, failure, result.Peak, result.Events)
	return result
}

func scaleP21SW182History(t *testing.T, f scaleP21SW182Fixture, result scaleP21SW182Run, test scaleP21SW182Case) {
	t.Helper()
	activities := map[string][]domain.ActivityEvent{}
	for _, row := range result.After.Content {
		if row.Kind == "activity" {
			var activity domain.ActivityEvent
			if err := json.Unmarshal(row.Data, &activity); err != nil {
				t.Fatal(err)
			}
			activities[row.Resource] = append(activities[row.Resource], activity)
		}
	}
	if len(activities[f.prefix+"merge"]) != 1 || activities[f.prefix+"merge"][0].ID != f.prefix+"merge-history" ||
		activities[f.prefix+"merge"][0].UpdatedAt == nil {
		t.Fatal("cycle migration lost the existing history merge")
	}
	if len(activities[f.prefix+"delete"]) != 0 {
		t.Fatal("cycle migration appended instead of deleting the canceled history edit")
	}
	if len(activities[f.prefix+"comment"]) != 2 ||
		!slices.ContainsFunc(activities[f.prefix+"comment"], func(event domain.ActivityEvent) bool { return event.ID == f.prefix+"comment-history" }) {
		t.Fatal("cycle migration ignored the intervening comment boundary")
	}
	if test.stableHistory {
		if len(activities[f.prefix+"recent"]) != 1 ||
			activities[f.prefix+"recent"][0].Type != "issue.updated" ||
			activities[f.prefix+"recent"][0].Metadata["cycle"] != f.prefix+"next" ||
			activities[f.prefix+"recent"][0].Metadata["cycleBefore"] != f.prefix+"previous" {
			t.Fatal("old-created issue did not append its exact cycle migration history")
		}
	} else if len(activities[f.prefix+"recent"]) != 0 {
		t.Fatal("cycle migration added history within the creator's initial three minutes")
	}
	migrated := 0
	for _, row := range result.After.Issues {
		values := row.([]any)
		var issue domain.Issue
		if err := json.Unmarshal(values[3].(json.RawMessage), &issue); err != nil {
			t.Fatal(err)
		}
		switch issue.ID {
		case f.prefix + "completed", f.prefix + "canceled", f.prefix + "backlog":
			if issue.CycleID == nil || *issue.CycleID != f.prefix+"previous" {
				t.Fatal("excluded status migrated cycles", issue.ID)
			}
		case f.prefix + "unrelated":
			if issue.CycleID == nil || *issue.CycleID != f.prefix+"other-cycle" {
				t.Fatal("ticker changed an issue outside the resolved cycle scope")
			}
		default:
			if issue.CycleID == nil || *issue.CycleID != f.prefix+"next" {
				t.Fatal("ticker did not migrate the exact cycle scope", issue.ID)
			}
			migrated++
		}
	}
	if migrated != 6+test.bulk {
		t.Fatal("cycle migration omitted resolved issues, including cross-team/fallback rows", migrated, 6+test.bulk)
	}
}

func scaleP21SW182Pair(t *testing.T, f scaleP21SW182Fixture, test scaleP21SW182Case, reload bool, key string) scaleP21SW182Run {
	t.Helper()
	now := time.Now().UTC()
	old := scaleP21SW182Tick(t, f, f.repository, test, now, reload, key, "")
	bounded := scaleP21SW182Tick(t, f, f.probe, test, now, reload, key, "")
	if !reflect.DeepEqual(old.Normalized, bounded.Normalized) {
		if !reflect.DeepEqual(old.Before, bounded.Before) {
			t.Log("ticker fixture SQL preimages differ", scaleP21M2Diff(old.Before, bounded.Before))
		}
		t.Fatal("actual cycle ticker native/SQL/publication differential", scaleP21M2Diff(old.Normalized, bounded.Normalized))
	}
	// The seed's native values come back from the legacy in-memory copy as
	// time.Local, while the bounded side decodes the identical stored JSON.
	// With a non-zero process offset encoding/json maps that offset back to
	// time.Local, but on a UTC process the stored value is "Z" and decodes to
	// time.UTC: same instant, offset and zone name, differing only in the
	// *Location pointer, which no response, stored row or event can carry
	// (those are compared byte for byte above). Canonicalize only that
	// zero-offset Local alias before the typed comparisons.
	canonicalZone := func(source []domain.Cycle) []domain.Cycle {
		cycles := slices.Clone(source)
		for index := range cycles {
			for _, clock := range []*time.Time{&cycles[index].StartsAt, &cycles[index].EndsAt, &cycles[index].CreatedAt, &cycles[index].UpdatedAt} {
				if name, offset := clock.Zone(); clock.Location() == time.Local && offset == 0 && name == "UTC" {
					*clock = clock.UTC()
				}
			}
		}
		return cycles
	}
	normalizeNative := func(result scaleP21SW182Run) []domain.Cycle {
		cycles := canonicalZone(result.Cycles)
		existing, generated := map[string]bool{}, map[string]string{}
		for _, prior := range result.PriorCycle {
			existing[prior.ID] = true
		}
		for index := range cycles {
			if id := cycles[index].ID; !existing[id] {
				if _, found := generated[id]; !found {
					generated[id] = fmt.Sprintf("<generated-cycle:%d>", len(generated))
				}
				cycles[index].ID = generated[id]
			}
			for _, clock := range []*time.Time{&cycles[index].CreatedAt, &cycles[index].UpdatedAt} {
				if !clock.Before(result.Started) && !clock.After(result.Finished) {
					*clock = time.Time{}
				}
			}
		}
		return cycles
	}
	// The original scoped writer normalizes NUL strings during its changed
	// cycle rebase. Preserve that actual AFTER value; never sanitize either
	// side or demand seed-to-after invariance for a changed occurrence.
	if !reflect.DeepEqual(normalizeNative(old), normalizeNative(bounded)) {
		t.Fatal("typed native cycle AFTER values differ", scaleP21M2Diff(normalizeNative(old), normalizeNative(bounded)))
	}
	oldPrior, oldCycles := canonicalZone(old.PriorCycle), canonicalZone(old.Cycles)
	boundedPrior, boundedCycles := canonicalZone(bounded.PriorCycle), canonicalZone(bounded.Cycles)
	for index, before := range oldPrior {
		// The literal scoped writer can rebase duplicate IDs into one native
		// occurrence. Only demand seed invariance where it preserved an ordinal.
		if index < len(oldCycles) && reflect.DeepEqual(before, oldCycles[index]) &&
			!reflect.DeepEqual(boundedPrior[index], boundedCycles[index]) {
			t.Fatal("bounded ticker changed a native cycle the literal ticker preserved", index, before.ID)
		}
	}
	oldAfter, boundedAfter := map[string]scaleP21SW182MetadataRow{}, map[string]scaleP21SW182MetadataRow{}
	for _, row := range old.Metadata {
		oldAfter[row.Field+"\n"+row.Key] = row
	}
	for _, row := range bounded.Metadata {
		boundedAfter[row.Field+"\n"+row.Key] = row
	}
	for _, row := range old.BeforeMeta {
		key := row.Field + "\n" + row.Key
		if reflect.DeepEqual(row, oldAfter[key]) {
			if !reflect.DeepEqual(row, boundedAfter[key]) {
				t.Fatal("bounded ticker changed a canonical row the literal ticker preserved", row.Field, row.Key)
			}
		}
	}
	if test.schedule == "cooldown" || test.schedule == "immediate" || test.schedule == "duplicates" {
		scaleP21SW182History(t, f, old, test)
		scaleP21SW182History(t, f, bounded, test)
	}
	return bounded
}

func TestScaleP21SW182CycleScheduleTickerDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21SW182Setup(t)
	for _, reload := range []bool{false, true} {
		for _, key := range []string{f.data.Workspace.URLKey, ""} {
			for _, test := range []scaleP21SW182Case{
				{name: "inclusive-last-day", schedule: "inclusive"},
				{name: "inclusive-UTC-day-with-process-local-offset", schedule: "inclusive-zone"},
				{name: "RFC3339-offset-through-actual-parseCycleDate", schedule: "parsed-offset"},
				{name: "future-next-not-due", schedule: "future"},
				{name: "disabled-current", schedule: "disabled"},
				{name: "manual-auto-create-off", schedule: "manual"},
				{name: "cooldown-history-and-cross-team-scope", schedule: "cooldown"},
				{name: "immediate-start-and-top-up", schedule: "immediate"},
				{name: "no-current-or-upcoming", schedule: "empty"},
				{name: "duplicate-cycle-occurrences-and-team-order", schedule: "duplicates", duplicates: 3},
				{name: "nested-aggregate-collision", schedule: "immediate", collision: true},
				{name: "bare-background-context", schedule: "immediate", background: true},
				{name: "independent-of-parent-frame", schedule: "immediate", parentOwner: true},
			} {
				t.Run(fmt.Sprintf("reload=%t/default=%t/%s", reload, key == "", test.name), func(t *testing.T) {
					scaleP21SW182Pair(t, f, test, reload, key)
				})
			}
		}
	}
}

func TestScaleP21SW182CycleScheduleFullScopeFallback(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21SW182Setup(t)
	t.Run("1000-batch-boundary-remains-scoped", func(t *testing.T) {
		test := scaleP21SW182Case{name: "scoped-control", schedule: "immediate", bulk: 1001,
			collision: true, background: true, inherited: true, poisonCount: true, minimal: true}
		scaleP21SW182Pair(t, f, test, false, f.data.Workspace.URLKey)
	})
	for _, reload := range []bool{false, true} {
		t.Run(fmt.Sprintf("raw-20000-threshold/reload=%t/inherited-member", reload), func(t *testing.T) {
			// The actual resolver supplies >10,500 duplicate cycle IDs.
			// Each old 500-value IN batch returns ~1,010 genuine issues;
			// the literal raw pre-dedup total exceeds maxScopedIssues=20,000.
			test := scaleP21SW182Case{name: "full-scope", schedule: "immediate", bulk: 1001, rawHeaders: 10500,
				collision: true, background: true, inherited: true, poisonCount: true, fullFallback: true, minimal: true, stableHistory: true}
			result := scaleP21SW182Pair(t, f, test, reload, "")
			if len(result.After.Issues) != 1011 || result.Events != 1 || result.Peak == 0 {
				t.Fatal("full fallback omitted native rows or its publication owner", len(result.After.Issues), result.Events, result.Peak)
			}
		})
	}
}

func TestScaleP21SW182CycleScheduleAdmissionShape(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21SW182Setup(t)
	var singlePeak int
	for _, duplicates := range []int{1, 64} {
		t.Run(fmt.Sprintf("duplicate-teams=%d", duplicates), func(t *testing.T) {
			test := scaleP21SW182Case{name: "future-batch", schedule: "empty", duplicates: duplicates, upcoming: 256}
			result := scaleP21SW182Pair(t, f, test, false, f.data.Workspace.URLKey)
			if len(result.Cycles) != 258 {
				t.Fatal("literal duplicate headers generated a second future batch", len(result.Cycles))
			}
			if singlePeak == 0 {
				singlePeak = result.Peak
			}
			if result.Peak > singlePeak+(2<<20) {
				t.Fatal("duplicate headers multiplied future-cycle reservations", singlePeak, result.Peak)
			}
		})
	}
	var smallPeak int
	for _, copies := range []int{1, 64} {
		t.Run(fmt.Sprintf("unrelated-body-growth=%d", copies), func(t *testing.T) {
			// Large race fixtures can take longer than the creator's grace
			// window. Prove explicit appended history there; the small table
			// still proves suppression inside the initial three minutes.
			test := scaleP21SW182Case{name: "unrelated-growth", schedule: "cooldown", bodyCopies: copies, stableHistory: copies > 1}
			result := scaleP21SW182Pair(t, f, test, true, f.data.Workspace.URLKey)
			if smallPeak == 0 {
				smallPeak = result.Peak
			}
			if result.Peak > smallPeak+(2<<20) {
				t.Fatal("unrelated resource bodies entered the cycle ticker allowance", smallPeak, result.Peak)
			}
		})
	}
}

func TestScaleP21SW182CycleScheduleRefusalRollbackAndRelease(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21SW182Setup(t)
	test := scaleP21SW182Case{name: "failure", schedule: "immediate", collision: true}
	for _, failure := range []string{"cancelled", "rollback", "missing-workspace"} {
		t.Run(failure, func(t *testing.T) {
			now, key := time.Now().UTC(), f.data.Workspace.URLKey
			if failure == "missing-workspace" {
				key = f.prefix + "missing-workspace"
			}
			old := scaleP21SW182Tick(t, f, f.repository, test, now, false, key, failure)
			bounded := scaleP21SW182Tick(t, f, f.probe, test, now, false, key, failure)
			if !reflect.DeepEqual(old.Normalized, bounded.Normalized) {
				t.Fatal("refused ticker differs from literal state/publications", scaleP21M2Diff(old.Normalized, bounded.Normalized))
			}
		})
	}
	t.Run("aggregate-admission-refusal", func(t *testing.T) {
		result := scaleP21SW182Tick(t, f, f.probe, test, time.Now().UTC(), false, f.data.Workspace.URLKey, "budget")
		if result.Events != 0 || !reflect.DeepEqual(result.Before, result.After) {
			t.Fatal("admission refusal committed a cycle/issue/history mutation")
		}
	})
	// The same bounded store remains usable after cancellation and rollback.
	scaleP21SW182Pair(t, f, scaleP21SW182Case{name: "after-refusal", schedule: "immediate", background: true}, false, "")
}
