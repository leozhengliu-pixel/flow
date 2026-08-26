import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { MyIssuesBulkAction } from './my-issues-bulk-action-bar'
import type { MyIssuesDetailsSummary, MyIssuesSummaryItem } from './my-issues-details-pane'
import { consolidateFilters, toggleFilterOption, updateFilterOperator, updateFilterValues, type MyIssuesAppliedFilter, type MyIssuesFilterOperator } from './my-issues-filter-types'
import type { MyIssuesGroupData, MyIssuesRowData } from './my-issues-list'
import { useMyIssuesSelection } from './use-my-issues-state'
import type { MyIssuesDisplayOptions, MyIssuesProperty, MyIssuesView } from './my-issues-surface'
import { matchesExplorerFilter, nestedIssueProjection } from '@/components/issue-explorer/issue-explorer-model'

export interface MyIssuesControllerAdapter {
  navigate: (href: string) => void
  persistDisplay: (view: MyIssuesView, options: MyIssuesDisplayOptions) => Promise<void>
  persistFilters?: (view: MyIssuesView, filters: MyIssuesAppliedFilter[]) => Promise<void>
  executeBulk: (action: MyIssuesBulkAction, issueIds: string[], value?: string) => Promise<MyIssuesRowData[] | void>
  optimisticBulk?: (action: MyIssuesBulkAction, issues: MyIssuesRowData[], value?: string) => MyIssuesRowData[]
}

export interface MyIssuesControllerOptions {
  workspaceSlug: string
  initialView: MyIssuesView
  initialGroups: MyIssuesGroupData[]
  initialDisplay: MyIssuesDisplayOptions
  initialFilters?: MyIssuesAppliedFilter[]
  adapter: MyIssuesControllerAdapter
}

