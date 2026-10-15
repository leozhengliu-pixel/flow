import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { TeamPinnedResource, TeamResourceSection } from '@/types/flow'

const api = vi.hoisted(() => ({
  createTeamResourceSection: vi.fn(),
  deleteTeamResource: vi.fn(),
  deleteTeamResourceSection: vi.fn(),
  fetchTeamResources: vi.fn(),
  pinTeamResource: vi.fn(),
  updateTeamResource: vi.fn(),
  updateTeamResourceSection: vi.fn(),
}))
vi.mock('@/lib/api', () => api)

import {
  applyTeamResourcesChange, deleteSection, onTeamResourcesChange, pinResource, refreshTeamResourcesForEvent, unpinResource, updateResource,
  type TeamResourceState,
} from './team-resource-sync'

const team = 'team-1'
const resource = (id: string, extra: Partial<TeamPinnedResource> = {}): TeamPinnedResource => ({
  id, teamId: team, sectionId: '', resourceType: 'document', resourceId: `doc-${id}`, title: id, position: 0, createdAt: '', updatedAt: '', ...extra,
})
const section = (id: string): TeamResourceSection => ({ id, teamId: team, name: id, position: 0, createdAt: '', updatedAt: '' })

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}

let state: TeamResourceState
let stop: () => void
beforeEach(() => {
  state = { resources: [], sections: [] }
  stop = onTeamResourcesChange(change => { state = applyTeamResourcesChange(state, change) })
  Object.values(api).forEach(mock => mock.mockReset())
})
afterEach(() => stop())

describe('team resource sync', () => {
  it('shows a pin at once and swaps in the saved row without refetching', async () => {
    const saved = deferred<TeamPinnedResource>()
    api.pinTeamResource.mockReturnValue(saved.promise)
    const request = pinResource(team, [resource('a', { position: 3 })], { resourceType: 'document', resourceId: 'doc-x', title: 'X' })
    expect(state.resources).toHaveLength(1)
    expect(state.resources[0]).toMatchObject({ resourceId: 'doc-x', position: 4 })
    saved.resolve(resource('x', { position: 4 }))
    await request
    expect(state.resources.map(item => item.id)).toEqual(['x'])
    expect(api.pinTeamResource).toHaveBeenCalledTimes(1)
    expect(api.fetchTeamResources).not.toHaveBeenCalled()
  })

  it('removes a pin that is still saving and deletes it once saved', async () => {
    const saved = deferred<TeamPinnedResource>()
    api.pinTeamResource.mockReturnValue(saved.promise)
    api.deleteTeamResource.mockResolvedValue(undefined)
    const pin = pinResource(team, [], { resourceType: 'link', url: 'https://example.test', title: 'Link' })
    const temporary = state.resources[0]
    const unpin = unpinResource(team, temporary)
    expect(state.resources).toEqual([])
    saved.resolve(resource('saved'))
    await Promise.all([pin, unpin])
    expect(state.resources).toEqual([])
    expect(api.deleteTeamResource).toHaveBeenCalledWith(team, 'saved')
  })

  it('starts the delete request before the change renders and restores the row when it fails', async () => {
    const order: string[] = []
    api.deleteTeamResource.mockImplementation(() => { order.push('request'); return Promise.reject(new Error('offline')) })
    const stopOrder = onTeamResourcesChange(() => order.push('change'))
    state = { resources: [resource('a')], sections: [] }
    await expect(unpinResource(team, resource('a'))).rejects.toThrow('offline')
    stopOrder()
    expect(order.slice(0, 2)).toEqual(['request', 'change'])
    expect(state.resources.map(item => item.id)).toEqual(['a'])
  })

  it('moves a row at once and keeps a newer move when an older one fails', async () => {
    const first = deferred<TeamPinnedResource>()
    api.updateTeamResource.mockReturnValueOnce(first.promise).mockResolvedValueOnce(resource('a', { sectionId: 's2' }))
    state = { resources: [resource('a')], sections: [] }
    const older = updateResource(team, resource('a'), { sectionId: 's1' })
    expect(state.resources[0].sectionId).toBe('s1')
    await updateResource(team, state.resources[0], { sectionId: 's2' })
    first.reject(new Error('conflict'))
    await expect(older).rejects.toThrow('conflict')
    expect(state.resources[0].sectionId).toBe('s2')
  })

  it('keeps a deleted section’s resources pinned outside any section', async () => {
    api.deleteTeamResourceSection.mockResolvedValue(undefined)
    state = { resources: [resource('a', { sectionId: 's1' })], sections: [section('s1')] }
    await deleteSection(team, section('s1'), state.resources)
    expect(state.sections).toEqual([])
    expect(state.resources[0].sectionId).toBe('')
  })

  it('replaces only the changed team’s rows in the workspace snapshot', () => {
    const other = resource('o', { teamId: 'team-2' })
    const next = applyTeamResourcesChange({ resources: [resource('a'), other], sections: [] }, { teamId: team, replace: { resources: [resource('b')], sections: [section('s')] } })
    expect(next.resources.map(item => item.id)).toEqual(['o', 'b'])
    expect(next.sections.map(item => item.id)).toEqual(['s'])
  })

  it('refetches one team’s list for another client’s change', async () => {
    api.fetchTeamResources.mockResolvedValue({ resources: [resource('remote')], sections: [] })
    const data = { teams: [{ id: team }], teamPinnedResources: [resource('known')], teamResourceSections: [] } as never
    expect(await refreshTeamResourcesForEvent(data, { type: 'team.resource_pinned', aggregateId: team })).toBe(true)
    expect(await refreshTeamResourcesForEvent(data, { type: 'team.resource_deleted', aggregateId: 'known' })).toBe(true)
    expect(api.fetchTeamResources).toHaveBeenCalledTimes(2)
    expect(api.fetchTeamResources).toHaveBeenCalledWith(team)
    expect(state.resources.map(item => item.id)).toEqual(['remote'])
    expect(await refreshTeamResourcesForEvent(data, { type: 'team.resource_deleted', aggregateId: 'unknown' })).toBe(false)
    expect(await refreshTeamResourcesForEvent(data, { type: 'issue.updated', aggregateId: team })).toBe(false)
  })
})
