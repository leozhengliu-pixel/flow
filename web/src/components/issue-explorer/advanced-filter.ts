import {
  ADVANCED_FILTER_MAX_DEPTH, filterId, filterOperatorLabel, filterValues, normalizeFilterOperator,
  type AdvancedFilterCondition, type AdvancedFilterConjunction, type AdvancedFilterGroup, type AdvancedFilterNode, type MyIssuesAppliedFilter, type MyIssuesFilterValue,
} from '@/components/my-issues/my-issues-filter-types'
import type { MyIssuesFilterKey } from '@/components/my-issues/my-issues-surface'

/**
 * Linear's advanced filter: a chip carrying a tree of conditions joined by and/or, at most three
 * levels (top level → group → nested group). Several advanced chips and the normal chips combine by AND.
 *
 * Persisted shape (inside a saved view's flat `filters` array, next to normal chips):
 *   { id, field: 'advanced', fieldLabel: 'Advanced filter', operator: 'is', value: '', valueLabel: '', values: [],
 *     tree: { id, conjunction: 'and' | 'or', items: [ {id, field, fieldLabel, operator, values} | group ] } }
 * Older views stored `values: ['status:<id>', …]` on an advanced chip (ANDed); `advancedFilterTree` reads both.
 */

export const ADVANCED_FIELD_LABEL = 'Advanced filter'
const LEGACY_FIELDS: Record<string, string> = { status: 'Status', assignee: 'Assignee', priority: 'Priority', labels: 'Labels', project: 'Project' }

export function isAdvancedGroup(node: AdvancedFilterNode): node is AdvancedFilterGroup {
  return Array.isArray((node as AdvancedFilterGroup).items)
}

/** Linear alternates the default conjunction by depth: top "and", group "or", nested group "and". */
export function defaultConjunction(depth: number): AdvancedFilterConjunction {
  return depth % 2 === 0 ? 'and' : 'or'
}

export function emptyAdvancedTree(): AdvancedFilterGroup {
  return { id: filterId('group'), conjunction: 'and', items: [] }
}

export function createAdvancedFilter(tree: AdvancedFilterGroup = emptyAdvancedTree()): MyIssuesAppliedFilter {
  return { id: filterId('advanced'), field: 'advanced', fieldLabel: ADVANCED_FIELD_LABEL, operator: 'is', value: '', valueLabel: '', values: [], tree }
}

/** The tree of an advanced chip, converting the legacy `values: ['field:id']` form (all ANDed). */
export function advancedFilterTree(filter: MyIssuesAppliedFilter): AdvancedFilterGroup {
  if (filter.tree && Array.isArray(filter.tree.items)) return sanitizeGroup(filter.tree, 0)
  const items: AdvancedFilterCondition[] = filterValues(filter).flatMap((item, index) => {
    const separator = item.value.indexOf(':')
    if (separator < 0) return []
    const field = item.value.slice(0, separator), value = item.value.slice(separator + 1)
    if (!LEGACY_FIELDS[field]) return []
    return [{ id: `${filter.id}-${index}`, field: field as MyIssuesFilterKey, fieldLabel: LEGACY_FIELDS[field], operator: 'is', values: [{ value, valueLabel: item.valueLabel, color: item.color }] }]
  })
  return { id: `${filter.id}-root`, conjunction: 'and', items }
}

function sanitizeGroup(group: AdvancedFilterGroup, depth: number): AdvancedFilterGroup {
  const items = (Array.isArray(group.items) ? group.items : []).flatMap((item): AdvancedFilterNode[] => {
    if (!item || typeof item !== 'object') return []
    if (isAdvancedGroup(item)) return depth + 1 < ADVANCED_FILTER_MAX_DEPTH ? [sanitizeGroup(item, depth + 1)] : []
    if (typeof item.field !== 'string') return []
    const values = (Array.isArray(item.values) ? item.values : []).flatMap((value: unknown): MyIssuesFilterValue[] => typeof value === 'string' ? [{ value, valueLabel: value }] : value && typeof value === 'object' && typeof (value as MyIssuesFilterValue).value === 'string' ? [value as MyIssuesFilterValue] : [])
    return [{ ...item, fieldLabel: item.fieldLabel ?? item.field, operator: item.operator ?? 'is', values }]
  })
  return { id: group.id ?? filterId('group'), conjunction: group.conjunction === 'or' ? 'or' : 'and', items }
}

