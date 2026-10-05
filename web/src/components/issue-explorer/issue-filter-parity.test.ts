import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { makeBootstrap, makeIssue, backlog, started, completed, viewer, teammate } from '@/test/fixtures'
import type { Issue } from '@/types/flow'
import { issueFiltersToQueryAst, type AdvancedFilterGroup, type IssueQueryAstNode, type MyIssuesAppliedFilter, type MyIssuesFilterOperator } from '@/components/my-issues/my-issues-filter-types'
import type { MyIssuesFilterKey } from '@/components/my-issues/my-issues-surface'
import { applyExplorerFilters } from './issue-explorer-model'
import { createAdvancedFilter, createCondition } from './advanced-filter'

/**
 * Paged lists filter on the server (`compileIssueFilter`), local lists on the client. This mirrors the
 * server's semantics for the fields involved and checks both agree for nested and/or/not trees, the
 * Linear operators ("include all of", "exclude if all", before / after) and nullable fields.
 */
const NOW = Date.parse('2026-10-04T12:00:00Z')
const bug = { id: 'label-bug', name: 'Bug', color: '#eb5757' }, feature = { id: 'label-1', name: 'Feature', color: '#5e6ad2' }
const day = (offset: number) => new Date(NOW + offset * 86_400_000).toISOString()
const localDay = (offset: number) => { const date = new Date(NOW + offset * 86_400_000); return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}` }
const issues: Issue[] = [
  makeIssue({ id: 'a', priority: 1, state: started, labels: [bug, feature] as never, assignee: viewer, dueDate: localDay(3), createdAt: day(-2), startedAt: day(-1) }),
  makeIssue({ id: 'b', priority: 2, state: backlog, labels: [bug] as never, assignee: undefined, dueDate: localDay(-2), createdAt: day(-20) }),
  makeIssue({ id: 'c', priority: 0, state: completed, labels: [] as never, assignee: teammate, createdAt: day(-90), completedAt: day(-3) }),
  makeIssue({ id: 'd', priority: 1, state: backlog, labels: [feature] as never, assignee: viewer, dueDate: localDay(40), createdAt: day(-400) }),
]
const data = makeBootstrap({ issues, labels: [bug, feature] as never })

function evaluate(node: IssueQueryAstNode, issue: Issue): boolean {
  if (node.and && !node.and.every(child => evaluate(child, issue))) return false
  if (node.or && node.or.length && !node.or.some(child => evaluate(child, issue))) return false
  if (node.not && evaluate(node.not as IssueQueryAstNode, issue)) return false
  if (!node.field) return true
  const values = node.values ?? [], operator = (node.operator ?? 'is').toLowerCase()
  const labels = issue.labels.map(label => label.id)
  const scalar: Record<string, string | undefined> = {
    id: issue.id, status: issue.state.id, priority: String(issue.priority), assignee: issue.assignee?.id, dueDate: issue.dueDate ?? undefined,
    createdAt: issue.createdAt, updatedAt: issue.updatedAt, startedAt: issue.startedAt ?? undefined, completedAt: issue.completedAt ?? undefined,
  }
  if (node.field === 'labels') {
    const has = (value: string) => value === '' ? labels.length === 0 : labels.includes(value)
    if (operator === 'includesall' || operator === 'excludesall') { const all = values.length > 0 && values.every(has); return operator === 'includesall' ? all : !all }
    if (operator === 'isempty' || operator === 'isnotempty') return (labels.length === 0) === (operator === 'isempty')
    const any = values.some(has)
    return operator === 'isnot' || operator === 'notin' ? !any : any
  }
  const actual = scalar[node.field]
  if (operator === 'isempty' || operator === 'isnotempty') return !actual === (operator === 'isempty')
  if (['before', 'after', 'gt', 'gte', 'lt', 'lte'].includes(operator)) {
    if (!actual) return false
    const left = node.field === 'dueDate' ? actual : Date.parse(actual), right = node.field === 'dueDate' ? values[0] : Date.parse(values[0])
    return operator === 'before' || operator === 'lt' ? left < right : operator === 'after' || operator === 'gt' ? left > right : operator === 'gte' ? left >= right : left <= right
  }
  const matched = values.includes(actual ?? '') || (node.field === 'status' && values.includes(issue.state.type))
  return operator === 'isnot' || operator === 'notin' ? !matched : matched
}

function chip(field: MyIssuesFilterKey, values: string[], operator: MyIssuesFilterOperator = 'is', fieldLabel: string = field): MyIssuesAppliedFilter {
  return { id: `${field}-${values.join('-')}-${operator}`, field, fieldLabel, operator, value: values[0] ?? '', valueLabel: values[0] ?? '', values: values.map(value => ({ value, valueLabel: value })) }
}
const cond = (field: MyIssuesFilterKey, values: string[], operator: MyIssuesFilterOperator = 'is') => ({ ...createCondition(field, field, { value: values[0], valueLabel: values[0] }, operator), values: values.map(value => ({ value, valueLabel: value })) })
const advanced = (tree: AdvancedFilterGroup) => createAdvancedFilter(tree)

const cases: Record<string, MyIssuesAppliedFilter[]> = {
  'labels include all of': [chip('labels', [bug.id, feature.id], 'includesAll')],
  'labels exclude if all': [chip('labels', [bug.id, feature.id], 'excludesAll')],
  'labels include any of / no labels': [chip('labels', [bug.id, ''])],
  'labels exclude if any of': [chip('labels', [feature.id], 'isNot')],
  'assignee is not (nullable)': [chip('assignee', ['', viewer.id], 'isNot')],
  'due date before 1 week from now': [chip('dates', ['due:+1w'], 'before', 'Due date')],
  'due date after 1 week from now': [chip('dates', ['due:+1w'], 'after', 'Due date')],
  'created after 1 month ago': [chip('dates', ['created:-1m'], 'after', 'Created date')],
  'created before custom day': [chip('dates', ['created:2026-09-01'], 'before', 'Created date')],
  'started after 1 week ago (missing values never match)': [chip('dates', ['started:-1w'], 'after', 'Started date')],
  'overdue and no due date': [chip('dates', ['overdue'], 'is', 'Due date')],
  'nested and / or / not': [advanced({ id: 'r', conjunction: 'or', items: [
    { id: 'g1', conjunction: 'and', items: [cond('priority', ['1']), cond('labels', [bug.id])] },
    { id: 'g2', conjunction: 'and', items: [cond('status', ['backlog']), { id: 'g3', conjunction: 'or', items: [cond('assignee', [''], 'isNot'), cond('labels', [feature.id, bug.id], 'excludesAll')] }] },
  ] })],
  'advanced AND with normal chips and empty groups': [chip('priority', ['1', '2']), advanced({ id: 'r', conjunction: 'and', items: [cond('labels', [bug.id, feature.id], 'includesAll'), { id: 'empty', conjunction: 'or', items: [] }] })],
  'several advanced chips': [advanced({ id: 'r1', conjunction: 'or', items: [cond('status', ['state-completed']), cond('dates', ['due:+1w'], 'before')] }), advanced({ id: 'r2', conjunction: 'and', items: [cond('priority', ['0', '1'], 'isNot')] })],
  'legacy advanced values': [{ id: 'legacy', field: 'advanced', fieldLabel: 'Advanced filter', operator: 'is', value: 'priority:1', valueLabel: 'Urgent', values: [{ value: 'priority:1', valueLabel: 'Urgent' }, { value: `labels:${feature.id}`, valueLabel: 'Feature' }] }],
}

function depth(node: IssueQueryAstNode): number {
  return 1 + Math.max(0, ...[...(node.and ?? []), ...(node.or ?? []), ...(node.not ? [node.not as IssueQueryAstNode] : [])].map(depth))
}
function size(node: IssueQueryAstNode): number {
  return 1 + [...(node.and ?? []), ...(node.or ?? []), ...(node.not ? [node.not as IssueQueryAstNode] : [])].reduce((total, child) => total + size(child), 0)
}

describe('client matching ↔ server query parity', () => {
  beforeAll(() => { vi.useFakeTimers(); vi.setSystemTime(NOW) })
  afterAll(() => vi.useRealTimers())

  it.each(Object.entries(cases))('%s', (_name, filters) => {
    const client = applyExplorerFilters(issues, filters, data).map(issue => issue.id)
    const ast = issueFiltersToQueryAst(filters, { data, now: NOW })
    const server = issues.filter(issue => evaluate(ast, issue)).map(issue => issue.id)
    expect(server).toEqual(client)
    // The page wraps the filters with display conditions: { and: [filters, ...] } must fit the server budgets.
    const wrapped = { and: [ast, { field: 'status', operator: 'in', values: ['x'] }] }
    expect(depth(wrapped)).toBeLessThanOrEqual(9)
    expect(size(wrapped)).toBeLessThanOrEqual(100)
  })

  it('compiles labels "include all of" to the server operator', () => {
    expect(issueFiltersToQueryAst(cases['labels include all of'], { data, now: NOW })).toEqual({ and: [{ field: 'labels', operator: 'includesAll', values: [bug.id, feature.id] }] })
    expect(issueFiltersToQueryAst(cases['labels exclude if all'], { data, now: NOW })).toEqual({ and: [{ not: { field: 'labels', operator: 'includesAll', values: [bug.id, feature.id] } }] })
  })

  it('selects the expected issues', () => {
    expect(applyExplorerFilters(issues, cases['labels include all of'], data).map(issue => issue.id)).toEqual(['a'])
    expect(applyExplorerFilters(issues, cases['labels include any of / no labels'], data).map(issue => issue.id)).toEqual(['a', 'b', 'c'])
    expect(applyExplorerFilters(issues, cases['nested and / or / not'], data).map(issue => issue.id)).toEqual(['a', 'b', 'd'])
  })
})
