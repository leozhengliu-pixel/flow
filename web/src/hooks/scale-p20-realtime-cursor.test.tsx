import { renderHook } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { makeBootstrap } from '@/test/fixtures'
import { APPLIED_CURSOR_SCHEMA } from '@/lib/scale-p08'
import { SYNC_V2_STREAM_SCHEMA } from '@/lib/scale-p13-wire'
import { useWorkspaceRealtime } from './use-workspace-realtime'

const transport = vi.hoisted(() => ({
  connect: vi.fn(), disconnect: vi.fn(), presence: vi.fn(), load: vi.fn(),
}))
vi.mock('@/lib/shared-realtime', () => ({ connectSharedRealtime: transport.connect }))
vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'p20-client', updatePresence: transport.presence }))
vi.mock('@/lib/realtime-cache', () => ({ loadRealtimeCursor: transport.load, saveRealtimeCursor: vi.fn() }))

beforeEach(() => {
  vi.stubEnv('VITE_APPLIED_REALTIME_CURSOR', 'false')
  vi.stubEnv('VITE_BOUNDED_BROWSER_CACHE', 'false')
  vi.stubGlobal('navigator', { sendBeacon: vi.fn() })
  transport.connect.mockReturnValue(transport.disconnect)
  transport.presence.mockResolvedValue([])
  transport.load.mockReturnValue('99')
})
afterEach(() => { vi.clearAllMocks(); vi.unstubAllEnvs(); vi.unstubAllGlobals() })

it('scale-p20 resumes the resource baseline in the workspace change-log domain', () => {
  const snapshot = makeBootstrap()
  snapshot.sync = { schema: 2, authzEpoch: 'epoch', head: '17', capabilities: ['resource-windows'], collections: {}, revision: 0 }
  const view = renderHook(() => useWorkspaceRealtime({
    workspaceKey: snapshot.workspace.urlKey, viewerId: snapshot.viewer.id,
    route: '/workspace/document/doc', snapshot, syncV2: true,
    syncCursor: snapshot.sync?.head, onRemoteSync: async () => {},
  }))
  const options = transport.connect.mock.calls[0][0]
  expect(options.schema).toBe(APPLIED_CURSOR_SCHEMA)
  expect(options.schema).not.toBe(SYNC_V2_STREAM_SCHEMA)
  expect(options.appliedCursor).toBe(true)
  const url = new URL(options.url(options.cursor()), 'http://localhost')
  expect(url.searchParams.get('since')).toBe('17')
  expect(url.searchParams.has('sync')).toBe(false)
  expect(transport.load).not.toHaveBeenCalled()
  view.unmount()
})

it('scale-p20 reconnects from its current published resource baseline', () => {
  const snapshot = makeBootstrap()
  snapshot.sync = { schema: 2, authzEpoch: 'epoch', head: '17', capabilities: ['resource-windows'], collections: {}, revision: 0 }
  const view = renderHook(({ head }) => useWorkspaceRealtime({
    workspaceKey: snapshot.workspace.urlKey, viewerId: snapshot.viewer.id,
    route: '/workspace/document/doc', snapshot, syncV2: true,
    syncCursor: head, onRemoteSync: async () => {},
  }), { initialProps: { head: '17' } })
  view.rerender({ head: '22' })
  expect(transport.disconnect).toHaveBeenCalledTimes(1)
  const options = transport.connect.mock.calls.at(-1)![0]
  expect(new URL(options.url(options.cursor()), 'http://localhost').searchParams.get('since')).toBe('22')
  expect(options.schema).toBe(APPLIED_CURSOR_SCHEMA)
  view.unmount()
})
