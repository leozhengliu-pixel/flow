import { describe, expect, it } from 'vitest'
import { makeIssue } from '@/test/fixtures'
import { similarIssues, titleSimilarity } from './similar-issues'

describe('similar issues', () => {
  it('scores overlapping titles and ignores stop words', () => {
    expect(titleSimilarity('Login button broken on Safari', 'Safari login button is broken')).toBeGreaterThan(0.7)
    expect(titleSimilarity('Add dark mode', 'Fix billing export')).toBe(0)
    expect(titleSimilarity('登录按钮失效', '登录按钮无响应')).toBeGreaterThan(0.2)
  })

  it('excludes the issue itself, linked and canceled issues', () => {
    const base = makeIssue({ id: 'a', title: 'Crash when exporting CSV report' })
    const twin = makeIssue({ id: 'b', title: 'Exporting CSV report crash' })
    const other = makeIssue({ id: 'c', title: 'Update onboarding copy' })
    expect(similarIssues(base, [base, twin, other]).map(item => item.candidate.id)).toEqual(['b'])
    expect(similarIssues({ ...base, relations: [{ id: 'r', type: 'related', relatedIssueId: 'b' }] } as never, [base, twin])).toEqual([])
  })
})
