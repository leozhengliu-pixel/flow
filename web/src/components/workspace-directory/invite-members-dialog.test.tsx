import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { inviteMembers } from '@/lib/api'
import { makeBootstrap } from '@/test/fixtures'
import { InviteMembersDialog } from './invite-members-dialog'

vi.mock('@/lib/api', () => ({ inviteMembers: vi.fn() }))
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(inviteMembers).mockResolvedValue([])
})

function renderDialog(props: Partial<Parameters<typeof InviteMembersDialog>[0]> = {}) {
  const data = makeBootstrap()
  const onClose = vi.fn(), onInvited = vi.fn()
  render(<I18nProvider><InviteMembersDialog workspace={data.workspace} teams={data.teams} open onClose={onClose} onInvited={onInvited} {...props}/></I18nProvider>)
  return { onClose, onInvited }
}

describe('InviteMembersDialog', () => {
  it('matches the Linear layout and keeps Send invites disabled until an email is valid', async () => {
    const user = userEvent.setup()
    const { onClose, onInvited } = renderDialog()
    const dialog = screen.getByRole('dialog', { name: 'Invite to your workspace' })
    expect(within(dialog).getByRole('button', { name: 'Close modal dialog' })).toBeInTheDocument()
    const email = within(dialog).getByRole('textbox', { name: 'Email' })
    expect(email).toHaveFocus()
    expect(email).toHaveAttribute('placeholder', 'email@gmail.com, email2@gmail.com…')
    expect(within(dialog).getByRole('combobox', { name: 'Role' })).toHaveTextContent('Member - Full access with limited permissions')
    const send = within(dialog).getByRole('button', { name: 'Send invites' })
    expect(send).toBeDisabled()
    await user.type(email, 'not-an-email')
    expect(send).toBeDisabled()
    await user.clear(email)
    await user.type(email, 'ada@example.com, grace@example.com')
    expect(send).toBeEnabled()
    await user.click(send)
    await waitFor(() => expect(inviteMembers).toHaveBeenCalledWith('workspace', { emails: ['ada@example.com', 'grace@example.com'], role: 'member', teamIds: [] }))
    await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
    expect(onInvited).toHaveBeenCalledWith([])
  })

  it('offers Guest / Member / Admin roles and scopes guests to a team', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.click(screen.getByRole('combobox', { name: 'Role' }))
    const options = await screen.findAllByRole('option')
    expect(options.map(option => option.textContent)).toEqual([
      'Guest - Limited access to teams',
      'Member - Full access with limited permissions',
      'Admin - Full administrative access',
    ])
    expect(options.map(option => option.getAttribute('aria-selected'))).toEqual(['false', 'true', 'false'])
    await user.click(options[0])
    expect(screen.getByRole('combobox', { name: 'Role' })).toHaveTextContent('Guest - Limited access to teams')
    await user.type(screen.getByRole('textbox', { name: 'Email' }), 'guest@example.com')
    await user.click(screen.getByRole('button', { name: 'Send invites' }))
    await waitFor(() => expect(inviteMembers).toHaveBeenCalledWith('workspace', { emails: ['guest@example.com'], role: 'guest', teamIds: ['team-1'] }))
  })

  it('reports invalid entries instead of silently dropping them', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.type(screen.getByRole('textbox', { name: 'Email' }), 'ada@example.com nope')
    await user.click(screen.getByRole('button', { name: 'Send invites' }))
    expect(screen.getByRole('alert')).toHaveTextContent('nope')
    expect(inviteMembers).not.toHaveBeenCalled()
  })
})
