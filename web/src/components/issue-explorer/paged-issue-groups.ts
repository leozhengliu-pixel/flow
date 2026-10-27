import type { BootstrapData } from '@/types/flow'
import type { IssueQueryInput } from '@/lib/api'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import { statusGroupIndex } from '@/lib/status-groups'
import { groupDescriptors, type IssueGroupingContext } from './issue-grouping'

type StatusGroupContext = Pick<BootstrapData, 'states' | 'teams'> & Partial<Pick<BootstrapData, 'teamSettings'>>

/**
 * The query of one group's pages. A status group that several team workflows share (`statusGroupFor`) pages all of
 * its statuses at once, always in the same order, so its cursors stay valid between pages.
 */
export function pagedGroupQuery(query: IssueQueryInput, value: string, data: StatusGroupContext): IssueQueryInput {
  if ((query.groupBy ?? 'status') !== 'status' || !data.states.length) return { ...query, groupValue: value }
  const members = statusGroupIndex(data).memberIds(value)
  return members.length > 1 ? { ...query, groupValue: undefined, groupValues: members } : { ...query, groupValue: value }
}

/** The server's per-status groups merged into the list's status groups (same name and type), in workflow order. */
export function pagedStatusGroups<T extends { value: string; count: number }>(groups: readonly T[], data: StatusGroupContext): T[] {
  const merged = new Map<string, { group: T; rank: number }>()
  for (const group of groups) {
    const state = data.states.find(item => item.id === group.value)
    const [descriptor] = groupDescriptors({ state: state ?? { id: group.value, name: group.value, type: '' } } as unknown as MyIssuesRowData, 'status', { data: data as IssueGroupingContext['data'] })
    const current = merged.get(descriptor.id)
    if (current) current.group = { ...current.group, count: current.group.count + group.count }
    else merged.set(descriptor.id, { group: { ...group, value: descriptor.id }, rank: Number(descriptor.rank) })
  }
  return [...merged.values()].sort((a, b) => a.rank - b.rank).map(item => item.group)
}
