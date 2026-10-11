import { toast } from '@/lib/toast'

import type { BootstrapData, Favorite } from '@/types/flow'

import { addFavorite, removeFavorite } from './api'

export const FAVORITES_CHANGED = 'flow-favorites-changed'

type FavoriteIntent = {
  workspaceKey: string
  userId: string
  resourceType: string
  resourceId: string
  tab?: string
  action: 'add' | 'remove'
  favorite: Favorite | null
  generation: number
  confirmedAt?: number
}

type FavoriteDelta = {
  workspaceKey: string
  resourceType: string
  resourceId: string
  /** The page tab the favorite targets; absent for the resource's root favorite. */
  tab?: string
  favorite: Favorite | null
}

const pendingIntents = new Map<string, FavoriteIntent>()
const pendingRequests = new Map<string, Promise<unknown>>()
const CONFIRMED_INTENT_TTL_MS = 30_000

function serializeFavoriteRequest<T>(key: string, operation: () => Promise<T>): Promise<T> {
  const previous = pendingRequests.get(key)
  const request = (async () => {
    if (previous) await previous.catch(() => undefined)
    return operation()
  })()
  pendingRequests.set(key, request)
  void request.then(() => { if (pendingRequests.get(key) === request) pendingRequests.delete(key) }, () => { if (pendingRequests.get(key) === request) pendingRequests.delete(key) })
  return request
}

/** Identifies one favorite target: a resource's root, or one of its page tabs. */
export function favoriteResourceKey(resourceType: string, resourceId: string, tab?: string) {
  return tab ? `${resourceType}:${resourceId}:${tab}` : `${resourceType}:${resourceId}`
}

/** Whether a favorite targets this resource and tab. Without a tab only the root favorite matches. */
export function favoriteMatches(item: Favorite, resourceType: string, resourceId: string, tab?: string) {
  return item.resourceType === resourceType && item.resourceId === resourceId && (item.tab || undefined) === (tab || undefined)
}

export function findFavorite(favorites: Favorite[] | undefined, userId: string, resourceType: string, resourceId: string, tab?: string) {
  return (favorites ?? []).find(item => item.userId === userId && favoriteMatches(item, resourceType, resourceId, tab))
}

/** The viewer's favorite state for a resource's root (tab favorites never count) or for one of its tabs. */
export function isFavoriteFor(data: Pick<BootstrapData, 'favorites' | 'viewer'>, resourceType: string, resourceId: string, tab?: string) {
  return Boolean(findFavorite(data.favorites, data.viewer.id, resourceType, resourceId, tab))
}

export function nextFavoritePosition(favorites: Favorite[] | undefined, userId: string) {
  let position = 0
  for (const item of favorites ?? []) {
    if (item.userId === userId && !item.folderId && item.position <= position) position = item.position - 1
  }
  return position
}

export function optimisticFavorite(userId: string, resourceType: string, resourceId: string, position = 0, tab?: string): Favorite {
  return {
    id: `optimistic:${favoriteResourceKey(resourceType, resourceId, tab)}`,
    userId,
    resourceType,
    resourceId,
    ...(tab ? { tab } : {}),
    position,
    createdAt: new Date().toISOString(),
  }
}

export function applyFavoriteDelta(data: BootstrapData, delta: { resourceType: string; resourceId: string; tab?: string; favorite: Favorite | null }): BootstrapData {
  const userId = data.viewer.id
  const { resourceType, resourceId, favorite } = delta
  const tab = delta.tab ?? favorite?.tab
  // Realtime favorite events can originate from another member. They must not
  // flip the viewer's resource marker or inject another user's favorite into
  // this session's sidebar.
  if (favorite && favorite.userId !== userId) return data
  const favorites = favorite
    ? [favorite, ...(data.favorites ?? []).filter(item => item.id !== favorite.id && !(item.userId === userId && favoriteMatches(item, resourceType, resourceId, tab)))]
    : (data.favorites ?? []).filter(item => !(item.userId === userId && favoriteMatches(item, resourceType, resourceId, tab)))
  const marked = Boolean(favorite)
  const next: BootstrapData = { ...data, favorites }
  // A tab favorite never changes the resource's own (root) favorite marker.
  if (tab) return next
  if (resourceType === 'document') next.documents = data.documents.map(item => item.id === resourceId ? { ...item, favorite: marked } : item)
  else if (resourceType === 'cycle') next.cycles = data.cycles.map(item => item.id === resourceId ? { ...item, favorite: marked } : item)
  else if (resourceType === 'initiative') next.initiatives = data.initiatives.map(item => item.id === resourceId ? { ...item, favorite: marked } : item)
  else if (resourceType === 'view') next.savedViews = data.savedViews.map(item => item.id === resourceId ? { ...item, favorite: marked } : item)
  else if (resourceType === 'review') next.reviews = (data.reviews ?? []).map(item => item.id === resourceId || item.slugId === resourceId ? { ...item, favorite: marked } : item)
  else if (resourceType === 'label') next.labels = data.labels.map(item => item.id === resourceId ? { ...item, favorite: marked } : item)
  return next
}

