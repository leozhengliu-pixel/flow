import { describe, expect, it } from 'vitest'
import { translateToChinese } from '@/i18n/translate'
import { viewer } from '@/test/fixtures'
import type { Notification } from '@/types/flow'
import { agentReplyLine, agentReplyProjectPath, finishedSessionTarget, pointsAtSessionCard } from './agent-reply-notification'
import { documentNotificationKind, documentNotificationLine, documentNotificationPath, isDocumentNotification } from './hosts/document-inbox-model'

const agent = { ...viewer, id: 'app-flow', displayName: 'Flow', app: true }
const notification = (agentActivity: string, excerpt = 'Two fields changed.') => ({ id: 'n', recipientId: viewer.id, type: 'agentReply', sourceType: 'agentSession', sourceId: 'task', issueId: 'issue-1', commentId: 'comment-1', actor: agent, category: 'comments', groupKey: 'g', occurrenceCount: 3, latestActorIds: [agent.id], favorite: false, payload: { excerpt, agentActivity }, createdAt: '', updatedAt: '' }) as unknown as Notification

describe('agent reply notifications', () => {
  it('reads "<Agent> replied: <first line>" in English and Chinese, without an occurrence count', () => {
    expect(agentReplyLine(notification('response'), source => source)).toBe('Flow replied: Two fields changed.')
    const zh = translateToChinese
    expect(agentReplyLine(notification('response'), zh)).toBe('Flow 回复了：Two fields changed.')
    expect(agentReplyLine(notification('elicitation', '用哪个优先级？'), zh)).toBe('Flow 正在等待你的回复：用哪个优先级？')
    expect(agentReplyLine(notification('error', ''), zh)).toBe('Flow 出错了')
    expect(agentReplyLine(notification('finished'), source => source)).toBe('Flow finished: Two fields changed.')
    expect(agentReplyLine(notification('finished', ''), source => source)).toBe('Flow finished')
    expect(agentReplyLine(notification('finished'), zh)).toBe('Flow 已完成：Two fields changed.')
  })

  it('anchors a delegated session without a pull request on its card, but a reply to a comment on the comment', () => {
    expect(pointsAtSessionCard({ ...notification('finished'), commentId: '' } as Notification)).toBe(true)
    expect(pointsAtSessionCard(notification('finished'))).toBe(false)
  })

  it('words document thread replies like issue thread replies and links to the comment', () => {
    const reply = { ...notification('response'), issueId: '', sourceType: 'document', sourceId: 'document-1', payload: { excerpt: 'Two fields changed.', agentActivity: 'response', documentSlugId: 'plan-1a2b' } } as unknown as Notification
    expect(isDocumentNotification(reply)).toBe(true)
    expect(documentNotificationLine(reply, source => source)).toBe('Flow replied: Two fields changed.')
    expect(documentNotificationLine(reply, translateToChinese)).toBe('Flow 回复了：Two fields changed.')
    expect(documentNotificationKind(reply)).toBe('comment')
    expect(documentNotificationPath('ws', reply)).toBe('/ws/document/plan-1a2b#comment-comment-1')
  })

  it('links project thread replies to the comment on the project Activity tab', () => {
    expect(agentReplyProjectPath('ws', { slugId: 'apollo-1a2b' }, 'project_comment_1')).toBe('/ws/project/apollo-1a2b/activity#comment-project_comment_1')
    expect(agentReplyProjectPath('ws', { slugId: 'apollo-1a2b' })).toBe('/ws/project/apollo-1a2b/activity')
  })

  it('opens a finished session with a pull request on its review, otherwise the issue', () => {
    const withSession = (agentActivity: string, payload: object = {}) => ({ ...notification(agentActivity), payload: { agentActivity, codingSessionId: 'cs_1', reviewId: 'review-1', ...payload } }) as unknown as Notification
    const data = { reviews: [{ id: 'review-1' }] }
    expect(finishedSessionTarget(data, withSession('finished'))).toEqual({ sessionId: 'cs_1', sessionReviewId: 'review-1' })
    expect(finishedSessionTarget(data, withSession('response'))).toEqual({})
    expect(finishedSessionTarget(data, withSession('finished', { reviewId: undefined }))).toEqual({})
    expect(finishedSessionTarget({ reviews: [] }, withSession('finished'))).toEqual({})
  })
})
