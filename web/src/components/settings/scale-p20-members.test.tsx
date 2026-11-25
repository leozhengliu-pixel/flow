import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, teammate } from '@/test/fixtures'
import { WorkspaceMembersPage } from './workspace-members-page'

afterEach(() => vi.unstubAllEnvs())

it('scale-p20 clears selected people and staged dialogs when the workspace changes', () => {
  vi.stubEnv('VITE_SYNC_V2', 'true'); vi.stubEnv('VITE_PEOPLE_WINDOWS', 'true')
  const data = makeBootstrap({ viewerRole: 'admin', members: [{ user: teammate, role: 'member', status: 'active', joinedAt: '2026-11-12' }], invitations: [], teamMembers: [] })
  const onReload = vi.fn().mockResolvedValue(undefined)
  const shell = (current: typeof data) => <I18nProvider><MemoryRouter><WorkspaceMembersPage data={current} onReload={onReload}/></MemoryRouter></I18nProvider>
  const view = render(shell(data))
  fireEvent.click(screen.getByRole('checkbox'))
  expect(screen.getByRole('toolbar', { name: 'Selected members' })).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Invite' }))
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  view.rerender(shell({ ...data, workspace: { ...data.workspace, id: 'next-workspace', urlKey: 'next-workspace' } }))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(screen.queryByRole('toolbar', { name: 'Selected members' })).not.toBeInTheDocument()
})
