import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { backlog, completed, makeBootstrap, makeIssue, started } from '@/test/fixtures'
import { listIssueRecordGroups, listIssueRecords } from '@/lib/api'
import { PagedIssueList } from './paged-issue-list'
import { PagedIssueCache, nestedIssuePage, nestedPageLines } from './paged-issue-cache'
import { clearNavigationCache } from '@/lib/navigation-cache'
import { clearPagedIssueListCache } from './paged-issue-list-cache'
import { ISSUE_QUERY_INVALIDATED } from './paged-issue-invalidation'

vi.mock('@/lib/api', () => ({ listIssueRecordGroups: vi.fn(), listIssueRecords: vi.fn() }))
vi.mock('react-virtuoso', async () => {
  const React = await import('react')
  return { Virtuoso: () => null, GroupedVirtuoso: ({ groupCounts, groupContent, itemContent, style }: { groupCounts: number[]; groupContent: (index: number) => React.ReactNode; itemContent: (index: number, group: number) => React.ReactNode; style?: React.CSSProperties }) => {
    const [start, setStart] = React.useState(0)
    let offset = 0
    return <div data-testid="paged-list" style={style}><button onClick={() => setStart(100)}>Next page viewport</button>{groupCounts.map((count, group) => {
      const base = offset; offset += count
      return <section key={group}>{groupContent(group)}{Array.from({ length: Math.max(0, Math.min(10, count - start)) }, (_, index) => <div key={index}>{itemContent(base + start + index, group)}</div>)}</section>
    })}</div>
  } }
})
vi.mock('@/components/my-issues/my-issues-list', async importOriginal => {
  const actual = await importOriginal<typeof import('@/components/my-issues/my-issues-list')>()
  return { ...actual, MyIssuesRow: ({ issue, onOpen, nestedLines, showSubIssueProgress }: { issue: { id: string; title: string }; onOpen: (issue: { id: string }) => void; nestedLines?: readonly boolean[]; showSubIssueProgress?: boolean }) => <button data-depth={nestedLines?.length ?? 0} data-progress={String(showSubIssueProgress ?? true)} onClick={() => onOpen(issue)}>{issue.title}</button> }
})

afterEach(() => clearPagedIssueListCache())

describe('server-backed issue groups', () => {
  beforeEach(() => vi.clearAllMocks())
  it('uses the shared hidden-column controls and complete counts on paged boards', async () => {
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'state-backlog', count: 240 }] })
    vi.mocked(listIssueRecords).mockResolvedValue({ items: [], hasMore: false, total: 240 })
    const show = vi.fn(), data = makeBootstrap({ issues: [], favorites: [], issueCollectionPaged: true })
    render(<I18nProvider><PagedIssueList data={data} query={{ groupBy: 'status' }} layout="board" hiddenGroupIds={['state-backlog']} onShowGroup={show} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    const restore = await screen.findByRole('button', { name: /Backlog\s*240/ })
    await userEvent.click(restore)
    expect(show).toHaveBeenCalledWith('state-backlog')
    expect(screen.getByRole('region', { name: 'Hidden columns' })).toBeInTheDocument()
  })
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

  it('sizes the identifier column to the longest loaded identifier', async () => {
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'state-backlog', count: 2 }] })
    vi.mocked(listIssueRecords).mockResolvedValue({ items: [makeIssue({ id: 'issue-a', identifier: 'AIDP-7', title: 'Short' }), makeIssue({ id: 'issue-b', identifier: 'AIDP-17795', title: 'Long' })], hasMore: false, total: -1 })
    const data = makeBootstrap({ issues: [], favorites: [], issueCollectionPaged: true })
    render(<I18nProvider><PagedIssueList data={data} query={{ groupBy: 'status' }} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    await screen.findByRole('button', { name: 'Long' })
    expect(screen.getByTestId('paged-list').style.getPropertyValue('--issue-identifier-width')).toBe('10ch')
  })

  it('keeps the loaded extent and revalidates only the page on screen when a refresh lands', async () => {
    const issues = (from: number, suffix = '') => Array.from({ length: 100 }, (_, index) => makeIssue({ id: `issue-${from + index}`, title: `Issue ${from + index}${suffix}` }))
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'state-backlog', count: 300 }] })
    vi.mocked(listIssueRecords).mockImplementation(async query => query?.cursor
      ? { items: issues(100, query.limit === 100 && vi.mocked(listIssueRecords).mock.calls.length > 2 ? ' fresh' : ''), hasMore: true, nextCursor: 'page-3', total: -1 }
      : { items: issues(0), hasMore: true, nextCursor: 'page-2', total: -1 })
    const data = makeBootstrap({ issues: [], favorites: [], issueCollectionPaged: true })
    const render_ = (revision: number) => <I18nProvider><PagedIssueList data={{ ...data, issueCollectionRevision: revision }} query={{ groupBy: 'status' }} onOpenIssueRecord={vi.fn()}/></I18nProvider>
    const { rerender } = render(render_(0))
    await screen.findByRole('button', { name: 'Issue 0' })
    await userEvent.click(screen.getByRole('button', { name: 'Next page viewport' }))
    await screen.findByRole('button', { name: 'Issue 100' })
    expect(listIssueRecords).toHaveBeenCalledTimes(2)
    // The group count refresh must not drop the second page (a drop scrolls the list back to the top).
    rerender(render_(1))
    await screen.findByRole('button', { name: 'Issue 100 fresh' })
    expect(screen.queryByRole('button', { name: 'Issue 100' })).not.toBeInTheDocument()
    expect(listIssueRecordGroups).toHaveBeenCalledTimes(2)
    // Only the visible second page was fetched again (by its own cursor); the first page stays cached until it is reached.
    expect(listIssueRecords).toHaveBeenCalledTimes(3)
    expect(listIssueRecords).toHaveBeenLastCalledWith(expect.objectContaining({ groupValue: 'state-backlog', cursor: 'page-2' }), expect.any(AbortSignal))
    expect(screen.getByText('Backlog').closest('header')).toHaveTextContent('300')
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

