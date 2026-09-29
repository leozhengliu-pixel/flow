import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { Loop, LoopRun } from '@/types/flow'

const api = vi.hoisted(() => ({
  getLoop: vi.fn(),
  getLoopRun: vi.fn(),
  listLoopRuns: vi.fn(),
  rateLoopRun: vi.fn(),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))

import { LoopRunPage } from './loop-run-page'

const loop: Loop = {
  id: 'loop-1', name: 'Weekly wrap', status: 'published', level: 'workspace', triggerType: 'schedule', triggerConfig: { unit: 'week' },
  instructions: 'Summarize the week.', connectorIds: [], teamAccess: 'allPublic', allowChangesOutsideTrigger: false, allowExternalSync: false,
  enabled: true, creator: viewer, createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-29T00:00:00Z',
}
const now = new Date()
const completed: LoopRun = {
  id: 'run-1', loopId: 'loop-1', status: 'completed', trigger: 'manual', triggerLabel: 'Manual run', output: 'Posted the **weekly** summary.',
  steps: [{ order: 0, title: 'Reviewing issues', at: now.toISOString() }],
  toolCalls: [{ order: 1, name: 'search_issues', label: 'Searched issues', args: 'completed this week', status: 'completed' }],
  startedAt: new Date(now.getTime() - 5000).toISOString(), finishedAt: now.toISOString(),
}
const failed: LoopRun = { id: 'run-2', loopId: 'loop-1', status: 'failed', trigger: 'schedule', triggerLabel: 'Scheduled run', error: 'AI credits are not set up', startedAt: new Date(now.getTime() - 86400000).toISOString(), finishedAt: new Date(now.getTime() - 86399000).toISOString() }
const running: LoopRun = { id: 'run-3', loopId: 'loop-1', status: 'running', trigger: 'manual', triggerLabel: 'Manual run', steps: [{ order: 0, title: 'Reviewing issues', at: now.toISOString() }], startedAt: now.toISOString() }

function renderPage(runId?: string) {
  const onNavigate = vi.fn()
  render(<I18nProvider><LoopRunPage data={makeBootstrap({ loops: [loop] })} loopId="loop-1" runId={runId} onNavigate={onNavigate} onOpenSidebar={vi.fn()}/></I18nProvider>)
  return { onNavigate }
}

describe('LoopRunPage', () => {
  beforeEach(() => {
    api.getLoop.mockReset().mockResolvedValue(loop)
    api.listLoopRuns.mockReset().mockResolvedValue([completed, failed])
    api.getLoopRun.mockReset().mockImplementation(async (_loop: string, id: string) => [completed, failed, running].find(run => run.id === id))
    api.rateLoopRun.mockReset()
    localStorage.clear()
  })

  it('lists runs and shows the latest run transcript and answer', async () => {
    const user = userEvent.setup()
    const { onNavigate } = renderPage()
    expect(await screen.findByRole('button', { name: /Scheduled run/ })).toBeVisible()
    expect(screen.getByPlaceholderText('Search runs…')).toBeVisible()
    expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent(/^Today at/)
    expect(screen.getByRole('button', { name: 'Edit loop' })).toBeVisible()
    expect(screen.getByText('Instructions')).toBeVisible()
    expect(await screen.findByText('Worked for 5 seconds')).toBeVisible()
    expect(screen.getByText('Searched issues')).toBeInTheDocument()
    expect(screen.getByText('completed this week')).toBeInTheDocument()
    expect(screen.getByText('Reviewing issues')).toBeInTheDocument()
    expect(screen.getByText('weekly')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Good response' })).toBeVisible()
    await user.click(screen.getByRole('button', { name: /Scheduled run/ }))
    expect(onNavigate).toHaveBeenCalledWith('/workspace/loop/loop-1/run/run-2')
  })

  it('shows the error card for a failed run', async () => {
    renderPage('run-2')
    expect(await screen.findByText("Loop couldn't run")).toBeVisible()
    expect(screen.getByText('AI credits are not set up')).toBeVisible()
  })

  it('polls a running run until it finishes', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      let calls = 0
      api.listLoopRuns.mockResolvedValue([running])
      api.getLoopRun.mockImplementation(async () => (++calls > 1 ? { ...running, status: 'completed', output: 'All done.', finishedAt: new Date().toISOString() } : running))
      renderPage('run-3')
      expect(await screen.findByText('Reviewing issues…')).toBeVisible()
      await vi.advanceTimersByTimeAsync(1600)
      await waitFor(() => expect(screen.getByText('All done.')).toBeVisible())
    } finally {
      vi.useRealTimers()
    }
  })

  it('filters runs by search', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByRole('button', { name: /Scheduled run/ })
    await user.type(screen.getByPlaceholderText('Search runs…'), 'scheduled')
    expect(screen.queryByRole('button', { name: /Manual run/ })).toBeNull()
    expect(screen.getByRole('button', { name: /Scheduled run/ })).toBeVisible()
  })

  it('persists 👍 👎 through the feedback endpoint and reflects viewerRating', async () => {
    const user = userEvent.setup()
    api.getLoopRun.mockImplementation(async (_loop: string, id: string) => (id === 'run-1' ? { ...completed, viewerRating: 'down' } : failed))
    api.rateLoopRun.mockImplementation(async (_loop: string, _run: string, rating: 'up' | 'down' | null) => ({ ...completed, viewerRating: rating }))
    renderPage('run-1')
    const down = await screen.findByRole('button', { name: 'Bad response' })
    await waitFor(() => expect(down).toHaveAttribute('aria-pressed', 'true'))
    await user.click(screen.getByRole('button', { name: 'Good response' }))
    expect(api.rateLoopRun).toHaveBeenLastCalledWith('loop-1', 'run-1', 'up')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Good response' })).toHaveAttribute('aria-pressed', 'true'))
    expect(down).toHaveAttribute('aria-pressed', 'false')
    // Clicking the active rating clears it.
    await user.click(screen.getByRole('button', { name: 'Good response' }))
    expect(api.rateLoopRun).toHaveBeenLastCalledWith('loop-1', 'run-1', null)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Good response' })).toHaveAttribute('aria-pressed', 'false'))
    expect(Object.keys(localStorage).filter(key => key.includes('loop-run-feedback'))).toEqual([])
  })

  it('rolls back a rating the server rejects', async () => {
    const user = userEvent.setup()
    api.rateLoopRun.mockRejectedValue(new Error('nope'))
    renderPage('run-1')
    const up = await screen.findByRole('button', { name: 'Good response' })
    await user.click(up)
    await waitFor(() => expect(api.rateLoopRun).toHaveBeenCalled())
    await waitFor(() => expect(up).toHaveAttribute('aria-pressed', 'false'))
  })

  it('shows the version a run used and server notices', async () => {
    const user = userEvent.setup()
    const run = { ...completed, version: 3, versionId: 'loop-1_v3', notices: ['Web search is enabled for this loop, but no web search provider is configured on this server.'] }
    api.listLoopRuns.mockResolvedValue([run])
    api.getLoopRun.mockResolvedValue(run)
    const { onNavigate } = renderPage('run-1')
    const link = await screen.findByRole('link', { name: /Ran version 3/ })
    expect(screen.getByRole('note')).toHaveTextContent('no web search provider is configured')
    await user.click(link)
    expect(onNavigate).toHaveBeenCalledWith('/workspace/loop/loop-1?versions=1')
  })
})
