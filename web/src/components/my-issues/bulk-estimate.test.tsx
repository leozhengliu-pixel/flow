import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import type { Issue, IssueUpdateInput, TeamSettings } from '@/types/flow'
import { executeExplorerBulkAction, explorerEstimateBulkOptions } from '@/components/issue-explorer/issue-explorer-model'
import { MyIssuesBulkActionBar } from './my-issues-bulk-action-bar'
import type { MyIssuesRowData } from './my-issues-list'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

const row = (id: string, teamId: string) => ({ id, identifier: id.toUpperCase(), title: id, href: '#', priority: 0, teamId, state: { id: 's', name: 'Todo', type: 'unstarted', color: '#999' }, createdAt: '', updatedAt: '' }) as MyIssuesRowData
const team = (teamId: string, settings: Partial<TeamSettings>) => ({ teamId, ...settings }) as TeamSettings
const data = makeBootstrap({
  teamSettings: {
    fib: team('fib', { estimateType: 'fibonacci' }),
    exp: team('exp', { estimateType: 'exponential' }),
    off: team('off', { estimateType: 'notUsed' }),
    child: team('child', { estimateType: 'notUsed', inheritIssueEstimation: true, parentTeamId: 'fib' }),
  },
})

function bar(issues: MyIssuesRowData[], onAction = vi.fn()) {
  render(<I18nProvider><MyIssuesBulkActionBar selectedIssues={issues} onAction={onAction} onClear={vi.fn()} actionOptions={action => action === 'estimate' ? explorerEstimateBulkOptions(data, issues.map(issue => issue.teamId)) : undefined}/></I18nProvider>)
  return onAction
}

describe('bulk "Set estimate…"', () => {
  it('lists the team scale with estimate icons, like the estimate picker', async () => {
    const user = userEvent.setup()
    const issues = [row('a', 'fib'), row('b', 'off')]
    const onAction = bar(issues)
    await user.click(screen.getByRole('button', { name: 'Open command menu' }))
    await user.click(await screen.findByRole('option', { name: /Set estimate/ }))
    expect(await screen.findByPlaceholderText('Set estimate to…')).toBeInTheDocument()
    const options = screen.getAllByRole('option')
    expect(options.map(option => option.textContent)).toEqual(['No estimate', '1 Point', '2 Points', '3 Points', '5 Points', '8 Points'])
    expect(options[0].querySelector('.estimate-icon[data-level="none"]')).not.toBeNull()
    expect(options[3].querySelector('.estimate-icon')).not.toBeNull()
    await user.click(screen.getByRole('option', { name: '3 Points' }))
    expect(onAction).toHaveBeenCalledWith('estimate', issues, '3')
  })

  it('is not offered when no selected team uses estimates', async () => {
    const user = userEvent.setup()
    bar([row('a', 'off')])
    await user.click(screen.getByRole('button', { name: 'Open command menu' }))
    await screen.findByRole('option', { name: /Change priority/ })
    expect(screen.queryByRole('option', { name: /Set estimate/ })).toBeNull()
  })

  it('merges the scales of several teams', () => {
    expect(explorerEstimateBulkOptions(data, ['fib', 'exp'])?.map(option => option.label)).toEqual(['No estimate', '1 Point', '2 Points', '3 Points', '4 Points', '5 Points', '8 Points', '16 Points'])
    expect(explorerEstimateBulkOptions(data, ['off', undefined])).toBeUndefined()
  })

  it('applies the value on each team scale and skips teams without estimates', async () => {
    const issues = [
      makeIssue({ id: 'a', team: { ...makeIssue().team, id: 'fib' } }),
      makeIssue({ id: 'b', team: { ...makeIssue().team, id: 'exp' } }),
      makeIssue({ id: 'c', team: { ...makeIssue().team, id: 'off' } }),
      makeIssue({ id: 'd', team: { ...makeIssue().team, id: 'child' } }),
    ] as Issue[]
    const onUpdateIssues = vi.fn(async (ids: string[], _input: IssueUpdateInput) => ids.map(id => issues.find(issue => issue.id === id)!))
    await executeExplorerBulkAction({ action: 'estimate', ids: issues.map(issue => issue.id), value: '3', data, issuesById: new Map(issues.map(issue => [issue.id, issue])), onUpdateIssue: vi.fn(), onUpdateIssues })
    expect(onUpdateIssues.mock.calls.map(([ids, input]) => [ids, input])).toEqual([[['a', 'd'], { estimate: 3 }], [['b'], { estimate: 4 }]])
    onUpdateIssues.mockClear()
    await executeExplorerBulkAction({ action: 'estimate', ids: ['a', 'c'], value: '-1', data, issuesById: new Map(issues.map(issue => [issue.id, issue])), onUpdateIssue: vi.fn(), onUpdateIssues })
    expect(onUpdateIssues.mock.calls.map(([ids, input]) => [ids, input])).toEqual([[['a'], { estimate: -1 }]])
  })
})
