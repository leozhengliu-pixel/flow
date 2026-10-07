import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { Initiative, User } from '@/types/flow'
import { InitiativeRowMenu, type InitiativeRowMenuProps } from './initiative-row-menu'

const viewer = { id: 'user-1', name: 'Dev User', displayName: 'Dev User', email: 'dev@flow.test' } as User
const initiative = {
  id: 'initiative-1', slugId: 'ini-1', name: 'Roadmap', summary: '', description: '', icon: 'Initiative', color: '#d6a526', status: 'active', priority: 2, priorityLabel: 'High',
  health: 'noUpdate', creator: viewer, owner: viewer, contributingTeamIds: [], labelIds: [], projectIds: [], resources: [], comments: [], favorite: false, subscribed: false,
  notificationRules: { descriptionChanges: true, newUpdate: true, allProjectUpdates: false }, updateSchedule: { cadence: 'none', weekday: 1, timeRange: '09:00-12:00' },
  descriptionHistory: [], createdAt: '', updatedAt: '', targetDate: '2026-10-01', targetDateResolution: 'quarter',
} as Initiative

function renderMenu(overrides: Partial<InitiativeRowMenuProps> = {}) {
  const props: InitiativeRowMenuProps = {
    children: <a data-linear-menu-row="" href="/i" role="row">Roadmap</a>,
    initiative, href: '/ws/initiative/ini-1/overview', initiatives: [initiative, { ...initiative, id: 'initiative-2', name: 'Platform' }], canParent: (child, parent) => child !== parent,
    users: [viewer], teams: [], labels: [{ id: 'label-1', name: 'Bet', color: '#f00' }],
    onCreateLabel: vi.fn(), onCreateReminder: vi.fn().mockResolvedValue(undefined), onCreateSubInitiative: vi.fn(), onDelete: vi.fn(), onEdit: vi.fn(),
    onNewComment: vi.fn(), onNewUpdate: vi.fn(), onUpdate: vi.fn(), onUpdateInitiative: vi.fn(), ...overrides,
  }
  render(<I18nProvider><InitiativeRowMenu {...props}/></I18nProvider>)
  fireEvent.contextMenu(screen.getByRole('row', { name: 'Roadmap' }), { clientX: 40, clientY: 40 })
  return props
}

describe('InitiativeRowMenu', () => {
  it('lists Linear\'s initiative row menu in order with shared, left-aligned rows', () => {
    renderMenu()
    const menu = screen.getByRole('menu', { name: 'Initiative actions' })
    const rows = [...menu.querySelectorAll<HTMLElement>(':scope > [role=menuitem], :scope > [role=separator]')]
    expect(rows.map(row => row.getAttribute('role') === 'separator' ? '—' : row.dataset.menuItem)).toEqual([
      'Edit', 'Status', 'Priority', 'Owner', 'Lead team', 'Target date…', 'Parent initiatives', 'Sub-initiatives', 'Labels', '—',
      'Copy', '—', 'Favorite', 'Subscribe', 'Remind me', '—', 'New initiative update', 'New comment…', '—', 'Delete',
    ])
    for (const row of rows.filter(row => row.getAttribute('role') === 'menuitem')) {
      expect(row).toHaveClass('linear-menu__row')
      expect(row.firstElementChild).toHaveClass('linear-menu__icon')
    }
    const hint = (name: string) => rows.find(row => row.dataset.menuItem === name)?.querySelector('[data-shortcut]')?.getAttribute('data-shortcut')
    expect(['Status', 'Owner', 'Target date…', 'Labels', 'Favorite', 'Remind me', 'New initiative update', 'New comment…'].map(hint)).toEqual(['S', 'N then O', 'Ctrl ⌥ D', 'N then L', '⌥ F', '⇧ H', 'N then U', 'N then C'])
    expect(rows.find(row => row.dataset.menuItem === 'Target date…')?.querySelector('.linear-menu__detail')?.textContent).toBe('Q4 2026')
    expect(rows.find(row => row.dataset.menuItem === 'Target date…')?.querySelector('.linear-menu__marker')).toBeNull()
    expect(rows.find(row => row.dataset.menuItem === 'Lead team')?.querySelector('.linear-menu__marker')?.textContent).toBe('▶')
    // Hints use the menu font, not monospace keycaps.
    expect(rows.find(row => row.dataset.menuItem === 'Owner')?.querySelector('.linear-menu__shortcut kbd')?.textContent).toBe('N')
  })

  it('applies status and priority from their submenus', async () => {
    const user = userEvent.setup()
    const props = renderMenu()
    await user.click(screen.getByRole('menuitem', { name: /^Status/ }))
    expect(screen.getByRole('menu', { name: 'Status' }).querySelector('input')).toHaveAttribute('placeholder', 'Change status…')
    await user.click(screen.getByRole('menuitem', { name: /Completed/ }))
    expect(props.onUpdate).toHaveBeenCalledWith({ status: 'completed' })
    fireEvent.contextMenu(screen.getByRole('row', { name: 'Roadmap' }), { clientX: 40, clientY: 40 })
    await user.click(screen.getByRole('menuitem', { name: /^Priority/ }))
    await user.click(screen.getByRole('menuitem', { name: /Urgent/ }))
    expect(props.onUpdate).toHaveBeenCalledWith({ priority: 1 })
  })

  it('runs key hints: S opens Status, ⌥ F favorites, N then U starts an update', async () => {
    const props = renderMenu()
    const menu = () => screen.getByRole('menu', { name: 'Initiative actions' })
    fireEvent.keyDown(menu(), { key: 's', code: 'KeyS' })
    expect(await screen.findByRole('menu', { name: 'Status' })).toBeInTheDocument()
    fireEvent.keyDown(screen.getByRole('menu', { name: 'Status' }).querySelector('input')!, { key: '5', code: 'Digit5' })
    expect(props.onUpdate).toHaveBeenCalledWith({ status: 'canceled' })

    fireEvent.contextMenu(screen.getByRole('row', { name: 'Roadmap' }), { clientX: 40, clientY: 40 })
    fireEvent.keyDown(menu(), { key: 'ƒ', code: 'KeyF', altKey: true })
    expect(props.onUpdate).toHaveBeenCalledWith({ favorite: true })

    fireEvent.contextMenu(screen.getByRole('row', { name: 'Roadmap' }), { clientX: 40, clientY: 40 })
    fireEvent.keyDown(menu(), { key: 'n', code: 'KeyN' })
    fireEvent.keyDown(menu(), { key: 'u', code: 'KeyU' })
    expect(props.onNewUpdate).toHaveBeenCalled()
  })

  it('sets parent initiatives and adds an existing sub-initiative', async () => {
    const user = userEvent.setup()
    const props = renderMenu()
    await user.click(screen.getByRole('menuitem', { name: /^Parent initiatives/ }))
    await user.click(screen.getByRole('menuitemcheckbox', { name: /Platform/ }))
    expect(props.onUpdate).toHaveBeenCalledWith({ parentInitiativeIds: ['initiative-2'] })
    await user.keyboard('{Escape}')
    fireEvent.contextMenu(screen.getByRole('row', { name: 'Roadmap' }), { clientX: 40, clientY: 40 })
    await user.click(screen.getByRole('menuitem', { name: /^Sub-initiatives/ }))
    await user.click(screen.getByRole('menuitem', { name: /Add existing/ }))
    await user.click(screen.getByRole('menuitemcheckbox', { name: /Platform/ }))
    expect(props.onUpdateInitiative).toHaveBeenCalledWith('initiative-2', { parentInitiativeIds: ['initiative-1'] })
  })
})
