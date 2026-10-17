package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestAgentChatAttachmentsReachTheModelOnEveryTurn(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newLoopTestServer(t, provider)
	host := httptest.NewServer(handler)
	t.Cleanup(host.Close)
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n0000fake-screenshot"))
	attachments := []map[string]any{
		{"name": "screen.png", "contentType": "image/png", "size": 26, "content": "data:image/png;base64," + png},
		{"name": "notes.md", "contentType": "text/markdown", "size": 30, "content": "# Notes\nThe header overlaps."},
		{"name": "spec.pdf", "contentType": "application/pdf", "size": 900},
	}
	stream := streamAgent(t, srv, host, "/api/agent/sessions/stream", map[string]any{"message": "What is wrong here?", "location": "page", "attachments": attachments}, nil)
	sessionID := regexp.MustCompile(`"id":"(agent_session_\d+)"`).FindStringSubmatch(stream)
	if sessionID == nil {
		t.Fatalf("no session in stream: %s", stream)
	}
	session := requestJSON[domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions/"+sessionID[1], nil, http.StatusOK)
	sent := session.Messages[0]
	if sent.Content != "What is wrong here?" || len(sent.Attachments) != 3 || !strings.HasPrefix(sent.Attachments[0].URL, "/uploads/agent_attachment_") || sent.Attachments[2].URL != "" {
		t.Fatalf("saved message = %#v", sent)
	}
	modelInput := func(question string) string {
		t.Helper()
		provider.mu.Lock()
		defer provider.mu.Unlock()
		for index := len(provider.inputs) - 1; index >= 0; index-- {
			if strings.Contains(provider.inputs[index], question) {
				return provider.inputs[index]
			}
		}
		t.Fatalf("no model request for %q", question)
		return ""
	}
	first := modelInput("What is wrong here?")
	if !strings.Contains(first, "input_image") || !strings.Contains(first, "data:image/png;base64,"+png) || strings.Contains(first, "The header overlaps.") || !strings.Contains(first, "Attached file notes.md (text/markdown, 28 bytes)") || !strings.Contains(first, "id: "+session.Messages[0].Attachments[1].URL+". Use read_file to read it.") || !strings.Contains(first, "Attached file spec.pdf (application/pdf, 900 bytes); its contents were not kept.") {
		t.Fatalf("attachments missing from model input: %s", first)
	}
	// A follow-up still sees the screenshot from the first message.
	streamAgent(t, srv, host, "/api/agent/sessions/"+sessionID[1]+"/messages/stream", map[string]any{"message": "And how do I fix it?"}, nil)
	if followUp := modelInput("And how do I fix it?"); !strings.Contains(followUp, "data:image/png;base64,"+png) {
		t.Fatalf("follow-up lost the image: %s", followUp)
	}
	// The owner can open the file; deleting the chat hides it.
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, sent.Attachments[1].URL, nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "The header overlaps.") {
		t.Fatalf("serve attachment = %d", recorder.Code)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/agent/sessions/"+sessionID[1], nil, http.StatusNoContent)
	// The chat is hidden at once; its files go when the retention purge removes it (agent_session_restore_test.go).
	requestJSON[any](t, handler, http.MethodGet, "/api/agent/sessions/"+sessionID[1], nil, http.StatusNotFound)
}

func TestAgentChatAttachmentLimits(t *testing.T) {
	srv, handler := newLoopTestServer(t, &fakeLoopProvider{})
	host := httptest.NewServer(handler)
	t.Cleanup(host.Close)
	many := make([]map[string]any, agentAttachmentMax+1)
	for index := range many {
		many[index] = map[string]any{"name": "a.txt", "contentType": "text/plain", "content": "x"}
	}
	large := "data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, agentAttachmentMaxBytes+1))
	for name, attachments := range map[string][]map[string]any{
		"too many":   many,
		"too large":  {{"name": "big.png", "contentType": "image/png", "content": large}},
		"bad image":  {{"name": "x.png", "contentType": "image/png", "content": "data:image/png;base64,not-base64!"}},
		"no name":    {{"name": " ", "contentType": "text/plain", "content": "x"}},
		"large text": {{"name": "log.txt", "contentType": "text/plain", "content": strings.Repeat("x", agentAttachmentMaxBytes+1)}},
	} {
		if stream := streamAgent(t, srv, host, "/api/agent/sessions/stream", map[string]any{"message": "hi", "attachments": attachments}, nil); !strings.Contains(stream, `"error"`) || strings.Contains(stream, "session.started") {
			t.Errorf("%s: accepted: %.200s", name, stream)
		}
	}
}

func TestAgentChatAttachmentsAreVisibleToTheOwnerOnly(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	provider := &fakeLoopProvider{}
	upstream := httptest.NewServer(http.HandlerFunc(provider.serve))
	t.Cleanup(upstream.Close)
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	api := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir(), agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: upstream.URL, Model: "flow-test", MaxOutputTokens: 256}, agentClient: upstream.Client()}))
	defer api.Close()

	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, api.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	member := authClient(t)
	registered := authRequest[struct {
		VerificationToken string `json:"verificationToken"`
	}](t, member, http.MethodPost, api.URL+"/api/auth/register", map[string]string{"name": "Member", "email": "member@example.com", "password": "initial-pass"}, "", http.StatusCreated)
	authRequest[any](t, member, http.MethodPost, api.URL+"/api/auth/verify-email", map[string]string{"token": registered.VerificationToken}, "", http.StatusOK)
	authRequest[domain.AuthSession](t, member, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": "member@example.com", "password": "initial-pass"}, "", http.StatusOK)
	invites := authRequest[[]domain.Invitation](t, admin, http.MethodPost, api.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{"member@example.com"}, "role": "member", "teamIds": []string{bootstrap.Teams[0].ID}}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, member, http.MethodPost, api.URL+"/api/invitations/accept", map[string]string{"token": invites[0].Token}, "", http.StatusOK)

	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nprivate"))
	session := authRequest[domain.AgentSession](t, admin, http.MethodPost, api.URL+"/api/agent/sessions", map[string]any{"message": "Look at this", "attachments": []map[string]any{{"name": "screen.png", "contentType": "image/png", "content": "data:image/png;base64," + png}}}, "test-workspace", http.StatusCreated)
	url := session.Messages[0].Attachments[0].URL
	status := func(client *http.Client) int {
		t.Helper()
		response, err := client.Get(api.URL + url)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	if got := status(admin); got != http.StatusOK {
		t.Fatalf("owner cannot open their chat attachment: %d", got)
	}
	if got := status(member); got == http.StatusOK {
		t.Fatal("another member opened someone else's chat attachment")
	}

	// A pasted image is the uploader's alone: others can neither open it nor send it as theirs.
	uploadFrom := func(client *http.Client) domain.AgentMessageAttachment {
		t.Helper()
		request, _ := http.NewRequest(http.MethodPost, api.URL+"/api/agent/uploads", nil)
		body, contentType := agentUploadForm(t, "image.png", []byte("\x89PNG\r\n\x1a\npasted"))
		request.Body, request.Header = io.NopCloser(body), http.Header{"Content-Type": {contentType}, "X-Workspace-Key": {"test-workspace"}}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var uploaded domain.AgentMessageAttachment
		if response.StatusCode != http.StatusCreated || json.NewDecoder(response.Body).Decode(&uploaded) != nil {
			t.Fatalf("upload = %d", response.StatusCode)
		}
		return uploaded
	}
	pasted := uploadFrom(admin)
	url = pasted.URL
	if got := status(admin); got != http.StatusOK {
		t.Fatalf("uploader cannot open their pasted image: %d", got)
	}
	if got := status(member); got == http.StatusOK {
		t.Fatal("another member opened someone else's pasted image")
	}
	borrowed := authRequest[domain.AgentSession](t, member, http.MethodPost, api.URL+"/api/agent/sessions", map[string]any{"message": "![image.png](" + pasted.URL + ")\n\nWhat is this?"}, "test-workspace", http.StatusCreated)
	if len(borrowed.Messages[0].Attachments) != 0 {
		t.Fatalf("someone else's upload was attached: %#v", borrowed.Messages[0].Attachments)
	}
}

