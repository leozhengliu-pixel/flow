import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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

import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap } from "@/test/fixtures";
import * as chatIntegrationsApi from "@/lib/chat-integrations-api";
import { preloadSettingsPage, SettingsPage } from "./settings-page";

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

it("draws every settings nav entry with its reference glyph, not lucide icons", () => {
  const { container } = render(
    <I18nProvider>
      <SettingsPage {...props()} />
    </I18nProvider>,
  );

  const glyph = (name: string) => [...container.querySelectorAll(".settings-sidebar nav button")].find((button) => button.textContent?.includes(name))?.querySelector("svg");
  expect(glyph("Agent 个性化")).toHaveAttribute("data-settings-nav-glyph", "agents");
  expect(glyph("AI 与 Agent")).toHaveAttribute("data-settings-nav-glyph", "ai");
  const navGlyphs = [...container.querySelectorAll(".settings-sidebar nav section:not(:last-child) button > svg")];
  expect(navGlyphs.length).toBeGreaterThan(20);
  for (const svg of navGlyphs) {
    expect(svg).toHaveAttribute("data-settings-nav-glyph");
    expect(svg).toHaveAttribute("fill", "currentColor");
    expect(svg).toHaveAttribute("width", "16");
  }
  expect(container.querySelector(".settings-sidebar nav .lucide")).toBeNull();
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
  // Asks lives in the lazy feature settings chunk; a cold import takes longer than findBy's 1s.
  await preloadSettingsPage('asks');
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

it('shows members the workspace feature pages and hides administration', () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  input.page = 'preferences';
  input.data = { ...input.data, viewerRole: 'member', workspaceSettings: { ...input.data.workspaceSettings, labelPermission: 'members', templatePermission: 'members', teamCreatePermission: 'members' } };
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  const labels = navLabels();
  for (const visible of ['Preferences', 'Labels', 'Templates', 'SLAs', 'Statuses', 'Updates', 'AI & agents', 'Loops', 'Initiatives', 'Documents', 'Customer requests', 'Releases', 'Pulse', 'Asks', 'Emojis', 'Integrations', 'Create a team'])
    expect(labels).toContain(visible);
  for (const hidden of ['Workspace', 'Members', 'Security', 'API', 'Applications', 'Billing', 'Usage & limits', 'Import & export'])
    expect(labels).not.toContain(hidden);
});

it('lists Billing and Usage & limits between Applications and Import & export for admins', async () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  input.page = 'billing';
  input.data = { ...input.data, viewerRole: 'owner' };
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  const labels = navLabels();
  const admin = labels.slice(labels.indexOf('Workspace'));
  expect(admin.slice(0, 9)).toEqual(['Workspace', 'Teams', 'Members', 'Security', 'API', 'Applications', 'Billing', 'Usage & limits', 'Import & export']);
  expect(await screen.findByRole('heading', { level: 1, name: 'Billing' })).toBeInTheDocument();
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

it('answers administration pages opened by URL with the reference app\'s "Not found" for members', () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  input.page = 'security';
  input.data = { ...input.data, viewerRole: 'member' };
  const { unmount } = render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  expect(screen.getByRole('status')).toHaveTextContent('Not found');
  expect(screen.getByRole('status')).toHaveTextContent('We could not find the page you were looking for');
  unmount();
  render(<I18nProvider><SettingsPage {...{ ...input, page: 'api' }}/></I18nProvider>);
  expect(screen.getByRole('status')).toHaveTextContent('Only workspace administrators can manage API settings');
});

it('lists Integrations as its own section, "All integrations" and enabled integrations for admins', async () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  input.page = 'preferences';
  const { unmount } = render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  const section = (title: string) => Array.from(document.querySelectorAll('.settings-sidebar nav section')).find(item => item.querySelector('h2')?.textContent === title);
  expect(section('Integrations')?.textContent).toContain('All integrations');
  unmount();
  // Admins also get each enabled integration under "All integrations" (the reference app: All integrations, GitHub).
  const connected = { ...input, page: 'integrations' as const, integrationProvider: 'github' as const, data: { ...input.data, integrationConnections: [
    { id: 'conn-github', provider: 'github', name: 'GitHub', status: 'connected' },
    { id: 'conn-slack', provider: 'slack', name: 'Slack', status: 'error' },
  ] } } as unknown as ReturnType<typeof props>;
  const admin = render(<I18nProvider><SettingsPage {...connected}/></I18nProvider>);
  const items = Array.from(section('Integrations')?.querySelectorAll('button') ?? []);
  expect(items.map(item => item.textContent?.trim())).toEqual(['All integrations', 'GitHub']);
  expect(items[1]).toHaveAttribute('aria-current', 'page');
  await userEvent.setup().click(items[0]);
  expect(input.onNavigate).toHaveBeenCalledWith('integrations');
  admin.unmount();
  render(<I18nProvider><SettingsPage {...{ ...input, data: { ...input.data, viewerRole: 'member' } }}/></I18nProvider>);
  expect(section('Integrations')?.querySelector('button')?.textContent?.trim()).toBe('Integrations');
  expect(section('Features')?.textContent).not.toContain('Integrations');
});

