import type { ReactNode } from 'react'
import { MessageSquare } from 'lucide-react'
import { PriorityIcon, StatusIcon } from '@/components/issue/issue-icons'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import styles from './issues-split-view.module.css'

export interface IssueDetailsPaneSidebarProps {
  issue?: MyIssuesRowData
  /** LS-0252 EntityAgentPanel slot (Wave 3). Host renders panel content here when ready. */
  agentPanel?: ReactNode
  agentOpen?: boolean
  onAgentOpenChange?: (open: boolean) => void
  /**
   * @deprecated Linear's issue properties sidebar has no customers section (requests live in the
   * issue's Customers section), so these rows are no longer rendered.
   */
  customerNeeds?: Array<{ id: string; customerName: string; body?: string }>
  className?: string
}

/**
 * LS-0346 — IssueDetailsPaneSidebar.
 * Property sidebar for split/detail hosts with an AgentPanel slot.
 */
export function IssueDetailsPaneSidebar({
  issue,
  agentPanel,
  agentOpen = false,
  onAgentOpenChange,
  className,
}: IssueDetailsPaneSidebarProps) {
  const replaceWithAgent = Boolean(onAgentOpenChange && agentOpen && agentPanel)

  return (
    <div
      className={[styles.sidebar, className].filter(Boolean).join(' ')}
      data-issue-details-pane-sidebar=""
      aria-label="Issue properties"
    >
      {onAgentOpenChange ? (
        <div className={styles.agentToggleRow}>
          <button
            type="button"
            className={styles.agentToggle}
            aria-expanded={agentOpen}
            aria-label={agentOpen ? 'Close chat' : 'Open chat'}
            data-active={agentOpen || undefined}
            onClick={() => onAgentOpenChange(!agentOpen)}
          >
            <MessageSquare size={14} />
            <span>Chat</span>
          </button>
        </div>
      ) : null}

      {replaceWithAgent ? (
        <div className={styles.agentSlot} data-agent-panel-slot="">
          {agentPanel}
        </div>
      ) : (
        <>
          <h3 className={styles.sidebarTitle}>Properties</h3>
          {issue ? <PropertyList issue={issue} /> : <p className={styles.sidebarEmpty}>No issue selected</p>}
          {agentPanel ? (
            <div className={styles.agentSlot} data-agent-panel-slot="">
              {agentPanel}
            </div>
          ) : (
            <div className={styles.agentSlot} data-agent-panel-slot="ready" hidden aria-hidden="true" />
          )}
        </>
      )}
    </div>
  )
}

function PropertyList({ issue }: { issue: MyIssuesRowData }) {
  return (
    <dl className={styles.propertyList}>
      <div>
        <dt>Status</dt>
        <dd>
          <StatusIcon state={issue.state} size={14} />
          <span>{issue.state.name}</span>
        </dd>
      </div>
      <div>
        <dt>Priority</dt>
        <dd>
          <PriorityIcon priority={issue.priority} size={14} />
          <span>{['No priority', 'Urgent', 'High', 'Medium', 'Low'][issue.priority]}</span>
        </dd>
      </div>
      <div>
        <dt>Assignee</dt>
        <dd>{issue.assignee?.name ?? 'Unassigned'}</dd>
      </div>
      <div>
        <dt>Project</dt>
        <dd>{issue.project?.name ?? '—'}</dd>
      </div>
      {issue.cycleName ? (
        <div>
          <dt>Cycle</dt>
          <dd>{issue.cycleName}</dd>
        </div>
      ) : null}
      {issue.labels?.length ? (
        <div>
          <dt>Labels</dt>
          <dd className={styles.labelChips}>
            {issue.labels.map((label) => (
              <span key={label.id} className={styles.labelChip}>
                <i style={{ backgroundColor: label.color }} />
                {label.name}
              </span>
            ))}
          </dd>
        </div>
      ) : null}
      {issue.dueDate ? (
        <div>
          <dt>Due date</dt>
          <dd>{issue.dueDate}</dd>
        </div>
      ) : null}
      {issue.estimate != null ? (
        <div>
          <dt>Estimate</dt>
          <dd>{issue.estimate}</dd>
        </div>
      ) : null}
    </dl>
  )
}
