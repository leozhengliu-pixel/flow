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
	// transport replaces the SMTP connection (tests capture mail with it).
	transport func(envelope string, to []string, message []byte) error
}

// mailMessage is one outgoing email. HTML is optional: without it the message is a single text/plain part.
type mailMessage struct {
	To, ReplyTo, Subject, Text, HTML string
	// ListUnsubscribe is the List-Unsubscribe URL; OneClick adds RFC 8058's List-Unsubscribe-Post.
	ListUnsubscribe string
	OneClick        bool
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

// send delivers a plain-text email whose body ends with link.
func (m *smtpMailer) send(to, subject, body, link string) error {
	return m.deliver(mailMessage{To: to, Subject: subject, Text: body + "\r\n\r\n" + link + "\r\n"})
}

// emailFontStack is the body font of Flow's HTML emails: Inter (served by the Flow instance) with system fallbacks.
const emailFontStack = `Inter,"SF Pro",-apple-system,BlinkMacSystemFont,ui-sans-serif,"Segoe UI",Roboto,Oxygen,Ubuntu,Cantarell,"Open Sans","Helvetica Neue",sans-serif`

// emailOutlookHead is the head block that keeps PNG images crisp in Outlook. html/template drops comments, so
// the conditional comment is inserted as trusted markup.
const emailOutlookHead = template.HTML(`<!--[if gte mso 9]>
      <xml>
        <o:OfficeDocumentSettings>
          <o:AllowPNG/>
          <o:PixelsPerInch>96</o:PixelsPerInch>
        </o:OfficeDocumentSettings>
      </xml>
    <![endif]-->`)

// emailLayout is one HTML email in Flow's layout: a centred 602px column holding a white card (logotype, headline,
// then Blocks in order) and a footer line with Flow's name and, for emails people can opt out of, Unsubscribe.
type emailLayout struct {
	Title, Preheader string
	Heading          template.HTML
	// RegularHeading sets the headline in medium-weight Inter (onboarding emails) instead of the bold display face.
	RegularHeading bool
	Blocks         []emailBlock
	Unsubscribe    string
}

// emailBlock is one piece of an email body. Kind selects the markup:
//
//	p        paragraph of HTML
//	hero     540x312 image
//	button   Label linking to URL
//	rule     24px-spaced divider; hr: the notification email's tighter divider
//	fields   bold "Label:" value lines
//	link     a bold "Label →" link to URL with HTML as its description
//	list     bullet list of Items
//	code     URL-like value in a monospace box
//	notice   notification entry: Label linking to URL, then Icon and HTML
type emailBlock struct {
	Kind, Label, URL, Image, Icon string
	HTML                          template.HTML
	Fields                        []emailField
	Items                         []template.HTML
}

type emailField struct{ Label, Value string }

// emailTemplate renders emailLayout. Styles are inline for clients that drop <style>. "Inter Display" is the
// variable Inter declared at weight 600 only, so the bold headline (and <strong> names in it) render semibold
// where web fonts load, and plain bold Inter or the system font elsewhere.
var emailTemplate = template.Must(template.New("email").Parse(`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html dir="ltr" xmlns="http://www.w3.org/1999/xhtml" xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office">
  <head>
    <meta http-equiv="Content-Type" content="text/html; charset=utf-8" />
    <meta http-equiv="X-UA-Compatible" content="IE=edge" />
    <meta name="viewport" content="width=device-width" />
    <title>{{.Layout.Title}}</title>
    <style type="text/css">
      @font-face{font-family:Inter;font-weight:100 900;font-display:swap;font-style:normal;src:url("{{.FontURL}}") format("woff2")}
      @font-face{font-family:"Inter Display";font-weight:600;font-display:swap;font-style:normal;src:url("{{.FontURL}}") format("woff2")}
      *,::after,::before{box-sizing:border-box}
      body{font-family:Inter,"SF Pro",-apple-system,BlinkMacSystemFont,ui-sans-serif,"Segoe UI",Roboto,Oxygen,Ubuntu,Cantarell,"Open Sans","Helvetica Neue",sans-serif;font-size:15px;line-height:1.4;-moz-osx-font-smoothing:grayscale;-webkit-font-smoothing:antialiased;text-rendering:optimizeLegibility;-ms-text-size-adjust:100%;-webkit-text-size-adjust:100%;text-size-adjust:100%;color:#3c4149;background:#f9f8f9}
      table{border-spacing:0}
      a{color:#66f;text-decoration:none}
      code{font-family:"SFMono Regular",Consolas,"Liberation Mono",Menlo,Courier,monospace}
      h1,h2,h3,h4,h5,h6{font-family:"Inter Display",Inter,"SF Pro",-apple-system,BlinkMacSystemFont,ui-sans-serif,"Segoe UI",Roboto,Oxygen,Ubuntu,Cantarell,"Open Sans","Helvetica Neue",sans-serif}
      p{margin-top:0;margin-bottom:16px}
      p>strong{color:#282a30}
      ol,ul{margin-top:16px;margin-bottom:16px;padding-left:16px}
      ol li,ul li{margin-top:6px;margin-left:0}
      #__bodyTable__{margin:0;padding:0;width:100% !important}
    </style>
    {{.Outlook}}
  </head>
  <body bgcolor="#F9F8F9" width="100%" style="-webkit-font-smoothing:antialiased;width:100% !important;background:#F9F8F9;-webkit-text-size-adjust:none;margin:0;padding:0;min-width:100%;direction:ltr">
    <table bgcolor="#F9F8F9" id="__bodyTable__" width="100%" style="-webkit-font-smoothing:antialiased;width:100% !important;background:#F9F8F9;-webkit-text-size-adjust:none;margin:0;padding:0;min-width:100%">
      <tr>
        <td align="center">
          <span style="display:none !important;color:#F9F8F9;margin:0;padding:0;font-size:1px;line-height:1px">{{.Layout.Preheader}}</span>
          <table id="main" width="602" style="width:602px;margin:24px;max-width:90vw" border="0" cellspacing="0" cellpadding="0"><tbody><tr><td align="left">
            <table width="100%" style="border:1px solid #EFF1F4;border-radius:5px;padding:32px 28px 24px 28px;background:#FFF;color:#3C4149" border="0" cellspacing="0" cellpadding="0"><tbody><tr><td>
              <table width="100%" style="margin-bottom:48px" border="0" cellspacing="0" cellpadding="0"><tbody><tr><td>
                <a href="{{.AppURL}}"><img alt="Flow" src="{{.LogoURL}}" height="20" width="80" style="display:block" /></a>
              </td></tr></tbody></table>
              {{- if .Layout.RegularHeading}}
              <h1 style="font-size:24px;line-height:1.2;font-weight:500;font-family:{{.BodyFont}};margin-top:0;margin-bottom:24px;color:#282a30">{{.Layout.Heading}}</h1>
              {{- else}}
              <h1 style="font-size:24px;line-height:1.2;font-weight:700;font-family:{{.HeadingFont}};font-variation-settings:'opsz' 32;margin-top:0;margin-bottom:24px;color:#282a30">{{.Layout.Heading}}</h1>
              {{- end}}
              {{- range .Layout.Blocks}}
              {{- if eq .Kind "p"}}
              <p>{{.HTML}}</p>
              {{- else if eq .Kind "hero"}}
              <p><img alt="" src="{{.Image}}" width="540" height="312" style="display:block;max-width:100%;height:auto;border-radius:10px" /></p>
              {{- else if eq .Kind "button"}}
              <a href="{{.URL}}" style="margin-top:8px;margin-bottom:24px;display:inline-block;padding:12px 16px;color:#fff;background:#5E6AD2;border-radius:5px;font-size:13px;line-height:1.2;font-weight:500;text-decoration:none;cursor:pointer">{{.Label}}</a>
              {{- else if eq .Kind "rule"}}
              <hr style="margin:8px 0 24px 0;border-top:0;border-left:0;border-right:0;border-bottom:1px solid #E3DFE6" />
              {{- else if eq .Kind "hr"}}
              <hr style="margin:12px 0px;border-top:0;border-left:0;border-right:0;border-bottom:1px solid #EFF1F4" />
              {{- else if eq .Kind "fields"}}
              {{- range .Fields}}
              <p style="margin-bottom:8px"><strong style="font-weight:600;color:#3C4149">{{.Label}}:</strong> {{.Value}}</p>
              {{- end}}
              <div style="height:16px;line-height:16px;font-size:1px">&nbsp;</div>
              {{- else if eq .Kind "link"}}
              <p style="margin-bottom:8px"><a href="{{.URL}}" style="font-weight:600;color:#66f">{{.Label}} &rarr;</a></p>
              <p style="margin-bottom:24px">{{.HTML}}</p>
              {{- else if eq .Kind "list"}}
              <ul>{{range .Items}}<li>{{.}}</li>{{end}}</ul>
              {{- else if eq .Kind "code"}}
              <p><code style="display:inline-block;padding:6px 10px;border:1px solid #EFF1F4;border-radius:5px;background:#F9F8F9;color:#282a30;font-size:13px;word-break:break-all">{{.Label}}</code></p>
              {{- else if eq .Kind "notice"}}
              <table width="100%" border="0" cellspacing="0" cellpadding="0"><tbody>
                <tr style="padding-top:10px;padding-bottom:10px"><td width="100%" style="padding:0px 16px 16px 0px"><a style="color:#3C4149;font-size:14px;font-weight:500;line-height:20px;vertical-align:baseline" href="{{.URL}}">{{.Label}}</a></td></tr>
                <tr><td valign="middle" colspan="2" style="padding:0px 0px 12px 0px;font-size:14px;color:#3C4149"><div style="display:flex">{{if .Icon}}<span style="margin-right:18px"><img width="16" height="16" style="vertical-align:text-top;border-radius:4px" alt="" src="{{.Icon}}" /></span>{{end}}<span>{{.HTML}}</span></div></td></tr>
              </tbody></table>
              {{- end}}
              {{- end}}
            </td></tr></tbody></table>
            <table width="100%" style="width:100%;margin-top:32px;padding-left:28px;padding-right:28px;font-size:14px;line-height:1.5" border="0" cellspacing="0" cellpadding="0"><tbody><tr>
              <td style="width:50%"><a href="{{.AppURL}}" style="color:#3C4149;white-space:nowrap">Flow</a></td>
              {{- if .Layout.Unsubscribe}}
              <td style="width:50%;text-align:right"><a href="{{.Layout.Unsubscribe}}">Unsubscribe</a></td>
              {{- end}}
            </tr></tbody></table>
          </td></tr></tbody></table>
        </td>
      </tr>
    </table>
  </body>
</html>
`))

// renderEmail renders layout as an HTML document with Flow's logotype, font and links on this instance's URL.
func (m *smtpMailer) renderEmail(layout emailLayout) (string, error) {
	var html bytes.Buffer
	err := emailTemplate.Execute(&html, map[string]any{
		"Layout":      layout,
		"AppURL":      m.appURL,
		"LogoURL":     m.emailAsset("flow-logotype@4x.png"),
		"FontURL":     m.appURL + "/fonts/InterVariable.woff2",
		"HeadingFont": template.CSS(`"Inter Display",` + emailFontStack),
		"BodyFont":    template.CSS(emailFontStack),
		"Outlook":     emailOutlookHead,
	})
	return html.String(), err
}

// emailAsset is the absolute URL of an image in web/public/email.
func (m *smtpMailer) emailAsset(name string) string {
	return m.appURL + "/email/" + name
}

// emailHTML formats an HTML fragment: string arguments are escaped, template.HTML arguments (strong, link) pass
// through.
func emailHTML(format string, args ...any) template.HTML {
	escaped := make([]any, len(args))
	for index, arg := range args {
		switch value := arg.(type) {
		case template.HTML:
			escaped[index] = string(value)
		default:
			escaped[index] = template.HTMLEscapeString(fmt.Sprint(value))
		}
	}
	return template.HTML(fmt.Sprintf(format, escaped...))
}

// emailStrong is text in <strong>, escaped.
func emailStrong(text string) template.HTML {
	return template.HTML("<strong>" + template.HTMLEscapeString(text) + "</strong>")
}

// emailLink is an escaped link; only http(s) URLs are linked.
func emailLink(href, text string) template.HTML {
	if !strings.HasPrefix(href, "https://") && !strings.HasPrefix(href, "http://") {
		return template.HTML(template.HTMLEscapeString(text))
	}
	return template.HTML(`<a href="` + template.HTMLEscapeString(href) + `" style="color:#66f;text-decoration:none">` + template.HTMLEscapeString(text) + `</a>`)
}

// invitationProduct describes Flow to someone who has not used it; the invitation's body and preheader start with it.
const invitationProduct = "Flow brings your team's issues, projects, code reviews, and agents together in one place."

// invitationMessage builds the invitation email: "<inviter> invited you to join <workspace> on Flow", in text and HTML.
func (m *smtpMailer) invitationMessage(email, workspace string, inviter domain.User, token string) (mailMessage, error) {
	inviterName := headerText(inviterDisplayName(inviter))
	workspace = headerText(workspace)
	if workspace == "" {
		workspace = "a workspace"
	}
	subject := inviterName + " invited you to join " + workspace + " on Flow"
	acceptURL := m.appURL + "/invite/" + url.PathEscape(token)
	html, err := m.renderEmail(emailLayout{
		Title:     subject,
		Preheader: invitationProduct + " Create your account to see what your team is working on.",
		Heading:   emailHTML("%s invited you to join %s on Flow", emailStrong(inviterName), emailStrong(workspace)),
		Blocks: []emailBlock{
			{Kind: "p", HTML: emailHTML("%s Join the workspace to see what your team is working on.", invitationProduct)},
			{Kind: "hero", Image: m.emailAsset("invite.jpg")},
			{Kind: "button", Label: "Accept invite", URL: acceptURL},
		},
	})
	if err != nil {
		return mailMessage{}, err
	}
	text := subject + ".\n\n" + invitationProduct + " Join the workspace to see what your team is working on.\n\nAccept invite: " + acceptURL + "\n"
	return mailMessage{To: email, ReplyTo: replyAddress(inviter), Subject: subject, Text: text, HTML: html}, nil
}

// inviterDisplayName is how an email names a person: display name, then full name, then the email's local
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
	if strings.ContainsAny(message.To+message.Subject+message.ReplyTo+message.ListUnsubscribe, "\r\n") || strings.ContainsAny(message.ListUnsubscribe, "<>") {
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
	if message.ListUnsubscribe != "" {
		header("List-Unsubscribe", "<"+message.ListUnsubscribe+">")
		if message.OneClick {
			header("List-Unsubscribe-Post", "List-Unsubscribe=One-Click")
		}
	}
	header("MIME-Version", "1.0")
	if message.HTML == "" {
		header("Content-Type", "text/plain; charset=UTF-8")
		header("Content-Transfer-Encoding", "quoted-printable")
		out.WriteString("\r\n")
		if err := writeQuotedPrintable(&out, message.Text); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
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
	if m.transport != nil {
		return m.transport(envelope, []string{to}, data)
	}
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
