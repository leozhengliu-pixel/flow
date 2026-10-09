import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import { makeIssue } from '@/test/fixtures'
import type { BootstrapData } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn(), listIssueRecords: vi.fn() }))
vi.mock('@/lib/api', () => api)

import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { RichComment } from '@/components/activity/rich-comment'
import { mentionFixture, mentionLabels, mentionUrls } from './mention-fixtures'
import { mentionAttrsForTarget } from './mention-model'
import { parseAgentEntityUrl } from '@/components/agent/agent-entity-refs'

function Where() {
  const location = useLocation()
  return <output data-testid="location">{`${location.pathname}${location.hash}`}</output>
}

function Shell({ children, data }: { children: ReactNode; data: BootstrapData }) {
  return <I18nProvider><MemoryRouter><WorkspaceStoreProvider account={null} data={data} session={null}>{children}<Where/></WorkspaceStoreProvider></MemoryRouter></I18nProvider>
}

const bodyDoc = (...attrs: Array<Record<string, string>>) => ({ type: 'doc', content: [{ type: 'paragraph', content: attrs.flatMap((attr, index) => [{ type: 'text', text: index ? ' ' : 'See ' }, { type: 'mention', attrs: attr }]) }] })

function attrsFor(data: BootstrapData, kind: keyof typeof mentionUrls) {
  if (kind === 'user') return { mentionType: 'user', id: 'user-1', label: 'Viewer' }
  const target = parseAgentEntityUrl(mentionUrls[kind], data)!
  return mentionAttrsForTarget(data, target, mentionUrls[kind])
}

function renderMentions(data: BootstrapData, ...attrs: Array<Record<string, string>>) {
  const doc = bodyDoc(...attrs)
  return render(<Shell data={data}><RichComment body="" data={doc}/></Shell>)
}

async function chip(kind: string) {
  return waitFor(() => {
    const found = document.querySelector(`a[data-agent-entity="${kind}"]`)
    expect(found, kind).not.toBeNull()
    return found as HTMLAnchorElement
  })
}

beforeEach(() => {
  for (const mock of Object.values(api)) mock.mockReset()
  resetAgentRecordCache()
})

describe('mention chips in rich text', () => {
  it('renders every resource type as a chip with its icon, live label and in-app link', async () => {
    const data = mentionFixture()
    const kinds = Object.keys(mentionUrls) as Array<keyof typeof mentionUrls>
    renderMentions(data, ...kinds.map(kind => attrsFor(data, kind)))
    const expected: Record<string, string> = { ...mentionLabels, issue: 'TST-1 Test issue', user: '@Viewer', milestone: 'Alpha · Project one' }
    for (const kind of kinds) {
      const found = await chip(kind)
      expect(found.textContent?.replace(/ /g, ' ').trim(), kind).toBe(expected[kind])
      expect(found.getAttribute('href'), kind).toBe(kind === 'user' ? '/workspace/profiles/viewer' : mentionUrls[kind])
    }
    expect(document.querySelector('a[data-agent-entity="issue"] svg')).not.toBeNull()
  })

  it('shows the resource\'s current title, not the one stored when it was mentioned', async () => {
    const data = mentionFixture({ issues: [makeIssue({ title: 'Renamed issue' })] })
    renderMentions(data, { mentionType: 'issue', id: 'issue-1', label: 'TST-1', title: 'Old title', href: mentionUrls.issue })
    expect(await chip('issue')).toHaveTextContent('TST-1 Renamed issue')
  })

  it('navigates inside the app on click and leaves a modified click to the browser', async () => {
    const user = userEvent.setup()
    const data = mentionFixture()
    renderMentions(data, attrsFor(data, 'document'))
    await user.click(await chip('document'))
    expect(screen.getByTestId('location')).toHaveTextContent('/workspace/document/plan-abc')
    const before = screen.getByTestId('location').textContent
    await user.keyboard('{Meta>}')
    await user.click(await chip('document'))
    await user.keyboard('{/Meta}')
    expect(screen.getByTestId('location').textContent).toBe(before)
  })

  it('opens the resource\'s hover card on focus', async () => {
    const data = mentionFixture()
    for (const [kind, texts] of [['issue', ['TST-1', 'Test issue', 'In progress']], ['project', ['Project one', 'High']], ['document', ['Launch plan', 'Last edited']], ['user', ['Viewer', '@viewer']]] as const) {
      const { unmount } = renderMentions(data, attrsFor(data, kind))
      const found = await chip(kind)
      await act(async () => { found.focus() })
      const card = await waitFor(() => {
        const element = document.querySelector(`[data-agent-entity-card="${kind}"]`)
        expect(element, kind).not.toBeNull()
        return element as HTMLElement
      })
      for (const text of texts) expect(card, `${kind}: ${text}`).toHaveTextContent(text)
      unmount()
    }
  })

  it('falls back to the stored label, marked unavailable, when the resource is gone', async () => {
    const data = mentionFixture()
    renderMentions(data, { mentionType: 'document', id: 'deleted-doc', label: 'Old plan', href: '/workspace/document/deleted' }, { mentionType: 'user', id: 'user-gone', label: 'Former Colleague' })
    const documentChip = await chip('document')
    expect(documentChip).toHaveTextContent('Old plan')
    expect(documentChip).toHaveAttribute('data-mention-state', 'missing')
    expect(documentChip).toHaveAttribute('title', 'This item was deleted or you do not have access to it')
    expect(await screen.findByText('@Former Colleague')).toHaveAttribute('data-mention-state', 'missing')
  })

  describe('paged workspaces', () => {
    const paged = () => mentionFixture({ issueCollectionPaged: true, issues: [], projects: [] })

    it('fetches an issue mention the client does not hold, once, and shows its title and card', async () => {
      api.fetchIssueRecord.mockResolvedValue(makeIssue({ id: '0a1b2c3d-1111-2222-3333-444455556666', identifier: 'TST-9', title: 'Remote issue' }))
      renderMentions(paged(), { mentionType: 'issue', id: '0a1b2c3d-1111-2222-3333-444455556666', label: 'TST-9', title: '', href: '/workspace/issue/TST-9/remote' }, { mentionType: 'issue', id: '0a1b2c3d-1111-2222-3333-444455556666', label: 'TST-9', title: '', href: '/workspace/issue/TST-9/remote' })
      await waitFor(() => expect(document.querySelectorAll('a[data-agent-entity="issue"]')).toHaveLength(2))
      expect(api.fetchIssueRecord).toHaveBeenCalledTimes(1)
      expect(document.querySelector('a[data-agent-entity="issue"]')).toHaveTextContent('TST-9 Remote issue')
    })

    it('shows the stored label while loading and when the fetch fails', async () => {
      api.fetchIssueRecord.mockRejectedValue(new Error('not found'))
      renderMentions(paged(), { mentionType: 'issue', id: '0a1b2c3d-9999-2222-3333-444455556666', label: 'TST-404', title: 'Gone issue', href: '/workspace/issue/TST-404/gone' })
      await waitFor(() => expect(document.querySelector('a[data-agent-entity="issue"]')).toHaveAttribute('data-mention-state', 'missing'))
      expect(document.querySelector('a[data-agent-entity="issue"]')).toHaveTextContent('TST-404 Gone issue')
    })

    it('fetches a project missing from the list view by slug', async () => {
      api.listProjectRecords.mockResolvedValue({ items: mentionFixture().projects, hasMore: false, total: 1 })
      renderMentions(paged(), { mentionType: 'project', id: 'project-one', label: 'project-one', title: '', href: mentionUrls.project })
      expect(await chip('project')).toHaveTextContent('Project one')
    })
  })
})