export function overlayPendingFavoriteIntents(data: BootstrapData): BootstrapData {
  if (!pendingIntents.size) return data
  let next = data
  for (const [key, intent] of pendingIntents) {
    if (intent.workspaceKey !== data.workspace.urlKey || intent.userId !== data.viewer.id) continue
    const snapshotFavorite = findFavorite(data.favorites, intent.userId, intent.resourceType, intent.resourceId, intent.tab)
    if (intent.confirmedAt && Boolean(snapshotFavorite) === (intent.action === 'add')) {
      pendingIntents.delete(key)
      continue
    }
    if (intent.confirmedAt && Date.now() - intent.confirmedAt >= CONFIRMED_INTENT_TTL_MS) {
      pendingIntents.delete(key)
      continue
    }
    next = applyFavoriteDelta(next, {
      resourceType: intent.resourceType,
      resourceId: intent.resourceId,
      tab: intent.tab,
      favorite: intent.action === 'add' ? intent.favorite : null,
    })
  }
  return next
}

export function emitFavoriteDelta(delta: FavoriteDelta) {
  window.dispatchEvent(new CustomEvent(FAVORITES_CHANGED, { detail: delta }))
}

export function patchFavorite(workspaceKey: string, favorite: Favorite) {
  emitFavoriteDelta({ workspaceKey, resourceType: favorite.resourceType, resourceId: favorite.resourceId, tab: favorite.tab, favorite })
}

export function toggleFavorite(input: {
  workspaceKey: string
  userId: string
  resourceType: string
  resourceId: string
  /** A page tab of the resource; absent toggles the root favorite. */
  tab?: string
  currentlyFavorited: boolean
  nextFavorite?: boolean
  existing?: Favorite
  position?: number
}): Promise<Favorite | void> {
  const tab = input.tab || undefined
  const key = `${input.workspaceKey}:${favoriteResourceKey(input.resourceType, input.resourceId, tab)}`
  const stored = pendingIntents.get(key)
  if (stored?.confirmedAt && Date.now() - stored.confirmedAt >= CONFIRMED_INTENT_TTL_MS) pendingIntents.delete(key)
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
    ...(tab ? { tab } : {}),
    generation,
  }
  const target = { workspaceKey: input.workspaceKey, resourceType: input.resourceType, resourceId: input.resourceId, ...(tab ? { tab } : {}) }
  if (next) {
    const favorite = optimisticFavorite(input.userId, input.resourceType, input.resourceId, input.position ?? 0, tab)
    pendingIntents.set(key, { ...base, action: 'add', favorite })
    emitFavoriteDelta({ ...target, favorite })
    return serializeFavoriteRequest(key, () => tab ? addFavorite(input.resourceType, input.resourceId, tab) : addFavorite(input.resourceType, input.resourceId)).then(created => {
      if (pendingIntents.get(key)?.generation !== generation) return created
      if (created) {
        pendingIntents.set(key, { ...base, action: 'add', favorite: created, confirmedAt: Date.now() })
        emitFavoriteDelta({ ...target, favorite: created })
      } else {
        pendingIntents.set(key, { ...base, action: 'add', favorite, confirmedAt: Date.now() })
      }
      return created
    }).catch(error => {
      if (pendingIntents.get(key)?.generation === generation) {
        pendingIntents.delete(key)
        emitFavoriteDelta({ ...target, favorite: null })
        toast.error('Could not add favorite', { description: error instanceof Error ? error.message : undefined })
      }
      throw error
    })
  }
  const previous = input.existing ?? (pending?.action === 'add' ? pending.favorite : null)
  pendingIntents.set(key, { ...base, action: 'remove', favorite: previous })
  emitFavoriteDelta({ ...target, favorite: null })
  return serializeFavoriteRequest(key, () => tab ? removeFavorite(input.resourceType, input.resourceId, tab) : removeFavorite(input.resourceType, input.resourceId)).then(() => {
    if (pendingIntents.get(key)?.generation === generation) pendingIntents.set(key, { ...base, action: 'remove', favorite: previous, confirmedAt: Date.now() })
  }).catch(error => {
    if (pendingIntents.get(key)?.generation === generation) {
      pendingIntents.delete(key)
      if (previous) emitFavoriteDelta({ ...target, favorite: previous })
      toast.error('Could not remove favorite', { description: error instanceof Error ? error.message : undefined })
    }
    throw error
  })
}

/** Toggles the viewer's root favorite of a resource; its tab favorites are left alone. */
export function toggleFavoriteFor(data: BootstrapData, resourceType: string, resourceId: string, nextFavorite?: boolean, currentlyFavorited?: boolean) {
  return toggleFavoriteTarget(data, resourceType, resourceId, undefined, nextFavorite, currentlyFavorited)
}

/** Toggles the viewer's favorite of one page tab of a resource (the reference app's tab context menu "Favorite"). */
export function toggleTabFavoriteFor(data: BootstrapData, resourceType: string, resourceId: string, tab: string, nextFavorite?: boolean) {
  return toggleFavoriteTarget(data, resourceType, resourceId, tab, nextFavorite)
}

function toggleFavoriteTarget(data: BootstrapData, resourceType: string, resourceId: string, tab: string | undefined, nextFavorite?: boolean, currentlyFavorited?: boolean) {
  const existing = findFavorite(data.favorites, data.viewer.id, resourceType, resourceId, tab)
  const favorited = currentlyFavorited ?? Boolean(existing)
  return toggleFavorite({
    workspaceKey: data.workspace.urlKey,
    userId: data.viewer.id,
    resourceType,
    resourceId,
    tab,
    currentlyFavorited: favorited,
    nextFavorite,
    existing,
    position: nextFavoritePosition(data.favorites, data.viewer.id),
  })
}

export function resetFavoriteIntents() {
  pendingIntents.clear()
  pendingRequests.clear()
}
