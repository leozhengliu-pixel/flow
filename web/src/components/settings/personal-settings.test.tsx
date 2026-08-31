import type { ComponentProps } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { SettingsPageId } from '@/lib/app-routes'
import { makeBootstrap, viewer } from '@/test/fixtures'
import { MemoryRouter } from 'react-router-dom'

const api = vi.hoisted(() => ({
  updateAccountProfile: vi.fn(), removeMember: vi.fn(), fetchAccountIdentities: vi.fn(), fetchAccountSessions: vi.fn(),
  listPushSubscriptions: vi.fn(), deletePushSubscription: vi.fn(), revokeAccountSession: vi.fn(),
  revokeOtherAccountSessions: vi.fn(), revokeOAuthAuthorization: vi.fn(), updateNotificationPreferences: vi.fn(),
  unlinkAccountIdentity: vi.fn(),
}))
vi.mock('@/lib/api', () => api)

import { PersonalSettings } from './personal-settings'

const values = {
  homeView: 'Flow Agent (default)', displayNames: 'Full name', firstDay: 'Monday', emoticons: true,
  sendComments: 'Enter', fontSize: 'Default', pointerCursor: false, underlineLinks: false,
  disableAnimatedImages: false, interfaceTheme: 'System preference', desktopLinks: false,
  autoAssign: false, assignStarted: false,
}

function props(page: SettingsPageId) {
  return {
    page,
    data: makeBootstrap({
      userSettings: { [viewer.id]: { userId: viewer.id, username: viewer.name, jobTitle: 'Engineer' } } as never,
      apiKeys: [], agentSkills: [], integrationConnections: [], oauthAuthorizations: [],
    }),
    values,
    setValue: vi.fn(), onNavigate: vi.fn(), onReload: vi.fn().mockResolvedValue(undefined),
    onBack: vi.fn(), onCustomizeSidebar: vi.fn(),
  } as unknown as ComponentProps<typeof PersonalSettings>
}

describe('personal settings workflows', () => {
  beforeEach(() => {
    localStorage.removeItem('flow:locale')
    Object.values(api).forEach(mock => mock.mockReset())
    api.updateAccountProfile.mockResolvedValue({})
    api.removeMember.mockResolvedValue(undefined)
    api.listPushSubscriptions.mockResolvedValue([])
    api.fetchAccountSessions.mockResolvedValue([])
    api.fetchAccountIdentities.mockResolvedValue([])
    api.updateNotificationPreferences.mockImplementation(async value => value)
  })

  it('updates preference toggles, menus, and sidebar customization', async () => {
    const user = userEvent.setup()
    const input = props('preferences')
    render(<I18nProvider><PersonalSettings {...input}/></I18nProvider>)
    await user.click(screen.getByRole('checkbox', { name: 'Auto-assign to self' }))
    expect(input.setValue).toHaveBeenCalledWith('autoAssign', true)
    screen.getByRole('combobox', { name: 'Default home view' }).focus()
    await user.keyboard('{Enter}')
    await user.click(await screen.findByRole('option', { name: 'Inbox' }))
    expect(input.setValue).toHaveBeenCalledWith('homeView', 'Inbox')
    await user.click(screen.getByRole('button', { name: 'Customize' }))
    expect(input.onCustomizeSidebar).toHaveBeenCalledOnce()
  })

  it('validates and persists profile edits and workspace departure', async () => {
    const user = userEvent.setup()
    const input = props('profile')
    render(<I18nProvider><PersonalSettings {...input}/></I18nProvider>)
    const fullName = screen.getByRole('textbox', { name: 'Full name' })
    await user.clear(fullName)
    await user.type(fullName, 'Updated Viewer')
    await user.click(screen.getByRole('button', { name: 'Update' }))
    await waitFor(() => expect(api.updateAccountProfile).toHaveBeenCalledWith(expect.objectContaining({ displayName: 'Updated Viewer', username: viewer.name, jobTitle: 'Engineer' })))
    expect(input.onReload).toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Leave workspace' }))
    await user.click(await screen.findByRole('button', { name: 'Leave workspace' }))
    await waitFor(() => expect(api.removeMember).toHaveBeenCalledWith('workspace', viewer.id))
    expect(input.onBack).toHaveBeenCalled()
  })

  it('localizes every personal page and its portal controls in Chinese', async () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    const user = userEvent.setup()
    const view = render(<I18nProvider><PersonalSettings {...props('preferences')}/></I18nProvider>)
    expect(screen.getByRole('heading', { name: '偏好设置' })).toBeInTheDocument()
    await user.click(screen.getByRole('combobox', { name: '每周第一天' }))
    expect(await screen.findByRole('option', { name: '星期六' })).toBeInTheDocument()
    await user.keyboard('{Escape}')

    view.rerender(<I18nProvider><PersonalSettings {...props('notifications')}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: /桌面端/ }))
    expect(await screen.findByRole('heading', { name: '桌面通知' })).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: '通知声音' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '完成' }))

    view.rerender(<I18nProvider><PersonalSettings {...props('code-and-reviews')}/></I18nProvider>)
    await user.click(screen.getByRole('combobox', { name: 'Git 附件格式' }))
    expect(await screen.findByRole('option', { name: '标题' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '标题和 URL' })).toBeInTheDocument()
  })

  it.each([
    ['profile', '个人资料'],
    ['account-security', '安全与访问'],
    ['connections', '已连接账户'],
    ['agents', '智能助手个性化'],
  ] as const)('localizes the %s page heading', async (page, heading) => {
    localStorage.setItem('flow:locale', 'zh-CN')
    render(<MemoryRouter><I18nProvider><PersonalSettings {...props(page)}/></I18nProvider></MemoryRouter>)
    expect(await screen.findByRole('heading', { name: heading })).toBeInTheDocument()
  })
})
