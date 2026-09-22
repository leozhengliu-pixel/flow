/**
 * LS-0782 useViewPreferencesFromQueryParams — apply layout/ordering/grouping/show*
 * from URL search params, then strip consumed keys (Open Link With Filters).
 */
import { useLayoutEffect, useRef } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import type { ViewPreferencesHandle } from '@/lib/view-preferences'

export const VIEW_PREF_QUERY_KEYS = {
  layout: 'layout',
  ordering: 'ordering',
  orderingDirection: 'orderingDirection',
  grouping: 'grouping',
  subGrouping: 'subGrouping',
  showCompletedIssues: 'showCompletedIssues',
  showCompletedProjects: 'showCompletedProjects',
  showSubIssues: 'showSubIssues',
  showTriageIssues: 'showTriageIssues',
  zoom: 'zoom',
  zoomScale: 'zoomScale',
} as const

const ISSUE_LAYOUTS = new Set(['list', 'board', 'timeline', 'calendar'])
const PROJECT_LAYOUTS = new Set(['list', 'board', 'timeline', 'dashboard'])
const ORDERING_DIRECTIONS = new Set(['asc', 'desc'])
const SHOW_COMPLETED = new Set(['all', 'pastWeek', 'pastMonth', 'pastDay', 'none', 'true', 'false'])
const ISSUE_GROUPINGS = new Set([
  'none',
  'status',
  'assignee',
  'priority',
  'project',
  'cycle',
  'label',
  'team',
  'focus',
])
const PROJECT_GROUPINGS = new Set(['none', 'status', 'lead', 'priority', 'health', 'team'])
const ISSUE_ORDERINGS = new Set([
  'manual',
  'priority',
  'status',
  'title',
  'updated',
  'created',
  'due',
  'importance',
  'dateCreated',
  'reverseDateCreated',
  'startedTriage',
])
const PROJECT_ORDERINGS = new Set(['manual', 'name', 'status', 'priority', 'targetDate', 'health', 'updated'])
const TRIAGE_ORDERINGS = new Set([
  'priority',
  'startedTriage',
  'dateCreated',
  'reverseDateCreated',
  'updated',
])
const ZOOM_LEVELS = new Set(['day', 'week', 'month', 'quarter', 'year'])

export type ViewPreferencesQuerySurface = 'issues' | 'projects' | 'triage'

export type UseViewPreferencesFromQueryParamsOptions = {
  surface?: ViewPreferencesQuerySurface
  /** Called once when any preference was applied (Open Link With Filters). */
  onOpenLinkWithFilters?: (surface: ViewPreferencesQuerySurface) => void
  enabled?: boolean
}

function parseBool(raw: string): boolean | undefined {
  if (raw === 'true') return true
  if (raw === 'false') return false
  return undefined
}

function stripParams(search: string, keys: string[]): string {
  const params = new URLSearchParams(search)
  for (const key of keys) params.delete(key)
  const next = params.toString()
  return next ? `?${next}` : ''
}

/**
 * Apply known view-preference query keys onto a ViewPreferencesHandle, then
 * replace the URL without those keys.
 */
