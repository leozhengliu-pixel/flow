import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { VirtuosoMockContext } from 'react-virtuoso'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import { I18nProvider } from '@/i18n/i18n'
import * as chatApi from '@/lib/chat-integrations-api'
import { resetRememberedChatPlatform } from '@/lib/chat-platform'
import { primaryIntegrations } from '@/lib/integration-catalog'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { ChatIntegrationSettings } from '@/types/chat-integrations'
import type { BootstrapData, TeamSettings } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchTeamResources: vi.fn(), updateStructuredTeamSettings: vi.fn() }))
vi.mock('@/lib/api', async (importOriginal) => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))
vi.mock('@/lib/chat-integrations-api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/chat-integrations-api')>(),
  fetchChatIntegration: vi.fn(),
  fetchChatIntegrationChats: vi.fn(),
  saveChatBinding: vi.fn(),
  revokeChatBinding: vi.fn(),
  removeChatBinding: vi.fn(),
}))
vi.mock('@/components/ui/action-dialog-service', () => ({ confirmAction: vi.fn().mockResolvedValue(true) }))
vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

import { TeamOverviewPage } from '@/components/team-overview/team-overview-page'
import { TeamWorkflowSettings } from './team-workflow-settings'

type Platform = NonNullable<BootstrapData['chatPlatform']>

function workspace(platform: Platform | undefined, overrides: Partial<BootstrapData> = {}) {
  const base = makeBootstrap()
  const team = base.teams[0]
  return makeBootstrap({
    chatPlatform: platform,
    viewerRole: 'member',
    documents: [], favorites: [], subscriptions: [], emailIntakeAddresses: [], integrationConnections: [],
    members: base.users.map(user => ({ user, role: 'member' as const, status: 'active' as const, joinedAt: '' })),
    teamMembers: [{ teamId: team.id, userId: viewer.id, role: 'owner', joinedAt: '2026-08-01T00:00:00Z' }],
    teamSettings: { [team.id]: { teamId: team.id, timezone: 'Etc/UTC', slackNotifications: {} } as unknown as TeamSettings },
    ...overrides,
  } as Partial<BootstrapData>)
}

const chatSettings = (overrides: Partial<ChatIntegrationSettings> = {}): ChatIntegrationSettings => ({
  connection: { id: 'connection-1', provider: 'lark', name: 'Lark', status: 'connected', config: { appId: 'app-1', tenantKey: 'tenant-1' }, scopes: [], channels: [], linkbackEnabled: false, deliveryAttempts: 0, connectedBy: viewer.id, createdAt: '2026-10-26T00:00:00Z', updatedAt: '2026-10-26T00:00:00Z' },
  identity: null, bindings: [], bindableTeamIds: ['team-1'], credentialsConfigured: true, webhookURL: '', canManage: false,
  permissions: { tenant: { status: 'authorized', canAuthorize: false, tenantKey: 'tenant-1' }, workspace: { canManage: false }, teams: { manageableTeamIds: ['team-1'], privateTeamIds: [] } },
  ...overrides,
})

function teamHome(data: BootstrapData) {
  return render(<I18nProvider><TooltipProvider><VirtuosoMockContext.Provider value={{ viewportHeight: 480, itemHeight: 48 }}>
    <TeamOverviewPage data={data} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)} team={data.teams[0]} view="overview"/>
  </VirtuosoMockContext.Provider></TooltipProvider></I18nProvider>)
}

