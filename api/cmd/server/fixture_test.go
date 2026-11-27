//go:build loadtest

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
	"flow/api/internal/store"

	"github.com/alicebob/miniredis/v2"
)

var scaleProbes sync.Map // *store.SQLiteStore -> *store.ScaleProbe

func scaleFloat(t *testing.T, name string, fallback float64) float64 {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 || math.IsInf(value, 0) || math.IsNaN(value) {
		t.Fatalf("%s must be a finite positive number", name)
	}
	return value
}

func scaleMetadataSpec(t *testing.T, teams, users []string) store.SyntheticMetadata {
	t.Helper()
	scale := scaleFloat(t, "FLOW_LOAD_METADATA_SCALE", scaleFloat(t, "FLOW_LOAD_SCALE", 1))
	count, body := scale, 1.0
	mode := os.Getenv("FLOW_LOAD_METADATA_MODE")
	if mode == "body" {
		count, body = 1, scale
	} else if mode != "" && mode != "count" {
		t.Fatal("FLOW_LOAD_METADATA_MODE must be count or body")
	}
	count = scaleFloat(t, "FLOW_LOAD_METADATA_COUNT_SCALE", count)
	body = scaleFloat(t, "FLOW_LOAD_METADATA_BODY_SCALE", body)
	return store.SyntheticMetadata{
		Projects: 2321, ProjectBytes: 12000, Trash: 210, TrashBytes: 41000, Documents: 78, DocumentBytes: 64000, Conversations: 400, ConversationBytes: 2000,
		CountScale: count, BodyScale: body, TeamIDs: teams, UserIDs: users, Notifications: len(users) * 2, Relations: true,
	}
}

func scaleNewPeers(t *testing.T) peerServers {
	t.Helper()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	redisServer := miniredis.RunT(t)
	path := filepath.Join(t.TempDir(), "scale-p01.db")
	config := store.DatabaseConfig{Driver: "sqlite", Path: path, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 1}
	config = scaleTestDatabaseConfig(t, config, 20)
	return scalePeers(t, config, redisServer.Addr())
}

func scalePeers(t *testing.T, config store.DatabaseConfig, redisAddr string) peerServers {
	t.Helper()
	var peers peerServers
	uploads := t.TempDir()
	for index := range 2 {
		repo, err := store.OpenDatabase(config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Close() })
		probe, err := repo.EnableScaleProbe(config)
		if err != nil {
			t.Fatal(err)
		}
		scaleProbes.Store(repo, probe)
		t.Cleanup(func() { scaleProbes.Delete(repo) })
		coordinator, err := coordination.Open(t.Context(), coordination.Config{Mode: "standalone", Addrs: []string{redisAddr}, Prefix: "scale-p01", ConnectTimeout: time.Second, LockTTL: 5 * time.Second, LockWait: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = coordinator.Close() })
		repo.SetWorkspaceCoordinator(coordinator)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		instance := &server{store: repo, uploadPath: uploads, coordinator: coordinator, coordinationContext: ctx}
		host := httptest.NewServer(scaleHandler(newTestHandler(t, instance), probe))
		t.Cleanup(host.Close)
		peers.stores[index], peers.servers[index], peers.deafen[index] = repo, instance, cancel
		if index == 0 {
			peers.first = host
		} else {
			peers.second = host
		}
	}
	return peers
}

type scaleIdentity struct {
	ID, Token, Role string
	TeamIDs         []string
	ExpectedKinds   map[string]bool
}

func scaleHandler(next http.Handler, probe *store.ScaleProbe) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Scale-P01-Request") != "" && r.Method != http.MethodGet && r.Method != http.MethodHead && !strings.HasPrefix(r.URL.Path, "/api/realtime/") {
			probe.BeginLoadWrite()
			defer probe.EndLoadWrite()
		}
		r = r.WithContext(store.ScaleRequestContext(r.Context(), r.Header.Get("X-Scale-P01-Request")))
		next.ServeHTTP(w, r)
	})
}

