import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { ApiError } from '@/lib/api-client'
import * as api from '@/lib/chat-integrations-api'
import { rememberChatPlatform, resetRememberedChatPlatform } from '@/lib/chat-platform'
import { viewer } from '@/test/fixtures'
import type { ChatInitiativeBindings } from '@/types/chat-integrations'
import type { Initiative } from '@/types/flow'

import { InitiativeChatGroupDialog } from './initiative-chat-group-dialog'
import { InitiativeNotificationMenu } from './initiative-header-menus'

vi.mock('@/lib/chat-integrations-api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/chat-integrations-api')>(),
  fetchChatInitiativeBindings: vi.fn(),
  fetchChatIntegrationChats: vi.fn(),
  saveChatInitiativeBinding: vi.fn(),
  revokeChatInitiativeBinding: vi.fn(),
  removeChatInitiativeBinding: vi.fn(),
}))
vi.mock('@/components/ui/action-dialog-service', () => ({ confirmAction: vi.fn().mockResolvedValue(true) }))
vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('@/lib/pulse-subscriptions', () => ({ usePulseSubscription: () => ({ subscribed: false, saving: false, toggle: vi.fn() }) }))

const initiative = {
  id: 'initiative-1', name: 'Company launch', slugId: 'company-launch-1', summary: '', description: '', color: '#5e6ad2', status: 'active',
  priority: 0, priorityLabel: 'No priority', health: 'onTrack', owner: viewer, creator: viewer, contributingTeamIds: [], labelIds: [], projectIds: [],
  resources: [], comments: [], favorite: false, subscribed: false,
} as unknown as Initiative

const listing = (overrides: Partial<ChatInitiativeBindings> = {}): ChatInitiativeBindings => ({ bindings: [], canManage: true, shareable: true, tenantAuthorized: true, ...overrides })

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  localStorage.setItem('flow:locale', 'en-US')
  vi.clearAllMocks()
  vi.mocked(api.fetchChatInitiativeBindings).mockResolvedValue(listing())
  vi.mocked(api.fetchChatIntegrationChats).mockResolvedValue({ items: [{ id: 'oc_launch', name: 'Launch room', external: false }, { id: 'oc_partner', name: 'Partners', external: true }], hasMore: false })
  vi.mocked(api.saveChatInitiativeBinding).mockResolvedValue({ id: 'b', connectionId: 'c', chatId: 'oc_launch', chatName: 'Launch room', initiativeId: 'initiative-1', enabled: true })
  vi.mocked(api.revokeChatInitiativeBinding).mockResolvedValue({ id: 'b', connectionId: 'c', chatId: 'oc_launch', chatName: 'Launch room', initiativeId: 'initiative-1', enabled: false })
})
afterEach(() => { vi.unstubAllGlobals(); resetRememberedChatPlatform() })

function dialog() {
  return render(<MemoryRouter><I18nProvider><InitiativeChatGroupDialog open onOpenChange={vi.fn()} provider="lark" initiative={initiative} users={[viewer]}/></I18nProvider></MemoryRouter>)
}

it("lets the initiative's owner connect a group with explicit approval, in Chinese", async () => {
  localStorage.setItem('flow:locale', 'zh-CN')
  dialog()
  const user = userEvent.setup()
  expect(await screen.findByText('连接一个 Lark 群，接收此计划的更新。')).toBeInTheDocument()
  await user.click(await screen.findByRole('button', { name: '连接 Lark 群' }))
  const form = await screen.findByRole('form', { name: '添加群绑定' })
  // The group list is requested for this initiative, so its owner may read it.
  await waitFor(() => expect(api.fetchChatIntegrationChats).toHaveBeenCalledWith('lark', undefined, 'initiative-1'))
  await user.click(within(form).getByRole('combobox', { name: '群' }))
  await user.click(await screen.findByRole('option', { name: 'Launch room' }))
  expect(within(form).getByText('该群只会收到此计划的更新，不会分享其团队的任何私有工作内容，连接也不会改变谁能在 Flow 中查看此计划。')).toBeInTheDocument()
  const save = within(form).getByRole('button', { name: '保存群绑定' })
  expect(save).toBeDisabled()
  await user.click(within(form).getByRole('checkbox', { name: '我同意将此计划的更新分享给该群中的所有人。' }))
  await user.click(save)
  await waitFor(() => expect(api.saveChatInitiativeBinding).toHaveBeenCalledWith('lark', { initiativeId: 'initiative-1', chatId: 'oc_launch', chatName: 'Launch room', enabled: true, authorize: true }))
})

it('lists connected groups with their authorization and revokes them', async () => {
  vi.mocked(api.fetchChatInitiativeBindings).mockResolvedValue(listing({ bindings: [{ id: 'binding-1', connectionId: 'c', chatId: 'oc_launch', chatName: 'Launch room', initiativeId: 'initiative-1', enabled: true, authorization: { userId: viewer.id, role: 'initiative_owner', authorizedAt: '2026-10-20T00:00:00Z' } }] }))
  dialog()
  const user = userEvent.setup()
  expect(await screen.findByText('Launch room')).toBeInTheDocument()
  expect(screen.getByText(/Authorized by/)).toHaveTextContent(viewer.displayName || viewer.name)
  await user.click(screen.getByRole('button', { name: 'Revoke group binding: Launch room' }))
  await waitFor(() => expect(api.revokeChatInitiativeBinding).toHaveBeenCalledWith('lark', 'binding-1'))
})

it.each([
  [listing({ canManage: false }), "Only the initiative's owner and workspace administrators can connect it to groups."],
  [listing({ shareable: false }), "Initiatives led by a private team can't be connected to a group."],
])('explains why the group cannot be connected', async (state, message) => {
  vi.mocked(api.fetchChatInitiativeBindings).mockResolvedValue(state)
  dialog()
  expect(await screen.findByText(message)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Connect Lark group' })).not.toBeInTheDocument()
})

it('points to the integration while the application is not set up', async () => {
  vi.mocked(api.fetchChatInitiativeBindings).mockRejectedValue(new ApiError('Chat application not configured', 404, 'chat_not_configured'))
  dialog()
  expect(await screen.findByRole('link', { name: 'Connect Lark' })).toHaveAttribute('href', expect.stringContaining('/settings/integrations/lark'))
})

it("opens the initiative's groups from its notifications menu", async () => {
  rememberChatPlatform({ chatPlatform: 'lark' })
  render(<MemoryRouter><I18nProvider><InitiativeNotificationMenu initiative={initiative} onUpdate={vi.fn()}/></I18nProvider></MemoryRouter>)
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Setup initiative notifications' }))
  await user.click(await screen.findByRole('button', { name: 'Connect' }))
  expect(await screen.findByRole('dialog')).toHaveTextContent('Lark notifications for')
  await waitFor(() => expect(api.fetchChatInitiativeBindings).toHaveBeenCalledWith('lark', 'initiative-1'))
})
