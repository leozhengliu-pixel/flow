import { useEffect, useState } from "react";
import { AlertCircle, Check, ChevronRight, CircleCheck, LoaderCircle } from "lucide-react";
import type { AgentMessage, AgentToolCall } from "@/types/flow";
import { useI18n } from "@/i18n/i18n";
import styles from "./agent-page.module.css";

/** Shared "Worked for N seconds" group used by the Agent page and embedded agent threads. */
export function AgentWorkGroup({ message, parts, onToolApproval, approvalBusy, running: forceRunning = false, className }: { message: Pick<AgentMessage, "durationMs">; parts: NonNullable<AgentMessage["parts"]>; onToolApproval: (call: AgentToolCall | undefined, decision: "approve" | "reject") => void; approvalBusy?: string; running?: boolean; className?: string }) {
  const { t } = useI18n();
  const running = forceRunning || parts.some(part => part.status === "running" || part.status === "pending" || part.toolCall?.status === "running" || part.toolCall?.status === "pending");
  const failed = parts.some(part => part.status === "error" || part.toolCall?.status === "error");
  const [open, setOpen] = useState(running || failed);
  useEffect(() => {
    if (running || failed) setOpen(true);
  }, [failed, running]);
  const toolCount = parts.filter(part => part.type === "toolCall").length;
  const duration = Math.max(1, Math.round((message.durationMs ?? 0) / 1000));
  const label = running
    ? t("Working…")
    : message.durationMs
      ? `${t("Worked for")} ${duration} ${t(duration === 1 ? "second" : "seconds")}`
      : toolCount > 2
        ? `${t("Used")} ${toolCount} ${t("tools")}`
        : t("Work completed");
  return <details className={`${styles.workGroup}${failed ? ` ${styles.workFailed}` : ""}${className ? ` ${className}` : ""}`} open={open} onToggle={event => setOpen(event.currentTarget.open)}>
    <summary>{running ? <LoaderCircle className={styles.spin}/> : failed ? <AlertCircle/> : <CircleCheck/>}<span>{label}</span><ChevronRight/></summary>
    <div className={styles.workItems}>
      {parts.map(part => part.type === "reasoning"
        ? <div className={styles.reasoningRow} key={part.id}><span>{part.status === "running" ? t("Thinking…") : t("Reasoning")}</span>{part.text && <p>{part.text}</p>}</div>
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
  return <details className={`${styles.toolCall} ${call.status === "error" ? styles.toolCallError : ""}`} open={approvalPending || call.status === "error" || undefined}>
    <summary>{running ? <LoaderCircle className={styles.spin}/> : call.status === "error" ? <AlertCircle/> : <Check/>}<span>{toolStatusLabel(call.name, running)}</span>{detail && <small>{detail}</small>}<ChevronRight/></summary>
    <div>{approvalPending && <div className={styles.approvalPrompt}><span>{t("Waiting for approval")}</span><span className={styles.approvalActions}><button disabled={approvalBusy === call.approvalId} onClick={() => onApproval(call, "reject")} type="button">{t("Reject tool")}</button><button disabled={approvalBusy === call.approvalId} onClick={() => onApproval(call, "approve")} type="button">{t("Approve tool")}</button></span></div>}{call.error && <p role="alert">{call.error}</p>}<code>{JSON.stringify(call.result ?? call.arguments ?? {}, null, 2)}</code></div>
  </details>;
}

function toolStatusLabel(name: string, running: boolean) {
  const labels: Record<string, [string, string]> = {
    list_issues: ["Looking at issues…", "Looked at issues"], list_projects: ["Looking at projects…", "Looked at projects"],
    list_initiatives: ["Looking at initiatives…", "Looked at initiatives"], list_documents: ["Looking at documents…", "Looked at documents"],
    search_documentation: ["Searching documentation…", "Searched documentation"], save_issue: ["Updating issue…", "Updated issue"],
    save_project: ["Updating project…", "Updated project"], save_initiative: ["Updating initiative…", "Updated initiative"],
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
  for (const key of ["query", "name", "id", "issueId", "projectId"]) {
    const item = value[key];
    if (typeof item === "string" && item && !internalId.test(item)) return item;
  }
  return "";
}
