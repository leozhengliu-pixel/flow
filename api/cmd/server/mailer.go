package main

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

	"flow/api/internal/domain"
)

type smtpMailer struct {
	host, port, username, password, from, appURL string
}

// mailMessage is one outgoing email. HTML is optional: without it the message is a single text/plain part.
type mailMessage struct {
	To, ReplyTo, Subject, Text, HTML string
}

// mailSenderName is the From display name when FLOW_SMTP_FROM is a bare address.
const mailSenderName = "Flow"

func smtpMailerFromEnv() *smtpMailer {
	host := strings.TrimSpace(os.Getenv("FLOW_SMTP_HOST"))
	from := strings.TrimSpace(os.Getenv("FLOW_SMTP_FROM"))
	if host == "" || from == "" {
		return nil
	}
	port := strings.TrimSpace(os.Getenv("FLOW_SMTP_PORT"))
	if port == "" {
		port = "587"
	}
	appURL := strings.TrimRight(strings.TrimSpace(os.Getenv("FLOW_APP_URL")), "/")
	if appURL == "" {
		appURL = "http://localhost:5173"
	}
	return &smtpMailer{host: host, port: port, username: os.Getenv("FLOW_SMTP_USERNAME"), password: os.Getenv("FLOW_SMTP_PASSWORD"), from: from, appURL: appURL}
}

func (m *smtpMailer) sendVerification(email, token string) error {
	return m.send(email, "Verify your email", "Verify your email address to finish creating your account.", m.appURL+"/verify-email?token="+url.QueryEscape(token)+"&email="+url.QueryEscape(email))
}

func (m *smtpMailer) sendPasswordReset(email, token string) error {
	return m.send(email, "Reset your password", "Use this link to choose a new password. It expires in one hour.", m.appURL+"/reset-password?token="+url.QueryEscape(token))
}

func (m *smtpMailer) sendMagicLink(email, token string) error {
	return m.send(email, "Sign in to Flow", "Use this link to sign in. It expires in one hour.", m.appURL+"/auth/token?email="+url.QueryEscape(email)+"&token="+url.QueryEscape(token))
}

// sendInvitation emails a workspace invitation from inviter; replies go to the inviter.
func (m *smtpMailer) sendInvitation(email, workspace string, inviter domain.User, token string) error {
	message, err := m.invitationMessage(email, workspace, inviter, token)
	if err != nil {
		return err
	}
	return m.deliver(message)
}

func (m *smtpMailer) sendNotification(email, workspaceKey, identifier, title, body string) error {
	subject := identifier + " " + title
	link := m.appURL + "/" + url.PathEscape(workspaceKey) + "/issue/" + url.PathEscape(identifier)
	return m.send(email, subject, body, link)
}

// send delivers a plain-text email whose body ends with link.
func (m *smtpMailer) send(to, subject, body, link string) error {
	return m.deliver(mailMessage{To: to, Subject: subject, Text: body + "\r\n\r\n" + link + "\r\n"})
}

// invitationProduct describes Flow to someone who has not used it; the invitation's body and preheader start with it.
const invitationProduct = "Flow brings your team's issues, projects, code reviews, and agents together in one place."

// emailFontStack is the body font of Flow's HTML emails: Inter (served by the Flow instance) with system fallbacks.
const emailFontStack = `Inter,"SF Pro",-apple-system,BlinkMacSystemFont,ui-sans-serif,"Segoe UI",Roboto,Oxygen,Ubuntu,Cantarell,"Open Sans","Helvetica Neue",sans-serif`

// invitationEmailOutlook is the head block that keeps PNG images crisp in Outlook. html/template drops comments, so
// the conditional comment is inserted as trusted markup.
const invitationEmailOutlook = template.HTML(`<!--[if gte mso 9]>
      <xml>
        <o:OfficeDocumentSettings>
          <o:AllowPNG/>
          <o:PixelsPerInch>96</o:PixelsPerInch>
        </o:OfficeDocumentSettings>
      </xml>
    <![endif]-->`)

