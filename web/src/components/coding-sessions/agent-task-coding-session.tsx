import type { BootstrapData } from '@/types/flow'
import type { CodingSession } from '@/types/coding-sessions'
import { CodingSessionPanel, CodingSessionView } from './coding-session-panel'
import { useCodingAvailability, useCodingSession, useCodingSessions } from './use-coding-sessions'

/** The agent task fields this needs. `codingSessionId` is set by the server when the task anchors a coding session. */
type AnchorTask = { id: string; issueId?: string; codingSessionId?: string }

/**
 * Mounted in each agent session thread (issue-agent-tasks.tsx): when the task anchors a coding session, its session
 * view renders inside the thread (the thread keeps rendering replies, plan and reply box; this adds state, sandbox,
 * PRs, steps and actions).
 */
export function AgentTaskCodingSession({ task, data }: { task: AnchorTask; data: BootstrapData }) {
  const available = useCodingAvailability(data)
  if (!available) return null
  if (task.codingSessionId) return <CodingSessionView sessionId={task.codingSessionId} data={data} activity="steps"/>
  return task.issueId ? <IssueAnchoredSession task={task} issueId={task.issueId} data={data}/> : null
}

/** Without `codingSessionId` on the task, the issue's sessions are listed (one shared request) and matched by anchor. */
function IssueAnchoredSession({ task, issueId, data }: { task: AnchorTask; issueId: string; data: BootstrapData }) {
  const { sessions } = useCodingSessions({ issueId })
  const match = sessions.find(session => session.agentSessionId === task.id)
  return match ? <LiveEmbeddedSession initial={match} data={data}/> : null
}

function LiveEmbeddedSession({ initial, data }: { initial: CodingSession; data: BootstrapData }) {
  const { session, apply } = useCodingSession(initial.id, initial)
  return session ? <CodingSessionPanel session={session} data={data} onChange={apply} activity="steps"/> : null
}
