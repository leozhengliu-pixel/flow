import type { Issue } from '@/types/flow'

const STOP_WORDS = new Set(['the', 'a', 'an', 'and', 'or', 'to', 'of', 'in', 'on', 'for', 'with', 'is', 'be', 'when', 'from', 'at', 'by', 'it', 'not', 'can', 'should'])

function tokens(value: string) {
  return new Set(value.toLocaleLowerCase().split(/[^\p{L}\p{N}]+/u).filter(token => token.length > 1 && !STOP_WORDS.has(token)))
}

/** Title-token Jaccard similarity; CJK titles fall back to character bigrams. */
export function titleSimilarity(left: string, right: string) {
  const a = tokens(left), b = tokens(right)
  const grams = (value: string) => { const text = value.replace(/\s+/g, ''); const set = new Set<string>(); for (let i = 0; i < text.length - 1; i++) set.add(text.slice(i, i + 2)); return set }
  const [x, y] = a.size + b.size > 2 ? [a, b] : [grams(left), grams(right)]
  if (!x.size || !y.size) return 0
  let shared = 0
  for (const token of x) if (y.has(token)) shared++
  return shared / (x.size + y.size - shared)
}

/** Occurrences of one recurring issue share a title by design; they are not duplicates. */
export function sameRecurringSeries(issue: Issue, candidate: Issue) {
  const series = (item: Issue) => item.recurrenceSeriesId ?? (item.recurrence ? item.id : undefined)
  const left = series(issue), right = series(candidate)
  return Boolean((left && (left === right || left === candidate.id)) || (right && right === issue.id))
}

export function similarIssues(issue: Issue, issues: Issue[], limit = 3, threshold = 0.4) {
  const linked = new Set([issue.id, issue.parentId, ...issue.subIssueIds, ...issue.relations.map(relation => relation.relatedIssueId)])
  return issues
    .filter(candidate => !linked.has(candidate.id) && !candidate.archivedAt && candidate.team.id === issue.team.id && candidate.state.type !== 'canceled' && !sameRecurringSeries(issue, candidate))
    .map(candidate => ({ candidate, score: titleSimilarity(issue.title, candidate.title) }))
    .filter(item => item.score >= threshold)
    .sort((left, right) => right.score - left.score)
    .slice(0, limit)
}
