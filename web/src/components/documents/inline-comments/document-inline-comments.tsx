/**
 * Inline comments on a document (the reference app has no page-level comment
 * list): the shared inline comment gutter (inline-comment-gutter.tsx) wired to
 * the document's comment API. Editors write the anchor mark into the
 * collaborative document, so the anchor follows concurrent edits; commenters
 * anchor by quote. Threads without an anchor (page-level comments written
 * before inline comments) stay visible, pinned to the top of the gutter.
 */
import type { Editor } from '@tiptap/react'
import { useCallback, useMemo } from 'react'
import { toast } from '@/lib/toast'

import { useI18n } from '@/i18n/i18n'
import { documentPath } from '@/lib/app-routes'
import { createDocumentComment, deleteDocumentComment, resolveDocumentComment, setDocumentThreadSubscription, toggleDocumentCommentReaction, updateDocumentComment, uploadDocumentAttachment } from '@/lib/api'
import { IssueAgentTasks } from '@/components/agent/issue-agent-tasks'
import type { AgentThreadControls } from '@/components/agent/issue-agent-task-context'
import type { BootstrapData, Comment, FlowDocument } from '@/types/flow'
import { removeAnchorMark, type InlineThread } from './inline-comments-model'
import type { ThreadActions } from './inline-thread-card'
import { InlineCommentGutter, ResolvedCommentsButton, ResolvedThreadsPanel, type CommentDraft } from './inline-comment-gutter'

export type { CommentDraft } from './inline-comment-gutter'
export { GUTTER_MIN_VIEWPORT } from './inline-comment-gutter'

function threadMuted(data: BootstrapData, document: FlowDocument, thread: InlineThread) {
  return (data.threadSubscriptions ?? []).some(item => item.userId === data.viewer.id && item.issueId === document.id && item.commentId === thread.root.id && item.state === 'muted')
}

/** An agent mentioned in a document comment answers inside the comment's card. */
function useAgentThreadRenderer(data: BootstrapData, document: FlowDocument) {
  return useCallback((threadIds: string[], controls: AgentThreadControls) => <IssueAgentTasks commentId={threadIds} data={data} issue={{ id: document.id }} onUpload={async file => (await uploadDocumentAttachment(document.id, file)).url} resourceType="document" teamId={document.teamIds[0]} threadControls={controls} variant="document"/>, [data, document.id, document.teamIds])
}

function useThreadActions({ data, document, editor, canEdit, onReload }: { data: BootstrapData; document: FlowDocument; editor: Editor | null; canEdit: boolean; onReload: () => Promise<void> }): ThreadActions {
  const { t } = useI18n()
  return useMemo(() => {
    const fail = (fallback: string) => (error: unknown) => { toast.error(error instanceof Error ? error.message : t(fallback)) }
    return {
      reply: async (thread, body, bodyData) => {
        try { await createDocumentComment(document.id, { body, bodyData, parentId: thread.root.id }); await onReload() }
        catch (error) { fail('Could not add reply')(error); throw error }
      },
      edit: async (comment, body, bodyData) => {
        try { await updateDocumentComment(document.id, comment.id, { body, bodyData, expectedVersion: comment.version }); await onReload() }
        catch (error) { fail('Could not edit comment')(error); throw error }
      },
      remove: async (thread, comment) => {
        try {
          await deleteDocumentComment(document.id, comment.id)
          if (comment.id === thread.root.id && canEdit) removeAnchorMark(editor, thread.anchorId)
          await onReload()
          toast.success(t('Comment deleted'))
        } catch (error) { fail('Could not delete comment')(error) }
      },
      react: async (comment, emoji) => {
        try { await toggleDocumentCommentReaction(document.id, comment.id, emoji); await onReload() }
        catch (error) { fail('Could not update reaction')(error) }
      },
      resolve: async (thread, resolved) => {
        try {
          await resolveDocumentComment(document.id, thread.root.id, resolved, thread.root.version)
          await onReload()
          toast.success(t(resolved ? 'Thread resolved' : 'Thread reopened'))
        } catch (error) { fail('Could not update thread')(error) }
      },
      toggleThreadSubscription: async thread => {
        const muted = threadMuted(data, document, thread)
        try {
          await setDocumentThreadSubscription(document.id, thread.root.id, muted ? null : 'muted')
          await onReload()
          toast.success(t(muted ? 'Subscribed to thread' : 'Unsubscribed from thread'))
        } catch (error) { fail('Could not update thread subscription')(error) }
      },
      copyLink: async comment => {
        try {
          await navigator.clipboard.writeText(`${new URL(documentPath(data.workspace.urlKey, document), window.location.origin).href}#comment-${comment.id}`)
          toast.success(t('Copied comment link to clipboard'))
        } catch { toast.error(t('Could not copy comment link')) }
      },
      newIssue: comment => {
        const url = `${new URL(documentPath(data.workspace.urlKey, document), window.location.origin).href}#comment-${comment.id}`
        window.dispatchEvent(new CustomEvent('flow-create-issue-from-selection', { detail: { text: `${comment.body}\n\n${t('From')} [${document.title || t('Untitled document')}](${url})`, teamId: document.teamIds[0] ?? data.teams[0]?.id, projectId: document.projectIds[0] } }))
      },
    } satisfies ThreadActions
  }, [canEdit, data, document, editor, onReload, t])
}

