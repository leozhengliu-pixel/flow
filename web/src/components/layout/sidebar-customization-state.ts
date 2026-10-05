import { useEffect, useState, type SetStateAction } from "react";

export type SidebarEntry =
  | "inbox"
  | "reviews"
  | "myIssues"
  | "pulse"
  | "drafts"
  | "agent"
  | "initiatives"
  | "projects"
  | "documents"
  | "views"
  | "dashboards"
  | "members"
  | "customers"
  | "teams"
  | "releases"
  | "loops";
export type SidebarVisibility = "always" | "badged" | "never";
export type SidebarBadgeStyle = "count" | "dot";
export type SidebarPreferences = Record<SidebarEntry, SidebarVisibility>;
export type SidebarGroup = "personal" | "workspace";
export type SidebarOrder = Record<SidebarGroup, SidebarEntry[]>;

// customers → customer-requests; Asks is a separate `asks` flag, not an entry.
const SIDEBAR_ENTRY_FEATURES: Partial<Record<SidebarEntry, string>> = {
  initiatives: "initiatives",
  customers: "customer-requests",
  releases: "releases",
  loops: "loops",
  dashboards: "dashboards",
  pulse: "pulse",
  agent: "ai",
};

export function workspaceFeatureEnabled(
  flags: Record<string, boolean> | undefined,
  feature: string,
) {
  const value =
    feature === "ai" ? flags?.["ai-agent"] ?? flags?.ai : flags?.[feature];
  return value !== false;
}

export function sidebarEntryAvailable(
  entry: SidebarEntry,
  flags: Record<string, boolean> | undefined,
) {
  const feature = SIDEBAR_ENTRY_FEATURES[entry];
  return !feature || workspaceFeatureEnabled(flags, feature);
}

const defaultPersonalOrder: SidebarEntry[] = [
  "inbox", "reviews", "myIssues", "pulse", "drafts", "agent",
];
const defaultWorkspaceOrder: SidebarEntry[] = [
  "members", "initiatives", "projects", "teams", "views",
  "dashboards", "releases", "loops", "customers",
];
const defaultPreferences: SidebarPreferences = {
  inbox: "always", reviews: "always", myIssues: "always", pulse: "always",
  drafts: "always", agent: "always", initiatives: "always",
  projects: "always", documents: "always", views: "always",
  members: "always", customers: "never", teams: "always",
  releases: "always", loops: "always",
  // Like Linear, Dashboards is its own page reached from the More menu.
  dashboards: "never",
};

/**
 * Sidebar customisation is per user (like Linear): stored under keys scoped to
 * the signed-in user, so one person's choices never change another's sidebar
 * in a shared browser. The first user to load after an upgrade adopts the old
 * unscoped values, which are then removed.
 */
export function sidebarStorageKey(base: string, userId?: string) {
  return userId ? `${base}:${userId}` : base
}
function readStored(base: string, userId?: string): string | null {
  const key = sidebarStorageKey(base, userId)
  const value = localStorage.getItem(key)
  if (value !== null || !userId) return value
  const legacy = localStorage.getItem(base)
  if (legacy !== null) {
    localStorage.setItem(key, legacy)
    localStorage.removeItem(base)
  }
  return legacy
}

export function useSidebarCustomizationState(userId?: string) {
  const [state, setState] = useState(() => ({ userId, preferences: readPreferences(userId), order: readOrder(userId), badgeStyle: readBadgeStyle(userId) }))
  // Another user signed in: load their own choices.
  const current = state.userId === userId ? state : { userId, preferences: readPreferences(userId), order: readOrder(userId), badgeStyle: readBadgeStyle(userId) }
  if (current !== state) setState(current)
  const { preferences, order, badgeStyle } = current
  useEffect(() => persist(sidebarStorageKey("flow.sidebar.preferences", userId), preferences), [preferences, userId]);
  useEffect(() => persist(sidebarStorageKey("flow.sidebar.order", userId), order), [order, userId]);
  useEffect(() => persist(sidebarStorageKey("flow.sidebar.badge-style", userId), badgeStyle), [badgeStyle, userId]);
  const update = <K extends "preferences" | "order" | "badgeStyle">(key: K, value: SetStateAction<(typeof current)[K]>) =>
    setState(previous => ({ ...previous, [key]: typeof value === "function" ? (value as (old: (typeof current)[K]) => (typeof current)[K])(previous[key]) : value }))
  const setPreferences = (value: SetStateAction<SidebarPreferences>) => update("preferences", value)
  const setBadgeStyle = (value: SetStateAction<SidebarBadgeStyle>) => update("badgeStyle", value)
  const reorder = (group: SidebarGroup, active: SidebarEntry, target: SidebarEntry) =>
    update("order", (value: SidebarOrder) => ({
      ...value,
      [group]: reorderEntries(value[group], active, target),
    }));
  return { badgeStyle, order, preferences, reorder, setBadgeStyle, setPreferences };
}

function readBadgeStyle(userId?: string): SidebarBadgeStyle {
  // Stored JSON-encoded ('"dot"'); older builds wrote the bare value.
  try { const value = readStored("flow.sidebar.badge-style", userId); return value === "dot" || value === '"dot"' ? "dot" : "count"; }
  catch { return "count"; }
}
function readPreferences(userId?: string): SidebarPreferences {
  try {
    return { ...defaultPreferences, ...JSON.parse(readStored("flow.sidebar.preferences", userId) ?? "{}") };
  } catch { return defaultPreferences; }
}
function readOrder(userId?: string): SidebarOrder {
  try {
    const stored = JSON.parse(readStored("flow.sidebar.order", userId) ?? "{}") as Partial<SidebarOrder>;
    return {
      personal: normalizeOrder(stored.personal, defaultPersonalOrder),
      workspace: normalizeOrder(stored.workspace, defaultWorkspaceOrder),
    };
  } catch {
    return { personal: [...defaultPersonalOrder], workspace: [...defaultWorkspaceOrder] };
  }
}
function normalizeOrder(stored: SidebarEntry[] | undefined, defaults: SidebarEntry[]) {
  const allowed = new Set(defaults);
  const valid = Array.isArray(stored)
    ? stored.filter((entry, index) => allowed.has(entry) && stored.indexOf(entry) === index)
    : [];
  const merged = [...valid, ...defaults.filter((entry) => !valid.includes(entry))];
  return defaults === defaultPersonalOrder
    ? ["inbox", "reviews", ...merged.filter((entry) => entry !== "inbox" && entry !== "reviews")] as SidebarEntry[]
    : merged;
}
function reorderEntries(entries: SidebarEntry[], active: SidebarEntry, target: SidebarEntry) {
  const sourceIndex = entries.indexOf(active), targetIndex = entries.indexOf(target);
  if (sourceIndex < 0 || targetIndex < 0 || sourceIndex === targetIndex) return entries;
  const reordered = [...entries];
  reordered.splice(sourceIndex, 1);
  reordered.splice(targetIndex, 0, active);
  return reordered;
}
function persist(key: string, value: unknown) {
  try { localStorage.setItem(key, JSON.stringify(value)); }
  catch { /* Preferences remain in memory. */ }
}
