package main

import (
	"bytes"
	"html/template"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestSMTPMailerConfigurationRequiresHostAndSender(t *testing.T) {
	t.Setenv("FLOW_SMTP_HOST", "")
	t.Setenv("FLOW_SMTP_FROM", "")
	if smtpMailerFromEnv() != nil {
		t.Fatal("mailer configured without host or sender")
	}
	t.Setenv("FLOW_SMTP_HOST", "smtp.example.test")
	t.Setenv("FLOW_SMTP_FROM", "flow@example.test")
	t.Setenv("FLOW_SMTP_PORT", "")
	t.Setenv("FLOW_APP_URL", "https://flow.example.test/")
	mailer := smtpMailerFromEnv()
	if mailer == nil || mailer.port != "587" || mailer.appURL != "https://flow.example.test" {
		t.Fatalf("mailer=%#v", mailer)
	}
}

// parsedMail is a composed message read back the way a mail client would.
type parsedMail struct {
	header      mail.Header
	contentType string
	text, html  string
	raw         string
}

func parseComposedMail(t *testing.T, raw []byte) parsedMail {
	t.Helper()
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("content type: %v", err)
	}
	result := parsedMail{header: message.Header, contentType: mediaType, raw: string(raw)}
	if mediaType != "multipart/alternative" {
		body, _ := io.ReadAll(quotedprintable.NewReader(message.Body))
		result.text = string(body)
		return result
	}
	parts := multipart.NewReader(message.Body, params["boundary"])
	for {
		part, err := parts.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("part: %v", err)
		}
		if encoding := part.Header.Get("Content-Transfer-Encoding"); encoding != "quoted-printable" {
			t.Fatalf("part encoding=%q", encoding)
		}
		body, err := io.ReadAll(quotedprintable.NewReader(part))
		if err != nil {
			t.Fatalf("decode part: %v", err)
		}
		switch part.Header.Get("Content-Type") {
		case "text/plain; charset=UTF-8":
			result.text = string(body)
		case "text/html; charset=UTF-8":
			result.html = string(body)
		default:
			t.Fatalf("unexpected part %q", part.Header.Get("Content-Type"))
		}
	}
	return result
}

func TestInvitationEmailMatchesTheInviteLayout(t *testing.T) {
	mailer := &smtpMailer{host: "127.0.0.1", port: "1", from: "notifications@flow.example.test", appURL: "https://flow.example.test"}
	inviter := domain.User{ID: "usr_1", Name: "Zheng Liu", DisplayName: "zheng <b>", Email: "zheng@example.test"}
	message, err := mailer.invitationMessage("new@example.test", `Acme & "Co"`, inviter, "tok/en+1")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := mailer.compose(message)
	if err != nil {
		t.Fatal(err)
	}
	email := parseComposedMail(t, raw)
	subject, err := new(mime.WordDecoder).DecodeHeader(email.header.Get("Subject"))
	if err != nil || subject != `zheng <b> invited you to join Acme & "Co" on Flow` {
		t.Fatalf("subject=%q err=%v", subject, err)
	}
	if from := email.header.Get("From"); from != `"Flow" <notifications@flow.example.test>` {
		t.Fatalf("from=%q", from)
	}
	if email.header.Get("Reply-To") != "zheng@example.test" || email.header.Get("To") != "new@example.test" {
		t.Fatalf("reply-to=%q to=%q", email.header.Get("Reply-To"), email.header.Get("To"))
	}
	if email.header.Get("MIME-Version") != "1.0" || email.header.Get("Date") == "" || !strings.HasSuffix(email.header.Get("Message-ID"), "@flow.example.test>") {
		t.Fatalf("headers=%v", email.header)
	}
	if email.contentType != "multipart/alternative" {
		t.Fatalf("content type=%q", email.contentType)
	}
	acceptURL := "https://flow.example.test/invite/tok%2Fen+1"
	wantText := "zheng <b> invited you to join Acme & \"Co\" on Flow.\r\n\r\n" + invitationProduct + " Join the workspace to see what your team is working on.\r\n\r\nAccept invite: " + acceptURL + "\r\n"
	if email.text != wantText {
		t.Fatalf("text=%q\nwant=%q", email.text, wantText)
	}
	html := email.html
	for _, want := range []string{
		`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN"`,
		`<html dir="ltr"`,
		`<!--[if gte mso 9]>`,
		`<title>zheng &lt;b&gt; invited you to join Acme &amp; &#34;Co&#34; on Flow</title>`,
		`<strong>zheng &lt;b&gt;</strong> invited you to join <strong>Acme &amp; &#34;Co&#34;</strong> on Flow</h1>`,
		`href="https://flow.example.test/invite/tok%2Fen&#43;1"`,
		`>Accept invite</a>`,
		`background:#5E6AD2`,
		`id="main" width="602" style="width:602px;margin:24px;max-width:90vw"`,
		`border:1px solid #EFF1F4;border-radius:5px;padding:32px 28px 24px 28px`,
		`src="https://flow.example.test/email/flow-logotype@4x.png" height="20" width="80"`,
		`src="https://flow.example.test/email/invite.jpg" width="540" height="312"`,
		`url("https://flow.example.test/fonts/InterVariable.woff2")`,
		`href="https://flow.example.test" style="color:#3C4149;white-space:nowrap">Flow</a>`,
		template.HTMLEscapeString(invitationProduct) + " Create your account to see what your team is working on.</span>",
		"<p>" + template.HTMLEscapeString(invitationProduct) + " Join the workspace to see what your team is working on.</p>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("html lacks %q", want)
		}
	}
	if strings.Contains(html, "<b>") {
		t.Fatal("inviter name was not escaped")
	}
	if strings.Contains(strings.ToLower(email.raw+email.text+html), "linear") {
		t.Fatal("invitation email mentions another product")
	}
}

