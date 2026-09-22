/**
 * LS-0267 FeedItemsListProvider — thin adapter over buildPulseFeed.
 * Exposes prefilteredItems / filteredItems for facet live counts (no MobX).
 */
import type { BootstrapData } from '@/types/flow'
import type { PulseRouteView } from '@/lib/app-routes'
import {
  buildPulseFeed,
  type PulseFilter,
  type PulseUpdateItem,
  type PulseViewConfig,
} from './pulse-model'

export type FeedItemsListProviderOptions = {
  data: BootstrapData
  view?: PulseRouteView
  /** Full feed before facet filters (defaults to buildPulseFeed view slice). */
  feedItems?: PulseUpdateItem[]
  filters?: PulseFilter[]
  match?: PulseViewConfig['match']
}

export type FeedItemsListProvider = {
  name: 'FeedItemsListProvider'
  modelType: 'feedItem'
  /** Items before the active facet filter set. */
  prefilteredItems: PulseUpdateItem[]
  /** Items after applying filters (all|any). */
  filteredItems: PulseUpdateItem[]
  filters: PulseFilter[]
  match: PulseViewConfig['match']
}

export function createFeedItemsListProvider({
  data,
  view = 'all',
  feedItems,
  filters = [],
  match = 'all',
}: FeedItemsListProviderOptions): FeedItemsListProvider {
  const prefilteredItems = feedItems ?? buildPulseFeed(data, view, { filters: [], match: 'all' })
  const filteredItems =
    filters.length === 0
      ? prefilteredItems
      : buildPulseFeed(data, view, { filters, match }).filter(item =>
          prefilteredItems.some(prefiltered => prefiltered.id === item.id),
        )

  return {
    name: 'FeedItemsListProvider',
    modelType: 'feedItem',
    prefilteredItems,
    filteredItems,
    filters,
    match,
  }
}

/** Live count helper for facet editors. */
export function feedItemsLiveCount(provider: FeedItemsListProvider) {
  return {
    prefiltered: provider.prefilteredItems.length,
    filtered: provider.filteredItems.length,
  }
}
