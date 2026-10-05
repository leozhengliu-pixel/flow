import type { BootstrapData, PulseItem, SavedView } from '@/types/flow'

export type PulseFilterField = 'author'|'team'|'createdDate'|'updateType'|'health'|'initiative'|'project'|'projectMember'|'projectStatus'|'projectStatusType'|'projectLabel'
export type PulseFilterOperator = 'is'|'isNot'
export type PulseFilter = { id: string; field: PulseFilterField; operator: PulseFilterOperator; values: string[] }
export type PulseFilterMatch = 'all'|'any'
export type PulseViewConfig = { filters: PulseFilter[]; match: PulseFilterMatch }

export const pulseFilterLabels: Record<PulseFilterField,string> = {
  author:'Author', team:'Team', createdDate:'Created date', updateType:'Update type', health:'Update health', initiative:'Initiative', project:'Project', projectMember:'Project members', projectStatus:'Project status', projectStatusType:'Project status type', projectLabel:'Project labels',
}

export function pulseConfigFromView(view?: SavedView): PulseViewConfig {
  if (!view) return { filters: [], match: 'all' }
  const filters = Array.isArray(view.filters) ? view.filters.filter(isPulseFilter) : []
  const match = view.display?.match === 'any' ? 'any' : 'all'
  return { filters, match }
}

export function pulseViewMutation(config: PulseViewConfig) { return { filters: config.filters, display: { match: config.match } } }

export function filterValues(data: BootstrapData, field: PulseFilterField) {
  switch (field) {
    case 'author': return data.users.map(user => ({ id:user.id, label:user.displayName || user.name }))
    case 'team': return data.teams.map(team => ({ id:team.id, label:team.name }))
    case 'createdDate': return [{id:'past-day',label:'Past 24 hours'},{id:'past-week',label:'Past week'},{id:'past-month',label:'Past month'},{id:'past-quarter',label:'Past 3 months'}]
    case 'updateType': {
      const values = [{id:'project',label:'Project update'},{id:'initiative',label:'Initiative update'}]
      // "Team update" only exists with team posts; the feed carries none yet, so it stays hidden.
      if (feedPostUpdateEnabled(data) && teamPostsExist(data)) values.push({id:'team',label:'Team update'})
      return values
    }
    case 'health': return [{id:'onTrack',label:'On track'},{id:'atRisk',label:'At risk'},{id:'offTrack',label:'Off track'},{id:'noUpdate',label:'No update'}]
    case 'initiative': return data.initiatives.map(item => ({ id:item.id, label:item.name }))
    case 'project': return data.projects.map(item => ({ id:item.id, label:item.name }))
    case 'projectMember': return data.users.map(user => ({ id:user.id, label:user.displayName || user.name }))
    case 'projectStatus': return data.projectStatuses.map(item => ({ id:item.id, label:item.name }))
    case 'projectStatusType': return unique(data.projectStatuses.map(item => item.type)).map(type => ({ id:type, label:statusTypeLabel(type) }))
    case 'projectLabel': return data.labels.filter(label => label.resourceType === 'project').map(label => ({ id:label.id, label:label.name }))
  }
}

/** LS-0266 — team update type option is feature-flag gated (`feedPostUpdate`). */
export function feedPostUpdateEnabled(data: BootstrapData) {
  const flags = data.workspaceSettings?.featureFlags ?? {}
  return flags.feedPostUpdate === true || flags['feed-post-update'] === true
}

/** Team posts are not part of Flow's feed yet (the feed API returns project and initiative updates only). */
function teamPostsExist(data: BootstrapData) {
  return (data as BootstrapData & { workspacePosts?: unknown[] }).workspacePosts?.length ? true : false
}

function statusTypeLabel(type: string) {
  switch (type) {
    case 'backlog': return 'Backlog'
    case 'planned': return 'Planned'
    case 'started': return 'Started'
    case 'paused': return 'Paused'
    case 'completed': return 'Completed'
    case 'canceled': return 'Canceled'
    default: return type[0]?.toUpperCase() + type.slice(1)
  }
}




