import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { BootstrapData, IssueTemplate, ProjectTemplate } from '@/types/flow'

const api = vi.hoisted(() => ({
  fetchIssueRecord: vi.fn(),
  listProjectRecords: vi.fn(),
  listIssueRecords: vi.fn(),
  createWorkspaceIssueTemplate: vi.fn(),
  updateWorkspaceIssueTemplate: vi.fn(),
  createIssueTemplate: vi.fn(),
  createProjectTemplate: vi.fn(),
  updateProjectTemplate: vi.fn(),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api, realtimeClientId: () => 'template-test' }))

import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
import { TemplateEditor, TemplateSettings } from './issues-projects-settings'
import { Shell } from './mention-field-shell'
import { documentMarkdown, paste, pickDocument, stubEditorDom } from './mention-field-test-kit'

const docChip = () => document.querySelector('a[data-agent-entity="document"]')
const noop = vi.fn()

function issueTemplate(overrides: Partial<IssueTemplate> = {}) {
  return { id: 'template-1', name: 'Bug report', title: 'Bug', body: '', scope: 'workspace', priority: 0, labelIds: [], formFields: [], subIssues: [], templateType: 'standard', ...overrides } as unknown as IssueTemplate
}
function projectTemplate(overrides: Partial<ProjectTemplate> = {}) {
  return { id: 'ptemplate-1', name: 'Launch', projectName: 'Launch', description: '', summary: '', priority: 0, labelIds: [], memberIds: [], teamIds: [], initiativeIds: [], dependencyIds: [], issueIds: [], milestones: [], visibility: 'workspace', ...overrides } as unknown as ProjectTemplate
}
const withTemplates = (data: BootstrapData) => ({ ...data, projectTemplates: [] }) as BootstrapData
function issueSettings(data: BootstrapData, mode: 'new' | 'edit', templateId?: string) {
  return <Shell data={data}><TemplateSettings data={data} type="issue" mode={mode} templateId={templateId} onNavigateList={noop} onReload={async () => {}} onCreateIssue={noop} onOpenIssue={noop} onDuplicateIssue={noop}/></Shell>
}
function projectSettings(data: BootstrapData, mode: 'new' | 'edit', templateId?: string) {
  return <Shell data={data}><TemplateSettings data={data} type="project" mode={mode} templateId={templateId} onNavigateList={noop} onReload={async () => {}} onCreateProject={noop} onOpenProject={noop} onDuplicateProject={noop}/></Shell>
}

beforeEach(() => {
  for (const mock of Object.values(api)) mock.mockReset()
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  for (const mock of [api.createWorkspaceIssueTemplate, api.updateWorkspaceIssueTemplate, api.createIssueTemplate, api.createProjectTemplate, api.updateProjectTemplate]) mock.mockResolvedValue({})
  noop.mockReset()
  resetAgentRecordCache()
  stubEditorDom()
})
afterEach(() => { vi.unstubAllGlobals() })

describe('issue template body', () => {
  it('saves a picked resource as [Label](path) and shows its chip again when the template is reopened', async () => {
    const user = userEvent.setup()
    const data = withTemplates(mentionFixture())
    const first = render(issueSettings(data, 'new'))
    await user.type(screen.getByRole('textbox', { name: 'Template name' }), 'Bug report')
    await pickDocument(user, await screen.findByRole('textbox', { name: 'Issue description' }))
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.createWorkspaceIssueTemplate).toHaveBeenCalled())
    const saved = api.createWorkspaceIssueTemplate.mock.calls[0][0].body as string
    expect(saved).toContain(documentMarkdown)
    first.unmount()
    render(issueSettings({ ...data, issueTemplates: [issueTemplate({ body: saved })] }, 'edit', 'template-1'))
    await waitFor(() => expect(docChip()).toHaveTextContent('Launch plan'))
  })

  it('turns a pasted Flow URL into a chip that is saved as a link', async () => {
    const user = userEvent.setup()
    render(issueSettings(withTemplates(mentionFixture()), 'new'))
    await user.type(screen.getByRole('textbox', { name: 'Template name' }), 'Bug report')
    const box = await screen.findByRole('textbox', { name: 'Issue description' })
    await user.click(box)
    paste(box, `${window.location.origin}${mentionUrls.document}`)
    await waitFor(() => expect(docChip()).toHaveTextContent('Launch plan'))
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.createWorkspaceIssueTemplate).toHaveBeenCalledWith(expect.objectContaining({ body: documentMarkdown })))
  })

  it('offers mentions in the sub-issue composer and when editing a saved sub-issue row', async () => {
    const user = userEvent.setup()
    const data = withTemplates(mentionFixture())
    render(issueSettings(data, 'new'))
    await user.type(screen.getByRole('textbox', { name: 'Template name' }), 'Bug report')
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('option', { name: /Add sub-issue/ }))
    const composer = screen.getByText('Create sub-issue').closest('section') as HTMLElement
    await user.type(within(composer).getByRole('textbox', { name: 'Issue title' }), 'Follow up')
    await pickDocument(user, within(composer).getByRole('textbox', { name: 'Issue description' }))
    await user.click(within(composer).getByRole('button', { name: 'Add sub-issue' }))
    expect(screen.getByText('Follow up')).toBeInTheDocument()
    expect(screen.getByText(/See Launch plan/)).toBeInTheDocument()
    // Editing the saved row opens the same field; a pasted URL becomes a chip and Cmd/Ctrl+Enter saves the row.
    await user.click(screen.getByRole('button', { name: 'Edit sub-issue' }))
    const editing = document.querySelector('.it-subissue-row.is-editing') as HTMLElement
    const row = within(editing).getByRole('textbox', { name: 'Issue description' })
    await waitFor(() => expect(editing.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan'))
    await user.click(row)
    await user.keyboard('{Control>}{Enter}{/Control}')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Edit sub-issue' })).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.createWorkspaceIssueTemplate).toHaveBeenCalled())
    const sub = api.createWorkspaceIssueTemplate.mock.calls[0][0].subIssues[0]
    expect(sub.description).toContain(documentMarkdown)
  })
})

