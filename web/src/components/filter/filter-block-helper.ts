/**
 * LS-0273 FilterBlockHelper — shared kernel for combining model filters,
 * FreeForm scoring, fiscal-year helpers, and compare-option catalogs.
 */
import type {
  FilterBlockDefinition,
  FilterBlockSelection,
  FilterCombineOperator,
  FilterCompareOption,
  FilterCompareOptionChoice,
  FilterModelNode,
  FilterBlockValueType,
} from './filter-block-types'

const NULL_SENTINEL = '____null____'

/** Unwrap a node that is already nested under the same combine operator. */
function unwrapMatching(operator: FilterCombineOperator, node: FilterModelNode | null | undefined): FilterModelNode[] {
  if (node == null) return []
  const nested = node[operator]
  if (Array.isArray(nested)) return nested as FilterModelNode[]
  if (isEmptyModelFilter(node)) return []
  return [node]
}

export function isEmptyModelFilter(node: FilterModelNode | null | undefined): boolean {
  if (node == null) return true
  const keys = Object.keys(node).filter(key => node[key] !== undefined)
  return keys.length === 0
}

/**
 * Combine model filters under `and` or `or`, flattening same-operator children
 * (Linear `combineModelFilters` parity). Produces Flow REST AST nodes.
 */
export function combineModelFilters(
  operator: FilterCombineOperator,
  filters: Array<FilterModelNode | null | undefined>,
): FilterModelNode {
  const flattened = filters.flatMap(filter => unwrapMatching(operator, filter))
  if (flattened.length === 0) return {}
  if (flattened.length === 1) return flattened[0] ?? {}
  return operator === 'or' ? { or: flattened } : { and: flattened }
}

/**
 * Map of named sub-filters whose combined result is OR'd together
 * (Linear CombinedFilter Map used by FreeForm / multi-source overlays).
 */
export class CombinedFilterMap {
  private readonly filters = new Map<string, FilterModelNode>()

  update(id: string, filter: FilterModelNode): FilterModelNode {
    this.filters.set(id, filter)
    return this.combinedFilter()
  }

  remove(id: string): FilterModelNode | undefined {
    this.filters.delete(id)
    if (this.filters.size > 0) return this.combinedFilter()
    return undefined
  }

  clear(): void {
    this.filters.clear()
  }

  get size(): number {
    return this.filters.size
  }

  combinedFilter(operator: FilterCombineOperator = 'or'): FilterModelNode {
    return combineModelFilters(operator, Array.from(this.filters.values()))
  }
}

/** Fingerprint for batch match-count caches (Linear getItemsFingerprint). */
export function getItemsFingerprint(items: Array<{ id: string }>): string {
  if (items.length === 0) return 'empty'
  return `${items.length}:${items[0].id}:${items[items.length - 1].id}`
}

/** FreeForm text scoring — skip fuzzy scoring for free-form input blocks. */
export function shouldSkipFilterScoring(block: Pick<FilterBlockDefinition, 'inputType'>): boolean {
  return block.inputType === 'freeForm'
}

export interface FreeFormScore {
  value: string
  label: string
  score: number
}

/**
 * Lightweight FreeForm / SearchScore-style ranking for filter option lists.
 * Higher score = better match. Empty query returns options with score 0 in order.
 */
export function scoreFreeFormOptions(
  query: string,
  options: Array<{ id: string; label: string; keywords?: string }>,
): FreeFormScore[] {
  const normalized = query.trim().toLowerCase()
  if (!normalized) {
    return options.map(option => ({ value: option.id, label: option.label, score: 0 }))
  }
  return options
    .map(option => {
      const label = option.label.toLowerCase()
      const haystack = `${label} ${option.keywords ?? ''}`.toLowerCase()
      let score = 0
      if (label === normalized) score = 100
      else if (label.startsWith(normalized)) score = 80
      else if (haystack.includes(normalized)) score = 50
      else {
        const tokens = normalized.split(/\s+/).filter(Boolean)
        const hits = tokens.filter(token => haystack.includes(token)).length
        if (hits === 0) score = -1
        else score = Math.round((hits / tokens.length) * 40)
      }
      return { value: option.id, label: option.label, score }
    })
    .filter(item => item.score >= 0)
    .sort((a, b) => b.score - a.score || a.label.localeCompare(b.label))
}

/** Calendar month (0–11) when the organization's fiscal year starts. Defaults to January. */
export function normalizeFiscalYearStartMonth(month: number | null | undefined): number {
  if (typeof month !== 'number' || !Number.isFinite(month)) return 0
  return ((Math.trunc(month) % 12) + 12) % 12
}

export function fiscalYearBounds(reference: Date, fiscalYearStartMonth: number): { start: Date; end: Date } {
  const startMonth = normalizeFiscalYearStartMonth(fiscalYearStartMonth)
  const year = reference.getFullYear()
  const month = reference.getMonth()
  const fiscalYear = month >= startMonth ? year : year - 1
  const start = new Date(fiscalYear, startMonth, 1)
  const end = new Date(fiscalYear + 1, startMonth, 1)
  end.setMilliseconds(end.getMilliseconds() - 1)
  return { start, end }
}

