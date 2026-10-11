import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, Project, ProjectStatus } from '@/types/flow'
import { createProjectStatus, deleteProjectStatus, updateProjectStatus } from '@/lib/api'
import { toast } from '@/lib/toast'
import { ProjectStatusesSection, ProjectStatusesSettings } from './project-statuses-settings'

vi.mock('@/lib/api', () => ({
  createProjectStatus: vi.fn(async () => ({})),
  updateProjectStatus: vi.fn(async () => ({})),
  deleteProjectStatus: vi.fn(async () => undefined),
  reorderProjectStatuses: vi.fn(async () => []),
}))
vi.mock('@/lib/toast', () => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }) }))

const STATUSES: ProjectStatus[] = [
  { id: 'ps_backlog', name: 'Backlog', color: '#F2994A', type: 'backlog', position: 0 },
  { id: 'ps_planned', name: 'Planned', color: '#D7D8DB', type: 'planned', position: 1 },
  { id: 'ps_review', name: 'Review', color: '#26b5ce', type: 'planned', position: 2, description: 'Waiting for sign-off' },
  { id: 'ps_progress', name: 'In Progress', color: '#F2C94C', type: 'started', position: 3 },
  { id: 'ps_completed', name: 'Completed', color: '#5E6AD2', type: 'completed', position: 4 },
  { id: 'ps_canceled', name: 'Canceled', color: '#8A8F98', type: 'canceled', position: 5 },
]

function data(): BootstrapData {
  const base = makeBootstrap()
  const project = { ...(base.projects[0] ?? {}), id: 'project-1', name: 'Launch', status: STATUSES[3] } as Project
  return { ...base, projectStatuses: STATUSES, projects: [project] }
}

function renderPage(onReload = vi.fn(async () => {})) {
  render(<I18nProvider><ProjectStatusesSettings data={data()} onReload={onReload} /></I18nProvider>)
  return onReload
}

function row(name: string) {
  return screen.getAllByRole('listitem').find(item => within(item).queryByText(name, { selector: '.pst-name' }))!
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.setItem('flow:locale', 'en-US')
})

