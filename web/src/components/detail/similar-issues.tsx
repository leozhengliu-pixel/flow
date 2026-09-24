import { useEffect, useMemo, useState } from 'react'
import { fetchSimilarIssues, listIssueRecords } from '@/lib/api'
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
export function SimilarIssues({ issue, issues, workspaceKey, onOpen, onMarkDuplicate }: { issue: Issue; issues: Issue[]; workspaceKey?: string; onOpen: (issue: Issue) => void; onMarkDuplicate: (issue: Issue) => void }) {
  // Prefer the server ranking (whole workspace, descriptions, synonyms); fall back to local titles.
  const [server, setServer] = useState<{ id: string; matches: { candidate: Issue; score: number; duplicate: boolean }[] } | null>(null)
  const [serverFailed, setServerFailed] = useState(false)
  useEffect(() => {
    const controller = new AbortController()
    setServerFailed(false)
    fetchSimilarIssues(issue.id, controller.signal, workspaceKey)
      .then(response => setServer({ id: issue.id, matches: response.results.slice(0, 3).map(result => ({ candidate: result.issue, score: result.score, duplicate: result.possibleDuplicate })) }))
      .catch(() => { if (!controller.signal.aborted) setServerFailed(true) })
    return () => controller.abort()
  }, [issue.id, issue.title, workspaceKey])
  // Deep links and paged workspaces only hold a few issues locally: compare against the team's recent issues.
  const teamIssueCount = issues.filter(item => item.team.id === issue.team.id).length
  const [remote, setRemote] = useState<Issue[]>([])
  useEffect(() => {
    if (!serverFailed || teamIssueCount > 50) return
    const controller = new AbortController()
    listIssueRecords({ teamId: issue.team.id, archived: 'false', sort: 'updatedAt', direction: 'desc', limit: 250 }, controller.signal, workspaceKey)
      .then(page => setRemote(page.items)).catch(() => undefined)
    return () => controller.abort()
  }, [issue.team.id, serverFailed, teamIssueCount, workspaceKey])
  const pool = useMemo(() => { const byId = new Map(remote.map(item => [item.id, item])); for (const item of issues) byId.set(item.id, item); return [...byId.values()] }, [issues, remote])
  const local = useMemo(() => serverFailed ? similarIssues(issue, pool).map(match => ({ ...match, duplicate: match.score >= 0.7 })) : [], [issue, pool, serverFailed])
  const matches = server?.id === issue.id && !serverFailed ? server.matches : local
  if (!matches.length || issue.relations.some(relation => relation.type === 'duplicate')) return null
  return <section className="issue-detail-section similar-issues" aria-label="Similar issues">
    <header><strong>{matches.some(match => match.duplicate) ? 'Possible duplicates' : 'Similar issues'}</strong><span>{matches.length}</span></header>
    {matches.map(({ candidate }) => <div className="similar-issue-row" key={candidate.id}>
      <StatusIcon state={candidate.state} size={14}/>
      <button type="button" className="similar-issue-open" onClick={() => onOpen(candidate)}><strong data-i18n-ignore>{candidate.identifier}</strong><span data-i18n-ignore>{candidate.title}</span></button>
      <FlowTooltip label={`Mark as duplicate of ${candidate.identifier}`}><button type="button" className="similar-issue-duplicate" onClick={() => onMarkDuplicate(candidate)}><Copy size={12}/>Mark as duplicate</button></FlowTooltip>
    </div>)}
  </section>
}
