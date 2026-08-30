import { useEffect, useMemo, useState } from 'react'
import type { ProjectDataGroup, ProjectPageItem, ProjectProperty, ProjectsDataViewProps, ProjectSortColumn } from './projects-data-view'
import { DEFAULT_PROJECTS_DISPLAY, type ProjectsDisplaySettings } from './projects-display-model'

export type ProjectsViewState = {
  display: ProjectsDisplaySettings
  selectedIds: string[]
  sort: { column: ProjectSortColumn, direction: 'asc' | 'desc' }
}

type ProjectsViewStateOptions = {
  initial?: Partial<ProjectsViewState>
  storageKey?: string
  workspaceDefault?: ProjectsDisplaySettings
}

const PRIORITY_GROUPS = [
  { id: 'priority-urgent', name: 'Urgent', color: '#e56a68' },
  { id: 'priority-high', name: 'High', color: '#c8c8cb' },
  { id: 'priority-medium', name: 'Medium', color: '#a7a7ac' },
  { id: 'priority-low', name: 'Low', color: '#6b6b70' },
  { id: 'priority-none', name: 'No priority', color: '#6b6b70' },
]

const HEALTH_GROUPS = [
  { id: 'health-on-track', name: 'On track', color: '#4d9b5d' },
  { id: 'health-at-risk', name: 'At risk', color: '#d3a036' },
  { id: 'health-off-track', name: 'Off track', color: '#d8605f' },
  { id: 'health-no-update', name: 'No update', color: '#57575c' },
]

const STATUS_ORDER = ['Backlog', 'Planned', 'In Progress', 'Completed', 'Canceled']
const STATUS_COLORS: Record<string, string> = {
  Backlog: '#6b6b70',
  Planned: '#d6b326',
  'In Progress': '#5e8fd8',
  Completed: '#4d9b5d',
  Canceled: '#77777c',
}

export function useProjectsViewState(projects: ProjectPageItem[], { initial, storageKey = 'workspace:all', workspaceDefault }: ProjectsViewStateOptions = {}) {
  const personalKey = `flow:projects:view:${storageKey}`
  const workspaceDefaultKey = `flow:projects:view-default:${storageKey}`
  const [state, setState] = useState<ProjectsViewState>(() => createInitialState(initial, personalKey, workspaceDefaultKey, workspaceDefault))

  useEffect(() => {
    writeStoredDisplay(personalKey, state.display)
  }, [personalKey, state.display])

  const grouped = useMemo(() => groupAndSort(projects, state), [projects, state])

  const dataViewProps: Pick<ProjectsDataViewProps, 'groups' | 'layout' | 'manualOrdering' | 'selectedIds' | 'sort' | 'visibleProperties' | 'onSelectionChange' | 'onSort'> = {
    groups: grouped,
    layout: state.display.layout,
    manualOrdering: state.display.ordering === 'Manual',
    onSelectionChange: selectedIds => setState(current => ({ ...current, selectedIds })),
    onSort: (column, direction) => setState(current => ({ ...current, display: { ...current.display, ordering: orderingForColumn(column), orderingDirection: direction }, sort: { column, direction } })),
    selectedIds: state.selectedIds,
    sort: state.sort,
    visibleProperties: state.display.properties,
  }

  return {
    state,
    groups: grouped,
    dataViewProps,
    visibleColumns: new Set(state.display.properties),
    setDisplay: (display: ProjectsDisplaySettings) => setState(current => ({ ...current, display: cloneDisplay(display), sort: sortForOrdering(display.ordering, current.sort) })),
    setSelectedIds: (selectedIds: string[]) => setState(current => ({ ...current, selectedIds })),
    resetDisplay: () => setState(current => ({ ...current, display: workspaceDefault ?? readStoredDisplay(workspaceDefaultKey) ?? defaultDisplay(), sort: { column: 'name', direction: 'asc' } })),
    setDisplayDefault: () => writeStoredDisplay(workspaceDefaultKey, state.display),
    updateProject: (project: ProjectPageItem, property: ProjectProperty, value: string) => updateProject(project, property, value),
  }
}

