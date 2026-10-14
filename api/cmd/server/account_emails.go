package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Account emails are the ones Flow sends to a person rather than about a workspace: "Welcome to Flow" after
// sign-up, "New login to Flow" for a sign-in from a new device or browser, and the onboarding follow-up
// "Connect Flow to your AI tools with MCP" a week after the welcome. The store's account email ledger records
// what was sent and which devices a person has signed in from.
const (
	accountEventDevice       = "login-device"
	accountEventWelcome      = "welcome"
	accountEventMCP          = "onboarding-mcp"
	accountEventUnsubscribed = "unsubscribed-onboarding"
	deviceCookieName         = "flow_device"
	// newAccountWindow is how long after creation a sign-in counts as the sign-up one (welcome email, and a
	// login alert even for the first device).
	newAccountWindow       = 24 * time.Hour
	defaultOnboardingDelay = 7 * 24 * time.Hour
	unsubscribeSecretName  = "email-unsubscribe"
	onboardingList         = "onboarding"
)

// onboardingEmailsEnabled is FLOW_ONBOARDING_EMAILS (default on): the welcome email and its follow-up.
func onboardingEmailsEnabled() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("FLOW_ONBOARDING_EMAILS")), "false")
}

// onboardingFollowUpDelay is FLOW_ONBOARDING_MCP_DELAY: how long after the welcome the MCP email goes out
// (default a week); 0 turns the follow-up off.
func onboardingFollowUpDelay() time.Duration {
	raw := strings.TrimSpace(os.Getenv("FLOW_ONBOARDING_MCP_DELAY"))
	if raw == "" {
		return defaultOnboardingDelay
	}
	delay, err := time.ParseDuration(raw)
	if err != nil || delay < 0 {
		return defaultOnboardingDelay
	}
	return delay
}

// supportEmail is FLOW_SUPPORT_EMAIL, the address onboarding emails invite replies to (none when unset).
func supportEmail() string {
	value := strings.TrimSpace(os.Getenv("FLOW_SUPPORT_EMAIL"))
	if strings.ContainsAny(value, "\r\n<> ") || !strings.Contains(value, "@") {
		return ""
	}
	return value
}

// afterSignIn runs once a sign-in has issued its session cookie: it remembers the browser in a long-lived device
// cookie, sends "New login to Flow" when the device is new for the account, and the welcome email after sign-up.
// Email failures never fail the sign-in.
func (s *server) afterSignIn(w http.ResponseWriter, r *http.Request, session domain.AuthSession) {
	device := ""
	if cookie, err := r.Cookie(deviceCookieName); err == nil && len(cookie.Value) == 64 {
		if _, err := hex.DecodeString(cookie.Value); err == nil {
			device = cookie.Value
		}
	}
	if device == "" {
		buffer := make([]byte, 32)
		if _, err := rand.Read(buffer); err != nil {
			return
		}
		device = hex.EncodeToString(buffer)
	}
	http.SetCookie(w, &http.Cookie{Name: deviceCookieName, Value: device, Path: "/", HttpOnly: true, Secure: secureCookie(r), SameSite: http.SameSiteLaxMode, MaxAge: 400 * 24 * 60 * 60})
	user := session.User
	if user.ID == "" {
		return
	}
	ctx, now := context.WithoutCancel(r.Context()), time.Now().UTC()
	known, err := s.store.HasAccountEmailEvent(ctx, user.ID, accountEventDevice)
	if err != nil {
		log.Printf("Account email ledger: %v", err)
		return
	}
	digest := sha256.Sum256([]byte(device))
	newDevice, err := s.store.RecordAccountEmailEvent(ctx, user.ID, accountEventDevice, hex.EncodeToString(digest[:]), now)
	if err != nil {
		log.Printf("Account email ledger: %v", err)
		return
	}
	created, err := s.store.AccountCreatedAt(ctx, user.ID)
	newAccount := err == nil && now.Sub(created) < newAccountWindow
	if s.mailer == nil || strings.TrimSpace(user.Email) == "" {
		return
	}
	workspaceKey := ""
	if len(session.Memberships) > 0 {
		workspaceKey = session.Memberships[0].Workspace.URLKey
	}
	// Accounts that existed before devices were recorded learn their first device silently; a new account is
	// alerted from its first sign-in.
	if newDevice && (known || newAccount) {
		device := domain.AccountSession{}
		decorateAccountSession(&device, r)
		message, err := s.mailer.newLoginMessage(user.Email, workspaceKey, device.Name, authClientAddress(r), now)
		if err == nil {
			err = s.mailer.deliver(message)
		}
		if err != nil {
			log.Printf("New login email: %v", err)
		}
	}
	if newAccount && onboardingEmailsEnabled() {
		first, err := s.store.RecordAccountEmailEvent(ctx, user.ID, accountEventWelcome, "", now)
		if err != nil || !first {
			return
		}
		unsubscribe, err := s.unsubscribeURL(ctx, user.ID)
		if err == nil {
			var message mailMessage
			message, err = s.mailer.welcomeMessage(user.Email, workspaceKey, unsubscribe)
			if err == nil {
				err = s.mailer.deliver(message)
			}
		}
		if err != nil {
			log.Printf("Welcome email: %v", err)
		}
	}
}

