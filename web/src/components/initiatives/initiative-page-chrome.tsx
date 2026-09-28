/**
 * LS-0311 InitiativePageChrome — packs AgentSidebar + UpdatesFloatingPanel into detail chrome.
 */
import type { ReactNode } from 'react'
import { Bot, Send } from 'lucide-react'
import { InitiativeAgentSidebarContainer } from './initiative-agent-sidebar-container'
import { UpdatesFloatingPanel } from './updates-floating-panel'
import type {
  Initiative,
  InitiativeMutationInput,
  InitiativeUpdate,
  Project,
  User,
} from '@/types/flow'

export type InitiativePageChromeProps = {
  initiative: Initiative
  initiativeUpdates: InitiativeUpdate[]
  viewer: User
  detailsOpen: boolean
  agentOpen: boolean
  onAgentOpenChange: (open: boolean) => void
  updatesOpen: boolean
  onUpdatesOpenChange: (open: boolean) => void
  onOpenActivity: () => void
  onCreateUpdate: (id: string, input: { body: string; health?: Project['health'] }) => Promise<InitiativeUpdate>
  onUpdate: (input: InitiativeMutationInput) => Promise<Initiative>
  /** Main body (overview / activity / projects / …). */
  children: ReactNode
  /** Details sidebar when agent is closed. */
  detailsSidebar?: ReactNode
  /** Optional Agent panel body slot (Wave 3 EntityAgentPanel). */
  agentChildren?: ReactNode
  className?: string
  tab: string
}

export function InitiativePageChrome({
  initiative,
  initiativeUpdates,
  viewer,
  detailsOpen,
  agentOpen,
  onAgentOpenChange,
  updatesOpen,
  onUpdatesOpenChange,
  onOpenActivity,
  onCreateUpdate,
  onUpdate,
  children,
  detailsSidebar,
  agentChildren,
  className,
  tab,
}: InitiativePageChromeProps) {
  const railOpen = detailsOpen || agentOpen
  return (
    <>
      <div
        className={`li-detail-body is-${tab}${railOpen ? ' has-details' : ''}${agentOpen ? ' has-agent' : ''}${className ? ` ${className}` : ''}`}
        data-initiative-page-chrome
      >
        <section className="li-detail-main">{children}</section>
        {agentOpen ? (
          <InitiativeAgentSidebarContainer
            initiative={initiative}
            open={agentOpen}
            onOpenChange={onAgentOpenChange}
          >
            {agentChildren}
          </InitiativeAgentSidebarContainer>
        ) : (
          detailsOpen && detailsSidebar
        )}
      </div>
      <UpdatesFloatingPanel
        initiative={initiative}
        updates={initiativeUpdates}
        viewer={viewer}
        open={updatesOpen}
        onClose={() => onUpdatesOpenChange(false)}
        onOpenActivity={() => {
          onUpdatesOpenChange(false)
          onOpenActivity()
        }}
        onCreateUpdate={onCreateUpdate}
        onUpdate={onUpdate}
      />
    </>
  )
}

/** Toolbar controls for Agent sidebar + floating updates (LS-0311). */
export function InitiativePageChromeActions({
  agentOpen,
  updatesOpen,
  onToggleAgent,
  onToggleUpdates,
}: {
  agentOpen: boolean
  updatesOpen: boolean
  onToggleAgent: () => void
  onToggleUpdates: () => void
}) {
  return (
    <>
      <button
        aria-expanded={updatesOpen}
        aria-label={updatesOpen ? 'Close initiative updates' : 'Open initiative updates'}
        className={updatesOpen ? 'is-active' : undefined}
        onClick={onToggleUpdates}
        title="Initiative updates"
        type="button"
      >
        <Send size={14} />
      </button>
      <button
        aria-expanded={agentOpen}
        aria-label={agentOpen ? 'Close agent sidebar' : 'Open agent sidebar'}
        className={agentOpen ? 'is-agent-active is-active' : undefined}
        onClick={onToggleAgent}
        title="Agent"
        type="button"
      >
        <Bot size={14} />
      </button>
    </>
  )
}
