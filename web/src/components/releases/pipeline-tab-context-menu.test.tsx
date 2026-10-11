import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useEffect, useState } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.hoisted(() => {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: (query: string) => ({ matches: false, media: query, addEventListener: vi.fn(), removeEventListener: vi.fn(), addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: vi.fn() }),
  })
})

const api = vi.hoisted(() => ({
  addFavorite: vi.fn(),
  removeFavorite: vi.fn(),
  listIssueRecords: vi.fn().mockResolvedValue({ items: [], hasMore: false, total: 0 }),
}))

vi.mock('@/lib/api', async importOriginal => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  ...api,
}))

import { I18nProvider } from '@/i18n/i18n'
import { applyFavoriteDelta, FAVORITES_CHANGED, resetFavoriteIntents } from '@/lib/favorites'
import { makeBootstrap, viewer } from '@/test/fixtures'
import { zhCN } from '@/i18n/translations'
import type { BootstrapData, Favorite, ReleasePipeline } from '@/types/flow'

import { ReleasePipelineView } from './release-pipeline-view'

const pipeline = {
  id: 'pipeline-1', slugId: 'app', name: 'App', teamIds: ['team-1'], type: 'scheduled', production: true,
  stages: ['Planning', 'Released'], stageStatuses: { Planning: 'planned', Released: 'released' },
  position: 0, pathFilters: [], autoGenerateReleaseNotes: false, createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '2026-01-02T00:00:00.000Z',
} as ReleasePipeline

function favorite(overrides: Partial<Favorite> = {}): Favorite {
  return { id: 'favorite-changelog', userId: viewer.id, resourceType: 'release_pipeline', resourceId: pipeline.id, tab: 'changelog', position: 0, createdAt: '2026-09-01T00:00:00.000Z', ...overrides }
}

function Harness({ initial }: { initial: BootstrapData }) {
  const [data, setData] = useState(initial)
  useEffect(() => {
    const update = (event: Event) => setData(current => applyFavoriteDelta(current, (event as CustomEvent).detail))
    window.addEventListener(FAVORITES_CHANGED, update)
    return () => window.removeEventListener(FAVORITES_CHANGED, update)
  }, [])
  return <I18nProvider><ReleasePipelineView data={data} pipeline={pipeline} tab="releases" onCreate={vi.fn()} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>
}

function renderView(favorites: Favorite[]) {
  return render(<Harness initial={makeBootstrap({ documents: [], releaseNotes: [], releasePipelines: [pipeline], releases: [], favorites })}/>)
}

function openTabMenu(name: 'Releases' | 'Changelog') {
  fireEvent.contextMenu(within(screen.getByRole('navigation', { name: 'Pipeline views' })).getByRole('link', { name }))
  return screen.findByRole('menu', { name: 'Tab options' })
}

describe('pipeline tab context menu', () => {
  beforeEach(() => {
    localStorage.clear()
    localStorage.setItem('flow:locale', 'en-US')
    resetFavoriteIntents()
    api.addFavorite.mockReset()
    api.removeFavorite.mockReset().mockResolvedValue(undefined)
  })

  it('keeps the header star on the root favorite when only a tab is favorited', async () => {
    renderView([favorite()])
    expect(screen.getByRole('button', { name: 'Add to favorites' })).toHaveAttribute('aria-pressed', 'false')
    await openTabMenu('Changelog')
    expect(screen.getByRole('menuitem', { name: 'Unfavorite' }).querySelector('[data-flow-glyph="favoriteFilled"]')).not.toBeNull()
  })

  it('favorites the Releases tab only, then offers Unfavorite', async () => {
    const user = userEvent.setup()
    api.addFavorite.mockResolvedValue(favorite({ id: 'favorite-releases', tab: 'releases' }))
    renderView([])
    await openTabMenu('Releases')
    expect(screen.getByRole('menuitem', { name: 'Copy link' })).toBeInTheDocument()
    await user.click(screen.getByRole('menuitem', { name: 'Favorite' }))
    expect(api.addFavorite).toHaveBeenCalledWith('release_pipeline', pipeline.id, 'releases')
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    expect(screen.getByRole('button', { name: 'Add to favorites' })).toHaveAttribute('aria-pressed', 'false')
    await openTabMenu('Releases')
    expect(screen.getByRole('menuitem', { name: 'Unfavorite' })).toBeInTheDocument()
  })

  it('unfavorites the Changelog tab', async () => {
    const user = userEvent.setup()
    renderView([favorite(), favorite({ id: 'favorite-root', tab: undefined })])
    expect(screen.getByRole('button', { name: 'Remove from favorites' })).toHaveAttribute('aria-pressed', 'true')
    await openTabMenu('Changelog')
    await user.click(screen.getByRole('menuitem', { name: 'Unfavorite' }))
    expect(api.removeFavorite).toHaveBeenCalledWith('release_pipeline', pipeline.id, 'changelog')
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    expect(screen.getByRole('button', { name: 'Remove from favorites' })).toHaveAttribute('aria-pressed', 'true')
  })

  it('copies the tab URL', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    renderView([])
    await openTabMenu('Changelog')
    await user.click(screen.getByRole('menuitem', { name: 'Copy link' }))
    expect(writeText).toHaveBeenCalledWith(`${window.location.origin}/workspace/pipeline/app/changelog`)
  })

  it('has Chinese for the menu copy', () => {
    for (const source of ['Tab options', 'Copy link', 'Favorite', 'Unfavorite', 'Link copied to clipboard', 'Could not copy link', 'Releases', 'Changelog']) expect(zhCN[source], source).toBeTruthy()
  })
})
