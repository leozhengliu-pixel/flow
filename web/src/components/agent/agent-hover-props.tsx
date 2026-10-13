import { AgentHoverCard } from './agent-hover-card'
import { AGENT_HOVER_CLASS, type AgentIdentity } from './agent-hover-class'

/** Option props that give an agent row of a picker its hover card. */
export function agentHoverProps(agent: AgentIdentity) {
  return { hoverContent: <AgentHoverCard agent={agent}/>, hoverClassName: AGENT_HOVER_CLASS }
}
