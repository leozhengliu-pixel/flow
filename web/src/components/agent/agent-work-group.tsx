import { useEffect, useState } from "react";
import type { AgentMessage, AgentToolCall } from "@/types/flow";
import { useI18n } from "@/i18n/i18n";
import styles from "./agent-page.module.css";

/** Shared "Worked for N seconds" group used by the Agent page and embedded agent threads. */
export function AgentWorkGroup({ message, parts: allParts, onToolApproval, approvalBusy, running: forceRunning = false, className, collapseWhenDone = false }: { message: Pick<AgentMessage, "durationMs">; parts: NonNullable<AgentMessage["parts"]>; onToolApproval: (call: AgentToolCall | undefined, decision: "approve" | "reject") => void; approvalBusy?: string; running?: boolean; className?: string; /** Fold back to "Worked for N seconds ▸" when the turn finishes (loop builder). */ collapseWhenDone?: boolean }) {
  const { t } = useI18n();
  // Older chats stored an unnamed placeholder for report_progress next to its step; it is not a real tool row.
  const settled = Boolean(message.durationMs) && !forceRunning;
  // The loop builder's ask_question calls render as answer chips, never as tool rows.
  const parts = allParts.filter(part => (part.type !== "toolCall" || Boolean(part.toolCall?.name) || !settled) && part.toolCall?.name !== "ask_question");
  const running = forceRunning || parts.some(part => part.status === "running" || part.status === "pending" || part.toolCall?.status === "running" || part.toolCall?.status === "pending");
  const failed = parts.some(part => part.status === "error" || part.toolCall?.status === "error");
  const [open, setOpen] = useState(running || failed);
  useEffect(() => {
    if (running || failed) setOpen(true);
    else if (collapseWhenDone) setOpen(false);
  }, [collapseWhenDone, failed, running]);
  const toolCount = parts.filter(part => part.type === "toolCall").length;
  const duration = Math.max(1, Math.round((message.durationMs ?? 0) / 1000));
  // Linear shows the current phase ("Reviewing inbox…") while working.
  const currentStep = [...parts].reverse().find(part => part.type === "step" && part.title)?.title;
  const label = running
    ? currentStep ? `${currentStep}…` : t("Working…")
    : message.durationMs
      ? `${t("Worked for")} ${duration} ${t(duration === 1 ? "second" : "seconds")}`
      : toolCount > 2
        ? `${t("Used")} ${toolCount} ${t("tools")}`
        : t("Work completed");
  return <details className={`${styles.workGroup}${failed ? ` ${styles.workFailed}` : ""}${className ? ` ${className}` : ""}`} open={open} onToggle={event => setOpen(event.currentTarget.open)}>
    {/* Linear: label plus a small disclosure triangle; steps are flat 12px rows, narration is a quote with a 2px bar. */}
    <summary><span className={running ? styles.workShimmer : undefined}>{label}</span><WorkDisclosureIcon/></summary>
    <div className={styles.workItems}>
      {parts.map(part => part.type === "step"
        ? <div className={styles.stepRow} key={part.id}><span data-i18n-ignore>{part.title}</span>{part.text && <div className={styles.reasoningRow}><p data-i18n-ignore>{part.text}</p></div>}</div>
        : part.type === "reasoning"
        ? <div className={styles.reasoningRow} key={part.id}>{part.status === "running" && !part.text && <span className={styles.workShimmer}>{t("Thinking…")}</span>}{part.text && <p>{part.text}</p>}</div>
        : part.toolCall ? <AgentToolCallItem key={part.id} part={part} onApproval={onToolApproval} approvalBusy={approvalBusy}/> : null)}
    </div>
  </details>;
}

function AgentToolCallItem({ part, onApproval, approvalBusy }: { part: NonNullable<AgentMessage["parts"]>[number]; onApproval: (call: AgentToolCall | undefined, decision: "approve" | "reject") => void; approvalBusy?: string }) {
  const { t } = useI18n();
  const call = part.toolCall!;
  const running = call.status === "running" || call.status === "pending";
  const detail = readableToolDetail(call.arguments, call.result);
  const approvalPending = call.status === "pending" && Boolean(call.approvalId);
  return <div className={`${styles.toolCall} ${call.status === "error" ? styles.toolCallError : ""}`}>
    <div className={styles.toolCallRow} title={call.error || undefined}><span className={running ? styles.workShimmer : undefined}>{call.title && !running ? t(call.title) : toolStatusLabel(call.name, running, call.arguments)}</span>{detail && <span data-i18n-ignore>{detail}</span>}</div>
    {approvalPending && <div className={styles.approvalPrompt}><span>{t("Waiting for approval")}</span><span className={styles.approvalActions}><button disabled={approvalBusy === call.approvalId} onClick={() => onApproval(call, "reject")} type="button">{t("Reject tool")}</button><button disabled={approvalBusy === call.approvalId} onClick={() => onApproval(call, "approve")} type="button">{t("Approve tool")}</button></span></div>}
    {call.error && <p className={styles.toolCallErrorText} role="alert">{call.error}</p>}
  </div>;
}

