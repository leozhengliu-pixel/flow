import { useEffect, useState } from "react";

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

const defaultPersonalOrder: SidebarEntry[] = [
  "inbox", "reviews", "myIssues", "pulse", "drafts", "agent",
];
const defaultWorkspaceOrder: SidebarEntry[] = [
  "members", "initiatives", "projects", "teams", "views",
  "releases", "loops", "customers",
];
const defaultPreferences: SidebarPreferences = {
  inbox: "always", reviews: "always", myIssues: "always", pulse: "always",
  drafts: "always", agent: "always", initiatives: "always",
  projects: "always", documents: "always", views: "always",
  members: "always", customers: "never", teams: "always",
  releases: "always", loops: "always",
};

export function useSidebarCustomizationState() {
  const [preferences, setPreferences] = useState(readPreferences);
  const [order, setOrder] = useState(readOrder);
  const [badgeStyle, setBadgeStyle] = useState<SidebarBadgeStyle>(readBadgeStyle);
  useEffect(() => persist("flow.sidebar.preferences", preferences), [preferences]);
  useEffect(() => persist("flow.sidebar.order", order), [order]);
  useEffect(() => persist("flow.sidebar.badge-style", badgeStyle), [badgeStyle]);
  const reorder = (group: SidebarGroup, active: SidebarEntry, target: SidebarEntry) =>
    setOrder((current) => ({
      ...current,
      [group]: reorderEntries(current[group], active, target),
    }));
  return { badgeStyle, order, preferences, reorder, setBadgeStyle, setPreferences };
}

function readBadgeStyle(): SidebarBadgeStyle {
  try { return localStorage.getItem("flow.sidebar.badge-style") === "dot" ? "dot" : "count"; }
  catch { return "count"; }
}
function readPreferences(): SidebarPreferences {
  try {
    return { ...defaultPreferences, ...JSON.parse(localStorage.getItem("flow.sidebar.preferences") ?? "{}") };
  } catch { return defaultPreferences; }
}
function readOrder(): SidebarOrder {
  try {
    const stored = JSON.parse(localStorage.getItem("flow.sidebar.order") ?? "{}") as Partial<SidebarOrder>;
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