func agentUploadForm(t *testing.T, name string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	return agentUploadFormType(t, name, "image/png", content)
}

func agentUploadFormType(t *testing.T, name, contentType string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	header := make(map[string][]string)
	header["Content-Disposition"] = []string{`form-data; name="file"; filename="` + name + `"`}
	header["Content-Type"] = []string{contentType}
	part, err := form.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(content)
	_ = form.Close()
	return &body, form.FormDataContentType()
}

func TestAgentChatPastedImagesReachTheModel(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newLoopTestServer(t, provider)
	host := httptest.NewServer(handler)
	t.Cleanup(host.Close)
	upload := func(name string, content []byte) (int, domain.AgentMessageAttachment) {
		t.Helper()
		body, contentType := agentUploadForm(t, name, content)
		request := httptest.NewRequest(http.MethodPost, "/api/agent/uploads", body)
		request.Header.Set("Content-Type", contentType)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		var uploaded domain.AgentMessageAttachment
		_ = json.Unmarshal(recorder.Body.Bytes(), &uploaded)
		return recorder.Code, uploaded
	}
	png := []byte("\x89PNG\r\n\x1a\npasted-screenshot")
	code, pasted := upload("Screen Shot 1.png", png)
	if code != http.StatusCreated || !strings.HasPrefix(pasted.URL, "/uploads/agent_upload_") || strings.Contains(pasted.URL, " ") || pasted.ContentType != "image/png" {
		t.Fatalf("upload = %d %#v", code, pasted)
	}
	if code, _ := upload("big.png", make([]byte, agentAttachmentMaxBytes+1)); code == http.StatusCreated {
		t.Fatal("an image over 2 MB was accepted")
	}
	message := "![Screen Shot 1.png](" + pasted.URL + ")\n\nWhat is wrong here?"
	stream := streamAgent(t, srv, host, "/api/agent/sessions/stream", map[string]any{"message": message, "location": "page"}, nil)
	sessionID := regexp.MustCompile(`"id":"(agent_session_\d+)"`).FindStringSubmatch(stream)
	if sessionID == nil {
		t.Fatalf("no session in stream: %s", stream)
	}
	session := requestJSON[domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions/"+sessionID[1], nil, http.StatusOK)
	sent := session.Messages[0]
	if sent.Content != message || len(sent.Attachments) != 1 || sent.Attachments[0].URL != pasted.URL || session.Title != "What is wrong here?" {
		t.Fatalf("saved = %#v title %q", sent, session.Title)
	}
	provider.mu.Lock()
	var input string
	for _, item := range provider.inputs {
		if strings.Contains(item, "What is wrong here?") && strings.Contains(item, "input_image") {
			input = item
		}
	}
	provider.mu.Unlock()
	if input == "" || !strings.Contains(input, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(png)) || strings.Contains(input, "](/uploads/agent_upload_") {
		t.Fatalf("model input = %s", input)
	}
	// Editing the message to drop the image removes the upload.
	requestJSON[domain.AgentSession](t, handler, http.MethodPatch, "/api/agent/sessions/"+sessionID[1]+"/messages/"+sent.ID, map[string]any{"message": "Never mind the picture"}, http.StatusOK)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, pasted.URL, nil))
	if recorder.Code == http.StatusOK {
		t.Fatal("an image dropped by an edit survived")
	}
}