export function useMyIssuesController({ workspaceSlug, initialView, initialGroups, initialDisplay, initialFilters = [], adapter }: MyIssuesControllerOptions) {
  const [view, setView] = useState(initialView)
  const [groups, setGroups] = useState(initialGroups)
  const [filters, setFilters] = useState(() => readFilters(workspaceSlug, initialView, initialFilters))
  const [display, setDisplay] = useState(() => readDisplay(workspaceSlug, initialView, initialDisplay))
  const [detailsOpen, setDetailsOpenState] = useState(() => readBoolean(`${workspaceSlug}:my-issues:details-open`, false))
  const [detailsWidth, setDetailsWidthState] = useState(() => readNumber(`${workspaceSlug}:my-issues:details-width`, 350))
  const [displaySaveState, setDisplaySaveState] = useState<'idle' | 'saving' | 'saved' | 'error'>('idle')
  const [filterSaveState, setFilterSaveState] = useState<'idle' | 'saving' | 'saved' | 'error'>('idle')
  const [bulkLoading, setBulkLoading] = useState(false)
  const [bulkError, setBulkError] = useState<string>()
  const displayRequest = useRef(0)
  useEffect(() => setGroups(initialGroups), [initialGroups])
  const visibleGroups = useMemo(() => projectGroups(applyFilters(groups, filters), display), [display, filters, groups])
  const visibleSelection = useMyIssuesSelection(visibleGroups)
  const summary = useMemo(() => deriveSummary(visibleGroups), [visibleGroups])
  const counts = useMemo(() => ({ [view]: visibleGroups.reduce((total, group) => total + group.issues.length, 0) }) as Partial<Record<MyIssuesView, number>>, [view, visibleGroups])

  const changeView = (next: MyIssuesView) => {
    if (next === view) return
    setView(next)
    visibleSelection.clearSelection()
    adapter.navigate(myIssuesViewHref(workspaceSlug, next))
  }

  const changeDisplay = useCallback((next: MyIssuesDisplayOptions) => {
    setDisplay(next)
    writeDisplay(workspaceSlug, view, next)
    setDisplaySaveState('saving')
    const request = ++displayRequest.current
    adapter.persistDisplay(view, next).then(() => { if (request === displayRequest.current) setDisplaySaveState('saved') }).catch(() => { if (request === displayRequest.current) setDisplaySaveState('error') })
  }, [adapter, view, workspaceSlug])

  const setDetailsOpen = useCallback((open: boolean) => {
    setDetailsOpenState(open)
    writeValue(`${workspaceSlug}:my-issues:details-open`, String(open))
  }, [workspaceSlug])

  const setDetailsWidth = useCallback((width: number) => {
    setDetailsWidthState(width)
    writeValue(`${workspaceSlug}:my-issues:details-width`, String(width))
  }, [workspaceSlug])

  const saveFilters = async () => {
    if (!adapter.persistFilters) return
    setFilterSaveState('saving')
    try { await adapter.persistFilters(view, filters); writeFilters(workspaceSlug, view, filters); setFilterSaveState('saved') } catch { setFilterSaveState('error') }
  }

  const executeBulk = async (action: MyIssuesBulkAction, value?: string) => {
    const issues = visibleSelection.selectedIssues
    if (!issues.length || bulkLoading) return
    const snapshot = groups
    setBulkLoading(true); setBulkError(undefined)
    if (adapter.optimisticBulk) setGroups(replaceIssues(groups, adapter.optimisticBulk(action, issues, value)))
    try {
      const saved = await adapter.executeBulk(action, issues.map(issue => issue.id), value)
      if (saved) setGroups(current => replaceIssues(current, saved))
      visibleSelection.clearSelection()
    } catch (error) {
      setGroups(snapshot)
      setBulkError(error instanceof Error ? error.message : 'Could not update selected issues')
    } finally { setBulkLoading(false) }
  }

  return {
    view, groups, visibleGroups, filters, display, detailsOpen, detailsWidth, summary, counts,
    selectedIds: visibleSelection.selectedIds, selectedIssues: visibleSelection.selectedIssues,
    previewIssue: visibleSelection.previewIssue, displaySaveState, filterSaveState, bulkLoading, bulkError,
    viewHref: (next: MyIssuesView) => myIssuesViewHref(workspaceSlug, next),
    changeView, changeDisplay, replaceGroups: setGroups, setDetailsOpen, setDetailsWidth,
    addFilter: (filter: MyIssuesAppliedFilter) => { setFilters(current => { const next = [...current, filter]; writeFilters(workspaceSlug, view, next); return next }); setFilterSaveState('idle') },
    toggleFilter: (field: MyIssuesAppliedFilter['field'], fieldLabel: string, option: { id: string; label: string; color?: string }) => { setFilters(current => { const next = toggleFilterOption(current, field, fieldLabel, option); writeFilters(workspaceSlug, view, next); return next }); setFilterSaveState('idle') },
    changeFilterOperator: (id: string, operator: MyIssuesFilterOperator) => { setFilters(current => { const next = updateFilterOperator(current, id, operator); writeFilters(workspaceSlug, view, next); return next }); setFilterSaveState('idle') },
    changeFilterValues: (id: string, options: { id: string; label: string; color?: string }[]) => { setFilters(current => { const next = updateFilterValues(current, id, options); writeFilters(workspaceSlug, view, next); return next }); setFilterSaveState('idle') },
    removeFilter: (id: string) => { setFilters(current => { const next = current.filter(filter => filter.id !== id); writeFilters(workspaceSlug, view, next); return next }); setFilterSaveState('idle') },
    clearFilters: () => { setFilters([]); writeFilters(workspaceSlug, view, []); setFilterSaveState('idle') }, saveFilters,
    selectIssue: visibleSelection.selectIssue, clearSelection: visibleSelection.clearSelection,
    openPreview: visibleSelection.openPreview, closePreview: visibleSelection.closePreview,
    executeBulk, clearBulkError: () => setBulkError(undefined),
  }
}

export function myIssuesViewHref(workspaceSlug: string, view: MyIssuesView) { return `/${workspaceSlug}/my-issues/${view}` }

function applyFilters(groups: MyIssuesGroupData[], filters: MyIssuesAppliedFilter[]) {
  return groups.map(group => ({ ...group, issues: group.issues.filter(issue => filters.every(filter => matchesExplorerFilter(issue, filter))) })).filter(group => group.issues.length)
}

