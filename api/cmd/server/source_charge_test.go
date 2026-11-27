package main

import (
	"database/sql"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/store"
)

// SW602: a resident workspace holds one installed-source charge (128 KiB of
// FLOW_METADATA_SOURCE_BYTES), however it was installed and however often it
// is written. A flag-on e2e run (default budgets, fresh database) refused
// POST /api/workspaces and POST /api/labels with "installed workspace sources
// use all of FLOW_METADATA_SOURCE_BYTES" after about 25 workspaces created at
// runtime: every rewritten field of a publication went to a storage of its
// own, each charged 128 KiB, so a created workspace held 12 charges and about
// 20 after a few writes.
//
// Here N workspaces are created through the HTTP API and written (settings,
// labels, issues, documents, views and COLD writes: project statuses and
// issue templates), written again, reloaded, and refreshed by a peer store
// over the same database. After every phase each store's installed sources
// share exactly one spool group per workspace, the charge is exactly one
// descriptor per workspace, the in-memory spools stay within the groups'
// allowance, and the process holds no charge beyond the installed sources.
func TestScaleP21SW602CreatedWorkspacesHoldOneSourceCharge(t *testing.T) {
	const workspaces = 6
	const perWorkspace = 128 << 10
	config, seeded := sw602ChargeDatabase(t)
	baseline := (&store.SQLiteStore{}).InstalledSourceChargeForTesting().Process
	st := sw602ChargeOpen(t, config, seeded)
	defer st.close()
	t.Setenv("FLOW_BOUNDED_METADATA", "on")
	peer, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	run := &sw602Run{t: t, st: st, names: &rtRun{t: t, labels: map[string]string{}}}

	check := func(phase string, want int) {
		t.Helper()
		var main, other store.SourceChargeForTesting
		var problems []string
		// Background passes (deliveries, presence) hold transient spools;
		// the steady state is reached once they finish.
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			main, other = st.repo.InstalledSourceChargeForTesting(), peer.InstalledSourceChargeForTesting()
			problems = problems[:0]
			for name, c := range map[string]store.SourceChargeForTesting{"server": main, "peer": other} {
				if c.Sources != want || c.Groups != c.Sources || c.Charged != int64(c.Sources)*perWorkspace {
					problems = append(problems, fmt.Sprintf("%s: %d sources (want %d) in %d spool groups over %d storages charged %d KiB, want %d KiB", name, c.Sources, want, c.Groups, c.Storages, c.Charged>>10, c.Sources*perWorkspace>>10))
				}
				if c.Resident > int64(c.Groups)*32<<10 {
					problems = append(problems, fmt.Sprintf("%s: %d bytes of in-memory spools over the groups' allowance", name, c.Resident))
				}
			}
			if extra := main.Process - baseline - main.Charged - other.Charged; extra != 0 {
				problems = append(problems, fmt.Sprintf("the process holds %d KiB of source charges beyond the installed sources", extra>>10))
			}
			if len(problems) == 0 || time.Now().After(deadline) {
				break
			}
		}
		t.Logf("%-22s server: %d sources, %d storages (%d files), %d KiB charged; peer: %d sources, %d KiB charged", phase, main.Sources, main.Storages, main.Files, main.Charged>>10, other.Sources, other.Charged>>10)
		if len(problems) > 0 {
			t.Fatalf("%s:\n%s", phase, strings.Join(problems, "\n"))
		}
	}
	keys := make([]string, workspaces)
	teams := map[string]string{}
	writes := func(key string, round int) {
		suffix := fmt.Sprint(round)
		// COLD writes: a full postimage (project statuses, issue templates,
		// drafts) or an incremental publication (documents, views).
		run.write("project-status-create", key, "POST", "/api/project-statuses", map[string]any{"name": "Charge status " + suffix, "color": "#4cb782", "type": "started"}, 201)
		run.write("issue-template-create", key, "POST", "/api/issue-templates", map[string]any{"name": "Charge template " + suffix, "teamId": teams[key]}, 201)
		run.write("draft-create", key, "POST", "/api/drafts", map[string]any{"type": "issue", "title": "Charge draft " + suffix, "body": "Draft body"}, 201)
		doc := run.write("document-create", key, "POST", "/api/documents", map[string]any{"title": "Charge document " + suffix, "content": "# body", "teamIds": []string{teams[key]}}, 201)
		run.write("document-update", key, "PATCH", "/api/documents/"+rtID(doc), map[string]any{"content": "# body\nsecond " + suffix}, 200)
		run.write("view-create", key, "POST", "/api/views", map[string]any{"name": "Charge view " + suffix, "resource": "issues", "scope": "workspace"}, 201)
		// HOT writes: each forks the installed source.
		run.write("account-settings", key, "PATCH", "/api/account/settings", map[string]any{"fontSize": []string{"small", "default"}[round%2]}, 200)
		label := run.write("label-create", key, "POST", "/api/labels", map[string]any{"name": "Charge label " + suffix, "color": "#4cb782"}, 201)
		run.write("label-update", key, "PATCH", "/api/labels/"+rtID(label), map[string]any{"name": "Charge label renamed " + suffix}, 200)
		issue := run.write("issue-create", key, "POST", "/api/issues", map[string]any{"title": "Charge issue " + suffix, "teamId": teams[key], "labelIds": []string{rtID(label)}}, 201)
		run.write("issue-update", key, "PATCH", "/api/issues/"+rtID(issue), map[string]any{"priority": 2, "title": "Charge issue edited " + suffix}, 200)
	}
	check("start", seeded)
	for i := range keys {
		keys[i] = fmt.Sprintf("sw602-charge-%d", i)
		run.write("create", "", "POST", "/api/workspaces", map[string]any{"name": fmt.Sprintf("Charge %d", i), "urlKey": keys[i]}, 201)
		_, raw := run.do(keys[i], "GET", "/api/bootstrap", nil)
		if match := strings.SplitN(string(raw), `"teams":[{"id":"`, 2); len(match) == 2 {
			teams[keys[i]] = strings.SplitN(match[1], `"`, 2)[0]
		}
		if err := peer.ReloadAllWorkspaces(t.Context()); err != nil {
			t.Fatal(err)
		}
		check("created "+keys[i], seeded+i+1)
		writes(keys[i], 0)
		check("written "+keys[i], seeded+i+1)
	}
	if len(run.failed) > 0 {
		t.Fatalf("unexpected statuses:\n%s", strings.Join(run.failed, "\n"))
	}
	for round := 1; round <= 3; round++ {
		for _, key := range keys {
			writes(key, round)
		}
		check(fmt.Sprintf("rewritten %d", round), seeded+workspaces)
		for _, key := range keys {
			if err := st.repo.ReloadWorkspace(t.Context(), key); err != nil {
				t.Fatal(err)
			}
			if err := peer.ReloadWorkspace(t.Context(), key); err != nil {
				t.Fatal(err)
			}
		}
		check(fmt.Sprintf("refreshed %d", round), seeded+workspaces)
	}
	if err := st.repo.ReloadAllWorkspaces(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := peer.ReloadAllWorkspaces(t.Context()); err != nil {
		t.Fatal(err)
	}
	check("reloaded", seeded+workspaces)
	if len(run.failed) > 0 {
		t.Fatalf("unexpected statuses:\n%s", strings.Join(run.failed, "\n"))
	}
}

