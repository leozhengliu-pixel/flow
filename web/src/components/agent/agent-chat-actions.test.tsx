import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { AgentSession } from '@/types/flow'

const api = vi.hoisted(() => ({
  createAgentSession: vi.fn(), deleteAgentSession: vi.fn(), restoreAgentSession: vi.fn(), createAgentSessionFeedback: vi.fn(),
  fetchAgentStatus: vi.fn(), getAgentSession: vi.fn(), markAgentSessionRead: vi.fn(), stopAgentSession: vi.fn(), updateAgentSession: vi.fn(), updateAgentMessageFeedback: vi.fn(), uploadAgentFile: vi.fn(),
}))
const streams = vi.hoisted(() => ({ streamNewAgentSession: vi.fn(), streamAgentSessionMessage: vi.fn(), streamAgentSessionMessageEdit: vi.fn() }))
const toasts = vi.hoisted(() => ({ toast: { info: vi.fn(), success: vi.fn(), error: vi.fn() } }))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/agent-stream', () => streams)
vi.mock('@/lib/toast', () => toasts)
const platform = vi.hoisted(() => ({ mac: false }))
vi.mock('@/components/project-detail/project-detail-shortcuts', async importOriginal => ({ ...await importOriginal<object>(), isMacPlatform: () => platform.mac }))

import { AgentPage } from './agent-page'
import { AgentChatPanel } from './agent-chat-panel'

const session: AgentSession = {
  id: 'session-1', slugId: 'chat', userId: 'user-1', title: 'Launch plan', favorite: false, location: 'page', issueIds: [], skillIds: [],
  messages: [{ id: 'm1', role: 'user', content: 'Hi', createdAt: '2026-10-12T00:00:00Z' }, { id: 'm2', role: 'assistant', content: 'Hello', createdAt: '2026-10-12T00:00:01Z' }],
  createdAt: '2026-10-12T00:00:00Z', updatedAt: '2026-10-12T00:00:01Z',
}
const menuLabels = ['Rename…ShiftR', 'Copy as markdown', 'Leave feedback…', 'DeleteCtrlDelete']
const deleteKey = { key: 'Delete', code: 'Delete', ctrlKey: true }
const renameKey = { key: 'R', code: 'KeyR', shiftKey: true }

function renderPage(props: { onNavigate?: () => void; onSessionChange?: () => void } = {}) {
  const onNavigate = vi.fn(), onSessionChange = vi.fn()
  const data = makeBootstrap({ agentSessions: [session], agentSkills: [] })
  const page = (chatSlug: string) => <I18nProvider><AgentPage chatSlug={chatSlug} data={data} onNavigate={onNavigate} onOpenSidebar={vi.fn()} onSessionChange={onSessionChange} {...props}/></I18nProvider>
  const { rerender } = render(page('chat'))
  // What the router does after onNavigate: the page gets the new chat slug.
  return { onNavigate, onSessionChange, navigateTo: (chatSlug: string) => rerender(page(chatSlug)) }
}
function renderPanel() {
  const handlers = { onClose: vi.fn(), onSessionChange: vi.fn(), onSessionDeleted: vi.fn(), onSessionRestored: vi.fn() }
  render(<I18nProvider><AgentChatPanel initialSession={{ ...session, location: 'toolbar' }} issues={[]} open {...handlers}/></I18nProvider>)
  return handlers
}
const undo = () => {
  const [, options] = toasts.toast.info.mock.calls.find(([title]) => title === 'Chat deleted')!
  expect(options.button.label).toBe('Undo')
  return options.button.onClick as () => void
}

beforeEach(() => {
  platform.mac = false
  Object.values(api).forEach(mock => mock.mockReset())
  Object.values(streams).forEach(mock => mock.mockReset())
  Object.values(toasts.toast).forEach(mock => mock.mockReset())
  api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'test' })
  api.getAgentSession.mockResolvedValue(session)
  api.deleteAgentSession.mockResolvedValue(undefined)
  api.restoreAgentSession.mockResolvedValue(session)
  // The server re-derives the slug from the new title.
  api.updateAgentSession.mockImplementation(async (_id, input) => ({ ...session, ...input, ...(input.title ? { slugId: `${input.title.toLowerCase()}-abc` } : {}) }))
})

