/**
 * Version comparison for the document history dialog: block-level + word-level diff of two ProseMirror JSON documents.
 *
 * `diffDocuments(previous, next)` returns `next` rendered as one document that also carries the previous version's
 * deletions in the same flow:
 *  - an added block gets `attrs.__diff = 'added'` (and its text the `diffAdded` mark), a removed block is kept in place
 *    with `attrs.__diff = 'removed'` (and its text the `diffRemoved` mark);
 *  - a block that exists in both versions but changed is diffed word by word: new words carry `diffAdded`, deleted words
 *    stay in the flow with `diffRemoved`;
 *  - every change (a whole block or a contiguous run of changed words in one block) has a sequential `change` id (1-based,
 *    document order) on the block attr `__change` / the mark attr `change`; `changes` is how many there are.
 * The renderer only has to know those two marks and two attrs.
 */

export interface PMMark { type: string; attrs?: Record<string, unknown> }
export interface PMNode {
  type?: string
  text?: string
  attrs?: Record<string, unknown>
  marks?: PMMark[]
  content?: PMNode[]
}

export const DIFF_ADDED_MARK = 'diffAdded'
export const DIFF_REMOVED_MARK = 'diffRemoved'
export type DiffKind = 'added' | 'removed'
export interface HistoryDiffResult { doc: PMNode; changes: number }

const TEXTBLOCKS = new Set(['paragraph', 'heading', 'codeBlock', 'detailsSummary'])
/** Above this many token pairs the word diff gives up and replaces the whole block (keeps the dialog snappy). */
const MAX_LCS_CELLS = 4_000_000

export function isValidDocument(value: unknown): value is PMNode {
  const doc = value as PMNode | null | undefined
  return Boolean(doc) && typeof doc === 'object' && doc?.type === 'doc' && Array.isArray(doc.content)
}

/** Concatenated text of a node (block boundaries become newlines). */
export function nodeText(node: PMNode | undefined): string {
  if (!node) return ''
  if (typeof node.text === 'string') return node.text
  if (node.type === 'hardBreak') return '\n'
  if (node.type === 'mention') return String(node.attrs?.label ?? '')
  const parts = (node.content ?? []).map(nodeText)
  return isTextblockLike(node) || !node.content ? parts.join('') : parts.join('\n')
}

function isInline(node: PMNode) {
  return node.type === 'text' || node.type === 'hardBreak' || node.type === 'mention' || node.type === 'image' || typeof node.text === 'string'
}

function isTextblockLike(node: PMNode) {
  if (node.type && TEXTBLOCKS.has(node.type)) return true
  return Array.isArray(node.content) && node.content.length > 0 && node.content.every(isInline)
}

function isContainer(node: PMNode) {
  return Array.isArray(node.content) && node.content.length > 0 && !isTextblockLike(node)
}

function blockText(node: PMNode) {
  return nodeText(node).trim()
}

const signature = (node: PMNode) => JSON.stringify(node)

/** Splits text into words and whitespace runs so unchanged spacing never counts as a change. */
export function wordTokens(text: string): string[] {
  return text.match(/\s+|[^\s]+/gu) ?? []
}

export interface WordOp { kind: 'same' | 'added' | 'removed'; tokens: string[] }

/** Longest-common-subsequence word diff of two token lists, grouped into runs (added comes before removed). */
export function diffWords(before: string[], after: string[]): WordOp[] {
  const ops = diffSequences(before, after)
  const runs: WordOp[] = []
  const push = (kind: WordOp['kind'], token: string) => {
    const last = runs[runs.length - 1]
    if (last && last.kind === kind) last.tokens.push(token)
    else runs.push({ kind, tokens: [token] })
  }
  // Within a replaced stretch the additions come first, then the struck deletions (like the reference app).
  let removed: string[] = []
  let added: string[] = []
  const flush = () => {
    for (const token of added) push('added', token)
    for (const token of removed) push('removed', token)
    removed = []
    added = []
  }
  for (const op of ops) {
    if (op.kind === 'same') { flush(); push('same', op.value) }
    else if (op.kind === 'removed') removed.push(op.value)
    else added.push(op.value)
  }
  flush()
  return runs
}

interface SeqOp<T> { kind: 'same' | 'added' | 'removed'; value: T; index: number }

