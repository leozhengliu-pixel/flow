package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
)

func TestEncodeDirectiveURL(t *testing.T) {
	cases := map[string]string{
		"/uploads/attachment_1_notes.txt":             "/uploads/attachment_1_notes.txt",
		"/uploads/attachment_1_测试 report.docx":        "/uploads/attachment_1_测试%20report.docx",
		"/uploads/attachment_1_测试报告.docx":             "/uploads/attachment_1_测试报告.docx",
		"/uploads/attachment_1_report (1).docx":       "/uploads/attachment_1_report%20%281%29.docx",
		"/uploads/attachment_1_100% done.pdf":         "/uploads/attachment_1_100%25%20done.pdf",
		"/uploads/attachment_1_a%20b.pdf":             "/uploads/attachment_1_a%20b.pdf",
		"/uploads/attachment_1_a%2.pdf":               "/uploads/attachment_1_a%252.pdf",
		"/uploads/attachment_1_end%":                  "/uploads/attachment_1_end%25",
		"/uploads/attachment_1_tab\tnew\nline.txt":    "/uploads/attachment_1_tab%09new%0Aline.txt",
		"/uploads/attachment_1_全角　空格.txt":             "/uploads/attachment_1_全角%E3%80%80空格.txt",
		"/uploads/attachment_1_nb sp.txt":             "/uploads/attachment_1_nb%C2%A0sp.txt",
		"/uploads/attachment_1_<a>`b`\\c.txt":         "/uploads/attachment_1_%3Ca%3E%60b%60%5Cc.txt",
		"/uploads/attachment_1_[x] {y} \"q\" é#?.txt": "/uploads/attachment_1_[x]%20{y}%20\"q\"%20é#?.txt",
		"https://cdn.example.test/clip.mp4?w=1&h=2":   "https://cdn.example.test/clip.mp4?w=1&h=2",
		"/uploads/attachment_1_bad\xffbyte.bin":       "/uploads/attachment_1_bad%FFbyte.bin",
		"":                                            "",
	}
	for raw, want := range cases {
		got := encodeDirectiveURL(raw)
		if got != want {
			t.Errorf("encodeDirectiveURL(%q) = %q, want %q", raw, got, want)
		}
		if again := encodeDirectiveURL(got); again != got {
			t.Errorf("encodeDirectiveURL is not idempotent for %q: %q -> %q", raw, got, again)
		}
		if strings.ContainsAny(got, " \t\n()") {
			t.Errorf("encoded %q still carries a character the tokenizer splits on", got)
		}
	}
	// The comparable key of the encoded URL is the raw attachment URL.
	for _, raw := range []string{"/uploads/attachment_1_测试 report.docx", "/uploads/attachment_1_report (1).docx", "/uploads/attachment_1_100% done.pdf"} {
		if key := directiveURLKey(encodeDirectiveURL(raw)); key != raw || directiveURLKey(raw) != raw {
			t.Errorf("directiveURLKey(%q) = %q", raw, key)
		}
	}
	if !containsAttachmentURL("::file[a b.docx](/uploads/x_a%20b.docx)", "/uploads/x_a b.docx") || containsAttachmentURL("body", "") {
		t.Fatal("containsAttachmentURL must see the encoded form and ignore an empty URL")
	}
}

