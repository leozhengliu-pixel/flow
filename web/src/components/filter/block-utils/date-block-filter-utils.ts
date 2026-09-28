/**
 * LS-0693 dateBlockFilterUtils — null / relative / absolute / fiscal date blocks
 * plus within→in rewrite used by issues, projects, initiatives, workflow.
 */
import {
  FILTER_NULL_SENTINEL,
  fiscalYearBounds,
  normalizeFiscalYearStartMonth,
  rewriteWithinToIn,
} from '../filter-block-helper'
import type {
  FilterBlockDefinition,
  FilterBlockOption,
  FilterBlockSelection,
  FilterCompareOption,
  FilterModelNode,
} from '../filter-block-types'

export const DATE_NULL_SENTINEL = FILTER_NULL_SENTINEL

/** ISO-8601 duration presets for “before” (future) relative dates. */
export const FUTURE_RELATIVE_DATE_OPTIONS: Array<{ value: string }> = [
  { value: 'P1D' },
  { value: 'P3D' },
  { value: 'P1W' },
  { value: 'P1M' },
  { value: 'P3M' },
  { value: 'P6M' },
  { value: 'P1Y' },
]

/** ISO-8601 duration presets for “after” / past relative dates. */
export const PAST_RELATIVE_DATE_OPTIONS: Array<{ value: string }> = [
  { value: '-P1D' },
  { value: '-P3D' },
  { value: '-P1W' },
  { value: '-P1M' },
  { value: '-P3M' },
  { value: '-P6M' },
  { value: '-P1Y' },
]

export type DateBlockCompare = Extract<
  FilterCompareOption,
  'within' | 'in' | 'before' | 'after' | 'on' | 'not' | 'empty'
>

/** Linear `defaultCompareOptionForDates` — before/after/within based on preset direction. */
export function defaultCompareOptionForDates(
  values: string[],
  preferred: DateBlockCompare = 'within',
): DateBlockCompare {
  if (values.length === 0) return preferred
  if (values.includes(DATE_NULL_SENTINEL)) return 'empty'
  if (values.length === 2 && values.every(v => /^\d{4}-\d{2}-\d{2}/.test(v))) return 'in'
  const first = values[0] ?? ''
  if (first.startsWith('-P') || first.startsWith('-PT')) return 'after'
  if (first.startsWith('P') || first.startsWith('PT')) return 'before'
  if (/^\d{4}-\d{2}-\d{2}/.test(first)) return 'on'
  return preferred
}

export function formatRelativeDateLabel(
  value: string,
  labelPostfix: 'ago' | 'from now',
  labelForNullFilterMatchValue = 'empty',
): string {
  if (value === DATE_NULL_SENTINEL) return labelForNullFilterMatchValue
  if (value === 'P0D' || value === 'PT0S' || value === '-P0D' || value === '-PT0S') return 'today'
  const match = value.match(/^-?P(?:(\d+)Y)?(?:(\d+)M)?(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?)?$/i)
  if (!match) return value
  const [, years, months, weeks, days, hours] = match
  const parts: string[] = []
  if (years) parts.push(`${years} year${years === '1' ? '' : 's'}`)
  if (months) parts.push(`${months} month${months === '1' ? '' : 's'}`)
  if (weeks) parts.push(`${weeks} week${weeks === '1' ? '' : 's'}`)
  if (days) parts.push(`${days} day${days === '1' ? '' : 's'}`)
  if (hours) parts.push(`${hours} hour${hours === '1' ? '' : 's'}`)
  if (parts.length === 0) return 'today'
  return `${parts.join(' ')} ${labelPostfix}`
}

export function relativeDateOptions(
  compare: DateBlockCompare,
  includeFuzzy = true,
): FilterBlockOption[] {
  const suffix = compare === 'before' ? 'from now' : 'ago'
  const source = compare === 'before' ? FUTURE_RELATIVE_DATE_OPTIONS : PAST_RELATIVE_DATE_OPTIONS
  const options: FilterBlockOption[] = source.map(({ value }) => ({
    id: value,
    label: formatRelativeDateLabel(value, suffix),
    keywords: value,
  }))
  if (includeFuzzy) {
    options.push({
      id: '__fuzzy_date__',
      label: 'Custom date or timeframe…',
      keywords: 'custom fuzzy absolute fiscal',
    })
  }
  return options
}

export function fiscalIntervalLabel(
  startIso: string,
  endIso: string,
  fiscalYearStartMonth: number,
): string | undefined {
  const start = new Date(startIso)
  const end = new Date(endIso)
  if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime())) return undefined
  const month = normalizeFiscalYearStartMonth(fiscalYearStartMonth)
  const bounds = fiscalYearBounds(start, month)
  // Match full fiscal year window (±1 day tolerance on end).
  if (
    Math.abs(bounds.start.getTime() - start.getTime()) < 86_400_000 &&
    Math.abs(bounds.end.getTime() - end.getTime()) < 86_400_000
  ) {
    const fy = bounds.start.getFullYear()
    return month === 0 ? `FY ${fy}` : `FY ${fy}/${String(fy + 1).slice(-2)}`
  }
  return undefined
}

export interface CreateDateFilterBlockArgs {
  id: string
  key: string
  name: string
  entityType: FilterBlockDefinition['entityType']
  sortPriority?: number
  defaultCompareOptionForDates?: DateBlockCompare
  labelForNullFilterMatchValue?: string
  includeNullOption?: boolean
}

/** Factory mirroring Linear `createDateFilterBlock` (REST AST, no GraphQL). */
export function createDateFilterBlock(args: CreateDateFilterBlockArgs): FilterBlockDefinition {
  const preferred = args.defaultCompareOptionForDates ?? 'within'
  const nullLabel = args.labelForNullFilterMatchValue ?? 'empty'
  return {
    id: args.id,
    key: args.key,
    name: args.name,
    valueType: 'dateOrInterval',
    inputType: 'fuzzyDate',
    entityType: args.entityType,
    sortPriority: args.sortPriority,
    defaultCompareOption: values => defaultCompareOptionForDates(values, preferred),
    options: () => {
      const options = relativeDateOptions(preferred)
      if (args.includeNullOption !== false) {
        options.unshift({
          id: DATE_NULL_SENTINEL,
          label: nullLabel,
          keywords: 'null empty unset',
        })
      }
      return options
    },
    toModelFilter: (selection: FilterBlockSelection): FilterModelNode => {
      const compare = rewriteWithinToIn(
        selection.compareOption === 'empty'
          ? 'empty'
          : defaultCompareOptionForDates(selection.values, selection.compareOption as DateBlockCompare),
      )
      if (compare === 'empty' || selection.values.includes(DATE_NULL_SENTINEL)) {
        return { field: args.key, operator: 'empty', values: [] }
      }
      return {
        field: args.key,
        operator: compare,
        values: selection.values.filter(v => v !== DATE_NULL_SENTINEL && v !== '__fuzzy_date__'),
      }
    },
  }
}

export const dateBlockFilterUtils = {
  DATE_NULL_SENTINEL,
  FUTURE_RELATIVE_DATE_OPTIONS,
  PAST_RELATIVE_DATE_OPTIONS,
  defaultCompareOptionForDates,
  formatRelativeDateLabel,
  relativeDateOptions,
  fiscalIntervalLabel,
  createDateFilterBlock,
  rewriteWithinToIn,
  fiscalYearBounds,
  normalizeFiscalYearStartMonth,
}
