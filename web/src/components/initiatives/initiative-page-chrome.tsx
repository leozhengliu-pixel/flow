/**
 * LS-0311 InitiativePageChrome — packs the details sidebar + UpdatesFloatingPanel into detail chrome. The page has no
 * docked agent: the floating chat (⌘J, with this initiative as context) is the agent.
 */
import type { ReactNode } from 'react'
import { Send } from 'lucide-react'
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
  updatesOpen: boolean
  onUpdatesOpenChange: (open: boolean) => void
  onOpenActivity: () => void
  onCreateUpdate: (id: string, input: { body: string; health?: Project['health'] }) => Promise<InitiativeUpdate>
  onUpdate: (input: InitiativeMutationInput) => Promise<Initiative>
  /** Main body (overview / activity / projects / …). */
  children: ReactNode
  /** Details sidebar, shown while `detailsOpen`. */
  detailsSidebar?: ReactNode
  className?: string
  tab: string
}

export function InitiativePageChrome({
  initiative,
  initiativeUpdates,
  viewer,
  detailsOpen,
  updatesOpen,
  onUpdatesOpenChange,
  onOpenActivity,
  onCreateUpdate,
  onUpdate,
  children,
  detailsSidebar,
  className,
  tab,
}: InitiativePageChromeProps) {
  return (
    <>
      <div
        className={`li-detail-body is-${tab}${detailsOpen ? ' has-details' : ''}${className ? ` ${className}` : ''}`}
        data-initiative-page-chrome
      >
        <section className="li-detail-main">{children}</section>
        {detailsOpen && detailsSidebar}
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

/** Toolbar control for the floating updates panel (LS-0311). */
export function InitiativePageChromeActions({
  updatesOpen,
  onToggleUpdates,
}: {
  updatesOpen: boolean
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
    </>
  )
}
