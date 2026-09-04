import { Building2, Clock3, Layers3 } from 'lucide-react'
import type { ActivityEvent, ProjectSummary, User, WorkspaceMember, WorkflowState } from '@/types/flow'
import { Avatar } from '@/components/issue/issue-row'
import { StatusIcon } from '@/components/issue/issue-icons'

export function StatusHoverPreview({ state, activities, issueCreatedAt }: { state: WorkflowState; activities: ActivityEvent[]; issueCreatedAt: string }) {
  const changes = activities
    .filter(activity => activity.metadata.state)
    .sort((left, right) => new Date(left.createdAt).getTime() - new Date(right.createdAt).getTime())
  const currentChange = [...changes].reverse().find(activity => activity.metadata.state === state.name)
  const previous = [...changes].reverse().find(activity => activity.metadata.stateBefore && activity.metadata.stateBefore !== state.name)
  return <div className="status-hover-preview">
    <strong>Time in status</strong>
    {previous&&<div><StatusIcon state={{...state,name:previous.metadata.stateBefore,color:'var(--status-neutral)',type:'unstarted'}}/><span>{previous.metadata.stateBefore}</span><time>{duration(issueCreatedAt,previous.createdAt)}</time></div>}
    <div><StatusIcon state={state}/><span>{state.name}</span><time>{duration(currentChange?.createdAt ?? issueCreatedAt)}</time></div>
    <footer><span>Change status</span><kbd>S</kbd></footer>
  </div>
}

export function AssigneeHoverPreview({ user, member, online = false, workspaceName, project }: { user: User; member?: WorkspaceMember; online?: boolean; workspaceName: string; project?: ProjectSummary }) {
  const isOnline = online && user.active && member?.status !== 'suspended'
  return <div className="assignee-hover-preview">
    <header><Avatar name={user.displayName}/><div><strong>{user.displayName}</strong><span>{user.name}</span></div></header>
    <div className="assignee-hover-preview__details">
      <span><i className={isOnline ? undefined : 'offline'}/>{isOnline ? 'Online' : 'Offline'}</span>
      <span><Clock3/><time>{new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</time><small>local time</small></span>
      <span><Building2/>{workspaceName}</span>
      <span><Layers3/>{project?.name ?? 'No project'}</span>
    </div>
  </div>
}

export function PropertyShortcutTooltip({ label, shortcut }: { label: string; shortcut: string }) {
  return <div className="property-shortcut-tooltip"><span>{label}</span><kbd>{shortcut}</kbd></div>
}

function duration(from: string, to = new Date().toISOString()) {
  const milliseconds = Math.max(0, new Date(to).getTime() - new Date(from).getTime())
  const minutes = Math.floor(milliseconds / 60_000)
  if (minutes < 60) return `${Math.max(1, minutes)}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days}d`
  return `${Math.floor(days / 30)}mo`
}
