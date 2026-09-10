import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";

vi.hoisted(() => {
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }),
  });
});

import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap } from "@/test/fixtures";
import { SettingsPage } from "./settings-page";

function props(): ComponentProps<typeof SettingsPage> {
  return {
    data: makeBootstrap({
      viewerRole: "admin",
      workspaceSettings: {
        fiscalMonth: "January",
        defaultHomeView: "agent",
        welcomeMessage: "",
      },
      userSettings: { "user-1": { userId: "user-1" } },
    } as never),
    page: "workspace" as const,
    onBack: vi.fn(),
    onNavigate: vi.fn(),
    onCreateReleasePipeline: vi.fn(),
    onOpenReleasePipeline: vi.fn(),
    onOpenIntegration: vi.fn(),
    onCreateIssueTemplate: vi.fn(),
    onOpenIssueTemplate: vi.fn(),
    onDuplicateIssueTemplate: vi.fn(),
    onCreateProjectTemplate: vi.fn(),
    onOpenProjectTemplate: vi.fn(),
    onDuplicateProjectTemplate: vi.fn(),
    onCreateTeam: vi.fn(),
    onWorkspaceUpdate: vi.fn().mockResolvedValue(undefined),
    onWorkspaceDelete: vi.fn().mockResolvedValue(undefined),
    onSettingsUpdate: vi.fn().mockResolvedValue({}),
    onReload: vi.fn().mockResolvedValue(undefined),
  } as ComponentProps<typeof SettingsPage>;
}

beforeEach(() => {
  localStorage.setItem("flow:locale", "zh-CN");
});

it("localizes settings navigation and keeps the active route announced", () => {
  render(
    <I18nProvider>
      <SettingsPage {...props()} />
    </I18nProvider>,
  );

  expect(screen.getByRole("heading", { name: "管理" })).toBeVisible();
  expect(screen.getByRole("button", { name: "工作区" })).toHaveAttribute(
    "aria-current",
    "page",
  );
  expect(screen.getByPlaceholderText("搜索…")).toBeVisible();
});

it("filters settings using translated labels", async () => {
  const user = userEvent.setup();
  render(
    <I18nProvider>
      <SettingsPage {...props()} />
    </I18nProvider>,
  );

  const search = screen.getByRole("textbox", { name: "搜索设置" });
  await user.type(search, "文档");
  expect(screen.getByRole("button", { name: "文档" })).toBeVisible();
  expect(screen.queryByRole("button", { name: "工作区" })).toBeNull();
});

it("treats workspace owners as administrators", () => {
  const ownerProps = props();
  ownerProps.data = { ...ownerProps.data, viewerRole: "owner" };

  render(
    <I18nProvider>
      <SettingsPage {...ownerProps} />
    </I18nProvider>,
  );

  expect(screen.queryByText("需要管理员权限")).toBeNull();
  expect(screen.getByRole("heading", { name: "工作区" })).toBeVisible();
});

it('limits Your teams to memberships and searches joined team identifiers', async () => {
  const user = userEvent.setup();
  const input = props();
  input.data = { ...input.data, teams: Array.from({ length: 1000 }, (_, index) => ({ id: `team-${index}`, key: `T${index}`, name: `Team ${index}`, color: '#777777' })), teamMembers: [{ teamId: 'team-8', userId: input.data.viewer.id, role: 'member', joinedAt: '' }] };
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  expect(screen.getByRole('button', { name: 'Team 8' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Team 999' })).not.toBeInTheDocument();
  expect(document.querySelectorAll('.settings-team-icon')).toHaveLength(1);
  await user.type(screen.getByRole('textbox', { name: '搜索设置' }), 'T8');
  await user.click(screen.getByRole('button', { name: 'Team 8' }));
  expect(input.onNavigate).toHaveBeenCalledWith('team', 'T8');
});
