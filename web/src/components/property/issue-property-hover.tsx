import type { ActivityEvent, WorkflowState } from '@/types/flow'
import { StatusIcon, WorkflowStatusGlyph } from '@/components/issue/issue-icons'
import { TRIAGE_STATUS } from '@/components/triage/triage-model'
import { formatStatusDuration, timeInStatus } from './time-in-status'
export { AssigneeHoverPreview } from './assignee-hover-card'

/** The reference app's "Time in status" card: every status the issue has been in with the time spent there, current one bright. */
export function StatusHoverPreview({ state, activities, issueCreatedAt, states, triagedAt, inTriage = false, loading = false }: { state: Pick<WorkflowState, 'id' | 'name' | 'type' | 'color'>; activities: ActivityEvent[]; issueCreatedAt: string; states?: WorkflowState[]; triagedAt?: string | null; inTriage?: boolean; loading?: boolean }) {
  const stints = timeInStatus({ activities, createdAt: issueCreatedAt, current: state, states, triagedAt, inTriage })
  return <div className="status-hover-preview">
    <strong>Time in status</strong>
    {stints.map(stint => <div key={stint.key} data-current={stint.current || undefined}>
      {stint.type === 'triage' ? <WorkflowStatusGlyph state={TRIAGE_STATUS}/> : <StatusIcon state={{ id: stint.id ?? stint.key, name: stint.name, type: stint.type as WorkflowState['type'], color: stint.color ?? (stint.current ? state.color : 'var(--status-neutral)') }} size={14}/>}
      <span data-i18n-ignore>{stint.name}</span>
      <time>{loading && !stint.current ? '' : formatStatusDuration(stint.ms)}</time>
    </div>)}
    <footer><span>Change status</span><kbd>S</kbd></footer>
  </div>
}

export function PropertyShortcutTooltip({ label, shortcut }: { label: string; shortcut: string }) {
  return <div className="property-shortcut-tooltip"><span>{label}</span><kbd>{shortcut}</kbd></div>
}