describe('project template description and milestones', () => {
  it('saves the description with its mention, shows the chip when reopened, and takes a pasted URL', async () => {
    const user = userEvent.setup()
    const data = withTemplates(mentionFixture())
    const first = render(projectSettings(data, 'new'))
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Launch')
    const box = await screen.findByRole('textbox', { name: 'Project description' })
    await pickDocument(user, box)
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.createProjectTemplate).toHaveBeenCalled())
    const saved = api.createProjectTemplate.mock.calls[0][0].description as string
    expect(saved).toContain(documentMarkdown)
    first.unmount()
    render(projectSettings({ ...data, projectTemplates: [projectTemplate({ description: saved })] } as BootstrapData, 'edit', 'ptemplate-1'))
    await waitFor(() => expect(docChip()).toHaveTextContent('Launch plan'))
  })

  it('pastes a Flow URL into the milestone description template', async () => {
    const user = userEvent.setup()
    render(projectSettings(withTemplates(mentionFixture()), 'new'))
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Launch')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    await user.type(await screen.findByRole('textbox', { name: 'Milestone name' }), 'Beta')
    const box = screen.getByRole('textbox', { name: 'Milestone description template' })
    await user.click(box)
    paste(box, `${window.location.origin}${mentionUrls.document}`)
    await waitFor(() => expect(docChip()).toHaveTextContent('Launch plan'))
    await user.click(screen.getByRole('button', { name: 'Add milestone' }))
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.createProjectTemplate).toHaveBeenCalled())
    expect(api.createProjectTemplate.mock.calls[0][0].milestones[0]).toMatchObject({ name: 'Beta', description: documentMarkdown })
  })
})

describe('team template editor', () => {
  it('saves an issue template body with a mention', async () => {
    const user = userEvent.setup()
    const data = withTemplates(mentionFixture())
    render(<Shell data={data}><TemplateEditor data={data} type="issue" teamId="team-1" template={null} onClose={noop} onSaved={async () => {}}/></Shell>)
    await user.type(screen.getByRole('textbox', { name: 'Template name' }), 'Team bug')
    await pickDocument(user, screen.getByRole('textbox', { name: 'Issue description' }))
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.createIssueTemplate).toHaveBeenCalled())
    expect(api.createIssueTemplate.mock.calls[0][1].body).toContain(documentMarkdown)
  })

  it('saves a project template description and a milestone description picked with the keyboard (modal dialog)', async () => {
    const user = userEvent.setup()
    const data = withTemplates(mentionFixture())
    render(<Shell data={data}><TemplateEditor data={data} type="project" template={null} onClose={noop} onSaved={async () => {}}/></Shell>)
    await user.type(screen.getByRole('textbox', { name: 'Template name' }), 'Team launch')
    await pickDocument(user, screen.getByRole('textbox', { name: 'Project description' }))
    await user.click(screen.getByRole('button', { name: 'Add' }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByRole('textbox', { name: 'Milestone name' }), 'Beta')
    await pickDocument(user, within(dialog).getByRole('textbox', { name: 'Milestone description template' }), { keyboard: true })
    await user.click(within(dialog).getByRole('button', { name: 'Add milestone' }))
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.createProjectTemplate).toHaveBeenCalled())
    const input = api.createProjectTemplate.mock.calls[0][0]
    expect(input.description).toContain(documentMarkdown)
    expect(input.milestones[0].description).toContain(documentMarkdown)
  })
})
