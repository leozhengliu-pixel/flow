import type { BootstrapData, SLARule } from "@/types/flow";

/** What an automation rule can look at on an issue; each one is a condition in the "When" row. */
export type SlaConditionKind = "priority" | "team" | "status" | "label" | "project";
export type SlaCondition = { kind: SlaConditionKind; value: string };
export type SlaAction = "add" | "remove";
export type SlaUnit = "minutes" | "hours" | "days" | "weeks";

export const SLA_CONDITION_KINDS: SlaConditionKind[] = ["priority", "team", "status", "label", "project"];
/** Priorities in the order the rule list shows them: Urgent first, "No priority" last. */
export const SLA_PRIORITIES = [1, 2, 3, 4, 0] as const;
export const SLA_STATUS_TYPES = ["backlog", "unstarted", "started", "completed", "canceled"] as const;

export type SlaDraft = {
  conditions: SlaCondition[];
  action: SlaAction;
  amount: number;
  unit: SlaUnit;
  businessHours: boolean;
  enabled: boolean;
};

/** Minutes in one unit: a business day is the eight working hours the deadline maths counts. */
export function minutesPerUnit(unit: SlaUnit, businessHours: boolean): number {
  if (unit === "minutes") return 1;
  if (unit === "hours") return 60;
  if (unit === "days") return businessHours ? 480 : 1440;
  return businessHours ? 2400 : 10080;
}

/**
 * The amount and unit a duration reads best in: whole weeks, then whole days beyond the first,
 * then whole hours ("24 hours", not "1 day"), then minutes.
 */
export function splitDuration(minutes: number, businessHours: boolean): { amount: number; unit: SlaUnit } {
  const week = minutesPerUnit("weeks", businessHours);
  const day = minutesPerUnit("days", businessHours);
  if (minutes > 0 && minutes % week === 0) return { amount: minutes / week, unit: "weeks" };
  if (minutes > day && minutes % day === 0) return { amount: minutes / day, unit: "days" };
  if (minutes > 0 && minutes % 60 === 0) return { amount: minutes / 60, unit: "hours" };
  return { amount: minutes, unit: "minutes" };
}

const DURATION_ONE: Record<SlaUnit, [calendar: string, business: string]> = {
  minutes: ["1 minute", "1 minute"],
  hours: ["1 hour", "1 business hour"],
  days: ["1 day", "1 business day"],
  weeks: ["1 week", "1 business week"],
};
const DURATION_MANY: Record<SlaUnit, [calendar: string, business: string]> = {
  minutes: ["{count} minutes", "{count} minutes"],
  hours: ["{count} hours", "{count} business hours"],
  days: ["{count} days", "{count} business days"],
  weeks: ["{count} weeks", "{count} business weeks"],
};

/** "24 hours", "1 week", "3 business days". Every phrase goes through the translator whole. */
export function formatSlaDuration(minutes: number, businessHours: boolean, t: (value: string) => string): string {
  const { amount, unit } = splitDuration(minutes, businessHours);
  const index = businessHours ? 1 : 0;
  if (amount === 1) return t(DURATION_ONE[unit][index]);
  return t(DURATION_MANY[unit][index]).replace("{count}", () => new Intl.NumberFormat().format(amount));
}

/** Legacy rules (before actions existed) have none and add an SLA. */
export function ruleAction(rule: Pick<SLARule, "action">): SlaAction {
  return rule.action === "remove" ? "remove" : "add";
}

/** Whether SLAs are switched on. A workspace that never chose keeps the rules it already has running. */
export function slaFeatureEnabled(data: Pick<BootstrapData, "settings" | "slaRules">): boolean {
  const stored = (data.settings?.sla as { enabled?: unknown } | undefined)?.enabled;
  if (typeof stored === "boolean") return stored;
  return (data.slaRules ?? []).length > 0;
}

/**
 * The conditions a rule applies, in the order the "When" row shows them. A rule written by the old
 * form lists every team it was created with: that means "any team", so it is not a condition.
 */
export function ruleConditions(rule: SLARule, data: Pick<BootstrapData, "teams">): SlaCondition[] {
  const conditions: SlaCondition[] = [];
  const filters = rule.filters ?? {};
  if (filters.priority !== undefined && filters.priority !== "") conditions.push({ kind: "priority", value: String(filters.priority) });
  const teamIds = rule.teamIds ?? [];
  const everyTeam = teamIds.length > 0 && data.teams.every((team) => teamIds.includes(team.id));
  if (teamIds.length > 0 && !everyTeam) conditions.push({ kind: "team", value: teamIds.join(",") });
  for (const kind of ["status", "label", "project"] as const) {
    const value = filters[kind];
    if (value !== undefined && value !== "") conditions.push({ kind, value: String(value) });
  }
  return conditions;
}

/** The filters and team ids a list of conditions stands for; the editor keeps one condition per kind. */
export function conditionsToRule(conditions: SlaCondition[]): { filters: Record<string, unknown>; teamIds: string[] } {
  const filters: Record<string, unknown> = {};
  let teamIds: string[] = [];
  for (const { kind, value } of conditions) {
    if (kind === "team") teamIds = value.split(",").filter(Boolean);
    else if (kind === "priority") filters.priority = Number(value);
    else filters[kind] = value;
  }
  return { filters, teamIds };
}

export function draftFromRule(rule: SLARule, data: Pick<BootstrapData, "teams">): SlaDraft {
  const businessHours = Boolean(rule.businessHours);
  const { amount, unit } = splitDuration(rule.targetMinutes > 0 ? rule.targetMinutes : 1440, businessHours);
  return { conditions: ruleConditions(rule, data), action: ruleAction(rule), amount, unit, businessHours, enabled: rule.enabled };
}

export function newDraft(): SlaDraft {
  return { conditions: [{ kind: "priority", value: "1" }], action: "add", amount: 24, unit: "hours", businessHours: false, enabled: true };
}

export function draftMinutes(draft: SlaDraft): number {
  return draft.amount * minutesPerUnit(draft.unit, draft.businessHours);
}

/** Whether the amount is a whole number of 1 or more that fits the API's range. */
export function validAmount(value: number): boolean {
  return Number.isInteger(value) && value >= 1 && value <= 99999;
}

const PRIORITY_NAMES: Record<string, string> = { "0": "No priority", "1": "Urgent", "2": "High", "3": "Medium", "4": "Low" };
export function priorityName(value: string | number): string {
  return PRIORITY_NAMES[String(value)] ?? "No priority";
}

export const STATUS_TYPE_NAMES: Record<string, string> = {
  backlog: "Backlog",
  unstarted: "Unstarted",
  started: "Started",
  completed: "Completed",
  canceled: "Canceled",
};

/** The name stored with a rule: the rule list never shows it, issue SLA chips use it. */
export function ruleNameFor(conditions: SlaCondition[], data: Pick<BootstrapData, "teams" | "labels" | "projects">): string {
  const parts = conditions.map(({ kind, value }) => {
    if (kind === "priority") return `Priority is ${priorityName(value)}`;
    if (kind === "status") return `Status is ${STATUS_TYPE_NAMES[value] ?? value}`;
    if (kind === "team") {
      const names = value.split(",").map((id) => data.teams.find((team) => team.id === id)?.name ?? id);
      return `Team is ${names.join(", ")}`;
    }
    if (kind === "label") return `Label is ${data.labels.find((label) => label.id === value)?.name ?? value}`;
    return `Project is ${data.projects.find((project) => project.id === value)?.name ?? value}`;
  });
  return (parts.join(" and ") || "SLA rule").slice(0, 120);
}