export function useViewPreferencesFromQueryParams(
  handle: ViewPreferencesHandle | null | undefined,
  options: UseViewPreferencesFromQueryParamsOptions = {},
) {
  const location = useLocation()
  const navigate = useNavigate()
  const applied = useRef(false)
  const surface = options.surface ?? 'issues'
  const enabled = options.enabled !== false

  useLayoutEffect(() => {
    if (!enabled || !handle || applied.current) return
    const params = new URLSearchParams(location.search)
    const consumed: string[] = []
    let changed = false

    const take = (key: string) => {
      const value = params.get(key)
      if (value == null || value === '') return ''
      return value
    }

    if (surface === 'issues' || surface === 'triage') {
      const layout = take(VIEW_PREF_QUERY_KEYS.layout)
      if (layout && ISSUE_LAYOUTS.has(layout)) {
        handle.setPreference('layout', layout)
        consumed.push(VIEW_PREF_QUERY_KEYS.layout)
        changed = true
      }
      const ordering = take(VIEW_PREF_QUERY_KEYS.ordering)
      if (ordering) {
        if (surface === 'triage' && TRIAGE_ORDERINGS.has(ordering)) {
          const mapped =
            ordering === 'dateCreated' || ordering === 'reverseDateCreated'
              ? 'startedTriage'
              : ordering
          handle.setPreference('triageViewOrdering', mapped)
          consumed.push(VIEW_PREF_QUERY_KEYS.ordering)
          changed = true
        } else if (surface === 'issues' && ISSUE_ORDERINGS.has(ordering)) {
          handle.setPreference('viewOrdering', ordering)
          consumed.push(VIEW_PREF_QUERY_KEYS.ordering)
          changed = true
        }
      }
      const direction = take(VIEW_PREF_QUERY_KEYS.orderingDirection)
      if (direction && ORDERING_DIRECTIONS.has(direction)) {
        handle.setPreference('viewOrderingDirection', direction)
        consumed.push(VIEW_PREF_QUERY_KEYS.orderingDirection)
        changed = true
      }
      const grouping = take(VIEW_PREF_QUERY_KEYS.grouping)
      if (grouping && ISSUE_GROUPINGS.has(grouping)) {
        handle.setPreference('issueGrouping', grouping)
        consumed.push(VIEW_PREF_QUERY_KEYS.grouping)
        changed = true
      }
      const subGrouping = take(VIEW_PREF_QUERY_KEYS.subGrouping)
      if (subGrouping && ISSUE_GROUPINGS.has(subGrouping)) {
        handle.setPreference('issueSubGrouping', subGrouping)
        consumed.push(VIEW_PREF_QUERY_KEYS.subGrouping)
        changed = true
      }
      const showCompleted = take(VIEW_PREF_QUERY_KEYS.showCompletedIssues)
      if (showCompleted && SHOW_COMPLETED.has(showCompleted)) {
        handle.setPreference('showCompletedIssues', showCompleted)
        consumed.push(VIEW_PREF_QUERY_KEYS.showCompletedIssues)
        changed = true
      }
      const showSub = take(VIEW_PREF_QUERY_KEYS.showSubIssues)
      const showSubBool = parseBool(showSub)
      if (showSubBool !== undefined) {
        handle.setPreference('showSubIssues', showSubBool)
        consumed.push(VIEW_PREF_QUERY_KEYS.showSubIssues)
        changed = true
      }
      const showTriage = take(VIEW_PREF_QUERY_KEYS.showTriageIssues)
      const showTriageBool = parseBool(showTriage)
      if (showTriageBool !== undefined) {
        handle.setPreference('showTriageIssues', showTriageBool)
        consumed.push(VIEW_PREF_QUERY_KEYS.showTriageIssues)
        changed = true
      }
    }

    if (surface === 'projects') {
      const layout = take(VIEW_PREF_QUERY_KEYS.layout)
      if (layout && PROJECT_LAYOUTS.has(layout)) {
        handle.setPreference('projectLayout', layout)
        consumed.push(VIEW_PREF_QUERY_KEYS.layout)
        changed = true
      }
      const ordering = take(VIEW_PREF_QUERY_KEYS.ordering)
      if (ordering && PROJECT_ORDERINGS.has(ordering)) {
        handle.setPreference('projectViewOrdering', ordering)
        consumed.push(VIEW_PREF_QUERY_KEYS.ordering)
        changed = true
      }
      const grouping = take(VIEW_PREF_QUERY_KEYS.grouping)
      if (grouping && PROJECT_GROUPINGS.has(grouping)) {
        handle.setPreference('projectGrouping', grouping)
        consumed.push(VIEW_PREF_QUERY_KEYS.grouping)
        changed = true
      }
      const subGrouping = take(VIEW_PREF_QUERY_KEYS.subGrouping)
      if (subGrouping && PROJECT_GROUPINGS.has(subGrouping)) {
        handle.setPreference('projectSubGrouping', subGrouping)
        consumed.push(VIEW_PREF_QUERY_KEYS.subGrouping)
        changed = true
      }
      const zoom = take(VIEW_PREF_QUERY_KEYS.zoom)
      if (zoom && ZOOM_LEVELS.has(zoom)) {
        handle.setPreference('projectZoomLevel', zoom)
        consumed.push(VIEW_PREF_QUERY_KEYS.zoom)
        changed = true
      }
      const zoomScale = take(VIEW_PREF_QUERY_KEYS.zoomScale)
      const scale = Number(zoomScale)
      if (zoomScale && Number.isFinite(scale) && scale >= 0.5 && scale <= 100) {
        handle.setPreference('timelineZoomScale', scale)
        consumed.push(VIEW_PREF_QUERY_KEYS.zoomScale)
        changed = true
      }
      const showCompleted = take(VIEW_PREF_QUERY_KEYS.showCompletedProjects)
      if (showCompleted && SHOW_COMPLETED.has(showCompleted)) {
        handle.setPreference('showCompletedProjects', showCompleted)
        consumed.push(VIEW_PREF_QUERY_KEYS.showCompletedProjects)
        changed = true
      }
    }

    if (!changed) {
      applied.current = true
      return
    }

    handle.save(true)
    options.onOpenLinkWithFilters?.(surface)
    applied.current = true
    const nextSearch = stripParams(location.search, consumed)
    if (nextSearch !== location.search) {
      navigate({ pathname: location.pathname, search: nextSearch, hash: location.hash }, { replace: true })
    }
  }, [enabled, handle, location.hash, location.pathname, location.search, navigate, options, surface])
}

