import { AGENT_OPTION_GROUP } from '@/lib/agent-members'
import { AgentLabel } from './agent-badge'

/** Extra option props for an agent in a person list: "Agent" pill and its own "Agents" section. */
export function agentOptionProps(user: { app?: boolean; displayName?: string; name?: string }) {
  return user.app ? { labelContent: <AgentLabel label={user.displayName || user.name || ''}/>, ...AGENT_OPTION_GROUP } : {}
}