function toolStatusLabel(name: string, running: boolean, args?: Record<string, unknown>) {
  const updating = typeof args?.id === "string" && args.id !== "";
  const labels: Record<string, [string, string]> = {
    list_issues: ["Looking at issues…", "Looked at issues"], list_projects: ["Looking at projects…", "Looked at projects"],
    list_initiatives: ["Looking at initiatives…", "Looked at initiatives"], list_documents: ["Looking at documents…", "Looked at documents"],
    search_documentation: ["Searching documentation…", "Searched documentation"], save_issue: updating ? ["Updating issue…", "Updated issue"] : ["Creating issue…", "Created issue"],
    save_project: updating ? ["Updating project…", "Updated project"] : ["Creating project…", "Created project"], save_initiative: updating ? ["Updating initiative…", "Updated initiative"] : ["Creating initiative…", "Created initiative"],
    save_comment: updating ? ["Updating comment…", "Updated comment"] : ["Adding comment…", "Added comment"],
    get_issue: ["Looking at issue…", "Looked at issue"], list_issue_history: ["Looking at issue activity…", "Looked at issue activity"],
    list_project_activity: ["Looking at project activity…", "Looked at project activity"], get_status_updates: ["Looking at project updates…", "Looked at project updates"],
    search_issues: ["Searching issues…", "Searched issues"], list_notifications: ["Reviewing inbox…", "Reviewed inbox"],
    list_users: ["Looking at users…", "Looked at users"], list_views: ["Looking at views…", "Looked at views"],
    list_templates: ["Looking at templates…", "Looked at templates"], list_customers: ["Looking at customers…", "Looked at customers"],
    save_status_update: ["Creating project update…", "Created project update"], save_draft: ["Creating draft…", "Created draft"],
    save_team: updating ? ["Updating team…", "Updated team"] : ["Creating team…", "Created team"],
    save_label: ["Saving label…", "Saved label"], delete_label: ["Deleting label…", "Deleted label"],
    save_view: ["Saving view…", "Saved view"], delete_issue: ["Deleting issue…", "Deleted issue"],
    triage_issue: ["Triaging issue…", "Triaged issue"], save_reaction: ["Reacting…", "Reacted"],
    save_subscription: ["Updating subscription…", "Updated subscription"], save_document: ["Saving document…", "Saved document"],
    save_template: ["Saving template…", "Saved template"], update_notification: ["Updating inbox…", "Updated inbox"],
    save_agent_skill: ["Saving skill…", "Saved skill"], save_loop: ["Saving loop…", "Saved loop"],
  };
  if (labels[name]) return labels[name][running ? 0 : 1];
  const [verb, ...words] = name.split("_");
  const subject = words.join(" ") || "workspace";
  const verbs: Record<string, [string, string]> = {
    list: ["Looking at", "Looked at"], get: ["Looking at", "Looked at"], search: ["Searching", "Searched"], extract: ["Extracting", "Extracted"],
    save: ["Updating", "Updated"], update: ["Updating", "Updated"], create: ["Creating", "Created"], delete: ["Deleting", "Deleted"],
    prepare: ["Preparing", "Prepared"], merge: ["Merging", "Merged"], submit: ["Submitting", "Submitted"], resolve: ["Resolving", "Resolved"],
  };
  const action = verbs[verb]?.[running ? 0 : 1];
  if (action) return `${action} ${subject}${running ? "…" : ""}`;
  const fallback = name.replaceAll("_", " ").replace(/^./, value => value.toUpperCase());
  return running ? `${fallback}…` : fallback;
}

/** Step subtitle: the looked-up entity's name when the result has one, never a bare internal id (project_123…). */
function readableToolDetail(value: Record<string, unknown> | undefined, result?: unknown) {
  const internalId = /^[a-z]+(?:_[a-z]+)*_\d{6,}$/;
  if (result && typeof result === "object" && !Array.isArray(result)) {
    const record = result as Record<string, unknown>;
    for (const key of ["identifier", "name", "title"]) {
      if (typeof record[key] === "string" && record[key]) return String(record[key]);
    }
  }
  if (!value) return "";
  // Linear lists every phrasing of a multi-query search: "import data", "data migration".
  if (Array.isArray(value.queries)) {
    const queries = value.queries.filter((item): item is string => typeof item === "string" && item.trim() !== "");
    if (queries.length) return queries.map(item => `"${item}"`).join(", ");
  }
  for (const key of ["query", "name", "title", "id", "issueId", "projectId", "project", "issue", "team", "emoji"]) {
    const item = value[key];
    if (typeof item === "string" && item && !internalId.test(item)) return item;
  }
  return "";
}

/** Linear's 16px disclosure triangle (points right when collapsed, down when open via CSS). */
function WorkDisclosureIcon() {
  return <svg aria-hidden="true" viewBox="0 0 16 16" width="16" height="16"><path d="M6.5 5.2v5.6a.4.4 0 0 0 .65.3l3.5-2.8a.4.4 0 0 0 0-.6l-3.5-2.8a.4.4 0 0 0-.65.3Z" fill="currentColor"/></svg>;
}