function projectGroups(groups: MyIssuesGroupData[], display: MyIssuesDisplayOptions): MyIssuesGroupData[] {
  const completedCutoff = completedCutoffDate(display.completedWindow)
  let issues = groups.flatMap(group => group.issues)
  if (!display.nestedSubIssues) issues = issues.filter(issue => issue.viewMatch !== false)
  issues = issues.filter(issue => {
    if (!display.showSubIssues && issue.parentId) return false
    if (display.completedWindow === 'none' && (issue.state.type === 'completed' || issue.state.type === 'canceled')) return false
    if (display.completedWindow === 'all' || display.completedWindow === 'currentCycle' || (issue.state.type !== 'completed' && issue.state.type !== 'canceled')) return true
    return new Date(issue.updatedAt).getTime() >= completedCutoff
  })
  issues = [...issues].sort(issueComparator(display.ordering, display.orderCompletedByRecency))
  const nested = display.nestedSubIssues ? nestedIssueProjection(issues) : undefined
  if (nested) issues = nested.rows
  if (display.grouping === 'none') return [{ id: 'all-issues', label: 'All issues', issues }]

  const projected = new Map<string, MyIssuesGroupData>()
  for (const issue of issues) {
    const group = groupForIssue(nested?.roots.get(issue.id) ?? issue, display.grouping)
    const current = projected.get(group.id) ?? { ...group, issues: [] }
    current.issues.push(issue)
    projected.set(group.id, current)
  }
  const grouped = [...projected.values()]
  return display.groupOrder === 'desc' ? grouped.reverse() : grouped
}

function groupForIssue(issue: MyIssuesRowData, grouping: MyIssuesDisplayOptions['grouping']): Omit<MyIssuesGroupData, 'issues'> {
  if (grouping === 'status' || grouping === 'focus') {
    return { id: grouping === 'focus' && issue.state.type === 'started' ? 'other-active' : issue.state.id, label: grouping === 'focus' && issue.state.type === 'started' ? 'Other active' : issue.state.name, stateType: issue.state.type, state: issue.state }
  }
  if (grouping === 'priority') return { id: `priority-${issue.priority}`, label: ['No priority', 'Urgent', 'High', 'Medium', 'Low'][issue.priority] }
  if (grouping === 'project') return { id: `project-${issue.project?.id ?? 'none'}`, label: issue.project?.name ?? 'No project' }
  if (grouping === 'assignee') return { id: `assignee-${issue.assignee?.id ?? 'none'}`, label: issue.assignee?.name ?? 'No assignee' }
  if (grouping === 'label') {
    const label = issue.labels?.[0]
    return { id: `label-${label?.id ?? 'none'}`, label: label?.name ?? 'No label' }
  }
  return { id: `${grouping}-none`, label: grouping[0].toUpperCase() + grouping.slice(1) }
}

function issueComparator(ordering: MyIssuesDisplayOptions['ordering'], completedByRecency: boolean) {
  return (left: MyIssuesRowData, right: MyIssuesRowData) => {
    const bothCompleted = ['completed', 'canceled'].includes(left.state.type) && ['completed', 'canceled'].includes(right.state.type)
    if (completedByRecency && bothCompleted) return Date.parse(right.updatedAt) - Date.parse(left.updatedAt)
    if (ordering === 'created') return Date.parse(right.createdAt) - Date.parse(left.createdAt)
    if (ordering === 'updated') return Date.parse(right.updatedAt) - Date.parse(left.updatedAt)
    if (ordering === 'priority') return left.priority - right.priority || (left.sortOrder ?? 0) - (right.sortOrder ?? 0)
    return (left.sortOrder ?? 0) - (right.sortOrder ?? 0)
  }
}

function completedCutoffDate(window: MyIssuesDisplayOptions['completedWindow']) {
  const days = window === 'pastDay' ? 1 : window === 'pastWeek' ? 7 : window === 'pastMonth' ? 30 : 0
  return Date.now() - days * 86_400_000
}

