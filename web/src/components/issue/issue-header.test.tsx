import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ComponentProps } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { TooltipProvider } from '@/components/ui/tooltip'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, teammate, viewer } from '@/test/fixtures'
import type { Presence, User } from '@/types/flow'
import { IssueHeader } from './issue-header'

const writeText = vi.fn(async (_text: string) => undefined)

function setup(overrides: Partial<ComponentProps<typeof IssueHeader>> = {}) {
  const issue = makeIssue()
  const data = makeBootstrap({ cycleSettings: {}, documents: [], members: [], drafts: [], reviews: [], favorites: [], customers: [], teamMembers: [], userSettings: {}, teamSettings: {}, projectTemplates: [], agentSkills: [], customerRequests: [], issueSlas: [], slaRules: [] } as never)
  const props: ComponentProps<typeof IssueHeader> = {
    issue, data, states: data.states, activities: [], saveState: 'idle', position: 3, total: 19,
    onClose: vi.fn(), onNavigate: vi.fn(), onUpdate: vi.fn(async () => undefined), onDelete: vi.fn(async () => undefined), onRelation: vi.fn(),
    ...overrides,
  }
  render(<MemoryRouter><I18nProvider><TooltipProvider><IssueHeader {...props}/></TooltipProvider></I18nProvider></MemoryRouter>)
  return props
}

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }

beforeEach(() => {
  globalThis.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver
  writeText.mockClear()
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
})

