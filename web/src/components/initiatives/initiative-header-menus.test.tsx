import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { Initiative, User } from '@/types/flow'
import { InitiativeActionsMenu, type InitiativeActionsMenuProps } from './initiative-header-menus'

const viewer = { id: 'user-1', name: 'Dev User', displayName: 'Dev User', email: 'dev@flow.test' } as User
const initiative = {
  id: 'initiative-1', slugId: 'ini-1', name: 'Roadmap', summary: '', description: '', icon: 'Initiative', color: '#d6a526', status: 'active', priority: 2, priorityLabel: 'High',
  health: 'noUpdate', creator: viewer, contributingTeamIds: [], labelIds: [], projectIds: [], resources: [], comments: [], favorite: false, subscribed: false,
  notificationRules: { descriptionChanges: true, newUpdate: true, allProjectUpdates: false }, updateSchedule: { cadence: 'none', weekday: 1, timeRange: '09:00-12:00' },
  descriptionHistory: [], createdAt: '', updatedAt: '',
} as Initiative

function renderMenu() {
  const props: InitiativeActionsMenuProps = {
    initiative, initiatives: [initiative, { ...initiative, id: 'initiative-2', name: 'Platform' }], users: [viewer], teams: [], labels: [], viewer,
    onCreateInitiative: vi.fn(), onCreateLabel: vi.fn(), onCreateReminder: vi.fn().mockResolvedValue(undefined), onDelete: vi.fn(), onNewUpdate: vi.fn(),
    onShowActivity: vi.fn(), onUpdate: vi.fn(), onUpdateInitiative: vi.fn(),
  }
  render(<I18nProvider><InitiativeActionsMenu {...props}/></I18nProvider>)
  return props
}

describe('InitiativeActionsMenu (initiative page "…")', () => {
  it('matches the row menu: shared rows, Parent / Sub-initiatives first, Linear\'s order', async () => {
    const user = userEvent.setup()
    renderMenu()
    await user.click(screen.getByRole('button', { name: 'Initiative actions' }))
    const menu = screen.getByRole('menu', { name: 'Initiative actions' })
    expect(menu).toHaveClass('linear-menu')
    const rows = [...menu.querySelectorAll<HTMLElement>(':scope > [role=menuitem], :scope > a[role=menuitem], :scope > [role=separator]')]
    expect(rows.map(row => row.getAttribute('role') === 'separator' ? '—' : row.dataset.menuItem)).toEqual([
      'Parent initiatives', 'Sub-initiatives', '—', 'Copy', '—', 'Favorite', 'Subscribe', 'Remind me', '—',
      'New initiative update', 'Change update schedule…', 'Configure Slack notifications…', '—',
      'Show description history', 'Show updates and activity', 'Export projects as CSV…', '—', 'Delete',
    ])
    for (const row of rows.filter(row => row.getAttribute('role') === 'menuitem')) expect(row).toHaveClass('linear-menu__row')
    expect(rows.find(row => row.dataset.menuItem === 'Configure Slack notifications…')).toHaveAttribute('href', expect.stringContaining('/settings/integrations/slack'))
    expect(rows.find(row => row.dataset.menuItem === 'Parent initiatives')?.querySelector('[data-shortcut]')).toHaveAttribute('data-shortcut', '⌘ ⇧ P')
  })

  it('runs every hint it shows from anywhere on the page', async () => {
    const props = renderMenu()
    fireEvent.keyDown(document.body, { key: 'ƒ', code: 'KeyF', altKey: true })
    expect(props.onUpdate).toHaveBeenCalledWith({ favorite: true })
    fireEvent.keyDown(document.body, { key: 'n', code: 'KeyN' })
    fireEvent.keyDown(document.body, { key: 'u', code: 'KeyU' })
    expect(props.onNewUpdate).toHaveBeenCalled()
    fireEvent.keyDown(document.body, { key: 'u', code: 'KeyU', ctrlKey: true })
    expect(props.onShowActivity).toHaveBeenCalled()
    fireEvent.keyDown(document.body, { key: 'H', code: 'KeyH', shiftKey: true })
    expect(await screen.findByRole('menu', { name: 'Remind me' })).toBeInTheDocument()
  })

  it('opens Parent initiatives with ⌘ ⇧ P and sets a parent', async () => {
    const user = userEvent.setup()
    const props = renderMenu()
    fireEvent.keyDown(document.body, { key: 'P', code: 'KeyP', ctrlKey: true, shiftKey: true })
    await screen.findByRole('menu', { name: 'Parent initiatives' })
    await user.click(screen.getByRole('menuitemcheckbox', { name: /Platform/ }))
    expect(props.onUpdate).toHaveBeenCalledWith({ parentInitiativeIds: ['initiative-2'] })
  })
})
