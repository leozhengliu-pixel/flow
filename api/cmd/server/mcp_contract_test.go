package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpContractFixture struct {
	repository *store.SQLiteStore
	service    *server
	host       *httptest.Server
	data       domain.Bootstrap
	secret     string
}

func newMCPContractFixture(t testing.TB) *mcpContractFixture {
	t.Helper()
	var repository *store.SQLiteStore
	var err error
	if driver := os.Getenv("FLOW_MCP_TEST_DATABASE_DRIVER"); driver != "" {
		repository, err = store.OpenDatabase(store.DatabaseConfig{Driver: driver, URL: os.Getenv("FLOW_MCP_TEST_DATABASE_URL"), FixtureProfile: "test", FixturePassword: "local-test-only", MaxOpenConns: 4, MaxIdleConns: 2})
	} else {
		repository, err = store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "mcp.db"))
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	f := &mcpContractFixture{repository: repository, data: repository.Bootstrap(), secret: "mcp-contract-local-only"}
	if err := repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "api_key.created", "contract-key", nil, func(data *domain.Bootstrap) error {
		data.APIKeys = append(data.APIKeys, domain.APIKey{ID: "contract-key", SecretHash: secretHash(f.secret), CreatorID: data.Viewer.ID, Scopes: []string{"read", "write"}, CreatedAt: time.Now().UTC()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.service = &server{store: repository, uploadPath: t.TempDir()}
	f.host = httptest.NewServer(newHandler(f.service))
	t.Cleanup(f.host.Close)
	return f
}

func FuzzMCPWireEnvelope(f *testing.F) {
	fixture := newMCPContractFixture(f)
	handler := fixture.service.mcpHTTP(false)
	for _, seed := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_issues","arguments":{}}}`,
		`null`, `[]`, `{"id":true}`, `{"jsonrpc":"2.0",`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 32768 {
			t.Skip()
		}
		request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(raw))
		request.Header.Set("Authorization", "Bearer "+fixture.secret)
		response := httptest.NewRecorder()
		handler(response, request)
		if response.Code == http.StatusAccepted {
			return
		}
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result["jsonrpc"] != "2.0" {
			t.Fatalf("invalid protocol response: %d %s", response.Code, response.Body.String())
		}
		if _, ok := result["id"]; !ok {
			t.Fatal("response lost its ID")
		}
	})
}
func (f *mcpContractFixture) raw(t *testing.T, body string) (int, map[string]any) {
	t.Helper()
	r, err := http.NewRequest("POST", f.host.URL+"/mcp", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+f.secret)
	r.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, result
}
func (f *mcpContractFixture) call(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	_, r := f.raw(t, string(raw))
	return r
}

func TestMCPRejectsMalformedArgumentsBeforeAnyWrite(t *testing.T) {
	f := newMCPContractFixture(t)
	before := len(f.repository.Bootstrap().Issues)
	for _, args := range []map[string]any{
		{"title": "Invalid priority", "team": f.data.Teams[0].ID, "priority": 1.5},
		{"title": "Invalid labels", "team": f.data.Teams[0].ID, "labels": "not-an-array"},
		{"title": "Wrong assignee field", "team": f.data.Teams[0].ID, "assigneeId": f.data.Viewer.ID},
	} {
		response := f.call(t, "save_issue", args)
		if response["error"] == nil {
			t.Errorf("invalid arguments accepted: %v", args)
		}
	}
	if got := len(f.repository.Bootstrap().Issues); got != before {
		t.Fatalf("invalid calls created %d issues", got-before)
	}
}

func TestMCPEveryToolRejectsWrongArgumentTypes(t *testing.T) {
	f := newMCPContractFixture(t)
	var inventory []flowMCPTool
	if err := json.Unmarshal(flowMCPToolInventory, &inventory); err != nil {
		t.Fatal(err)
	}
	for _, tool := range inventory {
		t.Run(tool.Name, func(t *testing.T) {
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			json.Unmarshal(tool.InputSchema, &schema)
			args := map[string]any{}
			for field := range schema.Properties {
				args[field] = map[string]any{"wrong": "type"}
				break
			}
			if len(args) == 0 {
				return
			}
			response := f.call(t, tool.Name[len("mcp__flow."):], args)
			if response["error"] == nil {
				t.Errorf("tool did not enforce advertised input schema")
			}
		})
	}
}

func TestMCPProtocolRejectsInvalidEnvelopeAndNegotiatesVersion(t *testing.T) {
	f := newMCPContractFixture(t)
	for _, raw := range []string{`{"jsonrpc":"2.0","id":{},"method":"ping"}`, `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"save_issue","arguments":{"title":"No ID","team":"team_test"}}}`, `null`, `[]`, `{"jsonrpc":"2.0",`} {
		_, r := f.raw(t, raw)
		if r["jsonrpc"] != "2.0" || r["error"] == nil {
			t.Fatalf("not a protocol error: %s => %v", raw, r)
		}
		if id, exists := r["id"]; !exists || id != nil {
			t.Fatalf("invalid envelope must have null response ID: %v", r)
		}
	}
	_, r := f.raw(t, `{"jsonrpc":"2.0","id":5,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"compatibility","version":"1"}}}`)
	if r["result"].(map[string]any)["protocolVersion"] != "2025-03-26" {
		t.Fatal("supported protocol version not negotiated")
	}
}

type mcpAuthTransport struct{ token string }

func (r mcpAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	next := request.Clone(request.Context())
	next.Header = request.Header.Clone()
	next.Header.Set("Authorization", "Bearer "+r.token)
	return http.DefaultTransport.RoundTrip(next)
}
func TestMCPRealSDKConnectsAndCallsTools(t *testing.T) {
	f := newMCPContractFixture(t)
	client := mcp.NewClient(&mcp.Implementation{Name: "Flow contract verification", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: f.host.URL + "/mcp", HTTPClient: &http.Client{Transport: mcpAuthTransport{f.secret}, Timeout: 10 * time.Second}, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 48 {
		t.Fatalf("inventory changed: %d", len(listed.Tools))
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_workspace", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("SDK read failed: %v %v", err, result)
	}
	if err := session.Ping(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Transport: &http.Transport{}}).Get(f.host.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatalf("unauthed GET %d", response.StatusCode)
	}
}

func TestMCPOAuthDiscoveryAndReadonlySurface(t *testing.T) {
	f := newMCPContractFixture(t)
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource/mcp/readonly"} {
		response, err := http.Get(f.host.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var metadata map[string]any
		err = json.NewDecoder(response.Body).Decode(&metadata)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 {
			t.Fatalf("discovery failed: %s %v", path, err)
		}
		if path == "/.well-known/oauth-authorization-server" && metadata["token_endpoint"] != f.host.URL+"/oauth/token" {
			t.Fatal("incorrect token endpoint", metadata)
		}
	}
	var inventory []flowMCPTool
	if err := json.Unmarshal(flowMCPToolInventory, &inventory); err != nil {
		t.Fatal(err)
	}
	for _, tool := range inventory {
		if tool.Access != "write" {
			continue
		}
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool.Name[len("mcp__flow."):], "arguments": map[string]any{}}})
		request, _ := http.NewRequest("POST", f.host.URL+"/mcp/readonly", bytes.NewReader(raw))
		request.Header.Set("Authorization", "Bearer "+f.secret)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		err = json.NewDecoder(response.Body).Decode(&result)
		response.Body.Close()
		if err != nil || result["result"].(map[string]any)["isError"] != true {
			t.Fatalf("readonly allowed %s: %v", tool.Name, result)
		}
	}
}
