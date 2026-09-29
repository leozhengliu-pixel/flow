import type { BootstrapData, Loop, LoopTriggerType, Team } from "@/types/flow";

/** Issue events a loop can react to (see docs/verification/loops-api-contract.md). */
export type LoopIssueEvent =
  | "created"
  | "updated"
  | "triage"
  | "status"
  | "priority"
  | "assignee"
  | "agent"
  | "project"
  | "team"
  | "labels"
  | "comment"
  | "customerRequest";
export type LoopEntityEvent = "created" | "updated" | "status" | "update" | "started" | "completed";
export type LoopFilterField = "status" | "priority" | "assignee" | "label" | "project" | "team" | "creator";
export type LoopFilter = { field: LoopFilterField; operator: "is" | "isNot"; value: string | null };
export type LoopWeekday = "sun" | "mon" | "tue" | "wed" | "thu" | "fri" | "sat";
export type LoopScheduleUnit = "hour" | "day" | "week" | "month";

export const WEEKDAYS: { id: LoopWeekday; short: string }[] = [
  { id: "sun", short: "Su" },
  { id: "mon", short: "Mo" },
  { id: "tue", short: "Tu" },
  { id: "wed", short: "We" },
  { id: "thu", short: "Th" },
  { id: "fri", short: "Fr" },
  { id: "sat", short: "Sa" },
];

export const PRIORITY_NAMES = ["No priority", "Urgent", "High", "Medium", "Low"];

/** Events offered for each non-issue trigger type. */
export const ENTITY_EVENTS: Record<Exclude<LoopTriggerType, "schedule" | "issue">, { id: LoopEntityEvent; label: string }[]> = {
  project: [
    { id: "created", label: "Created" },
    { id: "updated", label: "Property updated" },
    { id: "status", label: "Status changed" },
    { id: "update", label: "New project update" },
  ],
  initiative: [
    { id: "created", label: "Created" },
    { id: "updated", label: "Property updated" },
    { id: "status", label: "Status changed" },
    { id: "update", label: "New initiative update" },
  ],
  release: [
    { id: "created", label: "Created" },
    { id: "updated", label: "Property updated" },
    { id: "status", label: "Status changed" },
  ],
  team: [
    { id: "created", label: "Created" },
    { id: "updated", label: "Property updated" },
  ],
  cycle: [
    { id: "created", label: "Created" },
    { id: "updated", label: "Property updated" },
    { id: "started", label: "Started" },
    { id: "completed", label: "Completed" },
  ],
};

export const ENTITY_NAMES: Record<LoopTriggerType, string> = {
  schedule: "Schedule",
  issue: "Issue",
  project: "Project",
  initiative: "Initiative",
  release: "Release",
  team: "Team",
  cycle: "Cycle",
};

export function configString(config: Record<string, unknown> | undefined, key: string) {
  const value = config?.[key];
  return typeof value === "string" ? value : undefined;
}

export function configStrings(config: Record<string, unknown> | undefined, key: string) {
  const value = config?.[key];
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
}

/** The issue event of a loop, mapping legacy `action` configs like the server does. */
export function loopEvent(config: Record<string, unknown> | undefined): string {
  const event = configString(config, "event");
  if (event) return event;
  const action = configString(config, "action");
  if (action) return action === "created" ? "created" : "updated";
  return "created";
}

export function loopFilters(config: Record<string, unknown> | undefined): LoopFilter[] {
  const filters = config?.filters;
  if (Array.isArray(filters))
    return filters.filter((item): item is LoopFilter => Boolean(item) && typeof item === "object" && typeof (item as LoopFilter).field === "string");
  // Legacy single filter.
  if (config?.filter && typeof config.filterField === "string")
    return [{ field: config.filterField as LoopFilterField, operator: config.filterOperator === "isNot" ? "isNot" : "is", value: typeof config.filterValue === "string" ? config.filterValue : null }];
  return [];
}

/** `value` of a property event: undefined/"any" = any value, null = none. */
export function eventValue(config: Record<string, unknown> | undefined): string | null | undefined {
  if (!config || !("value" in config)) return undefined;
  const value = config.value;
  if (value === null) return null;
  if (typeof value === "string") return value === "any" ? undefined : value;
  if (typeof value === "number") return String(value);
  return undefined;
}

export function loopTeamId(loop: Pick<Loop, "level" | "teamId" | "triggerConfig">) {
  if (loop.level !== "team") return undefined;
  return loop.teamId ?? configStrings(loop.triggerConfig, "teamIds")[0];
}

export function loopTeam(data: Pick<BootstrapData, "teams">, loop: Pick<Loop, "level" | "teamId" | "triggerConfig">): Team | undefined {
  const id = loopTeamId(loop);
  return id ? data.teams.find((team) => team.id === id) : undefined;
}

export function isLoopDraft(loop: Pick<Loop, "status">) {
  return loop.status === "draft";
}

/** "07:00" → "7AM", "13:30" → "1:30PM" (Linear's 12-hour schedule labels). */
export function formatTime12(value: string) {
  const match = /^(\d{1,2}):(\d{2})/.exec(value);
  if (!match) return value;
  const hour = Number(match[1]);
  const minute = match[2];
  const suffix = hour < 12 ? "AM" : "PM";
  const display = hour % 12 === 0 ? 12 : hour % 12;
  return minute === "00" ? `${display}${suffix}` : `${display}:${minute}${suffix}`;
}

/** Half-hour steps across the day, labelled like Linear ("7AM", "7:30AM"). */
export function scheduleTimeOptions() {
  return Array.from({ length: 48 }, (_, index) => {
    const value = `${String(Math.floor(index / 2)).padStart(2, "0")}:${index % 2 ? "30" : "00"}`;
    return { value, label: formatTime12(value) };
  });
}

