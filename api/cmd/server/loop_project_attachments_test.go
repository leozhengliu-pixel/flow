package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func loopAttachmentUploadRequest(t *testing.T, parentPath, name string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, parentPath+"/attachments", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	return req
}

func uploadLoopProjectAttachment(t *testing.T, handler http.Handler, parentPath, name string, data []byte) domain.Attachment {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, loopAttachmentUploadRequest(t, parentPath, name, data))
	var attachment domain.Attachment
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &attachment) != nil || attachment.ID == "" {
		t.Fatalf("upload fixture: status %d, missing attachment", response.Code)
	}
	return attachment
}

func loopAttachmentTestDOCX(t *testing.T, text string) []byte {
	t.Helper()
	return testZip(t, "word/document.xml", `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>`+text+`</w:t></w:r></w:p></w:body></w:document>`)
}

func loopAttachmentTestJPEG(t *testing.T) []byte {
	t.Helper()
	// Render the number 13 in pixels so the fixture contains the image's evidence.
	rows := []string{" ##  ### ", "  #    # ", "  #  ### ", "  #    # ", " ### ### "}
	picture := image.NewGray(image.Rect(0, 0, len(rows[0])*8, len(rows)*8))
	for y := 0; y < picture.Bounds().Dy(); y++ {
		for x := 0; x < picture.Bounds().Dx(); x++ {
			if rows[y/8][x/8] != '#' {
				picture.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	var data bytes.Buffer
	if err := jpeg.Encode(&data, picture, nil); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestLoopReadsProjectAttachments(t *testing.T) {
	const wordText = "The acceptance threshold is 17 units"
	const imageText = "The image shows the number 13"
	docx := loopAttachmentTestDOCX(t, wordText)
	jpg := loopAttachmentTestJPEG(t)
	dataURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpg)
	for _, capability := range []string{"image-capable", "text-only"} {
		t.Run(capability, func(t *testing.T) {
			var imageRequests atomic.Int32
			provider := &fakeLoopProvider{}
			srv, handler := newLoopTestServerWithProvider(t, func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error("could not read fixture model request")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(raw))
				if !bytes.Contains(raw, []byte(`"type":"input_image"`)) {
					provider.serve(w, r)
					return
				}
				imageRequests.Add(1)
				if !bytes.Contains(raw, []byte(dataURL)) {
					t.Error("provider did not receive the stored JPG bytes")
				}
				if bytes.Contains(raw, []byte(`"tools"`)) {
					t.Error("the image inspection request offered tools")
				}
				if capability == "text-only" {
					http.Error(w, `{"error":{"message":"image input is unsupported: private-provider-payload"}}`, http.StatusBadRequest)
					return
				}
				(&fakeLoopProvider{replies: []string{imageText}}).serve(w, r)
			})
			bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
			project := bootstrap.Projects[0]
			document := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{
				"title": "Release evidence", "content": "Evidence is in the files.", "projectIds": []string{project.ID}, "teamIds": project.TeamIDs,
			}, http.StatusCreated)
			parent := "/api/documents/" + document.ID
			chart := uploadLoopProjectAttachment(t, handler, parent, "chart.jpg", jpg)
			missing := uploadLoopProjectAttachment(t, handler, parent, "missing.docx", docx)
			word := uploadLoopProjectAttachment(t, handler, parent, "evidence.docx", docx)
			storage, err := srv.storage()
			if err != nil {
				t.Fatal(err)
			}
			if err := storage.Delete(t.Context(), uploadNameFromURL(missing.URL)); err != nil {
				t.Fatal(err)
			}
			call := func(name string, args map[string]string) string {
				raw, _ := json.Marshal(args)
				return "tool:" + name + " " + string(raw)
			}
			args := func(attachment domain.Attachment) map[string]string {
				return map[string]string{"document": document.ID, "attachmentId": attachment.ID}
			}
			imageFinding := imageText
			if capability == "text-only" {
				imageFinding = "Image input is unavailable"
			}
			provider.script(
				call("get_document", map[string]string{"id": document.ID}),
				call("extract_images", args(chart)),
				call("read_file", args(missing)),
				call("read_file", args(word)),
				call(loopFinishToolName, map[string]string{"status": "incomplete", "summary": "evidence.docx: " + wordText + "; chart.jpg: " + imageFinding + "; missing.docx: could not read."}),
			)
			loop, started := startTestRun(t, handler, "Audit release evidence", "Quote the evidence in the document's attachments. Report any files that could not be inspected and continue with the rest.", map[string]any{
				"teamId": project.TeamIDs[0], "teamAccess": "selected",
			})
			finished := waitForRun(t, handler, loop.ID, started.ID)
			if finished.Summary == nil || finished.Status == "failed" || !strings.Contains(finished.Output, wordText) {
				t.Errorf("loop did not finish with its partial report (status %s)", finished.Status)
			}
			provider.mu.Lock()
			defer provider.mu.Unlock()
			if len(provider.inputs) != 5 {
				t.Fatalf("got %d Loop turns, want 5", len(provider.inputs))
			}
			if !slices.Contains(provider.tools[0], "read_file") {
				t.Error("Loop was not offered read_file")
			}
			if !strings.Contains(provider.inputs[1], chart.ID) || !strings.Contains(provider.inputs[1], word.ID) {
				t.Error("get_document did not identify the attachments")
			}
			result := provider.inputs[4]
			for _, want := range []string{wordText, word.ID, word.Title, imageFinding, chart.ID, chart.Title, "Could not read", missing.ID, missing.Title} {
				if !strings.Contains(result, want) {
					t.Errorf("Loop did not receive attachment evidence or limitation %q", want)
				}
			}
			if strings.Contains(result, "private-provider-payload") || strings.Contains(result, dataURL) {
				t.Error("vision request data leaked into the Loop's text history")
			}
			if n := imageRequests.Load(); n != 1 {
				t.Errorf("received %d provider requests with the JPG, want 1", n)
			}
		})
	}
}

