import { useLayoutEffect, useRef, useState } from 'react'
import { ChevronRight } from 'lucide-react'
import type { Issue, ProjectSummary, User, WorkspaceMember } from '@/types/flow'
import { AssigneePicker, DelegatePicker } from '@/components/issue/core-property-pickers'
import { NestingLine } from '@/components/ui/nesting-line'
import { useI18n } from '@/i18n/i18n'
import { memberProfileHandle, memberProfilePath } from '@/lib/app-routes'
import './issue-assignee-property.css'

const ACTIVE_SESSION_STATES = new Set<Issue['agentSessionState']>(['pending', 'active', 'awaitingInput'])

/**
 * The reference app's issue sidebar assignee block (`IssueDetailsPaneSidebar`): the human assignee row ("Assign" when
 * nobody is), and — when the issue is delegated — the agent's own row hung under it on a NestingLine
 * (32px tall with 4px above, 23px indent, stem 10px from x=14). The reference app keeps the human accountable: the
 * delegate never replaces the assignee. A delegate added while the sidebar is open grows in (32px over
 * 150ms) and slides right from the assignee's column; one present on mount just renders.
 */
export function IssueAssigneeProperty({ issue, users, teamId, workspaceKey, allowAgents, hoverContext, onUpdate }: {
  issue: Pick<Issue, 'assignee' | 'delegate' | 'agentSessionState'>
  users: User[]
  teamId: string
  workspaceKey: string
  allowAgents: boolean
  hoverContext: { member?: WorkspaceMember; online?: boolean; workspaceName: string; project?: ProjectSummary }
  onUpdate: (input: { assigneeId?: string; delegateId?: string; promptArchive?: boolean }) => void | Promise<void>
}) {
  const { t } = useI18n()
  const { assignee, delegate } = issue
  const entering = useEnteringDelegate(delegate?.id)
  return <div className="issue-assignee-group">
    <div className="issue-assignee-property">
      <AssigneePicker delegateRow value={assignee} delegate={delegate} delegateSessionActive={ACTIVE_SESSION_STATES.has(issue.agentSessionState)} allowAgents={allowAgents} users={users} teamId={teamId} hoverContext={hoverContext} onChange={assigneeId => onUpdate({ assigneeId })} onDelegate={delegateId => onUpdate({ delegateId })}/>
      {assignee && <a className="issue-assignee-profile" href={`/${workspaceKey}/members`} aria-label={t('Go to user')}><ChevronRight size={15}/></a>}
    </div>
    {delegate && <div className="issue-delegate-row" data-entering={entering || undefined}>
      <NestingLine className="issue-delegate-nesting-line" heightAbove={10}/>
      <div className="issue-assignee-property issue-delegate-property">
        <DelegatePicker delegate={delegate} allowAgents={allowAgents} users={users} teamId={teamId} onDelegate={delegateId => onUpdate(delegateId === '' ? { delegateId, promptArchive: true } : { delegateId })}/>
        <a className="issue-assignee-profile" href={memberProfilePath(workspaceKey, memberProfileHandle(delegate))} aria-label={t('Go to agent')}><ChevronRight size={15}/></a>
      </div>
    </div>}
  </div>
}

/** True while a delegate that arrived after mount plays the reference app's enter motion (cleared after it finishes). */
function useEnteringDelegate(delegateId: string | undefined) {
  const previous = useRef(delegateId)
  const [entering, setEntering] = useState(false)
  useLayoutEffect(() => {
    const arrived = Boolean(delegateId && delegateId !== previous.current && !previous.current)
    previous.current = delegateId
    if (!arrived) return
    setEntering(true)
    const timer = window.setTimeout(() => setEntering(false), 400)
    return () => window.clearTimeout(timer)
  }, [delegateId])
  return entering
}
