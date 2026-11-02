import { useEffect, useState } from 'react'
import { listIssueRecordGroups, type IssueQueryInput } from '@/lib/api'
import { estimatePoints, teamUsesEstimates } from '@/lib/estimates'
import { resolvedTeamSettings } from '@/lib/team-hierarchy'
import type { BootstrapData } from '@/types/flow'
import { pagedGroupFilter } from './paged-issue-groups'

type Context = Pick<BootstrapData, 'states' | 'teams' | 'teamSettings'>

export function useBoardEstimateTotals(query: IssueQueryInput | undefined, groups: { value: string }[], data: Context, revision: number) {
  const key = query && groups.length ? JSON.stringify({ query, groups: groups.map(group => group.value), data: { states: data.states, teams: data.teams, teamSettings: data.teamSettings }, revision }) : undefined
  const [result, setResult] = useState<{ key?: string; values?: Record<string, number> }>({})
  useEffect(() => {
    if (!key) return
    const abort = new AbortController()
    const { query, groups, data } = JSON.parse(key) as { query: IssueQueryInput; groups: string[]; data: Context }
    // Complete server buckets keep totals independent of how many cards have loaded.
    void (async () => {
      const totals: Record<string, number> = {}
      for (const value of groups) {
        let total: number | undefined
        for (const team of data.teams) {
          if (abort.signal.aborted) return
          const settings = resolvedTeamSettings(data.teamSettings, team.id)
          if (!teamUsesEstimates(settings)) continue
          const buckets = await listIssueRecordGroups({ ...query, groupBy: 'estimate', groupValue: undefined, groupValues: undefined, groupCases: undefined, filter: { and: [query.filter ?? { and: [] }, pagedGroupFilter(query, value, data), { field: 'teamId', values: [team.id] }] } }, abort.signal)
          if (buckets.groups.some(bucket => bucket.count > 0)) total = (total ?? 0) + buckets.groups.reduce((sum, bucket) => sum + bucket.count * estimatePoints(bucket.value === '' ? undefined : Number(bucket.value), settings), 0)
        }
        if (total !== undefined) totals[value] = total
      }
      if (!abort.signal.aborted) setResult({ key, values: totals })
    })().catch(() => { if (!abort.signal.aborted) setResult({ key }) })
    return () => abort.abort()
  }, [key])
  return result.key === key ? result.values : undefined
}
