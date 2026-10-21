import { createContext, useContext, useEffect, useState } from 'react'
import { getApplicationTask, type ApplicationActivity, type ApplicationTask } from '@/lib/application-agents'
import { agentThreadMessageCount } from './agent-thread-items'

export type Directory = { key: string; workspace?: string; tasks: ApplicationTask[]; error: string; refresh: () => void; onUpload?: (file: File) => Promise<string> }
export const TaskDirectory = createContext<Directory | null>(null)
export const CommentThreads = createContext<ReadonlySet<string>>(new Set())
export function useAgentCommentThreadIds() { return useContext(CommentThreads) }
/** Agent sessions that do not hang off a comment (delegations, agent-opened sessions), oldest first. */
export type StandaloneAgentSession = { id: string; createdAt: string; /** The agent working the session. */ agentId?: string }
export const StandaloneSessions = createContext<readonly StandaloneAgentSession[]>([])
/** The issue's standalone agent sessions, so the activity timeline can place them at their chronological position. */
export function useStandaloneAgentSessions() { return useContext(StandaloneSessions) }

/** A comment thread's own controls, offered again in the "…" menu of the agent's replies inside it. */
export type AgentThreadControls = {
  following: boolean
  onToggleSubscription?: () => void
  resolved: boolean
  onResolve?: () => void
  /** The issue the thread is on, for "New sub-issue from comment…". */
  parentIssueId?: string
}

/** Whether any comment of a thread started an agent session (its replies then go to the session). */
export function hasAgentThread(ids: ReadonlySet<string>, threadIds: readonly string[]) {
  return threadIds.some(id => ids.has(id))
}

/**
 * The messages the agent sessions of a thread add to it (the agent's replies and the replies to it), for the thread's
 * "N comments" count while it is collapsed. Reads the page's task directory; 0 without one.
 */
export function useAgentThreadMessageCount(threadIds: readonly string[]) {
  const directory = useContext(TaskDirectory)
  const workspace = directory?.workspace ?? ''
  const signature = (directory?.tasks ?? []).filter(task => task.commentId && threadIds.includes(task.commentId)).map(task => `${task.id}|${task.version}|${task.status}`).join(',')
  const [counts, setCounts] = useState<{ signature: string; values: Record<string, number> }>({ signature: '', values: {} })
  useEffect(() => {
    if (!workspace || !signature) return
    const abort = new AbortController()
    const load = async (id: string, status: string) => {
      const activities: ApplicationActivity[] = []
      let after = ''
      let result
      do {
        result = await getApplicationTask(workspace, id, after, abort.signal)
        activities.push(...result.activities)
        if (result.activities.length) after = result.activities.at(-1)!.id
      } while (result.activities.length === 100)
      return [id, agentThreadMessageCount(activities, status)] as const
    }
    void Promise.all(signature.split(',').map(entry => { const [id, , status] = entry.split('|'); return load(id, status) }))
      .then(entries => { if (!abort.signal.aborted) setCounts({ signature, values: Object.fromEntries(entries) }) })
      .catch(() => undefined)
    return () => abort.abort()
  }, [workspace, signature])
  if (!signature) return 0
  return signature.split(',').reduce((sum, entry) => sum + (counts.values[entry.split('|')[0]] ?? 0), 0)
}
