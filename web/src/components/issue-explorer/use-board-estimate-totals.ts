import { useEffect, useState } from 'react'
import { listIssueRecordGroups, type IssueQueryInput } from '@/lib/api'
import { teamUsesEstimates } from '@/lib/estimates'
import { resolvedTeamSettings } from '@/lib/team-hierarchy'
import type { BootstrapData } from '@/types/flow'
import { pagedStatusGroups } from './paged-issue-groups'

type Context = Pick<BootstrapData, 'states' | 'teams' | 'teamSettings'>

/**
 * Estimate point totals of a paged list's groups, keyed by group value: one groups request with
 * `aggregate: 'estimatePoints'`, which the server counts from complete buckets with each issue's team estimate
 * settings, so the totals do not depend on how many rows have loaded. A group without issues of a team that uses
 * estimates has no total, and without any such team there is no request. Statuses several team workflows share add
 * up into their merged group.
 */
export function useBoardEstimateTotals(query: IssueQueryInput | undefined, groups: { value: string }[], data: Context, revision: number) {
  const estimating = data.teams.some(team => teamUsesEstimates(resolvedTeamSettings(data.teamSettings ?? {}, team.id)))
  const key = query && groups.length && estimating ? JSON.stringify({ query, groups: groups.map(group => group.value), data: { states: data.states, teams: data.teams, teamSettings: data.teamSettings }, revision }) : undefined
  const [result, setResult] = useState<{ key?: string; values?: Record<string, number> }>({})
  useEffect(() => {
    if (!key) return
    const abort = new AbortController()
    const { query, data } = JSON.parse(key) as { query: IssueQueryInput; data: Context }
    listIssueRecordGroups({ ...query, aggregate: 'estimatePoints' }, abort.signal).then(response => {
      if (abort.signal.aborted) return
      const buckets = response.groups.flatMap(group => group.estimatePoints === undefined ? [] : [{ value: group.value, count: group.estimatePoints }])
      const merged = (query.groupBy ?? 'status') === 'status' ? pagedStatusGroups(buckets, data) : buckets
      setResult({ key, values: Object.fromEntries(merged.map(group => [group.value, group.count])) })
    }).catch(() => { if (!abort.signal.aborted) setResult({ key }) })
    return () => abort.abort()
  }, [key])
  return result.key === key ? result.values : undefined
}
