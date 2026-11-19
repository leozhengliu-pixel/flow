import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { I18nProvider } from '@/i18n/i18n'
import type { Initiative, User } from '@/types/flow'
import { InitiativesPage } from './initiatives-page'

const viewer = { id: 'u1', displayName: 'Ada', name: 'Ada' } as User
const initiative = { id: 'init-1', name: 'Launch', color: '#5E6AD2', icon: 'Initiative', status: 'active', health: 'onTrack', priority: 1, position: 0, createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z', projectIds: [], labelIds: [], subscribed: false } as unknown as Initiative

function page(displayDefault: { properties: string[] }) {
  return <I18nProvider><InitiativesPage initiatives={[initiative]} initiativeUpdates={{}} projects={[]} projectUpdates={{}} users={[viewer]} teams={[]} labels={[]} viewer={viewer} view="all" displayDefault={displayDefault}
    onViewChange={vi.fn()} onOpen={vi.fn()} onCreate={vi.fn()} onCreateLabel={vi.fn()} onCreateUpdate={vi.fn()} onUpdate={vi.fn()} onDelete={vi.fn()} onCreateReminder={vi.fn()} onSetDefault={vi.fn()}/></I18nProvider>
}

describe('InitiativesPage display default', () => {
  it('follows the default the server confirms after mounting from cached data', () => {
    localStorage.clear()
    const view = render(page({ properties: ['status'] }))
    expect(screen.getByRole('button', { name: 'Order by Status' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Order by Priority' })).not.toBeInTheDocument()
    view.rerender(page({ properties: ['priority'] }))
    expect(screen.getByRole('button', { name: 'Order by Priority' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Order by Status' })).not.toBeInTheDocument()
  })
})