function createInitialState(initial: Partial<ProjectsViewState> | undefined, personalKey: string, workspaceDefaultKey: string, workspaceDefault: ProjectsDisplaySettings | undefined): ProjectsViewState {
  const display = readStoredDisplay(personalKey) ?? workspaceDefault ?? readStoredDisplay(workspaceDefaultKey) ?? { ...defaultDisplay(), ...initial?.display }
  return {
    display: cloneDisplay(display),
    selectedIds: initial?.selectedIds ?? [],
    sort: initial?.sort ?? sortForOrdering(display.ordering, { column: 'name', direction: 'asc' }),
  }
}

function defaultDisplay(): ProjectsDisplaySettings { return cloneDisplay(DEFAULT_PROJECTS_DISPLAY) }

function cloneDisplay(display: ProjectsDisplaySettings): ProjectsDisplaySettings { return { ...display, properties: [...display.properties] } }

function readStoredDisplay(key: string): ProjectsDisplaySettings | null {
  try {
    const value = JSON.parse(globalThis.localStorage?.getItem(key) ?? 'null')
    if (!value || typeof value !== 'object' || !Array.isArray(value.properties)) return null
    return { ...defaultDisplay(), ...value, properties: value.properties.filter((item: unknown): item is string => typeof item === 'string') }
  } catch {
    return null
  }
}

function writeStoredDisplay(key: string, display: ProjectsDisplaySettings) {
  try {
    globalThis.localStorage?.setItem(key, JSON.stringify(cloneDisplay(display)))
  } catch {
    // Browser preference persistence is best-effort in private browsing.
  }
}

function groupAndSort(projects: ProjectPageItem[], state: ProjectsViewState): ProjectDataGroup[] {
  const visible = projects.filter(project => includeProject(project, state.display.showClosed))
  const primary = makeGroups(visible, state.display.grouping, state.display.showEmptyGroups)
  if (state.display.groupOrder === 'desc') primary.reverse()
  const shouldSubgroup = state.display.subGrouping !== 'No grouping' && state.display.subGrouping !== state.display.grouping

  return primary.map(group => {
    const orderedProjects = orderProjects(group.projects, state.display.ordering, state.display.orderingDirection)
    if (!shouldSubgroup) return { ...group, projects: orderedProjects }
    return {
      ...group,
      projects: [],
      subgroups: makeGroups(orderedProjects, state.display.subGrouping, state.display.showEmptyGroups).map(subgroup => ({
        ...subgroup,
        projects: orderProjects(subgroup.projects, state.display.ordering, state.display.orderingDirection),
      })),
    }
  })
}

function includeProject(project: ProjectPageItem, window: ProjectsDisplaySettings['showClosed']) {
  const closed = ['Completed', 'Canceled'].includes(project.status)
  if (!closed || window === 'All') return true
  if (window === 'None') return false
  const days = ({ 'Past week': 7, 'Past month': 31, 'Past 3 months': 92, 'Past 6 months': 183 } as const)[window as 'Past week' | 'Past month' | 'Past 3 months' | 'Past 6 months'] ?? 0
  const updatedAt = Date.parse(project.updatedAt ?? '')
  return Number.isFinite(updatedAt) && updatedAt >= Date.now() - days * 24 * 60 * 60 * 1000
}

function makeGroups(projects: ProjectPageItem[], grouping: string, includeEmpty: boolean): ProjectDataGroup[] {
  if (grouping === 'No grouping') return [{ id: 'all-projects', name: 'All projects', projects }]
  const buckets = new Map<string, ProjectDataGroup>()
  projects.forEach(project => {
    const descriptor = descriptorFor(project, grouping)
    const group = buckets.get(descriptor.id) ?? { ...descriptor, projects: [] }
    group.projects.push(project)
    buckets.set(descriptor.id, group)
  })
  const defaults = includeEmpty ? emptyGroupsFor(projects, grouping) : []
  defaults.forEach(group => { if (!buckets.has(group.id)) buckets.set(group.id, group) })
  return [...buckets.values()].sort((left, right) => groupOrder(left, right, grouping))
}

