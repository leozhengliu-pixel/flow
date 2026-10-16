package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type platformFixture struct {
	repository *store.SQLiteStore
	service    *server
	host       *httptest.Server
	admin      *http.Client
	data       domain.Bootstrap
}

var platformKeyCount int

func newPlatformFixture(t *testing.T, limits *apiRateLimiter) platformFixture {
	t.Helper()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "platform.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	service := &server{store: repository, uploadPath: t.TempDir(), apiRateLimiter: limits}
	host := httptest.NewServer(newHandler(service))
	t.Cleanup(host.Close)
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	data := authRequest[domain.Bootstrap](t, admin, http.MethodGet, host.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	return platformFixture{repository: repository, service: service, host: host, admin: admin, data: data}
}

func (f platformFixture) apiKey(t *testing.T, scopes []string, teamIDs []string) string {
	t.Helper()
	platformKeyCount++
	input := map[string]any{"name": "Platform test " + strconv.Itoa(platformKeyCount), "scopes": scopes}
	if teamIDs != nil {
		input["teamIds"] = teamIDs
	}
	key := authRequest[struct {
		Secret string `json:"secret"`
	}](t, f.admin, http.MethodPost, f.host.URL+"/api/api-keys", input, "test-workspace", http.StatusCreated)
	return key.Secret
}

func bearer(t *testing.T, method, target, token string, body any, wantStatus int) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, target, reader)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if wantStatus != 0 && response.StatusCode != wantStatus {
		t.Fatalf("%s %s status %d, want %d: %s", method, target, response.StatusCode, wantStatus, raw)
	}
	return response, raw
}

func decodeInto[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return value
}

func TestPublicConnectionsRespectAPIKeyTeamsAndPaginate(t *testing.T) {
	f := newPlatformFixture(t, nil)
	if len(f.data.Teams) < 2 {
		t.Skip("fixture needs two teams")
	}
	team := f.data.Teams[0]
	token := f.apiKey(t, []string{"read"}, []string{team.ID})

	response, raw := bearer(t, http.MethodGet, f.host.URL+"/api/teams", token, nil, http.StatusOK)
	teams := decodeInto[connection[domain.Team]](t, raw)
	if len(teams.Nodes) != 1 || teams.Nodes[0].ID != team.ID || teams.PageInfo.HasNextPage {
		t.Fatalf("team-restricted key saw %#v", teams)
	}
	if response.Header.Get("X-RateLimit-Requests-Limit") != "2500" || response.Header.Get("X-Complexity") != "51" || response.Header.Get("X-RateLimit-Complexity-Limit") != "3000000" {
		t.Fatalf("rate limit headers = %v", response.Header)
	}
	_, raw = bearer(t, http.MethodGet, f.host.URL+"/api/workflow-states?first=250", token, nil, http.StatusOK)
	states := decodeInto[connection[domain.WorkflowState]](t, raw)
	if len(states.Nodes) == 0 || slices.ContainsFunc(states.Nodes, func(state domain.WorkflowState) bool { return state.TeamID != team.ID }) {
		t.Fatalf("workflow states leaked other teams: %#v", states.Nodes)
	}
	_, raw = bearer(t, http.MethodGet, f.host.URL+"/api/viewer", token, nil, http.StatusOK)
	viewer := decodeInto[viewerResponse](t, raw)
	if viewer.Authentication != "apiKey" || viewer.Workspace == nil || viewer.Workspace.URLKey != "test-workspace" || !slices.Equal(viewer.TeamIDs, []string{team.ID}) || viewer.User.Email != "admin@example.test" {
		t.Fatalf("viewer = %#v", viewer)
	}
	_, raw = bearer(t, http.MethodGet, f.host.URL+"/api/organization", token, nil, http.StatusOK)
	if organization := decodeInto[organizationResponse](t, raw); organization.ID != f.data.Workspace.ID || organization.TeamCount != 1 {
		t.Fatalf("organization = %#v", organization)
	}

	// Pagination through users is stable and reports both directions.
	_, raw = bearer(t, http.MethodGet, f.host.URL+"/api/users?first=1", token, nil, http.StatusOK)
	first := decodeInto[connection[domain.User]](t, raw)
	if len(first.Nodes) != 1 || !first.PageInfo.HasNextPage || first.PageInfo.HasPreviousPage || first.PageInfo.EndCursor == nil {
		t.Fatalf("first page = %#v", first)
	}
	_, raw = bearer(t, http.MethodGet, f.host.URL+"/api/users?first=1&after="+url.QueryEscape(*first.PageInfo.EndCursor), token, nil, http.StatusOK)
	second := decodeInto[connection[domain.User]](t, raw)
	if len(second.Nodes) != 1 || second.Nodes[0].ID == first.Nodes[0].ID || !second.PageInfo.HasPreviousPage {
		t.Fatalf("second page = %#v", second)
	}
	_, raw = bearer(t, http.MethodGet, f.host.URL+"/api/users?last=1&before="+url.QueryEscape(*second.PageInfo.StartCursor), token, nil, http.StatusOK)
	back := decodeInto[connection[domain.User]](t, raw)
	if len(back.Nodes) != 1 || back.Nodes[0].ID != first.Nodes[0].ID {
		t.Fatalf("backward page = %#v", back)
	}

	// Invalid arguments use the documented error envelope.
	_, raw = bearer(t, http.MethodGet, f.host.URL+"/api/teams?first=1&last=1", token, nil, http.StatusBadRequest)
	if envelope := decodeInto[map[string]any](t, raw); envelope["type"] != apiErrorInvalidInput || envelope["code"] != apiErrorInvalidInput || envelope["error"] == "" {
		t.Fatalf("error envelope = %s", raw)
	}
	_, raw = bearer(t, http.MethodGet, f.host.URL+`/api/teams?filter={"colour":{"eq":"x"}}`, token, nil, http.StatusBadRequest)
	if !strings.Contains(string(raw), "unknown field") {
		t.Fatalf("unknown filter field: %s", raw)
	}
	// Webhooks stay admin-only; a read key without the admin scope is refused.
	_, raw = bearer(t, http.MethodGet, f.host.URL+"/api/webhooks?first=5", token, nil, 0)
	if envelope := decodeInto[map[string]any](t, raw); envelope["type"] != apiErrorAuthentication && envelope["type"] != apiErrorForbidden {
		t.Fatalf("webhooks with read key = %s", raw)
	}

	// The first-party session sees every team and is not rate limited.
	request, _ := http.NewRequest(http.MethodGet, f.host.URL+`/api/teams?filter={"key":{"eqIgnoreCase":"`+strings.ToLower(f.data.Teams[1].Key)+`"}}`, nil)
	request.Header.Set("X-Workspace-Key", "test-workspace")
	sessionResponse, err := f.admin.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	sessionRaw, _ := io.ReadAll(sessionResponse.Body)
	sessionResponse.Body.Close()
	filtered := decodeInto[connection[domain.Team]](t, sessionRaw)
	if len(filtered.Nodes) != 1 || filtered.Nodes[0].ID != f.data.Teams[1].ID || sessionResponse.Header.Get("X-RateLimit-Requests-Limit") != "" {
		t.Fatalf("session filter = %s headers %v", sessionRaw, sessionResponse.Header)
	}
	// Legacy list routes keep their shape without pagination arguments.
	legacy := authRequest[[]domain.Webhook](t, f.admin, http.MethodGet, f.host.URL+"/api/webhooks", nil, "test-workspace", http.StatusOK)
	_ = legacy
	paged := authRequest[connection[domain.Webhook]](t, f.admin, http.MethodGet, f.host.URL+"/api/webhooks?first=10", nil, "test-workspace", http.StatusOK)
	if paged.Nodes == nil {
		t.Fatal("webhook connection missing nodes")
	}
	projects := authRequest[connection[domain.Project]](t, f.admin, http.MethodGet, f.host.URL+"/api/projects?first=2&orderBy=updatedAt", nil, "test-workspace", http.StatusOK)
	if len(projects.Nodes) > 2 || len(projects.Nodes) > 1 && projects.Nodes[0].UpdatedAt.Before(projects.Nodes[1].UpdatedAt) {
		t.Fatalf("projects connection = %#v", projects.PageInfo)
	}
}

