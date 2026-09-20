import { beforeEach, describe, expect, it } from 'vitest'
import { inlineCommentsState, type InlineCommentMark } from './inline-comments-state'

const mark = (overrides: Partial<InlineCommentMark> = {}): InlineCommentMark => ({
  id: 'mark-1',
  commentId: 'comment-1',
  parentId: 'issue-1',
  parentType: 'issue',
  from: 2,
  to: 8,
  ...overrides,
})

describe('inlineCommentsState', () => {
  beforeEach(() => inlineCommentsState.reset())

  it('adds, activates, resolves, and counts unresolved marks', () => {
    inlineCommentsState.addMark(mark())
    expect(inlineCommentsState.getSnapshot('issue', 'issue-1').marks).toHaveLength(1)
    expect(inlineCommentsState.getSnapshot('issue', 'issue-1').activeCommentId).toBe('comment-1')
    expect(inlineCommentsState.activeUnresolvedCount('issue', 'issue-1')).toBe(1)
    inlineCommentsState.resolveMark('issue', 'issue-1', 'comment-1', true)
    expect(inlineCommentsState.activeUnresolvedCount('issue', 'issue-1')).toBe(0)
    inlineCommentsState.toggleThreadExpanded('issue', 'issue-1', 'comment-1')
    expect(inlineCommentsState.isThreadExpanded('issue', 'issue-1', 'comment-1')).toBe(true)
    inlineCommentsState.removeMark('issue', 'issue-1', 'comment-1')
    expect(inlineCommentsState.getSnapshot('issue', 'issue-1').marks).toHaveLength(0)
  })
})
