package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

// Interactive chat offers the model a small tool set instead of the whole Flow inventory (~80 tools, ~75 KB of
// schemas). Every turn resends the tools, so the full list made each provider call slow to start. Chat gets the
// core read tools, plus groups picked from the request and its surface (an issue, project, or document panel);
// anything else the model can pull in with load_tools.

// agentChatCoreTools are offered on every chat turn, in this order (a stable prefix keeps prompt caching warm).
var agentChatCoreTools = []string{
	"search_issues", "get_issue", "list_issues", "list_comments", "list_issue_history",
	"get_project", "list_projects", "list_project_activity", "get_status_updates", "list_milestones",
	"get_document", "list_documents",
	"list_users", "get_user", "list_teams", "get_team", "list_cycles", "list_issue_statuses", "get_issue_status", "list_issue_labels",
	"list_initiatives", "get_initiative", "get_workspace", "search_documentation",
}

type agentToolGroup struct {
	Name    string
	Summary string
	Tools   []string
	// Intent matches requests that need the group; NeedsChange groups also need a change verb in the request.
	Intent      *regexp.Regexp
	NeedsChange bool
}

// agentChangeIntent matches requests to create, change, or remove something (Chinese and English).
var agentChangeIntent = regexp.MustCompile(`(?i)改|设置|设为|设成|调成|调到|调整|更新|创建|新建|新增|建一个|建个|添加|加上|加个|加到|删除|删掉|去掉|移除|移到|分配|指派|指给|交给|关闭|关掉|完成|标记|评论|回复|留言|订阅|提醒|草稿|分流|接受|拒绝|重新打开|归档|恢复|发布|提交|合并|批准|写一?[个份篇]|\b(set|change|update|edit|create|add|remove|delete|assign|unassign|close|reopen|mark|move|rename|comment|reply|subscribe|unsubscribe|remind|draft|triage|accept|decline|file|log|make|prioriti[sz]e|label|tag|archive|restore|estimate|post|publish|submit|merge|approve|write)\b`)

// agentToolGroups are the optional chat tool groups, in the order they are appended after the core tools.
var agentToolGroups = []agentToolGroup{
	{Name: "issue_changes", Summary: "create, edit, comment on, triage, delete issues; reactions, subscriptions, reminders, drafts", NeedsChange: true,
		Tools:  []string{"save_issue", "save_comment", "delete_comment", "triage_issue", "delete_issue", "save_reaction", "save_subscription", "create_reminder", "create_issue_label", "save_draft", "delete_attachment"},
		Intent: regexp.MustCompile(`.`)},
	{Name: "project_changes", Summary: "create or edit projects, milestones, initiatives, and post status updates", NeedsChange: true,
		Tools:  []string{"save_project", "save_milestone", "save_status_update", "save_initiative", "create_initiative_label", "list_project_labels", "list_initiative_labels"},
		Intent: regexp.MustCompile(`(?i)项目|里程碑|计划|状态更新|进展|\b(project|milestone|initiative|status update|roadmap)`)},
	{Name: "documents", Summary: "write, delete, or restore documents; document history and sharing", NeedsChange: false,
		Tools:  []string{"save_document", "delete_document", "restore_document", "list_document_history", "restore_document_version", "get_document_permissions", "save_document_permissions"},
		Intent: regexp.MustCompile(`(?i)(写|起草|编辑|修改|更新|创建|新建|删除|恢复).{0,12}(文档|文章|页面|笔记)|(文档|文章|页面|笔记).{0,12}(历史|版本|权限|共享|分享)|\b(write|draft|edit|create|delete|restore|share)\b.{0,30}\b(doc|docs|document|page|note)s?\b|\bdocument (history|version|permission)`)},
	{Name: "code_reviews", Summary: "code reviews: diffs, review threads, submitting reviews, merging",
		Tools:  []string{"list_diffs", "get_diff", "get_diff_threads", "delete_diff_comment", "resolve_diff_thread", "submit_diff_review", "merge_diff"},
		Intent: regexp.MustCompile(`(?i)评审|代码审查|审查|合并请求|\b(diffs?|prs?|pull requests?|merge requests?|mrs?|code reviews?|reviews?)\b`)},
	{Name: "releases", Summary: "release pipelines, releases, release notes",
		Tools:  []string{"list_release_pipelines", "list_releases", "list_release_notes", "save_release"},
		Intent: regexp.MustCompile(`(?i)发布|发版|版本|上线|更新日志|\b(release|releases|changelog|pipeline|ship|deploy)`)},
	{Name: "workspace_setup", Summary: "saved views, templates, labels, team settings",
		Tools:  []string{"list_views", "save_view", "list_templates", "save_template", "save_label", "delete_label", "save_team"},
		Intent: regexp.MustCompile(`(?i)视图|模板|标签|团队设置|工作流|\b(view|views|template|templates|label|labels|team settings|workflow)\b`)},
	{Name: "inbox", Summary: "the user's notifications (inbox)",
		Tools:  []string{"list_notifications", "update_notification"},
		Intent: regexp.MustCompile(`(?i)通知|收件箱|未读|\b(notification|notifications|inbox|unread)\b`)},
	{Name: "customers", Summary: "customers and customer requests",
		Tools:  []string{"list_customers"},
		Intent: regexp.MustCompile(`(?i)客户|需求方|\b(customer|customers|client|clients)\b`)},
	{Name: "agent_skills", Summary: "the user's Flow Agent skills",
		Tools:  []string{"list_agent_skills", "get_agent_skill", "save_agent_skill"},
		Intent: regexp.MustCompile(`(?i)技能|\bskills?\b`)},
	{Name: "images", Summary: "view images and screenshots embedded in issues, comments, or documents",
		Tools:  []string{"extract_images"},
		Intent: regexp.MustCompile(`(?i)图片|截图|图像|配图|\b(image|images|screenshot|screenshots|picture|diagram)\b`)},
	{Name: "loops", Summary: "create or edit loops (automations)",
		Tools:  []string{"save_loop"},
		Intent: regexp.MustCompile(`(?i)自动化|定时|\b(loop|loops|automation|automations|automate)\b`)},
}

