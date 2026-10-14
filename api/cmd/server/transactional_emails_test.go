package main

import (
	"crypto/sha256"
	"encoding/base64"
	"html/template"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// mailSink is a test transport: every message the mailer would send over SMTP, parsed.
type mailSink struct {
	mu   sync.Mutex
	mail []parsedMail
}

func newSinkMailer(t *testing.T) (*smtpMailer, *mailSink) {
	sink := &mailSink{}
	mailer := &smtpMailer{from: "notifications@flow.example.test", appURL: "https://flow.example.test"}
	mailer.transport = func(envelope string, to []string, message []byte) error {
		if envelope != "notifications@flow.example.test" || len(to) != 1 {
			t.Errorf("envelope=%q to=%v", envelope, to)
		}
		parsed := parseComposedMail(t, message)
		sink.mu.Lock()
		sink.mail = append(sink.mail, parsed)
		sink.mu.Unlock()
		return nil
	}
	return mailer, sink
}

// take returns and forgets the captured mail.
func (s *mailSink) take() []parsedMail {
	s.mu.Lock()
	defer s.mu.Unlock()
	mail := s.mail
	s.mail = nil
	return mail
}

func mailSubject(t *testing.T, email parsedMail) string {
	t.Helper()
	subject, err := new(mime.WordDecoder).DecodeHeader(email.header.Get("Subject"))
	if err != nil {
		t.Fatal(err)
	}
	return subject
}

// subjectsTo lists the subjects of mail addressed to recipient.
func subjectsTo(t *testing.T, mail []parsedMail, recipient string) []string {
	subjects := []string{}
	for _, email := range mail {
		if email.header.Get("To") == recipient {
			subjects = append(subjects, mailSubject(t, email))
		}
	}
	return subjects
}

// mailComposer composes a built message the way deliver would, parses it back and checks what every Flow
// email shares.
func mailComposer(t *testing.T, mailer *smtpMailer) func(mailMessage, error) parsedMail {
	return func(message mailMessage, err error) parsedMail { return composeParsed(t, mailer, message, err) }
}

func composeParsed(t *testing.T, mailer *smtpMailer, message mailMessage, err error) parsedMail {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := mailer.compose(message)
	if err != nil {
		t.Fatal(err)
	}
	email := parseComposedMail(t, raw)
	if strings.Contains(strings.ToLower(email.raw+email.text+email.html), "linear") {
		t.Fatalf("%q mentions another product", message.Subject)
	}
	for _, want := range []string{`id="main" width="602"`, `src="https://flow.example.test/email/flow-logotype@4x.png"`, `>Flow</a>`} {
		if email.html != "" && !strings.Contains(email.html, want) {
			t.Errorf("%q html lacks %q", message.Subject, want)
		}
	}
	return email
}

func requireContains(t *testing.T, label, value string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(value, want) {
			t.Errorf("%s lacks %q", label, want)
		}
	}
}

