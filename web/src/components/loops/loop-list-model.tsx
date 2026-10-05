import { CalendarSync, UserRound } from "lucide-react";
import { LoopsIcon } from "@/components/layout/sidebar";
import { UserAvatar } from "@/components/ui/user-avatar";
import { NUMBER_COMPARE_OPTIONS } from "@/components/filter/block-utils/number-block-filter-utils";
import type { DirectoryFilterGroup } from "@/components/workspace-directory/directory-menus";
import { matchesDateChoice, teamDateChoices } from "@/components/workspace-directory/team-directory-model";
import type { BootstrapData, Loop, User } from "@/types/flow";
import { loopOwner } from "./loop-data";

/* ── Filters: Linear's Loops filter menu has Owner, Last executed and Runs (30d). ── */

export type LoopListFilterField = "owner" | "lastRun" | "runs";
export type LoopRunsOperator = "eq" | "gt" | "lt" | "gte" | "lte";
export interface LoopListFilters {
  /** User ids; "me" is Linear's "Current user". */
  owner: string[];
  /** One of "never", a relative choice ("1", "7", …, days) or `date:from[/to]`. */
  lastRun: string[];
  /** One whole number. */
  runs: string[];
  operators: { owner?: "is" | "isNot"; lastRun?: "is" | "isNot"; runs?: LoopRunsOperator };
  conjunction: "and" | "or";
  advanced: boolean;
}

export const LOOP_FILTER_FIELDS: LoopListFilterField[] = ["owner", "lastRun", "runs"];
export const CURRENT_USER = "me";
export const NEVER_EXECUTED = "never";
export const RUNS_OPERATORS = NUMBER_COMPARE_OPTIONS.filter((option): option is { label: string; value: LoopRunsOperator } => option.value !== "empty");

export const emptyLoopFilters = (): LoopListFilters => ({ owner: [], lastRun: [], runs: [], operators: {}, conjunction: "and", advanced: false });

export function loopFiltersActive(filters: LoopListFilters) {
  return filters.advanced || LOOP_FILTER_FIELDS.some((field) => filters[field].length > 0);
}

export function loopOwnerId(loop: Loop) {
  return loop.ownerId ?? loop.creator?.id;
}

function matchesField(loop: Loop, field: LoopListFilterField, filters: LoopListFilters, viewerId: string, now: Date) {
  const values = filters[field];
  if (field === "owner") {
    const owner = loopOwnerId(loop);
    const match = values.some((value) => (value === CURRENT_USER ? viewerId : value) === owner);
    return filters.operators.owner === "isNot" ? !match : match;
  }
  if (field === "lastRun") {
    const value = values[0];
    const timestamp = loop.lastRunAt ? Date.parse(loop.lastRunAt) : NaN;
    const negate = filters.operators.lastRun === "isNot";
    if (value === NEVER_EXECUTED) return negate ? Number.isFinite(timestamp) : !Number.isFinite(timestamp);
    // Like the team directory's Created date: loops without a run match neither "after" nor "before".
    if (!Number.isFinite(timestamp)) return false;
    const match = matchesDateChoice(timestamp, value, now);
    return negate ? !match : match;
  }
  const target = Number(values[0]);
  const runs = loop.runCount30d ?? 0;
  switch (filters.operators.runs ?? "gte") {
    case "eq":
      return runs === target;
    case "gt":
      return runs > target;
    case "lt":
      return runs < target;
    case "lte":
      return runs <= target;
    default:
      return runs >= target;
  }
}

export function matchesLoopFilters(loop: Loop, filters: LoopListFilters, viewerId: string, now = new Date()) {
  const conditions = LOOP_FILTER_FIELDS.filter((field) => filters[field].length).map((field) => matchesField(loop, field, filters, viewerId, now));
  if (!conditions.length) return true;
  return filters.advanced && filters.conjunction === "or" ? conditions.some(Boolean) : conditions.every(Boolean);
}

export type Translate = (source: string) => string;
export const loopCount = (t: Translate, count: number) => t(count === 1 ? "{count} loop" : "{count} loops").replace("{count}", String(count));

const ownerAvatar = (user: User) => <UserAvatar avatarUrl={user.avatarUrl} className="workspace-directory-avatar" name={user.displayName || user.name} />;

