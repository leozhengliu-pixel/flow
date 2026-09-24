import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import { issueToExplorerRow } from '@/components/issue-explorer/issue-explorer-model'
import { IssueRowActionsProvider } from './issue-row-actions'
import { MyIssuesList } from './my-issues-list'

describe('row context menu', () => {
  it('offers the full issue action set and assigns to the viewer', async () => {
    const data = makeBootstrap({ issues: [makeIssue({ assignee: undefined })] })
    const onUpdateIssue = vi.fn(async () => data.issues[0])
    const row = issueToExplorerRow(data.issues[0], data.workspace.urlKey, data.issues, data)
    render(<I18nProvider><IssueRowActionsProvider value={{ data, onUpdateIssue }}>
      <MyIssuesList groups={[{ id: 'all', label: 'All', issues: [row] }]} onPropertyChange={vi.fn()} onContextAction={vi.fn()}/>
    </IssueRowActionsProvider></I18nProvider>)
    fireEvent.contextMenu(screen.getByRole('link', { name: new RegExp(row.identifier) }))
    expect(await screen.findByText(/^(Subscribe|Unsubscribe)$/)).toBeInTheDocument()
    for (const label of ['Assign to me', 'Set parent issue…', 'Relations', 'Remind me', 'Copy git branch name', 'Open in new tab', 'Make a copy…', 'Archive']) expect(await screen.findByText(label)).toBeInTheDocument()
    fireEvent.click(screen.getByText('Assign to me'))
    expect(onUpdateIssue).toHaveBeenCalledWith(row.id, { assigneeId: data.viewer.id })
  })
})
