import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useWorkspaceRealtime } from './use-workspace-realtime'
import { readNavigationCache, writeNavigationCache } from '@/lib/navigation-cache'
import { makeBootstrap, viewer } from '@/test/fixtures'
import { ISSUE_QUERY_INVALIDATED } from '@/components/issue-explorer/paged-issue-invalidation'

vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'self', updatePresence: async () => [] }))

class Source {
  static all: Source[] = []
  onmessage: ((event: { data: string; lastEventId: string }) => void) | null = null
  onerror: (() => void) | null = null
  closed = false
  readonly url: string
  constructor(url: string) { this.url = url; Source.all.push(this) }
  close() { this.closed = true }
  emit(id: string, type = 'issue.updated', extra = {}) {
    this.onmessage?.({ data: JSON.stringify({ id, type, createdAt: '2026-11-11', ...extra }), lastEventId: id })
  }
}

const checkpoints = () => Array.from({ length: localStorage.length }, (_, i) => localStorage.key(i)!)
  .filter(key => key.startsWith('flow:realtime:')).map(key => JSON.parse(localStorage.getItem(key)!))

describe('scale-p08 applied workspace deliveries', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.stubEnv('VITE_APPLIED_REALTIME_CURSOR', 'true')
    localStorage.clear()
    Source.all = []
    vi.stubGlobal('EventSource', Source)
    vi.stubGlobal('BroadcastChannel', undefined)
    Object.defineProperty(navigator, 'sendBeacon', { value: () => true, configurable: true })
  })
  afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.unstubAllEnvs() })

  it('waits five seconds for apply and holds later immediate acknowledgements behind it', async () => {
    const onRemoteSync = vi.fn(async () => { await new Promise(resolve => setTimeout(resolve, 5000)) })
    const { unmount } = renderHook(() => useWorkspaceRealtime({ workspaceKey: 'acme', viewerId: 'u1', route: '/', onRemoteSync }))
    Source.all[0].emit('41')
    Source.all[0].emit('42', 'api_key.used')
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(checkpoints()).toEqual([])
    await act(async () => { await vi.advanceTimersByTimeAsync(500) })
    expect(checkpoints()).toEqual([expect.objectContaining({ workspaceKey: 'acme', viewerId: 'u1', schema: 2, cursor: '42' })])
    unmount()
  })

  it('applies its own echo before checkpointing when the write response may still be in flight', async () => {
    const onRemoteSync = vi.fn(async (event: { type: string }) => { if (event.type !== 'resync') await new Promise(resolve => setTimeout(resolve, 5000)) })
    const { unmount } = renderHook(() => useWorkspaceRealtime({ workspaceKey: 'acme', viewerId: 'u1', route: '/', onRemoteSync }))
    Source.all[0].emit('connected_1', 'connected')
    await act(async () => { await vi.advanceTimersByTimeAsync(100) })
    onRemoteSync.mockClear()
    Source.all[0].emit('41', 'issue.updated', { clientId: 'self' })
    await act(async () => { await vi.advanceTimersByTimeAsync(400) })
    expect(onRemoteSync).toHaveBeenCalledOnce()
    expect(onRemoteSync).toHaveBeenCalledWith(expect.objectContaining({ type: 'issue.updated', clientId: 'self' }))
    expect(checkpoints()).toEqual([])
    await act(async () => { await vi.advanceTimersByTimeAsync(5100) })
    expect(checkpoints()[0]?.cursor).toBe('41')
    unmount()
  })

  it.each(['{broken', 'null', '[]', '{"id":"bad"}', '{"id":"bad","type":"issue.updated","payload":[]}', '{"id":"bad","type":"presence.updated","payload":{"presence":{}}}'])('recovers invalid JSON or event shape %s before checkpointing', async data => {
    let release!: () => void
    const onRemoteSync = vi.fn(() => new Promise<void>(resolve => { release = resolve }))
    const { unmount } = renderHook(() => useWorkspaceRealtime({ workspaceKey: 'acme', viewerId: 'u1', route: '/', onRemoteSync }))
    Source.all[0].onmessage?.({ data, lastEventId: '41' })
    Source.all[0].emit('42', 'issue.updated', { clientId: 'self' })
    await act(async () => { await vi.advanceTimersByTimeAsync(400) })
    expect(onRemoteSync).toHaveBeenCalledWith(expect.objectContaining({ type: 'resync' }))
    expect(checkpoints()).toEqual([])
    await act(async () => { release(); await vi.advanceTimersByTimeAsync(400) })
    expect(checkpoints()[0]?.cursor).toBe('42')
    unmount()
  })

  it('keeps failed apply behind a retried snapshot barrier', async () => {
    let release!: () => void
    const onRemoteSync = vi.fn().mockRejectedValueOnce(new Error('offline')).mockRejectedValueOnce(new Error('still offline'))
      .mockImplementation(() => new Promise<void>(resolve => { release = resolve }))
    const { unmount } = renderHook(() => useWorkspaceRealtime({ workspaceKey: 'acme', viewerId: 'u1', route: '/', onRemoteSync }))
    Source.all[0].emit('41')
    Source.all[0].emit('42', 'notification.updated')
    await act(async () => { await vi.advanceTimersByTimeAsync(2500) })
    expect(checkpoints()).toEqual([])
    expect(onRemoteSync.mock.calls.slice(1).map(([event]) => event.type)).toEqual(['resync', 'resync'])
    await act(async () => { release(); await vi.advanceTimersByTimeAsync(400) })
    expect(checkpoints()[0]?.cursor).toBe('42')
    unmount()
  })

  it('does not resume a fresh baseline from disk and ignores completions after an account switch', async () => {
    localStorage.setItem('flow:realtime:acme', JSON.stringify({ cursor: 'old-viewer-event' }))
    let release!: () => void
    const onRemoteSync = vi.fn(() => new Promise<void>(resolve => { release = resolve }))
    const { rerender, unmount } = renderHook(({ viewerId }) => useWorkspaceRealtime({ workspaceKey: 'acme', viewerId, route: '/', onRemoteSync }), { initialProps: { viewerId: 'u1' } })
    expect(Source.all[0].url).not.toContain('since=')
    Source.all[0].emit('41')
    await act(async () => { await vi.advanceTimersByTimeAsync(100) })
    rerender({ viewerId: 'u2' })
    expect(Source.all[0].closed).toBe(true)
    expect(Source.all[1].url).not.toContain('since=')
    await act(async () => { release(); await vi.advanceTimersByTimeAsync(400) })
    expect(checkpoints().filter(record => record.schema === 2)).toEqual([])
    unmount()
  })

  it.each(['issue.permissions_updated', 'issue.unshared', 'team_member.deleted'])('invalidates revoked cached rows for %s while work is queued and checkpoints only after reconciliation', async type => {
    vi.stubEnv('VITE_BOUNDED_BROWSER_CACHE', 'true')
    let release!: () => void
    let reconcile!: () => void
    const onRemoteSync = vi.fn((event: { id: string; type: string }) => event.type === 'resync' ? Promise.resolve() : new Promise<void>(resolve => {
      if (event.id === '41') release = resolve
      else reconcile = resolve
    }))
    const invalidations: boolean[] = []
    const invalidated = (event: Event) => invalidations.push(Boolean((event as CustomEvent).detail.force))
    window.addEventListener(ISSUE_QUERY_INVALIDATED, invalidated)
    const { unmount } = renderHook(() => useWorkspaceRealtime({ workspaceKey: 'workspace', viewerId: viewer.id, route: '/', onRemoteSync }))
    try {
      Source.all[0].emit('connected_1', 'connected')
      await act(async () => { await vi.advanceTimersByTimeAsync(100) })
      writeNavigationCache(viewer.id, 'workspace', makeBootstrap())
      Source.all[0].emit('41')
      await act(async () => { await vi.advanceTimersByTimeAsync(100) })
      Source.all[0].emit('42', type)
      expect(readNavigationCache(viewer.id, 'workspace')).toBeUndefined()
      expect(invalidations).toContain(true)
      await act(async () => { await vi.advanceTimersByTimeAsync(400) })
      expect(checkpoints()).toEqual([])
      await act(async () => { release(); await vi.advanceTimersByTimeAsync(400) })
      expect(checkpoints()[0]?.cursor).toBe('41')
      await act(async () => { reconcile(); await vi.advanceTimersByTimeAsync(400) })
      expect(checkpoints()[0]?.cursor).toBe('42')
    } finally { unmount(); window.removeEventListener(ISSUE_QUERY_INVALIDATED, invalidated) }
  })
})
