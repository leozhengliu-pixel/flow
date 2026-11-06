import type { ReactNode } from 'react'
import { fireEvent, render, screen } from '@testing-library/react'
import { VirtuosoMockContext } from 'react-virtuoso'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import type { BootstrapData, SavedView } from '@/types/flow'
import type { MyIssuesDisplayOptions } from '@/components/my-issues/my-issues-surface'
import { defaultMyIssuesDisplayOptions } from '@/components/my-issues/my-issues-display-defaults'
import { IssueExplorerPage, type IssueExplorerPageProps } from './issue-explorer-page'

// The surface only reports the display options the explorer hands it, so each test reads the defaults from here.
vi.mock('./issue-explorer-surface', () => ({
  IssueExplorerSurface: ({ children, displayOptions, onDisplayOptionsChange }: { children: ReactNode; displayOptions: MyIssuesDisplayOptions; onDisplayOptionsChange: (display: MyIssuesDisplayOptions) => void }) => <div>
    <output data-testid="display">{JSON.stringify({ ...displayOptions, properties: [...displayOptions.properties] })}</output>
    <button onClick={() => onDisplayOptionsChange({ ...displayOptions, ordering: 'title' })}>Order by title</button>
    {children}
  </div>,
}))
vi.mock('@nivo/bar', () => ({ ResponsiveBar: () => <svg/> }))
class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

function page(data: BootstrapData, props: Partial<IssueExplorerPageProps> = {}) {
  return <I18nProvider><VirtuosoMockContext.Provider value={{ viewportHeight: 440, itemHeight: 44 }}><IssueExplorerPage data={data} scope={{ kind: 'workspace' }} view="all" viewHref={view => `#${view}`} onNavigateView={vi.fn()} onOpenIssue={vi.fn()} onUpdateIssue={vi.fn()} onUpdateIssues={vi.fn()} onDeleteIssues={vi.fn()} {...props}/></VirtuosoMockContext.Provider></I18nProvider>
}
function shownDisplay() { return JSON.parse(screen.getByTestId('display').textContent ?? '{}') as Omit<MyIssuesDisplayOptions, 'properties'> & { properties: string[] } }
function stubDesktop(matches: boolean) { vi.stubGlobal('matchMedia', vi.fn(() => ({ matches, addEventListener: vi.fn(), removeEventListener: vi.fn(), addListener: vi.fn(), removeListener: vi.fn() }))) }
function makeData() { return makeBootstrap({ favorites: [], teamMembers: [], issues: [makeIssue({ id: 'one', identifier: 'TST-1', title: 'First' })] }) }

describe('issue view display defaults (the reference app: List, grouped by Status, ordered by Priority)', () => {
  beforeEach(() => localStorage.clear())
  afterEach(() => vi.unstubAllGlobals())

  it.each(['all', 'active', 'backlog'] as const)('starts the team %s list ordered by priority and grouped by status', view => {
    stubDesktop(true)
    const data = makeData()
    render(page(data, { scope: { kind: 'team', team: data.teams[0] }, view }))
    expect(shownDisplay()).toMatchObject({ layout: 'list', grouping: 'status', subGrouping: 'none', ordering: 'priority', orderCompletedByRecency: false, showSubIssues: true, nestedSubIssues: true, showEmptyGroups: false, showTriageIssues: false, completedWindow: view === 'all' ? 'all' : 'none' })
    expect(shownDisplay().orderDirection).toBeUndefined()
    expect(shownDisplay().properties).toEqual(expect.arrayContaining(['id', 'status', 'assignee', 'priority', 'project', 'labels', 'created', 'dueDate', 'cycle', 'pullRequests']))
  })

  it('starts the workspace issue list ordered by priority', () => {
    stubDesktop(false)
    render(page(makeData()))
    expect(shownDisplay()).toMatchObject({ layout: 'list', grouping: 'status', ordering: 'priority' })
  })

  it('keeps Manual ordering on the board route', () => {
    stubDesktop(true)
    const data = makeData()
    render(page(data, { scope: { kind: 'team', team: data.teams[0] }, boardRoute: true }))
    expect(shownDisplay()).toMatchObject({ layout: 'board', ordering: 'importance', showEmptyGroups: true })
  })

  it.each([['workspace'], ['team']])('starts a new %s view draft in List, Status grouping, Priority ordering, showing completed and triage issues', kind => {
    stubDesktop(true)
    const data = makeData()
    render(page(data, { creatingView: true, ...(kind === 'team' ? { scope: { kind: 'team' as const, team: data.teams[0] } } : {}) }))
    expect(shownDisplay()).toMatchObject({ layout: 'list', grouping: 'status', subGrouping: 'none', ordering: 'priority', orderCompletedByRecency: false, completedWindow: 'all', showSubIssues: true, nestedSubIssues: true, showEmptyGroups: false, showTriageIssues: true })
    expect(shownDisplay().properties).toEqual(expect.arrayContaining(['id', 'status', 'assignee', 'priority', 'project', 'dueDate', 'labels', 'created', 'pullRequests']))
  })

  it('does not let a new view draft read or overwrite the All issues display', () => {
    stubDesktop(true)
    const data = makeData()
    const key = `${data.workspace.urlKey}:issue-explorer:workspace:all:display`
    localStorage.setItem(key, JSON.stringify({ ordering: 'title', grouping: 'none' }))
    render(page(data, { creatingView: true }))
    expect(shownDisplay()).toMatchObject({ grouping: 'status', ordering: 'priority' })
    fireEvent.click(screen.getByRole('button', { name: 'Order by title' }))
    expect(shownDisplay().ordering).toBe('title')
    expect(JSON.parse(localStorage.getItem(key)!)).toEqual({ ordering: 'title', grouping: 'none' })
  })

  it('keeps a display the user saved, a saved view ordering and a team default', () => {
    stubDesktop(true)
    const data = makeData()
    const team = data.teams[0]
    localStorage.setItem(`${data.workspace.urlKey}:issue-explorer:workspace:all:display`, JSON.stringify({ ordering: 'importance' }))
    const stored = render(page(data))
    expect(shownDisplay().ordering).toBe('importance')
    stored.unmount()
    const saved = (display: Record<string, unknown>) => ({ id: 'saved', name: 'Saved', resource: 'issues', scope: 'team', teamId: team.id, view: 'all', filters: [], display }) as unknown as SavedView
    const viewWithOrdering = render(page(data, { scope: { kind: 'team', team }, savedView: saved({ ordering: 'title' }) }))
    expect(shownDisplay().ordering).toBe('title')
    viewWithOrdering.unmount()
    const viewWithout = render(page(data, { scope: { kind: 'team', team }, savedView: saved({}) }))
    expect(shownDisplay().ordering).toBe('priority')
    viewWithout.unmount()
    const withDefault = { ...data, teamSettings: { ...data.teamSettings, [team.id]: { ...data.teamSettings?.[team.id], issueViewDefaults: { all: { ordering: 'importance' } } } } } as BootstrapData
    localStorage.clear()
    render(page(withDefault, { scope: { kind: 'team', team } }))
    expect(shownDisplay().ordering).toBe('importance')
  })

  it('leaves My issues on Focus grouping and Manual ordering', () => {
    expect(defaultMyIssuesDisplayOptions).toMatchObject({ layout: 'list', grouping: 'focus', subGrouping: 'none', ordering: 'importance', completedWindow: 'pastDay', showSubIssues: true })
  })
})