/**
 * Linear date UX: `within` on a range rewrites to `in` for display / REST.
 */
export function rewriteWithinToIn(compare: FilterCompareOption): FilterCompareOption {
  return compare === 'within' ? 'in' : compare
}

export function resolveDefaultCompareOption(
  block: Pick<FilterBlockDefinition, 'defaultCompareOption' | 'valueType'>,
  values: string[] = [],
): FilterCompareOption {
  const configured = block.defaultCompareOption
  if (typeof configured === 'function') return configured(values)
  if (configured) return configured
  switch (block.valueType) {
    case 'date':
    case 'dateOrInterval':
    case 'dateRange':
      return 'within'
    case 'number':
      return 'eq'
    case 'bool':
      return 'is'
    case 'str':
    case 'string':
      return 'contains'
    default:
      return values.length > 1 ? 'is' : 'is'
  }
}

export function compareOptionsForValueType(
  valueType: FilterBlockValueType,
  selectedCount = 1,
): FilterCompareOptionChoice[] {
  if (valueType === 'date' || valueType === 'dateOrInterval' || valueType === 'dateRange') {
    return [
      { label: 'is', value: 'in' },
      { label: 'is not', value: 'not' },
      { label: 'before', value: 'before' },
      { label: 'after', value: 'after' },
      { label: 'on', value: 'on' },
      { label: 'within', value: 'within' },
    ]
  }
  if (valueType === 'equalValue') {
    return [
      { label: selectedCount > 1 ? 'is any of' : 'is', value: 'is' },
      { label: 'is not', value: 'not' },
    ]
  }
  if (valueType === 'number') {
    return [
      { label: '=', value: 'eq' },
      { label: '>', value: 'gt' },
      { label: '<', value: 'lt' },
      { label: '≥', value: 'gte' },
      { label: '≤', value: 'lte' },
      { label: 'is empty', value: 'empty' },
    ]
  }
  if (valueType === 'bool') {
    return [
      { label: 'is', value: 'is' },
      { label: 'is not', value: 'not' },
    ]
  }
  if (valueType === 'str') {
    return [
      { label: selectedCount > 1 ? 'contains any' : 'contains', value: 'contains' },
      { label: 'does not contain', value: 'notContains' },
    ]
  }
  return [
    { label: selectedCount > 1 ? 'contains any' : 'contains', value: 'contains' },
    { label: 'does not contain', value: 'notContains' },
  ]
}

/** Map Flow UI operators (`is` / `isNot`) onto FilterBlock compare options. */
export function uiOperatorToCompare(operator: 'is' | 'isNot'): FilterCompareOption {
  return operator === 'isNot' ? 'not' : 'is'
}

export function compareToUiOperator(compare: FilterCompareOption): 'is' | 'isNot' {
  return compare === 'not' || compare === 'neither' || compare === 'notAll' || compare === 'notContains'
    ? 'isNot'
    : 'is'
}

/** Build a REST leaf from a block definition + selection. */
export function blockSelectionToModelFilter(
  block: FilterBlockDefinition,
  selection: FilterBlockSelection,
): FilterModelNode {
  if (block.toModelFilter) return block.toModelFilter(selection)
  const compare = selection.compareOption
  const values = selection.values.filter(value => value !== NULL_SENTINEL)
  if (compare === 'empty') {
    return { field: block.key, operator: 'empty', values: [] }
  }
  return {
    field: block.key,
    operator: rewriteWithinToIn(compare),
    values,
  }
}

/**
 * Walk a model filter tree and yield `[block, selection]` pairs for registered blocks.
 * Used by useFilterValidation (Linear `findBlocks` parity, REST AST shape).
 */
export function findBlocks(
  filter: FilterModelNode | null | undefined,
  blocks: FilterBlockDefinition[],
): Array<[FilterBlockDefinition, FilterBlockSelection]> {
  if (!filter || isEmptyModelFilter(filter)) return []
  const byKey = new Map(blocks.map(block => [block.key, block]))
  const results: Array<[FilterBlockDefinition, FilterBlockSelection]> = []

  const visit = (node: FilterModelNode) => {
    if (Array.isArray(node.and)) {
      node.and.forEach(visit)
      return
    }
    if (Array.isArray(node.or)) {
      node.or.forEach(visit)
      return
    }
    if (typeof node.field !== 'string') return
    const block = byKey.get(node.field) ?? blocks.find(item => item.id === node.field)
    if (!block) return
    const operator = typeof node.operator === 'string' ? node.operator : 'is'
    const values = Array.isArray(node.values) ? node.values.map(String) : []
    const selection: FilterBlockSelection = {
      compareOption: (operator as FilterCompareOption) || resolveDefaultCompareOption(block, values),
      values,
    }
    results.push([block, selection])
  }

  visit(filter)
  return results
}

export function createBlockSelection(
  values: string[],
  compareOption?: FilterCompareOption,
): FilterBlockSelection {
  return {
    compareOption: compareOption ?? (values.length > 1 ? 'is' : 'is'),
    values: [...values],
  }
}

export { NULL_SENTINEL as FILTER_NULL_SENTINEL }
