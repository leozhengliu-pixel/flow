import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, viewer } from '@/test/fixtures'
import type { ActivityEvent, BootstrapData, Comment } from '@/types/flow'
const mocked = vi.hoisted(() => ({ fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...mocked }))

import { ActivityTimeline } from './activity-timeline'

describe('issue activity timeline', () => {
  afterEach(() => { vi.useRealTimers(); window.history.replaceState(null, '', '/') })

  it('reveals the specific old notification event and clears its ring after two seconds', () => {
    vi.useFakeTimers()
    const events: ActivityEvent[] = Array.from({length:12},(_,i)=>({id:`event-${i}`,createdAt:new Date(2026,8,i+1).toISOString(),type:'issue.created',actor:viewer,metadata:{}}))
    const scroll = vi.fn()
    const originalScroll = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollIntoView')!
    Object.defineProperty(Element.prototype, 'scrollIntoView', {configurable:true,value:scroll})
    try {
      const props = {events,comments:[],viewerId:viewer.id,onReply:vi.fn(),onEdit:vi.fn(),onDelete:vi.fn(),onReaction:vi.fn()}
      const {container,rerender} = render(<I18nProvider><ActivityTimeline {...props} highlightTarget={{kind:'activity',id:'event-0',key:'notification-1'}}/></I18nProvider>)
      act(()=>vi.advanceTimersByTime(20))
      expect(container.querySelector('#activity-event-0')).toHaveAttribute('data-activity-highlight','enter')
      expect(container.querySelectorAll('.timeline-item')).toHaveLength(12)
      expect(scroll).toHaveBeenCalledWith({behavior:'smooth',block:'center'})
      act(()=>vi.advanceTimersByTime(2000))
      expect(container.querySelector('#activity-event-0')).toHaveAttribute('data-activity-highlight','exit')
      rerender(<I18nProvider><ActivityTimeline {...props} highlightTarget={{kind:'activity',id:'event-1',key:'notification-2'}}/></I18nProvider>)
      expect(container.querySelector('#activity-event-0')).not.toBeInTheDocument()
      expect(container.querySelector('#activity-event-1')).toHaveAttribute('data-activity-highlight','enter')
      fireEvent.pointerDown(document.body)
      expect(container.querySelector('#activity-event-1')).toHaveAttribute('data-activity-highlight','dismiss')
    } finally { Object.defineProperty(Element.prototype, 'scrollIntoView', originalScroll) }
  })

  it('highlights the reply itself and waits for asynchronously loaded comments', () => {
    vi.useFakeTimers()
    const root = {id:'root',body:'Original',user:viewer,createdAt:'2026-09-01T00:00:00Z',reactions:{}} as Comment
    const reply = {...root,id:'reply',body:'The changed reply',parentId:'root'}
    const props = {events:[],viewerId:viewer.id,onReply:vi.fn(),onEdit:vi.fn(),onDelete:vi.fn(),onReaction:vi.fn(),highlightTarget:{kind:'comment' as const,id:'reply',key:'notification'}}
    const {container,rerender} = render(<I18nProvider><ActivityTimeline {...props} comments={[]}/></I18nProvider>)
    expect(container.querySelector('[data-activity-highlight]')).toBeNull()
    rerender(<I18nProvider><ActivityTimeline {...props} comments={[root,reply]}/></I18nProvider>)
    expect(container.querySelector('#comment-reply')).toHaveAttribute('data-activity-highlight','enter')
    expect(container.querySelector('[data-activity-anchor="comment-root"]')).not.toHaveAttribute('data-activity-highlight')
    act(()=>vi.advanceTimersByTime(6999))
    expect(container.querySelector('#comment-reply')).toHaveAttribute('data-activity-highlight','enter')
    act(()=>vi.advanceTimersByTime(1))
    expect(container.querySelector('#comment-reply')).toHaveAttribute('data-activity-highlight','exit')
  })

  it('supports comment deep links without highlighting a different entry for stale IDs', () => {
    window.history.replaceState(null,'','#comment-comment-one')
    const comment = {id:'comment-one',body:'Linked comment',user:viewer,createdAt:'2026-09-01T00:00:00Z',reactions:{}} as Comment
    const props = {events:[],comments:[comment],viewerId:viewer.id,onReply:vi.fn(),onEdit:vi.fn(),onDelete:vi.fn(),onReaction:vi.fn()}
    const {container,rerender} = render(<I18nProvider><ActivityTimeline {...props}/></I18nProvider>)
    expect(container.querySelector('[data-activity-anchor="comment-comment-one"]')).toHaveAttribute('data-activity-highlight','enter')
    rerender(<I18nProvider><ActivityTimeline {...props} highlightTarget={{kind:'comment',id:'deleted',key:'missing'}}/></I18nProvider>)
    expect(container.querySelector('[data-activity-highlight]')).toBeNull()
  })

  it('lets a comment link override notification focus, then resets when another notification opens', () => {
    const comment = {id:'comment-one',body:'Linked comment',user:viewer,createdAt:'2026-09-01T00:00:00Z',reactions:{}} as Comment
    const events = [{id:'first',createdAt:'2026-09-01T00:00:00Z',type:'issue.created',actor:viewer,metadata:{}}] as ActivityEvent[]
    const props = {events,comments:[comment],viewerId:viewer.id,onReply:vi.fn(),onEdit:vi.fn(),onDelete:vi.fn(),onReaction:vi.fn()}
    const {container,rerender} = render(<I18nProvider><ActivityTimeline {...props} highlightTarget={{kind:'activity',id:'first',key:'notification-1'}}/></I18nProvider>)
    fireEvent.click(container.querySelector('a[href="#comment-comment-one"]')!)
    expect(container.querySelector('[data-activity-anchor="comment-comment-one"]')).toHaveAttribute('data-activity-highlight','enter')
    expect(container.querySelector('#activity-first')).not.toHaveAttribute('data-activity-highlight')
    rerender(<I18nProvider><ActivityTimeline {...props} highlightTarget={{kind:'activity',id:'first',key:'notification-2'}}/></I18nProvider>)
    expect(container.querySelector('#activity-first')).toHaveAttribute('data-activity-highlight','enter')
    expect(container.querySelector('[data-activity-anchor="comment-comment-one"]')).not.toHaveAttribute('data-activity-highlight')
  })

  it('filters autosaves before counting older entries and preserves meaningful history', () => {
    const events: ActivityEvent[] = Array.from({ length: 20 }, (_, i) => ({ id: `save-${i}`, createdAt: '2026-09-08T06:00:00Z', type: 'issue.updated', actor: viewer, metadata: { description: 'updated', descriptionBefore: 'old', descriptionStateBefore: 'private snapshot' } }))
    events.unshift({ id: 'created', createdAt: '2026-08-01T00:00:00Z', type: 'issue.created', actor: viewer, metadata: {} })
    const { container } = render(<I18nProvider><ActivityTimeline events={events} comments={[]} viewerId={viewer.id} context={makeBootstrap()} onReply={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} onReaction={vi.fn()}/></I18nProvider>)
    expect(screen.getByText(/created the issue/)).toBeVisible()
    expect(screen.queryByText(/older activities/)).not.toBeInTheDocument()
    expect(container.querySelectorAll('.timeline-item')).toHaveLength(1)
    expect(container.textContent).not.toMatch(/descriptionBefore|private snapshot|changed description/)
    expect(container.querySelector('.activity-time time')).toHaveAttribute('dateTime', '2026-08-01T00:00:00Z')
  })

  it('copies a link to the specific comment, including its anchor', async () => {
    const user = userEvent.setup()
    const copy = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
    const comment = { id: 'comment-one', body: 'Hello', user: viewer, createdAt: '2026-09-08T06:00:00Z', reactions: {} } as Comment
    render(<I18nProvider><ActivityTimeline events={[]} comments={[comment]} viewerId={viewer.id} onReply={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} onReaction={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Comment options' }))
    await user.click(screen.getByRole('menuitem', { name: 'Copy link to comment' }))
    expect(copy).toHaveBeenCalledWith(`${location.href.split('#')[0]}#comment-comment-one`)
  })

  it('subscribes to and mutes a single comment thread', async () => {
    const user = userEvent.setup()
    const other = { ...viewer, id: 'someone-else', displayName: 'Someone' }
    const root = { id: 'root', body: 'Question', user: other, createdAt: '2026-09-08T06:00:00Z', reactions: {} } as Comment
    const change = vi.fn().mockResolvedValue(undefined)
    const props = { events: [], comments: [root], viewerId: viewer.id, onReply: vi.fn(), onEdit: vi.fn(), onDelete: vi.fn(), onReaction: vi.fn(), onThreadSubscription: change }
    const { rerender } = render(<I18nProvider><ActivityTimeline {...props}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Comment options' }))
    await user.click(screen.getByRole('menuitem', { name: 'Subscribe to thread' }))
    expect(change).toHaveBeenCalledWith('root', 'subscribed')
    await user.click(screen.getByRole('button', { name: 'Comment options' }))
    await user.click(screen.getByRole('menuitem', { name: 'Unsubscribe from thread' }))
    expect(change).toHaveBeenLastCalledWith('root', null)

    // Participants follow implicitly, so unsubscribing mutes the thread.
    const reply = { ...root, id: 'reply', parentId: 'root', user: viewer }
    rerender(<I18nProvider><ActivityTimeline {...props} comments={[{ ...root, id: 'root-2' }, { ...reply, parentId: 'root-2' }]}/></I18nProvider>)
    await user.click(screen.getAllByRole('button', { name: 'Comment options' })[0])
    await user.click(screen.getByRole('menuitem', { name: 'Unsubscribe from thread' }))
    expect(change).toHaveBeenLastCalledWith('root-2', 'muted')
    await user.click(screen.getAllByRole('button', { name: 'Comment options' })[0])
    expect(screen.getByRole('menuitem', { name: 'Unmute thread' })).toBeInTheDocument()
  })

  it('parses markdown comments and replies through RichComment', async () => {
    const comment = { id: 'comment-one', version: 1, body: '## Root heading\n\nRoot paragraph.', user: viewer, createdAt: '2026-09-01T00:00:00Z', reactions: {} } as Comment
    const reply = { ...comment, id: 'reply-one', parentId: 'comment-one', version: 2, body: '- reply item' }
    render(<I18nProvider><ActivityTimeline events={[]} comments={[comment, reply]} viewerId={viewer.id} onReply={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} onReaction={vi.fn()}/></I18nProvider>)
    expect(await screen.findByRole('heading', { name: 'Root heading' })).toBeVisible()
    expect(screen.getByText('Root paragraph.')).toBeVisible()
    expect(screen.getByText('reply item')).toBeVisible()
    expect(screen.getByRole('list')).toBeVisible()
  })

  it('keeps reply composer commands separate from its text cancel action', async () => {
    const user = userEvent.setup()
    const comment = { id: 'comment-one', body: 'Reply here', user: viewer, createdAt: '2026-09-01T00:00:00Z', reactions: {} } as Comment
    const { container } = render(<I18nProvider><ActivityTimeline events={[]} comments={[comment]} viewerId={viewer.id} onReply={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} onReaction={vi.fn()}/></I18nProvider>)

    await user.click(screen.getByRole('button', { name: 'Reply' }))
    const cancel = screen.getByRole('button', { name: 'Cancel' })
    const send = screen.getByRole('button', { name: 'Submit comment' })
    expect(container.querySelector('.composer-tools')).toBeInTheDocument()
    expect(cancel).toHaveClass('composer-cancel')
    expect(cancel).not.toHaveClass('composer-send')
    expect(send).toHaveClass('composer-send')
    expect(cancel.className).toContain('px-2')
    expect(send.className).toContain('size-7')
    expect(container.querySelector('.composer-tools')?.parentElement).toHaveClass('composer-toolbar')
    expect(screen.queryByRole('button', { name: 'Bold' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Italic' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Code' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Link' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Mention' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Attach images, files, or videos' })).toBeVisible()
  })

  describe('referenced resources', () => {
    const eventOf = (id: string, type: string, metadata: Record<string, string>): ActivityEvent => ({ id, type, createdAt: '2026-09-01T00:00:00Z', actor: viewer, metadata })
    const renderTimeline = (events: ActivityEvent[], data: BootstrapData) => render(<I18nProvider><MemoryRouter><WorkspaceStoreProvider account={null} data={data} session={null}>
      <ActivityTimeline events={events} comments={[]} context={data} viewerId={viewer.id} onReply={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} onReaction={vi.fn()}/>
    </WorkspaceStoreProvider></MemoryRouter></I18nProvider>)

    it('renders the project, cycle, label, milestone and related issue an event names as chips', async () => {
      const data = mentionFixture({ issues: [makeIssue(), makeIssue({ id: 'issue-2', identifier: 'TST-2', title: 'Second issue' })] })
      const { container } = renderTimeline([
        eventOf('e1', 'issue.updated', { project: 'project-1' }),
        eventOf('e2', 'issue.updated', { projectMilestone: 'milestone-1' }),
        eventOf('e3', 'issue.updated', { cycle: 'cycle-1' }),
        eventOf('e4', 'issue.updated', { labels: 'label-1' }),
        eventOf('e5', 'issue.relation_added', { type: 'duplicate', relatedIssueId: 'issue-2' }),
      ], data)
      expect(container.querySelector('#activity-e1')).toHaveTextContent('added to project Project one')
      const rows = (id: string) => container.querySelector(`#activity-${id}`) as HTMLElement
      await waitFor(() => expect(rows('e1').querySelector('a[data-agent-entity="project"]')).toHaveAttribute('href', mentionUrls.project))
      expect(rows('e2').querySelector('a[data-agent-entity="milestone"]')).toHaveTextContent('Alpha')
      expect(rows('e3').querySelector('a[data-agent-entity="cycle"]')).toHaveAttribute('href', mentionUrls.cycle)
      expect(rows('e4').querySelector('a[data-agent-entity="label"]')).toHaveTextContent('Feature')
      const duplicate = rows('e5')
      expect(duplicate).toHaveTextContent('marked this as a duplicate of')
      expect(duplicate.querySelector('a[data-agent-entity="issue"]')).toHaveAttribute('href', '/workspace/issue/TST-2/second-issue')
      expect(container.querySelector('.timeline')?.textContent).not.toContain('](')
    })

    it('fetches the parent issue the paged client does not hold and keeps a placeholder when it is gone', async () => {
      resetAgentRecordCache()
      const remote = makeIssue({ id: '0a1b2c3d-1111-2222-3333-444455556666', identifier: 'TST-9', title: 'Remote parent' })
      mocked.fetchIssueRecord.mockReset().mockImplementation(async (id: string) => { if (id === remote.id) return remote; throw new Error('not found') })
      const data = mentionFixture({ issueCollectionPaged: true, issues: [], projects: [] })
      const { container } = renderTimeline([eventOf('e1', 'issue.updated', { parent: remote.id }), eventOf('e2', 'issue.relation_added', { type: 'blocked_by', relatedIssueId: '0a1b2c3d-9999-2222-3333-444455556666' })], data)
      await waitFor(() => expect(container.querySelector('#activity-e1 a[data-agent-entity="issue"]')).toHaveTextContent('TST-9 Remote parent'))
      expect(container.querySelector('#activity-e1')).toHaveTextContent('set the parent issue to')
      await waitFor(() => expect(container.querySelector('#activity-e2 [data-agent-entity="issue"]')).toHaveAttribute('data-mention-state', 'missing'))
      expect(container.querySelector('#activity-e2')).toHaveTextContent('marked this as blocked by another issue')
    })
  })
})