export interface DocumentInlineCommentsProps {
  data: BootstrapData
  document: FlowDocument
  comments: Comment[]
  editor: Editor | null
  /** The positioned element wrapping the editor; card tops are measured against it. */
  shell: HTMLElement | null
  draft?: CommentDraft
  onDraftChange: (draft?: CommentDraft) => void
  canComment: boolean
  /** Editors write the anchor mark into the document; commenters anchor by quote. */
  canEdit: boolean
  /** "Show comments" view option: hides the gutter cards (not the composer). */
  visible?: boolean
  onReload: () => Promise<void>
  /** Reports whether gutter cards are shown, so the page can shift the document left. */
  onGutterChange?: (shown: boolean) => void
  /** The agent rail is open: no room beside the text, so threads open as popovers. */
  railOpen?: boolean
}

export function DocumentInlineComments({ data, document, comments, editor, shell, draft, onDraftChange, canComment, canEdit, visible = true, onReload, onGutterChange, railOpen = false }: DocumentInlineCommentsProps) {
  const actions = useThreadActions({ data, document, editor, canEdit, onReload })
  const renderAgentThread = useAgentThreadRenderer(data, document)
  const canModerate = data.viewerRole === 'admin' || String(data.viewerRole) === 'owner'
  const createThread = useCallback((input: { body: string; bodyData?: Record<string, unknown>; anchorId: string; quotedText: string }) => createDocumentComment(document.id, input), [document.id])
  return <InlineCommentGutter
    comments={comments}
    viewer={data.viewer}
    users={data.users}
    editor={editor}
    shell={shell}
    draft={draft}
    onDraftChange={onDraftChange}
    canComment={canComment}
    writesMarks={canEdit}
    canModerate={canModerate}
    visible={visible}
    onGutterChange={onGutterChange}
    railOpen={railOpen}
    actions={actions}
    muted={thread => threadMuted(data, document, thread)}
    renderAgentThread={renderAgentThread}
    createThread={createThread}
    onReload={onReload}
  />
}

/** Header button (shown only when resolved threads exist) toggling the "Resolved comments" panel. */
export function DocumentResolvedCommentsButton({ count, open, onToggle }: { count: number; open: boolean; onToggle: () => void }) {
  return <ResolvedCommentsButton count={count} open={open} onToggle={onToggle}/>
}

/** "Resolved comments": the quote, the thread and its reactions; "Reopen thread" in each "…" menu. */
export function ResolvedCommentsPanel({ data, document, comments, editor, canComment, canEdit, onReload, onClose }: { data: BootstrapData; document: FlowDocument; comments: Comment[]; editor: Editor | null; canComment: boolean; canEdit: boolean; onReload: () => Promise<void>; onClose: () => void }) {
  const actions = useThreadActions({ data, document, editor, canEdit, onReload })
  const renderAgentThread = useAgentThreadRenderer(data, document)
  const canModerate = data.viewerRole === 'admin' || String(data.viewerRole) === 'owner'
  return <ResolvedThreadsPanel comments={comments} viewer={data.viewer} users={data.users} editor={editor} canComment={canComment} canModerate={canModerate} actions={actions} muted={thread => threadMuted(data, document, thread)} renderAgentThread={renderAgentThread} onClose={onClose}/>
}