describe('chat options on the Agent page', () => {
  it('lists the same four items as the reference app', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'Chat options' }))
    expect(within(await screen.findByRole('menu')).getAllByRole('menuitem').map(item => item.textContent)).toEqual(menuLabels)
  })

  it('deletes at once without a confirmation, goes to a new chat and Undo restores and reopens it', async () => {
    const user = userEvent.setup()
    const { onNavigate, onSessionChange } = renderPage()
    await user.click(await screen.findByRole('button', { name: 'Chat options' }))
    await user.click(screen.getByRole('menuitem', { name: /Delete/ }))
    expect(screen.queryByRole('dialog', { name: 'Delete chat' })).not.toBeInTheDocument()
    await waitFor(() => expect(api.deleteAgentSession).toHaveBeenCalledWith('session-1'))
    await waitFor(() => expect(onSessionChange).toHaveBeenCalledWith('session-1'))
    expect(onNavigate).toHaveBeenLastCalledWith('/workspace/agent')
    undo()()
    await waitFor(() => expect(api.restoreAgentSession).toHaveBeenCalledWith('session-1'))
    await waitFor(() => expect(onNavigate).toHaveBeenLastCalledWith('/workspace/agent/chat'))
    expect(onSessionChange).toHaveBeenCalledWith('session-1', expect.objectContaining({ id: 'session-1' }))
  })

  it('shows the error toast and keeps the chat when deleting fails', async () => {
    api.deleteAgentSession.mockRejectedValue(new Error('nope'))
    const user = userEvent.setup()
    const { onSessionChange } = renderPage()
    await user.click(await screen.findByRole('button', { name: 'Chat options' }))
    await user.click(screen.getByRole('menuitem', { name: /Delete/ }))
    await waitFor(() => expect(toasts.toast.error).toHaveBeenCalledWith('nope'))
    expect(onSessionChange).not.toHaveBeenCalled()
  })

  it('renames through the command palette: prefilled, Enter submits, empty cannot, Cancel closes', async () => {
    const user = userEvent.setup()
    const { onNavigate, onSessionChange, navigateTo } = renderPage()
    await user.click(await screen.findByRole('button', { name: 'Chat options' }))
    await user.click(screen.getByRole('menuitem', { name: /Rename/ }))
    const input = await screen.findByRole('combobox')
    expect(input).toHaveValue('Launch plan')
    expect(input).toHaveAttribute('placeholder', 'Filter…')
    expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual(['Rename chat to "Launch plan"', 'Cancel'])
    await user.clear(input)
    expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual(['Cancel'])
    await user.keyboard('{Enter}')
    expect(screen.getByRole('combobox')).toBeInTheDocument()
    expect(api.updateAgentSession).not.toHaveBeenCalled()
    await user.type(input, 'Roadmap')
    expect(screen.getAllByRole('option')[0]).toHaveTextContent('Rename chat to "Roadmap"')
    await user.keyboard('{Enter}')
    await waitFor(() => expect(api.updateAgentSession).toHaveBeenCalledWith('session-1', { title: 'Roadmap' }))
    await waitFor(() => expect(onSessionChange).toHaveBeenCalledWith('session-1', expect.objectContaining({ title: 'Roadmap', slugId: 'roadmap-abc' })))
    // The address follows the new slug, so the renamed chat stays open.
    expect(onNavigate).toHaveBeenLastCalledWith(expect.stringMatching(/\/agent\/roadmap-abc$/))
    navigateTo('roadmap-abc')
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
    expect(await screen.findByRole('heading', { name: 'Roadmap' })).toBeVisible()

    fireEvent.keyDown(document.body, renameKey)
    await user.click(await screen.findByRole('option', { name: 'Cancel' }))
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
    fireEvent.keyDown(document.body, renameKey)
    await screen.findByRole('combobox')
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
    expect(api.updateAgentSession).toHaveBeenCalledTimes(1)
  })

  it('wires Shift R and Ctrl Delete, but not while typing in the composer', async () => {
    renderPage()
    const composer = await screen.findByRole('textbox', { name: /Send a message/ })
    fireEvent.keyDown(composer, renameKey)
    fireEvent.keyDown(composer, deleteKey)
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
    expect(api.deleteAgentSession).not.toHaveBeenCalled()
    fireEvent.keyDown(document.body, deleteKey)
    await waitFor(() => expect(api.deleteAgentSession).toHaveBeenCalledWith('session-1'))
  })
})

