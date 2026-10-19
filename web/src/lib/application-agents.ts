import { ApiError, request } from './api-client'
import type { User } from '@/types/flow'
function apiRequest<T>(path: string, workspace: string, options: RequestInit = {}): Promise<T> {
  return request<T>(path, { ...options, headers: { 'Content-Type': 'application/json', 'X-Workspace-Key': workspace, ...options.headers } })
}

export type ApplicationInstallation = { id: string; workspaceKey: string; clientId: string; name: string; userId: string; installedBy: string; scopes: string[]; teamIds: string[]; allPublicTeams?: boolean; builtin: boolean; active: boolean; allTeams?: boolean; serverSuspended?: boolean; withoutRealtime?: boolean; webhookUrl?: string; avatarUrl?: string; createdAt: string; updatedAt: string }
/** A link an agent attaches to its session (a pull request, a run page). */
export type ApplicationExternalUrl = { url: string; label?: string }
/** One step of an agent session's plan (replaced as a whole by the agent). */
export type ApplicationPlanStep = { content: string; status: 'pending' | 'inProgress' | 'completed' | 'canceled' }
/**
 * An agent session. "stale" is derived by the server: a pending or active session whose agent has been silent for
 * 30 minutes. `unresponsive` is set when the agent did not acknowledge a delivered event within 10 seconds.
 */
export type ApplicationTask = { id: string; issueId: string; teamId: string; commentId?: string; appUserId: string; creatorId: string; status: 'pending' | 'active' | 'awaitingInput' | 'complete' | 'error' | 'canceled' | 'stale'; prompt: string; initialPrompt?: string; trigger: string; version: number; createdAt?: string; updatedAt: string; pendingTool?: {name:string;arguments:unknown;status:string}; externalUrls?: ApplicationExternalUrl[]; plan?: ApplicationPlanStep[]; unresponsive?: boolean; deliveredAt?: string; lastActivityAt?: string; /** Set when the task anchors a coding session (docs/coding-sessions-design.md §6). */ codingSessionId?: string }
/** Window event fired when the realtime stream reports an agent task change; detail is the task's resource id. */
export const AGENT_TASK_ACTIVITY_EVENT = 'flow:agent-task-activity'
/** Task states the agent may still change on its own; only these keep a fallback poll running. */
export const isApplicationTaskRunning = (task: Pick<ApplicationTask, 'status'>) => task.status === 'pending' || task.status === 'active'
export type ApplicationActivity = { id: string; sessionId: string; actorId: string; type: string; body: string; url?: string; createdAt: string; signal?: 'select' | 'auth' | 'stop'; signalMetadata?: { options?: { label?: string; value: string; /** The option the agent recommends (coding sessions: the clearly leading repository). */ recommended?: boolean }[]; /** The value preselected for the viewer (one of options). */ selected?: string; url?: string; userId?: string; providerName?: string; approval?: import('@/types/flow').AgentApproval }; metadata?: { action?: string; parameter?: string; result?: string; activityId?: string; enabled?: boolean } }

/** The select option an elicitation preselects: `signalMetadata.selected` when it names an option, else the first option marked recommended. */
export function recommendedChoiceValue(metadata: ApplicationActivity['signalMetadata']): string | undefined {
  const options = metadata?.options ?? []
  if (metadata?.selected && options.some(option => option.value === metadata.selected)) return metadata.selected
  return options.find(option => option.recommended)?.value
}
export const isAgentMember = (user: User) => Boolean(user.app && user.active && (user.appScopes?.includes('app:assignable') || user.appScopes?.includes('app:mentionable')))
export const canDelegateTo = (user: User, teamId: string) => Boolean(user.app && user.active && user.appScopes?.includes('app:assignable') && user.appTeamIds?.includes(teamId))
export const listApplications = (workspace: string) => apiRequest<ApplicationInstallation[]>('/api/application-installations', workspace)
export const saveApplication = (workspace: string, input: Partial<ApplicationInstallation>) => apiRequest<{application: ApplicationInstallation; webhookSecret: string}>(`/api/application-installations${input.id ? `/${encodeURIComponent(input.id)}` : ''}`, workspace, { method: input.id ? 'PATCH' : 'POST', body: JSON.stringify(input) })
/** Uninstalls an application from the workspace (admins): revokes its tokens, cancels its active sessions and removes its app user. */
export const uninstallApplication = (workspace: string, id: string) => apiRequest<void>(`/api/application-installations/${encodeURIComponent(id)}`, workspace, { method: 'DELETE' })
export const listApplicationTasks = (workspace: string, issueId: string, signal?: AbortSignal, resourceType = 'issue') => apiRequest<ApplicationTask[]>(`/api/agent-tasks?resourceType=${resourceType}&resourceId=${encodeURIComponent(issueId)}`, workspace, { signal })
export const getApplicationTask = (workspace: string, id: string, after = '', signal?: AbortSignal) => apiRequest<{session: ApplicationTask; activities: ApplicationActivity[]}>(`/api/agent-tasks/${encodeURIComponent(id)}?after=${encodeURIComponent(after)}`, workspace, { signal })
/** Removes one of the agent's replies from its thread (a "deleted" marker; the reply stays in the session log). */
export const deleteApplicationActivity = (workspace: string, task: ApplicationTask, activityId: string) => apiRequest<ApplicationTask>(`/api/agent-tasks/${encodeURIComponent(task.id)}/activities`, workspace, { method: 'POST', body: JSON.stringify({ expectedVersion: task.version, type: 'deleted', activityId }) })
export const reactApplicationTask = (workspace: string, task: ApplicationTask, activityId: string, emoji: string, enabled: boolean) => apiRequest<ApplicationTask>(`/api/agent-tasks/${encodeURIComponent(task.id)}/activities`, workspace, { method: 'POST', body: JSON.stringify({ expectedVersion: task.version, type: 'feedback', activityId, body: emoji, enabled }) })
export async function replyApplicationTask(workspace: string, task: ApplicationTask, type: 'prompt' | 'canceled' | 'retry', body: string, approve?: boolean): Promise<ApplicationTask> {
  const send = (version: number) => apiRequest<ApplicationTask>(`/api/agent-tasks/${encodeURIComponent(task.id)}/activities`, workspace, { method: 'POST', body: JSON.stringify({ expectedVersion: version, type, body, approve }) })
  try { return await send(task.version) }
  catch (error) {
    if (!(error instanceof ApiError) || error.status !== 409 || approve !== undefined || task.pendingTool || type === 'retry') throw error
    const next = await getApplicationTask(workspace, task.id, String(task.version).padStart(20, '0'))
    // Streaming may advance the version while the person presses Send. Never
    // retry across somebody else's prompt, approval, cancellation or result.
    if (!next.activities.length || next.activities.length === 100 || next.activities.some(activity => !['thought', 'output', 'action'].includes(activity.type)) || next.session.pendingTool) throw error
    return send(next.session.version)
  }
}
