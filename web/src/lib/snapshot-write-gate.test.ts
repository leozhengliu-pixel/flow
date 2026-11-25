import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiFetch } from './api-client'
import { heldSnapshotWorkspace, holdSnapshotWrites, rejectSnapshotWrites, releaseSnapshotWrites, SnapshotUnconfirmedError, snapshotWriteGate } from './snapshot-write-gate'

afterEach(() => { releaseSnapshotWrites(); vi.unstubAllGlobals() })

describe('snapshot write gate', () => {
  it('holds only writes for the unconfirmed workspace', () => {
    expect(snapshotWriteGate('POST', '/api/issues', 'acme')).toBeUndefined()
    holdSnapshotWrites('acme')
    expect(snapshotWriteGate('POST', '/api/issues', 'acme')).toBeInstanceOf(Promise)
    expect(snapshotWriteGate('patch', '/api/issues/1', 'acme')).toBeInstanceOf(Promise)
    expect(snapshotWriteGate('DELETE', '/api/issues/1', 'acme')).toBeInstanceOf(Promise)
    // Reads, sign-in/sign-out and other workspaces go out at once.
    expect(snapshotWriteGate('GET', '/api/issues', 'acme')).toBeUndefined()
    expect(snapshotWriteGate(undefined, '/api/bootstrap', 'acme')).toBeUndefined()
    expect(snapshotWriteGate('POST', '/api/auth/logout', 'acme')).toBeUndefined()
    expect(snapshotWriteGate('POST', '/api/issues', 'beta')).toBeUndefined()
    expect(snapshotWriteGate('POST', '/api/issues', '')).toBeUndefined()
  })

  it('sends held writes once the workspace is confirmed', async () => {
    holdSnapshotWrites('acme')
    const gate = snapshotWriteGate('POST', '/api/issues', 'acme')!
    let sent = false
    void gate.then(() => { sent = true })
    await Promise.resolve()
    expect(sent).toBe(false)
    releaseSnapshotWrites('beta')
    await Promise.resolve()
    expect(sent).toBe(false)
    releaseSnapshotWrites('acme')
    await gate
    expect(sent).toBe(true)
    expect(heldSnapshotWorkspace()).toBeUndefined()
    expect(snapshotWriteGate('POST', '/api/issues', 'acme')).toBeUndefined()
  })

  it('fails held writes like an unsent request when the cached view is dropped', async () => {
    holdSnapshotWrites('acme')
    const gate = snapshotWriteGate('POST', '/api/issues', 'acme')!
    rejectSnapshotWrites('acme')
    await expect(gate).rejects.toBeInstanceOf(SnapshotUnconfirmedError)
    await expect(gate).rejects.toBeInstanceOf(TypeError)
  })

  it('fails the writes of a workspace replaced by another unconfirmed one', async () => {
    holdSnapshotWrites('acme')
    const gate = snapshotWriteGate('POST', '/api/issues', 'acme')!
    holdSnapshotWrites('beta')
    await expect(gate).rejects.toBeInstanceOf(SnapshotUnconfirmedError)
    expect(heldSnapshotWorkspace()).toBe('beta')
  })

  it('makes apiFetch wait for confirmation before sending a write', async () => {
    const fetch = vi.fn(async () => new Response('{}', { status: 200 }))
    vi.stubGlobal('fetch', fetch)
    holdSnapshotWrites('acme')
    const write = apiFetch('/api/issues', { method: 'POST', headers: { 'X-Workspace-Key': 'acme' }, body: '{}' })
    const read = apiFetch('/api/issues', { headers: { 'X-Workspace-Key': 'acme' } })
    await read
    expect(fetch).toHaveBeenCalledTimes(1)
    releaseSnapshotWrites('acme')
    expect((await write).status).toBe(200)
    expect(fetch).toHaveBeenCalledTimes(2)
    expect((fetch.mock.calls[1] as unknown[])[1]).toMatchObject({ method: 'POST' })
  })
})
