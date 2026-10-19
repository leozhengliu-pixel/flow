import { useState } from 'react'
import type { BootstrapData } from '@/types/flow'
import type { CodingSession } from '@/types/coding-sessions'
import { CodingSessionDialog, CodingSessionStatusRow } from './coding-session-card'
import { useCodingAvailability, useCodingSession, useCodingSessions } from './use-coding-sessions'
import './coding-sessions.css'

/** The agent task fields this needs. `codingSessionId` is set by the server when the task anchors a coding session. */
type AnchorTask = { id: string; issueId?: string; codingSessionId?: string }

/**
 * Mounted in each agent session thread (issue-agent-tasks.tsx): when the task anchors a coding session, the thread shows
 * the session's status row ("Thinking… 02:42", "Error …"), like the reference app's session card in issue activity;
 * the row opens the full session view.
 */
export function AgentTaskCodingSession({ task, data }: { task: AnchorTask; data: BootstrapData }) {
  const available = useCodingAvailability(data)
  if (!available) return null
  if (task.codingSessionId) return <LiveEmbeddedSession sessionId={task.codingSessionId} data={data}/>
  return task.issueId ? <IssueAnchoredSession task={task} issueId={task.issueId} data={data}/> : null
}

/** Without `codingSessionId` on the task, the issue's sessions are listed (one shared request) and matched by anchor. */
function IssueAnchoredSession({ task, issueId, data }: { task: AnchorTask; issueId: string; data: BootstrapData }) {
  const { sessions } = useCodingSessions({ issueId })
  const match = sessions.find(session => session.agentSessionId === task.id)
  return match ? <LiveEmbeddedSession sessionId={match.id} initial={match} data={data}/> : null
}

function LiveEmbeddedSession({ sessionId, initial, data }: { sessionId: string; initial?: CodingSession; data: BootstrapData }) {
  const { session } = useCodingSession(sessionId, initial)
  const [open, setOpen] = useState(false)
  if (!session) return null
  return <div className="coding-session-embedded" data-state={session.state}>
    <CodingSessionStatusRow session={session} data={data} onOpen={() => setOpen(true)}/>
    {open && <CodingSessionDialog sessionId={session.id} initial={session} data={data} open onOpenChange={setOpen}/>}
  </div>
}
