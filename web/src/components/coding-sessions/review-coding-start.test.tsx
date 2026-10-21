import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { request } from '@/lib/api-client'
import { getApplicationTask } from '@/lib/application-agents'
import { startIssueCodingSession, startReviewCodingSession } from '@/lib/coding-sessions-api'
import { toast } from '@/lib/toast'
import { makeBootstrap } from '@/test/fixtures'
import type { CodingSession } from '@/types/coding-sessions'
import { CodingSessionErrorBoundary } from './coding-session-error-boundary'
import { makeSession } from './coding-session-test-fixtures'
import { ReviewCodingSessionActions } from './review-coding-actions'
import { resetCodingAvailabilityCache } from './use-coding-sessions'

// The real API client functions run against the server's wire shapes; only the transport is faked.
vi.mock('@/lib/api-client', async original => ({ ...await original<typeof import('@/lib/api-client')>(), request: vi.fn() }))
vi.mock('@/lib/application-agents', async original => ({ ...await original<typeof import('@/lib/application-agents')>(), getApplicationTask: vi.fn() }))
vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() } }))

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

const review = { id: 'review-1', status: 'open', headBranch: 'feature/x', provider: 'gitlab' } as const
const data = () => makeBootstrap({ reviews: [], users: [] } as never)

/** A brand-new review session as GET /api/coding/sessions/{id} returns it: no activities, plan, artifacts or diffs yet. */
const fresh = (): CodingSession => makeSession({ id: 'cs-review', agentSessionId: 'task-review', issueId: undefined, source: 'pullRequest', state: 'pending', phase: 'queued', reviewId: 'review-1' } as Partial<CodingSession>)

function serve(trigger: unknown) {
  vi.mocked(request).mockImplementation(async (url: string) => {
    if (url === '/api/coding/status') return { enabled: true }
    if (url === '/api/reviews/review-1/coding-session') return trigger
    if (url === '/api/coding/sessions/cs-review') return fresh()
    // Go encodes a nil slice as null.
    if (url === '/api/coding/sessions/cs-review/artifacts') return null
    if (url.startsWith('/api/coding/sessions/cs-review/diff')) return { source: 'none', additions: 0, deletions: 0, files: null }
    throw new Error(`unexpected request ${url}`)
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  resetCodingAvailabilityCache()
  vi.mocked(getApplicationTask).mockResolvedValue({ session: { id: 'task-review' }, activities: [] } as never)
})
afterEach(() => vi.restoreAllMocks())

describe('Fix with agent on a review', () => {
  it('opens the started session from the trigger result instead of crashing', async () => {
    const user = userEvent.setup()
    serve({ outcome: 'started', created: true, approvalRequired: false, session: fresh() })
    render(<MemoryRouter><I18nProvider><ReviewCodingSessionActions review={review} data={data()}/></I18nProvider></MemoryRouter>)
    await user.click(await screen.findByRole('button', { name: 'Fix with agent' }))
    const dialog = await screen.findByRole('dialog', { name: 'Coding session' })
    expect(await screen.findByRole('region', { name: 'Coding session' })).toBeInTheDocument()
    expect(dialog).toBeVisible()
    expect(screen.queryByText('Couldn’t show this coding session.')).not.toBeInTheDocument()
    expect(request).toHaveBeenCalledWith('/api/coding/sessions/cs-review')
    expect(toast.success).toHaveBeenCalled()
  })

  it('shows the reason when the trigger started nothing', async () => {
    const user = userEvent.setup()
    serve({ outcome: 'notStarted', created: false, approvalRequired: false, message: 'Nothing to change here.' })
    render(<MemoryRouter><I18nProvider><ReviewCodingSessionActions review={review} data={data()}/></I18nProvider></MemoryRouter>)
    await user.click(await screen.findByRole('button', { name: 'Fix with agent' }))
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Nothing to change here.'))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
})

describe('trigger client', () => {
  it('unwraps the session of a review or issue trigger result', async () => {
    vi.mocked(request).mockResolvedValue({ outcome: 'continued', created: false, session: fresh() })
    await expect(startReviewCodingSession('review-1', { kind: 'fix' })).resolves.toMatchObject({ id: 'cs-review' })
    await expect(startIssueCodingSession('issue-1')).resolves.toMatchObject({ id: 'cs-review' })
  })

  it('keeps a session that waits for approval', async () => {
    vi.mocked(request).mockResolvedValue({ outcome: 'notStarted', created: false, approvalRequired: true, session: fresh(), message: 'Waiting.' })
    await expect(startReviewCodingSession('review-1', { kind: 'fix' })).resolves.toMatchObject({ id: 'cs-review' })
  })
})

describe('CodingSessionErrorBoundary', () => {
  it('shows an inline error with Retry instead of blanking the page', async () => {
    const user = userEvent.setup()
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
    let fail = true
    const Boom = () => { if (fail) throw new TypeError("Cannot read properties of undefined (reading 'items')"); return <p>Recovered</p> }
    render(<I18nProvider><p>Host page</p><CodingSessionErrorBoundary><Boom/></CodingSessionErrorBoundary></I18nProvider>)
    expect(screen.getByText('Host page')).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent('Couldn’t show this coding session.')
    fail = false
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(screen.getByText('Recovered')).toBeInTheDocument()
  })
})
