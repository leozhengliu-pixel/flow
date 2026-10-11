import { beforeEach, describe, expect, it, vi } from 'vitest'

import { makeBootstrap, viewer } from '@/test/fixtures'
import type { BootstrapData, Favorite } from '@/types/flow'

const api = vi.hoisted(() => ({
  addFavorite: vi.fn(),
  removeFavorite: vi.fn(),
}))

vi.mock('./api', async importOriginal => ({
  ...(await importOriginal<typeof import('./api')>()),
  addFavorite: api.addFavorite,
  removeFavorite: api.removeFavorite,
}))

import { applyFavoriteDelta, FAVORITES_CHANGED, favoriteResourceKey, findFavorite, isFavoriteFor, resetFavoriteIntents, toggleFavoriteFor, toggleTabFavoriteFor } from './favorites'

function favorite(overrides: Partial<Favorite> = {}): Favorite {
  return { id: 'favorite-root', userId: viewer.id, resourceType: 'release_pipeline', resourceId: 'pipeline-1', position: 0, createdAt: '2026-09-01T00:00:00.000Z', ...overrides }
}

function deltas() {
  const seen: Array<{ tab?: string; favorite: Favorite | null }> = []
  const listener = (event: Event) => seen.push((event as CustomEvent).detail)
  window.addEventListener(FAVORITES_CHANGED, listener)
  return { seen, stop: () => window.removeEventListener(FAVORITES_CHANGED, listener) }
}

describe('tab favorites', () => {
  beforeEach(() => {
    resetFavoriteIntents()
    api.addFavorite.mockReset()
    api.removeFavorite.mockReset().mockResolvedValue(undefined)
  })

  it('keeps root and tab favorite state separate', () => {
    const data = makeBootstrap({ favorites: [favorite({ id: 'favorite-changelog', tab: 'changelog' })] })
    expect(isFavoriteFor(data, 'release_pipeline', 'pipeline-1')).toBe(false)
    expect(isFavoriteFor(data, 'release_pipeline', 'pipeline-1', 'changelog')).toBe(true)
    expect(isFavoriteFor(data, 'release_pipeline', 'pipeline-1', 'releases')).toBe(false)
    expect(findFavorite(data.favorites, viewer.id, 'release_pipeline', 'pipeline-1')).toBeUndefined()
    expect(favoriteResourceKey('release_pipeline', 'pipeline-1', 'changelog')).not.toBe(favoriteResourceKey('release_pipeline', 'pipeline-1'))
  })

  it('adds a tab favorite through the tab-scoped API without touching the root favorite', async () => {
    const created = favorite({ id: 'favorite-server', tab: 'releases', position: -1 })
    api.addFavorite.mockResolvedValue(created)
    const data = makeBootstrap({ favorites: [favorite()] })
    const events = deltas()
    await toggleTabFavoriteFor(data, 'release_pipeline', 'pipeline-1', 'releases')
    events.stop()
    expect(api.addFavorite).toHaveBeenCalledWith('release_pipeline', 'pipeline-1', 'releases')
    expect(events.seen.map(item => item.tab)).toEqual(['releases', 'releases'])
    let next: BootstrapData = data
    for (const delta of events.seen) next = applyFavoriteDelta(next, { resourceType: 'release_pipeline', resourceId: 'pipeline-1', ...delta })
    expect(next.favorites.map(item => [item.id, item.tab])).toEqual([['favorite-server', 'releases'], ['favorite-root', undefined]])
  })

  it('removes only the targeted favorite', async () => {
    const data = makeBootstrap({ favorites: [favorite(), favorite({ id: 'favorite-changelog', tab: 'changelog' })] })
    await toggleTabFavoriteFor(data, 'release_pipeline', 'pipeline-1', 'changelog')
    expect(api.removeFavorite).toHaveBeenCalledWith('release_pipeline', 'pipeline-1', 'changelog')
    const withoutTab = applyFavoriteDelta(data, { resourceType: 'release_pipeline', resourceId: 'pipeline-1', tab: 'changelog', favorite: null })
    expect(withoutTab.favorites.map(item => item.id)).toEqual(['favorite-root'])

    resetFavoriteIntents()
    await toggleFavoriteFor(data, 'release_pipeline', 'pipeline-1')
    expect(api.removeFavorite).toHaveBeenLastCalledWith('release_pipeline', 'pipeline-1')
    const withoutRoot = applyFavoriteDelta(data, { resourceType: 'release_pipeline', resourceId: 'pipeline-1', favorite: null })
    expect(withoutRoot.favorites.map(item => item.id)).toEqual(['favorite-changelog'])
  })

  it('never flips a resource marker for a tab favorite', () => {
    const data = makeBootstrap({ favorites: [] })
    const label = data.labels[0]
    expect(label).toBeDefined()
    const next = applyFavoriteDelta(data, { resourceType: 'label', resourceId: label.id, tab: 'issues', favorite: favorite({ resourceType: 'label', resourceId: label.id, tab: 'issues' }) })
    expect(next.labels.find(item => item.id === label.id)?.favorite).toBeFalsy()
    expect(next.favorites).toHaveLength(1)
    const root = applyFavoriteDelta(data, { resourceType: 'label', resourceId: label.id, favorite: favorite({ resourceType: 'label', resourceId: label.id }) })
    expect(root.labels.find(item => item.id === label.id)?.favorite).toBe(true)
  })
})
