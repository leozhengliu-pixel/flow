package main

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Create an issue from a message. The platform's message shortcut
// (消息快捷操作) opens the app's web page in the chat sidebar with a trigger code; the
// page (served here) reads the selected message with the client JSAPI
// getBlockActionSourceDetail, signs the member in with a one-time code
// (tt.requestAccess) and hands both to Flow. Flow resolves the code to the
// member's platform account, which must be linked to a Flow account, and
// sends them a private form card: team, title and description prefilled from
// the message (with a link back to its chat), assignee and priority.
// Submitting creates the issue as that member with their permissions, then
// replies under the original message with the issue card.
//
// The message itself is read again by the bot. Only a message the bot can
// read is trusted to be in its chat: then a bound group's rules apply (its
// "create issues" option and team scope; the reply is the binding's issue
// card) and other chats get a reply only for public-team issues. When the
// bot cannot read it (not in the chat, or without the message permissions),
// the claimed group still restricts the form, but the result goes to the
// member's bot conversation only.

const (
	chatMessageActionKind = "message_action" // inbound command: send the form
	chatMessageCreateKind = "message_create" // stored form action
	chatMessageResultKind = "message_result" // card job: reply with the result
	// The platform's H5 JS SDK, as its message shortcut guide loads it.
	chatH5SDKURL = "https://lf1-cdn-tos.bytegoofy.com/goofy/lark/op/h5-js-sdk-1.5.26.js"
)

func (s *server) registerChatMessageActionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/integrations/{provider}/{id}/chat/message-action", s.chatMessageActionPage)
	mux.HandleFunc("GET /api/integrations/{provider}/{id}/chat/message-action/config", s.chatMessageActionConfig)
	mux.HandleFunc("POST /api/integrations/{provider}/{id}/chat/message-action", s.submitChatMessageAction)
}

// chatMessageActionRoute: the shortcut page and its calls authenticate the
// member with the platform's one-time code, not a Flow session.
func chatMessageActionRoute(path string) bool {
	return strings.HasPrefix(path, "/api/integrations/") && (strings.HasSuffix(path, "/chat/message-action") || strings.HasSuffix(path, "/chat/message-action/config"))
}

func (s *server) chatMessageActionURL(record domain.ChatIntegrationRecord) string {
	return s.chatAppOrigin() + "/api/integrations/" + url.PathEscape(record.Provider) + "/" + url.PathEscape(record.ConnectionID) + "/chat/message-action"
}

func (s *server) chatMessageActionRecord(w http.ResponseWriter, r *http.Request) (domain.ChatIntegrationRecord, bool) {
	record, err := s.store.ChatIntegrationByID(r.Context(), r.PathValue("provider"), r.PathValue("id"))
	if err != nil || !chatProvider(record.Provider) {
		writeChatError(w, http.StatusNotFound, "Chat application not configured")
		return record, false
	}
	if !record.Connected || !chatTenantAuthorized(record) {
		writeChatError(w, http.StatusConflict, chatTenantRefusal(record))
		return record, false
	}
	return record, true
}

const chatMessageActionStyle = `body{font:14px/1.5 -apple-system,BlinkMacSystemFont,"PingFang SC","Segoe UI",sans-serif;margin:24px;color:#1f2329;background:#fff}a{color:#3370ff}p{margin:0 0 12px}`

