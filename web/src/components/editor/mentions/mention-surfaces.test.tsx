import { act, render, screen, waitFor, within } from '@testing-library/react'
import type { ReactNode } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import { makeIssue, project as baseProject, viewer } from '@/test/fixtures'
import type { BootstrapData, Project, ProjectUpdate } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn(), listIssueRecords: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))

import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { AgentEntityHover } from '@/components/agent/agent-entity-hover'
import { InboxNotificationRow, type InboxNotificationRowData } from '@/components/inbox/notification-row'
import { BaseUpdateInboxView } from '@/components/inbox/hosts/base-update-inbox-view'
import { ProjectOverviewInboxView } from '@/components/inbox/hosts/project-overview-inbox-view'
import { IssueLoadingPreview } from '@/components/issue/issue-loading-preview'
import { MentionBody } from './mention-body'
import { mentionFixture, mentionUrls } from './mention-fixtures'

function Shell({ children, data }: { children: ReactNode; data: BootstrapData }) {
  return <I18nProvider><MemoryRouter><WorkspaceStoreProvider account={null} data={data} session={null}>{children}</WorkspaceStoreProvider></MemoryRouter></I18nProvider>
}

/** A markdown body naming an issue, a project, a document and a person. */
export const referenceBody = `See [TST-1](${mentionUrls.issue}) in [Project one](${mentionUrls.project}) and [Launch plan](${mentionUrls.document}) cc @viewer`

async function chip(kind: string, scope: ParentNode = document) {
  return waitFor(() => {
    const found = scope.querySelector(`a[data-agent-entity="${kind}"]`)
    expect(found, kind).not.toBeNull()
    return found as HTMLAnchorElement
  })
}

/** The four references render as chips with the right label and in-app link. */
async function expectReferenceChips(scope: ParentNode = document) {
  const issue = await chip('issue', scope)
  expect(issue).toHaveTextContent('TST-1 Test issue')
  expect(issue).toHaveAttribute('href', mentionUrls.issue)
  const projectChip = await chip('project', scope)
  expect(projectChip).toHaveTextContent('Project one')
  expect(projectChip).toHaveAttribute('href', mentionUrls.project)
  const documentChip = await chip('document', scope)
  expect(documentChip).toHaveTextContent('Launch plan')
  expect(documentChip).toHaveAttribute('href', mentionUrls.document)
  const person = await chip('user', scope)
  expect(person).toHaveTextContent('@Viewer')
  expect(person).toHaveAttribute('href', mentionUrls.user)
  expect(scope instanceof Element ? scope.textContent : document.body.textContent).not.toContain('](')
}

beforeEach(() => {
  for (const mock of Object.values(api)) mock.mockReset()
  resetAgentRecordCache()
})

describe('MentionBody', () => {
  it('shows references in a stored markdown body as chips', async () => {
    render(<Shell data={mentionFixture()}><MentionBody body={referenceBody}/></Shell>)
    await expectReferenceChips()
  })

  it('fetches an issue the paged client does not hold, once, and keeps the stored label when it is gone', async () => {
    api.fetchIssueRecord.mockImplementation(async (id: string) => {
      if (id === 'TST-9') return makeIssue({ id: '0a1b2c3d-1111-2222-3333-444455556666', identifier: 'TST-9', title: 'Remote issue' })
      throw new Error('not found')
    })
    const data = mentionFixture({ issueCollectionPaged: true, issues: [], projects: [] })
    render(<Shell data={data}><MentionBody body="[TST-9](/workspace/issue/TST-9/remote) and [TST-9](/workspace/issue/TST-9/remote) and [TST-404](/workspace/issue/TST-404/gone)"/></Shell>)
    await waitFor(() => expect(document.querySelectorAll('a[data-agent-entity="issue"]')).toHaveLength(3))
    await waitFor(() => expect(document.querySelectorAll('a[data-agent-entity="issue"]')[0]).toHaveTextContent('TST-9 Remote issue'))
    expect(api.fetchIssueRecord.mock.calls.filter(call => call[0] === 'TST-9')).toHaveLength(1)
    await waitFor(() => expect(document.querySelectorAll('a[data-agent-entity="issue"]')[2]).toHaveAttribute('data-mention-state', 'missing'))
    expect(document.querySelectorAll('a[data-agent-entity="issue"]')[2]).toHaveTextContent('TST-404')
  })

  it('keeps the stored label, marked unavailable, for a deleted document or person in a stored document', async () => {
    const doc = { type: 'doc', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'Read ' }, { type: 'mention', attrs: { mentionType: 'document', id: 'deleted-doc', label: 'Old plan', href: '/workspace/document/deleted' } }, { type: 'text', text: ' from ' }, { type: 'mention', attrs: { mentionType: 'user', id: 'user-gone', label: 'Former Colleague' } }] }] }
    render(<Shell data={mentionFixture()}><MentionBody body="" data={doc}/></Shell>)
    expect(await chip('document')).toHaveAttribute('data-mention-state', 'missing')
    expect(document.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Old plan')
    expect(await screen.findByText('@Former Colleague')).toHaveAttribute('data-mention-state', 'missing')
  })
})