/** The filter menu's properties with Linear's per-value loop counts. */
export function loopFilterGroups({ data, loops, t, onRuns }: { data: BootstrapData; loops: Loop[]; t: Translate; onRuns: (value: string) => void }): DirectoryFilterGroup[] {
  const owners = new Map<string, { user: User; count: number }>();
  for (const loop of loops) {
    const user = loopOwner(data, loop);
    if (!user) continue;
    const entry = owners.get(user.id) ?? { user, count: 0 };
    entry.count += 1;
    owners.set(user.id, entry);
  }
  const mine = owners.get(data.viewer.id)?.count ?? 0;
  const now = new Date();
  const meta = (count: number) => (count ? loopCount(t, count) : undefined);
  const executed = (loop: Loop) => (loop.lastRunAt ? Date.parse(loop.lastRunAt) : NaN);
  return [
    {
      id: "owner",
      label: t("Owner"),
      icon: <UserRound />,
      choices: [
        { id: CURRENT_USER, label: t("Current user"), meta: loopCount(t, mine), icon: <span className="loops-current-user-glyph"><UserRound size={14} strokeWidth={1.75} /></span>, keywords: "me" },
        ...[...owners.values()]
          .sort((left, right) => (left.user.displayName || left.user.name).localeCompare(right.user.displayName || right.user.name))
          .map(({ user, count }) => ({ id: user.id, label: user.displayName || user.name, meta: loopCount(t, count), icon: ownerAvatar(user), person: user })),
      ],
    },
    {
      id: "lastRun",
      label: t("Last executed"),
      icon: <CalendarSync />,
      selectionMode: "single",
      choices: [
        { id: NEVER_EXECUTED, label: t("Never executed"), meta: meta(loops.filter((loop) => !Number.isFinite(executed(loop))).length) },
        ...teamDateChoices.map((choice) => ({
          id: choice.id,
          label: t(choice.label),
          meta: meta(loops.filter((loop) => Number.isFinite(executed(loop)) && matchesDateChoice(executed(loop), choice.id, now)).length),
        })),
        { id: "custom", label: t("Custom date or timeframe…") },
      ],
    },
    {
      id: "runs",
      label: t("Runs (30d)"),
      icon: <LoopsIcon />,
      input: { placeholder: t("Enter runs (30d)…"), numeric: true, onSubmit: onRuns },
    },
  ];
}

/* ── Display options: Linear's Loops popover (Grouping, Ordering, two switches; no display properties). ── */

export type LoopGrouping = "none" | "team" | "trigger" | "owner";
export type LoopOrdering = "name" | "lastRun" | "runs" | "trigger" | "team" | "owner";
export interface LoopDisplaySettings {
  grouping: LoopGrouping;
  ordering: LoopOrdering;
  descending: boolean;
  showTeamLoops: boolean;
  showDisabledLoops: boolean;
}
export const LOOP_GROUPINGS: { id: LoopGrouping; label: string }[] = [
  { id: "none", label: "No grouping" },
  { id: "team", label: "Team" },
  { id: "trigger", label: "Trigger" },
  { id: "owner", label: "Owner" },
];
export const LOOP_ORDERINGS: { id: LoopOrdering; label: string }[] = [
  { id: "name", label: "Name" },
  { id: "lastRun", label: "Last executed" },
  { id: "runs", label: "Runs" },
  { id: "trigger", label: "Trigger" },
  { id: "team", label: "Team" },
  { id: "owner", label: "Owner" },
];
export const DEFAULT_LOOP_DISPLAY: LoopDisplaySettings = { grouping: "team", ordering: "name", descending: false, showTeamLoops: true, showDisabledLoops: true };
/** Runs and last executed read best newest/most first, like Linear's column headers. */
export const defaultDescending = (ordering: LoopOrdering) => ordering === "runs" || ordering === "lastRun";

export function loopDisplayKey(data: Pick<BootstrapData, "workspace" | "viewer">) {
  return `flow:loops:display:${data.workspace.id}:${data.viewer.id}`;
}

export function readLoopDisplay(key: string): LoopDisplaySettings {
  try {
    const stored = JSON.parse(localStorage.getItem(key) ?? "null") as Partial<LoopDisplaySettings> | null;
    if (!stored || typeof stored !== "object") return DEFAULT_LOOP_DISPLAY;
    return {
      grouping: LOOP_GROUPINGS.some((item) => item.id === stored.grouping) ? stored.grouping! : DEFAULT_LOOP_DISPLAY.grouping,
      ordering: LOOP_ORDERINGS.some((item) => item.id === stored.ordering) ? stored.ordering! : DEFAULT_LOOP_DISPLAY.ordering,
      descending: stored.descending === true,
      showTeamLoops: stored.showTeamLoops !== false,
      showDisabledLoops: stored.showDisabledLoops !== false,
    };
  } catch {
    return DEFAULT_LOOP_DISPLAY;
  }
}

export function writeLoopDisplay(key: string, settings: LoopDisplaySettings) {
  try {
    localStorage.setItem(key, JSON.stringify(settings));
  } catch {
    /* storage unavailable */
  }
}
