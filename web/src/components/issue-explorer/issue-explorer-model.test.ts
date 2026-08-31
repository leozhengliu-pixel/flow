import { describe, expect, it, vi } from 'vitest'
import type { Issue } from '@/types/flow'
import type { MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import { createIssueDisplayOptions } from '@/components/my-issues/my-issues-display-defaults'
import { backlog, completed, label, makeBootstrap, makeIssue, project, started, teammate, viewer } from '@/test/fixtures'
import {
  applyExplorerFilters,
  buildExplorerIssueGroups,
  executeExplorerBulkAction,
  explorerPropertyOptions,
  explorerUpdateForAction,
  explorerUpdateForProperty,
  issueHierarchyFields,
  issueToExplorerRow,
  matchesExplorerFilter,
  nestedIssueProjection,
  optimisticExplorerRow,
  replaceExplorerRow,
  stateIdForExplorerGroup,
  withMapKey,
  withoutMapKey,
} from './issue-explorer-model'

function filter(field: MyIssuesAppliedFilter['field'], values: string[], operator: MyIssuesAppliedFilter['operator'] = 'is'): MyIssuesAppliedFilter {
  return {
    id: `${field}-filter`, field, fieldLabel: field, operator,
    value: values[0] ?? '', valueLabel: values[0] ?? '',
    values: values.map(value => ({ value, valueLabel: value })),
  }
}

describe('issue explorer workflow model', () => {
  it('projects hierarchy, release, project, and status-history data into rows', () => {
    const parent = makeIssue({ id: 'parent', identifier: 'TST-1', parentId: 'grandparent', subIssueIds: ['child', 'archived-child'] })
    const grandparent = makeIssue({ id: 'grandparent', identifier: 'TST-0', parentId: 'parent' })
    const child = makeIssue({ id: 'child', identifier: 'TST-2', parentId: 'parent', state: completed, priority: 9 })
    const archivedChild = makeIssue({ id: 'archived-child', identifier: 'TST-3', parentId: 'parent', archivedAt: '2026-08-03T00:00:00.000Z' })
    const data = makeBootstrap({
      issues: [parent, grandparent, child, archivedChild],
      activities: { parent: [{ id: 'activity-1', type: 'issue.updated', createdAt: '2026-08-02T00:00:00.000Z', actor: teammate, metadata: { stateBeforeId: backlog.id, stateId: started.id } }] },
      releases: [{ id: 'release-1', name: 'Release', pipelineId: 'pipeline-1', stage: 'Beta', status: 'released', releasedAt: '2026-08-10T00:00:00.000Z', issueIds: ['parent'] }] as never,
      projects: [{ ...project, initiatives: ['initiative-1'], labelIds: ['project-label'], milestones: [{ id: 'milestone-1', name: 'Beta' }] }] as never,
    })
    parent.project = { id: project.id, name: project.name, color: project.color, icon: project.icon }

    const row = issueToExplorerRow(parent, 'workspace', data.issues, data)
    expect(row.href).toContain('/workspace/issue/TST-1/')
    expect(row.ancestors?.map(item => item.id)).toEqual(['grandparent'])
    expect(row.subIssueProgress).toEqual({ completed: 1, total: 1 })
    expect(row.initiativeIds).toEqual(['initiative-1'])
    expect(row.projectMilestoneNames).toEqual(['Beta'])
    expect(row.releaseIds).toEqual(['release-1'])
    expect(row.hasReleasedRelease).toBe(true)
    expect(row.statusIntervals).toEqual([
      expect.objectContaining({ stateId: backlog.id, exitedAt: '2026-08-02T00:00:00.000Z' }),
      expect.objectContaining({ stateId: started.id }),
    ])
    expect(issueHierarchyFields(child, data.issues).ancestors?.[0].id).toBe('parent')
    expect(issueToExplorerRow(child, 'workspace', data.issues, data).priority).toBe(4)
  })

  it('builds property counts and applies issue filters', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-15T12:00:00.000Z'))
    const first = makeIssue({ dueDate: '2026-08-15', externalSource: 'email', autoClosed: true, templateId: 'template-1', suggestedLabelIds: [label.id] })
    first.project = { id: project.id, name: project.name, color: project.color, icon: project.icon }
    const second = makeIssue({ id: 'issue-2', identifier: 'TST-2', priority: 4, state: backlog, assignee: undefined, labels: [], subscriberIds: [], dueDate: undefined })
    const data = makeBootstrap({ issues: [first, second], issueTemplates: [{ id: 'template-1', name: 'Bug report' }] as never })
    const options = explorerPropertyOptions(data)
    expect(options.status.find(item => item.id === started.id)?.count).toBe(1)
    expect(options.assignee[0].count).toBe(1)
    expect(options.labels[0].count).toBe(1)

    expect(applyExplorerFilters(data.issues, [filter('priority', ['2'])], data).map(issue => issue.id)).toEqual([first.id])
    expect(applyExplorerFilters(data.issues, [filter('status', ['backlog'])], data).map(issue => issue.id)).toEqual([second.id])
    expect(matchesExplorerFilter(issueToExplorerRow(first, 'workspace', data.issues, data), filter('content', ['query:Issue body']))).toBe(true)
    expect(matchesExplorerFilter(issueToExplorerRow(first, 'workspace', data.issues, data), filter('dates', ['today']))).toBe(true)
    expect(matchesExplorerFilter(issueToExplorerRow(first, 'workspace', data.issues, data), filter('externalSource', ['email'], 'isNot'))).toBe(false)
    expect(matchesExplorerFilter(issueToExplorerRow(first, 'workspace', data.issues, data), filter('projectProperties', [`project-status:${project.status.id}`]))).toBe(true)
    vi.useRealTimers()
  })

  it('maps actions to updates and applies optimistic changes without mutating source groups', () => {
    const data = makeBootstrap()
    const row = issueToExplorerRow(data.issues[0], 'workspace', data.issues, data)
    expect(explorerUpdateForAction('status', backlog.id)).toEqual({ stateId: backlog.id })
    expect(explorerUpdateForAction('priority', '4')).toEqual({ priority: 4 })
    expect(explorerUpdateForProperty('labels', [label.id])).toEqual({ labelIds: [label.id] })
    expect(explorerUpdateForProperty('status', backlog.id)).toEqual({ stateId: backlog.id })

    const updated = optimisticExplorerRow(row, { stateId: backlog.id, priority: 8, assigneeId: '', projectId: '', dueDate: '', labelIds: [], sortOrder: 20 }, data)
    expect(updated).toMatchObject({ state: backlog, priority: 4, assignee: undefined, project: undefined, dueDate: undefined, labels: [], sortOrder: 20 })
    const groups = [{ id: 'group', label: 'Group', issues: [row] }]
    expect(replaceExplorerRow(groups, updated)[0].issues[0]).toBe(updated)
    expect(groups[0].issues[0]).toBe(row)
  })

  it('nests sub-issues, preserves root grouping, and resolves status groups', () => {
    const parent = issueToExplorerRow(makeIssue({ id: 'parent', identifier: 'TST-1', subIssueIds: ['child'] }), 'workspace')
    const child = issueToExplorerRow(makeIssue({ id: 'child', identifier: 'TST-2', parentId: 'parent', state: backlog }), 'workspace')
    const projection = nestedIssueProjection([child, parent])
    expect(projection.rows.map(row => row.id)).toEqual(['parent', 'child'])
    expect(projection.roots.get('child')?.id).toBe('parent')
    const data = makeBootstrap()
    const groups = buildExplorerIssueGroups([child, parent], createIssueDisplayOptions({ grouping: 'status', nestedSubIssues: true, showEmptyGroups: true, completedWindow: 'all' }), data)
    expect(groups.find(group => group.id === started.id)?.issues.map(row => row.id)).toEqual(['parent', 'child'])
    expect(stateIdForExplorerGroup({ id: 'other-active', label: 'Other active', issues: [] }, data)).toBe(started.id)
    expect(stateIdForExplorerGroup({ id: 'missing', label: 'Backlog', stateType: 'backlog', issues: [] }, data)).toBe(backlog.id)
  })

  it('executes bulk label, subscriber, batch, and clipboard actions', async () => {
    const data = makeBootstrap()
    const issue = data.issues[0]
    const issuesById = new Map<string, Issue>([[issue.id, issue]])
    const onUpdateIssue = vi.fn(async () => issue)
    const onUpdateIssues = vi.fn(async () => [issue])
    await executeExplorerBulkAction({ action: 'labels', ids: [issue.id], value: label.id, data, issuesById, onUpdateIssue, onUpdateIssues })
    expect(onUpdateIssue).toHaveBeenCalledWith(issue.id, { labelIds: [] })
    await executeExplorerBulkAction({ action: 'subscribers', ids: [issue.id], value: teammate.id, data, issuesById, onUpdateIssue, onUpdateIssues })
    expect(onUpdateIssue).toHaveBeenLastCalledWith(issue.id, { subscriberIds: [viewer.id, teammate.id] })
    await executeExplorerBulkAction({ action: 'unassignMe', ids: [issue.id], data, issuesById, onUpdateIssue, onUpdateIssues })
    expect(onUpdateIssues).toHaveBeenCalledWith([issue.id], { assigneeId: '' })

    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    await executeExplorerBulkAction({ action: 'copyId', ids: [issue.id], data, issuesById, onUpdateIssue, onUpdateIssues })
    expect(writeText).toHaveBeenCalledWith(issue.identifier)
  })

  it('updates immutable map state helpers', () => {
    const source = new Map([['a', '1']])
    expect(withMapKey(source, 'b', '2')).toEqual(new Map([['a', '1'], ['b', '2']]))
    expect(withoutMapKey(source, 'a').size).toBe(0)
    expect(source).toEqual(new Map([['a', '1']]))
  })
})
