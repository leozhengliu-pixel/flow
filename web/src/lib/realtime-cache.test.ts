import { beforeEach, describe, expect, it } from 'vitest'
import { clearRealtimeCache, loadRealtimeCache, saveRealtimeCache, saveRealtimeCursor } from './realtime-cache'

describe('realtime cache', () => {
  beforeEach(async () => {
    localStorage.clear()
    await clearRealtimeCache('workspace-test')
  })

  it('persists and loads a stream cursor when IndexedDB is unavailable', async () => {
    await saveRealtimeCache({ workspaceKey: 'workspace-test', cursor: 'evt_1', updatedAt: '2026-09-05T00:00:00.000Z' })
    await expect(loadRealtimeCache('workspace-test')).resolves.toMatchObject({ workspaceKey: 'workspace-test', cursor: 'evt_1' })
  })

  it('updates only the cursor while retaining a cached snapshot', async () => {
    const snapshot = { workspace: { urlKey: 'workspace-test' } } as never
    await saveRealtimeCache({ workspaceKey: 'workspace-test', snapshot, cursor: 'evt_1', updatedAt: '2026-09-05T00:00:00.000Z' })
    await saveRealtimeCursor('workspace-test', 'evt_2')
    await expect(loadRealtimeCache('workspace-test')).resolves.toMatchObject({ workspaceKey: 'workspace-test', cursor: 'evt_2', snapshot })
  })
})

