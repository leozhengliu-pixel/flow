import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ComponentProps } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { makeBootstrap, project } from '@/test/fixtures'
import { ProjectDetailPage } from './project-detail-page'

vi.mock('./project-overview', () => ({ ProjectOverview: ({ pickerRequest }: { pickerRequest?: { kind: string } }) => <div data-picker={pickerRequest?.kind}>overview body</div> }))
vi.mock('./project-activity', () => ({ ProjectActivity: () => <div>activity body</div> }))
vi.mock('./project-details-sidebar', () => ({ ProjectDetailsSidebar: ({ pickerRequest }: { pickerRequest?: { kind: string } }) => <aside data-picker={pickerRequest?.kind}>details sidebar</aside> }))
vi.mock('./project-insights', () => ({ ProjectInsights: () => <aside>insights panel</aside> }))
vi.mock('./project-issues', () => ({
  ProjectIssueFilterMenu: () => <button type="button">Filter</button>,
  ProjectIssueDisplayMenu: () => <button type="button">Display options</button>,
  ProjectIssues: () => <div>issues body</div>,
  ProjectNewView: () => <div>new view</div>,
}))

type Props = ComponentProps<typeof ProjectDetailPage>

function renderPage(overrides: Partial<Props> = {}) {
  const data = makeBootstrap()
  const props = {
    project: { ...project, milestones: [], resources: [] }, projects: [project], projectUpdates: [], issues: [], users: data.users,
    labels: [], labelGroups: [], viewer: data.viewer, tab: 'overview', savedViews: [], teams: data.teams, initiatives: [],
    documents: [], integrationConnections: [], projectStatuses: [project.status], activities: [],
    onTabChange: vi.fn(), onUpdate: vi.fn(), onDelete: vi.fn(), onToggleFavorite: vi.fn().mockResolvedValue(undefined),
    onSetSubscriptionEvents: vi.fn(), onCreateReminder: vi.fn(), onUpdateSavedView: vi.fn(), onDeleteSavedView: vi.fn(),
    ...overrides,
  } as unknown as Props
  const view = render(<I18nProvider><TooltipProvider delayDuration={0}><ProjectDetailPage {...props}/></TooltipProvider></I18nProvider>)
  return { ...view, props }
}

// jsdom has no layout, so Radix keeps a hovered tooltip open (pointer grace area); dismiss it between hovers.
async function hoverFresh(user: ReturnType<typeof userEvent.setup>, element: HTMLElement) {
  await user.keyboard('{Escape}')
  await user.hover(element)
}

async function findTooltip(text: string) {
  let match: HTMLElement | undefined
  await waitFor(() => {
    match = screen.queryAllByRole('tooltip').find(tooltip => tooltip.textContent?.includes(text))
    expect(match).toBeDefined()
  })
  return match!
}

beforeEach(() => { localStorage.clear(); vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} }) })
afterEach(() => vi.unstubAllGlobals())

