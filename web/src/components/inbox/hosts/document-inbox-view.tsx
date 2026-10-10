/**
 * Inbox rendering for document notifications (documentMention,
 * documentNewComment, documentThreadResolved, documentCommentReaction,
 * documentChanges, documentMoved, documentDeleted, documentRestored,
 * documentAddedAsOwner / RemovedAsOwner, documentSubscribed / Unsubscribed,
 * documentReminder): the row's localized line and the detail view.
 */
import { ArrowUpRight, FileText } from 'lucide-react'
import { useEffect, useState } from 'react'

import { RichComment } from '@/components/activity/rich-comment'
import { DocumentGlyph } from '@/components/documents/document-icon'
import { AppLink } from '@/components/ui/app-link'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import { listDocumentComments } from '@/lib/api'
import type { BootstrapData, Comment, FlowDocument, Notification } from '@/types/flow'
import type { InboxNotificationKind } from '../notification-row'
import './document-inbox-view.css'

type Translate = (source: string) => string

const LINES: Record<string, string> = {
  documentMention: '{actor} mentioned you in the document',
  documentCommentMention: '{actor} mentioned you in a comment',
  documentNewComment: '{actor} commented on the document',
  documentThreadResolved: '{actor} resolved a comment thread',
  documentCommentReaction: '{actor} reacted {emoji} to your comment',
  documentChanges: '{actor} edited the document',
  documentMoved: '{actor} moved the document',
  documentDeleted: '{actor} deleted the document',
  documentRestored: '{actor} restored the document',
  documentAddedAsOwner: '{actor} made you an owner of the document',
  documentRemovedAsOwner: '{actor} removed you as an owner of the document',
  documentSubscribed: '{actor} subscribed you to the document',
  documentUnsubscribed: '{actor} unsubscribed you from the document',
  documentReminder: '{actor} set a reminder',
}

export function isDocumentNotification(notification: Pick<Notification, 'sourceType' | 'type'>) {
  return notification.sourceType === 'document' || notification.type.startsWith('document')
}

/** The row line ("Ann commented on the document: …") in the active locale. */
export function documentNotificationLine(notification: Notification, t: Translate) {
  const template = LINES[notification.type] ?? '{actor} updated the document'
  const actor = notification.actor.displayName || notification.actor.name || t('Someone')
  const line = t(template).replace('{actor}', actor).replace('{emoji}', String(notification.payload?.emoji ?? ''))
  const excerpt = typeof notification.payload?.excerpt === 'string' ? notification.payload.excerpt : ''
  const occurrences = notification.occurrenceCount > 1 ? ` · ${t('{count} updates').replace('{count}', String(notification.occurrenceCount))}` : ''
  return (excerpt && /Comment|Mention$/.test(notification.type) && notification.type !== 'documentMention' ? `${line}: ${excerpt}` : line) + occurrences
}

export function documentNotificationKind(notification: Pick<Notification, 'type'>): InboxNotificationKind {
  if (notification.type === 'documentMention' || notification.type === 'documentCommentMention') return 'mention'
  if (notification.type === 'documentNewComment' || notification.type === 'documentThreadResolved' || notification.type === 'documentCommentReaction') return 'comment'
  return 'generic'
}

export function notificationDocument(data: Pick<BootstrapData, 'documents'>, notification: Pick<Notification, 'sourceId'>) {
  return data.documents?.find(document => document.id === notification.sourceId)
}

/** The document's URL (with the comment anchor for comment notifications). */
export function documentNotificationPath(workspaceKey: string, notification: Notification, document?: FlowDocument) {
  const slug = document?.slugId ?? (typeof notification.payload?.documentSlugId === 'string' ? notification.payload.documentSlugId : notification.sourceId)
  return `/${workspaceKey}/document/${encodeURIComponent(slug)}${notification.commentId ? `#comment-${notification.commentId}` : ''}`
}

export function DocumentInboxView({ data, notification }: { data: BootstrapData; notification: Notification }) {
  const { t, formatDate } = useI18n()
  const document = notificationDocument(data, notification)
  // Document threads are loaded per document, not with the workspace.
  const [comments, setComments] = useState<Comment[]>(() => data.comments?.[notification.sourceId] ?? [])
  useEffect(() => {
    if (!notification.commentId || !document) return
    let active = true
    listDocumentComments(document.id).then(items => { if (active) setComments(items) }).catch(() => undefined)
    return () => { active = false }
  }, [document, notification.commentId])
  const title = document?.title || notification.title || t('Untitled document')
  const comment = notification.commentId ? comments.find(item => item.id === notification.commentId) : undefined
  const root = comment?.parentId ? comments.find(item => item.id === comment.parentId) : comment
  const thread = root ? [root, ...comments.filter(item => item.parentId === root.id)] : []
  const quote = root?.quotedText || (typeof notification.payload?.quotedText === 'string' ? notification.payload.quotedText : '')
  const gone = !document && notification.type !== 'documentDeleted'
  return <div className="flow-inbox-document">
    <header>
      <span className="flow-inbox-document__icon">{document ? <DocumentGlyph document={document}/> : <FileText size={16}/>}</span>
      <div>
        <small>{t('Document')}</small>
        <h2 data-i18n-ignore={Boolean(document?.title || notification.title) || undefined}>{title}</h2>
      </div>
      {document && <AppLink className="flow-inbox-document__open" href={documentNotificationPath(data.workspace.urlKey, notification, document)}>{t('Open document')}<ArrowUpRight size={13}/></AppLink>}
    </header>
    <p className="flow-inbox-document__line">
      <UserAvatar avatarUrl={notification.actor.avatarUrl} className="flow-inbox-document__avatar" name={notification.actor.displayName || notification.actor.name}/>
      <span>{documentNotificationLine({ ...notification, payload: { ...notification.payload, excerpt: '' } }, t)}</span>
      <time dateTime={notification.updatedAt}>{formatDate(notification.updatedAt, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</time>
    </p>
    {quote && <blockquote className="flow-inbox-document__quote" data-i18n-ignore>{quote}</blockquote>}
    {thread.length > 0
      ? <div className="flow-inbox-document__thread">{thread.map(item => <article key={item.id} className={item.id === notification.commentId ? 'is-target' : undefined}>
        <header><UserAvatar avatarUrl={item.user.avatarUrl} className="flow-inbox-document__avatar" name={item.user.displayName || item.user.name}/><strong data-i18n-ignore>{item.user.displayName || item.user.name}</strong><time dateTime={item.createdAt}>{formatDate(item.createdAt, { month: 'short', day: 'numeric' })}</time></header>
        <RichComment body={item.body} data={item.bodyData}/>
      </article>)}</div>
      : typeof notification.payload?.excerpt === 'string' && notification.payload.excerpt && <p className="flow-inbox-document__excerpt" data-i18n-ignore>{notification.payload.excerpt}</p>}
    {gone && <p className="flow-inbox-document__gone">{t('This document is no longer available.')}</p>}
  </div>
}
