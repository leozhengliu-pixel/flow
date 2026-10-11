import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { BootstrapData, ProjectTemplate } from '@/types/flow'

const api = vi.hoisted(() => ({
  createProjectTemplate: vi.fn(),
  updateProjectTemplate: vi.fn(),
  deleteProjectTemplate: vi.fn(),
  listIssueRecords: vi.fn(),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api, realtimeClientId: () => 'project-template-test' }))
const dialogs = vi.hoisted(() => ({ confirmAction: vi.fn() }))
vi.mock('@/components/ui/action-dialog-service', async importOriginal => ({ ...(await importOriginal<typeof import('@/components/ui/action-dialog-service')>()), ...dialogs }))

import { mentionFixture } from '@/components/editor/mentions/mention-fixtures'
import { Shell } from './mention-field-shell'
import { stubEditorDom } from './mention-field-test-kit'
import { ProjectTemplateSettings } from './project-template-settings'

const noop = vi.fn()

function projectTemplate(overrides: Partial<ProjectTemplate> = {}) {
  return { id: 'ptemplate-1', name: 'Launch', projectName: 'Launch project', description: '', summary: '', priority: 0, labelIds: [], memberIds: [], teamIds: [], initiativeIds: [], dependencyIds: [], milestones: [], issues: [], visibility: 'workspace', createdAt: new Date().toISOString(), updatedAt: '', creator: { id: 'user-1' }, ...overrides } as unknown as ProjectTemplate
}

function fixture(templates: ProjectTemplate[] = []) {
  return { ...mentionFixture(), projectTemplates: templates } as BootstrapData
}

function settings(data: BootstrapData, mode?: 'new' | 'edit', templateId?: string, onReload = async () => {}) {
  return <Shell data={data}><ProjectTemplateSettings data={data} mode={mode} templateId={templateId} onCreate={noop} onOpen={noop} onDuplicate={noop} onNavigateList={noop} onReload={onReload}/></Shell>
}

beforeEach(() => {
  for (const mock of Object.values(api)) mock.mockReset()
  api.createProjectTemplate.mockResolvedValue({})
  api.updateProjectTemplate.mockResolvedValue({})
  api.deleteProjectTemplate.mockResolvedValue(undefined)
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  dialogs.confirmAction.mockReset()
  dialogs.confirmAction.mockResolvedValue(true)
  noop.mockReset()
  stubEditorDom()
})

