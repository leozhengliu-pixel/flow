import { useMemo } from 'react'
import type { AgentMessage, AgentMessagePart, AgentSession } from '@/types/flow'
import type { ApplicationActivity, ApplicationTask } from '@/lib/application-agents'

/**
 * LS-0752 useLinearAgentItems — unify agent session messages/parts + application task activities
 * into a single renderable item list (markdown hydrate happens via AgentRichText at render time).
 */

export type LinearAgentItem =
  | {
      kind: 'message'
      id: string
      role: AgentMessage['role']
      content: string
      parts?: AgentMessagePart[]
      createdAt: string
      sessionId: string
    }
  | {
      kind: 'activity'
      id: string
      type: string
      body: string
      url?: string
      createdAt: string
      taskId: string
      sessionId: string
    }
  | {
      kind: 'task'
      id: string
      status: ApplicationTask['status']
      prompt: string
      updatedAt: string
      taskId: string
    }

export function useLinearAgentItems(input: {
  sessions?: AgentSession[]
  tasks?: ApplicationTask[]
  activitiesByTaskId?: Record<string, ApplicationActivity[]>
  streamParts?: AgentMessagePart[]
  streamSessionId?: string
}): LinearAgentItem[] {
  const { sessions = [], tasks = [], activitiesByTaskId = {}, streamParts = [], streamSessionId } = input

  return useMemo(() => {
    const items: LinearAgentItem[] = []

    for (const session of sessions) {
      for (const message of session.messages) {
        items.push({
          kind: 'message',
          id: message.id,
          role: message.role,
          content: message.content,
          parts: message.parts,
          createdAt: message.createdAt,
          sessionId: session.id,
        })
      }
    }

    if (streamParts.length && streamSessionId) {
      items.push({
        kind: 'message',
        id: `stream:${streamSessionId}`,
        role: 'assistant',
        content: '',
        parts: streamParts,
        createdAt: new Date().toISOString(),
        sessionId: streamSessionId,
      })
    }

    for (const task of tasks) {
      items.push({
        kind: 'task',
        id: `task:${task.id}`,
        status: task.status,
        prompt: task.prompt,
        updatedAt: task.updatedAt,
        taskId: task.id,
      })
      for (const activity of activitiesByTaskId[task.id] ?? []) {
        items.push({
          kind: 'activity',
          id: activity.id,
          type: activity.type,
          body: activity.body,
          url: activity.url,
          createdAt: activity.createdAt,
          taskId: task.id,
          sessionId: activity.sessionId,
        })
      }
    }

    return items.sort((a, b) => {
      const aTime = 'createdAt' in a ? a.createdAt : a.updatedAt
      const bTime = 'createdAt' in b ? b.createdAt : b.updatedAt
      return aTime.localeCompare(bTime)
    })
  }, [sessions, tasks, activitiesByTaskId, streamParts, streamSessionId])
}

export function agentItemsToMarkdown(items: LinearAgentItem[]): string {
  return items
    .map(item => {
      if (item.kind === 'message') {
        return `**${item.role}:** ${item.content}`.trim()
      }
      if (item.kind === 'activity') {
        return `_${item.type}_: ${item.body}`.trim()
      }
      return `_task ${item.status}_: ${item.prompt}`.trim()
    })
    .filter(Boolean)
    .join('\n\n')
}