describe('IssueHeader', () => {
  it('shows the list position as "3 / 19" with Navigate down before Navigate up', () => {
    setup()
    const sequence = screen.getByRole('group', { name: 'Issue navigation' })
    expect(sequence).toHaveTextContent('3 / 19')
    expect(within(sequence).getAllByRole('button').map(button => button.getAttribute('aria-label'))).toEqual(['Navigate down', 'Navigate up'])
  })

  it('disables Navigate up on the first item and walks the list with the buttons', async () => {
    const props = setup({ position: 1, total: 4 })
    expect(screen.getByRole('button', { name: 'Navigate up' })).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: 'Navigate down' }))
    expect(props.onNavigate).toHaveBeenCalledWith('next')
  })

  it('shows only "<ID> <title>" when the issue was opened directly by URL', () => {
    setup({ returnPath: undefined, position: 1, total: 0 })
    const nav = screen.getByRole('navigation', { name: 'Issue breadcrumb' })
    expect(within(nav).getAllByRole('link')).toHaveLength(1)
    expect(within(nav).getByRole('link')).toHaveAttribute('aria-current', 'page')
    expect(nav).toHaveTextContent(/^TST-1\s*Test issue$/)
    expect(nav.querySelector('.issue-breadcrumb-separator')).toBeNull()
  })

  it('keeps the list breadcrumb when the issue was opened from a list', () => {
    setup({ returnPath: '/workspace/team/TST/all' })
    const nav = screen.getByRole('navigation', { name: 'Issue breadcrumb' })
    expect(within(nav).getAllByRole('link').map(link => link.textContent)).toEqual(['Test team', 'Issues', expect.stringContaining('Test issue')])
    expect(within(nav).getByRole('link', { name: 'Issues' })).toHaveAttribute('href', '/workspace/team/TST/all')
  })

  it('shows "Projects › ◆ Project" for an issue opened from a project\'s issues, the project linking back to that tab', async () => {
    setup({ returnPath: '/workspace/project/project-one/issues', navigationTrail: ['/workspace/projects/all', '/workspace/project/project-one/issues'] })
    const nav = screen.getByRole('navigation', { name: 'Issue breadcrumb' })
    expect(within(nav).getAllByRole('link').map(link => link.textContent)).toEqual(['Projects', 'Project one', expect.stringContaining('Test issue')])
    expect(within(nav).getByRole('link', { name: 'Projects' })).toHaveAttribute('href', '/workspace/projects/all')
    const projectLink = within(nav).getByRole('link', { name: 'Project one' })
    expect(projectLink).toHaveAttribute('href', '/workspace/project/project-one/issues')
    expect(projectLink.querySelector('.issue-breadcrumb-project-icon svg, .issue-breadcrumb-project-icon span')).not.toBeNull()
    await userEvent.hover(within(nav).getByRole('link', { name: 'Projects' }))
    expect(await screen.findByRole('tooltip')).toHaveTextContent('All projects')
  })

  it('shows the parent issue crumb (identifier, title on hover) for a sub-issue opened from its parent', async () => {
    const parent = makeIssue({ id: 'issue-2', identifier: 'TST-2', title: 'Parent issue' })
    const data = makeBootstrap({ issues: [makeIssue(), parent], cycleSettings: {}, documents: [], members: [], drafts: [], reviews: [], favorites: [], customers: [], teamMembers: [], userSettings: {}, teamSettings: {}, projectTemplates: [], agentSkills: [], customerRequests: [], issueSlas: [], slaRules: [] } as never)
    setup({ data, navigationTrail: ['/workspace/team/TST/all', '/workspace/issue/TST-2/parent-issue'], position: 1, total: 0 })
    const nav = screen.getByRole('navigation', { name: 'Issue breadcrumb' })
    expect(within(nav).getAllByRole('link').map(link => link.textContent)).toEqual(['Test team', 'TST-2', expect.stringContaining('Test issue')])
    await userEvent.hover(within(nav).getByRole('link', { name: 'TST-2' }))
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Parent issue')
  })

  it('hides the position when the issue was opened without a list', () => {
    setup({ position: 1, total: 0 })
    expect(screen.queryByRole('group', { name: 'Issue navigation' })).toBeNull()
  })

  it('has no close button on the full page: Esc closes and J / K walk the list', () => {
    const props = setup()
    expect(screen.queryByRole('button', { name: 'Close issue' })).toBeNull()
    fireEvent.keyDown(window, { key: 'j' })
    expect(props.onNavigate).toHaveBeenCalledWith('next')
    fireEvent.keyDown(window, { key: 'k' })
    expect(props.onNavigate).toHaveBeenCalledWith('previous')
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(props.onClose).toHaveBeenCalledTimes(1)
  })

  it('keeps a close button for previews and leaves their keys to the list', () => {
    const props = setup({ closable: true })
    fireEvent.keyDown(window, { key: 'Escape' })
    fireEvent.keyDown(window, { key: 'j' })
    expect(props.onClose).not.toHaveBeenCalled()
    expect(props.onNavigate).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Close issue' }))
    expect(props.onClose).toHaveBeenCalledTimes(1)
  })

  it('renders the floating copy / work strip in the reference order, copying a prompt instead of starting work', async () => {
    const props = setup()
    const strip = document.querySelector('.issue-command-strip') as HTMLElement
    expect(within(strip).getAllByRole('button').map(button => button.getAttribute('aria-label'))).toEqual(['Copy issue URL', 'Copy issue ID', 'Copy branch name', 'Copy as prompt', 'Choose coding tool'])
    expect(screen.queryByRole('button', { name: 'Start work on issue' })).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Copy issue ID' }))
    expect(writeText).toHaveBeenLastCalledWith('TST-1')
    await userEvent.click(screen.getByRole('button', { name: 'Copy as prompt' }))
    expect(writeText.mock.calls.at(-1)?.[0]).toContain('TST-1')
    expect(props.onUpdate).not.toHaveBeenCalled()
  })

  it('opens the coding tool menu with Copy as prompt and Configure coding tools', async () => {
    setup()
    await userEvent.click(screen.getByRole('button', { name: 'Choose coding tool' }))
    const menu = document.querySelector('.work-menu') as HTMLElement
    expect(within(menu).getByText('Copy as prompt')).toBeInTheDocument()
    expect(within(menu).getByText('Configure coding tools…')).toBeInTheDocument()
    expect(within(menu).getByLabelText('Command Option P')).toBeInTheDocument()
  })

  it('shows each other viewer once, never the viewer themself', () => {
    const third = { ...teammate, id: 'user-3', displayName: 'Third Person' } as User
    const at = '2026-10-12T00:00:00.000Z'
    const presence: Presence[] = [
      { clientId: 'viewer-tab-1', user: viewer, issueId: 'issue-1', lastSeenAt: at },
      { clientId: 'viewer-tab-2', user: viewer, issueId: 'issue-1', lastSeenAt: at },
      { clientId: 'mate-tab-1', user: teammate, issueId: 'issue-1', lastSeenAt: at },
      { clientId: 'mate-tab-2', user: teammate, issueId: 'issue-1', lastSeenAt: at },
      { clientId: 'third-elsewhere', user: third, issueId: 'issue-9', lastSeenAt: at },
    ]
    setup({ presence })
    const group = screen.getByRole('group', { name: 'People viewing this issue' })
    expect(within(group).getAllByLabelText(/./).map(item => item.getAttribute('aria-label'))).toEqual(['Teammate'])
  })

  it('renders no presence group when only the viewer has the issue open', () => {
    setup({ presence: [{ clientId: 'a', user: viewer, issueId: 'issue-1', lastSeenAt: '2026-10-12T00:00:00.000Z' }, { clientId: 'b', user: viewer, issueId: 'issue-1', lastSeenAt: '2026-10-12T00:00:00.000Z' }] })
    expect(screen.queryByRole('group', { name: 'People viewing this issue' })).toBeNull()
  })
})
