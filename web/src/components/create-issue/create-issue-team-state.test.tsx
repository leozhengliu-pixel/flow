import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { BootstrapData, Draft, Issue, IssueTemplate, Project, Team, TeamSettings, WorkflowState } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchIssueSuggestionPreview: vi.fn(), createDraft: vi.fn(), updateDraft: vi.fn(), deleteDraft: vi.fn(), listIssueRecords: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api, realtimeClientId: () => 'create-issue-team-state-test' }))

import { Shell } from '@/components/settings/mention-field-shell'
import { stubEditorDom } from '@/components/settings/mention-field-test-kit'
import { createIssueShortcutContext, projectCreateIssueContext } from '@/lib/create-issue-shortcut'
import { makeBootstrap, makeIssue, project as baseProject, viewer } from '@/test/fixtures'
import { CreateIssueDialog, type CreateIssueDialogProps } from './create-issue-dialog'

const state = (id: string, name: string, type: WorkflowState['type'], position: number, over: Partial<WorkflowState> = {}) => ({ id, name, color: '#888888', type, position, ...over }) as WorkflowState
// Workspace statuses, used by teams without a workflow of their own.
const workspaceStates = [state('ws-backlog', 'Backlog', 'backlog', 0), state('ws-todo', 'Todo', 'unstarted', 1), state('ws-progress', 'In Progress', 'started', 2), state('ws-done', 'Done', 'completed', 3)]
// A team with a custom workflow; its configured default is "Ready".
const customStates = [state('cus-triage', 'Intake', 'backlog', 0, { teamId: 'team-custom' }), state('cus-ready', 'Ready', 'unstarted', 1, { teamId: 'team-custom', default: true }), state('cus-review', 'Review', 'started', 2, { teamId: 'team-custom' }), state('cus-shipped', 'Shipped', 'completed', 3, { teamId: 'team-custom' })]
// A second custom workflow, for multi-team projects.
const otherStates = [state('oth-queued', 'Queued', 'unstarted', 0, { teamId: 'team-other' }), state('oth-doing', 'Doing', 'started', 1, { teamId: 'team-other' })]
// A sub-team keeps copies of its statuses but inherits the parent's workflow, so the copies are not valid.
const childCopies = [state('chd-ready', 'Child copy', 'unstarted', 0, { teamId: 'team-child' })]

const team = (id: string, key: string, name: string) => ({ id, key, name, color: '#5e6ad2' }) as Team
const teams = [team('team-custom', 'CUS', 'Custom team'), team('team-other', 'OTH', 'Other team'), team('team-plain', 'PLN', 'Plain team'), team('team-child', 'CHD', 'Child team')]
const settings = (teamId: string, over: Partial<TeamSettings> = {}) => ({ teamId, defaultStateId: '', access: 'public', ...over }) as TeamSettings
const teamSettings = {
  'team-custom': settings('team-custom', { defaultStateId: 'cus-ready' }),
  'team-other': settings('team-other', { defaultStateId: 'oth-queued' }),
  'team-plain': settings('team-plain', { defaultStateId: 'ws-todo' }),
  'team-child': settings('team-child', { defaultStateId: 'chd-ready', inheritWorkflowStatuses: true, parentTeamId: 'team-custom' }),
}
const singleTeamProject = { ...baseProject, id: 'project-single', slugId: 'single-team', name: 'Single team project', teamIds: ['team-custom'] } as Project
const multiTeamProject = { ...baseProject, id: 'project-multi', slugId: 'multi-team', name: 'Multi team project', teamIds: ['team-custom', 'team-other'] } as Project

function workspace(over: Partial<BootstrapData> = {}) {
  return makeBootstrap({
    teams, teamSettings, states: [...workspaceStates, ...customStates, ...otherStates, ...childCopies], cycleSettings: {}, teamMembers: [],
    projects: [singleTeamProject, multiTeamProject], issueTemplates: [], drafts: [], ...over,
  } as unknown as Partial<BootstrapData>)
}

