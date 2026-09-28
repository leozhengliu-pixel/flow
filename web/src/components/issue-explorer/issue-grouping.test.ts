import { describe, expect, it } from 'vitest'
import { createIssueDisplayOptions } from '@/components/my-issues/my-issues-display-defaults'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import { buildIssueGroups, focusDescriptor, groupMoveUpdate, issueComparator, labelGroups, pagedDisplayQuery, withinCompletedWindow } from './issue-grouping'

const NOW = Date.parse('2026-09-24T12:00:00Z')
const open = { id: 'todo', name: 'Todo', type: 'unstarted' as const, color: '#999' }
const done = { id: 'done', name: 'Done', type: 'completed' as const, color: '#5e6ad2' }

function row(id: string, patch: Partial<MyIssuesRowData> = {}): MyIssuesRowData {
  return { id, identifier: `TST-${id}`, title: `Issue ${id}`, priority: 0, state: open, createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-01T00:00:00Z', sortOrder: Number(id), ...patch }
}

describe('issue grouping engine', () => {
  it('orders priority groups Urgent → Low, then No priority', () => {
    const rows = [row('1', { priority: 0 }), row('2', { priority: 4 }), row('3', { priority: 2 }), row('4', { priority: 1 }), row('5', { priority: 3 })]
    const groups = buildIssueGroups(rows, createIssueDisplayOptions({ grouping: 'priority', completedWindow: 'all' }))
    expect(groups.map(group => group.label)).toEqual(['Urgent', 'High', 'Medium', 'Low', 'No priority'])
  })

  it('places an issue in every label group it carries', () => {
    const a = { id: 'a', name: 'Bug', color: '#f00' }, b = { id: 'b', name: 'Frontend', color: '#0f0' }
    const groups = buildIssueGroups([row('1', { labels: [a, b] }), row('2')], createIssueDisplayOptions({ grouping: 'label', completedWindow: 'all' }))
    expect(groups.map(group => [group.label, group.issues.map(issue => issue.id)])).toEqual([['Bug', ['1']], ['Frontend', ['1']], ['No label', ['2']]])
  })

  it('groups by one label group, keeps empty member groups, and swaps the exclusive label on move', () => {
    const type = { id: 'type', name: 'Type', color: '#000' }
    const bug = { id: 'bug', name: 'Bug', color: '#f00', groupId: 'type' }, feature = { id: 'feature', name: 'Feature', color: '#0f0', groupId: 'type' }, chore = { id: 'chore', name: 'Chore', color: '#00f', groupId: 'type' }
    const ui = { id: 'ui', name: 'UI', color: '#999' }
    const data = { states: [], cycles: [], projects: [], users: [], teams: [], releases: [], labels: [type, bug, feature, chore, ui] } as never
    expect(labelGroups([type, bug, feature, chore, ui]).map(label => label.id)).toEqual(['type'])
    const rows = [row('1', { labels: [bug, ui] }), row('2', { labels: [feature] }), row('3', { labels: [ui] })]
    const groups = buildIssueGroups(rows, createIssueDisplayOptions({ grouping: 'labelGroup', labelGroupId: 'type', showEmptyGroups: true, completedWindow: 'all' }), { data })
    expect(groups.map(group => [group.label, group.issues.map(issue => issue.id)])).toEqual([['Bug', ['1']], ['Chore', []], ['Feature', ['2']], ['No Type', ['3']]])
    expect(groupMoveUpdate(rows[0], 'labelGroup', 'labelgroup-feature', { labelGroupId: 'type' })).toEqual({ labelIds: ['ui', 'feature'] })
    expect(groupMoveUpdate(rows[0], 'labelGroup', 'labelgroup-none', { labelGroupId: 'type' })).toEqual({ labelIds: ['ui'] })
  })

  it('groups by release date, newest day first, with unreleased last', () => {
    const releases = [{ id: 'r1', name: 'v1', releasedAt: '2026-09-24T08:00:00' }, { id: 'r2', name: 'v2', releasedAt: '2026-09-23T08:00:00' }, { id: 'r3', name: 'v3' }]
    const data = { states: [], cycles: [], projects: [], users: [], teams: [], releases } as never
    const rows = [row('1', { releaseIds: ['r2'] }), row('2', { releaseIds: ['r1'] }), row('3', { releaseIds: ['r3'] }), row('4')]
    const groups = buildIssueGroups(rows, createIssueDisplayOptions({ grouping: 'releaseDate', completedWindow: 'all' }), { data, now: Date.parse('2026-09-24T12:00:00') })
    expect(groups.map(group => [group.label, group.issues.map(issue => issue.id)])).toEqual([['Today', ['2']], ['Yesterday', ['1']], ['Not released', ['3', '4']]])
  })

  it('groups by customer and agent by their real identity', () => {
    const rows = [row('1', { customerIds: ['c1'], customerNames: ['Acme'] }), row('2', { delegate: { id: 'agent', name: 'Codex' }, agentSessionId: 's1' }), row('3', { delegate: { id: 'agent', name: 'Codex' }, agentSessionId: 's2' })]
    expect(buildIssueGroups(rows, createIssueDisplayOptions({ grouping: 'customer', completedWindow: 'all' })).map(group => group.label)).toEqual(['Acme', 'No customer'])
    expect(buildIssueGroups(rows, createIssueDisplayOptions({ grouping: 'agent', completedWindow: 'all' })).map(group => [group.label, group.issues.length])).toEqual([['Codex', 2], ['No agent', 1]])
  })

  it('sorts missing values last in both directions and honors direction', () => {
    const rows = [row('1', { priority: 0 }), row('2', { priority: 3 }), row('3', { priority: 1 })]
    expect([...rows].sort(issueComparator('priority')).map(item => item.id)).toEqual(['3', '2', '1'])
    expect([...rows].sort(issueComparator('priority', 'desc')).map(item => item.id)).toEqual(['2', '3', '1'])
    const titled = [row('1', { title: 'beta' }), row('2', { title: 'Alpha' })]
    expect([...titled].sort(issueComparator('title')).map(item => item.id)).toEqual(['2', '1'])
  })

  it('applies completed-issue windows by completion time', () => {
    const recent = row('1', { state: done, completedAt: '2026-09-24T06:00:00Z' })
    const old = row('2', { state: done, completedAt: '2026-09-10T00:00:00Z' })
    expect(withinCompletedWindow(recent, 'pastDay', { now: NOW })).toBe(true)
    expect(withinCompletedWindow(old, 'pastDay', { now: NOW })).toBe(false)
    expect(withinCompletedWindow(old, 'pastMonth', { now: NOW })).toBe(true)
    expect(withinCompletedWindow(old, 'none', { now: NOW })).toBe(false)
    expect(withinCompletedWindow(row('3'), 'none', { now: NOW })).toBe(true)
  })

  it('emits sub-groups under their primary group', () => {
    const rows = [row('1', { priority: 1, assignee: { id: 'u1', name: 'Ada' } }), row('2', { priority: 1 }), row('3', { priority: 2, assignee: { id: 'u1', name: 'Ada' } })]
    const groups = buildIssueGroups(rows, createIssueDisplayOptions({ grouping: 'priority', subGrouping: 'assignee', completedWindow: 'all' }))
    expect(groups.map(group => [group.parentLabel, group.label, group.issues.length])).toEqual([['Urgent', 'Ada', 1], ['Urgent', 'No assignee', 1], ['High', 'Ada', 1]])
    expect(groups[0].createContext).toEqual({ priority: 1, assigneeId: 'u1' })
  })

  it('buckets focus like Linear', () => {
    expect(focusDescriptor(row('1', { priority: 1 }), { now: NOW }).label).toBe('Urgent issues')
    expect(focusDescriptor(row('2', { blockingCount: 1 }), { now: NOW }).label).toBe('Blocking issues')
    expect(focusDescriptor(row('3', { state: done }), { now: NOW }).label).toBe('Completed')
    expect(focusDescriptor(row('4', { triage: true, state: { ...open, type: 'backlog' } }), { now: NOW }).label).toBe('Triage')
    expect(focusDescriptor(row('5'), { now: NOW }).label).toBe('Other active')
  })

  it('maps drops to the grouped property only', () => {
    const issue = row('1', { labels: [{ id: 'a', name: 'Bug', color: '#f00' }] })
    expect(groupMoveUpdate(issue, 'priority', 'priority-2')).toEqual({ priority: 2 })
    expect(groupMoveUpdate(issue, 'assignee', 'assignee-none')).toEqual({ assigneeId: '' })
    expect(groupMoveUpdate(issue, 'milestone', 'milestone-m1')).toEqual({ projectMilestoneId: 'm1' })
    expect(groupMoveUpdate(issue, 'label', 'label-b')).toEqual({ labelIds: ['a', 'b'] })
    expect(groupMoveUpdate(issue, 'priority', 'priority-3::assignee-u1')).toEqual({ priority: 3 })
    expect(groupMoveUpdate(issue, 'team', 'team-x')).toBeUndefined()
  })

  it('builds server queries for completed windows and ordering', () => {
    const query = pagedDisplayQuery(createIssueDisplayOptions({ ordering: 'priority', completedWindow: 'pastWeek', showSubIssues: false }), NOW)
    expect(query.sort).toBe('priority')
    expect(query.direction).toBe('asc')
    expect(query.conditions).toContainEqual({ field: 'parent', operator: 'isEmpty' })
    expect(JSON.stringify(query.conditions)).toContain('completedAt')
  })
})
