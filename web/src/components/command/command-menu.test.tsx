import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, project } from '@/test/fixtures'
import type { Issue, IssueUpdateInput } from '@/types/flow'
import { CommandMenu } from './command-menu'
import { resetCommandContext, useRegisterCommandContext, type CommandContext } from './command-context'

const api = vi.hoisted(() => ({
  searchWorkspace: vi.fn(),
  listIssueRecords: vi.fn(),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/api')>(), ...api }))

const first = makeIssue()
const second = makeIssue({ id: 'issue-2', identifier: 'TST-2', number: 2, title: 'Second issue', labels: [] })
const data = makeBootstrap({ issues: [first, second] })

function Register({ context }: { context?: CommandContext }) {
  useRegisterCommandContext(context)
  return null
}

function setup(context?: CommandContext) {
  const onUpdateIssue = vi.fn(async (id: string, input: IssueUpdateInput) => ({ ...(id === second.id ? second : first), ...input }) as Issue)
  const onUpdateIssues = vi.fn(async (ids: string[], input: IssueUpdateInput) => ids.map(id => ({ ...(id === second.id ? second : first), ...input }) as Issue))
  const onOpenChange = vi.fn()
  const noop = vi.fn()
  function Harness() {
    const [open, setOpen] = useState(true)
    return <>
      <Register context={context}/>
      <CommandMenu open={open} onOpenChange={value => { onOpenChange(value); setOpen(value) }} data={data} onUpdateIssue={onUpdateIssue} onUpdateIssues={onUpdateIssues}
        onCreateIssue={noop} onCreateDocument={noop} onCreateIssueTemplate={noop} onCreateProject={noop} onCreateView={noop} onCreateInitiative={noop} onSearchWorkspace={noop}
        onNavigateInbox={noop} onNavigateMyIssues={noop} onNavigateProjects={noop} onNavigateInitiatives={noop} onNavigateViews={noop} onNavigateMembers={noop}
        onNavigateCustomers={noop} onNavigateAgent={noop} onOpenResult={noop}/>
    </>
  }
  render(<MemoryRouter><I18nProvider><Harness/></I18nProvider></MemoryRouter>)
  return { onUpdateIssue, onUpdateIssues, onOpenChange, user: userEvent.setup() }
}

const input = () => document.querySelector<HTMLInputElement>('[cmdk-input]')!

describe('context-aware command menu', () => {
  beforeEach(() => {
    localStorage.setItem('flow:locale', 'en-US')
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    api.searchWorkspace.mockResolvedValue({ results: [] })
    api.listIssueRecords.mockResolvedValue({ items: [] })
  })
  afterEach(() => { resetCommandContext(); vi.unstubAllGlobals(); vi.clearAllMocks() })

  it('shows only global actions without a context', () => {
    setup()
    expect(screen.queryByLabelText('Command context')).not.toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /Change status/ })).not.toBeInTheDocument()
    expect(screen.getByRole('option', { name: /Create new issue\.\.\.C/ })).toBeInTheDocument()
  })

  it('shows the open issue as a context chip and lists issue actions first', () => {
    setup({ kind: 'issues', source: 'detail', issues: [first] })
    expect(screen.getByLabelText('Command context')).toHaveTextContent('TST-1 Test issue')
    const options = screen.getAllByRole('option')
    expect(options[0]).toHaveTextContent('Change status…')
    expect(options.map(option => option.textContent)).toEqual(expect.arrayContaining([
      expect.stringContaining('Assign to…'), expect.stringContaining('Copy git branch name'), expect.stringContaining('Mark as duplicate…'), expect.stringContaining('Create new issue'),
    ]))
  })

  it('opens a nested status page and applies the picked state through the surface handler', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined)
    const { user, onUpdateIssue, onOpenChange } = setup({ kind: 'issues', source: 'detail', issues: [first], onUpdate })
    await user.click(screen.getByRole('option', { name: /Change status…/ }))
    expect(screen.getByLabelText('Command context')).toHaveTextContent('Change status…')
    expect(input()).toHaveValue('')
    const done = screen.getByRole('option', { name: /Done/ })
    expect(within(screen.getByRole('option', { name: /In progress/ })).getByLabelText('Current')).toBeInTheDocument()
    await user.click(done)
    expect(onUpdate).toHaveBeenCalledWith({ stateId: 'state-completed' })
    expect(onUpdateIssue).not.toHaveBeenCalled()
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('applies a change to every selected issue with the bulk update', async () => {
    const { user, onUpdateIssues } = setup({ kind: 'issues', source: 'selection', issues: [{ id: first.id, identifier: first.identifier, title: first.title }, { id: second.id, identifier: second.identifier, title: second.title }] })
    expect(screen.getByLabelText('Command context')).toHaveTextContent('2 issues')
    await user.click(screen.getByRole('option', { name: /Change priority…/ }))
    await user.click(screen.getByRole('option', { name: /Urgent/ }))
    expect(onUpdateIssues).toHaveBeenCalledWith(['issue-1', 'issue-2'], { priority: 1 })
  })

  it('toggles a label on every selected issue and keeps the multi-select page open', async () => {
    const { user, onUpdateIssue, onOpenChange } = setup({ kind: 'issues', source: 'selection', issues: [first, second] })
    await user.click(screen.getByRole('option', { name: /Add labels…/ }))
    const feature = screen.getByRole('option', { name: /Feature/ })
    expect(feature.querySelector('[data-checked="mixed"]')).not.toBeNull()
    await user.click(feature)
    await waitFor(() => expect(onUpdateIssue).toHaveBeenCalledTimes(2))
    expect(onUpdateIssue).toHaveBeenCalledWith('issue-1', { labelIds: ['label-1'] })
    expect(onUpdateIssue).toHaveBeenCalledWith('issue-2', { labelIds: ['label-1'] })
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
    expect(screen.getByRole('option', { name: /Feature/ })).toBeInTheDocument()
  })

  it('goes back to the root page with Backspace on an empty query', async () => {
    const { user } = setup({ kind: 'issues', source: 'detail', issues: [first] })
    await user.click(screen.getByRole('option', { name: /Assign to…/ }))
    expect(screen.getByRole('option', { name: /Teammate/ })).toBeInTheDocument()
    await user.type(input(), 'te')
    await user.keyboard('{Backspace}{Backspace}')
    expect(screen.getByRole('option', { name: /Teammate/ })).toBeInTheDocument()
    await user.keyboard('{Backspace}')
    expect(screen.queryByRole('option', { name: /Teammate/ })).not.toBeInTheDocument()
    expect(screen.getByRole('option', { name: /Change status…/ })).toBeInTheDocument()
    expect(screen.getByLabelText('Command context')).not.toHaveTextContent('Assign to…')
  })

  it('filters actions by the query and still shows search results', async () => {
    api.searchWorkspace.mockResolvedValue({ results: [{ id: 'issue-9', type: 'issue', identifier: 'TST-9', title: 'Copy editing pass' }] })
    const { user } = setup({ kind: 'issues', source: 'detail', issues: [first] })
    await user.type(input(), 'copy')
    expect(screen.getByRole('option', { name: /Copy issue ID/ })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /Change status…/ })).not.toBeInTheDocument()
    expect(await screen.findByRole('option', { name: /Copy editing pass/ })).toBeInTheDocument()
    expect(api.searchWorkspace).toHaveBeenCalledWith('copy', [], 12, expect.any(AbortSignal))
  })

  it('prefers the list selection over an open issue', () => {
    setup({ kind: 'issues', source: 'detail', issues: [first] })
    render(<Register context={{ kind: 'issues', source: 'selection', issues: [first, second] }}/>)
    expect(screen.getByLabelText('Command context')).toHaveTextContent('2 issues')
  })

  it('offers project actions on a project page', async () => {
    const onUpdate = vi.fn().mockResolvedValue(project)
    const { user } = setup({ kind: 'project', project, onUpdate })
    expect(screen.getByLabelText('Command context')).toHaveTextContent('Project one')
    await user.click(screen.getByRole('option', { name: /Change project lead…/ }))
    await user.click(screen.getByRole('option', { name: /Teammate/ }))
    expect(onUpdate).toHaveBeenCalledWith({ leadId: 'user-2' })
  })
})
