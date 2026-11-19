package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// SW530: every registered route answers the same with bounded metadata on.
//
// The route table walk of SW490 lists every pattern registered in the server
// package. Each one is bound to a real record of a seeded fixture workspace
// (public, private and child teams; members of every role; issues, projects,
// documents, cycles, views, initiatives, customers, releases, loops,
// webhooks, OAuth applications, API keys, trash ...) and sent through the
// complete production handler (newHandler: authentication, authorization,
// cold route dispatch, every middleware) of
//
//   - legacy: the flag-off store;
//   - warm:   the bounded view holding the legacy installed image;
//   - reloaded: the bounded view reloaded from SQL right before the request.
//
// For every request the status, the normalised body, the response headers,
// the webhook and realtime publications and the stored rows and domain
// events the request wrote must be identical. Each request starts from the
// same fixture state: whenever a run changed it, the workspace is restored
// before the next run.
//
// Fast mode (the default, CI) sends each route once, rotating owner, member,
// guest and outsider and alternating cookie and API key. FLOW_SW530_FULL=1
// sends every route as every principal with both credentials.
// FLOW_SW530_ROUTES=<substring> limits the walk; FLOW_SW530_REPORT=<file>
// writes every divergence as JSON.
//
// With bounded metadata on, every route not proven bounded runs COLD (SW550:
// full legacy behaviour behind the per-replica COLD slot), so every route
// must answer as legacy. A proven-bounded (HOT) route must also never run
// COLD: its bounded runs take no COLD slot and serve no legacy accessor
// through a legacy view.
//
// scaleP21SW530KnownDivergences pins the route classes that still differ, each
// with an accepted reason; the test fails on any other divergence and on a
// pinned one that no longer differs, so the list can only shrink.

var scaleP21SW530KnownDivergences = map[string]string{
	// Flag-off ResourceWindowMetadata exceeds the page budget on this
	// fixture (a 450 KB unrelated project and settings row) and answers 501
	// "use the legacy read endpoint"; the bounded adapter answers 200.
	// Accepted by the lead (2026-11-18): more permissive, no business
	// regression.
	"GET /api/resource-windows/trash": "ACCEPTED: flag-off 501 over the page budget, bounded 200",
}

type sw530Principal struct {
	name   string
	user   domain.User
	token  string
	secret string
}

type sw530Request struct {
	pattern, method, path, body string
	principal                   *sw530Principal
	bearer                      bool
}

func (r sw530Request) label() string {
	credential := "cookie"
	if r.bearer {
		credential = "api-key"
	}
	return r.method + " " + r.path + " as " + r.principal.name + "/" + credential
}

type sw530Outcome struct {
	Status   int
	Header   map[string]string
	Body     string
	Webhooks []string
	Realtime []string
	Rows     []string
	Events   []string
	TimedOut bool `json:"-"`
}

type sw530Source struct {
	name    string
	store   *store.SQLiteStore
	handler http.Handler
}

type sw530Sweep struct {
	fx         *scaleP21SW491Fixture
	workspace  string
	ids        map[string]string
	baseline   []byte
	membership map[string][][]any
	numbers    [][]any
	principals []*sw530Principal
	legacy     sw530Source
	bounded    sw530Source
	mu         sync.Mutex
	webhooks   []string
	realtime   []string
	skeletal   []string
	// skeletalBy lists, per pattern, the legacy whole-workspace reads its
	// bounded runs served through a legacy view (with their callers), and
	// coldSlots counts the COLD slots they took.
	skeletalBy map[string]map[string]bool
	coldSlots  map[string]int64
	resets     int
	probeStale bool
	// totalSlots and totalViews count the COLD slots and legacy views the
	// bounded runs took.
	totalSlots, totalViews int64
}

type sw530Divergence struct {
	Route, Request, Source, Kind, Legacy, Bounded string
}

var (
	sw530Stamp     = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	sw530Order     = regexp.MustCompile(`"collection_order":-?1[0-9]{15,}`)
	sw530RandomHex = regexp.MustCompile(`\b[0-9a-f]{32,}\b`)
	// Random tokens and generated addresses of invite links and intake.
	sw530Token     = regexp.MustCompile(`"(token|localPart|address|verificationToken|secretPrefix|challenge)":"[^"]*"`)
	sw530RandomB64 = regexp.MustCompile(`flow_[a-z]+_[A-Za-z0-9_-]{16,}`)
	// Values each run draws again: a stored secret prefix (in an escaped
	// row), a share token, a new slug's random suffix and the analytics
	// window, which starts a fixed time before the request.
	sw530EscapedToken = regexp.MustCompile(`\\"(secretPrefix)\\":\\"[^"\\]*\\"`)
	sw530ShareToken   = regexp.MustCompile(`dsh_[A-Za-z0-9_-]{16}`)
	sw530Registration = regexp.MustCompile(`passkey_reg_[A-Za-z0-9_-]{16,}`)
	// A WebAuthn challenge stored inside an escaped session row.
	sw530Challenge  = regexp.MustCompile(`(challenge\\+":\\+")[A-Za-z0-9_-]{16,}`)
	sw530SlugSuffix = regexp.MustCompile(`("slugId\\?":\\?"[^"\\]*-)[0-9a-f]{12}(\\?")`)
	sw530Since      = regexp.MustCompile(`"since":"[^"]*"`)
)

