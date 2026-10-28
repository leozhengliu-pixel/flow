package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"

	"github.com/go-sql-driver/mysql"
)

// lockedBuffer collects log output written from handler goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureServerLogs sends slog output to a buffer for the rest of the test.
func captureServerLogs(t *testing.T) *lockedBuffer {
	t.Helper()
	logs := &lockedBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return logs
}

// mysqlDataTooLong is the error a strict MySQL raises for an over-long value,
// wrapped the way the store wraps driver errors.
func mysqlDataTooLong() error {
	driverErr := &mysql.MySQLError{Number: 1406, SQLState: [5]byte{'2', '2', '0', '0', '1'}, Message: "Data too long for column 'previous_values' at row 1"}
	return fmt.Errorf("insert domain event: %w", driverErr)
}

func decodeErrorBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body is not JSON: %s", recorder.Body.String())
	}
	return body
}

func TestInternalErrorResponseHidesDatabaseText(t *testing.T) {
	logs := captureServerLogs(t)
	cases := map[string]http.HandlerFunc{
		"respondMutation": func(w http.ResponseWriter, r *http.Request) {
			respondMutation(w, mysqlDataTooLong(), http.StatusOK, nil)
		},
		"writeError 500": func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusInternalServerError, mysqlDataTooLong().Error())
		},
		"http.Error": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, mysqlDataTooLong().Error(), http.StatusInternalServerError)
		},
		"extra fields": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "boom", "details": mysqlDataTooLong().Error()})
		},
		// A database error behind a handler that would answer 400 or 502.
		"writeErrorFor 400": func(w http.ResponseWriter, r *http.Request) {
			writeErrorFor(w, r, http.StatusBadRequest, mysqlDataTooLong())
		},
		"writeErrorFor 502": func(w http.ResponseWriter, r *http.Request) {
			writeErrorFor(w, r, http.StatusBadGateway, mysqlDataTooLong())
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			chain := requestLog(apiErrorEnvelope(handler))
			recorder := httptest.NewRecorder()
			chain.ServeHTTP(recorder, httptest.NewRequest(http.MethodPatch, "/api/documents/doc_1", nil))
			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", recorder.Code)
			}
			raw := recorder.Body.String()
			for _, leaked := range []string{"1406", "previous_values", "Data too long", "22001", "insert domain event", "details"} {
				if strings.Contains(raw, leaked) {
					t.Fatalf("response leaks %q: %s", leaked, raw)
				}
			}
			body := decodeErrorBody(t, recorder)
			requestID := recorder.Header().Get("X-Request-ID")
			if body["error"] != internalErrorMessage || body["code"] != apiErrorInternal || body["type"] != apiErrorInternal || requestID == "" || body["requestId"] != requestID {
				t.Fatalf("body = %s (request id %q)", raw, requestID)
			}
			logged := logs.String()
			if !strings.Contains(logged, "request_id="+requestID) || !strings.Contains(logged, "previous_values") {
				t.Fatalf("log line for %s lacks the request id or the error:\n%s", requestID, logged)
			}
		})
	}
}

// Deliberate 4xx messages (validation, conflicts, permissions) keep their text.
func TestClientErrorMessagesUnchanged(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		status  int
		message string
	}{
		{"invalid", func(w http.ResponseWriter, r *http.Request) {
			respondMutation(w, fmt.Errorf("%w: title is required", errInvalid), http.StatusOK, nil)
		}, http.StatusBadRequest, "invalid input: title is required"},
		{"conflict", func(w http.ResponseWriter, r *http.Request) { respondMutation(w, errTeamKeyTaken, http.StatusOK, nil) }, http.StatusBadRequest, "A team with this key already exists"},
		{"forbidden", func(w http.ResponseWriter, r *http.Request) {
			respondMutation(w, store.ErrAuthForbidden, http.StatusOK, nil)
		}, http.StatusForbidden, "You don't have permission to perform this action"},
		{"typed request error", func(w http.ResponseWriter, r *http.Request) {
			writeErrorFor(w, r, http.StatusBadRequest, errors.New("token is invalid or expired"))
		}, http.StatusBadRequest, "token is invalid or expired"},
		{"upstream", func(w http.ResponseWriter, r *http.Request) {
			writeErrorFor(w, r, http.StatusBadGateway, errors.New("GitHub refused the merge"))
		}, http.StatusBadGateway, "GitHub refused the merge"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			requestLog(apiErrorEnvelope(test.handler)).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/issues", nil))
			body := decodeErrorBody(t, recorder)
			if recorder.Code != test.status || !strings.Contains(fmt.Sprint(body["error"]), test.message) || body["requestId"] == nil {
				t.Fatalf("status %d body %s, want %d %q", recorder.Code, recorder.Body.String(), test.status, test.message)
			}
		})
	}
}

// databaseFailureFixture is a SQLite workspace whose issue table refuses every
// insert with a driver error, like a strict MySQL refusing an over-long value.
type databaseFailureFixture struct {
	repo    *store.SQLiteStore
	service *server
	host    *httptest.Server
	data    domain.Bootstrap
	secret  string
}

const databaseFailureText = "Data too long for column 'title' at row 1"