/** LCS over comparable values; `index` is the position in `after` for same/added and in `before` for removed. */
function diffSequences<T extends string>(before: T[], after: T[]): SeqOp<T>[] {
  let start = 0
  while (start < before.length && start < after.length && before[start] === after[start]) start++
  let endBefore = before.length
  let endAfter = after.length
  while (endBefore > start && endAfter > start && before[endBefore - 1] === after[endAfter - 1]) { endBefore--; endAfter-- }
  const ops: SeqOp<T>[] = []
  for (let i = 0; i < start; i++) ops.push({ kind: 'same', value: after[i], index: i })
  const a = before.slice(start, endBefore)
  const b = after.slice(start, endAfter)
  if (!a.length || !b.length || a.length * b.length > MAX_LCS_CELLS) {
    a.forEach((value, i) => ops.push({ kind: 'removed', value, index: start + i }))
    b.forEach((value, i) => ops.push({ kind: 'added', value, index: start + i }))
  } else {
    const width = b.length + 1
    const table = new Uint32Array((a.length + 1) * width)
    for (let i = a.length - 1; i >= 0; i--) {
      for (let j = b.length - 1; j >= 0; j--) {
        table[i * width + j] = a[i] === b[j] ? table[(i + 1) * width + j + 1] + 1 : Math.max(table[(i + 1) * width + j], table[i * width + j + 1])
      }
    }
    let i = 0
    let j = 0
    while (i < a.length || j < b.length) {
      if (i < a.length && j < b.length && a[i] === b[j]) { ops.push({ kind: 'same', value: b[j], index: start + j }); i++; j++ }
      else if (i < a.length && (j >= b.length || table[(i + 1) * width + j] >= table[i * width + j + 1])) { ops.push({ kind: 'removed', value: a[i], index: start + i }); i++ }
      else { ops.push({ kind: 'added', value: b[j], index: start + j }); j++ }
    }
  }
  for (let i = endAfter; i < after.length; i++) ops.push({ kind: 'same', value: after[i], index: i })
  return ops
}

interface Context { changes: number }

function withMark(node: PMNode, kind: DiffKind, change: number): PMNode {
  const mark: PMMark = { type: kind === 'added' ? DIFF_ADDED_MARK : DIFF_REMOVED_MARK, attrs: { change } }
  return { ...node, marks: [...(node.marks ?? []), mark] }
}

/** Marks a whole block (and all the text inside it) as added or removed. */
function markBlock(node: PMNode, kind: DiffKind, change: number): PMNode {
  const marked = (child: PMNode): PMNode => {
    if (typeof child.text === 'string' || child.type === 'mention' || child.type === 'image') return withMark(child, kind, change)
    return child.content ? { ...child, content: child.content.map(marked) } : child
  }
  const inner = node.content ? { ...node, content: node.content.map(marked) } : node
  return { ...inner, attrs: { ...(inner.attrs ?? {}), __diff: kind, __change: change } }
}

function emitWhole(node: PMNode, kind: DiffKind, ctx: Context): PMNode {
  // An empty paragraph is spacing, not content: it never counts as a change.
  if (node.type === 'paragraph' && !blockText(node) && !(node.content ?? []).some(child => child.type !== 'text')) return node
  return markBlock(node, kind, ++ctx.changes)
}

interface InlineToken { text: string; node: PMNode }

function inlineTokens(block: PMNode): InlineToken[] {
  const tokens: InlineToken[] = []
  for (const child of block.content ?? []) {
    if (typeof child.text === 'string') {
      for (const piece of wordTokens(child.text)) tokens.push({ text: piece, node: { ...child, text: piece } })
    } else {
      tokens.push({ text: `\u0000${signature(child)}`, node: child })
    }
  }
  return tokens
}

function mergeText(nodes: PMNode[]): PMNode[] {
  const out: PMNode[] = []
  for (const node of nodes) {
    const last = out[out.length - 1]
    if (last && typeof last.text === 'string' && typeof node.text === 'string' && JSON.stringify(last.marks ?? []) === JSON.stringify(node.marks ?? [])) {
      out[out.length - 1] = { ...last, text: last.text + node.text }
    } else out.push(node)
  }
  return out
}