func sw530Normalize(text string, started time.Time) string {
	text = scaleP21TriageStamp.ReplaceAllString(text, "<stamp>")
	text = scaleP21TriageClock.ReplaceAllStringFunc(text, func(clock string) string {
		if at, err := time.Parse(time.RFC3339Nano, clock); err == nil && !at.Before(started) {
			return "<now>"
		}
		return clock
	})
	text = sw410Secret.ReplaceAllString(text, "flow_${1}_<secret>")
	text = sw450Generated.ReplaceAllString(text, "flow_${1}_<generated>")
	text = sw410Family.ReplaceAllString(text, "family_<random>")
	text = sw530RandomB64.ReplaceAllString(text, "flow_<random>")
	text = sw530Stamp.ReplaceAllString(text, "<uuid>")
	text = sw530Order.ReplaceAllString(text, `"collection_order":<order>`)
	text = sw530Token.ReplaceAllString(text, `"$1":"<random>"`)
	text = sw530EscapedToken.ReplaceAllString(text, `\"$1\":\"<random>\"`)
	text = sw530ShareToken.ReplaceAllString(text, "dsh_<random>")
	text = sw530Registration.ReplaceAllString(text, "passkey_reg_<random>")
	text = sw530Challenge.ReplaceAllString(text, "${1}<random>")
	text = sw530SlugSuffix.ReplaceAllString(text, "${1}<suffix>${2}")
	text = sw530Since.ReplaceAllString(text, `"since":"<since>"`)
	return sw530RandomHex.ReplaceAllString(text, "<hex>")
}

// Headers that carry per-request values only.
var sw530VolatileHeaders = map[string]bool{"Date": true, "X-Request-Id": true, "Content-Length": true, "Server-Timing": true, "X-Response-Time": true}

func TestScaleP21SW530AllRoutesFlagOnDifferential(t *testing.T) {
	t.Setenv("FLOW_API_RATE_LIMIT", "off")
	t.Setenv("FLOW_RECORD_ACCESS", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32))))
	full := os.Getenv("FLOW_SW530_FULL") == "1"
	oldSlots := webhookDeliverySlots
	webhookDeliverySlots = make(chan struct{})
	t.Cleanup(func() { webhookDeliverySlots = oldSlots })
	x := sw530Setup(t)
	requests := x.requests(t, full)
	t.Logf("SW530 sweep: %d requests (full=%v)", len(requests), full)
	var divergences []sw530Divergence
	statuses := map[int]int{}
	var trace []string
	started := time.Now()
	for index, request := range requests {
		got, status := x.compare(t, request)
		divergences = append(divergences, got...)
		statuses[status]++
		trace = append(trace, fmt.Sprintf("%d %s", status, request.label()))
		if index%100 == 99 {
			t.Logf("SW530 %d/%d requests, %d divergences, %d resets, %s", index+1, len(requests), len(divergences), x.resets, time.Since(started).Round(time.Second))
		}
	}
	t.Logf("SW530 legacy statuses: %v", statuses)
	skeletal := make([]string, 0, len(x.skeletalBy))
	for pattern, reads := range x.skeletalBy {
		skeletal = append(skeletal, pattern+": "+strings.Join(slices.Sorted(maps.Keys(reads)), "; "))
	}
	sort.Strings(skeletal)
	for _, line := range skeletal {
		t.Logf("SW530 skeletal bounded read: %s", line)
	}
	if path := os.Getenv("FLOW_SW530_TRACE"); path != "" {
		_ = os.WriteFile(path, []byte(strings.Join(trace, "\n")+"\n"), 0o600)
	}
	// Confirm each divergence with the sources in the other order: a
	// difference that follows the order comes from state the restore does not
	// cover (account rows), not from the flag.
	confirmed := []sw530Divergence{}
	seen := map[string]bool{}
	for _, item := range divergences {
		if seen[item.Request+"|"+item.Source] {
			continue
		}
		seen[item.Request+"|"+item.Source] = true
		var request sw530Request
		for _, candidate := range requests {
			if candidate.label() == item.Request {
				request = candidate
				break
			}
		}
		if x.confirm(t, request, item.Source) {
			confirmed = append(confirmed, item)
		} else {
			t.Logf("SW530 order-dependent (not a flag divergence): %s %s", item.Request, item.Source)
		}
	}
	classes := map[string][]sw530Divergence{}
	for _, item := range confirmed {
		classes[item.Route] = append(classes[item.Route], item)
	}
	names := slices.Sorted(maps.Keys(classes))
	if path := os.Getenv("FLOW_SW530_REPORT"); path != "" {
		raw, _ := json.MarshalIndent(confirmed, "", "  ")
		_ = os.WriteFile(path, raw, 0o600)
	}
	open := 0
	for _, name := range names {
		items := classes[name]
		first := items[0]
		message := fmt.Sprintf("%s: %d request(s) differ, e.g. %s (%s, %s):\n legacy=%s\nbounded=%s", name, len(items), first.Request, first.Source, first.Kind, sw530Clip(first.Legacy), sw530Clip(first.Bounded))
		if reason, known := scaleP21SW530KnownDivergences[name]; known {
			open++
			t.Logf("SW530 pinned divergence (%s) %s", reason, message)
			continue
		}
		t.Error("SW530 divergence: " + message)
	}
	// A proven-bounded route never runs COLD (SW550): no COLD slot, no
	// legacy accessor served through a legacy view.
	cold := 0
	for _, request := range requests {
		if !scaleP21RouteHot(request.pattern) {
			cold++
		}
	}
	for pattern, reads := range x.skeletalBy {
		if scaleP21RouteHot(pattern) {
			t.Errorf("SW530 HOT route %s read legacy accessors through a legacy view: %s", pattern, strings.Join(slices.Sorted(maps.Keys(reads)), "; "))
		}
	}
	for pattern, slots := range x.coldSlots {
		if scaleP21RouteHot(pattern) && slots != 0 {
			t.Errorf("SW530 HOT route %s took %d COLD slot(s)", pattern, slots)
		}
	}
	if os.Getenv("FLOW_SW530_ROUTES") == "" {
		for name := range scaleP21SW530KnownDivergences {
			if _, still := classes[name]; !still {
				t.Errorf("pinned divergence %s no longer differs: remove it", name)
			}
		}
	}
	t.Logf("SW530 COLD routing: %d of %d requests on COLD routes, %d COLD slot acquisitions, %d legacy accessor views", cold, len(requests), x.totalSlots, x.totalViews)
	if facades, facadeTime, viewTime := store.ColdHydrationForTesting(); facades > 0 {
		views := max(store.ColdAccessorReadsForTesting(), 1)
		t.Logf("SW530 COLD hydration: %d facade decodes, %s each on average (%s total); %d legacy views, %s each on average", facades, (facadeTime / time.Duration(facades)).Round(time.Microsecond), facadeTime.Round(time.Millisecond), views, (viewTime / time.Duration(views)).Round(time.Microsecond))
	}
	if holds, total, longest := store.ColdWriteLockHoldForTesting(); holds > 0 {
		t.Logf("SW530 COLD legacy writes held the shared write lock %d times, %s on average, %s at most", holds, (total / time.Duration(holds)).Round(time.Microsecond), longest.Round(time.Microsecond))
	}
	t.Logf("SW530 done: %d requests x 3 sources, %d divergent route classes (%d pinned), %d resets, %s", len(requests), len(names), open, x.resets, time.Since(started).Round(time.Second))
}

