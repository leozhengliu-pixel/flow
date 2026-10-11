import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { BootstrapData, IssueTemplate } from '@/types/flow'

const api = vi.hoisted(() => ({
  createWorkspaceIssueTemplate: vi.fn(), updateWorkspaceIssueTemplate: vi.fn(), deleteWorkspaceIssueTemplate: vi.fn(),
  createIssueTemplate: vi.fn(), updateIssueTemplate: vi.fn(), deleteIssueTemplate: vi.fn(),
  listIssueRecords: vi.fn(), listProjectRecords: vi.fn(), fetchIssueRecord: vi.fn(),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api, realtimeClientId: () => 'issue-template-settings-test' }))
const confirm = vi.hoisted(() => ({ confirmTemplateAction: vi.fn() }))
vi.mock('./template-confirm', () => confirm)
vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

import { makeBootstrap, viewer } from '@/test/fixtures'
import { IssueTemplateEditor, IssueTemplateSettings, IssueTemplateTypeDialog } from './issue-template-settings'
import { Shell } from './mention-field-shell'
import { stubEditorDom } from './mention-field-test-kit'
import { TeamWorkflowSettings } from './team-workflow-settings'

const noop = vi.fn()
const template = (over: Partial<IssueTemplate> = {}) => ({ id: 'tpl-1', name: 'Bug report', priority: 0, labelIds: [], creator: viewer, createdAt: '2026-10-01T00:00:00.000Z', updatedAt: '', scope: 'workspace', templateType: 'standard', ...over }) as IssueTemplate
const data = (issueTemplates: IssueTemplate[] = []) => makeBootstrap({ issueTemplates, viewerRole: 'admin' } as Partial<BootstrapData>)

function editor(props: Partial<React.ComponentProps<typeof IssueTemplateEditor>> = {}, value = data()) {
  const handlers = { onClose: vi.fn(), onReload: vi.fn().mockResolvedValue(undefined) }
  render(<Shell data={value}><IssueTemplateEditor data={value} templateType="standard" backLabel="Issue templates" {...handlers} {...props}/></Shell>)
  return handlers
}

beforeEach(() => {
  localStorage.setItem('flow:locale', 'en-US')
  for (const mock of Object.values(api)) mock.mockReset()
  for (const mock of [api.createWorkspaceIssueTemplate, api.updateWorkspaceIssueTemplate, api.createIssueTemplate, api.updateIssueTemplate]) mock.mockResolvedValue({})
  api.deleteWorkspaceIssueTemplate.mockResolvedValue(undefined)
  api.deleteIssueTemplate.mockResolvedValue(undefined)
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  confirm.confirmTemplateAction.mockReset()
  noop.mockReset()
  stubEditorDom()
})
afterEach(() => { vi.unstubAllGlobals() })

