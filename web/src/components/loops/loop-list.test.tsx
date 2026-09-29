import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import type { Loop } from '@/types/flow'

const api = vi.hoisted(() => ({ listLoops: vi.fn(), listLoopTemplates: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))

import { LoopList } from './loop-list'

const base: Pick<Loop, 'connectorIds' | 'teamAccess' | 'allowChangesOutsideTrigger' | 'allowExternalSync' | 'instructions' | 'createdAt' | 'updatedAt'> = {
  connectorIds: [], teamAccess: 'allPublic', allowChangesOutsideTrigger: true, allowExternalSync: false, instructions: 'Do it',
  createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-29T00:00:00Z',
}
const loops: Loop[] = [
  { ...base, id: 'loop-1', name: 'Triage agent', description: 'Routes incoming issues.', status: 'published', level: 'team', teamId: 'team-1', triggerType: 'issue', triggerConfig: { event: 'triage' }, enabled: true, ownerId: viewer.id, creator: viewer, runCount30d: 4, lastRunAt: new Date(Date.now() - 2 * 3600_000).toISOString() },
  { ...base, id: 'loop-2', name: 'Weekly wrap', description: 'Shares the highlights.', status: 'published', level: 'workspace', triggerType: 'schedule', triggerConfig: { unit: 'week', interval: 1 }, enabled: true, ownerId: teammate.id, creator: teammate, runCount30d: 1 },
  { ...base, id: 'loop-3', name: '', status: 'draft', level: 'workspace', triggerType: 'schedule', triggerConfig: {}, enabled: false, creator: viewer },
  { ...base, id: 'loop-4', name: 'Someone else draft', status: 'draft', level: 'workspace', triggerType: 'schedule', triggerConfig: {}, enabled: false, creator: teammate },
]

function renderList(items: Loop[] = loops) {
  api.listLoops.mockResolvedValue(items)
  const onNavigate = vi.fn()
  render(<I18nProvider><LoopList data={makeBootstrap({ loops: items, favorites: [] })} embedded={false} onNavigate={onNavigate} onOpenSidebar={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>)
  return { onNavigate }
}

describe('LoopList', () => {
  beforeEach(() => {
    localStorage.clear()
    api.listLoopTemplates.mockReset().mockResolvedValue([])
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  })

  it('shows the Linear table grouped by workspace and team', async () => {
    const user = userEvent.setup()
    const { onNavigate } = renderList()
    expect(screen.getByRole('heading', { name: 'Loops' })).toBeVisible()
    expect(screen.getAllByRole('button', { name: 'New loop' })[0]).toHaveClass('loops-new-button')
    expect(screen.getByRole('tab', { name: 'My loops' })).toBeVisible()
    expect(screen.getByRole('tab', { name: 'All' })).toHaveAttribute('aria-selected', 'true')
    const headers = screen.getAllByRole('columnheader').map(header => header.textContent)
    expect(headers.slice(0, 5)).toEqual(['Name', 'Trigger', 'Owner', 'Runs (30d)', 'Last executed'])
    const groups = screen.getAllByRole('rowgroup')
    expect(groups[0]).toHaveTextContent('Workspace')
    expect(groups[1]).toHaveTextContent('Test team')
    const triage = within(groups[1]).getAllByRole('row')[1]
    expect(triage).toHaveTextContent('Triage agent')
    expect(triage).toHaveTextContent('Routes incoming issues.')
    expect(triage).toHaveTextContent('Triage')
    expect(triage).toHaveTextContent('Viewer')
    expect(triage).toHaveTextContent('4')
    expect(triage).toHaveTextContent('2h ago')
    // The viewer's own draft is listed and marked; other people's drafts stay private.
    expect(screen.getByText('Draft')).toBeVisible()
    expect(screen.queryByText('Someone else draft')).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Triage agent' }))
    expect(onNavigate).toHaveBeenCalledWith('/workspace/loop/loop-1')
    await user.click(screen.getByRole('button', { name: 'Untitled loop' }))
    expect(onNavigate).toHaveBeenCalledWith('/workspace/loops/new?draftId=loop-3')
  })

  it('filters to my loops and by search', async () => {
    const user = userEvent.setup()
    renderList()
    await user.click(screen.getByRole('tab', { name: 'My loops' }))
    expect(screen.queryByText('Weekly wrap')).toBeNull()
    await user.click(screen.getByRole('tab', { name: 'All' }))
    await user.click(screen.getByRole('button', { name: 'Find loops…' }))
    await user.type(screen.getByPlaceholderText('Find loops…'), 'weekly')
    expect(screen.getByText('Weekly wrap')).toBeVisible()
    expect(screen.queryByText('Triage agent')).toBeNull()
  })

  it('shows the creation hub inline when there are no loops', async () => {
    renderList([])
    expect(await screen.findByRole('heading', { name: 'Create a new loop' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Start from scratch' })).toBeVisible()
    expect(screen.queryByRole('table')).toBeNull()
  })
})
