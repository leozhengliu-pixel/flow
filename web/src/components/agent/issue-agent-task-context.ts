import { createContext, useContext } from 'react'
import type { ApplicationTask } from '@/lib/application-agents'

export type Directory = { key: string; tasks: ApplicationTask[]; error: string; refresh: () => void; onUpload?: (file: File) => Promise<string> }
export const TaskDirectory = createContext<Directory | null>(null)
export const CommentThreads = createContext<ReadonlySet<string>>(new Set())
export function useAgentCommentThreadIds() { return useContext(CommentThreads) }
/** Agent sessions that do not hang off a comment (delegations, agent-opened sessions), oldest first. */
export type StandaloneAgentSession = { id: string; createdAt: string }
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
