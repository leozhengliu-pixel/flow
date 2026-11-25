import { expect, it } from 'vitest'
import { SyncClient } from './sync-client'
import { scaleP19ProjectSummary } from './scale-p19-project-summary'
import { makeBootstrap, project } from '@/test/fixtures'
import type { SyncManifest } from '@/types/flow'

it('scale-p19 never copies a cached project body or history into its query window', () => {
  const summary = scaleP19ProjectSummary({ ...project, description: 'body'.repeat(1000), descriptionRevisions: [{ body: 'history' } as never], milestones: [{ id: 'm1', name: 'Milestone', description: 'milestone body', descriptionRevisions: [{ body: 'history' } as never] } as never] })
  expect(summary.description).toBe('')
  expect(summary.descriptionRevisions).toEqual([])
  expect(summary.milestones[0]).toEqual({ id: 'm1', name: 'Milestone' })
  expect(summary.lead).toBe(project.lead)
})

it('scale-p19 releases only unowned summaries and preserves details, pins, versions and tombstones', () => {
  const core = makeBootstrap({ projects: [] })
  const manifest: SyncManifest = { schema: 2, workspaceId: core.workspace.id, viewerId: core.viewer.id, core, head: '0', authzEpoch: 'epoch', capabilities: ['sync-v2'], collections: {} }
  const client = new SyncClient(manifest, core.workspace.urlKey)
  const release = client.retainDetail({ kind: 'project', id: 'pinned' })
  client.install([
    { key: { kind: 'project', id: 'summary' }, version: '2', value: { id: 'summary' }, completeness: 'summary' },
    { key: { kind: 'project', id: 'pinned' }, version: '2', value: { id: 'pinned' }, completeness: 'summary' },
    { key: { kind: 'project', id: 'detail' }, version: '2', value: { id: 'detail', description: 'saved' }, completeness: 'detail' },
    { key: { kind: 'project', id: 'deleted' }, version: '2', completeness: 'deleted' },
  ])
  const keys = ['summary', 'pinned', 'detail', 'deleted'].map(id => ({ kind: 'project', id }))
  client.releaseSummaries(keys)
  expect(client.get(keys[0])).toBeUndefined()
  expect(client.get(keys[1])?.version).toBe('2')
  expect(client.get(keys[2])?.value?.description).toBe('saved')
  expect(client.get(keys[3])?.completeness).toBe('deleted')
  release()
  client.releaseSummaries([keys[1]])
  expect(client.get(keys[1])).toBeUndefined()
  client.install([{ key: keys[3], version: '1', value: { id: 'deleted' }, completeness: 'summary' }])
  expect(client.get(keys[3])?.completeness).toBe('deleted')
  client.dispose()
})
