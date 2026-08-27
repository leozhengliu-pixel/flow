import type { BootstrapData, InitiativeUpdate, ProjectUpdate, SavedView } from '@/types/flow'
import type { PulseRouteView } from '@/lib/app-routes'

export type PulseFilterField = 'author'|'team'|'createdDate'|'updateType'|'health'|'initiative'|'project'|'projectMember'|'projectStatus'|'projectLabel'
export type PulseFilterOperator = 'is'|'isNot'
export type PulseFilter = { id: string; field: PulseFilterField; operator: PulseFilterOperator; values: string[] }
export type PulseFilterMatch = 'all'|'any'
export type PulseViewConfig = { filters: PulseFilter[]; match: PulseFilterMatch }
export type PulseUpdateItem =
  | { id: string; kind: 'project'; project: BootstrapData['projects'][number]; update: ProjectUpdate; createdAt: string; score: number }
  | { id: string; kind: 'initiative'; initiative: BootstrapData['initiatives'][number]; update: InitiativeUpdate; createdAt: string; score: number }

export const pulseFilterLabels: Record<PulseFilterField,string> = {
  author:'Author', team:'Team', createdDate:'Created date', updateType:'Update type', health:'Update health', initiative:'Initiative', project:'Project', projectMember:'Project members', projectStatus:'Project status', projectLabel:'Project labels',
}

export function buildPulseFeed(data: BootstrapData, view: PulseRouteView, config: PulseViewConfig = { filters: [], match: 'all' }) {
  const projects = data.projects.flatMap(project => (data.projectUpdates[project.id] ?? []).map(update => ({
    id: `project:${update.id}`, kind: 'project' as const, project, update, createdAt: update.createdAt, score: engagement(update),
  })))
  const initiatives = data.initiatives.flatMap(initiative => (data.initiativeUpdates[initiative.id] ?? []).map(update => ({
    id: `initiative:${update.id}`, kind: 'initiative' as const, initiative, update, createdAt: update.createdAt, score: engagement(update),
  })))
  let feed: PulseUpdateItem[] = [...projects, ...initiatives]
  if (view === 'following') feed = feed.filter(item => follows(data, item))
  if (config.filters.length) feed = feed.filter(item => {
    const matches = config.filters.map(filter => matchesFilter(data, item, filter))
    return config.match === 'any' ? matches.some(Boolean) : matches.every(Boolean)
  })
  return feed.sort((left, right) => {
    if (view === 'popular') {
      const score = popularity(right) - popularity(left)
      if (score) return score
    }
    return +new Date(right.createdAt) - +new Date(left.createdAt)
  })
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
    case 'updateType': return [{id:'project',label:'Project update'},{id:'initiative',label:'Initiative update'}]
    case 'health': return [{id:'onTrack',label:'On track'},{id:'atRisk',label:'At risk'},{id:'offTrack',label:'Off track'},{id:'noUpdate',label:'No update'}]
    case 'initiative': return data.initiatives.map(item => ({ id:item.id, label:item.name }))
    case 'project': return data.projects.map(item => ({ id:item.id, label:item.name }))
    case 'projectMember': return data.users.map(user => ({ id:user.id, label:user.displayName || user.name }))
    case 'projectStatus': return data.projectStatuses.map(item => ({ id:item.id, label:item.name }))
    case 'projectLabel': return data.labels.filter(label => label.resourceType === 'project').map(label => ({ id:label.id, label:label.name }))
  }
}

function follows(data: BootstrapData, item: PulseUpdateItem) {
  if (item.kind === 'project') return item.project.lead?.id === data.viewer.id || item.project.memberIds.includes(data.viewer.id) || data.subscriptions.some(subscription => subscription.userId === data.viewer.id && subscription.resourceType === 'project' && subscription.resourceId === item.project.id)
  return item.initiative.owner?.id === data.viewer.id || item.initiative.subscribed || item.initiative.projectIds.some(projectId => data.projects.find(project => project.id === projectId)?.memberIds.includes(data.viewer.id))
}

function matchesFilter(data: BootstrapData, item: PulseUpdateItem, filter: PulseFilter) {
  const values = itemValues(data, item, filter.field)
  const match = filter.values.some(value => values.includes(value))
  return filter.operator === 'isNot' ? !match : match
}

function itemValues(data: BootstrapData, item: PulseUpdateItem, field: PulseFilterField): string[] {
  switch (field) {
    case 'author': return [item.update.user.id]
    case 'team': return item.kind === 'project' ? item.project.teamIds : unique([item.initiative.leadTeamId ?? '', ...item.initiative.contributingTeamIds])
    case 'createdDate': return dateBuckets(item.createdAt)
    case 'updateType': return [item.kind]
    case 'health': return [item.update.health]
    case 'initiative': return item.kind === 'initiative' ? [item.initiative.id] : item.project.initiatives
    case 'project': return item.kind === 'project' ? [item.project.id] : item.initiative.projectIds
    case 'projectMember': return item.kind === 'project' ? unique([item.project.lead?.id ?? '', ...item.project.memberIds]) : unique(item.initiative.projectIds.flatMap(id => { const project=data.projects.find(item=>item.id===id);return project?[project.lead?.id??'',...project.memberIds]:[] }))
    case 'projectStatus': return item.kind === 'project' ? [item.project.status.id] : unique(item.initiative.projectIds.map(id => data.projects.find(project => project.id === id)?.status.id ?? ''))
    case 'projectLabel': return item.kind === 'project' ? item.project.labelIds : unique(item.initiative.projectIds.flatMap(id => data.projects.find(project => project.id === id)?.labelIds ?? []))
  }
}

function dateBuckets(value: string) { const age=Date.now()-new Date(value).getTime();const day=86_400_000;return [['past-day',1],['past-week',7],['past-month',30],['past-quarter',90]].filter(([,days])=>age<=Number(days)*day).map(([id])=>String(id)) }
function popularity(item: PulseUpdateItem) { const ageHours=Math.max(0,(Date.now()-new Date(item.createdAt).getTime())/3_600_000);return item.score*100+Math.max(0,168-ageHours)/168 }
function engagement(update: ProjectUpdate | InitiativeUpdate) { return (update.comments ?? []).length + Object.values(update.reactions ?? {}).reduce((sum, users) => sum + users.length, 0) * 2 }
function unique(values:string[]){return [...new Set(values.filter(Boolean))]}
function isPulseFilter(value: unknown): value is PulseFilter { if(!value||typeof value!=='object')return false;const filter=value as Partial<PulseFilter>;return typeof filter.id==='string'&&typeof filter.field==='string'&&(filter.operator==='is'||filter.operator==='isNot')&&Array.isArray(filter.values) }
