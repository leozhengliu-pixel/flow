import type { BootstrapData } from '@/types/flow'
import { request } from './api-client'

export type ResourcePreferences = Pick<BootstrapData, 'favorites' | 'favoriteFolders' | 'subscriptions'> & { documentSubscriptions?: Record<string, boolean> }
export const RESOURCE_PREFERENCES_UPDATED = 'flow-resource-preferences-updated'

export async function refreshResourcePreferences(workspaceKey = decodeURIComponent(location.pathname.split('/').filter(Boolean)[0] ?? '')) {
  const preferences = await fetchResourcePreferences(workspaceKey)
  window.dispatchEvent(new CustomEvent(RESOURCE_PREFERENCES_UPDATED, { detail: { workspaceKey, preferences } }))
}

export function fetchResourcePreferences(workspaceKey: string) {
  return request<ResourcePreferences>('/api/resource-preferences', { headers: { 'X-Workspace-Key': workspaceKey } })
}

export function mergeResourcePreferences(data: BootstrapData, preferences: ResourcePreferences): BootstrapData {
  const { documentSubscriptions, ...collections } = preferences
  const favorites = new Set(preferences.favorites.filter(item => item.userId === data.viewer.id).map(item => `${item.resourceType}:${item.resourceId}`))
  const subscriptions = new Set(preferences.subscriptions.filter(item => item.userId === data.viewer.id).map(item => `${item.resourceType}:${item.resourceId}`))
  return {
    ...data, ...collections,
    documents: data.documents.map(item => ({ ...item, favorite: favorites.has(`document:${item.id}`), subscriberIds: documentSubscriptions?.[item.id] === undefined ? item.subscriberIds : documentSubscriptions[item.id] ? [...new Set([...(item.subscriberIds ?? []), data.viewer.id])] : (item.subscriberIds ?? []).filter(id => id !== data.viewer.id) })),
    cycles: data.cycles.map(item => ({ ...item, favorite: favorites.has(`cycle:${item.id}`) })),
    initiatives: data.initiatives.map(item => ({ ...item, favorite: favorites.has(`initiative:${item.id}`), subscribed: subscriptions.has(`initiative:${item.id}`) })),
    savedViews: data.savedViews.map(item => ({ ...item, favorite: favorites.has(`view:${item.id}`), subscribed: subscriptions.has(`view:${item.id}`) })),
    reviews: (data.reviews ?? []).map(item => ({ ...item, favorite: favorites.has(`review:${item.id}`) || favorites.has(`review:${item.slugId}`) })),
  }
}
