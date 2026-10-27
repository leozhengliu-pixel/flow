import fs from 'node:fs'
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import { issueToExplorerRow } from '@/components/issue-explorer/issue-explorer-model'
import type { TeamSettings } from '@/types/flow'
import { IssueRowActionsProvider } from './issue-row-actions'
import { MyIssuesList } from './my-issues-list'

function mount(settings: Partial<TeamSettings>) {
  const issues = [makeIssue({ id: 'a', identifier: 'TST-1', estimate: 3 }), makeIssue({ id: 'b', identifier: 'TST-2', estimate: 5 }), makeIssue({ id: 'c', identifier: 'TST-3', estimate: undefined })]
  const data = makeBootstrap({ issues, teamSettings: { 'team-1': { teamId: 'team-1', ...settings } as TeamSettings } })
  const rows = issues.map(issue => issueToExplorerRow(issue, data.workspace.urlKey, data.issues, data))
  render(<I18nProvider><IssueRowActionsProvider value={{ data, onUpdateIssue: vi.fn() }}>
    <MyIssuesList groups={[{ id: 'todo', label: 'Todo', issues: rows }]}/>
  </IssueRowActionsProvider></I18nProvider>)
}

describe('issue list estimates', () => {
  it('shows "△ N" chips on estimated rows and the group total (unestimated issues count as 1 point)', () => {
    mount({ estimateType: 'fibonacci' })
    expect(screen.getByLabelText('Estimate 3')).toHaveTextContent('3')
    expect(screen.getByLabelText('Estimate 5')).toHaveTextContent('5')
    expect(screen.getByLabelText('Estimate 3').querySelector('.estimate-icon')).not.toBeNull()
    expect(screen.getByLabelText('9 estimate points')).toHaveTextContent('9')
  })

  it('leaves unestimated issues out of the total when "Count unestimated issues" is off', () => {
    mount({ estimateType: 'fibonacci', estimateCountUnestimated: false })
    expect(screen.getByLabelText('8 estimate points')).toHaveTextContent('8')
  })

  it('counts a 0 estimate as unestimated unless zero estimates are allowed', () => {
    const issues = [makeIssue({ id: 'a', identifier: 'TST-1', estimate: 0 }), makeIssue({ id: 'b', identifier: 'TST-2', estimate: 5 })]
    for (const [settings, total] of [[{ estimateType: 'fibonacci' }, 6], [{ estimateType: 'fibonacci', estimateAllowZero: true }, 5]] as const) {
      const data = makeBootstrap({ issues, teamSettings: { 'team-1': { teamId: 'team-1', ...settings } as TeamSettings } })
      const { unmount } = render(<I18nProvider><IssueRowActionsProvider value={{ data, onUpdateIssue: vi.fn() }}>
        <MyIssuesList groups={[{ id: 'todo', label: 'Todo', issues: issues.map(issue => issueToExplorerRow(issue, data.workspace.urlKey, data.issues, data)) }]}/>
      </IssueRowActionsProvider></I18nProvider>)
      expect(screen.getByLabelText(`${total} estimate points`)).toBeInTheDocument()
      unmount()
    }
  })

  it('colours the group total with the estimate total token in both themes', () => {
    const tokens = fs.readFileSync('src/styles/tokens.css', 'utf8')
    const css = fs.readFileSync('src/components/my-issues/my-issues-list.module.css', 'utf8')
    expect(tokens).toMatch(/:root\{color-scheme:dark;[^}]*--estimate-group-total:lch\(63\.304% 7 271\.994\)/)
    expect(tokens).toMatch(/:root\[data-theme="light"\]\{[^}]*--estimate-group-total:lch\(/)
    expect(css).toContain('.groupEstimate:global(.estimate-chip){gap:4px;color:var(--estimate-group-total)')
  })

  it('hides chips and totals for teams that do not use estimates', () => {
    mount({ estimateType: 'notUsed' })
    expect(screen.queryByLabelText('Estimate 3')).toBeNull()
    expect(screen.queryByLabelText(/estimate points$/)).toBeNull()
  })
})