// agentChatHiddenTools are never offered in chat: progress narration costs a model round trip of its own, and
// the upload tools need file bytes the chat model does not have.
var agentChatHiddenTools = map[string]bool{
	agentProgressTool: true, "prepare_attachment_upload": true, "create_attachment_from_upload": true, "create_attachment": true,
}

const agentLoadToolsTool = "load_tools"

type agentChatToolsetKey struct{}

// agentChatToolset is the tool groups one chat reply may use. load_tools adds groups mid-reply.
type agentChatToolset struct {
	mu     sync.Mutex
	groups map[string]bool
}

func withAgentChatToolset(ctx context.Context, toolset *agentChatToolset) context.Context {
	return context.WithValue(ctx, agentChatToolsetKey{}, toolset)
}

func agentChatToolsetFrom(ctx context.Context) *agentChatToolset {
	toolset, _ := ctx.Value(agentChatToolsetKey{}).(*agentChatToolset)
	return toolset
}

// newAgentChatToolset picks the groups for a reply from the latest request, the surface the chat was opened on,
// and the tools earlier turns of the conversation already used.
func newAgentChatToolset(session domain.AgentSession) *agentChatToolset {
	toolset := &agentChatToolset{groups: map[string]bool{}}
	request := agentLatestUserMessage(session)
	change := agentChangeIntent.MatchString(request)
	for _, group := range agentToolGroups {
		if group.Intent.MatchString(request) && (!group.NeedsChange || change) {
			toolset.groups[group.Name] = true
		}
	}
	// A chat opened on an issue, project, or document is usually about changing it.
	if len(session.IssueIDs) > 0 {
		toolset.groups["issue_changes"] = true
	}
	if len(session.ProjectIDs) > 0 && change {
		toolset.groups["project_changes"] = true
	}
	if len(session.DocumentIDs) > 0 {
		toolset.groups["documents"] = true
	}
	if len(session.LoopIDs) > 0 {
		toolset.groups["loops"] = true
	}
	for _, message := range session.Messages {
		for _, part := range message.Parts {
			if part.ToolCall != nil {
				if group := agentToolGroupOf(strings.TrimPrefix(part.ToolCall.Name, "mcp__flow.")); group != "" {
					toolset.groups[group] = true
				}
			}
		}
	}
	return toolset
}

func agentToolGroupOf(name string) string {
	for _, group := range agentToolGroups {
		if slices.Contains(group.Tools, name) {
			return group.Name
		}
	}
	return ""
}

func (t *agentChatToolset) loaded(name string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.groups[name]
}

