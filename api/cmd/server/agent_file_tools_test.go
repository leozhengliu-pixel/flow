package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
)

// testPDF builds a small valid PDF with one line of Helvetica text per page.
func testPDF(pages ...string) []byte {
	var out bytes.Buffer
	offsets := []int{}
	object := func(body string) {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	out.WriteString("%PDF-1.4\n")
	object("<< /Type /Catalog /Pages 2 0 R >>")
	kids := make([]string, len(pages))
	for index := range pages {
		kids[index] = fmt.Sprintf("%d 0 R", 4+index*2)
	}
	object(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages)))
	object("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for index, text := range pages {
		object(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /Font << /F1 3 0 R >> >> >>", 5+index*2))
		stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
		object(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return out.Bytes()
}

func testZip(t *testing.T, entries ...string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for index := 0; index < len(entries); index += 2 {
		file, err := writer.Create(entries[index])
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(file, entries[index+1])
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func testXLSX(t *testing.T) []byte {
	return testZip(t,
		"[Content_Types].xml", `<Types/>`,
		"xl/workbook.xml", `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Sales" sheetId="1" r:id="rId1"/><sheet name="Notes &amp; Todo" sheetId="2" r:id="rId2"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels", `<Relationships><Relationship Id="rId1" Type="x" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="x" Target="/xl/worksheets/sheet2.xml"/></Relationships>`,
		"xl/sharedStrings.xml", `<sst><si><t>Region</t></si><si><t>Revenue</t></si><si><r><t>North </t></r><r><t>America</t></r></si><si><t>pipe | cell</t><rPh><t>ignored</t></rPh></si></sst>`,
		"xl/worksheets/sheet1.xml", `<worksheet><sheetData>
<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="inlineStr"><is><t>Closed</t></is></c></row>
<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>42.5</v></c><c r="C2" t="b"><v>1</v></c></row>
<row r="4"><c r="A4" t="s"><v>3</v></c><c r="B4"><v>7</v></c><c r="C4" t="b"><v>0</v></c></row>
<row r="5"><c r="A5"/><c r="B5"/></row>
</sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml", `<worksheet><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>Item</t></is></c></row><row r="2"><c r="A2" t="inlineStr"><is><t>Ship it</t></is></c></row></sheetData></worksheet>`,
	)
}

func TestReadFileParsesDocuments(t *testing.T) {
	read := func(name, contentType string, data []byte) string {
		t.Helper()
		text, err := parseAgentFile(name, contentType, data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return text
	}

	pdfText := read("report.pdf", "application/pdf", testPDF("FLOW PARITY TEST: quarterly revenue was 42 million dollars.", "Second page text"))
	if !strings.Contains(pdfText, "Page 1:\nFLOW PARITY TEST: quarterly revenue was 42 million dollars.") || !strings.Contains(pdfText, "Page 2:\nSecond page text") {
		t.Errorf("pdf = %q", pdfText)
	}
	if scanned := read("scan.pdf", "application/pdf", testPDF("")); !strings.Contains(scanned, "no extractable text") {
		t.Errorf("empty pdf = %q", scanned)
	}

	sheet := read("book.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", testXLSX(t))
	for _, want := range []string{
		"## Sales\n| Region | Revenue | Closed |\n| --- | --- | --- |\n| North America | 42.5 | TRUE |\n|  |  |  |\n| pipe \\| cell | 7 | FALSE |",
		"## Notes & Todo\n| Item |\n| --- |\n| Ship it |",
	} {
		if !strings.Contains(sheet, want) {
			t.Errorf("xlsx lacks %q:\n%s", want, sheet)
		}
	}
	if strings.Contains(sheet, "ignored") || strings.Contains(sheet, "A5") {
		t.Errorf("xlsx = %s", sheet)
	}
	// The same bytes are recognised under a wrong label.
	if mislabelled := read("book.bin", "application/octet-stream", testXLSX(t)); !strings.Contains(mislabelled, "## Sales") {
		t.Errorf("mislabelled xlsx = %s", mislabelled)
	}

	docx := read("memo.docx", "", testZip(t,
		"word/document.xml", `<w:document xmlns:w="w"><w:body>
<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Launch plan</w:t></w:r></w:p>
<w:p><w:r><w:t xml:space="preserve">Ship on </w:t></w:r><w:r><w:t>Friday.</w:t></w:r></w:p>
<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/></w:numPr></w:pPr><w:r><w:t>Tell everyone</w:t></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Owner</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Task</w:t></w:r></w:p></w:tc></w:tr><w:tr><w:tc><w:p><w:r><w:t>Ana</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Docs</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
</w:body></w:document>`))
	for _, want := range []string{"# Launch plan\n\nShip on Friday.\n\n- Tell everyone", "| Owner | Task |\n| --- | --- |\n| Ana | Docs |"} {
		if !strings.Contains(docx, want) {
			t.Errorf("docx lacks %q:\n%s", want, docx)
		}
	}

	pptx := read("deck.pptx", "", testZip(t,
		"ppt/presentation.xml", `<p/>`,
		"ppt/slides/slide2.xml", `<p:sld xmlns:a="a" xmlns:p="p"><a:p><a:r><a:t>Roadmap</a:t></a:r></a:p></p:sld>`,
		"ppt/slides/slide1.xml", `<p:sld xmlns:a="a" xmlns:p="p"><a:p><a:r><a:t>Welcome</a:t></a:r></a:p><a:p><a:r><a:t>Team</a:t></a:r></a:p></p:sld>`))
	if pptx != "Slide 1:\nWelcome\nTeam\n\nSlide 2:\nRoadmap" {
		t.Errorf("pptx = %q", pptx)
	}

	csv := read("data.csv", "text/csv", []byte("name,score\n\"Smith, Ann\",3\nBob,4\n"))
	if csv != "| name | score |\n| --- | --- |\n| Smith, Ann | 3 |\n| Bob | 4 |" {
		t.Errorf("csv = %q", csv)
	}
	if tsv := read("data.tsv", "", []byte("a\tb\n1\t2\n")); tsv != "| a | b |\n| --- | --- |\n| 1 | 2 |" {
		t.Errorf("tsv = %q", tsv)
	}
	if code := read("main.go", "", []byte("package main\n")); code != "package main\n" {
		t.Errorf("code = %q", code)
	}
	if typeless := read("LICENSE", "application/octet-stream", []byte("MIT License")); typeless != "MIT License" {
		t.Errorf("typeless text = %q", typeless)
	}
	for name, data := range map[string][]byte{"blob.bin": {0, 1, 2, 3}, "old.doc": []byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1rest"), "a.zip": testZip(t, "a.txt", "x")} {
		text := read(name, "application/octet-stream", data)
		if !strings.Contains(text, name) || !strings.Contains(text, "cannot be read") && !strings.Contains(text, "cannot be read as text") {
			t.Errorf("%s = %q", name, text)
		}
	}
}

func TestReadFileCapsOutputAndRefusesZipBombs(t *testing.T) {
	var big strings.Builder
	for range 6000 {
		big.WriteString("line of text that fills the budget quickly\n")
	}
	text, err := parseAgentFile("big.txt", "text/plain", []byte(big.String()))
	if err != nil || !strings.HasSuffix(text, "[truncated]") || len(text) > agentReadTextMax+len(agentReadTruncated) {
		t.Fatalf("text cap = %d bytes, err %v, tail %q", len(text), err, text[max(0, len(text)-20):])
	}

	var rows strings.Builder
	rows.WriteString("a,b\n")
	for index := range agentReadMaxRows + 50 {
		fmt.Fprintf(&rows, "%d,x\n", index)
	}
	csv, _ := parseAgentFile("rows.csv", "text/csv", []byte(rows.String()))
	if !strings.Contains(csv, "[truncated: only the first") || strings.Count(csv, "\n") > agentReadMaxRows+10 {
		t.Errorf("csv rows not capped: %d lines", strings.Count(csv, "\n"))
	}

	// A sheet that inflates past the entry limit is refused before it is parsed.
	bomb := testZip(t,
		"xl/workbook.xml", `<workbook xmlns:r="r"><sheets><sheet name="S" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels", `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml", `<worksheet><sheetData>`+strings.Repeat(" ", zipMaxEntryBytes+1)+`</sheetData></worksheet>`)
	if _, err := parseAgentFile("bomb.xlsx", "", bomb); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("zip bomb: err = %v", err)
	}
	// Too many entries.
	var many []string
	many = append(many, "word/document.xml", "<w/>")
	for index := range zipMaxEntries + 1 {
		many = append(many, fmt.Sprintf("f%d", index), "")
	}
	if _, err := parseAgentFile("many.docx", "", testZip(t, many...)); err == nil && zipFileKind(testZip(t, many...)) == "docx" {
		t.Error("an archive with too many entries was read")
	}
	// Garbage that claims to be a PDF does not crash.
	if _, err := parseAgentFile("bad.pdf", "application/pdf", []byte("%PDF-1.4 garbage")); err == nil {
		t.Error("broken pdf parsed")
	}
}

func TestAgentAttachmentReferenceNotes(t *testing.T) {
	pdf := domain.AgentMessageAttachment{Name: "report.pdf", ContentType: "application/pdf", URL: "/uploads/agent_upload_0123456789abcdef_1_report.pdf"}
	if got, want := agentAttachmentReference(pdf, 634, true), "Attached file report.pdf (application/pdf, 634 bytes) \u2014 id: /uploads/agent_upload_0123456789abcdef_1_report.pdf. Use read_file to read it."; got != want {
		t.Errorf("pdf note = %q", got)
	}
	video := domain.AgentMessageAttachment{Name: "demo.mp4", ContentType: "video/mp4", URL: "/uploads/agent_upload_0123456789abcdef_2_demo.mp4"}
	if got := agentAttachmentReference(video, 9, true); !strings.HasPrefix(got, "Attached video demo.mp4") || !strings.HasSuffix(got, "Use transcribe_media to get its content.") {
		t.Errorf("video note = %q", got)
	}
	if got := agentAttachmentReference(domain.AgentMessageAttachment{Name: "x.pdf", ContentType: "application/pdf"}, 3, true); !strings.Contains(got, "not kept") {
		t.Errorf("unstored note = %q", got)
	}
}

// fileToolSession starts a chat with the given uploads and returns the server pieces and the session id.
type fileToolChat struct {
	srv      *server
	handler  http.Handler
	host     *httptest.Server
	provider *fakeLoopProvider
}

func newFileToolChat(t *testing.T) *fileToolChat {
	t.Helper()
	provider := &fakeLoopProvider{}
	srv, handler := newLoopTestServer(t, provider)
	host := httptest.NewServer(handler)
	t.Cleanup(host.Close)
	return &fileToolChat{srv: srv, handler: handler, host: host, provider: provider}
}

func (c *fileToolChat) upload(t *testing.T, name, contentType string, content []byte) domain.AgentMessageAttachment {
	t.Helper()
	body, formType := agentUploadFormType(t, name, contentType, content)
	request := httptest.NewRequest(http.MethodPost, "/api/agent/uploads", body)
	request.Header.Set("Content-Type", formType)
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload %s = %d %s", name, recorder.Code, recorder.Body.String())
	}
	var uploaded domain.AgentMessageAttachment
	_ = json.Unmarshal(recorder.Body.Bytes(), &uploaded)
	return uploaded
}

func fileCard(file domain.AgentMessageAttachment) string {
	return fmt.Sprintf(`::file[%s](%s){size="%d" type="%s"}`, file.Name, file.URL, file.Size, file.ContentType)
}

// ask sends a message and returns the session once the reply finished.
func (c *fileToolChat) ask(t *testing.T, message string) domain.AgentSession {
	t.Helper()
	stream := streamAgent(t, c.srv, c.host, "/api/agent/sessions/stream", map[string]any{"message": message, "location": "page"}, nil)
	sessionID := regexp.MustCompile(`"id":"(agent_session_[0-9a-z_]+)"`).FindStringSubmatch(stream)
	if sessionID == nil {
		t.Fatalf("no session in stream: %s", stream)
	}
	return requestJSON[domain.AgentSession](t, c.handler, http.MethodGet, "/api/agent/sessions/"+sessionID[1], nil, http.StatusOK)
}

func toolCalls(session domain.AgentSession) []domain.AgentToolCall {
	var calls []domain.AgentToolCall
	for _, message := range session.Messages {
		for _, part := range message.Parts {
			if part.ToolCall != nil {
				calls = append(calls, *part.ToolCall)
			}
		}
	}
	return calls
}

func (c *fileToolChat) inputs() []string {
	c.provider.mu.Lock()
	defer c.provider.mu.Unlock()
	return append([]string(nil), c.provider.inputs...)
}

func TestAgentChatReadsAttachedFilesThroughTools(t *testing.T) {
	chat := newFileToolChat(t)
	pdf := chat.upload(t, "report.pdf", "application/pdf", testPDF("FLOW PARITY TEST: quarterly revenue was 42 million dollars."))
	notes := chat.upload(t, "notes.txt", "text/plain", []byte("SECRET-NOTES-CONTENT"))
	sheet := chat.upload(t, "sales.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", testXLSX(t))
	other := chat.upload(t, "other.pdf", "application/pdf", testPDF("NOT IN THIS CHAT"))
	chat.provider.replies = []string{
		`tool:read_file {"file":"` + pdf.URL + `"}`,
		`tool:read_file {"file":"sales.xlsx"}`,
		`tool:read_file {"file":"` + other.URL + `"}`,
		"The revenue was 42 million.",
	}
	session := chat.ask(t, fileCard(pdf)+"\n"+fileCard(notes)+"\n"+fileCard(sheet)+"\n\nWhat was the revenue?")

	inputs := chat.inputs()
	first := inputs[0]
	for _, want := range []string{
		"Attached file report.pdf (application/pdf, ", "id: " + pdf.URL + ". Use read_file to read it.",
		"Attached file notes.txt (text/plain, 20 bytes)", "Attached file sales.xlsx",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("model input lacks %q: %s", want, first)
		}
	}
	if strings.Contains(first, "SECRET-NOTES-CONTENT") {
		t.Errorf("file contents were inlined: %s", first)
	}
	chat.provider.mu.Lock()
	offered := chat.provider.tools[0]
	chat.provider.mu.Unlock()
	if !slices.Contains(offered, readFileTool) || slices.Contains(offered, transcribeMediaTool) {
		t.Errorf("tools offered = %v", offered)
	}

	calls := toolCalls(session)
	if len(calls) != 3 || calls[0].Name != readFileTool || calls[0].Title != "Read file report.pdf" || calls[0].Status != "completed" || calls[1].Title != "Read file sales.xlsx" {
		t.Fatalf("tool calls = %#v", calls)
	}
	if !strings.Contains(string(calls[0].Result), "Page 1:\\nFLOW PARITY TEST: quarterly revenue was 42 million dollars.") {
		t.Errorf("pdf result = %s", calls[0].Result)
	}
	if !strings.Contains(string(calls[1].Result), "## Sales") || !strings.Contains(string(calls[1].Result), "42.5") {
		t.Errorf("xlsx result = %s", calls[1].Result)
	}
	// A file uploaded by the same user but not sent in this chat cannot be opened.
	if calls[2].Status != "error" || !strings.Contains(calls[2].Error, "no attachment in this conversation") {
		t.Errorf("foreign file call = %#v", calls[2])
	}
	if strings.Contains(chat.inputs()[3], "NOT IN THIS CHAT") {
		t.Error("a file outside the session reached the model")
	}
	if !strings.Contains(chat.inputs()[1], "FLOW PARITY TEST") {
		t.Errorf("tool result did not reach the model: %s", chat.inputs()[1])
	}
}

func TestAgentChatWithoutFilesDoesNotOfferFileTools(t *testing.T) {
	chat := newFileToolChat(t)
	png := chat.upload(t, "shot.png", "image/png", []byte("\x89PNG\r\n\x1a\nshot"))
	chat.provider.replies = []string{"Looks fine."}
	chat.ask(t, "![shot.png]("+png.URL+")\nthoughts?")
	chat.provider.mu.Lock()
	offered := chat.provider.tools[0]
	chat.provider.mu.Unlock()
	if slices.Contains(offered, readFileTool) || slices.Contains(offered, transcribeMediaTool) {
		t.Errorf("file tools offered for an image-only chat: %v", offered)
	}
	if !strings.Contains(chat.inputs()[0], "Attached image shot.png (shown below).") {
		t.Errorf("image note missing: %s", chat.inputs()[0])
	}
}

func TestTranscribeMediaTool(t *testing.T) {
	var audioRequests, videoRequests int
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer media-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/audio/transcriptions":
			audioRequests++
			if err := r.ParseMultipartForm(1 << 20); err != nil || r.FormValue("model") != "whisper-test" || r.FormValue("response_format") != "verbose_json" {
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			file, header, err := r.FormFile("file")
			if err != nil || header.Filename != "call.wav" {
				http.Error(w, "no file", http.StatusBadRequest)
				return
			}
			data, _ := io.ReadAll(file)
			if string(data) != "RIFFfake-audio" {
				http.Error(w, "wrong bytes", http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"text":"Hello. Goodbye.","segments":[{"start":0.2,"end":2,"text":" (phone ringing)"},{"start":65.7,"end":70,"text":" Hello, this is Ana."}]}`)
		case "/v1/chat/completions":
			videoRequests++
			var payload struct {
				Model    string `json:"model"`
				Messages []struct {
					Content []map[string]any `json:"content"`
				} `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			url := ""
			if len(payload.Messages) == 1 && len(payload.Messages[0].Content) == 2 {
				if video, ok := payload.Messages[0].Content[1]["video_url"].(map[string]any); ok {
					url, _ = video["url"].(string)
				}
			}
			if payload.Model != "video-test" || !strings.HasPrefix(url, "data:video/mp4;base64,") {
				http.Error(w, "bad video request", http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"[00:00] A dark navy screen with bold yellow text: GREEN TIGER"}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(media.Close)

	chat := newFileToolChat(t)
	wav := chat.upload(t, "call.wav", "", []byte("RIFFfake-audio")) // no MIME type: accepted like a plain file
	mp4 := chat.upload(t, "demo.mp4", "video/mp4", []byte("\x00\x00\x00 ftypmp42video"))
	message := fileCard(wav) + "\n::video[demo.mp4](" + mp4.URL + ")\n\nWhat is in these?"

	// Not configured: a normal result, not a failed turn.
	chat.provider.replies = []string{`tool:transcribe_media {"file":"call.wav"}`, "Transcription is off."}
	session := chat.ask(t, message)
	calls := toolCalls(session)
	if len(calls) != 1 || calls[0].Status != "completed" || calls[0].Title != "Transcribed media" || !strings.Contains(string(calls[0].Result), "Media transcription is not enabled on this server.") {
		t.Fatalf("unconfigured call = %#v", calls)
	}
	chat.provider.mu.Lock()
	offered := chat.provider.tools[0]
	chat.provider.mu.Unlock()
	if !slices.Contains(offered, transcribeMediaTool) || slices.Contains(offered, readFileTool) {
		t.Errorf("tools offered = %v", offered)
	}
	if first := chat.inputs()[0]; !strings.Contains(first, "Attached audio call.wav") || !strings.Contains(first, "Attached video demo.mp4") || !strings.Contains(first, "Use transcribe_media") {
		t.Errorf("media notes missing: %s", first)
	}

	chat.srv.agent.Media = appconfig.AgentMediaConfig{BaseURL: media.URL + "/v1", APIKey: "media-key", TranscribeModel: "whisper-test", VideoModel: "video-test"}
	chat.provider.replies = []string{`tool:transcribe_media {"file":"call.wav"}`, `tool:transcribe_media {"file":"demo.mp4"}`, `tool:read_file {"file":"call.wav"}`, "Done."}
	session = chat.ask(t, message)
	calls = toolCalls(session)
	if len(calls) != 3 || audioRequests != 1 || videoRequests != 1 {
		t.Fatalf("calls = %#v audio %d video %d", calls, audioRequests, videoRequests)
	}
	if !strings.Contains(string(calls[0].Result), "[00:00] (phone ringing)\\n[01:05] Hello, this is Ana.") {
		t.Errorf("audio result = %s", calls[0].Result)
	}
	if !strings.Contains(string(calls[1].Result), "GREEN TIGER") {
		t.Errorf("video result = %s", calls[1].Result)
	}
	if !strings.Contains(string(calls[2].Result), "Use transcribe_media") {
		t.Errorf("read_file on audio = %s", calls[2].Result)
	}

	// A provider error is an error result of the call, not of the turn.
	chat.srv.agent.Media.APIKey = "wrong"
	chat.provider.replies = []string{`tool:transcribe_media {"file":"call.wav"}`, "Could not transcribe."}
	session = chat.ask(t, message)
	if calls = toolCalls(session); len(calls) != 1 || calls[0].Status != "completed" || !strings.Contains(string(calls[0].Result), "Could not read call.wav") {
		t.Errorf("failed transcription = %#v", calls)
	}
}

func TestAgentMediaTimestamp(t *testing.T) {
	for seconds, want := range map[float64]string{0: "00:00", 59.9: "00:59", 65.7: "01:05", 3725: "1:02:05", -3: "00:00"} {
		if got := agentMediaTimestamp(seconds); got != want {
			t.Errorf("agentMediaTimestamp(%v) = %q, want %q", seconds, got, want)
		}
	}
}

func TestParseAgentFileWithinBoundsConcurrentParses(t *testing.T) {
	for i := 0; i < cap(agentFileParseSlots); i++ {
		agentFileParseSlots <- struct{}{}
	}
	file := domain.AgentMessageAttachment{Name: "a.txt", ContentType: "text/plain"}
	_, err := parseAgentFileWithin(context.Background(), file, []byte("hi"))
	for i := 0; i < cap(agentFileParseSlots); i++ {
		<-agentFileParseSlots
	}
	if err == nil || !strings.Contains(err.Error(), "too many files") {
		t.Fatalf("full slots error = %v", err)
	}
	if text, err := parseAgentFileWithin(context.Background(), file, []byte("hi")); err != nil || text != "hi" {
		t.Fatalf("after release = %q, %v", text, err)
	}
	if len(agentFileParseSlots) != 0 {
		t.Errorf("slots leaked: %d", len(agentFileParseSlots))
	}
}

func TestDescribeVideoErrorPaths(t *testing.T) {
	for name, body := range map[string]string{
		"upstream error": "ERR",
		"unexpected":     `{"nothing":true}`,
		"oversized":      `{"choices":[{"message":{"content":"` + strings.Repeat("a", 20<<20) + `"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if body == "ERR" {
					http.Error(w, "boom", http.StatusBadGateway)
					return
				}
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(media.Close)
			chat := newFileToolChat(t)
			mp4 := chat.upload(t, "demo.mp4", "video/mp4", []byte("\x00\x00\x00 ftypmp42video"))
			chat.srv.agent.Media = appconfig.AgentMediaConfig{BaseURL: media.URL + "/v1", APIKey: "k", VideoModel: "video-test"}
			chat.provider.replies = []string{`tool:transcribe_media {"file":"demo.mp4"}`, "Could not."}
			session := chat.ask(t, "::video[demo.mp4]("+mp4.URL+")\n\nWhat is in this?")
			calls := toolCalls(session)
			if len(calls) != 1 || calls[0].Status != "completed" || !strings.Contains(string(calls[0].Result), "Could not read demo.mp4") {
				t.Errorf("calls = %#v", calls)
			}
		})
	}
}
