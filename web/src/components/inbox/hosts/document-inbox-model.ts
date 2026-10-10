import type { BootstrapData, FlowDocument, Notification } from '@/types/flow'
import type { InboxNotificationKind } from '../notification-row'

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
