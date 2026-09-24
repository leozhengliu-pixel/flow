import { useMemo } from 'react'
import { FlowTooltip } from '@/components/ui/tooltip'
import { Copy } from 'lucide-react'
import { StatusIcon } from '@/components/issue/issue-icons'
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

export function similarIssues(issue: Issue, issues: Issue[], limit = 3, threshold = 0.4) {
  const linked = new Set([issue.id, issue.parentId, ...issue.subIssueIds, ...issue.relations.map(relation => relation.relatedIssueId)])
  return issues
    .filter(candidate => !linked.has(candidate.id) && !candidate.archivedAt && candidate.team.id === issue.team.id && candidate.state.type !== 'canceled')
    .map(candidate => ({ candidate, score: titleSimilarity(issue.title, candidate.title) }))
    .filter(item => item.score >= threshold)
    .sort((left, right) => right.score - left.score)
    .slice(0, limit)
}

/** Linear "Similar issues / Possible duplicates" with one-click Mark as duplicate. */
export function SimilarIssues({ issue, issues, onOpen, onMarkDuplicate }: { issue: Issue; issues: Issue[]; onOpen: (issue: Issue) => void; onMarkDuplicate: (issue: Issue) => void }) {
  const matches = useMemo(() => similarIssues(issue, issues), [issue, issues])
  if (!matches.length || issue.relations.some(relation => relation.type === 'duplicate')) return null
  return <section className="issue-detail-section similar-issues" aria-label="Similar issues">
    <header><strong>{matches.some(match => match.score >= 0.7) ? 'Possible duplicates' : 'Similar issues'}</strong><span>{matches.length}</span></header>
    {matches.map(({ candidate }) => <div className="linked-issue similar-issue-row" key={candidate.id}>
      <StatusIcon state={candidate.state} size={14}/>
      <button type="button" className="similar-issue-open" onClick={() => onOpen(candidate)}><strong data-i18n-ignore>{candidate.identifier}</strong><span data-i18n-ignore>{candidate.title}</span></button>
      <FlowTooltip label={`Mark as duplicate of ${candidate.identifier}`}><button type="button" className="similar-issue-duplicate" onClick={() => onMarkDuplicate(candidate)}><Copy size={12}/>Mark as duplicate</button></FlowTooltip>
    </div>)}
  </section>
}