function descriptorFor(project: ProjectPageItem, grouping: string): Omit<ProjectDataGroup, 'projects'> {
  if (grouping === 'Status') return { id: `status-${project.status}`, name: project.status, color: STATUS_COLORS[project.status] ?? '#77777c' }
  if (grouping === 'Priority') {
    const name = priorityName(project.priority)
    return { id: `priority-${project.priority}`, name, color: PRIORITY_GROUPS.find(group => group.name === name)?.color }
  }
  if (grouping === 'Health') {
    const name = healthName(project.health)
    return { id: `health-${project.health}`, name, color: HEALTH_GROUPS.find(group => group.name === name)?.color }
  }
  if (grouping === 'Lead') return project.lead ? { id: `lead-${project.lead.id}`, name: project.lead.name, color: '#8e8e94' } : { id: 'lead-none', name: 'No lead', color: '#6b6b70' }
  if (grouping === 'Member') return project.lead ? { id: `member-${project.lead.id}`, name: project.lead.name, color: '#8e8e94' } : { id: 'member-none', name: 'No members', color: '#6b6b70' }
  if (grouping === 'Initiative') return { id: 'initiative-none', name: 'No initiative', color: '#6b6b70' }
  if (grouping === 'Label') return { id: 'label-none', name: 'No labels', color: '#6b6b70' }
  if (grouping === 'Start date') return { id: `start-${project.startDate ?? 'none'}`, name: project.startDate ?? 'No start date', color: '#8e8e94' }
  if (grouping === 'Target date') return { id: `target-${project.targetDate ?? 'none'}`, name: project.targetDate ?? 'No target date', color: '#8e8e94' }
  if (grouping === 'Team') return project.team ? { id: `team-${project.team.id}`, name: project.team.name, color: '#8e8e94' } : { id: 'team-none', name: 'No team', color: '#6b6b70' }
  return { id: 'all-projects', name: 'All projects' }
}

function emptyGroupsFor(projects: ProjectPageItem[], grouping: string): ProjectDataGroup[] {
  if (grouping === 'Status') return STATUS_ORDER.map(name => ({ id: `status-${name}`, name, color: STATUS_COLORS[name], projects: [] }))
  if (grouping === 'Priority') return PRIORITY_GROUPS.map(group => ({ ...group, projects: [] }))
  if (grouping === 'Health') return HEALTH_GROUPS.map(group => ({ ...group, projects: [] }))
  if (grouping === 'Lead') return [{ id: 'lead-none', name: 'No lead', color: '#6b6b70', projects: [] }, ...projects.filter(project => project.lead).map(project => ({ id: `lead-${project.lead!.id}`, name: project.lead!.name, color: '#8e8e94', projects: [] }))]
  if (grouping === 'Member') return [{ id: 'member-none', name: 'No members', color: '#6b6b70', projects: [] }, ...projects.filter(project => project.lead).map(project => ({ id: `member-${project.lead!.id}`, name: project.lead!.name, color: '#8e8e94', projects: [] }))]
  if (grouping === 'Team') return [{ id: 'team-none', name: 'No team', color: '#6b6b70', projects: [] }, ...projects.filter(project => project.team).map(project => ({ id: `team-${project.team!.id}`, name: project.team!.name, color: '#8e8e94', projects: [] }))]
  return []
}

function groupOrder(left: ProjectDataGroup, right: ProjectDataGroup, grouping: string) {
  const order = grouping === 'Status' ? STATUS_ORDER : grouping === 'Priority' ? PRIORITY_GROUPS.map(group => group.name) : grouping === 'Health' ? HEALTH_GROUPS.map(group => group.name) : []
  const leftIndex = order.indexOf(left.name)
  const rightIndex = order.indexOf(right.name)
  if (leftIndex >= 0 || rightIndex >= 0) return (leftIndex < 0 ? Number.MAX_SAFE_INTEGER : leftIndex) - (rightIndex < 0 ? Number.MAX_SAFE_INTEGER : rightIndex)
  return left.name.localeCompare(right.name)
}

