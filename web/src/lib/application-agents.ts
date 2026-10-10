import { request } from './api-client'
import type { User } from '@/types/flow'
function apiRequest<T>(path: string, workspace: string, options: RequestInit = {}): Promise<T> {
  return request<T>(path, { ...options, headers: { 'Content-Type': 'application/json', 'X-Workspace-Key': workspace, ...options.headers } })
}

export type ApplicationInstallation = { id: string; workspaceKey: string; clientId: string; name: string; userId: string; installedBy: string; scopes: string[]; teamIds: string[]; builtin: boolean; active: boolean; allTeams?: boolean; serverSuspended?: boolean; webhookUrl?: string; avatarUrl?: string; createdAt: string; updatedAt: string }
export type ApplicationTask = { id: string; issueId: string; teamId: string; appUserId: string; creatorId: string; status: 'pending' | 'active' | 'awaitingInput' | 'complete' | 'error' | 'canceled'; prompt: string; trigger: string; version: number; updatedAt: string; pendingTool?: {name:string;arguments:unknown;status:string} }
/** Window event fired when the realtime stream reports an agent task change; detail is the task's resource id. */
export const AGENT_TASK_ACTIVITY_EVENT = 'flow:agent-task-activity'
/** Task states the agent may still change on its own; only these keep a fallback poll running. */
export const isApplicationTaskRunning = (task: Pick<ApplicationTask, 'status'>) => task.status === 'pending' || task.status === 'active'
export type ApplicationActivity = { id: string; sessionId: string; actorId: string; type: string; body: string; url?: string; createdAt: string }
export const isAgentMember = (user: User) => Boolean(user.app && user.active && (user.appScopes?.includes('app:assignable') || user.appScopes?.includes('app:mentionable')))
export const canDelegateTo = (user: User, teamId: string) => Boolean(user.app && user.active && user.appScopes?.includes('app:assignable') && user.appTeamIds?.includes(teamId))
export const listApplications = (workspace: string) => apiRequest<ApplicationInstallation[]>('/api/application-installations', workspace)
export const saveApplication = (workspace: string, input: Partial<ApplicationInstallation>) => apiRequest<{application: ApplicationInstallation; webhookSecret: string}>(`/api/application-installations${input.id ? `/${encodeURIComponent(input.id)}` : ''}`, workspace, { method: input.id ? 'PATCH' : 'POST', body: JSON.stringify(input) })
export const listApplicationTasks = (workspace: string, issueId: string, signal?: AbortSignal, resourceType = 'issue') => apiRequest<ApplicationTask[]>(`/api/agent-tasks?resourceType=${resourceType}&resourceId=${encodeURIComponent(issueId)}`, workspace, { signal })
export const getApplicationTask = (workspace: string, id: string, after = '', signal?: AbortSignal) => apiRequest<{session: ApplicationTask; activities: ApplicationActivity[]}>(`/api/agent-tasks/${encodeURIComponent(id)}?after=${encodeURIComponent(after)}`, workspace, { signal })
export const replyApplicationTask = (workspace: string, task: ApplicationTask, type: 'prompt' | 'canceled' | 'retry', body: string, approve?:boolean) => apiRequest<ApplicationTask>(`/api/agent-tasks/${encodeURIComponent(task.id)}/activities`, workspace, { method: 'POST', body: JSON.stringify({expectedVersion: task.version, type, body, approve}) })
