package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"flow/api/internal/domain"
)

// Cards, platform calls and content protection of direct messages with the
// bot (chat_dm_agent.go). Cards use the platform's interactive card format
// (the same as group cards, chat_cards.go) and stay below its 30 KiB limit.

// chatDMText picks the member's language: Chinese when they wrote Chinese,
// or on Feishu when the message has no text to tell.
func chatDMText(zh bool, en, cn string) string {
	if zh {
		return cn
	}
	return en
}

func chatDMChinese(text, provider string) bool {
	if containsHan(text) {
		return true
	}
	return strings.TrimSpace(text) == "" && provider == "feishu"
}

func chatDMCard(title, template string, elements ...map[string]any) map[string]any {
	card := chatCard(title, elements...)
	card["header"].(map[string]any)["template"] = template
	return card
}

func chatDMMarkdown(text string) map[string]any {
	return map[string]any{"tag": "markdown", "content": chatTruncateText(text, 12000)}
}

func chatDMNote(text string) map[string]any {
	return map[string]any{"tag": "note", "elements": []map[string]string{chatPlain(text)}}
}

func chatDMLinkButton(label, link string, primary bool) map[string]any {
	kind := "default"
	if primary {
		kind = "primary"
	}
	return map[string]any{"tag": "button", "text": chatPlain(label), "url": link, "type": kind}
}

func chatDMActionButton(label, actionID, kind string) map[string]any {
	return map[string]any{"tag": "button", "text": chatPlain(label), "type": kind, "value": map[string]string{"actionId": actionID}}
}

func chatDMButtons(buttons ...map[string]any) map[string]any {
	return map[string]any{"tag": "action", "actions": buttons}
}

// chatDMOptionalInput is a form input the member may leave empty.
func chatDMOptionalInput(name, placeholder string, multiline bool) map[string]any {
	input := map[string]any{"tag": "input", "name": name, "placeholder": chatPlain(placeholder), "required": false, "max_length": 1000}
	if multiline {
		input["input_type"] = "multiline_text"
		input["rows"] = 4
	}
	return input
}

