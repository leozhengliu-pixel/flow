package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// documentAttachmentFixture is the MCP contract fixture (the admin's API key)
// plus workspace members with their own API keys.
type documentAttachmentFixture struct {
	*mcpContractFixture
	workspaceID string
}

func newDocumentAttachmentFixture(t *testing.T) *documentAttachmentFixture {
	t.Helper()
	f := newMCPContractFixture(t)
	metadata, ok := f.repository.WorkspaceMetadata(f.data.Workspace.URLKey)
	if !ok {
		t.Fatal("workspace metadata")
	}
	return &documentAttachmentFixture{mcpContractFixture: f, workspaceID: metadata.Workspace.ID}
}

// member adds a workspace member (role member) and returns them with an API key secret.
func (f *documentAttachmentFixture) member(t *testing.T, email string) (domain.User, string) {
	t.Helper()
	session, _, err := f.repository.LoginExternal(t.Context(), email, strings.Split(email, "@")[0], "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repository.EnsureWorkspaceMembership(t.Context(), f.workspaceID, session.User.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.repository.UpdateMemberRole(t.Context(), f.workspaceID, session.User.ID, "member"); err != nil {
		t.Fatal(err)
	}
	secret := "member-key-" + session.User.ID
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "api_key.created", "key-"+session.User.ID, nil, func(data *domain.Bootstrap) error {
		data.APIKeys = append(data.APIKeys, domain.APIKey{ID: "key-" + session.User.ID, SecretHash: secretHash(secret), CreatorID: session.User.ID, Scopes: []string{"read", "write"}, CreatedAt: time.Now().UTC()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return session.User, secret
}

// tool calls a Flow tool over /mcp with an API key and returns its result, or the tool error text.
func (f *documentAttachmentFixture) tool(t *testing.T, secret, name string, args map[string]any) (map[string]any, string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	request, _ := http.NewRequest(http.MethodPost, f.host.URL+"/mcp", bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var envelope map[string]any
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	result, ok := envelope["result"].(map[string]any)
	if !ok {
		t.Fatalf("%s: protocol error %v", name, envelope)
	}
	if result["isError"] == true {
		return nil, result["content"].([]any)[0].(map[string]any)["text"].(string)
	}
	structured, _ := result["structuredContent"].(map[string]any)
	return structured, ""
}

func (f *documentAttachmentFixture) must(t *testing.T, secret, name string, args map[string]any) map[string]any {
	t.Helper()
	result, failure := f.tool(t, secret, name, args)
	if failure != "" {
		t.Fatalf("%s %v: %s", name, args, failure)
	}
	return result
}

func (f *documentAttachmentFixture) refused(t *testing.T, secret, name string, args map[string]any, want string) {
	t.Helper()
	if _, failure := f.tool(t, secret, name, args); failure == "" || !strings.Contains(failure, want) {
		t.Fatalf("%s %v = %q, want a refusal containing %q", name, args, failure, want)
	}
}

// fetch GETs a file URL as the holder of bearer.
func fetchFile(t *testing.T, target, bearer string) (*http.Response, []byte) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, target, nil)
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response, body
}

func putUpload(t *testing.T, prepared map[string]any, body []byte) int {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPut, prepared["uploadUrl"].(string), bytes.NewReader(body))
	request.Header.Set("Content-Type", prepared["headers"].(map[string]any)["Content-Type"].(string))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	return response.StatusCode
}

func base64Args(target map[string]any, filename, contentType string, body []byte) map[string]any {
	digest := sha256.Sum256(body)
	args := map[string]any{"filename": filename, "contentType": contentType, "base64Content": base64.StdEncoding.EncodeToString(body), "sha256": hex.EncodeToString(digest[:]), "size": len(body)}
	for key, value := range target {
		args[key] = value
	}
	return args
}

var evidencePNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR evidence")

func documentContentNode(document domain.Document, kind string) map[string]any {
	var walk func(value any) map[string]any
	walk = func(value any) map[string]any {
		switch typed := value.(type) {
		case map[string]any:
			if typed["type"] == kind {
				return typed
			}
			return walk(typed["content"])
		case []any:
			for _, item := range typed {
				if found := walk(item); found != nil {
					return found
				}
			}
		}
		return nil
	}
	return walk(document.ContentData)
}

// The document target: prepare → PUT → confirm with insert none and append,
// the stable assetUrl (404 before the confirm, served after), the old
// confirm-by-uploadUrl flow, base64 uploads, links, delete and the refusals.
func TestMCPDocumentAttachmentUploadFlow(t *testing.T) {
	f := newDocumentAttachmentFixture(t)
	admin := f.secret
	created := f.must(t, admin, "save_document", map[string]any{"title": "Test report", "content": "Verdict below."})
	documentID := created["id"].(string)
	stored := func() domain.Document {
		t.Helper()
		for _, document := range f.repository.Bootstrap().Documents {
			if document.ID == documentID {
				return document
			}
		}
		t.Fatal("document missing")
		return domain.Document{}
	}
	before := stored()

	prepared := f.must(t, admin, "prepare_attachment_upload", map[string]any{"document": created["slugId"], "filename": "shot.png", "contentType": "image/png", "size": len(evidencePNG)})
	assetURL, uploadURL := prepared["assetUrl"].(string), prepared["uploadUrl"].(string)
	if assetURL == uploadURL || !strings.HasPrefix(assetURL, f.host.URL+"/uploads/asset_") || !strings.HasSuffix(assetURL, prepared["url"].(string)) || !strings.Contains(uploadURL, "/api/mcp/uploads/") {
		t.Fatalf("prepared urls = %v", prepared)
	}
	if response, _ := fetchFile(t, assetURL, admin); response.StatusCode != http.StatusNotFound {
		t.Fatalf("asset before the PUT = %d", response.StatusCode)
	}
	if status := putUpload(t, prepared, evidencePNG); status != http.StatusNoContent {
		t.Fatalf("PUT = %d", status)
	}
	if response, _ := fetchFile(t, assetURL, admin); response.StatusCode != http.StatusNotFound {
		t.Fatalf("asset before the confirm = %d", response.StatusCode)
	}
	// Bound to its document: neither another target nor the issue form confirms it.
	other := f.must(t, admin, "save_document", map[string]any{"title": "Other document"})
	f.refused(t, admin, "create_attachment_from_upload", map[string]any{"document": other["id"], "assetUrl": assetURL}, "different document")
	f.refused(t, admin, "create_attachment_from_upload", map[string]any{"issue": f.data.Issues[0].ID, "assetUrl": assetURL}, "different issue")
	image := f.must(t, admin, "create_attachment_from_upload", map[string]any{"document": documentID, "assetUrl": assetURL, "title": "Login screen"})
	if image["documentId"] != documentID || image["inserted"] != false || image["assetUrl"] != assetURL || image["url"] != prepared["url"] || image["markdown"] != "![Login screen]("+prepared["url"].(string)+")" || image["id"] == "" {
		t.Fatalf("image attachment = %v", image)
	}
	if _, has := image["issueId"]; has {
		t.Fatalf("document attachment carries issueId: %v", image)
	}
	if after := stored(); after.Content != before.Content || !strings.Contains(after.Content, "Verdict below.") || len(after.Attachments) != 1 {
		t.Fatalf("insert none changed the body or missed the record: %q %v", after.Content, after.Attachments)
	}
	response, body := fetchFile(t, assetURL, admin)
	if response.StatusCode != http.StatusOK || !bytes.Equal(body, evidencePNG) || response.Header.Get("Content-Type") != "image/png" || response.Header.Get("Content-Disposition") != "" || !strings.Contains(response.Header.Get("Content-Security-Policy"), "sandbox") || response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("image served %d %v", response.StatusCode, response.Header)
	}
	if response, _ := fetchFile(t, assetURL, ""); response.StatusCode == http.StatusOK {
		t.Fatal("asset served without credentials")
	}
	f.refused(t, admin, "create_attachment_from_upload", map[string]any{"document": documentID, "assetUrl": assetURL}, "not a completed Flow upload")

	// insert "append" adds a file card; the old flow confirms with the upload URL.
	verdict := []byte(`{"verdict":"pass"}`)
	prepared = f.must(t, admin, "prepare_attachment_upload", map[string]any{"document": documentID, "filename": "verdict.json", "contentType": "application/json", "size": len(verdict)})
	if status := putUpload(t, prepared, verdict); status != http.StatusNoContent {
		t.Fatalf("PUT = %d", status)
	}
	file := f.must(t, admin, "create_attachment_from_upload", map[string]any{"document": documentID, "assetUrl": prepared["uploadUrl"], "insert": "append"})
	if file["inserted"] != true || file["markdown"] != "[verdict.json]("+prepared["url"].(string)+")" {
		t.Fatalf("appended file = %v", file)
	}
	appended := stored()
	card := documentContentNode(appended, "file")
	if !strings.HasPrefix(appended.Content, "Verdict below.\n\n::file[verdict.json]("+prepared["url"].(string)+")") || card == nil || card["attrs"].(map[string]any)["src"] != prepared["url"] {
		t.Fatalf("append = %q %v", appended.Content, appended.ContentData)
	}
	if response, body := fetchFile(t, prepared["assetUrl"].(string), admin); response.StatusCode != http.StatusOK || string(body) != string(verdict) || !strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("json served %d %v", response.StatusCode, response.Header)
	}

	// base64 and links.
	log := []byte("step 1 ok\nstep 2 ok\n")
	logFile := f.must(t, admin, "create_attachment", base64Args(map[string]any{"document": documentID}, "run log.txt", "text/plain", log))
	if logFile["inserted"] != false || !strings.HasPrefix(logFile["markdown"].(string), "[run log.txt](/uploads/asset_") || !strings.HasSuffix(logFile["url"].(string), ".txt") {
		t.Fatalf("base64 document attachment = %v", logFile)
	}
	if response, body := fetchFile(t, f.host.URL+logFile["url"].(string), admin); response.StatusCode != http.StatusOK || string(body) != string(log) {
		t.Fatalf("base64 file served %d", response.StatusCode)
	}
	link := f.must(t, admin, "save_attachment", map[string]any{"document": documentID, "url": "https://ci.example.com/runs/7", "title": "CI run 7"})
	relinked := f.must(t, admin, "save_attachment", map[string]any{"document": documentID, "url": "https://ci.example.com/runs/7", "subtitle": "passed"})
	if link["id"] != relinked["id"] || relinked["subtitle"] != "passed" || relinked["markdown"] != "[CI run 7](https://ci.example.com/runs/7)" || relinked["documentId"] != documentID {
		t.Fatalf("document link = %v %v", link, relinked)
	}
	if got := len(stored().Attachments); got != 4 {
		t.Fatalf("document attachments = %d, want 4", got)
	}
	f.must(t, admin, "delete_attachment", map[string]any{"id": logFile["id"]})
	if response, _ := fetchFile(t, f.host.URL+logFile["url"].(string), admin); response.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted file served %d", response.StatusCode)
	}

	// Exactly one target; the size and file type limits apply.
	for _, name := range []string{"prepare_attachment_upload", "create_attachment_from_upload", "create_attachment", "save_attachment"} {
		args := map[string]any{"filename": "a.txt", "contentType": "text/plain", "size": 1, "assetUrl": assetURL, "url": "https://example.com", "base64Content": "eA==", "sha256": "x"}
		switch name {
		case "prepare_attachment_upload":
			delete(args, "assetUrl")
			delete(args, "url")
			delete(args, "base64Content")
			delete(args, "sha256")
		case "create_attachment_from_upload":
			args = map[string]any{"assetUrl": assetURL}
		case "save_attachment":
			args = map[string]any{"url": "https://example.com"}
		case "create_attachment":
			delete(args, "assetUrl")
			delete(args, "url")
		}
		f.refused(t, admin, name, args, "pass exactly one of `issue`")
		args["issue"], args["document"] = f.data.Issues[0].ID, documentID
		f.refused(t, admin, name, args, "pass exactly one of `issue`")
	}
	f.refused(t, admin, "prepare_attachment_upload", map[string]any{"document": documentID, "filename": "big.bin", "contentType": "application/octet-stream", "size": maxUploadBytes + 1}, "Invalid arguments")
	f.refused(t, admin, "prepare_attachment_upload", map[string]any{"document": documentID, "filename": "a.txt", "contentType": "not a type", "size": 1}, "valid media type")
	f.refused(t, admin, "prepare_attachment_upload", map[string]any{"document": "missing-document", "filename": "a.txt", "contentType": "text/plain", "size": 1}, "not found")
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "workspace.settings_updated", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.RestrictFileUploads, data.WorkspaceSettings.AllowedFileExtensions = true, []string{"json"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.refused(t, admin, "create_attachment", base64Args(map[string]any{"document": documentID}, "tool.exe", "application/octet-stream", []byte("MZ binary")), "upload policy")
	blocked := f.must(t, admin, "prepare_attachment_upload", map[string]any{"document": documentID, "filename": "tool.exe", "contentType": "application/octet-stream", "size": 9})
	if status := putUpload(t, blocked, []byte("MZ binary")); status != http.StatusForbidden {
		t.Fatalf("PUT of a blocked type = %d", status)
	}
}

