/**
 * LS-0732 useFilterValidation — validateBlock / highlightInvalidBlocks / isValid
 * before incomplete blocks are shipped to saved views.
 */
import { useMemo } from 'react'
import type {
  FilterBlockDefinition,
  FilterBlockSelection,
  FilterModelNode,
  FilterValidationContext,
} from './filter-block-types'
import { findBlocks } from './filter-block-helper'

/** Compare options that do not require selected values to be valid. */
const VALUELESS_COMPARE = new Set(['not', 'neither'])

/** Compare options that require *every* selected value to be valid. */
const ALL_MUST_BE_VALID = new Set(['all', 'notAll'])

function blockKey(block: FilterBlockDefinition, selection: FilterBlockSelection): string {
  return `${block.id}:${JSON.stringify(selection)}`
}

function isBlockValid(
  block: FilterBlockDefinition,
  selection: FilterBlockSelection,
  context?: FilterValidationContext,
): boolean {
  const compare = block.getCompareOption?.(selection) ?? selection.compareOption
  if (VALUELESS_COMPARE.has(compare)) return true

  const values = block.selectedValues?.(selection) ?? selection.values
  if (!values.length) {
    // Empty selection is incomplete (invalid) unless the compare is explicitly empty.
    return compare === 'empty'
  }

  const flags = values.map(value => {
    if (block.isValueValid) return block.isValueValid(value, context)
    if (context?.knownValueIds) return context.knownValueIds.has(value)
    return value.trim().length > 0
  })

  if (flags.some(flag => !flag)) {
    if (flags.every(flag => !flag)) return false
    return !ALL_MUST_BE_VALID.has(compare)
  }
  return true
}

export interface UseFilterValidationArgs {
  filter: FilterModelNode | null | undefined
  blocks: FilterBlockDefinition[]
  context?: FilterValidationContext
  /** When false, callers can suppress red outlines while editing. Default true. */
  highlightInvalidBlocks?: boolean
}

export interface FilterValidationApi {
  isValid: boolean
  highlightInvalidBlocks: boolean
  validateBlock: (block: FilterBlockDefinition, selection: FilterBlockSelection) => boolean
  validateSelectedValue: (block: FilterBlockDefinition, selection: FilterBlockSelection, value: string) => boolean
}

export function useFilterValidation({
  filter,
  blocks,
  context,
  highlightInvalidBlocks = true,
}: UseFilterValidationArgs): FilterValidationApi {
  const [isValid, validity] = useMemo(() => {
    try {
      let valid = true
      const found = filter ? findBlocks(filter, blocks) : []
      const map = new Map<string, { isValid: boolean }>()
      found.forEach(([block, selection]) => {
        const ok = isBlockValid(block, selection, context)
        if (!ok) valid = false
        map.set(blockKey(block, selection), { isValid: ok })
      })
      return [valid, map] as const
    } catch {
      return [false, new Map<string, { isValid: boolean }>()] as const
    }
  }, [filter, blocks, context])

  return {
    isValid,
    highlightInvalidBlocks,
    validateBlock: (block, selection) => validity.get(blockKey(block, selection))?.isValid ?? true,
    validateSelectedValue: (block, _selection, value) => block.isValueValid?.(value, context) ?? true,
  }
}

/** Pure helper for non-React callers (saved-view submit guards). */
export function validateFilterModel(
  filter: FilterModelNode | null | undefined,
  blocks: FilterBlockDefinition[],
  context?: FilterValidationContext,
): boolean {
  if (!filter) return true
  return findBlocks(filter, blocks).every(([block, selection]) => isBlockValid(block, selection, context))
}
