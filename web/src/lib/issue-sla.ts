import type { BootstrapData, Issue, IssueSLA } from '@/types/flow'

export type SlaState = 'breached' | 'high' | 'medium' | 'low' | 'achieved' | 'failed' | 'paused' | 'none'
export type SlaTiming = IssueSLA & { highRiskAt?: string; mediumRiskAt?: string; canceledAt?: string }

export const SLA_STATE_LABELS: Record<SlaState, string> = {
  breached: 'Breached', high: 'High Risk', medium: 'Moderate Risk', low: 'Low Risk',
  achieved: 'Achieved', failed: 'Failed', paused: 'Paused', none: 'No SLA',
}
export const SLA_STATE_ORDER: SlaState[] = ['breached', 'high', 'medium', 'low', 'achieved', 'failed', 'paused', 'none']

/** Risk is a fixed window before the deadline, rather than a fraction of the target. */
export function getSlaState(sla: SlaTiming | undefined, now = Date.now()): SlaState {
  if (!sla || sla.status === 'removed' || sla.canceledAt) return 'none'
  const due = Date.parse(sla.dueAt)
  if (!Number.isFinite(due)) return 'none'
  if (sla.completedAt || sla.status === 'completed') {
    return sla.completedAt ? Date.parse(sla.completedAt) >= due ? 'failed' : 'achieved' : sla.breachedAt ? 'failed' : 'achieved'
  }
  if (sla.status === 'paused') return 'paused'
  if (sla.status === 'breached' || sla.breachedAt || now >= due) return 'breached'
  const high = sla.highRiskAt ? Date.parse(sla.highRiskAt) : due - 24 * 3_600_000
  const medium = sla.mediumRiskAt ? Date.parse(sla.mediumRiskAt) : due - 168 * 3_600_000
  return now >= high ? 'high' : now >= medium ? 'medium' : 'low'
}

/** Canceled issues have no SLA; manual deadlines also participate in issue views. */
export function issueSlaFor(issue: Pick<Issue, 'id' | 'state' | 'canceledAt' | 'completedAt' | 'createdAt' | 'slaBreachesAt'>, slas: readonly IssueSLA[]): SlaTiming | undefined {
  if (issue.canceledAt || issue.state.type === 'canceled') return undefined
  const current = slas.findLast(sla => sla.issueId === issue.id && sla.status !== 'removed')
  if (current) return current
  if (!issue.slaBreachesAt) return undefined
  return {
    id: `manual-${issue.id}`, issueId: issue.id, ruleId: '', startedAt: issue.createdAt,
    dueAt: issue.slaBreachesAt, completedAt: issue.completedAt,
    status: issue.completedAt ? 'completed' : 'active', pausedMinutes: 0, remainingMinutes: 0,
  }
}

export function slaInstances(data: Partial<Pick<BootstrapData, 'issueSlas' | 'issues'>>): Map<string, SlaTiming> {
  const instances = new Map((data.issueSlas ?? []).filter(sla => sla.status !== 'removed').map(sla => [sla.issueId, sla]))
  for (const issue of data.issues ?? []) {
    if (issue.canceledAt || issue.state.type === 'canceled') instances.delete(issue.id)
    else if (!instances.has(issue.id)) {
      const manual = issueSlaFor(issue, [])
      if (manual) instances.set(issue.id, manual)
    }
  }
  return instances
}

/** Query id lists stay within the server's per-predicate value budget. */
export function slaIssueIdsQuery(ids: string[]): Record<string, unknown> {
  if (ids.length <= 1000) return { field: 'id', operator: 'in', values: ids }
  const or = []
  for (let offset = 0; offset < ids.length; offset += 1000) or.push({ field: 'id', operator: 'in', values: ids.slice(offset, offset + 1000) })
  return { or }
}
