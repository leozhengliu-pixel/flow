import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { BootstrapData, ProjectTemplate } from '@/types/flow'

const api = vi.hoisted(() => ({
  createProjectTemplate: vi.fn(), updateProjectTemplate: vi.fn(), deleteProjectTemplate: vi.fn(),
  listIssueRecords: vi.fn(), listProjectRecords: vi.fn(), fetchIssueRecord: vi.fn(),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api, realtimeClientId: () => 'team-project-templates-test' }))
vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

import { makeBootstrap, viewer } from '@/test/fixtures'
import { Shell } from './mention-field-shell'
import { stubEditorDom } from './mention-field-test-kit'
import { TeamWorkflowSettings } from './team-workflow-settings'

const template = (over: Partial<ProjectTemplate> = {}) => ({ id: 'ptpl-1', name: 'Launch', priority: 0, labelIds: [], teamIds: [], memberIds: [], initiativeIds: [], milestones: [], issues: [], visibility: 'workspace', creator: viewer, createdAt: '2026-10-01T00:00:00.000Z', updatedAt: '', ...over }) as ProjectTemplate
const data = (projectTemplates: ProjectTemplate[] = []) => makeBootstrap({ projectTemplates, issueTemplates: [], documentTemplates: [], viewerRole: 'admin' } as Partial<BootstrapData>)
const projectSection = () => screen.getByRole('heading', { name: 'Project templates' }).closest('section') as HTMLElement

function page(value: BootstrapData, subPath?: string, onNavigate = vi.fn()) {
  render(<Shell data={value}><TeamWorkflowSettings data={value} team={value.teams[0]} section="templates" subPath={subPath} onNavigate={onNavigate} onReload={async () => {}}/></Shell>)
  return onNavigate
}

beforeEach(() => {
  for (const mock of Object.values(api)) mock.mockReset()
  api.createProjectTemplate.mockResolvedValue({})
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  stubEditorDom()
})

describe('team project templates', () => {
  it('shows the shared project template card with its empty state and routes "New template" to the full-page editor', async () => {
    const user = userEvent.setup()
    const onNavigate = page(data([template({ id: 'w', name: 'Workspace launch' })]))
    const section = projectSection()
    expect(section.querySelector('.pt-list-card')).toBeTruthy()
    expect(within(section).getByText('No project templates')).toBeInTheDocument()
    expect(within(section).queryByText('Workspace launch')).not.toBeInTheDocument()
    await user.click(within(section).getByRole('button', { name: 'Create new project template' }))
    expect(onNavigate).toHaveBeenCalledWith('templates', 'project/new')
  })

  it('lists the team\'s own project templates as rows that open their editor', async () => {
    const user = userEvent.setup()
    const value = data([template({ id: 'team-tpl', name: 'Team launch', visibility: 'teams', visibilityTeamId: 'team-1', teamIds: ['team-1'] })])
    const onNavigate = page(value)
    const section = projectSection()
    expect(within(section).getByText('1 project template')).toBeInTheDocument()
    expect(within(section).getByText('Team launch')).toBeInTheDocument()
    expect(within(section).getByText(viewer.displayName)).toBeInTheDocument()
    await user.click(within(section).getByRole('button', { name: 'Team launch' }))
    expect(onNavigate).toHaveBeenCalledWith('templates', 'project/team-tpl/edit')
    expect(screen.getByText('Default project template')).toBeInTheDocument()
  })

  it('creates a team template with the team preset as its team and visibility', async () => {
    const user = userEvent.setup()
    const value = data()
    page(value, 'project/new')
    expect(screen.getByRole('heading', { name: 'New project template' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Team templates' })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Change project teams' })).toHaveTextContent(value.teams[0].name)
    expect(screen.getByRole('button', { name: 'Template visibility' })).toHaveTextContent(value.teams[0].name)
    await user.type(screen.getByPlaceholderText('Add a descriptive name…'), 'Team launch')
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.createProjectTemplate).toHaveBeenCalledWith(expect.objectContaining({ name: 'Team launch', teamIds: ['team-1'], visibility: 'teams', visibilityTeamId: 'team-1' })))
  })

  it('duplicates a team template into a new draft', () => {
    page(data([template({ id: 'team-tpl', name: 'Team launch', visibility: 'teams', visibilityTeamId: 'team-1', teamIds: ['team-1'] })]), 'project/team-tpl/duplicate')
    expect(screen.getByRole('heading', { name: 'New project template' })).toBeInTheDocument()
    expect(screen.getByPlaceholderText('Add a descriptive name…')).toHaveValue('Copy of Team launch')
  })
})