describe('workspace issue template list', () => {
  it('shows the reference app\'s empty card and lists only workspace templates', async () => {
    const user = userEvent.setup()
    const onCreate = vi.fn()
    const value = data([template({ id: 'w', name: 'Workspace bug' }), template({ id: 't', name: 'Team only', scope: undefined, teamId: 'team-1' })])
    render(<Shell data={value}><IssueTemplateSettings data={value} onCreate={onCreate} onOpen={noop} onDuplicate={noop} onNavigateList={noop} onReload={async () => {}}/></Shell>)
    expect(screen.getByRole('heading', { name: 'Issue templates' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Docs' })).toHaveAttribute('target', '_blank')
    expect(screen.getByText('Workspace bug')).toBeInTheDocument()
    expect(screen.queryByText('Team only')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Create new issue template' }))
    const dialog = await screen.findByRole('dialog', { name: 'Choose issue template type' })
    await user.click(within(dialog).getByRole('button', { name: /Custom Form/ }))
    expect(onCreate).toHaveBeenCalledWith(true)
  })

  it('says there are no templates', () => {
    const value = data()
    render(<Shell data={value}><IssueTemplateSettings data={value} onCreate={noop} onOpen={noop} onDuplicate={noop} onNavigateList={noop} onReload={async () => {}}/></Shell>)
    expect(screen.getByText('No issue templates')).toBeInTheDocument()
  })

  it('deletes a template from its row menu after confirming', async () => {
    const user = userEvent.setup()
    confirm.confirmTemplateAction.mockResolvedValue(true)
    const onReload = vi.fn().mockResolvedValue(undefined)
    const value = data([template()])
    render(<Shell data={value}><IssueTemplateSettings data={value} onCreate={noop} onOpen={noop} onDuplicate={noop} onNavigateList={noop} onReload={onReload}/></Shell>)
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    await waitFor(() => expect(api.deleteWorkspaceIssueTemplate).toHaveBeenCalledWith('tpl-1'))
    expect(onReload).toHaveBeenCalled()
  })
})

describe('type chooser', () => {
  it('previews a standard and a custom form template', async () => {
    const user = userEvent.setup()
    const onChoose = vi.fn()
    render(<Shell data={data()}><IssueTemplateTypeDialog open onOpenChange={noop} onChoose={onChoose}/></Shell>)
    expect(screen.getByText('Basic template with title, description, and default attributes')).toBeInTheDocument()
    expect(screen.getByText('Repro steps')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Standard/ }))
    expect(onChoose).toHaveBeenCalledWith(false)
  })
})

describe('issue template editor', () => {
  it('lays out the name, description, issue card and default property pills', async () => {
    editor()
    expect(screen.getByRole('button', { name: 'Issue templates' })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Template name' })).toHaveAttribute('placeholder', 'Template name')
    expect(screen.getByRole('textbox', { name: 'Template description' })).toHaveAttribute('placeholder', 'Add an optional template description…')
    expect(screen.getByRole('textbox', { name: 'Issue title' })).toBeInTheDocument()
    expect(await screen.findByRole('textbox', { name: 'Issue description' })).toBeInTheDocument()
    expect(screen.getByText('Automatically applied upon issue creation, and editable when composing within Flow')).toBeInTheDocument()
    for (const name of ['Set team', 'Change priority. No priority is selected', 'Change assignee. Currently no one is assigned.', 'Change project', 'Change labels']) expect(screen.getByRole('combobox', { name })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Open menu' })).toBeInTheDocument()
    // Status appears once a team is set.
    expect(screen.queryByRole('combobox', { name: 'Change status' })).not.toBeInTheDocument()
  })

  it('sets the team, presets its default status and creates a workspace template', async () => {
    const user = userEvent.setup()
    const { onClose, onReload } = editor()
    await user.type(screen.getByRole('textbox', { name: 'Template name' }), 'Bug report')
    await user.type(screen.getByRole('textbox', { name: 'Issue title' }), 'Bug: ')
    await user.click(screen.getByRole('combobox', { name: 'Set team' }))
    await user.click(await screen.findByRole('option', { name: /Test team/ }))
    expect(await screen.findByRole('combobox', { name: 'Change status' })).toHaveTextContent('Backlog')
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.createWorkspaceIssueTemplate).toHaveBeenCalled())
    expect(api.createWorkspaceIssueTemplate.mock.calls[0][0]).toMatchObject({ name: 'Bug report', title: 'Bug: ', teamId: 'team-1', stateId: 'state-backlog', templateType: 'standard', visibilityTeamId: '' })
    await waitFor(() => expect(onReload).toHaveBeenCalled())
    expect(onClose).toHaveBeenCalled()
  })

  it('creates a team template through the team with the team and status preset', async () => {
    const user = userEvent.setup()
    editor({ scopeTeamId: 'team-1', backLabel: 'Team templates' })
    expect(screen.getByRole('button', { name: 'Team templates' })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Set team' })).toHaveTextContent('Test team')
    expect(screen.getByRole('combobox', { name: 'Change status' })).toHaveTextContent('Backlog')
    await user.type(screen.getByRole('textbox', { name: 'Template name' }), 'Team bug')
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.createIssueTemplate).toHaveBeenCalledWith('team-1', expect.objectContaining({ name: 'Team bug', teamId: 'team-1', stateId: 'state-backlog' })))
    expect(api.createIssueTemplate.mock.calls[0][1]).not.toHaveProperty('visibilityTeamId')
    expect(api.createWorkspaceIssueTemplate).not.toHaveBeenCalled()
  })

  it('does not save without a name and focuses the name', async () => {
    const user = userEvent.setup()
    editor()
    await user.click(screen.getByRole('button', { name: 'Create' }))
    expect(api.createWorkspaceIssueTemplate).not.toHaveBeenCalled()
    expect(screen.getByRole('textbox', { name: 'Template name' })).toHaveFocus()
  })

  it('adds sub-issues from the "…" menu and keeps the composer open with Create more', async () => {
    const user = userEvent.setup()
    editor()
    await user.type(screen.getByRole('textbox', { name: 'Template name' }), 'Launch')
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: /Add sub-issue/ }))
    const composer = screen.getByRole('region', { name: 'Create sub-issue' })
    await user.click(within(composer).getByRole('checkbox', { name: 'Create more' }))
    await user.type(within(composer).getByRole('textbox', { name: 'Issue title' }), 'Write docs')
    await user.click(within(composer).getByRole('button', { name: 'Add sub-issue' }))
    expect(screen.getByText('Write docs')).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Create sub-issue' })).toBeInTheDocument()
    await user.click(within(screen.getByRole('region', { name: 'Create sub-issue' })).getByRole('button', { name: 'Discard changes' }))
    expect(screen.queryByRole('region', { name: 'Create sub-issue' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.createWorkspaceIssueTemplate).toHaveBeenCalled())
    expect(api.createWorkspaceIssueTemplate.mock.calls[0][0].subIssues).toEqual([expect.objectContaining({ title: 'Write docs', priority: 0 })])
  })

  it('builds a form template from Add field and saves its fields', async () => {
    const user = userEvent.setup()
    editor({ templateType: 'customForm' })
    expect(screen.getByText('No fields added yet')).toBeInTheDocument()
    expect(screen.getByText('Automatically applied upon issue creation')).toBeInTheDocument()
    await user.type(screen.getByRole('textbox', { name: 'Template name' }), 'Bug form')
    await user.click(screen.getByRole('button', { name: 'Add field' }))
    await user.click(await screen.findByRole('option', { name: /^Text/ }))
    await user.type(screen.getByRole('textbox', { name: 'Field label' }), 'Repro steps')
    await user.click(screen.getByRole('checkbox', { name: 'Required' }))
    await user.click(screen.getByRole('button', { name: 'Add field' }))
    await user.click(await screen.findByRole('option', { name: /Due date/ }))
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.createWorkspaceIssueTemplate).toHaveBeenCalled())
    const input = api.createWorkspaceIssueTemplate.mock.calls[0][0]
    expect(input.templateType).toBe('customForm')
    expect(input.formFields).toEqual([
      expect.objectContaining({ type: 'text', label: 'Repro steps', required: true }),
      expect.objectContaining({ type: 'dueDate', label: 'Due date', required: false }),
    ])
  })

  it('asks before discarding changes and keeps the editor open when cancelled', async () => {
    const user = userEvent.setup()
    confirm.confirmTemplateAction.mockResolvedValue(false)
    const { onClose } = editor()
    await user.type(screen.getByRole('textbox', { name: 'Template name' }), 'Draft')
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(confirm.confirmTemplateAction).toHaveBeenCalledWith(expect.objectContaining({ title: 'Discard changes?', description: 'Confirm that you want to discard your changes.', confirmLabel: 'Discard' }))
    expect(onClose).not.toHaveBeenCalled()
  })

  it('saves an edited team template through its team', async () => {
    const user = userEvent.setup()
    const saved = template({ id: 'team-tpl', scope: undefined, teamId: 'team-1', name: 'Old' })
    editor({ template: saved, scopeTeamId: 'team-1' }, data([saved]))
    await user.clear(screen.getByRole('textbox', { name: 'Template name' }))
    await user.type(screen.getByRole('textbox', { name: 'Template name' }), 'New')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.updateIssueTemplate).toHaveBeenCalledWith('team-1', 'team-tpl', expect.objectContaining({ name: 'New' })))
  })
})

