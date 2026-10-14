/**
 * Inline comments on a project or initiative description, like a document's: "Comment" on the description's selection
 * toolbar opens a composer beside the text; the thread is saved on the entity with the selection as its anchor and quote,
 * underlines that text, and shows as a card in the right gutter (a popover under the text while a side panel takes the
 * room). Resolved threads move to the "Show resolved comments" panel. Inline threads are not part of the Activity feed.
 *
 * Descriptions are plain markdown, so the anchor is never written into the text: a thread finds its text again by quote.
 */
import { useCallback, useMemo } from 'react'
import { toast } from '@/lib/toast'
import { useI18n } from '@/i18n/i18n'
import { createEntityComment, deleteEntityComment, resolveEntityComment, setEntityThreadSubscription, toggleEntityCommentReaction, updateEntityComment } from '@/lib/api'
import type { BootstrapData, Comment } from '@/types/flow'
import type { InlineThread } from '@/components/documents/inline-comments/inline-comments-model'
import type { ThreadActions } from '@/components/documents/inline-comments/inline-thread-card'
import { InlineCommentGutter, ResolvedThreadsPanel, type NewInlineThread } from '@/components/documents/inline-comments/inline-comment-gutter'
import { CREATE_ISSUE_FROM_SELECTION } from './description-selection-actions'
import type { EntityInlineCommentsState } from './entity-inline-comments-state'
import './entity-inline-comments.css'

/** The project or initiative whose description carries the threads. */
export type InlineCommentEntity = {
  kind: 'project' | 'initiative'
  id: string
  name: string
  /** Path of the entity's page (comment links point at it). */
  href?: string
  /** Team an issue made from a comment goes to. */
  teamId?: string
}

function entityThreadMuted(data: BootstrapData, entity: InlineCommentEntity, thread: InlineThread) {
  return (data.threadSubscriptions ?? []).some(item => item.userId === data.viewer.id && item.commentId === thread.root.id && item.state === 'muted' && !item.issueId
    && (entity.kind === 'project' ? item.projectId === entity.id : item.initiativeId === entity.id))
}

function canModerate(data: BootstrapData) {
  return data.viewerRole === 'admin' || String(data.viewerRole) === 'owner'
}

function useEntityThreadActions(data: BootstrapData, entity: InlineCommentEntity, onReload: () => Promise<void>): ThreadActions {
  const { t } = useI18n()
  return useMemo(() => {
    const target = { kind: entity.kind, id: entity.id }
    const fail = (fallback: string) => (error: unknown) => { toast.error(error instanceof Error ? error.message : t(fallback)) }
    const commentUrl = (comment: Comment) => `${new URL(entity.href ?? window.location.pathname, window.location.origin).href}#comment-${comment.id}`
    return {
      reply: async (thread, body, bodyData) => {
        try { await createEntityComment(target, { body, bodyData, parentId: thread.root.id }); await onReload() }
        catch (error) { fail('Could not add reply')(error); throw error }
      },
      edit: async (comment, body, bodyData) => {
        try { await updateEntityComment(target, comment.id, { body, bodyData, expectedVersion: comment.version }); await onReload() }
        catch (error) { fail('Could not edit comment')(error); throw error }
      },
      remove: async (_thread, comment) => {
        try { await deleteEntityComment(target, comment.id); await onReload(); toast.success(t('Comment deleted')) }
        catch (error) { fail('Could not delete comment')(error) }
      },
      react: async (comment, emoji) => {
        try { await toggleEntityCommentReaction(target, comment.id, emoji); await onReload() }
        catch (error) { fail('Could not update reaction')(error) }
      },
      resolve: async (thread, resolved) => {
        try {
          await resolveEntityComment(target, thread.root.id, resolved, thread.root.version || undefined)
          await onReload()
          toast.success(t(resolved ? 'Thread resolved' : 'Thread reopened'))
        } catch (error) { fail('Could not update thread')(error) }
      },
      toggleThreadSubscription: async thread => {
        const muted = entityThreadMuted(data, entity, thread)
        try {
          await setEntityThreadSubscription(target, thread.root.id, muted ? null : 'muted')
          await onReload()
          toast.success(t(muted ? 'Subscribed to thread' : 'Unsubscribed from thread'))
        } catch (error) { fail('Could not update thread subscription')(error) }
      },
      copyLink: async comment => {
        try { await navigator.clipboard.writeText(commentUrl(comment)); toast.success(t('Copied comment link to clipboard')) }
        catch { toast.error(t('Could not copy comment link')) }
      },
      newIssue: comment => {
        const teamId = entity.teamId ?? data.teams[0]?.id
        if (!teamId) return
        window.dispatchEvent(new CustomEvent(CREATE_ISSUE_FROM_SELECTION, { detail: { text: `${comment.body}\n\n${t('From')} [${entity.name}](${commentUrl(comment)})`, teamId, projectId: entity.kind === 'project' ? entity.id : undefined } }))
      },
    } satisfies ThreadActions
  }, [data, entity, onReload, t])
}

/** The gutter (or popover) of a description's open inline threads and the composer of a new one. */
export function EntityInlineComments({ state, entity, data, railOpen, onReload }: { state: EntityInlineCommentsState; entity: InlineCommentEntity; data: BootstrapData; railOpen: boolean; onReload: () => Promise<void> }) {
  const actions = useEntityThreadActions(data, entity, onReload)
  const createThread = useCallback((input: NewInlineThread) => createEntityComment({ kind: entity.kind, id: entity.id }, input), [entity.id, entity.kind])
  return <InlineCommentGutter
    comments={state.anchored}
    viewer={data.viewer}
    users={data.users}
    editor={state.editor}
    shell={state.shell}
    draft={state.draft}
    onDraftChange={state.setDraft}
    canComment
    writesMarks={false}
    canModerate={canModerate(data)}
    onGutterChange={state.setGutter}
    railOpen={railOpen}
    actions={actions}
    muted={thread => entityThreadMuted(data, entity, thread)}
    createThread={createThread}
    onReload={onReload}
  />
}

/** "Resolved comments" of a description, in place of the details panel. */
export function EntityResolvedCommentsPanel({ state, entity, data, onReload, onClose }: { state: EntityInlineCommentsState; entity: InlineCommentEntity; data: BootstrapData; onReload: () => Promise<void>; onClose: () => void }) {
  const actions = useEntityThreadActions(data, entity, onReload)
  return <ResolvedThreadsPanel comments={state.anchored} viewer={data.viewer} users={data.users} editor={state.editor} canComment canModerate={canModerate(data)} actions={actions} muted={thread => entityThreadMuted(data, entity, thread)} source={entity.name} onClose={onClose}/>
}
