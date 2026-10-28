import { useState } from "react";
import type { AgentMessage, AgentMessageAttachment, AgentToolCall } from "@/types/flow";
import { useI18n } from "@/i18n/i18n";
import { AgentRichText } from "./agent-rich-text";
import { isProviderReasoning, readFileName, toolStatusLabel, translatePhaseTitle, translateToolTitle, workingLabel } from "./agent-step-labels";
import styles from "./agent-page.module.css";
import workStyles from "./agent-work-group.module.css";

/** Shared "Worked for N seconds" group used by the Agent page and embedded agent threads. */
export function AgentWorkGroup({ message, parts: allParts, onToolApproval, approvalBusy, running: forceRunning = false, className, collapseWhenDone = false, attachments }: { message: Pick<AgentMessage, "durationMs">; parts: NonNullable<AgentMessage["parts"]>; onToolApproval: (call: AgentToolCall | undefined, decision: "approve" | "reject") => void; approvalBusy?: string; running?: boolean; className?: string; /** Fold back to "Worked for N seconds ▸" when the turn finishes (loop builder). */ collapseWhenDone?: boolean; /** The session's message attachments, to name the file a read_file step opened. */ attachments?: AgentMessageAttachment[] }) {
  const { t } = useI18n();
  // Older chats stored an unnamed placeholder for report_progress next to its step; it is not a real tool row.
  const settled = Boolean(message.durationMs) && !forceRunning;
  // The loop builder's ask_question calls render as answer chips, never as tool rows.
  // Provider reasoning is the model's scratchpad (often English in a Chinese UI):, only step labels and the
  // agent's own narration show. Older chats stored it; it stays hidden.
  const parts = allParts.filter(part => !isProviderReasoning(part) && (part.type !== "toolCall" || Boolean(part.toolCall?.name) || !settled) && part.toolCall?.name !== "ask_question");
  // A finished reply (older stopped chats saved calls as still running) is never "working…".
  const running = forceRunning || !settled && parts.some(part => part.status === "running" || part.status === "pending" || part.toolCall?.status === "running" || part.toolCall?.status === "pending");
  const failed = parts.some(part => part.status === "error" || part.toolCall?.status === "error");
  const [open, setOpen] = useState(running || failed);
  // When the turn starts, fails or (with collapseWhenDone) finishes, the group follows; the user's own toggle stands otherwise.
  const followKey = `${collapseWhenDone}|${failed}|${running}`;
  const [seenFollowKey, setSeenFollowKey] = useState(followKey);
  if (seenFollowKey !== followKey) {
    setSeenFollowKey(followKey);
    if (running || failed) setOpen(true);
    else if (collapseWhenDone) setOpen(false);
  }
  const toolCount = parts.filter(part => part.type === "toolCall").length;
  const duration = Math.max(1, Math.round((message.durationMs ?? 0) / 1000));
  const label = running
    ? workingLabel(parts, t)
    : message.durationMs
      ? `${t("Worked for")} ${duration} ${t(duration === 1 ? "second" : "seconds")}`
      : toolCount > 2
        ? `${t("Used")} ${toolCount} ${t("tools")}`
        : t("Work completed");
  if (!parts.length && !running) return null;
  return <details className={`${styles.workGroup}${failed ? ` ${styles.workFailed}` : ""}${className ? ` ${className}` : ""}`} open={open} onToggle={event => setOpen(event.currentTarget.open)}>
    {/* The reference app: label plus a small disclosure triangle; steps are flat 12px rows, narration is a quote with a 2px bar. */}
    <summary><span className={running ? styles.workShimmer : undefined}>{label}</span><WorkDisclosureIcon/></summary>
    <div className={styles.workItems}>
      {parts.map(part => part.type === "step"
        ? <div className={styles.stepRow} key={part.id}><span data-i18n-ignore>{translatePhaseTitle(part.title ?? "", t)}</span>{part.text && <div className={styles.reasoningRow}><AgentReasoningText text={part.text}/></div>}</div>
        : part.type === "reasoning"
        ? <div className={styles.reasoningRow} key={part.id}>{part.status === "running" && !part.text && <span className={styles.workShimmer}>{t("Thinking…")}</span>}{part.text && <AgentReasoningText text={part.text}/>}</div>
        : part.toolCall ? <AgentToolCallItem key={part.id} attachments={attachments} part={settled ? settledToolPart(part) : part} onApproval={onToolApproval} approvalBusy={approvalBusy}/> : null)}
    </div>
  </details>;
}

