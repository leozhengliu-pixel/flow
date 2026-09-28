import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import { issueToExplorerRow } from '@/components/issue-explorer/issue-explorer-model'
import { IssueRowActionsProvider } from './issue-row-actions'
import { MyIssuesList } from './my-issues-list'

describe('row context menu', () => {
  it('groups issue actions like the reference menu', async () => {
    const data = makeBootstrap({ issues: [makeIssue({ assignee: undefined })] })
    const onUpdateIssue = vi.fn(async () => data.issues[0])
    const row = issueToExplorerRow(data.issues[0], data.workspace.urlKey, data.issues, data)
    render(<I18nProvider><IssueRowActionsProvider value={{ data, onUpdateIssue }}>
      <MyIssuesList groups={[{ id: 'all', label: 'All', issues: [row] }]} onPropertyChange={vi.fn()} onContextAction={vi.fn()}/>
    </IssueRowActionsProvider></I18nProvider>)
    fireEvent.contextMenu(screen.getByRole('link', { name: new RegExp(row.identifier) }))
    expect(await screen.findByText(/^(Subscribe|Unsubscribe)$/)).toBeInTheDocument()
    for (const label of ['More properties', 'Mark as', 'Copy', 'Make a copy…', 'Open in', 'Favorite', 'Remind me', 'Delete']) expect(await screen.findByText(label)).toBeInTheDocument()
    for (const label of ['Assign to me', 'Archive', 'Relations']) expect(screen.queryByText(label)).not.toBeInTheDocument()
    fireEvent.click(screen.getByText(/^(Subscribe|Unsubscribe)$/))
    expect(onUpdateIssue).toHaveBeenCalledWith(row.id, expect.objectContaining({ subscriberIds: expect.any(Array) }))
  })
})
