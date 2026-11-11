import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { RealtimeEventQueue } from './realtime-event-queue'
import { makeIssue } from '@/test/fixtures'

describe('scale-p08 coalesced completion barriers', () => {
  beforeEach(() => { vi.stubEnv('VITE_APPLIED_REALTIME_CURSOR', 'true') })
  afterEach(() => { vi.unstubAllEnvs() })
  const event = (id: string) => ({ id, type: 'issue.updated', aggregateId: 'issue-1', createdAt: '2026-11-11', payload: { issue: makeIssue({ title: id }) } })

  it('finishes every coalesced delivery only after the replacement succeeds', () => {
    const queue = new RealtimeEventQueue()
    const a = vi.fn(), b = vi.fn(), c = vi.fn()
    queue.push(event('41'), 100, a)
    queue.push({ ...event('42'), aggregateId: 'other' }, 100, b)
    queue.push(event('43'), 100, c)
    expect(queue.length).toBe(2)
    const first = queue.shiftDelivery()!
    queue.complete(first)
    expect(b).toHaveBeenCalledOnce()
    expect(a).not.toHaveBeenCalled()
    const replacement = queue.shiftDelivery()!
    expect(replacement.event.id).toBe('43')
    queue.complete(replacement)
    expect(a).toHaveBeenCalledOnce()
    expect(c).toHaveBeenCalledOnce()
  })

  it('carries overflow and failed-apply completions through a snapshot retry', () => {
    const queue = new RealtimeEventQueue()
    const completions = Array.from({ length: 300 }, () => vi.fn())
    completions.forEach((acknowledge, i) => queue.push({ ...event(String(i)), aggregateId: String(i) }, 100, acknowledge))
    expect(queue.length).toBe(1)
    const resync = queue.shiftDelivery()!
    expect(resync.event.type).toBe('resync')
    queue.recover(resync)
    expect(completions.every(acknowledge => acknowledge.mock.calls.length === 0)).toBe(true)
    queue.complete(queue.shiftDelivery()!)
    expect(completions.every(acknowledge => acknowledge.mock.calls.length === 1)).toBe(true)
  })

  it('does not supersede a detail with a summary that omits its body', () => {
    const queue = new RealtimeEventQueue()
    queue.push(event('41'), 100, vi.fn(() => undefined))
    queue.push({ ...event('42'), payload: { issue: makeIssue({ isSummary: true, description: '' }) } }, 100, vi.fn(() => undefined))
    expect(queue.length).toBe(2)
    expect(queue.shift()?.payload?.issue?.description).toBe('Issue body')
  })

  it('acknowledges ignored credential activity without scheduling a reload', () => {
    const queue = new RealtimeEventQueue()
    const acknowledge = vi.fn()
    queue.push({ ...event('41'), type: 'api_key.used' }, 100, acknowledge)
    expect(queue.length).toBe(0)
    expect(acknowledge).toHaveBeenCalledOnce()
  })

  it('keeps a newer queued replacement when an older replay arrives afterwards', () => {
    const queue = new RealtimeEventQueue()
    queue.push({ ...event('42'), payload: { issue: makeIssue({ version: 2 }) } }, 100)
    queue.push({ ...event('41'), payload: { issue: makeIssue({ version: 1 }) } }, 100)
    expect(queue.length).toBe(2)
    expect(queue.shift()?.payload?.issue?.version).toBe(2)
  })
})