// The credential-bearing fixture is written only to an explicit 0600 file.
// It is never part of the public load report.
type fixtureScaleFixture struct {
	Workspace, WorkspaceID string
	Identities             []scaleIdentity
	Admin                  scaleIdentity
	TeamIDs, IssueIDs      []string
	DocumentID, ProjectID  string
	Spec                   store.SyntheticMetadata
	Sizes                  map[string]int
}

func scaleClient(identity scaleIdentity, bases []string, transport *http.Transport, timeout time.Duration) *http.Client {
	jar, _ := cookiejar.New(nil)
	for _, base := range bases {
		u, _ := url.Parse(base)
		jar.SetCookies(u, []*http.Cookie{{Name: sessionCookieName, Value: identity.Token, Path: "/"}})
	}
	return &http.Client{Jar: jar, Transport: transport, Timeout: timeout}
}

func scaleIdentityFromClient(t *testing.T, base string, client *http.Client, role string, teams []string) scaleIdentity {
	t.Helper()
	session := authRequest[domain.AuthSession](t, client, http.MethodGet, base+"/api/auth/session", nil, "", http.StatusOK)
	u, _ := url.Parse(base)
	for _, cookie := range client.Jar.Cookies(u) {
		if cookie.Name == sessionCookieName {
			return scaleIdentity{ID: session.User.ID, Token: cookie.Value, Role: role, TeamIDs: teams}
		}
	}
	t.Fatal("P01 session cookie missing")
	return scaleIdentity{}
}

