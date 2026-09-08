import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { ActivityEvent, Comment } from '@/types/flow'
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
})
