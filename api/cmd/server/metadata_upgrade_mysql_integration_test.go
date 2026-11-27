//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/store"

	"github.com/go-sql-driver/mysql"
)

func TestDefaultMetadataUpgradeWithRestrictedMySQL(t *testing.T) {
	raw := os.Getenv("FLOW_TEST_MYSQL_ADMIN_URL")
	if raw == "" {
		t.Skip("set FLOW_TEST_MYSQL_ADMIN_URL for a disposable MySQL 8.4 service")
	}
	adminURL, err := url.Parse(raw)
	if err != nil || adminURL.Scheme != "mysql" || adminURL.Host == "" || adminURL.User == nil {
		t.Fatal("invalid MySQL integration admin URL")
	}
	adminConfig := mysql.NewConfig()
	adminConfig.User = adminURL.User.Username()
	adminConfig.Passwd, _ = adminURL.User.Password()
	adminConfig.Net, adminConfig.Addr = "tcp", adminURL.Host
	admin, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatal("open MySQL integration admin connection")
	}
	defer admin.Close()
	var version string
	var binlog, trust int
	err = admin.QueryRowContext(t.Context(), "SELECT VERSION(), @@GLOBAL.log_bin, @@GLOBAL.log_bin_trust_function_creators").Scan(&version, &binlog, &trust)
	if err != nil || !strings.HasPrefix(version, "8.4.") || binlog != 1 || trust != 0 {
		t.Fatalf("fixture requires MySQL 8.4 with binary logging on and trigger creator trust off (database code %q)", store.DatabaseErrorCode(err))
	}
	for _, test := range []struct {
		name           string
		trigger, fresh bool
	}{
		{"fresh-install", false, true},
		{"without-trigger-grant", false, false},
		{"binlog-requires-super", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Use no wildcard characters in the schema name used in GRANT.
			name := fmt.Sprintf("flowupgrade%x", time.Now().UnixNano())
			password := rand.Text()
			exec := func(label, statement string) {
				t.Helper()
				if _, err := admin.ExecContext(t.Context(), statement); err != nil {
					t.Fatalf("%s failed (database code %q)", label, store.DatabaseErrorCode(err))
				}
			}
			cleanup := func(label, statement string) {
				t.Helper()
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					if _, err := admin.ExecContext(ctx, statement); err != nil {
						t.Errorf("%s failed (database code %q)", label, store.DatabaseErrorCode(err))
					}
				})
			}
			exec("create scratch schema", "CREATE DATABASE "+name)
			cleanup("drop scratch schema", "DROP DATABASE "+name)
			account := "'" + name + "'@'%'"
			exec("create restricted account", "CREATE USER "+account+" IDENTIFIED BY '"+password+"'")
			cleanup("drop restricted account", "DROP USER "+account)
			grants := "SELECT, INSERT, UPDATE, DELETE, CREATE, DROP, ALTER, INDEX, REFERENCES, CREATE TEMPORARY TABLES"
			if test.trigger {
				grants += ", TRIGGER"
			}
			exec("grant schema access", "GRANT "+grants+" ON "+name+".* TO "+account)

			privilegedURL := *adminURL
			privilegedURL.Path = "/" + name
			privileged := store.DatabaseConfig{Driver: "mysql", URL: privilegedURL.String(), MaxOpenConns: 4}
			restrictedURL := privilegedURL
			restrictedURL.User = url.UserPassword(name, password)
			restricted := privileged
			restricted.URL = restrictedURL.String()
			restricted.FixtureProfile, restricted.FixturePassword = "test", "test-password"
			probeConfig := *adminConfig
			probeConfig.User, probeConfig.Passwd, probeConfig.DBName = name, password, name
			probe, err := sql.Open("mysql", probeConfig.FormatDSN())
			if err != nil {
				t.Fatal("open restricted MySQL fixture connection")
			}
			defer probe.Close()
			exerciseRestrictedMySQLMetadataUpgrade(t, restricted, privileged, probe, admin, name, test.trigger, test.fresh)
		})
	}
}

