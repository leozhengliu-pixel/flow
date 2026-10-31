import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, viewer } from '@/test/fixtures'
import * as api from '@/lib/api'
import type { ActivityEvent, Comment } from '@/types/flow'
import { ActivityTimeline } from './activity-timeline'

const props = { viewerId: viewer.id, onReply: vi.fn(), onEdit: vi.fn(), onDelete: vi.fn(), onReaction: vi.fn() }
const event = (id: string, metadata = { priority: 'High' }): ActivityEvent => ({ id, type: 'issue.updated', metadata, actor: viewer, createdAt: `2026-10-01T10:0${id}:00Z` })
const comment = (id: string, createdAt: string): Comment => ({ id, createdAt, user: viewer, body: 'Comment', reactions: {} }) as Comment

describe('desktop issue activity', () => {
  beforeEach(() => vi.spyOn(window, 'matchMedia').mockImplementation(query => ({ matches: query === '(min-width: 1024px)', media: query, addEventListener: vi.fn(), removeEventListener: vi.fn() }) as unknown as MediaQueryList))
  afterEach(() => vi.restoreAllMocks())

  it('collapses consecutive events, expands and collapses them, and leaves comments outside', () => {
    const events = [event('1'), event('2'), event('3')]
    const comments = [comment('before', '2026-10-01T10:00:00Z'), comment('after', '2026-10-01T10:04:00Z')]
    const view = render(<I18nProvider><ActivityTimeline {...props} events={events} comments={comments}/></I18nProvider>)
    const toggle = screen.getByRole('button', { name: /^Show 3 events/ })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(view.container.querySelectorAll('.timeline-item.event')).toHaveLength(0)
    expect(view.container.querySelectorAll('.timeline-item.comment')).toHaveLength(2)
    fireEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(view.container.querySelectorAll('.timeline-item.event')).toHaveLength(3)
    fireEvent.click(toggle)
    expect(view.container.querySelectorAll('.timeline-item.event')).toHaveLength(0)
    view.rerender(<I18nProvider><ActivityTimeline {...props} events={events} comments={comments} highlightTarget={{ kind: 'activity', id: '2', key: 'notice' }}/></I18nProvider>)
    expect(view.container.querySelector('#activity-2')).toHaveAttribute('data-activity-highlight', 'enter')
    fireEvent.click(screen.getByRole('button', { name: /^Show 3 events/ }))
    expect(view.container.querySelector('#activity-2')).not.toBeInTheDocument()
    view.rerender(<I18nProvider><ActivityTimeline {...props} events={events} comments={comments} highlightTarget={{ kind: 'activity', id: '2', key: 'another-notice' }}/></I18nProvider>)
    expect(view.container.querySelector('#activity-2')).toHaveAttribute('data-activity-highlight', 'enter')
  })

  it('does not coalesce away the individual fields of an expandable run', () => {
    render(<I18nProvider><ActivityTimeline {...props} events={[event('1'), { ...event('2'), metadata: { assignee: viewer.id } }, { ...event('3'), metadata: { state: 'Todo' } }]} comments={[]} context={makeBootstrap()}/></I18nProvider>)
    expect(screen.getByRole('button', { name: /^Show 3 events/ })).toBeVisible()
  })

  it('does not group across a comment or include the issue creation row', () => {
    render(<I18nProvider><ActivityTimeline {...props} events={[{ ...event('0'), type: 'issue.created' }, event('1'), event('2'), event('3')]} comments={[comment('middle', '2026-10-01T10:02:30Z')]}/></I18nProvider>)
    expect(screen.queryByRole('button', { name: /Show .* events/ })).not.toBeInTheDocument()
    expect(screen.getByText(/created the issue/)).toBeVisible()
  })

  it('hides only the redundant parent event and renders a fetched issue title as a plain link', async () => {
    const target = makeIssue({ id: 'target', identifier: 'TST-2', title: 'Related target' })
    vi.spyOn(api, 'fetchIssueRecord').mockResolvedValue(target)
    const events: ActivityEvent[] = [
      { ...event('1'), type: 'issue.relation_added', metadata: { type: 'parent_of', relatedIssueId: target.id } },
      { ...event('2'), type: 'issue.relation_added', metadata: { type: 'blocks', relatedIssueId: target.id } },
    ]
    const view = render(<I18nProvider><ActivityTimeline {...props} events={events} comments={[]} context={makeBootstrap({ issues: [] })}/></I18nProvider>)
    expect(view.container.textContent).not.toContain('parent of')
    const link = await screen.findByRole('link', { name: 'Related target' })
    expect(link).toHaveClass('activity-relation-link')
    expect(link).not.toHaveTextContent('TST-2')
    await waitFor(() => expect(link).toHaveAttribute('href', '/workspace/issue/TST-2/related-target'))
  })
})
