import type { ComponentProps, ReactNode } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, project } from '@/test/fixtures'

vi.mock('@/components/property/property-menu', () => ({
  PropertyMenu: ({ label, onChange, options, selectedId, value }: { label: string; value: string; selectedId?: string; options: Array<{ id: string }>; onChange: (id: string) => void }) => <button aria-label={`Change ${label}`} onClick={() => onChange(options.find(option => option.id !== selectedId)?.id ?? '')}>{value}</button>,
}))
vi.mock('@/components/views/view-icon-picker', () => ({ ViewIconPicker: ({ onChange }: { onChange: (value: { icon: string; color: string }) => void }) => <button aria-label="Change project icon" onClick={() => onChange({ icon: 'Cube', color: '#123456' })}>Icon</button>, ViewGlyph: ({ color, icon }: { color?: string; icon?: string }) => <svg data-team-icon={icon} style={{ color }} /> }))
vi.mock('@/components/issue/issue-description-editor', () => ({ IssueDescriptionEditor: ({ ariaLabel, onBlur, onChange, value }: { ariaLabel: string; value: string; onBlur: () => void; onChange: (value: { markdown: string }) => void }) => <textarea aria-label={ariaLabel} defaultValue={value} onBlur={onBlur} onChange={event => onChange({ markdown: event.target.value })}/> }))
vi.mock('@/components/projects-page/project-target-date-picker', () => ({ ProjectDatePicker: ({ buttonClassName, children, label, onChange }: { buttonClassName?: string; children: ReactNode; label: string; onChange: (value: string) => void }) => <button aria-label={label} className={buttonClassName} onClick={() => onChange('2026-09-01')}>{children}</button> }))

import { ProjectOverview } from './project-overview'

