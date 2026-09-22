import { useCallback, useContext } from 'react'
import { EntityStoreContext } from '@/store/application-store-context'
import { normalizeEntityType } from '@/store/entity-type'
import type { WorkspaceStore } from '@/store/workspace-store'

export type AiContextItem = {
  type: string
  id: string
}

export type HydratedAiContextItem = AiContextItem & {
  model: unknown | undefined
}

/**
 * LS-0736 — batch `(type,id)[]` → store.hydrateModel before Agent / Ask asks.
 * Linear: useHydrateAiContext → useStore + resolvePromise(hydrateModel…).
 * Returns an async hydrator (Flow has no suspense resolvePromise yet).
 */
export function useHydrateAiContext(): (
  items: AiContextItem[] | null | undefined,
) => Promise<HydratedAiContextItem[]> {
  const store = useContext(EntityStoreContext)
  return useCallback(
    (items) => hydrateAiContext(store, items),
    [store],
  )
}

/** Imperative batch hydrate used by the hook and Agent panel submit paths. */
export async function hydrateAiContext(
  store: WorkspaceStore | null | undefined,
  items: AiContextItem[] | null | undefined,
): Promise<HydratedAiContextItem[]> {
  if (!store || !items?.length) return []
  const seen = new Set<string>()
  const unique: AiContextItem[] = []
  for (const item of items) {
    if (!item?.id || !item.type) continue
    const type = normalizeEntityType(item.type)
    const key = `${type}:${item.id}`
    if (seen.has(key)) continue
    seen.add(key)
    unique.push({ type, id: item.id })
  }
  return Promise.all(
    unique.map(async ({ type, id }) => {
      try {
        const model = await store.hydrateModel(type, id)
        return { type, id, model }
      } catch {
        return { type, id, model: undefined }
      }
    }),
  )
}