function unique(values:string[]){return [...new Set(values.filter(Boolean))]}
function isPulseFilter(value: unknown): value is PulseFilter { if(!value||typeof value!=='object')return false;const filter=value as Partial<PulseFilter>;return typeof filter.id==='string'&&typeof filter.field==='string'&&(filter.operator==='is'||filter.operator==='isNot')&&Array.isArray(filter.values) }

/** The feed API's `filter` parameter: the saved-view filters, match all/any. */
export function pulseFilterParam(config: PulseViewConfig) {
  const filters = config.filters.filter(filter => filter.values.length).map(({ field, operator, values }) => ({ field, operator, values }))
  return filters.length ? { match: config.match, filters } : undefined
}

export type PulseGroupKey = 'today'|'thisWeek'|'lastWeek'|'thisMonth'|'lastMonth'|'older'
export const pulseGroupLabels: Record<PulseGroupKey,string> = { today:'Today', thisWeek:'This week', lastWeek:'Last week', thisMonth:'This month', lastMonth:'Last month', older:'Older' }

function startOfDay(value: Date) { return new Date(value.getFullYear(), value.getMonth(), value.getDate()) }

/** Linear's feed groups: Today / This week / Last week / This month / Last month / Older. */
export function pulseGroupKey(createdAt: string, now = new Date(), weekStartsOn = 1): PulseGroupKey {
  const at = new Date(createdAt).getTime()
  const today = startOfDay(now)
  if (at >= today.getTime()) return 'today'
  const weekStart = new Date(today)
  weekStart.setDate(today.getDate() - ((today.getDay() - weekStartsOn + 7) % 7))
  if (at >= weekStart.getTime()) return 'thisWeek'
  const lastWeekStart = new Date(weekStart)
  lastWeekStart.setDate(weekStart.getDate() - 7)
  if (at >= lastWeekStart.getTime()) return 'lastWeek'
  const monthStart = new Date(today.getFullYear(), today.getMonth(), 1)
  if (at >= monthStart.getTime()) return 'thisMonth'
  if (at >= new Date(today.getFullYear(), today.getMonth() - 1, 1).getTime()) return 'lastMonth'
  return 'older'
}

/** Index → group header for the first item of each group (sorted newest first). */
export function pulseGroupStarts(items: Pick<PulseItem,'update'>[], now = new Date(), weekStartsOn = 1) {
  const starts = new Map<number, PulseGroupKey>()
  let previous: PulseGroupKey | undefined
  items.forEach((item, index) => {
    const key = pulseGroupKey(item.update.createdAt, now, weekStartsOn)
    if (key !== previous) starts.set(index, key)
    previous = key
  })
  return starts
}

/** First item at or before the stable last-seen time, when newer items sit above it. */
export function pulseLastSeenItemId(items: Pick<PulseItem,'id'|'update'>[], lastSeen: number) {
  if (!lastSeen) return undefined
  const index = items.findIndex(item => new Date(item.update.createdAt).getTime() <= lastSeen)
  return index > 0 ? items[index].id : undefined
}

/** createdAt (ms) of the newest loaded update, or 0 when nothing is loaded. */
export function newestPulseItemTime(items: Pick<PulseItem, 'update'>[]) {
  let newest = 0
  for (const item of items) {
    const time = new Date(item.update.createdAt).getTime()
    if (Number.isFinite(time) && time > newest) newest = time
  }
  return newest
}

/** Last seen only ever moves forward. */
export function nextPulseSeen(previous: string | number | undefined, candidate: string | number) {
  const before = typeof previous === 'number' ? previous : previous ? new Date(previous).getTime() : 0
  const after = typeof candidate === 'number' ? candidate : new Date(candidate).getTime()
  return Number.isFinite(after) && after > (Number.isFinite(before) ? before : 0) ? after : undefined
}

export function weekStartsOnFromSetting(firstDay?: string) {
  const value = (firstDay ?? '').toLowerCase()
  if (value === 'sunday' || value === '0') return 0
  if (value === 'saturday' || value === '6') return 6
  return 1
}
