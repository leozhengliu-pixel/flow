/**
 * LS-0500 ProjectsRelevance — user-relative relevance scorer for project lists / pickers.
 * Mirrors Linear's ProjectsRelevance: lead/member boosts, inaccessible-team penalty,
 * recency / self-created boosts, active boost, completed/canceled penalty; sort by
 * -relevance then name.
 */

export type ProjectRelevanceInput = {
  id: string
  name: string
  leadId?: string | null
  memberIds?: string[]
  teamIds?: string[]
  createdAt?: string | null
  creatorId?: string | null
  /** True when project is in an active/started workflow status. */
  isActive?: boolean
  /** True when completed or canceled. */
  isClosed?: boolean
}

export type ProjectRelevanceViewer = {
  id: string
  /** Team IDs the viewer can currently access / is active on. */
  activeTeamIds?: string[]
}

export type ProjectRelevanceOptions = {
  /** Override clock for tests (ms since epoch). */
  now?: number
  /** Custom sort after scoring (receives scored rows). */
  sortFn?: <T extends ProjectRelevanceInput>(
    rows: Array<{ project: T; relevance: number }>,
  ) => Array<{ project: T; relevance: number }>
}

const DAY_MS = 24 * 60 * 60 * 1000

export function projectRelevanceScore(
  project: ProjectRelevanceInput,
  viewer: ProjectRelevanceViewer,
  now = Date.now(),
): number {
  const members = project.memberIds ?? []
  const isMember = members.includes(viewer.id)
  const isLead = project.leadId === viewer.id
  const accessibleTeamIds = new Set(viewer.activeTeamIds ?? [])
  const projectTeams = project.teamIds ?? []
  const hasAccessibleTeam =
    projectTeams.length === 0 ||
    projectTeams.some(id => accessibleTeamIds.has(id))
  const createdAt = project.createdAt ? Date.parse(project.createdAt) : NaN
  const recentCreate = Number.isFinite(createdAt) && now - createdAt <= 2 * DAY_MS
  const selfCreatedRecent =
    Number.isFinite(createdAt) &&
    now - createdAt <= 10 * DAY_MS &&
    project.creatorId === viewer.id
  const closed = Boolean(project.isClosed)
  let score = 0
  if (isLead) score += closed ? 0.25 : 1.25
  else if (isMember) score += closed ? 0.2 : 1
  if (!hasAccessibleTeam) score -= 0.3
  if (recentCreate) score += 0.15
  if (selfCreatedRecent) score += 0.5
  if (project.isActive) score += 0.25
  if (closed) score -= 1
  return score
}

function defaultSort<T extends ProjectRelevanceInput>(
  rows: Array<{ project: T; relevance: number }>,
) {
  return [...rows].sort((left, right) => {
    if (right.relevance !== left.relevance) return right.relevance - left.relevance
    return left.project.name.localeCompare(right.project.name)
  })
}

/** Rank projects for the given viewer; returns projects in relevance order. */
export function sortProjectsByRelevance<T extends ProjectRelevanceInput>(
  projects: T[],
  viewer: ProjectRelevanceViewer | undefined,
  options?: ProjectRelevanceOptions,
): T[] {
  if (!viewer) return projects
  const now = options?.now ?? Date.now()
  const scored = projects.map(project => ({
    project,
    relevance: projectRelevanceScore(project, viewer, now),
  }))
  const sorted = (options?.sortFn ?? defaultSort)(scored)
  return sorted.map(row => row.project)
}

/** Adapt a list/board ProjectPageItem-shaped record into relevance input. */
export function relevanceInputFromPageItem(item: {
  id: string
  name: string
  lead?: { id: string } | null
  memberIds?: string[]
  teamIds?: string[]
  createdAt?: string
  statusType?: string
}): ProjectRelevanceInput {
  const statusType = item.statusType ?? ''
  return {
    id: item.id,
    name: item.name,
    leadId: item.lead?.id,
    memberIds: item.memberIds,
    teamIds: item.teamIds,
    createdAt: item.createdAt,
    isActive: statusType === 'started',
    isClosed: statusType === 'completed' || statusType === 'canceled',
  }
}

/** Adapt a domain Project into relevance input. */
export function relevanceInputFromProject(project: {
  id: string
  name: string
  lead?: { id: string } | null
  memberIds?: string[]
  teamIds?: string[]
  createdAt?: string
  status?: { type?: string }
}): ProjectRelevanceInput {
  const statusType = project.status?.type ?? ''
  return {
    id: project.id,
    name: project.name,
    leadId: project.lead?.id,
    memberIds: project.memberIds,
    teamIds: project.teamIds,
    createdAt: project.createdAt,
    isActive: statusType === 'started',
    isClosed: statusType === 'completed' || statusType === 'canceled',
  }
}