// The issue form keeps its behaviour (embedded by default), and insert
// "none" now leaves the description alone.
func TestMCPIssueAttachmentInsertNone(t *testing.T) {
	f := newDocumentAttachmentFixture(t)
	issue := f.must(t, f.secret, "save_issue", map[string]any{"team": f.data.Teams[0].ID, "title": "Evidence", "description": "Requirement text"})
	embedded := f.must(t, f.secret, "create_attachment", base64Args(map[string]any{"issue": issue["id"]}, "shot.png", "image/png", evidencePNG))
	if embedded["inserted"] != true || embedded["issueId"] != issue["id"] || !strings.HasPrefix(embedded["markdown"].(string), "![shot.png](/uploads/") {
		t.Fatalf("default issue attachment = %v", embedded)
	}
	record, _ := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, issue["id"].(string))
	if !strings.Contains(record.Description, embedded["url"].(string)) {
		t.Fatalf("default did not embed: %q", record.Description)
	}
	plain := f.must(t, f.secret, "create_attachment", base64Args(map[string]any{"issue": issue["id"], "insert": "none"}, "log.txt", "text/plain", []byte("log")))
	prepared := f.must(t, f.secret, "prepare_attachment_upload", map[string]any{"issue": issue["id"], "filename": "trace.txt", "contentType": "text/plain", "size": 5})
	if status := putUpload(t, prepared, []byte("trace")); status != http.StatusNoContent {
		t.Fatalf("PUT = %d", status)
	}
	uploaded := f.must(t, f.secret, "create_attachment_from_upload", map[string]any{"issue": issue["id"], "assetUrl": prepared["assetUrl"], "insert": "none"})
	after, _ := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, issue["id"].(string))
	if after.Description != record.Description || plain["inserted"] != false || uploaded["inserted"] != false || len(after.Attachments) != 3 {
		t.Fatalf("insert none changed the description: %q (attachments %d)", after.Description, len(after.Attachments))
	}
	if response, body := fetchFile(t, uploaded["assetUrl"].(string), f.secret); response.StatusCode != http.StatusOK || string(body) != "trace" {
		t.Fatalf("issue file served %d", response.StatusCode)
	}
}

