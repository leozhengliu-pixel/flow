import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, viewer } from '@/test/fixtures'
import type { Loop } from '@/types/flow'

const api = vi.hoisted(() => ({
  getLoop: vi.fn(),
  runLoopNow: vi.fn(),
  updateLoop: vi.fn(),
  fetchAgentStatus: vi.fn(),
  listAgentSessions: vi.fn(),
  getAgentSession: vi.fn(),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))

import { LoopDetail } from './loop-detail'
import { RunLoopOnPicker } from './loop-pickers'
import { markLoopAgentHandoff } from './loop-data'

const baseLoop: Loop = {
  id: 'loop-1', name: 'Triage agent', description: 'Routes incoming issues to the right owner.', status: 'published', level: 'team', teamId: 'team-1',
  triggerType: 'issue', triggerConfig: { event: 'triage', filters: [{ field: 'assignee', operator: 'is', value: null }] }, instructions: 'Route every issue.',
  connectorIds: [], teamAccess: 'allPublic', allowChangesOutsideTrigger: true, allowExternalSync: false, enabled: true, ownerId: viewer.id,
  creator: viewer, runCount30d: 3, createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-29T00:00:00Z',
}

function renderDetail(loop: Loop = baseLoop) {
  api.getLoop.mockResolvedValue(loop)
  const onNavigate = vi.fn()
  const data = makeBootstrap({ loops: [loop], favorites: [], drafts: [] })
  render(<I18nProvider><LoopDetail data={data} loopId={loop.id} onNavigate={onNavigate} onOpenSidebar={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>)
  return { onNavigate }
}

describe('LoopDetail', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    api.getLoop.mockReset()
    api.runLoopNow.mockReset().mockResolvedValue({ id: 'run-7' })
    api.updateLoop.mockReset()
    api.fetchAgentStatus.mockReset().mockResolvedValue({ enabled: true })
    api.listAgentSessions.mockReset().mockResolvedValue([])
    api.getAgentSession.mockReset()
    sessionStorage.clear()
  })

  it('docks the loop builder after it published the loop and follows the reply still running on the server', async () => {
    const setup = { id: 'm-user', role: 'user', content: 'Set up this loop from the Triage agent template', createdAt: '2026-09-29T10:00:00Z' }
    const reply = { id: 'm-reply', role: 'assistant', content: 'The Triage agent loop is live.', durationMs: 4000, createdAt: '2026-09-29T10:00:04Z', parts: [] }
    const session = { id: 'session-1', title: 'Triage agent', loopIds: ['loop-1'], messages: [setup], updatedAt: '2026-09-29T10:00:00Z' }
    api.listAgentSessions.mockResolvedValue([session])
    api.getAgentSession.mockResolvedValue({ ...session, messages: [setup, reply] })
    markLoopAgentHandoff('loop-1')
    renderDetail({ ...baseLoop, templateId: 'triage-agent', icon: 'Triage' })
    const panel = await screen.findByRole('complementary', { name: 'Loop agent' })
    await waitFor(() => expect(panel).toHaveTextContent('The Triage agent loop is live.'), { timeout: 4000 })
    expect(panel).not.toHaveTextContent('Set up this loop from the Triage agent template')
    expect(api.getAgentSession).toHaveBeenCalledWith('session-1')
    // The loop keeps the template's icon on its page.
    expect(document.querySelector('.loops-detail-icon .status-glyph')).not.toBeNull()
  })

  it('opens without the agent panel otherwise', () => {
    renderDetail()
    expect(screen.queryByRole('complementary', { name: 'Loop agent' })).toBeNull()
  })

  it('shows the trigger without a team scope for team loops', () => {
    renderDetail({ ...baseLoop, triggerConfig: { event: 'triage' } })
    const trigger = screen.getByRole('region', { name: 'Trigger' })
    expect(trigger).toHaveTextContent('An issueis in triage')
    expect(trigger).not.toHaveTextContent('All teams')
  })

  it('shows the loop page header, run summary, trigger and instructions', () => {
    renderDetail()
    expect(screen.getByRole('heading', { level: 1, name: 'Triage agent' })).toBeVisible()
    expect(screen.getByText('Routes incoming issues to the right owner.')).toBeVisible()
    expect(screen.getByText('All team members can edit')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Edit' })).toBeVisible()
    expect(screen.getByRole('checkbox', { name: 'Enabled' })).toBeChecked()
    expect(screen.getByText(/Owned by/)).toBeVisible()
    expect(screen.getByRole('button', { name: /Run history/ })).toHaveTextContent('Ran 3 times over the last 30 days')
    expect(screen.getByRole('region', { name: 'Trigger' })).toHaveTextContent('An issueis in triage')
    expect(screen.getByRole('region', { name: 'Trigger' })).toHaveTextContent('No assignee')
    expect(screen.getByRole('region', { name: 'Instructions' })).toHaveTextContent('Route every issue.')
  })

  it('runs an event loop on a picked issue and opens the run page', async () => {
    const user = userEvent.setup()
    const { onNavigate } = renderDetail()
    await user.click(screen.getByRole('button', { name: 'Run now' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByPlaceholderText('Search for issue to run loop on…')).toBeVisible()
    expect(dialog).toHaveTextContent('Run loop on')
    await user.click(within(dialog).getByRole('option', { name: /TST-1/ }))
    await waitFor(() => expect(api.runLoopNow).toHaveBeenCalledWith('loop-1', { entityType: 'issue', entityId: 'issue-1' }))
    expect(onNavigate).toHaveBeenCalledWith('/workspace/loop/loop-1/run/run-7')
  })

  it('runs a schedule loop immediately', async () => {
    const user = userEvent.setup()
    const { onNavigate } = renderDetail({ ...baseLoop, triggerType: 'schedule', triggerConfig: { startDate: '2026-09-29', interval: 1, unit: 'day', time: '07:00' } })
    await user.click(screen.getByRole('button', { name: 'Run now' }))
    await waitFor(() => expect(api.runLoopNow).toHaveBeenCalledWith('loop-1', undefined))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(onNavigate).toHaveBeenCalledWith('/workspace/loop/loop-1/run/run-7')
  })
})

describe('RunLoopOnPicker', () => {
  beforeEach(() => vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} }))

  it('lists recent issues, filters by search and picks one', async () => {
    const user = userEvent.setup()
    const onSelect = vi.fn()
    const data = makeBootstrap({ issues: [makeIssue(), makeIssue({ id: 'issue-2', identifier: 'TST-2', title: 'Login fails', updatedAt: '2026-09-01T00:00:00Z' })] })
    render(<I18nProvider><RunLoopOnPicker data={data} open onOpenChange={vi.fn()} onSelect={onSelect}/></I18nProvider>)
    expect(screen.getByText('Recent issues')).toBeVisible()
    expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual(['TST-2Login fails', 'TST-1Test issue'])
    await user.type(screen.getByPlaceholderText('Search for issue to run loop on…'), 'login')
    expect(screen.getAllByRole('option')).toHaveLength(1)
    await user.keyboard('{Enter}')
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: 'issue-2' }))
  })
})