func exerciseRestrictedMySQLMetadataUpgrade(t *testing.T, restricted, privileged store.DatabaseConfig, probe, admin *sql.DB, schema string, triggerGrant, fresh bool) {
	t.Helper()
	const fallbackWarning = "MySQL record journal trigger permission denied. Using legacy metadata and sync"
	var repo *store.SQLiteStore
	var handler http.Handler
	defer func() {
		if repo != nil {
			_ = repo.Close()
		}
	}()
	closeStore := func() {
		t.Helper()
		if err := repo.Close(); err != nil {
			t.Fatal("close MySQL upgrade store")
		}
		repo = nil
	}
	openStore := func(config store.DatabaseConfig, wantReady, wantFallback bool) {
		t.Helper()
		var logs bytes.Buffer
		previous := slog.Default()
		slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
		var err error
		repo, err = store.OpenDatabase(config)
		slog.SetDefault(previous)
		if err != nil {
			t.Fatalf("MySQL startup failed (ready=%t, fallback=%t, database code %q)", wantReady, wantFallback, store.DatabaseErrorCode(err))
		}
		warnings := 0
		for decoder := json.NewDecoder(&logs); ; {
			var entry map[string]any
			if err := decoder.Decode(&entry); err == io.EOF {
				break
			} else if err != nil {
				t.Fatal("could not read startup warning")
			}
			message, _ := entry["msg"].(string)
			if strings.Contains(message, "record journal") || strings.Contains(message, "startup metadata") {
				warnings++
				if message != fallbackWarning || len(entry) != 3 || entry["level"] != "WARN" {
					t.Fatal("unexpected or unsanitized metadata startup warning")
				}
			}
		}
		if warnings > 1 || (warnings == 1) != wantFallback {
			t.Fatalf("got %d fallback warnings (want fallback=%t)", warnings, wantFallback)
		}
		s := metadataUpgradeServer(t, repo)
		_, syncReady := s.syncV2Journal()
		if repo.RecordJournalReady() != wantReady || repo.BoundedMetadataEnabled() != wantReady ||
			syncReady != wantReady || s.syncV2RealtimeEnabled() != wantReady {
			t.Fatalf("journal, bounded metadata and Sync V2 must all have readiness %t", wantReady)
		}
		handler = newHandler(s)
	}
	for _, flag := range metadataUpgradeFlags {
		t.Setenv(flag, "off")
	}
	defaults := func() {
		t.Helper()
		for _, flag := range metadataUpgradeFlags {
			if err := os.Unsetenv(flag); err != nil {
				t.Fatal("clear metadata upgrade flag")
			}
		}
	}
	if fresh {
		// A fresh install starts in real bounded mode on its first start.
		defaults()
		openStore(restricted, true, false)
	} else {
		openStore(restricted, false, false)
	}
	_, session, err := repo.Login(t.Context(), "admin@example.test", restricted.FixturePassword)
	if err != nil {
		t.Fatal("log in to previous-default workspace")
	}
	fixture := repo.Bootstrap()
	workspace, team := fixture.Workspace.URLKey, fixture.Teams[0].ID

	type route struct{ path, sync string }
	request := func(method string, target route, body, bearer string, status int) *httptest.ResponseRecorder {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		r := httptest.NewRequestWithContext(ctx, method, target.path, strings.NewReader(body))
		r.Header.Set("X-Workspace-Key", workspace)
		if target.sync != "" {
			r.Header.Set("X-Flow-Sync", target.sync)
		}
		if bearer == "" {
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		} else {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s request returned %d, want %d", method, w.Code, status)
		}
		return w
	}
	idFrom := func(w *httptest.ResponseRecorder) string {
		t.Helper()
		var item struct{ ID string }
		if json.Unmarshal(w.Body.Bytes(), &item) != nil || item.ID == "" {
			t.Fatal("mutation response lacks an ID")
		}
		return item.ID
	}
	create := func(path, body string) string {
		t.Helper()
		return idFrom(request(http.MethodPost, route{path: path}, body, "", http.StatusCreated))
	}
	project := create("/api/projects", fmt.Sprintf(`{"name":"Previous project","teamIds":[%q]}`, team))
	existingIssue := create("/api/issue-records", fmt.Sprintf(`{"title":"Previous issue","teamId":%q,"projectId":%q}`, team, project))
	existingDocument := create("/api/documents", fmt.Sprintf(`{"title":"Previous document","content":"Saved body","projectId":%q}`, project))
	if fresh {
		for start := range 2 {
			if repo.RecordJournalCapture() != store.RecordJournalCaptureApplication {
				t.Fatalf("fresh start %d captures with %q", start, repo.RecordJournalCapture())
			}
			catalogs, err := repo.MetadataCatalogCompleteness(t.Context())
			if err != nil || len(catalogs) == 0 {
				t.Fatal("fresh install has no catalog")
			}
			for _, catalog := range catalogs {
				if catalog.Incomplete != 0 {
					t.Fatal("fresh install left incomplete catalog collections")
				}
			}
			var page syncV2ChangesResponse
			w := request(http.MethodGet, route{path: "/api/sync/changes?after=0"}, "", "", http.StatusOK)
			if json.Unmarshal(w.Body.Bytes(), &page) != nil || page.ResyncRequired || len(page.Transactions) == 0 {
				t.Fatal("fresh install Sync V2 did not return its writes")
			}
			found := false
			for _, transaction := range page.Transactions {
				for _, upsert := range transaction.Upserts {
					found = found || upsert.Key.Kind == "documents" && upsert.Key.ID == existingDocument
				}
			}
			if !found {
				t.Fatal("fresh install Sync V2 lacks the document")
			}
			request(http.MethodGet, route{path: "/api/issue-records/" + existingIssue}, "", "", http.StatusOK)
			closeStore()
			openStore(restricted, true, false)
		}
		var journalDDL int
		if err := admin.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM performance_schema.events_statements_summary_by_digest WHERE SCHEMA_NAME=? AND (DIGEST_TEXT LIKE 'CREATE TRIGGER%' OR DIGEST_TEXT LIKE 'DROP TRIGGER%')`, schema).Scan(&journalDDL); err != nil || journalDDL != 0 {
			t.Fatalf("fresh install issued %d trigger statements", journalDDL)
		}
		return
	}
	if status, err := repo.MetadataCatalogStatus(t.Context(), workspace, "projects"); err != nil || status.Complete {
		t.Fatal("previous-default fixture already has catalog coverage")
	}
	closeStore()

	// Prove the intended denial separately from the API's startup behavior.
	_, err = probe.ExecContext(t.Context(), `CREATE TRIGGER flow_upgrade_probe AFTER INSERT ON auth_users FOR EACH ROW SET @flow_upgrade_probe=1`)
	code := store.DatabaseErrorCode(err)
	if code != "mysql_1419" && (triggerGrant || code != "mysql_1142") {
		t.Fatalf("fixture trigger was not denied as expected (database code %q)", code)
	}

	reads := []route{
		{path: "/api/bootstrap"},
		{path: "/api/issue-records/bootstrap"},
		{path: "/api/issue-records?limit=10&includeTotal=true"},
		{path: "/api/issue-records/" + existingIssue},
		{path: "/api/projects/" + project},
		{path: "/api/documents/" + existingDocument},
		{path: "/api/api-keys"},
	}
	snapshot := func(paths []route) []routeSweepOutcome {
		t.Helper()
		outcomes := make([]routeSweepOutcome, 0, len(paths))
		started := time.Now()
		for _, target := range paths {
			w := request(http.MethodGet, target, "", "", http.StatusOK)
			if !json.Valid(w.Body.Bytes()) {
				t.Fatal("comparison read did not return JSON")
			}
			outcomes = append(outcomes, routeSweepOutcome{
				Status: w.Code,
				Body:   routeSweepNormalize(canonicalJSON(w.Body.Bytes()), started),
				Header: map[string]string{
					"Content-Type":     w.Header().Get("Content-Type"),
					changeCursorHeader: w.Header().Get(changeCursorHeader),
				},
			})
		}
		return outcomes
	}
	compare := func(want, got []routeSweepOutcome) {
		t.Helper()
		for i := range want {
			if kind, _, _ := routeSweepDifferences(want[i], got[i]); kind != "" {
				t.Errorf("read %d changed %s across startup modes", i, kind)
			}
		}
	}
	assertLegacySync := func() {
		t.Helper()
		request(http.MethodGet, route{path: "/api/sync/changes?after=0"}, "", "", http.StatusNotFound)
	}
	openStore(restricted, false, false)
	before := snapshot(reads)
	closeStore()

	// The previous-default database upgrades to real bounded mode on the
	// restricted account: application capture, a complete catalog and
	// Sync V2, with no trigger DDL.
	defaults()
	openStore(restricted, true, false)
	assertApplicationCapture := func() {
		t.Helper()
		if repo.RecordJournalCapture() != store.RecordJournalCaptureApplication {
			t.Fatalf("restricted start captures with %q", repo.RecordJournalCapture())
		}
		catalogs, err := repo.MetadataCatalogCompleteness(t.Context())
		if err != nil || len(catalogs) == 0 {
			t.Fatal("bounded start did not build the catalog")
		}
		for _, catalog := range catalogs {
			if catalog.Incomplete != 0 {
				t.Fatal("bounded start left incomplete catalog collections")
			}
		}
	}
	assertApplicationCapture()
	compare(before, snapshot(reads))
	compare(before[1:2], snapshot([]route{{path: "/api/issue-records/bootstrap", sync: "summary"}}))

	// syncFrom drains /api/sync/changes from a cursor and checks the
	// transactions arrive in strictly increasing cursor order.
	syncFrom := func(cursor string) (syncV2ChangesResponse, []syncV2Transaction) {
		t.Helper()
		var page syncV2ChangesResponse
		var all []syncV2Transaction
		last := int64(-1)
		if value, err := strconv.ParseInt(cursor, 10, 64); err == nil {
			last = value
		}
		for range 1000 {
			w := request(http.MethodGet, route{path: "/api/sync/changes?after=" + url.QueryEscape(cursor)}, "", "", http.StatusOK)
			page = syncV2ChangesResponse{}
			if json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Head == "" {
				t.Fatal("Sync V2 did not return a page")
			}
			if page.ResyncRequired {
				return page, all
			}
			for _, transaction := range page.Transactions {
				seq, err := strconv.ParseInt(transaction.Seq, 10, 64)
				if err != nil || seq <= last {
					t.Fatalf("Sync V2 transaction cursor %q after %d", transaction.Seq, last)
				}
				last = seq
			}
			all = append(all, page.Transactions...)
			cursor = page.Next
			if !page.HasMore {
				return page, all
			}
		}
		t.Fatal("Sync V2 did not reach its head")
		return page, all
	}
	start, _ := syncFrom("0")
	if start.ResyncRequired {
		t.Fatal("Sync V2 requires a resync after the upgrade")
	}

	issue := create("/api/issue-records", fmt.Sprintf(`{"title":"Restricted create","teamId":%q,"projectId":%q}`, team, project))
	request(http.MethodPatch, route{path: "/api/issue-records/" + issue}, `{"title":"Restricted edit"}`, "", http.StatusOK)
	create("/api/issue-records/"+issue+"/comments", `{"body":"Restricted comment"}`)
	request(http.MethodPatch, route{path: "/api/projects/" + project}, `{"name":"Restricted project edit"}`, "", http.StatusOK)
	doc := create("/api/documents", fmt.Sprintf(`{"title":"Restricted document","content":"Restricted body","projectId":%q}`, project))
	label := create("/api/labels", `{"name":"Restricted label","color":"#4cb782"}`)
	request(http.MethodPatch, route{path: "/api/labels/" + label}, `{"name":"Restricted label renamed"}`, "", http.StatusOK)
	request(http.MethodDelete, route{path: "/api/labels/" + label}, "", "", http.StatusNoContent)
	gone := create("/api/issue-records", fmt.Sprintf(`{"title":"Restricted delete","teamId":%q}`, team))
	request(http.MethodDelete, route{path: "/api/issue-records/" + gone}, "", "", http.StatusNoContent)
	key := request(http.MethodPost, route{path: "/api/api-keys"}, `{"name":"Restricted key","scopes":["read"]}`, "", http.StatusCreated)
	var secrets struct{ Secret string }
	if json.Unmarshal(key.Body.Bytes(), &secrets) != nil || secrets.Secret == "" {
		t.Fatal("API key creation did not return a credential")
	}
	assertKey := func() {
		t.Helper()
		request(http.MethodGet, route{path: "/api/issue-records/" + issue}, "", secrets.Secret, http.StatusOK)
	}
	assertKey()

	// Creations, updates, the deletion's tombstone and the permission change
	// (a new API key advances the authorization epoch) arrive in order.
	page, changes := syncFrom(start.Next)
	if page.ResyncRequired || len(changes) == 0 {
		t.Fatal("Sync V2 did not return the restricted writes")
	}
	if page.AuthzEpoch == start.AuthzEpoch {
		t.Fatal("the API key did not advance the authorization epoch")
	}
	position := func(kind, id string, removal bool) int {
		for i, transaction := range changes {
			if removal {
				for _, item := range transaction.Removals {
					if item.Key.Kind == kind && item.Key.ID == id {
						return i
					}
				}
				continue
			}
			for _, item := range transaction.Upserts {
				if item.Key.Kind == kind && item.Key.ID == id {
					return i
				}
			}
		}
		return -1
	}
	invalidatedAt := func(kind string, after int) int {
		for i := after + 1; i < len(changes); i++ {
			if slices.Contains(changes[i].Invalidations, kind) {
				return i
			}
		}
		return -1
	}
	// Projects and documents are summarized upserts; Sync V2 serves the
	// other collections as invalidations, an issue deletion as a removal
	// when it can prove the reader saw the issue.
	edited, documented := position("projects", project, false), position("documents", doc, false)
	labelCreated := invalidatedAt("labels", documented)
	labelRemoved := invalidatedAt("labels", invalidatedAt("labels", labelCreated))
	removedAt := position("issues", gone, true)
	if removedAt < 0 {
		removedAt = invalidatedAt("issues", labelRemoved)
	}
	if edited < 0 || documented <= edited || labelCreated <= documented || labelRemoved <= labelCreated || removedAt <= labelRemoved {
		t.Fatalf("Sync V2 order: project %d, document %d, label %d..%d, issue removal %d", edited, documented, labelCreated, labelRemoved, removedAt)
	}
	// The trusted journal behind it holds the versions and the tombstones.
	journal, err := repo.ReadCommittedChanges(t.Context(), fixture.Workspace.ID, start.Next, 500, store.MetadataRecordPageMaxBytes)
	if err != nil || journal.ResyncRequired || journal.HasMore {
		t.Fatal("the journal page after the upgrade is incomplete")
	}
	versions := map[store.RecordKey][]store.RecordDelta{}
	epochs := map[string]bool{}
	for _, transaction := range journal.Transactions {
		epochs[transaction.AuthzEpoch] = true
		for _, record := range transaction.Records {
			versions[record.Key] = append(versions[record.Key], record)
		}
	}
	history := func(key store.RecordKey) []store.RecordDelta { return versions[key] }
	labelHistory := history(store.RecordKey{Kind: "labels", ID: label})
	issueHistory := history(store.RecordKey{Kind: "issues", ID: gone})
	if len(labelHistory) < 3 || labelHistory[0].Deleted || !labelHistory[len(labelHistory)-1].Deleted ||
		len(issueHistory) < 2 || !issueHistory[len(issueHistory)-1].Deleted || len(epochs) < 2 {
		t.Fatalf("journal lacks the creation, update or tombstone (label %+v, issue %+v, epochs %d)", labelHistory, issueHistory, len(epochs))
	}
	for _, records := range versions {
		for i := 1; i < len(records); i++ {
			previous, _ := strconv.ParseInt(records[i-1].Version, 10, 64)
			current, _ := strconv.ParseInt(records[i].Version, 10, 64)
			if current <= previous {
				t.Fatalf("record versions went back: %+v", records)
			}
		}
	}

	// The key's lastUsedAt is written by the store's asynchronous use worker,
	// which can land after the next read. Every later startup loads it from
	// the database, so wait until the bootstrap shows it before taking the
	// snapshot those startups are compared against.
	type keyUse struct {
		Name       string
		LastUsedAt *time.Time
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var data struct{ APIKeys []keyUse }
		w := request(http.MethodGet, reads[0], "", "", http.StatusOK)
		if json.Unmarshal(w.Body.Bytes(), &data) != nil {
			t.Fatal("bootstrap did not return JSON")
		}
		if slices.ContainsFunc(data.APIKeys, func(key keyUse) bool {
			return key.Name == "Restricted key" && key.LastUsedAt != nil
		}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("API key use was not recorded before the comparison snapshot")
		}
	}
	reads = append(reads,
		route{path: "/api/issue-records/" + issue},
		route{path: "/api/comments?issueId=" + issue},
		route{path: "/api/documents/" + doc},
	)
	after := snapshot(reads)
	issueTitle := "Restricted edit"
	assertWrites := func(outcomes []routeSweepOutcome) {
		t.Helper()
		expected := map[string]string{
			"/api/issue-records?limit=10&includeTotal=true": `"title":"` + issueTitle + `"`,
			"/api/issue-records/" + issue:                   `"title":"` + issueTitle + `"`,
			"/api/projects/" + project:                      `"name":"Restricted project edit"`,
			"/api/api-keys":                                 `"name":"Restricted key"`,
			"/api/comments?issueId=" + issue:                `"body":"Restricted comment"`,
			"/api/documents/" + doc:                         `"content":"Restricted body"`,
		}
		for i, target := range reads {
			if value, checked := expected[target.path]; checked && !strings.Contains(outcomes[i].Body, value) {
				t.Errorf("read %d did not return the committed update", i)
			}
		}
	}
	assertWrites(after)
	_, settled := syncFrom(page.Next)
	cursor := page.Next
	for _, transaction := range settled {
		cursor = transaction.Seq
	}
	closeStore()

	// A restart keeps bounded mode and continues the same cursor.
	openStore(restricted, true, false)
	assertApplicationCapture()
	compare(after, snapshot(reads))
	assertKey()
	request(http.MethodPatch, route{path: "/api/issue-records/" + issue}, `{"title":"Restart edit"}`, "", http.StatusOK)
	page, changes = syncFrom(cursor)
	if page.ResyncRequired || position("issues", issue, false) < 0 && invalidatedAt("issues", -1) < 0 {
		t.Fatal("Sync V2 lost its cursor or the write after a restart")
	}
	cursor = page.Next
	closeStore()

	// Back to the previous default (legacy reads) and forward again: the
	// journal walks every workspace again, so the legacy write is not lost.
	for _, flag := range metadataUpgradeFlags {
		t.Setenv(flag, "off")
	}
	openStore(restricted, false, false)
	assertLegacySync()
	request(http.MethodPatch, route{path: "/api/issue-records/" + issue}, `{"title":"Legacy edit"}`, "", http.StatusOK)
	issueTitle = "Legacy edit"
	closeStore()
	defaults()
	openStore(restricted, true, false)
	assertApplicationCapture()
	if !strings.Contains(request(http.MethodGet, route{path: "/api/issue-records/" + issue}, "", "", http.StatusOK).Body.String(), `"title":"Legacy edit"`) {
		t.Fatal("the legacy write is missing after the journal returned")
	}
	page, changes = syncFrom(cursor)
	invalidated := page.ResyncRequired
	for _, transaction := range changes {
		invalidated = invalidated || slices.Contains(transaction.Invalidations, "workspace")
	}
	if !invalidated && position("issues", issue, false) < 0 && invalidatedAt("issues", -1) < 0 {
		t.Fatal("Sync V2 neither resynced nor returned the legacy write")
	}
	closeStore()

	// The privileged account keeps the database's application capture.
	openStore(privileged, true, false)
	assertApplicationCapture()
	assertWrites(snapshot(reads))
	assertKey()
	request(http.MethodPatch, route{path: "/api/issue-records/" + issue}, `{"title":"Journal recovered"}`, "", http.StatusOK)
	start, _ = syncFrom("0")
	if start.ResyncRequired || start.Head == "" {
		t.Fatal("privileged restart did not expose Sync V2")
	}
	var journalTriggers, journalDDL int
	if err := admin.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=? AND TRIGGER_NAME LIKE 'flow\_rj\_%'`, schema).Scan(&journalTriggers); err != nil {
		t.Fatal("count journal triggers")
	}
	if err := admin.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM performance_schema.events_statements_summary_by_digest WHERE SCHEMA_NAME=? AND DIGEST_TEXT LIKE '%TRIGGER `+"`"+`flow\_rj\_%'`, schema).Scan(&journalDDL); err != nil {
		t.Fatal("count journal trigger statements")
	}
	if journalTriggers != 0 || journalDDL != 0 {
		t.Fatalf("journal trigger DDL ran: %d triggers, %d statements", journalTriggers, journalDDL)
	}
}