// load adds the groups a load_tools call asked for and reports the tools now available.
func (t *agentChatToolset) load(arguments json.RawMessage, available []agentProviderTool) (string, bool) {
	var input struct {
		Groups []string `json:"groups"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil || len(input.Groups) == 0 {
		return `{"error":"groups is required"}`, true
	}
	names := map[string]bool{}
	for _, tool := range available {
		names[tool.Name] = true
	}
	loaded, tools := []string{}, []string{}
	t.mu.Lock()
	for _, name := range input.Groups {
		index := slices.IndexFunc(agentToolGroups, func(group agentToolGroup) bool { return group.Name == name })
		if index < 0 {
			t.mu.Unlock()
			return fmt.Sprintf(`{"error":"unknown group %q"}`, name), true
		}
		t.groups[name] = true
		loaded = append(loaded, name)
		for _, tool := range agentToolGroups[index].Tools {
			if names[tool] {
				tools = append(tools, tool)
			}
		}
	}
	t.mu.Unlock()
	raw, _ := json.Marshal(map[string]any{"loaded": loaded, "tools": tools, "note": "These tools are available from your next step."})
	return string(raw), false
}

// tools orders the available Flow tools for a chat turn: the core set, then each loaded group, then load_tools for
// the groups still left. Schemas are compacted.
func (t *agentChatToolset) tools(available []agentProviderTool) []agentProviderTool {
	byName := make(map[string]agentProviderTool, len(available))
	for _, tool := range available {
		byName[tool.Name] = tool
	}
	result := make([]agentProviderTool, 0, len(agentChatCoreTools)+16)
	add := func(name string) {
		if tool, ok := byName[name]; ok && !agentChatHiddenTools[name] {
			tool.Parameters = compactAgentToolSchema(tool.Parameters)
			result = append(result, tool)
		}
	}
	for _, name := range agentChatCoreTools {
		add(name)
	}
	remaining := []string{}
	summaries := []string{}
	for _, group := range agentToolGroups {
		if t.loaded(group.Name) {
			for _, name := range group.Tools {
				add(name)
			}
			continue
		}
		if slices.ContainsFunc(group.Tools, func(name string) bool { _, ok := byName[name]; return ok }) {
			remaining = append(remaining, group.Name)
			summaries = append(summaries, group.Name+" ("+group.Summary+")")
		}
	}
	if len(remaining) > 0 {
		enum, _ := json.Marshal(remaining)
		result = append(result, agentProviderTool{
			Name:        agentLoadToolsTool,
			Description: "Load more Flow tools when the request needs something the tools you have don't cover, such as making a change. Call it alone, then use the loaded tools in your next step. Groups: " + strings.Join(summaries, "; ") + ".",
			Parameters:  json.RawMessage(`{"type":"object","required":["groups"],"properties":{"groups":{"type":"array","items":{"type":"string","enum":` + string(enum) + `}}}}`),
			Access:      "read",
		})
	}
	return result
}

// agentChatToolRule replaces the report_progress rule in chat instructions.
const agentChatToolRule = "- You only see some Flow tools. When the request needs one you don't have (for example to create or change something), call load_tools with the matching group first instead of saying you can't.\n"

// compactAgentToolSchema drops JSON Schema keywords the model doesn't need to pick arguments (defaults and length
// or count limits; the tools validate input themselves and their descriptions state the important limits).
func compactAgentToolSchema(raw json.RawMessage) json.RawMessage {
	var schema any
	if len(raw) == 0 || json.Unmarshal(raw, &schema) != nil {
		return raw
	}
	compactAgentSchemaNode(schema)
	compacted, err := json.Marshal(schema)
	if err != nil {
		return raw
	}
	return compacted
}

func compactAgentSchemaNode(node any) {
	object, ok := node.(map[string]any)
	if !ok {
		return
	}
	for _, key := range []string{"$schema", "default", "minLength", "maxLength", "minItems", "maxItems", "examples"} {
		delete(object, key)
	}
	if properties, ok := object["properties"].(map[string]any); ok {
		for _, property := range properties {
			compactAgentSchemaNode(property)
		}
	}
	for _, key := range []string{"items", "additionalProperties", "not"} {
		compactAgentSchemaNode(object[key])
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if list, ok := object[key].([]any); ok {
			for _, item := range list {
				compactAgentSchemaNode(item)
			}
		}
	}
}
