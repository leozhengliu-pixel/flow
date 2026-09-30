import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useEffect, useState } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { applyFavoriteDelta, FAVORITES_CHANGED, resetFavoriteIntents, toggleFavoriteFor } from '@/lib/favorites'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, Favorite } from '@/types/flow'
import { FavoritesSection } from './sidebar'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

const api = vi.hoisted(() => ({
  addFavorite: vi.fn(),
  removeFavorite: vi.fn(),
}))

vi.mock('@/lib/api', async importOriginal => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  addFavorite: api.addFavorite,
  removeFavorite: api.removeFavorite,
}))

function Harness({ initial }: { initial: BootstrapData }) {
  const [data, setData] = useState(initial)
  useEffect(() => {
    const update = (event: Event) => {
      const delta = (event as CustomEvent).detail
      setData(current => applyFavoriteDelta(current, delta))
    }
    window.addEventListener(FAVORITES_CHANGED, update)
    return () => window.removeEventListener(FAVORITES_CHANGED, update)
  }, [])
  const favorites = data.favorites ?? []
  return (
    <MemoryRouter>
      <I18nProvider>
        <button type="button" onClick={() => void toggleFavoriteFor(data, 'project', data.projects[0].id)}>Favorite project</button>
        {favorites.length > 0 && (
          <FavoritesSection
            data={data}
            favorites={favorites}
            folders={[]}
            onCreateFolder={async () => undefined}
            onMoveFavorite={async () => undefined}
            onMoveFolder={async () => undefined}
            onNavigate={vi.fn()}
            onRemoveFavorite={vi.fn()}
            onRemoveFolder={async () => undefined}
            onRenameFolder={async () => undefined}
            workspaceSlug={data.workspace.urlKey}
          />
        )}
      </I18nProvider>
    </MemoryRouter>
  )
}

it('shows the sidebar favorite before the favorite request resolves', async () => {
  resetFavoriteIntents()
  let resolveCreated: (value: Favorite) => void = () => undefined
  api.addFavorite.mockReset().mockReturnValue(new Promise<Favorite>(resolve => { resolveCreated = resolve }))
  const user = userEvent.setup()
  const data = makeBootstrap({ favorites: [] })
  render(<Harness initial={data} />)

  await user.click(screen.getByRole('button', { name: 'Favorite project' }))

  expect(screen.getByRole('link', { name: /Project one/ })).toBeInTheDocument()
  expect(api.addFavorite).toHaveBeenCalledWith('project', data.projects[0].id)
  resolveCreated({
    id: 'favorite-server',
    userId: data.viewer.id,
    resourceType: 'project',
    resourceId: data.projects[0].id,
    position: -1,
    createdAt: '2026-09-11T00:00:00Z',
  })
})

it('shows a favorited project with its chosen icon and colour, not the default cube', () => {
  const data = makeBootstrap()
  const project = { ...data.projects[0], icon: 'Starred', color: '#5e6ad2' }
  const favorite = { id: 'favorite-icon', resourceType: 'project', resourceId: project.id, position: 0 } as unknown as Favorite
  const { container } = render(<MemoryRouter><I18nProvider><FavoritesSection data={{ ...data, projects: [project, ...data.projects.slice(1)], favorites: [favorite] }} favorites={[favorite]} folders={[]} onCreateFolder={async () => undefined} onMoveFavorite={async () => undefined} onMoveFolder={async () => undefined} onNavigate={vi.fn()} onRemoveFavorite={vi.fn()} onRemoveFolder={async () => undefined} onRenameFolder={async () => undefined} workspaceSlug={data.workspace.urlKey}/></I18nProvider></MemoryRouter>)
  const glyph = container.querySelector('.sidebar-favorite-icon svg')
  expect(glyph?.querySelector('use')?.getAttribute('href')).toMatch(/#Starred$/)
  expect(glyph).toHaveStyle({ color: '#5e6ad2' })
})
