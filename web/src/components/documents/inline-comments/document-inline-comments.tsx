/**
 * Inline comments on a document (the reference app has no page-level comment
 * list): select text → toolbar "Comment" → a card in the right gutter with
 * "Add a comment…". Threads anchor to an inline comment mark stored in the
 * collaborative document (its id is saved on the thread as anchorId), so the
 * anchor follows concurrent edits. Open threads underline their text; the
 * active one is highlighted. Resolving hides the highlight and the card; the
 * header's comment button opens "Resolved comments".
 *
 * Threads without an anchor (page-level comments written before inline
 * comments) stay visible, pinned to the top of the gutter.
 */
import type { Editor } from '@tiptap/react'
import { MessageSquareText, X } from 'lucide-react'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'

import { Composer } from '@/components/editor/composer'
import { FlowTooltip, TooltipProvider } from '@/components/ui/tooltip'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import { documentPath } from '@/lib/app-routes'
import { createDocumentComment, deleteDocumentComment, resolveDocumentComment, setDocumentThreadSubscription, toggleDocumentCommentReaction, updateDocumentComment } from '@/lib/api'
import type { BootstrapData, Comment, FlowDocument } from '@/types/flow'
import { buildThreads, findMarkRanges, findQuoteRange, INLINE_COMMENT_MARK, layoutGutter, type AnchorRange, type InlineThread } from './inline-comments-model'
import { createInlineCommentsPlugin, inlineCommentsPluginKey, type InlineCommentsMeta, type InlineCommentsPluginState } from './inline-comments-plugin'
import { InlineThreadCard, type ThreadActions } from './inline-thread-card'
import './document-inline-comments.css'

export interface CommentDraft { from: number; to: number; text: string }

const MAX_QUOTE = 4000

export function useInlineThreads(comments: Comment[]) {
  return useMemo(() => {
    const threads = buildThreads(comments)
    return { threads, open: threads.filter(thread => !thread.root.resolved), resolved: threads.filter(thread => thread.root.resolved) }
  }, [comments])
}

function sameRanges(left: Map<string, AnchorRange>, right: Map<string, AnchorRange>) {
  if (left.size !== right.size) return false
  for (const [id, range] of right) {
    const other = left.get(id)
    if (!other || other.from !== range.from || other.to !== range.to) return false
  }
  return true
}

function newAnchorId() {
  const random = typeof crypto !== 'undefined' && 'randomUUID' in crypto ? crypto.randomUUID().replace(/-/g, '') : Math.random().toString(16).slice(2) + Date.now().toString(16)
  return `cmt_${random.slice(0, 24)}`
}

function threadMuted(data: BootstrapData, document: FlowDocument, thread: InlineThread) {
  return (data.threadSubscriptions ?? []).some(item => item.userId === data.viewer.id && item.issueId === document.id && item.commentId === thread.root.id && item.state === 'muted')
}

function useThreadActions({ data, document, editor, canEdit, onReload, onResolved }: { data: BootstrapData; document: FlowDocument; editor: Editor | null; canEdit: boolean; onReload: () => Promise<void>; onResolved?: (thread: InlineThread, resolved: boolean) => void }): ThreadActions {
  const { t } = useI18n()
  const fail = (fallback: string) => (error: unknown) => { toast.error(error instanceof Error ? error.message : t(fallback)) }
  const removeMark = (anchorId: string) => {
    if (!editor || editor.isDestroyed || !canEdit || !anchorId) return
    const type = editor.schema.marks[INLINE_COMMENT_MARK]
    const range = findMarkRanges(editor.state.doc).get(anchorId)
    if (!type || !range) return
    const tr = editor.state.tr
    editor.state.doc.nodesBetween(range.from, range.to, (node, pos) => {
      if (!node.isText) return
      for (const mark of node.marks) if (mark.type === type && mark.attrs.commentId === anchorId) tr.removeMark(pos, pos + node.nodeSize, mark)
    })
    editor.view.dispatch(tr.setMeta('addToHistory', false))
  }
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
        if (comment.id === thread.root.id) removeMark(thread.anchorId)
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
        onResolved?.(thread, resolved)
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
  }
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
}

