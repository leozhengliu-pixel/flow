import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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

it("draws the agent settings entries with the agent glyphs (cursor, AI burst), not lucide icons", () => {
  const { container } = render(
    <I18nProvider>
      <SettingsPage {...props()} />
    </I18nProvider>,
  );

  const glyph = (name: string) => [...container.querySelectorAll("button")].find((button) => button.textContent?.includes(name))?.querySelector("svg");
  expect(glyph("Agent 个性化")).toHaveAttribute("data-agent-glyph", "agentPointer");
  expect(glyph("AI 与 Agent")).toHaveAttribute("data-agent-glyph", "aiBurst");
  expect(container.querySelector(".lucide-bot, .lucide-sparkles")).toBeNull();
});

it('does not advertise the retired shortcuts settings page', () => {
  const input = { ...props(), page: 'shortcuts' as const };
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  expect(screen.getByRole('status')).toHaveTextContent('找不到你要访问的页面');
  expect(screen.queryByRole('button', { name: '快捷键' })).toBeNull();
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

it("searches page sections and individual settings, then navigates with keyboard", async () => {
  localStorage.setItem("flow:locale", "en");
  const user = userEvent.setup();
  const input = props();
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);

  const search = screen.getByRole("textbox", { name: "Search settings" });
  await user.type(search, "theme");
  expect(screen.getByRole("button", { name: "Code & reviews" })).toBeVisible();
  expect(screen.getByRole("button", { name: "Code theme" })).toBeVisible();
  expect(screen.getByRole("button", { name: "Interface theme" })).toBeVisible();

  await user.keyboard("{ArrowDown}");
  expect(document.activeElement).toHaveAttribute("data-settings-search-result");
  await user.keyboard("{Enter}");
  expect(input.onNavigate).toHaveBeenCalled();
  expect(search).toHaveValue("theme");
});

it("shows an empty state and clears the query with Escape", async () => {
  localStorage.setItem("flow:locale", "en");
  const user = userEvent.setup();
  render(<I18nProvider><SettingsPage {...props()} /></I18nProvider>);

  const search = screen.getByRole("textbox", { name: "Search settings" });
  await user.type(search, "does-not-exist");
  expect(screen.getByText("No matching settings")).toBeVisible();
  await user.keyboard("{Escape}");
  expect(search).toHaveValue("");
});

it("focuses settings search with slash", () => {
  render(<I18nProvider><SettingsPage {...props()} /></I18nProvider>);

  const search = screen.getByRole("textbox", { name: "搜索设置" });
  fireEvent.keyDown(window, { key: "/" });
  expect(search).toHaveFocus();
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

it('shows Asks settings read-only to regular members with a searchable enable control', async () => {
  localStorage.setItem('flow:locale', 'en-US');
  const input = props();
  input.page = 'asks';
  input.data = { ...input.data, viewerRole: 'member', integrationConnections: [], emailIntakeAddresses: [] };
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  expect(await screen.findByRole('checkbox', { name: 'Enable Asks' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Asks' })).toHaveAttribute('aria-current', 'page');
  const user = userEvent.setup();
  await user.type(screen.getByRole('textbox', { name: 'Search settings' }), 'Enable Asks');
  await user.click(screen.getByRole('button', { name: 'Enable Asks' }));
  expect(input.onNavigate).toHaveBeenCalledWith('asks');
  await waitFor(() => expect(screen.getByRole('checkbox', { name: 'Enable Asks' }).closest('.feature-row')).toHaveClass('settings-search-target'));
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
  expect(input.onNavigate).toHaveBeenCalledWith('team', 'T8', undefined);
});

it('searches team settings and opens the matching section', async () => {
  localStorage.setItem('flow:locale', 'en');
  const user = userEvent.setup();
  const input = props();
  input.data = {
    ...input.data,
    teams: [{ id: 'team-eng', key: 'ENG', name: 'Engineering', color: '#777777' }],
    teamMembers: [{ teamId: 'team-eng', userId: input.data.viewer.id, role: 'member', joinedAt: '' }],
  };
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);

  await user.type(screen.getByRole('textbox', { name: 'Search settings' }), 'cycle duration');
  expect(screen.getByRole('button', { name: 'Engineering' })).toBeVisible();
  await user.click(screen.getByRole('button', { name: 'Cycle duration' }));
  expect(input.onNavigate).toHaveBeenCalledWith('team', 'ENG', 'cycles');
});

function navLabels() {
  return Array.from(document.querySelectorAll('.settings-sidebar nav section, aside nav section'))
    .flatMap(section => Array.from(section.querySelectorAll('button')).map(button => button.textContent?.trim() ?? ''))
}

it('shows members the workspace feature pages and hides administration like Linear', () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  input.page = 'preferences';
  input.data = { ...input.data, viewerRole: 'member', workspaceSettings: { ...input.data.workspaceSettings, labelPermission: 'members', templatePermission: 'members', teamCreatePermission: 'members' } };
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  const labels = navLabels();
  for (const visible of ['Preferences', 'Labels', 'Templates', 'SLAs', 'Statuses', 'Updates', 'AI & Agents', 'Loops', 'Initiatives', 'Documents', 'Customer requests', 'Releases', 'Pulse', 'Asks', 'Emojis', 'Integrations', 'Create a team'])
    expect(labels).toContain(visible);
  for (const hidden of ['Workspace', 'Members', 'Security', 'API', 'Applications', 'Import & export'])
    expect(labels).not.toContain(hidden);
});

it('hides workspace templates from members when only admins manage templates', () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  input.page = 'preferences';
  input.data = { ...input.data, viewerRole: 'member', workspaceSettings: { ...input.data.workspaceSettings, labelPermission: 'admins', templatePermission: 'admins', teamCreatePermission: 'admins' } };
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  const labels = navLabels();
  expect(labels).toContain('Labels');
  expect(labels).not.toContain('Templates');
  expect(labels).not.toContain('Documents');
  expect(labels).not.toContain('Create a team');
});

it('limits guests to their personal settings', () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  input.page = 'preferences';
  input.data = { ...input.data, viewerRole: 'guest', workspaceSettings: { ...input.data.workspaceSettings, labelPermission: 'members', templatePermission: 'members' } };
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  const labels = navLabels();
  expect(labels).toEqual(expect.arrayContaining(['Preferences', 'Profile', 'Notifications', 'Security & access', 'Connected accounts']));
  for (const hidden of ['Labels', 'Releases', 'Asks', 'Integrations', 'Members', 'API']) expect(labels).not.toContain(hidden);
});

it('blocks administration pages opened by URL for members', () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  input.page = 'security';
  input.data = { ...input.data, viewerRole: 'member' };
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  expect(screen.getByRole('heading', { name: 'Admin access required' })).toBeVisible();
});

it('renders admin-only workspace configuration read-only for members with Linear wording', async () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  input.page = 'project-statuses';
  input.data = { ...input.data, viewerRole: 'member' };
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  expect(await screen.findByRole('note')).toHaveTextContent('Only admins can edit project statuses');
  const fields = document.querySelector('fieldset.settings-read-only-fields') as HTMLFieldSetElement;
  expect(fields).toBeDisabled();
  expect(fields.querySelectorAll('button').length).toBeGreaterThan(0);
});

