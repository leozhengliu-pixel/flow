import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import { listIssueRecordGroups, listIssueRecords } from '@/lib/api'
import { PagedIssueList } from './paged-issue-list'
import { ISSUE_QUERY_INVALIDATED } from './paged-issue-invalidation'

vi.mock('@/lib/api', () => ({ listIssueRecordGroups: vi.fn(), listIssueRecords: vi.fn() }))
vi.mock('react-virtuoso', async () => {
  const React = await import('react')
  return { GroupedVirtuoso: ({ groupCounts, groupContent, itemContent }: { groupCounts: number[]; groupContent: (index: number) => React.ReactNode; itemContent: (index: number, group: number) => React.ReactNode }) => {
    const [start, setStart] = React.useState(0)
    let offset = 0
    return <div><button onClick={() => setStart(100)}>Next page viewport</button>{groupCounts.map((count, group) => {
      const base = offset; offset += count
      return <section key={group}>{groupContent(group)}{Array.from({ length: Math.max(0, Math.min(10, count - start)) }, (_, index) => <div key={index}>{itemContent(base + start + index, group)}</div>)}</section>
    })}</div>
  } }
})
vi.mock('@/components/my-issues/my-issues-list', async importOriginal => {
  const actual = await importOriginal<typeof import('@/components/my-issues/my-issues-list')>()
  return { ...actual, MyIssuesRow: ({ issue, onOpen }: { issue: { title: string }; onOpen: () => void }) => <button onClick={onOpen}>{issue.title}</button> }
})