// invitationEmail is the HTML invitation: a centred 602px column holding a white card (logotype, headline, product
// paragraph, hero image, Accept invite button) and a footer line. Styles are inline for clients that drop <style>.
// "Inter Display" is the variable Inter declared at weight 600 only, so the bold headline (and the <strong> names in
// it) render semibold where web fonts load, and plain bold Inter or the system font elsewhere.
var invitationEmail = template.Must(template.New("invitation").Parse(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html dir="ltr" xmlns="http://www.w3.org/1999/xhtml" xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office">
  <head>
    <meta http-equiv="Content-Type" content="text/html; charset=utf-8" />
    <meta http-equiv="X-UA-Compatible" content="IE=edge" />
    <meta name="viewport" content="width=device-width" />
    <title>{{.Subject}}</title>
    <style type="text/css">
      @font-face{font-family:Inter;font-weight:100 900;font-display:swap;font-style:normal;src:url("{{.FontURL}}") format("woff2")}
      @font-face{font-family:"Inter Display";font-weight:600;font-display:swap;font-style:normal;src:url("{{.FontURL}}") format("woff2")}
      *,::after,::before{box-sizing:border-box}
      body{font-family:Inter,"SF Pro",-apple-system,BlinkMacSystemFont,ui-sans-serif,"Segoe UI",Roboto,Oxygen,Ubuntu,Cantarell,"Open Sans","Helvetica Neue",sans-serif;font-size:15px;line-height:1.4;-moz-osx-font-smoothing:grayscale;-webkit-font-smoothing:antialiased;text-rendering:optimizeLegibility;-ms-text-size-adjust:100%;-webkit-text-size-adjust:100%;text-size-adjust:100%;color:#3c4149;background:#f9f8f9}
      table{border-spacing:0}
      a{color:#66f;text-decoration:none}
      h1,h2,h3,h4,h5,h6{font-family:"Inter Display",Inter,"SF Pro",-apple-system,BlinkMacSystemFont,ui-sans-serif,"Segoe UI",Roboto,Oxygen,Ubuntu,Cantarell,"Open Sans","Helvetica Neue",sans-serif}
      p{margin-top:0;margin-bottom:16px}
      p>strong{color:#282a30}
      #__bodyTable__{margin:0;padding:0;width:100% !important}
    </style>
    {{.Outlook}}
  </head>
  <body bgcolor="#F9F8F9" width="100%" style="-webkit-font-smoothing:antialiased;width:100% !important;background:#F9F8F9;-webkit-text-size-adjust:none;margin:0;padding:0;min-width:100%;direction:ltr">
    <table bgcolor="#F9F8F9" id="__bodyTable__" width="100%" style="-webkit-font-smoothing:antialiased;width:100% !important;background:#F9F8F9;-webkit-text-size-adjust:none;margin:0;padding:0;min-width:100%">
      <tr>
        <td align="center">
          <span style="display:none !important;color:#F9F8F9;margin:0;padding:0;font-size:1px;line-height:1px">{{.Preheader}}</span>
          <table id="main" width="602" style="width:602px;margin:24px;max-width:90vw" border="0" cellspacing="0" cellpadding="0"><tbody><tr><td align="left">
            <table width="100%" style="border:1px solid #EFF1F4;border-radius:5px;padding:32px 28px 24px 28px;background:#FFF;color:#3C4149" border="0" cellspacing="0" cellpadding="0"><tbody><tr><td>
              <table width="100%" style="margin-bottom:48px" border="0" cellspacing="0" cellpadding="0"><tbody><tr><td>
                <a href="{{.AppURL}}"><img alt="Flow" src="{{.LogoURL}}" height="20" width="80" style="display:block" /></a>
              </td></tr></tbody></table>
              <h1 style="font-size:24px;line-height:1.2;font-weight:700;font-family:{{.HeadingFont}};font-variation-settings:'opsz' 32;margin-top:0;margin-bottom:24px;color:#282a30"><strong>{{.Inviter}}</strong> invited you to join <strong>{{.Workspace}}</strong> on Flow</h1>
              <p>{{.Product}} Join the workspace to see what your team is working on.</p>
              <p><img alt="" src="{{.HeroURL}}" width="540" height="312" style="display:block;max-width:100%;height:auto;border-radius:10px" /></p>
              <a href="{{.AcceptURL}}" style="margin-top:8px;margin-bottom:24px;display:inline-block;padding:12px 16px;color:#fff;background:#5E6AD2;border-radius:5px;font-size:13px;line-height:1.2;font-weight:500;text-decoration:none;cursor:pointer">Accept invite</a>
            </td></tr></tbody></table>
            <table width="100%" style="width:100%;margin-top:32px;padding-left:28px;padding-right:28px;font-size:14px;line-height:1.5" border="0" cellspacing="0" cellpadding="0"><tbody><tr>
              <td style="width:50%"><a href="{{.AppURL}}" style="color:#3C4149;white-space:nowrap">Flow</a></td>
            </tr></tbody></table>
          </td></tr></tbody></table>
        </td>
      </tr>
    </table>
  </body>
</html>
`))

// invitationMessage builds the invitation email: "<inviter> invited you to join <workspace> on Flow", in text and HTML.
func (m *smtpMailer) invitationMessage(email, workspace string, inviter domain.User, token string) (mailMessage, error) {
	inviterName := headerText(inviterDisplayName(inviter))
	workspace = headerText(workspace)
	if workspace == "" {
		workspace = "a workspace"
	}
	subject := inviterName + " invited you to join " + workspace + " on Flow"
	acceptURL := m.appURL + "/invite/" + url.PathEscape(token)
	var html bytes.Buffer
	err := invitationEmail.Execute(&html, map[string]any{
		"Subject":     subject,
		"Inviter":     inviterName,
		"Workspace":   workspace,
		"Product":     invitationProduct,
		"Preheader":   invitationProduct + " Create your account to see what your team is working on.",
		"AppURL":      m.appURL,
		"AcceptURL":   acceptURL,
		"LogoURL":     m.appURL + "/email/flow-logotype@4x.png",
		"HeroURL":     m.appURL + "/email/invite.jpg",
		"FontURL":     m.appURL + "/fonts/InterVariable.woff2",
		"HeadingFont": template.CSS(`"Inter Display",` + emailFontStack),
		"Outlook":     invitationEmailOutlook,
	})
	if err != nil {
		return mailMessage{}, err
	}
	text := subject + ".\n\n" + invitationProduct + " Join the workspace to see what your team is working on.\n\nAccept invite: " + acceptURL + "\n"
	return mailMessage{To: email, ReplyTo: replyAddress(inviter), Subject: subject, Text: text, HTML: html.String()}, nil
}

// inviterDisplayName is how the invitation names its sender: display name, then full name, then the email's local
// part, so a teammate without a profile still reads as a person.
func inviterDisplayName(inviter domain.User) string {
	for _, name := range []string{inviter.DisplayName, inviter.Name, strings.SplitN(inviter.Email, "@", 2)[0]} {
		if name = headerText(name); name != "" {
			return name
		}
	}
	return "A teammate"
}

// replyAddress is the inviter's address for Reply-To, or "" for apps and anything that is not a single plain address.
func replyAddress(inviter domain.User) string {
	if inviter.App || strings.ContainsAny(inviter.Email, "\r\n") {
		return ""
	}
	address, err := mail.ParseAddress(strings.TrimSpace(inviter.Email))
	if err != nil || address.Name != "" {
		return ""
	}
	return address.Address
}

// headerText flattens user-provided text for a header line: control characters become spaces, runs of space collapse.
func headerText(value string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)), " ")
}

// sender is the From header ("Flow <flow@example.com>" unless FLOW_SMTP_FROM names its own sender) and the envelope address.
func (m *smtpMailer) sender() (header, envelope string) {
	address, err := mail.ParseAddress(m.from)
	if err != nil {
		return m.from, m.from
	}
	name := address.Name
	if name == "" {
		name = mailSenderName
	}
	return (&mail.Address{Name: name, Address: address.Address}).String(), address.Address
}

// compose renders message as RFC 5322 bytes: quoted-printable UTF-8 text, with an HTML alternative when present.
func (m *smtpMailer) compose(message mailMessage) ([]byte, error) {
	if strings.ContainsAny(message.To+message.Subject+message.ReplyTo, "\r\n") {
		return nil, fmt.Errorf("invalid email header")
	}
	from, envelope := m.sender()
	var out bytes.Buffer
	header := func(name, value string) { out.WriteString(name + ": " + value + "\r\n") }
	header("From", from)
	header("To", message.To)
	if message.ReplyTo != "" {
		header("Reply-To", message.ReplyTo)
	}
	header("Subject", mime.QEncoding.Encode("utf-8", message.Subject))
	header("Date", time.Now().Format(time.RFC1123Z))
	header("Message-ID", "<"+randomMessageID()+"@"+messageIDDomain(envelope)+">")
	header("MIME-Version", "1.0")
	if message.HTML == "" {
		header("Content-Type", "text/plain; charset=UTF-8")
		header("Content-Transfer-Encoding", "quoted-printable")
		out.WriteString("\r\n")
		return out.Bytes(), writeQuotedPrintable(&out, message.Text)
	}
	var body bytes.Buffer
	parts := multipart.NewWriter(&body)
	header("Content-Type", mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": parts.Boundary()}))
	out.WriteString("\r\n")
	for _, part := range []struct{ contentType, content string }{{"text/plain; charset=UTF-8", message.Text}, {"text/html; charset=UTF-8", message.HTML}} {
		writer, err := parts.CreatePart(textproto.MIMEHeader{"Content-Type": {part.contentType}, "Content-Transfer-Encoding": {"quoted-printable"}})
		if err != nil {
			return nil, err
		}
		if err := writeQuotedPrintable(writer, part.content); err != nil {
			return nil, err
		}
	}
	if err := parts.Close(); err != nil {
		return nil, err
	}
	out.Write(body.Bytes())
	return out.Bytes(), nil
}

func writeQuotedPrintable(target io.Writer, content string) error {
	writer := quotedprintable.NewWriter(target)
	if _, err := writer.Write([]byte(content)); err != nil {
		return err
	}
	return writer.Close()
}

func randomMessageID() string {
	buffer := make([]byte, 16)
	_, _ = rand.Read(buffer)
	return hex.EncodeToString(buffer)
}

func messageIDDomain(address string) string {
	if at := strings.LastIndex(address, "@"); at >= 0 && at < len(address)-1 {
		return address[at+1:]
	}
	return "flow.local"
}

// deliver sends message over SMTP (implicit TLS on port 465, STARTTLS negotiation otherwise).
func (m *smtpMailer) deliver(message mailMessage) error {
	data, err := m.compose(message)
	if err != nil {
		return err
	}
	_, envelope := m.sender()
	to := message.To
	address := net.JoinHostPort(m.host, m.port)
	var auth smtp.Auth
	if m.username != "" {
		auth = smtp.PlainAuth("", m.username, m.password, m.host)
	}
	if m.port != "465" {
		return smtp.SendMail(address, auth, envelope, []string{to}, data)
	}
	connection, err := tls.Dial("tcp", address, &tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(connection, m.host)
	if err != nil {
		return err
	}
	defer client.Close()
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(envelope); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(data); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
