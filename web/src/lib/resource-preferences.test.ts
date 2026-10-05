import { expect, it } from 'vitest'
import { makeBootstrap } from '@/test/fixtures'
import { mergeResourcePreferences } from './resource-preferences'

it('updates personal flags without replacing loaded issues or discussion state', () => {
  const data = makeBootstrap()
  data.savedViews = [{ id: 'view', favorite: true, subscribed: false }] as typeof data.savedViews
  data.documents = [{ id: 'doc', favorite: false }] as typeof data.documents
  const preferences = {
    favorites: [{ id: 'favorite', userId: data.viewer.id, resourceType: 'document', resourceId: 'doc' }] as typeof data.favorites,
    favoriteFolders: [],
    subscriptions: [{ id: 'subscription', userId: data.viewer.id, resourceType: 'view', resourceId: 'view' }] as typeof data.subscriptions,
  }
  const updated = mergeResourcePreferences(data, preferences)
  expect(updated.issues).toBe(data.issues)
  expect(updated.comments).toBe(data.comments)
  expect(updated.activities).toBe(data.activities)
  expect(updated.documents[0].favorite).toBe(true)
  expect(updated.savedViews[0]).toMatchObject({ favorite: false, subscribed: true })
  expect(data.savedViews[0].favorite).toBe(true)
})

it('a Pulse opt-out-only record does not mark the initiative as subscribed', () => {
  const data = makeBootstrap()
  data.documents = []
  data.savedViews = []
  data.cycles = data.cycles ?? []
  data.initiatives = [{ id: 'opted-out', subscribed: true }, { id: 'subscribed', subscribed: false }, { id: 'pulse-only', subscribed: false }] as typeof data.initiatives
  const subscriptions = [
    { id: 's1', userId: data.viewer.id, resourceType: 'initiative', resourceId: 'opted-out', events: [], optOutEvents: ['pulse'], createdAt: '' },
    { id: 's2', userId: data.viewer.id, resourceType: 'initiative', resourceId: 'subscribed', events: ['newUpdate'], createdAt: '' },
    { id: 's3', userId: data.viewer.id, resourceType: 'initiative', resourceId: 'pulse-only', events: ['pulse'], createdAt: '' },
  ]
  const updated = mergeResourcePreferences(data, { favorites: [], favoriteFolders: [], subscriptions })
  expect(updated.initiatives.map(item => [item.id, item.subscribed])).toEqual([['opted-out', false], ['subscribed', true], ['pulse-only', true]])
  // The record itself is kept: it overrides the default Pulse rules.
  expect(updated.subscriptions).toHaveLength(3)
})