func TestApplicationInstalledAndRevokedEmails(t *testing.T) {
	mailer := &smtpMailer{from: "notifications@flow.example.test", appURL: "https://flow.example.test"}
	compose := mailComposer(t, mailer)
	workspace := domain.Workspace{Name: "Acme <Ops>", URLKey: "acme"}
	installed := compose(mailer.applicationInstalledMessage("admin@example.test", `QA "<bot>"`, workspace, nil, true))
	if subject := mailSubject(t, installed); subject != `The "QA "<bot>"" application has been installed in your workspace` || installed.header.Get("Reply-To") != "" {
		t.Fatalf("subject=%q", subject)
	}
	if installed.text != "The \"QA \"<bot>\"\" application now has access to your Acme <Ops> workspace. The application's user has access to all public teams. \r\nTo view all applications you have authorized visit your settings at: https://flow.example.test/acme/settings/applications\r\n" {
		t.Fatalf("text=%q", installed.text)
	}
	requireContains(t, "installed html", installed.html,
		`<h1 style="font-size:24px;line-height:1.2;font-weight:700;`,
		`>The &#34;QA &#34;&lt;bot&gt;&#34;&#34; application has been installed in your workspace</h1>`,
		`<p>The <strong>QA &#34;&lt;bot&gt;&#34;</strong> application now has access to your Acme &lt;Ops&gt; workspace.</p>`,
		`<p>The application's user has access to all public teams.</p>`,
		`href="https://flow.example.test/acme/settings/applications"`, `>View authorized applications</a>`)
	teams := compose(mailer.applicationInstalledMessage("admin@example.test", "Bot", workspace, []string{"Web", "Mobile"}, false))
	requireContains(t, "team access", teams.html, `access to these teams: <strong>Web</strong>, <strong>Mobile</strong>.`)
	single := compose(mailer.applicationInstalledMessage("admin@example.test", "Bot", workspace, []string{"Web"}, false))
	requireContains(t, "single team", single.text, "access to the Web team.")

	revoked := compose(mailer.applicationRevokedMessage("dev@example.test", "QA – reconsent", "flow-app"))
	if subject := mailSubject(t, revoked); subject != `All OAuth tokens for the application "QA – reconsent" were revoked` {
		t.Fatalf("subject=%q", subject)
	}
	if revoked.text != "The OAuth application \"QA – reconsent\" in your flow-app workspace was deactivated and all tokens were revoked.\r\n" {
		t.Fatalf("text=%q", revoked.text)
	}
	requireContains(t, "revoked html", revoked.html,
		`>An OAuth application on your account has had its tokens revoked and been archived</h1>`,
		`<p>The OAuth application <strong>QA – reconsent</strong> in your flow-app workspace was deactivated and all tokens were revoked.</p>`)
	if strings.Contains(revoked.html, "#5E6AD2") {
		t.Fatal("revoked email has a button")
	}
}

func TestNewLoginWelcomeAndMCPEmails(t *testing.T) {
	t.Setenv("FLOW_SUPPORT_EMAIL", "support@flow.example.test")
	mailer := &smtpMailer{from: "notifications@flow.example.test", appURL: "https://flow.example.test"}
	compose := mailComposer(t, mailer)
	at := time.Date(2026, 9, 28, 1, 25, 45, 0, time.UTC)
	login := compose(mailer.newLoginMessage("pat@example.test", "acme", "Chrome on <macOS>", "203.0.113.9", at))
	if mailSubject(t, login) != "New login to Flow" || login.header.Get("List-Unsubscribe") != "" {
		t.Fatalf("login headers=%v", login.header)
	}
	requireContains(t, "login text", login.text, "Login detected with Chrome on <macOS> from 203.0.113.9", "Platform: Chrome on <macOS>\r\nIP address: 203.0.113.9\r\nTime: Mon, 28 Sep 2026 01:25:45 (UTC)", "Security & access [https://flow.example.test/acme/settings/account/security]")
	requireContains(t, "login html", login.html, `>New login to Flow</h1>`, `<strong style="font-weight:600;color:#3C4149">Platform:</strong> Chrome on &lt;macOS&gt;`, `<strong style="font-weight:600;color:#3C4149">Time:</strong> Mon, 28 Sep 2026 01:25:45 (UTC)`, `>Security &amp; access</a> settings for a list`, `href="https://flow.example.test/acme/settings/account/security" style="margin-top:8px`)

	unsubscribe := "https://flow.example.test/api/email/unsubscribe?list=onboarding&sig=abc&user=usr_1"
	welcome := compose(mailer.welcomeMessage("pat@example.test", "acme", unsubscribe))
	if mailSubject(t, welcome) != "Welcome to Flow" || welcome.header.Get("Reply-To") != "support@flow.example.test" || welcome.header.Get("List-Unsubscribe") != "<"+unsubscribe+">" || welcome.header.Get("List-Unsubscribe-Post") != "List-Unsubscribe=One-Click" {
		t.Fatalf("welcome headers=%v", welcome.header)
	}
	requireContains(t, "welcome html", welcome.html, `font-weight:500;`, `>Welcome to Flow</h1>`, `href="https://flow.example.test/acme" style="font-weight:600;color:#66f">Getting started &rarr;</a>`, `/acme/agent"`, `/acme/settings/members"`, `support@flow.example.test`, `— The Flow team`, `<a href="https://flow.example.test/api/email/unsubscribe?list=onboarding&amp;sig=abc&amp;user=usr_1">Unsubscribe</a>`)
	requireContains(t, "welcome text", welcome.text, "Welcome to Flow", "Getting started → [https://flow.example.test/acme]", "Unsubscribe ["+unsubscribe+"]")

	mcp := compose(mailer.mcpOnboardingMessage("pat@example.test", "", unsubscribe))
	if mailSubject(t, mcp) != "Connect Flow to your AI tools with MCP" || mcp.header.Get("List-Unsubscribe-Post") == "" {
		t.Fatalf("mcp headers=%v", mcp.header)
	}
	requireContains(t, "mcp html", mcp.html, `<ul><li><strong>Create and update issues from your editor or chat.</strong>`, `>https://flow.example.test/mcp</code>`, `href="https://flow.example.test/" style="margin-top:8px`, `>Manage connected apps</a>`)
	requireContains(t, "mcp text", mcp.text, "* Ask questions about your workspace.", "https://flow.example.test/mcp\r\n")
}

