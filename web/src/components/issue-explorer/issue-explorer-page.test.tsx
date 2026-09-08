import type { ReactNode } from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { VirtuosoMockContext } from 'react-virtuoso'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { backlog, started, makeBootstrap, makeIssue } from '@/test/fixtures'
import type { BootstrapData, SavedView } from '@/types/flow'
import type { MyIssuesDisplayOptions } from '@/components/my-issues/my-issues-surface'
import { IssueExplorerPage, type IssueExplorerPageProps } from './issue-explorer-page'

vi.mock('./issue-explorer-surface', () => ({
  IssueExplorerSurface: ({ children, displayOptions, onDisplayOptionsChange }: { children: ReactNode; displayOptions: MyIssuesDisplayOptions; onDisplayOptionsChange: (display: MyIssuesDisplayOptions) => void }) => <div><button onClick={() => onDisplayOptionsChange({ ...displayOptions, grouping: 'priority' })}>Group by priority</button>{children}</div>,
}))

function page(data: BootstrapData, props: Partial<IssueExplorerPageProps> = {}) {
  return <I18nProvider><VirtuosoMockContext.Provider value={{ viewportHeight: 440, itemHeight: 44 }}><IssueExplorerPage data={data} scope={{ kind: 'workspace' }} view="all" viewHref={view => `#${view}`} onNavigateView={vi.fn()} onOpenIssue={vi.fn()} onUpdateIssue={vi.fn()} onUpdateIssues={vi.fn()} onDeleteIssues={vi.fn()} {...props}/></VirtuosoMockContext.Provider></I18nProvider>
}

function dataWithGroups(first: number, second: number) {
  return makeBootstrap({ favorites: [], issues: Array.from({ length: first + second }, (_, index) => makeIssue({ id: `issue-${index}`, identifier: `TST-${index + 1}`, number: index + 1, sortOrder: index, title: `Issue ${index + 1}`, state: index < first ? backlog : started, priority: index < first ? 1 : 2 })) })
}

describe('complete issue grouping', () => {
  beforeEach(() => localStorage.clear())

  it('shows full counts and later groups without loading more pages', async () => {
    const { container } = render(page(dataWithGroups(160, 230)))
    await waitFor(() => expect(screen.getByText('Backlog').closest('header')).toHaveTextContent('160'))
    fireEvent.click(screen.getByRole('button', { name: 'Collapse group' }))
    await waitFor(() => expect(screen.getByText('In progress').closest('header')).toHaveTextContent('230'))
    expect(container.querySelectorAll('a[href*="/issue/"]').length).toBeLessThan(80)
    fireEvent.click(screen.getByRole('button', { name: 'Group by priority' }))
    await waitFor(() => expect(screen.getByText('High').closest('header')).toHaveTextContent('230'))
  })

  it('keeps the same complete collection for team scopes and saved views', async () => {
    const data = dataWithGroups(140, 210)
    data.issues.push(makeIssue({ id: 'foreign', team: { ...data.teams[0], id: 'other-team' } }), makeIssue({ id: 'archived', archivedAt: '2026-09-01' }))
    const savedView = { id: 'saved', name: 'Active work', resource: 'issues', scope: 'team', teamId: data.teams[0].id, view: 'active', filters: [], display: {} } as unknown as SavedView
    render(page(data, { scope: { kind: 'team', team: data.teams[0] }, view: 'active', savedView }))
    await waitFor(() => expect(screen.getByText('In progress').closest('header')).toHaveTextContent('210'))
    expect(screen.queryByText('Backlog')).not.toBeInTheDocument()
  })

  it('keeps ten thousand issues counted while only mounting viewport rows and reflects sync updates', async () => {
    const data = dataWithGroups(10000, 0)
    const { container, rerender } = render(page(data))
    await waitFor(() => expect(screen.getByText('Backlog').closest('header')).toHaveTextContent('10000'))
    const mounted = container.querySelectorAll('a[href*="/issue/"]').length
    expect(mounted).toBeGreaterThan(0)
    expect(mounted).toBeLessThan(80)
    rerender(page({ ...data, issues: data.issues.slice(1) }))
    await waitFor(() => expect(screen.getByText('Backlog').closest('header')).toHaveTextContent('9999'))
  })
})
