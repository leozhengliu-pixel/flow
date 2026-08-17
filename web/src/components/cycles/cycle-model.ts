import type { Cycle, Issue } from '@/types/flow'

export interface CycleStats {
  scope: number
  started: number
  completed: number
  capacity: number
  capacityPercent: number
  successPercent: number
  startedPercent: number
  completedPercent: number
}

export function cycleStats(cycle: Cycle, issues: Issue[]): CycleStats {
  const scoped = issues.filter(issue => issue.cycleId === cycle.id && !issue.archivedAt)
  const completed = scoped.filter(issue => issue.state.type === 'completed').length
  const started = scoped.filter(issue => issue.state.type === 'started').length
  const scope = scoped.length
  const capacity = Math.max(cycle.capacity, 1)
  return {
    scope,
    started,
    completed,
    capacity: cycle.capacity,
    capacityPercent: Math.round(scope / capacity * 100),
    successPercent: scope ? Math.round((completed + started * .25) / scope * 100) : 0,
    startedPercent: scope ? Math.round(started / scope * 100) : 0,
    completedPercent: scope ? Math.round(completed / scope * 100) : 0,
  }
}

export function formatCycleRange(cycle: Cycle, compact = false) {
  const formatter = new Intl.DateTimeFormat('en-US', compact ? { month: 'short', day: 'numeric' } : { month: 'short', day: 'numeric', year: 'numeric' })
  return `${formatter.format(new Date(cycle.startsAt))} – ${formatter.format(new Date(cycle.endsAt))}`
}

export function formatCycleDay(value: string) {
  const date = new Date(value)
  return { month: new Intl.DateTimeFormat('en-US', { month: 'short' }).format(date), day: date.getUTCDate() }
}

export function weekdaysLeft(cycle: Cycle) {
  const cursor = new Date()
  const end = new Date(cycle.endsAt)
  let days = 0
  cursor.setHours(0, 0, 0, 0)
  while (cursor <= end) {
    if (cursor.getDay() !== 0 && cursor.getDay() !== 6) days++
    cursor.setDate(cursor.getDate() + 1)
  }
  return Math.max(0, days)
}

export function cycleStatusLabel(status: Cycle['status']) {
  return status[0].toUpperCase() + status.slice(1)
}

