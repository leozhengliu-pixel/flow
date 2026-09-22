import { afterEach, describe, expect, it } from "vitest";
import {
  clearCustomShortcuts,
  DEFAULT_SHORTCUT_ACTIONS,
  findShortcutConflict,
  setCustomShortcut,
  resetCustomShortcut,
  resolveShortcutKeys,
} from "./custom-shortcuts";

describe("custom shortcuts (LS-0008)", () => {
  afterEach(() => clearCustomShortcuts());

  it("stores and restores custom bindings with conflict detection", () => {
    expect(resolveShortcutKeys(DEFAULT_SHORTCUT_ACTIONS[0])).toEqual(["⌘", "K"]);
    setCustomShortcut("create-issue", ["⌘", "K"]);
    const conflict = findShortcutConflict(
      "create-issue",
      ["⌘", "K"],
      DEFAULT_SHORTCUT_ACTIONS,
    );
    expect(conflict?.id).toBe("search-workspace");
    resetCustomShortcut("create-issue");
    expect(resolveShortcutKeys(DEFAULT_SHORTCUT_ACTIONS.find((a) => a.id === "create-issue")!)).toEqual(["C"]);
  });
});
