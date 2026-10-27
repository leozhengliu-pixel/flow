import type { ReactNode } from 'react'
import { render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import type { BootstrapData, Cycle, IssueSLA } from '@/types/flow'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import { listIssueRecordGroups, listIssueRecords } from '@/lib/api'
import { createIssueDisplayOptions } from '@/components/my-issues/my-issues-display-defaults'
import { focusCasesKey, focusDescriptor, focusGroupCases, pagedDisplayQuery } from './issue-grouping'
import { PagedIssueList } from './paged-issue-list'
import { clearPagedIssueListCache } from './paged-issue-list-cache'

vi.mock('@/lib/api', () => ({ listIssueRecordGroups: vi.fn(), listIssueRecords: vi.fn() }))
vi.mock('react-virtuoso', () => {
  return { Virtuoso: () => null, GroupedVirtuoso: ({ groupCounts, groupContent }: { groupCounts: number[]; groupContent: (index: number) => ReactNode }) => <div>{groupCounts.map((_, group) => <section key={group}>{groupContent(group)}</section>)}</div> }
})
afterEach(() => clearPagedIssueListCache())

const NOW = Date.parse('2026-10-26T12:00:00Z')
const hours = (value: number) => new Date(NOW + value * 3_600_000).toISOString()
const current = { id: 'cycle-now', name: 'Cycle 12', teamId: 'team-a', status: 'current', startsAt: hours(-48), endsAt: hours(48) } as Cycle
const past = { id: 'cycle-past', name: 'Cycle 11', teamId: 'team-a', status: 'completed', startsAt: hours(-400), endsAt: hours(-200) } as Cycle
const sla = (issueId: string, patch: Partial<IssueSLA>): IssueSLA => ({ id: `sla-${issueId}`, issueId, ruleId: 'rule', startedAt: hours(-10), dueAt: hours(100), pausedMinutes: 0, remainingMinutes: 0, status: 'active', ...patch })
const data = {
  cycles: [current, past],
  teamSettings: { 'team-t': { triageEnabled: true }, 'team-a': { triageEnabled: false } } as unknown as BootstrapData['teamSettings'],
  issueSlas: [sla('sla-breached', { status: 'breached' }), sla('sla-high', { dueAt: hours(5) }), sla('sla-medium', { dueAt: hours(50) }), sla('sla-low', { dueAt: hours(200) }), sla('sla-met', { status: 'completed' }), sla('sla-removed', { status: 'removed', dueAt: hours(1) })],
}

const type = (value: string) => ({ id: value, name: value, type: value as 'started', color: '#999' })
function row(id: string, patch: Partial<MyIssuesRowData> = {}): MyIssuesRowData {
  return { id, identifier: id, title: id, priority: 0, state: type('started'), teamId: 'team-a', createdAt: hours(-100), updatedAt: hours(-100), ...patch }
}
const rows: MyIssuesRowData[] = [
  row('done', { state: type('completed'), priority: 1 }), row('canceled', { state: type('canceled') }),
  row('triage', { state: type('backlog'), teamId: 'team-t', triage: true }), row('triaged', { state: type('backlog'), teamId: 'team-t', triagedAt: hours(-1), triage: false }),
  row('agent', { delegate: { id: 'agent', name: 'Agent' }, agentSessionId: 'session', priority: 1 }), row('agent-backlog', { state: type('backlog'), delegate: { id: 'agent', name: 'Agent' }, agentSessionId: 'session' }),
  row('urgent', { priority: 1, cycleId: current.id }),
  row('sla-breached'), row('sla-high'), row('sla-medium'), row('sla-low'), row('sla-met'), row('sla-removed'),
  row('cycle', { cycleId: current.id, blockingCount: 1 }), row('cycle-backlog', { state: type('backlog'), cycleId: current.id }), row('past-cycle', { cycleId: past.id }),
  row('blocking', { blockingCount: 2, state: type('backlog') }), row('backlog', { state: type('backlog') }), row('active', { state: type('unstarted') }),
]
for (const item of rows) item.sla = data.issueSlas.filter(entry => entry.issueId === item.id && entry.status !== 'removed').at(-1)

/** Evaluates the server query vocabulary the Focus cases use against a row. */
function matches(node: Record<string, unknown>, item: MyIssuesRowData): boolean {
  if (Array.isArray(node.and)) return node.and.every(child => matches(child, item))
  if (Array.isArray(node.or)) return node.or.some(child => matches(child, item))
  if (node.not) return !matches(node.not as Record<string, unknown>, item)
  const field = String(node.field)
  const value = ({ statusType: item.state.type, team: item.teamId, triagedAt: item.triagedAt, delegateId: item.delegate?.id, agentSessionId: item.agentSessionId, priority: String(item.priority), id: item.id, cycle: item.cycleId, 'relation:blocks': (item.blockingCount ?? 0) > 0 ? 'true' : '' } as Record<string, string | undefined>)[field]
  if (node.operator === 'isEmpty') return !value
  if (node.operator === 'isNotEmpty') return Boolean(value)
  return (node.values as string[]).includes(value ?? '')
}

describe('Focus grouping on server-backed lists', () => {
  beforeEach(() => vi.clearAllMocks())

  it('puts every issue in the bucket the in-memory Focus grouping does', () => {
    const cases = focusGroupCases(data, NOW)
    for (const item of rows) {
      const bucket = cases.find(entry => matches(entry.filter, item))?.value
      expect([item.id, bucket]).toEqual([item.id, focusDescriptor(item, { data: { ...makeBootstrap(), ...data }, now: NOW }).id])
    }
    expect(cases.map(entry => entry.value)).toEqual(['focus-completed', 'focus-canceled', 'focus-triage', 'focus-agents', 'focus-urgent', 'focus-sla-breached', 'focus-sla-high', 'focus-sla-medium', 'focus-sla-low', 'focus-cycle-cycle-now', 'focus-blocking', 'focus-backlog', 'focus-active'])
  })

  it('leaves out a triage bucket without triage teams and splits long SLA lists', () => {
    const many = Array.from({ length: 1500 }, (_, index) => sla(`issue-${String(index).padStart(4, '0')}`, { status: 'breached' }))
    const cases = focusGroupCases({ cycles: [], issueSlas: many }, NOW)
    expect(cases.some(entry => entry.value === 'focus-triage')).toBe(false)
    const breached = cases.find(entry => entry.value === 'focus-sla-breached')!.filter as { or: { values: string[] }[] }
    expect(breached.or.map(node => node.values.length)).toEqual([1000, 500])
  })

  it('queries Focus as ordered cases, and only for paged Focus lists', () => {
    const display = createIssueDisplayOptions({ grouping: 'focus' })
    expect(pagedDisplayQuery(display, NOW, [], data)).toMatchObject({ groupBy: 'cases', groupCases: focusGroupCases(data, NOW) })
    expect(pagedDisplayQuery(display, NOW)).toMatchObject({ groupBy: 'status' })
    expect(pagedDisplayQuery(createIssueDisplayOptions({ grouping: 'priority' }), NOW, [], data)).not.toHaveProperty('groupCases')
    expect(focusCasesKey(display, { ...data, issueCollectionPaged: true }, NOW)).toBe(JSON.stringify(focusGroupCases(data, NOW)))
    expect(focusCasesKey(display, { ...data, issueCollectionPaged: false }, NOW)).toBe('')
    expect(focusCasesKey(createIssueDisplayOptions({ grouping: 'status' }), { ...data, issueCollectionPaged: true }, NOW)).toBe('')
  })

  it('shows the Focus buckets with their counts in Focus order', async () => {
    const bootstrap = makeBootstrap({ issues: [], cycles: [current], issueCollectionPaged: true })
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'focus-completed', count: 4 }, { value: 'focus-cycle-cycle-now', count: 2 }, { value: 'focus-urgent', count: 3 }, { value: 'focus-active', count: 7 }] })
    vi.mocked(listIssueRecords).mockResolvedValue({ items: [makeIssue({ id: 'a', title: 'A' })], hasMore: false, total: -1 })
    const query = { groupBy: 'cases', groupCases: focusGroupCases({ cycles: [current] }, NOW) }
    render(<I18nProvider><PagedIssueList data={bootstrap} query={query} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    await screen.findByText('Urgent issues')
    expect([...document.querySelectorAll('header')].map(header => header.textContent)).toEqual([expect.stringMatching(/^Urgent issues.*3/), expect.stringMatching(/^Cycle 12.*2/), expect.stringMatching(/^Other active.*7/), expect.stringMatching(/^Completed.*4/)])
    // The first page of the first bucket is requested by its case value, with the cases.
    expect(listIssueRecords).toHaveBeenCalledWith(expect.objectContaining({ groupBy: 'cases', groupValue: 'focus-urgent', groupCases: query.groupCases }), expect.any(AbortSignal))
    expect(listIssueRecordGroups).toHaveBeenCalledWith(expect.objectContaining({ groupBy: 'cases', groupCases: query.groupCases }), expect.any(AbortSignal))
  })
})
