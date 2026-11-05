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
	"issue.created":           {"Issue created", "事项已创建"},
	"issue.status_changed":    {"Status changed", "状态已变更"},
	"issue.assignee_changed":  {"Assignee changed", "负责人已变更"},
	"comment.created":         {"New comment", "新评论"},
	"request_received":        {"Request received. Replies in this thread go to the request.", "已收到需求，在此话题中的回复会同步到需求。"},
	"request":                 {"Request", "需求"},
	"saved":                   {"Saved in Flow", "已保存到 Flow"},
	"saved_link":              {"Saved in Flow: %s %s", "已保存到 Flow：%s %s"},
	"unassigned":              {"Unassigned", "未分配"},
	"open_in_flow":            {"Open in Flow", "在 Flow 中打开"},
	"comment":                 {"Comment", "评论"},
	"assign_to_me":            {"Assign to me", "指派给我"},
	"status":                  {"Status", "状态"},
	"state":                   {"Status", "状态"},
	"priority":                {"Priority", "优先级"},
	"assignee":                {"Assignee", "负责人"},
	"priority.0":              {"No priority", "无优先级"},
	"priority.1":              {"Urgent", "紧急"},
	"priority.2":              {"High", "高"},
	"priority.3":              {"Medium", "中"},
	"priority.4":              {"Low", "低"},
	"issue_title":             {"Issue title", "事项标题"},
	"create_issue":            {"Create issue", "创建事项"},
	"create_flow_issue":       {"Create Flow issue", "创建 Flow 事项"},
	"team_line":               {"Team: %s", "团队：%s"},
	"project":                 {"Project", "项目"},
	"project_line":            {"Project: %s", "项目：%s"},
	"projects_line":           {"Projects: %s", "项目：%s"},
	"list_separator":          {", ", "、"},
	"update_issue":            {"Update %s", "更新 %s"},
	"confirm":                 {"Confirm", "确认"},
	"commented":               {"%s commented", "%s 评论了"},
	"replied":                 {"%s replied", "%s 回复了"},
	"edited_in_flow":          {"Edited in Flow", "已在 Flow 中编辑"},
	"comment_deleted":         {"This comment was deleted in Flow.", "此评论已在 Flow 中删除。"},
	"thread_comment_deleted":  {"A comment in this thread was deleted in Flow.", "此话题中的一条评论已在 Flow 中删除。"},
	"project_update":          {"Project update", "项目更新"},
	"initiative_update":       {"Initiative update", "目标更新"},
	"update_deleted":          {"This update was deleted in Flow.", "此更新已在 Flow 中删除。"},
	"update_edited":           {"Edited", "已编辑"},
	"open_update":             {"Open update", "查看更新"},
	"read_more":               {"Read more", "阅读更多"},
	"health.onTrack":          {"On track", "进展正常"},
	"health.atRisk":           {"At risk", "存在风险"},
	"health.offTrack":         {"Off track", "偏离计划"},
	"health.noUpdate":         {"No update", "暂无更新"},
	"someone":                 {"Someone", "某成员"},
	"form_unavailable":        {"This group's team, project, or template is no longer available for creating an issue. Ask a Flow administrator to check the group binding.", "此群绑定的团队、项目或模板已无法用于创建事项，请联系 Flow 管理员检查群绑定。"},
	"issue_outside_scope":     {"This issue is no longer available in this group scope.", "此事项已不在此群的 Flow 范围内。"},
	"action_forbidden":        {"Your Flow account does not have permission to perform this action.", "你的 Flow 账号没有执行此操作的权限。"},
	"form_too_large":          {"This form exceeds the platform's card limit. Open Flow to finish the operation: %s", "此表单超出了平台的卡片大小限制，请在 Flow 中完成操作：%s"},
	"time_utc":                {"UTC", "UTC"},
	"time_layout":             {"Jan 2, 2006, 3:04 PM", "2006年1月2日 15:04"},
	"time_zone":               {"%s (%s)", "%s（%s）"},
	"comment_heading_unknown": {"Flow", "Flow"},
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
