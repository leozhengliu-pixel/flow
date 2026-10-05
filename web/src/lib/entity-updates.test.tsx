import { act, render } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { listInitiativeUpdates, listProjectUpdates } from '@/lib/api'
import { mergeWorkspaceDirectory } from '@/lib/issue-detail-cache'
import { makeBootstrap, project } from '@/test/fixtures'
import type { BootstrapData, ProjectUpdate } from '@/types/flow'
import { entityUpdatesEventTarget, requestEntityUpdates, useEntityUpdatesLoader } from './entity-updates'
import { projectHasUpdates } from './project-has-updates'

vi.mock('@/lib/api', async original => ({
  ...(await original<typeof import('@/lib/api')>()),
  listProjectUpdates: vi.fn(),
  listInitiativeUpdates: vi.fn(),
}))

const update = { id: 'u1', projectId: project.id, body: 'Shipped', health: 'onTrack', createdAt: '2026-10-01T00:00:00Z', comments: [], reactions: {}, attachments: [] } as unknown as ProjectUpdate

let latest: BootstrapData | null = null
function Harness({ initial }: { initial: BootstrapData }) {
  const [data, setData] = useState<BootstrapData | null>(initial)
  latest = data
  useEntityUpdatesLoader(data, setData)
  return null
}

afterEach(() => { vi.mocked(listProjectUpdates).mockReset(); vi.mocked(listInitiativeUpdates).mockReset() })

describe('paged project/initiative updates', () => {
  it('loads requested entities in paged mode and refreshes them on realtime update events', async () => {
    vi.mocked(listProjectUpdates).mockResolvedValue([update])
    render(<Harness initial={makeBootstrap({ issueCollectionPaged: true, projectUpdates: {} })}/>)
    await act(async () => { requestEntityUpdates('project', [project.id, project.id]); await Promise.resolve() })
    await vi.waitFor(() => expect(latest?.projectUpdates[project.id]).toEqual([update]))
    expect(listProjectUpdates).toHaveBeenCalledTimes(1)
    // Already loaded: another request does not refetch, a realtime event does.
    await act(async () => { requestEntityUpdates('project', [project.id]) })
    expect(listProjectUpdates).toHaveBeenCalledTimes(1)
    await act(async () => { window.dispatchEvent(new CustomEvent('flow:pulse-activity', { detail: { type: 'project.update_commented', aggregateId: project.id } })) })
    await vi.waitFor(() => expect(listProjectUpdates).toHaveBeenCalledTimes(2))
  })

  it('ignores requests outside paged mode', async () => {
    render(<Harness initial={makeBootstrap({ projectUpdates: {} })}/>)
    await act(async () => { requestEntityUpdates('project', [project.id]) })
    expect(listProjectUpdates).not.toHaveBeenCalled()
  })

  it('keeps loaded updates when a paged bootstrap (without updates) is merged', () => {
    const current = makeBootstrap({ issueCollectionPaged: true, projectUpdates: { [project.id]: [update], gone: [update] } })
    const next = makeBootstrap({ issueCollectionPaged: true, projectUpdates: {} })
    expect(mergeWorkspaceDirectory(current, next).projectUpdates).toEqual({ [project.id]: [update] })
  })

  it('reads the entity from realtime update events and falls back to health for "has updates"', () => {
    expect(entityUpdatesEventTarget({ type: 'initiative.update_created', aggregateId: 'i1' })).toEqual({ kind: 'initiative', id: 'i1' })
    expect(entityUpdatesEventTarget({ type: 'project.updated', aggregateId: 'p1' })).toBeUndefined()
    expect(projectHasUpdates({ id: 'p1', health: 'atRisk' }, {})).toBe(true)
    expect(projectHasUpdates({ id: 'p1', health: 'noUpdate' }, {})).toBe(false)
    expect(projectHasUpdates({ id: 'p1', health: 'atRisk' }, { p1: [] })).toBe(false)
  })
})
