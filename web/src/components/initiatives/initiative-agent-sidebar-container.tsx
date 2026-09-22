/**
 * LS-0299 InitiativeAgentSidebarContainer — initiative-bound Agent sidebar/panel.
 * Provides width + open state and docks EntityAgentPanel (slot if Wave 3 not merged).
 */
import type { ReactNode } from 'react'
import { EntityAgentPanel, type AgentSidebarTarget, type EntityAgentPanelProps } from '@/components/agent/entity-agent-panel'
import { usePageAgentSidebarOpen } from '@/components/agent/use-page-agent-sidebar-open'
import type { Initiative } from '@/types/flow'
import './initiative-agent-sidebar.css'

export type InitiativeAgentSidebarContainerProps = {
  initiative: Initiative
  open?: boolean
  onOpenChange?: (open: boolean) => void
  agentSidebarTarget?: AgentSidebarTarget
  onOpenFullPage?: EntityAgentPanelProps['onOpenFullPage']
  /** Slot for a full EntityAgentPanel body (Wave 3). */
  children?: ReactNode
  emptyLabel?: string
  className?: string
}

export function InitiativeAgentSidebarContainer({
  initiative,
  open: openProp,
  onOpenChange,
  agentSidebarTarget,
  onOpenFullPage,
  children,
  emptyLabel,
  className,
}: InitiativeAgentSidebarContainerProps) {
  const [storedOpen, setStoredOpen] = usePageAgentSidebarOpen(`initiative:${initiative.id}`, false)
  const open = openProp ?? storedOpen
  const setOpen = (next: boolean) => {
    setStoredOpen(next)
    onOpenChange?.(next)
  }
  if (!open) return null
  return (
    <aside
      aria-label="Initiative agent sidebar"
      className={`li-detail-sidebar li-agent-sidebar li-initiative-agent-sidebar${className ? ` ${className}` : ''}`}
      data-initiative-agent-sidebar
      data-open={open || undefined}
    >
      <EntityAgentPanel
        agentSidebarTarget={agentSidebarTarget}
        emptyLabel={emptyLabel}
        onOpenFullPage={onOpenFullPage}
        onRequestClose={() => setOpen(false)}
        open={open}
        target={{ type: 'initiative', id: initiative.id, title: initiative.name }}
      />
      {children}
    </aside>
  )
}

export { usePageAgentSidebarOpen }