describe('issue loading preview', () => {
  it('renders references in the description as chips instead of raw links', async () => {
    const issue = makeIssue({ identifier: 'TST-2', id: 'issue-2', description: referenceBody, isSummary: false })
    render(<Shell data={mentionFixture()}><IssueLoadingPreview issue={issue} onBack={vi.fn()}/></Shell>)
    await expectReferenceChips(document.querySelector('.issue-loading-preview-description')!)
  })

  it('fetches a referenced issue the paged client does not hold', async () => {
    api.fetchIssueRecord.mockResolvedValue(makeIssue({ id: '0a1b2c3d-1111-2222-3333-444455556666', identifier: 'TST-9', title: 'Remote issue' }))
    const data = mentionFixture({ issueCollectionPaged: true, issues: [], projects: [] })
    render(<Shell data={data}><IssueLoadingPreview issue={makeIssue({ description: 'Blocked by [TST-9](/workspace/issue/TST-9/remote)', isSummary: false })} onBack={vi.fn()}/></Shell>)
    expect(await chip('issue')).toHaveTextContent('TST-9 Remote issue')
    expect(api.fetchIssueRecord).toHaveBeenCalledTimes(1)
  })
})

const row: InboxNotificationRowData = { id: 'n1', actor: 'Teammate', kind: 'comment', identifier: 'TST-1', title: 'Test issue', body: 'Teammate commented: see [TST-2](/workspace/issue/TST-2/x) and **bold** ![shot](https://x.test/a.png)', timeLabel: '2h', timestamp: 'Sep 12', read: false }

describe('inbox snippets', () => {
  it('shows a notification body as plain text, never as link syntax', () => {
    const { container } = render(<InboxNotificationRow notification={row} onOpen={vi.fn()} onReadChange={vi.fn()} onDelete={vi.fn()} onSnooze={vi.fn()}/>)
    const body = container.querySelector('.flow-inbox-row__body')!
    expect(body.textContent).toBe('Teammate commented: see TST-2 and bold shot')
    expect(container.innerHTML).not.toContain('](')
    expect(container.querySelector('[aria-label*="TST-2"]')?.getAttribute('aria-label')).not.toContain('](')
  })
})

describe('inbox hosts', () => {
  const update = { id: 'u1', body: referenceBody, health: 'onTrack', createdAt: '2026-09-01T00:00:00Z', user: viewer } as ProjectUpdate

  it('renders update bodies in the update stream as chips', async () => {
    render(<Shell data={mentionFixture()}><BaseUpdateInboxView surfaceId="LS-0493" entityKind="project" entityName="Project one" entityColor="#5e6ad2" updates={[{ id: 'u1', body: referenceBody, health: 'onTrack', createdAt: '2026-09-01T00:00:00Z', user: viewer }]} viewer={viewer} onOpenEntity={vi.fn()}/></Shell>)
    await expectReferenceChips(document.querySelector('.flow-inbox-host__update')!)
  })

  it('renders the project overview description and latest update with chips and the summary as plain text', async () => {
    const project = { ...baseProject, summary: 'Ships [TST-1](/workspace/issue/TST-1/test-issue)', description: referenceBody } as Project
    const { container } = render(<Shell data={mentionFixture()}><ProjectOverviewInboxView project={project} latestUpdate={update} onOpenProject={vi.fn()}/></Shell>)
    expect(container.querySelector('.flow-inbox-host__summary')!.textContent).toBe('Ships TST-1')
    const description = container.querySelector('.flow-inbox-host__description')! as HTMLElement
    await waitFor(() => expect(within(description).getAllByRole('link').length).toBeGreaterThan(0))
    await expectReferenceChips(description)
    await expectReferenceChips(container.querySelector('.flow-inbox-host__latest-update')!)
  })
})

describe('hover cards', () => {
  it('shows a project summary / description as plain text', async () => {
    const data = mentionFixture()
    const project = { ...data.projects[0], summary: '', description: 'Plan: [Launch plan](/workspace/document/plan-abc) with **care**' } as Project
    render(<Shell data={data}><AgentEntityHover data={data} entity={{ kind: 'project', project }}><button type="button">Open</button></AgentEntityHover></Shell>)
    await act(async () => { screen.getByRole('button', { name: 'Open' }).focus() })
    const card = await waitFor(() => {
      const element = document.querySelector('[data-agent-entity-card="project"]')
      expect(element).not.toBeNull()
      return element as HTMLElement
    })
    expect(card).toHaveTextContent('Plan: Launch plan with care')
    expect(card.textContent).not.toContain('](')
  })
})