func TestOutsiderCannotListWorkspaceResources(t *testing.T) {
	f := newPlatformFixture(t, nil)
	outsider, _ := verifiedAuthClient(t, f.host.URL, "Outsider", "outsider-platform@example.test")
	for _, path := range []string{"/api/teams", "/api/users", "/api/customers", "/api/organization"} {
		request, _ := http.NewRequest(http.MethodGet, f.host.URL+path, nil)
		request.Header.Set("X-Workspace-Key", "test-workspace")
		response, err := outsider.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s for outsider = %d %s", path, response.StatusCode, raw)
		}
	}
}

func TestAPIRateLimitBucketsHeadersAnd429(t *testing.T) {
	config := defaultAPIRateLimitConfig()
	config.Budgets[apiRateAPIKey] = apiRateBudget{Requests: 3, Complexity: 120}
	config.Budgets[apiRateUnauthenticated] = apiRateBudget{Requests: 2, Complexity: 1000}
	limiter := newAPIRateLimiter(config, nil)
	f := newPlatformFixture(t, limiter)
	first := f.apiKey(t, []string{"read"}, nil)
	second := f.apiKey(t, []string{"read"}, nil)

	// Two keys of the same user share one request budget (the reference app: per user).
	response, _ := bearer(t, http.MethodGet, f.host.URL+"/api/viewer", first, nil, http.StatusOK)
	if response.Header.Get("X-RateLimit-Requests-Remaining") != "2" {
		t.Fatalf("remaining after one = %q", response.Header.Get("X-RateLimit-Requests-Remaining"))
	}
	reset, err := strconv.ParseInt(response.Header.Get("X-RateLimit-Requests-Reset"), 10, 64)
	if err != nil || reset <= time.Now().UnixMilli() {
		t.Fatalf("reset = %q", response.Header.Get("X-RateLimit-Requests-Reset"))
	}
	bearer(t, http.MethodGet, f.host.URL+"/api/viewer", second, nil, http.StatusOK)
	// Complexity: first=100 costs 101 points, more than the 118 left after two
	// 1-point requests allows twice.
	response, raw := bearer(t, http.MethodGet, f.host.URL+"/api/teams?first=100", second, nil, 0)
	if response.StatusCode != http.StatusOK || response.Header.Get("X-Complexity") != "101" {
		t.Fatalf("complex request = %d %v %s", response.StatusCode, response.Header, raw)
	}
	response, raw = bearer(t, http.MethodGet, f.host.URL+"/api/viewer", first, nil, http.StatusTooManyRequests)
	envelope := decodeInto[map[string]any](t, raw)
	if envelope["code"] != apiErrorRateLimited || envelope["type"] != apiErrorRateLimited || response.Header.Get("Retry-After") == "" || response.Header.Get("X-RateLimit-Requests-Remaining") != "0" {
		t.Fatalf("429 = %v %s", response.Header, raw)
	}

	// Unauthenticated OAuth calls are limited per client address.
	for attempt := 0; attempt < 2; attempt++ {
		response, _ := bearer(t, http.MethodPost, f.host.URL+"/oauth/revoke", "", map[string]string{}, 0)
		if response.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("attempt %d limited early", attempt)
		}
	}
	response, _ = bearer(t, http.MethodPost, f.host.URL+"/oauth/revoke", "", map[string]string{}, http.StatusTooManyRequests)
	if response.Header.Get("X-RateLimit-Requests-Limit") != "2" {
		t.Fatalf("unauthenticated headers = %v", response.Header)
	}
}

