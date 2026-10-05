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
  "initiatives", "projects", "loops", "views", "members", "releases",
  "teams", "dashboards", "customers",
];
const legacyWorkspaceOrder: SidebarEntry[] = [
  "members", "initiatives", "projects", "teams", "views",
  "dashboards", "releases", "loops", "customers",
];
// Like Linear: the Workspace section shows Initiatives, Projects, Loops and
// Views; Members, Teams, Releases (and Dashboards, Customers) live under More.
const defaultPreferences: SidebarPreferences = {
  inbox: "always", reviews: "always", myIssues: "always", pulse: "always",
  drafts: "always", agent: "always", initiatives: "always",
  projects: "always", documents: "always", views: "always",
  members: "never", customers: "never", teams: "never",
  releases: "never", loops: "always",
  dashboards: "never",
};
// Defaults before the Linear-parity change. Builds up to then saved the whole
// preference object, so values equal to these were never explicit choices.
const legacyDefaultPreferences: Partial<SidebarPreferences> = { members: "always", teams: "always", releases: "always" };

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
  useEffect(() => persist(sidebarStorageKey(PREFERENCE_OVERRIDES_KEY, userId), preferenceOverrides(preferences)), [preferences, userId]);
  useEffect(() => {
    // Saved only once rearranged, so later default changes reach everyone else.
    const key = sidebarStorageKey("flow.sidebar.order", userId)
    const isDefault = sameEntries(order.personal, defaultPersonalOrder) && sameEntries(order.workspace, defaultWorkspaceOrder)
    if (isDefault) { try { localStorage.removeItem(key) } catch { /* ignore */ } } else persist(key, order)
  }, [order, userId]);
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
  return { ...defaultPreferences, ...readPreferenceOverrides(userId) }
}
/** Only the entries the user changed; everything else follows the defaults. */
function readPreferenceOverrides(userId?: string): Partial<SidebarPreferences> {
  try {
    const stored = readStored(PREFERENCE_OVERRIDES_KEY, userId)
    if (stored !== null) return JSON.parse(stored) as Partial<SidebarPreferences>
    const legacy = JSON.parse(readStored("flow.sidebar.preferences", userId) ?? "{}") as Partial<SidebarPreferences>
    const legacyDefaults: Partial<SidebarPreferences> = { ...defaultPreferences, ...legacyDefaultPreferences }
    return Object.fromEntries(Object.entries(legacy).filter(([key, value]) => legacyDefaults[key as SidebarEntry] !== value)) as Partial<SidebarPreferences>
  } catch { return {} }
}
function preferenceOverrides(preferences: SidebarPreferences): Partial<SidebarPreferences> {
  return Object.fromEntries(Object.entries(preferences).filter(([key, value]) => defaultPreferences[key as SidebarEntry] !== value)) as Partial<SidebarPreferences>
}
const PREFERENCE_OVERRIDES_KEY = "flow.sidebar.preference-overrides"
function readOrder(userId?: string): SidebarOrder {
  try {
    const stored = JSON.parse(readStored("flow.sidebar.order", userId) ?? "{}") as Partial<SidebarOrder>;
    return {
      personal: normalizeOrder(stored.personal, defaultPersonalOrder),
      // An order saved before the Linear-parity change that still equals the
      // old default was never customised: use the new default.
      workspace: normalizeOrder(sameOrder(stored.workspace, legacyWorkspaceOrder) ? undefined : stored.workspace, defaultWorkspaceOrder),
    };
  } catch {
    return { personal: [...defaultPersonalOrder], workspace: [...defaultWorkspaceOrder] };
  }
}
/**
 * True when stored keeps the reference's relative order for the core entries.
 * Older builds appended entries added later (Loops, Dashboards, Customers) at
 * the end and saved the result, so those are ignored here.
 */
function sameOrder(stored: SidebarEntry[] | undefined, reference: SidebarEntry[]) {
  if (!stored?.length) return false
  const appendedLater = new Set<SidebarEntry>(["loops", "dashboards", "customers"])
  const core = stored.filter(entry => !appendedLater.has(entry))
  if (core.some(entry => !reference.includes(entry))) return false
  return core.every((entry, index) => index === 0 || reference.indexOf(core[index - 1]) < reference.indexOf(entry))
}
function sameEntries(a: SidebarEntry[], b: SidebarEntry[]) {
  return a.length === b.length && a.every((entry, index) => entry === b[index])
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
