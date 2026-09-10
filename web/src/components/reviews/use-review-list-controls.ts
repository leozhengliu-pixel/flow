import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { parseReviewDisplay, parseReviewFilters, type ReviewDisplay, type ReviewFilter, type ReviewView } from './review-list-model'

export function useReviewListControls(workspaceId: string, userId: string, view: ReviewView) {
  const [search, setSearch] = useSearchParams()
  const rawFilters = search.get('reviewFilters')
  const filters = useMemo(() => parseReviewFilters(rawFilters), [rawFilters])
  const key = `flow:reviews:${workspaceId}:${userId}:${view}`
  const initial = useMemo(() => {
    try { return parseReviewDisplay(localStorage.getItem(key), view) } catch { return parseReviewDisplay(null, view) }
  }, [key, view])
  const [preferences, setPreferences] = useState<Record<string, ReviewDisplay>>({})
  const display = preferences[key] ?? initial
  const setDisplay = (next: ReviewDisplay) => {
    setPreferences(current => ({ ...current, [key]: next }))
    try { localStorage.setItem(key, JSON.stringify(next)) } catch { /* Preferences still apply when storage is unavailable. */ }
  }
  const setFilters = (next: ReviewFilter[]) => setSearch(current => {
    const params = new URLSearchParams(current)
    if (next.length) params.set('reviewFilters', JSON.stringify(next))
    else params.delete('reviewFilters')
    return params
  }, { replace: true })
  return { filters, setFilters, display, setDisplay }
}
