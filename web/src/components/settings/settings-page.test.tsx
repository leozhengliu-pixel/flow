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
        plan: "enterprise",
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
