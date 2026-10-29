import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { inviteMembers } from '@/lib/api'
import { makeBootstrap } from '@/test/fixtures'
import { InviteMembersDialog } from './invite-members-dialog'

const fs = await import('node:' + 'fs') as { readFileSync: (path: string, encoding: 'utf8') => string }
const here = import.meta.url
const readStyles = (name: string) => fs.readFileSync(decodeURIComponent(new URL(name, here).pathname), 'utf8')
const inviteCss = readStyles('invite-members-dialog.css')
const lightCss = readStyles('../../styles/light-parity.css')

vi.mock('@/lib/api', () => ({ inviteMembers: vi.fn() }))
vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(inviteMembers).mockResolvedValue([])
})

/** Engineering (team-1), Design (team-2) and Brand (team-3, a sub-team of Design). */
function teams() {
  const [engineering] = makeBootstrap().teams
  return [
    { ...engineering, id: 'team-1', name: 'Engineering', key: 'ENG' },
    { ...engineering, id: 'team-2', name: 'Design', key: 'DES' },
    { ...engineering, id: 'team-3', name: 'Brand', key: 'BRD' },
  ]
}

function renderDialog(props: Partial<Parameters<typeof InviteMembersDialog>[0]> = {}) {
  const data = makeBootstrap()
  const onClose = vi.fn(), onInvited = vi.fn()
  render(<I18nProvider><InviteMembersDialog workspace={data.workspace} teams={data.teams} open onClose={onClose} onInvited={onInvited} {...props}/></I18nProvider>)
  return { onClose, onInvited }
}

describe('InviteMembersDialog', () => {
  it('uses theme tokens for its surface, fields, placeholders and errors, retaining light focus and invalid borders', () => {
    expect(inviteCss).toMatch(/\.dialog-content\.invite-members-dialog\s*\{[^}]*background: var\(--invite-dialog-bg\)/)
    expect(inviteCss).toMatch(/textarea\s*\{[^}]*border: 0\.5px solid var\(--invite-dialog-field-border\)[^}]*background: var\(--invite-dialog-bg\)[^}]*color: var\(--theme-text-primary\)/)
    expect(inviteCss).toMatch(/textarea::placeholder\s*\{\s*color: var\(--theme-text-tertiary\)/)
    expect(inviteCss).toMatch(/\.invite-members-role-trigger\s*\{[^}]*background: var\(--invite-dialog-control-bg\)/)
    expect(inviteCss).toMatch(/span\.is-placeholder\s*\{\s*color: var\(--theme-text-secondary\)/)
    expect(inviteCss).toMatch(/\.invite-members-dialog__error\s*\{[^}]*color: var\(--invite-dialog-error\)/)
    expect(lightCss).toMatch(/html:root\[data-theme="light"\] body \.dialog-content\.invite-members-dialog\s*\{\s*--invite-dialog-error: var\(--connector-dialog-error\)/)
    expect(lightCss).toMatch(/\.dialog-content\.invite-members-dialog textarea:focus\s*\{\s*border-color: var\(--accent-primary\)/)
    expect(lightCss).toMatch(/\.dialog-content\.invite-members-dialog textarea\[aria-invalid="true"\]\s*\{\s*border-color: var\(--invite-dialog-error\)/)
    expect(inviteCss).not.toMatch(/#[\da-f]{3,8}\b|\b(?:rgb|rgba|hsl|hsla|lch|oklch)\(/i)
  })

  it('matches the reference app layout and keeps Send invites disabled until an email is valid', async () => {
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

  it('offers Guest / Member / Admin roles', async () => {
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
  })

  it('lets Members and Admins pick optional teams in a searchable multi-select that closes on each pick', async () => {
    const user = userEvent.setup()
    renderDialog({ teams: teams(), teamSettings: { 'team-3': { parentTeamId: 'team-2' } } })
    const dialog = screen.getByRole('dialog', { name: 'Invite to your workspace' })
    expect(within(dialog).getByText('Add to team')).toBeInTheDocument()
    expect(within(dialog).getByText('(optional)')).toBeInTheDocument()
    expect(within(dialog).queryByText(/only have access to the selected teams/)).not.toBeInTheDocument()
    const picker = within(dialog).getByRole('combobox', { name: 'Add to team' })
    expect(picker).toHaveTextContent('Select teams…')

    await user.click(picker)
    expect(await screen.findByPlaceholderText('Search teams…')).toBeInTheDocument()
    let rows = screen.getAllByRole('option')
    // Sub-teams follow their parent.
    expect(rows.map(row => row.textContent)).toEqual(['EngineeringENG', 'DesignDES', 'BrandBRD'])
    await user.click(rows[1])
    await waitFor(() => expect(screen.queryByPlaceholderText('Search teams…')).not.toBeInTheDocument())
    expect(picker).toHaveTextContent('DES')

    await user.click(picker)
    rows = screen.getAllByRole('option')
    // The chosen team moves to the top, checked.
    expect(rows.map(row => row.textContent)).toEqual(['DesignDES', 'EngineeringENG', 'BrandBRD'])
    expect(rows.map(row => row.getAttribute('aria-checked'))).toEqual(['true', 'false', 'false'])
    await user.type(screen.getByPlaceholderText('Search teams…'), 'BRD')
    await user.click(screen.getAllByRole('option')[0])
    expect(picker).toHaveTextContent('DES, BRD')

    // A role change keeps the teams.
    await user.click(within(dialog).getByRole('combobox', { name: 'Role' }))
    await user.click((await screen.findAllByRole('option'))[2])
    expect(within(dialog).getByRole('combobox', { name: 'Add to team' })).toHaveTextContent('DES, BRD')
    await user.type(within(dialog).getByRole('textbox', { name: 'Email' }), 'ada@example.com')
    await user.click(within(dialog).getByRole('button', { name: 'Send invites' }))
    await waitFor(() => expect(inviteMembers).toHaveBeenCalledWith('workspace', { emails: ['ada@example.com'], role: 'admin', teamIds: ['team-2', 'team-3'] }))
  })

  it('requires a team for guests, with no default', async () => {
    const user = userEvent.setup()
    renderDialog({ teams: teams() })
    const dialog = screen.getByRole('dialog', { name: 'Invite to your workspace' })
    await user.click(within(dialog).getByRole('combobox', { name: 'Role' }))
    await user.click((await screen.findAllByRole('option'))[0])
    expect(within(dialog).queryByText('(optional)')).not.toBeInTheDocument()
    expect(within(dialog).getByText('Guests only have access to the selected teams.')).toBeInTheDocument()
    const picker = within(dialog).getByRole('combobox', { name: 'Add to team' })
    expect(picker).toHaveTextContent('Select teams…')
    await user.type(within(dialog).getByRole('textbox', { name: 'Email' }), 'guest@example.com')
    await user.click(within(dialog).getByRole('button', { name: 'Send invites' }))
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent('Select a team for guest access')
    // The error belongs to the team picker: it sits right under it and the Email field stays valid.
    expect(picker.nextElementSibling).toBe(alert)
    expect(within(dialog).getByRole('textbox', { name: 'Email' })).not.toHaveAttribute('aria-invalid')
    expect(inviteMembers).not.toHaveBeenCalled()
    await user.click(picker)
    await user.click(screen.getAllByRole('option')[0])
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Send invites' }))
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
