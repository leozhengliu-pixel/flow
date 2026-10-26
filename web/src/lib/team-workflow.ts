import type { BootstrapData, WorkflowState, WorkflowStateType } from '@/types/flow'

type WorkflowData = Pick<BootstrapData, 'states' | 'teamSettings'>

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