describe('project overview workflow', () => {
  it('hides disabled initiative and customer features including the properties menu', async () => {
    const data=makeBootstrap()
    data.workspaceSettings.featureFlags={...data.workspaceSettings.featureFlags,initiatives:false,'customer-requests':false}
    const props={issueData:data,project:{...project,milestones:[],resources:[],customers:['Existing customer'],initiatives:['initiative']},projects:[project],initiatives:[{id:'initiative',name:'Hidden initiative',status:'planned'}],documents:[],projectStatuses:[project.status],projectUpdates:[],users:data.users,teams:data.teams,labels:[],labelGroups:[],projectIssues:[],save:vi.fn(),onTabChange:vi.fn(),integrationConnections:[],viewer:data.viewer} as unknown as ComponentProps<typeof ProjectOverview>
    render(<I18nProvider><ProjectOverview {...props}/></I18nProvider>)
    expect(screen.queryByText('Customers')).toBeNull()
    expect(screen.queryByText('Initiatives')).toBeNull()
    expect(screen.queryByText('Existing customer')).toBeNull()
    await userEvent.click(screen.getByRole('button',{name:'More project properties'}))
    expect(screen.queryByText('Initiatives')).toBeNull()
  })
  it('edits project fields, navigates updates, and creates scoped resources', async () => {
    const user = userEvent.setup()
    const data = makeBootstrap()
    const save = vi.fn().mockResolvedValue(undefined)
    const onTabChange = vi.fn()
    const onCreateResource = vi.fn().mockResolvedValue({ id: 'resource-1' })
    const onCreateMilestone = vi.fn().mockResolvedValue({ id: 'milestone-1', name: 'Launch' })
    const props = {
      project: { ...project, milestones: [], resources: [], customers: [] }, projects: [project], initiatives: [],
      documents: data.documents, projectStatuses: [project.status], projectUpdates: [], users: data.users, teams: data.teams,
      labels: [], labelGroups: [], projectIssues: [], save, onTabChange,
      onCreateResource, onUpdateResource: vi.fn(), onDeleteResource: vi.fn(),
      onCreateMilestone, onUpdateMilestone: vi.fn(), onDeleteMilestone: vi.fn(),
    } as unknown as ComponentProps<typeof ProjectOverview>
    render(<I18nProvider><ProjectOverview {...props}/></I18nProvider>)

    const name = screen.getByRole('textbox', { name: 'Project name' })
    await user.clear(name)
    await user.type(name, 'Renamed project')
    await user.tab()
    expect(save).toHaveBeenCalledWith({ name: 'Renamed project' })
    await user.click(screen.getByRole('button', { name: 'Change project icon' }))
    expect(save).toHaveBeenCalledWith({ icon: 'Cube', color: '#123456' })
    await user.click(screen.getByRole('button', { name: /Write first project update/ }))
    expect(onTabChange).toHaveBeenCalledWith('activity')
    expect(screen.queryByText('No issues in scope')).toBeNull()
    expect(screen.getByText('Write first project update').closest('section')).toHaveClass('is-empty')

    await user.click(screen.getByRole('button', { name: 'Milestone' }))
    await user.type(screen.getByRole('textbox', { name: 'Milestone name' }), 'Launch')
    await user.keyboard('{Enter}')
    await waitFor(() => expect(onCreateMilestone).toHaveBeenCalledWith(project.id, expect.objectContaining({ name: 'Launch' })))

    expect(screen.queryByText('Customers')).toBeNull()
    await user.click(screen.getByRole('button', { name: 'More project properties' }))
    await user.click(await screen.findByText('Customer request…'))
    const customer = await screen.findByRole('textbox', { name: 'Add customer request' })
    await user.type(customer, 'Acme request')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    expect(save).toHaveBeenCalledWith({ customers: ['Acme request'] })

    await user.click(screen.getByRole('button', { name: 'Add document or link…' }))
    await user.click(await screen.findByText('Create new document…'))
    expect(onCreateResource).toHaveBeenCalledWith(project.id, { type: 'document', title: 'Untitled document' })
  })

  it('hides empty optional rows and shows them once they have values', () => {
    const data = makeBootstrap()
    const label = { id: 'label-1', name: 'Frontend', color: '#5e6ad2' }
    const base = { issueData: data, projects: [project], documents: [], projectStatuses: [project.status], projectUpdates: [], users: data.users, teams: data.teams, labelGroups: [], projectIssues: [], save: vi.fn(), onTabChange: vi.fn(), integrationConnections: [], viewer: data.viewer, onCreateResource: vi.fn(), onUpdateResource: vi.fn(), onDeleteResource: vi.fn() }
    const initiative = { id: 'initiative-1', name: 'Growth', status: 'active' }
    const { rerender } = render(<I18nProvider><ProjectOverview {...({ ...base, project: { ...project, milestones: [], resources: [], customers: [], initiatives: [], labelIds: [] }, initiatives: [initiative], labels: [label] } as unknown as ComponentProps<typeof ProjectOverview>)}/></I18nProvider>)
    expect(screen.queryByRole('heading', { name: 'Initiatives' })).toBeNull()
    expect(screen.queryByRole('heading', { name: 'Labels' })).toBeNull()
    expect(screen.queryByRole('heading', { name: 'Customers' })).toBeNull()
    expect(screen.getByRole('heading', { name: 'Resources' })).toBeVisible()
    rerender(<I18nProvider><ProjectOverview {...({ ...base, project: { ...project, milestones: [], resources: [], customers: ['Acme'], initiatives: ['initiative-1'], labelIds: ['label-1'] }, initiatives: [initiative], labels: [label] } as unknown as ComponentProps<typeof ProjectOverview>)}/></I18nProvider>)
    expect(screen.getByRole('heading', { name: 'Initiatives' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Labels' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Customers' })).toBeVisible()
  })

  it('lists H1–H3 headings of an HTML description in the outline rail', () => {
    const data = makeBootstrap()
    const props = {
      issueData: data, project: { ...project, description: '<p>Intro</p><h2>Goals &amp; scope</h2><p>x</p><h3><strong>Risks</strong></h3>', milestones: [], resources: [], customers: [] },
      projects: [project], initiatives: [], documents: [], projectStatuses: [project.status], projectUpdates: [], users: data.users, teams: data.teams,
      labels: [], labelGroups: [], projectIssues: [], save: vi.fn(), onTabChange: vi.fn(), integrationConnections: [], viewer: data.viewer,
      onCreateResource: vi.fn(), onUpdateResource: vi.fn(), onDeleteResource: vi.fn(), onCreateMilestone: vi.fn(), onUpdateMilestone: vi.fn(), onDeleteMilestone: vi.fn(),
    } as unknown as ComponentProps<typeof ProjectOverview>
    render(<I18nProvider><ProjectOverview {...props}/></I18nProvider>)
    const outline = screen.getByRole('navigation', { name: 'Document outline' })
    expect([...outline.querySelectorAll('button')].map(button => button.getAttribute('aria-label'))).toEqual(['Description', 'Goals & scope', 'Risks'])
  })

  it('renders milestones collapsed with a static title and an outline rail', async () => {
    const user = userEvent.setup()
    const data = makeBootstrap()
    const onUpdateMilestone = vi.fn().mockResolvedValue(undefined)
    const props = {
      issueData: data, project: { ...project, description: 'Intro\n\n## Goals\n\n- one', milestones: [{ id: 'm1', name: 'Alpha', description: '', targetDate: '' }], resources: [], customers: [] },
      projects: [project], initiatives: [], documents: [], projectStatuses: [project.status], projectUpdates: [], users: data.users, teams: data.teams,
      labels: [], labelGroups: [], projectIssues: [], save: vi.fn(), onTabChange: vi.fn(), integrationConnections: [], viewer: data.viewer,
      onCreateResource: vi.fn(), onUpdateResource: vi.fn(), onDeleteResource: vi.fn(), onCreateMilestone: vi.fn(), onUpdateMilestone, onDeleteMilestone: vi.fn(),
    } as unknown as ComponentProps<typeof ProjectOverview>
    render(<I18nProvider><ProjectOverview {...props}/></I18nProvider>)

    const outline = screen.getByRole('navigation', { name: 'Document outline' })
    expect([...outline.querySelectorAll('button')].map(button => button.getAttribute('aria-label'))).toEqual(['Description', 'Goals', 'Milestones', 'Alpha'])
    // Linear: sections carry a 16px icon, children do not; the section in view is marked current; one rail bar per row.
    expect(outline.querySelectorAll('.project-overview__outline-bar')).toHaveLength(4)
    expect(outline.querySelectorAll('[aria-current="location"]')).toHaveLength(1)
    expect(outline.querySelector('[aria-current="location"]')).toHaveAttribute('data-level', '0')
    expect(screen.getByRole('button', { name: 'Description' }).querySelector('svg')).not.toBeNull()
    expect(screen.getByRole('button', { name: 'Milestones' }).querySelector('svg')).not.toBeNull()
    expect(screen.getByRole('button', { name: 'Goals' }).querySelector('svg')).toBeNull()
    expect(screen.queryByRole('textbox', { name: 'Milestone description' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Expand' })).toHaveAttribute('aria-expanded', 'false')
    expect(screen.getByText('Set target date')).toBeVisible()
    // Undated milestones reveal "Set target date" (and its separator) only on row hover / focus-within.
    expect(screen.getByText('Set target date').closest('button')).toHaveClass('project-overview__milestone-date', 'is-unset')
    expect(document.querySelector('.project-overview__milestone-dot')).toHaveClass('is-unset')
    expect(screen.queryByRole('textbox', { name: 'Milestone name' })).toBeNull()

    await user.click(screen.getByRole('button', { name: 'Rename Alpha' }))
    const name = screen.getByRole('textbox', { name: 'Milestone name' })
    expect(name).toHaveFocus()
    await user.clear(name)
    await user.type(name, 'Beta{Enter}')
    expect(onUpdateMilestone).toHaveBeenCalledWith(project.id, 'm1', { name: 'Beta' })
    expect(screen.queryByRole('textbox', { name: 'Milestone name' })).toBeNull()
  })

  it('shows the team name and resource tooltips like Linear', async () => {
    const user = userEvent.setup()
    const data = makeBootstrap()
    const team = data.teams.find(item => item.id === 'team-1') ?? data.teams[0]
    const props = { issueData: data, project: { ...project, teamIds: [team.id], milestones: [], resources: [], customers: [] }, projects: [project], initiatives: [], documents: [], projectStatuses: [project.status], projectUpdates: [], users: data.users, teams: data.teams, labels: [], labelGroups: [], projectIssues: [], save: vi.fn(), onTabChange: vi.fn(), integrationConnections: [], viewer: data.viewer } as unknown as ComponentProps<typeof ProjectOverview>
    render(<I18nProvider><ProjectOverview {...props}/></I18nProvider>)
    const teamChip = screen.getByRole('button', { name: team.name })
    expect(teamChip).toHaveAttribute('aria-disabled', 'true')
    await user.hover(teamChip)
    await waitFor(() => expect(screen.queryAllByRole('tooltip').some(tooltip => tooltip.textContent === team.name)).toBe(true), { timeout: 2000 })
    await user.keyboard('{Escape}')
    await user.hover(screen.getByRole('button', { name: /Add document or link/ }))
    await waitFor(() => expect(screen.queryAllByRole('tooltip').some(tooltip => tooltip.textContent === 'Add document or link')).toBe(true), { timeout: 2000 })
  })
})