describe('server-backed issue list loading states', () => {
  beforeEach(() => vi.clearAllMocks())
  it('shows only the loader caret, never loading text, before the first page arrives', () => {
    vi.mocked(listIssueRecordGroups).mockImplementation(() => new Promise(() => {}))
    vi.mocked(listIssueRecords).mockImplementation(() => new Promise(() => {}))
    const { container } = render(<I18nProvider><PagedIssueList data={makeBootstrap({ issues: [], issueCollectionPaged: true })} query={{ groupBy: 'status' }} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    const caret = screen.getByRole('status', { name: 'Loading issues' })
    expect(caret).toHaveClass('flow-loading-caret', 'is-positioned')
    expect(container).toHaveTextContent('')
    expect(container.querySelector('.flow-loading-frame')).toHaveAttribute('aria-busy', 'true')
  })

  it('keeps the previous result, inert, while the next query loads', async () => {
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'state-backlog', count: 1 }] })
    vi.mocked(listIssueRecords).mockResolvedValue({ items: [makeIssue({ title: 'Previous view row' })], hasMore: false, total: -1 })
    const data = makeBootstrap({ issues: [], issueCollectionPaged: true })
    const { rerender } = render(<I18nProvider><PagedIssueList data={data} query={{ groupBy: 'status', teamId: 'a' }} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    await screen.findByText('Previous view row')
    let resolveGroups!: (value: { groups: { value: string; count: number }[] }) => void
    vi.mocked(listIssueRecordGroups).mockImplementation(() => new Promise(resolve => { resolveGroups = resolve }))
    vi.mocked(listIssueRecords).mockResolvedValue({ items: [makeIssue({ id: 'next', title: 'Next view row' })], hasMore: false, total: -1 })
    rerender(<I18nProvider><PagedIssueList data={data} query={{ groupBy: 'status', teamId: 'b' }} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    const previous = screen.getByText('Previous view row')
    expect(previous.closest('.flow-loading-content')).toHaveClass('is-loading')
    expect(previous.closest('.flow-loading-content')).toHaveAttribute('inert')
    expect(screen.getByRole('status', { name: 'Loading issues' })).toBeInTheDocument()
    await act(async () => resolveGroups({ groups: [{ value: 'state-backlog', count: 1 }] }))
    await screen.findByText('Next view row')
    expect(screen.queryByText('Previous view row')).not.toBeInTheDocument()
    expect(screen.queryByRole('status', { name: 'Loading issues' })).not.toBeInTheDocument()
    expect(screen.getByText('Next view row').closest('.flow-loading-content')).not.toHaveAttribute('inert')
  })

  it('shows the list-footer caret instead of a text button for a page still loading', async () => {
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'state-backlog', count: 150 }] })
    vi.mocked(listIssueRecords).mockImplementation(async query => query?.cursor ? new Promise(() => {}) : ({ items: Array.from({ length: 100 }, (_, index) => makeIssue({ id: `issue-${index}`, title: `Issue ${index}` })), hasMore: true, nextCursor: 'page-2', total: -1 }))
    render(<I18nProvider><PagedIssueList data={makeBootstrap({ issues: [], issueCollectionPaged: true })} query={{ groupBy: 'status' }} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    await screen.findByRole('button', { name: 'Issue 0' })
    await userEvent.click(screen.getByRole('button', { name: 'Next page viewport' }))
    const caret = await screen.findByRole('status', { name: 'Loading issues' })
    expect(caret.parentElement).toHaveClass('flow-loading-footer')
    expect(caret).toHaveStyle({ animationDelay: '350ms' })
    expect(screen.queryByRole('button', { name: /Load issues/ })).not.toBeInTheDocument()
    expect(listIssueRecords).toHaveBeenLastCalledWith(expect.objectContaining({ cursor: 'page-2' }), expect.any(AbortSignal))
  })

  it('shows another viewer none of the rows cached for the previous one, and none after logout', async () => {
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'state-backlog', count: 1 }] })
    vi.mocked(listIssueRecords).mockResolvedValue({ items: [makeIssue({ id: 'private', title: 'Viewer A private row' })], hasMore: false, total: -1 })
    const viewerA = makeBootstrap({ issues: [], issueCollectionPaged: true })
    const viewerB = { ...viewerA, viewer: { ...viewerA.viewer, id: 'viewer-b' } }
    const view = (data: typeof viewerA) => <I18nProvider><PagedIssueList data={data} query={{ groupBy: 'status' }} onOpenIssueRecord={vi.fn()}/></I18nProvider>
    const first = render(view(viewerA))
    await screen.findByText('Viewer A private row')
    first.unmount()
    // The same viewer coming back sees the warm rows at once.
    const again = render(view(viewerA))
    expect(screen.getByText('Viewer A private row')).toBeInTheDocument()
    again.unmount()
    // Another viewer in the same tab starts empty and loads its own rows.
    vi.mocked(listIssueRecords).mockResolvedValue({ items: [makeIssue({ id: 'shared', title: 'Viewer B row' })], hasMore: false, total: -1 })
    const second = render(view(viewerB))
    expect(screen.queryByText('Viewer A private row')).not.toBeInTheDocument()
    await screen.findByText('Viewer B row')
    second.unmount()
    // Logout clears the cache: the viewer who comes back has to ask the server again.
    clearNavigationCache()
    vi.mocked(listIssueRecords).mockResolvedValue({ items: [makeIssue({ id: 'fresh', title: 'Fresh row' })], hasMore: false, total: -1 })
    render(view(viewerA))
    expect(screen.queryByText('Viewer A private row')).not.toBeInTheDocument()
    await screen.findByText('Fresh row')
  })

  it('drops cached lists nobody is showing when permissions force an invalidation', async () => {
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'state-backlog', count: 1 }] })
    vi.mocked(listIssueRecords).mockResolvedValue({ items: [makeIssue({ id: 'lost', title: 'Row the viewer may lose' })], hasMore: false, total: -1 })
    const data = makeBootstrap({ issues: [], issueCollectionPaged: true })
    const first = render(<I18nProvider><PagedIssueList data={data} query={{ groupBy: 'status' }} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    await screen.findByText('Row the viewer may lose')
    first.unmount()
    act(() => { window.dispatchEvent(new CustomEvent(ISSUE_QUERY_INVALIDATED, { detail: { workspaceKey: data.workspace.urlKey, force: true } })) })
    vi.mocked(listIssueRecords).mockResolvedValue({ items: [], hasMore: false, total: -1 })
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [] })
    render(<I18nProvider><PagedIssueList data={data} query={{ groupBy: 'status' }} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    expect(screen.queryByText('Row the viewer may lose')).not.toBeInTheDocument()
  })
})

