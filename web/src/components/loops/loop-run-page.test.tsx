import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { zhCN } from '@/i18n/translations'
import { LOOP_RUN_ACTIVITY_EVENT } from '@/lib/loop-run-activity'
import { REASON_LABELS, STATUS_LABELS } from './loop-run-status'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { Loop, LoopRun } from '@/types/flow'

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
const now = new Date()
const completed: LoopRun = {
  id: 'run-1', loopId: 'loop-1', status: 'completed', trigger: 'manual', triggerLabel: 'Manual run', output: 'Posted the **weekly** summary.',
  steps: [{ order: 0, title: 'Reviewing issues', at: now.toISOString() }],
  toolCalls: [{ order: 1, name: 'search_issues', label: 'Searched issues', args: 'completed this week', status: 'completed' }],
  startedAt: new Date(now.getTime() - 5000).toISOString(), finishedAt: now.toISOString(),
}
const failed: LoopRun = { id: 'run-2', loopId: 'loop-1', status: 'failed', trigger: 'schedule', triggerLabel: 'Scheduled run', error: 'AI credits are not set up', startedAt: new Date(now.getTime() - 86400000).toISOString(), finishedAt: new Date(now.getTime() - 86399000).toISOString() }
const running: LoopRun = { id: 'run-3', loopId: 'loop-1', status: 'running', trigger: 'manual', triggerLabel: 'Manual run', steps: [{ order: 0, title: 'Reviewing issues', at: now.toISOString() }], startedAt: now.toISOString() }

