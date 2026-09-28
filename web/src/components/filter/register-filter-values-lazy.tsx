/**
 * LS-0519 RegisterFilterValuesShouldBeLazyLoaded — lazy companion that packs
 * Project / Initiative / Team / PR / Notification / … FilterBlocks into the registry.
 */
import { useEffect, useState } from 'react'
import type { FilterEntityType } from './filter-block-types'
import { hasRegisteredFilterValues, registerFilterValues } from './register-filter-values'
import { loadFilterBlocksPack } from './filter-blocks'

export interface RegisterFilterValuesShouldBeLazyLoadedProps {
  type: FilterEntityType
  /** Force re-register even if the pack is already present. */
  force?: boolean
  onReady?: (type: FilterEntityType) => void
  onError?: (error: unknown) => void
}

/**
 * Side-effect component (returns null) that lazy-loads and registers a FilterBlocks pack.
 * Mount once per entity surface (e.g. next to the universal panel).
 */
export function RegisterFilterValuesShouldBeLazyLoaded({
  type,
  force = false,
  onReady,
  onError,
}: RegisterFilterValuesShouldBeLazyLoadedProps) {
  const [status, setStatus] = useState<'idle' | 'loading' | 'ready' | 'error'>(() =>
    !force && hasRegisteredFilterValues(type) ? 'ready' : 'idle',
  )

  useEffect(() => {
    let cancelled = false
    if (!force && hasRegisteredFilterValues(type)) {
      setStatus('ready')
      onReady?.(type)
      return
    }
    setStatus('loading')
    loadFilterBlocksPack(type)
      .then(blocks => {
        if (cancelled) return
        registerFilterValues(type, blocks)
        setStatus('ready')
        onReady?.(type)
      })
      .catch(error => {
        if (cancelled) return
        setStatus('error')
        onError?.(error)
      })
    return () => { cancelled = true }
  }, [type, force, onReady, onError])

  // Expose status for tests via data attribute when wrapped; component itself is invisible.
  void status
  return null
}

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
