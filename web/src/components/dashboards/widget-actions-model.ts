import type { Dashboard, DashboardWidget } from '@/types/flow'

export type DashboardPickerGroup = {
  id: string
  label: string
  dashboards: Array<Pick<Dashboard, 'id' | 'name' | 'visibility' | 'teamIds' | 'updatedAt'>>
}

export function insightDefaultTitle(config: DashboardWidget['config']): string {
  const measure = String(config.measure ?? 'issue_count').replaceAll('_', ' ')
  const slice = config.slice && config.slice !== 'none' ? String(config.slice) : undefined
  const segment = config.segment && config.segment !== 'none' ? String(config.segment) : undefined
  if (slice && segment) return `${measure} by ${slice} and ${segment}`
  if (slice) return `${measure} by ${slice}`
  return measure.replace(/\b\w/g, char => char.toUpperCase())
}

export function formatOriginDescription(sourceName: string): string {
  return `Added from dashboard ${sourceName}`
}

export function groupDashboardsForCopy(options: {
  dashboards: Dashboard[]
  currentDashboardId?: string
  currentTeamId?: string
  recentIds?: string[]
}): DashboardPickerGroup[] {
  const { dashboards, currentDashboardId, currentTeamId, recentIds = [] } = options
  const others = dashboards.filter(item => item.id !== currentDashboardId)
  const recent = recentIds
    .map(id => others.find(item => item.id === id))
    .filter(Boolean) as Dashboard[]
  const team = currentTeamId
    ? others.filter(item => item.visibility === 'team' && item.teamIds.includes(currentTeamId) && !recent.some(r => r.id === item.id))
    : []
  const workspace = others.filter(
    item => item.visibility === 'workspace' && !recent.some(r => r.id === item.id) && !team.some(t => t.id === item.id),
  )
  const rest = others.filter(
    item => !recent.some(r => r.id === item.id) && !team.some(t => t.id === item.id) && !workspace.some(w => w.id === item.id),
  )
  const groups: DashboardPickerGroup[] = []
  if (recent.length) groups.push({ id: 'recent', label: 'Recent', dashboards: recent })
  if (team.length) groups.push({ id: 'team', label: 'Current team', dashboards: team })
  if (workspace.length) groups.push({ id: 'workspace', label: 'Workspace', dashboards: workspace })
  if (rest.length) groups.push({ id: 'other', label: 'Other dashboards', dashboards: rest })
  return groups
}

export function cloneWidget(widget: DashboardWidget, position: number): DashboardWidget {
  return {
    ...widget,
    id: typeof crypto !== 'undefined' && 'randomUUID' in crypto
      ? crypto.randomUUID()
      : `widget_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`,
    position,
    title: widget.title,
  }
}