// sendDueOnboardingEmails sends the MCP follow-up to accounts welcomed at least the follow-up delay ago that
// have neither had it nor unsubscribed. The ledger row is written first, so an email goes out at most once.
func (s *server) sendDueOnboardingEmails(ctx context.Context, now time.Time) {
	delay := onboardingFollowUpDelay()
	if s.mailer == nil || !onboardingEmailsEnabled() || delay == 0 {
		return
	}
	users, err := s.store.AccountsDueFollowUp(ctx, accountEventMCP, accountEventWelcome, accountEventUnsubscribed, now.Add(-delay), 50)
	if err != nil {
		log.Printf("Onboarding emails: %v", err)
		return
	}
	for _, user := range users {
		if first, err := s.store.RecordAccountEmailEvent(ctx, user.ID, accountEventMCP, "", now); err != nil || !first {
			continue
		}
		workspaceKey := ""
		if account, err := s.store.AccountForUser(ctx, user.ID); err == nil && len(account.Workspaces) > 0 {
			workspaceKey = account.Workspaces[0].Workspace.URLKey
		}
		unsubscribe, err := s.unsubscribeURL(ctx, user.ID)
		if err == nil {
			var message mailMessage
			message, err = s.mailer.mcpOnboardingMessage(user.Email, workspaceKey, unsubscribe)
			if err == nil {
				err = s.mailer.deliver(message)
			}
		}
		if err != nil {
			log.Printf("MCP onboarding email user=%s: %v", user.ID, err)
		}
	}
}

func (s *server) unsubscribeKey(ctx context.Context) ([]byte, error) {
	secret, err := s.store.SigningSecret(ctx, unsubscribeSecretName)
	return []byte(secret), err
}

func unsubscribeSignature(key []byte, list, userID string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(list + "\n" + userID))
	return hex.EncodeToString(mac.Sum(nil))
}

// unsubscribeURL is the signed one-click link that stops onboarding emails for userID.
func (s *server) unsubscribeURL(ctx context.Context, userID string) (string, error) {
	key, err := s.unsubscribeKey(ctx)
	if err != nil {
		return "", err
	}
	query := url.Values{"list": {onboardingList}, "user": {userID}, "sig": {unsubscribeSignature(key, onboardingList, userID)}}
	return s.mailer.appURL + "/api/email/unsubscribe?" + query.Encode(), nil
}

