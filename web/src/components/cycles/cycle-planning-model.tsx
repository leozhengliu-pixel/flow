import type { ReactNode } from 'react'

import type { Cycle, Issue, TeamSettings } from '@/types/flow'
import { LabelIcon, NoAssigneeIcon, NoProjectIcon, PriorityIcon, ProjectIcon, TeamIcon } from '@/components/issue/issue-icons'
import { UserAvatar } from '@/components/ui/user-avatar'
import { estimatePoints, teamUsesEstimates } from '@/lib/estimates'

export type CyclePlanningTab = 'assignees' | 'labels' | 'priority' | 'projects' | 'teams'

export interface CyclePlanningRow { id: string; name: string; value: number; icon: ReactNode }

export type CyclePlanningSettings = (teamId: string) => Partial<Pick<TeamSettings, 'estimateType' | 'estimateAllowZero' | 'estimateCountUnestimated'>> | undefined

const PRIORITY_NAMES = ['No priority', 'Urgent', 'High', 'Medium', 'Low']

/**
 * Planning breakdown of a cycle's issues: estimate points (or issue counts when
 * the team does not use estimates) per assignee, label, priority, project or team.
 */
export function cyclePlanningRows(issues: readonly Issue[], tab: CyclePlanningTab, settingsFor: CyclePlanningSettings): CyclePlanningRow[] {
  const rows = new Map<string, CyclePlanningRow>()
  const add = (id: string, name: string, icon: ReactNode, value: number) => {
    const row = rows.get(id)
    if (row) row.value += value
    else rows.set(id, { id, name, icon, value })
  }
  for (const issue of issues) {
    const settings = settingsFor(issue.team.id)
    const value = teamUsesEstimates(settings) ? estimatePoints(issue.estimate, settings) : 1
    if (tab === 'assignees') {
      if (issue.assignee) add(issue.assignee.id, issue.assignee.displayName || issue.assignee.name, <UserAvatar className="cycle-planning-avatar" avatarUrl={issue.assignee.avatarUrl} name={issue.assignee.displayName || issue.assignee.name}/>, value)
      else add('', 'No assignee', <NoAssigneeIcon size={14}/>, value)
    } else if (tab === 'labels') {
      if (!issue.labels.length) add('', 'No label', <LabelIcon size={14}/>, value)
      for (const label of issue.labels) add(label.id, label.name, <i className="option-dot" style={{ background: label.color }}/>, value)
    } else if (tab === 'priority') {
      add(String(issue.priority), PRIORITY_NAMES[issue.priority] ?? 'No priority', <PriorityIcon priority={issue.priority} size={14}/>, value)
    } else if (tab === 'projects') {
      if (issue.project) add(issue.project.id, issue.project.name, <ProjectIcon size={14} style={{ color: issue.project.color }}/>, value)
      else add('', 'No project', <NoProjectIcon size={14}/>, value)
    } else {
      add(issue.team.id, issue.team.name, <TeamIcon team={issue.team} size={14}/>, value)
    }
  }
  // Priority rows follow the priority order (Urgent → Low, then No priority); the others are largest first.
  const order = (row: CyclePlanningRow) => row.id === '0' ? 5 : Number(row.id)
  return [...rows.values()].sort((a, b) => tab === 'priority' ? order(a) - order(b) : b.value - a.value || a.name.localeCompare(b.name))
}

/**
 * Days until an upcoming cycle starts, or left in a current one: calendar days
 * from the viewer's today to the cycle's start (end) date, never negative —
 * a cycle starting Nov 2 is 7 days away all of Oct 26.
 */
export function cyclePlanningDays(cycle: Pick<Cycle, 'status' | 'startsAt' | 'endsAt'>, now = Date.now()) {
  const local = new Date(now)
  const today = Date.UTC(local.getFullYear(), local.getMonth(), local.getDate())
  const target = Date.parse((cycle.status === 'current' ? cycle.endsAt : cycle.startsAt).slice(0, 10))
  return Math.max(0, Math.round((target - today) / 86_400_000))
}