it('lets members open release pipeline settings and create a pipeline', async () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  input.page = 'releases';
  input.data = { ...input.data, viewerRole: 'member', trash: [] } as never;
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  const create = await screen.findByRole('button', { name: 'New pipeline' });
  expect(create).toBeEnabled();
  await userEvent.setup().click(create);
  expect(input.onCreateReleasePipeline).toHaveBeenCalled();
});

it('shows team settings read-only to team members when only owners manage them', async () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  input.page = 'team';
  input.teamKey = 'ENG';
  input.teamSection = 'cycles';
  input.data = {
    ...input.data,
    viewerRole: 'member',
    teams: [{ id: 'team-eng', key: 'ENG', name: 'Engineering', color: '#777777' }],
    teamMembers: [{ teamId: 'team-eng', userId: input.data.viewer.id, role: 'member', joinedAt: '' }],
    teamSettings: { 'team-eng': { teamId: 'team-eng', settingsPermission: 'owners' } },
    cycleSettings: {},
  } as never;
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  expect(await screen.findByRole('note')).toHaveTextContent('Only admins and team owners can modify the team’s cycle settings');
  expect(document.querySelector('fieldset.settings-read-only-fields')).toBeDisabled();
  // The crumb back to the team stays usable above the read-only fields.
  const crumb = document.querySelector('.settings-read-only-crumb .settings-crumb') as HTMLButtonElement;
  expect(crumb).toHaveTextContent('Engineering');
  expect(crumb.closest('fieldset')).toBeNull();
  expect(crumb).toBeEnabled();
});

it('asks non-members for permission before showing team settings', () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  input.page = 'team';
  input.teamKey = 'ENG';
  input.teamSection = 'general';
  input.data = { ...input.data, viewerRole: 'member', teams: [{ id: 'team-eng', key: 'ENG', name: 'Engineering', color: '#777777' }], teamMembers: [], teamSettings: {} } as never;
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  expect(screen.getByRole('heading', { name: 'You need permission to view this team’s settings.' })).toBeVisible();
});
