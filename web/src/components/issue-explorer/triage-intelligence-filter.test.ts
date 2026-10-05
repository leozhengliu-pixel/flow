import { describe, expect, it } from 'vitest'
import type { User } from '@/types/flow'
import type { MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import { chipValueOptions, filterChipItem } from '@/components/my-issues/my-issues-filter-chips'
import { label, makeBootstrap, makeIssue, project, teammate, viewer } from '@/test/fixtures'
import { explorerFilterOptions, explorerPropertyOptions, explorerUpdateForAction, explorerUpdateForProperty, issueToExplorerRow, matchesExplorerFilter, optimisticExplorerRow } from './issue-explorer-model'
import { filterToQueryNode } from './issue-filter-query'

const agent = { id: 'app_flow', name: 'Flow', displayName: 'Flow', email: '', active: true, app: true, builtinAgent: true, appScopes: ['read', 'write', 'app:assignable'], appTeamIds: ['team-1'] } as User
const filter = (field: MyIssuesAppliedFilter['field'], values: string[], operator: MyIssuesAppliedFilter['operator'] = 'is'): MyIssuesAppliedFilter => ({ id: field, field, fieldLabel: field, operator, value: values[0] ?? '', valueLabel: '', values: values.map(value => ({ value, valueLabel: value })) })

function fixture() {
  const suggested = makeIssue({ suggestedAssigneeIds: [teammate.id], suggestedProjectIds: [project.id], suggestedLabelIds: [label.id], suggestedTeamIds: ['team-1'], suggestedDuplicateIds: ['issue-9'] })
  const plain = makeIssue({ id: 'issue-2', identifier: 'TST-2' })
  const delegated = makeIssue({ id: 'issue-3', identifier: 'TST-3', delegate: agent, agentSessionId: 'task-1', agentSessionState: 'error' })
  const data = makeBootstrap({ users: [viewer, teammate, agent], issues: [suggested, plain, delegated] })
  const row = (issue = suggested) => issueToExplorerRow(issue, 'workspace', data.issues, data)
  return { data, suggested, plain, delegated, row }
}

describe('Triage Intelligence filter (Linear parity)', () => {
  it('offers Suggested assignee/project/label/team submenus and the relation/duplicate toggles', () => {
    const { data } = fixture()
    const options = explorerFilterOptions('triageIntelligence', explorerPropertyOptions(data))!
    expect(options.map(option => option.label)).toEqual(['Suggested assignee', 'Suggested project', 'Suggested label', 'Suggested team', 'Suggested relations', 'Suggested duplicates'])
    const assignees = options[0].children!
    expect(assignees[0]).toMatchObject({ id: 'assignee:*', label: 'Any user', count: 1 })
    expect(assignees.find(option => option.id === `assignee:${teammate.id}`)).toMatchObject({ count: 1, filterLabel: 'Suggested assignee' })
    // Agent users are listed last with the "Agent" pill.
    expect(assignees.at(-1)).toMatchObject({ id: `assignee:${agent.id}`, agent: true })
    expect(options[1].children![0]).toMatchObject({ id: 'project:*', label: 'Any project', count: 1 })
    expect(options[5]).toMatchObject({ id: 'duplicate', count: 1 })
    // Chips narrow their value picker to the chosen sub-field.
    expect(chipValueOptions('Suggested project', options).every(option => option.id.startsWith('project:'))).toBe(true)
  })

  it('matches active suggestion targets in memory and translates to indexed server fields', () => {
    const { data, plain, row } = fixture()
    const cases: [string, unknown][] = [
      ['assignee:*', { field: 'suggested:assignee', operator: 'isNotEmpty' }],
      [`assignee:${teammate.id}`, { field: `suggestedAssignee:${teammate.id}`, operator: 'isNotEmpty' }],
      [`project:${project.id}`, { field: `suggestedProject:${project.id}`, operator: 'isNotEmpty' }],
      [`label:${label.id}`, { field: `suggestedLabel:${label.id}`, operator: 'isNotEmpty' }],
      ['team:team-1', { field: 'suggestedTeam:team-1', operator: 'isNotEmpty' }],
      ['duplicate', { field: 'suggested:duplicate', operator: 'isNotEmpty' }],
    ]
    for (const [value, node] of cases) {
      expect(matchesExplorerFilter(row(), filter('triageIntelligence', [value])), value).toBe(true)
      expect(matchesExplorerFilter(row(plain), filter('triageIntelligence', [value])), value).toBe(false)
      expect(filterToQueryNode(filter('triageIntelligence', [value]), { data })).toEqual(node)
    }
    expect(matchesExplorerFilter(row(), filter('triageIntelligence', ['related']))).toBe(false)
    expect(matchesExplorerFilter(row(), filter('triageIntelligence', [`assignee:${viewer.id}`]))).toBe(false)
    expect(filterToQueryNode(filter('triageIntelligence', ['related', 'duplicate'], 'isNot'), { data })).toEqual({ not: { or: [{ field: 'suggested:related', operator: 'isNotEmpty' }, { field: 'suggested:duplicate', operator: 'isNotEmpty' }] } })
    // Legacy "No suggested label" chips use the presence row instead of one clause per label.
    expect(filterToQueryNode(filter('suggestedLabel', ['']), { data })).toEqual({ field: 'suggested:label', operator: 'isEmpty' })
  })
})

describe('agents in assignee and agent filters', () => {
  it('lists delegatable agents with the badge and matches delegated issues', () => {
    const { data, delegated, row } = fixture()
    const options = explorerPropertyOptions(data)
    const assignee = options.assignee.find(option => option.id === agent.id)
    expect(assignee).toMatchObject({ agent: true, groupId: 'agents', count: 1 })
    expect(options.assignee.findIndex(option => option.id === agent.id)).toBe(options.assignee.length - 1)
    // All delegatable agents are offered, not only those on loaded issues.
    expect(explorerPropertyOptions(data, []).agent.some(option => option.id === agent.id)).toBe(true)
    expect(matchesExplorerFilter(row(delegated), filter('assignee', [agent.id]))).toBe(true)
    expect(filterToQueryNode(filter('assignee', [agent.id, viewer.id]), { data })).toEqual({ or: [{ field: 'assignee', operator: 'in', values: [viewer.id] }, { field: 'delegateId', operator: 'in', values: [agent.id] }] })
    expect(filterToQueryNode(filter('assignee', [viewer.id]), { data })).toEqual({ field: 'assignee', operator: 'in', values: [viewer.id] })
  })

  it('delegates when an agent is picked from row, context and bulk assignee menus', () => {
    const { data, row, plain } = fixture()
    expect(explorerUpdateForProperty('assignee', agent.id)).toEqual({ delegateId: agent.id })
    expect(explorerUpdateForAction('assign', agent.id)).toEqual({ delegateId: agent.id })
    expect(explorerUpdateForProperty('assignee', teammate.id)).toEqual({ assigneeId: teammate.id })
    const optimistic = optimisticExplorerRow(row(plain), { delegateId: agent.id }, data)
    expect(optimistic.delegate?.id).toBe(agent.id)
    expect(optimistic.assignee?.id).toBe(row(plain).assignee?.id)
  })

  it('filters agent sessions by Linear\'s four states', () => {
    const { data, delegated, suggested, row } = fixture()
    const awaiting = makeIssue({ id: 'issue-4', identifier: 'TST-4', delegate: agent, agentSessionId: 'task-2', agentSessionState: 'awaitingInput' })
    const merged = makeIssue({ id: 'issue-5', identifier: 'TST-5', delegate: agent, agentSessionId: 'task-3', agentSessionState: 'merged' })
    const dismissed = makeIssue({ id: 'issue-6', identifier: 'TST-6', delegate: agent, agentSessionId: 'task-4', agentSessionState: 'canceled' })
    const complete = makeIssue({ id: 'issue-7', identifier: 'TST-7', delegate: agent, agentSessionId: 'task-5', agentSessionState: 'complete' })
    const states = explorerPropertyOptions({ ...data, issues: [...data.issues, awaiting, merged, dismissed, complete] }).agentSession
    expect(states.map(option => option.label)).toEqual(['Active', 'Error', 'Dismissed', 'Merged'])
    expect(states.map(option => [option.id, option.count])).toEqual([['state:active', 1], ['state:error', 1], ['state:canceled', 1], ['state:merged', 1]])
    const matches = (issue: typeof delegated, values: string[]) => matchesExplorerFilter(row(issue), filter('agentSession', values))
    expect(matches(delegated, ['state:error'])).toBe(true)
    expect(matches(delegated, ['state:active'])).toBe(false)
    expect(matches(awaiting, ['state:active'])).toBe(true)
    expect(matches(merged, ['state:merged'])).toBe(true)
    expect(matches(merged, ['state:active'])).toBe(false)
    expect(matches(dismissed, ['state:canceled'])).toBe(true)
    expect(matches(complete, ['state:active', 'state:error', 'state:canceled', 'state:merged'])).toBe(false)
    // Saved views keep working: Awaiting input is Active, Complete is dropped, none/any still match.
    expect(matches(awaiting, ['state:awaitingInput'])).toBe(true)
    expect(matches(complete, ['state:complete'])).toBe(false)
    expect(matches(suggested, [''])).toBe(true)
    expect(matches(delegated, ['*'])).toBe(true)
    // Chips show the Linear label for a legacy value and keep its stored id.
    expect(filterChipItem(filter('agentSession', ['state:awaitingInput']), states).values[0]).toMatchObject({ id: 'state:awaitingInput', label: 'Active' })
    expect(filterChipItem(filter('agentSession', ['state:merged']), states).values[0]).toMatchObject({ id: 'state:merged', label: 'Merged' })
    // The paged query sends the same states, so server and client agree.
    expect(filterToQueryNode(filter('agentSession', ['state:active']), { data })).toEqual({ field: 'agentSessionState', operator: 'in', values: ['pending', 'active', 'awaitingInput'] })
    expect(filterToQueryNode(filter('agentSession', ['state:awaitingInput', 'state:active']), { data })).toEqual({ field: 'agentSessionState', operator: 'in', values: ['pending', 'active', 'awaitingInput'] })
    expect(filterToQueryNode(filter('agentSession', ['state:canceled', 'state:merged']), { data })).toEqual({ field: 'agentSessionState', operator: 'in', values: ['canceled', 'merged'] })
    expect(filterToQueryNode(filter('agentSession', ['', 'state:error']), { data })).toEqual({ or: [{ field: 'agentSessionId', operator: 'isEmpty' }, { field: 'agentSessionState', operator: 'in', values: ['error'] }] })
    expect(filterToQueryNode(filter('agentSession', ['*']), { data })).toEqual({ field: 'agentSessionId', operator: 'isNotEmpty' })
    expect(filterToQueryNode(filter('agentSession', ['state:complete']), { data })).toEqual({ field: 'id', operator: 'in', values: [] })
  })
})