function orderProjects(projects: ProjectPageItem[], ordering: string, direction: 'asc' | 'desc') {
  const result = [...projects].sort((left, right) => compare(left, right, ordering))
  return direction === 'desc' ? result.reverse() : result
}

function compare(left: ProjectPageItem, right: ProjectPageItem, ordering: string) {
  const leftValue = sortValue(left, ordering)
  const rightValue = sortValue(right, ordering)
  return typeof leftValue === 'number' && typeof rightValue === 'number' ? leftValue - rightValue : String(leftValue).localeCompare(String(rightValue))
}

function sortValue(project: ProjectPageItem, ordering: string): string | number {
  if (ordering === 'Manual') return project.position ?? 0
  if (ordering === 'Priority') return ({ urgent: 0, high: 1, medium: 2, low: 3, none: 4 })[project.priority]
  if (ordering === 'Health') return ({ 'on-track': 0, 'at-risk': 1, 'off-track': 2, 'no-update': 3 })[project.health]
  if (ordering === 'Health updated') return ({ 'on-track': 0, 'at-risk': 1, 'off-track': 2, 'no-update': 3 })[project.health]
  if (ordering === 'Start date') return project.startDate ?? '9999-12-31'
  if (ordering === 'Target date') return project.targetDate ?? '9999-12-31'
  if (ordering === 'Created') return project.createdAt ?? ''
  if (ordering === 'Updated') return project.updatedAt ?? ''
  if (ordering === 'Customer count' || ordering === 'Customer revenue' || ordering === 'Important count') return 0
  if (ordering === 'Status') return project.status
  return project.name
}

function sortForOrdering(ordering: string, previous: ProjectsViewState['sort']): ProjectsViewState['sort'] {
  if (ordering === 'Priority') return { column: 'priority', direction: 'asc' }
  if (ordering === 'Health') return { column: 'health', direction: 'asc' }
  if (ordering === 'Health updated') return { column: 'health', direction: 'asc' }
  if (ordering === 'Start date') return { column: 'targetDate', direction: 'asc' }
  if (ordering === 'Target date') return { column: 'targetDate', direction: 'asc' }
  if (ordering === 'Status') return { column: 'status', direction: 'asc' }
  if (ordering === 'Name') return { column: 'name', direction: 'asc' }
  return previous
}

function orderingForColumn(column: ProjectSortColumn) {
  if (column === 'targetDate') return 'Target date'
  return column[0].toUpperCase() + column.slice(1)
}

function priorityName(priority: ProjectPageItem['priority']) { return ({ urgent: 'Urgent', high: 'High', medium: 'Medium', low: 'Low', none: 'No priority' } as const)[priority] }
function healthName(health: ProjectPageItem['health']) { return ({ 'on-track': 'On track', 'at-risk': 'At risk', 'off-track': 'Off track', 'no-update': 'No update' } as const)[health] }

function updateProject(project: ProjectPageItem, property: ProjectProperty, value: string): ProjectPageItem {
  if (property === 'health') return { ...project, health: value as ProjectPageItem['health'] }
  if (property === 'priority') return { ...project, priority: value as ProjectPageItem['priority'] }
  if (property === 'targetDate') return { ...project, targetDate: value || undefined }
  if (property === 'startDate') return { ...project, startDate: value || undefined }
  if (property === 'members') return { ...project, memberIds: value ? value.split(',') : [] }
  if (property === 'labels') return { ...project, labelIds: value ? value.split(',') : [] }
  if (property === 'status') return { ...project, status: value }
  return { ...project, lead: value ? { id: value, name: value } : undefined }
}
