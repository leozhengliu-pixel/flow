/**
 * LS-0156 / LS-0224 — view preference helpers for CustomViewEditPage.
 */

import { useCallback, useMemo, useState } from 'react'
import type { MyIssuesDisplayOptions } from '@/components/my-issues/my-issues-surface'
import type { SavedView } from '@/types/flow'

export type ViewPreferencesSnapshot = {
  display: MyIssuesDisplayOptions
  insights?: Record<string, unknown>
}

export function displayFromSavedViewPrefs(view: SavedView | undefined, fallback: MyIssuesDisplayOptions): MyIssuesDisplayOptions {
  if (!view?.display || typeof view.display !== 'object') return fallback
  return { ...fallback, ...(view.display as Partial<MyIssuesDisplayOptions>) }
}

export function useViewPreferences(options: {
  view?: SavedView
  fallback: MyIssuesDisplayOptions
  storageKey?: string
}) {
  const { view, fallback, storageKey } = options
  const initial = useMemo(() => {
    if (storageKey && typeof localStorage !== 'undefined') {
      try {
        const raw = localStorage.getItem(storageKey)
        if (raw) return { ...fallback, ...(JSON.parse(raw) as Partial<MyIssuesDisplayOptions>) }
      } catch { /* ignore */ }
    }
    return displayFromSavedViewPrefs(view, fallback)
  }, [fallback, storageKey, view])

  const [display, setDisplay] = useState<MyIssuesDisplayOptions>(initial)
  const [insights, setInsights] = useState<Record<string, unknown> | undefined>(
    () => (view?.insights as Record<string, unknown> | undefined),
  )

  const persist = useCallback((next: MyIssuesDisplayOptions) => {
    setDisplay(next)
    if (!storageKey || typeof localStorage === 'undefined') return
    try { localStorage.setItem(storageKey, JSON.stringify(next)) } catch { /* ignore */ }
  }, [storageKey])

  const snapshot = useCallback((): ViewPreferencesSnapshot => ({
    display,
    insights,
  }), [display, insights])

  const applyAsDefault = useCallback(async (
    commit: (prefs: ViewPreferencesSnapshot) => Promise<void> | void,
  ) => {
    await commit(snapshot())
  }, [snapshot])

  return {
    display,
    setDisplay: persist,
    insights,
    setInsights,
    snapshot,
    applyAsDefault,
  }
}
