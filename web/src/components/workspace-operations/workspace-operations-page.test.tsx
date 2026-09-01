import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { Draft } from '@/types/flow'

const api = vi.hoisted(() => ({ deleteAllDrafts: vi.fn(), deleteDraft: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))

import { WorkspaceOperationsPage } from './workspace-operations-page'

const renderDrafts = (drafts: Draft[]) => render(
  <MemoryRouter>
    <I18nProvider>
      <WorkspaceOperationsPage data={makeBootstrap({ drafts })} view="drafts" onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)} onResumeDraft={vi.fn()} />
    </I18nProvider>
  </MemoryRouter>,
)

it('lists persisted drafts and discards one after confirmation', async () => {
  const user = userEvent.setup()
  const draft = { id: 'draft-1', userId: 'user-1', type: 'issue', title: 'Persisted draft', body: 'Body', metadata: { teamId: 'team-1' }, createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-01T00:00:00Z' } as Draft
  api.deleteDraft.mockResolvedValue(undefined)
  renderDrafts([draft])

  expect(screen.getByText('Persisted draft')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Discard draft' }))
  expect(screen.getByRole('dialog')).toBeVisible()
  await user.click(screen.getByRole('button', { name: /^Discard$/ }))
  await waitFor(() => expect(api.deleteDraft).toHaveBeenCalledWith(draft.id))
})

it('discovers an unsynced issue draft from local storage', () => {
  localStorage.setItem('flow:create-issue-draft:team-1', JSON.stringify({ title: 'Offline draft', teamId: 'team-1', description: { markdown: 'Saved locally' }, updatedAt: '2026-09-01T00:00:00Z' }))
  renderDrafts([])
  expect(screen.getByText('Offline draft')).toBeVisible()
  localStorage.removeItem('flow:create-issue-draft:team-1')
})

it('discovers parent-scoped composer drafts from local storage', () => {
  const bootstrap = makeBootstrap()
  const project = bootstrap.projects[0]
  localStorage.setItem(`flow:composer-draft:project_update:${project.id}`, JSON.stringify({ type: 'project_update', resourceId: project.id, body: 'Unsynced project update', updatedAt: '2026-09-01T00:00:00Z' }))
  render(<MemoryRouter><I18nProvider><WorkspaceOperationsPage data={{ ...bootstrap, drafts: [] }} view="drafts" onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)} onResumeDraft={vi.fn()} /></I18nProvider></MemoryRouter>)
  expect(screen.getByText('Unsynced project update')).toBeVisible()
  localStorage.removeItem(`flow:composer-draft:project_update:${project.id}`)
})

it('lists loop drafts with scope metadata and resumes the editor', async () => {
  const user = userEvent.setup()
  const draft = { id: 'draft-loop-1', userId: 'user-1', type: 'loop', title: 'Automation draft', body: 'Review issues', metadata: { level: 'workspace' }, createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-01T00:00:00Z' } as Draft
  const onNavigate = vi.fn()
  render(<MemoryRouter><I18nProvider><WorkspaceOperationsPage data={makeBootstrap({ drafts: [draft] })} view="drafts" onNavigate={onNavigate} onOpenSidebar={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)} onResumeDraft={vi.fn()} /></I18nProvider></MemoryRouter>)
  expect(screen.getByText('Loops')).toBeVisible()
  expect(screen.getByText('Workspace')).toBeVisible()
  await user.click(screen.getByRole('link', { name: 'Edit draft' }))
  expect(onNavigate).toHaveBeenCalledWith('/workspace/loops/new?draftId=draft-loop-1')
})

it('groups parent-scoped update and comment drafts and links them to their parent', async () => {
  const user = userEvent.setup()
  const bootstrap = makeBootstrap()
  const project = bootstrap.projects[0]
  const drafts: Draft[] = [
    { id: 'draft-project-update', userId: bootstrap.viewer.id, type: 'project_update', resourceId: project.id, title: 'Project update', body: 'On track', createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-01T00:00:00Z' },
    { id: 'draft-project-comment', userId: bootstrap.viewer.id, type: 'comment', resourceId: project.id, title: '', body: 'Comment', metadata: { resourceType: 'project' }, createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-01T00:00:00Z' },
  ]
  if (bootstrap.initiatives[0]) drafts.push({ id: 'draft-initiative-update', userId: bootstrap.viewer.id, type: 'initiative_update', resourceId: bootstrap.initiatives[0].id, title: 'Initiative update', body: 'Progress', createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-09-01T00:00:00Z' })
  const onNavigate = vi.fn()
  render(<MemoryRouter><I18nProvider><WorkspaceOperationsPage data={{ ...bootstrap, drafts }} view="drafts" onNavigate={onNavigate} onOpenSidebar={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)} onResumeDraft={vi.fn()} /></I18nProvider></MemoryRouter>)
  expect(screen.getByText('Project updates')).toBeVisible()
  if (bootstrap.initiatives[0]) expect(screen.getByText('Initiative updates')).toBeVisible()
  expect(screen.getByText('Commenting on a project')).toBeVisible()
  await user.click(screen.getAllByRole('link', { name: 'Edit draft' })[0])
  expect(onNavigate).toHaveBeenCalledWith(`/workspace/project/${project.slugId}/activity`)
})
