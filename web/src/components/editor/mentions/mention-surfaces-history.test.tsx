import { render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import { makeIssue, viewer } from '@/test/fixtures'
import type { BootstrapData, Initiative, InitiativeUpdate, Project, ProjectUpdate } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn(), listIssueRecords: vi.fn().mockResolvedValue({ items: [], hasMore: false, total: 0 }) }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))

import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { DescriptionHistoryDialog } from '@/components/project-detail/project-header-menus'
import { InitiativeDescriptionHistoryDialog } from '@/components/initiatives/initiative-header-menus'
import { ProjectUpdatesPreview } from '@/components/projects-page/project-updates-preview'
import { UpdatesFloatingPanel } from '@/components/initiatives/updates-floating-panel'
import { mentionFixture, mentionUrls } from './mention-fixtures'

function Shell({ children, data }: { children: ReactNode; data: BootstrapData }) {
  return <I18nProvider><MemoryRouter><WorkspaceStoreProvider account={null} data={data} session={null}>{children}</WorkspaceStoreProvider></MemoryRouter></I18nProvider>
}

const body = `Shipped [TST-1](${mentionUrls.issue}) for [Project one](${mentionUrls.project}) per [Launch plan](${mentionUrls.document}) cc @viewer`

async function expectChips(scope: ParentNode) {
  await waitFor(() => expect(scope.querySelector('a[data-agent-entity="issue"]')).toHaveTextContent('TST-1 Test issue'))
  expect(scope.querySelector('a[data-agent-entity="issue"]')).toHaveAttribute('href', mentionUrls.issue)
  expect(scope.querySelector('a[data-agent-entity="project"]')).toHaveAttribute('href', mentionUrls.project)
  expect(scope.querySelector('a[data-agent-entity="document"]')).toHaveAttribute('href', mentionUrls.document)
  expect(scope.querySelector('a[data-agent-entity="user"]')).toHaveTextContent('@Viewer')
}

beforeEach(() => {
  for (const mock of Object.values(api)) mock.mockReset()
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  resetAgentRecordCache()
})

describe('description history dialogs', () => {
  it('show earlier project descriptions with their references as chips', async () => {
    const revisions = [{ id: 'r1', description: body, createdAt: '2026-09-01T00:00:00Z', author: viewer }] as unknown as Project['descriptionRevisions']
    render(<Shell data={mentionFixture()}><DescriptionHistoryDialog open onOpenChange={vi.fn()} revisions={revisions}/></Shell>)
    await expectChips(await screen.findByRole('dialog'))
    expect(screen.getByRole('dialog').textContent).not.toContain('](')
  })

  it('show earlier initiative descriptions with their references as chips', async () => {
    const data = mentionFixture()
    const initiative = { ...data.initiatives[0], descriptionHistory: [{ id: 'r1', description: body, editedAt: '2026-09-01T00:00:00Z', editor: viewer }] } as unknown as Initiative
    render(<Shell data={data}><InitiativeDescriptionHistoryDialog initiative={initiative} open onOpenChange={vi.fn()} onUpdate={vi.fn()}/></Shell>)
    await expectChips(await screen.findByRole('dialog'))
  })
})

describe('update lists', () => {
  it('shows project updates in the updates preview with chips, fetching an issue the paged client does not hold', async () => {
    api.fetchIssueRecord.mockResolvedValue(makeIssue({ id: '0a1b2c3d-1111-2222-3333-444455556666', identifier: 'TST-9', title: 'Remote issue' }))
    const data = mentionFixture({ issueCollectionPaged: true, issues: [] })
    const update = { id: 'u1', projectId: 'project-1', body: 'Waiting on [TST-9](/workspace/issue/TST-9/remote)', health: 'onTrack', createdAt: '2026-09-01T00:00:00Z', user: viewer, comments: [], reactions: {} } as unknown as ProjectUpdate
    render(<Shell data={data}><ProjectUpdatesPreview onClose={vi.fn()} project={data.projects[0]} updates={[update]} viewer={viewer}/></Shell>)
    await waitFor(() => expect(document.querySelector('.lp-project-updates-preview__text a[data-agent-entity="issue"]')).toHaveTextContent('TST-9 Remote issue'))
    expect(api.fetchIssueRecord).toHaveBeenCalledTimes(1)
  })

  it('shows initiative updates in the updates panel with chips', async () => {
    const data = mentionFixture()
    const update = { id: 'u1', initiativeId: 'initiative-1', body, health: 'onTrack', createdAt: '2026-09-01T00:00:00Z', user: viewer } as unknown as InitiativeUpdate
    render(<Shell data={data}><UpdatesFloatingPanel initiative={data.initiatives[0]} open updates={[update]} viewer={viewer} onClose={vi.fn()} onCreateUpdate={vi.fn()} onOpenActivity={vi.fn()} onUpdate={vi.fn()}/></Shell>)
    await expectChips(document.querySelector('.li-rich-text')!.parentElement!)
  })
})
