import { toast } from 'sonner'

import type { BootstrapData, Favorite } from '@/types/flow'

import { addFavorite, removeFavorite } from './api'

export const FAVORITES_CHANGED = 'flow-favorites-changed'

type FavoriteIntent = {
  workspaceKey: string
  userId: string
  resourceType: string
  resourceId: string
  action: 'add' | 'remove'
  favorite: Favorite | null
  generation: number
}

type FavoriteDelta = {
  workspaceKey: string
  resourceType: string
  resourceId: string
  favorite: Favorite | null
}

const pendingIntents = new Map<string, FavoriteIntent>()

export function favoriteResourceKey(resourceType: string, resourceId: string) {
  return `${resourceType}:${resourceId}`
}

export function findFavorite(favorites: Favorite[] | undefined, userId: string, resourceType: string, resourceId: string) {
  return (favorites ?? []).find(item => item.userId === userId && item.resourceType === resourceType && item.resourceId === resourceId)
}

export function nextFavoritePosition(favorites: Favorite[] | undefined, userId: string) {
  let position = 0
  for (const item of favorites ?? []) {
    if (item.userId === userId && !item.folderId && item.position <= position) position = item.position - 1
  }
  return position
}

export function optimisticFavorite(userId: string, resourceType: string, resourceId: string, position = 0): Favorite {
  return {
    id: `optimistic:${resourceType}:${resourceId}`,
    userId,
    resourceType,
    resourceId,
    position,
    createdAt: new Date().toISOString(),
  }
}

export function applyFavoriteDelta(data: BootstrapData, delta: { resourceType: string; resourceId: string; favorite: Favorite | null }): BootstrapData {
  const userId = data.viewer.id
  const { resourceType, resourceId, favorite } = delta
  // Realtime favorite events can originate from another member. They must not
  // flip the viewer's resource marker or inject another user's favorite into
  // this session's sidebar.
  if (favorite && favorite.userId !== userId) return data
  const favorites = favorite
    ? [favorite, ...(data.favorites ?? []).filter(item => item.id !== favorite.id && !(item.userId === userId && item.resourceType === resourceType && item.resourceId === resourceId))]
    : (data.favorites ?? []).filter(item => !(item.userId === userId && item.resourceType === resourceType && item.resourceId === resourceId))
  const marked = Boolean(favorite)
  const next: BootstrapData = { ...data, favorites }
  if (resourceType === 'document') next.documents = data.documents.map(item => item.id === resourceId ? { ...item, favorite: marked } : item)
  else if (resourceType === 'cycle') next.cycles = data.cycles.map(item => item.id === resourceId ? { ...item, favorite: marked } : item)
  else if (resourceType === 'initiative') next.initiatives = data.initiatives.map(item => item.id === resourceId ? { ...item, favorite: marked } : item)
  else if (resourceType === 'view') next.savedViews = data.savedViews.map(item => item.id === resourceId ? { ...item, favorite: marked } : item)
  else if (resourceType === 'review') next.reviews = (data.reviews ?? []).map(item => item.id === resourceId || item.slugId === resourceId ? { ...item, favorite: marked } : item)
  return next
}

export function overlayPendingFavoriteIntents(data: BootstrapData): BootstrapData {
  if (!pendingIntents.size) return data
  let next = data
  for (const intent of pendingIntents.values()) {
    if (intent.workspaceKey !== data.workspace.urlKey || intent.userId !== data.viewer.id) continue
    next = applyFavoriteDelta(next, {
      resourceType: intent.resourceType,
      resourceId: intent.resourceId,
      favorite: intent.action === 'add' ? intent.favorite : null,
    })
  }
  return next
}

export function emitFavoriteDelta(delta: FavoriteDelta) {
  window.dispatchEvent(new CustomEvent(FAVORITES_CHANGED, { detail: delta }))
}

export function patchFavorite(workspaceKey: string, favorite: Favorite) {
  emitFavoriteDelta({ workspaceKey, resourceType: favorite.resourceType, resourceId: favorite.resourceId, favorite })
}

export function toggleFavorite(input: {
  workspaceKey: string
  userId: string
  resourceType: string
  resourceId: string
  currentlyFavorited: boolean
  nextFavorite?: boolean
  existing?: Favorite
  position?: number
}): Promise<Favorite | void> {
  const key = `${input.workspaceKey}:${favoriteResourceKey(input.resourceType, input.resourceId)}`
  const pending = pendingIntents.get(key)
  const currentlyFavorited = pending ? pending.action === 'add' : input.currentlyFavorited
  const next = pending ? !currentlyFavorited : (input.nextFavorite ?? !currentlyFavorited)
  if (next === currentlyFavorited) return Promise.resolve()
  const generation = (pending?.generation ?? 0) + 1
  const base = {
    workspaceKey: input.workspaceKey,
    userId: input.userId,
    resourceType: input.resourceType,
    resourceId: input.resourceId,
    generation,
  }
  if (next) {
    const favorite = optimisticFavorite(input.userId, input.resourceType, input.resourceId, input.position ?? 0)
    pendingIntents.set(key, { ...base, action: 'add', favorite })
    emitFavoriteDelta({ workspaceKey: input.workspaceKey, resourceType: input.resourceType, resourceId: input.resourceId, favorite })
    return addFavorite(input.resourceType, input.resourceId).then(created => {
      if (pendingIntents.get(key)?.generation !== generation) return created
      if (created) {
        pendingIntents.set(key, { ...base, action: 'add', favorite: created })
        emitFavoriteDelta({ workspaceKey: input.workspaceKey, resourceType: input.resourceType, resourceId: input.resourceId, favorite: created })
      }
      pendingIntents.delete(key)
      return created
    }).catch(error => {
      if (pendingIntents.get(key)?.generation === generation) {
        pendingIntents.delete(key)
        emitFavoriteDelta({ workspaceKey: input.workspaceKey, resourceType: input.resourceType, resourceId: input.resourceId, favorite: null })
        toast.error('Could not add favorite', { description: error instanceof Error ? error.message : undefined })
      }
      throw error
    })
  }
  const previous = input.existing ?? null
  pendingIntents.set(key, { ...base, action: 'remove', favorite: previous })
  emitFavoriteDelta({ workspaceKey: input.workspaceKey, resourceType: input.resourceType, resourceId: input.resourceId, favorite: null })
  return removeFavorite(input.resourceType, input.resourceId).then(() => {
    if (pendingIntents.get(key)?.generation === generation) pendingIntents.delete(key)
  }).catch(error => {
    if (pendingIntents.get(key)?.generation === generation) {
      pendingIntents.delete(key)
      if (previous) emitFavoriteDelta({ workspaceKey: input.workspaceKey, resourceType: input.resourceType, resourceId: input.resourceId, favorite: previous })
      toast.error('Could not remove favorite', { description: error instanceof Error ? error.message : undefined })
    }
    throw error
  })
}

export function toggleFavoriteFor(data: BootstrapData, resourceType: string, resourceId: string, nextFavorite?: boolean, currentlyFavorited?: boolean) {
  const existing = findFavorite(data.favorites, data.viewer.id, resourceType, resourceId)
  const favorited = currentlyFavorited ?? Boolean(existing)
  return toggleFavorite({
    workspaceKey: data.workspace.urlKey,
    userId: data.viewer.id,
    resourceType,
    resourceId,
    currentlyFavorited: favorited,
    nextFavorite,
    existing,
    position: nextFavoritePosition(data.favorites, data.viewer.id),
  })
}

export function resetFavoriteIntents() {
  pendingIntents.clear()
}