describe('chat options in the floating chat', () => {
  it('has the identical menu', async () => {
    const user = userEvent.setup()
    renderPanel()
    await user.click(await screen.findByRole('button', { name: 'Chat options' }))
    expect(within(await screen.findByRole('menu')).getAllByRole('menuitem').map(item => item.textContent)).toEqual(menuLabels)
  })

  it('opens the feedback dialog from the menu', async () => {
    const user = userEvent.setup()
    renderPanel()
    await user.click(await screen.findByRole('button', { name: 'Chat options' }))
    await user.click(screen.getByRole('menuitem', { name: 'Leave feedback…' }))
    expect(await screen.findByRole('dialog', { name: 'Leave feedback' })).toBeVisible()
  })

  it('deletes at once, closes the panel, and Undo hands the restored chat back to the host', async () => {
    const user = userEvent.setup()
    const handlers = renderPanel()
    await user.click(await screen.findByRole('button', { name: 'Chat options' }))
    await user.click(screen.getByRole('menuitem', { name: /Delete/ }))
    await waitFor(() => expect(handlers.onClose).toHaveBeenCalled())
    expect(handlers.onSessionDeleted).toHaveBeenCalledWith('session-1')
    undo()()
    await waitFor(() => expect(handlers.onSessionRestored).toHaveBeenCalledWith(expect.objectContaining({ id: 'session-1' })))
  })

  it('renames from the palette and from Shift R, and Ctrl Delete deletes when not typing', async () => {
    const user = userEvent.setup()
    const handlers = renderPanel()
    await screen.findByRole('button', { name: 'Chat options' })
    fireEvent.keyDown(screen.getByRole('button', { name: 'Chat options' }), renameKey)
    const input = await screen.findByRole('combobox')
    expect(input).toHaveValue('Launch plan')
    await user.type(input, ' v2{Enter}')
    await waitFor(() => expect(api.updateAgentSession).toHaveBeenCalledWith('session-1', { title: 'Launch plan v2' }))
    await waitFor(() => expect(handlers.onSessionChange).toHaveBeenCalledWith(expect.objectContaining({ title: 'Launch plan v2' })))
    fireEvent.keyDown(await screen.findByRole('textbox', { name: /Send a message/ }), deleteKey)
    expect(api.deleteAgentSession).not.toHaveBeenCalled()
    fireEvent.keyDown(screen.getByRole('button', { name: 'Chat options' }), deleteKey)
    await waitFor(() => expect(api.deleteAgentSession).toHaveBeenCalledWith('session-1'))
  })
})

describe('hotkey guards', () => {
  it('shows the Command Backspace label and deletes on Meta Backspace on macOS', async () => {
    platform.mac = true
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'Chat options' }))
    expect(screen.getByRole('menuitem', { name: /Delete/ })).toHaveTextContent('⌘⌫')
    await user.keyboard('{Escape}')
    fireEvent.keyDown(document.body, { key: 'Backspace', code: 'Backspace', metaKey: true })
    await waitFor(() => expect(api.deleteAgentSession).toHaveBeenCalledWith('session-1'))
  })

  it('does nothing while a menu or dialog is open', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'Chat options' }))
    await screen.findByRole('menu')
    fireEvent.keyDown(document.body, deleteKey)
    expect(api.deleteAgentSession).not.toHaveBeenCalled()
    await user.click(screen.getByRole('menuitem', { name: 'Leave feedback…' }))
    await screen.findByRole('dialog', { name: 'Leave feedback' })
    fireEvent.keyDown(document.body, deleteKey)
    expect(api.deleteAgentSession).not.toHaveBeenCalled()
  })

  it('keeps the floating chat hotkeys inside the panel', async () => {
    const user = userEvent.setup()
    render(<button type="button">Outside chat</button>)
    renderPanel()
    await screen.findByRole('button', { name: 'Chat options' })
    // Move focus outside explicitly: the composer may autofocus while the panel loads.
    const outside = screen.getByRole('button', { name: 'Outside chat' })
    await user.click(outside)
    expect(outside).toHaveFocus()
    await user.keyboard('{Control>}{Delete}{/Control}')
    expect(api.deleteAgentSession).not.toHaveBeenCalled()
    screen.getByRole('button', { name: 'Chat options' }).focus()
    await user.keyboard('{Control>}{Delete}{/Control}')
    await waitFor(() => expect(api.deleteAgentSession).toHaveBeenCalledWith('session-1'))
  })
})
