import type { LoopRun, LoopRunReply } from "@/types/flow";
import { classifyAgentError, describeAgentError, looksRaw } from "@/components/agent/agent-error-detail";
import { translateToolTitle } from "@/components/agent/agent-step-labels";

type Translate = (source: string) => string;

/**
 * English trigger reasons by code; mirrors loopTriggerReasonTexts in api/cmd/server/loop_triggers.go
 * (TestLoopRunVocabularyTranslated checks the two stay equal). {value} is the property's new value.
 */
export const TRIGGER_REASON_TEXTS: Record<string, string> = {
  created: "created",
  updated: "updated",
  comment: "new comment",
  customerRequest: "new customer request",
  triage: "entering triage",
  status: "status → {value}",
  statusChanged: "status changed",
  priority: "priority → {value}",
  assignee: "assignee → {value}",
  agent: "agent → {value}",
  project: "project → {value}",
  team: "team → {value}",
  label: "label {value} added",
  update: "new update",
  started: "started",
  completed: "completed",
};

/** What a cleared property reads as; mirrors loopTriggerReasonNone. */
export const TRIGGER_REASON_NONE: Record<string, string> = { assignee: "No assignee", agent: "No agent", project: "No project" };

/** Reasons whose value is workspace vocabulary that has a translation (status and priority names). */
const TRANSLATED_VALUES = new Set(["status", "priority"]);

/** The translation key of a reason: "Triggered by {name} entering triage". */
export const triggerReasonTemplate = (code: string) => `Triggered by {name} ${TRIGGER_REASON_TEXTS[code]}`;

type TriggerRun = Pick<LoopRun, "trigger" | "triggerLabel" | "triggerReason" | "triggerValue" | "entityIdentifier">;

/**
 * The run's trigger, translated: "Manual run", "Scheduled run", "Triggered by DEV-24 entering triage"…
 * Runs stored before reason codes existed are read off their English label.
 */
export function runTriggerText(run: TriggerRun, t: Translate) {
  const name = run.entityIdentifier ?? "";
  if (run.trigger === "manual") return name ? t("Manual run on {identifier}").replace("{identifier}", () => name) : t("Manual run");
  if (run.trigger === "schedule") return t("Scheduled run");
  const reason = run.triggerReason && TRIGGER_REASON_TEXTS[run.triggerReason] ? { code: run.triggerReason, value: run.triggerValue ?? "", name } : parseTriggerLabel(run.triggerLabel, name);
  if (reason) {
    const none = TRIGGER_REASON_NONE[reason.code];
    const value = reason.value ? (TRANSLATED_VALUES.has(reason.code) ? t(reason.value) : reason.value) : none ? t(none) : "";
    return t(triggerReasonTemplate(reason.code)).replace("{name}", () => reason.name).replace("{value}", () => value);
  }
  if (run.triggerLabel && run.triggerLabel !== "Triggered run" && run.triggerLabel !== `Triggered by ${name}`) return run.triggerLabel;
  return name ? t("Triggered by {name}").replace("{name}", () => name) : t("Triggered run");
}

/** Reads the reason off a stored English label ("Triggered by DEV-24 status → In Progress"). */
export function parseTriggerLabel(label: string | undefined, entityName: string) {
  const prefix = `Triggered by ${entityName} `;
  if (!entityName || !label?.startsWith(prefix)) return undefined;
  const rest = label.slice(prefix.length);
  for (const [code, text] of Object.entries(TRIGGER_REASON_TEXTS)) {
    const match = new RegExp(`^${escapeRegExp(text).replace("\\{value\\}", "(.*)")}$`).exec(rest);
    if (!match) continue;
    const value = (match[1] ?? "").trim();
    return { code, value: value === TRIGGER_REASON_NONE[code] ? "" : value, name: entityName };
  }
  return undefined;
}

/** English output descriptions by kind; mirrors loopOutputLabels in api/cmd/server/loop_run_reliability.go. */
export const OUTPUT_LABELS: Record<string, string> = {
  statusUpdate: "a project or initiative status update",
  issue: "a new issue",
  comment: "a comment",
  document: "a document",
  change: "a change in Flow",
};

/**
 * Fixed run failure messages the server stores in run.error (loop_run_reliability.go, loop_runtime.go), as
 * translation keys; {limit}, {size} are read back from the stored text.
 */