function teamNotifications(data: BootstrapData, section: 'overview' | 'notifications' = 'notifications') {
  return render(<MemoryRouter><I18nProvider><TooltipProvider><TeamWorkflowSettings data={data} team={data.teams[0]} section={section} onNavigate={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></TooltipProvider></I18nProvider></MemoryRouter>)
}

describe('chat platform entry points', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    localStorage.setItem('flow:locale', 'en-US')
    resetRememberedChatPlatform()
    api.fetchTeamResources.mockReset().mockResolvedValue({ resources: [], sections: [] })
    vi.mocked(chatApi.fetchChatIntegration).mockReset().mockResolvedValue(chatSettings())
    vi.mocked(chatApi.fetchChatIntegrationChats).mockReset().mockResolvedValue({ items: [{ id: 'oc_room', name: 'Room', external: false }], hasMore: false })
    vi.mocked(chatApi.saveChatBinding).mockReset().mockResolvedValue({})
  })
  afterEach(() => { vi.unstubAllGlobals() })

  it.each([
    [undefined, 'Connect channel'],
    ['slack', 'Connect channel'],
    ['lark', 'Connect Lark group'],
    ['feishu', 'Connect Feishu group'],
  ] as const)('team home links %s deployments to the team chat page as "%s"', async (platform, label) => {
    teamHome(workspace(platform))
    const link = await screen.findByRole('link', { name: label })
    expect(link).toHaveAttribute('href', expect.stringContaining('/settings/teams/TST/notifications'))
  })

  it('names Feishu 飞书 on the team home in Chinese', async () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    teamHome(workspace('feishu'))
    expect(await screen.findByRole('link', { name: '连接 飞书 群' })).toBeInTheDocument()
  })

  it('keeps the Slack channel settings on Slack deployments', async () => {
    teamNotifications(workspace('slack'))
    expect(await screen.findByRole('heading', { level: 1, name: 'Slack notifications' })).toBeInTheDocument()
    expect(screen.getAllByText('Connect a Slack channel').length).toBeGreaterThan(0)
    expect(chatApi.fetchChatIntegration).not.toHaveBeenCalled()
  })

  it('lists the team chat page under its Lark name in the team settings overview', async () => {
    teamNotifications(workspace('lark'), 'overview')
    expect(await screen.findByText('Lark notifications')).toBeInTheDocument()
    expect(screen.getByText('Broadcast notifications to Lark groups')).toBeInTheDocument()
    expect(screen.queryByText('Slack notifications')).not.toBeInTheDocument()
  })

  it('lets the team owner connect a Lark group for their team with explicit authorization', async () => {
    teamNotifications(workspace('lark'))
    expect(await screen.findByRole('heading', { level: 1, name: 'Lark notifications' })).toBeInTheDocument()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Connect Lark group' }))
    const form = await screen.findByRole('form', { name: 'Add group binding' })
    expect(within(form).queryByRole('combobox', { name: 'Team' })).not.toBeInTheDocument()
    expect(within(form).getByText('Test team')).toBeInTheDocument()
    await user.click(within(form).getByRole('combobox', { name: 'Group' }))
    await user.click(await screen.findByRole('option', { name: 'Room' }))
    expect(within(form).getByRole('button', { name: 'Save group binding' })).toBeDisabled()
    await user.click(within(form).getByRole('checkbox', { name: /I approve sharing/ }))
    await user.click(within(form).getByRole('button', { name: 'Save group binding' }))
    await waitFor(() => expect(chatApi.saveChatBinding).toHaveBeenCalledWith('lark', expect.objectContaining({ teamId: 'team-1', chatId: 'oc_room', authorize: true, events: expect.arrayContaining(['project.update_created']) })))
  })

  it('tells members without team binding permission who can connect groups', async () => {
    vi.mocked(chatApi.fetchChatIntegration).mockResolvedValue(chatSettings({ bindableTeamIds: [], permissions: { tenant: { status: 'authorized', canAuthorize: false }, workspace: { canManage: false }, teams: { manageableTeamIds: [], privateTeamIds: [] } } }))
    teamNotifications(workspace('feishu', { teamMembers: [] }))
    expect(await screen.findByText("Only the team's owners and workspace administrators can connect groups to this team.")).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Connect Feishu group' })).not.toBeInTheDocument()
  })

  it('points to the integration until the tenant is authorized', async () => {
    vi.mocked(chatApi.fetchChatIntegration).mockResolvedValue(chatSettings({ permissions: { tenant: { status: 'pending', canAuthorize: false }, workspace: { canManage: false }, teams: { manageableTeamIds: ['team-1'], privateTeamIds: [] } } }))
    teamNotifications(workspace('lark'))
    expect(await screen.findByText("The organization hasn't authorized Lark yet. A tenant administrator completes it in the integration settings.")).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Open settings' })).toBeInTheDocument()
  })

  it('orders the chosen platform first among chat integrations', () => {
    const slugs = (platform: 'slack' | 'lark' | 'feishu') => primaryIntegrations(platform).map(entry => entry.slug).filter(slug => ['slack', 'lark', 'feishu'].includes(slug))
    expect(slugs('slack')).toEqual(['slack', 'feishu', 'lark'])
    expect(slugs('lark')).toEqual(['lark', 'slack', 'feishu'])
    expect(slugs('feishu')).toEqual(['feishu', 'slack', 'lark'])
  })
})
