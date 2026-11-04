import { describe, expect, it, vi } from 'vitest'
import { StrictMode } from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import { issueToExplorerRow } from '@/components/issue-explorer/issue-explorer-model'
import { IssueRowActionsProvider } from './issue-row-actions'
import { MyIssuesList } from './my-issues-list'
const layout=vi.hoisted(()=>({desktop:false}))
vi.mock('@/hooks/use-desktop-layout',()=>({useDesktopLayout:()=>layout.desktop}))

describe('row context menu', () => {
  it('opens searchable Team choices from a focused row shortcut and filters forbidden teams', async () => {
    layout.desktop = true
    const issue = makeIssue()
    const target = { ...issue.team, id: 'target', name: 'Destination', key: 'DST' }
    const privateTeam = { ...issue.team, id: 'private', name: 'Private', private: true }
    const data = makeBootstrap({ issues: [issue], teams: [issue.team, target, privateTeam] })
    const onUpdateIssue = vi.fn().mockResolvedValue(issue)
    const row = issueToExplorerRow(issue, data.workspace.urlKey, data.issues, data)
    render(<StrictMode><I18nProvider><IssueRowActionsProvider value={{ data, onUpdateIssue }}><MyIssuesList groups={[{ id: 'all', label: 'All', issues: [row] }]} onPropertyChange={vi.fn()}/></IssueRowActionsProvider></I18nProvider></StrictMode>)
    const link = screen.getByRole('link', { name: new RegExp(issue.identifier) })
    link.focus()
    fireEvent.keyDown(link, { key: 'M', code: 'KeyM', ctrlKey: true, shiftKey: true })
    const search = await screen.findByPlaceholderText('Move to team…')
    expect(screen.queryByText('Private TST')).not.toBeInTheDocument()
    fireEvent.change(search, { target: { value: 'DST' } })
    fireEvent.keyDown(search, { key: 'ArrowDown' })
    fireEvent.keyDown(search, { key: 'Enter' })
    await waitFor(() => expect(onUpdateIssue).toHaveBeenCalledExactlyOnceWith(issue.id, { teamId: target.id }))
  })
  it.each([false,true])('keeps subscription and relation removal after the standard actions: %s', async desktop => {
    layout.desktop=desktop
    const issue = makeIssue({ assignee: undefined, parentId: 'parent', subscriberIds: [] })
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
    const labels = screen.getAllByRole('menuitem').map(item => item.querySelector('span:not([aria-hidden])')?.textContent)
    expect(labels.slice(labels.indexOf('Copy'))).toEqual(['Copy', 'Convert to', 'Make a copy…', 'Move', 'Open in', 'Favorite', 'Subscribe', 'Remind me', 'Remove…', 'Delete'])
    fireEvent.click(screen.getByText(/^(Subscribe|Unsubscribe)$/))
    expect(onUpdateIssue).toHaveBeenCalledWith(row.id, expect.objectContaining({ subscriberIds: expect.any(Array) }))
  })
})
