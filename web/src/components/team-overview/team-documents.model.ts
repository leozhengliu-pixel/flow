import { isThisMonth, isThisWeek, isToday } from 'date-fns'

import { documentDisplayTitle, documentOwner, type Translate } from '@/components/documents/document-actions'
import { advancedFilterTree, decodeFiltersParam, encodeFiltersParam, isAdvancedGroup, conditionAsFilter } from '@/components/issue-explorer/advanced-filter'
import { DATE_FILTER_FIELDS, compareDateFilter, dateFilterMenu, parseDateFilterValue } from '@/components/issue-explorer/issue-date-filter'
import { defaultDateOperator, filterValues, isComparableDateValue, type AdvancedFilterGroup, type MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import type { BootstrapData, Cycle, FlowDocument, Project, Team, User } from '@/types/flow'

export type DocumentGrouping = 'project' | 'owner' | 'cycle' | 'recency' | 'none'
export type DocumentOrdering = 'name' | 'created' | 'updated' | 'owner' | 'project'
export type DocumentProperty = 'created' | 'updated' | 'owner'

/** Filter fields the Documents list offers (Linear: Creator, Owner, Project, Dates + Advanced filter). */
export const DOCUMENT_FILTER_FIELDS: MyIssuesFilterKey[] = ['advanced', 'creator', 'owner', 'project', 'dates']
export const DOCUMENT_FILTER_LABELS: Partial<Record<MyIssuesFilterKey, string>> = { creator: 'Creator', owner: 'Owner', project: 'Project', dates: 'Dates', advanced: 'Advanced filter' }
/** Filter value of the "Current user" row; resolved to the viewer when matching. */
export const CURRENT_USER_FILTER = 'current-user'
/** Group key of documents without an owner. */
const NO_OWNER_GROUP = 'none'
const GROUPINGS: DocumentGrouping[] = ['project', 'owner', 'cycle', 'recency', 'none']
const ORDERINGS: DocumentOrdering[] = ['name', 'created', 'updated', 'owner', 'project']
const PROPERTIES: DocumentProperty[] = ['owner', 'updated', 'created']

const userName = (user: User) => user.displayName || user.name

function people(data: BootstrapData, kind: 'creator' | 'owner'): MyIssuesFilterOption[] {
  const active = data.users.filter(user => user.active)
  const option = (user: User): MyIssuesFilterOption => ({ id: user.id, label: userName(user), avatarUrl: user.avatarUrl, kind, ...(user.app ? { agent: true } : {}) })
  return [
    ...(kind === 'owner' ? [{ id: '', label: 'No owner', kind }] : []),
    { id: CURRENT_USER_FILTER, label: 'Current user', kind: 'currentUser' },
    ...active.filter(user => !user.app).map(option),
    ...active.filter(user => user.app).map(option),
  ]
}

/** The values each filter-menu field offers on the Documents list (same option shapes the issue menu renders). */
export function documentFilterOptions(field: MyIssuesFilterKey, data: BootstrapData): MyIssuesFilterOption[] | undefined {
  if (field === 'creator' || field === 'owner') return people(data, field)
  if (field === 'project') return [{ id: '', label: 'No project', kind: 'project' }, ...data.projects.filter(project => !project.archivedAt).map(project => ({ id: project.id, label: project.name, color: project.color, kind: 'project' }))]
  if (field === 'dates') return dateFilterMenu().filter(option => option.id === 'created-date' || option.id === 'updated-date')
  return undefined
}

const DOCUMENT_DATE_KINDS = new Set(['created', 'updated'])

function matchesDocumentDate(document: FlowDocument, value: string, comparison: 'before' | 'after' | undefined, now: number) {
  const parsed = parseDateFilterValue(value)
  if (!parsed || !DOCUMENT_DATE_KINDS.has(parsed.kind) || !isComparableDateValue(value)) return false
  return compareDateFilter(document[DATE_FILTER_FIELDS[parsed.kind] as 'createdAt' | 'updatedAt'], parsed, comparison ?? defaultDateOperator(value), now)
}

/** One applied chip (or advanced-filter tree) against a document; `isNot` negates. */
export function matchesDocumentFilter(document: FlowDocument, filter: MyIssuesAppliedFilter, data: BootstrapData, now = Date.now()): boolean {
  if (filter.field === 'advanced') return matchesDocumentGroup(document, advancedFilterTree(filter), data, now)
  const values = filterValues(filter).map(value => value.value)
  const person = (value: string) => value === CURRENT_USER_FILTER ? data.viewer.id : value
  const comparison = filter.operator === 'before' || filter.operator === 'after' ? filter.operator : undefined
  let matched = true
  if (filter.field === 'creator') matched = values.some(value => person(value) === document.creator.id)
  else if (filter.field === 'owner') matched = values.some(value => person(value) === (documentOwner(document, data.users)?.id ?? ''))
  else if (filter.field === 'project') matched = values.some(value => value ? document.projectIds.includes(value) : !document.projectIds.length)
  else if (filter.field === 'dates') matched = values.some(value => matchesDocumentDate(document, value, comparison, now))
  return filter.operator === 'isNot' ? !matched : matched
}

/** AND / OR over the advanced tree; empty groups and value-less conditions do not constrain. */
export function matchesDocumentGroup(document: FlowDocument, group: AdvancedFilterGroup, data: BootstrapData, now = Date.now()): boolean {
  const results = group.items.flatMap(item => isAdvancedGroup(item)
    ? (item.items.length ? [matchesDocumentGroup(document, item, data, now)] : [])
    : (item.values.length ? [matchesDocumentFilter(document, conditionAsFilter(item), data, now)] : []))
  if (!results.length) return true
  return group.conjunction === 'or' ? results.some(Boolean) : results.every(Boolean)
}

export function documentMatchesFilters(document: FlowDocument, filters: MyIssuesAppliedFilter[], data: BootstrapData, now = Date.now()) {
  return filters.every(filter => matchesDocumentFilter(document, filter, data, now))
}

/** The project a document is listed under (a document has a single parent). */
export function documentProject(data: Pick<BootstrapData, 'projects' | 'initiatives' | 'teams'>, document: FlowDocument): Project | undefined {
  if (document.issueId) return undefined
  return data.projects.find(project => document.projectIds.includes(project.id))
}

/** Whether the project is hidden by "Show inactive projects" / "Show only my projects". */
export function projectHidden(project: Project | undefined, data: BootstrapData, showInactive: boolean, onlyMine: boolean) {
  if (!project) return false
  const inactive = Boolean(project.archivedAt) || ['completed', 'canceled', 'cancelled'].includes(project.status.type)
  if (!showInactive && inactive) return true
  return onlyMine && project.lead?.id !== data.viewer.id && !project.memberIds.includes(data.viewer.id)
}

function orderValue(document: FlowDocument, ordering: DocumentOrdering, data: BootstrapData, t: Translate): string | number {
  if (ordering === 'name') return documentDisplayTitle(document, t)
  if (ordering === 'owner') return documentOwner(document, data.users)?.displayName ?? ''
  if (ordering === 'project') return documentProject(data, document)?.name ?? ''
  return new Date(ordering === 'created' ? document.createdAt : document.updatedAt).getTime()
}

export function sortDocuments(documents: FlowDocument[], ordering: DocumentOrdering, descending: boolean, data: BootstrapData, t: Translate) {
  return [...documents].sort((a, b) => {
    const left = orderValue(a, ordering, data, t)
    const right = orderValue(b, ordering, data, t)
    const result = typeof left === 'number' && typeof right === 'number'
      ? left - right
      : String(left).localeCompare(String(right), undefined, { numeric: true, sensitivity: 'base' })
    const stable = result || documentDisplayTitle(a, t).localeCompare(documentDisplayTitle(b, t)) || a.id.localeCompare(b.id)
    return descending ? -stable : stable
  })
}

export type DocumentGroupIcon =
  | { kind: 'team'; team: Team }
  | { kind: 'project'; project: Project }
  | { kind: 'user'; user: User }
  | { kind: 'cycle'; cycle: Cycle }

export interface DocumentGroup {
  id: string
  /** Display name; `entity` names are workspace data and never translated. */
  name: string
  entity: boolean
  icon?: DocumentGroupIcon
  /** Colour the header tint follows (a project or team colour). */
  color?: string
  items: FlowDocument[]
}

export function groupDocuments(documents: FlowDocument[], grouping: DocumentGrouping, data: BootstrapData, team: Team): DocumentGroup[] {
  if (grouping === 'none') return [{ id: 'all', name: 'Documents', entity: false, items: documents }]
  if (grouping === 'owner') {
    const groups = new Map<string, DocumentGroup>()
    for (const document of documents) {
      const owner = documentOwner(document, data.users)
      const key = owner?.id ?? NO_OWNER_GROUP
      const group = groups.get(key) ?? (owner
        ? { id: `owner:${owner.id}`, name: owner.displayName || owner.name, entity: true, icon: { kind: 'user' as const, user: owner }, items: [] }
        : { id: `owner:${NO_OWNER_GROUP}`, name: 'No owner', entity: false, items: [] })
      group.items.push(document)
      groups.set(key, group)
    }
    return [...groups.values()].sort((a, b) => Number(a.id === `owner:${NO_OWNER_GROUP}`) - Number(b.id === `owner:${NO_OWNER_GROUP}`) || a.name.localeCompare(b.name))
  }
  if (grouping === 'cycle') {
    const inCycle = (cycle: Cycle, document: FlowDocument) => cycle.resources.some(resource => resource.documentId === document.id)
    const groups = data.cycles
      .filter(cycle => documents.some(document => inCycle(cycle, document)))
      .map(cycle => ({ id: `cycle:${cycle.id}`, name: cycle.name, entity: true, icon: { kind: 'cycle' as const, cycle }, items: documents.filter(document => inCycle(cycle, document)) }))
    const assigned = new Set(groups.flatMap(group => group.items.map(item => item.id)))
    return [...groups, { id: 'no-cycle', name: 'No cycle', entity: false, items: documents.filter(document => !assigned.has(document.id)) }].filter(group => group.items.length)
  }
  if (grouping === 'recency') {
    const buckets = [
      { id: 'today', name: 'Today', entity: false, items: [] as FlowDocument[] },
      { id: 'week', name: 'This week', entity: false, items: [] as FlowDocument[] },
      { id: 'month', name: 'This month', entity: false, items: [] as FlowDocument[] },
      { id: 'older', name: 'Older', entity: false, items: [] as FlowDocument[] },
    ]
    for (const document of documents) {
      const date = new Date(document.updatedAt)
      buckets[isToday(date) ? 0 : isThisWeek(date) ? 1 : isThisMonth(date) ? 2 : 3].items.push(document)
    }
    return buckets.filter(bucket => bucket.items.length)
  }
  const teamItems: FlowDocument[] = []
  const byProject = new Map<string, FlowDocument[]>()
  for (const document of documents) {
    const project = documentProject(data, document)
    if (!project) teamItems.push(document)
    else byProject.set(project.id, [...(byProject.get(project.id) ?? []), document])
  }
  const projectGroups = data.projects
    .filter(project => byProject.has(project.id))
    .sort((a, b) => a.name.localeCompare(b.name))
    .map(project => ({ id: project.id, name: project.name, entity: true, icon: { kind: 'project' as const, project }, color: project.color, items: byProject.get(project.id)! }))
  return [{ id: 'team', name: 'Team documents', entity: false, icon: { kind: 'team' as const, team }, color: team.color, items: teamItems }, ...projectGroups].filter(group => group.items.length)
}

/* ---------- state kept in the address bar ---------- */

export interface DocumentDirectoryState {
  filters: MyIssuesAppliedFilter[]
  search: string
  grouping: DocumentGrouping
  ordering: DocumentOrdering
  descending: boolean
  showInactive: boolean
  onlyMyProjects: boolean
  properties: Set<DocumentProperty>
}

export function readDocumentDirectoryState(search = typeof location === 'undefined' ? '' : location.search): DocumentDirectoryState {
  const params = new URLSearchParams(search)
  const group = params.get('doc-group') === 'created' ? 'recency' : params.get('doc-group') ?? ''
  const grouping = (GROUPINGS.includes(group as DocumentGrouping) ? group : 'project') as DocumentGrouping
  const order = params.get('doc-order') ?? ''
  const ordering = (ORDERINGS.includes(order as DocumentOrdering) ? order : 'name') as DocumentOrdering
  const columns = params.get('doc-columns')
  const properties = new Set<DocumentProperty>(columns === null ? PROPERTIES : columns.split(',').filter(value => PROPERTIES.includes(value as DocumentProperty)) as DocumentProperty[])
  return {
    filters: decodeFiltersParam(params.get('doc-filter')).filter(filter => DOCUMENT_FILTER_FIELDS.includes(filter.field)),
    search: params.get('doc-search') ?? '', grouping, ordering,
    descending: params.get('doc-direction') === 'desc', showInactive: params.get('doc-inactive') === '1', onlyMyProjects: params.get('doc-mine') === '1', properties,
  }
}

export function persistDocumentDirectoryState(state: DocumentDirectoryState) {
  if (typeof history === 'undefined' || typeof location === 'undefined') return
  const params = new URLSearchParams(location.search)
  const setOptional = (key: string, value: string, keep: boolean) => { if (keep) params.set(key, value); else params.delete(key) }
  setOptional('doc-filter', state.filters.length ? encodeFiltersParam(state.filters) : '', state.filters.length > 0)
  setOptional('doc-search', state.search, Boolean(state.search))
  setOptional('doc-group', state.grouping, state.grouping !== 'project')
  setOptional('doc-order', state.ordering, state.ordering !== 'name')
  setOptional('doc-direction', 'desc', state.descending)
  setOptional('doc-inactive', '1', state.showInactive)
  setOptional('doc-mine', '1', state.onlyMyProjects)
  const columns = [...state.properties].sort().join(',')
  setOptional('doc-columns', columns, columns !== [...PROPERTIES].sort().join(','))
  const query = params.toString()
  history.replaceState(history.state, '', `${location.pathname}${query ? `?${query}` : ''}${location.hash}`)
}
