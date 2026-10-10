/**
 * ProseMirror plugin that draws document inline comment anchors:
 * - open threads: orange underline (`inline-comment-open`)
 * - the active thread: yellow highlight (`is-active`)
 * - the selection a new comment is being written on (`inline-comment-pending`)
 * Anchors come from inline comment marks in the document; a thread whose
 * mark is missing (written by a commenter, who cannot edit) is anchored to
 * its quoted text. Resolved threads draw nothing, so resolving never edits
 * the document. The plugin also exposes the resolved ranges for the gutter.
 */
import { Plugin, PluginKey, type EditorState, type Transaction } from '@tiptap/pm/state'
import { Decoration, DecorationSet } from '@tiptap/pm/view'
import { findMarkRanges, findQuoteRange, type AnchorRange } from './inline-comments-model'

export interface OpenAnchor { id: string; quote?: string }

export interface InlineCommentsPluginState {
  open: OpenAnchor[]
  active?: string
  pending?: AnchorRange
  /** Resolved anchor ranges of open threads (by anchor id). */
  ranges: Map<string, AnchorRange>
  /** Open anchors that are drawn from their quote because the mark is missing. */
  unmarked: Set<string>
  decorations: DecorationSet
}

export interface InlineCommentsMeta {
  open?: OpenAnchor[]
  active?: string | null
  pending?: AnchorRange | null
}

export const inlineCommentsPluginKey = new PluginKey<InlineCommentsPluginState>('flowDocumentInlineComments')

function build(state: Pick<EditorState, 'doc'>, open: OpenAnchor[], active: string | undefined, pending: AnchorRange | undefined, previous?: Map<string, AnchorRange>): InlineCommentsPluginState {
  const doc = state.doc
  const marks = open.length ? findMarkRanges(doc) : new Map<string, AnchorRange>()
  const ranges = new Map<string, AnchorRange>()
  const unmarked = new Set<string>()
  const decorations: Decoration[] = []
  for (const anchor of open) {
    let range = marks.get(anchor.id)
    if (!range && anchor.quote) {
      range = findQuoteRange(doc, anchor.quote, previous?.get(anchor.id)?.from ?? 0)
      if (range) unmarked.add(anchor.id)
    }
    if (!range || range.from >= range.to || range.to > doc.content.size) continue
    ranges.set(anchor.id, range)
    decorations.push(Decoration.inline(range.from, range.to, { class: `inline-comment-open${anchor.id === active ? ' is-active' : ''}`, 'data-comment-anchor': anchor.id }))
  }
  if (pending && pending.from < pending.to && pending.to <= doc.content.size) {
    decorations.push(Decoration.inline(pending.from, pending.to, { class: 'inline-comment-pending' }))
  }
  return { open, active, pending, ranges, unmarked, decorations: DecorationSet.create(doc, decorations) }
}

export function createInlineCommentsPlugin() {
  return new Plugin<InlineCommentsPluginState>({
    key: inlineCommentsPluginKey,
    state: {
      init: (_config, state) => build(state, [], undefined, undefined),
      apply(tr: Transaction, value, _old, next) {
        const meta = tr.getMeta(inlineCommentsPluginKey) as InlineCommentsMeta | undefined
        if (!meta && !tr.docChanged) return value
        const open = meta?.open ?? value.open
        const active = meta && 'active' in meta ? meta.active ?? undefined : value.active
        let pending = meta && 'pending' in meta ? meta.pending ?? undefined : value.pending
        if (pending && tr.docChanged && !(meta && 'pending' in meta)) {
          const from = tr.mapping.map(pending.from, 1), to = tr.mapping.map(pending.to, -1)
          pending = from < to ? { from, to } : undefined
        }
        return build(next, open, active, pending, value.ranges)
      },
    },
    props: {
      decorations: state => inlineCommentsPluginKey.getState(state)?.decorations,
    },
  })
}
