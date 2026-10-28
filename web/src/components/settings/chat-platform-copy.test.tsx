import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { VirtuosoMockContext } from 'react-virtuoso'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import { I18nProvider } from '@/i18n/i18n'
import * as chatApi from '@/lib/chat-integrations-api'
import { rememberChatPlatform, resetRememberedChatPlatform } from '@/lib/chat-platform'
import { teamReadOnlyNotice } from '@/lib/settings-access'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { ChatIntegrationSettings } from '@/types/chat-integrations'
import type { BootstrapData, SavedView, TeamSettings } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchTeamResources: vi.fn(), listPushSubscriptions: vi.fn() }))
vi.mock('@/lib/api', async (importOriginal) => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))
vi.mock('@/lib/chat-integrations-api', async (importOriginal) => ({ ...await importOriginal<typeof import('@/lib/chat-integrations-api')>(), fetchChatIntegration: vi.fn() }))
vi.mock('@/components/ui/action-dialog-service', () => ({ confirmAction: vi.fn().mockResolvedValue(true) }))
vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

import { TeamOverviewPage } from '@/components/team-overview/team-overview-page'
import { SavedViewMenu } from '@/components/issue-explorer/saved-view-editor'
import { FeatureSettingsPage } from './feature-settings'
import { PersonalSettings } from './personal-settings'
import { chatPlatformSearchPages, createSettingsSearchIndex, SETTINGS_SEARCH_PAGES } from './settings-search'
import { TeamWorkflowSettings } from './team-workflow-settings'

type Platform = 'lark' | 'feishu'

function workspace(platform: Platform): BootstrapData {
  const base = makeBootstrap()
  const team = base.teams[0]
  return makeBootstrap({
    chatPlatform: platform, viewerRole: 'admin',
    documents: [], favorites: [], subscriptions: [], emailIntakeAddresses: [], integrationConnections: [],
    members: base.users.map(user => ({ user, role: 'member' as const, status: 'active' as const, joinedAt: '' })),
    teamMembers: [{ teamId: team.id, userId: viewer.id, role: 'owner', joinedAt: '2026-08-01T00:00:00Z' }],
    teamSettings: { [team.id]: { teamId: team.id, timezone: 'Etc/UTC', slackNotifications: {} } as unknown as TeamSettings },
  } as Partial<BootstrapData>)
}

const chatSettings = (): ChatIntegrationSettings => ({
  connection: { id: 'connection-1', provider: 'lark', name: 'Lark', status: 'connected', config: {}, scopes: [], channels: [], linkbackEnabled: false, deliveryAttempts: 0, connectedBy: viewer.id, createdAt: '', updatedAt: '' },
  identity: null, bindings: [], bindableTeamIds: ['team-1'], credentialsConfigured: true, webhookURL: '', canManage: true,
  permissions: { tenant: { status: 'authorized', tenantKey: 'tenant-1' }, workspace: { canManage: true }, teams: { manageableTeamIds: ['team-1'], privateTeamIds: [] } },
})

const view = { id: 'view-1', name: 'Urgent todo', description: '', scope: 'workspace', resource: 'issues', view: 'all', filters: [], display: {}, createdAt: '', updatedAt: '' } as SavedView

/** Renders each surface that names the chat platform. */
async function surfaces(data: BootstrapData) {
  const shells = [
    <MemoryRouter key="team-settings"><TeamWorkflowSettings data={data} team={data.teams[0]} section="overview" onNavigate={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></MemoryRouter>,
    <MemoryRouter key="team-notifications"><TeamWorkflowSettings data={data} team={data.teams[0]} section="notifications" onNavigate={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></MemoryRouter>,
    <VirtuosoMockContext.Provider key="team-home" value={{ viewportHeight: 480, itemHeight: 48 }}><TeamOverviewPage data={data} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)} team={data.teams[0]} view="overview"/></VirtuosoMockContext.Provider>,
    <PersonalSettings key="notifications" page="notifications" data={data} values={{}} setValue={vi.fn()} onNavigate={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)} onBack={vi.fn()} onCustomizeSidebar={vi.fn()} {...({} as Record<string, never>)}/>,
    <FeatureSettingsPage key="initiatives" page="initiatives" data={data} onCreateReleasePipeline={vi.fn()} onOpenReleasePipeline={vi.fn()} onOpenIntegration={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)} onNavigateSettings={vi.fn()} {...({} as Record<string, never>)}/>,
  ]
  for (const shell of shells) {
    const view = render(<I18nProvider><TooltipProvider>{shell}</TooltipProvider></I18nProvider>)
    await waitFor(() => expect(view.container.textContent).not.toBe(''))
    // Let the chat integration and team resources load.
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 30)) })
    expect(view.container.textContent, String(shell.key)).not.toMatch(/Slack/i)
    view.unmount()
  }
  // The saved view menu has no Slack-only item on a Lark or Feishu deployment.
  rememberChatPlatform(data)
  const user = userEvent.setup()
  const menu = render(<I18nProvider><SavedViewMenu view={view} users={[]} teams={[]} onEdit={vi.fn()} onSetSubscriptionEvents={vi.fn()} onCopy={vi.fn()} onDelete={vi.fn()}/></I18nProvider>)
  await user.click(screen.getByRole('button', { name: /view options|视图选项/ }))
  expect(screen.getByRole('menu').textContent).not.toMatch(/Slack/i)
  menu.unmount()
}

describe('chat platform copy on a Lark or Feishu deployment', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    localStorage.setItem('flow:locale', 'en-US')
    resetRememberedChatPlatform()
    api.fetchTeamResources.mockReset().mockResolvedValue({ resources: [], sections: [] })
    api.listPushSubscriptions.mockReset().mockResolvedValue([])
    vi.mocked(chatApi.fetchChatIntegration).mockReset().mockResolvedValue(chatSettings())
  })
  afterEach(() => { vi.unstubAllGlobals() })

  it('never says Slack in English', async () => {
    await surfaces(workspace('lark'))
  })

  it('never says Slack in Chinese, and calls Feishu 飞书', async () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    const data = workspace('feishu')
    await surfaces(data)
    render(<I18nProvider><TooltipProvider><MemoryRouter><TeamWorkflowSettings data={data} team={data.teams[0]} section="overview" onNavigate={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></MemoryRouter></TooltipProvider></I18nProvider>)
    expect((await screen.findAllByText(/飞书/)).length).toBeGreaterThan(0)
  })

  it('names the platform in settings search and the read-only team notice', () => {
    const translate = (value: string) => value.replaceAll('{name}', 'Lark')
    const titles = createSettingsSearchIndex(chatPlatformSearchPages(SETTINGS_SEARCH_PAGES, 'lark'), translate).search('lark').map(result => translate(result.title))
    expect(titles).toEqual(expect.arrayContaining(['Lark', 'Send initiative updates to Lark groups']))
    const notificationTitles = chatPlatformSearchPages(SETTINGS_SEARCH_PAGES, 'lark').filter(page => page.id === 'notifications' || page.id === 'initiatives').flatMap(page => page.groups?.flatMap(group => group.items?.map(item => item.title) ?? []) ?? [])
    expect(notificationTitles.join(' ')).not.toMatch(/Slack/)
    expect(translate(teamReadOnlyNotice('notifications'))).toBe('Only admins and team owners can enable Lark notifications')
  })
})