describe('team templates page', () => {
  const page = (value: BootstrapData, subPath?: string, onNavigate = vi.fn()) => render(<Shell data={value}><TeamWorkflowSettings data={value} team={value.teams[0]} section="templates" subPath={subPath} onNavigate={onNavigate} onReload={async () => {}}/></Shell>)

  it('lists the team\'s own issue and document templates with links to the workspace sections', () => {
    const value = { ...data([template({ id: 'w', name: 'Workspace bug' }), template({ id: 't', name: 'Team bug', scope: undefined, teamId: 'team-1' })]), documentTemplates: [], projectTemplates: [] } as BootstrapData
    page(value)
    expect(screen.getByRole('heading', { name: 'Team templates' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'issue' })).toHaveAttribute('href', '/workspace/settings/issue-templates')
    expect(screen.getByRole('link', { name: 'document' })).toHaveAttribute('href', '/workspace/settings/documents')
    expect(screen.getByText('Team bug')).toBeInTheDocument()
    expect(screen.queryByText('Workspace bug')).not.toBeInTheDocument()
    expect(screen.getByText('No document templates')).toBeInTheDocument()
  })

  it('opens the type chooser and routes to the full-page editor', async () => {
    const user = userEvent.setup()
    const onNavigate = vi.fn()
    page({ ...data(), documentTemplates: [], projectTemplates: [] } as BootstrapData, undefined, onNavigate)
    await user.click(screen.getByRole('button', { name: 'Create new issue template' }))
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: /Standard/ }))
    expect(onNavigate).toHaveBeenCalledWith('templates', 'issue/new')
  })

  it('renders the team issue template editor as its own page', async () => {
    page({ ...data(), documentTemplates: [], projectTemplates: [] } as BootstrapData, 'issue/new/form')
    expect(await screen.findByText('No fields added yet')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Team templates' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Team templates' })).not.toBeInTheDocument()
  })
})