/** Normalise filters read from storage / a saved view: advanced chips always carry a tree. */
export function normalizeStoredFilters(filters: unknown): MyIssuesAppliedFilter[] {
  if (!Array.isArray(filters)) return []
  return filters.filter((item): item is MyIssuesAppliedFilter => Boolean(item) && typeof item === 'object' && typeof (item as MyIssuesAppliedFilter).field === 'string')
    .map(filter => filter.field === 'advanced' ? { ...filter, fieldLabel: ADVANCED_FIELD_LABEL, operator: 'is', value: '', valueLabel: '', values: [], tree: advancedFilterTree(filter) } : filter)
}

export function conditionAsFilter(condition: AdvancedFilterCondition): MyIssuesAppliedFilter {
  const first = condition.values[0] ?? { value: '', valueLabel: '' }
  return { id: condition.id, field: condition.field, fieldLabel: condition.fieldLabel, operator: condition.operator, value: first.value, valueLabel: first.valueLabel, color: first.color, values: condition.values }
}

export function createCondition(field: MyIssuesFilterKey, fieldLabel: string, value: MyIssuesFilterValue, operator: AdvancedFilterCondition['operator'] = 'is'): AdvancedFilterCondition {
  return { id: filterId('condition'), field, fieldLabel, operator, values: [value] }
}

// ---------------------------------------------------------------- tree edits (immutable)

function mapGroup(group: AdvancedFilterGroup, id: string, change: (group: AdvancedFilterGroup) => AdvancedFilterGroup): AdvancedFilterGroup {
  if (group.id === id) return change(group)
  return { ...group, items: group.items.map(item => isAdvancedGroup(item) ? mapGroup(item, id, change) : item) }
}

export function addConditionToGroup(tree: AdvancedFilterGroup, groupId: string, condition: AdvancedFilterCondition) {
  return mapGroup(tree, groupId, group => ({ ...group, items: [...group.items, condition] }))
}

export function addGroupToGroup(tree: AdvancedFilterGroup, groupId: string): { tree: AdvancedFilterGroup; groupId?: string } {
  const depth = groupDepth(tree, groupId)
  if (depth === undefined || depth + 1 >= ADVANCED_FILTER_MAX_DEPTH) return { tree }
  const child: AdvancedFilterGroup = { id: filterId('group'), conjunction: defaultConjunction(depth + 1), items: [] }
  return { tree: mapGroup(tree, groupId, group => ({ ...group, items: [...group.items, child] })), groupId: child.id }
}

export function toggleGroupConjunction(tree: AdvancedFilterGroup, groupId: string) {
  return mapGroup(tree, groupId, group => ({ ...group, conjunction: group.conjunction === 'and' ? 'or' : 'and' }))
}

export function removeNode(tree: AdvancedFilterGroup, id: string): AdvancedFilterGroup {
  return { ...tree, items: tree.items.filter(item => item.id !== id).map(item => isAdvancedGroup(item) ? removeNode(item, id) : item) }
}

export function updateCondition(tree: AdvancedFilterGroup, id: string, change: (condition: AdvancedFilterCondition) => AdvancedFilterCondition): AdvancedFilterGroup {
  return { ...tree, items: tree.items.map(item => isAdvancedGroup(item) ? updateCondition(item, id, change) : item.id === id ? change(item) : item) }
}

export function setConditionValues(tree: AdvancedFilterGroup, id: string, values: MyIssuesFilterValue[]) {
  return values.length
    ? updateCondition(tree, id, condition => ({ ...condition, values, operator: normalizeFilterOperator(condition.field, condition.operator, values.map(value => value.value)) }))
    : removeNode(tree, id)
}

/** Empty groups disappear when the editor closes (Linear). */
export function pruneEmptyGroups(tree: AdvancedFilterGroup): AdvancedFilterGroup {
  return { ...tree, items: tree.items.flatMap((item): AdvancedFilterNode[] => {
    if (!isAdvancedGroup(item)) return item.values.length ? [item] : []
    const pruned = pruneEmptyGroups(item)
    return pruned.items.length ? [pruned] : []
  }) }
}

