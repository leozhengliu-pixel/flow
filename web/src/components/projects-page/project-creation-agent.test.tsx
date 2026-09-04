import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type { AgentSession } from '@/types/flow'
import { I18nProvider } from '@/i18n/i18n'
import { NewProjectDialog } from './new-project-dialog'
import { ProjectCreationAgent } from './project-creation-agent'

const api = vi.hoisted(() => ({ fetchAgentStatus: vi.fn() }))
const streams = vi.hoisted(() => ({ streamNewAgentSession: vi.fn(), streamAgentSessionMessage: vi.fn() }))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/agent-stream', () => streams)

const session: AgentSession = {
  id: 'agent-session-1',
  slugId: 'project-draft',
  userId: 'user-1',
  title: 'Project draft',
  favorite: false,
  location: 'page',
  issueIds: [],
  skillIds: [],
  messages: [],
  createdAt: '2026-09-04T00:00:00.000Z',
  updatedAt: '2026-09-04T00:00:00.000Z',
}

describe('ProjectCreationAgent', () => {
  it('opens, hides, and closes from the new project dialog', async () => {
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'test-model' })
    const user = userEvent.setup()
    const onClose = vi.fn()
    render(<I18nProvider><NewProjectDialog open onClose={onClose} onCreate={vi.fn()} teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]}/></I18nProvider>)

    await user.click(screen.getByRole('button', { name: 'Create with Agent' }))
    expect(screen.getByRole('complementary', { name: 'Project creation assistant' })).toBeVisible()

    await user.click(screen.getByRole('button', { name: 'Hide agent' }))
    expect(screen.getByRole('complementary', { name: 'Project creation assistant' })).toHaveAttribute('data-hidden', 'true')

    await user.click(screen.getByRole('button', { name: 'Create with Agent' }))
    expect(screen.getByRole('complementary', { name: 'Project creation assistant' })).not.toHaveAttribute('data-hidden', 'true')
    await user.click(screen.getByRole('button', { name: 'Close project creation' }))
    const discardDialog = screen.queryByRole('alertdialog', { name: 'Discard changes?' })
    if (discardDialog) await user.click(discardDialog.querySelector<HTMLButtonElement>('.is-danger')!)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('dispatches a structured request when a suggestion is selected', async () => {
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'test-model' })
    streams.streamNewAgentSession.mockImplementation(async (_input, onEvent) => {
      onEvent({ type: 'session.started', session: { ...session, messages: [] } })
      onEvent({ type: 'session.completed', session })
      return session
    })
    const user = userEvent.setup()
    render(<I18nProvider><ProjectCreationAgent onClose={vi.fn()} onHide={vi.fn()}/></I18nProvider>)

    await waitFor(() => expect(api.fetchAgentStatus).toHaveBeenCalled())
    await user.click(screen.getByRole('button', { name: 'Outline the scope' }))
    await waitFor(() => expect(streams.streamNewAgentSession).toHaveBeenCalled())
    expect(streams.streamNewAgentSession).toHaveBeenCalledWith(expect.objectContaining({ location: 'page' }), expect.any(Function), expect.any(AbortSignal))
    expect(streams.streamNewAgentSession.mock.calls[0][0].message).toContain('Outline the scope')
    expect(screen.getByText('Outline the scope')).toBeVisible()
  })

  it('applies JSON draft fields returned by the assistant', async () => {
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'test-model' })
    streams.streamNewAgentSession.mockImplementation(async (_input, onEvent) => {
      onEvent({ type: 'session.completed', session: { ...session, messages: [{ id: 'assistant-1', role: 'assistant', content: '```json\n{"name":"Launch","targetDate":"2027-06-30","milestones":["Beta"]}\n```', createdAt: session.createdAt }] } })
      return { ...session, messages: [{ id: 'assistant-1', role: 'assistant', content: '{"name":"Launch"}', createdAt: session.createdAt }] }
    })
    const user = userEvent.setup()
    const onApplyDraft = vi.fn()
    render(<I18nProvider><ProjectCreationAgent onApplyDraft={onApplyDraft} onClose={vi.fn()} onHide={vi.fn()}/></I18nProvider>)

    await waitFor(() => expect(api.fetchAgentStatus).toHaveBeenCalled())
    const input = screen.getByRole('textbox', { name: 'Send a message to Linear AI' })
    await user.type(input, 'Create a launch project')
    await user.click(screen.getByRole('button', { name: 'Submit comment' }))
    await waitFor(() => expect(onApplyDraft).toHaveBeenCalledWith({ name: 'Launch', targetDate: '2027-06-30', milestones: ['Beta'] }))
  })

  it('keeps structured manual milestones when the assistant adds milestones', async () => {
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'test-model' })
    streams.streamNewAgentSession.mockImplementation(async (_input, onEvent) => {
      const completed = { ...session, messages: [{ id: 'assistant-2', role: 'assistant' as const, content: '```json\n{"milestones":["General availability"]}\n```', createdAt: session.createdAt }] }
      onEvent({ type: 'session.completed', session: completed })
      return completed
    })
    const user = userEvent.setup()
    const onCreate = vi.fn().mockResolvedValue(undefined)
    render(<I18nProvider><NewProjectDialog open onClose={vi.fn()} onCreate={onCreate} teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]}/></I18nProvider>)

    await user.click(screen.getByRole('button', { name: 'Add' }))
    await user.type(screen.getByRole('textbox', { name: 'Milestone name' }), 'Beta')
    await user.type(screen.getByRole('textbox', { name: 'Milestone description' }), 'Manual milestone')
    await user.click(screen.getByRole('button', { name: 'Add milestone' }))
    await user.click(screen.getByRole('button', { name: 'Create with Agent' }))
    const input = screen.getByRole('textbox', { name: 'Send a message to Linear AI' })
    await user.type(input, 'Add the final milestone')
    await user.click(screen.getByRole('button', { name: 'Submit comment' }))

    await waitFor(() => expect(screen.getByText('General availability')).toBeVisible())
    await user.type(screen.getByRole('textbox', { name: 'Project name' }), 'Mixed milestone project')
    await user.click(screen.getByRole('button', { name: 'Create project' }))
    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({
      milestones: ['Beta', 'General availability'],
      milestoneDetails: [{ name: 'Beta', description: 'Manual milestone' }, { name: 'General availability' }],
    }))
  })

  it('exposes the create-skill action from the composer menu', async () => {
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'test-model' })
    const user = userEvent.setup()
    const onCreateSkill = vi.fn()
    render(<I18nProvider><ProjectCreationAgent onCreateSkill={onCreateSkill} onClose={vi.fn()} onHide={vi.fn()}/></I18nProvider>)

    await waitFor(() => expect(api.fetchAgentStatus).toHaveBeenCalled())
    await user.click(screen.getByRole('button', { name: 'Skills' }))
    await user.click(screen.getByRole('menuitem', { name: 'Create skill' }))
    expect(onCreateSkill).toHaveBeenCalledTimes(1)
  })
})
