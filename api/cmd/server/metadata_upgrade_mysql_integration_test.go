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
		name    string
		trigger bool
	}{
		{"without-trigger-grant", false},
		{"binlog-requires-super", true},
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
			exerciseRestrictedMySQLMetadataUpgrade(t, restricted, privileged, probe, test.trigger)
		})
	}
}

func exerciseRestrictedMySQLMetadataUpgrade(t *testing.T, restricted, privileged store.DatabaseConfig, probe *sql.DB, triggerGrant bool) {
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
		if repo.RecordJournalReady() != wantReady || repo.BoundedMetadataEnabled() != wantReady || syncReady != wantReady {
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
	openStore(restricted, false, false)
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
	snapshot := func(paths []route) []sw530Outcome {
		t.Helper()
		outcomes := make([]sw530Outcome, 0, len(paths))
		started := time.Now()
		for _, target := range paths {
			w := request(http.MethodGet, target, "", "", http.StatusOK)
			if !json.Valid(w.Body.Bytes()) {
				t.Fatal("comparison read did not return JSON")
			}
			outcomes = append(outcomes, sw530Outcome{
				Status: w.Code,
				Body:   sw530Normalize(sw530CanonicalJSON(w.Body.Bytes()), started),
				Header: map[string]string{
					"Content-Type":     w.Header().Get("Content-Type"),
					changeCursorHeader: w.Header().Get(changeCursorHeader),
				},
			})
		}
		return outcomes
	}
	compare := func(want, got []sw530Outcome) {
		t.Helper()
		for i := range want {
			if kind, _, _ := sw530Differences(want[i], got[i]); kind != "" {
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
	defaults()
	openStore(restricted, false, true)
	compare(before, snapshot(reads))
	compare(before[1:2], snapshot([]route{{path: "/api/issue-records/bootstrap", sync: "summary"}}))
	assertLegacySync()

	cursor := request(http.MethodGet, route{path: "/api/issue-records/bootstrap"}, "", "", http.StatusOK).Header().Get(changeCursorHeader)
	if _, err := strconv.ParseInt(cursor, 10, 64); err != nil {
		t.Fatal("legacy bootstrap did not expose a realtime cursor")
	}
	realtime := "/api/realtime/changes?since=" + cursor + "&limit=50"
	issue := create("/api/issue-records", fmt.Sprintf(`{"title":"Restricted create","teamId":%q,"projectId":%q}`, team, project))
	request(http.MethodPatch, route{path: "/api/issue-records/" + issue}, `{"title":"Restricted edit"}`, "", http.StatusOK)
	create("/api/issue-records/"+issue+"/comments", `{"body":"Restricted comment"}`)
	request(http.MethodPatch, route{path: "/api/projects/" + project}, `{"name":"Restricted project edit"}`, "", http.StatusOK)
	doc := create("/api/documents", fmt.Sprintf(`{"title":"Restricted document","content":"Restricted body","projectId":%q}`, project))
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
	reads = append(reads,
		route{path: "/api/issue-records/" + issue},
		route{path: "/api/comments?issueId=" + issue},
		route{path: "/api/documents/" + doc},
		route{path: realtime},
	)
	after := snapshot(reads)
	assertLegacyWrites := func(outcomes []sw530Outcome) {
		t.Helper()
		expected := map[string]string{
			"/api/issue-records?limit=10&includeTotal=true": `"title":"Restricted edit"`,
			"/api/issue-records/" + issue:                   `"title":"Restricted edit"`,
			"/api/projects/" + project:                      `"name":"Restricted project edit"`,
			"/api/api-keys":                                 `"name":"Restricted key"`,
			"/api/comments?issueId=" + issue:                `"body":"Restricted comment"`,
			"/api/documents/" + doc:                         `"content":"Restricted body"`,
			realtime:                                        `"type":"issue.updated"`,
		}
		for i, target := range reads {
			if value, checked := expected[target.path]; checked && !strings.Contains(outcomes[i].Body, value) {
				t.Errorf("read %d did not return the committed update", i)
			}
		}
	}
	assertLegacyWrites(after)
	// Negotiating delta must still return the legacy feed when capture is unavailable.
	compare(after[len(after)-1:], snapshot([]route{{path: realtime, sync: "delta"}}))
	closeStore()

	openStore(restricted, false, true)
	compare(after, snapshot(reads))
	assertKey()
	assertLegacySync()
	closeStore()

	for _, flag := range metadataUpgradeFlags {
		t.Setenv(flag, "off")
	}
	openStore(restricted, false, false)
	compare(after, snapshot(reads))
	assertLegacySync()
	closeStore()

	defaults()
	openStore(privileged, true, false)
	assertLegacyWrites(snapshot(reads))
	assertKey()
	catalogs, err := repo.MetadataCatalogCompleteness(t.Context())
	if err != nil || len(catalogs) == 0 {
		t.Fatal("privileged restart did not build the catalog")
	}
	for _, catalog := range catalogs {
		if catalog.Incomplete != 0 {
			t.Fatal("privileged restart left incomplete catalog collections")
		}
	}
	page := request(http.MethodGet, route{path: "/api/sync/changes?after=0"}, "", "", http.StatusOK)
	var syncPage syncV2ChangesResponse
	if json.Unmarshal(page.Body.Bytes(), &syncPage) != nil || syncPage.Head == "" {
		t.Fatal("privileged restart did not expose Sync V2")
	}
	request(http.MethodPatch, route{path: "/api/issue-records/" + issue}, `{"title":"Journal recovered"}`, "", http.StatusOK)
	page = request(http.MethodGet, route{path: "/api/sync/changes?after=" + url.QueryEscape(syncPage.Head)}, "", "", http.StatusOK)
	if json.Unmarshal(page.Body.Bytes(), &syncPage) != nil || len(syncPage.Transactions) == 0 || syncPage.ResyncRequired {
		t.Fatal("privileged restart did not capture subsequent writes")
	}
}