func TestInvitationEmailFallbacks(t *testing.T) {
	mailer := &smtpMailer{from: "Flow Team <flow@example.test>", appURL: "https://flow.example.test"}
	message, err := mailer.invitationMessage("new@example.test", "Acme\r\nBcc: x@example.test", domain.User{Email: "pat@example.test"}, "token")
	if err != nil {
		t.Fatal(err)
	}
	if message.Subject != "pat invited you to join Acme Bcc: x@example.test on Flow" || message.ReplyTo != "pat@example.test" {
		t.Fatalf("subject=%q reply-to=%q", message.Subject, message.ReplyTo)
	}
	if header, envelope := mailer.sender(); header != `"Flow Team" <flow@example.test>` || envelope != "flow@example.test" {
		t.Fatalf("sender=%q envelope=%q", header, envelope)
	}
	app, _ := mailer.invitationMessage("new@example.test", "Acme", domain.User{App: true, DisplayName: "Importer", Email: "app@example.test"}, "token")
	if app.ReplyTo != "" || !strings.HasPrefix(app.Subject, "Importer invited") {
		t.Fatalf("app invitation=%#v", app)
	}
	anonymous, _ := mailer.invitationMessage("new@example.test", "Acme", domain.User{}, "token")
	if !strings.HasPrefix(anonymous.Subject, "A teammate invited you") || anonymous.ReplyTo != "" {
		t.Fatalf("anonymous invitation=%#v", anonymous)
	}
	// Non-ASCII names are encoded words in the subject header.
	unicode, _ := mailer.invitationMessage("new@example.test", "设计团队", domain.User{DisplayName: "刘铮"}, "token")
	raw, err := mailer.compose(unicode)
	if err != nil {
		t.Fatal(err)
	}
	email := parseComposedMail(t, raw)
	if subject, _ := new(mime.WordDecoder).DecodeHeader(email.header.Get("Subject")); subject != "刘铮 invited you to join 设计团队 on Flow" || !strings.Contains(email.html, "<strong>刘铮</strong>") {
		t.Fatalf("subject=%q", subject)
	}
}

func TestPlainEmailIsQuotedPrintableText(t *testing.T) {
	mailer := &smtpMailer{from: "flow@example.test", appURL: "https://flow.example.test"}
	raw, err := mailer.compose(mailMessage{To: "a@example.test", Subject: "Reset your password", Text: "Use this link.\r\n\r\nhttps://flow.example.test/reset-password?token=abc\r\n"})
	if err != nil {
		t.Fatal(err)
	}
	email := parseComposedMail(t, raw)
	if email.contentType != "text/plain" || email.header.Get("Reply-To") != "" || email.text != "Use this link.\r\n\r\nhttps://flow.example.test/reset-password?token=abc\r\n" {
		t.Fatalf("plain email=%#v", email)
	}
}

func TestSMTPMailerRejectsHeaderInjectionBeforeConnecting(t *testing.T) {
	mailer := &smtpMailer{host: "127.0.0.1", port: "1", from: "flow@example.test"}
	if err := mailer.send("victim@example.test\r\nBcc: attacker@example.test", "Subject", "Body", "https://example.test"); err == nil || !strings.Contains(err.Error(), "invalid email header") {
		t.Fatalf("header injection error=%v", err)
	}
	if err := mailer.send("victim@example.test", "Subject\nBcc: attacker@example.test", "Body", "https://example.test"); err == nil {
		t.Fatal("subject header injection was accepted")
	}
}
