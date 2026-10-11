import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { BootstrapData, Favorite, ReleasePipeline } from '@/types/flow'
import { FavoritesSection } from './sidebar'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

const pipeline = {
  id: 'pipeline-1', slugId: 'app', name: 'App pipeline', teamIds: [], type: 'scheduled', production: true, stages: [], stageStatuses: {},
  position: 0, pathFilters: [], autoGenerateReleaseNotes: false, createdAt: '2026-01-01T00:00:00.000Z', updatedAt: '2026-01-01T00:00:00.000Z',
} as ReleasePipeline

function favorite(overrides: Partial<Favorite>): Favorite {
  return { id: 'favorite-root', userId: viewer.id, resourceType: 'release_pipeline', resourceId: pipeline.id, position: 0, createdAt: '2026-09-01T00:00:00.000Z', ...overrides }
}

function renderSection(data: BootstrapData, onRemoveFavorite = vi.fn()) {
  render(
    <MemoryRouter>
      <I18nProvider>
        <FavoritesSection
          data={data}
          favorites={data.favorites}
          folders={[]}
          onCreateFolder={async () => undefined}
          onMoveFavorite={async () => undefined}
          onMoveFolder={async () => undefined}
          onNavigate={vi.fn()}
          onRemoveFavorite={onRemoveFavorite}
          onRemoveFolder={async () => undefined}
          onRenameFolder={async () => undefined}
          workspaceSlug={data.workspace.urlKey}
        />
      </I18nProvider>
    </MemoryRouter>,
  )
}

it('renders root and tab favorites of one pipeline as separate rows, the tab row with a badge and the tab URL', async () => {
  localStorage.setItem('flow:locale', 'en-US')
  const user = userEvent.setup()
  const onRemove = vi.fn()
  renderSection(makeBootstrap({
    releasePipelines: [pipeline],
    favorites: [
      favorite({ id: 'favorite-root', position: 0 }),
      favorite({ id: 'favorite-changelog', tab: 'changelog', position: -2 }),
      favorite({ id: 'favorite-releases', tab: 'releases', position: -1 }),
    ],
  }), onRemove)

  const rows = screen.getAllByRole('link', { name: /App pipeline/ })
  expect(rows.map(row => row.getAttribute('href'))).toEqual(['/workspace/pipeline/app/changelog', '/workspace/pipeline/app/releases', '/workspace/pipeline/app/releases'])
  // Only tab favorites carry the badge; the root row shows the bare pipeline icon.
  expect(rows[2].querySelector('.favorite-tab-badge')).toBeNull()
  const changelogBadge = within(rows[0]).getByRole('img', { name: 'Changelog' })
  expect(changelogBadge.querySelector('[data-icon="changelog"]')).not.toBeNull()
  expect(within(rows[1]).getByRole('img', { name: 'Releases' }).querySelector('[data-icon="releases"]')).not.toBeNull()
  expect(rows[0].querySelector('.favorite-tab-icon [data-icon="release-pipeline"]')).not.toBeNull()

  await user.hover(changelogBadge)
  expect(await screen.findByRole('tooltip')).toHaveTextContent('Changelog')

  await user.click(within(rows[0]).getByRole('button', { name: 'Remove favorite' }))
  expect(onRemove).toHaveBeenCalledWith(expect.objectContaining({ id: 'favorite-changelog', tab: 'changelog' }))
})

it('labels the tab badge in Chinese', () => {
  localStorage.setItem('flow:locale', 'zh-CN')
  renderSection(makeBootstrap({ releasePipelines: [pipeline], favorites: [favorite({ id: 'favorite-changelog', tab: 'changelog' })] }))
  expect(screen.getByRole('img', { name: '更新日志' })).toBeInTheDocument()
  localStorage.setItem('flow:locale', 'en-US')
})
