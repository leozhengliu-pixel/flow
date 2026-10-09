import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { ApiError } from '@/lib/api-client'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { Loop, LoopRun } from '@/types/flow'
import { isPermanentLoadError, pollRetryDelay } from './loop-poll'

const api = vi.hoisted(() => ({
  cancelLoopRun: vi.fn(),
  getLoop: vi.fn(),
  getLoopRun: vi.fn(),
  listLoopRuns: vi.fn(),
  rateLoopRun: vi.fn(),
  replyToLoopRun: vi.fn(),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))

import { LoopRunPage } from './loop-run-page'

const loop: Loop = {
  id: 'loop-1', name: 'Weekly wrap', status: 'published', level: 'workspace', triggerType: 'schedule', triggerConfig: { unit: 'week' },
  instructions: 'Summarize the week.', connectorIds: [], teamAccess: 'allPublic', allowChangesOutsideTrigger: false, allowExternalSync: false,
  enabled: true, creator: viewer, createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-29T00:00:00Z',
}
const completed: LoopRun = {
  id: 'run-1', loopId: 'loop-1', status: 'completed', trigger: 'manual', triggerLabel: 'Manual run', output: 'Posted the summary.',
  startedAt: '2026-09-29T00:00:00Z', finishedAt: '2026-09-29T00:00:05Z',
}
const bad = () => new ApiError('Request failed: 502', 502)

function renderPage() {
  render(<I18nProvider><LoopRunPage data={makeBootstrap({ loops: [loop] })} loopId="loop-1" onNavigate={vi.fn()} onOpenSidebar={vi.fn()}/></I18nProvider>)
}
/** Lets pending promise callbacks run, then advances the fake clock. */
async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms)
  })
}

describe('loop poll backoff', () => {
  it('backs off 1s, 2s, 4s… capped at 15s', () => {
    expect([1, 2, 3, 4, 5, 6, 10].map(pollRetryDelay)).toEqual([1000, 2000, 4000, 8000, 15000, 15000, 15000])
  })

  it('treats 5xx and network errors as transient, 4xx as permanent', () => {
    expect(isPermanentLoadError(new ApiError('x', 502))).toBe(false)
    expect(isPermanentLoadError(new ApiError('x', 429))).toBe(false)
    expect(isPermanentLoadError(new TypeError('Failed to fetch'))).toBe(false)
    expect(isPermanentLoadError(new ApiError('x', 404))).toBe(true)
  })
})

describe('LoopRunPage recovers from a failed poll', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    localStorage.clear()
    api.getLoop.mockReset().mockResolvedValue(loop)
    api.listLoopRuns.mockReset()
    api.getLoopRun.mockReset().mockResolvedValue(completed)
  })
  afterEach(() => {
    cleanup()
    vi.useRealTimers()
  })

  it('retries a 502 on the run list without reopening the page', async () => {
    api.listLoopRuns.mockRejectedValueOnce(bad()).mockRejectedValueOnce(bad()).mockResolvedValue([completed])
    renderPage()
    await advance(0)
    expect(screen.getByText('Could not load loop runs. Retrying…')).toBeInTheDocument()
    expect(screen.queryByText(/Request failed/)).not.toBeInTheDocument()
    expect(api.listLoopRuns).toHaveBeenCalledTimes(1)

    await advance(1000)
    expect(api.listLoopRuns).toHaveBeenCalledTimes(2)
    // The second failure waits twice as long.
    await advance(1500)
    expect(api.listLoopRuns).toHaveBeenCalledTimes(2)
    await advance(500)
    expect(api.listLoopRuns).toHaveBeenCalledTimes(3)

    expect(screen.getByRole('button', { name: /Manual run/ })).toBeInTheDocument()
    expect(screen.queryByText('Could not load loop runs. Retrying…')).not.toBeInTheDocument()
    await advance(30000)
    expect(api.listLoopRuns).toHaveBeenCalledTimes(3)
  })

  it('keeps the loaded runs and shows a transient notice while the poll fails, then clears it', async () => {
    const running: LoopRun = { ...completed, id: 'run-2', status: 'running', finishedAt: undefined, startedAt: '2026-09-29T01:00:00Z' }
    api.getLoopRun.mockImplementation(async (_loop: string, id: string) => (id === 'run-2' ? running : completed))
    api.listLoopRuns.mockResolvedValueOnce([running, completed]).mockRejectedValueOnce(bad()).mockResolvedValue([{ ...running, status: 'completed' }, completed])
    renderPage()
    await advance(0)
    expect(screen.getAllByRole('button', { name: /Manual run/ })).toHaveLength(2)

    // The next 1.5s poll fails: the rows stay and a notice shows.
    await advance(1500)
    expect(screen.getAllByRole('button', { name: /Manual run/ })).toHaveLength(2)
    expect(screen.getByText('Connection lost. Retrying…')).toBeInTheDocument()

    // The backoff retry succeeds and the notice goes away.
    await advance(1000)
    expect(screen.queryByText('Connection lost. Retrying…')).not.toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: /Manual run/ })).toHaveLength(2)
  })

  it('retries the selected run when its first load fails', async () => {
    api.listLoopRuns.mockResolvedValue([completed])
    api.getLoopRun.mockRejectedValueOnce(bad()).mockResolvedValue(completed)
    renderPage()
    await advance(0)
    await advance(1000)
    expect(api.getLoopRun).toHaveBeenCalledTimes(2)
    expect(screen.getByText('Posted the summary.')).toBeInTheDocument()
  })

  it('does not report the loop missing when loading it hits a 502', async () => {
    api.listLoopRuns.mockResolvedValue([])
    api.getLoop.mockRejectedValueOnce(bad()).mockResolvedValue(loop)
    cleanup()
    render(<I18nProvider><LoopRunPage data={makeBootstrap({ loops: [] })} loopId="loop-1" onNavigate={vi.fn()} onOpenSidebar={vi.fn()}/></I18nProvider>)
    await advance(0)
    expect(screen.queryByText('Loop not found')).not.toBeInTheDocument()
    await advance(1000)
    expect(api.getLoop).toHaveBeenCalledTimes(2)
    expect(screen.queryByText('Loop not found')).not.toBeInTheDocument()
  })

  it('still reports a loop that is really gone', async () => {
    api.listLoopRuns.mockResolvedValue([])
    api.getLoop.mockRejectedValue(new ApiError('not found', 404))
    render(<I18nProvider><LoopRunPage data={makeBootstrap({ loops: [] })} loopId="loop-1" onNavigate={vi.fn()} onOpenSidebar={vi.fn()}/></I18nProvider>)
    await advance(0)
    expect(screen.getByRole('heading', { name: 'Loop not found' })).toBeInTheDocument()
  })
})
