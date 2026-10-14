import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { makeBootstrap, viewer } from "@/test/fixtures";
import type { AccountBootstrap, BootstrapData } from "@/types/flow";
import { WorkspaceMenu } from "./workspace-menu";

async function openMenu(role: BootstrapData["viewerRole"]) {
  const data = makeBootstrap({ viewerRole: role });
  const account = { viewer, workspaces: [{ workspace: data.workspace, role: "Member", joinedAt: "" }] } as unknown as AccountBootstrap;
  render(<WorkspaceMenu account={account} data={data} onSettings={vi.fn()} onSwitch={vi.fn()} onCreate={vi.fn()} onLogout={vi.fn().mockResolvedValue(undefined)} />);
  await userEvent.setup().click(screen.getByRole("button", { name: /Workspace Menu/ }));
  return (await screen.findAllByRole("menuitem")).map((item) => item.textContent ?? "");
}

describe("workspace menu", () => {
  it("offers members Settings but not member management (the reference app member view)", async () => {
    const items = await openMenu("member");
    expect(items.some((item) => item.startsWith("Settings"))).toBe(true);
    expect(items).not.toContain("Invite and manage members");
  });

  it("lets admins invite and manage members", async () => {
    expect(await openMenu("admin")).toContain("Invite and manage members");
  });
});