func TestWorkerLoopAttachmentImageHandoff(t *testing.T) {
	h := newWorkerLoopHarness(t)
	data := requestJSON[domain.Bootstrap](t, h.handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	team := data.Teams[0].ID
	const signed = "https://flow.test/api/files/signed/chart.jpg?expires=1&signature=fixture"
	const presigned = "https://storage.test/chart.jpg?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Signature=fixture"
	const object = "https://fixture.s3.amazonaws.com/chart.jpg"
	const public = "https://example.test/chart.jpg"
	doc := requestJSON[domain.Document](t, h.handler, http.MethodPost, "/api/documents", map[string]any{
		"title": "Audit", "teamIds": []string{team}, "content": "![chart](" + signed + ")",
	}, http.StatusCreated)
	parent := "/api/documents/" + doc.ID
	jpg := loopAttachmentTestJPEG(t)
	image := uploadLoopProjectAttachment(t, h.handler, parent, "chart.jpg", jpg)
	broken := uploadLoopProjectAttachment(t, h.handler, parent, "broken.docx", []byte("invalid Word file"))
	issue := requestJSON[domain.Issue](t, h.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Audit", "teamId": team}, http.StatusCreated)
	const quote = "Measure 23 units"
	word := uploadLoopProjectAttachment(t, h.handler, "/api/issues/"+issue.ID, "measure.docx", loopAttachmentTestDOCX(t, quote))
	loop, started := startTestRun(t, h.handler, "Attachment audit", "Report attachment evidence.", map[string]any{"teamId": team, "teamAccess": "selected"})
	run := h.claim(t)
	token := h.call(t, run, "/token", map[string]any{}, http.StatusCreated)["token"].(string)
	if tools := listedRunTools(t, h.handler, token); tools[readFileTool] == nil || tools[loopImageTool] == nil {
		t.Fatal("attachment tools missing from worker Loop")
	}

	args := map[string]any{"document": doc.ID, "attachmentId": image.ID}
	fallback := callRunTool(t, h.handler, token, loopImageTool, args, nil)
	optedIn := callRunTool(t, h.handler, token, loopImageTool, args, map[string]any{"flow/inspect-image": true})
	receipt := runToolText(fallback)
	if optedIn["isError"] == true || strings.Contains(receipt, "/uploads/") || runToolText(optedIn) != receipt {
		t.Fatal("expected the same text receipt with and without opt-in")
	}
	var file loopAttachmentResult
	if err := json.Unmarshal([]byte(receipt), &file); err != nil || file.Status != "unavailable" || file.AttachmentID != image.ID || file.File != image.Title {
		t.Fatal("image fallback did not identify the attachment")
	}
	plain, _ := fallback["content"].([]any)
	blocks, _ := optedIn["content"].([]any)
	if len(plain) != 1 || len(blocks) != 2 {
		t.Fatal("image bytes should be returned only to an opted-in worker")
	}
	block, _ := blocks[1].(map[string]any)
	if block["type"] != "image" || block["mimeType"] != "image/jpeg" || block["data"] != base64.StdEncoding.EncodeToString(jpg) {
		t.Fatal("handoff did not contain the original JPEG")
	}

	documentText := runToolText(callRunTool(t, h.handler, token, "get_document", map[string]any{"id": doc.ID}, nil))
	if !strings.Contains(documentText, image.ID) || strings.Contains(documentText, "signature=fixture") {
		t.Fatal("document response lost the attachment ID or exposed a file signature")
	}
	markdown := "![a](" + presigned + ") ![b](" + public + ") ![c](" + object + ")"
	references := runToolText(callRunTool(t, h.handler, token, loopImageTool, map[string]any{"markdown": markdown}, nil))
	if strings.Contains(references, "storage.test") || strings.Contains(references, object) || !strings.Contains(references, public) {
		t.Fatal("legacy image references should omit file capabilities and retain ordinary URLs")
	}
	for _, tc := range []struct {
		attachment   domain.Attachment
		status, text string
	}{
		{broken, "unreadable", "Could not read this attachment as a Word document"},
		{image, "unsupported", "Use extract_images"},
	} {
		result := callRunTool(t, h.handler, token, readFileTool, map[string]any{"document": doc.ID, "attachmentId": tc.attachment.ID}, nil)
		if err := json.Unmarshal([]byte(runToolText(result)), &file); err != nil || result["isError"] == true || file.Status != tc.status || file.AttachmentID != tc.attachment.ID || file.File != tc.attachment.Title || !strings.Contains(file.Text, tc.text) {
			t.Fatalf("expected an attachment receipt with status %s", tc.status)
		}
	}
	wordResult := callRunTool(t, h.handler, token, readFileTool, map[string]any{"issue": issue.ID, "attachmentId": word.ID}, nil)
	if err := json.Unmarshal([]byte(runToolText(wordResult)), &file); err != nil || file.Status != "read" || file.AttachmentID != word.ID || file.File != word.Title || !strings.Contains(file.Text, quote) {
		t.Fatal("worker Loop did not receive the issue attachment text")
	}
	callRunTool(t, h.handler, token, loopFinishToolName, map[string]any{"status": "done", "summary": "Completed the attachment audit."}, nil)
	if finish := h.guard(t, run, map[string]any{"op": "finish", "phase": "reply"}); !strings.Contains(finish.Reply, "Completed the attachment audit.") {
		t.Fatal("the worker did not return its finished audit")
	}
	h.call(t, run, "/complete", map[string]any{"status": "done"}, http.StatusOK)
	finished := waitForRun(t, h.handler, loop.ID, started.ID)
	if finished.Status != "completed" || finished.Produced["change"] != 0 {
		t.Fatalf("attachment tools were not read-only (status %s)", finished.Status)
	}
}

func TestLoopAttachmentReadRespectsScopeAndTriggeringUser(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	fixture, _ := newLoopTestServer(t, &fakeLoopProvider{})
	srv := &server{store: fixture.store, uploadPath: fixture.uploadPath}
	disableBackgroundWorkersForTest(srv)
	api := httptest.NewServer(newHandler(srv))
	defer api.Close()
	const workspace = "test-workspace"
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, api.URL+"/api/bootstrap", nil, workspace, http.StatusOK)
	home := bootstrap.Teams[0]
	other := authRequest[domain.Team](t, admin, http.MethodPost, api.URL+"/api/workspaces/"+workspace+"/teams", map[string]any{"name": "Other", "key": "OTH"}, "", http.StatusCreated)
	member, user := verifiedAuthClient(t, api.URL, "Loop member", "loop-attachment-member@example.com")
	invites := authRequest[[]domain.Invitation](t, admin, http.MethodPost, api.URL+"/api/workspaces/"+workspace+"/invitations", map[string]any{"emails": []string{user.Email}, "role": "member", "teamIds": []string{home.ID}}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, member, http.MethodPost, api.URL+"/api/invitations/accept", map[string]string{"token": invites[0].Token}, "", http.StatusOK)

	makeFile := func(team string) (string, domain.Attachment) {
		t.Helper()
		input := map[string]any{"title": "Attachment ACL"}
		if team != "" {
			input["teamIds"] = []string{team}
		}
		doc := authRequest[domain.Document](t, admin, http.MethodPost, api.URL+"/api/documents", input, workspace, http.StatusCreated)
		request := loopAttachmentUploadRequest(t, "/api/documents/"+doc.ID, "private.txt", []byte("Authorized evidence."))
		upload, err := http.NewRequest(request.Method, api.URL+request.URL.Path, request.Body)
		if err != nil {
			t.Fatal(err)
		}
		upload.Header = request.Header
		upload.Header.Set("X-Workspace-Key", workspace)
		response, err := admin.Do(upload)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var attachment domain.Attachment
		if response.StatusCode != http.StatusCreated || json.NewDecoder(response.Body).Decode(&attachment) != nil || attachment.ID == "" {
			t.Fatalf("upload fixture: status %d, missing attachment", response.StatusCode)
		}
		return doc.ID, attachment
	}
	parent, file := makeFile("")
	teamParent, teamFile := makeFile(home.ID)
	outside, outsideFile := makeFile(other.ID)
	// Remove the workspace grant. The Loop owner can still read it, but the
	// member who can use the team's Loop cannot open its private attachment.
	authRequest[[]domain.DocumentPermission](t, admin, http.MethodPut, api.URL+"/api/documents/"+parent+"/permissions", map[string]any{"permissions": []any{}}, workspace, http.StatusOK)
	response, err := member.Get(api.URL + file.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("private download status = %d, want 404", response.StatusCode)
	}
	loop := domain.Loop{OwnerID: bootstrap.Viewer.ID, TeamID: home.ID, TeamAccess: "selected"}
	for _, tc := range []struct {
		name, parent, id, manual, event, reply, editor string
		read                                           bool
	}{
		{name: "owner", parent: parent, id: file.ID, manual: loop.OwnerID, read: true},
		{name: "team member", parent: teamParent, id: teamFile.ID, manual: user.ID, read: true},
		{name: "manual trigger", parent: parent, id: file.ID, manual: user.ID},
		{name: "event trigger", parent: parent, id: file.ID, event: user.ID},
		{name: "follow-up trigger", parent: parent, id: file.ID, manual: loop.OwnerID, reply: user.ID},
		{name: "behavior editor", parent: parent, id: file.ID, manual: loop.OwnerID, editor: user.ID},
		{name: "out of scope", parent: outside, id: outsideFile.ID, manual: loop.OwnerID},
		{name: "different parent", parent: teamParent, id: file.ID, manual: loop.OwnerID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trigger := loopTrigger{ActorID: tc.event}
			info := workerLoopRunInfo{LoopRunID: "fixture", Trigger: trigger}
			payload, _ := json.Marshal(map[string]any{"loop": info})
			queued, ok := workerLoopInfo(store.AgentRun{Kind: store.AgentRunKindLoop, Payload: payload})
			if !ok || queued.Trigger.ActorID != tc.event {
				t.Fatal("worker payload lost the event identity")
			}
			rec := &loopRunRecorder{run: domain.LoopRun{ActorID: tc.manual}}
			if tc.reply != "" {
				rec.reply = &domain.LoopRunReply{UserID: tc.reply}
			}
			loop := loop
			loop.BehaviorEditorID = tc.editor
			lc := loopRunContext{s: srv, workspace: workspace, data: bootstrap, loop: loop, scope: srv.newLoopScope(bootstrap, loop, trigger), trigger: queued.Trigger, rec: rec}
			got, image := lc.readWorkspaceAttachment(t.Context(), readFileTool, map[string]any{"document": tc.parent, "attachmentId": tc.id})
			if tc.read {
				if got.Status != "read" || got.AttachmentID != tc.id || got.Text != "Authorized evidence." {
					t.Fatal("authorized attachment was not readable")
				}
			} else if got.Status != "unreadable" || got.File != "" || got.AttachmentID != "" || image != nil {
				t.Fatal("unauthorized attachment returned metadata or bytes")
			}
		})
	}
}
