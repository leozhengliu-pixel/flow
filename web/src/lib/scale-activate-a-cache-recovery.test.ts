import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { clearNavigationCache, navigationCacheGeneration, readNavigationCache, writeNavigationCache, writeRecoveredNavigationCache } from '@/lib/navigation-cache'
import { makeBootstrap } from '@/test/fixtures'

beforeEach(() => {
  vi.useFakeTimers()
  vi.stubEnv('VITE_BOUNDED_BROWSER_CACHE', 'true')
  localStorage.clear()
  clearNavigationCache()
})
afterEach(() => { vi.useRealTimers(); vi.unstubAllEnvs() })

describe('scale-activate-a recovered navigation cache', () => {
  it('repopulates only the bounded navigation DTO after a successful resync baseline', async () => {
    const data = makeBootstrap()
    writeNavigationCache(data.viewer.id, data.workspace.urlKey, data)
    clearNavigationCache(data.viewer.id, data.workspace.urlKey)
    const generation = navigationCacheGeneration()
    expect(readNavigationCache(data.viewer.id, data.workspace.urlKey)).toBeUndefined()
    writeRecoveredNavigationCache(data.viewer.id, data.workspace.urlKey, data, generation)
    await vi.runAllTimersAsync()
    const recovered = readNavigationCache(data.viewer.id, data.workspace.urlKey)!
    expect(recovered.viewer.id).toBe(data.viewer.id)
    expect(recovered.resourceDetailsOmitted).toBe(true)
    expect(recovered.notifications).toEqual([])
    expect(recovered.documents).toEqual([])
  })

  it('does not resurrect an in-flight baseline or pending idle write after revocation', async () => {
    const data = makeBootstrap()
    const generation = navigationCacheGeneration()
    writeRecoveredNavigationCache(data.viewer.id, data.workspace.urlKey, data, generation)
    clearNavigationCache(data.viewer.id, data.workspace.urlKey)
    writeRecoveredNavigationCache(data.viewer.id, data.workspace.urlKey, data, generation)
    await vi.runAllTimersAsync()
    expect(readNavigationCache(data.viewer.id, data.workspace.urlKey)).toBeUndefined()
  })

  it('leaves the legacy cache path unchanged with the bounded flag off', async () => {
    vi.stubEnv('VITE_BOUNDED_BROWSER_CACHE', 'false')
    const data = makeBootstrap()
    writeRecoveredNavigationCache(data.viewer.id, data.workspace.urlKey, data, navigationCacheGeneration())
    await vi.runAllTimersAsync()
    expect(readNavigationCache(data.viewer.id, data.workspace.urlKey)).toBeUndefined()
    writeNavigationCache(data.viewer.id, data.workspace.urlKey, data)
    expect(readNavigationCache(data.viewer.id, data.workspace.urlKey)).toBeDefined()
  })
})