func TestAgentChatUploadsAnyFileButAudio(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newLoopTestServer(t, provider)
	host := httptest.NewServer(handler)
	t.Cleanup(host.Close)
	upload := func(name, contentType string, content []byte) (int, domain.AgentMessageAttachment) {
		t.Helper()
		body, formType := agentUploadFormType(t, name, contentType, content)
		request := httptest.NewRequest(http.MethodPost, "/api/agent/uploads", body)
		request.Header.Set("Content-Type", formType)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		var uploaded domain.AgentMessageAttachment
		_ = json.Unmarshal(recorder.Body.Bytes(), &uploaded)
		return recorder.Code, uploaded
	}
	png := []byte("\x89PNG\r\n\x1a\nshot")
	notes := []byte("Remember the header.")
	code, image := upload("shot.png", "image/png", png)
	if code != http.StatusCreated {
		t.Fatalf("png upload = %d", code)
	}
	code, pdf := upload("spec.pdf", "application/pdf", []byte("%PDF-1.4 spec"))
	if code != http.StatusCreated || pdf.ContentType != "application/pdf" || pdf.Size != 13 {
		t.Fatalf("pdf upload = %d %#v", code, pdf)
	}
	code, video := upload("demo.webm", "video/webm", []byte("\x1a\x45\xdf\xa3webm"))
	if code != http.StatusCreated || video.ContentType != "video/webm" {
		t.Fatalf("webm upload = %d %#v", code, video)
	}
	code, text := upload("notes.txt", "text/plain", notes)
	if code != http.StatusCreated {
		t.Fatalf("text upload = %d", code)
	}
	if code, _ := upload("song.mp3", "audio/mpeg", []byte("ID3")); code != http.StatusBadRequest {
		t.Fatalf("audio upload = %d", code)
	}
	// Only the declared MIME type counts, as in the reference app: a typeless audio file is an ordinary file.
	if code, _ := upload("song.flac", "application/octet-stream", []byte("fLaC")); code != http.StatusCreated {
		t.Fatalf("typeless audio upload = %d", code)
	}
	if code, _ := upload("big.zip", "application/zip", make([]byte, agentUploadMaxBytes+1)); code == http.StatusCreated {
		t.Fatal("a file over 20 MB was accepted")
	}
	if code, _ := upload("big.png", "image/png", make([]byte, agentAttachmentMaxBytes+1)); code == http.StatusCreated {
		t.Fatal("an image over 2 MB was accepted")
	}
	if code, _ := upload("big.svg", "image/svg+xml", make([]byte, agentAttachmentMaxBytes+1)); code != http.StatusCreated {
		t.Fatalf("a large svg = %d", code)
	}

	message := "![shot.png](" + image.URL + ")\n::video[demo.webm](" + video.URL + ")\n::file[spec.pdf](" + pdf.URL + `){size="13" type="application/pdf"}` + "\n::file[notes.txt](" + text.URL + ")\n\nSummarise these."
	stream := streamAgent(t, srv, host, "/api/agent/sessions/stream", map[string]any{"message": message, "location": "page"}, nil)
	sessionID := regexp.MustCompile(`"id":"(agent_session_\d+)"`).FindStringSubmatch(stream)
	if sessionID == nil {
		t.Fatalf("no session in stream: %s", stream)
	}
	session := requestJSON[domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions/"+sessionID[1], nil, http.StatusOK)
	sent := session.Messages[0]
	if len(sent.Attachments) != 4 || sent.Attachments[1].ContentType != "video/webm" || sent.Attachments[2].ContentType != "application/pdf" || sent.Attachments[2].Size != 13 || session.Title != "Summarise these." {
		t.Fatalf("saved = %#v title %q", sent.Attachments, session.Title)
	}
	provider.mu.Lock()
	var input string
	for _, item := range provider.inputs {
		if strings.Contains(item, "Summarise these.") && strings.Contains(item, "input_image") {
			input = item
		}
	}
	provider.mu.Unlock()
	for _, want := range []string{"data:image/png;base64," + base64.StdEncoding.EncodeToString(png), "Attached video demo.webm (video/webm, 8 bytes)", "Use transcribe_media", "Attached file notes.txt (text/plain", "Attached file spec.pdf (application/pdf, 13 bytes)", "Use read_file to read it."} {
		if !strings.Contains(input, want) {
			t.Errorf("model input lacks %q: %s", want, input)
		}
	}
	if strings.Contains(input, "Remember the header.") {
		t.Errorf("a text file was inlined: %s", input)
	}
	if strings.Contains(input, "::file") || strings.Contains(input, "::video") {
		t.Errorf("raw tokens reached the model: %s", input)
	}

	// Dropping the video and the card from the text removes their uploads.
	requestJSON[domain.AgentSession](t, handler, http.MethodPatch, "/api/agent/sessions/"+sessionID[1]+"/messages/"+sent.ID, map[string]any{"message": "![shot.png](" + image.URL + ")\nonly the picture"}, http.StatusOK)
	for url, want := range map[string]bool{image.URL: true, video.URL: false, pdf.URL: false} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, url, nil))
		if (recorder.Code == http.StatusOK) != want {
			t.Errorf("%s served = %d, want kept %v", url, recorder.Code, want)
		}
	}
}

