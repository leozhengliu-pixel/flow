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

// Cycle boundaries are calendar days stored as UTC midnights; format and count
// them in UTC so the viewer's timezone never shifts a cycle by a day.
export function formatCycleRange(cycle: Cycle, compact = false) {
  const formatter = new Intl.DateTimeFormat('en-US', compact ? { month: 'short', day: 'numeric', timeZone: 'UTC' } : { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' })
  return `${formatter.format(new Date(cycle.startsAt))} – ${formatter.format(new Date(cycle.endsAt))}`
}

export function formatCycleDay(value: string) {
  const date = new Date(value)
  return { month: new Intl.DateTimeFormat('en-US', { month: 'short', timeZone: 'UTC' }).format(date), day: date.getUTCDate() }
}

/** Weekdays from the viewer's today through the cycle's last calendar day, inclusive. */
export function weekdaysLeft(cycle: Cycle) {
  const now = new Date()
  const end = new Date(cycle.endsAt)
  const cursor = new Date(Date.UTC(now.getFullYear(), now.getMonth(), now.getDate()))
  const last = Date.UTC(end.getUTCFullYear(), end.getUTCMonth(), end.getUTCDate())
  let days = 0
  while (cursor.getTime() <= last) {
    if (cursor.getUTCDay() !== 0 && cursor.getUTCDay() !== 6) days++
    cursor.setUTCDate(cursor.getUTCDate() + 1)
  }
  return days
}

export function cycleStatusLabel(status: Cycle['status']) {
  return status[0].toUpperCase() + status.slice(1)
}

