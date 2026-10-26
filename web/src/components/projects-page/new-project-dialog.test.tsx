import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { MentionShell, pasteText, stubEditorEnvironment } from '@/test/mention-host-harness'

const api = vi.hoisted(() => ({ fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn(), listIssueRecords: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/api')>(), ...api }))

import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
import { NewProjectDialog } from './new-project-dialog'

beforeEach(() => {
  for (const mock of [api.fetchIssueRecord, api.listProjectRecords, api.listIssueRecords]) mock.mockReset()
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  resetAgentRecordCache()
  stubEditorEnvironment()
})
afterEach(() => { vi.unstubAllGlobals() })

describe('NewProjectDialog', () => {
  it('starts a new project in the team it is created from, not the first team', async () => {
    const user = userEvent.setup()
    const onCreate = vi.fn().mockResolvedValue(undefined)
    render(
      <I18nProvider>
        <NewProjectDialog
          open
          defaultTeamId="team-design"
          onClose={vi.fn()}
          onCreate={onCreate}
          statuses={[{ id: 'Planned', label: 'Planned' }]}
          teams={[{ id: 'team-eng', label: 'Engineering' }, { id: 'team-design', label: 'Design' }]}
        />
      </I18nProvider>,
    )
    await user.type(screen.getByRole('textbox', { name: 'Project name' }), 'Design project')
    await user.click(screen.getByRole('button', { name: 'Create project' }))
    await waitFor(() => expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ teamIds: ['team-design'] })))
  })
  it('uses the provided project status dictionary without English fallback states', async () => {
    const user = userEvent.setup()
    const onCreate = vi.fn()
    render(
      <I18nProvider>
        <NewProjectDialog
          defaultStatus="建设中"
          open
          onClose={vi.fn()}
          onCreate={onCreate}
          statuses={[
            { id: '待规划', label: '待规划' },
            { id: '建设中', label: '建设中' },
            { id: '已完成', label: '已完成' },
          ]}
          teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]}
        />
      </I18nProvider>,
    )

    await user.click(screen.getByRole('combobox', { name: 'Change project status' }))
    expect(screen.getByRole('option', { name: '待规划' })).toBeVisible()
    expect(screen.getByRole('option', { name: '建设中' })).toBeVisible()
    expect(screen.queryByRole('option', { name: 'Backlog' })).toBeNull()
    await user.keyboard('{Escape}')
    await user.type(screen.getByRole('textbox', { name: 'Project name' }), '本地化项目')
    await user.click(screen.getByRole('button', { name: 'Create project' }))
    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ status: '建设中' }))
  })

  it('keeps a selected icon color when the project name changes', async () => {
    const user = userEvent.setup()
    render(
      <I18nProvider>
        <NewProjectDialog
          open
          onClose={vi.fn()}
          onCreate={vi.fn()}
          teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]}
        />
      </I18nProvider>,
    )

    await user.click(screen.getByRole('button', { name: 'Choose icon' }))
    await user.click(screen.getByRole('radio', { name: 'Teal' }))
    await user.type(screen.getByRole('textbox', { name: 'Project name' }), 'Cyan project')

    expect(screen.getByRole('button', { name: 'Choose icon' })).toHaveStyle({
      '--view-color': '#26b5ce',
    })
    await user.keyboard('{Escape}')
  })

  it('shows the full initiative name beside its icon in the create picker', async () => {
    const user = userEvent.setup()
    render(
      <I18nProvider>
        <NewProjectDialog
          initiatives={[{ id: 'init-1', label: 'Favorite perf initiative', color: '#eb5757', groupLabel: 'Active' }]}
          open
          onClose={vi.fn()}
          onCreate={vi.fn()}
          teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]}
        />
      </I18nProvider>,
    )
    // The dialog focuses the name field on the next animation frame; opening a picker before that
    // moves focus outside the popover and dismisses it, so wait for the dialog to settle first.
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Project name' })).toHaveFocus())
    await user.click(screen.getByRole('combobox', { name: 'Change project initiatives' }))
    const option = await screen.findByRole('option', { name: 'Favorite perf initiative' })
    expect(option.closest('.lp-new-project-picker__initiatives')).toBeTruthy()
    expect(within(option).getByText('Favorite perf initiative')).toBeVisible()
    expect(option.querySelector('.property-command-checkbox')).toBeTruthy()
    expect(option.querySelector('.property-command-icon')).toBeTruthy()
    expect(screen.getByPlaceholderText('Change initiatives…')).toBeInTheDocument()
    expect(screen.getByText('Active')).toBeVisible()
  })

  it('uses the shared person picker for the project lead', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    render(
      <I18nProvider>
        <NewProjectDialog
          leads={[{ id: 'user-1', label: 'Zheng Liu', email: 'zheng@example.com' }]}
          open
          onClose={vi.fn()}
          onCreate={onCreate}
          teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]}
        />
      </I18nProvider>,
    )

    expect(screen.getByRole('combobox', { name: 'Set project lead' })).toBeVisible()
  })

  it('includes milestones and date precision in the create draft', async () => {
    const user = userEvent.setup()
    const onCreate = vi.fn().mockResolvedValue(undefined)
    render(
      <I18nProvider>
        <NewProjectDialog open onClose={vi.fn()} onCreate={onCreate} teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]} />
      </I18nProvider>,
    )

    await user.click(screen.getByRole('button', { name: 'Add' }))
    await user.click(screen.getByRole('textbox', { name: 'Milestone name' }))
    await user.paste('Launch')
    await user.click(screen.getByRole('textbox', { name: 'Milestone description' }))
    await user.paste('Launch criteria')
    await user.click(screen.getByRole('button', { name: 'Choose date' }))
    const today = new Date()
    await user.click(screen.getByRole('gridcell', { name: new Intl.DateTimeFormat('en-US', { dateStyle: 'full' }).format(today) }))
    await user.click(screen.getByRole('button', { name: 'Add milestone' }))
    await user.click(screen.getByRole('button', { name: 'Change project target date' }))
    await user.click(screen.getByRole('tab', { name: 'Month' }))
    await user.click(screen.getAllByRole('button', { name: 'Jan' })[0])
    await user.click(screen.getByRole('textbox', { name: 'Project name' }))
    await user.paste('Roadmap')
    await user.click(screen.getByRole('button', { name: 'Create project' }))

    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({
      milestones: ['Launch'],
      milestoneDetails: [{ name: 'Launch', description: 'Launch criteria', targetDate: `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, '0')}-${String(today.getDate()).padStart(2, '0')}` }],
      targetDateResolution: 'month',
    }))
  }, 15000)

  it('resets a cancelled milestone draft and ignores an empty milestone', async () => {
    const user = userEvent.setup()
    render(
      <I18nProvider>
        <NewProjectDialog open onClose={vi.fn()} onCreate={vi.fn()} teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]} />
      </I18nProvider>,
    )

    await user.click(screen.getByRole('button', { name: 'Add' }))
    await user.type(screen.getByRole('textbox', { name: 'Milestone name' }), 'Discard me')
    await user.type(screen.getByRole('textbox', { name: 'Milestone description' }), 'Temporary details')
    await user.click(screen.getByRole('button', { name: 'Discard changes' }))

    await user.click(screen.getByRole('button', { name: 'Add' }))
    expect(screen.getByRole('textbox', { name: 'Milestone name' })).toHaveValue('')
    expect(screen.getByRole('textbox', { name: 'Milestone description' })).toHaveTextContent(/^$/)
    await user.click(screen.getByRole('button', { name: 'Add milestone' }))
    expect(screen.getByText('Create milestone')).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Remove Discard me' })).not.toBeInTheDocument()
  })

  it('removes an added milestone from both create payload representations', async () => {
    const user = userEvent.setup()
    const onCreate = vi.fn().mockResolvedValue(undefined)
    render(
      <I18nProvider>
        <NewProjectDialog open onClose={vi.fn()} onCreate={onCreate} teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]} />
      </I18nProvider>,
    )

    await user.click(screen.getByRole('button', { name: 'Add' }))
    await user.type(screen.getByRole('textbox', { name: 'Milestone name' }), 'Remove me')
    await user.click(screen.getByRole('button', { name: 'Add milestone' }))
    await user.click(screen.getByRole('button', { name: 'Remove Remove me' }))
    await user.type(screen.getByRole('textbox', { name: 'Project name' }), 'No milestones')
    await user.click(screen.getByRole('button', { name: 'Create project' }))

    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({
      milestones: [],
      milestoneDetails: [],
    }))
  })

  it('persists selected members and directional dependencies in the draft', async () => {
    const user = userEvent.setup()
    const onCreate = vi.fn().mockResolvedValue(undefined)
    render(
      <I18nProvider>
        <NewProjectDialog
          dependencies={[{ id: 'project-1', label: 'Blocked project', color: '#5e6ad2' }]}
          members={[{ id: 'user-1', label: 'Zheng Liu', email: 'zheng@example.com' }]}
          open
          onClose={vi.fn()}
          onCreate={onCreate}
          teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]}
        />
      </I18nProvider>,
    )

    await new Promise(resolve => setTimeout(resolve, 20))
    await user.click(screen.getByRole('combobox', { name: 'Change project members' }))
    await user.click(await screen.findByRole('option', { name: /Zheng Liu/ }))
    await user.click(screen.getByRole('combobox', { name: 'Add dependencies' }))
    await user.click(await screen.findByRole('option', { name: 'Blocked by…' }))
    await user.click(await screen.findByRole('option', { name: 'Blocked project' }))
    await user.type(screen.getByRole('textbox', { name: 'Project name' }), 'Roadmap')
    await user.click(screen.getByRole('button', { name: 'Create project' }))

    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({
      memberIds: ['user-1'],
      dependencyIds: ['project-1'],
      dependencyRelations: [{ projectId: 'project-1', type: 'blocked_by' }],
    }))
  })

  it('confirms before discarding a changed project draft', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    render(
      <I18nProvider>
        <NewProjectDialog open onClose={onClose} onCreate={vi.fn()} teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]} />
      </I18nProvider>,
    )

    await user.type(screen.getByRole('textbox', { name: 'Project name' }), 'Changed')
    await user.click(screen.getByRole('button', { name: 'Discard project' }))
    expect(screen.getByRole('alertdialog', { name: 'Discard changes?' })).toBeVisible()
    const confirmation = screen.getByRole('alertdialog', { name: 'Discard changes?' })
    await user.click(within(confirmation).getByRole('button', { name: 'Cancel' }))
    expect(onClose).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Discard project' }))
    await user.click(within(screen.getByRole('alertdialog', { name: 'Discard changes?' })).getByRole('button', { name: 'Discard' }))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('renders the property row with the shared create-pill style', () => {
    const previousLocale = localStorage.getItem('flow:locale')
    localStorage.setItem('flow:locale', 'zh-CN')
    try {
      render(
        <I18nProvider>
          <NewProjectDialog
            leads={[{ id: 'user-1', label: 'Zheng Liu', email: 'zheng@example.com' }]}
            open
            onClose={vi.fn()}
            onCreate={vi.fn()}
            statuses={[{ id: 'backlog', label: 'Backlog' }]}
            teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]}
          />
        </I18nProvider>,
      )
      const row = document.querySelector('.lp-new-project__properties')
      expect(row).toHaveClass('create-pill-row')
      const pills = [...(row?.querySelectorAll('button') ?? [])]
      expect(pills.length).toBeGreaterThanOrEqual(8)
      for (const pill of pills) expect(pill).toHaveClass('create-pill')
      // Status and priority show set values; Lead, Members, Start, Target, Labels and Dependencies are placeholders.
      expect(pills.filter(pill => !pill.classList.contains('is-placeholder')).map(pill => pill.getAttribute('aria-label'))).toEqual(['更改项目状态', '更改项目优先级'])
      // Priority is a fixed choice, so it is translated (statuses are workspace data).
      expect(screen.getByRole('combobox', { name: '更改项目优先级' })).toHaveTextContent('无优先级')
    } finally {
      if (previousLocale === null) localStorage.removeItem('flow:locale')
      else localStorage.setItem('flow:locale', previousLocale)
    }
  })

  it('mentions resources in the project description and keeps them in the saved markdown', async () => {
    const user = userEvent.setup()
    const onCreate = vi.fn().mockResolvedValue(undefined)
    render(
      <MentionShell data={mentionFixture()}>
        <NewProjectDialog open onClose={vi.fn()} onCreate={onCreate} teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]} />
      </MentionShell>,
    )

    await user.type(screen.getByRole('textbox', { name: 'Project name' }), 'Mentions')
    const box = screen.getByRole('textbox', { name: 'Project description' })
    await user.click(box)
    await user.keyboard('Plan: @Launch')
    await user.click(await screen.findByRole('option', { name: /Launch plan/ }))
    pasteText(box, `${window.location.origin}${mentionUrls.project}`)
    await waitFor(() => expect(document.querySelector('a[data-agent-entity="project"]')).toHaveTextContent('Project one'))
    expect(document.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan')
    await user.click(screen.getByRole('button', { name: 'Create project' }))

    const description = onCreate.mock.calls[0][0].description as string
    expect(description).toContain('[Launch plan](/workspace/document/plan-abc)')
    expect(description).toContain(`[Project one](${mentionUrls.project})`)
  })

  it('shows a template description with its mentions as chips and mentions in the milestone description', async () => {
    const user = userEvent.setup()
    const onCreate = vi.fn().mockResolvedValue(undefined)
    render(
      <MentionShell data={mentionFixture()}>
        <NewProjectDialog initialTemplateId="template-1" open onClose={vi.fn()} onCreate={onCreate} teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]} templates={[{ id: 'template-1', label: 'Brief', name: 'Brief', description: 'Read [Launch plan](/workspace/document/plan-abc)' }]} />
      </MentionShell>,
    )

    await waitFor(() => expect(document.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan'))
    await user.click(screen.getByRole('button', { name: 'Add' }))
    await user.type(screen.getByRole('textbox', { name: 'Milestone name' }), 'Beta')
    await user.click(screen.getByRole('textbox', { name: 'Milestone description' }))
    await user.keyboard('Owner @Road')
    await user.click(await screen.findByRole('option', { name: /Roadmap/ }))
    await user.click(screen.getByRole('button', { name: 'Add milestone' }))
    expect(screen.getByText('Owner Roadmap')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Create project' }))

    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({
      description: 'Read [Launch plan](/workspace/document/plan-abc)',
      milestoneDetails: [expect.objectContaining({ name: 'Beta', description: expect.stringContaining('[Roadmap](/workspace/initiative/roadmap/overview)') })],
    }))
  })
  it('applies a template lead, members, dependency relations and milestones, and clears them for No template', async () => {
    const user = userEvent.setup()
    const onCreate = vi.fn().mockResolvedValue(undefined)
    const people = [{ id: 'user-lead', label: 'Lead Person' }, { id: 'user-a', label: 'Member A' }, { id: 'user-b', label: 'Member B' }]
    render(
      <I18nProvider>
        <NewProjectDialog
          open
          onClose={vi.fn()}
          onCreate={onCreate}
          dependencies={[{ id: 'project-blocker', label: 'Blocker' }, { id: 'project-blocked', label: 'Blocked' }]}
          initiatives={[{ id: 'initiative-1', label: 'Roadmap' }]}
          leads={people}
          members={people}
          statuses={[{ id: 'Planned', label: 'Planned' }, { id: 'In Progress', label: 'In Progress' }]}
          teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]}
          templates={[{
            id: 'template-1',
            label: 'Launch',
            name: 'Product launch',
            summary: 'Ship it',
            status: 'In Progress',
            priority: 'High',
            leadId: 'user-lead',
            memberIds: ['user-a', 'user-b', 'user-gone'],
            teamIds: ['team-1'],
            initiativeIds: ['initiative-1'],
            dependencyIds: ['project-blocker'],
            dependencyRelations: [{ projectId: 'project-blocker', type: 'blocked_by' }, { projectId: 'project-blocked', type: 'blocks' }, { projectId: 'project-gone', type: 'blocks' }],
            milestones: [{ templateMilestoneId: 'milestone-beta', name: 'Beta', description: 'Beta exit' }],
          }]}
        />
      </I18nProvider>,
    )

    await user.click(screen.getByRole('combobox', { name: 'Apply project template' }))
    await user.click(screen.getByRole('option', { name: 'Launch' }))
    expect(screen.getByRole('textbox', { name: 'Project name' })).toHaveValue('Product launch')
    expect(screen.getByText('Beta')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Create project' }))
    expect(onCreate).toHaveBeenLastCalledWith(expect.objectContaining({
      templateId: 'template-1',
      name: 'Product launch',
      summary: 'Ship it',
      status: 'In Progress',
      priority: 'High',
      leadId: 'user-lead',
      memberIds: ['user-a', 'user-b'],
      initiativeIds: ['initiative-1'],
      dependencyIds: ['project-blocker'],
      dependencyRelations: [{ projectId: 'project-blocker', type: 'blocked_by' }, { projectId: 'project-blocked', type: 'blocks' }],
      milestones: ['Beta'],
      milestoneDetails: [{ templateMilestoneId: 'milestone-beta', name: 'Beta', description: 'Beta exit' }],
    }))

    await user.clear(screen.getByRole('textbox', { name: 'Project name' }))
    await user.type(screen.getByRole('textbox', { name: 'Project name' }), 'My launch')
    await user.click(screen.getByRole('combobox', { name: 'Apply project template' }))
    await user.click(screen.getByRole('option', { name: 'No template' }))
    expect(screen.queryByText('Beta')).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Create project' }))
    const draft = onCreate.mock.lastCall?.[0]
    expect(draft).toEqual(expect.objectContaining({
      templateId: undefined,
      name: 'My launch',
      summary: '',
      status: 'Planned',
      priority: 'No priority',
      leadId: undefined,
      memberIds: [],
      initiativeIds: [],
      dependencyIds: [],
      dependencyRelations: [],
      milestones: [],
    }))
  })
})