func scaleSeedFixture(t *testing.T, peers peerServers) fixtureScaleFixture {
	t.Helper()
	base := peers.first.URL
	admin := peerAdmin(t, base)
	fixture := fixtureScaleFixture{Workspace: "test-workspace", WorkspaceID: peers.stores[0].Bootstrap().Workspace.ID, DocumentID: "lt-doc-0000", ProjectID: "lt-project-00000"}
	fixture.Admin = scaleIdentityFromClient(t, base, admin, "admin", nil)
	fixture.Identities = append(fixture.Identities, fixture.Admin)
	unique := loadEnvInt("FLOW_LOAD_UNIQUE_USERS", 5)
	if unique < 1 || unique > 10000 {
		t.Fatal("FLOW_LOAD_UNIQUE_USERS must be 1..10000")
	}
	permissionMode := os.Getenv("FLOW_LOAD_PERMISSION_MODE")
	if permissionMode != "" && permissionMode != "uniform" && permissionMode != "unique" {
		t.Fatal("FLOW_LOAD_PERMISSION_MODE must be uniform or unique")
	}
	teamCount := 4
	if permissionMode == "unique" {
		teamCount = 13
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	for index := range teamCount {
		team := authRequest[domain.Team](t, admin, http.MethodPost, base+"/api/workspaces/test-workspace/teams", map[string]any{"name": fmt.Sprintf("P01 team %d %s", index, suffix), "key": fmt.Sprintf("P%02d%s", index, suffix[len(suffix)-4:]), "private": index >= 2}, "", http.StatusCreated)
		fixture.TeamIDs = append(fixture.TeamIDs, team.ID)
	}
	if unique > 1 {
		people, err := peers.stores[0].SeedScalePrincipals(t.Context(), fixture.Workspace, fixture.TeamIDs, unique-1, permissionMode == "unique")
		if err != nil {
			t.Fatal(err)
		}
		for _, person := range people {
			fixture.Identities = append(fixture.Identities, scaleIdentity{ID: person.User.ID, Token: person.Token, Role: person.Role, TeamIDs: person.TeamIDs})
		}
	}
	for index := range 60 {
		issue := authRequest[domain.Issue](t, admin, http.MethodPost, base+"/api/issues", map[string]any{"title": fmt.Sprintf("P01 issue %d", index), "teamId": fixture.TeamIDs[index%len(fixture.TeamIDs)]}, fixture.Workspace, http.StatusCreated)
		fixture.IssueIDs = append(fixture.IssueIDs, issue.ID)
	}
	userIDs := []string{}
	for _, identity := range fixture.Identities {
		userIDs = append(userIDs, identity.ID)
	}
	fixture.Spec = scaleMetadataSpec(t, fixture.TeamIDs, userIDs)
	if err := peers.stores[1].SeedSyntheticMetadata(t.Context(), fixture.Workspace, fixture.Spec); err != nil {
		t.Fatal(err)
	}
	for _, repo := range peers.stores {
		if err := repo.ReloadWorkspace(t.Context(), fixture.Workspace); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	fixture.Sizes, err = peers.stores[0].WorkspaceStateBytes(t.Context(), fixture.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	for index := range fixture.Identities {
		identity := &fixture.Identities[index]
		identity.ExpectedKinds = map[string]bool{}
		request := httptest.NewRequest(http.MethodGet, "/api/realtime/events", nil)
		request.Header.Set("X-Workspace-Key", fixture.Workspace)
		request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, domain.User{ID: identity.ID, Active: true}))
		for _, candidate := range []struct{ kind, event, id string }{{"document", "document.updated", fixture.DocumentID}, {"comment", "project.commented", fixture.ProjectID}, {"team", "team.updated", fixture.TeamIDs[0]}, {"issue", "issue.updated", fixture.IssueIDs[0]}} {
			_, visible, err := peers.servers[0].pagedRealtimeEvent(request, domain.RealtimeEvent{ID: "p01-eligibility-" + identity.ID + "-" + candidate.kind, Type: candidate.event, AggregateID: candidate.id})
			if err != nil {
				t.Fatal("cannot establish authoritative load recipient set")
			}
			identity.ExpectedKinds[candidate.kind] = visible
		}
		identity.ExpectedKinds["access"] = true
	}
	return fixture
}

func scaleReadFixture(t *testing.T, path string) fixtureScaleFixture {
	t.Helper()
	var fixture fixtureScaleFixture
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &fixture) != nil || fixture.Workspace == "" || fixture.Admin.Token == "" || len(fixture.Identities) == 0 || len(fixture.TeamIDs) < 4 || len(fixture.IssueIDs) == 0 || fixture.DocumentID == "" || fixture.ProjectID == "" {
		t.Fatal("invalid FLOW_LOAD_FIXTURE (credential contents suppressed)")
	}
	seen := map[string]bool{}
	tokens := map[string]bool{}
	for _, identity := range fixture.Identities {
		if identity.ID == "" || identity.Token == "" || seen[identity.ID] || tokens[identity.Token] || identity.ExpectedKinds == nil {
			t.Fatal("fixture identities must have distinct IDs and valid sessions")
		}
		seen[identity.ID] = true
		tokens[identity.Token] = true
	}
	if unique := loadEnvInt("FLOW_LOAD_UNIQUE_USERS", len(fixture.Identities)); unique != len(fixture.Identities) {
		t.Fatal("FLOW_LOAD_UNIQUE_USERS does not match seeded fixture")
	}
	want := scaleMetadataSpec(t, fixture.TeamIDs, nil)
	if want.CountScale != fixture.Spec.CountScale || want.BodyScale != fixture.Spec.BodyScale {
		t.Fatal("requested metadata scales do not match seeded fixture")
	}
	return fixture
}

func scaleWriteCredentials(path string, raw []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".scale-p01-credentials-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func scaleURLs(t *testing.T, name string, required bool) []string {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" && !required {
		return nil
	}
	values := strings.Split(raw, ",")
	if len(values) != 2 || values[0] == values[1] {
		t.Fatalf("%s requires two distinct URLs", name)
	}
	for i, value := range values {
		u, err := url.Parse(strings.TrimRight(strings.TrimSpace(value), "/"))
		if err != nil || u.Host == "" || u.User != nil || u.Scheme != "http" && u.Scheme != "https" || u.RawQuery != "" || u.Fragment != "" {
			t.Fatalf("invalid %s (URL contents suppressed)", name)
		}
		values[i] = u.String()
	}
	return values
}