const settle = async () => {
  await waitFor(() => expect(screen.getByLabelText('Issue title')).toHaveFocus())
  await act(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
}

function mount(props: Partial<CreateIssueDialogProps> = {}, data = workspace()) {
  const onCreate = vi.fn().mockResolvedValue(makeIssue() as Issue)
  const view = render(<Shell data={data}><CreateIssueDialog open data={data} onOpenChange={vi.fn()} onCreate={onCreate} {...props}/></Shell>)
  const rerender = (next: BootstrapData) => view.rerender(<Shell data={next}><CreateIssueDialog open data={next} onOpenChange={vi.fn()} onCreate={onCreate} {...props}/></Shell>)
  return { onCreate, rerender }
}

const statusTrigger = () => screen.getByRole('combobox', { name: /^Change Status\./ })
const teamTrigger = () => screen.getByRole('combobox', { name: 'Set team' })

async function statusOptions(user: ReturnType<typeof userEvent.setup>) {
  await user.click(statusTrigger())
  const names = (await screen.findAllByRole('option', { hidden: true })).map(option => option.textContent?.replace(/\d+$/, '').trim())
  await user.keyboard('{Escape}')
  return names
}

async function chooseTeam(user: ReturnType<typeof userEvent.setup>, name: string) {
  await user.click(teamTrigger())
  await user.click(await screen.findByRole('option', { name: new RegExp(name), hidden: true }))
}

async function chooseStatus(user: ReturnType<typeof userEvent.setup>, name: string) {
  await user.click(statusTrigger())
  await user.click(await screen.findByRole('option', { name: new RegExp(`^${name}`), hidden: true }))
}

async function submit(user: ReturnType<typeof userEvent.setup>, title = 'New work') {
  await user.click(screen.getByLabelText('Issue title'))
  await user.keyboard(title)
  await user.click(screen.getByRole('button', { name: 'Create issue' }))
}

beforeEach(() => {
  localStorage.clear()
  localStorage.setItem('flow:locale', 'en-US')
  for (const mock of Object.values(api)) mock.mockReset()
  api.fetchIssueSuggestionPreview.mockResolvedValue({ suggestions: [] })
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  stubEditorDom()
})
afterEach(() => { vi.unstubAllGlobals() })

describe('new issue: the status comes from the target team workflow', () => {
  it('a workspace Todo handed in for a custom-workflow team becomes that team default and is never offered', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    // What the app used to pass for every create entry: the first workspace unstarted status.
    const { onCreate } = mount({ initialTeamId: 'team-custom', initialStateId: 'ws-todo' })
    await settle()
    expect(statusTrigger()).toHaveAccessibleName(/Current value is Ready$/)
    expect(await statusOptions(user)).toEqual(['Intake', 'Ready', 'Review', 'Shipped'])
    await submit(user)
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    expect(onCreate.mock.calls[0][0]).toMatchObject({ teamId: 'team-custom', stateId: 'cus-ready' })
  })

  it('an inheriting sub-team uses the parent workflow, not its own copies', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const { onCreate } = mount({ initialTeamId: 'team-child', initialStateId: 'chd-ready' })
    await settle()
    expect(statusTrigger()).toHaveAccessibleName(/Current value is Ready$/)
    expect(await statusOptions(user)).toEqual(['Intake', 'Ready', 'Review', 'Shipped'])
    await submit(user)
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    expect(onCreate.mock.calls[0][0]).toMatchObject({ teamId: 'team-child', stateId: 'cus-ready' })
  })

  it('a team without its own workflow uses the workspace statuses and its configured default', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const { onCreate } = mount({ initialTeamId: 'team-plain' })
    await settle()
    expect(statusTrigger()).toHaveAccessibleName(/Current value is Todo$/)
    expect(await statusOptions(user)).toEqual(['Backlog', 'Todo', 'In Progress', 'Done'])
    await submit(user)
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    expect(onCreate.mock.calls[0][0]).toMatchObject({ teamId: 'team-plain', stateId: 'ws-todo' })
  })

  it('keeps a valid explicit status and replaces a foreign one with the team default', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const valid = mount({ initialTeamId: 'team-custom', initialStateId: 'cus-review' })
    await settle()
    expect(statusTrigger()).toHaveAccessibleName(/Current value is Review$/)
    await submit(user)
    await waitFor(() => expect(valid.onCreate).toHaveBeenCalled())
    expect(valid.onCreate.mock.calls[0][0]).toMatchObject({ teamId: 'team-custom', stateId: 'cus-review' })
  })

  it('a group preset from another team does not leak into the status menu', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const { onCreate } = mount({ initialContext: { teamId: 'team-custom', stateId: 'oth-doing' } })
    await settle()
    expect(statusTrigger()).toHaveAccessibleName(/Current value is Ready$/)
    expect(await statusOptions(user)).not.toContain('Doing')
    await submit(user)
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    expect(onCreate.mock.calls[0][0]).toMatchObject({ teamId: 'team-custom', stateId: 'cus-ready' })
  })

  it('a group preset without a team opens in a team whose workflow has that status', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const { onCreate } = mount({ initialContext: { stateId: 'oth-doing' } })
    await settle()
    expect(teamTrigger()).toHaveTextContent('OTH')
    expect(statusTrigger()).toHaveAccessibleName(/Current value is Doing$/)
    await submit(user)
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    expect(onCreate.mock.calls[0][0]).toMatchObject({ teamId: 'team-other', stateId: 'oth-doing' })
  })
})