export const FAILURE_MESSAGES = [
  "Too many loop runs are in progress on this server (limit {limit}); try again when one finishes",
  "Too many loop runs are in progress in this workspace (limit {limit}); try again when one finishes",
  "The run's conversation reached {size} KB, over its {limit} KB context budget. Narrow the instructions or the tool queries (filters, smaller limits).",
  "The run made more than {limit} tool calls, its budget. Narrow the instructions or raise FLOW_LOOP_MAX_TOOL_CALLS.",
  "The run exceeded its {limit} time limit",
  "Interrupted: the run lost its lease (the server stopped renewing it)",
  "Interrupted by server restart",
  "Loops are disabled for this workspace",
  "the loop is a draft; create it before running it",
  "the loop is paused",
  "Flow Agent is not configured on this server",
  "the loop owner is no longer in this workspace",
  "the loop owner cannot access this workspace",
];

/** What the run page shows under a run's failure reason: a translated message, model-written items, the raw detail. */
export type RunFailureView = { message?: string; items?: string[]; detail?: string };

type FailedRun = Pick<LoopRun, "failureReason" | "error" | "expectedOutputs" | "produced" | "toolCalls" | "summary">;

/** Builds the failure explanation from the run's structured fields; the stored English error becomes the "Details". */
export function describeRunFailure(run: FailedRun | LoopRunReply, t: Translate, locale: string): RunFailureView {
  const raw = run.error?.trim() ?? "";
  const full = run as FailedRun;
  const list = (items: string[]) => new Intl.ListFormat(locale, { type: "conjunction" }).format(items);
  const missing = () => {
    const expected = full.expectedOutputs?.filter((kind) => OUTPUT_LABELS[kind] && !full.produced?.[kind]) ?? [];
    // Older runs only have the English message: read the kinds back from it.
    const kinds = expected.length ? expected : Object.keys(OUTPUT_LABELS).filter((kind) => raw.includes(OUTPUT_LABELS[kind]));
    return kinds.map((kind) => t(OUTPUT_LABELS[kind]));
  };
  switch (run.failureReason) {
    case "no_output": {
      const outputs = missing();
      if (!outputs.length) break;
      let message = t("The loop's instructions call for {outputs}, but the run made none.").replace("{outputs}", list(outputs));
      if (full.summary?.status === "nothing_to_do" || raw.includes("nothing to do")) message += `${locale.startsWith("zh") ? "" : " "}${t("The agent reported there was nothing to do.")}`;
      return { message };
    }
    case "tool_error": {
      const outputs = missing();
      const failed = [...(full.toolCalls ?? [])].reverse().find((call) => (call.status === "error" || call.status === "blocked") && writeTool(call.name));
      if (!outputs.length || !failed) break;
      const tool = translateToolTitle(failed.label || failed.name, t);
      return { message: t("Expected {outputs}, but {tool} failed.").replace("{outputs}", list(outputs)).replace("{tool}", tool), detail: failed.error || undefined };
    }
    case "incomplete": {
      const items = full.summary?.notDone?.length ? full.summary.notDone : raw.split(/^The agent reported unfinished work:?\s*/)[1]?.split("; ").filter(Boolean);
      return items?.length ? { items } : {};
    }
  }
  if (!raw) return {};
  const known = fixedFailureMessage(raw, t);
  if (known) return { message: known };
  switch (run.failureReason) {
    case "cancelled":
    case "interrupted":
      return {};
    case "provider_timeout":
    case "empty_response":
    case "timeout":
    case "budget_exhausted":
    case "no_output":
    case "tool_error":
      // The reason label already says what happened: readable English stays visible for English readers.
      return !looksRaw(raw) && !locale.startsWith("zh") ? { message: raw } : { detail: raw };
    default:
      return describeAgentError(raw, t, locale, classifyAgentError(raw));
  }
}

/** A stored fixed message, translated with its numbers; undefined when the text is not one of FAILURE_MESSAGES. */
export function fixedFailureMessage(raw: string, t: Translate) {
  for (const template of FAILURE_MESSAGES) {
    const names: string[] = [];
    const pattern = new RegExp(`^${escapeRegExp(template).replace(/\\\{(\w+)\\\}/g, (_, name: string) => {
      names.push(name);
      return "(.+?)";
    })}$`, "i");
    const match = raw.match(pattern);
    if (!match) continue;
    return names.reduce((text, name, index) => text.replace(`{${name}}`, name === "limit" ? goDuration(match[index + 1]) : match[index + 1]), t(template));
  }
  return undefined;
}

/** Go duration text "15m0s" → "15m"; other values pass through. */
function goDuration(value: string) {
  return /^(\d+h)?(\d+m)?(\d+(\.\d+)?s)?$/.test(value) && value ? value.replace(/(\D)0s$/, "$1").replace(/(h)0m$/, "$1") : value;
}

/** Mirrors loopWriteToolName: whether a recorded tool call could change data. */
function writeTool(name: string) {
  return /^(save|create|delete|update|triage|merge|submit|resolve)_/.test(name.replace(/^mcp__flow\./, ""));
}

function escapeRegExp(text: string) {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
