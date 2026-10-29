/**
 * Chat tool economy, ported from api/cmd/server/agent_chat_tools.go: every turn offers the core read tools plus the
 * groups picked from the request; the model pulls in other groups with load_tools. In production the run payload
 * carries the core list and groups (Flow owns them); the worker only applies them.
 * Generated from the Go source; keep in sync until the payload carries the groups.
 */
export type ToolGroup = { name: string; summary: string; tools: string[]; intent: RegExp; needsChange: boolean }

export const coreTools: string[] = ["search_issues", "get_issue", "list_issues", "list_comments", "list_issue_history", "get_project", "list_projects", "list_project_activity", "get_status_updates", "list_milestones", "get_document", "list_documents", "list_users", "get_user", "list_teams", "get_team", "list_cycles", "list_issue_statuses", "get_issue_status", "list_issue_labels", "list_initiatives", "get_initiative", "get_workspace", "search_documentation"]

/** Matches requests to create, change, or remove something (Chinese and English). */
export const changeIntent = /改|设置|设为|设成|调成|调到|调整|更新|创建|新建|新增|建一个|建个|添加|加上|加个|加到|删除|删掉|去掉|移除|移到|分配|指派|指给|交给|关闭|关掉|完成|标记|评论|回复|留言|订阅|提醒|草稿|分流|接受|拒绝|重新打开|归档|恢复|发布|提交|合并|批准|写一?[个份篇]|\b(set|change|update|edit|create|add|remove|delete|assign|unassign|close|reopen|mark|move|rename|comment|reply|subscribe|unsubscribe|remind|draft|triage|accept|decline|file|log|make|prioriti[sz]e|label|tag|archive|restore|estimate|post|publish|submit|merge|approve|write)\b/i

export const toolGroups: ToolGroup[] = [
  { name: "issue_changes", summary: "create, edit, comment on, triage, delete issues; reactions, subscriptions, reminders, drafts", needsChange: true,
    tools: ["save_issue", "save_comment", "delete_comment", "triage_issue", "delete_issue", "save_reaction", "save_subscription", "create_reminder", "create_issue_label", "save_draft", "delete_attachment", "save_attachment"],
    intent: /./ },
  { name: "project_changes", summary: "create, edit, or delete projects and initiatives; milestones, status updates, and cycle names, descriptions and links", needsChange: true,
    tools: ["save_project", "save_milestone", "save_status_update", "save_initiative", "create_initiative_label", "list_project_labels", "list_initiative_labels", "delete_project", "delete_initiative", "save_cycle"],
    intent: /项目|里程碑|计划|目标|状态更新|进展|周期|迭代|\b(project|milestone|initiative|status update|roadmap|cycle|sprint)/i },
  { name: "documents", summary: "write, delete, or restore documents; document history and sharing", needsChange: false,
    tools: ["save_document", "delete_document", "restore_document", "list_document_history", "restore_document_version", "get_document_permissions", "save_document_permissions"],
    intent: /(写|起草|编辑|修改|更新|创建|新建|删除|恢复).{0,12}(文档|文章|页面|笔记)|(文档|文章|页面|笔记).{0,12}(历史|版本|权限|共享|分享)|\b(write|draft|edit|create|delete|restore|share)\b.{0,30}\b(doc|docs|document|page|note)s?\b|\bdocument (history|version|permission)/i },
  { name: "code_reviews", summary: "code reviews: diffs, review threads, submitting reviews, merging", needsChange: false,
    tools: ["list_diffs", "get_diff", "get_diff_threads", "delete_diff_comment", "resolve_diff_thread", "submit_diff_review", "merge_diff"],
    intent: /评审|代码审查|审查|合并请求|\b(diffs?|prs?|pull requests?|merge requests?|mrs?|code reviews?|reviews?)\b/i },
  { name: "releases", summary: "release pipelines, releases, release notes", needsChange: false,
    tools: ["list_release_pipelines", "list_releases", "list_release_notes", "save_release"],
    intent: /发布|发版|版本|上线|更新日志|\b(release|releases|changelog|pipeline|ship|deploy)/i },
  { name: "workspace_setup", summary: "saved views (create, edit, delete), templates, labels, team settings", needsChange: false,
    tools: ["list_views", "save_view", "delete_view", "list_templates", "save_template", "save_label", "delete_label", "save_team"],
    intent: /视图|模板|标签|团队设置|工作流|\b(view|views|template|templates|label|labels|team settings|workflow)\b/i },
  { name: "inbox", summary: "the user's notifications (inbox)", needsChange: false,
    tools: ["list_notifications", "update_notification"],
    intent: /通知|收件箱|未读|\b(notification|notifications|inbox|unread)\b/i },
  { name: "customers", summary: "customers and customer requests: look up, create, edit, log requests on issues or projects, delete", needsChange: false,
    tools: ["list_customers", "get_customer", "save_customer", "save_customer_request", "delete_customer", "delete_customer_request"],
    intent: /客户|需求方|\b(customer|customers|client|clients)\b/i },
  { name: "agent_skills", summary: "the user's and teams' Flow Agent skills (create, edit, delete)", needsChange: false,
    tools: ["list_agent_skills", "get_agent_skill", "save_agent_skill", "delete_agent_skill"],
    intent: /技能|\bskills?\b/i },
  { name: "images", summary: "view images and screenshots embedded in issues, comments, or documents", needsChange: false,
    tools: ["extract_images"],
    intent: /图片|截图|图像|配图|\b(image|images|screenshot|screenshots|picture|diagram)\b/i },
  { name: "coding", summary: "start a coding session that changes code and opens a draft pull request", needsChange: false,
    tools: ["start_coding_session"],
    intent: /修复|实现|重构|改代码|写代码|代码|测试|合并请求|\b(fix|fixes|bug|implement|refactor|code|coding|tests?|pr|pull request|merge request|mr|patch|repo|repository|rebase|lint)\b/i },
  { name: "loops", summary: "create, edit, or delete loops (automations)", needsChange: false,
    tools: ["save_loop", "delete_loop"],
    intent: /自动化|定时|\b(loop|loops|automation|automations|automate)\b/i },
]