describe('new issue: switching team', () => {
  it('selects the new team default when the chosen status is not in its workflow', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const { onCreate } = mount({ initialTeamId: 'team-custom' })
    await settle()
    await chooseStatus(user, 'Review')
    await chooseTeam(user, 'Other team')
    await waitFor(() => expect(statusTrigger()).toHaveAccessibleName(/Current value is Queued$/))
    expect(await statusOptions(user)).toEqual(['Queued', 'Doing'])
    await submit(user)
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    expect(onCreate.mock.calls[0][0]).toMatchObject({ teamId: 'team-other', stateId: 'oth-queued' })
  })

  it('keeps a status the new team workflow still has', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const { onCreate } = mount({ initialTeamId: 'team-custom' })
    await settle()
    await chooseStatus(user, 'Review')
    await chooseTeam(user, 'Child team')
    expect(statusTrigger()).toHaveAccessibleName(/Current value is Review$/)
    await submit(user)
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    expect(onCreate.mock.calls[0][0]).toMatchObject({ teamId: 'team-child', stateId: 'cus-review' })
  })
})

describe('new issue: templates, drafts and loading', () => {
  const template = (over: Partial<IssueTemplate>) => ({ id: 'tpl', name: 'Template', priority: 0, labelIds: [], creator: viewer, createdAt: '', updatedAt: '', templateType: 'standard', ...over }) as IssueTemplate

  it('applies a template status only when it is valid for the team', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const data = workspace({ issueTemplates: [
      template({ id: 'foreign', name: 'Foreign status', scope: 'team', teamId: 'team-custom', stateId: 'ws-progress' }),
      template({ id: 'valid', name: 'Valid status', scope: 'team', teamId: 'team-custom', stateId: 'cus-review' }),
      template({ id: 'moves', name: 'Moves team', scope: 'workspace', teamId: 'team-other', stateId: 'oth-doing' }),
    ] })
    const { onCreate } = mount({ initialTeamId: 'team-custom' }, data)
    await settle()
    const pick = async (name: string) => {
      await user.click(screen.getByRole('combobox', { name: 'Choose a template' }))
      await user.click(await screen.findByRole('option', { name: new RegExp(name), hidden: true }))
    }
    await pick('Valid status')
    expect(statusTrigger()).toHaveAccessibleName(/Current value is Review$/)
    await pick('Foreign status')
    await waitFor(() => expect(statusTrigger()).toHaveAccessibleName(/Current value is Ready$/))
    expect(await statusOptions(user)).not.toContain('In Progress')
    await pick('Moves team')
    await waitFor(() => expect(teamTrigger()).toHaveTextContent('OTH'))
    expect(statusTrigger()).toHaveAccessibleName(/Current value is Doing$/)
    await submit(user)
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    expect(onCreate.mock.calls[0][0]).toMatchObject({ teamId: 'team-other', stateId: 'oth-doing' })
  })

  it('a template opened from a link with a foreign status falls back to the team default', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const data = workspace({ issueTemplates: [template({ id: 'linked', name: 'Linked', title: 'Linked', scope: 'team', teamId: 'team-custom', stateId: 'ws-progress' })] })
    const { onCreate } = mount({ initialTeamId: 'team-custom', initialTemplateId: 'linked' }, data)
    await settle()
    await waitFor(() => expect(statusTrigger()).toHaveAccessibleName(/Current value is Ready$/))
    await submit(user, ' issue')
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    expect(onCreate.mock.calls[0][0]).toMatchObject({ teamId: 'team-custom', stateId: 'cus-ready' })
  })

  it('a restored local draft with a stale status is corrected for its team', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    localStorage.setItem('flow:create-issue-draft:team-custom', JSON.stringify({ title: 'Saved', teamId: 'team-custom', description: null, stateId: 'ws-todo', priority: 0, estimate: 0, assigneeId: viewer.id, projectId: '', projectMilestoneId: '', cycleId: '', dueDate: '', labelIds: [] }))
    const { onCreate } = mount({ draftId: 'local:team-custom', initialTeamId: 'team-custom' })
    await settle()
    expect(statusTrigger()).toHaveAccessibleName(/Current value is Ready$/)
    await submit(user, ' draft')
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    expect(onCreate.mock.calls[0][0]).toMatchObject({ teamId: 'team-custom', stateId: 'cus-ready' })
  })

  it('a restored server draft keeps its valid status in its own team', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const draft = { id: 'draft-1', type: 'issue', title: 'Server draft', body: '', metadata: { title: 'Server draft', teamId: 'team-other', stateId: 'oth-doing' } } as unknown as Draft
    const { onCreate } = mount({ draftId: 'draft-1' }, workspace({ drafts: [draft] } as Partial<BootstrapData>))
    await settle()
    expect(teamTrigger()).toHaveTextContent('OTH')
    expect(statusTrigger()).toHaveAccessibleName(/Current value is Doing$/)
    await submit(user, ' more')
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    expect(onCreate.mock.calls[0][0]).toMatchObject({ teamId: 'team-other', stateId: 'oth-doing' })
  })

  it('does not submit before the statuses load, then uses the requested status once they do', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const { onCreate, rerender } = mount({ initialTeamId: 'team-custom', initialStateId: 'cus-review' }, workspace({ states: [] }))
    await settle()
    await submit(user)
    expect(await screen.findByRole('alert')).toHaveTextContent('Statuses are still loading')
    expect(onCreate).not.toHaveBeenCalled()
    rerender(workspace())
    await waitFor(() => expect(statusTrigger()).toHaveAccessibleName(/Current value is Review$/))
    await user.click(screen.getByRole('button', { name: 'Create issue' }))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    expect(onCreate.mock.calls[0][0]).toMatchObject({ teamId: 'team-custom', stateId: 'cus-review' })
  })

  it('does not submit for a team that has no usable status', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    // Only team workflows exist, so a team without its own has nothing to use.
    const { onCreate } = mount({ initialTeamId: 'team-plain' }, workspace({ states: [...customStates, ...otherStates] }))
    await settle()
    await submit(user)
    expect(await screen.findByRole('alert')).toHaveTextContent('Plain team has no status for new issues')
    expect(onCreate).not.toHaveBeenCalled()
  })
})