var unsubscribePage = template.Must(template.New("unsubscribe").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}} · Flow</title>
<style>body{margin:0;font-family:Inter,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#f9f8f9;color:#3c4149;font-size:15px;line-height:1.4}main{max-width:480px;margin:64px auto;padding:32px 28px;background:#fff;border:1px solid #eff1f4;border-radius:5px}h1{font-size:20px;color:#282a30;margin:0 0 12px}button{margin-top:12px;padding:12px 16px;border:0;border-radius:5px;background:#5e6ad2;color:#fff;font:500 13px/1.2 inherit;cursor:pointer}</style></head>
<body><main><h1>{{.Title}}</h1><p>{{.Text}}</p>{{if .Confirm}}<form method="post"><button type="submit">Unsubscribe</button></form>{{end}}</main></body></html>`))

// unsubscribeEmails is GET/POST /api/email/unsubscribe?list=onboarding&user=&sig=. GET asks for confirmation
// (link scanners must not unsubscribe anyone); POST, including RFC 8058 one-click from the mail client,
// stops the onboarding emails.
func (s *server) unsubscribeEmails(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	list, userID, signature := query.Get("list"), query.Get("user"), query.Get("sig")
	key, err := s.unsubscribeKey(r.Context())
	valid := err == nil && list == onboardingList && userID != "" && hmac.Equal([]byte(signature), []byte(unsubscribeSignature(key, list, userID)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if !valid {
		w.WriteHeader(http.StatusNotFound)
		_ = unsubscribePage.Execute(w, map[string]any{"Title": "Link not valid", "Text": "This unsubscribe link is not valid."})
		return
	}
	if r.Method == http.MethodGet {
		_ = unsubscribePage.Execute(w, map[string]any{"Title": "Unsubscribe from onboarding emails", "Text": "You will no longer get Flow's getting-started emails. Notifications and security emails are not affected.", "Confirm": true})
		return
	}
	if _, err := s.store.RecordAccountEmailEvent(r.Context(), userID, accountEventUnsubscribed, "", time.Now().UTC()); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = unsubscribePage.Execute(w, map[string]any{"Title": "Something went wrong", "Text": "Try the link again in a moment."})
		return
	}
	_ = unsubscribePage.Execute(w, map[string]any{"Title": "You're unsubscribed", "Text": "Flow won't send you more getting-started emails."})
}

// workspaceURL is the Flow URL of path in workspaceKey, or the app root without a workspace.
func (m *smtpMailer) workspaceURL(workspaceKey, path string) string {
	if workspaceKey == "" {
		return m.appURL + "/"
	}
	return m.appURL + "/" + url.PathEscape(workspaceKey) + path
}

// newLoginMessage is "New login to Flow": the device, IP address and time, and the way to Security & access.
func (m *smtpMailer) newLoginMessage(to, workspaceKey, platform, address string, at time.Time) (mailMessage, error) {
	platform, address = headerText(platform), headerText(address)
	if platform == "" {
		platform = "Unknown browser"
	}
	when := at.UTC().Format("Mon, 02 Jan 2006 15:04:05") + " (UTC)"
	security := m.workspaceURL(workspaceKey, "/settings/account/security")
	preheader := "Login detected with " + platform
	if address != "" {
		preheader += " from " + address
	}
	fields := []emailField{{"Platform", platform}}
	if address != "" {
		fields = append(fields, emailField{"IP address", address})
	}
	fields = append(fields, emailField{"Time", when})
	warning := "If you didn’t authorize this or don’t recognize this activity, you should log out your sessions and contact your workspace admin immediately."
	html, err := m.renderEmail(emailLayout{
		Title:     "New login to Flow",
		Preheader: preheader,
		Heading:   "New login to Flow",
		Blocks: []emailBlock{
			{Kind: "p", HTML: "We noticed a login to Flow from a new device or browser."},
			{Kind: "rule"},
			{Kind: "fields", Fields: fields},
			{Kind: "rule"},
			{Kind: "p", HTML: emailHTML("Go to %s settings for a list of your active sessions and devices.", emailLink(security, "Security & access"))},
			{Kind: "button", Label: "Security & access", URL: security},
			{Kind: "p", HTML: emailHTML("%s", warning)},
		},
	})
	if err != nil {
		return mailMessage{}, err
	}
	var text strings.Builder
	text.WriteString(preheader + "\n\nNew login to Flow\n\nWe noticed a login to Flow from a new device or browser.\n\n")
	for _, field := range fields {
		text.WriteString(field.Label + ": " + field.Value + "\n")
	}
	text.WriteString("\nGo to Security & access [" + security + "] settings for a list of your active sessions and devices.\n\nSecurity & access [" + security + "]\n\n" + warning + "\n")
	return mailMessage{To: to, Subject: "New login to Flow", Text: text.String(), HTML: html}, nil
}

// onboardingClosing is the end of the onboarding emails: how to get help (when FLOW_SUPPORT_EMAIL is set) and
// the sign-off, in HTML blocks and text.
func onboardingClosing() ([]emailBlock, string) {
	blocks := []emailBlock{{Kind: "rule"}}
	text := ""
	if support := supportEmail(); support != "" {
		blocks = append(blocks, emailBlock{Kind: "p", HTML: emailHTML("If you need anything, reply to this email or reach out at %s.", emailLink("mailto:"+support, support))})
		text += "If you need anything, reply to this email or reach out at " + support + ".\n\n"
	}
	blocks = append(blocks, emailBlock{Kind: "p", HTML: "— The Flow team"})
	return blocks, text + "— The Flow team\n"
}

// welcomeMessage is "Welcome to Flow": what Flow is and three places to start in the person's workspace.
func (m *smtpMailer) welcomeMessage(to, workspaceKey, unsubscribe string) (mailMessage, error) {
	intro := "Flow is where your team plans, tracks, and ships its work together. Here are a few ways to get up to speed."
	starts := []struct{ label, url, description string }{
		{"Getting started", m.workspaceURL(workspaceKey, ""), "Your workspace opens with Getting started issues that walk through issues, projects, and the agent."},
		{"Ask Flow Agent", m.workspaceURL(workspaceKey, "/agent"), "Find, file, and update work by asking in plain language."},
		{"Invite your team", m.workspaceURL(workspaceKey, "/settings/members"), "Flow works best with the people you build with."},
	}
	blocks := []emailBlock{{Kind: "p", HTML: emailHTML("%s", intro)}, {Kind: "rule"}}
	text := "Let’s get you up to speed\n\nWelcome to Flow\n\n" + intro + "\n\n"
	for _, start := range starts {
		blocks = append(blocks, emailBlock{Kind: "link", Label: start.label, URL: start.url, HTML: emailHTML("%s", start.description)})
		text += start.label + " → [" + start.url + "]\n" + start.description + "\n\n"
	}
	closing, closingText := onboardingClosing()
	blocks = append(blocks, closing...)
	text += closingText + "\nUnsubscribe [" + unsubscribe + "]\n"
	html, err := m.renderEmail(emailLayout{Title: "Welcome to Flow", Preheader: "Let’s get you up to speed", Heading: "Welcome to Flow", RegularHeading: true, Blocks: blocks, Unsubscribe: unsubscribe})
	if err != nil {
		return mailMessage{}, err
	}
	return mailMessage{To: to, ReplyTo: supportEmail(), Subject: "Welcome to Flow", Text: text, HTML: html, ListUnsubscribe: unsubscribe, OneClick: true}, nil
}

// mcpOnboardingMessage is "Connect Flow to your AI tools with MCP": what Flow's MCP server does and its URL on
// this instance.
func (m *smtpMailer) mcpOnboardingMessage(to, workspaceKey, unsubscribe string) (mailMessage, error) {
	server := m.appURL + "/mcp"
	connected := m.workspaceURL(workspaceKey, "/settings/account/security")
	intro := "Flow's MCP server gives AI tools that support the Model Context Protocol secure access to your Flow workspace, so you can work with issues, projects, and comments from the tool you're already in, like Claude, Cursor, VS Code, and other MCP clients."
	items := []struct{ lead, rest string }{
		{"Create and update issues from your editor or chat.", "File a bug or a task in plain language while you work, without switching back to Flow."},
		{"Ask questions about your workspace.", "See what's assigned to you, what's in a project, or what's left in the cycle."},
	}
	list := make([]template.HTML, 0, len(items))
	text := intro + "\n\nA couple of things you can do once it's connected:\n\n"
	for _, item := range items {
		list = append(list, emailHTML("%s %s", emailStrong(item.lead), item.rest))
		text += "* " + item.lead + " " + item.rest + "\n"
	}
	setup := "Add this server URL to your client. The first time it connects, you sign in to Flow and approve it:"
	text += "\n" + setup + "\n" + server + "\n\nManage connected apps [" + connected + "]\n\n"
	blocks := []emailBlock{
		{Kind: "p", HTML: emailHTML("%s", intro)},
		{Kind: "p", HTML: "A couple of things you can do once it's connected:"},
		{Kind: "list", Items: list},
		{Kind: "p", HTML: emailHTML("%s", setup)},
		{Kind: "code", Label: server},
		{Kind: "button", Label: "Manage connected apps", URL: connected},
	}
	closing, closingText := onboardingClosing()
	blocks = append(blocks, closing...)
	text = "Connect Flow to your AI tools with MCP\n\n" + text + closingText + "\nUnsubscribe [" + unsubscribe + "]\n"
	subject := "Connect Flow to your AI tools with MCP"
	html, err := m.renderEmail(emailLayout{Title: subject, Preheader: "Work with Flow from Claude, Cursor, and other AI tools.", Heading: emailHTML("%s", subject), RegularHeading: true, Blocks: blocks, Unsubscribe: unsubscribe})
	if err != nil {
		return mailMessage{}, err
	}
	return mailMessage{To: to, ReplyTo: supportEmail(), Subject: subject, Text: text, HTML: html, ListUnsubscribe: unsubscribe, OneClick: true}, nil
}