func TestNotificationEmailLayout(t *testing.T) {
	mailer := &smtpMailer{from: "notifications@flow.example.test", appURL: "https://flow.example.test"}
	compose := mailComposer(t, mailer)
	items := []notificationEmailItem{{Title: "Triage <agent>", URL: "https://flow.example.test/acme/inbox", Icon: mailer.emailAsset("flow-mark-128.png"), Line: "Loop failed & stopped"}}
	email := compose(mailer.notificationMessage("pat@example.test", "acme", "Triage <agent>", items, false))
	settings := "https://flow.example.test/acme/settings/account/notifications/email"
	if mailSubject(t, email) != "Triage <agent>" || email.header.Get("List-Unsubscribe") != "<"+settings+">" || email.header.Get("List-Unsubscribe-Post") != "" {
		t.Fatalf("headers=%v", email.header)
	}
	if email.text != "You have 1 unread notification on Flow.\r\n\r\nTriage <agent>\r\nLoop failed & stopped\r\nhttps://flow.example.test/acme/inbox\r\n\r\nOpen your Inbox: https://flow.example.test/acme/inbox\r\n\r\nUnsubscribe from these emails: "+settings+"\r\n" {
		t.Fatalf("text=%q", email.text)
	}
	requireContains(t, "notification html", email.html, `>You have an unread notification on Flow.</h1>`, `border-bottom:1px solid #EFF1F4`, `href="https://flow.example.test/acme/inbox">Triage &lt;agent&gt;</a>`, `src="https://flow.example.test/email/flow-mark-128.png"`, `<span>Loop failed &amp; stopped</span>`, `>Open your Inbox</a>`, `<a href="`+settings+`">Unsubscribe</a>`)
	two := compose(mailer.notificationMessage("pat@example.test", "acme", "Flow: 2 notifications", append(items, items[0]), false))
	requireContains(t, "digest", two.html, `>You have 2 unread notifications on Flow.</h1>`)
	hidden := compose(mailer.notificationMessage("pat@example.test", "acme", "Secret title", items, true))
	if mailSubject(t, hidden) != "Flow notification" || strings.Contains(hidden.html+hidden.text, "Triage") || strings.Contains(hidden.html+hidden.text, "Loop failed") {
		t.Fatal("protected notification content leaked")
	}
}