describe('server-backed issue groups', () => {
  beforeEach(() => vi.clearAllMocks())
  it('uses complete group counts and fetches a separate cursor page as the viewport advances', async () => {
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'state-backlog', count: 10000000 }] })
    vi.mocked(listIssueRecords).mockImplementation(async query => ({ items: Array.from({ length: 100 }, (_, index) => makeIssue({ id: `issue-${query?.cursor ? index + 100 : index}`, title: `Issue ${query?.cursor ? index + 100 : index}` })), hasMore: true, nextCursor: query?.cursor ? 'page-3' : 'page-2', total: -1 }))
    const data = makeBootstrap({ issues: [], favorites: [], issueCollectionPaged: true })
    const onOpen = vi.fn(), onTotal = vi.fn()
    render(<I18nProvider><PagedIssueList data={data} query={{ groupBy: 'status' }} onOpenIssueRecord={onOpen} onTotalChange={onTotal}/></I18nProvider>)
    await screen.findByRole('button', { name: 'Issue 0' })
    expect(screen.getByText('Backlog').closest('header')).toHaveTextContent('10000000')
    expect(onTotal).toHaveBeenCalledWith(10000000)
    await userEvent.click(screen.getByRole('button', { name: 'Next page viewport' }))
    await screen.findByRole('button', { name: 'Issue 100' })
    expect(listIssueRecords).toHaveBeenLastCalledWith(expect.objectContaining({ groupValue: 'state-backlog', cursor: 'page-2', limit: 100 }), expect.any(AbortSignal))
    await userEvent.click(screen.getByRole('button', { name: 'Issue 100' }))
    expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ id: 'issue-100' }), expect.arrayContaining(['issue-100', 'issue-101']))
  })

  it('ignores an old group response after the query changes', async () => {
    let resolveOld!: (value: { groups: { value: string; count: number }[] }) => void
    vi.mocked(listIssueRecordGroups).mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
      .mockResolvedValue({ groups: [{ value: 'state-started', count: 42 }] })
    vi.mocked(listIssueRecords).mockResolvedValue({ items: [makeIssue({ title: 'Current query result' })], hasMore: false, total: -1 })
    const data = makeBootstrap({ issues: [], favorites: [], issueCollectionPaged: true })
    const { rerender } = render(<I18nProvider><PagedIssueList data={data} query={{ groupBy: 'status', teamId: 'a' }} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    rerender(<I18nProvider><PagedIssueList data={data} query={{ groupBy: 'status', teamId: 'b' }} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    await screen.findByText('Current query result')
    resolveOld({ groups: [{ value: 'state-backlog', count: 999 }] })
    await waitFor(() => expect(screen.queryByText('Backlog')).not.toBeInTheDocument())
    expect(screen.getByText('In progress').closest('header')).toHaveTextContent('42')
  })

  it.each(['status', 'none'])('starts the first %s page before the group count returns', async groupBy => {
    let resolveGroups!: (value: { groups: { value: string; count: number }[] }) => void
    vi.mocked(listIssueRecordGroups).mockImplementation(() => new Promise(resolve => { resolveGroups = resolve }))
    vi.mocked(listIssueRecords).mockResolvedValue({ items: [makeIssue({ title: 'Prefetched first row' })], hasMore: false, total: -1 })
    render(<I18nProvider><PagedIssueList data={makeBootstrap({ issues: [] })} query={{ groupBy }} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    expect(listIssueRecords).toHaveBeenCalledTimes(1)
    await act(async () => resolveGroups({ groups: [{ value: groupBy === 'none' ? 'all' : 'state-backlog', count: 1 }] }))
    await screen.findByText('Prefetched first row')
    expect(listIssueRecords).toHaveBeenCalledTimes(1)
  })

  it('ignores unrelated team and assignee updates without clearing cached rows', async () => {
    const issue = makeIssue({ title: 'Current assigned issue' })
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'state-backlog', count: 1 }] })
    vi.mocked(listIssueRecords).mockResolvedValue({ items: [issue], hasMore: false, total: -1 })
    const data = makeBootstrap({ issues: [], issueCollectionPaged: true })
    const query = { groupBy: 'status', teamId: issue.team.id, filter: { field: 'assignee', values: [issue.assignee?.id ?? ''] } }
    const { rerender } = render(<I18nProvider><PagedIssueList data={data} query={query} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    await screen.findByText('Current assigned issue')
    rerender(<I18nProvider><PagedIssueList data={{ ...data, issues: [makeIssue({ id: 'unrelated', team: { ...issue.team, id: 'other-team' } }), makeIssue({ id: 'other-person', assignee: { ...data.viewer, id: 'different-person' } })] }} query={query} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    expect(listIssueRecordGroups).toHaveBeenCalledTimes(1)
    expect(listIssueRecords).toHaveBeenCalledTimes(1)
    expect(screen.getByText('Current assigned issue')).toBeVisible()
  })

  it('keeps existing rows during a count refresh but clears them on permission invalidation', async () => {
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'state-backlog', count: 1 }] })
    vi.mocked(listIssueRecords).mockResolvedValue({ items: [makeIssue({ title: 'Existing row' })], hasMore: false, total: -1 })
    const data = makeBootstrap({ issues: [], issueCollectionPaged: true })
    const { rerender } = render(<I18nProvider><PagedIssueList data={data} query={{ groupBy: 'status' }} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    await screen.findByText('Existing row')
    vi.mocked(listIssueRecordGroups).mockImplementation(() => new Promise(() => {}))
    rerender(<I18nProvider><PagedIssueList data={{ ...data, issueCollectionRevision: 1 }} query={{ groupBy: 'status' }} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    expect(screen.getByText('Existing row')).toBeVisible()
    await act(async () => window.dispatchEvent(new CustomEvent(ISSUE_QUERY_INVALIDATED, { detail: { workspaceKey: data.workspace.urlKey, force: true } })))
    expect(screen.queryByText('Existing row')).not.toBeInTheDocument()
  })

  it('coalesces a burst of relevant remote deletions into one background query', async () => {
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'state-backlog', count: 1 }] })
    vi.mocked(listIssueRecords).mockResolvedValue({ items: [makeIssue({ title: 'Remaining issue' })], hasMore: false, total: -1 })
    const data = makeBootstrap({ issues: [], issueCollectionPaged: true })
    render(<I18nProvider><PagedIssueList data={data} query={{ groupBy: 'status' }} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    await screen.findByText('Remaining issue')
    act(() => { for (let i = 0; i < 20; i++) window.dispatchEvent(new CustomEvent(ISSUE_QUERY_INVALIDATED, { detail: { workspaceKey: data.workspace.urlKey, issueId: `deleted-${i}` } })) })
    expect(screen.getByText('Remaining issue')).toBeVisible()
    await waitFor(() => expect(listIssueRecordGroups).toHaveBeenCalledTimes(2))
    expect(listIssueRecords).toHaveBeenCalledTimes(2)
  })
})