func newDatabaseFailureFixture(t *testing.T) *databaseFailureFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "failing.db")
	repo, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	f := &databaseFailureFixture{repo: repo, data: repo.Bootstrap(), secret: "database-failure-local-only"}
	if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "api_key.created", "failure-key", nil, func(data *domain.Bootstrap) error {
		data.APIKeys = append(data.APIKeys, domain.APIKey{ID: "failure-key", SecretHash: secretHash(f.secret), CreatorID: data.Viewer.ID, Scopes: []string{"read", "write"}, CreatedAt: time.Now().UTC()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TRIGGER refuse_issue_insert BEFORE INSERT ON issue_records BEGIN SELECT RAISE(ABORT, '` + strings.ReplaceAll(databaseFailureText, "'", "''") + `'); END`); err != nil {
		t.Fatal(err)
	}
	f.service = &server{store: repo, uploadPath: t.TempDir(), authDisabled: true}
	f.service.agent.ToolsEnabled, f.service.agent.WriteTools = true, true
	f.host = httptest.NewServer(newHandler(f.service))
	t.Cleanup(f.host.Close)
	return f
}

func assertNoDatabaseText(t *testing.T, where, text string) {
	t.Helper()
	for _, leaked := range []string{"Data too long", "column", "issue_records", "SQL", "constraint"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("%s leaks %q: %s", where, leaked, text)
		}
	}
}

// A real driver error on the REST, MCP and built-in agent write paths: the
// client and the model see a generic message and the request id, the log
// line has the driver text and the same id.
func TestDatabaseFailureOnEveryWritePathIsGeneric(t *testing.T) {
	f := newDatabaseFailureFixture(t)
	logs := captureServerLogs(t)
	team := f.data.Teams[0].ID

	t.Run("rest", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{"title": "Refused by the database", "teamId": team})
		response, err := http.Post(f.host.URL+"/api/issues", "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(response.Body).Decode(&body)
		encoded, _ := json.Marshal(body)
		requestID := response.Header.Get("X-Request-ID")
		if response.StatusCode != http.StatusInternalServerError || body["error"] != internalErrorMessage || body["requestId"] != requestID || requestID == "" {
			t.Fatalf("REST create = %d %s", response.StatusCode, encoded)
		}
		assertNoDatabaseText(t, "REST body", string(encoded))
		if logged := logs.String(); !strings.Contains(logged, requestID) || !strings.Contains(logged, "Data too long") {
			t.Fatalf("log lacks the error for request %s:\n%s", requestID, logged)
		}
	})

	t.Run("mcp", func(t *testing.T) {
		rpc, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "save_issue", "arguments": map[string]any{"title": "Refused by the database", "team": team}}})
		request, _ := http.NewRequest(http.MethodPost, f.host.URL+"/mcp", bytes.NewReader(rpc))
		request.Header.Set("Authorization", "Bearer "+f.secret)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Request-ID", "mcp-failure-request")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result map[string]any
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		text := mcpToolErrorText(t, result)
		assertNoDatabaseText(t, "MCP tool error", text)
		if !strings.Contains(text, "could not be saved") || !strings.Contains(text, "mcp-failure-request") {
			t.Fatalf("MCP tool error = %q", text)
		}
		if logged := logs.String(); !strings.Contains(logged, "request_id=mcp-failure-request") || !strings.Contains(logged, "Data too long") {
			t.Fatalf("log lacks the MCP failure:\n%s", logged)
		}
	})

	t.Run("agent", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), authUserContextKey{}, f.data.Viewer)
		ctx = context.WithValue(ctx, requestIDKey{}, "agent-failure-request")
		ctx = store.ContextWithActor(ctx, f.data.Viewer)
		request := httptest.NewRequest(http.MethodPost, "/api/agent?workspace="+f.data.Workspace.URLKey, nil).WithContext(ctx)
		args, _ := json.Marshal(map[string]any{"title": "Refused by the database", "team": team})
		_, err := f.service.executeAgentTool(request, f.data, domain.AgentToolCall{Name: "save_issue", Arguments: args})
		if err == nil {
			t.Fatal("the save succeeded")
		}
		assertNoDatabaseText(t, "agent tool error", err.Error())
		for _, text := range []string{err.Error(), agentToolFailureText(nil, err), agentToolErrorContent(err)} {
			if !strings.Contains(text, "could not be saved") || !strings.Contains(text, "agent-failure-request") {
				t.Fatalf("agent tool error = %q", text)
			}
		}
		if logged := logs.String(); !strings.Contains(logged, "request_id=agent-failure-request") {
			t.Fatalf("log lacks the agent failure:\n%s", logged)
		}
	})

	if got := len(f.repo.Bootstrap().Issues); got != len(f.data.Issues) {
		t.Fatalf("issues = %d, want %d (nothing written)", got, len(f.data.Issues))
	}
}

func TestPublicToolErrorKeepsCallerMessages(t *testing.T) {
	ctx := context.WithValue(context.Background(), requestIDKey{}, "tool-request")
	plain := errors.New("issue DEV-9 not found")
	if got := publicToolError(ctx, "get_issue", plain); got != plain {
		t.Fatalf("caller error replaced: %v", got)
	}
	if publicToolError(ctx, "save_issue", nil) != nil {
		t.Fatal("nil error replaced")
	}
	read := publicToolError(ctx, "mcp__flow.list_issues", mysqlDataTooLong()).Error()
	if !strings.Contains(read, "could not read") || !strings.Contains(read, "tool-request") || strings.Contains(read, "1406") {
		t.Fatalf("read tool error = %q", read)
	}
	// Outside a request (a background loop run) the log and the message still share an id.
	logs := captureServerLogs(t)
	detached := publicToolError(context.Background(), "save_issue", mysqlDataTooLong()).Error()
	id := strings.TrimSuffix(strings.TrimPrefix(detached[strings.Index(detached, "request id "):], "request id "), "). Try again later.")
	if id == "" || !strings.Contains(logs.String(), "request_id="+id) {
		t.Fatalf("detached error %q not logged with its id:\n%s", detached, logs.String())
	}
}
