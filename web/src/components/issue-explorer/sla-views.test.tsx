import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import type { IssueSLA } from '@/types/flow'
import type { MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import { MyIssuesFilterMenu } from '@/components/my-issues/my-issues-filter-menu'
import { MyIssuesFilterBar } from '@/components/my-issues/my-issues-filter-bar'
import { createIssueDisplayOptions } from '@/components/my-issues/my-issues-display-defaults'
import { availableIssueGroupings, buildIssueGroups, caseGroupDescriptor, issueComparator, PAGED_ORDERINGS, pagedDisplayQuery, slaGroupCases } from './issue-grouping'
import { explorerFilterOptions, explorerPropertyOptions, issueToExplorerRow, matchesExplorerFilter } from './issue-explorer-model'
import { filterToQueryNode } from './issue-filter-query'
import { IssueBoardGroupHeader } from './issue-board'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

const NOW = Date.parse('2026-11-10T12:00:00Z')
const time = (hours: number) => new Date(NOW + hours * 3_600_000).toISOString()
const sla = (issueId: string, hours: number, patch: Partial<IssueSLA> = {}): IssueSLA => ({ id: `sla-${issueId}`, issueId, ruleId: 'rule', startedAt: time(-48), dueAt: time(hours), pausedMinutes: 0, remainingMinutes: 0, status: 'active', ...patch })
const filter = (values: string[], operator: MyIssuesAppliedFilter['operator'] = 'is'): MyIssuesAppliedFilter => ({ id: 'sla', field: 'sla', fieldLabel: 'SLA status', operator, value: values[0], valueLabel: values[0], values: values.map(value => ({ value, valueLabel: value })) })

function fixtures() {
  const issues = ['breached', 'high', 'moderate', 'low', 'achieved', 'failed', 'none'].map(id => makeIssue({ id, title: id }))
  return makeBootstrap({ issues, settings: { sla: { enabled: true } }, issueCollectionPaged: true, issueSlas: [sla('breached', -1), sla('high', 24), sla('moderate', 100), sla('low', 200), sla('achieved', -1, { completedAt: time(-2), status: 'completed' }), sla('failed', -2, { completedAt: time(-1), status: 'completed' })] })
}

beforeEach(() => { vi.spyOn(Date, 'now').mockReturnValue(NOW); localStorage.setItem('flow:locale', 'en-US') })
afterEach(() => vi.restoreAllMocks())

describe('SLA issue views', () => {
  it('groups all outcomes in the same order for local and server-paged views', () => {
    const data = fixtures(), display = createIssueDisplayOptions({ grouping: 'sla' })
    const rows = data.issues.map(issue => issueToExplorerRow(issue, data.workspace.urlKey, data.issues, data))
    const groups = buildIssueGroups(rows, display, { now: NOW })
    expect(groups.map(group => group.label)).toEqual(['Breached', 'High Risk', 'Moderate Risk', 'Low Risk', 'Achieved', 'Failed', 'No SLA'])
    expect(groups.every(group => group.translatableLabel)).toBe(true)
    const query = pagedDisplayQuery(display, NOW, [], data)
    expect(query.groupBy).toBe('cases')
    expect(query.groupCases).toEqual(slaGroupCases(data, NOW))
    expect(query.groupCases!.map(group => caseGroupDescriptor(group.value).label)).toEqual(['Breached', 'High Risk', 'Moderate Risk', 'Low Risk', 'Achieved', 'Failed', 'Paused', 'No SLA'])
  })

  it('translates selected risk/outcomes to scoped issue id predicates and honors negation', () => {
    const data = fixtures()
    const selected = filter(['medium', 'failed'])
    expect(filterToQueryNode(selected, { data, now: NOW })).toEqual({ field: 'id', operator: 'in', values: ['moderate', 'failed'] })
    expect(filterToQueryNode(filter(['high'], 'isNot'), { data, now: NOW })).toEqual({ not: { field: 'id', operator: 'in', values: ['high'] } })
    expect(filterToQueryNode(filter(['none']), { data, now: NOW })).toEqual({ or: [{ field: 'id', operator: 'in', values: [] }, { not: { field: 'id', operator: 'in', values: ['breached', 'high', 'moderate', 'low', 'achieved', 'failed'] } }] })
    expect(data.issues.filter(issue => matchesExplorerFilter(issueToExplorerRow(issue, data.workspace.urlKey, data.issues, data), selected, NOW)).map(issue => issue.id)).toEqual(['moderate', 'failed'])
    // Metadata has already passed workspace visibility checks; unavailable issues cannot enter the predicate.
    const visible = { ...data, issues: data.issues.filter(issue => issue.id !== 'high'), issueSlas: data.issueSlas.filter(sla => sla.issueId !== 'high') }
    expect(filterToQueryNode(filter(['high']), { data: visible, now: NOW })).toEqual({ field: 'id', operator: 'in', values: [] })
  })

  it('splits long SLA groups at the per-predicate id budget', () => {
    const data = { issueSlas: Array.from({ length: 1500 }, (_, index) => sla(String(index), -1)) }
    const breached = slaGroupCases(data, NOW)[0].filter as { or: { values: string[] }[] }
    expect(breached.or.map(node => node.values.length)).toEqual([1000, 500])
  })

  it('uses an SLA deadline before a normal due date, keeping missing dates last', () => {
    const data = fixtures()
    const rows = [makeIssue({ id: 'sla', dueDate: time(1) }), makeIssue({ id: 'normal', dueDate: time(2) }), makeIssue({ id: 'none' })].map(issue => issueToExplorerRow(issue, data.workspace.urlKey, [], { ...data, issueSlas: [sla('sla', 3)] }))
    expect([...rows].sort(issueComparator('dueDate')).map(row => row.id)).toEqual(['normal', 'sla', 'none'])
    expect([...rows].sort(issueComparator('dueDate', 'desc')).map(row => row.id)).toEqual(['sla', 'normal', 'none'])
    expect(PAGED_ORDERINGS).toContain('dueDate')
    expect(pagedDisplayQuery(createIssueDisplayOptions({ ordering: 'dueDate' }), NOW).sort).toBe('dueDate')
  })

  it('hides disabled SLA filtering/grouping and badges', () => {
    const data = fixtures(), disabled = { ...data, settings: { sla: { enabled: false } } }
    expect(availableIssueGroupings(data)).toContain('sla')
    expect(availableIssueGroupings(disabled)).not.toContain('sla')
    expect(explorerPropertyOptions(disabled).sla).toEqual([])
    expect(issueToExplorerRow(data.issues[0], data.workspace.urlKey, data.issues, disabled).sla).toBeUndefined()
  })

  it('offers the complete translated SLA outcome picker and applies a selected value', async () => {
    const data = fixtures(), options = explorerPropertyOptions(data), toggle = vi.fn()
    expect(options.sla.map(option => option.label)).toEqual(['No SLA', 'Breached', 'High Risk', 'Moderate Risk', 'Low Risk', 'Achieved', 'Failed'])
    render(<I18nProvider><MyIssuesFilterMenu open trigger={<button>Filter</button>} options={field => explorerFilterOptions(field, options)} onOpenChange={vi.fn()} onToggle={toggle}/></I18nProvider>)
    fireEvent.mouseMove(screen.getByRole('option', { name: /^SLA/ }))
    const risk = await screen.findByRole('option', { name: /^Moderate Risk/ })
    expect(risk.querySelector('svg')).not.toBeNull()
    fireEvent.click(risk)
    expect(toggle).toHaveBeenCalledWith('sla', expect.objectContaining({ id: 'medium', label: 'Moderate Risk' }))
  })

  it('translates SLA chip values and their value picker in Chinese', () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    const options = explorerPropertyOptions(fixtures())
    render(<I18nProvider><MyIssuesFilterBar filters={[filter(['medium'])]} filterOptions={() => options.sla} onClear={vi.fn()} onRemove={vi.fn()}/></I18nProvider>)
    expect(screen.getByText('中等风险')).toBeVisible()
    fireEvent.click(screen.getByText('中等风险'))
    expect(screen.getAllByText('中等风险')).toHaveLength(2)
    expect(screen.queryByText('Moderate Risk')).not.toBeInTheDocument()
  })

  it('translates generated SLA board column labels', () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    render(<I18nProvider><IssueBoardGroupHeader group={{ id: 'sla-medium', label: 'Moderate Risk', translatableLabel: true, issues: [] }}/></I18nProvider>)
    expect(screen.getByText('中等风险')).toBeVisible()
  })
})
