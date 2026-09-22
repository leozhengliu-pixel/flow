import { useContext, useEffect, useMemo, useState } from 'react'
import { extractModelsFromMarkdown, type ModelRef } from '@/lib/model-from-url'
import { EntityStoreContext } from '@/store/application-store-context'
import type { WorkspaceStore } from '@/store/workspace-store'

export type HydratedMarkdownModel = {
  type: string
  id: string
  model: unknown
}

function resolvedIdFromModel(model: unknown, fallback: string): string {
  if (typeof model === 'object' && model && 'id' in model && typeof (model as { id: unknown }).id === 'string') {
    return (model as { id: string }).id
  }
  return fallback
}

async function hydrateRefs(
  store: WorkspaceStore,
  refs: ModelRef[],
): Promise<HydratedMarkdownModel[]> {
  const out: HydratedMarkdownModel[] = []
  for (const ref of refs) {
    try {
      const model = await store.hydrateModel(ref.type, ref.id)
      if (model == null) continue
      out.push({ type: ref.type, id: resolvedIdFromModel(model, ref.id), model })
    } catch {
      /* skip failed hydrate */
    }
  }
  return out
}

/**
 * LS-0738 — parse markdown/href → type/id → store.hydrateModel.
 * Linear: useHydrateModelsInMarkdown + MarkdownViewer.
 * Safe outside WorkspaceStoreProvider (no-op).
 */
export function useHydrateModelsInMarkdown(
  markdown: string | null | undefined,
): {
  refs: ModelRef[]
  models: HydratedMarkdownModel[]
  pending: boolean
} {
  const store = useContext(EntityStoreContext)
  const refs = useMemo(() => extractModelsFromMarkdown(markdown), [markdown])
  const refsKey = useMemo(() => refs.map((ref) => `${ref.type}:${ref.id}`).join(','), [refs])
  const [models, setModels] = useState<HydratedMarkdownModel[]>([])
  const [pending, setPending] = useState(false)

  useEffect(() => {
    if (!store || refs.length === 0) {
      setModels([])
      setPending(false)
      return
    }
    let cancelled = false
    setPending(true)
    void hydrateRefs(store, refs).then((next) => {
      if (cancelled) return
      setModels(next)
      setPending(false)
    })
    return () => {
      cancelled = true
    }
    // refsKey serializes refs; store is the hydrate target.
    // eslint-disable-next-line react-hooks/exhaustive-deps -- refsKey serializes refs
  }, [store, refsKey])

  return { refs, models, pending }
}

/** Imperative helper for tests / non-React callers. */
export async function hydrateModelsInMarkdown(
  store: WorkspaceStore | null | undefined,
  markdown: string | null | undefined,
): Promise<HydratedMarkdownModel[]> {
  if (!store) return []
  return hydrateRefs(store, extractModelsFromMarkdown(markdown))
}
