import { useResetOnChange } from '@/hooks/use-reset-on-change'
/**
 * Inline comment threads beside rich text (a document, or a project or initiative description), shared by every page that
 * has them: select text → toolbar "Comment" → a card in the right gutter with "Add a comment…". A thread anchors to its
 * selection: in a collaborative document an editor writes an inline comment mark (its id is saved on the thread as
 * anchorId) so the anchor follows concurrent edits; elsewhere (and for commenters, who cannot edit) the thread is found
 * again by its quoted text. Open threads underline their text; the active one is highlighted. Resolving hides the
 * highlight and the card; the page's "Show resolved comments" button opens the resolved threads.
 *
 * Threads without an anchor (page-level comments written before inline comments) stay visible, pinned to the top of the
 * gutter. Pages whose page-level comments live elsewhere (a project's Activity) pass only their anchored threads.
 */
import type { Editor } from '@tiptap/react'
import { MessageSquareText, X } from 'lucide-react'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { toast } from '@/lib/toast'

import { Composer } from '@/components/editor/composer'
import { FlowTooltip, TooltipProvider } from '@/components/ui/tooltip'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import type { AgentThreadControls } from '@/components/agent/issue-agent-task-context'
import type { Comment, User } from '@/types/flow'
import { findMarkRanges, findQuoteRange, INLINE_COMMENT_MARK, layoutGutter, newAnchorId, type AnchorRange, type InlineThread } from './inline-comments-model'
import { createInlineCommentsPlugin, inlineCommentsPluginKey, type InlineCommentsMeta, type InlineCommentsPluginState } from './inline-comments-plugin'
import { InlineThreadCard, type ThreadActions } from './inline-thread-card'
import { useInlineThreads } from './use-inline-threads'
import './document-inline-comments.css'

export interface CommentDraft { from: number; to: number; text: string }

/** What a new thread is saved with: the comment, a fresh anchor id and the selected text. */
export interface NewInlineThread { body: string; bodyData?: Record<string, unknown>; anchorId: string; quotedText: string }

export type RenderAgentThread = (threadIds: string[], controls: AgentThreadControls) => ReactNode

const MAX_QUOTE = 4000

/** Viewports narrower than this (CSS px; the reference app flips between 1230 and 1232) show the open thread as a popover, not a gutter card. */
export const GUTTER_MIN_VIEWPORT = 1232
/** The popover is 360px wide, starts 12.5px left of the text it is about and sits just under it. */
const POPOVER_WIDTH = 360
const POPOVER_INSET = 12.5
const POPOVER_GAP = 8

