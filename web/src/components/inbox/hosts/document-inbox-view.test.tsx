import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { zhCN } from '@/i18n/translations'
import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import type { FlowDocument, Notification } from '@/types/flow'

const api = vi.hoisted(() => ({ listDocumentComments: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))
vi.mock('@/components/activity/rich-comment', () => ({ RichComment: ({ body }: { body: string }) => <p>{body}</p> }))

import { DocumentInboxView, documentNotificationKind, documentNotificationLine, documentNotificationPath, isDocumentNotification } from './document-inbox-view'

const flowDocument = { id: 'document-1', slugId: 'plan-1a2b3c4d5e6f', title: 'Plan', content: '', creator: viewer, projectIds: [], teamIds: [], subscriberIds: [], favorite: false, createdAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-01T00:00:00Z', revisions: [] } as FlowDocument

function notification(overrides: Partial<Notification>): Notification {
  return {
    id: 'n1', recipientId: viewer.id, type: 'documentNewComment', sourceType: 'document', sourceId: 'document-1', issueId: '', actor: teammate,
    category: 'comments', groupKey: 'g', occurrenceCount: 1, latestActorIds: [teammate.id], favorite: false,
    createdAt: '2026-10-02T00:00:00Z', updatedAt: '2026-10-02T00:00:00Z', ...overrides,
  } as Notification
}

describe('document notifications in the inbox', () => {
  it('classifies and words every document notification type', () => {
    const t = (value: string) => value
    expect(isDocumentNotification(notification({}))).toBe(true)
    expect(isDocumentNotification(notification({ sourceType: 'issue', type: 'comment' }))).toBe(false)
    expect(documentNotificationLine(notification({ payload: { excerpt: 'Looks good' } }), t)).toBe('Teammate commented on the document: Looks good')
    expect(documentNotificationLine(notification({ type: 'documentCommentReaction', payload: { emoji: '👍' } }), t)).toBe('Teammate reacted 👍 to your comment')
    expect(documentNotificationLine(notification({ type: 'documentChanges', occurrenceCount: 3 }), t)).toBe('Teammate edited the document · 3 updates')
    expect(documentNotificationKind(notification({ type: 'documentMention' }))).toBe('mention')
    expect(documentNotificationKind(notification({ type: 'documentMoved' }))).toBe('generic')
    expect(documentNotificationPath('ws', notification({ commentId: 'c1' }), flowDocument)).toBe('/ws/document/plan-1a2b3c4d5e6f#comment-c1')
    expect(documentNotificationPath('ws', notification({ payload: { documentSlugId: 'old-slug' } }))).toBe('/ws/document/old-slug')
    for (const type of ['documentMention', 'documentCommentMention', 'documentNewComment', 'documentThreadResolved', 'documentCommentReaction', 'documentChanges', 'documentMoved', 'documentDeleted', 'documentRestored', 'documentAddedAsOwner', 'documentRemovedAsOwner', 'documentSubscribed', 'documentUnsubscribed']) {
      const line = documentNotificationLine(notification({ type }), source => zhCN[source] ?? `MISSING:${source}`)
      expect(line, type).not.toMatch(/MISSING/)
    }
  })

  it('renders the document, the thread with its quote, and a link to the comment', async () => {
    api.listDocumentComments.mockResolvedValue([
      { id: 'root', version: 1, body: 'Is this final?', reactions: {}, createdAt: '2026-10-02T00:00:00Z', user: teammate, anchorId: 'cmt_a', quotedText: 'three phases' },
      { id: 'reply', version: 1, body: 'Yes', parentId: 'root', reactions: {}, createdAt: '2026-10-02T01:00:00Z', user: viewer },
    ])
    const data = makeBootstrap({ documents: [flowDocument] })
    render(<MemoryRouter><I18nProvider><DocumentInboxView data={data} notification={notification({ commentId: 'reply' })}/></I18nProvider></MemoryRouter>)
    expect(screen.getByRole('heading', { name: 'Plan' })).toBeTruthy()
    await waitFor(() => expect(screen.getByText('Is this final?')).toBeTruthy())
    expect(screen.getByText('three phases')).toBeTruthy()
    expect(screen.getByRole('link', { name: /Open document/ }).getAttribute('href')).toBe(`/${data.workspace.urlKey}/document/plan-1a2b3c4d5e6f#comment-reply`)
  })
})
