import { useCallback, useState } from 'react'

/** Persist per-page agent sidebar open flag (LS-0756 companion for EntityAgentPanel). */
export function usePageAgentSidebarOpen(pageKey: string, fallback = false) {
  const storageKey = `flow:agent-sidebar-open:${pageKey}`
  const [open, setOpenState] = useState(() => {
    try {
      const stored = localStorage.getItem(storageKey)
      return stored === null ? fallback : stored === 'true'
    } catch {
      return fallback
    }
  })
  const setOpen = useCallback((next: boolean | ((current: boolean) => boolean)) => {
    setOpenState(current => {
      const resolved = typeof next === 'function' ? next(current) : next
      try {
        localStorage.setItem(storageKey, String(resolved))
      } catch {
        /* Persistence is best-effort. */
      }
      return resolved
    })
  }, [storageKey])
  return [open, setOpen] as const
}