func TestFailedAuthenticationSpendsAddressBudget(t *testing.T) {
	config := defaultAPIRateLimitConfig()
	config.Budgets[apiRateUnauthenticated] = apiRateBudget{Requests: 2, Complexity: 1000}
	f := newPlatformFixture(t, newAPIRateLimiter(config, nil))
	for attempt := 0; attempt < 2; attempt++ {
		_, raw := bearer(t, http.MethodGet, f.host.URL+"/api/viewer", "flow_api_invalid", nil, http.StatusUnauthorized)
		if envelope := decodeInto[map[string]any](t, raw); envelope["type"] != apiErrorAuthentication {
			t.Fatalf("401 envelope = %s", raw)
		}
	}
	bearer(t, http.MethodGet, f.host.URL+"/api/viewer", "flow_api_invalid", nil, http.StatusTooManyRequests)
}

func TestMemoryTokenBucketRefillsContinuously(t *testing.T) {
	buckets := newMemoryTokenBuckets()
	now := time.Unix(1_700_000_000, 0)
	for index := 0; index < 3; index++ {
		if allowed, _, _ := buckets.TakeTokens(context.Background(), "k", 3, time.Hour, 1, now); !allowed {
			t.Fatalf("take %d refused", index)
		}
	}
	if allowed, remaining, _ := buckets.TakeTokens(context.Background(), "k", 3, time.Hour, 1, now); allowed || remaining != 0 {
		t.Fatal("empty bucket allowed a request")
	}
	if allowed, _, _ := buckets.TakeTokens(context.Background(), "k", 3, time.Hour, 1, now.Add(20*time.Minute)); !allowed {
		t.Fatal("bucket did not refill at limit/period")
	}
}

