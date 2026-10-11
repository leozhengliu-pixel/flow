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
  })

  it('sends release filters as indexed release fields, never issue id lists', () => {
    expect(filterToQueryNode(filter('releases', ['released-any']), { data })).toEqual({ field: 'releasedAt', operator: 'isNotEmpty' })
    expect(filterToQueryNode(filter('releases', ['no-releases']), { data })).toEqual({ field: 'release', operator: 'isEmpty' })
    expect(filterToQueryNode(filter('releases', ['release:r1', 'release-pipeline:p9']), { data })).toEqual({ or: [{ field: 'release', operator: 'in', values: ['r1'] }, { field: 'releasePipeline', operator: 'in', values: ['p9'] }] })
    expect(filterToQueryNode(filter('releases', ['release-stage:QA', 'release-stage-type:released']), { data })).toEqual({ or: [{ field: 'releaseStage', operator: 'in', values: ['QA'] }, { field: 'releaseStatus', operator: 'in', values: ['released'] }] })
  })

  it('compares Released date presets, custom days and "No released date" (Releases and Dates menus)', () => {
    const now = Date.parse('2026-10-10T12:00:00Z')
    expect(filterToQueryNode(filter('releases', ['released:-1w'], 'after'), { data, now })).toEqual({ field: 'releasedAt', operator: 'after', values: ['2026-10-03T12:00:00.000Z'] })
    expect(filterToQueryNode(filter('releases', ['released:-3d'], 'before'), { data, now })).toEqual({ field: 'releasedAt', operator: 'before', values: ['2026-10-07T12:00:00.000Z'] })
    // A chip whose operator is still "is" reads the preset's default (past presets: after).
    expect(filterToQueryNode(filter('releases', ['released:-1m']), { data, now })).toEqual({ field: 'releasedAt', operator: 'after', values: ['2026-09-10T12:00:00.000Z'] })
    // Custom days start at local midnight.
    expect(filterToQueryNode(filter('dates', ['released:2026-10-01'], 'before'), { data, now })).toEqual({ field: 'releasedAt', operator: 'before', values: [new Date('2026-10-01T00:00:00').toISOString()] })
    expect(filterToQueryNode(filter('releases', ['no-released-date']), { data, now })).toEqual({ field: 'releasedAt', operator: 'isEmpty' })
    expect(filterToQueryNode(filter('dates', ['no-released-date'], 'isNot'), { data, now })).toEqual({ not: { field: 'releasedAt', operator: 'isEmpty' } })
    expect(filterToQueryNode(filter('dates', ['autoClosed:-1w'], 'after'), { data, now })).toEqual({ field: 'autoClosedAt', operator: 'after', values: ['2026-10-03T12:00:00.000Z'] })
    expect(filterToQueryNode(filter('dates', ['no-completed-date']), { data, now })).toEqual({ field: 'completedAt', operator: 'isEmpty' })
    expect(filterToQueryNode(filter('dates', ['status:-10d'], 'before'), { data, now })).toEqual({ field: 'statusChangedAt', operator: 'before', values: ['2026-09-30T12:00:00.000Z'] })
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
