import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { mentionFixture } from '@/components/editor/mentions/mention-fixtures'
import { makeBootstrap } from '@/test/fixtures'
import { MentionShell, pasteText, stubEditorEnvironment } from '@/test/mention-host-harness'
import type { Draft } from '@/types/flow'

const api = vi.hoisted(() => ({ deleteAllDrafts: vi.fn(), deleteDraft: vi.fn(), createAsk: vi.fn(), fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn(), listIssueRecords: vi.fn(), realtimeClientId: () => 'ops-test' }))
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

beforeEach(() => {
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  stubEditorEnvironment()
})
afterEach(() => { vi.unstubAllGlobals() })

it('creates an ask whose description keeps mentions as markdown links', async () => {
  const user = userEvent.setup()
  const onReload = vi.fn().mockResolvedValue(undefined)
  api.createAsk.mockResolvedValue({})
  const data = mentionFixture({ asks: [], integrationConnections: [], issueTemplates: [] })
  render(
    <MentionShell data={data}>
      <WorkspaceOperationsPage data={data} view="asks" onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onReload={onReload} onResumeDraft={vi.fn()} />
    </MentionShell>,
  )
  await user.click(screen.getByRole('button', { name: 'Create ask' }))
  const dialog = await screen.findByRole('dialog', { name: 'Create ask' })
  await user.type(within(dialog).getByPlaceholderText('What do you need?'), 'Need a plan')
  const box = within(dialog).getByRole('textbox', { name: 'Description' })
  await user.click(box)
  await user.keyboard('Details in @Launch')
  // The dialog is modal: the page outside it is inert, so the option is picked with the keyboard.
  await screen.findByRole('option', { name: /Launch plan/ })
  await user.keyboard('{Enter}')
  pasteText(box, `${window.location.origin}/workspace/project/project-one/overview`)
  await waitFor(() => expect(box.querySelector('a[data-agent-entity="project"]')).toHaveTextContent('Project one'))
  await user.click(within(dialog).getByRole('button', { name: 'Create' }))
  await waitFor(() => expect(api.createAsk).toHaveBeenCalledTimes(1))
  const body = api.createAsk.mock.calls[0][0].body as string
  expect(body).toContain('[Launch plan](/workspace/document/plan-abc)')
  expect(body).toContain('[Project one](/workspace/project/project-one/overview)')
})
