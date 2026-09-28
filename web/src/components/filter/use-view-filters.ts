/**
 * LS-0780 useViewFilters — React binding over getViewFilters with URL sync helpers.
 */
import { useCallback, useMemo, useState } from 'react'
import type { FilterModelNode } from './filter-block-types'
import {
  encodeFilterForSearch,
  getViewFilters,
  type ViewFilters,
  type ViewFiltersOptions,
} from './get-view-filters'
import { isEmptyModelFilter } from './filter-block-helper'

export interface UseViewFiltersArgs {
  urlKey: string
  pathname: string
  search?: string
  options?: ViewFiltersOptions
  /** Controlled universal filter override. */
  filter?: FilterModelNode
  onFilterChange?: (filter: FilterModelNode) => void
}

export interface UseViewFiltersResult {
  viewFilters: ViewFilters
  filter: FilterModelNode
  setFilter: (filter: FilterModelNode) => void
  clearFilter: () => void
  isFiltering: boolean
  /** Serialize current filter for router navigation (`?filters=...`). */
  toSearchParams: () => string
}

export function useViewFilters(args: UseViewFiltersArgs): UseViewFiltersResult {
  const options = args.options
  const bundle = useMemo(
    () =>
      getViewFilters(
        { urlKey: args.urlKey, pathname: args.pathname, search: args.search },
        options,
      ),
    [args.urlKey, args.pathname, args.search, options],
  )

  const controlled = args.filter !== undefined
  const [local, setLocal] = useState<FilterModelNode>(bundle.universal.filter)
  const filter = controlled ? (args.filter as FilterModelNode) : local

  const setFilter = useCallback(
    (next: FilterModelNode) => {
      if (!controlled) setLocal(next)
      args.onFilterChange?.(next)
    },
    [args, controlled],
  )

  const clearFilter = useCallback(() => setFilter({}), [setFilter])

  const viewFilters = useMemo<ViewFilters>(
    () => ({
      ...bundle,
      universal: {
        ...bundle.universal,
        filter,
        isFiltering: !isEmptyModelFilter(filter),
      },
      isFiltering: scope => {
        if (scope === 'quick') return bundle.isFiltering('quick')
        const main = !isEmptyModelFilter(filter) || bundle.insights.isFiltering
        if (scope === 'main') return main
        return main || bundle.isFiltering('quick')
      },
    }),
    [bundle, filter],
  )

  return {
    viewFilters,
    filter,
    setFilter,
    clearFilter,
    isFiltering: viewFilters.isFiltering('all'),
    toSearchParams: () =>
      encodeFilterForSearch(filter, options?.urlFilterParam),
  }
}
