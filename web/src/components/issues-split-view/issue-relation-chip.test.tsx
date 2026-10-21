import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Issue } from '@/types/flow'
import { IssueRelationChip } from './issue-relation-chip'

const fetchIssueRecord = vi.hoisted(() => vi.fn())
vi.mock('@/lib/api', () => ({ fetchIssueRecord }))

const related = { id: 'issue-2', identifier: 'FLO-54', title: 'REL test B blocked', state: { id: 's1', name: 'Todo', type: 'unstarted', color: '#e2e2e2' } } as unknown as Issue

describe('IssueRelationChip', () => {
  it('opens a hover card with the related issue, fetched when the paged list only has its id', async () => {
    fetchIssueRecord.mockResolvedValue(related)
    const { container } = render(<IssueRelationChip kind="blocks" count={1} ids={['issue-2']} />)
    const chip = container.querySelector('[data-relation="blocks"]') as HTMLElement
    expect(chip).toHaveAttribute('aria-label', 'Blocking 1 issue')
    expect(chip).toHaveTextContent('1')
    await userEvent.hover(chip)
    await waitFor(() => expect(screen.getByText('REL test B blocked')).toBeInTheDocument())
    expect(screen.getByText('FLO-54')).toBeInTheDocument()
    expect(screen.getByText('Todo')).toBeInTheDocument()
    expect(fetchIssueRecord).toHaveBeenCalledWith('issue-2')
  })

  it('renders only the glyph on a compact chip without ids', () => {
    const { container } = render(<IssueRelationChip kind="blocked_by" count={2} compact />)
    const chip = container.querySelector('[data-relation="blocked_by"]') as HTMLElement
    expect(chip).toHaveAttribute('aria-label', 'Blocked by 2 issues')
    expect(chip).toHaveTextContent('')
  })
})