function diffTextblock(previous: PMNode, next: PMNode, ctx: Context): PMNode {
  const before = inlineTokens(previous)
  const after = inlineTokens(next)
  const ops = diffSequences(before.map(token => token.text), after.map(token => token.text))
  const out: PMNode[] = []
  let current = 0
  let open = false
  const whitespace = (text: string) => /^\s+$/.test(text)
  const beforeText = new Map<number, InlineToken>(before.map((token, index) => [index, token]))
  // Within a replaced stretch the additions come first, then the struck deletions.
  const pending: SeqOp<string>[] = []
  const emitChange = (op: SeqOp<string>) => {
    if (!open) { open = true; current = ++ctx.changes }
    if (op.kind === 'removed') out.push(withMark(beforeText.get(op.index)!.node, 'removed', current))
    else out.push(withMark(after[op.index].node, 'added', current))
  }
  const flush = () => {
    for (const op of pending.filter(item => item.kind === 'added')) emitChange(op)
    for (const op of pending.filter(item => item.kind === 'removed')) emitChange(op)
    pending.length = 0
  }
  for (let index = 0; index < ops.length; index++) {
    const op = ops[index]
    if (op.kind !== 'same') { pending.push(op); continue }
    flush()
    // Spacing between two changed words belongs to the same change.
    const spacing = whitespace(op.value)
    let ahead = index + 1
    while (ahead < ops.length && ops[ahead].kind === 'same' && whitespace(ops[ahead].value)) ahead++
    if (!(open && spacing && ahead < ops.length && ops[ahead].kind !== 'same')) open = false
    out.push(after[op.index].node)
  }
  flush()
  return { ...next, content: mergeText(out) }
}

/** Is `candidate` similar enough to `reference` to be shown as an edit of it instead of a removal plus an addition? */
function similar(previous: PMNode, next: PMNode): boolean {
  if (previous.type !== next.type) return false
  if (isContainer(previous) && isContainer(next)) return true
  if (!isTextblockLike(previous) || !isTextblockLike(next)) return false
  const a = wordTokens(blockText(previous)).filter(token => !/^\s+$/.test(token))
  const b = new Set(wordTokens(blockText(next)).filter(token => !/^\s+$/.test(token)))
  if (!a.length || !b.size) return !a.length && !b.size
  const shared = a.filter(token => b.has(token)).length
  return shared / Math.max(a.length, b.size) >= 0.3
}

function diffBlock(previous: PMNode, next: PMNode, ctx: Context): PMNode {
  if (isContainer(previous) && isContainer(next)) return { ...next, content: diffBlocks(previous.content ?? [], next.content ?? [], ctx) }
  return diffTextblock(previous, next, ctx)
}

function diffBlocks(before: PMNode[], after: PMNode[], ctx: Context): PMNode[] {
  const out: PMNode[] = []
  const ops = diffSequences(before.map(signature), after.map(signature))
  let removed: PMNode[] = []
  let added: PMNode[] = []
  const flushGap = () => {
    // Pair edited blocks in order, then emit the unpaired removals before the additions around them.
    const pairs: Array<[number, number]> = []
    let cursor = 0
    added.forEach((node, j) => {
      for (let i = cursor; i < removed.length; i++) {
        if (similar(removed[i], node)) { pairs.push([i, j]); cursor = i + 1; break }
      }
    })
    let oi = 0
    let nj = 0
    for (const [i, j] of pairs) {
      for (; oi < i; oi++) out.push(emitWhole(removed[oi], 'removed', ctx))
      for (; nj < j; nj++) out.push(emitWhole(added[nj], 'added', ctx))
      out.push(diffBlock(removed[i], added[j], ctx))
      oi = i + 1
      nj = j + 1
    }
    for (; oi < removed.length; oi++) out.push(emitWhole(removed[oi], 'removed', ctx))
    for (; nj < added.length; nj++) out.push(emitWhole(added[nj], 'added', ctx))
    removed = []
    added = []
  }
  for (const op of ops) {
    if (op.kind === 'same') { flushGap(); out.push(after[op.index]) }
    else if (op.kind === 'removed') removed.push(before[op.index])
    else added.push(after[op.index])
  }
  flushGap()
  return out
}

/**
 * `next` with the differences to `previous` embedded (see the file comment). Without a `previous` version there is
 * nothing to compare against: the document comes back untouched with zero changes.
 */
export function diffDocuments(previous: PMNode | null | undefined, next: PMNode): HistoryDiffResult {
  if (!previous || !isValidDocument(previous) || !isValidDocument(next)) return { doc: next, changes: 0 }
  const ctx: Context = { changes: 0 }
  const content = diffBlocks(previous.content ?? [], next.content ?? [], ctx)
  return { doc: { ...next, content }, changes: ctx.changes }
}