// sw602ChargeDatabase is the database the charge test runs on and the number
// of workspaces it holds: a copy of the round trip's seed on SQLite (its
// fixture workspace), or an empty scratch database beside
// FLOW_TEST_DATABASE_URL on MySQL, which a bounded server starts on as the
// e2e run did.
func sw602ChargeDatabase(t *testing.T) (store.DatabaseConfig, int) {
	t.Helper()
	if os.Getenv("FLOW_TEST_DATABASE_DRIVER") != "mysql" {
		seed := rtSeed(t)
		path := filepath.Join(t.TempDir(), "charge.db")
		rtCopy(t, seed, path)
		return rtConfig(path), 1
	}
	rtEnv(t)
	parsed, err := url.Parse(os.Getenv("FLOW_TEST_DATABASE_URL"))
	if err != nil || parsed.Scheme != "mysql" {
		t.Fatalf("FLOW_TEST_DATABASE_URL %q is not a mysql:// URL", os.Getenv("FLOW_TEST_DATABASE_URL"))
	}
	password, _ := parsed.User.Password()
	admin, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s)/", parsed.User.Username(), password, parsed.Host))
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("flow_sw602_charge_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE `"+name+"` CHARACTER SET utf8mb4"); err != nil {
		admin.Close()
		t.Fatalf("create a scratch database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP DATABASE IF EXISTS `" + name + "`"); err != nil {
			t.Errorf("drop scratch database %s: %v", name, err)
		}
		admin.Close()
	})
	parsed.Path = "/" + name
	return store.DatabaseConfig{Driver: "mysql", URL: parsed.String(), MaxOpenConns: 8, MaxIdleConns: 8}, 0
}

// sw602ChargeOpen is rtOpen on config: a flag-on server signed in as the
// fixture admin, or as an owner it registers on an empty database.
func sw602ChargeOpen(t *testing.T, config store.DatabaseConfig, seeded int) *rtStack {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "on")
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatalf("open bounded: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if !repo.BoundedMetadataEnabled() {
		t.Fatal("the store opened with bounded metadata off")
	}
	svc := &server{store: repo, uploadPath: t.TempDir()}
	svc.agent.Enabled, svc.agent.WorkerSecret, svc.agent.Runtime = true, testWorkerSecret, appconfig.AgentRuntimeWorker
	host := httptest.NewServer(newTestHandler(t, svc))
	t.Cleanup(host.Close)
	st := &rtStack{t: t, repo: repo, svc: svc, host: host, bounded: true}
	email, password := "admin@example.test", "test-password"
	if seeded == 0 {
		email, password = "sw602-charge-owner@example.test", "sw602-charge-password"
		_, token, err := repo.Register(t.Context(), "SW602 charge owner", email, password)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.VerifyEmail(t.Context(), token); err != nil {
			t.Fatal(err)
		}
	}
	st.admin = rtLogin(t, host.URL, email, password)
	return st
}
