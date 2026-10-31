import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { makeBootstrap, viewer } from "@/test/fixtures";
import type { AccountBootstrap, BootstrapData } from "@/types/flow";
import { WorkspaceMenu } from "./workspace-menu";
import { usePhoneLayout } from '@/hooks/use-phone-layout';

vi.mock('@/hooks/use-phone-layout', () => ({ usePhoneLayout: vi.fn(() => false) }));
beforeEach(() => { vi.mocked(usePhoneLayout).mockReturnValue(false) });
afterEach(() => { vi.unstubAllGlobals() });

async function openMenu(role: BootstrapData["viewerRole"]) {
  const data = makeBootstrap({ viewerRole: role });
  const account = { viewer, workspaces: [{ workspace: data.workspace, role: "Member", joinedAt: "" }] } as unknown as AccountBootstrap;
  render(<WorkspaceMenu account={account} data={data} onSettings={vi.fn()} onSwitch={vi.fn()} onCreate={vi.fn()} onLogout={vi.fn().mockResolvedValue(undefined)} />);
  await userEvent.setup().click(screen.getByRole("button", { name: /Workspace Menu/ }));
  return (await screen.findAllByRole("menuitem")).map((item) => item.textContent ?? "");
}

describe("workspace menu", () => {
  it('filters phone commands and clears the query when opening the workspace list', async () => {
    vi.mocked(usePhoneLayout).mockReturnValue(true);
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
    const user = userEvent.setup();
    const data = makeBootstrap({ viewerRole: 'admin' });
    const account = { viewer, workspaces: [{ workspace: data.workspace, role: 'Admin', joinedAt: '' }] } as unknown as AccountBootstrap;
    render(<WorkspaceMenu account={account} data={data} onSettings={vi.fn()} onSwitch={vi.fn()} onCreate={vi.fn()} onLogout={vi.fn().mockResolvedValue(undefined)}/>);
    await user.click(screen.getByRole('button', { name: /Workspace Menu/ }));
    expect(screen.getAllByRole('option')).toHaveLength(5);
    expect(screen.getByRole('option', { name: 'Settings' })).toHaveAttribute('data-selected', 'true');
    await user.keyboard('{ArrowDown}');
    expect(screen.getByRole('option', { name: 'Invite and manage members' })).toHaveAttribute('data-selected', 'true');
    expect(screen.getByRole('option', { name: 'Settings' })).toHaveAttribute('data-selected', 'false');
    await user.type(screen.getByRole('combobox'), 'Switch');
    expect(screen.getAllByRole('option')).toHaveLength(1);
    await user.click(screen.getByRole('option', { name: 'Switch workspace' }));
    expect(screen.getByRole('combobox')).toHaveValue('');
    expect(screen.getAllByRole('option').some(item => item.getAttribute('data-value') === data.workspace.id)).toBe(true);
  });

  it("offers members Settings but not member management (the reference app member view)", async () => {
    const items = await openMenu("member");
    expect(items.some((item) => item.startsWith("Settings"))).toBe(true);
    expect(items).not.toContain("Invite and manage members");
  });

  it("lets admins invite and manage members", async () => {
    expect(await openMenu("admin")).toContain("Invite and manage members");
  });

  it("opens the switcher with O then W and logs out with ⌥⇧Q", async () => {
    const onLogout = vi.fn().mockResolvedValue(undefined);
    const data = makeBootstrap({ viewerRole: "member" });
    const account = { viewer, workspaces: [{ workspace: data.workspace, role: "Member", joinedAt: "" }] } as unknown as AccountBootstrap;
    render(<WorkspaceMenu account={account} data={data} onSettings={vi.fn()} onSwitch={vi.fn()} onCreate={vi.fn()} onLogout={onLogout} />);

    fireEvent.keyDown(window, { key: "o" });
    fireEvent.keyDown(window, { key: "w" });
    expect(await screen.findByText("Settings", { exact: true })).toBeInTheDocument();

    fireEvent.keyDown(window, { key: "q", altKey: true, shiftKey: true });
    expect(onLogout).toHaveBeenCalledTimes(1);
  });
});