func TestSignInSendsNewLoginAndWelcomeOnceAndOnboardingFollowUp(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	t.Setenv("FLOW_ONBOARDING_MCP_DELAY", "")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "emails.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	mailer, sink := newSinkMailer(t)
	s := &server{store: repository, uploadPath: t.TempDir(), mailer: mailer}
	host := httptest.NewServer(newHandler(s))
	defer host.Close()

	client, user := verifiedAuthClient(t, host.URL, "Pat", "pat@example.test")
	if got := subjectsTo(t, sink.take(), "pat@example.test"); strings.Join(got, "|") != "Verify your email|New login to Flow|Welcome to Flow" {
		t.Fatalf("sign-up mail=%v", got)
	}
	login := map[string]string{"email": "pat@example.test", "password": "initial-pass"}
	authRequest[domain.AuthSession](t, client, http.MethodPost, host.URL+"/api/auth/login", login, "", http.StatusOK)
	if got := sink.take(); len(got) != 0 {
		t.Fatalf("known device was alerted: %v", subjectsTo(t, got, "pat@example.test"))
	}
	other := authClient(t)
	authRequest[domain.AuthSession](t, other, http.MethodPost, host.URL+"/api/auth/login", login, "", http.StatusOK)
	got := sink.take()
	if subjects := subjectsTo(t, got, "pat@example.test"); strings.Join(subjects, "|") != "New login to Flow" {
		t.Fatalf("new device mail=%v", subjects)
	}
	requireContains(t, "login alert", got[0].text, "IP address: 127.0.0.1")

	now := time.Now().UTC()
	s.sendDueOnboardingEmails(t.Context(), now)
	if got := sink.take(); len(got) != 0 {
		t.Fatal("follow-up sent before its delay")
	}
	s.sendDueOnboardingEmails(t.Context(), now.Add(8*24*time.Hour))
	followUp := sink.take()
	if subjects := subjectsTo(t, followUp, "pat@example.test"); strings.Join(subjects, "|") != "Connect Flow to your AI tools with MCP" {
		t.Fatalf("follow-up=%v", subjects)
	}
	s.sendDueOnboardingEmails(t.Context(), now.Add(9*24*time.Hour))
	if got := sink.take(); len(got) != 0 {
		t.Fatal("follow-up sent twice")
	}

	// A second account unsubscribes from the welcome email's link and gets no follow-up.
	verifiedAuthClient(t, host.URL, "Sam", "sam@example.test")
	welcome := sink.take()
	var unsubscribe string
	for _, email := range welcome {
		if mailSubject(t, email) == "Welcome to Flow" {
			unsubscribe = strings.Trim(email.header.Get("List-Unsubscribe"), "<>")
		}
	}
	if !strings.HasPrefix(unsubscribe, "https://flow.example.test/api/email/unsubscribe?") {
		t.Fatalf("unsubscribe=%q", unsubscribe)
	}
	local := host.URL + strings.TrimPrefix(unsubscribe, "https://flow.example.test")
	response, err := http.Get(local)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(page), `<form method="post">`) {
		t.Fatalf("confirm page %d %s", response.StatusCode, page)
	}
	forged := strings.Replace(local, "sig=", "sig=0", 1)
	if response, err := http.Post(forged, "application/x-www-form-urlencoded", strings.NewReader("List-Unsubscribe=One-Click")); err != nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("forged unsubscribe status=%v err=%v", response, err)
	}
	response, err = http.Post(local, "application/x-www-form-urlencoded", strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("one-click unsubscribe status=%v err=%v", response, err)
	}
	response.Body.Close()
	s.sendDueOnboardingEmails(t.Context(), now.Add(10*24*time.Hour))
	if got := sink.take(); len(got) != 0 {
		t.Fatalf("unsubscribed account got %v", subjectsTo(t, got, "sam@example.test"))
	}
	if user.ID == "" {
		t.Fatal("registered user missing")
	}
}

func TestOnboardingEmailsCanBeTurnedOff(t *testing.T) {
	t.Setenv("FLOW_ONBOARDING_EMAILS", "false")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "quiet.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	mailer, sink := newSinkMailer(t)
	host := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir(), mailer: mailer}))
	defer host.Close()
	verifiedAuthClient(t, host.URL, "Quinn", "quinn@example.test")
	if got := subjectsTo(t, sink.take(), "quinn@example.test"); strings.Join(got, "|") != "Verify your email|New login to Flow" {
		t.Fatalf("mail=%v", got)
	}
}

