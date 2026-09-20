import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ActivitySidebarSection, type ActivitySidebarItem } from './activity-sidebar-section'

const items: ActivitySidebarItem[] = [
  { id: '1', createdAt: '2026-09-20T08:10:00.000Z', category: 'property', userIds: ['u1'], userNames: ['Ada'], text: 'changed status', actorLabel: 'Ada' },
  { id: '2', createdAt: '2026-09-20T08:20:00.000Z', category: 'property', userIds: ['u1'], userNames: ['Ada'], text: 'changed priority', actorLabel: 'Ada' },
  { id: '3', createdAt: '2026-09-20T09:00:00.000Z', category: 'comment', userIds: ['u2'], userNames: ['Bea'], text: 'left a comment', actorLabel: 'Bea' },
  { id: '4', createdAt: '2026-09-20T10:00:00.000Z', category: 'update', userIds: ['u1'], userNames: ['Ada'], text: 'posted an update', actorLabel: 'Ada' },
]

describe('LS-0019 ActivitySidebarSection', () => {
  it('renders See all teaser with small-activity-section marker', async () => {
    const onSeeAll = vi.fn()
    const user = userEvent.setup()
    render(<ActivitySidebarSection entityId="proj-1" items={items} onSeeAll={onSeeAll} teaserLimit={2} />)
    expect(document.querySelector('[data-small-activity-section]')).toBeTruthy()
    expect(screen.getByText('changed status')).toBeTruthy()
    expect(screen.queryByText('posted an update')).toBeNull()
    await user.click(screen.getByRole('link', { name: 'See all' }))
    expect(onSeeAll).toHaveBeenCalled()
  })

  it('expands into filtered grouped history', async () => {
    const user = userEvent.setup()
    render(<ActivitySidebarSection entityId="proj-1" expanded items={items} />)
    expect(document.querySelector('[data-small-activity-section]')).toBeNull()
    await user.click(screen.getByRole('button', { name: 'History' }))
    expect(screen.getByText('changed status')).toBeTruthy()
    expect(screen.queryByText('posted an update')).toBeNull()
    expect(screen.getByText('2 events')).toBeTruthy()
  })
})
