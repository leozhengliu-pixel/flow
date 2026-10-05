import { render, renderHook, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { backlog, makeBootstrap, makeIssue, teammate, viewer } from '@/test/fixtures'
import type { BootstrapData } from '@/types/flow'
import { TriageResponsibilityDialog } from './triage-responsibility-dialog'
import { useTriageCount } from './use-triage-count'
import { TRIAGE_CHANGED_EVENT } from './triage-model'

const api = vi.hoisted(() => ({ listIssueRecords: vi.fn(), updateStructuredTeamSettings: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<object>()), ...api }))

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }

function data(overrides: Partial<BootstrapData> = {}) {
  return makeBootstrap({
    issues: [makeIssue({ state: backlog, triagedAt: undefined }), makeIssue({ id: 'snoozed', state: backlog, snoozedUntil: new Date(Date.now() + 86_400_000).toISOString() })],
    teamSettings: { 'team-1': { triageEnabled: true, triageAction: 'none', triageActionUserIds: [] } } as never,
    teamMembers: [{ teamId: 'team-1', userId: viewer.id }, { teamId: 'team-1', userId: teammate.id }] as never,
    ...overrides,
  })
}

describe('triage responsibility', () => {
  beforeEach(() => {
    globalThis.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver
    vi.clearAllMocks()
    api.updateStructuredTeamSettings.mockImplementation(async (_team: string, input: object) => input)
  })

  it('saves No action / Notify members / Assign a member', async () => {
    const user = userEvent.setup()
    const onSaved = vi.fn()
    const workspace = data()
    render(<I18nProvider><TooltipProvider><TriageResponsibilityDialog data={workspace} team={workspace.teams[0]} open onOpenChange={vi.fn()} onSaved={onSaved}/></TooltipProvider></I18nProvider>)
    const dialog = screen.getByRole('dialog', { name: 'Triage responsibility settings' })
    expect(within(dialog).getByText('Define how incoming issues and requests are handled in triage')).toBeInTheDocument()
    expect(within(dialog).getByText('When a new issue is added to triage, take the following action')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('combobox', { name: 'Action' }))
    await user.click(await screen.findByRole('option', { name: 'Notify' }))
    await waitFor(() => expect(api.updateStructuredTeamSettings).toHaveBeenCalledWith('team-1', { triageAction: 'notify', triageActionUserIds: [] }))
    await user.click(within(dialog).getByRole('combobox', { name: 'Members to notify' }))
    await user.click(await screen.findByRole('option', { name: /Teammate/ }))
    await waitFor(() => expect(api.updateStructuredTeamSettings).toHaveBeenLastCalledWith('team-1', { triageAction: 'notify', triageActionUserIds: [teammate.id] }))
    expect(onSaved).toHaveBeenCalled()
  })
})

describe('useTriageCount', () => {
  beforeEach(() => vi.clearAllMocks())

  it('counts unsnoozed triage issues locally when every issue is loaded', () => {
    const { result } = renderHook(() => useTriageCount(data(), { id: 'team-1' }))
    expect(result.current).toBe(1)
    expect(api.listIssueRecords).not.toHaveBeenCalled()
  })

  it('is undefined for teams without triage', () => {
    const { result } = renderHook(() => useTriageCount(data({ teamSettings: { 'team-1': { triageEnabled: false } } as never }), { id: 'team-1' }))
    expect(result.current).toBeUndefined()
  })

  it('asks the server for the total in paged workspaces, refreshing on triage changes', async () => {
    api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 12 })
    const { result } = renderHook(() => useTriageCount(data({ issueCollectionPaged: true } as never), { id: 'team-1' }))
    await waitFor(() => expect(result.current).toBe(12))
    const query = api.listIssueRecords.mock.calls[0][0]
    expect(query).toMatchObject({ teamId: 'team-1', archived: 'false', limit: 1, includeTotal: true })
    expect(JSON.stringify(query.filter)).toContain('snoozedUntil')
    api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 11 })
    window.dispatchEvent(new Event(TRIAGE_CHANGED_EVENT))
    await waitFor(() => expect(result.current).toBe(11))
  })

  it('falls back to counting without the snooze filter on servers that reject it', async () => {
    api.listIssueRecords.mockRejectedValueOnce(new Error('unsupported field snoozedUntil')).mockResolvedValue({ items: [], hasMore: false, total: 4 })
    const { result } = renderHook(() => useTriageCount(data({ issueCollectionPaged: true } as never), { id: 'team-1' }))
    await waitFor(() => expect(result.current).toBe(4))
    expect(JSON.stringify(api.listIssueRecords.mock.calls[1][0].filter)).not.toContain('snoozedUntil')
  })
})