// Document roles decide: a viewer cannot attach, a private team's inherited
// scope hides the document from outsiders (and its files with it), and the
// files are served to everyone who can see the document.
func TestMCPDocumentAttachmentPermissions(t *testing.T) {
	f := newDocumentAttachmentFixture(t)
	admin := f.secret
	member, memberKey := f.member(t, "doc-member@example.test")
	_, outsiderKey := f.member(t, "doc-outsider@example.test")

	readOnly := f.must(t, admin, "save_document", map[string]any{"title": "Read-only report", "content": "Shared for reading"})
	f.must(t, admin, "save_document_permissions", map[string]any{"id": readOnly["id"], "permissions": []map[string]any{{"subject": "workspace", "role": "viewer"}}})
	target := map[string]any{"document": readOnly["id"]}
	f.refused(t, memberKey, "prepare_attachment_upload", map[string]any{"document": readOnly["id"], "filename": "a.png", "contentType": "image/png", "size": 4}, "cannot edit")
	f.refused(t, memberKey, "create_attachment", base64Args(target, "a.png", "image/png", evidencePNG), "cannot edit")
	f.refused(t, memberKey, "save_attachment", map[string]any{"document": readOnly["id"], "url": "https://example.com/x"}, "cannot edit")
	shot := f.must(t, admin, "create_attachment", base64Args(target, "shot.png", "image/png", evidencePNG))
	f.refused(t, memberKey, "delete_attachment", map[string]any{"id": shot["id"]}, "cannot edit")
	if response, _ := fetchFile(t, f.host.URL+shot["url"].(string), memberKey); response.StatusCode != http.StatusOK {
		t.Fatalf("viewer cannot open the document's file: %d", response.StatusCode)
	}

	// A document on a private team's issue: outside the team it is hidden.
	team := f.must(t, admin, "save_team", map[string]any{"name": "Vault", "key": "VLT", "private": true})
	if err := f.repository.SetTeamMembership(t.Context(), f.workspaceID, team["id"].(string), member.ID, "member", true); err != nil {
		t.Fatal(err)
	}
	issue := f.must(t, admin, "save_issue", map[string]any{"team": team["id"], "title": "Private requirement"})
	private := f.must(t, admin, "save_document", map[string]any{"title": "Private report", "issue": issue["id"], "content": "Team only"})
	f.refused(t, outsiderKey, "prepare_attachment_upload", map[string]any{"document": private["id"], "filename": "a.png", "contentType": "image/png", "size": 4}, "not found")
	evidence := f.must(t, memberKey, "create_attachment", base64Args(map[string]any{"document": private["id"]}, "evidence.png", "image/png", evidencePNG))
	if response, _ := fetchFile(t, evidence["assetUrl"].(string), memberKey); response.StatusCode != http.StatusOK {
		t.Fatalf("team member cannot open the file: %d", response.StatusCode)
	}
	if response, _ := fetchFile(t, evidence["assetUrl"].(string), outsiderKey); response.StatusCode != http.StatusNotFound {
		t.Fatalf("outsider opened the private document's file: %d", response.StatusCode)
	}
	if response, _ := fetchFile(t, evidence["assetUrl"].(string), admin); response.StatusCode != http.StatusOK {
		t.Fatalf("admin cannot open the file: %d", response.StatusCode)
	}
}