const chatMessageActionScript = `(function () {
  var zh = (navigator.language || "").toLowerCase().indexOf("zh") === 0;
  var text = zh ? {
    working: "正在读取消息…", open: "请在消息的「更多」菜单中选择「创建事项」。", client: "请在飞书或 Lark 客户端中打开。",
    sent: "已将创建表单发送到你与 Flow 机器人的会话。", link: "请先关联你的 Flow 账号：", failed: "无法完成操作。错误码："
  } : {
    working: "Reading the message…", open: "Choose Create issue from a message's More menu.", client: "Open this page in the Feishu or Lark app.",
    sent: "The create form is in your Flow bot conversation.", link: "Connect your Flow account first: ", failed: "Could not complete this. Code: "
  };
  var status = document.getElementById("status");
  function show(message, href) {
    status.textContent = message;
    if (href) { var a = document.createElement("a"); a.href = href; a.textContent = href; a.target = "_blank"; status.appendChild(a); }
  }
  var trigger = "";
  try { trigger = JSON.parse(new URLSearchParams(location.search).get("bdp_launch_query") || "{}").__trigger_id__ || ""; } catch (e) {}
  if (!trigger) { show(text.open); return; }
  if (!window.h5sdk || !window.tt) { show(text.client); return; }
  show(text.working);
  var base = location.pathname;
  function fail(code) { show(text.failed + (code || "chat_message_unavailable")); }
  function access(appId, done) {
    tt.requestAccess({ appID: appId, scopeList: [], success: function (res) { done(res.code); }, fail: function () {
      tt.requestAuthCode({ appId: appId, success: function (res) { done(res.code); }, fail: function () { fail("chat_authorization_declined"); } });
    } });
  }
  fetch(base + "/config?url=" + encodeURIComponent(location.href.split("#")[0])).then(function (r) { return r.json(); }).then(function (config) {
    if (!config.signature) { fail(config.code); return; }
    window.h5sdk.config({ appId: config.appId, timestamp: config.timestamp, nonceStr: config.nonceStr, signature: config.signature, jsApiList: ["getBlockActionSourceDetail"], onFail: function () { fail("chat_jsapi_unavailable"); } });
    window.h5sdk.ready(function () {
      access(config.appId, function (code) {
        tt.getBlockActionSourceDetail({ triggerCode: trigger, success: function (detail) {
          var message = ((detail.content || {}).messages || [])[0] || {};
          var body = { code: code, messageId: message.openMessageId || "", chatId: message.openChatId || "", messageType: message.messageType || "", content: message.support ? (message.content || "") : "" };
          fetch(base, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }).then(function (r) { return r.json(); }).then(function (result) {
            if (result.status === "sent") { show(text.sent); } else if (result.status === "not_linked") { show(text.link, result.linkUrl); } else { fail(result.code); }
          }, function () { fail(); });
        }, fail: function () { fail("chat_message_unavailable"); } });
      });
    });
  }, function () { fail(); });
})();`

var chatMessageActionPolicy = func() string {
	script := sha256.Sum256([]byte(chatMessageActionScript))
	style := sha256.Sum256([]byte(chatMessageActionStyle))
	return "default-src 'none'; script-src 'sha256-" + base64.StdEncoding.EncodeToString(script[:]) + "' https://lf1-cdn-tos.bytegoofy.com; style-src 'sha256-" + base64.StdEncoding.EncodeToString(style[:]) + "'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'"
}()

