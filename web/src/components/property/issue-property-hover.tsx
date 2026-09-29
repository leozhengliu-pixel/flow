import { Building2, Layers3 } from 'lucide-react'
import type { ActivityEvent, ProjectSummary, User, WorkspaceMember, WorkflowState } from '@/types/flow'
import { Avatar } from '@/components/issue/issue-row'
import { StatusIcon, WorkflowStatusGlyph } from '@/components/issue/issue-icons'
import { TRIAGE_STATUS } from '@/components/triage/triage-model'
import { formatStatusDuration, timeInStatus } from './time-in-status'
import { PersonIdentityDetails } from './person-info'
import { personDisplayName } from '@/lib/people'
import { useI18n } from '@/i18n/i18n'

/** Linear's "Time in status" card: every status the issue has been in with the time spent there, current one bright. */
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

export function AssigneeHoverPreview({ user, member, online, workspaceName, project }: { user: User; member?: WorkspaceMember; online?: boolean; workspaceName: string; project?: ProjectSummary }) {
  const { t } = useI18n()
  const name = personDisplayName(user) || t('Unknown user')
  const isOnline = online && user.active && member?.status !== 'suspended'
  return <div className="assignee-hover-preview">
    <header><Avatar name={name}/><div><strong data-i18n-ignore>{name}</strong>{user.name && user.name !== user.id && <span data-i18n-ignore>{user.name}</span>}</div></header>
    <PersonIdentityDetails person={user}/>
    <div className="assignee-hover-preview__details">
      {member?.status === 'suspended' ? <span>Suspended</span> : !user.active ? <span>Inactive</span> : online !== undefined ? <span><i className={isOnline ? undefined : 'offline'}/>{isOnline ? 'Online' : 'Offline'}</span> : null}
      <span><Building2/>{workspaceName}</span>
      {project && <span><Layers3/>{project.name}</span>}
    </div>
  </div>
}

export function PropertyShortcutTooltip({ label, shortcut }: { label: string; shortcut: string }) {
  return <div className="property-shortcut-tooltip"><span>{label}</span><kbd>{shortcut}</kbd></div>
}