/** Never offered in chat: progress narration costs a model round trip, upload tools need file bytes. */
export const hiddenTools = new Set(["report_progress", "prepare_attachment_upload", "create_attachment_from_upload", "create_attachment"])

/** The groups a request starts with (agent_chat_tools.go newAgentChatToolset, without surface context). */
export function initialGroups(request: string, previousTools: string[] = []): string[] {
  const change = changeIntent.test(request)
  const groups = new Set<string>()
  for (const group of toolGroups) if (group.intent.test(request) && (!group.needsChange || change)) groups.add(group.name)
  for (const tool of previousTools) {
    const group = groupOf(tool)
    if (group) groups.add(group)
  }
  return [...groups]
}

export function groupOf(tool: string): string | undefined {
  return toolGroups.find(group => group.tools.includes(tool))?.name
}

/**
 * Tools Flow's /mcp lists to a run only while its session gives them (api/cmd/server/agent_runtime_session_tools.go):
 * the conversation's file tools, and a loop builder's chip question. Offered whenever listed.
 */
export const sessionTools = new Set(['read_file', 'transcribe_media', 'ask_question'])

/** Session tools whose call waits for the person (a chip question: up to 5 minutes on Flow). */
export const waitingTools = new Set(['ask_question'])

/** Whether a Flow tool is offered this turn: core, a session tool, or in a loaded group, and never hidden. */
export function toolOffered(tool: string, loadedGroups: readonly string[]): boolean {
  if (hiddenTools.has(tool)) return false
  // Connector (MCP) tools are offered as in chat; the run guard bounds them (run-guard.ts).
  if (coreTools.includes(tool) || sessionTools.has(tool) || tool.startsWith('external_')) return true
  const group = groupOf(tool)
  return group !== undefined && loadedGroups.includes(group)
}

