/**
 * Pure model for document inline comment threads: threads from the flat
 * comment list, anchor ranges in the editor document, and gutter layout.
 */
import type { Node as ProseMirrorNode } from '@tiptap/pm/model'
import type { Comment } from '@/types/flow'

export const INLINE_COMMENT_MARK = 'inlineComment'

export interface InlineThread {
  root: Comment
  replies: Comment[]
  /** Mark id anchoring the thread; empty for page-level (legacy) threads. */
  anchorId: string
}

export interface AnchorRange { from: number; to: number }

/** Roots (oldest first) with their replies; replies to replies join their root. */
export function buildThreads(comments: Comment[]): InlineThread[] {
  const byId = new Map(comments.map(comment => [comment.id, comment]))
  const rootOf = (comment: Comment) => {
    let current = comment
    for (let depth = 0; current.parentId && depth < 16; depth++) {
      const parent = byId.get(current.parentId)
      if (!parent) break
      current = parent
    }
    return current
  }
  const threads = new Map<string, InlineThread>()
  const sorted = [...comments].sort((left, right) => Date.parse(left.createdAt) - Date.parse(right.createdAt))
  for (const comment of sorted) {
    if (comment.parentId && byId.has(comment.parentId)) continue
    threads.set(comment.id, { root: comment, replies: [], anchorId: comment.anchorId ?? '' })
  }
  for (const comment of sorted) {
    if (!comment.parentId || !byId.has(comment.parentId)) continue
    threads.get(rootOf(comment).id)?.replies.push(comment)
  }
  return [...threads.values()]
}

/** Ranges covered by each inline comment mark (by its commentId attribute). */
export function findMarkRanges(doc: ProseMirrorNode): Map<string, AnchorRange> {
  const ranges = new Map<string, AnchorRange>()
  doc.descendants((node, pos) => {
    if (!node.isText) return
    for (const mark of node.marks) {
      if (mark.type.name !== INLINE_COMMENT_MARK) continue
      const id = mark.attrs.commentId as string | null
      if (!id) continue
      const end = pos + node.nodeSize
      const current = ranges.get(id)
      ranges.set(id, current ? { from: Math.min(current.from, pos), to: Math.max(current.to, end) } : { from: pos, to: end })
    }
  })
  return ranges
}

/**
 * Finds quoted text in the document (block boundaries read as newlines), the
 * occurrence nearest `near` when it appears more than once. Anchors a thread
 * whose mark could not be written (a commenter cannot edit the document).
 */
export function findQuoteRange(doc: ProseMirrorNode, quote: string, near = 0): AnchorRange | undefined {
  const needle = quote.trim()
  if (!needle) return undefined
  let text = ''
  const positions: number[] = []
  doc.descendants((node, pos) => {
    if (node.isTextblock && text) { text += '\n'; positions.push(pos) }
    if (node.isText) for (let index = 0; index < (node.text?.length ?? 0); index++) { positions.push(pos + index); text += node.text![index] }
  })
  let best: AnchorRange | undefined
  for (let index = text.indexOf(needle); index >= 0; index = text.indexOf(needle, index + 1)) {
    const range = { from: positions[index], to: positions[index + needle.length - 1] + 1 }
    if (!best || Math.abs(range.from - near) < Math.abs(best.from - near)) best = range
  }
  return best
}

export interface GutterItem { id: string; top: number; height: number }

/**
 * Places cards at their anchors' tops, pushing later cards down so none
 * overlap. The active card keeps its anchor position; cards above it move up
 * when there is room (like the reference app).
 */
export function layoutGutter(items: GutterItem[], activeId?: string, gap = 8): Map<string, number> {
  const sorted = [...items].sort((left, right) => left.top - right.top || left.id.localeCompare(right.id))
  const result = new Map<string, number>()
  const activeIndex = activeId ? sorted.findIndex(item => item.id === activeId) : -1
  if (activeIndex < 0) {
    let cursor = -Infinity
    for (const item of sorted) {
      const top = Math.max(item.top, cursor)
      result.set(item.id, top)
      cursor = top + item.height + gap
    }
    return result
  }
  const active = sorted[activeIndex]
  result.set(active.id, active.top)
  let cursor = active.top + active.height + gap
  for (const item of sorted.slice(activeIndex + 1)) {
    const top = Math.max(item.top, cursor)
    result.set(item.id, top)
    cursor = top + item.height + gap
  }
  let ceiling = active.top - gap
  for (const item of sorted.slice(0, activeIndex).reverse()) {
    const top = Math.min(item.top, ceiling - item.height)
    result.set(item.id, top)
    ceiling = top - gap
  }
  return result
}

/** A comment's body as Markdown for "Copy content as Markdown". */
export function commentMarkdown(comment: Pick<Comment, 'body'>) {
  return comment.body.trim()
}