/** The open thread a `#comment-<id>` deep link points at (the link may name the thread's root or one of its replies). */
function deepLinkThread(open: InlineThread[]) {
  const id = window.location.hash.match(/^#comment-(.+)$/)?.[1]
  if (!id) return undefined
  const thread = open.find(item => item.root.id === id || item.replies.some(reply => reply.id === id))
  return thread ? { id, thread } : undefined
}

/** One stable ref callback per card (so re-renders do not re-observe) and the resize observer measuring the cards. */
class CardRegistry {
  private observer: ResizeObserver | null = null
  private cards = new Map<string, HTMLDivElement>()
  private callbacks = new Map<string, (element: HTMLDivElement | null) => void>()
  /** Starts measuring cards attached from now on; returns the stop function. */
  start(onResize: ResizeObserverCallback) {
    const observer = new ResizeObserver(onResize)
    this.observer = observer
    return () => { observer.disconnect(); if (this.observer === observer) this.observer = null }
  }
  attach(id: string) {
    let callback = this.callbacks.get(id)
    if (!callback) {
      callback = (element: HTMLDivElement | null) => {
        const previous = this.cards.get(id)
        if (previous && previous !== element) this.observer?.unobserve(previous)
        if (element) { this.cards.set(id, element); this.observer?.observe(element) }
        else this.cards.delete(id)
      }
      this.callbacks.set(id, callback)
    }
    return callback
  }
}

function sameRanges(left: Map<string, AnchorRange>, right: Map<string, AnchorRange>) {
  if (left.size !== right.size) return false
  for (const [id, range] of right) {
    const other = left.get(id)
    if (!other || other.from !== range.from || other.to !== range.to) return false
  }
  return true
}

export interface InlineCommentGutterProps {
  comments: Comment[]
  viewer: User
  users: User[]
  editor: Editor | null
  /** The positioned element wrapping the editor; card tops are measured against it. */
  shell: HTMLElement | null
  draft?: CommentDraft
  onDraftChange: (draft?: CommentDraft) => void
  canComment: boolean
  /** Writes the anchor mark into the document (collaborative documents, for editors); otherwise threads anchor by their quote. */
  writesMarks: boolean
  /** Workspace admins may edit and delete any comment. */
  canModerate: boolean
  /** "Show comments" view option: hides the gutter cards (not the composer). */
  visible?: boolean
  /** Reports whether gutter cards are shown, so the page can shift its column left. */
  onGutterChange?: (shown: boolean) => void
  /** A side panel takes the room beside the text, so threads open as popovers. */
  railOpen?: boolean
  actions: ThreadActions
  muted: (thread: InlineThread) => boolean
  renderAgentThread?: RenderAgentThread
  createThread: (input: NewInlineThread) => Promise<Comment>
  onReload: () => Promise<void>
}

export function InlineCommentGutter({ comments, viewer, users, editor, shell, draft, onDraftChange, canComment, writesMarks, canModerate, visible = true, onGutterChange, railOpen = false, actions, muted, renderAgentThread, createThread, onReload }: InlineCommentGutterProps) {
  const { t } = useI18n()
  const { open } = useInlineThreads(comments)
  const [active, setActive] = useState<string>()
  const [anchors, setAnchors] = useState<Map<string, AnchorRange>>(new Map())
  const [unmarked, setUnmarked] = useState<Set<string>>(new Set())
  const [tops, setTops] = useState<Map<string, number>>(new Map())
  const [heights, setHeights] = useState<Map<string, number>>(new Map())
  const [layoutTick, setLayoutTick] = useState(0)
  // Unknown until the page has been measured: threads stay closed (popover rules) rather than flashing as gutter cards.
  const [narrow, setNarrow] = useState<boolean | undefined>(undefined)
  const popover = narrow !== false || railOpen
  const materialized = useRef(new Set<string>())
  const [cardRegistry] = useState(() => new CardRegistry())
  // Resolving the thread being read also closes it.
  const threadActions = useMemo<ThreadActions>(() => ({
    ...actions,
    resolve: async (thread, resolved) => {
      await actions.resolve(thread, resolved)
      if (resolved) setActive(current => current === thread.root.id ? undefined : current)
    },
  }), [actions])
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
    if (!editor || editor.isDestroyed || !writesMarks) return
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
  }, [anchors, writesMarks, editor, unmarked])

  // Clicking highlighted text activates its thread; clicking elsewhere in the
  // text clears it.
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
  useResetOnChange([open], () => {
    const link = deepLinkThread(open)
    if (link) setActive(link.thread.root.id)
  }, { runOnMount: true })
  useEffect(() => {
    const link = deepLinkThread(open)
    if (!link) return
    window.setTimeout(() => window.document.getElementById(`comment-${link.id}`)?.scrollIntoView({ block: 'center' }), 60)
  }, [open])

  // Measure each card; layout is recomputed when a card resizes.
  useEffect(() => {
    if (typeof ResizeObserver === 'undefined') return
    return cardRegistry.start(entries => {
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
  }, [cardRegistry])

  // Gutter cards need a wide viewport (a media query, like the reference app: the page's own width plays no part).
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return
    const query = window.matchMedia(`(min-width: ${GUTTER_MIN_VIEWPORT}px)`)
    const update = () => setNarrow(!query.matches)
    update()
    query.addEventListener('change', update)
    return () => query.removeEventListener('change', update)
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
      // The card's top edge lines up with the top of the text's 24px line box (the glyph box starts 3px lower).
      return editor.view.coordsAtPos(position).top - shell.getBoundingClientRect().top - 3
    } catch { return 0 }
  }, [editor, shell])

  /** Popover placement: under the text of the range, 12.5px left of it, kept inside the page. */
  const popoverStyle = useCallback((range: AnchorRange | undefined): React.CSSProperties => {
    if (!editor || editor.isDestroyed || !shell || !range) return { top: 0, left: 0 }
    try {
      const position = Math.min(Math.max(range.from, 0), editor.state.doc.content.size)
      const coords = editor.view.coordsAtPos(position)
      const box = shell.getBoundingClientRect()
      const left = Math.max(0, Math.min(coords.left - box.left - POPOVER_INSET, box.width - POPOVER_WIDTH))
      return { top: coords.bottom - box.top + POPOVER_GAP, left }
    } catch { return { top: 0, left: 0 } }
  }, [editor, shell])

  // A popover shows only the thread being read (or the composer being written); the others wait to be clicked.
  const visibleThreads = visible ? (popover ? open.filter(thread => thread.root.id === active) : open) : []
  // Measures editor/DOM geometry (coordsAtPos) and stores the result before paint: a layout effect that syncs React with the DOM.
  useLayoutEffect(() => {
    const items = visibleThreads.map(thread => ({ id: threadKey(thread), top: thread.anchorId ? anchorTop(anchors.get(thread.anchorId)) : 0, height: heights.get(threadKey(thread)) ?? 96 }))
    if (draft) items.push({ id: 'draft', top: anchorTop(draft), height: heights.get('draft') ?? 96 })
    const next = layoutGutter(items, draft ? 'draft' : active)
    // oxlint-disable-next-line react/set-state-in-effect -- stores measured DOM geometry before paint; this is the layout-effect measurement pattern, not derived state
    setTops(current => {
      if (current.size === next.size && [...next].every(([id, top]) => current.get(id) === top)) return current
      return next
    })
  }, [active, anchorTop, anchors, draft, heights, layoutTick, visibleThreads]) // eslint-disable-line react-hooks/exhaustive-deps

  const shown = visibleThreads.length > 0 || Boolean(draft)
  const shifts = shown && !popover
  useEffect(() => { onGutterChange?.(shifts) }, [onGutterChange, shifts])

  // In popover mode, clicking anywhere outside the thread, its anchor text and its menus closes it.
  useEffect(() => {
    if (!popover || !active) return
    const onDown = (event: MouseEvent) => {
      const target = event.target as HTMLElement | null
      // The composers' selection toolbar floats outside the card (in the page body).
      if (target?.closest('.document-inline-gutter, [data-comment-anchor], [role="menu"], [role="dialog"], [data-radix-popper-content-wrapper], .selection-toolbar')) return
      setActive(undefined)
    }
    window.document.addEventListener('mousedown', onDown)
    return () => window.document.removeEventListener('mousedown', onDown)
  }, [active, popover])

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
      // Stop once the composer has closed: nothing is left to focus.
      else if (element.isConnected && Date.now() - started < 3000) window.setTimeout(focus, 30)
    }
    focus()
  }, [])

  const submitDraft = async (body: string, bodyData?: Record<string, unknown>) => {
    if (!draft) return
    const anchorId = newAnchorId()
    try {
      const created = await createThread({ body, bodyData, anchorId, quotedText: draft.text.slice(0, MAX_QUOTE) })
      const pending = editor && !editor.isDestroyed ? inlineCommentsPluginKey.getState(editor.state)?.pending : undefined
      const type = editor?.schema.marks[INLINE_COMMENT_MARK]
      if (editor && !editor.isDestroyed && writesMarks && pending && type) {
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
  return <TooltipProvider><div className={`document-inline-gutter${popover ? ' is-popover' : ''}`} aria-label={t('Comments')} role="complementary">
    {draft && <div className="document-thread-card is-draft is-active" data-draft-card="draft" ref={cardRegistry.attach('draft')} style={popover ? popoverStyle(draft) : { top: tops.get('draft') ?? anchorTop(draft) }}>
      <div className="document-thread-draft">
        <UserAvatar avatarUrl={viewer.avatarUrl} className="document-thread-avatar" name={viewer.displayName || viewer.name}/>
        <div className="document-thread-draft-composer" ref={focusComposer}>
          <Composer compact users={users} placeholder={t('Add a comment…')} onCancel={() => onDraftChange(undefined)} onSubmit={submitDraft}/>
        </div>
      </div>
    </div>}
    {visibleThreads.map(thread => <InlineThreadCard
      key={threadKey(thread)}
      ref={cardRegistry.attach(threadKey(thread))}
      thread={thread}
      viewer={viewer}
      users={users}
      active={active === thread.root.id}
      canComment={canComment}
      canModerate={canModerate}
      muted={muted(thread)}
      actions={threadActions}
      onActivate={() => setActive(thread.root.id)}
      renderAgentThread={renderAgentThread}
      style={popover ? popoverStyle(thread.anchorId ? anchors.get(thread.anchorId) : undefined) : { top: tops.get(threadKey(thread)) ?? 0 }}
    />)}
  </div></TooltipProvider>
}

/** Header button (shown only when resolved threads exist) toggling the resolved comments panel. */
export function ResolvedCommentsButton({ count, open, onToggle, className = 'document-icon-button', label: name = 'Resolved comments' }: { count: number; open: boolean; onToggle: () => void; className?: string; label?: string }) {
  const { t } = useI18n()
  if (!count) return null
  const label = t(name)
  return <TooltipProvider><FlowTooltip label={label}><button aria-expanded={open} aria-label={label} className={`${className}${open ? ' is-active' : ''}`} data-active={open || undefined} onClick={onToggle} type="button"><MessageSquareText size={15}/></button></FlowTooltip></TooltipProvider>
}

export interface ResolvedThreadsPanelProps {
  comments: Comment[]
  viewer: User
  users: User[]
  editor: Editor | null
  canComment: boolean
  canModerate: boolean
  actions: ThreadActions
  muted: (thread: InlineThread) => boolean
  renderAgentThread?: RenderAgentThread
  /** The page the threads are on, named above each quote (like the reference app). */
  source?: string
  onClose: () => void
}

/** "Resolved comments": the quote, the thread and its reactions; "Reopen thread" in each "…" menu. */
export function ResolvedThreadsPanel({ comments, viewer, users, editor, canComment, canModerate, actions, muted, renderAgentThread, source, onClose }: ResolvedThreadsPanelProps) {
  const { t } = useI18n()
  const { resolved } = useInlineThreads(comments)
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
        viewer={viewer}
        users={users}
        active={false}
        canComment={canComment}
        canModerate={canModerate}
        muted={muted(thread)}
        actions={actions}
        onActivate={() => reveal(thread)}
        renderAgentThread={renderAgentThread}
        source={source}
        variant="resolved"
      />)}
    </div>
  </aside></TooltipProvider>
}
