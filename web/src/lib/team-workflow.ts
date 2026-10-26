import type { BootstrapData, WorkflowState, WorkflowStateType } from '@/types/flow'

export type WorkflowData = Pick<BootstrapData, 'states'> & Partial<Pick<BootstrapData, 'teamSettings'>>

/**
 * The team's effective workflow: a team that inherits its parent's statuses uses the
 * parent's workflow, a team with its own statuses uses those, and any other team uses
 * the workspace statuses. Mirrors the API's statesForTeam, which rejects any other state.
 */
export function teamWorkflowStates(data: WorkflowData, teamId: string, seen = new Set<string>()): WorkflowState[] {
  if (seen.has(teamId)) return []
  seen.add(teamId)
  const settings = data.teamSettings?.[teamId]
  if (settings?.inheritWorkflowStatuses && settings.parentTeamId) return teamWorkflowStates(data, settings.parentTeamId, seen)
  const specific = data.states.some(state => state.teamId === teamId)
  return data.states.filter(state => (specific ? state.teamId === teamId : !state.teamId)).sort((a, b) => (a.position ?? 0) - (b.position ?? 0))
}

/** A new issue's status in the team: its configured default, the workflow's default, the first unstarted, else the first status. */
export function teamDefaultState(data: WorkflowData, teamId: string, states = teamWorkflowStates(data, teamId)): WorkflowState | undefined {
  const configured = data.teamSettings?.[teamId]?.defaultStateId
  return states.find(state => state.id === configured)
    ?? states.find(state => state.default)
    ?? states.find(state => state.type === 'unstarted')
    ?? states[0]
}

/** A new sub-issue's status in the team, as in the reference app: its first unstarted status, else the team default. */
export function teamSubIssueState(data: WorkflowData, teamId: string, states = teamWorkflowStates(data, teamId)): WorkflowState | undefined {
  return states.find(state => state.type === 'unstarted') ?? teamDefaultState(data, teamId, states)
}

/** The team's status of a type: the configured default when it has that type, else the first non-reserved one. */
export function teamWorkflowStateOfType(data: WorkflowData, teamId: string, type: WorkflowStateType): WorkflowState | undefined {
  const states = teamWorkflowStates(data, teamId).filter(state => state.type === type)
  const configured = data.teamSettings?.[teamId]?.defaultStateId
  return states.find(state => state.id === configured) ?? states.find(state => !state.reserved) ?? states[0]
}

/** Whether the status belongs to the team's effective workflow. */
export function teamHasWorkflowState(data: WorkflowData, teamId: string, stateId: string | undefined): boolean {
  return Boolean(stateId && teamId && teamWorkflowStates(data, teamId).some(state => state.id === stateId))
}

/**
 * The status a new issue in the team starts in: the first candidate the team's workflow
 * has, else the team default — never another team's status. Before any statuses have
 * loaded the first candidate is kept, so it can be checked once they arrive.
 */
export function teamStateIdFor(data: WorkflowData, teamId: string, ...candidates: (string | undefined)[]): string {
  const states = teamWorkflowStates(data, teamId)
  if (!states.length) return (data.states.length ? undefined : candidates.find(Boolean)) ?? ''
  return candidates.find(id => id && states.some(state => state.id === id)) ?? teamDefaultState(data, teamId, states)?.id ?? ''
}

/** The first of the teams whose workflow has the status, so a status preset picks a team it is valid for. */
export function teamForWorkflowState(data: WorkflowData, teamIds: readonly string[], stateId: string | undefined): string | undefined {
  return stateId ? teamIds.find(teamId => teamHasWorkflowState(data, teamId, stateId)) : undefined
}

/**
 * The team's status for a status of another workflow: the same status when the team has it, else one of the
 * same type and name, else the first of that type. Mirrors the API's team move, without its default fallback.
 */
export function teamEquivalentState(data: WorkflowData, teamId: string, state: Pick<WorkflowState, 'id' | 'name' | 'type'>): WorkflowState | undefined {
  const states = teamWorkflowStates(data, teamId)
  const name = state.name.trim().toLowerCase()
  const ofType = states.filter(item => item.type === state.type)
  return states.find(item => item.id === state.id)
    ?? ofType.find(item => !item.reserved && item.name.trim().toLowerCase() === name)
    ?? ofType.find(item => !item.reserved)
    ?? ofType[0]
}

/**
 * The statuses a change across issues of several teams can offer: the ones every team's workflow has, in the
 * first team's order, else the first team's workflow (each team then gets its equivalent status).
 */
export function statesForTeams(data: WorkflowData, teamIds: readonly (string | undefined)[]): WorkflowState[] {
  const unique = [...new Set(teamIds.filter((id): id is string => Boolean(id)))]
  if (!unique.length) return []
  const [first, ...rest] = unique.map(teamId => teamWorkflowStates(data, teamId))
  const shared = first.filter(state => rest.every(states => states.some(item => item.id === state.id)))
  return shared.length ? shared : first
}

/**
 * Keeps the status menu options the teams can use (see statesForTeams). Options for statuses the data does not
 * know (the triage pseudo status, fixtures) are kept, and nothing is filtered before the statuses load.
 */
export function workflowStatusOptions<T extends { id: string }>(data: WorkflowData | undefined, teamIds: readonly (string | undefined)[], options: T[]): T[] {
  if (!data?.states.length || !teamIds.some(Boolean)) return options
  const valid = new Set(statesForTeams(data, teamIds).map(state => state.id))
  const known = new Set(data.states.map(state => state.id))
  return options.filter(option => !known.has(option.id) || valid.has(option.id))
}

/**
 * Splits one status change across issues of several teams: each team gets the status or its equivalent, so the
 * API never receives another team's status. Issues of a team without an equivalent are left out; issues without
 * a known team, or a status the data does not know, keep the status as given.
 */
export function teamStateChanges(data: WorkflowData, issues: readonly { id: string; teamId?: string }[], stateId: string): { stateId: string; ids: string[] }[] {
  const target = data.states.find(state => state.id === stateId)
  const changes = new Map<string, string[]>()
  for (const issue of issues) {
    const next = target && issue.teamId ? teamEquivalentState(data, issue.teamId, target)?.id : stateId
    if (next) changes.set(next, [...(changes.get(next) ?? []), issue.id])
  }
  return [...changes].map(([id, ids]) => ({ stateId: id, ids }))
}