func sw530Clip(text string) string {
	if len(text) > 1500 {
		return text[:1500] + "..."
	}
	return text
}

func sw530Setup(t *testing.T) *sw530Sweep {
	t.Helper()
	fx := scaleP21SW491Setup(t, nil)
	x := &sw530Sweep{fx: fx, workspace: fx.workspace, ids: map[string]string{}, skeletalBy: map[string]map[string]bool{}, coldSlots: map[string]int64{}}
	f := fx.f
	uploads := t.TempDir()
	newService := func(repo *store.SQLiteStore) http.Handler {
		s := &server{store: repo, uploadPath: uploads}
		// Routes only: no background workers write while requests are compared.
		for _, started := range []interface{ Store(bool) }{&s.socketAccessSweepStarted, &s.workflowSchedulerStarted, &s.deliverySchedulerStarted, &s.loopReconcilerStarted, &s.agentRunReconcilerStarted, &s.agentLiveSweepStarted, &s.coordinationStarted} {
			started.Store(true)
		}
		handler := newHandler(s)
		repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
			x.mu.Lock()
			defer x.mu.Unlock()
			x.webhooks = append(x.webhooks, string(scaleP21SW106Envelope(t, map[string]any{
				"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
				"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot,
			})))
		})
		repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
			x.mu.Lock()
			defer x.mu.Unlock()
			x.realtime = append(x.realtime, string(scaleP21SW106Envelope(t, map[string]any{
				"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
				"clientId": event.ClientID, "payload": event.Payload,
			})))
		})
		return handler
	}
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	x.legacy = sw530Source{"legacy", f.repository, newService(f.repository)}
	x.bounded = sw530Source{"bounded", f.probe, newService(f.probe)}
	f.probe.SetSkeletalReadObserverForTesting(func(accessor string) {
		x.mu.Lock()
		defer x.mu.Unlock()
		x.skeletal = append(x.skeletal, accessor+" from "+sw530Caller())
	})
	t.Cleanup(func() { f.probe.SetSkeletalReadObserverForTesting(nil) })

	byName := map[string]domain.User{}
	for _, item := range fx.principals {
		byName[item.name] = item.user
	}
	for _, spec := range []struct{ name, fixture string }{
		{"owner", "admin-private-outsider"}, {"admin", "admin"}, {"member", "member-private-outsider"},
		{"guest", "guest-explicit"}, {"subteam", "sub-team-inheritance"}, {"suspended", "suspended"}, {"outsider", "outsider"},
	} {
		user, ok := byName[spec.fixture]
		if !ok {
			t.Fatal("SW530 principal missing", spec.fixture)
		}
		x.principals = append(x.principals, &sw530Principal{name: spec.name, user: user, token: "sw530-session-" + spec.name, secret: "sw530-key-secret-" + spec.name})
	}
	x.sessions(t)
	x.seed(t)
	// The fixture writes raw records into the legacy installed image; a
	// server reading SQL normalizes them. Compare what a running server
	// holds: the legacy image reloaded from SQL.
	if err := f.repository.ReloadWorkspace(t.Context(), x.workspace); err != nil {
		t.Fatal(err)
	}
	full, ok := f.repository.BootstrapFor(x.workspace)
	if !ok {
		t.Fatal("SW530 workspace missing")
	}
	var err error
	if x.baseline, err = json.Marshal(full); err != nil {
		t.Fatal(err)
	}
	x.numbers = x.rows(t, `SELECT last_number FROM issue_number_sequences WHERE workspace_key=?`, x.workspace)
	x.membership = map[string][][]any{}
	for _, table := range []string{"workspace_memberships", "team_memberships"} {
		x.membership[table] = x.rows(t, `SELECT * FROM `+table+` WHERE workspace_id=?`, f.data.Workspace.ID)
	}
	x.probeStale = true
	return x
}

// sessions (re)creates each principal's cookie session; a request may have
// signed one out.
func (x *sw530Sweep) sessions(t *testing.T) {
	t.Helper()
	stamp := time.Now().UTC()
	for _, principal := range x.principals {
		x.exec(t, `DELETE FROM auth_sessions WHERE token_hash=?`, secretHash(principal.token))
		x.exec(t, `INSERT INTO auth_sessions(token_hash,user_id,expires_at,created_at,last_seen_at) VALUES(?,?,?,?,?)`, secretHash(principal.token), principal.user.ID,
			stamp.Add(24*time.Hour).Format(time.RFC3339Nano), stamp.Format(time.RFC3339Nano), stamp.Format(time.RFC3339Nano))
	}
}