// One MCP session through the official SDK writes a test report: the
// document, a screenshot and a verdict file uploaded to it, and the body
// linking both, then reads the document and the files back.
func TestMCPDocumentReportWithEvidenceEndToEnd(t *testing.T) {
	f := newDocumentAttachmentFixture(t)
	client := mcp.NewClient(&mcp.Implementation{Name: "Report writer", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: f.host.URL + "/mcp", HTTPClient: &http.Client{Transport: mcpAuthTransport{f.secret}, Timeout: 10 * time.Second}, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if result.IsError {
			raw, _ := json.Marshal(result.Content)
			t.Fatalf("%s: %s", name, raw)
		}
		var object map[string]any
		if err := jsonClone(result.StructuredContent, &object); err != nil {
			t.Fatal(err)
		}
		return object
	}
	report := call("save_document", map[string]any{"title": "Login test report", "content": "Running."})
	upload := func(filename, contentType string, body []byte) map[string]any {
		t.Helper()
		prepared := call("prepare_attachment_upload", map[string]any{"document": report["id"], "filename": filename, "contentType": contentType, "size": len(body)})
		if status := putUpload(t, prepared, body); status != http.StatusNoContent {
			t.Fatalf("PUT %s = %d", filename, status)
		}
		return call("create_attachment_from_upload", map[string]any{"document": report["id"], "assetUrl": prepared["assetUrl"]})
	}
	verdict := []byte(`{"verdict":"pass","cases":3}`)
	screenshot, verdictFile := upload("login.png", "image/png", evidencePNG), upload("verdict.json", "application/json", verdict)
	content := "## Result\n\nAll 3 cases passed.\n\n" + screenshot["markdown"].(string) + "\n\nVerdict: " + verdictFile["markdown"].(string)
	call("save_document", map[string]any{"id": report["id"], "content": content})

	read := call("get_document", map[string]any{"id": report["id"]})
	if !strings.Contains(read["content"].(string), screenshot["markdown"].(string)) || !strings.Contains(read["content"].(string), verdictFile["markdown"].(string)) {
		t.Fatalf("document content = %v", read["content"])
	}
	document := authRequest[domain.Document](t, &http.Client{Transport: mcpAuthTransport{f.secret}}, http.MethodGet, f.host.URL+"/api/documents/"+report["id"].(string), nil, f.data.Workspace.URLKey, http.StatusOK)
	image := documentContentNode(document, "image")
	if image == nil || image["attrs"].(map[string]any)["src"] != screenshot["url"] || len(document.Attachments) != 2 {
		t.Fatalf("editor document: image %v, attachments %d, data %v", image, len(document.Attachments), document.ContentData)
	}
	raw, _ := json.Marshal(document.ContentData)
	if !strings.Contains(string(raw), `"href":"`+verdictFile["url"].(string)+`"`) {
		t.Fatalf("verdict link missing from the editor document: %s", raw)
	}
	for _, file := range []struct {
		url, contentType string
		body             []byte
		inline           bool
	}{{screenshot["assetUrl"].(string), "image/png", evidencePNG, true}, {verdictFile["assetUrl"].(string), "application/json", verdict, false}} {
		response, body := fetchFile(t, file.url, f.secret)
		if response.StatusCode != http.StatusOK || !bytes.Equal(body, file.body) || response.Header.Get("Content-Type") != file.contentType || (response.Header.Get("Content-Disposition") == "") != file.inline {
			t.Fatalf("%s served %d %v", file.url, response.StatusCode, response.Header)
		}
	}
}

// Lines that hold only an image, video or file card become those editor
// blocks; anything else stays text.
func TestMCPMarkdownMediaBlocks(t *testing.T) {
	document := mcpMarkdownDocument("Intro\n![Shot](/uploads/asset_a.png \"Login\")\n::file[log.txt](/uploads/asset_b.txt){size=\"12\" type=\"text/plain\"}\n::video[Run](https://cdn.example.com/run.mp4)\nInline ![x](/uploads/c.png) image\n![bad](javascript:alert(1))")
	content := document["content"].([]any)
	kinds := []string{}
	for _, block := range content {
		kinds = append(kinds, block.(map[string]any)["type"].(string))
	}
	if strings.Join(kinds, ",") != "paragraph,image,file,video,paragraph" {
		t.Fatalf("blocks = %v", kinds)
	}
	image := content[1].(map[string]any)["attrs"].(map[string]any)
	file := content[2].(map[string]any)["attrs"].(map[string]any)
	if image["src"] != "/uploads/asset_a.png" || image["alt"] != "Shot" || image["title"] != "Login" || file["size"] != int64(12) || file["contentType"] != "text/plain" || file["title"] != "log.txt" {
		t.Fatalf("attrs = %v %v", image, file)
	}
}
