import { render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { StandaloneSessions } from '@/components/agent/issue-agent-task-context'
import { viewer } from '@/test/fixtures'
import type { ActivityEvent, Comment } from '@/types/flow'
import { ActivityTimeline } from './activity-timeline'
import { withoutSessionDelegations, type ActivityContext } from './issue-activity-model'

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
  it('drops the delegation row once its session shows as a card and keeps the agent status move', () => {
    const agent = { ...viewer, id: 'app-agent', name: 'Flow', displayName: 'Flow', app: true }
    const at = '2026-10-12T11:00:00Z'
    const events = [
      { id: 'delegated', type: 'issue.updated', createdAt: at, actor: viewer, metadata: { delegate: agent.id, agentSessionId: 'task-1' } },
      { id: 'moved', type: 'issue.updated', createdAt: '2026-10-12T11:00:00.001Z', actor: agent, metadata: { stateBefore: 'Todo', state: 'In Progress' } },
      { id: 'old', type: 'issue.updated', createdAt: '2026-10-12T09:00:00Z', actor: viewer, metadata: { delegate: agent.id, agentSessionId: 'task-0' } },
    ] as unknown as ActivityEvent[]
    const context = { users: [viewer, agent], projects: [], labels: [], cycles: [], issues: [], states: [] } as unknown as ActivityContext
    const { container } = render(<I18nProvider><StandaloneSessions.Provider value={[{ id: 'task-1', createdAt: at, agentId: agent.id }]}>
      <ActivityTimeline {...props} events={events} context={context} comments={[]} renderAgentSession={id => <p data-session={id}>Session {id}</p>}/>
    </StandaloneSessions.Provider></I18nProvider>)
    const rows = [...container.querySelectorAll('.timeline-item')].map(node => node.querySelector('[data-session]')?.getAttribute('data-session') ?? node.textContent)
    // The archived session's delegation (no card) stays; the shown session's delegation gives way to its card.
    expect(rows).toHaveLength(3)
    expect(rows[0]).toContain('delegated the issue to Flow')
    expect(rows[1]).toBe('task-1')
    expect(rows[2]).toContain('Flow')
    expect(rows[2]).toContain('moved from Todo to In Progress')
    expect(container.textContent?.match(/delegated the issue/g)).toHaveLength(1)
  })
  it('matches a delegation row without a session id to a card by the same agent within a minute', () => {
    const events = [
      { id: 'delegated', type: 'issue.updated', createdAt: '2026-10-12T11:00:00Z', actor: viewer, metadata: { delegate: 'app-agent', title: 'Renamed' } },
    ] as unknown as ActivityEvent[]
    expect(withoutSessionDelegations(events, [{ id: 'task-1', createdAt: '2026-10-12T11:00:20Z', agentId: 'app-agent' }]).map(event => event.metadata)).toEqual([{ title: 'Renamed' }])
    expect(withoutSessionDelegations(events, [{ id: 'task-1', createdAt: '2026-10-12T11:05:00Z', agentId: 'app-agent' }])).toEqual(events)
    expect(withoutSessionDelegations(events, [{ id: 'task-1', createdAt: '2026-10-12T11:00:20Z', agentId: 'other' }])).toEqual(events)
  })
  it('leaves standalone sessions out when the host does not render them', () => {
    const { container } = render(<I18nProvider><StandaloneSessions.Provider value={[{ id: 'task-1', createdAt: '2026-10-12T11:00:00Z' }]}>
      <ActivityTimeline {...props} comments={[comment('only', '2026-10-12T10:00:00Z')]}/>
    </StandaloneSessions.Provider></I18nProvider>)
    expect(container.querySelectorAll('.timeline-item')).toHaveLength(1)
  })
})
