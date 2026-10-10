/**
 * Tool step labels in agent work groups (Agent page, embedded agent threads, loop runs), translated with t().
 * Chat tool calls are labelled from the tool name; loop run tool calls carry the server's English label
 * (loopToolLabel in api/cmd/server/loop_runtime.go), translated here by exact entry or by its verb template.
 * Chinese copy lives in i18n/translations-agent-steps.ts.
 */
type Translate = (source: string) => string

/** Chat labels by tool name: [running, done]. */
export const CHAT_TOOL_LABELS: Record<string, [string, string]> = {
  list_issues: ["Looking at issues…", "Looked at issues"], list_projects: ["Looking at projects…", "Looked at projects"],
  list_initiatives: ["Looking at initiatives…", "Looked at initiatives"], list_documents: ["Looking at documents…", "Looked at documents"],
  search_documentation: ["Searching documentation…", "Searched documentation"], save_issue: ["Creating issue…", "Created issue"],
  save_project: ["Creating project…", "Created project"], save_initiative: ["Creating initiative…", "Created initiative"],
  save_comment: ["Adding comment…", "Added comment"],
  get_issue: ["Looking at issue…", "Looked at issue"], list_issue_history: ["Looking at issue activity…", "Looked at issue activity"],
  list_project_activity: ["Looking at project activity…", "Looked at project activity"], get_status_updates: ["Looking at project updates…", "Looked at project updates"],
  search_issues: ["Searching issues…", "Searched issues"], list_notifications: ["Reviewing inbox…", "Reviewed inbox"],
  list_users: ["Looking at users…", "Looked at users"], list_views: ["Looking at views…", "Looked at views"],
  list_templates: ["Looking at templates…", "Looked at templates"], list_customers: ["Looking at customers…", "Looked at customers"],
  save_status_update: ["Creating project update…", "Created project update"], save_draft: ["Creating draft…", "Created draft"],
  save_team: ["Creating team…", "Created team"],
  save_label: ["Saving label…", "Saved label"], delete_label: ["Deleting label…", "Deleted label"],
  save_view: ["Saving view…", "Saved view"], delete_issue: ["Deleting issue…", "Deleted issue"],
  triage_issue: ["Triaging issue…", "Triaged issue"], save_reaction: ["Reacting…", "Reacted"],
  save_subscription: ["Updating subscription…", "Updated subscription"], save_document: ["Saving document…", "Saved document"],
  delete_document: ["Deleting document…", "Deleted document"], restore_document: ["Restoring document…", "Restored document"],
  list_document_history: ["Looking at document history…", "Looked at document history"],
  restore_document_version: ["Restoring document version…", "Restored document version"],
  get_document_permissions: ["Looking at document access…", "Looked at document access"],
  save_document_permissions: ["Updating document access…", "Updated document access"],
  save_template: ["Saving template…", "Saved template"], update_notification: ["Updating inbox…", "Updated inbox"],
  save_agent_skill: ["Saving skill…", "Saved skill"], save_loop: ["Saving loop…", "Saved loop"],
  report_progress: ["Reporting progress…", "Reported progress"],
}

/** Chat labels of save tools called with an id (an update instead of a create). */
export const CHAT_TOOL_UPDATE_LABELS: Record<string, [string, string]> = {
  save_issue: ["Updating issue…", "Updated issue"], save_project: ["Updating project…", "Updated project"],
  save_initiative: ["Updating initiative…", "Updated initiative"], save_comment: ["Updating comment…", "Updated comment"],
  save_team: ["Updating team…", "Updated team"],
}

/** Chat labels of other tools by verb: [running, done] templates; {subject} is the rest of the tool name. */
export const CHAT_TOOL_VERBS: Record<string, [string, string]> = {
  list: ["Looking at {subject}…", "Looked at {subject}"], get: ["Looking at {subject}…", "Looked at {subject}"],
  search: ["Searching {subject}…", "Searched {subject}"], extract: ["Extracting {subject}…", "Extracted {subject}"],
  save: ["Updating {subject}…", "Updated {subject}"], update: ["Updating {subject}…", "Updated {subject}"],
  create: ["Creating {subject}…", "Created {subject}"], delete: ["Deleting {subject}…", "Deleted {subject}"],
  prepare: ["Preparing {subject}…", "Prepared {subject}"], merge: ["Merging {subject}…", "Merged {subject}"],
  submit: ["Submitting {subject}…", "Submitted {subject}"], resolve: ["Resolving {subject}…", "Resolved {subject}"],
}

/** Any other tool (connectors, future tools): its humanized name. */
export const CHAT_TOOL_FALLBACK: [string, string] = ["Running {subject}…", "Ran {subject}"]

/**
 * Verb templates of the server's loop run labels ("Listed release notes", "Used github search"); mirrors
 * loopToolVerbTemplates, loopExternalToolTemplate and loopOtherToolTemplate in api/cmd/server/loop_runtime.go.
 */
export const LOOP_TOOL_TEMPLATES = [
  "Read {subject}", "Listed {subject}", "Searched {subject}", "Saved {subject}", "Deleted {subject}", "Created {subject}",
  "Updated {subject}", "Extracted {subject}", "Prepared {subject}", "Merged {subject}", "Submitted {subject}", "Resolved {subject}",
  "Used {subject}", "Ran {subject}",
]

/** A running or finished chat tool call's label, translated. */
export function toolStatusLabel(name: string, running: boolean, t: Translate, args?: Record<string, unknown>) {
  const tool = name.replace(/^mcp__flow\./, "")
  const updating = typeof args?.id === "string" && args.id !== ""
  const fixed = (updating ? CHAT_TOOL_UPDATE_LABELS[tool] : undefined) ?? CHAT_TOOL_LABELS[tool]
  if (fixed) return t(fixed[running ? 0 : 1])
  const [verb, ...words] = tool.split("_")
  const templates = words.length ? CHAT_TOOL_VERBS[verb] : undefined
  if (templates) return fillSubject(templates[running ? 0 : 1], words.join(" "), t)
  return fillSubject(CHAT_TOOL_FALLBACK[running ? 0 : 1], tool.replaceAll("_", " ") || "tool", t)
}

/** A server-written tool label (loop run steps, the loop builder's save_loop title), translated. */
export function translateToolTitle(title: string, t: Translate) {
  const exact = t(title)
  if (exact !== title) return exact
  for (const template of LOOP_TOOL_TEMPLATES) {
    const prefix = template.replace("{subject}", "")
    if (title.startsWith(prefix) && title.length > prefix.length) return fillSubject(template, title.slice(prefix.length), t)
  }
  return title
}

/** Fills {subject} with its translation; a translated (Chinese) template gets spaces around a Latin subject. */
export function fillSubject(template: string, subject: string, t: Translate) {
  const translated = t(template)
  const text = translated.replace("{subject}", () => subjectNoun(subject, t))
  return translated === template ? text : spaceMixedScripts(text)
}

/**
 * A tool subject's translation, kept under its own "subject:" key: shared nouns such as "issues" already translate
 * as counted words ("个事项") elsewhere. Unknown subjects (connector tools) stay as written.
 */
export function subjectNoun(subject: string, t: Translate) {
  const key = `${SUBJECT_KEY}${subject}`
  const translated = t(key)
  return translated === key ? subject : translated
}

/** Translation key prefix of tool subjects ("subject:release notes"). */
export const SUBJECT_KEY = "subject:"

function spaceMixedScripts(text: string) {
  return text
    .replace(/([㐀-鿿])([A-Za-z0-9])/g, "$1 $2")
    .replace(/([A-Za-z0-9])([㐀-鿿])/g, "$1 $2")
}
