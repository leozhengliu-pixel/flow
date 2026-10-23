import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useWorkspaceRealtime } from './use-workspace-realtime'
import { AGENT_TASK_ACTIVITY_EVENT } from '@/lib/application-agents'
import { CODING_SESSION_EVENT } from '@/components/coding-sessions/coding-session-events'

vi.mock('@/lib/api', () => ({
  realtimeClientId: () => 'tab-self',
  updatePresence: vi.fn(async () => []),
}))

class FakeEventSource {
  static all: FakeEventSource[] = []
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((event: { data: string; lastEventId: string }) => void) | null = null
  closed = false
  readonly url: string
  constructor(url: string) { this.url = url; FakeEventSource.all.push(this) }
  close() { this.closed = true }
  emit(id: string, event: Record<string, unknown>) {
    this.onmessage?.({ data: JSON.stringify({ id, ...event, createdAt: '2026-01-01T00:00:00Z' }), lastEventId: id })
  }
}

describe('useWorkspaceRealtime', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    localStorage.clear()
    FakeEventSource.all = []
    vi.stubGlobal('EventSource', FakeEventSource)
    // Plain test environment: no cross-tab coordination, so the tab keeps its own stream.
    vi.stubGlobal('BroadcastChannel', undefined)
    Object.defineProperty(navigator, 'sendBeacon', { value: vi.fn(() => true), configurable: true })
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('opens one stream, syncs remote events, skips its own and presence echoes, and closes on unmount', async () => {
    const onRemoteSync = vi.fn(async () => undefined)
    const { result, unmount } = renderHook(() => useWorkspaceRealtime({ workspaceKey: 'acme', viewerId: 'u1', route: '/acme', onRemoteSync }))
    expect(FakeEventSource.all).toHaveLength(1)
    expect(FakeEventSource.all[0].url).toBe('/api/realtime/events?workspace=acme')
    const stream = FakeEventSource.all[0]
    act(() => { stream.onopen?.() })
    expect(result.current.connected).toBe(true)
    act(() => {
      stream.emit('connected_1', { type: 'connected', payload: { presence: [{ clientId: 'other' }] } })
      stream.emit('evt_1', { type: 'issue.updated', aggregateId: 'i1', clientId: 'tab-self' })
      stream.emit('presence_1', { type: 'presence.updated', payload: { presence: [{ clientId: 'other' }, { clientId: 'x' }] } })
      stream.emit('evt_2', { type: 'comment.created', aggregateId: 'i1', clientId: 'someone-else' })
    })
    expect(result.current.presence).toHaveLength(2)
    await act(async () => { await vi.advanceTimersByTimeAsync(400) })
    expect(onRemoteSync).toHaveBeenCalledTimes(1)
    expect(onRemoteSync).toHaveBeenCalledWith(expect.objectContaining({ id: 'evt_2', type: 'comment.created' }))
    // The resume cursor ignores the per-connection handshake id.
    expect(JSON.parse(localStorage.getItem('flow:realtime:acme') ?? '{}').cursor).toBe('evt_2')
    unmount()
    expect(stream.closed).toBe(true)
  })

  it('resumes from the stored cursor and reopens for another workspace on switch', async () => {
    localStorage.setItem('flow:realtime:acme', JSON.stringify({ workspaceKey: 'acme', cursor: 'evt_9', updatedAt: 'x' }))
    const onRemoteSync = vi.fn(async () => undefined)
    const { rerender, unmount } = renderHook(({ workspaceKey }) => useWorkspaceRealtime({ workspaceKey, viewerId: 'u1', route: '/', onRemoteSync }), { initialProps: { workspaceKey: 'acme' } })
    expect(FakeEventSource.all[0].url).toBe('/api/realtime/events?workspace=acme&since=evt_9')
    rerender({ workspaceKey: 'globex' })
    expect(FakeEventSource.all[0].closed).toBe(true)
    expect(FakeEventSource.all).toHaveLength(2)
    expect(FakeEventSource.all[1].url).toBe('/api/realtime/events?workspace=globex')
    unmount()
    expect(FakeEventSource.all[1].closed).toBe(true)
  })

  it('refreshes agent task panels and coding session views on a resync, not only the workspace', async () => {
    const onRemoteSync = vi.fn(async () => undefined)
    const tasks: unknown[] = []
    const sessions: unknown[] = []
    const onTask = (event: Event) => tasks.push((event as CustomEvent).detail)
    const onSession = (event: Event) => sessions.push((event as CustomEvent).detail)
    window.addEventListener(AGENT_TASK_ACTIVITY_EVENT, onTask)
    window.addEventListener(CODING_SESSION_EVENT, onSession)
    try {
      const { unmount } = renderHook(() => useWorkspaceRealtime({ workspaceKey: 'acme', viewerId: 'u1', route: '/acme', onRemoteSync }))
      const stream = FakeEventSource.all[0]
      act(() => {
        stream.emit('agent_t1_3', { type: 'agent_task.updated', aggregateId: 'i1', payload: { taskId: 't1' } })
        stream.emit('coding_s1_2_1', { type: 'coding_session.updated', aggregateId: 's1', payload: { agentSessionId: 't1', issueId: 'i1' } })
      })
      expect(tasks).toEqual(['i1'])
      expect(sessions).toEqual([{ sessionId: 's1', agentSessionId: 't1', issueId: 'i1' }])
      // Pushes missed while disconnected: the server cannot replay them.
      act(() => { stream.emit('resync_1', { type: 'workspace.resync_required' }) })
      expect(tasks).toEqual(['i1', ''])
      expect(sessions).toEqual([{ sessionId: 's1', agentSessionId: 't1', issueId: 'i1' }, { sessionId: '' }])
      await act(async () => { await vi.advanceTimersByTimeAsync(400) })
      expect(onRemoteSync).toHaveBeenCalledWith(expect.objectContaining({ type: 'workspace.resync_required' }))
      unmount()
    } finally {
      window.removeEventListener(AGENT_TASK_ACTIVITY_EVENT, onTask)
      window.removeEventListener(CODING_SESSION_EVENT, onSession)
    }
  })

  it('applies a burst of self-contained issue patches in one task, but yields around events that fetch', async () => {
    const order: string[] = []
    const onRemoteSync = vi.fn(async (event: { id: string }) => { order.push(event.id) })
    renderHook(() => useWorkspaceRealtime({ workspaceKey: 'acme', viewerId: 'u1', route: '/acme', onRemoteSync }))
    const stream = FakeEventSource.all[0]
    const patch = (id: string) => stream.emit(id, { type: 'issue.updated', aggregateId: id, payload: { issue: { id, identifier: 'A-1' } }, clientId: 'other' })
    act(() => {
      patch('p1'); patch('p2'); patch('p3')
      stream.emit('comment_1', { type: 'label.created', aggregateId: 'x', clientId: 'other' })
      patch('p4')
      // A macrotask queued behind the drain: it only runs once the drain yields.
      setTimeout(() => order.push('yield'), 80)
    })
    await act(async () => { await vi.advanceTimersByTimeAsync(80) })
    // The three patches ran back to back (before the queued macrotask); the next, non-patch event waited for a new task.
    expect(order).toEqual(['p1', 'p2', 'p3', 'yield'])
    await act(async () => { await vi.advanceTimersByTimeAsync(10) })
    expect(order).toEqual(['p1', 'p2', 'p3', 'yield', 'comment_1', 'p4'])
  })
})