func TestOAuthInstallAndDeleteEmailTheRightPeopleOnce(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "oauth-mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	mailer, sink := newSinkMailer(t)
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir(), mailer: mailer}))
	defer host.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", 200)
	sink.take()
	data := repo.Bootstrap()

	registered := authRequest[domain.OAuthClient](t, admin, "POST", host.URL+"/oauth/register", map[string]any{"client_name": "QA <worker>", "redirect_uris": []string{"https://example.test/callback"}, "token_endpoint_auth_method": "none"}, "", 201)
	verifier := strings.Repeat("v", 48)
	digest := sha256.Sum256([]byte(verifier))
	input := map[string]any{"actor": "app", "clientId": registered.ClientID, "redirectUri": registered.RedirectURIs[0], "responseType": "code", "scope": "read app:mentionable", "codeChallenge": base64.RawURLEncoding.EncodeToString(digest[:]), "codeChallengeMethod": "S256", "workspaceKey": data.Workspace.URLKey, "approve": true, "teamIds": []string{data.Teams[0].ID}}
	authRequest[map[string]string](t, admin, "POST", host.URL+"/api/oauth/authorization-request", input, "", 200)
	installed := sink.take()
	want := `The "QA <worker>" application has been installed in your workspace`
	if subjects := subjectsTo(t, installed, "admin@example.test"); len(installed) != 1 || strings.Join(subjects, "|") != want {
		t.Fatalf("install mail=%v (total %d)", subjects, len(installed))
	}
	requireContains(t, "install html", installed[0].html, "<strong>"+template.HTMLEscapeString(data.Teams[0].Name)+"</strong> team.")
	// Authorizing the installed application again ("Update access") is not a new installation.
	authRequest[map[string]string](t, admin, "POST", host.URL+"/api/oauth/authorization-request", input, "", 200)
	if got := sink.take(); len(got) != 0 {
		t.Fatalf("re-authorization emailed %v", subjectsTo(t, got, "admin@example.test"))
	}
	// A user-actor authorization (an MCP client acting as the person) is not an installation.
	delete(input, "actor")
	authRequest[map[string]string](t, admin, "POST", host.URL+"/api/oauth/authorization-request", input, "", 200)
	if got := sink.take(); len(got) != 0 {
		t.Fatal("user authorization sent an installation email")
	}

	app := authRequest[domain.OAuthApplication](t, admin, "POST", host.URL+"/api/oauth-applications", map[string]any{"name": "QA – leftovers", "scopes": []string{"read"}, "supportsClientCredentials": true}, data.Workspace.URLKey, 201)
	if got := sink.take(); len(got) != 0 {
		t.Fatal("creating an application sent mail")
	}
	authRequest[any](t, admin, "DELETE", host.URL+"/api/oauth-applications/"+url.PathEscape(app.ID), nil, data.Workspace.URLKey, 204)
	revoked := sink.take()
	if subjects := subjectsTo(t, revoked, "admin@example.test"); len(revoked) != 1 || strings.Join(subjects, "|") != `All OAuth tokens for the application "QA – leftovers" were revoked` {
		t.Fatalf("delete mail=%v (total %d)", subjects, len(revoked))
	}
	requireContains(t, "revoked text", revoked[0].text, "in your "+data.Workspace.Name+" workspace")
}

func TestNotificationEmailItemsDescribeTheirSource(t *testing.T) {
	mailer := &smtpMailer{appURL: "https://flow.example.test"}
	s := &server{mailer: mailer}
	data := &domain.Bootstrap{Issues: []domain.Issue{{ID: "issue_1", Identifier: "WEB-1", Title: "Fix <login>"}}}
	person := domain.User{ID: "usr_2", DisplayName: "Ada", AvatarURL: "https://cdn.example.test/ada.png"}
	item, err := s.notificationEmailItem(data, "acme", domain.Notification{Type: "assignment", IssueID: "issue_1", Actor: person})
	if err != nil || item.Title != "WEB-1 Fix <login>" || item.URL != "https://flow.example.test/acme/issue/WEB-1" || item.Line != "Ada assigned the issue to you" || item.Icon != person.AvatarURL {
		t.Fatalf("issue item=%#v err=%v", item, err)
	}
	agent, err := s.notificationEmailItem(data, "acme", domain.Notification{Title: "Triage agent", Text: "Loop failed to run", Actor: domain.User{ID: "app_1", App: true}})
	if err != nil || agent.Title != "Triage agent" || agent.Line != "Loop failed to run" || agent.URL != "https://flow.example.test/acme/inbox" || agent.Icon != "https://flow.example.test/email/flow-mark-128.png" {
		t.Fatalf("agent item=%#v err=%v", agent, err)
	}
	if _, err := s.notificationEmailItem(data, "acme", domain.Notification{IssueID: "missing"}); err == nil {
		t.Fatal("missing issue produced an email item")
	}
}
