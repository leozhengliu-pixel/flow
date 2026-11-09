import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
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
    expect(screen.getAllByRole('option')).toHaveLength(4);
    expect(screen.queryByText('Download desktop app')).not.toBeInTheDocument();
    expect(screen.getAllByRole('option').every(item => item.getAttribute('data-selected') === 'false')).toBe(true);
    await user.keyboard('{ArrowDown}');
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
    fireEvent.keyDown(window, { key: "Œ", code: "KeyQ", altKey: true, shiftKey: true });
    expect(onLogout).toHaveBeenCalledTimes(2);
  });

  it('opens O then W with focus on Switch workspace and the submenu open, never on the first item', async () => {
    const data = makeBootstrap({ viewerRole: 'member' });
    const account = { viewer, workspaces: [{ workspace: data.workspace, role: 'Member', joinedAt: '' }] } as unknown as AccountBootstrap;
    render(<WorkspaceMenu account={account} data={data} onSettings={vi.fn()} onSwitch={vi.fn()} onCreate={vi.fn()} onLogout={vi.fn().mockResolvedValue(undefined)} />);
    const settingsFocus = vi.fn();
    document.addEventListener('focusin', event => { if ((event.target as Element).textContent?.startsWith('Settings') && (event.target as Element).getAttribute('role') === 'menuitem') settingsFocus() });
    fireEvent.keyDown(window, { key: 'o' });
    fireEvent.keyDown(window, { key: 'w' });
    const trigger = await screen.findByRole('menuitem', { name: /Switch workspace/ });
    await waitFor(() => expect(trigger).toHaveFocus());
    await waitFor(() => expect(trigger).toHaveAttribute('data-state', 'open'));
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 80)) });
    expect(settingsFocus).not.toHaveBeenCalled();
    expect(trigger).toHaveFocus();
    expect(trigger).toHaveAttribute('data-state', 'open');
  });

  it('replaces the workspace name with an Offline pill while the browser is offline', () => {
    const data = makeBootstrap({ viewerRole: 'member' });
    const account = { viewer, workspaces: [{ workspace: data.workspace, role: 'Member', joinedAt: '' }] } as unknown as AccountBootstrap;
    const online = vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(true);
    try {
      render(<WorkspaceMenu account={account} data={data} onSettings={vi.fn()} onSwitch={vi.fn()} onCreate={vi.fn()} onLogout={vi.fn().mockResolvedValue(undefined)} />);
      const trigger = screen.getByRole('button', { name: /Workspace Menu/ });
      expect(trigger).toHaveTextContent(data.workspace.name);
      expect(screen.queryByRole('status')).not.toBeInTheDocument();

      online.mockReturnValue(false);
      act(() => { window.dispatchEvent(new Event('offline')) });
      expect(screen.getByRole('status')).toHaveTextContent('Offline');
      expect(trigger).not.toHaveTextContent(data.workspace.name);

      online.mockReturnValue(true);
      act(() => { window.dispatchEvent(new Event('online')) });
      expect(screen.queryByRole('status')).not.toBeInTheDocument();
      expect(trigger).toHaveTextContent(data.workspace.name);
    } finally { online.mockRestore() }
  });
});
