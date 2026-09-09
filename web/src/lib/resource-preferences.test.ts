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