const skill = { id: 'skill-1', userId: viewer.id, name: 'Terse answers', instructions: 'One sentence.', createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-01T00:00:00Z' }

function cleanupAndRender(runId: string) {
  cleanup()
  return renderPage(runId)
}

function renderPage(runId?: string) {
  const onNavigate = vi.fn()
  render(<I18nProvider><LoopRunPage data={makeBootstrap({ loops: [loop], agentSkills: [skill] })} loopId="loop-1" runId={runId} onNavigate={onNavigate} onOpenSidebar={vi.fn()}/></I18nProvider>)
  return { onNavigate }
}

describe('LoopRunPage', () => {
  beforeEach(() => {
    api.getLoop.mockReset().mockResolvedValue(loop)
    api.listLoopRuns.mockReset().mockResolvedValue([completed, failed])
    api.getLoopRun.mockReset().mockImplementation(async (_loop: string, id: string) => [completed, failed, running].find(run => run.id === id))
    api.rateLoopRun.mockReset()
    api.cancelLoopRun.mockReset()
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

  it('moves to the next and previous run with ↓/↑ like Linear', async () => {
    const user = userEvent.setup()
    const { onNavigate } = renderPage('run-1')
    expect(await screen.findByRole('button', { name: /Scheduled run/ })).toBeVisible()
    const next = screen.getByRole('button', { name: 'Go to next run' })
    await waitFor(() => expect(next).toBeEnabled())
    expect(screen.getByRole('button', { name: 'Go to previous run' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Go to previous run' })).toHaveAttribute('title', 'No previous run')
    await user.click(next)
    expect(onNavigate).toHaveBeenLastCalledWith('/workspace/loop/loop-1/run/run-2')
    await user.keyboard('j')
    expect(onNavigate).toHaveBeenLastCalledWith('/workspace/loop/loop-1/run/run-2')
  })

  it('replies to a run and shows the agent answer', async () => {
    const user = userEvent.setup()
    const reply = { id: 'reply-1', userId: viewer.id, body: 'Which bugs?', status: 'completed' as const, output: 'Bugs **A** and B.', createdAt: now.toISOString(), finishedAt: now.toISOString() }
    api.replyToLoopRun.mockResolvedValue({ ...completed, replies: [reply] })
    renderPage('run-1')
    const box = await screen.findByRole('textbox', { name: 'Reply…' })
    expect(screen.getByRole('button', { name: 'Send message' })).toBeDisabled()
    await user.type(box, 'Which bugs?{Enter}')
    await waitFor(() => expect(api.replyToLoopRun).toHaveBeenCalledWith('loop-1', 'run-1', 'Which bugs?', { skillIds: [], attachments: [] }))
    expect(await screen.findByText('Which bugs?')).toBeVisible()
    expect(screen.getByText('A')).toBeVisible()
    expect(box).toHaveValue('')
  })

  it('sends the selected skills and attached files with a reply, like the Agent composer', async () => {
    const user = userEvent.setup()
    const reply = { id: 'reply-1', userId: viewer.id, body: 'Check this', status: 'running' as const, attachments: [{ name: 'notes.md', contentType: 'text/markdown', size: 12 }], createdAt: now.toISOString() }
    api.replyToLoopRun.mockResolvedValue({ ...completed, replies: [reply] })
    const { onNavigate } = renderPage('run-1')
    const box = await screen.findByRole('textbox', { name: 'Reply…' })
    const footer = box.closest('form')!.querySelector('.loops-run-composer-actions')!
    // Linear's layout: Skills on the left, attach then send on the right.
    expect([...footer.querySelectorAll('button')].map(button => button.getAttribute('aria-label'))).toEqual(['Skills', 'Attach images, files, or videos', 'Send message'])

    await user.click(screen.getByRole('button', { name: 'Skills' }))
    await user.click(await screen.findByRole('option', { name: /Terse answers/ }))
    await user.click(screen.getByRole('option', { name: /Create skill/ }))
    expect(onNavigate).toHaveBeenLastCalledWith('/workspace/settings/skill/new')
    await user.keyboard('{Escape}')

    const input = box.closest('form')!.querySelector<HTMLInputElement>('input[type="file"]')!
    expect(input).toHaveAttribute('accept', expect.stringContaining('image/*'))
    expect(input.multiple).toBe(true)
    const big = new File(['x'], 'huge.png', { type: 'image/png' })
    Object.defineProperty(big, 'size', { value: 3 * 1024 * 1024 })
    await user.upload(input, [new File(['# Escalations'], 'notes.md', { type: 'text/markdown' }), new File(['%PDF'], 'deck.pdf', { type: 'application/pdf' }), big])
    expect(screen.getByText('notes.md')).toBeVisible()
    expect(screen.queryByText('huge.png')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Remove attachment deck.pdf' }))
    expect(screen.queryByText('deck.pdf')).not.toBeInTheDocument()

    await user.type(box, 'Check this{Enter}')
    await waitFor(() => expect(api.replyToLoopRun).toHaveBeenCalledWith('loop-1', 'run-1', 'Check this', {
      skillIds: ['skill-1'],
      attachments: [{ name: 'notes.md', contentType: 'text/markdown', size: 13, content: '# Escalations' }],
    }))
    // The sent reply lists its files; the composer is cleared.
    expect(await screen.findByRole('list', { name: 'Attachments' })).toHaveTextContent('notes.md')
    expect(box).toHaveValue('')
    expect(box.closest('form')!.querySelector('.loops-run-composer-attachments')).toBeNull()
  })

  it('cancels a running run and shows it as cancelled', async () => {
    const user = userEvent.setup()
    api.listLoopRuns.mockResolvedValue([running])
    api.getLoopRun.mockResolvedValue(running)
    api.cancelLoopRun.mockResolvedValue({ ...running, status: 'cancelled', failureReason: 'cancelled', error: 'Cancelled', cancelledBy: viewer.id, finishedAt: new Date().toISOString() })
    renderPage('run-3')
    const cancel = await screen.findByRole('button', { name: 'Cancel run' })
    await user.click(cancel)
    expect(api.cancelLoopRun).toHaveBeenCalledWith('loop-1', 'run-3')
    expect(await screen.findByText('Run cancelled')).toBeVisible()
    expect(screen.getByText(`Cancelled by ${viewer.displayName || viewer.name}`)).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Cancel run' })).toBeNull()
    expect(screen.getAllByLabelText('Cancelled').length).toBeGreaterThan(0)
  })

  it('keeps the run when cancelling fails', async () => {
    const user = userEvent.setup()
    api.listLoopRuns.mockResolvedValue([running])
    api.getLoopRun.mockResolvedValue(running)
    api.cancelLoopRun.mockRejectedValue(new Error('This run is not running'))
    renderPage('run-3')
    await user.click(await screen.findByRole('button', { name: 'Cancel run' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Cancel run' })).toBeEnabled())
    expect(screen.queryByText('Run cancelled')).toBeNull()
  })

  it('flags a run that produced no output for review', async () => {
    const review: LoopRun = { ...completed, id: 'run-4', status: 'needs_review', failureReason: 'no_output', error: 'No output produced: the loop\'s instructions call for a project or initiative status update, but the run made none' }
    api.listLoopRuns.mockResolvedValue([review])
    api.getLoopRun.mockResolvedValue(review)
    renderPage('run-4')
    expect(await screen.findByText('Needs review', { selector: 'strong' })).toBeVisible()
    expect(screen.getByText('No output produced')).toBeVisible()
    expect(screen.getByText(/but the run made none/)).toBeVisible()
    expect(screen.getAllByLabelText('Needs review').length).toBeGreaterThan(0)
    expect(screen.queryByRole('button', { name: 'Cancel run' })).toBeNull()
  })

  it('shows interrupted runs and failure reasons', async () => {
    const interrupted: LoopRun = { ...failed, id: 'run-5', status: 'interrupted', failureReason: 'interrupted', error: 'Interrupted by server restart' }
    const timedOut: LoopRun = { ...failed, id: 'run-6', failureReason: 'provider_timeout', error: 'Flow Agent provider is unavailable' }
    api.listLoopRuns.mockResolvedValue([interrupted, timedOut])
    api.getLoopRun.mockImplementation(async (_loop: string, id: string) => [interrupted, timedOut].find(run => run.id === id))
    renderPage('run-5')
    expect(await screen.findByText('Run interrupted')).toBeVisible()
    expect(screen.getAllByLabelText('Interrupted').length).toBeGreaterThan(0)
    cleanupAndRender('run-6')
    expect(await screen.findByText("Loop couldn't run")).toBeVisible()
    expect(screen.getByText('The model provider timed out')).toBeVisible()
    expect(screen.getByText('Flow Agent provider is unavailable')).toBeVisible()
  })

  it('refetches the run when a realtime run signal arrives', async () => {
    api.listLoopRuns.mockResolvedValue([running])
    api.getLoopRun.mockResolvedValue(running)
    renderPage('run-3')
    await screen.findByText('Reviewing issues…')
    api.getLoopRun.mockResolvedValue({ ...running, status: 'completed', output: 'Signal received.', finishedAt: new Date().toISOString() })
    window.dispatchEvent(new CustomEvent(LOOP_RUN_ACTIVITY_EVENT, { detail: { type: 'loop_run.finished', loopId: 'loop-1', runId: 'run-3', status: 'completed' } }))
    expect(await screen.findByText('Signal received.')).toBeVisible()
  })

  it('shows the trigger and the failure in Chinese, with the raw provider error under Details', async () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    const raw = 'Flow Agent provider returned status 429: {"error":{"message":"Rate limit reached"}}'
    const triggered: LoopRun = { ...failed, id: 'run-7', trigger: 'event', triggerLabel: 'Triggered by DEV-24 entering triage', triggerReason: 'triage', entityIdentifier: 'DEV-24', failureReason: 'provider_error', error: raw }
    api.listLoopRuns.mockResolvedValue([triggered])
    api.getLoopRun.mockResolvedValue(triggered)
    renderPage('run-7')
    expect((await screen.findAllByText('由 DEV-24 进入分流触发')).length).toBeGreaterThan(0)
    expect(screen.getByText('模型服务商正在限制请求频率。请稍候再试。')).toBeVisible()
    expect(screen.getByText('详情', { selector: 'summary' })).toBeVisible()
    expect(document.querySelector('.loops-run-error details code')).toHaveTextContent(raw)
    expect(screen.queryByText(/Triggered by/)).toBeNull()
  })

  it('has Chinese copy for every run status and failure reason', () => {
    for (const label of [...Object.values(STATUS_LABELS), ...Object.values(REASON_LABELS)]) {
      expect(zhCN[label], label).toBeTruthy()
    }
  })
})
