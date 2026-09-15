import { beforeEach, describe, expect, it, vi } from 'vitest'

import { makeBootstrap, viewer } from '@/test/fixtures'
import type { Favorite } from '@/types/flow'

const api = vi.hoisted(() => ({
  addFavorite: vi.fn(),
  removeFavorite: vi.fn(),
}))

vi.mock('./api', async importOriginal => ({
  ...(await importOriginal<typeof import('./api')>()),
  addFavorite: api.addFavorite,
  removeFavorite: api.removeFavorite,
}))

import {
  applyFavoriteDelta,
  FAVORITES_CHANGED,
  nextFavoritePosition,
  optimisticFavorite,
  overlayPendingFavoriteIntents,
  resetFavoriteIntents,
  toggleFavorite,
  toggleFavoriteFor,
} from './favorites'

function favorite(overrides: Partial<Favorite> = {}): Favorite {
  return {
    id: 'favorite-1',
    userId: viewer.id,
    resourceType: 'project',
    resourceId: 'project-1',
    position: 0,
    createdAt: '2026-09-01T00:00:00.000Z',
    ...overrides,
  }
}

describe('favorites', () => {
  beforeEach(() => {
    resetFavoriteIntents()
    api.addFavorite.mockReset()
    api.removeFavorite.mockReset()
  })

  it('places a new favorite above existing root items', () => {
    expect(nextFavoritePosition([favorite({ position: 0 }), favorite({ id: 'favorite-2', position: 1 })], viewer.id)).toBe(-1)
    expect(nextFavoritePosition([], viewer.id)).toBe(0)
  })

  it('patches favorites and matching resource flags immediately', () => {
    const data = makeBootstrap({
      documents: [{ id: 'doc', favorite: false }] as never,
      savedViews: [{ id: 'view', favorite: false }] as never,
      favorites: [],
    })
    const created = optimisticFavorite(viewer.id, 'document', 'doc', -1)
    const updated = applyFavoriteDelta(data, { resourceType: 'document', resourceId: 'doc', favorite: created })
    expect(updated.favorites[0]).toMatchObject({ resourceType: 'document', resourceId: 'doc' })
    expect(updated.documents[0].favorite).toBe(true)
    expect(applyFavoriteDelta(updated, { resourceType: 'document', resourceId: 'doc', favorite: null }).favorites).toEqual([])
  })

  it('emits an optimistic favorite before the network request resolves', async () => {
    let resolveCreated: (value: Favorite) => void = () => undefined
    api.addFavorite.mockReturnValue(new Promise<Favorite>(resolve => { resolveCreated = resolve }))
    const events: Favorite[] = []
    const onChange = (event: Event) => {
      events.push((event as CustomEvent).detail.favorite)
    }
    window.addEventListener(FAVORITES_CHANGED, onChange)
    const pending = toggleFavorite({
      workspaceKey: 'workspace',
      userId: viewer.id,
      resourceType: 'project',
      resourceId: 'project-1',
      currentlyFavorited: false,
      position: -1,
    })
    expect(events[0]).toMatchObject({ id: 'optimistic:project:project-1', resourceType: 'project', resourceId: 'project-1' })
    const created = favorite({ id: 'favorite-server' })
    resolveCreated(created)
    await pending
    expect(events.at(-1)).toEqual(created)
    window.removeEventListener(FAVORITES_CHANGED, onChange)
  })

  it('rolls back an optimistic add when the request fails', async () => {
    api.addFavorite.mockRejectedValue(new Error('offline'))
    const events: Array<Favorite | null> = []
    const onChange = (event: Event) => {
      events.push((event as CustomEvent).detail.favorite)
    }
    window.addEventListener(FAVORITES_CHANGED, onChange)
    await expect(toggleFavorite({
      workspaceKey: 'workspace',
      userId: viewer.id,
      resourceType: 'team',
      resourceId: 'team-1',
      currentlyFavorited: false,
    })).rejects.toThrow('offline')
    expect(events[0]?.id).toBe('optimistic:team:team-1')
    expect(events.at(-1)).toBeNull()
    window.removeEventListener(FAVORITES_CHANGED, onChange)
  })

  it('uses the in-flight intent so rapid toggles alternate add and remove', async () => {
    const created: Favorite[] = []
    const removed: string[] = []
    api.addFavorite.mockImplementation(async (_type: string, id: string) => {
      await Promise.resolve()
      const item = favorite({ id: `favorite-${created.length}`, resourceId: id })
      created.push(item)
      return item
    })
    api.removeFavorite.mockImplementation(async (_type: string, id: string) => {
      await Promise.resolve()
      removed.push(id)
    })
    const first = toggleFavorite({ workspaceKey: 'workspace', userId: viewer.id, resourceType: 'project', resourceId: 'project-1', currentlyFavorited: false, nextFavorite: true })
    const second = toggleFavorite({ workspaceKey: 'workspace', userId: viewer.id, resourceType: 'project', resourceId: 'project-1', currentlyFavorited: false, nextFavorite: true })
    const third = toggleFavorite({ workspaceKey: 'workspace', userId: viewer.id, resourceType: 'project', resourceId: 'project-1', currentlyFavorited: false, nextFavorite: true })
    await Promise.allSettled([first, second, third])
    expect(api.addFavorite).toHaveBeenCalledTimes(2)
    expect(api.removeFavorite).toHaveBeenCalledTimes(1)
    expect(removed).toEqual(['project-1'])
  })

  it('serializes an add followed by remove so the server matches the final intent', async () => {
    let resolveAdd!: (value: Favorite) => void
    const order: string[] = []
    api.addFavorite.mockImplementation(() => new Promise<Favorite>(resolve => { resolveAdd = value => { order.push('add'); resolve(value) } }))
    api.removeFavorite.mockImplementation(async () => { order.push('remove') })
    const added = toggleFavorite({ workspaceKey: 'workspace', userId: viewer.id, resourceType: 'issue', resourceId: 'issue-1', currentlyFavorited: false })
    const removed = toggleFavorite({ workspaceKey: 'workspace', userId: viewer.id, resourceType: 'issue', resourceId: 'issue-1', currentlyFavorited: false })
    await Promise.resolve()
    expect(order).toEqual([])
    resolveAdd(favorite({ id: 'server-favorite', resourceType: 'issue', resourceId: 'issue-1' }))
    await Promise.all([added, removed])
    expect(order).toEqual(['add', 'remove'])
  })

  it('keeps the latest local intent over a stale preferences refresh', async () => {
    let resolveCreated: (value: Favorite) => void = () => undefined
    api.addFavorite.mockReturnValue(new Promise<Favorite>(resolve => { resolveCreated = resolve }))
    const data = makeBootstrap({ favorites: [] })
    const pending = toggleFavoriteFor(data, 'project', 'project-1', true)
    const refreshed = overlayPendingFavoriteIntents({ ...data, favorites: [] })
    expect(refreshed.favorites).toHaveLength(1)
    expect(refreshed.favorites[0].id).toBe('optimistic:project:project-1')
    resolveCreated(favorite({ id: 'favorite-server' }))
    await pending
  })

  it('keeps a confirmed add until a preferences snapshot acknowledges it', async () => {
    const created = favorite({ id: 'favorite-server', resourceType: 'release', resourceId: 'release-1' })
    api.addFavorite.mockResolvedValue(created)
    const data = makeBootstrap({ favorites: [] })

    await toggleFavoriteFor(data, 'release', 'release-1', true)
    expect(overlayPendingFavoriteIntents({ ...data, favorites: [] }).favorites).toContainEqual(created)
    expect(overlayPendingFavoriteIntents({ ...data, favorites: [created] }).favorites).toContainEqual(created)
    expect(overlayPendingFavoriteIntents({ ...data, favorites: [] }).favorites).toEqual([])
  })

  it('keeps a confirmed removal over a stale preferences snapshot', async () => {
    const existing = favorite({ resourceType: 'release', resourceId: 'release-1' })
    api.removeFavorite.mockResolvedValue(undefined)
    const data = makeBootstrap({ favorites: [existing] })

    await toggleFavoriteFor(data, 'release', 'release-1', false)
    expect(overlayPendingFavoriteIntents(data).favorites).toEqual([])
    expect(overlayPendingFavoriteIntents({ ...data, favorites: [] }).favorites).toEqual([])
    expect(overlayPendingFavoriteIntents(data).favorites).toEqual([existing])
  })
})
