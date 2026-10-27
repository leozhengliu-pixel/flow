import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, project } from '@/test/fixtures'
import type { AgentChange, AgentMessage, AgentSession } from '@/types/flow'

const api = vi.hoisted(() => ({ undoAgentMessageChanges: vi.fn(), restoreAgentMessageChanges: vi.fn() }))
const toasts = vi.hoisted(() => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn(), info: vi.fn() }) }))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/toast', () => toasts)

import { AgentChangeCards } from './agent-change-cards'

const data = makeBootstrap({ issues: [makeIssue()], projects: [{ ...project, description: 'Ship exports.\n\nRisks ahead for large workspaces' }] })

const createdIssue: AgentChange = { action: 'created', entityType: 'issue', entityId: 'issue-1', label: 'TST-1', undoable: true }
const updatedProject: AgentChange = {
  action: 'updated', entityType: 'project', entityId: project.id, label: project.name, fields: ['description'], undoable: true,
  before: { description: 'Ship exports.' }, after: { description: 'Ship exports.\n\nRisks ahead for large workspaces' },
}

const reply = (...changes: AgentChange[]): AgentMessage => ({
  id: 'reply-1', role: 'assistant', content: 'Done.', createdAt: '2026-10-12T00:00:00Z',
  parts: changes.map((change, index) => ({ id: `part-${index}`, type: 'toolCall', status: 'completed', toolCall: { id: `call-${index}`, name: 'save', status: 'completed', change } })),
})

const session = (message: AgentMessage): AgentSession => ({
  id: 'session-1', slugId: 's', userId: 'user-1', title: 'Chat', favorite: false, location: 'page', issueIds: [], skillIds: [],
  messages: [message], createdAt: '2026-10-12T00:00:00Z', updatedAt: '2026-10-12T00:00:00Z',
})

const withState = (message: AgentMessage, state: AgentChange['state']): AgentMessage => ({
  ...message, parts: message.parts?.map(part => part.toolCall?.change ? { ...part, toolCall: { ...part.toolCall, change: { ...part.toolCall.change, state } } } : part),
})

function renderCards(message: AgentMessage, props: { sessionId?: string; onSessionChange?: (session: AgentSession) => void } = { sessionId: 'session-1' }) {
  return render(<I18nProvider><AgentChangeCards data={data} message={message} {...props}/></I18nProvider>)
}

