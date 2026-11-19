//go:build loadtest

package main

// TestLoadRigSeed seeds one realistic workspace for the bounded-metadata load
// rig (scripts/loadrig/loadrig.sh seed). The size is FLOW_LOADRIG_SCALE times
// a mid-size production workspace (1 = 5,000 issues with comments and
// history, 2,321 projects, ~100 documents with large bodies, cycles, labels,
// views, loops, workflows, webhooks, API keys and OAuth applications).
//
// A bounded store refuses to start on an empty database, so the seed runs in
// two phases on the target database (SQLite path or PostgreSQL URL from the
// usual FLOW_DATABASE_* variables):
//
//	A. flag off (record hydration and journal on): the configuration through
//	   the API on an in-process server, members and sessions, issues through
//	   the production bulk importer and their comments and activity through
//	   the production content writer, plus a sample of real HTTP edits;
//	B. flag on (FLOW_LOADRIG_SEED_WRITER=bounded, the default): the large
//	   metadata collections through the bounded seed writer, which appends
//	   records without decoding the workspace (the legacy full mutation is
//	   quadratic in the workspace size). FLOW_LOADRIG_SEED_WRITER=legacy uses
//	   the flag-off writer instead.
//
// It writes FLOW_LOADRIG_OUT/fixture.json (0600: session tokens and API key
// secrets) and FLOW_LOADRIG_OUT/seed.json (timings and sizes, no secrets).

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func loadRigDatabaseConfig() store.DatabaseConfig {
	return store.DatabaseConfig{
		Driver: loadRigEnv("FLOW_DATABASE_DRIVER", "sqlite"), Path: os.Getenv("FLOW_DATABASE_PATH"), URL: os.Getenv("FLOW_DATABASE_URL"),
		FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 8, MaxIdleConns: 8, MaxTransactionBytes: 32 << 20,
	}
}