it('keeps Feishu workspace configuration read-only for members without personal account actions', async () => {
  localStorage.setItem('flow:locale', 'en');
  const fetch = vi.spyOn(chatIntegrationsApi, 'fetchChatIntegration').mockResolvedValue({
    connection: { id: 'chat-connection', provider: 'feishu', status: 'connected' } as never,
    identity: null, bindings: [], credentialsConfigured: true, webhookURL: '', canManage: false,
  });
  const input = props();
  input.page = 'integrations';
  input.integrationSlug = 'feishu';
  input.data = { ...input.data, viewerRole: 'member', integrationConnections: [] };
  const view = render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  try {
    expect(await screen.findByRole('region', { name: 'Connection' })).toBeInTheDocument();
    expect(document.querySelector('fieldset.settings-read-only-fields')).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Link account' })).not.toBeInTheDocument();
    expect(screen.queryByRole('textbox', { name: 'App ID' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add group' })).not.toBeInTheDocument();
  } finally {
    view.unmount();
    fetch.mockRestore();
  }
});

it('lets members connect chat identities from Connected accounts without workspace configuration controls', async () => {
  localStorage.setItem('flow:locale', 'en');
  const fetch = vi.spyOn(chatIntegrationsApi, 'fetchChatIntegration').mockResolvedValue({
    connection: { id: 'chat-connection', provider: 'feishu', status: 'connected' } as never,
    identity: null, bindings: [], credentialsConfigured: true, webhookURL: '', canManage: false,
  });
  const input = props();
  input.page = 'connections';
  input.data = { ...input.data, viewerRole: 'member', integrationConnections: [{ id: 'chat-connection', provider: 'feishu', name: 'Feishu', status: 'connected' } as never] };
  const view = render(<MemoryRouter><I18nProvider><SettingsPage {...input}/></I18nProvider></MemoryRouter>);
  try {
    const row = await screen.findByRole('region', { name: 'Feishu account' });
    expect(screen.queryByRole('region', { name: 'Lark account' })).not.toBeInTheDocument();
    expect(await within(row).findByRole('button', { name: 'Connect' })).toBeEnabled();
    expect(screen.queryByRole('textbox', { name: 'App ID' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add group' })).not.toBeInTheDocument();
  } finally {
    view.unmount();
    fetch.mockRestore();
  }
});

it('draws the Projects › Statuses nav item with the reference app\'s hexagon glyph', () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  input.page = 'project-statuses';
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  const statuses = Array.from(document.querySelectorAll('aside nav button, .settings-sidebar nav button')).find(button => button.textContent?.trim() === 'Statuses');
  const glyph = statuses?.querySelector('svg[data-settings-nav-glyph="projectStatuses"]');
  expect(glyph).not.toBeNull();
  expect(glyph?.getAttribute('viewBox')).toBe('1 1 14 14');
  expect(glyph?.querySelectorAll('path')).toHaveLength(2);
  expect(glyph?.querySelector('path')?.getAttribute('mask')).toMatch(/^url\(#settings-nav-status-/);
});

it('lets admins edit and delete every project status from its menu', async () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  input.page = 'project-statuses';
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  const rows = await screen.findAllByRole('listitem', {}, { timeout: 5000 });
  const statusRows = rows.filter(row => row.classList.contains('pst-row'));
  expect(statusRows.length).toBe(input.data.projectStatuses.length);
  for (const row of statusRows) expect(row.querySelector('button[aria-label="Open menu"]')).not.toBeNull();
});

it('renders admin-only workspace configuration read-only for members with the reference app wording', async () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  input.page = 'project-statuses';
  input.data = { ...input.data, viewerRole: 'member' };
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  await screen.findAllByRole('listitem', {}, { timeout: 5000 });
  const fields = document.querySelector('fieldset.settings-read-only-fields') as HTMLFieldSetElement;
  expect(fields).toBeDisabled();
  expect(fields).toHaveAccessibleName('Only admins can edit project statuses');
  // The reference app shows no banner: the reason appears when hovering a disabled control.
  expect(screen.queryByRole('note')).toBeNull();
  const create = fields.querySelector('button') as HTMLButtonElement;
  fireEvent.pointerMove(create, { clientX: 1, clientY: 1 });
  const hint = await screen.findByRole('tooltip');
  expect(hint).toHaveTextContent('Only workspace admins can edit project statuses');
  expect(within(hint).getByRole('link', { name: 'View admins →' })).toHaveAttribute('href', '/workspace/members');
});

it('lets members open release pipeline settings and create a pipeline', async () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  input.page = 'releases';
  input.data = { ...input.data, viewerRole: 'member', trash: [] } as never;
  render(<MemoryRouter><I18nProvider><SettingsPage {...input}/></I18nProvider></MemoryRouter>);
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
  expect(await screen.findByRole('note', {}, { timeout: 5000 })).toHaveTextContent('Only admins and team owners can modify the team’s cycle settings');
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

function memberOfOnlyTeam(input: ReturnType<typeof props>) {
  input.page = 'team';
  input.teamKey = 'ENG';
  input.data = {
    ...input.data,
    viewerRole: 'member',
    teams: [{ id: 'team-eng', key: 'ENG', name: 'Engineering', color: '#777777' }, { id: 'team-ops', key: 'OPS', name: 'Ops', color: '#777777' }],
    teamMembers: [{ teamId: 'team-eng', userId: input.data.viewer.id, role: 'member', joinedAt: '' }],
    teamSettings: { 'team-eng': { teamId: 'team-eng', settingsPermission: 'allMembers', memberPermission: 'allMembers' } },
  } as never;
}

it('sends team members from Access and permissions back to the team overview', async () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  memberOfOnlyTeam(input);
  input.teamSection = 'security';
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  await waitFor(() => expect(input.onNavigate).toHaveBeenCalledWith('team', 'ENG'));
});

it('disables the team danger zone for members with the reference app\'s reasons', async () => {
  localStorage.setItem('flow:locale', 'en');
  const input = props();
  memberOfOnlyTeam(input);
  input.teamSection = 'overview';
  render(<I18nProvider><SettingsPage {...input}/></I18nProvider>);
  const leave = await screen.findByRole('button', { name: 'Leave team…' }, { timeout: 5000 });
  expect(leave).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Retire…' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Delete…' })).toBeDisabled();
  await userEvent.setup().hover(screen.getByRole('button', { name: 'Delete…' }).parentElement as HTMLElement);
  expect(await screen.findByRole('tooltip')).toHaveTextContent('Only workspace owners, admins, and team owners can delete teams');
});
