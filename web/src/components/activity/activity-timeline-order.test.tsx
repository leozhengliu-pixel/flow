import { render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { StandaloneSessions } from '@/components/agent/issue-agent-task-context'
import { viewer } from '@/test/fixtures'
import type { Comment } from '@/types/flow'
import { ActivityTimeline } from './activity-timeline'

const comment = (id: string, createdAt: string, parentId?: string) => ({ id, body: `Body ${id}`, user: viewer, createdAt, reactions: {}, parentId }) as Comment
const props = { events: [], viewerId: viewer.id, onReply: vi.fn(), onEdit: vi.fn(), onDelete: vi.fn(), onReaction: vi.fn() }

describe('activity timeline order', () => {
  it('reads oldest to newest, comments and their replies alike, whatever order they arrive in', () => {
    // Pages and optimistic inserts may hand the comments over newest first.
    const comments = [comment('third', '2026-10-12T12:00:00Z'), comment('reply-late', '2026-10-12T11:30:00Z', 'first'), comment('second', '2026-10-12T11:00:00Z'), comment('reply-early', '2026-10-12T10:30:00Z', 'first'), comment('first', '2026-10-12T10:00:00Z')]
    const { container } = render(<I18nProvider><ActivityTimeline {...props} comments={comments}/></I18nProvider>)
    expect([...container.querySelectorAll('.timeline-item.comment')].map(node => node.id)).toEqual(['comment-first', 'comment-second', 'comment-third'])
    expect([...container.querySelectorAll('#comment-first .comment-reply')].map(node => node.id)).toEqual(['comment-reply-early', 'comment-reply-late'])
  })
  it('places a delegated agent session (no source comment) at its chronological position', () => {
    const comments = [comment('before', '2026-10-12T10:00:00Z'), comment('after', '2026-10-12T12:00:00Z')]
    const { container } = render(<I18nProvider><StandaloneSessions.Provider value={[{ id: 'task-1', createdAt: '2026-10-12T11:00:00Z' }]}>
      <ActivityTimeline {...props} comments={comments} renderAgentSession={id => <p data-session={id}>Session {id}</p>}/>
    </StandaloneSessions.Provider></I18nProvider>)
    expect([...container.querySelectorAll('.timeline-item')].map(node => node.id || node.querySelector('[data-session]')?.getAttribute('data-session'))).toEqual(['comment-before', 'task-1', 'comment-after'])
  })
  it('leaves standalone sessions out when the host does not render them', () => {
    const { container } = render(<I18nProvider><StandaloneSessions.Provider value={[{ id: 'task-1', createdAt: '2026-10-12T11:00:00Z' }]}>
      <ActivityTimeline {...props} comments={[comment('only', '2026-10-12T10:00:00Z')]}/>
    </StandaloneSessions.Provider></I18nProvider>)
    expect(container.querySelectorAll('.timeline-item')).toHaveLength(1)
  })
})
