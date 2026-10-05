import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { fetchPulseCapabilities, fetchPulseSummary, reportPulseSummary, updateUserSettings } from '@/lib/api'
import { makeBootstrap, makeIssue, teammate, viewer } from '@/test/fixtures'
import type { BootstrapData, Notification, PulseSummary } from '@/types/flow'

import { InboxAppPage } from './inbox-app-page'
import { inboxNotificationCategory, matchesInboxFilter } from './inbox-filter-model'
import { resetPulseCapabilitiesCache } from './pulse-summary-model'
import { PulseSummaryView } from './pulse-summary-view'

vi.mock('@/lib/api', async original => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchPulseSummary: vi.fn(),
  fetchPulseCapabilities: vi.fn(),
  reportPulseSummary: vi.fn(),
  updateUserSettings: vi.fn(),
  updateInboxNotification: vi.fn(async () => undefined),
}))
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const flowActor = { id: 'flow', name: 'Flow', displayName: 'Flow', email: '', active: true } as unknown as Notification['actor']

function notification(overrides: Partial<Notification>): Notification {
  return {
    id: 'n-1', recipientId: viewer.id, type: 'assignment', sourceType: 'issue', sourceId: 'issue-1', actor: teammate,
    category: 'assignments', groupKey: 'n-1', occurrenceCount: 1, latestActorIds: [], favorite: false,
    createdAt: '2026-10-01T06:00:00.000Z', updatedAt: '2026-10-01T06:00:00.000Z',
    ...overrides,
  } as Notification
}

const dailyPulse = notification({
  id: 'pulse-1', type: 'pulseSummary', category: 'pulse', sourceType: 'pulse', sourceId: '2026-10-01T06:00:00Z', actor: flowActor,
  title: 'Daily Pulse', text: 'Update from Apollo', payload: { schedule: 'daily', updateIds: ['update-1'] },
})
const legacyPulse = notification({
  id: 'pulse-2', type: 'pulseSummary', category: 'pulse', sourceType: 'pulse', sourceId: '2026-09-28T06:00:00Z', actor: flowActor,
  occurrenceCount: 3, payload: { schedule: 'weekly', updateIds: ['a', 'b', 'c'] },
})
const assignment = notification({ id: 'n-issue', issueId: 'issue-1' })

function bootstrap(notifications: Notification[], overrides: Partial<BootstrapData> = {}) {
  return makeBootstrap({
    issues: [makeIssue()], notifications, reviews: [], comments: {}, userSettings: {},
    ...overrides,
  } as Partial<BootstrapData>)
}

function renderInbox(data: BootstrapData) {
  const noop = vi.fn(async () => undefined)
  return render(
    <I18nProvider><TooltipProvider>
      <InboxAppPage
        data={data}
        onReload={noop}
        onOpenIssue={vi.fn()}
        onDeleteRelation={noop}
        onCreateSubIssue={noop}
        onReactIssue={noop}
        onCreateComment={noop}
        onEditComment={noop}
        onDeleteComment={noop}
        onReactComment={noop}
        onUploadAttachment={noop}
        onDeleteAttachment={noop}
      />
    </TooltipProvider></I18nProvider>,
  )
}

