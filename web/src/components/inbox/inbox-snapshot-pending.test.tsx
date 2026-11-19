import { render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, project, viewer } from '@/test/fixtures'
import type { BootstrapData, Notification } from '@/types/flow'

vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

import { InboxAppPage } from './inbox-app-page'

const reply = {
  id: 'n-project', recipientId: viewer.id, type: 'agentReply', sourceType: 'project', sourceId: project.id, projectId: project.id, issueId: '', commentId: 'c', actor: viewer, category: 'comments',
  groupKey: 'g', occurrenceCount: 1, latestActorIds: [viewer.id], favorite: false, text: 'Flow replied: Done.', title: project.name,
  payload: { excerpt: 'Done.', agentActivity: 'response' }, createdAt: '2026-10-02T00:00:00Z', updatedAt: '2026-10-02T01:00:00Z',
} as unknown as Notification

function page(data: BootstrapData, pending: boolean) {
  const noop = vi.fn(async () => undefined)
  return <I18nProvider><TooltipProvider>
    <InboxAppPage data={data} pending={pending} onReload={noop} onOpenIssue={vi.fn()} onDeleteRelation={noop} onCreateSubIssue={noop} onReactIssue={noop} onCreateComment={noop} onEditComment={noop} onDeleteComment={noop} onReactComment={noop} onUploadAttachment={noop} onDeleteAttachment={noop}/>
  </TooltipProvider></I18nProvider>
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  localStorage.clear()
  localStorage.setItem('flow:locale', 'en-US')
})
afterEach(() => {
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

describe('inbox opened from a cached workspace snapshot', () => {
  it('keeps the notification a link selected while the snapshot has no notifications', async () => {
    window.history.replaceState(null, '', `/workspace/inbox?notification=${reply.id}`)
    const cached = makeBootstrap({ notifications: [], projects: [project], reviews: [], comments: {}, userSettings: {} } as unknown as Partial<BootstrapData>)
    const view = render(page(cached, true))
    view.rerender(page({ ...cached, notifications: [reply] }, false))
    expect(await screen.findByRole('heading', { name: project.name })).toBeInTheDocument()
  })
})