describe('project template editor', () => {
  it('lays out the template like the reference editor: name, project card, property pills, milestones and footer', async () => {
    render(settings(fixture(), 'new'))
    expect(screen.getByRole('heading', { name: 'New project template' })).toBeInTheDocument()
    expect(screen.getByPlaceholderText('Add a descriptive name…')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Choose icon' })).toBeInTheDocument()
    expect(screen.getByPlaceholderText('Project name')).toBeInTheDocument()
    expect(screen.getByPlaceholderText('Add a short summary…')).toBeInTheDocument()
    const pills = document.querySelector('.pt-pills') as HTMLElement
    expect([...pills.querySelectorAll('button')].map(button => button.getAttribute('aria-label'))).toEqual([
      'Change project status', 'Change project priority', 'Set project lead', 'Change project members', 'Change project teams',
      'Change project initiatives', 'Change labels', 'Add dependencies', 'Add issue',
    ])
    expect(within(pills).getByText('No priority')).toBeInTheDocument()
    expect(await screen.findByRole('textbox', { name: 'Project description' })).toBeInTheDocument()
    expect(screen.getByText('Milestones')).toBeInTheDocument()
    const footer = document.querySelector('.pt-footer') as HTMLElement
    expect(within(footer).getByText('Visibility')).toBeInTheDocument()
    expect(within(footer).getByRole('button', { name: 'Template visibility' })).toHaveTextContent('Workspace')
    expect(within(footer).getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
    expect(within(footer).getByRole('button', { name: 'Create' })).toBeInTheDocument()
  })

  it('adds, edits and removes milestones in the milestone composer', async () => {
    const user = userEvent.setup()
    render(settings(fixture(), 'new'))
    await user.click(screen.getByRole('button', { name: 'Add' }))
    expect(screen.getByText('Create milestone')).toBeInTheDocument()
    await user.type(screen.getByRole('textbox', { name: 'Milestone name' }), 'Alpha milestone')
    await user.click(screen.getByRole('button', { name: 'Add milestone' }))
    const section = document.querySelector('.pt-section') as HTMLElement
    expect(within(section).getByText('Alpha milestone')).toBeInTheDocument()
    expect(within(section).getByText('1')).toBeInTheDocument()
    await user.click(within(section).getByText('Alpha milestone'))
    expect(screen.getByText('Edit milestone')).toBeInTheDocument()
    const name = screen.getByRole('textbox', { name: 'Milestone name' })
    await user.clear(name)
    await user.type(name, 'Beta milestone')
    await user.click(screen.getByRole('button', { name: 'Save milestone' }))
    expect(screen.getByText('Beta milestone')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Remove milestone' }))
    expect(screen.queryByText('Beta milestone')).not.toBeInTheDocument()
  })

  it('creates template issues from the Issues pill, with create-more', async () => {
    const user = userEvent.setup()
    render(settings(fixture(), 'new'))
    await user.click(screen.getByRole('button', { name: 'Add issue' }))
    expect(screen.getByText('Create issue')).toBeInTheDocument()
    await user.click(screen.getByRole('switch'))
    await user.type(screen.getByRole('textbox', { name: 'Issue title' }), 'Kickoff{Enter}')
    // Create more keeps the composer open with an empty title.
    expect(screen.getByRole('textbox', { name: 'Issue title' })).toHaveValue('')
    await user.type(screen.getByRole('textbox', { name: 'Issue title' }), 'Retro{Enter}')
    await user.click(screen.getByRole('button', { name: 'Discard changes' }))
    const issues = document.querySelector('.pt-issues') as HTMLElement
    expect(within(issues).getByText('Kickoff')).toBeInTheDocument()
    expect(within(issues).getByText('Retro')).toBeInTheDocument()
    expect(within(document.querySelector('.pt-pills') as HTMLElement).getByRole('button', { name: 'Add issue' })).toHaveTextContent('2 issues')
  })

  it('saves every preset, milestones, issues, dependencies and the team visibility', async () => {
    const user = userEvent.setup()
    const onReload = vi.fn(async () => {})
    render(settings(fixture(), 'new', undefined, onReload))
    await user.type(screen.getByPlaceholderText('Add a descriptive name…'), 'QA template')
    await user.type(screen.getByPlaceholderText('Project name'), 'QA project')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    await user.type(screen.getByRole('textbox', { name: 'Milestone name' }), 'Alpha')
    await user.click(screen.getByRole('button', { name: 'Add milestone' }))
    await user.click(screen.getByRole('button', { name: 'Add issue' }))
    await user.type(screen.getByRole('textbox', { name: 'Issue title' }), 'Kickoff{Enter}')
    await user.click(screen.getByRole('button', { name: 'Template visibility' }))
    await user.click(await screen.findByRole('menuitem', { name: /Test team/ }))
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.createProjectTemplate).toHaveBeenCalled())
    const input = api.createProjectTemplate.mock.calls[0][0]
    expect(input).toEqual(expect.objectContaining({
      name: 'QA template',
      projectName: 'QA project',
      priority: 0,
      milestones: [expect.objectContaining({ name: 'Alpha' })],
      issues: [expect.objectContaining({ title: 'Kickoff', priority: 0, labelIds: [] })],
      dependencyRelations: [],
      visibility: 'teams',
      visibilityTeamId: 'team-1',
    }))
    expect(onReload).toHaveBeenCalled()
  })

  it('asks for a name instead of saving an unnamed template', async () => {
    const user = userEvent.setup()
    render(settings(fixture(), 'new'))
    await user.click(screen.getByRole('button', { name: 'Create' }))
    expect(api.createProjectTemplate).not.toHaveBeenCalled()
    expect(screen.getByPlaceholderText('Add a descriptive name…')).toHaveFocus()
  })

  it('opens a saved template with its legacy dependencies, milestones and issues', () => {
    const template = projectTemplate({
      dependencyIds: ['project-1'],
      milestones: [{ id: 'm-1', name: 'Alpha' }],
      issues: [{ id: 'i-1', title: 'Kickoff', priority: 0, labelIds: [], milestoneId: 'm-1' }, { id: 'i-2', title: 'Child', priority: 0, labelIds: [], parentId: 'i-1' }],
    })
    render(settings(fixture([template]), 'edit', template.id))
    expect(screen.getByRole('heading', { name: 'Edit project template' })).toBeInTheDocument()
    expect(screen.getByPlaceholderText('Add a descriptive name…')).toHaveValue('Launch')
    expect(screen.getByRole('button', { name: 'Add dependencies' })).toHaveTextContent('1 dependency')
    expect(within(document.querySelector('.pt-pills') as HTMLElement).getByRole('button', { name: 'Add issue' })).toHaveTextContent('2 issues')
    const rows = [...document.querySelectorAll('.pt-issue-row')] as HTMLElement[]
    expect(rows.map(row => row.querySelector('.pt-issue-title')?.textContent)).toEqual(['Kickoff', 'Child'])
    expect(rows[1].style.paddingLeft).toBe('40px')
    expect(screen.getByRole('button', { name: 'Save' })).toBeInTheDocument()
  })

  it('confirms before discarding unsaved changes', async () => {
    const user = userEvent.setup()
    dialogs.confirmAction.mockResolvedValue(false)
    render(settings(fixture(), 'new'))
    await user.type(screen.getByPlaceholderText('Add a descriptive name…'), 'Draft')
    await user.click(within(document.querySelector('.pt-footer') as HTMLElement).getByRole('button', { name: 'Cancel' }))
    expect(dialogs.confirmAction).toHaveBeenCalledWith('Discard changes?', expect.objectContaining({ description: 'Confirm that you want to discard your changes.', confirmLabel: 'Discard', danger: true }))
    expect(noop).not.toHaveBeenCalled()
  })
})

describe('project template list', () => {
  it('shows the empty state and the New template action', async () => {
    const user = userEvent.setup()
    render(settings(fixture()))
    expect(screen.getByRole('heading', { name: 'Project templates' })).toBeInTheDocument()
    expect(screen.getByText('No project templates')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Create new project template' }))
    expect(noop).toHaveBeenCalled()
  })

  it('lists workspace templates and deletes one after confirming', async () => {
    const user = userEvent.setup()
    const onReload = vi.fn(async () => {})
    render(settings(fixture([projectTemplate(), projectTemplate({ id: 'team-only', name: 'Team only', visibility: 'teams', visibilityTeamId: 'team-1' })]), undefined, undefined, onReload))
    expect(screen.getByText('1 project template')).toBeInTheDocument()
    expect(screen.getByText('Launch')).toBeInTheDocument()
    expect(screen.queryByText('Team only')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: /Delete/ }))
    await waitFor(() => expect(api.deleteProjectTemplate).toHaveBeenCalledWith('ptemplate-1'))
    expect(dialogs.confirmAction).toHaveBeenCalledWith('Delete the template "Launch"?', expect.objectContaining({ danger: true, confirmLabel: 'Delete' }))
    expect(onReload).toHaveBeenCalled()
  })
})
