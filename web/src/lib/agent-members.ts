import type { Issue, User } from '@/types/flow'

/** Agents get their own section in assignee pickers (Linear lists them after people with an "Agent" badge). */
export const AGENT_OPTION_GROUP = { groupId: 'agents', groupLabel: 'Agents' } as const

/** An agent member that accepts delegation, optionally in a given team (server: `User.CanDelegateTo`). */
export function isAssignableAgent(user: Pick<User, 'app' | 'active' | 'appScopes' | 'appTeamIds'>, teamId?: string) {
  return Boolean(user.app && user.active && user.appScopes?.includes('app:assignable') && (!teamId || user.appTeamIds?.includes(teamId)))
}

/** Agent principals are `app_<hash>` users (server `InstallApplication`). */
export function isAgentUserId(id: string | undefined) {
  return Boolean(id?.startsWith('app_'))
}

/** The update for picking someone as assignee: an agent is delegated to (Linear), a person assigned. */
export function assigneeUpdate(id: string): { assigneeId: string } | { delegateId: string } {
  return isAgentUserId(id) ? { delegateId: id } : { assigneeId: id }
}

/** People for an assignee picker: humans, then the agents that can take the issue's team. */
export function assigneeCandidates(users: User[], teamId?: string) {
  return [...users.filter(user => !user.app), ...users.filter(user => isAssignableAgent(user, teamId))]
}

export type AgentSessionState = NonNullable<Issue['agentSessionState']>

/**
 * Linear's Agent Session filter values mapped onto Flow session states (values are `state:<id>`):
 * Active (pending, working or awaiting input), Error, Dismissed (canceled) and Merged (a pull request
 * linked to the issue merged; the server reports it over the task status).
 */
export const AGENT_SESSION_STATE_FILTERS: { id: string; label: string; glyph: 'agentSessionActive' | 'agentSessionError' | 'agentSessionDismissed' | 'agentSessionMerged'; states: AgentSessionState[] }[] = [
  { id: 'state:active', label: 'Active', glyph: 'agentSessionActive', states: ['pending', 'active', 'awaitingInput'] },
  { id: 'state:error', label: 'Error', glyph: 'agentSessionError', states: ['error'] },
  { id: 'state:canceled', label: 'Dismissed', glyph: 'agentSessionDismissed', states: ['canceled'] },
  { id: 'state:merged', label: 'Merged', glyph: 'agentSessionMerged', states: ['merged'] },
]

/** Values saved by earlier Flow versions: "Awaiting input" is now part of Active; "Complete" has no Linear equivalent. */
const LEGACY_AGENT_SESSION_VALUES: Record<string, string | undefined> = { 'state:awaitingInput': 'state:active', 'state:complete': undefined }

/** The offered filter value a stored value stands for (legacy values map onto Linear's four). */
export function agentSessionFilterValue(value: string): string | undefined {
  return value in LEGACY_AGENT_SESSION_VALUES ? LEGACY_AGENT_SESSION_VALUES[value] : value
}

export function agentSessionStatesFor(value: string): AgentSessionState[] {
  return agentSessionStateOption(value)?.states ?? []
}

/** The Linear Agent Session option a (possibly legacy) stored value shows as: label and glyph. */
export function agentSessionStateOption(value: string) {
  const current = agentSessionFilterValue(value)
  return AGENT_SESSION_STATE_FILTERS.find(option => option.id === current)
}
