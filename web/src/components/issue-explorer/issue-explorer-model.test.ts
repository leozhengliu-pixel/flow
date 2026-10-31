import { describe, expect, it, vi } from 'vitest'
import type { Issue } from '@/types/flow'
import type { MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import { createIssueDisplayOptions } from '@/components/my-issues/my-issues-display-defaults'
import { backlog, completed, label, makeBootstrap, makeIssue, project, started, teammate, viewer } from '@/test/fixtures'
import {
  applyExplorerFilters,
  buildExplorerIssueGroups,
  createExplorerRowCache,
  explorerBoardGroupUpdate,
  explorerBulkOptions,
  executeExplorerBulkAction,
  explorerFilterOptions,
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
    const first = makeIssue({ dueDate: '2026-08-15', externalSource: 'email', autoClosed: true, templateId: 'template-1', suggestedLabelIds: [label.id], estimate: 2 })
    first.project = { id: project.id, name: project.name, color: project.color, icon: project.icon }
    const second = makeIssue({ id: 'issue-2', identifier: 'TST-2', priority: 4, state: backlog, assignee: undefined, labels: [], subscriberIds: [], dueDate: undefined })
    const data = makeBootstrap({
      issues: [first, second],
      issueTemplates: [{ id: 'template-1', name: 'Bug report' }] as never,
      projects: [{ ...project, milestones: [{ id: 'milestone-1', name: 'Beta' }] }] as never,
    })
    const options = explorerPropertyOptions(data)
    expect(options.status.find(item => item.id === started.id)?.count).toBe(1)
    expect(explorerFilterOptions('assignee', options)?.[0].count).toBe(1)
    expect(options.labels[0].count).toBe(1)
    expect(options.team.some(option => option.id === first.team.id)).toBe(true)
    expect(options.estimate.find(option => option.id === '2')?.count).toBe(1)

    expect(applyExplorerFilters(data.issues, [filter('priority', ['2'])], data).map(issue => issue.id)).toEqual([first.id])
    expect(applyExplorerFilters(data.issues, [filter('status', ['backlog'])], data).map(issue => issue.id)).toEqual([second.id])
    expect(applyExplorerFilters(data.issues, [filter('team', [first.team.id])], data).map(issue => issue.id)).toEqual([first.id, second.id])
    expect(applyExplorerFilters(data.issues, [filter('estimate', ['2'])], data).map(issue => issue.id)).toEqual([first.id])
    expect(matchesExplorerFilter(issueToExplorerRow(first, 'workspace', data.issues, data), filter('content', ['query:Issue body']))).toBe(true)
    expect(matchesExplorerFilter(issueToExplorerRow(first, 'workspace', data.issues, data), filter('dates', ['today']))).toBe(true)
    expect(matchesExplorerFilter(issueToExplorerRow(first, 'workspace', data.issues, data), filter('externalSource', ['email'], 'isNot'))).toBe(false)
    expect(matchesExplorerFilter(issueToExplorerRow(first, 'workspace', data.issues, data), filter('projectProperties', [`project-status:${project.status.id}`]))).toBe(true)
    first.projectMilestoneId = 'milestone-1'
    expect(applyExplorerFilters([first, second], [filter('projectMilestone', ['milestone-1'])], data).map(issue => issue.id)).toEqual([first.id])
    expect(explorerPropertyOptions(data, [first]).projectMilestone.some(option => option.id === 'milestone-1')).toBe(true)
    vi.useRealTimers()
  })

  it('filters issues by linked customers', () => {
    const first = makeIssue()
    const second = makeIssue({ id: 'issue-2', identifier: 'TST-2' })
    const data = makeBootstrap({
      issues: [first, second],
      customers: [{ id: 'customer-1', name: 'TTEST', status: 'active', ownerId: viewer.id, annualRevenue: 1000, size: 12, domains: [] }] as never,
      customerRequests: [
        { id: 'request-1', customerId: 'customer-1', issueId: first.id, body: 'Need', source: 'manual', creator: viewer, attachments: [] },
        { id: 'request-2', customerId: 'missing', issueId: second.id, body: 'Unknown', source: 'manual', creator: viewer, attachments: [] },
      ] as never,
    })
    expect(applyExplorerFilters(data.issues, [filter('customers', ['customer:customer-1'])], data).map(issue => issue.id)).toEqual([first.id])
    expect(applyExplorerFilters(data.issues, [filter('customers', ['customer:'])], data).map(issue => issue.id)).toEqual([second.id])
    expect(applyExplorerFilters(data.issues, [filter('customers', ['customer-status:active'])], data).map(issue => issue.id)).toEqual([first.id])
    expect(explorerPropertyOptions(data).customers.find(option => option.id === 'customer-name')?.children?.some(option => option.id === 'customer:customer-1')).toBe(true)
  })

  it('maps actions to updates and applies optimistic changes without mutating source groups', () => {
    const data = makeBootstrap()
    const row = issueToExplorerRow(data.issues[0], 'workspace', data.issues, data)
    expect(explorerUpdateForAction('status', backlog.id)).toEqual({ stateId: backlog.id })
    expect(explorerUpdateForAction('priority', '4')).toEqual({ priority: 4 })
    expect(explorerUpdateForProperty('labels', [label.id])).toEqual({ labelIds: [label.id] })
    expect(explorerUpdateForProperty('status', backlog.id)).toEqual({ stateId: backlog.id })
    expect(explorerUpdateForProperty('cycle', 'cycle-2')).toEqual({ cycleId: 'cycle-2' })
    expect(explorerUpdateForProperty('cycle', '')).toEqual({ cycleId: '' })

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
    expect(groups.find(group => group.id === started.id)?.createContext).toEqual({ stateId: started.id })
    const priorityGroups = buildExplorerIssueGroups([parent], createIssueDisplayOptions({ grouping: 'priority' }), data)
    expect(priorityGroups[0].createContext).toEqual({ priority: parent.priority })
    expect(stateIdForExplorerGroup({ id: 'other-active', label: 'Other active', issues: [] }, data)).toBe(started.id)
    expect(stateIdForExplorerGroup({ id: 'missing', label: 'Backlog', stateType: 'backlog', issues: [] }, data)).toBe(backlog.id)
  })

  it('maps board moves to persisted property updates', () => {
    const data = makeBootstrap({ issues: [makeIssue({ labels: [label] })] })
    const row = issueToExplorerRow(data.issues[0], 'workspace', data.issues, data)
    expect(explorerBoardGroupUpdate(row, 'status', backlog.id, data)).toEqual({ stateId: backlog.id })
    expect(explorerBoardGroupUpdate(row, 'priority', 'priority-4', data)).toEqual({ priority: 4 })
    expect(explorerBoardGroupUpdate(row, 'project', 'project-none', data)).toEqual({ projectId: '' })
    expect(explorerBoardGroupUpdate(row, 'cycle', 'cycle-none', data)).toEqual({ cycleId: '' })
    expect(explorerBoardGroupUpdate(row, 'team', 'team-other', data)).toEqual({})
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

  describe('status changes across team workflows', () => {
    const team = (id: string) => ({ id, key: id.toUpperCase(), name: id, color: '#5e6ad2' })
    const ready = { id: 'custom-ready', name: 'Ready', color: '#888', type: 'unstarted', position: 0, teamId: 'custom' }
    const shipping = { id: 'custom-progress', name: 'In progress', color: '#888', type: 'started', position: 1, teamId: 'custom' }
    const data = makeBootstrap({
      teams: [team('team-1'), team('custom'), team('child')] as never,
      states: [backlog, started, completed, ready, shipping] as never,
      teamSettings: { child: { inheritWorkflowStatuses: true, parentTeamId: 'custom' } } as never,
    })
    const plainIssue = makeIssue()
    const customIssue = makeIssue({ id: 'issue-c', identifier: 'C-1', team: team('custom') as never, state: ready as never })
    const childIssue = makeIssue({ id: 'issue-k', identifier: 'K-1', team: team('child') as never, state: ready as never })

    it("moves a card dropped on another team's status column to its own team's equivalent", () => {
      const row = issueToExplorerRow(customIssue, 'workspace', [customIssue], data)
      expect(explorerBoardGroupUpdate(row, 'status', started.id, data)).toEqual({ stateId: 'custom-progress' })
      const child = issueToExplorerRow(childIssue, 'workspace', [childIssue], data)
      expect(explorerBoardGroupUpdate(child, 'status', 'custom-progress', data)).toEqual({ stateId: 'custom-progress' })
      expect(explorerBoardGroupUpdate(child, 'status', backlog.id, data)).toEqual({})
    })

    it('sends each team of a bulk status change its own status and offers the shared ones', async () => {
      const issuesById = new Map<string, Issue>([plainIssue, customIssue, childIssue].map(issue => [issue.id, issue]))
      const onUpdateIssues = vi.fn(async (ids: string[]) => ids.map(id => issuesById.get(id)!))
      await executeExplorerBulkAction({ action: 'status', ids: [plainIssue.id, customIssue.id, childIssue.id], value: started.id, data, issuesById, onUpdateIssue: vi.fn(), onUpdateIssues })
      expect(onUpdateIssues.mock.calls).toEqual([[[plainIssue.id], { stateId: started.id }], [[customIssue.id, childIssue.id], { stateId: 'custom-progress' }]])
      const options = explorerPropertyOptions(data, [plainIssue, customIssue])
      expect(explorerBulkOptions('status', options, { data, teamIds: ['custom', 'child'] })?.map(option => option.id)).toEqual(['custom-ready', 'custom-progress'])
      expect(explorerBulkOptions('status', options)?.map(option => option.id)).toHaveLength(5)
    })
  })

  it('updates immutable map state helpers', () => {
    const source = new Map([['a', '1']])
    expect(withMapKey(source, 'b', '2')).toEqual(new Map([['a', '1'], ['b', '2']]))
    expect(withoutMapKey(source, 'a').size).toBe(0)
    expect(source).toEqual(new Map([['a', '1']]))
  })

  it('rebuilds only the rows of changed issues across realtime updates, but everything when hierarchy or metadata moved', () => {
    const first = makeIssue({ id: 'one', identifier: 'T-1', title: 'One' }), second = makeIssue({ id: 'two', identifier: 'T-2', title: 'Two' })
    const data = makeBootstrap({ issues: [first, second] })
    const rows = createExplorerRowCache()
    const initial = rows(data.issues, data)
    // A title edit replaces one issue object: the other row keeps its identity.
    const edited = { ...second, title: 'Two, edited' }
    const patched = { ...data, issues: [first, edited] }
    const next = rows(patched.issues, patched)
    expect(next[0]).toBe(initial[0])
    expect(next[1]).not.toBe(initial[1])
    expect(next[1].title).toBe('Two, edited')
    // Metadata the rows read (here: projects) changed, so nothing may be reused.
    const moved = { ...patched, projects: [...patched.projects] }
    expect(rows(moved.issues, moved)[0]).not.toBe(next[0])
    // A change to an issue in a hierarchy can alter other rows (parent trail, sub-issue progress).
    const parent = makeIssue({ id: 'parent', identifier: 'T-3', title: 'Parent', subIssueIds: ['child'] }), child = makeIssue({ id: 'child', identifier: 'T-4', title: 'Child', parentId: 'parent' })
    const family = { ...moved, issues: [first, parent, child] }
    const familyRows = rows(family.issues, family)
    const renamed = { ...family, issues: [first, { ...parent, title: 'Renamed parent' }, child] }
    const renamedRows = rows(renamed.issues, renamed)
    expect(renamedRows[0]).not.toBe(familyRows[0])
    expect(renamedRows[2].parent?.title).toBe('Renamed parent')
  })
})