func TestAPIErrorEnvelopeNormalizesErrors(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "errors.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/route-that-does-not-exist", nil))
	body := decodeInto[map[string]any](t, recorder.Body.Bytes())
	if recorder.Code != http.StatusNotFound || body["type"] != apiErrorNotFound || body["code"] != apiErrorNotFound || body["requestId"] == "" || !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("404 = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPatch, "/api/issues/missing-issue", strings.NewReader(`{"title":"x"}`)))
	if body := decodeInto[map[string]any](t, recorder.Body.Bytes()); recorder.Code < 400 || body["type"] == nil || body["error"] == nil {
		t.Fatalf("handler error = %d %s", recorder.Code, recorder.Body.String())
	}
	conflict := normalizeAPIErrorBody(http.StatusConflict, http.Header{"Content-Type": {"application/json"}}, []byte(`{"error":"changed","code":"VERSION_CONFLICT","current":{"id":"x"}}`), httptest.NewRequest(http.MethodGet, "/api/x", nil))
	if body := decodeInto[map[string]any](t, conflict); body["code"] != "VERSION_CONFLICT" || body["type"] != apiErrorConflict || body["current"] == nil {
		t.Fatalf("conflict = %s", conflict)
	}
	internal := normalizeAPIErrorBody(http.StatusInternalServerError, http.Header{}, []byte("boom\n"), httptest.NewRequest(http.MethodGet, "/api/x", nil))
	if body := decodeInto[map[string]any](t, internal); body["type"] != apiErrorInternal || body["error"] != "boom" {
		t.Fatalf("internal = %s", internal)
	}
	// OAuth keeps RFC 6749 errors.
	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader("grant_type=bogus"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(recorder, request)
	if body := decodeInto[map[string]any](t, recorder.Body.Bytes()); body["type"] != nil {
		t.Fatalf("oauth error was rewritten: %s", recorder.Body.String())
	}
}

type pagedItem struct {
	ID string
	At time.Time
}

func TestPaginateConnectionCursorsAreStable(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	items := []pagedItem{{"a", base}, {"b", base}, {"c", base.Add(time.Minute)}, {"d", base.Add(2 * time.Minute)}, {"e", base.Add(2 * time.Minute)}}
	key := func(item pagedItem) pageKey { return pageKey{At: item.At, ID: item.ID} }
	ids := func(page connection[pagedItem]) string {
		parts := []string{}
		for _, node := range page.Nodes {
			parts = append(parts, node.ID)
		}
		return strings.Join(parts, "")
	}
	args := pageArgs{Size: 2, OrderBy: "createdAt"}
	page := paginateConnection(items, key, args)
	if ids(page) != "ed" || !page.PageInfo.HasNextPage || page.PageInfo.HasPreviousPage {
		t.Fatalf("first page = %s %#v", ids(page), page.PageInfo)
	}
	cursor, err := decodePageCursor(*page.PageInfo.EndCursor, "createdAt")
	if err != nil {
		t.Fatal(err)
	}
	// A newer item created between requests does not shift the next page.
	items = append(items, pagedItem{"z", base.Add(time.Hour)})
	page = paginateConnection(items, key, pageArgs{Size: 2, OrderBy: "createdAt", After: cursor})
	if ids(page) != "cb" || !page.PageInfo.HasNextPage || !page.PageInfo.HasPreviousPage {
		t.Fatalf("second page = %s %#v", ids(page), page.PageInfo)
	}
	cursor, _ = decodePageCursor(*page.PageInfo.EndCursor, "createdAt")
	page = paginateConnection(items, key, pageArgs{Size: 2, OrderBy: "createdAt", After: cursor})
	if ids(page) != "a" || page.PageInfo.HasNextPage {
		t.Fatalf("last page = %s %#v", ids(page), page.PageInfo)
	}
	before, _ := decodePageCursor(*page.PageInfo.StartCursor, "createdAt")
	page = paginateConnection(items, key, pageArgs{Size: 2, OrderBy: "createdAt", Before: before, Backward: true})
	if ids(page) != "cb" || !page.PageInfo.HasPreviousPage || !page.PageInfo.HasNextPage {
		t.Fatalf("backward page = %s %#v", ids(page), page.PageInfo)
	}
	if _, err := decodePageCursor(*page.PageInfo.EndCursor, "updatedAt"); err == nil {
		t.Fatal("cursor accepted for a different ordering")
	}
	if _, err := decodePageCursor("not-a-cursor", "createdAt"); err == nil {
		t.Fatal("malformed cursor accepted")
	}
	request := httptest.NewRequest(http.MethodGet, "/api/teams?first=0", nil)
	if _, err := parsePageArgs(request); err == nil {
		t.Fatal("first=0 accepted")
	}
	request = httptest.NewRequest(http.MethodGet, "/api/teams?first=251", nil)
	if _, err := parsePageArgs(request); err == nil {
		t.Fatal("first above the maximum accepted")
	}
}

func TestResourceFilterComparators(t *testing.T) {
	now := time.Now()
	type row struct {
		Name    string
		Created time.Time
		Team    string
		Labels  []string
		Count   int
	}
	rows := []row{
		{Name: "Bug in login", Created: now.Add(-24 * time.Hour), Team: "ENG", Labels: []string{"bug", "auth"}, Count: 3},
		{Name: "Feature", Created: now.Add(-30 * 24 * time.Hour), Team: "DES", Labels: []string{"feature"}, Count: 10},
		{Name: "bug report", Created: now.Add(-time.Hour), Team: "ENG", Count: 0},
	}
	fields := map[string]func(row) any{
		"name":      func(r row) any { return r.Name },
		"createdAt": func(r row) any { return r.Created },
		"team":      func(r row) any { return filterRelation(r.Team, map[string]any{"key": r.Team}) },
		"labels":    func(r row) any { return filterList(r.Labels) },
		"count":     func(r row) any { return r.Count },
	}
	cases := map[string]int{
		`{"name":{"containsIgnoreCase":"bug"}}`:                       2,
		`{"name":{"startsWith":"Bug"}}`:                               1,
		`{"createdAt":{"gt":"-P2W"}}`:                                 2,
		`{"createdAt":{"lt":"-P1W"}}`:                                 1,
		`{"team":{"key":{"eq":"ENG"}}}`:                               2,
		`{"team":{"id":{"in":["DES"]}}}`:                              1,
		`{"labels":{"eq":"bug"}}`:                                     1,
		`{"labels":{"null":true}}`:                                    1,
		`{"count":{"gte":3,"lt":10}}`:                                 1,
		`{"or":[{"count":{"eq":0}},{"name":{"eq":"Feature"}}]}`:       2,
		`{"and":[{"team":{"key":{"neq":"DES"}}},{"count":{"gt":1}}]}`: 1,
		`{"name":{"notContainsIgnoreCase":"bug"}}`:                    1,
	}
	for raw, want := range cases {
		got, err := applyResourceFilter(slices.Clone(rows), raw, typedFields(fields))
		if err != nil || len(got) != want {
			t.Errorf("%s matched %d (%v), want %d", raw, len(got), err, want)
		}
	}
	for _, raw := range []string{`{"nope":{"eq":1}}`, `{"name":{"like":"x"}}`, `[1]`, `{"or":{"name":{"eq":"x"}}}`} {
		if _, err := applyResourceFilter(slices.Clone(rows), raw, typedFields(fields)); err == nil {
			t.Errorf("%s accepted", raw)
		}
	}
	if at, ok := parseFilterTime("-P1DT2H", now); !ok || at.After(now.Add(-25*time.Hour)) {
		t.Fatalf("duration = %v %v", at, ok)
	}
}

func TestFileUploadSignedURLsAndAssetAccess(t *testing.T) {
	f := newPlatformFixture(t, nil)
	contents := []byte("integration notes")
	prepared := authRequest[fileUploadPayload](t, f.admin, http.MethodPost, f.host.URL+"/api/files", map[string]any{"filename": "notes.txt", "contentType": "text/plain", "size": len(contents)}, "test-workspace", http.StatusOK)
	if !strings.Contains(prepared.UploadURL, "/api/files/uploads/") || !strings.Contains(prepared.AssetURL, "/uploads/asset_") || len(prepared.Headers) != 1 || prepared.Headers[0].Key != "Content-Type" {
		t.Fatalf("prepared = %#v", prepared)
	}
	put := func(target string, body []byte) int {
		request, _ := http.NewRequest(http.MethodPut, target, bytes.NewReader(body))
		request.Header.Set("Content-Type", "text/plain")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	if code := put(prepared.UploadURL, []byte("wrong size")); code != http.StatusBadRequest {
		t.Fatalf("size mismatch = %d", code)
	}
	if code := put(prepared.UploadURL, contents); code != http.StatusNoContent {
		t.Fatalf("upload = %d", code)
	}
	if code := put(prepared.UploadURL, contents); code != http.StatusConflict {
		t.Fatalf("replayed upload = %d", code)
	}
	download := func(client *http.Client, target string) (int, string) {
		response, err := client.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(raw)
	}
	if code, body := download(f.admin, prepared.AssetURL); code != http.StatusOK || body != string(contents) {
		t.Fatalf("uploader download = %d %q", code, body)
	}
	member, memberUser := verifiedAuthClient(t, f.host.URL, "Asset member", "asset-member@example.test")
	if err := f.repository.EnsureWorkspaceMembership(context.Background(), f.data.Workspace.ID, memberUser.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := download(member, prepared.AssetURL); code != http.StatusOK {
		t.Fatalf("workspace member download = %d", code)
	}
	outsider, _ := verifiedAuthClient(t, f.host.URL, "Asset outsider", "asset-outsider@example.test")
	if code, _ := download(outsider, prepared.AssetURL); code != http.StatusNotFound {
		t.Fatalf("outsider download = %d", code)
	}
	if code, _ := download(http.DefaultClient, prepared.AssetURL); code != http.StatusUnauthorized {
		t.Fatalf("anonymous download = %d", code)
	}

	// Signed URLs work without credentials until they expire.
	signed := authRequest[struct {
		Files []signedFile `json:"files"`
	}](t, f.admin, http.MethodGet, f.host.URL+"/api/files/sign?expiresIn=60&url="+url.QueryEscape(prepared.AssetURL), nil, "test-workspace", http.StatusOK)
	if len(signed.Files) != 1 || signed.Files[0].ExpiresAt.Before(time.Now()) {
		t.Fatalf("signed = %#v", signed)
	}
	if code, body := download(http.DefaultClient, signed.Files[0].SignedURL); code != http.StatusOK || body != string(contents) {
		t.Fatalf("signed download = %d %q", code, body)
	}
	tampered := strings.Replace(signed.Files[0].SignedURL, "signature=", "signature=0", 1)
	if code, _ := download(http.DefaultClient, tampered); code != http.StatusForbidden {
		t.Fatalf("tampered signature = %d", code)
	}
	key, err := f.service.fileSigningKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	name := strings.TrimPrefix(prepared.AssetURL, f.host.URL+"/uploads/")
	expired := signedFileURL(key, f.host.URL, name, time.Now().Add(-time.Minute))
	if code, _ := download(http.DefaultClient, expired); code != http.StatusForbidden {
		t.Fatalf("expired signature = %d", code)
	}
	if _, raw := bearer(t, http.MethodGet, f.host.URL+"/api/files/sign?url="+url.QueryEscape(prepared.AssetURL), "", nil, http.StatusUnauthorized); !strings.Contains(string(raw), apiErrorAuthentication) {
		t.Fatalf("anonymous sign = %s", raw)
	}

	// Expired upload tokens are rejected and cleaned up.
	late := authRequest[fileUploadPayload](t, f.admin, http.MethodPost, f.host.URL+"/api/files", map[string]any{"filename": "late.txt", "contentType": "text/plain", "size": 4}, "test-workspace", http.StatusOK)
	restore := fileUploadNow
	fileUploadNow = func() time.Time { return time.Now().UTC().Add(fileUploadTTL + time.Minute) }
	code := put(late.UploadURL, []byte("late"))
	f.service.cleanupFileUploads(context.Background())
	fileUploadNow = restore
	if code != http.StatusNotFound {
		t.Fatalf("expired token upload = %d", code)
	}
	if code := put(late.UploadURL, []byte("late")); code != http.StatusNotFound {
		t.Fatalf("cleaned-up token upload = %d", code)
	}
}

func TestSignedFileURLHeaderRewritesResponses(t *testing.T) {
	key := []byte("test-key")
	expires := time.Unix(1_900_000_000, 0)
	body := []byte(`{"url":"/uploads/attachment_1_report v2.pdf","description":"see ![x](/uploads/asset_abc.png) and https://flow.test/uploads/asset_def.txt","upload":"https://flow.test/api/files/uploads/upload_x"}`)
	rewritten := string(rewriteUploadURLs(body, key, "https://flow.test", expires))
	var decoded map[string]string
	if err := json.Unmarshal([]byte(rewritten), &decoded); err != nil {
		t.Fatalf("rewritten JSON invalid: %v", err)
	}
	for _, name := range []string{"attachment_1_report v2.pdf", "asset_abc.png", "asset_def.txt"} {
		if !strings.Contains(decoded["url"]+decoded["description"], signedFileURL(key, "https://flow.test", name, expires)) {
			t.Fatalf("%s not signed in %s", name, rewritten)
		}
	}
	if !strings.Contains(rewritten, `"https://flow.test/api/files/uploads/upload_x"`) {
		t.Fatalf("upload route rewritten: %s", rewritten)
	}
}

func TestSignedFileURLsEscapeUntrustedOriginInJSON(t *testing.T) {
	body := []byte(`{"url":"/uploads/a.png","description":"see /uploads/b.png"}`)
	rewritten := rewriteUploadURLs(body, []byte("key"), `https://flow.test/"<script>alert(1)</script>`, time.Unix(1900000000, 0))
	var decoded map[string]string
	if err := json.Unmarshal(rewritten, &decoded); err != nil {
		t.Fatalf("origin broke JSON encoding: %v", err)
	}
	if bytes.ContainsAny(rewritten, "<>") || !strings.Contains(decoded["url"], "signature=") || !strings.Contains(decoded["description"], "signature=") {
		t.Fatalf("unsafe signed URL response: %s", rewritten)
	}
}

func TestLinkAttachmentUpsertLookupAndSignedHeader(t *testing.T) {
	f := newPlatformFixture(t, nil)
	issue := f.data.Issues[0]
	base := f.host.URL
	created := authRequest[domain.Attachment](t, f.admin, http.MethodPost, base+"/api/issues/"+issue.ID+"/link-attachments", map[string]any{"url": "https://support.example.com/tickets/7", "title": "Ticket 7", "subtitle": "Open", "iconUrl": "https://support.example.com/icon.png", "metadata": map[string]any{"priority": "high"}}, "test-workspace", http.StatusCreated)
	if created.Subtitle != "Open" || created.IconURL == "" || created.Metadata["priority"] != "high" || created.UpdatedAt == nil {
		t.Fatalf("created = %#v", created)
	}
	updated := authRequest[domain.Attachment](t, f.admin, http.MethodPost, base+"/api/issues/"+issue.ID+"/link-attachments", map[string]any{"url": "https://support.example.com/tickets/7", "subtitle": "Solved"}, "test-workspace", http.StatusOK)
	if updated.ID != created.ID || updated.Title != "Ticket 7" || updated.Subtitle != "Solved" {
		t.Fatalf("upsert did not update by URL: %#v", updated)
	}
	authRequest[map[string]any](t, f.admin, http.MethodPost, base+"/api/issues/"+issue.ID+"/link-attachments", map[string]any{"url": "javascript:alert(1)"}, "test-workspace", http.StatusBadRequest)
	patched := authRequest[domain.Attachment](t, f.admin, http.MethodPatch, base+"/api/issues/"+issue.ID+"/attachments/"+created.ID, map[string]any{"title": "Ticket #7"}, "test-workspace", http.StatusOK)
	if patched.Title != "Ticket #7" || patched.Subtitle != "Solved" {
		t.Fatalf("patched = %#v", patched)
	}
	byIssue := authRequest[connection[domain.Attachment]](t, f.admin, http.MethodGet, base+"/api/attachments?issueId="+issue.ID, nil, "test-workspace", http.StatusOK)
	if !slices.ContainsFunc(byIssue.Nodes, func(a domain.Attachment) bool { return a.ID == created.ID }) {
		t.Fatalf("by issue = %#v", byIssue)
	}
	byURL := authRequest[connection[domain.Attachment]](t, f.admin, http.MethodGet, base+"/api/attachments?url="+url.QueryEscape("https://support.example.com/tickets/7"), nil, "test-workspace", http.StatusOK)
	if len(byURL.Nodes) != 1 || byURL.Nodes[0].ID != created.ID {
		t.Fatalf("by url = %#v", byURL)
	}
	one := authRequest[domain.Attachment](t, f.admin, http.MethodGet, base+"/api/attachments/"+created.ID, nil, "test-workspace", http.StatusOK)
	if one.Title != "Ticket #7" {
		t.Fatalf("get = %#v", one)
	}
	authRequest[map[string]any](t, f.admin, http.MethodGet, base+"/api/attachments/attachment_missing", nil, "test-workspace", http.StatusNotFound)

	// A file attachment comes back with a signed URL when asked to.
	file := uploadAuthAttachment(t, f.admin, base, issue.ID, "test-workspace", http.StatusCreated)
	request, _ := http.NewRequest(http.MethodGet, base+"/api/attachments/"+file.ID, nil)
	request.Header.Set("X-Workspace-Key", "test-workspace")
	request.Header.Set("Public-File-Urls-Expire-In", "120")
	response, err := f.admin.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	signed := decodeInto[domain.Attachment](t, raw)
	if !strings.Contains(signed.URL, "/api/files/signed/") {
		t.Fatalf("attachment url not signed: %s", raw)
	}
	anonymous, err := http.Get(signed.URL)
	if err != nil {
		t.Fatal(err)
	}
	anonymous.Body.Close()
	if anonymous.StatusCode != http.StatusOK {
		t.Fatalf("signed attachment download = %d", anonymous.StatusCode)
	}
}

func TestCommentsConnectionPagesIndexedComments(t *testing.T) {
	f := newPlatformFixture(t, nil)
	issue := f.data.Issues[0]
	for _, body := range []string{"first", "second", "third"} {
		authRequest[domain.Comment](t, f.admin, http.MethodPost, f.host.URL+"/api/issues/"+issue.ID+"/comments", map[string]any{"body": body}, "test-workspace", http.StatusCreated)
		time.Sleep(2 * time.Millisecond)
	}
	page := authRequest[connection[domain.Comment]](t, f.admin, http.MethodGet, f.host.URL+"/api/comments?first=2&issueId="+issue.ID, nil, "test-workspace", http.StatusOK)
	if len(page.Nodes) != 2 || page.Nodes[0].Body != "third" || !page.PageInfo.HasNextPage || page.PageInfo.HasPreviousPage {
		t.Fatalf("first comments page = %#v", page)
	}
	next := authRequest[connection[domain.Comment]](t, f.admin, http.MethodGet, f.host.URL+"/api/comments?first=2&issueId="+issue.ID+"&after="+url.QueryEscape(*page.PageInfo.EndCursor), nil, "test-workspace", http.StatusOK)
	if !slices.ContainsFunc(next.Nodes, func(c domain.Comment) bool { return c.Body == "first" }) || !next.PageInfo.HasPreviousPage || slices.ContainsFunc(next.Nodes, func(c domain.Comment) bool { return c.Body == "third" || c.Body == "second" }) {
		t.Fatalf("next comments page = %#v", next)
	}
	last := authRequest[connection[domain.Comment]](t, f.admin, http.MethodGet, f.host.URL+"/api/comments?last=1&issueId="+issue.ID, nil, "test-workspace", http.StatusOK)
	if len(last.Nodes) != 1 || !last.PageInfo.HasPreviousPage || last.PageInfo.HasNextPage {
		t.Fatalf("last comment page = %#v", last)
	}
	authRequest[map[string]any](t, f.admin, http.MethodGet, f.host.URL+"/api/comments", nil, "test-workspace", http.StatusBadRequest)
	authRequest[map[string]any](t, f.admin, http.MethodGet, f.host.URL+"/api/comments?issueId=missing-issue", nil, "test-workspace", http.StatusNotFound)
}

func TestCustomerUpsertExternalIDsAndNeeds(t *testing.T) {
	f := newPlatformFixture(t, nil)
	base := f.host.URL
	created := authRequest[domain.Customer](t, f.admin, http.MethodPut, base+"/api/customers", map[string]any{"name": "Acme", "externalId": "crm-1", "domains": []string{"acme.com"}}, "test-workspace", http.StatusCreated)
	again := authRequest[domain.Customer](t, f.admin, http.MethodPut, base+"/api/customers", map[string]any{"externalId": "crm-1", "domains": []string{"acme.io"}, "size": 50}, "test-workspace", http.StatusOK)
	if again.ID != created.ID || !slices.Equal(again.Domains, []string{"acme.com", "acme.io"}) || again.Size != 50 || again.Name != "Acme" {
		t.Fatalf("upsert by external id = %#v", again)
	}
	byDomain := authRequest[domain.Customer](t, f.admin, http.MethodPut, base+"/api/customers", map[string]any{"domains": []string{"ACME.io"}, "externalIds": []string{"zendesk-9"}}, "test-workspace", http.StatusOK)
	if byDomain.ID != created.ID || !slices.Equal(byDomain.ExternalIDs, []string{"crm-1", "zendesk-9"}) {
		t.Fatalf("upsert by domain = %#v", byDomain)
	}
	authRequest[map[string]any](t, f.admin, http.MethodPut, base+"/api/customers", map[string]any{"externalId": "new-without-name"}, "test-workspace", http.StatusBadRequest)
	authRequest[map[string]any](t, f.admin, http.MethodPost, base+"/api/customers", map[string]any{"name": "Duplicate", "externalIds": []string{"crm-1"}}, "test-workspace", http.StatusBadRequest)

	filtered := authRequest[connection[domain.Customer]](t, f.admin, http.MethodGet, base+`/api/customers?filter=`+url.QueryEscape(`{"externalIds":{"eq":"zendesk-9"}}`), nil, "test-workspace", http.StatusOK)
	if len(filtered.Nodes) != 1 || filtered.Nodes[0].ID != created.ID {
		t.Fatalf("customer filter = %#v", filtered)
	}
	issue := f.data.Issues[0]
	need := authRequest[domain.CustomerRequest](t, f.admin, http.MethodPost, base+"/api/customer-requests", map[string]any{"customerExternalId": "crm-1", "body": "Needs SSO", "issueId": issue.ID, "attachmentUrl": "https://support.example.com/conversations/5"}, "test-workspace", http.StatusCreated)
	if need.CustomerID != created.ID || need.SourceURL != "https://support.example.com/conversations/5" {
		t.Fatalf("need = %#v", need)
	}
	attachments := authRequest[connection[domain.Attachment]](t, f.admin, http.MethodGet, base+"/api/attachments?issueId="+issue.ID+"&url="+url.QueryEscape("https://support.example.com/conversations/5"), nil, "test-workspace", http.StatusOK)
	if len(attachments.Nodes) != 1 {
		t.Fatalf("need attachment = %#v", attachments)
	}
	authRequest[map[string]any](t, f.admin, http.MethodPost, base+"/api/customer-requests", map[string]any{"customerExternalId": "unknown", "body": "x"}, "test-workspace", http.StatusNotFound)
	needs := authRequest[connection[domain.CustomerRequest]](t, f.admin, http.MethodGet, base+"/api/customer-requests?customerId="+created.ID, nil, "test-workspace", http.StatusOK)
	if len(needs.Nodes) != 1 || needs.Nodes[0].ID != need.ID {
		t.Fatalf("needs = %#v", needs)
	}
}

func TestAuditLogFiltersAndPagination(t *testing.T) {
	f := newPlatformFixture(t, nil)
	if err := f.repository.MutateWorkspace(context.Background(), "test-workspace", "test.audit", "", nil, func(data *domain.Bootstrap) error {
		data.AuditLog = nil
		for index := 0; index < 5; index++ {
			appendAudit(data, "created", "customer", "customer_"+strconv.Itoa(index), map[string]any{"ip": "10.0.0." + strconv.Itoa(index%2)})
			time.Sleep(time.Millisecond)
		}
		appendAudit(data, "deleted", "team", "team_x", nil)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	base := f.host.URL + "/api/workspace/audit-log"
	all := authRequest[[]domain.AuditLogEntry](t, f.admin, http.MethodGet, base, nil, "test-workspace", http.StatusOK)
	if len(all) < 6 {
		t.Fatalf("audit log = %d entries", len(all))
	}
	typed := authRequest[[]domain.AuditLogEntry](t, f.admin, http.MethodGet, base+"?type=customer.created&ip=10.0.0.1", nil, "test-workspace", http.StatusOK)
	if len(typed) != 2 {
		t.Fatalf("type+ip filter = %d", len(typed))
	}
	page := authRequest[connection[domain.AuditLogEntry]](t, f.admin, http.MethodGet, base+"?type=customer.created&first=2", nil, "test-workspace", http.StatusOK)
	if len(page.Nodes) != 2 || !page.PageInfo.HasNextPage || page.Nodes[0].ResourceID != "customer_4" {
		t.Fatalf("audit page = %#v", page)
	}
	rest := authRequest[connection[domain.AuditLogEntry]](t, f.admin, http.MethodGet, base+"?type=customer.created&first=10&after="+url.QueryEscape(*page.PageInfo.EndCursor), nil, "test-workspace", http.StatusOK)
	if len(rest.Nodes) != 3 || rest.PageInfo.HasNextPage {
		t.Fatalf("audit rest = %#v", rest)
	}
	since := authRequest[[]domain.AuditLogEntry](t, f.admin, http.MethodGet, base+"?since=-PT1H&actorEmail=ADMIN@example.test", nil, "test-workspace", http.StatusOK)
	if len(since) == 0 {
		t.Fatal("since/actorEmail filter returned nothing")
	}
	authRequest[map[string]any](t, f.admin, http.MethodGet, base+"?since=yesterday-ish", nil, "test-workspace", http.StatusBadRequest)
	types := authRequest[[]auditEntryTypeCount](t, f.admin, http.MethodGet, base+"/types", nil, "test-workspace", http.StatusOK)
	if !slices.ContainsFunc(types, func(item auditEntryTypeCount) bool { return item.Type == "customer.created" && item.Count == 5 }) {
		t.Fatalf("types = %#v", types)
	}
}
