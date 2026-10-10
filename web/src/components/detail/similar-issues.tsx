import { useEffect, useMemo, useRef, useState } from 'react'
import { fetchSimilarIssues, listIssueRecords } from '@/lib/api'
import { FlowTooltip } from '@/components/ui/tooltip'
import { Copy } from 'lucide-react'
import { StatusIcon } from '@/components/issue/issue-icons'
import type { Issue } from '@/types/flow'
import { sameRecurringSeries, similarIssues } from './similar-issues-model'

/** Linear "Similar issues / Possible duplicates" with one-click Mark as duplicate. */
export function SimilarIssues({ issue, issues, workspaceKey, onOpen, onMarkDuplicate }: { issue: Issue; issues: Issue[]; workspaceKey?: string; onOpen: (issue: Issue) => void; onMarkDuplicate: (issue: Issue) => void }) {
  // Prefer the server ranking (whole workspace, descriptions, synonyms); fall back to local titles.
  const [server, setServer] = useState<{ id: string; matches: { candidate: Issue; score: number; duplicate: boolean }[] } | null>(null)
  const [serverFailed, setServerFailed] = useState(false)
  // Latest issue for the response filter, so a new issue object identity does not refetch.
  const issueRef = useRef(issue)
  useEffect(() => { issueRef.current = issue })
  useEffect(() => {
    const controller = new AbortController()
    setServerFailed(false)
    fetchSimilarIssues(issue.id, controller.signal, workspaceKey)
      .then(response => setServer({ id: issue.id, matches: response.results.filter(result => !sameRecurringSeries(issueRef.current, result.issue)).slice(0, 3).map(result => ({ candidate: result.issue, score: result.score, duplicate: result.possibleDuplicate })) }))
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
