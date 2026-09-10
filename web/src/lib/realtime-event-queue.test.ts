import { describe, expect, it } from 'vitest'
import { RealtimeEventQueue } from './realtime-event-queue'
import { makeIssue } from '@/test/fixtures'

describe('realtime event backlog', () => {
  const event = (id: number, type = 'issue.updated') => ({ id: String(id), type, aggregateId: String(id), createdAt: '2026-09-09' })

  it('preserves independent notifications and issue changes in order', () => {
    const queue = new RealtimeEventQueue()
    queue.push(event(1), 100)
    queue.push(event(2, 'notification.updated'), 100)
    queue.push(event(3, 'notification.updated'), 100)
    expect([queue.shift()?.id, queue.shift()?.id, queue.shift()?.id]).toEqual(['1', '2', '3'])
    expect(queue.length).toBe(0)
  })

  it('bounds both event count and bytes with a resync marker', () => {
    for (const size of [100, 300_000]) {
      const queue = new RealtimeEventQueue()
      for (let i = 0; i < 500; i++) queue.push(event(i), size)
      expect(queue.length).toBe(1)
      expect(queue.shift()).toEqual({ id: '499', type: 'resync', createdAt: '2026-09-09' })
      queue.push(event(500), 100)
      expect(queue.shift()?.type).toBe('issue.updated')
    }
  })

  it('coalesces full replacements without crossing a deletion or dropping partial updates', () => {
    const queue = new RealtimeEventQueue()
    const full = (id: number) => ({ ...event(id), aggregateId: 'same', payload: { issue: makeIssue({ id: 'same', title: String(id) }) } })
    queue.push(full(1), 100)
    queue.push(event(2, 'notification.updated'), 100)
    queue.push(full(3), 100)
    expect([queue.shift()?.id, queue.shift()?.id]).toEqual(['2', '3'])
    queue.push(full(4), 100)
    queue.push({ ...event(5, 'issue.deleted'), aggregateId: 'same' }, 100)
    queue.push(full(6), 100)
    queue.push({ ...event(7), aggregateId: 'same' }, 100)
    expect([queue.shift()?.id, queue.shift()?.id, queue.shift()?.id, queue.shift()?.id]).toEqual(['4', '5', '6', '7'])
  })
})
