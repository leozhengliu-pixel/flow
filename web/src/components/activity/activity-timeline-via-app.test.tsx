import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
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
