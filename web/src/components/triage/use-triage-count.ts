import { useEffect, useMemo, useState } from 'react'
import { listIssueRecords } from '@/lib/api'
import type { BootstrapData, Team } from '@/types/flow'
import { isIssueInTriage, isSnoozed, TRIAGE_CHANGED_EVENT as CHANGED, triageIssueFilter } from './triage-model'


/**
 * Sidebar Triage count: unsnoozed issues waiting in the team's triage. With every issue in the bootstrap it is counted
 * locally; paged workspaces ask the server for the total instead of scanning a partial collection.
 */
export function useTriageCount(data: BootstrapData, team: Pick<Team, 'id'>): number | undefined {
  const enabled = Boolean(data.teamSettings?.[team.id]?.triageEnabled)
  const paged = Boolean(data.issueCollectionPaged)
  const local = useMemo(() => {
    if (!enabled || paged) return 0
    let count = 0
    for (const issue of data.issues) if (issue.team.id === team.id && !issue.archivedAt && isIssueInTriage(issue, data.teamSettings) && !isSnoozed(issue)) count++
    return count
  }, [data.issues, data.teamSettings, enabled, paged, team.id])
  // Local edits that may move issues in or out of triage refresh the server count.
  const signature = useMemo(() => {
    if (!enabled || !paged) return ''
    return data.issues.filter(issue => issue.team.id === team.id).map(issue => `${issue.id}:${issue.state.type}:${issue.triagedAt ?? ''}:${issue.snoozedUntil ?? ''}:${issue.archivedAt ?? ''}`).join('|')
  }, [data.issues, enabled, paged, team.id])
  const [remote, setRemote] = useState<number>()
  const [tick, setTick] = useState(0)
  useEffect(() => {
    if (!enabled || !paged) return
    const refresh = () => setTick(value => value + 1)
    window.addEventListener(CHANGED, refresh)
    return () => window.removeEventListener(CHANGED, refresh)
  }, [enabled, paged])
  useEffect(() => {
    if (!enabled || !paged) return
    const controller = new AbortController()
    const timer = setTimeout(() => {
      const count = (excludeSnoozed: boolean) => listIssueRecords({ teamId: team.id, archived: 'false', filter: triageIssueFilter({ excludeSnoozed }), limit: 1, includeTotal: true }, controller.signal, data.workspace.urlKey)
      // A server without the snooze index rejects the snooze filter: count every triage issue instead.
      void count(true).catch(error => { if (controller.signal.aborted) throw error; return count(false) })
        .then(page => { if (!controller.signal.aborted) setRemote(page.total ?? page.items.length) })
        .catch(() => undefined)
    }, 150)
    return () => { clearTimeout(timer); controller.abort() }
  }, [data.workspace.urlKey, enabled, paged, signature, team.id, tick])
  if (!enabled) return undefined
  return paged ? remote ?? 0 : local
}