function displayKey(workspace: string, view: MyIssuesView) { return `${workspace}:my-issues:${view}:display` }
function filterKey(workspace: string, view: MyIssuesView) { return `${workspace}:my-issues:${view}:filters` }
function readDisplay(workspace: string, view: MyIssuesView, fallback: MyIssuesDisplayOptions): MyIssuesDisplayOptions {
  try {
    const raw = globalThis.localStorage?.getItem(displayKey(workspace, view))
    if (!raw) return fallback
    const value = JSON.parse(raw) as Omit<MyIssuesDisplayOptions, 'properties'> & { properties?: MyIssuesDisplayOptions['properties'] | string[] }
    const properties = Array.isArray(value.properties) ? value.properties.filter(isDisplayProperty) : [...fallback.properties]
    return { ...fallback, ...value, properties: new Set<MyIssuesProperty>(properties) }
  } catch { return fallback }
}
function writeDisplay(workspace: string, view: MyIssuesView, value: MyIssuesDisplayOptions) {
  writeValue(displayKey(workspace, view), JSON.stringify({ ...value, properties: [...value.properties] }))
}
function readFilters(workspace: string, view: MyIssuesView, fallback: MyIssuesAppliedFilter[]): MyIssuesAppliedFilter[] {
  try {
    const value = JSON.parse(globalThis.localStorage?.getItem(filterKey(workspace, view)) ?? 'null')
    return Array.isArray(value) ? consolidateFilters(value.filter(isAppliedFilter)) : fallback
  } catch { return fallback }
}
function writeFilters(workspace: string, view: MyIssuesView, value: MyIssuesAppliedFilter[]) { writeValue(filterKey(workspace, view), JSON.stringify(value)) }
function isAppliedFilter(value: unknown): value is MyIssuesAppliedFilter {
  if (!value || typeof value !== 'object') return false
  const filter = value as Partial<MyIssuesAppliedFilter>
  return typeof filter.id === 'string' && typeof filter.field === 'string' && typeof filter.fieldLabel === 'string' && (filter.operator === 'is' || filter.operator === 'isNot') && typeof filter.value === 'string' && typeof filter.valueLabel === 'string'
}
function readBoolean(key: string, fallback: boolean) { try { const value = globalThis.localStorage?.getItem(key); return value == null ? fallback : value === 'true' } catch { return fallback } }
function readNumber(key: string, fallback: number) { try { const value = Number(globalThis.localStorage?.getItem(key)); return Number.isFinite(value) && value > 0 ? value : fallback } catch { return fallback } }
function writeValue(key: string, value: string) { try { globalThis.localStorage?.setItem(key, value) } catch { /* Persistence is best-effort in private browsing. */ } }
function isDisplayProperty(value: unknown): value is MyIssuesProperty { return typeof value === 'string' && DISPLAY_PROPERTIES.has(value as MyIssuesProperty) }
const DISPLAY_PROPERTIES = new Set<MyIssuesProperty>(['id', 'status', 'assignee', 'priority', 'project', 'dueDate', 'milestone', 'labels', 'links', 'customers', 'customerRevenue', 'timeInStatus', 'created', 'updated'])
function replaceIssues(groups: MyIssuesGroupData[], replacements: MyIssuesRowData[]) {
  const byId = new Map(replacements.map(issue => [issue.id, issue]))
  return groups.map(group => ({ ...group, issues: group.issues.map(issue => byId.get(issue.id) ?? issue) }))
}
function deriveSummary(groups: MyIssuesGroupData[]): MyIssuesDetailsSummary {
  const issues = groups.flatMap(group => group.issues)
  return {
    labels: countItems(issues.flatMap(issue => issue.labels ?? []).map(item => ({ id: item.id, label: item.name, color: item.color }))),
    priority: countItems(issues.map(issue => ({ id: String(issue.priority), label: ['No priority', 'Urgent', 'High', 'Medium', 'Low'][issue.priority] }))),
    projects: countItems(issues.filter(issue => issue.project).map(issue => ({ id: issue.project!.id, label: issue.project!.name, color: issue.project!.color }))),
  }
}
function countItems(items: Omit<MyIssuesSummaryItem, 'count'>[]) {
  const values = new Map<string, MyIssuesSummaryItem>()
  for (const item of items) values.set(item.id, { ...item, count: (values.get(item.id)?.count ?? 0) + 1 })
  return [...values.values()].sort((a, b) => b.count - a.count || a.label.localeCompare(b.label))
}