/** Model-written narration (reasoning summaries, step plans) is markdown: "**Planning** the work" renders bold, never literal asterisks. */
function AgentReasoningText({ text }: { text: string }) {
  const { t } = useI18n();
  return <AgentRichText ariaLabel={t("Agent reasoning")} className={workStyles.reasoningText} content={splitAdjacentBold(text)}/>;
}

/** Reasoning summaries concatenate bold headings ("**One****Two**"), which markdown cannot parse; put each on its own paragraph. */
function splitAdjacentBold(text: string) {
  return text.replace(/(\S)\*\*\*\*(?=\S)/g, "$1**\n\n**");
}

function AgentToolCallItem({ part, onApproval, approvalBusy, attachments }: { attachments?: AgentMessageAttachment[]; part: NonNullable<AgentMessage["parts"]>[number]; onApproval: (call: AgentToolCall | undefined, decision: "approve" | "reject") => void; approvalBusy?: string }) {
  const { t } = useI18n();
  const call = part.toolCall!;
  const running = call.status === "running" || call.status === "pending";
  const detail = readableToolDetail(call.arguments, call.result, call.name, attachments);
  const approvalPending = call.status === "pending" && Boolean(call.approvalId);
  return <div className={`${styles.toolCall} ${call.status === "error" ? styles.toolCallError : ""}`}>
    <div className={styles.toolCallRow} title={call.error || undefined}><span className={running ? styles.workShimmer : undefined} data-i18n-ignore>{call.title && !running ? translateToolTitle(call.title, t, call) : toolStatusLabel(call.name, running, t, call.arguments, call.status === "running", call)}</span>{detail && <span data-i18n-ignore>{detail}</span>}</div>
    {approvalPending && <div className={styles.approvalPrompt}><span>{t("Waiting for approval")}</span><span className={styles.approvalActions}><button disabled={approvalBusy === call.approvalId} onClick={() => onApproval(call, "reject")} type="button">{t("Reject tool")}</button><button disabled={approvalBusy === call.approvalId} onClick={() => onApproval(call, "approve")} type="button">{t("Approve tool")}</button></span></div>}
    {call.error && <p className={styles.toolCallErrorText} role="alert">{t(call.error)}</p>}
  </div>;
}

/** A step a finished reply never completed (stopped mid-call in an older chat) reads as stopped, not as running. */
function settledToolPart(part: NonNullable<AgentMessage["parts"]>[number]) {
  const call = part.toolCall;
  if (!call || (call.status !== "running" && call.status !== "pending") || call.approvalId) return part;
  return { ...part, status: "error" as const, toolCall: { ...call, status: "error" as const, error: call.error || "Stopped before this step finished." } };
}

/** Step subtitle: the looked-up entity's name when the result has one, never a bare internal id (project_123…). */
function readableToolDetail(value: Record<string, unknown> | undefined, result?: unknown, tool?: string, attachments?: AgentMessageAttachment[]) {
  // Read file <name>; "Transcribed media" carries no name, like the reference app.
  if (tool === "transcribe_media") return "";
  if (tool === "read_file") return readFileName(value?.file, attachments);
  // project_1790…, or project_1793…_k3j9x0abcd (time digits plus a random tail).
  const internalId = /^[a-z]+(?:_[a-z]+)*_\d{6,}(?:_[0-9a-z]{10})?$/;
  if (result && typeof result === "object" && !Array.isArray(result)) {
    const record = result as Record<string, unknown>;
    for (const key of ["identifier", "name", "title"]) {
      if (typeof record[key] === "string" && record[key]) return String(record[key]);
    }
  }
  if (!value) return "";
  // List every phrasing of a multi-query search: "import data", "data migration".
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

/** The reference app's 16px disclosure triangle (points right when collapsed, down when open via CSS). */
function WorkDisclosureIcon() {
  return <svg aria-hidden="true" viewBox="0 0 16 16" width="16" height="16"><path d="M6.5 5.2v5.6a.4.4 0 0 0 .65.3l3.5-2.8a.4.4 0 0 0 0-.6l-3.5-2.8a.4.4 0 0 0-.65.3Z" fill="currentColor"/></svg>;
}

