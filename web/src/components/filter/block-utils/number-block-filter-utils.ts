/**
 * LS-0710 numberBlockFilterUtils — eq/gt/lt/null FreeForm number blocks (estimate, etc.).
 */
import { FILTER_NULL_SENTINEL } from '../filter-block-helper'
import type {
  FilterBlockDefinition,
  FilterBlockSelection,
  FilterCompareOption,
  FilterEntityType,
  FilterModelNode,
} from '../filter-block-types'

export const NUMBER_COMPARE_OPTIONS: Array<{ label: string; value: FilterCompareOption }> = [
  { label: '=', value: 'eq' },
  { label: '>', value: 'gt' },
  { label: '<', value: 'lt' },
  { label: '≥', value: 'gte' },
  { label: '≤', value: 'lte' },
  { label: 'is empty', value: 'empty' },
]

/** Parse typed FreeForm number input — supports `12`, `1.5`, `3K`, `2M`. */
export function parseNumberFilterInput(raw: string | null | undefined): {
  parsedNumber: number | undefined
  rawInput: string
} {
  const input = (raw ?? '').trim()
  if (!input) return { parsedNumber: undefined, rawInput: input }
  const scaled = input.match(/^(\d+(?:\.\d+)?)\s*([KkMm])$/)
  if (scaled) {
    const base = Number.parseFloat(scaled[1]!)
    const factor = scaled[2]!.toUpperCase() === 'K' ? 1_000 : 1_000_000
    return { parsedNumber: base * factor, rawInput: input }
  }
  const cleaned = Number.parseInt(input.replace(/[.,$\s]/g, ''), 10)
  return { parsedNumber: Number.isNaN(cleaned) ? undefined : cleaned, rawInput: input }
}

export function numberSelectionToModelFilter(
  key: string,
  selection: FilterBlockSelection,
): FilterModelNode {
  if (selection.compareOption === 'empty' || selection.values.includes(FILTER_NULL_SENTINEL)) {
    return { field: key, operator: 'empty', values: [] }
  }
  const values = selection.values
    .map(value => parseNumberFilterInput(value).parsedNumber)
    .filter((value): value is number => value !== undefined)
    .map(String)
  return {
    field: key,
    operator: selection.compareOption,
    values,
  }
}

export interface CreateNumberFilterBlockArgs {
  id: string
  key: string
  name: string
  entityType: FilterEntityType
  sortPriority?: number
  subActionPlaceholderSuffix?: string
}

export function createNumberFilterBlock(args: CreateNumberFilterBlockArgs): FilterBlockDefinition {
  return {
    id: args.id,
    key: args.key,
    name: args.name,
    valueType: 'number',
    inputType: 'freeForm',
    entityType: args.entityType,
    sortPriority: args.sortPriority,
    defaultCompareOption: 'eq',
    toModelFilter: selection => numberSelectionToModelFilter(args.key, selection),
  }
}

/** Issue estimate filter — unlocks estimate as a real FilterBlock (was display-only). */
export function createEstimateFilterBlock(
  entityType: FilterEntityType = 'issue',
): FilterBlockDefinition {
  return createNumberFilterBlock({
    id: 'estimate',
    key: 'estimate',
    name: 'Estimate',
    entityType,
    sortPriority: 45,
    subActionPlaceholderSuffix: 'points',
  })
}

export const numberBlockFilterUtils = {
  NUMBER_COMPARE_OPTIONS,
  parseNumberFilterInput,
  numberSelectionToModelFilter,
  createNumberFilterBlock,
  createEstimateFilterBlock,
  FILTER_NULL_SENTINEL,
}