export function DocumentInlineComments({ data, document, comments, editor, shell, draft, onDraftChange, canComment, canEdit, visible = true, onReload, onGutterChange }: DocumentInlineCommentsProps) {
  const { t } = useI18n()
  const { open } = useInlineThreads(comments)
  const [active, setActive] = useState<string>()
  const [anchors, setAnchors] = useState<Map<string, AnchorRange>>(new Map())
  const [unmarked, setUnmarked] = useState<Set<string>>(new Set())
  const [tops, setTops] = useState<Map<string, number>>(new Map())
  const [heights, setHeights] = useState<Map<string, number>>(new Map())
  const [layoutTick, setLayoutTick] = useState(0)
  const materialized = useRef(new Set<string>())
  const observer = useRef<ResizeObserver | null>(null)
  const cards = useRef(new Map<string, HTMLDivElement>())
  const actions = useThreadActions({ data, document, editor, canEdit, onReload, onResolved: (thread, resolved) => { if (resolved && active === thread.root.id) setActive(undefined) } })
  const canModerate = data.viewerRole === 'admin' || String(data.viewerRole) === 'owner'
  const threadKey = (thread: InlineThread) => thread.root.id

  // Register the decoration plugin on the live editor.
  useEffect(() => {
    if (!editor || editor.isDestroyed) return
    editor.registerPlugin(createInlineCommentsPlugin())
    let last: InlineCommentsPluginState | undefined
    const sync = () => {
      const state = inlineCommentsPluginKey.getState(editor.state) as InlineCommentsPluginState | undefined
      if (!state || state === last) return
      last = state
      setAnchors(current => sameRanges(current, state.ranges) ? current : state.ranges)
      setUnmarked(current => current.size === state.unmarked.size && [...state.unmarked].every(id => current.has(id)) ? current : state.unmarked)
      setLayoutTick(value => value + 1)
    }
    editor.on('transaction', sync)
    return () => {
      editor.off('transaction', sync)
      if (!editor.isDestroyed) editor.unregisterPlugin(inlineCommentsPluginKey)
    }
  }, [editor])

  const dispatchMeta = useCallback((meta: InlineCommentsMeta) => {
    if (!editor || editor.isDestroyed) return
    editor.view.dispatch(editor.state.tr.setMeta(inlineCommentsPluginKey, meta).setMeta('addToHistory', false))
  }, [editor])

  // Open threads and the active one drive the decorations.
  // Keyed by content so re-renders with equal threads never dispatch again
  // (each dispatch is a transaction, which re-renders this component).
  const openKey = JSON.stringify(open.filter(thread => thread.anchorId).map(thread => [thread.anchorId, thread.root.quotedText ?? '']))
  const activeAnchor = open.find(thread => thread.root.id === active)?.anchorId
  useEffect(() => {
    const anchors = (JSON.parse(openKey) as [string, string][]).map(([id, quote]) => ({ id, quote: quote || undefined }))
    dispatchMeta({ open: anchors, active: activeAnchor ?? null })
  }, [dispatchMeta, openKey, activeAnchor])
  const pendingFrom = draft?.from, pendingTo = draft?.to
  useEffect(() => { dispatchMeta({ pending: pendingFrom !== undefined && pendingTo !== undefined ? { from: pendingFrom, to: pendingTo } : null }) }, [dispatchMeta, pendingFrom, pendingTo])

  // An editor writes the anchor mark for threads a commenter started (they
  // could only anchor by quote), so the anchor then follows edits.
  useEffect(() => {
    if (!editor || editor.isDestroyed || !canEdit) return
    const type = editor.schema.marks[INLINE_COMMENT_MARK]
    if (!type) return
    const missing = [...unmarked].filter(id => !materialized.current.has(id) && anchors.has(id))
    if (!missing.length) return
    const tr = editor.state.tr
    for (const id of missing) {
      const range = anchors.get(id)!
      materialized.current.add(id)
      tr.addMark(range.from, range.to, type.create({ commentId: id }))
    }
    editor.view.dispatch(tr.setMeta('addToHistory', false))
  }, [anchors, canEdit, editor, unmarked])

  // Clicking highlighted text activates its thread; clicking elsewhere in the
  // document clears it.
  useEffect(() => {
    if (!editor || editor.isDestroyed) return
    const dom = editor.view.dom
    const onClick = (event: MouseEvent) => {
      const target = (event.target as HTMLElement).closest<HTMLElement>('[data-comment-anchor]')
      const anchorId = target?.dataset.commentAnchor
      const thread = anchorId ? open.find(item => item.anchorId === anchorId) : undefined
      setActive(thread?.root.id)
    }
    dom.addEventListener('click', onClick)
    return () => dom.removeEventListener('click', onClick)
  }, [editor, open])

  // Deep links (#comment-<id>) activate and reveal their thread.
  useEffect(() => {
    const id = window.location.hash.match(/^#comment-(.+)$/)?.[1]
    if (!id) return
    const thread = open.find(item => item.root.id === id || item.replies.some(reply => reply.id === id))
    if (!thread) return
    setActive(thread.root.id)
    window.setTimeout(() => window.document.getElementById(`comment-${id}`)?.scrollIntoView({ block: 'center' }), 60)
  }, [open])

  // Measure each card; layout is recomputed when a card resizes.
  useEffect(() => {
    observer.current = new ResizeObserver(entries => {
      setHeights(current => {
        let changed = false
        const next = new Map(current)
        for (const entry of entries) {
          const id = (entry.target as HTMLElement).dataset.threadId ?? (entry.target as HTMLElement).dataset.draftCard
          if (!id) continue
          const height = Math.round(entry.contentRect.height + 2)
          if (next.get(id) !== height) { next.set(id, height); changed = true }
        }
        return changed ? next : current
      })
    })
    return () => observer.current?.disconnect()
  }, [])
  // One stable ref callback per card, so re-renders do not re-observe.
  const cardRefs = useRef(new Map<string, (element: HTMLDivElement | null) => void>())
  const cardRef = useCallback((id: string) => {
    let callback = cardRefs.current.get(id)
    if (!callback) {
      callback = (element: HTMLDivElement | null) => {
        const previous = cards.current.get(id)
        if (previous && previous !== element) observer.current?.unobserve(previous)
        if (element) { cards.current.set(id, element); observer.current?.observe(element) }
        else cards.current.delete(id)
      }
      cardRefs.current.set(id, callback)
    }
    return callback
  }, [])

  useEffect(() => {
    const onResize = () => setLayoutTick(value => value + 1)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  const anchorTop = useCallback((range: AnchorRange | undefined) => {
    if (!editor || editor.isDestroyed || !shell || !range) return 0
    try {
      const position = Math.min(Math.max(range.from, 0), editor.state.doc.content.size)
      return editor.view.coordsAtPos(position).top - shell.getBoundingClientRect().top
    } catch { return 0 }
  }, [editor, shell])

  const visibleThreads = visible ? open : []
  useLayoutEffect(() => {
    const items = visibleThreads.map(thread => ({ id: threadKey(thread), top: thread.anchorId ? anchorTop(anchors.get(thread.anchorId)) : 0, height: heights.get(threadKey(thread)) ?? 96 }))
    if (draft) items.push({ id: 'draft', top: anchorTop(draft), height: heights.get('draft') ?? 96 })
    const next = layoutGutter(items, draft ? 'draft' : active)
    setTops(current => {
      if (current.size === next.size && [...next].every(([id, top]) => current.get(id) === top)) return current
      return next
    })
  }, [active, anchorTop, anchors, draft, heights, layoutTick, visibleThreads]) // eslint-disable-line react-hooks/exhaustive-deps

  const shown = visibleThreads.length > 0 || Boolean(draft)
  useEffect(() => { onGutterChange?.(shown) }, [onGutterChange, shown])

  // Escape closes the composer / deactivates the thread.
  useEffect(() => {
    if (!draft && !active) return
    const onKey = (event: KeyboardEvent) => { if (event.key === 'Escape') { onDraftChange(undefined); setActive(undefined) } }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [active, draft, onDraftChange])

  // The composer takes focus when it opens (its editor mounts a frame later).
  const focusComposer = useCallback((element: HTMLDivElement | null) => {
    if (!element) return
    const started = Date.now()
    const focus = () => {
      const target = element.querySelector<HTMLElement>('[contenteditable="true"]')
      if (target) target.focus()
      else if (Date.now() - started < 3000) window.setTimeout(focus, 30)
    }
    focus()
  }, [])

  const submitDraft = async (body: string, bodyData?: Record<string, unknown>) => {
    if (!draft) return
    const anchorId = newAnchorId()
    try {
      const created = await createDocumentComment(document.id, { body, bodyData, anchorId, quotedText: draft.text.slice(0, MAX_QUOTE) })
      const pending = editor && !editor.isDestroyed ? inlineCommentsPluginKey.getState(editor.state)?.pending : undefined
      const type = editor?.schema.marks[INLINE_COMMENT_MARK]
      if (editor && !editor.isDestroyed && canEdit && pending && type) {
        materialized.current.add(anchorId)
        editor.view.dispatch(editor.state.tr.addMark(pending.from, pending.to, type.create({ commentId: anchorId })).setMeta(inlineCommentsPluginKey, { pending: null }).setMeta('addToHistory', false))
      }
      onDraftChange(undefined)
      setActive(created.id)
      await onReload()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not add comment'))
      throw error
    }
  }

  if (!shown) return null
  return <TooltipProvider><div className="document-inline-gutter" aria-label={t('Comments')} role="complementary">
    {draft && <div className="document-thread-card is-draft is-active" data-draft-card="draft" ref={cardRef('draft')} style={{ top: tops.get('draft') ?? anchorTop(draft) }}>
      <div className="document-thread-draft">
        <UserAvatar avatarUrl={data.viewer.avatarUrl} className="document-thread-avatar" name={data.viewer.displayName || data.viewer.name}/>
        <div className="document-thread-draft-composer" ref={focusComposer}>
          <Composer compact users={data.users} placeholder={t('Add a comment…')} onCancel={() => onDraftChange(undefined)} onSubmit={submitDraft}/>
        </div>
      </div>
    </div>}
    {visibleThreads.map(thread => <InlineThreadCard
      key={threadKey(thread)}
      ref={cardRef(threadKey(thread))}
      thread={thread}
      viewer={data.viewer}
      users={data.users}
      active={active === thread.root.id}
      canComment={canComment}
      canModerate={canModerate}
      muted={threadMuted(data, document, thread)}
      actions={actions}
      onActivate={() => setActive(thread.root.id)}
      style={{ top: tops.get(threadKey(thread)) ?? 0 }}
    />)}
  </div></TooltipProvider>
}

/** Header button (shown only when resolved threads exist) toggling the "Resolved comments" panel. */
export function DocumentResolvedCommentsButton({ count, open, onToggle }: { count: number; open: boolean; onToggle: () => void }) {
  const { t } = useI18n()
  if (!count) return null
  const label = t('Resolved comments')
  return <TooltipProvider><FlowTooltip label={label}><button aria-expanded={open} aria-label={label} className={`document-icon-button${open ? ' is-active' : ''}`} data-active={open || undefined} onClick={onToggle} type="button"><MessageSquareText size={15}/></button></FlowTooltip></TooltipProvider>
}

/** "Resolved comments": the quote, the thread and its reactions; "Reopen thread" in each "…" menu. */
export function ResolvedCommentsPanel({ data, document, comments, editor, canComment, canEdit, onReload, onClose }: { data: BootstrapData; document: FlowDocument; comments: Comment[]; editor: Editor | null; canComment: boolean; canEdit: boolean; onReload: () => Promise<void>; onClose: () => void }) {
  const { t } = useI18n()
  const { resolved } = useInlineThreads(comments)
  const actions = useThreadActions({ data, document, editor, canEdit, onReload })
  const canModerate = data.viewerRole === 'admin' || String(data.viewerRole) === 'owner'
  useEffect(() => { if (!resolved.length) onClose() }, [onClose, resolved.length])
  // Selecting a resolved thread reveals the text it was written on.
  const reveal = (thread: InlineThread) => {
    if (!editor || editor.isDestroyed) return
    const range = findMarkRanges(editor.state.doc).get(thread.anchorId) ?? (thread.root.quotedText ? findQuoteRange(editor.state.doc, thread.root.quotedText) : undefined)
    if (range) editor.chain().setTextSelection(range).scrollIntoView().run()
  }
  return <TooltipProvider><aside className="document-resolved-panel" aria-label={t('Resolved comments')}>
    <header>
      <strong>{t('Resolved comments')}</strong>
      <FlowTooltip label={t('Close')}><button aria-label={t('Close')} className="document-thread-icon" onClick={onClose} type="button"><X size={14}/></button></FlowTooltip>
    </header>
    <div className="document-resolved-list">
      {[...resolved].reverse().map(thread => <InlineThreadCard
        key={thread.root.id}
        thread={thread}
        viewer={data.viewer}
        users={data.users}
        active={false}
        canComment={canComment}
        canModerate={canModerate}
        muted={threadMuted(data, document, thread)}
        actions={actions}
        onActivate={() => reveal(thread)}
        variant="resolved"
      />)}
    </div>
  </aside></TooltipProvider>
}
