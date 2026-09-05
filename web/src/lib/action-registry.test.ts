import { describe, expect, it, vi } from "vitest";
import { ActionRegistry } from "./action-registry";

describe("ActionRegistry", () => {
  it("executes registered actions and preserves the undo queue", async () => {
    const registry = new ActionRegistry();
    const run = vi.fn();
    const undo = vi.fn();
    registry.register({ id: "rename", label: "Rename", run, undo });

    await expect(registry.execute("rename", { source: "keyboard" })).resolves.toBe(true);
    await expect(registry.undo()).resolves.toBe(true);
    expect(run).toHaveBeenCalledWith({ source: "keyboard" });
    expect(undo).toHaveBeenCalledWith({ source: "keyboard" });
  });

  it("does not execute disabled actions", async () => {
    const registry = new ActionRegistry();
    const run = vi.fn();
    registry.register({ id: "restricted", label: "Restricted", enabled: () => false, run });

    await expect(registry.execute("restricted")).resolves.toBe(false);
    expect(run).not.toHaveBeenCalled();
    expect(registry.list()).toHaveLength(0);
  });
});