/** "2026-09-29" → "09/29/2026". */
export function formatScheduleDate(value: string) {
  const match = /^(\d{4})-(\d{2})-(\d{2})/.exec(value);
  return match ? `${match[2]}/${match[3]}/${match[1]}` : value;
}

export function todayISO(now = new Date()) {
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;
}

export function defaultScheduleConfig(now = new Date()): Record<string, unknown> {
  return { startDate: todayISO(now), interval: 1, unit: "day", time: "07:00" };
}

export function scheduleStart(config: Record<string, unknown> | undefined) {
  return configString(config, "startDate") ?? configString(config, "starting") ?? todayISO();
}

/** Short trigger label for lists (Linear shows "Triage", "Weekly", "Issue created"…). */
type Translate = (source: string) => string;
const same: Translate = (source) => source;

export function triggerSummary(loop: Pick<Loop, "triggerType" | "triggerConfig">, t: Translate = same): string {
  const config = loop.triggerConfig;
  if (loop.triggerType === "schedule") {
    const unit = configString(config, "unit") ?? "day";
    const interval = Number(config?.interval ?? 1) || 1;
    const names: Record<string, [string, string]> = { hour: ["Hourly", "hours"], day: ["Daily", "days"], week: ["Weekly", "weeks"], month: ["Monthly", "months"] };
    const [single, plural] = names[unit] ?? names.day;
    return interval === 1 ? t(single) : t(`Every {count} ${plural}`).replace("{count}", String(interval));
  }
  const event = loopEvent(config);
  if (loop.triggerType === "issue") {
    const labels: Record<string, string> = {
      created: "Issue created",
      updated: "Issue updated",
      triage: "Triage",
      status: "Status change",
      priority: "Priority change",
      assignee: "Assignee change",
      agent: "Agent change",
      project: "Project change",
      team: "Team change",
      labels: "Label added",
      comment: "New comment",
      customerRequest: "Customer request",
    };
    return t(labels[event] ?? "Issue change");
  }
  const entity = ENTITY_NAMES[loop.triggerType];
  const found = ENTITY_EVENTS[loop.triggerType as keyof typeof ENTITY_EVENTS]?.find((item) => item.id === event);
  if (!found) return t(entity);
  if (event === "update") return t(found.label);
  const verbs: Record<string, string> = { created: "created", updated: "updated", status: "status changed", started: "started", completed: "completed" };
  return t(`${entity} ${verbs[event] ?? found.label.toLowerCase()}`);
}

/** Linear-style relative time: "just now", "5m ago", "3h ago", "Yesterday", "Sep 12". */
export function relativeTime(value: string | undefined, now = Date.now(), t: Translate = same) {
  if (!value) return "";
  const time = Date.parse(value);
  if (!Number.isFinite(time)) return "";
  const seconds = Math.max(0, Math.round((now - time) / 1000));
  const count = (template: string, amount: number) => t(template).replace("{count}", String(amount));
  if (seconds < 60) return t("just now");
  if (seconds < 3600) return count("{count}m ago", Math.floor(seconds / 60));
  if (seconds < 86400) return count("{count}h ago", Math.floor(seconds / 3600));
  const days = Math.floor(seconds / 86400);
  if (days === 1) return t("Yesterday");
  if (days < 7) return count("{count}d ago", days);
  const date = new Date(time);
  const sameYear = date.getFullYear() === new Date(now).getFullYear();
  return t(date.toLocaleDateString("en-US", sameYear ? { month: "short", day: "numeric" } : { month: "short", day: "numeric", year: "numeric" }));
}

/** "Today", "Yesterday" or "Sep 12" for run lists. */
export function dayLabel(value: string, now = new Date()) {
  const date = new Date(value);
  const start = (item: Date) => new Date(item.getFullYear(), item.getMonth(), item.getDate()).getTime();
  const diff = Math.round((start(now) - start(date)) / 86400000);
  if (diff === 0) return "Today";
  if (diff === 1) return "Yesterday";
  return date.toLocaleDateString("en-US", date.getFullYear() === now.getFullYear() ? { month: "short", day: "numeric" } : { month: "short", day: "numeric", year: "numeric" });
}

/** "1s", "42s", "3m 5s". */
export function runDuration(startedAt: string, finishedAt?: string, now = Date.now()) {
  const end = finishedAt ? Date.parse(finishedAt) : now;
  const seconds = Math.max(0, Math.round((end - Date.parse(startedAt)) / 1000));
  if (!Number.isFinite(seconds)) return "";
  if (seconds < 60) return `${Math.max(1, seconds)}s`;
  const minutes = Math.floor(seconds / 60);
  return seconds % 60 ? `${minutes}m ${seconds % 60}s` : `${minutes}m`;
}

export function runTriggerLabel(run: { trigger: "manual" | "schedule" | "event"; triggerLabel?: string; entityIdentifier?: string }) {
  if (run.triggerLabel) return run.triggerLabel;
  if (run.trigger === "manual") return run.entityIdentifier ? `Manual run on ${run.entityIdentifier}` : "Manual run";
  if (run.trigger === "schedule") return "Scheduled run";
  return run.entityIdentifier ? `Triggered by ${run.entityIdentifier}` : "Triggered run";
}

/** Instruction placeholder follows the trigger like Linear. */
export function instructionsPlaceholder(triggerType: LoopTriggerType) {
  if (triggerType === "schedule")
    return "For example, summarize this week's progress, highlight blocked issues, and post a team update…";
  const entity = ENTITY_NAMES[triggerType].toLowerCase();
  return `For example, review the ${entity}'s changes, check for blockers, and suggest next steps…`;
}