/** Depth of a group (root = 0), or undefined when it is not in the tree. */
export function groupDepth(tree: AdvancedFilterGroup, groupId: string, depth = 0): number | undefined {
  if (tree.id === groupId) return depth
  for (const item of tree.items) if (isAdvancedGroup(item)) {
    const found = groupDepth(item, groupId, depth + 1)
    if (found !== undefined) return found
  }
}

export function treeDepth(group: AdvancedFilterGroup): number {
  return 1 + Math.max(0, ...group.items.filter(isAdvancedGroup).map(treeDepth))
}

export function treeConditions(group: AdvancedFilterGroup): AdvancedFilterCondition[] {
  return group.items.flatMap(item => isAdvancedGroup(item) ? treeConditions(item) : [item])
}

/** Conditions a view filters by: a normal chip counts once, an advanced chip counts its conditions. */
export function countFilterConditions(filters: unknown): number {
  return normalizeStoredFilters(filters).reduce((total, filter) => total + (filter.field === 'advanced' ? treeConditions(advancedFilterTree(filter)).length : 1), 0)
}

// ---------------------------------------------------------------- summary ("Status is Triage or Priority is Urgent")

export interface AdvancedSummaryPart { conjunction?: AdvancedFilterConjunction; condition: AdvancedFilterCondition; operatorLabel: string }
export interface AdvancedSummary { parts: AdvancedSummaryPart[]; more: number }

export function advancedFilterSummary(tree: AdvancedFilterGroup, shown = 2): AdvancedSummary {
  const parts: AdvancedSummaryPart[] = []
  const visit = (group: AdvancedFilterGroup) => {
    for (const item of group.items) {
      if (isAdvancedGroup(item)) { visit(item); continue }
      if (!item.values.length) continue
      parts.push({ conjunction: parts.length ? group.conjunction : undefined, condition: item, operatorLabel: filterOperatorLabel(item, item.values.map(value => value.value)) })
    }
  }
  visit(tree)
  return { parts: parts.slice(0, shown), more: Math.max(0, parts.length - shown) }
}

/** Plural nouns for "2 priorities" style value summaries. */
const VALUE_NOUNS: Partial<Record<MyIssuesFilterKey, [string, string]>> = {
  owner: ['owner', 'owners'], status: ['status', 'statuses'], priority: ['priority', 'priorities'], assignee: ['assignee', 'assignees'], creator: ['creator', 'creators'],
  labels: ['label', 'labels'], project: ['project', 'projects'], cycle: ['cycle', 'cycles'], subscribers: ['subscriber', 'subscribers'],
  agent: ['agent', 'agents'], initiative: ['initiative', 'initiatives'], projectMilestone: ['milestone', 'milestones'], customers: ['customer', 'customers'],
  template: ['template', 'templates'], relations: ['relation', 'relations'], releases: ['release', 'releases'],
}

export function valueSummaryLabel(field: MyIssuesFilterKey, values: { valueLabel?: string; label?: string }[]): string {
  if (values.length === 1) return values[0].valueLabel ?? values[0].label ?? ''
  const noun = VALUE_NOUNS[field] ?? ['value', 'values']
  return `${values.length} ${noun[1]}`
}

// ---------------------------------------------------------------- shareable `?filter=` (base64 JSON, like Linear)

export function encodeFiltersParam(filters: MyIssuesAppliedFilter[]): string {
  const bytes = new TextEncoder().encode(JSON.stringify(filters))
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

export function decodeFiltersParam(value: string | null | undefined): MyIssuesAppliedFilter[] {
  if (!value) return []
  try {
    const binary = atob(value.replace(/-/g, '+').replace(/_/g, '/'))
    const json = new TextDecoder().decode(Uint8Array.from(binary, character => character.charCodeAt(0)))
    return normalizeStoredFilters(JSON.parse(json))
  } catch {
    return []
  }
}

/** Filters worth saving: empty advanced chips and empty groups are dropped. */
export function savableFilters(filters: MyIssuesAppliedFilter[]): MyIssuesAppliedFilter[] {
  return filters.flatMap(filter => {
    if (filter.field !== 'advanced') return [filter]
    const tree = pruneEmptyGroups(advancedFilterTree(filter))
    return tree.items.length ? [{ ...filter, tree }] : []
  })
}
