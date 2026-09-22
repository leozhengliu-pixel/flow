import { useState, type ReactNode } from 'react'
import { MessageSquare, Sparkles, X } from 'lucide-react'
import './entity-activity-panel.css'

export type EntityActivityPanelEntityType = 'issue' | 'project' | 'initiative' | 'document'
export type EntityActivityPanelMode = 'activity' | 'agent'

export type EntityActivityPanelProps = {
  entityType: EntityActivityPanelEntityType
  entityId: string
  entityTitle: string
  open?: boolean
  onClose?: () => void
  mode?: EntityActivityPanelMode
  onModeChange?: (mode: EntityActivityPanelMode) => void
  /** LS-0252 EntityAgentPanel slot — host supplies agent chrome when available. */
  agentSlot?: ReactNode
  allowAgent?: boolean
  selectedItemId?: string
  emptyLabel?: string
  children?: ReactNode
  className?: string
}

/**
 * LS-0250 EntityActivityPanel — shared Activity / Agent shell for
 * issue · project · initiative · document.
 */
export function EntityActivityPanel({
  entityType,
  entityId,
  entityTitle,
  open = true,
  onClose,
  mode: modeProp,
  onModeChange,
  agentSlot,
  allowAgent = true,
  selectedItemId,
  emptyLabel = 'No activity yet',
  children,
  className,
}: EntityActivityPanelProps) {
  const [modeState, setModeState] = useState<EntityActivityPanelMode>('activity')
  const mode = modeProp ?? modeState
  const setMode = (next: EntityActivityPanelMode) => {
    onModeChange?.(next)
    if (modeProp === undefined) setModeState(next)
  }

  if (!open) return null

  const activityTitle = entityTitle.trim() || entityType
  const showAgent = allowAgent && mode === 'agent'

  return (
    <aside
      aria-label={showAgent ? 'Agent chat' : 'Activity'}
      className={['entity-activity-panel', className].filter(Boolean).join(' ')}
      data-entity-activity-panel=""
      data-entity-id={entityId}
      data-entity-type={entityType}
      data-mode={mode}
      data-selected={selectedItemId || undefined}
    >
      <header className="entity-activity-panel__header">
        {showAgent ? (
          <button
            className="entity-activity-panel__back"
            onClick={() => setMode('activity')}
            type="button"
          >
            Back to {activityTitle}
          </button>
        ) : (
          <strong>Activity</strong>
        )}
        <div className="entity-activity-panel__actions">
          {allowAgent && !showAgent && (
            <button
              aria-label="Agent chat"
              className="entity-activity-panel__agent-toggle"
              onClick={() => setMode('agent')}
              type="button"
            >
              <Sparkles size={14} />
              <span>Agent</span>
            </button>
          )}
          {onClose && (
            <button
              aria-label="Close Activity"
              className="entity-activity-panel__close"
              onClick={onClose}
              type="button"
            >
              <X size={14} />
            </button>
          )}
        </div>
      </header>

      {showAgent ? (
        <div className="entity-activity-panel__agent" data-agent-panel-slot="">
          {agentSlot ?? (
            <div className="entity-activity-panel__agent-empty">
              <MessageSquare size={16} />
              <p>Agent chat is unavailable for this entity.</p>
            </div>
          )}
        </div>
      ) : (
        <div className="entity-activity-panel__body" data-activity-panel-body="">
          {selectedItemId && (
            <div className="entity-activity-panel__selection" role="status">
              Selected activity item
            </div>
          )}
          {children ? children : (
            <div className="entity-activity-panel__empty">
              <p>{emptyLabel}</p>
            </div>
          )}
        </div>
      )}
    </aside>
  )
}
