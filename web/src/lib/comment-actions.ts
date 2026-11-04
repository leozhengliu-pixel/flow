import { jsonRequest, request } from './api-client'
import type { Issue } from '@/types/flow'

export interface CommentAction {
  id: string
  issueId: string
  commentId: string
  commentVersion?: number
  targetUserId: string
  status: 'pending' | 'accepted' | 'dismissed'
  action: 'status' | 'assignee' | 'priority' | 'labels' | 'subIssue' | 'link'
  arguments: { stateId?: string; assigneeId?: string; priority?: number; labelIds?: string[]; title?: string; description?: string; url?: string }
  createdAt: string
  resolvedAt?: string
  resolvedBy?: string
  resultIssueId?: string
}

export const COMMENT_ACTIONS_CHANGED = 'flow:comment-actions-changed'
export const COMMENT_ACTION_RESOLVED = 'flow:comment-action-resolved'

export interface CommentActionResolution {
  workspaceKey: string
  viewerId: string
  issue: Issue
}

export function listCommentActions(issueId: string, signal?: AbortSignal): Promise<CommentAction[]> {
  return request(`/api/issue-records/${encodeURIComponent(issueId)}/comment-actions`, { signal })
}

export function resolveCommentAction(issueId: string, id: string, status: 'accepted' | 'dismissed'): Promise<{ suggestion: CommentAction; issue: Issue }> {
  return request(`/api/issue-records/${encodeURIComponent(issueId)}/comment-actions/${encodeURIComponent(id)}`, jsonRequest('POST', { status }))
}
