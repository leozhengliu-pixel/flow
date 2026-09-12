export const MILESTONE_SHAPE_PATH = 'M7.3406 2.32C7.68741 1.89333 8.31259 1.89333 8.6594 2.32L12.7903 7.402C13.0699 7.74597 13.0699 8.25403 12.7903 8.598L8.6594 13.68C8.31259 14.1067 7.68741 14.1067 7.3406 13.68L3.2097 8.598C2.9301 8.25403 2.9301 7.74597 3.2097 7.402L7.3406 2.32Z'
export const MILESTONE_PATH_LENGTH = 31

type MilestoneIssue = {
  archivedAt?: string
  project?: { id: string }
  projectMilestoneId?: string
  state: { type: string }
}

export function milestoneIssueProgress(issues: MilestoneIssue[], projectId: string | undefined, milestoneId: string) {
  const scoped = issues.filter(issue => issue.project?.id === projectId && issue.projectMilestoneId === milestoneId && !issue.archivedAt)
  const done = scoped.filter(issue => issue.state.type === 'completed' || issue.state.type === 'canceled').length
  return scoped.length ? Math.round(done / scoped.length * 100) : 0
}

export function currentProjectMilestone<T extends { id: string }>(milestones: T[] | undefined, progressFor: (id: string) => number) {
  const items = milestones ?? []
  return items.find(item => progressFor(item.id) < 100) ?? items[0]
}

export function isMilestoneDateOverdue(value?: string) {
  if (!value) return false
  const date = new Date(/^\d{4}-\d{2}-\d{2}$/.test(value) ? `${value}T00:00:00` : value)
  if (!Number.isFinite(date.getTime())) return false
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  return date < today
}

export function milestoneProgressLength(progress: number) {
  const clamped = Math.max(0, Math.min(100, progress))
  const length = Math.round(MILESTONE_PATH_LENGTH * clamped) / 100
  return Math.max(2.5, Math.min(MILESTONE_PATH_LENGTH, length))
}