const summary: PulseSummary = {
  title: 'Daily Pulse',
  generatedAt: '2026-10-01T06:00:00.000Z',
  text: 'Apollo is on track.',
  ai: true,
  sections: [
    { kind: 'project', items: [{ updateId: 'update-1', sourceName: 'Apollo', health: 'onTrack', summary: 'Apollo shipped the beta and is on track.' }] },
    { kind: 'initiative', items: [{ updateId: 'update-2', sourceName: 'Growth', health: 'atRisk', summary: 'Growth is waiting on pricing.' }] },
  ],
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.clearAllMocks()
  resetPulseCapabilitiesCache()
  localStorage.clear()
  localStorage.setItem('flow:locale', 'en-US')
  window.history.replaceState(null, '', '/workspace/inbox')
  vi.mocked(fetchPulseSummary).mockResolvedValue(summary)
  vi.mocked(fetchPulseCapabilities).mockResolvedValue({ aiSummaries: true, audio: false })
  vi.mocked(updateUserSettings).mockResolvedValue({} as Awaited<ReturnType<typeof updateUserSettings>>)
  vi.mocked(reportPulseSummary).mockResolvedValue(undefined)
})
afterEach(() => {
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

describe('Pulse summaries in the inbox list', () => {
  it('renders the Pulse avatar, title and summary line, never the raw type', () => {
    renderInbox(bootstrap([dailyPulse, legacyPulse, assignment]))
    const rows = screen.getAllByRole('link')
    const daily = rows.find(row => row.textContent?.includes('Daily Pulse'))!
    expect(daily).toBeTruthy()
    expect(within(daily).getByText('Update from Apollo')).toBeInTheDocument()
    expect(within(daily).getByTestId('pulse-summary-avatar')).toBeInTheDocument()
    // Older records without title/text derive them from the payload.
    const weekly = rows.find(row => row.textContent?.includes('Weekly Pulse'))!
    expect(within(weekly).getByText('3 updates')).toBeInTheDocument()
    expect(document.body.innerHTML).not.toMatch(/pulseSummary/)
    expect(document.body.textContent).not.toMatch(/project and initiative updates/)
  })

  it('"Pulse summaries" filter keeps summaries and drops everything else', () => {
    expect(inboxNotificationCategory(dailyPulse)).toBe('pulse')
    expect(inboxNotificationCategory({ type: 'pulseSummary', category: 'updates' })).toBe('pulse')
    const filter = { id: 'f', property: 'notificationType' as const, operator: 'is' as const, values: [{ value: 'pulse', valueLabel: 'Pulse summaries' }] }
    const target = { issueId: '', actorId: 'flow', initiativeIds: [], issuePriority: 0, issueStatusType: 'started' }
    expect(matchesInboxFilter({ ...target, notificationType: 'pulse' }, filter)).toBe(true)
    expect(matchesInboxFilter({ ...target, notificationType: 'assignment' }, filter)).toBe(false)

    const encoded = btoa(JSON.stringify([filter])).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '')
    window.history.replaceState(null, '', `/workspace/inbox?filter=${encoded}`)
    renderInbox(bootstrap([dailyPulse, assignment]))
    expect(screen.getByText('Daily Pulse')).toBeInTheDocument()
    expect(screen.queryByText('Test issue')).not.toBeInTheDocument()
  })

  it('"Pulse frequency" in the row context menu saves the personal schedule', async () => {
    const user = userEvent.setup()
    renderInbox(bootstrap([dailyPulse], { userSettings: { [viewer.id]: { pulseSchedule: 'default' } } } as unknown as Partial<BootstrapData>))
    fireEvent.contextMenu(screen.getAllByRole('link').find(row => row.textContent?.includes('Daily Pulse'))!)
    const trigger = await screen.findByRole('menuitem', { name: /Pulse frequency/ })
    expect(screen.queryByRole('menuitem', { name: /Copy/ })).not.toBeInTheDocument()
    await user.click(trigger)
    const weekly = await screen.findByRole('menuitemradio', { name: 'Weekly' })
    expect(screen.getByRole('menuitemradio', { name: 'Daily' })).toHaveAttribute('aria-checked', 'true')
    fireEvent.click(weekly)
    await waitFor(() => expect(updateUserSettings).toHaveBeenCalledWith({ pulseSchedule: 'weekly' }))
  })

  it('opens "Your Pulse" when the summary is selected', async () => {
    const user = userEvent.setup()
    renderInbox(bootstrap([dailyPulse]))
    await user.click(screen.getAllByRole('link').find(row => row.textContent?.includes('Daily Pulse'))!)
    expect(await screen.findByRole('heading', { name: 'Daily Pulse' })).toBeInTheDocument()
    expect(await screen.findByText('Apollo shipped the beta and is on track.')).toBeInTheDocument()
    expect(fetchPulseSummary).toHaveBeenCalledWith('pulse-1', expect.any(AbortSignal))
  })
})

describe('Your Pulse', () => {
  const data = makeBootstrap()
  const view = () => render(<I18nProvider><PulseSummaryView notificationId="pulse-1" title="Daily Pulse" data={data} /></I18nProvider>)

  it('hides Listen when audio is unavailable', async () => {
    view()
    expect(await screen.findByText('Apollo shipped the beta and is on track.')).toBeInTheDocument()
    await waitFor(() => expect(fetchPulseCapabilities).toHaveBeenCalled())
    expect(screen.queryByRole('button', { name: 'Listen' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Pulse display options' })).toBeInTheDocument()
  })

  it('shows Listen and the playback speed when audio is available', async () => {
    vi.mocked(fetchPulseCapabilities).mockResolvedValue({ aiSummaries: true, audio: true })
    view()
    expect(await screen.findByRole('button', { name: 'Listen' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Playback speed' })).toHaveTextContent('1×')
  })

  it('groups summaries by type, initiatives first', async () => {
    view()
    await screen.findByText('Growth is waiting on pricing.')
    const groups = screen.getAllByRole('region').map(region => region.getAttribute('aria-label'))
    expect(groups).toEqual(['Initiatives', 'Projects'])
  })

  it('"Report invalid summary…" sends the reason for that update', async () => {
    const user = userEvent.setup()
    view()
    const card = await screen.findByRole('article', { name: 'Apollo' })
    await user.click(within(card).getByRole('button', { name: 'Update actions' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Report invalid summary…' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('button', { name: 'Send' })).toBeDisabled()
    await user.type(within(dialog).getByRole('textbox', { name: 'Tell us more' }), 'Apollo did not ship')
    await user.click(within(dialog).getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(reportPulseSummary).toHaveBeenCalledWith('pulse-1', 'Apollo did not ship', 'update-1'))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('remembers the Updates display and lists sources in the table of contents', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchPulseSummary).mockResolvedValue({
      ...summary,
      sections: [{ kind: 'project', items: ['Apollo', 'Hermes', 'Zeus'].map((name, index) => ({ updateId: `u-${index}`, sourceName: name, health: 'onTrack' as const, summary: `${name} summary` })) }],
    })
    view()
    await screen.findByText('Apollo summary')
    await user.click(screen.getByRole('button', { name: 'Pulse display options' }))
    await user.click(await screen.findByRole('menuitemradio', { name: 'Updates' }))
    const toc = await screen.findByRole('navigation', { name: 'Table of contents' })
    expect(within(toc).getAllByRole('button').map(button => button.textContent)).toEqual(['Apollo', 'Hermes', 'Zeus'])
    expect(localStorage.getItem('flow.pulse.summaryDisplay')).toContain('updates')
  })

  it('"Updates" display renders the full update with its diff block', async () => {
    localStorage.setItem('flow.pulse.summaryDisplay', JSON.stringify({ [viewer.id]: 'updates' }))
    const withUpdates = makeBootstrap({
      projectUpdates: { 'project-1': [{ id: 'update-1', projectId: 'project-1', body: 'Shipped the beta.', health: 'onTrack', createdAt: '2026-10-01T05:00:00.000Z', user: teammate, comments: [], reactions: {}, attachments: [], diff: { status: { from: { name: 'Planned' }, to: { name: 'In progress' } }, milestones: [{ id: 'm-1', name: 'Beta', from: 0.5, to: 1, completed: true }] } }] },
    } as unknown as Partial<BootstrapData>)
    render(<I18nProvider><PulseSummaryView notificationId="pulse-1" title="Daily Pulse" data={withUpdates} /></I18nProvider>)
    const card = await screen.findByRole('article', { name: 'Apollo' })
    expect(within(card).getByText('Shipped the beta.')).toBeInTheDocument()
    const diff = within(card).getByRole('region', { name: 'Changes since last update' })
    expect(within(diff).getByText('Planned')).toBeInTheDocument()
    expect(within(diff).getByText('In progress')).toBeInTheDocument()
    expect(within(diff).getByText('Beta')).toBeInTheDocument()
  })

  it('shows an error state with retry', async () => {
    vi.mocked(fetchPulseSummary).mockRejectedValueOnce(new Error('boom'))
    const user = userEvent.setup()
    view()
    expect(await screen.findByText('Unable to load summary')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByText('Apollo shipped the beta and is on track.')).toBeInTheDocument()
  })
})