describe('nested sub-issues in server-backed lists', () => {
  beforeEach(() => vi.clearAllMocks())
  const hierarchy = () => [
    makeIssue({ id: 'child-a', title: 'Child A', parentId: 'parent' }),
    makeIssue({ id: 'other', title: 'Other' }),
    makeIssue({ id: 'parent', title: 'Parent' }),
    makeIssue({ id: 'child-b', title: 'Child B', parentId: 'parent' }),
    makeIssue({ id: 'grandchild', title: 'Grandchild', parentId: 'child-b' }),
  ]
  const rows = () => screen.getAllByRole('button').filter(button => button.hasAttribute('data-depth')).map(button => `${button.textContent}:${button.getAttribute('data-depth')}`)
  const groupHeader = (name: string) => screen.getByText(name).closest('header')

  it('asks the server for nested groups and shows each sub-issue under its parent, whatever its own status', async () => {
    // The server leaves Child A (In progress) out of its own group and returns it with its parent's page.
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'state-backlog', count: 5 }, { value: 'state-started', count: 1 }] })
    vi.mocked(listIssueRecords).mockImplementation(async query => query?.groupValue === 'state-started'
      ? { items: [makeIssue({ id: 'orphan', title: 'Orphan', parentId: 'filtered-out' })], hasMore: false, total: -1 }
      : { items: [makeIssue({ id: 'other', title: 'Other', state: backlog }), makeIssue({ id: 'parent', title: 'Parent', state: backlog })], descendants: [hierarchy()[0], hierarchy()[3], hierarchy()[4]], hasMore: false, total: -1 })
    const data = makeBootstrap({ issues: [], issueCollectionPaged: true })
    render(<I18nProvider><PagedIssueList data={data} query={{ groupBy: 'status' }} nestedSubIssues onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    await screen.findByRole('button', { name: 'Grandchild' })
    expect(listIssueRecordGroups).toHaveBeenCalledWith(expect.objectContaining({ nested: true }), expect.any(AbortSignal))
    expect(listIssueRecords).toHaveBeenCalledWith(expect.objectContaining({ nested: true, groupValue: 'state-backlog' }), expect.any(AbortSignal))
    await screen.findByRole('button', { name: 'Orphan' })
    // A sub-issue whose parent is not in the list stays a top-level row of its own group.
    expect(rows()).toEqual(['Other:0', 'Parent:0', 'Child A:1', 'Child B:1', 'Grandchild:2', 'Orphan:0'])
    // Group headers count the nested rows under their parent's group, like the full list.
    expect(groupHeader('Backlog')).toHaveTextContent('5')
    expect(groupHeader('In progress')).toHaveTextContent('1')
    // Nested rows show the hierarchy itself instead of the sub-issue progress chip.
    expect(screen.getByRole('button', { name: 'Parent' })).toHaveAttribute('data-progress', 'false')
  })

  it('shows a parent on a later page with its sub-issues, even one that sorts first in another status', async () => {
    const fillers = Array.from({ length: 100 }, (_, index) => makeIssue({ id: `filler-${index}`, title: `Filler ${index}`, state: backlog }))
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'state-backlog', count: 103 }] })
    vi.mocked(listIssueRecords).mockImplementation(async query => query?.cursor
      ? { items: [makeIssue({ id: 'late-parent', title: 'Late parent', state: backlog })], descendants: [makeIssue({ id: 'late-child', title: 'Late child', parentId: 'late-parent', state: started }), makeIssue({ id: 'late-grandchild', title: 'Late grandchild', parentId: 'late-child', state: completed })], hasMore: false, total: -1 }
      : { items: fillers, hasMore: true, nextCursor: 'page-2', total: -1 })
    const onOpen = vi.fn()
    render(<I18nProvider><PagedIssueList data={makeBootstrap({ issues: [], issueCollectionPaged: true })} query={{ groupBy: 'status' }} nestedSubIssues onOpenIssueRecord={onOpen}/></I18nProvider>)
    await screen.findByRole('button', { name: 'Filler 0' })
    await userEvent.click(screen.getByRole('button', { name: 'Next page viewport' }))
    await screen.findByRole('button', { name: 'Late grandchild' })
    expect(listIssueRecords).toHaveBeenLastCalledWith(expect.objectContaining({ nested: true, cursor: 'page-2' }), expect.any(AbortSignal))
    expect(rows()).toEqual(['Late parent:0', 'Late child:1', 'Late grandchild:2'])
    // Opening a nested row keeps the list order for previous / next.
    await userEvent.click(screen.getByRole('button', { name: 'Late child' }))
    expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ id: 'late-child' }), expect.arrayContaining(['late-parent', 'late-child', 'late-grandchild']))
  })

  it('re-queries when a loaded sub-issue moves to another parent', async () => {
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'state-backlog', count: 2 }] })
    vi.mocked(listIssueRecords).mockResolvedValue({ items: [makeIssue({ id: 'parent', title: 'Parent', state: backlog })], descendants: [hierarchy()[0]], hasMore: false, total: -1 })
    const data = makeBootstrap({ issues: [], issueCollectionPaged: true })
    const { rerender } = render(<I18nProvider><PagedIssueList data={data} query={{ groupBy: 'status' }} nestedSubIssues onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    await screen.findByRole('button', { name: 'Child A' })
    rerender(<I18nProvider><PagedIssueList data={{ ...data, issues: [{ ...hierarchy()[0], parentId: 'another-parent' }] }} query={{ groupBy: 'status' }} nestedSubIssues onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    await waitFor(() => expect(listIssueRecordGroups).toHaveBeenCalledTimes(2))
  })

  it('keeps the server order and no guides when nesting is off, and the board never nests', async () => {
    vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: 'state-backlog', count: 5 }] })
    vi.mocked(listIssueRecords).mockResolvedValue({ items: hierarchy(), hasMore: false, total: -1 })
    const data = makeBootstrap({ issues: [], issueCollectionPaged: true })
    const { unmount } = render(<I18nProvider><PagedIssueList data={data} query={{ groupBy: 'status' }} onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    await screen.findByRole('button', { name: 'Grandchild' })
    expect(rows()).toEqual(['Child A:0', 'Other:0', 'Parent:0', 'Child B:0', 'Grandchild:0'])
    expect(screen.getByRole('button', { name: 'Parent' })).toHaveAttribute('data-progress', 'true')
    expect(vi.mocked(listIssueRecords).mock.calls.every(([query]) => !query?.nested)).toBe(true)
    unmount()
    render(<I18nProvider><PagedIssueList data={data} query={{ groupBy: 'status' }} layout="board" nestedSubIssues onOpenIssueRecord={vi.fn()}/></I18nProvider>)
    await waitFor(() => expect(listIssueRecordGroups).toHaveBeenCalledTimes(2))
    expect(vi.mocked(listIssueRecordGroups).mock.calls.every(([query]) => !query?.nested)).toBe(true)
  })
})

describe('nestedIssuePage', () => {
  it('puts every descendant right after its parent, in the page order, once', () => {
    const page = nestedIssuePage({ items: [makeIssue({ id: 'a' }), makeIssue({ id: 'b' })], descendants: [makeIssue({ id: 'b2', parentId: 'b' }), makeIssue({ id: 'a1', parentId: 'a' }), makeIssue({ id: 'b1', parentId: 'b' }), makeIssue({ id: 'b21', parentId: 'b2' }), makeIssue({ id: 'a1', parentId: 'a' })], hasMore: false })
    expect(page.items.map(issue => issue.id)).toEqual(['a', 'a1', 'b', 'b2', 'b21', 'b1'])
    expect(nestedPageLines(page.items).get('b21')).toEqual([false, true])
    expect(nestedPageLines(page.items).get('b1')).toEqual([true])
    const cache = new PagedIssueCache()
    cache.put('g', 0, page)
    expect(cache.start('g', 1)).toBe(6)
  })
})
