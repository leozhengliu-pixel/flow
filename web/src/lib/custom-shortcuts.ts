/**
 * Persist user-customized keyboard shortcuts for AccountShortcutsSettingsPage (LS-0008).
 * Local-only prefs (REST-safe); conflict detection scans the same registry list.
 */
import { useSyncExternalStore } from "react";

export type CustomShortcut = {
  actionId: string;
  /** Display parts, e.g. ["⌘", "K"] or ["G", "then", "I"]. */
  keys: string[];
};

export type ShortcutAction = {
  id: string;
  label: string;
  group: string;
  defaultKeys: string[];
  keywords?: string;
};

const STORAGE_KEY = "flow:custom-shortcuts";

let cache: CustomShortcut[] | null = null;
const listeners = new Set<() => void>();

function read(): CustomShortcut[] {
  if (cache) return cache;
  if (typeof localStorage === "undefined") {
    cache = [];
    return cache;
  }
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    const parsed = raw ? (JSON.parse(raw) as CustomShortcut[]) : [];
    cache = Array.isArray(parsed) ? parsed.filter((item) => item?.actionId && Array.isArray(item.keys)) : [];
  } catch {
    cache = [];
  }
  return cache;
}

function write(next: CustomShortcut[]) {
  cache = next;
  if (typeof localStorage !== "undefined") {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
    } catch {
      /* best-effort */
    }
  }
  for (const listener of listeners) listener();
}

export function listCustomShortcuts() {
  return read();
}

export function getCustomShortcut(actionId: string) {
  return read().find((item) => item.actionId === actionId);
}

export function setCustomShortcut(actionId: string, keys: string[]) {
  const without = read().filter((item) => item.actionId !== actionId);
  write([...without, { actionId, keys }]);
}

export function resetCustomShortcut(actionId: string) {
  write(read().filter((item) => item.actionId !== actionId));
}

export function clearCustomShortcuts() {
  write([]);
}

export function subscribeCustomShortcuts(listener: () => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener) };
}

export function useCustomShortcuts() {
  return useSyncExternalStore(subscribeCustomShortcuts, listCustomShortcuts, () => []);
}

export function resolveShortcutKeys(
  action: Pick<ShortcutAction, "id" | "defaultKeys">,
  customs = listCustomShortcuts(),
) {
  return customs.find((item) => item.actionId === action.id)?.keys ?? action.defaultKeys;
}

export function keysEqual(left: string[], right: string[]) {
  if (left.length !== right.length) return false;
  return left.every((part, index) => part === right[index]);
}

export function formatShortcutKeys(keys: string[]) {
  return keys
    .map((part) => (part === "then" ? "then" : part))
    .join(keys.includes("then") ? " " : "");
}

/** Find another action already bound to the same key chord. */
export function findShortcutConflict(
  actionId: string,
  keys: string[],
  actions: ShortcutAction[],
  customs = listCustomShortcuts(),
) {
  if (!keys.length) return undefined;
  for (const action of actions) {
    if (action.id === actionId) continue;
    const bound = resolveShortcutKeys(action, customs);
    if (keysEqual(bound, keys)) return action;
  }
  return undefined;
}

/** Built-in shortcut catalog mirrored from command menu + help dialog. */
export const DEFAULT_SHORTCUT_ACTIONS: ShortcutAction[] = [
  { id: "search-workspace", label: "Search workspace", group: "Navigation", defaultKeys: ["⌘", "K"], keywords: "command menu find" },
  { id: "go-inbox", label: "Go to Inbox", group: "Navigation", defaultKeys: ["G", "then", "I"] },
  { id: "go-my-issues", label: "Go to My issues", group: "Navigation", defaultKeys: ["G", "then", "M"] },
  { id: "open-settings", label: "Open settings", group: "Navigation", defaultKeys: ["G", "then", "S"] },
  { id: "go-agent", label: "Go to Agent", group: "Navigation", defaultKeys: ["G", "then", "J"] },
  { id: "go-reviews", label: "Go to Reviews", group: "Navigation", defaultKeys: ["G", "then", "R"] },
  { id: "create-issue", label: "Create issue", group: "Create", defaultKeys: ["C"], keywords: "new ticket task" },
  { id: "create-project", label: "Create project", group: "Create", defaultKeys: ["N", "then", "P"] },
  { id: "create-initiative", label: "Create initiative", group: "Create", defaultKeys: ["N", "then", "I"] },
  { id: "keyboard-help", label: "Keyboard shortcuts help", group: "General", defaultKeys: ["⌘", "/"] },
];
