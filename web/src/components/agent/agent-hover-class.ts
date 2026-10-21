import type { User } from '@/types/flow'

/** Class for the hover surface an agent row opens in assignee pickers (`PropertyMenu` option `hoverClassName`). */
export const AGENT_HOVER_CLASS = 'agent-hover-surface'

export type AgentIdentity = Pick<User, 'id' | 'name' | 'displayName'> & Partial<Pick<User, 'avatarUrl' | 'username'>>

/** The agent's `@handle`: its lowercase username (e.g. "flow"). */
export function agentHandle(agent: Pick<User, 'name' | 'displayName'> & Partial<Pick<User, 'username'>>) {
  if (agent.username) return agent.username.toLowerCase()
  return (agent.name || agent.displayName || '').trim().toLowerCase().replace(/\s+/g, '-')
}
