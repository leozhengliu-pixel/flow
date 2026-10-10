import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { expect, it, vi } from 'vitest'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { Favorite, FavoriteFolder, FlowDocument, SavedView } from '@/types/flow'
import { FavoritesSection } from './sidebar'

it('renders every core favorite type and manages favorite folders', async () => {
  const user = userEvent.setup()
  const data = makeBootstrap({
    documents: [{ id: 'document-1', slugId: 'release-notes', title: 'Release notes', icon: '', color: '', projectIds: [], teamIds: [], subscriberIds: [], favorite: true, content: '', creator: viewer, createdAt: '', updatedAt: '', revisions: [] }] as FlowDocument[],
    savedViews: [{ id: 'view-1', slugId: 'priority-work', name: 'Priority work', description: '', icon: 'CustomView', color: '#5e6ad2', resource: 'issues', scope: 'workspace', favorite: true, subscribed: false, view: 'all', filters: [], display: {}, insights: {} }] as unknown as SavedView[],
  })
  const favorites: Favorite[] = [
    { id: 'favorite-issue', userId: data.viewer.id, resourceType: 'issue', resourceId: data.issues[0].id, position: 0, createdAt: '2026-09-01T00:00:00Z' },
    { id: 'favorite-view', userId: data.viewer.id, resourceType: 'view', resourceId: 'view-1', position: 1, createdAt: '2026-09-01T00:00:01Z' },
    { id: 'favorite-team', userId: data.viewer.id, resourceType: 'team', resourceId: data.teams[0].id, position: 2, createdAt: '2026-09-01T00:00:02Z' },
    { id: 'favorite-document', userId: data.viewer.id, resourceType: 'document', resourceId: 'document-1', folderId: 'folder-1', position: 0, createdAt: '2026-09-01T00:00:03Z' },
  ]
  const folders: FavoriteFolder[] = [{ id: 'folder-1', userId: data.viewer.id, name: 'Planning', position: 0, createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-01T00:00:00Z' }]
  const onCreateFolder = vi.fn().mockResolvedValue(undefined)
  const onRemoveFavorite = vi.fn()

  render(
    <MemoryRouter>
      <I18nProvider>
        <FavoritesSection
          data={data}
          favorites={favorites}
          folders={folders}
          onCreateFolder={onCreateFolder}
          onMoveFavorite={vi.fn().mockResolvedValue(undefined)}
          onMoveFolder={vi.fn().mockResolvedValue(undefined)}
          onNavigate={vi.fn()}
          onRemoveFavorite={onRemoveFavorite}
          onRemoveFolder={vi.fn().mockResolvedValue(undefined)}
          onRenameFolder={vi.fn().mockResolvedValue(undefined)}
          workspaceSlug={data.workspace.urlKey}
        />
      </I18nProvider>
    </MemoryRouter>,
  )

  expect(screen.getByRole('link', { name: /Test issue/ })).toHaveAttribute('href', expect.stringContaining('/issue/TST-1/'))
  expect(screen.getByRole('link', { name: /Priority work/ })).toHaveAttribute('href', '/workspace/view/priority-work')
  expect(screen.getByRole('link', { name: /Test team/ })).toHaveAttribute('href', '/workspace/team/TST/overview')
  expect(screen.getByRole('link', { name: /Release notes/ })).toHaveAttribute('href', '/workspace/document/release-notes')

  await user.click(screen.getByRole('button', { name: 'Create new folder for favorites' }))
  await user.type(screen.getByRole('textbox', { name: 'Folder name…' }), 'Roadmap{Enter}')
  expect(onCreateFolder).toHaveBeenCalledWith('Roadmap')

  await user.click(screen.getAllByRole('button', { name: 'Remove favorite' })[0])
  expect(onRemoveFavorite).toHaveBeenCalledWith(favorites[0])
})

function renderFavorites(data: ReturnType<typeof makeBootstrap>, favorites: Favorite[]) {
  return render(
    <MemoryRouter>
      <I18nProvider>
        <FavoritesSection
          data={data}
          favorites={favorites}
          folders={[]}
          onCreateFolder={vi.fn().mockResolvedValue(undefined)}
          onMoveFavorite={vi.fn().mockResolvedValue(undefined)}
          onMoveFolder={vi.fn().mockResolvedValue(undefined)}
          onNavigate={vi.fn()}
          onRemoveFavorite={vi.fn()}
          onRemoveFolder={vi.fn().mockResolvedValue(undefined)}
          onRenameFolder={vi.fn().mockResolvedValue(undefined)}
          workspaceSlug={data.workspace.urlKey}
        />
      </I18nProvider>
    </MemoryRouter>,
  )
}

function documentFixture(overrides: Partial<FlowDocument>): FlowDocument {
  return { id: 'document-1', slugId: 'doc', title: 'Release notes', icon: '', color: '', projectIds: [], teamIds: [], subscriberIds: [], favorite: true, content: '', creator: viewer, createdAt: '', updatedAt: '', revisions: [], ...overrides } as FlowDocument
}

function documentFavorite(id: string, position: number): Favorite {
  return { id: `favorite-${id}`, userId: viewer.id, resourceType: 'document', resourceId: id, position, createdAt: '2026-09-01T00:00:00Z' }
}

it('shows a document favorite with its team, project or initiative name muted after the title', () => {
  const base = makeBootstrap()
  const project = base.projects[0]
  const data = makeBootstrap({
    documents: [
      documentFixture({ id: 'in-team', slugId: 'in-team', title: 'Team doc', teamIds: [base.teams[0].id] }),
      documentFixture({ id: 'in-project', slugId: 'in-project', title: 'Project doc', projectIds: [project.id] }),
      documentFixture({ id: 'in-issue', slugId: 'in-issue', title: 'Issue doc', teamIds: [base.teams[0].id], issueId: base.issues[0].id }),
      documentFixture({ id: 'loose', slugId: 'loose', title: 'Loose doc' }),
    ],
    initiatives: [{ ...base.initiatives[0], id: 'initiative-1', name: 'Grow revenue', resources: [{ id: 'resource-1', type: 'document', documentId: 'in-initiative' }] } as never],
  })
  data.documents.push(documentFixture({ id: 'in-initiative', slugId: 'in-initiative', title: 'Initiative doc' }))
  renderFavorites(data, ['in-team', 'in-project', 'in-issue', 'loose', 'in-initiative'].map(documentFavorite))

  const parentOf = (name: RegExp) => screen.getByRole('link', { name }).querySelector('.sidebar-favorite-parent')
  expect(parentOf(/Team doc/)).toHaveTextContent(base.teams[0].name)
  expect(parentOf(/Project doc/)).toHaveTextContent(project.name)
  expect(parentOf(/Initiative doc/)).toHaveTextContent('Grow revenue')
  expect(parentOf(/Issue doc/)).toBeNull()
  expect(parentOf(/Loose doc/)).toBeNull()
  // The title and the muted parent share one truncating line.
  expect(screen.getByRole('link', { name: /Team doc/ }).querySelector('.sidebar-favorite-title')).toHaveAttribute('title', `Team doc · ${base.teams[0].name}`)
})

it('falls back to Untitled for an untitled document favorite and keeps long names in one truncating title', () => {
  const base = makeBootstrap()
  const longTitle = 'A very long document title that cannot possibly fit inside the sidebar row'
  const data = makeBootstrap({
    documents: [
      documentFixture({ id: 'blank', slugId: 'blank', title: '  ', teamIds: [base.teams[0].id] }),
      documentFixture({ id: 'long', slugId: 'long', title: longTitle, teamIds: [base.teams[0].id] }),
    ],
  })
  renderFavorites(data, [documentFavorite('blank', 0), documentFavorite('long', 1)])

  const untitled = screen.getByRole('link', { name: new RegExp(`^Untitled ${base.teams[0].name}`) })
  expect(untitled.querySelector('.sidebar-favorite-title')).toHaveTextContent(`Untitled ${base.teams[0].name}`)
  const long = screen.getByRole('link', { name: new RegExp(longTitle) }).querySelector('.sidebar-favorite-title')!
  expect(long).toHaveAttribute('title', `${longTitle} · ${base.teams[0].name}`)
  // The parent is a muted span inside the title element, so the ellipsis applies to both.
  expect(long.querySelector('.sidebar-favorite-parent')).toHaveTextContent(base.teams[0].name)
})