var chatMessageActionTemplate = template.Must(template.New("message-action").Parse(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Flow</title><style>{{.Style}}</style><script src="{{.SDK}}"></script></head><body><p><strong>Flow</strong></p><p id="status"></p><script>{{.Script}}</script></body></html>`))

func (s *server) chatMessageActionPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.chatMessageActionRecord(w, r); !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", chatMessageActionPolicy)
	_ = chatMessageActionTemplate.Execute(w, map[string]any{"Style": template.CSS(chatMessageActionStyle), "SDK": chatH5SDKURL, "Script": template.JS(chatMessageActionScript)})
}

var chatJSAPITickets = struct {
	sync.Mutex
	items map[string]chatCachedToken
}{items: map[string]chatCachedToken{}}

// chatJSAPITicket is the app's JSAPI ticket (POST /open-apis/jssdk/ticket/get
// with the tenant token), cached until shortly before it expires.
func (s *server) chatJSAPITicket(ctx context.Context, record domain.ChatIntegrationRecord) (string, error) {
	key := record.Provider + "\x00" + record.ConnectionID + "\x00" + record.AppID
	chatJSAPITickets.Lock()
	cached, ok := chatJSAPITickets.items[key]
	chatJSAPITickets.Unlock()
	if ok && time.Now().Before(cached.ExpiresAt) {
		return cached.Token, nil
	}
	var response struct {
		Data struct {
			Ticket   string `json:"ticket"`
			ExpireIn int    `json:"expire_in"`
		} `json:"data"`
	}
	if err := s.chatPlatformRequest(ctx, record, http.MethodPost, "/open-apis/jssdk/ticket/get", map[string]string{}, &response); err != nil {
		return "", err
	}
	if response.Data.Ticket == "" {
		return "", errorsChatResponse()
	}
	lifetime := time.Duration(max(response.Data.ExpireIn-300, 60)) * time.Second
	chatJSAPITickets.Lock()
	chatJSAPITickets.items[key] = chatCachedToken{Token: response.Data.Ticket, ExpiresAt: time.Now().Add(lifetime)}
	chatJSAPITickets.Unlock()
	return response.Data.Ticket, nil
}

// chatJSAPISignature is the H5 SDK signature: SHA-1 of jsapi_ticket, noncestr,
// timestamp (milliseconds) and the page URL without its fragment.
func chatJSAPISignature(ticket, nonce string, timestamp int64, pageURL string) string {
	digest := sha1.Sum([]byte(fmt.Sprintf("jsapi_ticket=%s&noncestr=%s&timestamp=%d&url=%s", ticket, nonce, timestamp, pageURL)))
	return hex.EncodeToString(digest[:])
}

func (s *server) chatMessageActionConfig(w http.ResponseWriter, r *http.Request) {
	record, ok := s.chatMessageActionRecord(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	// Only the shortcut page itself is signed.
	pageURL := r.URL.Query().Get("url")
	base := s.chatMessageActionURL(record)
	if len(pageURL) > 4096 || pageURL != base && !strings.HasPrefix(pageURL, base+"?") || strings.Contains(pageURL, "#") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "chat_invalid_configuration"})
		return
	}
	ticket, err := s.chatJSAPITicket(r.Context(), record)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"code": "chat_platform_failed"})
		return
	}
	nonce, err := randomSecret("")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "chat_error"})
		return
	}
	nonce = strings.NewReplacer("-", "", "_", "").Replace(nonce)
	timestamp := time.Now().UnixMilli()
	writeJSON(w, http.StatusOK, map[string]any{"appId": record.AppID, "timestamp": timestamp, "nonceStr": nonce, "signature": chatJSAPISignature(ticket, nonce, timestamp, pageURL)})
}

// chatUserForCode resolves a one-time sign-in code of the app's web page to
// the member's platform account (open_id and tenant).
func (s *server) chatUserForCode(ctx context.Context, record domain.ChatIntegrationRecord, code string) (string, string, error) {
	credential, err := s.chatCredentials(ctx, record)
	if err != nil {
		return "", "", err
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	body := map[string]string{"grant_type": "authorization_code", "client_id": record.AppID, "client_secret": credential.AppSecret, "code": code}
	if err := chatRequest(ctx, chatBaseURL(record.Provider)+"/open-apis/authen/v2/oauth/token", http.MethodPost, "", body, &token); err != nil {
		return "", "", err
	}
	if token.AccessToken == "" {
		return "", "", errorsChatResponse()
	}
	var profile struct {
		Data struct {
			OpenID    string `json:"open_id"`
			TenantKey string `json:"tenant_key"`
		} `json:"data"`
	}
	if err := chatRequest(ctx, chatBaseURL(record.Provider)+"/open-apis/authen/v1/user_info", http.MethodGet, token.AccessToken, nil, &profile); err != nil {
		return "", "", err
	}
	if profile.Data.OpenID == "" || profile.Data.TenantKey == "" {
		return "", "", errorsChatResponse()
	}
	return profile.Data.OpenID, profile.Data.TenantKey, nil
}

var chatMentionPlaceholder = regexp.MustCompile(`@_user_\d+`)

// chatMessageText is the plain text of a text or rich text (post) message.
func chatMessageText(messageType, content string) string {
	switch messageType {
	case "text":
		var value struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(content), &value) != nil {
			return ""
		}
		return strings.TrimSpace(chatMentionPlaceholder.ReplaceAllString(value.Text, ""))
	case "post":
		var value struct {
			Title   string `json:"title"`
			Content [][]struct {
				Tag      string `json:"tag"`
				Text     string `json:"text"`
				Href     string `json:"href"`
				UserName string `json:"user_name"`
			} `json:"content"`
		}
		if json.Unmarshal([]byte(content), &value) != nil {
			return ""
		}
		lines := []string{}
		if strings.TrimSpace(value.Title) != "" {
			lines = append(lines, strings.TrimSpace(value.Title))
		}
		for _, paragraph := range value.Content {
			var line strings.Builder
			for _, item := range paragraph {
				switch item.Tag {
				case "text", "a", "md", "code_block":
					line.WriteString(item.Text)
					if item.Tag == "a" && item.Href != "" && item.Href != item.Text {
						line.WriteString(" (" + item.Href + ")")
					}
				case "at":
					line.WriteString("@" + item.UserName)
				}
			}
			lines = append(lines, line.String())
		}
		return strings.TrimSpace(chatMentionPlaceholder.ReplaceAllString(strings.Join(lines, "\n"), ""))
	}
	return ""
}

func chatValidPlatformID(value, prefix string) bool {
	return strings.HasPrefix(value, prefix) && len(value) <= 128 && !strings.ContainsAny(value, "/?#% \\")
}

func (s *server) submitChatMessageAction(w http.ResponseWriter, r *http.Request) {
	record, ok := s.chatMessageActionRecord(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var input struct {
		Code        string `json:"code"`
		MessageID   string `json:"messageId"`
		ChatID      string `json:"chatId"`
		MessageType string `json:"messageType"`
		Content     string `json:"content"`
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil || json.Unmarshal(raw, &input) != nil || input.Code == "" || len(input.Code) > 512 || !chatValidPlatformID(input.MessageID, "om_") || input.ChatID != "" && !chatValidPlatformID(input.ChatID, "oc_") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "chat_message_unavailable"})
		return
	}
	openID, tenantKey, err := s.chatUserForCode(r.Context(), record, input.Code)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "chat_platform_authorization_failed"})
		return
	}
	if tenantKey != record.TenantKey {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "chat_other_tenant"})
		return
	}
	text := chatMessageText(input.MessageType, input.Content)
	command := chatInboundCommand{Kind: chatMessageActionKind, OpenID: openID, ChatID: input.ChatID, MessageID: input.MessageID, Values: map[string]json.RawMessage{}}
	command.Values["text"], _ = json.Marshal(chatTruncateText(text, 8000))
	payload, _ := json.Marshal(command)
	// A double tap opens one form; using the shortcut again later opens a new one.
	dedup := chatKey(record.ConnectionID, "message_action", input.MessageID, openID, strconv.FormatInt(time.Now().Unix()/60, 10))
	if _, err := s.store.EnqueueChatJob(r.Context(), domain.ChatJob{ID: "chat_job_" + dedup, WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Kind: "inbound", DedupKey: dedup, Payload: payload, RunAt: time.Now().UTC()}); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "chat_busy"})
		return
	}
	identity, err := s.store.ChatIdentityForOpenID(r.Context(), record.WorkspaceKey, record.ConnectionID, openID)
	if err != nil || identity.TenantKey != record.TenantKey {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "not_linked", "code": "chat_not_linked", "linkUrl": s.chatConnectionsURL(record)})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "sent"})
}

func (s *server) chatConnectionsURL(record domain.ChatIntegrationRecord) string {
	return fmt.Sprintf("%s/%s/settings/account/connections", s.chatAppOrigin(), url.PathEscape(record.WorkspaceKey))
}

func chatCardI18n(en, zh string, elements ...map[string]any) map[string]any {
	card := chatCard(en, elements...)
	card["header"] = map[string]any{"title": chatI18nPlain(chatTruncateText(en, 100), chatTruncateText(zh, 100)), "template": "blue"}
	return card
}

func chatCardI18nText(en, zh string) map[string]any {
	return map[string]any{"tag": "div", "text": chatI18nPlain(chatTruncateText(en, 2048), chatTruncateText(zh, 2048))}
}

// chatSendLinkAccountCard asks an unlinked member to link their account.
func (s *server) chatSendLinkAccountCard(ctx context.Context, record domain.ChatIntegrationRecord, job domain.ChatJob, openID string) error {
	card := chatCardI18n("Connect your Flow account", "关联你的 Flow 账号", chatCardI18nText("Link your account to create and update Flow issues from chat. Linking never grants Flow access by itself.", "关联账号后即可在聊天中创建和更新 Flow 事项。关联本身不会授予 Flow 访问权限。"), map[string]any{"tag": "action", "actions": []map[string]any{{"tag": "button", "type": "primary", "text": chatI18nPlain("Connect account", "关联账号"), "url": s.chatConnectionsURL(record)}}})
	content, _ := json.Marshal(card)
	return s.chatWorkflowRequest(ctx, record, http.MethodPost, "/open-apis/im/v1/messages?receive_id_type=open_id", map[string]any{"receive_id": openID, "msg_type": "interactive", "content": string(content), "uuid": chatKey(job.DedupKey, "link_card")[:32]}, nil)
}

// chatChatLink opens the message's chat in the client (the platform's
// AppLink opens a chat, not a single message).
func chatChatLink(provider, chatID string) string {
	host := "applink.feishu.cn"
	if provider == "lark" {
		host = "applink.larksuite.com"
	}
	return "https://" + host + "/client/chat/open?openChatId=" + url.QueryEscape(chatID)
}

// chatCreatableTeams are the teams the member can create issues in, their
// own teams first.
func chatCreatableTeams(metadata domain.Bootstrap) []domain.Team {
	guest := strings.EqualFold(metadata.ViewerRole, "guest")
	teams := []domain.Team{}
	for _, team := range metadata.Teams {
		if team.ArchivedAt != nil || team.RetiredAt != nil || !teamContentVisible(metadata, team.ID) || guest && !viewerOnTeam(metadata, team.ID) {
			continue
		}
		teams = append(teams, team)
	}
	slices.SortStableFunc(teams, func(a, b domain.Team) int {
		am, bm := viewerOnTeam(metadata, a.ID), viewerOnTeam(metadata, b.ID)
		switch {
		case am && !bm:
			return -1
		case bm && !am:
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return teams[:min(len(teams), 100)]
}

type chatPlatformMessage struct {
	Data struct {
		Items []struct {
			MessageID string `json:"message_id"`
			ChatID    string `json:"chat_id"`
			MsgType   string `json:"msg_type"`
			Body      struct {
				Content string `json:"content"`
			} `json:"body"`
		} `json:"items"`
	} `json:"data"`
}

// chatMessageActionBinding finds the claimed chat's binding that allows
// creating issues. found reports whether the chat is bound at all.
func (s *server) chatMessageActionBinding(ctx context.Context, record domain.ChatIntegrationRecord, chatID string) (domain.ChatBinding, bool, error) {
	if chatID == "" {
		return domain.ChatBinding{}, false, nil
	}
	bindings, err := s.store.ChatBindings(ctx, record.WorkspaceKey, record.ConnectionID)
	if err != nil {
		return domain.ChatBinding{}, false, err
	}
	found := false
	for _, binding := range bindings {
		if binding.ChatID != chatID || !binding.Enabled || binding.RevokedAt != nil {
			continue
		}
		found = true
		if binding.AllowCreate {
			return binding, true, nil
		}
	}
	return domain.ChatBinding{}, found, nil
}

func (s *server) chatSendMessageActionForm(ctx context.Context, record domain.ChatIntegrationRecord, identity domain.ChatIdentity, metadata domain.Bootstrap, job domain.ChatJob, command chatInboundCommand) error {
	fail := func(code string) error { return s.chatSendActionError(ctx, record, metadata, identity, job, code) }
	language := chatLanguage(metadata, identity.UserID, record.Provider)
	chatID, text, verified := command.ChatID, chatString(command.Values, "text"), false
	var message chatPlatformMessage
	err := s.chatPlatformRequest(ctx, record, http.MethodGet, "/open-apis/im/v1/messages/"+url.PathEscape(command.MessageID), nil, &message)
	var platform *chatPlatformError
	switch {
	case err == nil && len(message.Data.Items) > 0 && message.Data.Items[0].MessageID == command.MessageID && message.Data.Items[0].ChatID != "":
		item := message.Data.Items[0]
		chatID, verified = item.ChatID, true
		if value := chatMessageText(item.MsgType, item.Body.Content); value != "" {
			text = value
		}
	case errors.As(err, &platform) && platform.Retryable:
		return err
	case err != nil && !errors.As(err, &platform):
		return err
	}
	binding, bound, err := s.chatMessageActionBinding(ctx, record, chatID)
	if err != nil {
		return err
	}
	if bound && binding.ID == "" {
		return fail("chat_binding_create_disabled")
	}
	action := chatExtendedAction{chatCardAction: chatCardAction{Kind: chatMessageCreateKind, OpenID: identity.OpenID, SourceMessageID: command.MessageID}, SourceChatID: chatID, Verified: verified}
	var teams []domain.Team
	defaultTeam := ""
	if bound {
		if err := s.chatBindingPermitted(ctx, record, binding); err != nil {
			var refusal chatGroupRefusal
			if errors.As(err, &refusal) || errors.Is(err, store.ErrAuthForbidden) || errors.As(err, &platform) && !platform.Retryable {
				return fail("chat_binding_unavailable")
			}
			return err
		}
		if _, err := chatCreateDefaults(metadata, binding, "Issue"); err != nil {
			return fail("chat_team_unavailable")
		}
		action.BindingID, action.BindingScope, action.ChatID = binding.ID, chatBindingFingerprint(binding), binding.ChatID
		for _, team := range metadata.Teams {
			if team.ID == binding.TeamID {
				teams = append(teams, team)
			}
		}
		defaultTeam = binding.TeamID
	} else {
		teams = chatCreatableTeams(metadata)
	}
	if len(teams) == 0 {
		return fail("chat_team_unavailable")
	}
	if defaultTeam == "" {
		defaultTeam = teams[0].ID
	}
	teamOptions := make([]map[string]any, 0, len(teams))
	for _, team := range teams {
		teamOptions = append(teamOptions, chatOption(team.ID, team.Name))
		action.TeamIDs = append(action.TeamIDs, team.ID)
	}
	title := text
	if index := strings.IndexByte(title, '\n'); index >= 0 {
		title = title[:index]
	}
	if runes := []rune(strings.TrimSpace(title)); len(runes) > 200 {
		title = string(runes[:200])
	}
	action.Title = strings.TrimSpace(title)
	source := "Source message: "
	if language == "zh" {
		source = "来源消息："
	}
	link := ""
	if chatID != "" {
		link = "\n\n" + source + chatChatLink(record.Provider, chatID)
	}
	description := []rune(text)
	if room := 1000 - utf8.RuneCountInString(link); len(description) > room {
		description = append(description[:max(room-1, 0)], '…')
	}
	assignees, assigneeIDs := chatAssigneeOptions(metadata, identity.UserID)
	action.AssigneeIDs = assigneeIDs
	priority := teamSettings(&metadata, defaultTeam).DefaultPriority
	descriptionInput := map[string]any{"tag": "input", "name": "description", "input_type": "multiline_text", "rows": 4, "placeholder": chatI18nPlain("Description", "描述"), "default_value": strings.TrimSpace(string(description) + link), "required": false, "max_length": 1000}
	actionID := "chat_action_" + chatKey(record.ConnectionID, job.DedupKey, chatMessageCreateKind)
	submit := chatSubmit(actionID, "Create issue")
	submit["text"] = chatI18nPlain("Create issue", "创建事项")
	fields := []map[string]any{chatSelect("teamId", "Team", defaultTeam, teamOptions), chatInput("title", "Issue title", action.Title, 1000), descriptionInput}
	// A bound group with several projects asks which one the issue belongs to.
	if bound {
		if projectSelect, offered := chatCreateProjectSelect(metadata, binding, ""); projectSelect != nil {
			action.ProjectIDs = offered
			fields = append(fields, projectSelect)
		}
	}
	fields = append(fields, chatSelect("assigneeId", "Assignee", "__unassigned", assignees), chatSelect("priority", "Priority", strconv.Itoa(min(max(priority, 0), 4)), chatPriorityOptions()), submit)
	card := chatCardI18n("Create Flow issue", "创建 Flow 事项",
		chatCardI18nText("From a chat message. The issue is created with your Flow account.", "来自聊天消息，将以你的 Flow 账号创建事项。"),
		chatForm(fields...))
	return s.chatSendExtendedForm(ctx, record, identity, job, actionID, action, card)
}

// chatSendExtendedForm sends a private form card whose stored action keeps
// the extended fields (see chatSendPersonalForm).
func (s *server) chatSendExtendedForm(ctx context.Context, record domain.ChatIntegrationRecord, identity domain.ChatIdentity, job domain.ChatJob, actionID string, action chatExtendedAction, card map[string]any) error {
	payload, _ := json.Marshal(action)
	stored := domain.ChatAction{ID: actionID, WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, UserID: identity.UserID, Payload: payload, ExpiresAt: time.Now().UTC().Add(15 * time.Minute)}
	if err := s.store.PutChatAction(ctx, stored); err != nil {
		return err
	}
	content, _ := json.Marshal(card)
	if len(content) > 28<<10 {
		return s.chatSendCommandError(ctx, record, job, identity.OpenID, "This form exceeds the platform's card limit. Open Flow to finish the operation: "+s.chatAppOrigin()+"/"+url.PathEscape(record.WorkspaceKey))
	}
	var result chatMessageResult
	if err := s.chatWorkflowRequest(ctx, record, http.MethodPost, "/open-apis/im/v1/messages?receive_id_type=open_id", map[string]any{"receive_id": identity.OpenID, "msg_type": "interactive", "content": string(content), "uuid": chatKey(job.DedupKey, "form")[:32]}, &result); err != nil {
		return err
	}
	if result.Data.MessageID == "" || result.Data.ChatID == "" {
		return errorsChatResponse()
	}
	action.PersonalChatID, action.PersonalMessage = result.Data.ChatID, result.Data.MessageID
	stored.Payload, _ = json.Marshal(action)
	return s.store.UpdateChatAction(ctx, stored)
}

func (s *server) chatCreateFromMessage(ctx context.Context, record domain.ChatIntegrationRecord, identity domain.ChatIdentity, metadata domain.Bootstrap, job domain.ChatJob, command chatInboundCommand, stored domain.ChatAction) error {
	var action chatExtendedAction
	if json.Unmarshal(stored.Payload, &action) != nil {
		return chatInvalid("invalid stored form")
	}
	fail := func(code string) error { return s.chatSendActionError(ctx, record, metadata, identity, job, code) }
	if stored.UserID == "" || stored.UserID != identity.UserID {
		return fail("chat_action_expired")
	}
	var binding *domain.ChatBinding
	if action.BindingID != "" {
		current, err := s.chatBindingByID(ctx, record, action.BindingID)
		if err != nil {
			var permanent chatPermanentError
			var refusal chatGroupRefusal
			if errors.As(err, &permanent) || errors.As(err, &refusal) || errors.Is(err, store.ErrAuthForbidden) {
				return fail("chat_binding_unavailable")
			}
			return err
		}
		if action.BindingScope != chatBindingFingerprint(current) {
			return fail("chat_binding_scope_changed")
		}
		if !current.AllowCreate {
			return fail("chat_binding_create_disabled")
		}
		binding = &current
	}
	teamID := chatString(command.Values, "teamId")
	if teamID == "" && len(action.TeamIDs) == 1 {
		teamID = action.TeamIDs[0]
	}
	title := chatString(command.Values, "title")
	if title == "" {
		title = action.Title
	}
	description := chatString(command.Values, "description")
	if !slices.Contains(action.TeamIDs, teamID) || binding != nil && teamID != binding.TeamID || title == "" || len(title) > 2048 || utf8.RuneCountInString(title) > 1000 || utf8.RuneCountInString(description) > 10000 {
		return fail("chat_form_invalid")
	}
	input := domain.IssueCreateInput{Title: title, Description: description, TeamID: teamID}
	if binding != nil {
		if _, err := chatCreateDefaults(metadata, *binding, title); err != nil {
			return fail("chat_team_unavailable")
		}
		input.TemplateID = binding.TemplateID
		projectID, err := chatCreateProject(*binding, action.chatCardAction, command.Values)
		if err != nil {
			return fail("chat_form_invalid")
		}
		if projectID != "" {
			input.ProjectID = &projectID
		}
	}
	assigneeID := chatString(command.Values, "assigneeId")
	if assigneeID == "__unassigned" {
		unassigned := ""
		input.AssigneeID = &unassigned
	} else if assigneeID != "" {
		if !slices.Contains(action.AssigneeIDs, assigneeID) {
			return fail("chat_form_invalid")
		}
		input.AssigneeID = &assigneeID
	}
	if value := chatString(command.Values, "priority"); value != "" {
		priority, err := strconv.Atoi(value)
		if err != nil || priority < 0 || priority > 4 {
			return fail("chat_form_invalid")
		}
		input.Priority = &priority
	}
	var issue domain.Issue
	createCtx := store.WithIssueCreationKey(ctx, "issue_chat_"+chatKey(record.ConnectionID, command.ActionID))
	if err := s.chatAuthorizedMutation(createCtx, record.WorkspaceKey, identity.UserID, http.MethodPost, "/api/issue-records", input, &issue); err != nil {
		code, err := chatMutationCode(err, "chat_action_failed")
		if err != nil {
			return err
		}
		return fail(code)
	}
	// The reply is a separate durable delivery (a crash after the commit
	// resumes with the same issue through its creation key).
	result := chatNotificationJob{IssueID: issue.ID, Kind: chatMessageResultKind, Operation: "create", PersonalOpenID: identity.OpenID}
	if action.Verified {
		result.SourceMessageID = action.SourceMessageID
	}
	if binding != nil && action.Verified && action.SourceChatID == binding.ChatID {
		result = chatNotificationJob{BindingID: binding.ID, BindingScope: chatBindingFingerprint(*binding), IssueID: issue.ID, SourceMessageID: action.SourceMessageID, Kind: "result", Operation: "create", PersonalOpenID: identity.OpenID}
	}
	payload, _ := json.Marshal(result)
	_, err := s.store.EnqueueChatJob(ctx, domain.ChatJob{ID: "chat_result_" + job.DedupKey, WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Kind: "card", DedupKey: chatKey(job.DedupKey, "result"), Payload: payload, RunAt: time.Now().UTC()})
	return err
}

// processChatMessageResult reports an issue created from a message outside a
// binding: a reply under the message for a public team's issue (when the
// bot could read the message), otherwise the issue card in the member's bot
// conversation. Workspaces with sensitive data protection get the link only.
func (s *server) processChatMessageResult(ctx context.Context, record domain.ChatIntegrationRecord, job domain.ChatJob, payload chatNotificationJob) error {
	identity, err := s.store.ChatIdentityForOpenID(ctx, record.WorkspaceKey, record.ConnectionID, payload.PersonalOpenID)
	if err != nil || identity.TenantKey != record.TenantKey {
		return chatInvalid("result recipient unbound")
	}
	var issue domain.Issue
	if err := s.chatAuthorizedMutation(withChatIdentityAuthentication(ctx, identity), record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/"+url.PathEscape(payload.IssueID), nil, &issue); err != nil {
		return chatInvalid("result recipient access revoked")
	}
	data, ok := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "teams", "teamSettings", "workspaceSettings", "userSettings")
	if !ok {
		return chatInvalid("workspace unavailable")
	}
	link := fmt.Sprintf("%s/%s/issue/%s", s.chatAppOrigin(), url.PathEscape(record.WorkspaceKey), url.PathEscape(issue.Identifier))
	saved := "Saved in Flow: " + issue.Identifier + " " + link
	if chatLanguage(data, identity.UserID, record.Provider) == "zh" {
		saved = "已保存到 Flow：" + issue.Identifier + " " + link
	}
	if data.WorkspaceSettings.HIPAACompliance {
		return s.chatSendPersonalText(ctx, record, identity.OpenID, saved, chatKey(job.DedupKey, "success"))
	}
	card := chatCard(issue.Identifier, chatCardText(issue.Title), chatCardText(chatIssueSummaryLine(issue)), map[string]any{"tag": "action", "actions": []map[string]any{{"tag": "button", "text": chatI18nPlain("Open in Flow", "在 Flow 中打开"), "url": link}}})
	content, _ := json.Marshal(card)
	if payload.SourceMessageID != "" && chatPublicTeam(data, issue.Team.ID) && len(issue.Permissions) == 0 && issue.ArchivedAt == nil {
		err := s.chatWorkflowRequest(ctx, record, http.MethodPost, "/open-apis/im/v1/messages/"+url.PathEscape(payload.SourceMessageID)+"/reply", map[string]any{"msg_type": "interactive", "content": string(content), "reply_in_thread": true, "uuid": chatKey(job.DedupKey, "reply")[:32]}, nil)
		var platform *chatPlatformError
		if err == nil {
			return s.chatSendPersonalText(ctx, record, identity.OpenID, saved, chatKey(job.DedupKey, "success"))
		}
		if !errors.As(err, &platform) || platform.Retryable {
			return err
		}
	}
	return s.chatWorkflowRequest(ctx, record, http.MethodPost, "/open-apis/im/v1/messages?receive_id_type=open_id", map[string]any{"receive_id": identity.OpenID, "msg_type": "interactive", "content": string(content), "uuid": chatKey(job.DedupKey, "personal_card")[:32]}, nil)
}
