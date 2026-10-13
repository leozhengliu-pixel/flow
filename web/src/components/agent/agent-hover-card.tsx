import * as Tooltip from '@radix-ui/react-tooltip'
import type { ReactElement } from 'react'
import { UserAvatar } from '@/components/ui/user-avatar'
import { avatarColor } from '@/components/issue/avatar-color'
import { AGENT_HOVER_CLASS, agentHandle, type AgentIdentity } from './agent-hover-class'
import './agent-hover-card.css'

/**
 * The reference app's hover card for an agent row in an assignee picker: 32px agent avatar, the name, then the
 * lowercase handle next to the "Agent" pill. The pill only lives here; picker rows carry none.
 */
export function AgentHoverCard({ agent }: { agent: AgentIdentity }) {
  const name = agent.displayName || agent.name
  return <div className="agent-hover-card" data-agent-hover-card="">
    <UserAvatar avatarUrl={agent.avatarUrl} className="agent-hover-card__avatar" color={avatarColor(agent.id)} name={name}/>
    <div className="agent-hover-card__copy">
      <strong className="agent-hover-card__name" data-i18n-ignore>{name}</strong>
      <span className="agent-hover-card__meta">
        <span className="agent-hover-card__handle" data-i18n-ignore>{agentHandle(agent)}</span>
        <span className="agent-hover-card__pill" data-agent-badge="" data-i18n-ignore>Agent</span>
      </span>
    </div>
  </div>
}

/** Wraps a menu row (Radix menus) so hovering it opens the agent hover card to its right. */
export function AgentHover({ agent, children }: { agent: AgentIdentity; children: ReactElement }) {
  return <Tooltip.Provider delayDuration={350} skipDelayDuration={150}><Tooltip.Root><Tooltip.Trigger asChild>{children}</Tooltip.Trigger><Tooltip.Portal><Tooltip.Content data-flow-motion="tooltip" className={AGENT_HOVER_CLASS} side="right" align="start" sideOffset={6} collisionPadding={8}><AgentHoverCard agent={agent}/></Tooltip.Content></Tooltip.Portal></Tooltip.Root></Tooltip.Provider>
}
