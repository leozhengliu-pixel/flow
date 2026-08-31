import type { ComponentProps } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'

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

function props(page: 'preferences' | 'profile') {
  return {
    page,
    data: makeBootstrap({ userSettings: { [viewer.id]: { userId: viewer.id, username: viewer.name, jobTitle: 'Engineer' } } as never }),
    values,
    setValue: vi.fn(), onNavigate: vi.fn(), onReload: vi.fn().mockResolvedValue(undefined),
    onBack: vi.fn(), onCustomizeSidebar: vi.fn(),
  } as unknown as ComponentProps<typeof PersonalSettings>
}

describe('personal settings workflows', () => {
  beforeEach(() => {
    Object.values(api).forEach(mock => mock.mockReset())
    api.updateAccountProfile.mockResolvedValue({})
    api.removeMember.mockResolvedValue(undefined)
    api.listPushSubscriptions.mockResolvedValue([])
  })

  it('updates preference toggles, menus, and sidebar customization', async () => {
    const user = userEvent.setup()
    const input = props('preferences')
    render(<I18nProvider><PersonalSettings {...input}/></I18nProvider>)
    await user.click(screen.getByRole('checkbox', { name: 'Auto-assign to self' }))
    expect(input.setValue).toHaveBeenCalledWith('autoAssign', true)
    await user.click(screen.getByRole('button', { name: 'Default home view' }))
    await user.click(await screen.findByText('Inbox'))
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
})
