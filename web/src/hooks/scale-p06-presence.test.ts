import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useWorkspaceRealtime } from './use-workspace-realtime'

const state = vi.hoisted(() => ({ heartbeat: vi.fn(), message: undefined as undefined | ((message: { data: string; lastEventId: string }, acknowledge: () => void) => void) }))
vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'self', updatePresence: state.heartbeat }))
vi.mock('@/lib/shared-realtime', () => ({ connectSharedRealtime: (options: { onMessage: typeof state.message }) => { state.message = options.onMessage; return () => undefined } }))

beforeEach(() => {
  vi.useFakeTimers()
  state.heartbeat.mockReset()
  Object.defineProperty(navigator, 'sendBeacon', { configurable: true, value: vi.fn(() => true) })
})
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks() })

describe('P06 heartbeat roster negotiation', () => {
  const open = () => renderHook(() => useWorkspaceRealtime({ workspaceKey: 'acme', viewerId: 'me', issueId: 'issue', route: '/acme/issue/ISS-1', onRemoteSync: async () => undefined }))
  const presence = (type: string, clients: string[]) => state.message?.({ lastEventId: '', data: JSON.stringify({ type, payload: { presence: clients.map(clientId => ({ clientId })) } }) }, () => undefined)

  it('keeps initial and broadcast rosters after 204 and follows joins/moves/leaves', async () => {
    state.heartbeat.mockResolvedValue(undefined)
    const { result, unmount } = open()
    await act(async () => { presence('connected', ['initial']); await Promise.resolve() })
    expect(result.current.presence.map(p => p.clientId)).toEqual(['initial'])
    await act(async () => { await vi.advanceTimersByTimeAsync(20_000) })
    expect(result.current.presence.map(p => p.clientId)).toEqual(['initial'])
    act(() => presence('presence.updated', ['initial', 'joined']))
    expect(result.current.presence).toHaveLength(2)
    act(() => presence('presence.updated', ['moved']))
    expect(result.current.presence.map(p => p.clientId)).toEqual(['moved'])
    act(() => presence('presence.updated', []))
    expect(result.current.presence).toEqual([])
    unmount()
  })

  it('replaces the roster from a legacy 200 heartbeat', async () => {
    state.heartbeat.mockResolvedValue([{ clientId: 'legacy' }])
    const { result, unmount } = open()
    await act(async () => { presence('connected', ['initial']); await Promise.resolve() })
    expect(result.current.presence.map(p => p.clientId)).toEqual(['legacy'])
    state.heartbeat.mockResolvedValue([])
    await act(async () => { await vi.advanceTimersByTimeAsync(20_000) })
    expect(result.current.presence).toEqual([])
    unmount()
  })
})
