import type { ComponentProps } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { ChatIntegrationSettings } from '@/types/chat-integrations'

const api = vi.hoisted(() => ({ listPushSubscriptions: vi.fn(), deletePushSubscription: vi.fn(), updateNotificationPreferences: vi.fn() }))
vi.mock('@/lib/api', () => api)
const chatApi = vi.hoisted(() => ({ fetchChatIntegration: vi.fn() }))
vi.mock('@/lib/chat-integrations-api', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/chat-integrations-api')>(), ...chatApi }))

import { PersonalSettings } from './personal-settings'

const chatSettings = (overrides: Partial<ChatIntegrationSettings> = {}): ChatIntegrationSettings => ({
  connection: { id: 'connection-1', provider: 'lark', name: 'Lark', status: 'connected', config: {}, scopes: [], channels: [], linkbackEnabled: false, deliveryAttempts: 0, connectedBy: viewer.id, createdAt: '', updatedAt: '' },
  permissions: { tenant: { status: 'authorized', tenantKey: 'tenant-1' }, workspace: { canManage: false }, teams: { manageableTeamIds: [], privateTeamIds: [] } },
  identity: { userId: viewer.id, openId: 'ou_viewer', tenantKey: 'tenant-1' },
  bindings: [], credentialsConfigured: true, webhookURL: '', canManage: false,
  ...overrides,
})

function show(platform: 'lark' | 'feishu' | 'slack', channel?: 'chat') {
  const onNavigate = vi.fn()
  const input = {
    page: 'notifications', notificationChannel: channel,
    data: makeBootstrap({ chatPlatform: platform, integrationConnections: [] } as never),
    values: {}, setValue: vi.fn(), onNavigate, onReload: vi.fn().mockResolvedValue(undefined), onBack: vi.fn(), onCustomizeSidebar: vi.fn(),
  } as unknown as ComponentProps<typeof PersonalSettings>
  render(<I18nProvider><PersonalSettings {...input}/></I18nProvider>)
  return { onNavigate }
}

describe('personal Lark and Feishu notifications', () => {
  beforeEach(() => {
    localStorage.removeItem('flow:locale')
    Object.values(api).forEach(mock => mock.mockReset())
    chatApi.fetchChatIntegration.mockReset()
    api.listPushSubscriptions.mockResolvedValue([])
    api.updateNotificationPreferences.mockImplementation(async value => value)
    chatApi.fetchChatIntegration.mockResolvedValue(chatSettings())
  })

  it('turns personal notifications on and chooses their types', async () => {
    const user = userEvent.setup()
    show('lark')
    const row = await screen.findByRole('button', { name: /Lark.*Disabled/ })
    expect(chatApi.fetchChatIntegration).toHaveBeenCalledWith('lark')
    await user.click(row)
    expect(await screen.findByRole('heading', { name: 'Lark' })).toBeInTheDocument()
    expect(screen.getByText('Direct messages from the Lark app bot to your linked account')).toBeInTheDocument()
    const enable = screen.getByRole('checkbox', { name: 'Enable Lark notifications' })
    expect(enable).toBeEnabled()
    expect(screen.getByRole('checkbox', { name: 'Comments and replies' })).toBeDisabled()
    await user.click(enable)
    await waitFor(() => expect(api.updateNotificationPreferences).toHaveBeenCalled())
    expect(api.updateNotificationPreferences.mock.calls.at(-1)?.[0].chat).toMatchObject({ enabled: true, categories: { comments: true, assignments: true } })
    await user.click(await screen.findByRole('checkbox', { name: 'Comments and replies' }))
    await waitFor(() => expect(api.updateNotificationPreferences.mock.calls.at(-1)?.[0].chat.categories.comments).toBe(false))
    expect(api.updateNotificationPreferences.mock.calls.at(-1)?.[0].chat.enabled).toBe(true)
  })

  it('asks a member to link their account first', async () => {
    chatApi.fetchChatIntegration.mockResolvedValue(chatSettings({ identity: null }))
    const user = userEvent.setup()
    const { onNavigate } = show('lark', 'chat')
    expect(await screen.findByText('Link your Lark account to turn this on')).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: 'Enable Lark notifications' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Connected accounts' }))
    expect(onNavigate).toHaveBeenCalledWith('connections')
  })

  it('shows when the app is not available to the member or not approved yet', async () => {
    chatApi.fetchChatIntegration.mockResolvedValue(chatSettings({ identity: { userId: viewer.id, openId: 'ou_viewer', tenantKey: 'tenant-1', personalDelivery: 'unavailable' } }))
    show('lark')
    expect(await screen.findByText("The app isn't available to your Lark account. Ask your Lark admin to add you to its availability range.")).toBeInTheDocument()
  })

  it('waits for the app to be approved', async () => {
    chatApi.fetchChatIntegration.mockResolvedValue(chatSettings({ permissions: { tenant: { status: 'not_approved', errorCode: 'chat_tenant_not_approved' }, workspace: { canManage: false }, teams: { manageableTeamIds: [], privateTeamIds: [] } } }))
    show('lark', 'chat')
    expect(await screen.findByText('Waiting for the app to be approved')).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: 'Enable Lark notifications' })).toBeDisabled()
  })

  it('names Feishu 飞书 in Chinese', async () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    chatApi.fetchChatIntegration.mockResolvedValue(chatSettings({ identity: null }))
    show('feishu')
    expect(await screen.findByText('关联你的 飞书 账号后即可开启')).toBeInTheDocument()
    expect(chatApi.fetchChatIntegration).toHaveBeenCalledWith('feishu')
  })

  it('keeps the Slack row as it was', async () => {
    show('slack')
    expect(await screen.findByRole('button', { name: /Slack/ })).toBeDisabled()
    expect(chatApi.fetchChatIntegration).not.toHaveBeenCalled()
  })
})
