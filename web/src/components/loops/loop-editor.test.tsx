import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { AgentSession, Loop } from '@/types/flow'
import type { AgentStreamEvent } from '@/lib/agent-stream'

const api = vi.hoisted(() => ({
  getLoop: vi.fn(),
  updateLoop: vi.fn(),
  deleteLoop: vi.fn(),
  fetchAgentStatus: vi.fn(),
  listAgentSessions: vi.fn(),
  getLoopConfig: vi.fn(),
}))
const stream = vi.hoisted(() => ({ streamNewAgentSession: vi.fn(), streamAgentSessionMessage: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))
vi.mock('@/lib/agent-stream', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/agent-stream')>()), ...stream }))

import { LoopEditor } from './loop-editor'
import { markLoopAgentAutostart, resetLoopConfigCache } from './loop-data'

const draft: Loop = {
  id: 'loop-9', name: 'Triage agent', status: 'draft', templateId: 'triage-agent', level: 'team', teamId: 'team-1', triggerType: 'issue',
  triggerConfig: { event: 'triage' }, instructions: 'Route incoming issues.', connectorIds: [], teamAccess: 'allPublic',
  allowChangesOutsideTrigger: true, allowExternalSync: false, webSearch: false, codeAccess: 'read', enabled: false, creator: viewer,
  createdAt: '2026-09-29T00:00:00Z', updatedAt: '2026-09-29T00:00:00Z',
}

function renderEditor(loop: Loop = draft) {
  api.getLoop.mockResolvedValue(loop)
  const onNavigate = vi.fn()
  const data = makeBootstrap({ loops: [loop], drafts: [], favorites: [], integrationConnections: [], workspaceSettings: { ...makeBootstrap().workspaceSettings, trustedSourcesMode: 'none', trustedSourcesAllowlist: [] } })
  const view = render(<I18nProvider><LoopEditor data={data} draftId={loop.status === 'draft' ? loop.id : undefined} loopId={loop.status === 'draft' ? undefined : loop.id} onNavigate={onNavigate} onOpenSidebar={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>)
  return { onNavigate, view }
}

describe('LoopEditor', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    for (const mock of [...Object.values(api), ...Object.values(stream)]) mock.mockReset()
    api.fetchAgentStatus.mockResolvedValue({ enabled: true })
    api.listAgentSessions.mockResolvedValue([])
    resetLoopConfigCache()
    api.getLoopConfig.mockResolvedValue({ webSearchAvailable: true, webSearchProvider: 'tavily' })
    api.updateLoop.mockImplementation(async (id: string, input: Partial<Loop>) => ({ ...draft, ...input, id }))
  })

  it('lays out the name row, trigger, instructions, connectors, permissions and footer like Linear', async () => {
    const { view } = renderEditor({ ...draft, templateId: undefined })
    const heading = view.container.querySelector('.loops-editor-heading')!
    expect(within(heading as HTMLElement).getByRole('textbox', { name: 'Loop name' })).toHaveValue('Triage agent')
    expect(within(heading as HTMLElement).getByRole('button', { name: 'Loop level' })).toHaveTextContent('Test team')
    expect(heading.querySelector('h3')).toBeNull()
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('Loops›New loop')
    expect(screen.getByRole('region', { name: 'Trigger' })).toHaveTextContent('An issueis in triage')
    expect(screen.getByRole('button', { name: 'Compose with Agent' })).toBeVisible()
    expect(await screen.findByRole('textbox', { name: 'Instructions' })).toHaveTextContent('Route incoming issues.')
    expect(screen.getByText('No connectors added')).toBeVisible()
    const permissions = screen.getByRole('region', { name: 'Permissions' })
    expect(permissions).toHaveTextContent("Choose which team's data are available to this loop")
    expect(within(permissions).getByRole('button', { name: 'Team access' })).toHaveTextContent('All public teams')
    expect(within(permissions).getByRole('checkbox', { name: 'Allow changes outside triggering issue' })).toBeChecked()
    expect(within(permissions).getByRole('checkbox', { name: 'Web search' })).not.toBeChecked()
    expect(within(permissions).getByRole('combobox', { name: 'Access code' })).toHaveTextContent('Read')
    expect(within(permissions).getByRole('checkbox', { name: 'Allow changes to externally synced issues and comments' })).not.toBeChecked()
    // Trusted sources live in the workspace Loops settings, like Linear; the editor only links there.
    expect(permissions.querySelector('.automation-trusted-source-editor, [data-testid="automation-trusted-source-editor"]')).toBeNull()
    expect(within(permissions).queryByRole('checkbox', { name: /trusted/i })).toBeNull()
    expect(within(permissions).getAllByRole('link', { name: 'Loops settings' }).length).toBeGreaterThan(0)
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Create loop' })).toBeEnabled()
    // No agent panel for a scratch draft until asked.
    expect(screen.queryByRole('complementary', { name: 'Loop agent' })).toBeNull()
  })

  it('keeps the web search toggle but explains when web search is not configured', async () => {
    const user = userEvent.setup()
    api.getLoopConfig.mockResolvedValue({ webSearchAvailable: false, webSearchProvider: '' })
    const { onNavigate } = renderEditor({ ...draft, templateId: undefined })
    const permissions = screen.getByRole('region', { name: 'Permissions' })
    expect(await within(permissions).findByRole('note')).toHaveTextContent("Web search isn't configured for this workspace.")
    const toggle = within(permissions).getByRole('checkbox', { name: 'Web search' })
    expect(toggle).toBeEnabled()
    await user.click(within(permissions).getByRole('link', { name: 'Learn more' }))
    expect(onNavigate).toHaveBeenCalledWith('/workspace/settings/loops#web-search')
  })

  it('shows no web search hint when a provider is configured', async () => {
    renderEditor({ ...draft, templateId: undefined })
    await waitFor(() => expect(api.getLoopConfig).toHaveBeenCalled())
    expect(within(screen.getByRole('region', { name: 'Permissions' })).queryByRole('note')).toBeNull()
  })

  it('uses the trigger placeholder for empty instructions', async () => {
    const { view } = renderEditor({ ...draft, templateId: undefined, instructions: '' })
    await screen.findByRole('textbox', { name: 'Instructions' })
    await waitFor(() => expect(view.container.querySelector('.loops-instructions-editor [data-placeholder]')).toHaveAttribute('data-placeholder', "For example, review the issue's changes, check for blockers, and suggest next steps…"))
  })

  it('publishes a draft with Create loop and opens the loop page', async () => {
    const user = userEvent.setup()
    const { onNavigate } = renderEditor({ ...draft, templateId: undefined })
    await user.click(screen.getByRole('button', { name: 'Create loop' }))
    await waitFor(() => expect(api.updateLoop).toHaveBeenCalledWith('loop-9', expect.objectContaining({ status: 'published', enabled: true, name: 'Triage agent', teamId: 'team-1' })))
    expect(onNavigate).toHaveBeenCalledWith('/workspace/loop/loop-9')
  })

  it('opens the loop builder for a template draft, sends the first message and applies save_loop edits', async () => {
    markLoopAgentAutostart('loop-9')
    const session = { id: 'session-1', title: 'Triage agent', messages: [], loopIds: ['loop-9'], updatedAt: '2026-09-29T00:00:00Z' } as unknown as AgentSession
    stream.streamNewAgentSession.mockImplementation(async (_input: unknown, onEvent: (event: AgentStreamEvent) => void) => {
      api.getLoop.mockResolvedValue({ ...draft, name: 'Triage agent (routes only)', instructions: 'Route, but do not close.' })
      onEvent({ type: 'tool.completed', part: { id: 'part-1', type: 'toolCall', toolCall: { id: 'call-1', name: 'save_loop', title: 'Updated workflow definition draft', status: 'completed', result: { id: 'loop-9', name: 'Triage agent (routes only)', status: 'draft' } } } })
      return session
    })
    renderEditor()
    expect(await screen.findByRole('complementary', { name: 'Loop agent' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Configure with Agent' })).toBeVisible()
    await waitFor(() => expect(stream.streamNewAgentSession).toHaveBeenCalledWith({ message: 'Set up this loop from the Triage agent template', loopIds: ['loop-9'], location: 'toolbar' }, expect.any(Function), expect.any(AbortSignal)))
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Loop name' })).toHaveValue('Triage agent (routes only)'))
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Instructions' })).toHaveTextContent('Route, but do not close.'))
  })

  it('navigates to the loop page when the agent publishes', async () => {
    markLoopAgentAutostart('loop-9')
    stream.streamNewAgentSession.mockImplementation(async (_input: unknown, onEvent: (event: AgentStreamEvent) => void) => {
      api.getLoop.mockResolvedValue({ ...draft, status: 'published', enabled: true })
      onEvent({ type: 'tool.completed', part: { id: 'part-2', type: 'toolCall', toolCall: { id: 'call-2', name: 'save_loop', title: 'Created automation', status: 'completed', result: { id: 'loop-9', status: 'published', published: true } } } })
      return undefined
    })
    const { onNavigate } = renderEditor()
    await waitFor(() => expect(onNavigate).toHaveBeenCalledWith('/workspace/loop/loop-9'))
  })
})
