import { useEffect, useMemo, useRef, useState } from 'react'
import type { BootstrapData, UserSettings } from '@/types/flow'
import type { PulseRouteView } from '@/lib/app-routes'
import { FeedStableLastSeen } from './feed-stable-last-seen'
import { buildPulseFeed, pulseConfigFromView, type PulseUpdateItem, type PulseViewConfig } from './pulse-model'
import { createFeedItemsListProvider, type FeedItemsListProvider } from './feed-items-list-provider'

export type UseFeedViewOptions = {
  data: BootstrapData
  view: PulseRouteView
  viewId?: string
  config?: PulseViewConfig
  onPersistLastSeen?: (iso: string) => Promise<UserSettings | void> | UserSettings | void
}

export type UseFeedViewResult = {
  items: PulseUpdateItem[]
  newItems: PulseUpdateItem[]
  olderItems: PulseUpdateItem[]
  newItemsCount: number
  unreadCount: number
  lastSeen: number
  lastSeenFeedItemId?: string
  resetStableLastSeenTime: () => void
  /** LS-0267 thin list provider for facet live counts. */
  listProvider: FeedItemsListProvider
}

/**
 * LS-0731 — useFeedView / usePulseFeedView.
 * unread / newItems / lastSeen; beforeunload persists; drives 「N new」 separator.
 */
export function useFeedView({
  data,
  view,
  viewId,
  config,
  onPersistLastSeen,
}: UseFeedViewOptions): UseFeedViewResult {
  const pulseViews = data.savedViews.filter(
    saved => saved.resource === 'pulse' && saved.scope === 'personal' && saved.ownerId === data.viewer.id,
  )
  const activeSavedView = pulseViews.find(saved => saved.id === viewId)
  const activeConfig = config ?? pulseConfigFromView(activeSavedView)
  const items = useMemo(
    () => buildPulseFeed(data, activeSavedView ? 'all' : view, activeSavedView ? activeConfig : { filters: [], match: 'all' }),
    [activeConfig, activeSavedView, data, view],
  )
  const listProvider = useMemo(
    () => createFeedItemsListProvider({
      data,
      view: activeSavedView ? 'all' : view,
      feedItems: items,
      filters: activeConfig.filters,
      match: activeConfig.match,
    }),
    [activeConfig.filters, activeConfig.match, activeSavedView, data, items, view],
  )

  const userSettings = data.userSettings[data.viewer.id]
  const persisted = userSettings?.feedLastSeenTime
  const [, bump] = useState(0)
  useEffect(() => FeedStableLastSeen.subscribe(() => bump(value => value + 1)), [])

  const lastSeen = FeedStableLastSeen.effectiveLastSeenTime(persisted)
  const newItems = useMemo(
    () => (lastSeen > 0 ? items.filter(item => +new Date(item.createdAt) > lastSeen) : []),
    [items, lastSeen],
  )
  const olderItems = useMemo(
    () => (lastSeen > 0 ? items.filter(item => +new Date(item.createdAt) <= lastSeen) : items),
    [items, lastSeen],
  )
  const lastSeenFeedItemId = olderItems[0]?.id

  const persistRef = useRef(onPersistLastSeen)
  persistRef.current = onPersistLastSeen
  const itemsRef = useRef(items)
  itemsRef.current = items

  useEffect(() => {
    const mountFallback = persisted ? +new Date(persisted) : Date.now()
    FeedStableLastSeen.publish(Number.isFinite(mountFallback) ? mountFallback : Date.now())
    return () => {
      FeedStableLastSeen.clear()
    }
  }, [persisted])

  useEffect(() => {
    const persist = () => {
      const newest = itemsRef.current[0]
      const time = newest ? newest.createdAt : new Date().toISOString()
      void persistRef.current?.(time)
    }
    window.addEventListener('beforeunload', persist)
    return () => {
      persist()
      window.removeEventListener('beforeunload', persist)
    }
  }, [])

  return {
    items,
    newItems,
    olderItems,
    newItemsCount: newItems.length,
    unreadCount: newItems.length,
    lastSeen,
    lastSeenFeedItemId,
    resetStableLastSeenTime: () => FeedStableLastSeen.clear(),
    listProvider,
  }
}

export const usePulseFeedView = useFeedView
