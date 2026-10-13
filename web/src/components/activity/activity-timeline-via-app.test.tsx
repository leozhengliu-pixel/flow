import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { loadChineseDictionary } from '@/i18n/translate'
import { viewer } from '@/test/fixtures'
import type { ActivityEvent, Comment } from '@/types/flow'
import { ActivityTimeline } from './activity-timeline'

const noop = vi.fn()
const base = { viewerId: 'someone-else', onReply: noop, onEdit: noop, onDelete: noop, onReaction: noop }
const bot = { type: 'oauthApplication', name: 'Zendesk' }

describe('via App attribution in the issue activity timeline', () => {
  it('shows "<name> via <App>" for comments and activity created through an application', () => {
    const comment = { id: 'c1', version: 1, body: 'From support', user: viewer, createdAt: '2026-09-01T00:00:00Z', reactions: {}, botActor: bot } as Comment
    const plain = { ...comment, id: 'c2', body: 'Plain comment', botActor: undefined } as Comment
    const external = { ...comment, id: 'c3', body: 'External', botActor: { ...bot, userDisplayName: 'Customer Jo' } } as Comment
    const event: ActivityEvent = { id: 'e1', type: 'issue.created', createdAt: '2026-09-01T00:00:00Z', actor: viewer, metadata: {}, botActor: bot }
    const { container } = render(<MemoryRouter><I18nProvider><ActivityTimeline {...base} events={[event]} comments={[comment, plain, external]} /></I18nProvider></MemoryRouter>)
    const header = (id: string) => container.querySelector(`#comment-${id}`)?.closest('.comment-card')?.querySelector('header') ?? container.querySelector(`[data-activity-anchor="comment-${id}"] header`)
    expect(header('c1')).toHaveTextContent(`${viewer.displayName} via Zendesk`)
    expect(header('c2')).not.toHaveTextContent('via')
    expect(header('c3')).toHaveTextContent('Customer Jo via Zendesk')
    expect(header('c3')).not.toHaveTextContent(viewer.displayName)
    const row = container.querySelector('.activity-row') as HTMLElement
    expect(within(row).getByText('Zendesk')).toBeInTheDocument()
    expect(row).toHaveTextContent(`${viewer.displayName} via Zendesk`)
    expect(screen.queryAllByText('via').length).toBeGreaterThan(0)
  })
})

describe('Flow agent attribution in the issue activity timeline', () => {
  const agent = { id: 'builtin-flow-agent', type: 'agent', name: 'Flow', avatarUrl: '/favicon.svg', onBehalfOfId: viewer.id, onBehalfOfName: viewer.displayName }
  const at = (seconds: number) => new Date(Date.UTC(2026, 9, 12, 9, 0, seconds)).toISOString()
  const change = (id: string, seconds: number, metadata: Record<string, string>, botActor: ActivityEvent['botActor'] = agent): ActivityEvent => ({ id, type: 'issue.updated', createdAt: at(seconds), actor: viewer, metadata, botActor })
  const events = [
    change('e1', 0, { priority: 'Urgent' }),
    change('e2', 4, { dueDate: '2026-10-16' }),
    change('e3', 6, { estimate: '3', estimateBefore: '0' }),
    { ...change('e4', 30, { priority: 'Low' }), botActor: undefined },
  ]

  it('names Flow as the actor, the person after "on behalf of", and folds its field changes into one row', () => {
    const { container } = render(<MemoryRouter><I18nProvider><ActivityTimeline {...base} events={events} comments={[]} /></I18nProvider></MemoryRouter>)
    const rows = Array.from(container.querySelectorAll('.activity-row'))
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent(`Flow set priority to Urgent, set the due date to 2026-10-16 and set the estimate to 3 on behalf of ${viewer.displayName}`)
    expect(rows[0].querySelector('strong')).toHaveTextContent(/^Flow$/)
    // A row that changed several fields shows its actor: the agent's avatar.
    expect(container.querySelector('#activity-e1 .activity-event-icon [data-agent-avatar-slot]')).toBeInTheDocument()
    // The person's own change afterwards stays theirs.
    expect(rows[1]).toHaveTextContent(`${viewer.displayName} set priority to Low`)
    expect(rows[1]).not.toHaveTextContent('on behalf of')
  })

  it('keeps a comment posted between two changes between their rows', () => {
    const comment = { id: 'c1', version: 1, body: 'In between', user: viewer, createdAt: at(2), reactions: {} } as Comment
    const { container } = render(<MemoryRouter><I18nProvider><ActivityTimeline {...base} events={events.slice(0, 2)} comments={[comment]} /></I18nProvider></MemoryRouter>)
    expect(container.querySelectorAll('.activity-row')).toHaveLength(2)
  })

  it('reads "Flow 代表 <User> …" in Chinese', async () => {
    const previous = localStorage.getItem('flow:locale')
    localStorage.setItem('flow:locale', 'zh-CN')
    await loadChineseDictionary()
    try {
      const { container } = render(<MemoryRouter><I18nProvider><ActivityTimeline {...base} events={events.slice(0, 3)} comments={[]} /></I18nProvider></MemoryRouter>)
      expect(container.querySelector('.activity-row')).toHaveTextContent(`Flow 代表 ${viewer.displayName} 将优先级设为 紧急，将截止日期设为 2026-10-16，并将估算设为 3`)
    } finally {
      if (previous === null) localStorage.removeItem('flow:locale')
      else localStorage.setItem('flow:locale', previous)
    }
  })
})
