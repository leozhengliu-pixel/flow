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
 * Linear's agent session filter states mapped onto Flow task statuses: Active (pending or working),
 * Awaiting input, Error, Complete and Dismissed (canceled). Values are `state:<id>`.
 */
export const AGENT_SESSION_STATE_FILTERS: { id: string; label: string; states: AgentSessionState[] }[] = [
  { id: 'state:active', label: 'Active', states: ['pending', 'active'] },
  { id: 'state:awaitingInput', label: 'Awaiting input', states: ['awaitingInput'] },
  { id: 'state:error', label: 'Error', states: ['error'] },
  { id: 'state:complete', label: 'Complete', states: ['complete'] },
  { id: 'state:canceled', label: 'Dismissed', states: ['canceled'] },
]

export function agentSessionStatesFor(value: string): AgentSessionState[] {
  return AGENT_SESSION_STATE_FILTERS.find(option => option.id === value)?.states ?? []
}
