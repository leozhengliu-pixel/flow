import { renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SYNC_V2_STREAM_SCHEMA } from '@/lib/scale-p13-wire'
import { useWorkspaceRealtime } from './use-workspace-realtime'

const transport = vi.hoisted(() => ({
  connect: vi.fn(), disconnect: vi.fn(), presence: vi.fn(), load: vi.fn(), save: vi.fn(),
}))
vi.mock('@/lib/shared-realtime', () => ({ connectSharedRealtime: transport.connect }))
vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'p13-client', updatePresence: transport.presence }))
vi.mock('@/lib/realtime-cache', () => ({ loadRealtimeCursor: transport.load, saveRealtimeCursor: transport.save }))

beforeEach(() => {
  vi.clearAllMocks()
  vi.stubEnv('VITE_APPLIED_REALTIME_CURSOR', 'false')
  vi.stubEnv('VITE_BOUNDED_BROWSER_CACHE', 'false')
  vi.stubGlobal('navigator', { sendBeacon: vi.fn() })
  transport.connect.mockReturnValue(transport.disconnect)
  transport.presence.mockResolvedValue([])
  transport.load.mockReturnValue('old-checkpoint')
})
afterEach(() => { vi.unstubAllEnvs(); vi.unstubAllGlobals() })

describe('scale-p13 realtime negotiation', () => {
  it('uses the fresh manifest head, applied delivery, and a separate stream namespace', () => {
    const view = renderHook(() => useWorkspaceRealtime({ workspaceKey: 'workspace', viewerId: 'viewer', route: '/workspace/document/doc', syncV2: true, syncCursor: '9007199254740993', onRemoteSync: async () => {} }))
    const options = transport.connect.mock.calls[0][0]
    expect(options.schema).toBe(SYNC_V2_STREAM_SCHEMA)
    expect(options.appliedCursor).toBe(true)
    expect(options.cursor()).toBe('9007199254740993')
    const url = new URL(options.url(options.cursor()), 'http://localhost')
    expect(url.searchParams.get('sync')).toBe('2')
    expect(url.searchParams.get('since')).toBe('9007199254740993')
    expect(transport.load).not.toHaveBeenCalled()
    view.unmount()
  })

  it('restores the legacy stream options when negotiation is absent', () => {
    const view = renderHook(() => useWorkspaceRealtime({ workspaceKey: 'workspace', viewerId: 'viewer', route: '/workspace/inbox', onRemoteSync: async () => {} }))
    const options = transport.connect.mock.calls[0][0]
    expect(options.schema).not.toBe(SYNC_V2_STREAM_SCHEMA)
    expect(options.appliedCursor).toBe(false)
    expect(options.cursor()).toBe('old-checkpoint')
    expect(new URL(options.url(options.cursor()), 'http://localhost').searchParams.has('sync')).toBe(false)
    view.unmount()
  })
})