// chatDMMarkdownEscape keeps a title from being read as card markdown.
func chatDMMarkdownEscape(text string) string {
	var builder strings.Builder
	for _, r := range text {
		if strings.ContainsRune("\\`*_[]()<>#~|", r) {
			builder.WriteRune('\\')
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func chatDMMarkdownLink(label, link string) string {
	return "[" + chatDMMarkdownEscape(label) + "](" + strings.NewReplacer("(", "%28", ")", "%29", " ", "%20").Replace(link) + ")"
}

func (s *server) chatDMWorkspaceURL(workspace, path string) string {
	return s.chatAppOrigin() + "/" + url.PathEscape(workspace) + path
}

func (s *server) chatDMLinkCard(workspace string, zh bool) map[string]any {
	link := s.chatDMWorkspaceURL(workspace, "/settings/account/connections")
	return chatDMCard(chatDMText(zh, "Link your Flow account", "关联你的 Flow 账号"), "blue",
		chatCardText(chatDMText(zh,
			"To chat with Flow here, link this chat account to your Flow account first. Replies here run with your own Flow permissions: you only see and change what you can in Flow.",
			"先把这个聊天账号关联到你的 Flow 账号，才能在这里和 Flow 对话。这里的回复使用你自己的 Flow 权限：你只能看到和修改你在 Flow 中有权限的内容。")),
		chatDMButtons(chatDMLinkButton(chatDMText(zh, "Link account", "关联账号"), link, true)))
}

func chatDMNoticeCard(title, text string) map[string]any {
	return chatDMCard(title, "grey", chatCardText(text))
}

// Sensitive content never leaves for the chat platform: secrets (keys,
// tokens, passwords, private keys), payment card numbers and national ID
// numbers in what Flow sends are replaced. Tool arguments and results are not
// sent at all; cards name the steps only.
var (
	chatDMSecretPatterns = []*regexp.Regexp{
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`),
		regexp.MustCompile(`\b(?:flow_[a-z]+_[A-Za-z0-9_\-]{12,}|sk-[A-Za-z0-9_\-]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_\-]{16,}|xox[abposr]-[A-Za-z0-9\-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_\-]{30,}|t-[a-z0-9]{8,}\.[A-Za-z0-9_\-]{16,})`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`),
		regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/\-]{16,}=*`),
	}
	chatDMLabeledSecret = regexp.MustCompile(`(?i)(\b(?:password|passwd|pwd|passcode|secret|client[_\- ]?secret|api[_\- ]?key|access[_\- ]?key|private[_\- ]?key|token|access[_\- ]?token)\b|密码|密钥|口令|令牌)(\s*(?:[:=：]|是|为)\s*)("[^"\n]*"|'[^'\n]*'|` + "`[^`\n]*`" + `|[^\s,，;；]+)`)
	chatDMCardNumber    = regexp.MustCompile(`\b\d(?:[ -]?\d){12,18}\b`)
	chatDMNationalID    = regexp.MustCompile(`\b\d{17}[\dXx]\b`)
)

func chatDMRedact(text string, zh bool) string {
	mask := chatDMText(zh, "[hidden]", "[已隐藏]")
	for _, pattern := range chatDMSecretPatterns {
		text = pattern.ReplaceAllString(text, mask)
	}
	text = chatDMLabeledSecret.ReplaceAllString(text, "${1}${2}"+mask)
	text = chatDMNationalID.ReplaceAllString(text, mask)
	return chatDMCardNumber.ReplaceAllStringFunc(text, func(match string) string {
		digits := strings.Map(func(r rune) rune {
			if unicode.IsDigit(r) {
				return r
			}
			return -1
		}, match)
		if chatDMLuhn(digits) {
			return mask
		}
		return match
	})
}

func chatDMLuhn(digits string) bool {
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum := 0
	for index := range digits {
		digit := int(digits[len(digits)-1-index] - '0')
		if index%2 == 1 {
			if digit *= 2; digit > 9 {
				digit -= 9
			}
		}
		sum += digit
	}
	return sum%10 == 0
}

// chatDMToolLabel names a step for the card without its arguments.
func chatDMToolLabel(call *domain.AgentToolCall) string {
	if call == nil {
		return ""
	}
	if call.Title != "" {
		return call.Title
	}
	name := call.Name
	if call.RemoteTool != "" {
		name = call.RemoteTool
		if call.Connector != "" {
			return call.Connector + ": " + strings.ReplaceAll(name, "_", " ")
		}
	}
	name = strings.ReplaceAll(agentToolBaseName(name), "_", " ")
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// Platform calls. Messages are sent and updated through the outbox's rate
// limit (chatPlatformRequest).

// chatDMSend replies to the member's message (or sends a new message to them
// when replyTo is empty). key makes the send idempotent across retries.
func (s *server) chatDMSend(ctx context.Context, record domain.ChatIntegrationRecord, openID, replyTo, key string, card map[string]any) (messageID, chatID string, err error) {
	content, _ := json.Marshal(card)
	if len(content) > 28<<10 {
		return "", "", chatInvalid("card exceeds the platform limit")
	}
	var result chatMessageResult
	uuid := chatKey(key, "dm")[:32]
	if replyTo != "" {
		err = s.chatWorkflowRequest(ctx, record, http.MethodPost, "/open-apis/im/v1/messages/"+url.PathEscape(replyTo)+"/reply", map[string]any{"msg_type": "interactive", "content": string(content), "uuid": uuid}, &result)
	} else {
		err = s.chatWorkflowRequest(ctx, record, http.MethodPost, "/open-apis/im/v1/messages?receive_id_type=open_id", map[string]any{"receive_id": openID, "msg_type": "interactive", "content": string(content), "uuid": uuid}, &result)
	}
	if chatUnavailableToUser(err) {
		// The app is not available to this account: its own state on the
		// member's connected-account row.
		if identity, lookupErr := s.store.ChatIdentityForOpenID(ctx, record.WorkspaceKey, record.ConnectionID, openID); lookupErr == nil {
			s.setChatPersonalDeliveryState(ctx, record, identity, chatPersonalStateUnavailable)
		}
		return "", "", chatInvalid(chatAppUnavailableMessage)
	}
	if err != nil {
		return "", "", err
	}
	if result.Data.MessageID == "" {
		return "", "", errorsChatResponse()
	}
	return result.Data.MessageID, result.Data.ChatID, nil
}

// chatDMUpdate replaces a card the bot sent.
func (s *server) chatDMUpdate(ctx context.Context, record domain.ChatIntegrationRecord, messageID string, card map[string]any) error {
	content, _ := json.Marshal(card)
	if len(content) > 28<<10 {
		return chatInvalid("card exceeds the platform limit")
	}
	return s.chatWorkflowRequest(ctx, record, http.MethodPatch, "/open-apis/im/v1/messages/"+url.PathEscape(messageID), map[string]any{"content": string(content)}, nil)
}

// chatDMDownload reads an image or file the member sent, with the app's
// tenant token (the im:resource permission). It reads at most limit bytes:
// tooLarge reports a larger file without keeping it.
func (s *server) chatDMDownload(ctx context.Context, record domain.ChatIntegrationRecord, messageID, key, kind string, limit int64) (content []byte, contentType string, tooLarge bool, err error) {
	token, err := s.chatTenantToken(ctx, record)
	if err != nil {
		return nil, "", false, err
	}
	endpoint := chatBaseURL(record.Provider) + "/open-apis/im/v1/messages/" + url.PathEscape(messageID) + "/resources/" + url.PathEscape(key) + "?type=" + url.QueryEscape(kind)
	if chatBaseURL(record.Provider) == "" {
		return nil, "", false, errInvalid
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", false, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := chatHTTPClient().Do(request)
	if err != nil {
		return nil, "", false, &chatPlatformError{Retryable: true}
	}
	defer response.Body.Close()
	contentType, _, _ = mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || contentType == "application/json" {
		var envelope struct {
			Code int `json:"code"`
		}
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		_ = json.Unmarshal(raw, &envelope)
		return nil, "", false, &chatPlatformError{Code: envelope.Code, Status: response.StatusCode, Retryable: response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500}
	}
	if response.ContentLength > limit {
		return nil, contentType, true, nil
	}
	content, err = io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, "", false, &chatPlatformError{Retryable: true}
	}
	if int64(len(content)) > limit {
		return nil, contentType, true, nil
	}
	return content, contentType, false, nil
}

// chatDMResource is an image or file of a member's message.
type chatDMResource struct {
	Key  string
	Kind string // image or file
	Name string
}

// chatDMContent reads the text and resources of a received message: text,
// rich text (post, with its images, links and mentions as text), an image or
// a file.
func chatDMContent(messageType, content string) (string, []chatDMResource, bool) {
	switch messageType {
	case "text":
		var value struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(content), &value) != nil {
			return "", nil, false
		}
		return strings.TrimSpace(value.Text), nil, true
	case "image":
		var value struct {
			ImageKey string `json:"image_key"`
		}
		if json.Unmarshal([]byte(content), &value) != nil || value.ImageKey == "" {
			return "", nil, false
		}
		return "", []chatDMResource{{Key: value.ImageKey, Kind: "image", Name: "image.png"}}, true
	case "file":
		var value struct {
			FileKey  string `json:"file_key"`
			FileName string `json:"file_name"`
		}
		if json.Unmarshal([]byte(content), &value) != nil || value.FileKey == "" {
			return "", nil, false
		}
		return "", []chatDMResource{{Key: value.FileKey, Kind: "file", Name: firstNonEmpty(value.FileName, "file")}}, true
	case "post":
		type element struct {
			Tag      string `json:"tag"`
			Text     string `json:"text"`
			Href     string `json:"href"`
			ImageKey string `json:"image_key"`
			UserName string `json:"user_name"`
		}
		type post struct {
			Title   string      `json:"title"`
			Content [][]element `json:"content"`
		}
		var value post
		if json.Unmarshal([]byte(content), &value) != nil || value.Content == nil {
			// Some events wrap the post in its locale.
			var localized map[string]post
			if json.Unmarshal([]byte(content), &localized) != nil {
				return "", nil, false
			}
			for _, item := range localized {
				value = item
				break
			}
		}
		lines := []string{}
		if value.Title != "" {
			lines = append(lines, value.Title)
		}
		resources := []chatDMResource{}
		for _, paragraph := range value.Content {
			var line strings.Builder
			for _, item := range paragraph {
				switch item.Tag {
				case "text", "md":
					line.WriteString(item.Text)
				case "a":
					line.WriteString(firstNonEmpty(item.Href, item.Text))
				case "at":
					line.WriteString("@" + item.UserName)
				case "img":
					if item.ImageKey != "" {
						resources = append(resources, chatDMResource{Key: item.ImageKey, Kind: "image", Name: fmt.Sprintf("image-%d.png", len(resources)+1)})
					}
				}
			}
			if text := strings.TrimSpace(line.String()); text != "" {
				lines = append(lines, text)
			}
		}
		return strings.Join(lines, "\n"), resources, true
	}
	return "", nil, false
}

// chatDMAttachments downloads the message's resources as agent chat
// attachments with the in-app limits: up to agentAttachmentMax files of at
// most 2 MB; images are kept as images, text files as text, any other file by
// name and size (as the composer sends them).
func (s *server) chatDMAttachments(ctx context.Context, record domain.ChatIntegrationRecord, messageID string, resources []chatDMResource, zh bool) ([]loopRunReplyAttachmentInput, string, error) {
	if len(resources) > agentAttachmentMax {
		return nil, chatDMText(zh, fmt.Sprintf("Send up to %d files at a time.", agentAttachmentMax), fmt.Sprintf("一次最多发送 %d 个文件。", agentAttachmentMax)), nil
	}
	inputs := make([]loopRunReplyAttachmentInput, 0, len(resources))
	for _, resource := range resources {
		content, contentType, tooLarge, err := s.chatDMDownload(ctx, record, messageID, resource.Key, resource.Kind, agentAttachmentMaxBytes)
		if err != nil {
			var platform *chatPlatformError
			if errors.As(err, &platform) && !platform.Retryable {
				return nil, chatDMText(zh, "Flow could not read "+resource.Name+". Check that the app has the im:resource permission.", "Flow 无法读取 "+resource.Name+"。请确认应用已开通 im:resource 权限。"), nil
			}
			return nil, "", err
		}
		if tooLarge {
			return nil, chatDMText(zh, resource.Name+" is larger than 2 MB.", resource.Name+" 超过 2 MB。"), nil
		}
		name := resource.Name
		if resource.Kind == "image" {
			contentType = http.DetectContentType(content)
			if loopAttachmentImageType(contentType) == "" {
				return nil, chatDMText(zh, "Flow could not read this image.", "Flow 无法读取这张图片。"), nil
			}
			if extensions, _ := mime.ExtensionsByType(contentType); len(extensions) > 0 {
				name = strings.TrimSuffix(name, ".png") + extensions[len(extensions)-1]
			}
			inputs = append(inputs, loopRunReplyAttachmentInput{Name: name, ContentType: contentType, Size: int64(len(content)), Content: "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(content)})
			continue
		}
		if byName := mime.TypeByExtension(strings.ToLower(extensionOf(name))); byName != "" {
			contentType = byName
		}
		contentType = firstNonEmpty(contentType, "application/octet-stream")
		input := loopRunReplyAttachmentInput{Name: name, ContentType: contentType, Size: int64(len(content))}
		if loopAttachmentIsText(domain.LoopAttachment{Name: name, ContentType: contentType}) {
			input.Content = string(content)
		}
		inputs = append(inputs, input)
	}
	return inputs, "", nil
}

func extensionOf(name string) string {
	if index := strings.LastIndex(name, "."); index >= 0 {
		return name[index:]
	}
	return ""
}

// chatDMReplyView is what the reply card shows of an agent reply.
type chatDMReplyView struct {
	Text      string
	Steps     []chatDMStep
	Status    string // working, waiting, done, failed, stopped
	Failure   string
	Questions []string
	Link      string
}

type chatDMStep struct {
	ID, Label, Status string
}

func chatDMStepSummary(steps []chatDMStep, zh bool) string {
	if len(steps) == 0 {
		return ""
	}
	counts := map[string]int{}
	running := ""
	for _, step := range steps {
		switch step.Status {
		case "completed":
			counts["done"]++
		case "error":
			counts["failed"]++
		case "pending":
			counts["waiting"]++
		default:
			counts["running"]++
			running = step.Label
		}
	}
	parts := []string{}
	for _, item := range []struct{ key, en, cn string }{{"done", "%d done", "%d 步完成"}, {"failed", "%d failed", "%d 步失败"}, {"waiting", "%d waiting for approval", "%d 步待批准"}, {"running", "%d running", "%d 步进行中"}} {
		if counts[item.key] > 0 {
			parts = append(parts, fmt.Sprintf(chatDMText(zh, item.en, item.cn), counts[item.key]))
		}
	}
	summary := chatDMText(zh, "Steps: ", "步骤：") + strings.Join(parts, chatDMText(zh, " · ", " · "))
	if running != "" {
		summary += "\n" + chatDMText(zh, "Now: ", "当前：") + running
	}
	return summary
}

func chatDMReplyCard(view chatDMReplyView, zh bool) map[string]any {
	title, template := chatDMText(zh, "Flow Agent", "Flow Agent"), "blue"
	elements := []map[string]any{}
	text := chatDMRedact(strings.TrimSpace(view.Text), zh)
	switch view.Status {
	case "working":
		if text == "" {
			text = chatDMText(zh, "Thinking…", "思考中…")
		}
	case "waiting":
		template = "orange"
	case "done":
		template = "green"
	case "failed":
		template = "red"
		failure := chatDMRedact(firstNonEmpty(view.Failure, chatDMText(zh, "The reply failed.", "回复失败。")), zh)
		if text != "" {
			text += "\n\n"
		}
		text += "**" + chatDMMarkdownEscape(failure) + "**"
	case "stopped":
		template = "grey"
		if text != "" {
			text += "\n\n"
		}
		text += chatDMMarkdownEscape(firstNonEmpty(view.Failure, chatDMText(zh, "Stopped.", "已停止。")))
	}
	if text != "" {
		elements = append(elements, chatDMMarkdown(text))
	}
	for _, question := range view.Questions {
		elements = append(elements, chatDMMarkdown(chatDMRedact(question, zh)))
	}
	if summary := chatDMStepSummary(view.Steps, zh); summary != "" {
		elements = append(elements, chatDMNote(summary))
	}
	if view.Link != "" {
		elements = append(elements, chatDMButtons(chatDMLinkButton(chatDMText(zh, "Open in Flow", "在 Flow 中打开"), view.Link, false)))
	}
	if view.Status == "done" || view.Status == "failed" || view.Status == "stopped" {
		elements = append(elements, chatDMNote(chatDMText(zh, "Reply to this message to continue the conversation.", "回复这条消息即可继续对话。")))
	}
	return chatDMCard(title, template, elements...)
}

// chatDMApprovalPreview is a held call's arguments as the approval card
// shows them: redacted and shortened.
func chatDMApprovalPreview(call *domain.AgentToolCall, zh bool) string {
	if call == nil || len(call.Arguments) == 0 || string(call.Arguments) == "{}" || string(call.Arguments) == "null" {
		return ""
	}
	return chatTruncateText(chatDMRedact(string(call.Arguments), zh), 600)
}

// chatDMApprovalCard asks to approve one held call, or shows how it was
// answered (resolution approved, rejected or expired).
func chatDMApprovalCard(label, preview, approveID, rejectID, resolution string, zh bool) map[string]any {
	elements := []map[string]any{chatCardText(chatDMText(zh, "Flow Agent wants to run: ", "Flow Agent 想要执行：") + label)}
	if preview != "" {
		elements = append(elements, chatDMNote(preview))
	}
	template := "orange"
	switch resolution {
	case "":
		elements = append(elements, chatDMButtons(chatDMActionButton(chatDMText(zh, "Approve", "批准"), approveID, "primary"), chatDMActionButton(chatDMText(zh, "Reject", "拒绝"), rejectID, "danger")))
	case "approved":
		template = "green"
		elements = append(elements, chatDMNote(chatDMText(zh, "Approved.", "已批准。")))
	case "rejected":
		template = "grey"
		elements = append(elements, chatDMNote(chatDMText(zh, "Rejected.", "已拒绝。")))
	default:
		template = "grey"
		elements = append(elements, chatDMNote(chatDMText(zh, "This request is no longer waiting.", "这个请求已不再等待。")))
	}
	return chatDMCard(chatDMText(zh, "Approval needed", "需要批准"), template, elements...)
}

func chatDMIssueLine(s *server, workspace string, issue domain.Issue) string {
	line := chatDMMarkdownLink(issue.Identifier+" "+issue.Title, s.chatDMWorkspaceURL(workspace, "/issue/"+url.PathEscape(issue.Identifier)))
	if issue.State.Name != "" {
		line += " · " + chatDMMarkdownEscape(issue.State.Name)
	}
	return line
}

// chatDMIssueCard previews an issue the member can open.
func (s *server) chatDMIssueCard(workspace string, issue domain.Issue, zh bool) map[string]any {
	assignee := chatDMText(zh, "Unassigned", "未分配")
	if issue.Assignee != nil {
		assignee = issue.Assignee.Name
	}
	lines := []string{
		chatDMText(zh, "Status: ", "状态：") + issue.State.Name,
		chatDMText(zh, "Priority: ", "优先级：") + priorityLabel(issue.Priority),
		chatDMText(zh, "Assignee: ", "负责人：") + assignee,
		chatDMText(zh, "Team: ", "团队：") + issue.Team.Name,
	}
	if issue.Project != nil {
		lines = append(lines, chatDMText(zh, "Project: ", "项目：")+issue.Project.Name)
	}
	elements := []map[string]any{chatCardText(chatDMRedact(strings.Join(lines, "\n"), zh))}
	if description := strings.TrimSpace(issue.Description); description != "" {
		elements = append(elements, chatDMNote(chatTruncateText(chatDMRedact(description, zh), 400)))
	}
	elements = append(elements, chatDMButtons(chatDMLinkButton(chatDMText(zh, "Open in Flow", "在 Flow 中打开"), s.chatDMWorkspaceURL(workspace, "/issue/"+url.PathEscape(issue.Identifier)), true)))
	return chatDMCard(chatDMRedact(issue.Identifier+" "+issue.Title, zh), "blue", elements...)
}

func (s *server) chatDMProjectCard(workspace string, project domain.Project, zh bool) map[string]any {
	lines := []string{chatDMText(zh, "Status: ", "状态：") + firstNonEmpty(project.Status.Name, "-")}
	if project.Lead != nil {
		lines = append(lines, chatDMText(zh, "Lead: ", "负责人：")+project.Lead.Name)
	}
	if project.TargetDate != nil && *project.TargetDate != "" {
		lines = append(lines, chatDMText(zh, "Target date: ", "目标日期：")+*project.TargetDate)
	}
	elements := []map[string]any{chatCardText(strings.Join(lines, "\n"))}
	if summary := strings.TrimSpace(project.Summary); summary != "" {
		elements = append(elements, chatDMNote(chatTruncateText(chatDMRedact(summary, zh), 400)))
	}
	elements = append(elements, chatDMButtons(chatDMLinkButton(chatDMText(zh, "Open in Flow", "在 Flow 中打开"), s.chatDMWorkspaceURL(workspace, "/project/"+url.PathEscape(project.SlugID)+"/overview"), true)))
	return chatDMCard(chatDMRedact(project.Name, zh), "blue", elements...)
}

// chatDMListCard lists linked lines (issues, notifications) with a button to
// the full list in Flow.
func chatDMListCard(title string, lines []string, empty, buttonLabel, link string) map[string]any {
	elements := []map[string]any{}
	if len(lines) == 0 {
		elements = append(elements, chatCardText(empty))
	} else {
		elements = append(elements, chatDMMarkdown(strings.Join(lines, "\n")))
	}
	elements = append(elements, chatDMButtons(chatDMLinkButton(buttonLabel, link, false)))
	return chatDMCard(title, "blue", elements...)
}

func chatDMPriorityOptions(zh bool) []map[string]any {
	return []map[string]any{chatOption("0", chatDMText(zh, "No priority", "无优先级")), chatOption("1", chatDMText(zh, "Urgent", "紧急")), chatOption("2", chatDMText(zh, "High", "高")), chatOption("3", chatDMText(zh, "Medium", "中")), chatOption("4", chatDMText(zh, "Low", "低"))}
}
