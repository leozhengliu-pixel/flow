import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useWorkspaceRealtime } from './use-workspace-realtime'

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
})
