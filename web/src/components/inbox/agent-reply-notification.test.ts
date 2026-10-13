import { describe, expect, it } from 'vitest'
import { translateToChinese } from '@/i18n/translate'
import { viewer } from '@/test/fixtures'
import type { Notification } from '@/types/flow'
import { agentReplyLine } from './agent-reply-notification'

const agent = { ...viewer, id: 'app-flow', displayName: 'Flow', app: true }
const notification = (agentActivity: string, excerpt = 'Two fields changed.') => ({ id: 'n', recipientId: viewer.id, type: 'agentReply', sourceType: 'agentSession', sourceId: 'task', issueId: 'issue-1', commentId: 'comment-1', actor: agent, category: 'comments', groupKey: 'g', occurrenceCount: 3, latestActorIds: [agent.id], favorite: false, payload: { excerpt, agentActivity }, createdAt: '', updatedAt: '' }) as unknown as Notification

describe('agent reply notifications', () => {
  it('reads "<Agent> replied: <first line>" in English and Chinese, without an occurrence count', () => {
    expect(agentReplyLine(notification('response'), source => source)).toBe('Flow replied: Two fields changed.')
    const zh = translateToChinese
    expect(agentReplyLine(notification('response'), zh)).toBe('Flow 回复了：Two fields changed.')
    expect(agentReplyLine(notification('elicitation', '用哪个优先级？'), zh)).toBe('Flow 正在等待你的回复：用哪个优先级？')
    expect(agentReplyLine(notification('error', ''), zh)).toBe('Flow 出错了')
  })
})