describe('project detail header and tabs', () => {
  it('shows the Projects crumb only when opened from a projects list', async () => {
    const user = userEvent.setup()
    const { unmount } = renderPage()
    expect(screen.queryByRole('link', { name: 'Projects' })).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: project.name })).toBeVisible()
    unmount()
    const onOpenProjects = vi.fn()
    renderPage({ projectsOriginPath: '/acme/team/ENG/projects/all', onOpenProjects })
    const crumb = screen.getByRole('link', { name: 'Projects' })
    expect(crumb).toHaveAttribute('href', '/acme/team/ENG/projects/all')
    await user.click(crumb)
    expect(onOpenProjects).toHaveBeenCalledTimes(1)
  })

  it('keeps project insights out of the actions menu', async () => {
    const user = userEvent.setup(); renderPage()
    await user.click(screen.getByRole('button', { name: 'Project actions' }))
    expect(screen.queryByRole('menuitem', { name: /project insights/ })).not.toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: /Show updates and activity/ })).toBeVisible()
  })

  it('shows the insights toggle on every tab, right before project details', async () => {
    const user = userEvent.setup()
    const { unmount } = renderPage()
    expect(Array.from(document.querySelectorAll('.project-detail-page__toolbar-actions > button')).map(button => button.getAttribute('aria-label'))).toEqual(['Open project insights', 'Close project details'])
    unmount()
    renderPage({ tab: 'issues' })
    const toolbarButtons = Array.from(document.querySelectorAll('.project-detail-page__toolbar-actions > button')).map(button => button.textContent || button.getAttribute('aria-label'))
    expect(toolbarButtons).toEqual(['Filter', 'Display options', 'Open project insights', 'Close project details'])
    await user.click(screen.getByRole('button', { name: 'Open project insights' }))
    expect(screen.getByText('insights panel')).toBeVisible()
    expect(screen.queryByText('details sidebar')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Close project insights' })).toHaveAttribute('aria-pressed', 'true')
    await user.click(screen.getByRole('button', { name: 'Close project insights' }))
    expect(screen.queryByText('insights panel')).not.toBeInTheDocument()
  })

  it('shows shortcut tooltips on the project tabs', async () => {
    const user = userEvent.setup(); renderPage()
    await user.hover(screen.getByRole('link', { name: 'Activity' }))
    const tooltip = await screen.findByRole('tooltip')
    expect(tooltip).toHaveTextContent('View updates and activity')
    expect(tooltip).toHaveTextContent('2')
    await hoverFresh(user, screen.getByRole('link', { name: 'Overview' }))
    expect(await findTooltip('View Overview')).toHaveTextContent('1')
    await hoverFresh(user, screen.getByRole('link', { name: 'Issues' }))
    expect(await findTooltip('View Issues')).toHaveTextContent('3')
  })

  it('shows Linear tooltips on the header and toolbar buttons', async () => {
    const user = userEvent.setup(); renderPage()
    await user.hover(screen.getByRole('button', { name: 'Copy page URL' }))
    const copy = await findTooltip('Copy project URL')
    expect(copy.textContent).toMatch(/C$/)
    await hoverFresh(user, screen.getByRole('button', { name: 'Setup project notifications' }))
    expect(await findTooltip('Project notifications')).toBeInTheDocument()
    await hoverFresh(user, screen.getByRole('button', { name: 'Add new view' }))
    expect(await findTooltip('Create new view')).toBeInTheDocument()
    await hoverFresh(user, screen.getByRole('button', { name: 'Close project details' }))
    expect((await findTooltip('Close project details')).textContent).toMatch(/I$/)
    expect(screen.getByRole('button', { name: 'Close project details' })).not.toHaveAttribute('title')
  })

  it('shows no tooltip on the favorite and project actions buttons', async () => {
    const user = userEvent.setup(); renderPage()
    await user.hover(screen.getByRole('switch', { name: 'Add to favorites' }))
    await user.hover(screen.getByRole('button', { name: 'Project actions' }))
    await new Promise(resolve => setTimeout(resolve, 20))
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  })

  it('switches tabs with 1/2/3 but not while typing', () => {
    const { props } = renderPage()
    fireEvent.keyDown(window, { key: '2' })
    expect(props.onTabChange).toHaveBeenLastCalledWith('activity')
    fireEvent.keyDown(window, { key: '3' })
    expect(props.onTabChange).toHaveBeenLastCalledWith('issues')
    fireEvent.keyDown(window, { key: '1' })
    expect(props.onTabChange).toHaveBeenLastCalledWith('overview')
    const input = document.createElement('input'); document.body.append(input)
    fireEvent.keyDown(input, { key: '2' })
    const editor = document.createElement('div'); editor.setAttribute('contenteditable', 'true'); document.body.append(editor)
    fireEvent.keyDown(editor, { key: '3' })
    fireEvent.keyDown(window, { key: '2', metaKey: true })
    expect(props.onTabChange).toHaveBeenCalledTimes(3)
    input.remove(); editor.remove()
  })

  it('copies the project URL with Cmd/Ctrl+Shift+C', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    renderPage()
    fireEvent.keyDown(window, { key: 'C', code: 'KeyC', metaKey: true, shiftKey: true })
    expect(writeText).toHaveBeenCalledWith(location.href)
    fireEvent.keyDown(window, { key: 'C', code: 'KeyC', ctrlKey: true, shiftKey: true })
    expect(writeText).toHaveBeenCalledTimes(2)
    const input = document.createElement('input'); document.body.append(input)
    fireEvent.keyDown(input, { key: 'C', code: 'KeyC', metaKey: true, shiftKey: true })
    expect(writeText).toHaveBeenCalledTimes(2)
    input.remove()
  })

  it('toggles project details with Cmd/Ctrl+I', () => {
    renderPage()
    expect(screen.getByText('details sidebar')).toBeInTheDocument()
    fireEvent.keyDown(window, { key: 'i', metaKey: true })
    expect(screen.queryByText('details sidebar')).not.toBeInTheDocument()
    fireEvent.keyDown(window, { key: 'i', ctrlKey: true })
    expect(screen.getByText('details sidebar')).toBeInTheDocument()
  })

  it('opens sidebar pickers with P then S/P/A/M/L', () => {
    renderPage()
    const sidebar = screen.getByText('details sidebar')
    for (const [key, kind] of [['s', 'status'], ['p', 'priority'], ['a', 'lead'], ['m', 'members'], ['l', 'labels']]) {
      fireEvent.keyDown(window, { key: 'p' })
      fireEvent.keyDown(window, { key })
      expect(sidebar).toHaveAttribute('data-picker', kind)
    }
    expect(screen.getByText('overview body')).not.toHaveAttribute('data-picker')
  })

  it('opens date pickers with Ctrl+Alt+S and Ctrl+Alt+D', () => {
    renderPage()
    const sidebar = screen.getByText('details sidebar')
    fireEvent.keyDown(window, { key: 'ß', code: 'KeyS', ctrlKey: true, altKey: true })
    expect(sidebar).toHaveAttribute('data-picker', 'startDate')
    fireEvent.keyDown(window, { key: '∂', code: 'KeyD', ctrlKey: true, altKey: true })
    expect(sidebar).toHaveAttribute('data-picker', 'targetDate')
  })

  it('uses the overview chips when the sidebar is closed, and reopens the sidebar elsewhere', () => {
    localStorage.setItem(`flow:project:${project.id}:details`, 'false')
    const { unmount } = renderPage()
    expect(screen.queryByText('details sidebar')).not.toBeInTheDocument()
    fireEvent.keyDown(window, { key: 'p' }); fireEvent.keyDown(window, { key: 's' })
    expect(screen.getByText('overview body')).toHaveAttribute('data-picker', 'status')
    expect(screen.queryByText('details sidebar')).not.toBeInTheDocument()
    // The overview has no Labels chip until the project has labels, so the sidebar opens instead.
    fireEvent.keyDown(window, { key: 'p' }); fireEvent.keyDown(window, { key: 'l' })
    expect(screen.getByText('details sidebar')).toHaveAttribute('data-picker', 'labels')
    unmount()
    localStorage.setItem(`flow:project:${project.id}:details`, 'false')
    renderPage({ tab: 'issues' })
    fireEvent.keyDown(window, { key: 'p' }); fireEvent.keyDown(window, { key: 'p' })
    expect(screen.getByText('details sidebar')).toHaveAttribute('data-picker', 'priority')
  })

  it('ignores picker shortcuts while typing, in dialogs, after a timeout, or for other keys', () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    try {
      renderPage()
      const sidebar = screen.getByText('details sidebar')
      const input = document.createElement('input'); document.body.append(input)
      fireEvent.keyDown(input, { key: 'p' }); fireEvent.keyDown(input, { key: 's' })
      fireEvent.keyDown(input, { key: 'd', code: 'KeyD', ctrlKey: true, altKey: true })
      input.remove()
      const dialog = document.createElement('div'); dialog.setAttribute('role', 'dialog'); document.body.append(dialog)
      fireEvent.keyDown(window, { key: 'p' }); fireEvent.keyDown(window, { key: 's' })
      fireEvent.keyDown(window, { key: 'd', code: 'KeyD', ctrlKey: true, altKey: true })
      dialog.remove()
      fireEvent.keyDown(window, { key: 's' })
      fireEvent.keyDown(window, { key: 'p' }); vi.setSystemTime(Date.now() + 2000); fireEvent.keyDown(window, { key: 's' })
      fireEvent.keyDown(window, { key: 'p' }); fireEvent.keyDown(window, { key: 'x' }); fireEvent.keyDown(window, { key: 's' })
      expect(sidebar).not.toHaveAttribute('data-picker')
    } finally {
      vi.useRealTimers()
    }
  })
})