describe('agent result cards', () => {
  beforeEach(() => {
    api.undoAgentMessageChanges.mockReset()
    api.restoreAgentMessageChanges.mockReset()
    toasts.toast.mockReset()
    toasts.toast.error.mockReset()
  })
  afterEach(() => document.querySelectorAll('[data-agent-change-highlight], .ProseMirror').forEach(node => node.remove()))

  it('shows "Created TST-1" above an issue card with its status and priority', () => {
    renderCards(reply(createdIssue))
    expect(screen.getByText('Created TST-1')).toBeVisible()
    const card = screen.getByRole('region', { name: 'Created TST-1' })
    expect(card).toHaveTextContent('Test issue')
    expect(card).toHaveTextContent('In progress')
    expect(card).toHaveTextContent('High')
    expect(screen.getByRole('link', { name: /TST-1/ })).toHaveAttribute('href', expect.stringContaining('TST-1'))
  })

  it('Undo reverts the card’s writes and turns into Restore', async () => {
    const message = reply(updatedProject)
    const onSessionChange = vi.fn()
    api.undoAgentMessageChanges.mockResolvedValue(session(withState(message, 'undone')))
    api.restoreAgentMessageChanges.mockResolvedValue(session(withState(message, '')))
    renderCards(message, { sessionId: 'session-1', onSessionChange })
    expect(screen.getByText('Updated project')).toBeVisible()
    expect(screen.getByText('Changed the description.')).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: 'Undo' }))
    expect(api.undoAgentMessageChanges).toHaveBeenCalledWith('session-1', 'reply-1', ['part-0'])
    await screen.findByRole('button', { name: 'Restore' })
    expect(onSessionChange).toHaveBeenCalledTimes(1)
    await userEvent.click(screen.getByRole('button', { name: 'Restore' }))
    expect(api.restoreAgentMessageChanges).toHaveBeenCalledWith('session-1', 'reply-1', ['part-0'])
    await screen.findByRole('button', { name: 'Undo' })
  })

  it('reports a conflict and keeps the card as it was', async () => {
    api.undoAgentMessageChanges.mockRejectedValue(new Error('Project "Project one" changed after the agent\'s edit (description), so nothing was changed'))
    renderCards(reply(updatedProject))
    await userEvent.click(screen.getByRole('button', { name: 'Undo' }))
    await waitFor(() => expect(toasts.toast.error).toHaveBeenCalledWith('This changed after the agent’s edit, so nothing was undone.'))
    expect(screen.getByRole('button', { name: 'Undo' })).toBeEnabled()
  })

  it('tells which fields a partial undo left alone', async () => {
    const message = reply(updatedProject)
    const partial = withState(message, 'undone')
    partial.parts![0].toolCall!.change!.skipped = ['description']
    api.undoAgentMessageChanges.mockResolvedValue(session(partial))
    renderCards(message)
    await userEvent.click(screen.getByRole('button', { name: 'Undo' }))
    await waitFor(() => expect(toasts.toast).toHaveBeenCalledWith('Left the description as is: it changed after the agent’s edit.'))
  })

  it('links a record the client has not loaded yet through the recorded path', () => {
    renderCards(reply({ action: 'created', entityType: 'initiative', entityId: 'init-new', label: 'QA fix – new initiative', url: '/workspace/initiative/qa-fix-new', undoable: true }))
    expect(screen.getByText('Created initiative')).toBeVisible()
    expect(screen.getByRole('link', { name: /QA fix – new initiative/ })).toHaveAttribute('href', '/workspace/initiative/qa-fix-new')
  })

  it('has no Undo before the chat is saved', () => {
    renderCards(reply(createdIssue), {})
    expect(screen.queryByRole('button', { name: 'Undo' })).toBeNull()
  })

  it('Highlight changes marks the changed blocks in the live editor', async () => {
    const editor = document.createElement('div')
    editor.className = 'ProseMirror'
    editor.setAttribute('contenteditable', 'true')
    editor.innerHTML = '<p>Ship exports.</p><p>Risks ahead for large workspaces</p>'
    document.body.append(editor)
    renderCards(reply(updatedProject))
    await userEvent.click(screen.getByRole('checkbox', { name: 'Highlight changes' }))
    await waitFor(() => expect(document.querySelectorAll('[data-agent-change-highlight] > *')).toHaveLength(1))
    expect(screen.queryByRole('list', { name: 'Changes' })).toBeNull()
    await userEvent.click(screen.getByRole('checkbox', { name: 'Highlight changes' }))
    expect(document.querySelector('[data-agent-change-highlight]')).toBeNull()
  })

  it('shows the diff in the card when the edited editor is not on screen', async () => {
    renderCards(reply(updatedProject))
    await userEvent.click(screen.getByRole('checkbox', { name: 'Highlight changes' }))
    const diff = await screen.findByRole('list', { name: 'Changes' })
    expect(diff.querySelector('[data-kind="added"]')).toHaveTextContent('Risks ahead for large workspaces')
    expect(diff.querySelector('[data-kind="same"]')).toHaveTextContent('Ship exports.')
  })

  it('switches from the diff to editor bars once the editor shows the new text', async () => {
    renderCards(reply(updatedProject))
    await userEvent.click(screen.getByRole('checkbox', { name: 'Highlight changes' }))
    await screen.findByRole('list', { name: 'Changes' })
    const editor = document.createElement('div')
    editor.className = 'ProseMirror'
    editor.setAttribute('contenteditable', 'true')
    editor.innerHTML = '<p>Ship exports.</p><p>Risks ahead for large workspaces</p>'
    document.body.append(editor)
    await waitFor(() => expect(document.querySelectorAll('[data-agent-change-highlight] > *')).toHaveLength(1), { timeout: 2500 })
    expect(screen.queryByRole('list', { name: 'Changes' })).toBeNull()
  })

  it('lists several created issues as one card', () => {
    const second = makeIssue({ id: 'issue-2', identifier: 'TST-2', number: 2, title: 'Second issue' })
    render(<I18nProvider><AgentChangeCards data={makeBootstrap({ issues: [makeIssue(), second] })} message={reply(createdIssue, { ...createdIssue, entityId: 'issue-2', label: 'TST-2' })} sessionId="session-1"/></I18nProvider>)
    expect(screen.getByText('Created 2 issues')).toBeVisible()
    expect(screen.getByRole('list', { name: 'Created issues' })).toHaveTextContent('Second issue')
    expect(screen.getAllByRole('button', { name: 'Undo' })).toHaveLength(1)
  })

  it('shows "Created team" with the team icon, its name and its key at the right', () => {
    const team: AgentChange = { action: 'created', entityType: 'team', entityId: 'team-new', label: 'Invite Test', url: '/test/team/INV/all', undoable: false }
    const message = reply(team)
    // The new team is not in the client's data yet: the key comes from the tool's receipt.
    message.parts![0].toolCall!.result = { id: 'team-new', key: 'INV', name: 'Invite Test', created: true }
    renderCards(message)
    const card = screen.getByRole('region', { name: 'Created team' })
    const link = screen.getByRole('link', { name: /Invite Test/ })
    expect(link).toHaveAttribute('href', expect.stringContaining('/team/INV'))
    expect(link).toHaveAttribute('data-agent-entity', 'team')
    expect(link.querySelector('svg, img')).not.toBeNull()
    expect(link.lastElementChild).toHaveTextContent('INV')
    expect(card).not.toHaveTextContent('Undo')
  })

  it('a team the client has loaded shows its own icon, name and key', () => {
    const loaded = data.teams[0]
    renderCards(reply({ action: 'created', entityType: 'team', entityId: loaded.id, label: loaded.name, undoable: false }))
    const card = screen.getByRole('region', { name: 'Created team' })
    expect(card).toHaveTextContent(loaded.name)
    expect(card).toHaveTextContent(loaded.key)
  })
})
