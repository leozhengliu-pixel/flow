import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { expect, it, vi } from 'vitest'

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
