import type { FilterEntityType } from './filter-block-types'
import { hasRegisteredFilterValues, registerFilterValues } from './register-filter-values'
import { loadFilterBlocksPack } from './filter-blocks'

/** Imperative helper for non-React callers / tests. */
export async function ensureFilterValuesRegistered(type: FilterEntityType, force = false): Promise<void> {
  if (!force && hasRegisteredFilterValues(type)) return
  const blocks = await loadFilterBlocksPack(type)
  registerFilterValues(type, blocks)
}

/** Preload several packs (e.g. search surfaces that span entity types). */
export async function preloadFilterValuePacks(types: FilterEntityType[]): Promise<void> {
  await Promise.all(types.map(type => ensureFilterValuesRegistered(type)))
}