func TestAgentUploadTokensOfOthersAreIgnored(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	own := "/uploads/agent_upload_" + agentUploadOwner(authUser(request).ID) + "_1_a.pdf"
	other := "/uploads/agent_upload_0123456789abcdef_1_b.pdf"
	uploads := agentMessageUploads(request, "::file[b.pdf]("+other+`){size="5" type="application/pdf"}`+"\n::file[a.pdf]("+own+`){size="7" type="application/pdf"}`+"\n::video[c.mp4]("+other+")")
	if len(uploads) != 1 || uploads[0].URL != own || uploads[0].Size != 7 {
		t.Fatalf("uploads = %#v", uploads)
	}
	if got := withoutAgentUploads("::file[b.pdf]("+other+"){size=\"5\"}", uploads); !strings.Contains(got, other) {
		t.Fatalf("someone else's token was stripped: %q", got)
	}
}

func TestAgentChatMislabelledImageIsNotSentAsImage(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newLoopTestServer(t, provider)
	host := httptest.NewServer(handler)
	t.Cleanup(host.Close)
	body, formType := agentUploadFormType(t, "doc.pdf", "application/pdf", []byte("%PDF-1.4 spec"))
	request := httptest.NewRequest(http.MethodPost, "/api/agent/uploads", body)
	request.Header.Set("Content-Type", formType)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var pdf domain.AgentMessageAttachment
	_ = json.Unmarshal(recorder.Body.Bytes(), &pdf)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("pdf upload = %d", recorder.Code)
	}
	message := "![doc.pdf](" + pdf.URL + ")\n::file[doc.pdf](" + pdf.URL + `){type="image/png"}` + "\nLook."
	streamAgent(t, srv, host, "/api/agent/sessions/stream", map[string]any{"message": message, "location": "page"}, nil)
	provider.mu.Lock()
	defer provider.mu.Unlock()
	found := false
	for _, item := range provider.inputs {
		if strings.Contains(item, "Look.") {
			found = true
			if strings.Contains(item, "input_image") {
				t.Errorf("non-image bytes sent as an image: %s", item)
			}
		}
	}
	if !found {
		t.Fatal("provider never saw the message")
	}
}