func TestAttachmentEmbedMarkdownEncodesTheURLOnly(t *testing.T) {
	cases := []struct {
		attachment domain.Attachment
		want       string
	}{
		{domain.Attachment{Title: "测试 report.docx", URL: "/uploads/attachment_1_测试 report.docx", ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", Size: 42},
			`::file[测试 report.docx](/uploads/attachment_1_测试%20report.docx){size="42" type="application/vnd.openxmlformats-officedocument.wordprocessingml.document"}`},
		{domain.Attachment{Title: "report (1).docx", URL: "/uploads/attachment_2_report (1).docx", ContentType: `text/plain; charset="utf-8"`, Size: 7},
			`::file[report (1).docx](/uploads/attachment_2_report%20%281%29.docx){size="7" type="text/plain; charset=utf-8"}`},
		{domain.Attachment{Title: "my clip.mp4", URL: "/uploads/attachment_3_my clip.mp4", ContentType: "video/mp4"},
			`::video[my clip.mp4](/uploads/attachment_3_my%20clip.mp4)`},
		{domain.Attachment{Title: "shot [final] (2).png", URL: "/uploads/attachment_4_shot [final] (2).png", ContentType: "image/png"},
			`![shot final (2).png](/uploads/attachment_4_shot%20[final]%20%282%29.png)`},
		{domain.Attachment{Title: "already.pdf", URL: "/uploads/attachment_5_a%20b.pdf", ContentType: "application/pdf", Size: 1},
			`::file[already.pdf](/uploads/attachment_5_a%20b.pdf){size="1" type="application/pdf"}`},
	}
	for _, item := range cases {
		if got := attachmentEmbedMarkdown(item.attachment); got != item.want {
			t.Errorf("attachmentEmbedMarkdown(%q) = %q, want %q", item.attachment.URL, got, item.want)
		}
	}
	if got := attachmentMarkdownLink(domain.Attachment{Title: "a b.pdf", URL: "/uploads/x_a b (1).pdf", ContentType: "application/pdf"}); got != "[a b.pdf](/uploads/x_a%20b%20%281%29.pdf)" {
		t.Fatalf("attachmentMarkdownLink = %q", got)
	}
}

// Uploading files whose names hold a space or parentheses stores directives the
// editor reads as file cards, keeps the attachment's name, URL and storage key,
// and the directive URL downloads the file under the usual visibility checks.
func TestHTTPAttachmentDirectiveURLDownloads(t *testing.T) {
	e := newSurfaceEnv(t, appconfig.CSPEnforce, nil)
	directive := regexp.MustCompile(`::file\[([^\]]*)]\(([^)\s]+)\)\{size="([^"]*)" type="([^"]*)"\}`)
	files := []struct{ name, content string }{{"测试 report.docx", "first file"}, {"report (1).docx", "second file"}}
	attachments := make([]domain.Attachment, 0, len(files))
	for _, file := range files {
		attachment := e.uploadFile(file.name, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", []byte(file.content))
		if attachment.Title != file.name || !strings.HasSuffix(attachment.URL, "_"+file.name) || !strings.HasPrefix(attachment.URL, "/uploads/"+attachment.ID+"_") {
			t.Fatalf("the attachment must keep its original name and raw URL: %+v", attachment)
		}
		attachments = append(attachments, attachment)
	}
	response, raw := e.admins(http.MethodGet, "/api/issue-records/"+e.issueID, nil, nil)
	e.expect("issue", response, http.StatusOK, kindAPI)
	var issue domain.Issue
	if err := json.Unmarshal(raw, &issue); err != nil {
		t.Fatal(err)
	}
	matches := directive.FindAllStringSubmatch(issue.Description, -1)
	if len(matches) != len(files) {
		t.Fatalf("want %d file directives the tokenizer accepts, got %d in %q", len(files), len(matches), issue.Description)
	}
	for index, match := range matches {
		attachment := attachments[index]
		if match[1] != files[index].name || match[2] != encodeDirectiveURL(attachment.URL) || directiveURLKey(match[2]) != attachment.URL {
			t.Fatalf("directive %d = %q for %+v", index, match[0], attachment)
		}
		download, body := e.admins(http.MethodGet, match[2], nil, nil)
		if download.StatusCode != http.StatusOK || string(body) != files[index].content {
			t.Fatalf("GET %s = %d %q", match[2], download.StatusCode, body)
		}
		anonymous, _ := e.do(surfaceRequest{path: match[2]})
		if anonymous.StatusCode != http.StatusUnauthorized {
			t.Fatalf("GET %s without a session = %d", match[2], anonymous.StatusCode)
		}
	}
	// The same upload never embeds twice once its encoded directive is present.
	before := issue.Description
	if err := applyIssueAttachmentEmbed(&domain.Bootstrap{}, &issue, attachments[0]); err != nil || issue.Description != before {
		t.Fatalf("an encoded directive must count as already embedded: %v %q", err, issue.Description)
	}
}