func TestLoadRigSeed(t *testing.T) {
	out := os.Getenv("FLOW_LOADRIG_OUT")
	if os.Getenv("FLOW_LOADRIG_SEED") != "1" || out == "" {
		t.Skip("set FLOW_LOADRIG_SEED=1 and FLOW_LOADRIG_OUT")
	}
	scale := loadRigEnvFloat(t, "FLOW_LOADRIG_SCALE", 1)
	scaled := func(base float64) int { return max(1, int(math.Round(base*scale))) }
	users := loadRigEnvInt(t, "FLOW_LOADRIG_USERS", min(2000, scaled(40)))
	issueCount := loadRigEnvInt(t, "FLOW_LOADRIG_ISSUES", scaled(5000))
	httpEdits := loadRigEnvInt(t, "FLOW_LOADRIG_HTTP_EDITS", min(120, scaled(40)))
	writer := loadRigEnv("FLOW_LOADRIG_SEED_WRITER", "bounded")
	if writer != "bounded" && writer != "legacy" {
		t.Fatal("FLOW_LOADRIG_SEED_WRITER must be bounded or legacy")
	}
	config := loadRigDatabaseConfig()
	if config.Driver == "sqlite" && config.Path == "" {
		t.Fatal("FLOW_DATABASE_PATH is required for a SQLite seed")
	}
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	report := map[string]any{"scale": scale, "driver": config.Driver, "writer": writer, "started": time.Now().UTC()}
	phases := map[string]float64{}
	problems := []string{}
	note := func(format string, args ...any) {
		message := fmt.Sprintf(format, args...)
		problems = append(problems, message)
		t.Log(message)
	}
	timed := func(name string, run func()) {
		began := time.Now()
		run()
		phases[name] = time.Since(began).Seconds()
		t.Logf("seed %s: %.1fs", name, phases[name])
	}
	ctx := t.Context()
	fixture := loadRigFixture{Workspace: "test-workspace", Scale: scale, Extra: map[string][]string{}}

	// Phase A: flag off.
	var repo *store.SQLiteStore
	timed("open-legacy", func() {
		var err error
		if repo, err = store.OpenDatabase(config); err != nil {
			t.Fatal(err)
		}
	})
	service := &server{store: repo, uploadPath: t.TempDir()}
	host := httptest.NewServer(newHandler(service))
	admin := peerAdmin(t, host.URL)
	ws := fixture.Workspace
	call := func(method, path string, input, out any) bool {
		if _, err := loadRigCall(ctx, admin, method, host.URL+path, ws, input, out); err != nil {
			note("seed: %v", err)
			return false
		}
		return true
	}
	fixture.WorkspaceID = repo.Bootstrap().Workspace.ID
	adminID := scaleP01IdentityFromClient(t, host.URL, admin, "admin", nil)
	fixture.Admin = loadRigIdentity{ID: adminID.ID, Token: adminID.Token, Role: "admin"}
	suffix := fmt.Sprintf("%04d", time.Now().Unix()%10000)

	teamCount := loadRigEnvInt(t, "FLOW_LOADRIG_TEAMS", min(24, max(6, scaled(6))))
	timed("teams", func() {
		names := []string{"Platform", "Web", "Mobile", "Data", "Infrastructure", "Growth", "Security", "Design", "Billing", "Search", "Support", "Docs"}
		for index := range teamCount {
			key := fmt.Sprintf("L%c%s", 'A'+rune(index%26), suffix[len(suffix)-2:])
			var team domain.Team
			if !call(http.MethodPost, "/api/workspaces/"+ws+"/teams", map[string]any{"name": fmt.Sprintf("%s %d", names[index%len(names)], index), "key": key, "private": index >= 2}, &team) {
				t.Fatal("team creation failed")
			}
			fixture.Teams = append(fixture.Teams, loadRigTeam{ID: team.ID, Key: team.Key, Private: team.Private})
		}
	})
	teamIDs := []string{}
	for _, team := range fixture.Teams {
		teamIDs = append(teamIDs, team.ID)
	}
	fixture.Admin.TeamIDs = teamIDs

	timed("members", func() {
		people, err := repo.SeedScaleP01Principals(ctx, ws, teamIDs, users, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, person := range people {
			fixture.Identities = append(fixture.Identities, loadRigIdentity{ID: person.User.ID, Token: person.Token, Role: person.Role, TeamIDs: person.TeamIDs})
		}
		if err := repo.ReloadWorkspace(ctx, ws); err != nil {
			t.Fatal(err)
		}
	})

	timed("configuration", func() {
		for index := range 24 {
			var label domain.IssueLabel
			if call(http.MethodPost, "/api/labels", map[string]any{"name": fmt.Sprintf("Load label %02d", index), "color": "#4cb782", "description": "load rig"}, &label) {
				fixture.LabelIDs = append(fixture.LabelIDs, label.ID)
			}
		}
		for _, team := range teamIDs {
			call(http.MethodPatch, "/api/teams/"+team+"/cycle-settings", map[string]any{"enabled": true, "autoCreate": true, "autoMigrate": true, "upcomingCount": 2}, nil)
		}
		for index := range scaled(20) {
			var view domain.SavedView
			if call(http.MethodPost, "/api/views", map[string]any{"name": fmt.Sprintf("Load view %d", index), "resource": "issues", "scope": "workspace"}, &view) {
				fixture.ViewIDs = append(fixture.ViewIDs, view.ID)
			}
		}
		for index := range scaled(20) {
			var project domain.Project
			if call(http.MethodPost, "/api/projects", map[string]any{"name": fmt.Sprintf("Load roadmap %d", index), "teamIds": []string{teamIDs[index%len(teamIDs)]}}, &project) {
				fixture.Projects = append(fixture.Projects, loadRigRef{ID: project.ID, TeamID: teamIDs[index%len(teamIDs)]})
			}
		}
		// Documents with large bodies (128 KiB to 512 KiB).
		for index := range scaled(12) {
			var document domain.Document
			body := loadRigFiller(fmt.Sprintf("Runbook %d", index), (128<<10)*(1+index%4))
			if call(http.MethodPost, "/api/documents", map[string]any{"title": fmt.Sprintf("Load runbook %d", index), "content": body, "teamIds": []string{teamIDs[index%len(teamIDs)]}}, &document) {
				fixture.Documents = append(fixture.Documents, loadRigRef{ID: document.ID, TeamID: teamIDs[index%len(teamIDs)]})
			}
		}
		for index := range max(4, scaled(8)) {
			var loop domain.Loop
			if call(http.MethodPost, "/api/loops", map[string]any{"name": fmt.Sprintf("Load loop %d", index), "status": "published",
				"instructions": "Read the team list, then call finish_run with a one-line summary. Only read; do not create or change anything. Set notDone to []."}, &loop) {
				fixture.LoopIDs = append(fixture.LoopIDs, loop.ID)
			}
		}
		for index, schedule := range []string{"* * * * *", "*/5 * * * *", "0 9 * * *"} {
			var workflow domain.WorkflowDefinition
			if call(http.MethodPost, "/api/workflows", map[string]any{"name": fmt.Sprintf("Load reminder %d", index), "trigger": "schedule", "schedule": schedule, "actions": []map[string]any{{"type": "notify", "config": map[string]string{}}}, "maxAttempts": 3}, &workflow) {
				fixture.Extra["workflows"] = append(fixture.Extra["workflows"], workflow.ID)
			}
		}
		for index, types := range [][]string{{"issues"}, {"issues", "comments"}, {"projects", "documents"}} {
			var hook domain.Webhook
			// .invalid never resolves: deliveries run, fail and retry without leaving this machine.
			if call(http.MethodPost, "/api/webhooks", map[string]any{"name": fmt.Sprintf("Load hook %d", index), "url": fmt.Sprintf("https://hooks-%d.loadrig.invalid/flow", index), "resourceTypes": types, "enabled": true}, &hook) {
				fixture.Extra["webhooks"] = append(fixture.Extra["webhooks"], hook.ID)
			}
		}
		for index, spec := range []struct {
			scopes []string
			teams  []string
		}{{[]string{"read", "write"}, nil}, {[]string{"read"}, nil}, {[]string{"read"}, teamIDs[:1]}, {[]string{"read"}, teamIDs[2:4]}} {
			var key struct {
				ID  string `json:"id"`
				Key struct {
					ID string `json:"id"`
				} `json:"key"`
				Secret string `json:"secret"`
			}
			input := map[string]any{"name": fmt.Sprintf("Load key %d", index), "scopes": spec.scopes}
			if spec.teams != nil {
				input["teamIds"] = spec.teams
			}
			if call(http.MethodPost, "/api/api-keys", input, &key) && key.Secret != "" {
				fixture.APIKeys = append(fixture.APIKeys, loadRigAPIKey{ID: cmp.Or(key.Key.ID, key.ID), Secret: key.Secret, TeamIDs: spec.teams, Scopes: spec.scopes})
			}
		}
		for index := range 2 {
			var app struct {
				ID string `json:"id"`
			}
			if call(http.MethodPost, "/api/oauth-applications", map[string]any{"name": fmt.Sprintf("Load app %d", index), "scopes": []string{"read"}, "redirectUris": []string{fmt.Sprintf("https://app-%d.loadrig.invalid/callback", index)}}, &app) {
				fixture.Extra["oauthApplications"] = append(fixture.Extra["oauthApplications"], app.ID)
			}
		}
		for index := range 4 {
			call(http.MethodPost, "/api/issue-templates", map[string]any{"name": fmt.Sprintf("Load template %d", index), "teamId": teamIDs[index%len(teamIDs)]}, nil)
		}
		for index := range scaled(10) {
			call(http.MethodPost, "/api/customers", map[string]any{"name": fmt.Sprintf("Load customer %d", index)}, nil)
		}
	})

	data, ok := repo.BootstrapFor(ws)
	if !ok {
		t.Fatal("seeded workspace missing")
	}
	world := newLoadRigWorld(data, fixture, issueCount)
	timed("issues", func() {
		for start := 0; start < issueCount; start += 1000 {
			batch := make([]domain.Issue, 0, 1000)
			for i := start; i < min(issueCount, start+1000); i++ {
				batch = append(batch, world.issue(i))
			}
			if err := repo.ImportIssues(ctx, ws, batch); err != nil {
				t.Fatalf("import issues %d: %v", start, err)
			}
		}
	})
	fixture.IssueCount = issueCount
	timed("issue-content", func() {
		batch := []store.LoadRigContent{}
		flush := func() {
			if err := repo.SeedLoadRigContent(ctx, ws, batch); err != nil {
				t.Fatalf("seed content: %v", err)
			}
			batch = batch[:0]
		}
		for i := range issueCount {
			batch = append(batch, world.content(i)...)
			if len(batch) >= 2000 {
				flush()
			}
		}
		flush()
	})
	if err := repo.ReloadWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	// Issue references the driver uses: a spread sample over every team.
	step := max(1, issueCount/4000)
	if step%5 == 0 {
		step++
	}
	for i := 0; i < issueCount; i += step {
		if i%25 != 0 { // archived issues refuse edits
			fixture.Issues = append(fixture.Issues, world.ref(i))
		}
	}
	timed("http-edits", func() {
		var wait sync.WaitGroup
		var mu sync.Mutex
		jobs := make(chan int)
		for range 4 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				for i := range jobs {
					index := (i * 7919) % issueCount
					if index%25 == 0 { // archived issues refuse edits
						index = (index + 1) % issueCount
					}
					ref := world.ref(index)
					var err error
					for attempt := 0; attempt < 3; attempt++ {
						var status int
						if status, err = loadRigCall(ctx, admin, http.MethodPatch, host.URL+"/api/issue-records/"+ref.ID, ws, map[string]any{"priority": 1 + i%4}, nil); err == nil {
							status, err = loadRigCall(ctx, admin, http.MethodPost, host.URL+"/api/issue-records/"+ref.ID+"/comments", ws, map[string]any{"body": fmt.Sprintf("Seed follow-up %d", i)}, nil)
						}
						if status != http.StatusConflict {
							break
						}
					}
					if err != nil {
						mu.Lock()
						note("seed edit: %v", err)
						mu.Unlock()
					}
				}
			}()
		}
		for i := range httpEdits {
			jobs <- i
		}
		close(jobs)
		wait.Wait()
	})
	fixture.Words = []string{"payment", "webhook", "search", "import", "cycle", "milestone", "attachment", "inbox", "comment", "notification", "Load"}
	host.Close()
	_ = service.stopDeliveryScheduler(context.Background())
	completeCatalog := func(name string) {
		timed(name, func() {
			collections, err := repo.CompleteLoadRigMetadataCatalog(ctx)
			if err != nil {
				t.Fatalf("metadata catalog: %v", err)
			}
			t.Logf("metadata catalog complete over %d collections", collections)
		})
	}
	// A bounded store starts only on a complete metadata catalog (in
	// production: FLOW_METADATA_CATALOG=shadow on the flag-off replicas).
	completeCatalog("catalog-legacy")
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}

	// Phase B: the large metadata collections.
	identityIDs := []string{fixture.Admin.ID}
	for _, identity := range fixture.Identities {
		identityIDs = append(identityIDs, identity.ID)
	}
	spec := store.SyntheticMetadata{
		Projects: 2321, ProjectBytes: 12000, Trash: 210, TrashBytes: 41000, Documents: 78, DocumentBytes: 64000, Conversations: 400, ConversationBytes: 2000,
		CountScale: scale, BodyScale: 1, TeamIDs: teamIDs, UserIDs: identityIDs, Notifications: len(identityIDs) * 2, Relations: true,
	}
	if writer == "bounded" {
		t.Setenv("FLOW_BOUNDED_METADATA", "on")
	}
	if bytes := loadRigEnvInt(t, "FLOW_LOADRIG_SEED_BATCH_BYTES", 0); bytes > 0 {
		store.SetLoadRigSeedBatchBytes(bytes)
	}
	timed("open-"+writer, func() {
		var err error
		if repo, err = store.OpenDatabase(config); err != nil {
			t.Fatalf("open the %s store for the metadata seed: %v", writer, err)
		}
	})
	timed("metadata", func() {
		err := repo.SeedSyntheticMetadata(ctx, ws, spec)
		if errors.Is(err, store.ErrBoundedMetadataCoverage) && writer == "bounded" {
			// The bounded seed writer commits canonical rows only; its final
			// reload refuses them until the catalog covers them (activation
			// blocker 11). Complete the catalog and reload again.
			note("bounded seed writer: final reload refused until the catalog is completed (%v)", err)
			if _, err = repo.CompleteLoadRigMetadataCatalog(ctx); err == nil {
				err = repo.ReloadWorkspace(ctx, ws)
			}
		}
		if err != nil {
			t.Fatalf("seed metadata: %v", err)
		}
	})
	completeCatalog("catalog-" + writer)
	for i := range int(math.Ceil(78 * scale)) {
		fixture.Documents = append(fixture.Documents, loadRigRef{ID: fmt.Sprintf("lt-doc-%04d", i), TeamID: teamIDs[i%len(teamIDs)]})
	}
	for i := 0; i < int(math.Ceil(2321*scale)); i += max(1, int(2321*scale)/200) {
		fixture.Projects = append(fixture.Projects, loadRigRef{ID: fmt.Sprintf("lt-project-%05d", i), TeamID: teamIDs[i%len(teamIDs)]})
	}
	if err := repo.ExtendLoadRigSessions(ctx, identityIDs, 30*24*time.Hour); err != nil {
		note("extend sessions: %v", err)
	}
	sizes, err := repo.WorkspaceStateBytes(ctx, ws)
	if err != nil {
		note("workspace sizes: %v", err)
	}
	report["sizes"] = sizes
	report["rows"] = repo.LoadRigTableRows(ctx, ws)
	report["databaseBytes"] = repo.LoadRigDatabaseBytes(ctx)
	if config.Driver == "sqlite" {
		total := int64(0)
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if info, err := os.Stat(config.Path + suffix); err == nil {
				total += info.Size()
			}
		}
		report["fileBytes"] = total
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	report["phasesSeconds"] = phases
	total := 0.0
	for _, seconds := range phases {
		total += seconds
	}
	report["totalSeconds"] = total
	report["counts"] = map[string]any{"teams": len(fixture.Teams), "members": len(fixture.Identities), "issues": issueCount, "httpEdits": httpEdits,
		"labels": len(fixture.LabelIDs), "views": len(fixture.ViewIDs), "apiProjects": scaled(20), "apiDocuments": scaled(12), "loops": len(fixture.LoopIDs),
		"apiKeys": len(fixture.APIKeys), "syntheticProjects": int(math.Ceil(2321 * scale)), "syntheticDocuments": int(math.Ceil(78 * scale)),
		"trash": int(math.Ceil(210 * scale)), "conversations": int(math.Ceil(400 * scale)), "notifications": int(math.Ceil(float64(spec.Notifications) * scale))}
	report["problems"] = problems
	fixture.Seed = map[string]any{"scale": scale, "issues": issueCount, "writer": writer}
	if err := loadRigWriteJSON(filepath.Join(out, "fixture.json"), fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadRigWriteJSON(filepath.Join(out, "seed.json"), report, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("seed done in %.1fs (%d problems); report %s", total, len(problems), filepath.Join(out, "seed.json"))
}

func loadRigFiller(seed string, size int) string {
	words := []string{"payment", "retry", "webhook", "queue", "search", "archived", "filter", "import", "cycle", "capacity", "milestone", "inbox", "attachment", "notification", "release"}
	var b strings.Builder
	b.Grow(size + 64)
	for line := 0; b.Len() < size; line++ {
		if line%12 == 0 {
			fmt.Fprintf(&b, "\n## %s section %d\n\n", seed, line/12)
		}
		fmt.Fprintf(&b, "%s %s %s step %d keeps the %s consistent.\n", words[line%len(words)], words[(line*7)%len(words)], words[(line*3)%len(words)], line, words[(line*5)%len(words)])
	}
	return b.String()[:size]
}

// loadRigWorld builds production-shaped issues and their content for the
// seeded teams, members, states, labels, projects and cycles.
type loadRigWorld struct {
	teams    []domain.Team
	states   map[string][]domain.WorkflowState
	labels   []domain.IssueLabel
	projects []domain.ProjectSummary
	cycles   map[string][]string
	users    []domain.User
}

func newLoadRigWorld(data domain.Bootstrap, fixture loadRigFixture, issues int) *loadRigWorld {
	world := &loadRigWorld{states: map[string][]domain.WorkflowState{}, cycles: map[string][]string{}}
	byID := map[string]domain.Team{}
	for _, team := range data.Teams {
		byID[team.ID] = team
	}
	for _, team := range fixture.Teams {
		world.teams = append(world.teams, byID[team.ID])
	}
	for _, team := range world.teams {
		for _, state := range data.States {
			if state.TeamID == team.ID {
				world.states[team.ID] = append(world.states[team.ID], state)
			}
		}
		if len(world.states[team.ID]) == 0 {
			for _, state := range data.States {
				if state.TeamID == "" && !state.Reserved {
					world.states[team.ID] = append(world.states[team.ID], state)
				}
			}
		}
	}
	for _, label := range data.Labels {
		if slices.Contains(fixture.LabelIDs, label.ID) {
			world.labels = append(world.labels, label)
		}
	}
	for _, project := range data.Projects {
		if slices.ContainsFunc(fixture.Projects, func(ref loadRigRef) bool { return ref.ID == project.ID }) {
			world.projects = append(world.projects, domain.ProjectSummary{ID: project.ID, Name: project.Name, Icon: project.Icon, Color: project.Color})
		}
	}
	for _, cycle := range data.Cycles {
		world.cycles[cycle.TeamID] = append(world.cycles[cycle.TeamID], cycle.ID)
	}
	members := map[string]bool{}
	for _, identity := range fixture.Identities {
		members[identity.ID] = true
	}
	for _, user := range data.Users {
		if members[user.ID] || user.ID == fixture.Admin.ID {
			world.users = append(world.users, user)
		}
	}
	if len(world.users) == 0 {
		world.users = []domain.User{data.Viewer}
	}
	return world
}

func (w *loadRigWorld) ref(i int) loadRigIssueRef {
	team := w.teams[i%len(w.teams)]
	return loadRigIssueRef{ID: fmt.Sprintf("lr-issue-%06d", i), Identifier: fmt.Sprintf("%s-%d", team.Key, i/len(w.teams)+1), TeamID: team.ID}
}

func (w *loadRigWorld) issue(i int) domain.Issue {
	ref := w.ref(i)
	team := w.teams[i%len(w.teams)]
	states := w.states[team.ID]
	state := states[(i*7)%len(states)]
	random := rand.New(rand.NewPCG(uint64(i), 17))
	created := time.Now().UTC().Add(-time.Duration(loadRigIssueAge(i)) * time.Minute)
	updated := created.Add(time.Duration(random.IntN(600)) * time.Minute)
	creator := w.users[(i*7)%len(w.users)]
	issue := domain.Issue{
		ID: ref.ID, Version: 1, Identifier: ref.Identifier, Number: i/len(w.teams) + 1,
		Title: loadRigIssueTitle(i), Description: loadRigIssueDescription(i) + "\n\n" + loadRigFiller("Notes", 400+random.IntN(2400)),
		Priority: i % 5, PriorityLabel: loadRigPriorityLabel(i % 5), SortOrder: float64(i),
		CreatedAt: created, UpdatedAt: updated, Team: team, State: state, Creator: creator,
		Labels: []domain.IssueLabel{}, SubscriberIDs: []string{creator.ID}, Reactions: map[string][]string{},
		SubIssueIDs: []string{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{},
	}
	if i%9 != 0 {
		assignee := w.users[(i*3)%len(w.users)]
		issue.Assignee = &assignee
		issue.SubscriberIDs = append(issue.SubscriberIDs, assignee.ID)
	}
	for n := 0; n < i%4 && len(w.labels) > 0; n++ {
		issue.Labels = append(issue.Labels, w.labels[(i+n*5)%len(w.labels)])
	}
	if i%2 == 0 && len(w.projects) > 0 {
		project := w.projects[i%len(w.projects)]
		issue.Project = &project
	}
	if cycles := w.cycles[team.ID]; i%3 == 0 && len(cycles) > 0 {
		cycle := cycles[i%len(cycles)]
		issue.CycleID = &cycle
	}
	if i%5 != 0 {
		estimate := float64([]int{1, 2, 3, 5, 8}[i%5])
		issue.Estimate = &estimate
	}
	if i%20 == 0 && i >= 3*len(w.teams) {
		issue.Relations = append(issue.Relations, domain.IssueRelation{ID: fmt.Sprintf("lr-rel-%06d", i), Type: "related", IssueID: ref.ID, RelatedIssueID: w.ref(i - 3*len(w.teams)).ID})
	}
	switch state.Type {
	case "completed":
		at := updated
		issue.CompletedAt = &at
	case "canceled":
		at := updated
		issue.CanceledAt = &at
	case "started":
		at := created.Add(time.Hour)
		issue.StartedAt = &at
	}
	if i%25 == 0 {
		at := updated.Add(time.Hour)
		issue.ArchivedAt = &at
	}
	return issue
}

func loadRigIssueAge(i int) int { return 60*24*180 - i%(60*24*180) }

func (w *loadRigWorld) content(i int) []store.LoadRigContent {
	ref := w.ref(i)
	created := time.Now().UTC().Add(-time.Duration(loadRigIssueAge(i)) * time.Minute)
	items := []store.LoadRigContent{}
	for c := 0; c < i%5; c++ {
		user := w.users[(i+c)%len(w.users)]
		items = append(items, store.LoadRigContent{Kind: "comment", Resource: ref.ID, Item: domain.Comment{ID: fmt.Sprintf("lr-comment-%06d-%d", i, c), Version: 1,
			Body: fmt.Sprintf("Comment %d: %s", c, loadRigIssueDescription(i+c)), Reactions: map[string][]string{}, CreatedAt: created.Add(time.Duration(c*7) * time.Minute), User: user}})
	}
	for a := 0; a < 3+i%4; a++ {
		user := w.users[(i+a)%len(w.users)]
		field := []string{"status", "priority", "assignee", "labels"}[a%4]
		items = append(items, store.LoadRigContent{Kind: "activity", Resource: ref.ID, Item: domain.ActivityEvent{ID: fmt.Sprintf("lr-activity-%06d-%d", i, a), Type: "issue.updated",
			CreatedAt: created.Add(time.Duration(a) * time.Minute), Actor: user, Metadata: domain.ActivityMetadata{"field": field, "to": fmt.Sprintf("value %d", a)}}})
	}
	return items
}

func loadRigIssueTitle(i int) string {
	titles := []string{"Payment retry drops webhook delivery", "Search results ignore archived filter", "Import stalls on duplicate identifiers",
		"Notification grouping merges unrelated issues", "Board columns scroll past the viewport", "Cycle capacity ignores estimates",
		"Project milestone target date drifts", "Attachment upload retries forever", "Inbox unread count is stale after sync", "Comment thread pagination repeats a row"}
	return fmt.Sprintf("%s (#%d)", titles[i%len(titles)], i)
}

func loadRigIssueDescription(i int) string {
	descriptions := []string{"The payment worker retries every request but the webhook is dropped when the queue is full.",
		"Search returns archived issues because the filter is applied after the candidate page.",
		"The import stops at the first duplicate identifier instead of continuing with the rest.",
		"Grouping notifications by group key merges activity from unrelated threads.",
		"Horizontal scrolling on the board keeps the header fixed above the columns.",
		"Cycle capacity counts unestimated issues as one point even when the team disabled that.",
		"Milestone target dates move whenever the project is saved with a new timezone.",
		"Attachment upload failures are retried with no backoff and never expire.",
		"The inbox badge keeps the previous count until a full reload finishes.",
		"A comment page boundary repeats the last row of the previous page."}
	return descriptions[i%len(descriptions)]
}

func loadRigPriorityLabel(priority int) string {
	return []string{"No priority", "Urgent", "High", "Medium", "Low"}[priority%5]
}
