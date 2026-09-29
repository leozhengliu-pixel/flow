import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import type { Loop, LoopVersion } from '@/types/flow'

const api = vi.hoisted(() => ({
  getLoop: vi.fn(),
  listLoopVersions: vi.fn(),
  restoreLoopVersion: vi.fn(),
  updateLoop: vi.fn(),
  runLoopNow: vi.fn(),
}))
const dialogs = vi.hoisted(() => ({ confirmAction: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))
vi.mock('@/components/ui/action-dialog-service', async importOriginal => ({ ...(await importOriginal<typeof import('@/components/ui/action-dialog-service')>()), ...dialogs }))

import { LoopDetail } from './loop-detail'
import { LoopVersionsDialog, versionDiff, versionSummary } from './loop-versions'

const loop: Loop = {
  id: 'loop-1', name: 'Weekly wrap', status: 'published', level: 'workspace', triggerType: 'schedule',
  triggerConfig: { startDate: '2026-09-29', interval: 1, unit: 'week', time: '07:00', weekdays: ['fri'] }, instructions: 'Summarize the week for the team.',
  connectorIds: [], teamAccess: 'allPublic', allowChangesOutsideTrigger: false, allowExternalSync: false, webSearch: true, codeAccess: 'read',
  enabled: true, creator: viewer, version: 3, versionId: 'loop-1_v3', createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-29T00:00:00Z',
}
const definition = (patch: Partial<LoopVersion['definition']> = {}): LoopVersion['definition'] => ({
  name: loop.name, description: '', level: 'workspace', triggerType: 'schedule', triggerConfig: loop.triggerConfig, instructions: loop.instructions,
  connectorIds: [], teamAccess: 'allPublic', allowChangesOutsideTrigger: false, allowExternalSync: false, webSearch: true, codeAccess: 'read', ...patch,
})
const now = Date.now()
const versions: LoopVersion[] = [
  { id: 'loop-1_v3', loopId: 'loop-1', version: 3, publishedAt: new Date(now - 3600_000).toISOString(), publishedBy: viewer, changeSummary: ['instructions', 'webSearch'], current: true, definition: definition() },
  { id: 'loop-1_v2', loopId: 'loop-1', version: 2, publishedAt: new Date(now - 2 * 86400_000).toISOString(), publishedBy: teammate, changeSummary: ['trigger'], current: false,
    definition: definition({ instructions: 'Summarize the week.', webSearch: false, triggerConfig: { startDate: '2026-09-29', interval: 1, unit: 'week', time: '09:00' } }) },
  { id: 'loop-1_v1', loopId: 'loop-1', version: 1, publishedAt: new Date(now - 5 * 86400_000).toISOString(), publishedBy: viewer, changeSummary: ['published'], current: false, definition: definition({ instructions: 'Summarize.' }) },
]

function renderDialog(onRestored = vi.fn()) {
  render(<I18nProvider><LoopVersionsDialog data={makeBootstrap({ loops: [loop] })} loop={loop} open onOpenChange={vi.fn()} onRestored={onRestored} onNavigate={vi.fn()}/></I18nProvider>)
  return { onRestored }
}

describe('LoopVersionsDialog', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    for (const mock of [...Object.values(api), ...Object.values(dialogs)]) mock.mockReset()
    api.listLoopVersions.mockResolvedValue(versions)
    api.getLoop.mockResolvedValue(loop)
  })

  it('lists versions newest first with author, time and change summary', async () => {
    renderDialog()
    const list = await screen.findByRole('list', { name: 'Versions' })
    const rows = within(list).getAllByRole('button')
    expect(rows.map(row => row.querySelector('strong')?.textContent)).toEqual(['Version 3', 'Version 2', 'Version 1'])
    expect(rows[0]).toHaveTextContent('Current')
    expect(rows[0]).toHaveTextContent('Viewer')
    expect(rows[0]).toHaveTextContent('1h ago')
    expect(rows[0]).toHaveTextContent('Changed instructions, web search')
    expect(rows[1]).toHaveTextContent('Teammate')
    expect(rows[2]).toHaveTextContent('Published')
  })

  it('shows the current version read-only and cannot restore it', async () => {
    renderDialog()
    const detail = await screen.findByRole('region', { name: 'Version 3' })
    expect(within(detail).getByRole('status')).toHaveTextContent('Same as the current loop')
    expect(within(detail).getByRole('region', { name: 'Trigger' })).toHaveTextContent('Starting')
    expect(within(detail).getByRole('region', { name: 'Instructions' })).toHaveTextContent('Summarize the week for the team.')
    expect(within(detail).getByRole('region', { name: 'Permissions' })).toHaveTextContent('Web searchEnabled')
    expect(within(detail).getByRole('button', { name: 'Restore this version' })).toBeDisabled()
    expect(within(detail).queryByRole('textbox')).toBeNull()
  })

  it('selects an older version, hints the diff and restores it after confirming', async () => {
    const user = userEvent.setup()
    dialogs.confirmAction.mockResolvedValue(true)
    const restored = { ...loop, instructions: 'Summarize the week.', version: 4 }
    api.restoreLoopVersion.mockResolvedValue(restored)
    const { onRestored } = renderDialog()
    await user.click(await screen.findByRole('button', { name: /Version 2/ }))
    const detail = await screen.findByRole('region', { name: 'Version 2' })
    expect(within(detail).getByRole('status')).toHaveTextContent('Differs from the current loop: trigger, instructions, web search')
    expect(within(detail).getByRole('region', { name: 'Permissions' })).toHaveTextContent('Web searchDisabled')
    await user.click(within(detail).getByRole('button', { name: 'Restore this version' }))
    expect(dialogs.confirmAction).toHaveBeenCalledWith('Restore version 2?', expect.objectContaining({ confirmLabel: 'Restore' }))
    await waitFor(() => expect(api.restoreLoopVersion).toHaveBeenCalledWith('loop-1', 'loop-1_v2'))
    expect(onRestored).toHaveBeenCalledWith(restored)
    await waitFor(() => expect(api.listLoopVersions).toHaveBeenCalledTimes(2))
  })

  it('does not restore when the confirmation is cancelled', async () => {
    const user = userEvent.setup()
    dialogs.confirmAction.mockResolvedValue(false)
    renderDialog()
    await user.click(await screen.findByRole('button', { name: /Version 1/ }))
    await user.click(within(await screen.findByRole('region', { name: 'Version 1' })).getByRole('button', { name: 'Restore this version' }))
    await waitFor(() => expect(dialogs.confirmAction).toHaveBeenCalled())
    expect(api.restoreLoopVersion).not.toHaveBeenCalled()
  })

  it('opens from the loop page ⋯ menu', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><LoopDetail data={makeBootstrap({ loops: [loop], favorites: [], drafts: [] })} loopId="loop-1" onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>)
    expect(screen.getByRole('button', { name: 'Version 3' })).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Loop actions' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Show published versions' }))
    expect(await screen.findByRole('dialog', { name: 'Published versions' })).toBeVisible()
    expect(api.listLoopVersions).toHaveBeenCalledWith('loop-1')
  })
})

describe('version helpers', () => {
  it('summarizes changes', () => {
    expect(versionSummary({ changeSummary: ['published'] })).toBe('Published')
    expect(versionSummary({ changeSummary: ['restored', 'instructions'], restoredFromVersion: 2 })).toBe('Restored version 2')
    expect(versionSummary({ changeSummary: ['trigger', 'codeAccess'] })).toBe('Changed trigger, access code')
  })

  it('diffs a definition against the current loop', () => {
    expect(versionDiff(definition(), loop)).toEqual([])
    expect(versionDiff(definition({ name: 'Old', codeAccess: 'disabled', level: 'team', teamId: 'team-1' }), loop)).toEqual(['name', 'codeAccess', 'level'])
  })
})
