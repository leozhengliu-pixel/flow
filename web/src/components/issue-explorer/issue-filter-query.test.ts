import { describe, expect, it } from 'vitest'
import type { MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import { issueFiltersToQueryAst } from '@/components/my-issues/my-issues-filter-types'
import { filterToQueryNode } from './issue-filter-query'

const data = {
  viewer: { id: 'me' },
  projects: [{ id: 'p1', initiatives: ['i1'], milestones: [] }, { id: 'p2', initiatives: [], milestones: [] }],
  releases: [{ id: 'r1', issueIds: ['a', 'b'], releasedAt: '2026-09-01' }, { id: 'r2', issueIds: ['c'] }],
  labels: [{ id: 'l1' }],
} as never
const filter = (field: MyIssuesAppliedFilter['field'], values: string[], operator: MyIssuesAppliedFilter['operator'] = 'is'): MyIssuesAppliedFilter => ({ id: field, field, fieldLabel: field, operator, value: values[0] ?? '', valueLabel: '', values: values.map(value => ({ value, valueLabel: value })) })

describe('filter → server query translation', () => {
  it('resolves workspace-dependent fields to ids', () => {
    expect(filterToQueryNode(filter('initiative', ['i1']), { data })).toEqual({ field: 'project', operator: 'in', values: ['p1'] })
    expect(filterToQueryNode(filter('releases', ['released-any']), { data })).toEqual({ field: 'id', operator: 'in', values: ['a', 'b'] })
    expect(filterToQueryNode(filter('releases', ['no-releases']), { data })).toEqual({ not: { field: 'id', operator: 'in', values: ['a', 'b', 'c'] } })
  })

  it('maps multi-valued properties to indexed attributes and negates compound filters', () => {
    expect(filterToQueryNode(filter('relations', ['blocked_by', 'blocks']), { data })).toEqual({ or: [{ field: 'relation:blocked_by', operator: 'isNotEmpty' }, { field: 'relation:blocks', operator: 'isNotEmpty' }] })
    expect(filterToQueryNode(filter('links', ['has-links'], 'isNot'), { data })).toEqual({ not: { field: 'hasLinks', operator: 'isNotEmpty' } })
    expect(filterToQueryNode(filter('assignee', ['', 'u1']), { data })).toEqual({ or: [{ field: 'assignee', operator: 'in', values: ['u1'] }, { field: 'assignee', operator: 'isEmpty' }] })
  })

  it('turns date categories into comparisons', () => {
    const now = Date.parse('2026-09-24T12:00:00Z')
    expect(filterToQueryNode(filter('dates', ['created-past-week']), { data, now })).toEqual({ field: 'createdAt', operator: 'after', values: ['2026-09-17T12:00:00.000Z'] })
    expect(filterToQueryNode(filter('dates', ['no-due-date']), { data, now })).toEqual({ field: 'dueDate', operator: 'isEmpty' })
    expect(filterToQueryNode(filter('ai', ['assigned-to-me']), { data, now })).toEqual({ field: 'assignee', operator: 'in', values: ['me'] })
  })

  it('keeps the legacy leaf mapping without context', () => {
    expect(issueFiltersToQueryAst([filter('labels', ['l1'])])).toEqual({ and: [{ field: 'labelId', operator: 'is', values: ['l1'] }] })
  })
})
