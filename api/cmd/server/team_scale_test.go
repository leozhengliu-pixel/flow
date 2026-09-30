package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/pprof"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// TestTeamMutationsAtScale times team and membership administration (and a
// sample of everyday edits) against a workspace shaped like a large
// production tenant: ~75k issue records, ~200k workspace metadata records,
// hundreds of teams and users. Opt-in:
//
//	FLOW_SCALE_TEST=1 go test ./cmd/server -run TestTeamMutationsAtScale -v -timeout 60m
//
// FLOW_SCALE_DATABASE_DRIVER (sqlite|mysql|postgres) and FLOW_SCALE_DATABASE_URL
// select an external database; FLOW_SCALE_SQLITE_PATH keeps a SQLite fixture.
// THE EXTERNAL DATABASE IS WIPED unless FLOW_SCALE_REUSE=1 finds an already
// seeded fixture. FLOW_SCALE_ISSUES, FLOW_SCALE_METADATA, FLOW_SCALE_REPEAT and
// FLOW_SCALE_CASCADE_REPEAT override the sizes; FLOW_SCALE_AUTH=0 runs the
// auth-disabled (development) handlers instead of signed-in ones;
// FLOW_SCALE_LOG_EACH=1 logs every request as it completes;
// FLOW_SCALE_OPEN_ONLY=1 only times opening the store (FLOW_SCALE_OPEN_PROFILE
// writes a CPU profile of it).
func TestTeamMutationsAtScale(t *testing.T) {
	if os.Getenv("FLOW_SCALE_TEST") != "1" {
		t.Skip("set FLOW_SCALE_TEST=1 to run the team mutation timing test")
	}
	driver := strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_SCALE_DATABASE_DRIVER")))
	if driver == "" {
		driver = "sqlite"
	}
	url := os.Getenv("FLOW_SCALE_DATABASE_URL")
	config := store.DatabaseConfig{Driver: driver, URL: url, FixtureProfile: "test", FixturePassword: "test-password"}
	if driver == "sqlite" {
		config.Path = os.Getenv("FLOW_SCALE_SQLITE_PATH")
		if config.Path == "" {
			config.Path = filepath.Join(t.TempDir(), "flow.db")
		}
		config.MaxOpenConns = 1
		url = config.Path
	} else if url == "" {
		t.Fatal("FLOW_SCALE_DATABASE_URL is required for " + driver)
	}
	issues := scaleEnvInt("FLOW_SCALE_ISSUES", 75000)
	metadata := scaleEnvInt("FLOW_SCALE_METADATA", 200000)
	repeat := scaleEnvInt("FLOW_SCALE_REPEAT", 10)
	auth := os.Getenv("FLOW_SCALE_AUTH") != "0"

	db := openScaleDatabase(t, driver, url)
	defer db.Close()
	seeded := false
	if os.Getenv("FLOW_SCALE_REUSE") == "1" {
		var count int
		if db.QueryRow(`SELECT COUNT(*) FROM issue_records WHERE workspace_key='test-workspace'`).Scan(&count) == nil && count >= issues {
			seeded = true
			t.Logf("reusing seeded %s fixture with %d issues", driver, count)
		}
	}
	if !seeded {
		started := time.Now()
		if driver != "sqlite" {
			resetScaleDatabase(t, db)
		}
		repository, err := store.OpenDatabase(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := repository.Close(); err != nil {
			t.Fatal(err)
		}
		seedTeamScaleFixture(t, db, issues, metadata)
		t.Logf("seeded %s fixture in %s", driver, time.Since(started).Round(time.Millisecond))
	}
	opened := time.Now()
	if profile := os.Getenv("FLOW_SCALE_OPEN_PROFILE"); profile != "" {
		file, err := os.Create(profile)
		if err != nil {
			t.Fatal(err)
		}
		if err := pprof.StartCPUProfile(file); err != nil {
			t.Fatal(err)
		}
	}
	repository, err := store.OpenDatabase(config)
	if os.Getenv("FLOW_SCALE_OPEN_PROFILE") != "" {
		pprof.StopCPUProfile()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if os.Getenv("FLOW_SCALE_OPEN_ONLY") == "1" {
		t.Logf("opened store in %s", time.Since(opened).Round(time.Millisecond))
		return
	}
	if loaded, ok := repository.WorkspaceMetadataFields("test-workspace", "users", "teams", "issueSlas", "slaEvents", "subscriptions", "auditLog", "favorites", "integrationDeliveries", "trash"); ok {
		t.Logf("opened store in %s; in memory: %d users, %d teams, %d SLAs, %d SLA events, %d subscriptions, %d audit, %d favorites, %d deliveries, %d trash", time.Since(opened).Round(time.Millisecond), len(loaded.Users), len(loaded.Teams), len(loaded.IssueSLAs), len(loaded.SLAEvents), len(loaded.Subscriptions), len(loaded.AuditLog), len(loaded.Favorites), len(loaded.IntegrationDeliveries), len(loaded.Trash))
	}
	if auth {
		t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	}
	srv := &server{store: repository, uploadPath: t.TempDir(), authDisabled: !auth}
	api := httptest.NewServer(newHandler(srv))
	defer api.Close()
	client := authClient(t)
	if auth {
		authRequest[domain.AuthSession](t, client, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	}
	samples := map[string][]time.Duration{}
	order := []string{}
	timed := func(label, method, path string, input any, want ...int) map[string]any {
		t.Helper()
		var body io.Reader
		if input != nil {
			raw, _ := json.Marshal(input)
			body = bytes.NewReader(raw)
		}
		request, _ := http.NewRequest(method, api.URL+path, body)
		request.Header.Set("X-Workspace-Key", "test-workspace")
		if input != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		begin := time.Now()
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		elapsed := time.Since(begin)
		if _, known := samples[label]; !known {
			order = append(order, label)
		}
		samples[label] = append(samples[label], elapsed)
		if os.Getenv("FLOW_SCALE_LOG_EACH") == "1" {
			t.Logf("%s %s %s: %d ms", label, method, path, elapsed.Milliseconds())
		}
		if len(want) > 0 && !slices.Contains(want, response.StatusCode) || len(want) == 0 && response.StatusCode >= 300 {
			t.Errorf("%s: status %d: %.300s", label, response.StatusCode, raw)
			return map[string]any{}
		}
		var decoded map[string]any
		_ = json.Unmarshal(raw, &decoded)
		return decoded
	}
	id := func(value map[string]any) string {
		text, _ := value["id"].(string)
		return text
	}
	ws := "/api/workspaces/test-workspace"
	suffix := strconv.FormatInt(time.Now().UnixNano()%(36*36*36), 36)
	created := []string{}
	for i := 0; i < repeat; i++ {
		key := strings.ToUpper(fmt.Sprintf("Q%s%d", suffix, i))
		if len(key) > 5 {
			key = key[:5]
		}
		team := timed("team create", http.MethodPost, ws+"/teams", map[string]any{"name": "Scale created " + key, "key": key})
		created = append(created, id(team))
	}
	for i, teamID := range created {
		timed("team rename", http.MethodPatch, ws+"/teams/"+teamID, map[string]any{"name": fmt.Sprintf("Scale renamed %d", i)})
	}
	for _, teamID := range created {
		timed("team set parent", http.MethodPatch, "/api/teams/"+teamID+"/settings", map[string]any{"parentTeamId": "team_scale_2"})
		timed("team timezone", http.MethodPatch, "/api/teams/"+teamID+"/settings", map[string]any{"timezone": "Asia/Shanghai"})
		timed("team clear parent", http.MethodPatch, "/api/teams/"+teamID+"/settings", map[string]any{"parentTeamId": ""})
	}
	target := "team_scale_3"
	for i := 0; i < repeat; i++ {
		user := fmt.Sprintf("usr_scale_%d", 200+i)
		timed("member add", http.MethodPut, ws+"/teams/"+target+"/members/"+user, map[string]any{"member": true, "role": "member"}, http.StatusNoContent)
		timed("member role owner", http.MethodPut, ws+"/teams/"+target+"/members/"+user, map[string]any{"member": true, "role": "owner"}, http.StatusNoContent)
		timed("member role member", http.MethodPut, ws+"/teams/"+target+"/members/"+user, map[string]any{"member": true, "role": "member"}, http.StatusNoContent)
		timed("member remove", http.MethodPut, ws+"/teams/"+target+"/members/"+user, map[string]any{"member": false}, http.StatusNoContent)
	}
	if !seeded {
		timed("member remove (40 issues)", http.MethodPut, ws+"/teams/team_scale_1/members/usr_scale_250", map[string]any{"member": false}, http.StatusNoContent)
		timed("member remove (600 issues)", http.MethodPut, ws+"/teams/team_scale_1/members/usr_scale_251", map[string]any{"member": false}, http.StatusNoContent)
	}
	if !seeded {
		var assigned, subscribed int
		if err := db.QueryRow(`SELECT COUNT(*) FROM issue_records WHERE workspace_key='test-workspace' AND team_id='team_scale_1' AND assignee_id='usr_scale_250'`).Scan(&assigned); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM issue_subscriber_records WHERE workspace_key='test-workspace' AND user_id='usr_scale_250'`).Scan(&subscribed); err != nil {
			t.Fatal(err)
		}
		if assigned != 0 || subscribed != 0 {
			t.Errorf("member removal left %d assignments and %d subscriptions", assigned, subscribed)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM issue_records WHERE workspace_key='test-workspace' AND team_id='team_scale_1' AND assignee_id='usr_scale_251'`).Scan(&assigned); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM issue_subscriber_records WHERE workspace_key='test-workspace' AND user_id='usr_scale_251'`).Scan(&subscribed); err != nil {
			t.Fatal(err)
		}
		if assigned != 0 || subscribed != 0 {
			t.Errorf("batched member removal left %d assignments and %d subscriptions", assigned, subscribed)
		}
	}
	for _, teamID := range created {
		timed("team delete (empty)", http.MethodDelete, ws+"/teams/"+teamID, nil, http.StatusNoContent)
	}
	for _, teamID := range created[:min(3, len(created))] {
		timed("team restore", http.MethodPost, ws+"/deleted-teams/"+teamID+"/restore", nil)
	}
	if !seeded {
		// A reused fixture already deleted team_scale_1.
		timed("team delete (with issues)", http.MethodDelete, ws+"/teams/team_scale_1", nil, http.StatusNoContent)
	}

	// A sample of the everyday fast paths from TestCommonMutationsAtScale.
	for i := 0; i < repeat; i++ {
		timed("project icon", http.MethodPatch, "/api/projects/project_aut", map[string]any{"icon": fmt.Sprintf("Rocket%d", i%2)})
		timed("team settings description", http.MethodPatch, "/api/teams/team_test/settings", map[string]any{"description": fmt.Sprintf("Scale team %d", i)})
		timed("label update", http.MethodPatch, "/api/labels/label_type_defect", map[string]any{"color": fmt.Sprintf("#00ff%02d", i)})
		timed("favorite add", http.MethodPut, "/api/favorites/project/project_aut", nil)
		timed("favorite remove", http.MethodDelete, "/api/favorites/project/project_aut", nil)
		timed("issue-record title", http.MethodPatch, "/api/issue-records/scale_"+strconv.Itoa(10+i), map[string]any{"title": fmt.Sprintf("Scale issue renamed %d", i)})
		timed("issue-record comment", http.MethodPost, "/api/issue-records/scale_"+strconv.Itoa(10+i)+"/comments", map[string]any{"body": "Scale comment"})
		timed("account settings", http.MethodPatch, "/api/account/settings", map[string]any{"fontSize": []string{"small", "default"}[i%2]})
	}
	// High-frequency settings and catalog writes (the workspace metadata tier).
	for i := 0; i < repeat; i++ {
		n := strconv.Itoa(i)
		timed("project display default", http.MethodPut, "/api/workspace/project-display-default", map[string]any{"display": map[string]any{"layout": []string{"list", "board"}[i%2]}})
		timed("workspace settings", http.MethodPut, "/api/workspace/settings", map[string]any{"name": "Test workspace " + n})
		timed("notification preferences", http.MethodPatch, "/api/notification-preferences", map[string]any{"soundEnabled": i%2 == 0})
		timed("account profile", http.MethodPatch, "/api/account/profile", map[string]any{"displayName": "Admin " + n, "username": "admin" + n})
		view := timed("view create", http.MethodPost, "/api/views", map[string]any{"name": "Scale view " + n, "resource": "issues", "scope": "workspace"})
		timed("view update", http.MethodPatch, "/api/views/"+id(view), map[string]any{"name": "Scale view renamed " + n})
		timed("view delete", http.MethodDelete, "/api/views/"+id(view), nil, http.StatusNoContent, http.StatusOK)
		label := timed("label create", http.MethodPost, "/api/labels", map[string]any{"name": "scale-label-" + suffix + n, "color": "#ff0000"})
		timed("label rename", http.MethodPatch, "/api/labels/"+id(label), map[string]any{"name": "scale-renamed-" + suffix + n})
		group := timed("label group create", http.MethodPost, "/api/label-groups", map[string]any{"name": "Scale group " + suffix + n})
		timed("label group update", http.MethodPatch, "/api/label-groups/"+id(group), map[string]any{"description": "Scale"})
		status := timed("project status create", http.MethodPost, "/api/project-statuses", map[string]any{"name": "Scale status " + suffix + n, "type": "started", "color": "#aaaaaa"})
		timed("project status update", http.MethodPatch, "/api/project-statuses/"+id(status), map[string]any{"color": "#bbbbbb"})
		template := timed("issue template create", http.MethodPost, "/api/issue-templates", map[string]any{"name": "Scale template " + n})
		timed("issue template update", http.MethodPatch, "/api/issue-templates/"+id(template), map[string]any{"name": "Scale template renamed " + n})
		customer := timed("customer create", http.MethodPost, "/api/customers", map[string]any{"name": "Scale customer " + n})
		timed("customer update", http.MethodPatch, "/api/customers/"+id(customer), map[string]any{"tier": "enterprise"})
		dashboard := timed("dashboard create", http.MethodPost, "/api/dashboards", map[string]any{"name": "Scale dashboard " + n})
		timed("dashboard update", http.MethodPatch, "/api/dashboards/"+id(dashboard), map[string]any{"name": "Scale dashboard renamed " + n})
		loop := timed("loop create", http.MethodPost, "/api/loops", map[string]any{"name": "Scale loop " + n, "instructions": "Summarize", "triggerType": "schedule"})
		timed("loop update", http.MethodPatch, "/api/loops/"+id(loop), map[string]any{"name": "Scale loop renamed " + n})
		timed("invitation create", http.MethodPost, ws+"/invitations", map[string]any{"emails": []string{"invitee" + suffix + n + "@example.test"}, "role": "member"}, http.StatusCreated, http.StatusOK)
		timed("project update post", http.MethodPost, "/api/projects/project_aut/updates", map[string]any{"body": "On track " + n, "health": "onTrack"})
		timed("project rename", http.MethodPatch, "/api/projects/project_aut", map[string]any{"name": "Test project " + n})
		timed("workflow state update", http.MethodPatch, "/api/teams/team_scale_5/states/team_scale_5_state_todo", map[string]any{"color": fmt.Sprintf("#12%04d", i)})
		timed("legacy issue title", http.MethodPatch, "/api/issues/scale_"+strconv.Itoa(20+i), map[string]any{"title": "Legacy renamed " + n})
		timed("legacy issue comment", http.MethodPost, "/api/issues/scale_"+strconv.Itoa(20+i)+"/comments", map[string]any{"body": "Legacy comment"})
	}
	runIssueCascadeScaleRoutes(t, srv, repository, timed, scaleEnvInt("FLOW_SCALE_CASCADE_REPEAT", repeat), suffix)
	initiative := timed("initiative create", http.MethodPost, "/api/initiatives", map[string]any{"name": "Scale initiative"})
	timed("initiative update", http.MethodPatch, "/api/initiatives/"+id(initiative), map[string]any{"summary": "Scale summary"})
	issue := timed("issue-record create", http.MethodPost, "/api/issue-records", map[string]any{"title": "Scale created issue", "teamId": "team_test"})
	timed("issue-record priority", http.MethodPatch, "/api/issue-records/"+id(issue), map[string]any{"priority": 1})
	timed("GET issue-records/bootstrap", http.MethodGet, "/api/issue-records/bootstrap", nil)

	extra := []string{}
	for label := range scaleExtraSamples {
		extra = append(extra, label)
	}
	sort.Strings(extra)
	for _, label := range extra {
		if _, known := samples[label]; !known {
			order = append(order, label)
		}
		samples[label] = append(samples[label], scaleExtraSamples[label]...)
	}
	var summary strings.Builder
	fmt.Fprintf(&summary, "\n  %-28s %5s %10s %10s %10s", "operation", "n", "p50 ms", "p95 ms", "max ms")
	for _, label := range order {
		values := slices.Clone(samples[label])
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		p := func(q float64) float64 {
			index := int(q*float64(len(values))+0.999999) - 1
			index = max(0, min(index, len(values)-1))
			return float64(values[index].Microseconds()) / 1000
		}
		fmt.Fprintf(&summary, "\n  %-28s %5d %10.1f %10.1f %10.1f", label, len(values), p(.5), p(.95), p(1))
	}
	t.Logf("%s (auth=%v) timings at %d issues / %d metadata records:%s", driver, auth, issues, metadata, summary.String())
}

func scaleEnvInt(name string, fallback int) int {
	if value, err := strconv.Atoi(os.Getenv(name)); err == nil && value > 0 {
		return value
	}
	return fallback
}

type scaleDB struct {
	*sql.DB
	driver string
}

func (db scaleDB) rebind(query string) string {
	if db.driver != "postgres" {
		return query
	}
	var builder strings.Builder
	argument := 1
	for _, r := range query {
		if r == '?' {
			builder.WriteString("$" + strconv.Itoa(argument))
			argument++
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func (db scaleDB) Exec(query string, args ...any) (sql.Result, error) {
	return db.DB.Exec(db.rebind(query), args...)
}

func (db scaleDB) QueryRow(query string, args ...any) *sql.Row {
	return db.DB.QueryRow(db.rebind(query), args...)
}

func (db scaleDB) Query(query string, args ...any) (*sql.Rows, error) {
	return db.DB.Query(db.rebind(query), args...)
}

func openScaleDatabase(t *testing.T, driver, url string) scaleDB {
	t.Helper()
	name, dsn := driver, url
	switch driver {
	case "postgres":
		name = "pgx"
	case "mysql":
		if strings.Contains(dsn, "?") {
			dsn += "&parseTime=true"
		} else {
			dsn += "?parseTime=true"
		}
	}
	db, err := sql.Open(name, dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return scaleDB{DB: db, driver: driver}
}

func resetScaleDatabase(t *testing.T, db scaleDB) {
	t.Helper()
	switch db.driver {
	case "postgres":
		for _, statement := range []string{`DROP SCHEMA public CASCADE`, `CREATE SCHEMA public`} {
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
	case "mysql":
		rows, err := db.Query(`SELECT table_name FROM information_schema.tables WHERE table_schema=DATABASE()`)
		if err != nil {
			t.Fatal(err)
		}
		tables := []string{}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			tables = append(tables, name)
		}
		rows.Close()
		if _, err := db.Exec(`SET FOREIGN_KEY_CHECKS=0`); err != nil {
			t.Fatal(err)
		}
		for _, table := range tables {
			if _, err := db.Exec("DROP TABLE IF EXISTS `" + table + "`"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(`SET FOREIGN_KEY_CHECKS=1`); err != nil {
			t.Fatal(err)
		}
	}
}

// scaleInserter batches multi-row INSERTs for any dialect.
type scaleInserter struct {
	t       *testing.T
	db      scaleDB
	prefix  string
	columns int
	rows    [][]any
}

func (in *scaleInserter) add(values ...any) {
	in.rows = append(in.rows, values)
	if len(in.rows) >= 400 {
		in.flush()
	}
}

func (in *scaleInserter) flush() {
	if len(in.rows) == 0 {
		return
	}
	placeholder := "(" + strings.TrimSuffix(strings.Repeat("?,", in.columns), ",") + ")"
	groups := make([]string, len(in.rows))
	args := make([]any, 0, len(in.rows)*in.columns)
	for i, row := range in.rows {
		groups[i] = placeholder
		args = append(args, row...)
	}
	if _, err := in.db.Exec(in.prefix+" VALUES "+strings.Join(groups, ","), args...); err != nil {
		in.t.Fatal(err)
	}
	in.rows = in.rows[:0]
}

func seedTeamScaleFixture(t *testing.T, db scaleDB, issueCount, metadataCount int) {
	t.Helper()
	const workspace, workspaceID = "test-workspace", "workspace_test"
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	const userCount, teamCount = 300, 400

	var passwordHash string
	if err := db.QueryRow(`SELECT password_hash FROM auth_users WHERE id='usr_admin'`).Scan(&passwordHash); err != nil {
		t.Fatal(err)
	}
	usersSQL := &scaleInserter{t: t, db: db, prefix: `INSERT INTO auth_users(id,email,name,display_name,avatar_url,password_hash,email_verified_at,active,created_at,updated_at)`, columns: 10}
	membersSQL := &scaleInserter{t: t, db: db, prefix: `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at)`, columns: 5}
	for i := 0; i < userCount; i++ {
		userID := fmt.Sprintf("usr_scale_%d", i)
		usersSQL.add(userID, fmt.Sprintf("scale%d@example.test", i), fmt.Sprintf("Scale user %d", i), fmt.Sprintf("scale%d", i), "", passwordHash, stamp, 1, stamp, stamp)
		membersSQL.add(workspaceID, userID, "member", "active", stamp)
	}
	usersSQL.flush()
	membersSQL.flush()

	// Metadata records: users, teams (settings, cycle settings, states) and the
	// high-volume collections a long-lived tenant accumulates.
	var rootRaw []byte
	if err := db.QueryRow(`SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&rootRaw); err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(rootRaw, &root); err != nil {
		t.Fatal(err)
	}
	shapes := map[string]string{}
	_ = json.Unmarshal(root["_flowCollections"], &shapes)
	orders := map[string]int64{}
	for field := range map[string]bool{"users": true, "teams": true, "states": true, "teamSettings": true, "cycleSettings": true} {
		var highest sql.NullInt64
		if err := db.QueryRow(`SELECT MAX(collection_order) FROM workspace_metadata_records WHERE workspace_key=? AND field=?`, workspace, field).Scan(&highest); err != nil {
			t.Fatal(err)
		}
		orders[field] = highest.Int64 + 1
	}
	metadataSQL := &scaleInserter{t: t, db: db, prefix: `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data)`, columns: 5}
	total := 0
	put := func(field, shape, key string, value any) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if shapes[field] == "" {
			shapes[field] = shape
		}
		metadataSQL.add(workspace, field, key, orders[field], raw)
		orders[field]++
		total++
	}
	for i := 0; i < userCount; i++ {
		userID := fmt.Sprintf("usr_scale_%d", i)
		put("users", "array", userID, domain.User{ID: userID, Name: fmt.Sprintf("Scale user %d", i), DisplayName: fmt.Sprintf("scale%d", i), Email: fmt.Sprintf("scale%d@example.test", i), Active: true, EmailVerified: true})
	}
	teamMembersSQL := &scaleInserter{t: t, db: db, prefix: `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at)`, columns: 5}
	teamIDs := []string{"team_test"}
	for i := 0; i < teamCount; i++ {
		teamID := fmt.Sprintf("team_scale_%d", i)
		teamIDs = append(teamIDs, teamID)
		created := now.Add(-time.Duration(teamCount-i) * time.Hour)
		put("teams", "array", teamID, domain.Team{ID: teamID, Name: fmt.Sprintf("Scale team %d", i), Key: fmt.Sprintf("S%03d", i), Color: "#5E6AD2", CreatedAt: &created, UpdatedAt: &created})
		put("teamSettings", "map", teamID, domain.TeamSettings{TeamID: teamID, Timezone: "Etc/UTC", EstimateType: "notUsed", DefaultStateID: teamID + "_state_backlog", Access: "public", MembershipRestriction: "open", SettingsPermission: "allMembers", LabelPermission: "allMembers", TemplatePermission: "allMembers", AgentSkillPermission: "allMembers", LoopPermission: "allMembers", MemberPermission: "allMembers", SlackNotifications: map[string]bool{}, PRAutomations: map[string]string{}, StaleMonths: 6, AutoArchiveMonths: 6, ProgressOrder: "first", TriageAction: "none", ResolvedSummaries: true, ShowInitiatives: true})
		put("cycleSettings", "map", teamID, domain.CycleSettings{DurationWeeks: 2, StartsOn: 1, UpcomingCount: 2, Capacity: 4, AutoCreate: true, AutoMigrate: true})
		for position, name := range []string{"backlog", "todo", "progress", "done", "canceled", "duplicate"} {
			kind := map[string]string{"backlog": "backlog", "todo": "unstarted", "progress": "started", "done": "completed", "canceled": "canceled", "duplicate": "canceled"}[name]
			put("states", "array", teamID+"_state_"+name, domain.WorkflowState{ID: teamID + "_state_" + name, Name: strings.ToUpper(name[:1]) + name[1:], Color: "#6B6F76", Type: kind, Position: float64(position), TeamID: teamID, Default: name == "backlog"})
		}
		teamMembersSQL.add(workspaceID, teamID, "usr_admin", "owner", stamp)
		for j := 0; j < 10; j++ {
			teamMembersSQL.add(workspaceID, teamID, fmt.Sprintf("usr_scale_%d", (i*7+j)%200), "member", stamp)
		}
	}
	teamMembersSQL.flush()
	admin := domain.User{ID: "usr_admin", Name: "Test admin", Email: "admin@example.test", Active: true}
	remaining := metadataCount - total
	bulk := []struct {
		field  string
		weight int
	}{{"issueSlas", 34}, {"slaEvents", 24}, {"subscriptions", 24}, {"auditLog", 0}, {"favorites", 7}, {"integrationDeliveries", 7}, {"trash", 4}}
	for _, item := range bulk {
		count := remaining * item.weight / 100
		if item.field == "auditLog" {
			// appendAudit keeps the newest 1,000 entries.
			count = 1000
		}
		for i := 0; i < count; i++ {
			key := fmt.Sprintf("%s_scale_%d", item.field, i)
			issueID := fmt.Sprintf("scale_%d", i%max(issueCount, 1)+1)
			userID := fmt.Sprintf("usr_scale_%d", i%userCount)
			at := now.Add(-time.Duration(i) * time.Minute)
			var value any
			switch item.field {
			case "issueSlas":
				value = domain.IssueSLA{ID: key, IssueID: issueID, RuleID: "sla_rule_scale", StartedAt: at, DueAt: at.Add(48 * time.Hour), CompletedAt: &at, Status: "completed"}
			case "slaEvents":
				value = domain.SLAEvent{ID: key, IssueID: issueID, SLAID: "issueSlas_scale_" + strconv.Itoa(i), Type: "completed", CreatedAt: at}
			case "subscriptions":
				value = domain.Subscription{ID: key, UserID: userID, ResourceType: "issue", ResourceID: issueID, CreatedAt: at}
			case "auditLog":
				value = domain.AuditLogEntry{ID: key, Actor: admin, Action: "updated", ResourceType: "issue", ResourceID: issueID, Metadata: map[string]any{"field": "title", "from": "Old title " + strconv.Itoa(i), "to": "New title " + strconv.Itoa(i)}, CreatedAt: at}
			case "favorites":
				value = domain.Favorite{ID: key, UserID: userID, ResourceType: "issue", ResourceID: issueID, Position: float64(i), CreatedAt: at}
			case "integrationDeliveries":
				value = domain.IntegrationDelivery{ID: key, ConnectionID: "connection_scale", EventType: "issue.updated", ResourceID: issueID, Payload: json.RawMessage(`{"title":"Scale issue"}`), Status: "delivered", Attempts: 1, CreatedAt: at, UpdatedAt: at}
			case "trash":
				value = domain.TrashEntry{ID: key, ResourceType: "document", ResourceID: "document_" + key, Title: "Deleted document", Payload: json.RawMessage(`{}`), DeletedBy: admin, DeletedAt: at, ExpiresAt: at.Add(30 * 24 * time.Hour)}
			}
			put(item.field, "array", key, value)
		}
	}
	metadataSQL.flush()
	root["_flowCollections"], _ = json.Marshal(shapes)
	encoded, _ := json.Marshal(root)
	if _, err := db.Exec(`UPDATE workspace_states SET data=? WHERE workspace_key=?`, encoded, workspace); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d metadata records, %d users, %d teams", total, userCount, teamCount)

	// Issue records cloned from the fixture's first issue, plus one activity
	// content record each. team_scale_1 owns 2,000 issues for the
	// delete-with-issues case; most issues belong to team_test.
	rows, err := db.Query(`SELECT * FROM issue_records WHERE workspace_key=? ORDER BY collection_order LIMIT 1`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	columns, _ := rows.Columns()
	template := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range template {
		pointers[i] = &template[i]
	}
	if !rows.Next() {
		t.Fatal("fixture has no issue records")
	}
	if err := rows.Scan(pointers...); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	index := map[string]int{}
	for i, column := range columns {
		index[strings.ToLower(column)] = i
	}
	var issueTemplate map[string]any
	if err := json.Unmarshal(scaleBytes(template[index["data"]]), &issueTemplate); err != nil {
		t.Fatal(err)
	}
	quoted := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = column
	}
	issueSQL := &scaleInserter{t: t, db: db, prefix: `INSERT INTO issue_records(` + strings.Join(quoted, ",") + `)`, columns: len(columns)}
	contentSQL := &scaleInserter{t: t, db: db, prefix: `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data)`, columns: 6}
	teamTemplate, _ := issueTemplate["team"].(map[string]any)
	subscriberSQL := &scaleInserter{t: t, db: db, prefix: `INSERT INTO issue_subscriber_records(workspace_key,issue_id,user_id)`, columns: 3}
	// usr_scale_250 is a member of team_scale_1 with 20 assigned and 20
	// subscribed issues there, for the member-removal cleanup case.
	// usr_scale_251 has 300 assigned and 300 subscribed issues there, for the
	// batched member-removal case.
	for _, user := range []string{"usr_scale_250", "usr_scale_251"} {
		if _, err := db.Exec(`INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,?,?)`, workspaceID, "team_scale_1", user, "member", stamp); err != nil {
			t.Fatal(err)
		}
	}
	for n := 1; n <= issueCount; n++ {
		issueID := fmt.Sprintf("scale_%d", n)
		teamID, teamKey, teamName := "team_test", "TST", "Test team"
		if n > issueCount-2000 {
			teamID, teamKey, teamName = "team_scale_1", "S001", "Scale team 1"
		}
		issue := make(map[string]any, len(issueTemplate))
		for key, value := range issueTemplate {
			issue[key] = value
		}
		team := map[string]any{}
		for key, value := range teamTemplate {
			team[key] = value
		}
		team["id"], team["key"], team["name"] = teamID, teamKey, teamName
		issue["id"], issue["identifier"], issue["number"], issue["title"], issue["team"] = issueID, fmt.Sprintf("SCL-%d", n), n, fmt.Sprintf("Scale issue %d", n), team
		delete(issue, "parentId")
		delete(issue, "cycleId")
		delete(issue, "projectId")
		delete(issue, "project")
		assignee := scaleBytes(template[index["assignee_id"]])
		if offset := n - (issueCount - 2000); offset >= 0 && offset < 40 {
			if offset < 20 {
				assignee = []byte("usr_scale_250")
				issue["assignee"] = map[string]any{"id": "usr_scale_250", "name": "Scale user 250", "displayName": "scale250", "email": "scale250@example.test", "active": true}
			} else {
				issue["subscriberIds"] = []string{"usr_admin", "usr_scale_250"}
				subscriberSQL.add(workspace, issueID, "usr_scale_250")
			}
		} else if offset >= 100 && offset < 700 {
			if offset < 400 {
				assignee = []byte("usr_scale_251")
				issue["assignee"] = map[string]any{"id": "usr_scale_251", "name": "Scale user 251", "displayName": "scale251", "email": "scale251@example.test", "active": true}
			} else {
				issue["subscriberIds"] = []string{"usr_admin", "usr_scale_251"}
				subscriberSQL.add(workspace, issueID, "usr_scale_251")
			}
		}
		raw, _ := json.Marshal(issue)
		values := slices.Clone(template)
		set := func(column string, value any) {
			if i, ok := index[column]; ok {
				values[i] = value
			}
		}
		set("id", issueID)
		set("identifier", fmt.Sprintf("SCL-%d", n))
		set("team_id", teamID)
		set("project_id", "")
		set("cycle_id", "")
		set("parent_id", "")
		set("title", fmt.Sprintf("Scale issue %d", n))
		set("assignee_id", string(assignee))
		set("sort_order", n)
		set("collection_order", 100000+n)
		set("data", raw)
		set("list_data", nil)
		issueSQL.add(values...)
		activity, _ := json.Marshal(domain.ActivityEvent{ID: "activity_" + issueID, Type: "issue.created", CreatedAt: now, Actor: admin, Metadata: map[string]string{}})
		contentSQL.add(workspace, "activity", issueID, "activity_"+issueID, stamp, activity)
	}
	issueSQL.flush()
	contentSQL.flush()
	subscriberSQL.flush()
	if _, err := db.Exec(`UPDATE issue_collection_counts SET total=(SELECT COUNT(*) FROM issue_records WHERE workspace_key=?) WHERE workspace_key=?`, workspace, workspace); err != nil {
		t.Fatal(err)
	}
	// Let the store rebuild derived issue statistics and list projections on open.
	for _, table := range []string{"issue_stats_migrations", "issue_list_migrations"} {
		if _, err := db.Exec(`DELETE FROM `+table+` WHERE workspace_key=?`, workspace); err != nil {
			t.Fatal(err)
		}
	}
}

func scaleBytes(value any) []byte {
	switch typed := value.(type) {
	case []byte:
		return typed
	case string:
		return []byte(typed)
	}
	return nil
}
