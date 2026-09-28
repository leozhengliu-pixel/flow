import { describe, expect, it } from 'vitest'
import { makeBootstrap, makeIssue, project, viewer } from '@/test/fixtures'
import { applyRealtimePatch, canApplyRealtimePatch } from './apply-realtime-patch'
import { buildEntityDirectory, getEntityById } from './entity-directory'
import { createWorkspaceStore } from './workspace-store'

describe('entity directory (LS-0718)', () => {
  it('indexes bootstrap collections for O(1) getById', () => {
    const data = makeBootstrap()
    const directory = buildEntityDirectory(data)
    expect(getEntityById(directory, 'issue', 'issue-1')).toMatchObject({ id: 'issue-1', identifier: 'TST-1' })
    expect(getEntityById(directory, 'Issue', 'issue-1')).toMatchObject({ id: 'issue-1' })
    expect(getEntityById(directory, 'project', project.id)).toMatchObject({ name: 'Project one' })
    expect(getEntityById(directory, 'user', viewer.id)).toMatchObject({ email: 'viewer@example.test' })
    expect(getEntityById(directory, 'issue', 'missing')).toBeUndefined()
  })

  it('returns empty maps for null bootstrap', () => {
    const directory = buildEntityDirectory(null)
    expect(directory.issue.size).toBe(0)
    expect(getEntityById(directory, 'team', 'x')).toBeUndefined()
  })
})

describe('createWorkspaceStore (LS-0770 / LS-0718)', () => {
  it('exposes getById and reserved hydrateModel from the snapshot', async () => {
    const store = createWorkspaceStore(makeBootstrap())
    expect(store.getById('issue', 'issue-1')).toMatchObject({ title: 'Test issue' })
    expect(store.getById('state', 'state-started')).toMatchObject({ name: 'In progress' })
    await expect(store.hydrateModel('issue', 'issue-1')).resolves.toMatchObject({ id: 'issue-1' })
    await expect(store.hydrateModel('issue', 'absent')).resolves.toBeUndefined()
  })

  it('applyBootstrap replaces the directory', () => {
    const first = createWorkspaceStore(makeBootstrap())
    const next = makeBootstrap({ issues: [makeIssue({ id: 'issue-2', identifier: 'TST-2', title: 'Next' })] })
    const replaced = first.applyBootstrap(next)
    expect(replaced.getById('issue', 'issue-1')).toBeUndefined()
    expect(replaced.getById('issue', 'issue-2')).toMatchObject({ title: 'Next' })
  })

  it('applyPatch merges issue upserts and deletions', () => {
    const store = createWorkspaceStore(makeBootstrap())
    const updated = makeIssue({ id: 'issue-1', title: 'Patched title', version: 2 })
    const patched = store.applyPatch({
      id: '1',
      type: 'issue.updated',
      createdAt: '2026-09-20',
      payload: { issue: updated },
    })
    expect(patched.result.handled).toBe(true)
    expect(patched.store.getById('issue', 'issue-1')).toMatchObject({ title: 'Patched title' })

    const deleted = patched.store.applyPatch({
      id: '2',
      type: 'issue.deleted',
      aggregateId: 'issue-1',
      createdAt: '2026-09-20',
    })
    expect(deleted.result.handled).toBe(true)
    expect(deleted.store.getById('issue', 'issue-1')).toBeUndefined()
  })

  it('falls back when the event cannot be patched locally', () => {
    const store = createWorkspaceStore(makeBootstrap())
    const result = store.applyPatch({
      id: '3',
      type: 'workspace.resync_required',
      createdAt: '2026-09-20',
    })
    expect(result.result.handled).toBe(false)
    expect(result.store).toBe(store)
  })
})

describe('applyRealtimePatch (LS-0718 SSE/patch)', () => {
  it('upserts notifications for the viewer only', () => {
    const data = makeBootstrap({
      notifications: [],
    })
    const notification = {
      id: 'n-1',
      type: 'issueAssigned',
      readAt: undefined,
      createdAt: '2026-09-20',
      recipientId: viewer.id,
      issueId: 'issue-1',
    } as BootstrapDataNotification
    const result = applyRealtimePatch(data, {
      id: '1',
      type: 'notification.created',
      createdAt: '2026-09-20',
      payload: { entity: notification },
    })
    expect(result.handled).toBe(true)
    if (result.handled) {
      expect(result.data.notifications).toHaveLength(1)
      expect(result.data.notifications[0]?.id).toBe('n-1')
    }
  })

  it('patches projects when entity payload is present', () => {
    const data = makeBootstrap()
    const nextProject = { ...project, name: 'Renamed project' }
    const result = applyRealtimePatch(data, {
      id: '1',
      type: 'project.updated',
      createdAt: '2026-09-20',
      payload: { entity: nextProject },
    })
    expect(result.handled).toBe(true)
    if (result.handled) {
      expect(result.data.projects.find((item) => item.id === project.id)?.name).toBe('Renamed project')
    }
  })

  it('reports canApplyRealtimePatch for supported event shapes', () => {
    expect(canApplyRealtimePatch({ id: '1', type: 'issue.deleted', aggregateId: 'x', createdAt: 't' })).toBe(true)
    expect(canApplyRealtimePatch({ id: '1', type: 'label.deleted', createdAt: 't' })).toBe(false)
  })
})

// Minimal notification shape for fixtures without pulling the full type graph.
type BootstrapDataNotification = {
  id: string
  type: string
  recipientId: string
  createdAt: string
  issueId?: string
  readAt?: string
}