func (x *sw530Sweep) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	database := x.fx.f.reader
	database.query(t, `SELECT 1 FROM workspace_states WHERE workspace_key=?`, database.key).Close()
	if database.config.Driver == "postgres" || database.config.Driver == "postgresql" {
		for index := range args {
			query = strings.Replace(query, "?", fmt.Sprintf("$%d", index+1), 1)
		}
	}
	err := sw530Retry(func() error {
		_, err := database.reader.ExecContext(t.Context(), query, args...)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// sw530Retry retries a fixture statement while a request the sweep gave up
// on (or its follow-up goroutine) still holds the SQLite write lock.
func sw530Retry(run func() error) error {
	var err error
	for range 250 {
		if err = run(); err == nil || !strings.Contains(err.Error(), "database is locked") && !strings.Contains(err.Error(), "SQLITE_BUSY") {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
	return err
}

// query is scaleP04Database.query with sw530Retry.
func (x *sw530Sweep) query(t *testing.T, query string, arg any) *sql.Rows {
	t.Helper()
	database := x.fx.f.reader
	database.query(t, `SELECT 1 FROM workspace_states WHERE workspace_key=?`, database.key).Close()
	if database.config.Driver == "postgres" || database.config.Driver == "postgresql" {
		query = strings.Replace(query, "?", "$1", 1)
	}
	var rows *sql.Rows
	err := sw530Retry(func() error {
		var err error
		rows, err = database.reader.Query(query, arg)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func (x *sw530Sweep) rows(t *testing.T, query string, arg any) [][]any {
	t.Helper()
	rows := x.query(t, query, arg)
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		for index, value := range values {
			if raw, ok := value.([]byte); ok {
				values[index] = string(raw)
			}
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

// call sends one request through handler.
func (x *sw530Sweep) call(handler http.Handler, method, path, body string, principal *sw530Principal, bearer bool) (*httptest.ResponseRecorder, bool) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, reader)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("X-Workspace-Key", x.workspace)
	r.Header.Set("X-Request-ID", "sw530")
	if principal != nil {
		if bearer {
			r.Header.Set("Authorization", "Bearer "+principal.secret)
		} else {
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: principal.token})
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		handler.ServeHTTP(w, r.WithContext(ctx))
	}()
	select {
	case <-done:
		return w, false
	case <-time.After(6 * time.Second):
		return httptest.NewRecorder(), true
	}
}

// create sends a fixture request as the owner on the legacy handler and
// returns the created record's id ("" when the route refused it).
func (x *sw530Sweep) create(t *testing.T, key, method, path, body string) string {
	t.Helper()
	w, _ := x.call(x.legacy.handler, method, path, body, x.principals[0], false)
	if w.Code >= 300 {
		t.Logf("SW530 fixture %s: %s %s answered %d %s", key, method, path, w.Code, sw530Clip(w.Body.String()))
		return ""
	}
	var value map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Logf("SW530 fixture %s: %s", key, err)
		return ""
	}
	if nested, ok := value["key"].(map[string]any); ok {
		value = nested
	}
	id, _ := value["id"].(string)
	if id != "" {
		x.ids[key] = id
	}
	return id
}

// seed adds a record of every kind a route can address, through the legacy
// routes where one exists (so each record is valid), and an API key per
// principal.
func (x *sw530Sweep) seed(t *testing.T) {
	t.Helper()
	f, fx := x.fx.f, x.fx
	public, private, child := f.public, fx.privateTeam, fx.childTeam
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	keyed := func(principal *sw530Principal, workspace string) domain.APIKey {
		return domain.APIKey{ID: "sw530-key-" + principal.name, Name: "SW530 " + principal.name, Prefix: "sw530_" + principal.name, SecretHash: secretHash(principal.secret),
			CreatorID: principal.user.ID, Scopes: []string{"read", "write", "admin"}, TeamIDs: []string{}, TeamRestriction: "all", CreatedAt: at}
	}
	if err := f.repository.MutateWorkspace(t.Context(), x.workspace, "scale_p21_sw530.seed", "", nil, func(data *domain.Bootstrap) error {
		for _, principal := range x.principals {
			if principal.name != "outsider" {
				data.APIKeys = append(data.APIKeys, keyed(principal, x.workspace))
			}
		}
		data.Cycles = append(data.Cycles,
			domain.Cycle{ID: "sw530-cycle", Number: 1, Name: "SW530 cycle", TeamID: public, StartsAt: at, EndsAt: at.AddDate(0, 0, 14), Status: "current"},
			domain.Cycle{ID: "sw530-cycle-private", Number: 1, Name: "SW530 private cycle", TeamID: private, StartsAt: at, EndsAt: at.AddDate(0, 0, 14), Status: "current"},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	foreign := fx.foreign.data.Workspace.URLKey
	for _, principal := range x.principals {
		if principal.name == "outsider" {
			if err := f.repository.MutateWorkspace(t.Context(), foreign, "scale_p21_sw530.foreign_key", "", nil, func(data *domain.Bootstrap) error {
				data.APIKeys = append(data.APIKeys, keyed(principal, foreign))
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	x.ids["cycles"], x.ids["cycles-private"] = "sw530-cycle", "sw530-cycle-private"
	x.ids["projects"], x.ids["initiatives"], x.ids["trash"] = fx.ids["pub-project"], fx.ids["pub-initiative"], fx.prefix+"trash"
	x.ids["reviews"], x.ids["webhooks"] = fx.prefix+"review-open", "m2-hook"
	x.ids["teams"], x.ids["private-team"], x.ids["child-team"] = public, private, child
	x.ids["api-keys"] = "sw530-key-member"
	jsonBody := func(value any) string {
		raw, _ := json.Marshal(value)
		return string(raw)
	}
	issue := x.create(t, "issues", http.MethodPost, "/api/issue-records", jsonBody(map[string]any{"title": "SW530 public issue", "teamId": public, "description": "SW530 body"}))
	x.ids["issue-records"] = issue
	x.create(t, "private-issue", http.MethodPost, "/api/issue-records", jsonBody(map[string]any{"title": "SW530 private issue", "teamId": private}))
	x.create(t, "child-issue", http.MethodPost, "/api/issue-records", jsonBody(map[string]any{"title": "SW530 child issue", "teamId": child}))
	if issue != "" {
		x.create(t, "issues/comments", http.MethodPost, "/api/issues/"+issue+"/comments", jsonBody(map[string]any{"body": "SW530 comment"}))
		x.create(t, "issues/attachments", http.MethodPost, "/api/issues/"+issue+"/links", jsonBody(map[string]any{"url": "https://example.test/sw530", "title": "SW530 link"}))
	}
	document := x.create(t, "documents", http.MethodPost, "/api/documents", jsonBody(map[string]any{"title": "SW530 document", "content": "SW530 content", "projectId": x.ids["projects"]}))
	if document != "" {
		x.create(t, "documents/comments", http.MethodPost, "/api/documents/"+document+"/comments", jsonBody(map[string]any{"body": "SW530 document comment"}))
	}
	x.create(t, "projects/comments", http.MethodPost, "/api/projects/"+x.ids["projects"]+"/comments", jsonBody(map[string]any{"body": "SW530 project comment"}))
	x.create(t, "projects/updates", http.MethodPost, "/api/projects/"+x.ids["projects"]+"/updates", jsonBody(map[string]any{"body": "SW530 update", "health": "onTrack"}))
	x.create(t, "initiatives/updates", http.MethodPost, "/api/initiatives/"+x.ids["initiatives"]+"/updates", jsonBody(map[string]any{"body": "SW530 initiative update", "health": "onTrack"}))
	x.create(t, "views", http.MethodPost, "/api/views", jsonBody(map[string]any{"name": "SW530 view", "resource": "issues", "scope": "workspace"}))
	customer := x.create(t, "customers", http.MethodPost, "/api/customers", jsonBody(map[string]any{"name": "SW530 customer"}))
	if customer != "" {
		x.create(t, "customer-requests", http.MethodPost, "/api/customer-requests", jsonBody(map[string]any{"customerId": customer, "body": "SW530 request", "issueId": issue}))
	}
	pipeline := x.create(t, "release-pipelines", http.MethodPost, "/api/release-pipelines", jsonBody(map[string]any{"name": "SW530 pipeline", "teamIds": []string{public}}))
	x.create(t, "releases", http.MethodPost, "/api/releases", jsonBody(map[string]any{"name": "SW530 release", "pipelineId": pipeline}))
	x.create(t, "labels", http.MethodPost, "/api/labels", jsonBody(map[string]any{"name": "SW530 label", "color": "#4cb782"}))
	x.create(t, "label-groups", http.MethodPost, "/api/label-groups", jsonBody(map[string]any{"name": "SW530 group", "color": "#4cb782"}))
	x.create(t, "loops", http.MethodPost, "/api/loops", jsonBody(map[string]any{"name": "SW530 loop", "status": "draft"}))
	x.create(t, "dashboards", http.MethodPost, "/api/dashboards", jsonBody(map[string]any{"name": "SW530 dashboard"}))
	x.create(t, "asks", http.MethodPost, "/api/asks", jsonBody(map[string]any{"title": "SW530 ask", "teamId": public}))
	x.create(t, "oauth-applications", http.MethodPost, "/api/oauth-applications", jsonBody(map[string]any{"name": "SW530 app", "scopes": []string{"read", "app:assignable"}, "redirectUris": []string{"https://example.test/sw530/callback"}}))
	x.create(t, "issue-templates", http.MethodPost, "/api/issue-templates", jsonBody(map[string]any{"name": "SW530 template", "teamId": public}))
	x.create(t, "document-templates", http.MethodPost, "/api/document-templates", jsonBody(map[string]any{"name": "SW530 doc template", "title": "Record"}))
	x.create(t, "project-statuses", http.MethodPost, "/api/project-statuses", jsonBody(map[string]any{"name": "SW530 status", "type": "started", "color": "#aaaaaa"}))
	x.create(t, "sla-rules", http.MethodPost, "/api/sla-rules", jsonBody(map[string]any{"name": "SW530 SLA", "teamIds": []string{public}}))
	x.create(t, "project-templates", http.MethodPost, "/api/project-templates", jsonBody(map[string]any{"name": "SW530 project template", "projectName": "SW530 templated"}))
	x.create(t, "drafts", http.MethodPost, "/api/drafts", jsonBody(map[string]any{"type": "issue", "title": "SW530 draft", "body": "Draft body"}))
	x.create(t, "webhooks-new", http.MethodPost, "/api/webhooks", jsonBody(map[string]any{"name": "SW530 hook", "url": "https://example.com/sw530", "resourceTypes": []string{"issues"}}))
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), f.repository, x.workspace); err != nil {
		t.Fatal(err)
	}
	t.Logf("SW530 fixture ids: %v", x.ids)
}

// sw530Bind fills a pattern's wildcards with fixture records: the record of
// the collection named before the wildcard (nested under its parent where
// the fixture has one), the workspace key, or a missing id.
func (x *sw530Sweep) bind(pattern string) (method, path string) {
	method, path, found := strings.Cut(pattern, " ")
	if !found {
		method, path = http.MethodGet, pattern
	}
	segments := strings.Split(path, "/")
	parent := ""
	for index, segment := range segments {
		if !strings.HasPrefix(segment, "{") {
			if segment != "" && segment != "api" {
				if index+1 < len(segments) && strings.HasPrefix(segments[index+1], "{") && parent == "" {
					parent = segment
				}
			}
			continue
		}
		name := strings.Trim(segment, "{}")
		previous := ""
		if index > 0 {
			previous = segments[index-1]
		}
		value := "sw530-missing"
		switch {
		case name == "$":
			value = ""
		case strings.HasSuffix(name, "..."):
			value = "sw530/tail"
		case strings.Contains(strings.ToLower(name), "workspace"):
			value = x.workspace
		case name == "userId":
			value = x.principals[2].user.ID
		case name == "type" && (previous == "favorites" || previous == "subscriptions"):
			value = "project"
		case name == "provider":
			value = "github"
		default:
			if id, ok := x.ids[parent+"/"+previous]; ok && parent != previous {
				value = id
			} else if id, ok := x.ids[previous]; ok {
				value = id
			} else if previous == "issues" || previous == "issue" {
				value = x.ids["issue-records"]
			} else if previous == "teams" || name == "teamId" {
				value = x.ids["teams"]
			}
		}
		if value == "" && name != "$" {
			value = "sw530-missing"
		}
		segments[index] = value
	}
	return method, strings.Join(segments, "/")
}

// sw530Skipped are patterns the sweep cannot send to all sources in turn:
// streams that never end and account deletion (the fixture cannot be
// restored). Each is covered by its own differential.
var sw530Skipped = map[string]string{
	"GET /api/events":      "server-sent event stream",
	"DELETE /api/me":       "deletes the principal's account",
	"DELETE /api/users/me": "deletes the principal's account",
}

// sw530Bodies are the request bodies of the routes whose minimal valid
// body is not {}.
var sw530Bodies = map[string]string{
	"POST /api/issue-records":                     `{"title":"SW530 created","teamId":"$public"}`,
	"POST /api/issues":                            `{"title":"SW530 created","teamId":"$public"}`,
	"PATCH /api/issue-records/{id}":               `{"title":"SW530 renamed","priority":2}`,
	"PATCH /api/issues/{id}":                      `{"title":"SW530 renamed","priority":2}`,
	"POST /api/issues/{id}/comments":              `{"body":"SW530 reply"}`,
	"POST /api/issue-records/{id}/comments":       `{"body":"SW530 reply"}`,
	"PATCH /api/issues/{id}/comments/{commentId}": `{"body":"SW530 edited"}`,
	"POST /api/documents":                         `{"title":"SW530 doc"}`,
	"PATCH /api/documents/{id}":                   `{"title":"SW530 doc renamed"}`,
	"POST /api/documents/{id}/comments":           `{"body":"SW530 doc reply"}`,
	"POST /api/projects":                          `{"name":"SW530 project","teamIds":["$public"]}`,
	"PATCH /api/projects/{id}":                    `{"name":"SW530 project renamed"}`,
	"POST /api/projects/{id}/comments":            `{"body":"SW530 project reply"}`,
	"POST /api/projects/{id}/updates":             `{"body":"SW530 project update","health":"atRisk"}`,
	"POST /api/initiatives":                       `{"name":"SW530 initiative"}`,
	"PATCH /api/initiatives/{id}":                 `{"name":"SW530 initiative renamed"}`,
	"POST /api/initiatives/{id}/updates":          `{"body":"SW530 initiative update","health":"atRisk"}`,
	"POST /api/views":                             `{"name":"SW530 view 2","resource":"issues","scope":"workspace"}`,
	"PATCH /api/views/{id}":                       `{"name":"SW530 view renamed"}`,
	"POST /api/customers":                         `{"name":"SW530 customer 2"}`,
	"PATCH /api/customers/{id}":                   `{"name":"SW530 customer renamed"}`,
	"POST /api/customer-requests":                 `{"customerId":"$customers","body":"SW530 request 2"}`,
	"POST /api/releases":                          `{"name":"SW530 release 2","pipelineId":"$release-pipelines"}`,
	"PATCH /api/releases/{id}":                    `{"name":"SW530 release renamed"}`,
	"POST /api/release-pipelines":                 `{"name":"SW530 pipeline 2","teamIds":["$public"]}`,
	"POST /api/labels":                            `{"name":"SW530 label 2","color":"#ff0000"}`,
	"PATCH /api/labels/{id}":                      `{"name":"SW530 label renamed"}`,
	"POST /api/loops":                             `{"name":"SW530 loop 2","status":"draft"}`,
	"PATCH /api/loops/{id}":                       `{"name":"SW530 loop renamed"}`,
	"POST /api/dashboards":                        `{"name":"SW530 dashboard 2"}`,
	"POST /api/asks":                              `{"title":"SW530 ask 2","teamId":"$public"}`,
	"POST /api/api-keys":                          `{"name":"SW530 new key","scopes":["read"]}`,
	"PATCH /api/api-keys/{id}":                    `{"name":"SW530 key renamed"}`,
	"POST /api/oauth-applications":                `{"name":"SW530 app 2","scopes":["read"]}`,
	"PATCH /api/oauth-applications/{id}":          `{"name":"SW530 app renamed"}`,
	"POST /api/webhooks":                          `{"name":"SW530 hook 2","url":"https://example.com/sw530-2","resourceTypes":["issues"]}`,
	"PATCH /api/webhooks/{id}":                    `{"name":"SW530 hook renamed"}`,
	"POST /api/drafts":                            `{"type":"issue","title":"SW530 draft 2","body":"Draft"}`,
	"POST /api/issue-templates":                   `{"name":"SW530 template 2","teamId":"$public"}`,
	"POST /api/document-templates":                `{"name":"SW530 doc template 2","title":"Record"}`,
	"POST /api/project-statuses":                  `{"name":"SW530 status 2","type":"started","color":"#bbbbbb"}`,
	"POST /api/sla-rules":                         `{"name":"SW530 SLA 2","teamIds":["$public"]}`,
	"POST /api/workspaces/{workspaceKey}/teams":   `{"name":"SW530 team","key":"SWT"}`,
	"PATCH /api/teams/{id}":                       `{"name":"SW530 team renamed"}`,
	"PATCH /api/me":                               `{"name":"SW530 renamed user"}`,
}

func (x *sw530Sweep) body(pattern, method string) string {
	if method == http.MethodGet || method == http.MethodHead {
		return ""
	}
	body, ok := sw530Bodies[pattern]
	if !ok {
		return "{}"
	}
	body = strings.ReplaceAll(body, "$public", x.ids["teams"])
	for key, id := range x.ids {
		body = strings.ReplaceAll(body, `"$`+key+`"`, strconv.Quote(id))
	}
	return body
}

func (x *sw530Sweep) requests(t *testing.T, full bool) []sw530Request {
	t.Helper()
	filter := os.Getenv("FLOW_SW530_ROUTES")
	sweep := []*sw530Principal{x.principals[0], x.principals[2], x.principals[3], x.principals[6]}
	if full {
		sweep = x.principals
	}
	var result []sw530Request
	for _, pattern := range scaleP21SW490Patterns(t) {
		if _, skip := sw530Skipped[pattern]; skip || filter != "" && !strings.Contains(pattern, filter) {
			continue
		}
		method, path := x.bind(pattern)
		methods := []string{method}
		if !strings.Contains(pattern, " ") {
			methods = []string{http.MethodGet, http.MethodPost}
		}
		for _, method := range methods {
			body := x.body(pattern, method)
			if full {
				for _, principal := range sweep {
					for _, bearer := range []bool{false, true} {
						result = append(result, sw530Request{pattern, method, path, body, principal, bearer})
					}
				}
				continue
			}
			// The pattern picks its principal and credential, so adding a
			// route never moves another one to a different principal.
			digest := fnv.New32a()
			_, _ = digest.Write([]byte(pattern))
			pick := int(digest.Sum32())
			result = append(result, sw530Request{pattern, method, path, body, sweep[pick%len(sweep)], pick/len(sweep)%2 == 1})
		}
	}
	return result
}

type sw530Snapshot map[string]string

// snapshot reads every workspace row a request can write (metadata records,
// issue and content records, the root, memberships, installations) and the
// workspace's domain and audit events.
func (x *sw530Sweep) snapshot(t *testing.T) sw530Snapshot {
	t.Helper()
	f := x.fx.f
	result := sw530Snapshot{}
	add := func(table string, keys int, query string, arg any) {
		for _, row := range x.rows(t, query, arg) {
			key := table
			for _, value := range row[:keys] {
				key += "|" + fmt.Sprint(value)
			}
			raw, _ := json.Marshal(row[keys:])
			result[key] = string(raw)
		}
	}
	add("metadata", 2, `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=?`, x.workspace)
	add("issue", 1, `SELECT id,data FROM issue_records WHERE workspace_key=?`, x.workspace)
	add("content", 3, `SELECT kind,resource_id,id,created_at,data,status FROM workspace_content_records WHERE workspace_key=?`, x.workspace)
	add("root", 1, `SELECT workspace_key,data FROM workspace_states WHERE workspace_key=?`, x.workspace)
	add("member", 1, `SELECT user_id,role,status FROM workspace_memberships WHERE workspace_id=?`, f.data.Workspace.ID)
	add("team-member", 2, `SELECT team_id,user_id,role FROM team_memberships WHERE workspace_id=?`, f.data.Workspace.ID)
	add("installation", 1, `SELECT id,client_id,user_id,data FROM application_installations WHERE workspace_key=?`, x.workspace)
	add("event", 1, `SELECT id,event_type,aggregate_id,payload,previous_values FROM domain_events WHERE workspace_id=?`, f.data.Workspace.ID)
	add("audit", 1, `SELECT id,actor_id,action,resource_type,resource_id,result,metadata FROM audit_events WHERE workspace_id=?`, f.data.Workspace.ID)
	return result
}

// delta lists what changed between two snapshots: restorable rows (rows)
// and appended events (events), normalised.
func sw530Delta(before, after sw530Snapshot, started time.Time) (rows, events []string, dirty bool) {
	for key, value := range after {
		if previous, ok := before[key]; ok && previous == value {
			continue
		}
		if strings.HasSuffix(key, "|workspaceInviteLink|token") {
			value = "<random token>"
		}
		line := sw530Normalize(key+" = "+value, started)
		if strings.HasPrefix(key, "event|") || strings.HasPrefix(key, "audit|") {
			events = append(events, line)
			continue
		}
		dirty = true
		rows = append(rows, line)
	}
	for key := range before {
		if _, ok := after[key]; !ok {
			if strings.HasPrefix(key, "event|") || strings.HasPrefix(key, "audit|") {
				events = append(events, sw530Normalize(key+" removed", started))
				continue
			}
			dirty = true
			rows = append(rows, sw530Normalize(key+" removed", started))
		}
	}
	sort.Strings(rows)
	sort.Strings(events)
	return rows, events, dirty
}

// reset restores the fixture workspace through the literal full writer, the
// membership rows and the principals' sessions.
func (x *sw530Sweep) reset(t *testing.T) {
	t.Helper()
	f := x.fx.f
	x.resets++
	var data domain.Bootstrap
	if err := json.Unmarshal(x.baseline, &data); err != nil {
		t.Fatal(err)
	}
	// A bounded run may have written; refresh the literal owner first.
	if err := f.repository.ReloadWorkspace(t.Context(), x.workspace); err != nil {
		t.Fatal(err)
	}
	restore := store.ForceFullMutationsForTesting(true)
	err := f.repository.MutateWorkspace(t.Context(), x.workspace, "scale_p21_sw530.reset", "", nil, func(next *domain.Bootstrap) error {
		*next = data
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repository.ReloadWorkspace(t.Context(), x.workspace); err != nil {
		t.Fatal(err)
	}
	for table, rows := range x.membership {
		if current := x.rows(t, `SELECT * FROM `+table+` WHERE workspace_id=?`, f.data.Workspace.ID); fmt.Sprint(current) == fmt.Sprint(rows) {
			continue
		}
		columns := x.columns(t, table)
		x.exec(t, `DELETE FROM `+table+` WHERE workspace_id=?`, f.data.Workspace.ID)
		for _, row := range rows {
			x.exec(t, `INSERT INTO `+table+`(`+strings.Join(columns, ",")+`) VALUES(`+strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")+`)`, row...)
		}
	}
	for _, row := range x.numbers {
		x.exec(t, `UPDATE issue_number_sequences SET last_number=? WHERE workspace_key=?`, row[0], x.workspace)
	}
	x.sessions(t)
	x.probeStale = true
}

func (x *sw530Sweep) columns(t *testing.T, table string) []string {
	t.Helper()
	rows := x.query(t, `SELECT * FROM `+table+` WHERE 1=?`, 0)
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	return columns
}

// run sends request to one source from the fixture state and records the
// outcome. dirty reports whether it changed restorable rows.
func (x *sw530Sweep) run(t *testing.T, source string, request sw530Request, started time.Time) (sw530Outcome, bool) {
	t.Helper()
	f := x.fx.f
	handler := x.legacy.handler
	if source != "legacy" {
		handler = x.bounded.handler
		if x.probeStale {
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), f.repository, x.workspace); err != nil {
				t.Fatal(err)
			}
			x.probeStale = false
		}
		if source == "reloaded" {
			scaleP21SW201ReloadSource(t, f.reader, f.probe)
		}
	}
	before := x.snapshot(t)
	x.mu.Lock()
	x.webhooks, x.realtime, x.skeletal = nil, nil, nil
	x.mu.Unlock()
	slots, views := store.ColdSlotAcquisitionsForTesting(), store.ColdAccessorReadsForTesting()
	w, timedOut := x.call(handler, request.method, request.path, request.body, request.principal, request.bearer)
	if source != "legacy" {
		slots, views = store.ColdSlotAcquisitionsForTesting()-slots, store.ColdAccessorReadsForTesting()-views
		x.coldSlots[request.pattern] += slots
		x.totalSlots += slots
		x.totalViews += views
	}
	// Publications and follow-up writes (an export's completion) may follow
	// the response on another goroutine; a COLD one first hydrates its
	// facade. Wait until the stored state stops changing.
	time.Sleep(time.Millisecond)
	after := x.snapshot(t)
	for range 50 {
		time.Sleep(25 * time.Millisecond)
		again := x.snapshot(t)
		if maps.Equal(after, again) {
			break
		}
		after = again
	}
	got := sw530Outcome{Status: w.Code, Header: map[string]string{}, TimedOut: timedOut}
	got.Body = sw530Normalize(w.Body.String(), started)
	for name, values := range w.Header() {
		if !sw530VolatileHeaders[name] && !strings.HasPrefix(name, "X-Ratelimit") {
			got.Header[name] = sw530Normalize(strings.Join(values, ", "), started)
		}
	}
	x.mu.Lock()
	if source != "legacy" {
		for _, item := range x.skeletal {
			if x.skeletalBy[request.pattern] == nil {
				x.skeletalBy[request.pattern] = map[string]bool{}
			}
			x.skeletalBy[request.pattern][item] = true
		}
	}
	for _, item := range x.webhooks {
		got.Webhooks = append(got.Webhooks, sw530Normalize(item, started))
	}
	for _, item := range x.realtime {
		got.Realtime = append(got.Realtime, sw530Normalize(item, started))
	}
	x.mu.Unlock()
	sort.Strings(got.Webhooks)
	sort.Strings(got.Realtime)
	var dirty bool
	got.Rows, got.Events, dirty = sw530Delta(before, after, started)
	if source == "legacy" && dirty {
		x.probeStale = true
	}
	return got, dirty
}

// outcomes runs request on each source in order, each from the fixture state.
func (x *sw530Sweep) outcomes(t *testing.T, request sw530Request, order []string) map[string]sw530Outcome {
	t.Helper()
	started := time.Now().UTC()
	result := map[string]sw530Outcome{}
	for _, source := range order {
		got, dirty := x.run(t, source, request, started)
		result[source] = got
		if dirty {
			x.reset(t)
		}
	}
	return result
}

func sw530Differences(want, got sw530Outcome) (kind, legacy, bounded string) {
	switch {
	case want.TimedOut || got.TimedOut:
		if want.TimedOut == got.TimedOut {
			return "", "", ""
		}
		return "timeout", fmt.Sprint(want.TimedOut), fmt.Sprint(got.TimedOut)
	case want.Status != got.Status:
		return "status", fmt.Sprintf("%d %s", want.Status, want.Body), fmt.Sprintf("%d %s", got.Status, got.Body)
	case want.Body != got.Body:
		return "body", want.Body, got.Body
	case !reflect.DeepEqual(want.Header, got.Header):
		return "header", fmt.Sprint(want.Header), fmt.Sprint(got.Header)
	case !slices.Equal(want.Rows, got.Rows):
		return "rows", strings.Join(want.Rows, "\n"), strings.Join(got.Rows, "\n")
	case !slices.Equal(want.Events, got.Events):
		return "events", strings.Join(want.Events, "\n"), strings.Join(got.Events, "\n")
	case !slices.Equal(want.Webhooks, got.Webhooks):
		return "webhooks", strings.Join(want.Webhooks, "\n"), strings.Join(got.Webhooks, "\n")
	case !slices.Equal(want.Realtime, got.Realtime):
		return "realtime", strings.Join(want.Realtime, "\n"), strings.Join(got.Realtime, "\n")
	}
	return "", "", ""
}

func (x *sw530Sweep) compare(t *testing.T, request sw530Request) ([]sw530Divergence, int) {
	t.Helper()
	got := x.outcomes(t, request, []string{"legacy", "warm", "reloaded"})
	var result []sw530Divergence
	for _, source := range []string{"warm", "reloaded"} {
		if kind, legacy, bounded := sw530Differences(got["legacy"], got[source]); kind != "" {
			result = append(result, sw530Divergence{Route: request.pattern, Request: request.label(), Source: source, Kind: kind, Legacy: legacy, Bounded: bounded})
		}
	}
	return result, got["legacy"].Status
}

// confirm re-runs a divergent request with the bounded source first.
func (x *sw530Sweep) confirm(t *testing.T, request sw530Request, source string) bool {
	t.Helper()
	got := x.outcomes(t, request, []string{source, "legacy"})
	kind, _, _ := sw530Differences(got["legacy"], got[source])
	return kind != ""
}

// sw530Caller lists the server functions on the stack below the store,
// innermost first, without closures, middleware and the harness.
func sw530Caller() string {
	pcs := make([]uintptr, 512)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)])
	// A tested main package is named by its import path.
	const prefix = "flow/api/cmd/server."
	var chain []string
	for {
		frame, more := frames.Next()
		name := strings.TrimPrefix(frame.Function, prefix)
		if name != frame.Function && !strings.Contains(name, "sw530") && !strings.Contains(name, ".func") && !slices.Contains(chain, name) {
			chain = append(chain, name)
		}
		if !more || len(chain) == 6 {
			break
		}
	}
	if len(chain) == 0 {
		return "?"
	}
	return strings.Join(chain, " < ")
}
