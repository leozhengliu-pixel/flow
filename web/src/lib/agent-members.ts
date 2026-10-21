import type { Issue, User } from '@/types/flow'

/** The "Invite and assign…" action row of an assignee picker (the reference app lists it under a "New user" label). */
export const ASSIGNEE_INVITE_ID = '__invite-assignee__'

/** Number keys 1-9 pick rows of an assignee picker; "No assignee" is 0 and rows past 9 have no key. */
export function assigneeShortcut(position: number) {
  return position >= 0 && position <= 9 ? String(position) : undefined
}

/** Guests may be barred from agents by the workspace (`preventGuestAgents`); then no picker offers them. */
export function agentsAvailable(data: { viewerRole?: string; workspaceSettings?: { preventGuestAgents?: boolean } }) {
  return !(data.viewerRole === 'guest' && data.workspaceSettings?.preventGuestAgents)
}

/** An agent member that accepts delegation, optionally in a given team (server: `User.CanDelegateTo`). */
export function isAssignableAgent(user: Pick<User, 'app' | 'active' | 'appScopes' | 'appTeamIds'>, teamId?: string) {
  return Boolean(user.app && user.active && user.appScopes?.includes('app:assignable') && (!teamId || user.appTeamIds?.includes(teamId)))
}

/** Agent principals are `app_<hash>` users (server `InstallApplication`). */
export function isAgentUserId(id: string | undefined) {
  return Boolean(id?.startsWith('app_'))
}

/** Picked in a list row's agent submenu: take the delegated agent off the issue (the human assignee stays). */
export const NO_AGENT_ID = '__no-agent__'
/** Confirmed "Remove <agent> too?" after unassigning from a list row: clears the assignee and the delegate together. */
export const CLEAR_ASSIGNEE_AND_AGENT_ID = '__clear-assignee-and-agent__'

/** The update for picking someone as assignee: an agent is delegated to, a person assigned. */
export function assigneeUpdate(id: string): { assigneeId: string } | { delegateId: string } | { assigneeId: string; delegateId: string } {
  if (id === NO_AGENT_ID) return { delegateId: '' }
  if (id === CLEAR_ASSIGNEE_AND_AGENT_ID) return { assigneeId: '', delegateId: '' }
  return isAgentUserId(id) ? { delegateId: id } : { assigneeId: id }
}

/** People for an assignee picker: humans, then the agents that can take the issue's team. */
export function assigneeCandidates(users: User[], teamId?: string, { agents = true }: { agents?: boolean } = {}) {
  return [...users.filter(user => !user.app), ...(agents ? users.filter(user => isAssignableAgent(user, teamId)) : [])]
}

export type AgentSessionState = NonNullable<Issue['agentSessionState']>

/**
 * The reference app's Agent Session filter values mapped onto Flow session states (values are `state:<id>`):
 * Active (pending, working or awaiting input), Error, Dismissed (canceled) and Merged (a pull request
 * linked to the issue merged; the server reports it over the task status), plus Stale: an agent silent for
 * 30 minutes while pending or working (the agent interaction guidelines' "stale" session state).
 */
export const AGENT_SESSION_STATE_FILTERS: { id: string; label: string; glyph: 'agentSessionActive' | 'agentSessionError' | 'agentSessionDismissed' | 'agentSessionMerged'; states: AgentSessionState[] }[] = [
  { id: 'state:active', label: 'Active', glyph: 'agentSessionActive', states: ['pending', 'active', 'awaitingInput'] },
  { id: 'state:error', label: 'Error', glyph: 'agentSessionError', states: ['error'] },
  { id: 'state:canceled', label: 'Dismissed', glyph: 'agentSessionDismissed', states: ['canceled'] },
  { id: 'state:merged', label: 'Merged', glyph: 'agentSessionMerged', states: ['merged'] },
  { id: 'state:stale', label: 'Stale', glyph: 'agentSessionDismissed', states: ['stale'] },
]

/** Values saved by earlier Flow versions: "Awaiting input" is now part of Active; "Complete" has no the reference app equivalent. */
const LEGACY_AGENT_SESSION_VALUES: Record<string, string | undefined> = { 'state:awaitingInput': 'state:active', 'state:complete': undefined }

/** The offered filter value a stored value stands for (legacy values map onto the reference app's four). */
export function agentSessionFilterValue(value: string): string | undefined {
  return value in LEGACY_AGENT_SESSION_VALUES ? LEGACY_AGENT_SESSION_VALUES[value] : value
}

export function agentSessionStatesFor(value: string): AgentSessionState[] {
  return agentSessionStateOption(value)?.states ?? []
}

/** The the reference app Agent Session option a (possibly legacy) stored value shows as: label and glyph. */
export function agentSessionStateOption(value: string) {
  const current = agentSessionFilterValue(value)
  return AGENT_SESSION_STATE_FILTERS.find(option => option.id === current)
}