func TestAgentChatUploadSizesAndEditsKeepReusedUploads(t *testing.T) {
	srv, handler := newLoopTestServer(t, &fakeLoopProvider{})
	host := httptest.NewServer(handler)
	t.Cleanup(host.Close)
	upload := func(name string, content []byte) domain.AgentMessageAttachment {
		t.Helper()
		body, contentType := agentUploadForm(t, name, content)
		request := httptest.NewRequest(http.MethodPost, "/api/agent/uploads", body)
		request.Header.Set("Content-Type", contentType)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		var uploaded domain.AgentMessageAttachment
		if recorder.Code != http.StatusCreated || json.Unmarshal(recorder.Body.Bytes(), &uploaded) != nil {
			t.Fatalf("upload %s = %d %s", name, recorder.Code, recorder.Body.String())
		}
		return uploaded
	}
	first := upload("one.png", []byte("\x89PNG\r\n\x1a\nfirst"))
	second := upload("two.png", []byte("\x89PNG\r\n\x1a\nsecond-image"))
	// The size in a ::file token is the client's claim; the stored size wins.
	stream := streamAgent(t, srv, host, "/api/agent/sessions/stream", map[string]any{"message": "::file[one.png](" + first.URL + `){size="999999" type="image/png"}` + "\n\nFirst"}, nil)
	sessionID := regexp.MustCompile(`"id":"(agent_session_\d+)"`).FindStringSubmatch(stream)[1]
	streamAgent(t, srv, host, "/api/agent/sessions/"+sessionID+"/messages/stream", map[string]any{"message": "![two.png](" + second.URL + ")\n\nSecond"}, nil)
	session := requestJSON[domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions/"+sessionID, nil, http.StatusOK)
	if got := session.Messages[0].Attachments[0].Size; got != int64(len("\x89PNG\r\n\x1a\nfirst")) {
		t.Fatalf("recorded size = %d", got)
	}
	// Editing the first message to show the second image drops the later message but keeps that image.
	requestJSON[domain.AgentSession](t, handler, http.MethodPatch, "/api/agent/sessions/"+sessionID+"/messages/"+session.Messages[0].ID, map[string]any{"message": "![two.png](" + second.URL + ")\n\nOnly the second"}, http.StatusOK)
	status := func(url string) int {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, url, nil))
		return recorder.Code
	}
	if status(second.URL) != http.StatusOK {
		t.Fatal("an upload the edit still shows was deleted")
	}
	if status(first.URL) == http.StatusOK {
		t.Fatal("an upload the edit dropped survived")
	}
}