describe('new issue from a project', () => {
  it('the empty project list entry of a single-team project creates in that team with its default', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const data = workspace()
    const { onCreate } = mount({ initialContext: projectCreateIssueContext(data, singleTeamProject.id), initialProjectId: singleTeamProject.id })
    await settle()
    await submit(user)
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    expect(onCreate.mock.calls[0][0]).toMatchObject({ projectId: singleTeamProject.id, teamId: 'team-custom', stateId: 'cus-ready' })
  })

  it('a status group of the second team in a multi-team project creates in that team', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const data = workspace()
    const { onCreate } = mount({ initialContext: projectCreateIssueContext(data, multiTeamProject.id, undefined, { stateId: 'oth-doing' }), initialProjectId: multiTeamProject.id })
    await settle()
    expect(statusTrigger()).toHaveAccessibleName(/Current value is Doing$/)
    expect(await statusOptions(user)).toEqual(['Queued', 'Doing'])
    await submit(user)
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    expect(onCreate.mock.calls[0][0]).toMatchObject({ projectId: multiTeamProject.id, teamId: 'team-other', stateId: 'oth-doing' })
  })

  it('the C shortcut on a multi-team project opens in its first team with that team default', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 })
    const data = workspace()
    const context = createIssueShortcutContext({ kind: 'project', workspaceSlug: 'workspace', projectSlugId: multiTeamProject.slugId, tab: 'issues' }, data)
    expect(context).toEqual({ projectId: multiTeamProject.id, teamId: 'team-custom' })
    const { onCreate } = mount({ initialContext: context, initialProjectId: multiTeamProject.id })
    await settle()
    expect(await statusOptions(user)).toEqual(['Intake', 'Ready', 'Review', 'Shipped'])
    await submit(user)
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    expect(onCreate.mock.calls[0][0]).toMatchObject({ teamId: 'team-custom', stateId: 'cus-ready' })
  })
})

describe('projectCreateIssueContext', () => {
  const data = workspace()
  it('uses the given team, else the project team whose workflow has the status, else the first project team', () => {
    expect(projectCreateIssueContext(data, multiTeamProject.id)).toMatchObject({ projectId: multiTeamProject.id, teamId: 'team-custom' })
    expect(projectCreateIssueContext(data, multiTeamProject.id, 'milestone-1', { stateId: 'oth-queued' })).toMatchObject({ projectId: multiTeamProject.id, projectMilestoneId: 'milestone-1', teamId: 'team-other', stateId: 'oth-queued' })
    expect(projectCreateIssueContext(data, multiTeamProject.id, undefined, { stateId: 'ws-todo' })).toMatchObject({ teamId: 'team-custom' })
    expect(projectCreateIssueContext(data, multiTeamProject.id, undefined, { teamId: 'team-plain', stateId: 'oth-queued' })).toMatchObject({ teamId: 'team-plain' })
  })
})
