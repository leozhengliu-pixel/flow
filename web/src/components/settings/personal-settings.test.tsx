import type { ComponentProps } from 'react'
import { render, screen, waitFor, within } from '@testing-library/react'
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
  createAPIKey: vi.fn(), revokeAPIKey: vi.fn(), rotateAPIKey: vi.fn(), logoutAccount: vi.fn(),
  fetchPasskeys: vi.fn(), beginPasskeyRegistration: vi.fn(), finishPasskeyRegistration: vi.fn(),
  updatePasskey: vi.fn(), deletePasskey: vi.fn(), fetchCommitSigningKey: vi.fn(),
  addCommitSigningKey: vi.fn(), removeCommitSigningKey: vi.fn(),
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
    api.createAPIKey.mockResolvedValue({ key: { id: 'key-1' }, secret: 'flow_test_secret' })
    api.revokeAPIKey.mockResolvedValue(undefined)
    api.rotateAPIKey.mockResolvedValue({ key: { id: 'key-1' }, secret: 'flow_rotated_secret' })
    api.fetchAccountIdentities.mockResolvedValue([])
    api.fetchPasskeys.mockResolvedValue([])
    api.fetchCommitSigningKey.mockResolvedValue(null)
    api.addCommitSigningKey.mockResolvedValue({ name: 'Laptop key', type: 'ssh', fingerprint: 'SHA256:test', addedAt: '2026-09-04T00:00:00.000Z' })
    api.removeCommitSigningKey.mockResolvedValue(undefined)
    api.deletePasskey.mockResolvedValue(undefined)
    api.logoutAccount.mockResolvedValue(undefined)
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

  it('renders the complete security inventory and supports session revocation', async () => {
    const user = userEvent.setup()
    const sessions = [
      { id: 'session-current', current: true, createdAt: '2026-08-01T00:00:00.000Z', lastSeenAt: '2026-09-04T06:30:00.000Z', expiresAt: '2026-10-04T06:30:00.000Z' },
      { id: 'session-other-1', current: false, createdAt: '2026-08-02T00:00:00.000Z', lastSeenAt: '2026-08-20T10:00:00.000Z', expiresAt: '2026-10-05T06:30:00.000Z' },
      { id: 'session-other-2', current: false, createdAt: '2026-08-03T00:00:00.000Z', lastSeenAt: '2026-08-19T10:00:00.000Z', expiresAt: '2026-10-06T06:30:00.000Z' },
    ]
    api.fetchAccountSessions.mockResolvedValue(sessions)
    api.revokeAccountSession.mockResolvedValue(undefined)
    api.revokeOtherAccountSessions.mockResolvedValue(undefined)
    render(<MemoryRouter><I18nProvider><PersonalSettings {...props('account-security')}/></I18nProvider></MemoryRouter>)
    expect(await screen.findByRole('heading', { name: 'Security & access' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Sessions' })).toBeVisible()
    expect(screen.getByRole('heading', { name: '2 other sessions' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Passkeys' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Personal API keys' })).toBeVisible()
    expect(screen.getByText('No passkeys registered')).toBeVisible()
    expect(screen.getByText('No API keys created')).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Commit signing key' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Authorized applications' })).toBeVisible()
    const revokeButtons = screen.getAllByRole('button', { name: 'Revoke' })
    expect(revokeButtons).toHaveLength(2)
    await user.click(revokeButtons[0])
    const revokeDialog = await screen.findByRole('dialog')
    await user.click(within(revokeDialog).getByRole('button', { name: 'Revoke' }))
    expect(api.revokeAccountSession).toHaveBeenCalledWith('session-other-1')
    await user.click(screen.getByRole('button', { name: 'Revoke all' }))
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent('Revoke all other sessions?')
    await user.click(within(dialog).getByRole('button', { name: 'Revoke all' }))
    expect(api.revokeOtherAccountSessions).toHaveBeenCalledOnce()
  })

  it('keeps security empty states actionable and localizes them', async () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    api.fetchAccountSessions.mockResolvedValue([])
    const input = props('account-security')
    render(<MemoryRouter><I18nProvider><PersonalSettings {...input}/></I18nProvider></MemoryRouter>)
    expect(await screen.findByRole('heading', { name: '安全与访问' })).toBeVisible()
    expect(screen.getByRole('heading', { name: '通行密钥' })).toBeVisible()
    expect(screen.getByText('尚未注册通行密钥')).toBeVisible()
    expect(screen.getByRole('heading', { name: '个人 API 密钥' })).toBeVisible()
    expect(screen.getByText('尚未创建 API 密钥')).toBeVisible()
    expect(screen.getByRole('button', { name: '新建 API 密钥' })).toBeVisible()
    expect(screen.getByRole('button', { name: '添加密钥' })).toBeEnabled()
    await userEvent.setup().click(screen.getByRole('button', { name: '新建 API 密钥' }))
    expect(input.onNavigate).toHaveBeenCalledWith('api')
  })

  it('completes the API key creation flow with scoped team access', async () => {
    const user = userEvent.setup()
    const input = props('account-security')
    input.apiKeyMode = 'new'
    render(<MemoryRouter><I18nProvider><PersonalSettings {...input}/></I18nProvider></MemoryRouter>)
    expect(screen.getByRole('heading', { name: 'Create API key' })).toBeVisible()

    await user.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Key name is required')

    await user.type(screen.getByRole('textbox', { name: 'Key name' }), 'CI integration')
    await user.click(screen.getByRole('radio', { name: 'Only select permissions…' }))
    await user.click(screen.getAllByRole('checkbox', { name: /Read/ })[0])
    await user.click(screen.getByRole('radio', { name: 'Only select teams…' }))
    const teams = screen.getByRole('combobox', { name: 'Select teams…' })
    await user.click(teams)
    await user.click(await screen.findByRole('option', { name: 'Test team' }))
    await user.click(screen.getByRole('button', { name: 'Create' }))

    await waitFor(() => expect(api.createAPIKey).toHaveBeenCalledWith(expect.objectContaining({
      name: 'CI integration', scopes: ['read'], teamIds: ['team-1'], teamRestriction: 'selected',
    })))
    expect(await screen.findByRole('heading', { name: 'API key created' })).toBeVisible()
    expect(screen.getByRole('textbox', { name: 'API key' })).toHaveValue('flow_test_secret')
    expect(screen.getByRole('button', { name: 'Done' })).toBeVisible()
  })

  it('uses a dedicated signing-key route and uploads the key form', async () => {
    const user = userEvent.setup()
    const input = props('account-security')
    input.signingKeyMode = 'new'
    api.fetchCommitSigningKey.mockResolvedValue(null)
    render(<MemoryRouter><I18nProvider><PersonalSettings {...input}/></I18nProvider></MemoryRouter>)

    expect(await screen.findByRole('heading', { name: 'Add commit signing key' })).toBeVisible()
    const name = screen.getByRole('textbox', { name: 'Key name' })
    const privateKey = screen.getByRole('textbox', { name: 'Private key' })
    expect(screen.getByRole('button', { name: 'Upload key' })).toBeDisabled()
    await user.type(name, 'Laptop key')
    await user.type(privateKey, '-----BEGIN OPENSSH PRIVATE KEY-----\\nmock\\n-----END OPENSSH PRIVATE KEY-----')
    await user.click(screen.getByRole('button', { name: 'Upload key' }))

    await waitFor(() => expect(api.addCommitSigningKey).toHaveBeenCalledWith({
      name: 'Laptop key',
      privateKey: '-----BEGIN OPENSSH PRIVATE KEY-----\\nmock\\n-----END OPENSSH PRIVATE KEY-----',
    }))
    expect(input.onReload).toHaveBeenCalled()
    expect(input.onNavigate).toHaveBeenCalledWith('account-security')
  })
})
