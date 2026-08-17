import type { BootstrapData, Initiative, InitiativeUpdate, Project, ProjectUpdate } from '@/types/flow'
import type { PulseRouteView } from '@/lib/app-routes'

export type PulseSourceFilter = 'all' | 'projects' | 'initiatives'
export type PulseUpdateItem =
  | { id: string; kind: 'project'; project: Project; update: ProjectUpdate; createdAt: string; score: number }
  | { id: string; kind: 'initiative'; initiative: Initiative; update: InitiativeUpdate; createdAt: string; score: number }

export type PulseSavedView = { id: string; name: string; icon: string; color: string; source: PulseSourceFilter }

export function buildPulseFeed(data: BootstrapData, view: PulseRouteView, source: PulseSourceFilter = 'all') {
  const projects = data.projects.flatMap(project => (data.projectUpdates[project.id] ?? []).map(update => ({
    id: `project:${update.id}`, kind: 'project' as const, project, update, createdAt: update.createdAt, score: engagement(update),
  })))
  const initiatives = data.initiatives.flatMap(initiative => (data.initiativeUpdates[initiative.id] ?? []).map(update => ({
    id: `initiative:${update.id}`, kind: 'initiative' as const, initiative, update, createdAt: update.createdAt, score: engagement(update),
  })))
  let feed: PulseUpdateItem[] = source === 'projects' ? projects : source === 'initiatives' ? initiatives : [...projects, ...initiatives]
  if (view === 'following') feed = feed.filter(item => item.kind === 'project'
    ? item.project.lead?.id === data.viewer.id || item.project.memberIds.includes(data.viewer.id) || readProjectSubscription(item.project.id)
    : item.initiative.owner?.id === data.viewer.id || item.initiative.subscribed)
  if (view === 'popular') feed = feed.filter(item => item.score > 0)
  return feed.sort((left, right) => view === 'popular' && right.score !== left.score
    ? right.score - left.score
    : +new Date(right.createdAt) - +new Date(left.createdAt))
}

export function pulseViewsKey(workspaceId: string) { return `flow:pulse:${workspaceId}:views` }
export function readPulseViews(workspaceId: string): PulseSavedView[] {
  try {
    const value = JSON.parse(localStorage.getItem(pulseViewsKey(workspaceId)) ?? '[]')
    return Array.isArray(value) ? value.filter(item => item && typeof item.name === 'string').map(item => ({ icon: 'CustomView', color: '#8a8f98', source: 'all', ...item })) : []
  } catch { return [] }
}

function engagement(update: ProjectUpdate | InitiativeUpdate) {
  return (update.comments ?? []).length + Object.values(update.reactions ?? {}).reduce((sum, users) => sum + users.length, 0) * 2
}
function readProjectSubscription(projectId: string) { return localStorage.getItem(`flow:project:${projectId}:subscribed`) === 'true' }
