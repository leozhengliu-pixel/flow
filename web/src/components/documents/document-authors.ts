/**
 * "Show author names": which user wrote each top-level block of a document.
 *
 * Authorship is not stored per block (the collaboration log carries raw Yjs
 * updates without an author), so it is derived from the saved revisions: the
 * blocks of consecutive revisions are matched with a longest-common-subsequence
 * on their JSON, and a block that is new or changed in a revision is attributed
 * to that revision's author. Blocks that never changed since the first revision
 * keep that revision's author; blocks that differ from the latest revision are
 * attributed to the last editor. The work is capped (revisions and blocks) so
 * it stays cheap on large documents.
 */
import type { DocumentRevision, FlowDocument, User } from '@/types/flow'

export const AUTHOR_REVISION_CAP = 40
export const AUTHOR_BLOCK_CAP = 600

type Block = Record<string, unknown>

export function topLevelBlocks(contentData: Record<string, unknown> | undefined): string[] {
  const content = contentData?.content
  if (!Array.isArray(content)) return []
  return (content as Block[]).slice(0, AUTHOR_BLOCK_CAP).map(block => JSON.stringify(block))
}

/** Maps each position of `next` that also occurs (in order) in `previous` to its position there. */
function matchedPositions(previous: string[], next: string[]): Map<number, number> {
  const n = previous.length
  const m = next.length
  // Trim the common prefix and suffix first: most edits touch one place.
  let start = 0
  while (start < n && start < m && previous[start] === next[start]) start += 1
  let endPrev = n
  let endNext = m
  while (endPrev > start && endNext > start && previous[endPrev - 1] === next[endNext - 1]) { endPrev -= 1; endNext -= 1 }
  const matches = new Map<number, number>()
  for (let index = 0; index < start; index += 1) matches.set(index, index)
  const rows = endPrev - start
  const cols = endNext - start
  if (rows > 0 && cols > 0) {
    const table = Array.from({ length: rows + 1 }, () => new Uint16Array(cols + 1))
    for (let i = rows - 1; i >= 0; i -= 1) {
      for (let j = cols - 1; j >= 0; j -= 1) {
        table[i][j] = previous[start + i] === next[start + j] ? table[i + 1][j + 1] + 1 : Math.max(table[i + 1][j], table[i][j + 1])
      }
    }
    let i = 0
    let j = 0
    while (i < rows && j < cols) {
      if (previous[start + i] === next[start + j]) { matches.set(start + j, start + i); i += 1; j += 1 }
      else if (table[i + 1][j] >= table[i][j + 1]) i += 1
      else j += 1
    }
  }
  for (let offset = 0; endNext + offset < m; offset += 1) matches.set(endNext + offset, endPrev + offset)
  return matches
}

/**
 * The author of every top-level block of the current document, in order.
 * `lastEditor` writes whatever differs from the newest saved revision.
 */
export function deriveBlockAuthors(
  document: Pick<FlowDocument, 'creator' | 'revisions'>,
  current: Record<string, unknown> | undefined,
  lastEditor: User,
): User[] {
  const currentBlocks = topLevelBlocks(current)
  if (!currentBlocks.length) return []
  const revisions = [...(document.revisions ?? [])]
    .filter((revision): revision is DocumentRevision => Boolean(revision.contentData))
    .sort((left, right) => Date.parse(left.createdAt) - Date.parse(right.createdAt))
    .slice(-AUTHOR_REVISION_CAP)
  const states: Array<{ blocks: string[]; author: User }> = revisions.map(revision => ({ blocks: topLevelBlocks(revision.contentData), author: revision.author }))
  states.push({ blocks: currentBlocks, author: lastEditor })

  // The first kept state is the baseline: its blocks belong to the creator.
  let authors: User[] = states[0].blocks.map(() => document.creator)
  let previous = states[0].blocks
  for (let index = 1; index < states.length; index += 1) {
    const state = states[index]
    const matched = matchedPositions(previous, state.blocks)
    // Matched blocks keep their author; new or changed ones belong to this state's author.
    authors = state.blocks.map((_, position) => {
      const source = matched.get(position)
      return source === undefined ? state.author : authors[source] ?? state.author
    })
    previous = state.blocks
  }
  return authors
}

/** Positions where a new author run starts (the first block always starts one). */
export function authorRuns(authors: User[]): Array<{ index: number; author: User }> {
  const runs: Array<{ index: number; author: User }> = []
  authors.forEach((author, index) => {
    if (index === 0 || authors[index - 1].id !== author.id) runs.push({ index, author })
  })
  return runs
}