describe('ProjectStatusesSettings', () => {
  it('lays out the page: header, one card, a header per status type and a row per status', () => {
    renderPage()
    expect(screen.getByRole('heading', { level: 1, name: 'Project statuses' })).toBeInTheDocument()
    expect(screen.getByText('Project statuses define the workflow that projects go through from start to completion')).toBeInTheDocument()
    const card = screen.getByRole('list', { name: 'Project statuses' })
    expect([...card.querySelectorAll('h3')].map(heading => heading.textContent)).toEqual(['Backlog', 'Planned', 'In Progress', 'Completed', 'Canceled'])
    expect(within(card).getAllByRole('button', { name: 'Create new project status' })).toHaveLength(5)
    expect(screen.getAllByRole('listitem').map(item => item.getAttribute('data-status-id'))).toEqual(STATUSES.map(status => status.id))
    expect(within(row('In Progress')).getByRole('link', { name: 'View projects' })).toHaveTextContent('1 project')
    expect(within(row('Review')).getByText('Waiting for sign-off')).toBeInTheDocument()
  })

  it('gives every status — even the only one of its type — a "…" menu with the reference app\'s Edit and Delete glyphs', async () => {
    const user = userEvent.setup()
    renderPage()
    for (const status of STATUSES) expect(within(row(status.name)).getByRole('button', { name: 'Open menu' })).toBeInTheDocument()
    await user.click(within(row('Backlog')).getByRole('button', { name: 'Open menu' }))
    const options = await screen.findAllByRole('menuitem')
    expect(options.map(option => option.textContent)).toEqual(['Edit', 'Delete'])
    expect(options.map(option => option.querySelector('svg')?.getAttribute('data-flow-glyph'))).toEqual(['edit', 'delete'])
    expect(options.map(option => option.querySelector('svg')?.getAttribute('data-menu-glyph'))).toEqual(['edit', 'delete'])
  })

  it('shows drag handles only where a type has more than one status', () => {
    renderPage()
    expect(row('Planned').querySelector('.pst-drag-handle')).not.toBeNull()
    expect(row('Review').querySelector('.pst-drag-handle')).not.toBeNull()
    expect(row('Backlog').querySelector('.pst-drag-handle')).toBeNull()
    expect(row('Backlog')).not.toHaveAttribute('draggable', 'true')
  })

  it('edits a status inline: name, description and a swatch colour', async () => {
    const user = userEvent.setup()
    const onReload = renderPage()
    await user.click(within(row('Backlog')).getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }))
    const name = screen.getByRole('textbox', { name: 'Name' })
    expect(name).toHaveValue('Backlog')
    await user.clear(name)
    await user.type(name, 'Ideas')
    await user.type(screen.getByRole('textbox', { name: 'Description' }), 'Not scheduled yet')
    await user.click(screen.getByRole('button', { name: 'Color' }))
    const swatches = await screen.findAllByRole('radio')
    expect(swatches.map(swatch => swatch.getAttribute('aria-label'))).toEqual(['Grey', 'Dark Grey', 'Purple', 'Teal', 'Green', 'Yellow', 'Orange', 'Pink', 'Red'])
    expect(screen.getByRole('radio', { name: 'Orange' })).toHaveAttribute('aria-checked', 'true')
    await user.click(screen.getByRole('radio', { name: 'Teal' }))
    expect(screen.getByRole('button', { name: 'Color' })).toHaveAttribute('data-color', '#26b5ce')
    await user.click(screen.getByRole('button', { name: 'Submit' }))
    await waitFor(() => expect(updateProjectStatus).toHaveBeenCalledWith('ps_backlog', {
      name: 'Ideas', description: 'Not scheduled yet', color: '#26b5ce', type: 'backlog',
    }))
    expect(onReload).toHaveBeenCalled()
    await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Name' })).not.toBeInTheDocument())
  })

  it('sets a custom hex colour from the custom picker', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(within(row('Completed')).getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }))
    await user.click(screen.getByRole('button', { name: 'Color' }))
    await user.click(await screen.findByRole('button', { name: 'Set custom color' }))
    const hex = screen.getByRole('textbox', { name: 'HEX' })
    await user.clear(hex)
    await user.type(hex, '#123abc{Enter}')
    expect(screen.getByRole('button', { name: 'Select default color' })).toHaveAttribute('aria-pressed', 'true')
    await user.click(screen.getByRole('button', { name: 'Submit' }))
    await waitFor(() => expect(updateProjectStatus).toHaveBeenCalledWith('ps_completed', expect.objectContaining({ color: '#123abc' })))
  })

  it('creates a status in the chosen type with that type\'s default colour', async () => {
    const user = userEvent.setup()
    renderPage()
    const canceled = screen.getByRole('list', { name: 'Project statuses' }).querySelector('[data-status-type="canceled"]') as HTMLElement
    await user.click(within(canceled).getByRole('button', { name: 'Create new project status' }))
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Abandoned')
    expect(screen.getByRole('button', { name: 'Submit' })).toHaveTextContent('Create')
    await user.click(screen.getByRole('button', { name: 'Submit' }))
    await waitFor(() => expect(createProjectStatus).toHaveBeenCalledWith({ name: 'Abandoned', description: '', color: '#8a8f98', type: 'canceled' }))
  })

  it('cancels an edit with Escape without saving', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(within(row('Planned')).getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }))
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('textbox', { name: 'Name' })).not.toBeInTheDocument()
    expect(updateProjectStatus).not.toHaveBeenCalled()
  })

  it('refuses to delete the last status of a type or a status projects use, and deletes otherwise', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(within(row('Backlog')).getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    expect(toast).toHaveBeenCalledWith("Can't delete status", { description: "You can't delete the last status of a type." })

    await user.click(within(row('Review')).getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    await waitFor(() => expect(deleteProjectStatus).toHaveBeenCalledWith('ps_review'))
    expect(deleteProjectStatus).toHaveBeenCalledTimes(1)
  })

  it('blocks deleting a status that projects still use', async () => {
    const user = userEvent.setup()
    const value = data()
    value.projectStatuses = [...STATUSES, { id: 'ps_qa', name: 'QA', color: '#4cb782', type: 'started', position: 6 }]
    render(<I18nProvider><ProjectStatusesSection data={value} onReload={vi.fn(async () => {})} /></I18nProvider>)
    await user.click(within(row('In Progress')).getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    expect(toast).toHaveBeenCalledWith('Can\'t delete the "In Progress" project status', {
      description: 'The status has 1 project assigned. Please archive or move them before deleting the status.',
    })
    expect(deleteProjectStatus).not.toHaveBeenCalled()
  })

  it('is read-only when disabled (members, inherited team statuses)', () => {
    render(<I18nProvider><ProjectStatusesSection data={data()} onReload={vi.fn(async () => {})} disabled /></I18nProvider>)
    expect(screen.queryByRole('button', { name: 'Open menu' })).not.toBeInTheDocument()
    for (const button of screen.getAllByRole('button', { name: 'Create new project status' })) expect(button).toBeDisabled()
    expect(document.querySelector('.pst-drag-handle')).toBeNull()
  })
})
