/**
 * LS-0701 getViewFilters — transformer catalog + URL filter root path + empty-when-missing.
 * REST-only: filters are FilterModelNode ASTs (no GraphQL).
 */
import type { FilterModelNode } from './filter-block-types'
import { isEmptyModelFilter } from './filter-block-helper'

export type ViewFilterKind = 'issue' | 'project' | 'initiative' | 'notification' | 'customer' | 'document'

export type ViewFilterTransformer = (filter: FilterModelNode) => FilterModelNode

export interface ViewFiltersOptions {
  /** Logical pathname used as persistence / URL key (workspace-relative ok). */
  identifier?: string
  /** When false, ignore URL `filter` / `filters` search params. Default true. */
  readFilterFromUrl?: boolean
  /** When true and URL has no filter param, force an empty filter. */
  emptyFilterWhenUrlParamMissing?: boolean
  /** Skip inline find-as-you-type store. */
  noInlineFilter?: boolean
  /** Skip insights filter slice. */
  noInsightsFilter?: boolean
  /** Skip quick filter chips. */
  noQuickFilters?: boolean
  /** Entity type for default transformers (issue/project get SLA/priority-ready hooks). */
  type?: ViewFilterKind
  /** Extra transformers applied after built-ins. */
  filterTransformer?: ViewFilterTransformer | ViewFilterTransformer[]
  /** Named quick filters (string id or [id, initialFilter]). */
  quickFilters?: Array<string | [string, FilterModelNode]>
  /** URL search param name. Default `filters`. */
  urlFilterParam?: string
}

export interface ViewFilterStore {
  filter: FilterModelNode
  isFiltering: boolean
  persistenceKey?: string
}

export interface ViewFilters {
  universal: ViewFilterStore
  inline?: ViewFilterStore
  insights: ViewFilterStore
  quick: Record<string, ViewFilterStore>
  isFiltering: (scope?: 'all' | 'quick' | 'main') => boolean
  getFilters: (scope?: 'all' | 'quick' | 'main') => ViewFilterStore[]
}

const URL_FILTER_PARAM_DEFAULT = 'filters'

/** Decode a JSON FilterModelNode from URL search params. */
export function readFilterFromSearch(
  search: string,
  param = URL_FILTER_PARAM_DEFAULT,
): FilterModelNode | undefined {
  const raw = new URLSearchParams(search.startsWith('?') ? search : `?${search}`).get(param)
  if (!raw) return undefined
  try {
    const parsed = JSON.parse(raw) as unknown
    if (parsed && typeof parsed === 'object') return parsed as FilterModelNode
  } catch {
    return undefined
  }
  return undefined
}

export function encodeFilterForSearch(
  filter: FilterModelNode | null | undefined,
  param = URL_FILTER_PARAM_DEFAULT,
): string {
  if (!filter || isEmptyModelFilter(filter)) return ''
  const params = new URLSearchParams()
  params.set(param, JSON.stringify(filter))
  return params.toString()
}

function asTransformerList(
  value: ViewFilterTransformer | ViewFilterTransformer[] | undefined,
): ViewFilterTransformer[] {
  if (!value) return []
  return Array.isArray(value) ? value : [value]
}

/** Identity transformer placeholder for priority/SLA quick filters (API-ready). */
export function prioritySlaQuickFilterTransformer(filter: FilterModelNode): FilterModelNode {
  return filter
}

function applyTransformers(
  filter: FilterModelNode,
  transformers: ViewFilterTransformer[],
): FilterModelNode {
  return transformers.reduce((current, transform) => transform(current), filter)
}

function store(filter: FilterModelNode, persistenceKey?: string): ViewFilterStore {
  return {
    filter,
    isFiltering: !isEmptyModelFilter(filter),
    persistenceKey,
  }
}

/**
 * Build the view filter bundle (universal / inline / insights / quick).
 * Callers own persistence; this helper only shapes the REST AST + URL seed.
 */
export function getViewFilters(
  ctx: { urlKey: string; pathname: string; search?: string },
  options: ViewFiltersOptions = {},
): ViewFilters {
  const urlKey = ctx.urlKey.replace(/^\/+|\/+$/g, '')
  let identifier = options.identifier ?? ctx.pathname
  if (!identifier.startsWith(`/${urlKey}/`)) {
    identifier = `/${urlKey}/${identifier.replace(/^\//, '')}`
  }

  const readUrl = options.readFilterFromUrl !== false
  const param = options.urlFilterParam ?? URL_FILTER_PARAM_DEFAULT
  const fromUrl = readUrl ? readFilterFromSearch(ctx.search ?? '', param) : undefined
  const forceEmpty = readUrl && options.emptyFilterWhenUrlParamMissing === true && fromUrl === undefined

  const transformers = asTransformerList(options.filterTransformer)
  if ((options.type === 'issue' || options.type === 'project') && transformers.length === 0) {
    transformers.push(prioritySlaQuickFilterTransformer)
  }

  const seed = forceEmpty ? {} : applyTransformers(fromUrl ?? {}, transformers)
  const universal = store(seed, identifier)
  const insights = store({}, options.noInsightsFilter ? undefined : `${identifier}/insights`)
  const inline = options.noInlineFilter ? undefined : store({}, `${identifier}/inline`)

  const quick: Record<string, ViewFilterStore> = {}
  if (!options.noQuickFilters) {
    for (const entry of options.quickFilters ?? []) {
      const [name, initial] = typeof entry === 'string' ? [entry, {} as FilterModelNode] : entry
      quick[name] = store(
        applyTransformers(initial ?? {}, transformers),
        `${identifier}/quick/${name}`,
      )
    }
  }

  const collect = (scope: 'all' | 'quick' | 'main' = 'all'): ViewFilterStore[] => {
    if (scope === 'quick') return Object.values(quick).filter(item => item.isFiltering)
    const main = [universal, insights, inline].filter((item): item is ViewFilterStore => Boolean(item))
    if (scope === 'main') return main.filter(item => item.isFiltering)
    return [...main, ...Object.values(quick)].filter(item => item.isFiltering)
  }

  return {
    universal,
    inline,
    insights,
    quick,
    isFiltering: scope => collect(scope).length > 0,
    getFilters: collect,
  }
}

/** Lightweight non-URL view filters (saved view / embedded lists). */
export function getStaticViewFilters(seed: {
  universal?: FilterModelNode
  insights?: FilterModelNode
}): ViewFilters {
  const universal = store(seed.universal ?? {})
  const insights = store(seed.insights ?? {})
  return {
    universal,
    insights,
    inline: undefined,
    quick: {},
    isFiltering: scope => {
      if (scope === 'quick') return false
      return [universal, insights].some(item => item.isFiltering)
    },
    getFilters: scope => {
      if (scope === 'quick') return []
      return [universal, insights].filter(item => item.isFiltering)
    },
  }
}