/** Pure helper for tests — apply params to a handle without navigation. */
export function applyViewPreferencesFromSearchParams(
  handle: ViewPreferencesHandle,
  search: string,
  surface: ViewPreferencesQuerySurface = 'issues',
): string[] {
  const params = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search)
  const consumed: string[] = []
  const get = (key: string) => params.get(key) || ''

  if (surface === 'issues') {
    const layout = get(VIEW_PREF_QUERY_KEYS.layout)
    if (ISSUE_LAYOUTS.has(layout)) {
      handle.setPreference('layout', layout)
      consumed.push(VIEW_PREF_QUERY_KEYS.layout)
    }
    const ordering = get(VIEW_PREF_QUERY_KEYS.ordering)
    if (ISSUE_ORDERINGS.has(ordering)) {
      handle.setPreference('viewOrdering', ordering)
      consumed.push(VIEW_PREF_QUERY_KEYS.ordering)
    }
    const grouping = get(VIEW_PREF_QUERY_KEYS.grouping)
    if (ISSUE_GROUPINGS.has(grouping)) {
      handle.setPreference('issueGrouping', grouping)
      consumed.push(VIEW_PREF_QUERY_KEYS.grouping)
    }
    const showSub = parseBool(get(VIEW_PREF_QUERY_KEYS.showSubIssues))
    if (showSub !== undefined) {
      handle.setPreference('showSubIssues', showSub)
      consumed.push(VIEW_PREF_QUERY_KEYS.showSubIssues)
    }
  }
  if (surface === 'projects') {
    const layout = get(VIEW_PREF_QUERY_KEYS.layout)
    if (PROJECT_LAYOUTS.has(layout)) {
      handle.setPreference('projectLayout', layout)
      consumed.push(VIEW_PREF_QUERY_KEYS.layout)
    }
  }
  if (surface === 'triage') {
    const ordering = get(VIEW_PREF_QUERY_KEYS.ordering)
    if (TRIAGE_ORDERINGS.has(ordering)) {
      const mapped =
        ordering === 'dateCreated' || ordering === 'reverseDateCreated'
          ? 'startedTriage'
          : ordering
      handle.setPreference('triageViewOrdering', mapped)
      consumed.push(VIEW_PREF_QUERY_KEYS.ordering)
    }
  }
  if (consumed.length) handle.save(true)
  return consumed
}
