import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import { issueToExplorerRow } from '@/components/issue-explorer/issue-explorer-model'
import { IssueRowActionsProvider } from './issue-row-actions'
import { MyIssuesList } from './my-issues-list'

describe('row context menu', () => {
  it('groups issue actions like the reference menu', async () => {
    const issue = makeIssue({ assignee: undefined })
    const otherTeam = { ...issue.team, id: 'team-2', key: 'NEXT', name: 'Next team' }
    const data = makeBootstrap({ issues: [issue], teams: [issue.team, otherTeam] })
    const onUpdateIssue = vi.fn(async () => data.issues[0])
    const onConvertIssue = vi.fn(async () => undefined)
    const row = issueToExplorerRow(issue, data.workspace.urlKey, data.issues, data)
    render(<I18nProvider><IssueRowActionsProvider value={{ data, onUpdateIssue, onConvertIssue }}>
      <MyIssuesList groups={[{ id: 'all', label: 'All', issues: [row] }]} onPropertyChange={vi.fn()} onContextAction={vi.fn()}/>
    </IssueRowActionsProvider></I18nProvider>)
    fireEvent.contextMenu(screen.getByRole('link', { name: new RegExp(row.identifier) }))
    expect(await screen.findByText(/^(Subscribe|Unsubscribe)$/)).toBeInTheDocument()
    for (const label of ['More properties', 'Mark as', 'Copy', 'Convert to', 'Make a copy…', 'Move', 'Open in', 'Favorite', 'Remind me', 'Delete']) expect(await screen.findByText(label)).toBeInTheDocument()
    for (const label of ['Assign to me', 'Archive', 'Relations']) expect(screen.queryByText(label)).not.toBeInTheDocument()
    fireEvent.click(screen.getByText(/^(Subscribe|Unsubscribe)$/))
    expect(onUpdateIssue).toHaveBeenCalledWith(row.id, expect.objectContaining({ subscriberIds: expect.any(Array) }))
  })
})
