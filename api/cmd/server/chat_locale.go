package main

import (
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Language of chat cards and messages. A group hears Flow in its binding's
// language (domain.ChatBinding.Language); a binding without one uses the
// platform's default: Feishu tenants Chinese, Lark tenants English. A member
// hears personal messages in their Flow interface language (chatLanguage).
// Every fixed text of the group cards, summaries, thread messages and
// comment sync comes from chatIMTexts, so a card never mixes languages; the
// test TestChatIMBuildersHaveNoUntranslatedText fails on a new literal.

const (
	chatLanguageEnglish = "en"
	chatLanguageChinese = "zh"
)

// chatBindingLanguages are the values a binding's language may take ("" is
// the platform default).
var chatBindingLanguages = []string{"", "en-US", "zh-CN"}

// chatGroupLanguage is the language of everything Flow posts in the group.
func chatGroupLanguage(record domain.ChatIntegrationRecord, binding domain.ChatBinding) string {
	switch strings.ToLower(binding.Language) {
	case "zh-cn", "zh":
		return chatLanguageChinese
	case "en-us", "en":
		return chatLanguageEnglish
	}
	return chatProviderLanguage(record.Provider)
}

// chatDMLanguage is the language of a direct message (chatDMText).
func chatDMLanguage(zh bool) string {
	if zh {
		return chatLanguageChinese
	}
	return chatLanguageEnglish
}

// chatIMTexts: key -> English, Chinese. The Chinese texts follow the web
// interface's translations (web/src/i18n) where it has the same phrase.
var chatIMTexts = map[string][2]string{
	"issue.created":                  {"Issue created", "事项已创建"},
	"issue.status_changed":           {"Status changed", "状态已变更"},
	"issue.assignee_changed":         {"Assignee changed", "负责人已变更"},
	"comment.created":                {"New comment", "新评论"},
	"request_received":               {"Request received. Replies in this thread go to the request.", "已收到需求，在此话题中的回复会同步到需求。"},
	"request":                        {"Request", "需求"},
	"saved":                          {"Saved in Flow", "已保存到 Flow"},
	"saved_link":                     {"Saved in Flow: %s %s", "已保存到 Flow：%s %s"},
	"unassigned":                     {"Unassigned", "未分配"},
	"open_in_flow":                   {"Open in Flow", "在 Flow 中打开"},
	"comment":                        {"Comment", "评论"},
	"assign_to_me":                   {"Assign to me", "指派给我"},
	"status":                         {"Status", "状态"},
	"state":                          {"Status", "状态"},
	"priority":                       {"Priority", "优先级"},
	"assignee":                       {"Assignee", "负责人"},
	"priority.0":                     {"No priority", "无优先级"},
	"priority.1":                     {"Urgent", "紧急"},
	"priority.2":                     {"High", "高"},
	"priority.3":                     {"Medium", "中"},
	"priority.4":                     {"Low", "低"},
	"issue_title":                    {"Issue title", "事项标题"},
	"create_issue":                   {"Create issue", "创建事项"},
	"create_flow_issue":              {"Create Flow issue", "创建 Flow 事项"},
	"team_line":                      {"Team: %s", "团队：%s"},
	"project":                        {"Project", "项目"},
	"project_line":                   {"Project: %s", "项目：%s"},
	"projects_line":                  {"Projects: %s", "项目：%s"},
	"list_separator":                 {", ", "、"},
	"update_issue":                   {"Update %s", "更新 %s"},
	"confirm":                        {"Confirm", "确认"},
	"commented":                      {"%s commented", "%s 评论了"},
	"replied":                        {"%s replied", "%s 回复了"},
	"edited_in_flow":                 {"Edited in Flow", "已在 Flow 中编辑"},
	"comment_deleted":                {"This comment was deleted in Flow.", "此评论已在 Flow 中删除。"},
	"thread_comment_deleted":         {"A comment in this thread was deleted in Flow.", "此话题中的一条评论已在 Flow 中删除。"},
	"project_update":                 {"Project update", "项目更新"},
	"initiative_update":              {"Initiative update", "目标更新"},
	"update_deleted":                 {"This update was deleted in Flow.", "此更新已在 Flow 中删除。"},
	"update_edited":                  {"Edited", "已编辑"},
	"open_update":                    {"Open update", "查看更新"},
	"read_more":                      {"Read more", "阅读更多"},
	"health.onTrack":                 {"On track", "进展正常"},
	"health.atRisk":                  {"At risk", "存在风险"},
	"health.offTrack":                {"Off track", "偏离计划"},
	"health.noUpdate":                {"No update", "暂无更新"},
	"someone":                        {"Someone", "某成员"},
	"form_unavailable":               {"This group's team, project, or template is no longer available for creating an issue. Ask a Flow administrator to check the group binding.", "此群绑定的团队、项目或模板已无法用于创建事项，请联系 Flow 管理员检查群绑定。"},
	"issue_outside_scope":            {"This issue is no longer available in this group scope.", "此事项已不在此群的 Flow 范围内。"},
	"action_forbidden":               {"Your Flow account does not have permission to perform this action.", "你的 Flow 账号没有执行此操作的权限。"},
	"form_too_large":                 {"This form exceeds the platform's card limit. Open Flow to finish the operation: %s", "此表单超出了平台的卡片大小限制，请在 Flow 中完成操作：%s"},
	"notify.assignment":              {"%s assigned the issue to you", "%s 将此事项指派给了你"},
	"notify.mention":                 {"%s mentioned you", "%s 提到了你"},
	"notify.comment":                 {"%s commented on the issue", "%s 评论了此事项"},
	"notify.status":                  {"%s changed the status to %s", "%s 将状态改为「%s」"},
	"notify.activity":                {"%s updated %s", "%s 更新了 %s"},
	"notify.review":                  {"%s updated the review", "%s 更新了代码审查"},
	"notify.open_to_read":            {"Open Flow to read it.", "请打开 Flow 查看。"},
	"notify.with_excerpt":            {"%s: %s", "%s：%s"},
	"notify.agent_replied":           {"%s replied", "%s 回复了"},
	"notify.agent_finished":          {"%s finished", "%s 已完成"},
	"notify.agent_waiting":           {"%s is waiting for your input", "%s 正在等待你的输入"},
	"notify.agent_error":             {"%s ran into an error", "%s 遇到了错误"},
	"notify.update_comment":          {"%s commented on your update", "%s 评论了你的更新"},
	"notify.update_reaction":         {"%s reacted to your update", "%s 回应了你的更新"},
	"notify.document_reaction":       {"%s reacted to your comment", "%s 回应了你的评论"},
	"notify.document_reaction_emoji": {"%s reacted %s to your comment", "%s 对你的评论回应了 %s"},
	"notify.documentMention":         {"%s mentioned you in the document", "%s 在文档中提到了你"},
	"notify.documentCommentMention":  {"%s mentioned you in a comment", "%s 在评论中提到了你"},
	"notify.documentNewComment":      {"%s commented on the document", "%s 评论了文档"},
	"notify.documentThreadResolved":  {"%s resolved a comment thread", "%s 解决了一个评论话题"},
	"notify.documentChanges":         {"%s edited the document", "%s 编辑了文档"},
	"notify.documentMoved":           {"%s moved the document", "%s 移动了文档"},
	"notify.documentDeleted":         {"%s deleted the document", "%s 删除了文档"},
	"notify.documentRestored":        {"%s restored the document", "%s 恢复了文档"},
	"notify.documentAddedAsOwner":    {"%s made you an owner of the document", "%s 将你设为文档所有者"},
	"notify.documentRemovedAsOwner":  {"%s removed you as an owner of the document", "%s 取消了你的文档所有者身份"},
	"notify.documentSubscribed":      {"%s subscribed you to the document", "%s 为你订阅了文档"},
	"notify.documentUnsubscribed":    {"%s unsubscribed you from the document", "%s 为你取消订阅了文档"},
	"description":                    {"Description", "描述"},
	"team":                           {"Team", "团队"},
	"request_title":                  {"Request title", "需求标题"},
	"submit_request":                 {"Submit as request", "提交为需求"},
	"submit_flow_request":            {"Submit a Flow request", "提交 Flow 需求"},
	"message_intro_create":           {"From a chat message. The issue is created with your Flow account.", "来自聊天消息，将以你的 Flow 账号创建事项。"},
	"message_intro_request":          {"From a chat message, with your Flow account. Submit as request sends it to the team's triage with this group's request settings and follows it in the message's thread.", "来自聊天消息，将以你的 Flow 账号提交。「提交为需求」会按此群的需求设置进入团队的分诊，并在消息的话题中跟进。"},
	"source_message":                 {"Source message: %s", "来源消息：%s"},
	"connect_title":                  {"Connect your Flow account", "关联你的 Flow 账号"},
	"connect_text":                   {"Link your account to create and update Flow issues from chat. Linking never grants Flow access by itself.", "关联账号后即可在聊天中创建和更新 Flow 事项。关联本身不会授予 Flow 访问权限。"},
	"connect_account":                {"Connect account", "关联账号"},
	"request_submitted":              {"Request submitted", "需求已提交"},
	"message_is_request":             {"This message is already a request in Flow.", "这条消息已经提交为 Flow 需求。"},
	"message_is_issue":               {"This message already created an issue in Flow.", "这条消息已经创建了 Flow 事项。"},
	"message_is_request_link":        {"This message is already a request: %s", "这条消息已经提交为需求：%s"},
	"message_is_issue_link":          {"This message already created an issue: %s", "这条消息已经创建了事项：%s"},
	"request_saved_link":             {"Request saved in Flow: %s %s", "需求已保存到 Flow：%s %s"},
	"request_from":                   {"Request from %s", "来自「%s」的需求"},
	"request_rejected":               {"Request rejected: %s", "需求已拒绝：%s"},
	"request_reason":                 {"Reason: %s", "原因：%s"},
	"request_completed":              {"Request completed: %s", "需求已完成：%s"},
	"request_closed":                 {"Request closed (%s): %s", "需求已关闭（%s）：%s"},
	"request_reopened":               {"Request reopened: %s", "需求已重新打开：%s"},
	"status_changed_to":              {"Status changed to %s", "状态已更新为：%s"},
	"help_title":                     {"Flow in this group", "此群中的 Flow"},
	"help_create":                    {"Create an issue: @Flow create issue <title>, or use the message shortcut.", "创建事项：@Flow 创建事项 <标题>，或使用消息快捷操作。"},
	"help_request":                   {"Submit a request: @Flow request <text> (提需求 / 需求), or use Submit as request in the message shortcut.", "提交需求：@Flow 提需求 <内容>（也可用「需求」或 request），或在消息快捷操作中选择「提交为需求」。"},
	"help_reaction":                  {"Request reaction: %s", "需求表情：%s"},
	"help_update":                    {"Update issues with their card controls; reply in an issue thread to comment.", "通过事项卡片更新事项；在事项话题中回复即可评论。"},
	"help_off":                       {"Group actions are off. A team owner can configure this group's Flow binding.", "此群未开启群内操作，团队管理员可配置此群的 Flow 绑定。"},
	"preview_neutral":                {"Flow link", "Flow 链接"},
	"preview_connect":                {"Flow · Connect your Flow account to preview links", "Flow · 关联 Flow 账号后可预览链接"},
	"labels":                         {"Labels", "标签"},
	"due_date":                       {"Due date", "截止日期"},
	"updated_at":                     {"Updated %s", "更新于 %s"},
	"health":                         {"Health", "健康度"},
	"lead":                           {"Lead", "负责人"},
	"owner":                          {"Owner", "负责人"},
	"no_lead":                        {"No lead", "无负责人"},
	"no_owner":                       {"No owner", "无负责人"},
	"progress":                       {"Progress", "进度"},
	"percent":                        {"%d%%", "%d%%"},
	"target_date":                    {"Target date", "目标日期"},
	"teams":                          {"Teams", "团队"},
	"author":                         {"Author", "作者"},
	"posted":                         {"Posted", "发布时间"},
	"last_edited":                    {"Last edited by %s, %s", "%s 最近编辑于 %s"},
	"latest_update":                  {"Latest update by %s, %s", "最新更新：%s，%s"},
	"untitled_document":              {"Untitled document", "无标题文档"},
	"team_key":                       {"Identifier", "标识"},
	"cycle_number":                   {"Cycle %d", "周期 %d"},
	"cycle_active":                   {"Active", "进行中"},
	"cycle_upcoming":                 {"Upcoming", "未开始"},
	"cycle_completed":                {"Completed", "已结束"},
	"dates":                          {"Dates", "日期"},
	"date_range":                     {"%s – %s", "%s – %s"},
	"date_layout":                    {"Jan 2, 2006", "2006年1月2日"},
	"more_count":                     {"+%d", "+%d"},
	"label_separator":                {": ", "："},
	"time_utc":                       {"UTC", "UTC"},
	"time_layout":                    {"Jan 2, 2006, 3:04 PM", "2006年1月2日 15:04"},
	"time_zone":                      {"%s (%s)", "%s（%s）"},
	"comment_heading_unknown":        {"Flow", "Flow"},
}

// chatT returns the text in the language; arguments fill its verbs.
func chatT(language, key string, args ...any) string {
	texts, ok := chatIMTexts[key]
	if !ok {
		return key
	}
	text := texts[0]
	if language == chatLanguageChinese {
		text = texts[1]
	}
	if len(args) > 0 {
		return fmt.Sprintf(text, args...)
	}
	return text
}

func chatPriorityText(language string, priority int) string {
	return chatT(language, fmt.Sprintf("priority.%d", min(max(priority, 0), 4)))
}

// chatHealthText is an update's health label and the card colour it uses.
func chatHealthText(language, health string) (string, string) {
	switch health {
	case "onTrack":
		return chatT(language, "health.onTrack"), "green"
	case "atRisk":
		return chatT(language, "health.atRisk"), "orange"
	case "offTrack":
		return chatT(language, "health.offTrack"), "red"
	case "noUpdate":
		return chatT(language, "health.noUpdate"), "grey"
	}
	return "", "blue"
}

// chatPersonLabel names a person in chat text: their display name, then
// their name; the username or the email's local part only when neither is
// set, and the account ID only when nothing else is. directory, when given,
// holds the workspace's current users: a stale reference (an issue's
// assignee, a comment's author) is read from it.
func chatPersonLabel(user *domain.User, directory []domain.User) string {
	if user == nil {
		return ""
	}
	current := *user
	for _, candidate := range directory {
		if candidate.ID != "" && candidate.ID == user.ID {
			current = candidate
			break
		}
	}
	for _, value := range []string{current.DisplayName, user.DisplayName, current.Name, user.Name} {
		value = strings.TrimSpace(value)
		if value != "" && value != current.ID && value != current.Email {
			return value
		}
	}
	if current.Username != "" {
		return current.Username
	}
	if local, _, found := strings.Cut(current.Email, "@"); found && local != "" {
		return local
	}
	return firstNonEmpty(current.Email, current.ID)
}

// chatFormatTime writes a time in the language, in the zone (an IANA name;
// UTC when empty or unknown), with the zone's offset.
func chatFormatTime(language string, at time.Time, zone string) string {
	location := time.UTC
	if zone != "" {
		if loaded, err := time.LoadLocation(zone); err == nil {
			location = loaded
		}
	}
	local := at.In(location)
	offset := chatT(language, "time_utc")
	if _, seconds := local.Zone(); seconds != 0 {
		offset += local.Format("-07:00")
	}
	return chatT(language, "time_zone", local.Format(chatT(language, "time_layout")), offset)
}
