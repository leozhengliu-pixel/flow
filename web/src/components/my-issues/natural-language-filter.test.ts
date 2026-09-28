import { describe, expect, it, vi } from 'vitest'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from './my-issues-surface'

const api = vi.hoisted(() => ({ aiIssueFilter: vi.fn() }))
vi.mock('@/lib/api', () => api)

import { parseNaturalLanguageFilter, resolveAIFilter } from './natural-language-filter'

const options: Partial<Record<MyIssuesFilterKey, MyIssuesFilterOption[]>> = {
  status: [{ id: 's-review', label: 'In Review' }, { id: 's-todo', label: 'Todo' }],
  priority: [{ id: '1', label: 'Urgent' }, { id: '2', label: 'High' }],
  labels: [{ id: 'l-bug', label: 'Bug' }, { id: 'l-front', label: 'Frontend' }],
  assignee: [{ id: '', label: 'No assignee' }, { id: 'u-ada', label: 'Ada Lovelace' }],
}
const optionsFor = (field: MyIssuesFilterKey) => options[field]

describe('natural-language filter', () => {
  it('maps words to workspace values, dates and me', () => {
    const parsed = parseNaturalLanguageFilter('urgent bugs in review assigned to me due this week', optionsFor)
    expect(parsed.map(item => `${item.field}:${item.option.id}`)).toEqual(['ai:assigned-to-me', 'dates:next-week', 'status:s-review', 'priority:1', 'labels:l-bug'])
  })

  it('matches people and keeps leftover words as a content search', () => {
    const parsed = parseNaturalLanguageFilter('frontend crash assigned to ada', optionsFor)
    expect(parsed.map(item => `${item.field}:${item.option.id}`)).toEqual(['assignee:u-ada', 'labels:l-front', 'content:query:crash'])
  })

  it('uses the Agent reply with the workspace vocabulary, then falls back to the parser', async () => {
    api.aiIssueFilter.mockResolvedValueOnce({ filters: [{ field: 'labels', option: { id: 'l-bug', label: 'Bug' } }] })
    const parsed = await resolveAIFilter('broken stuff', optionsFor)
    expect(parsed).toEqual([{ field: 'labels', option: { id: 'l-bug', label: 'Bug' } }])
    const [, vocabulary] = api.aiIssueFilter.mock.calls[0]
    expect(vocabulary.labels).toEqual([{ id: 'l-bug', label: 'Bug' }, { id: 'l-front', label: 'Frontend' }])
    expect(vocabulary.dates.map((item: { id: string }) => item.id)).toContain('overdue')

    api.aiIssueFilter.mockRejectedValueOnce(new Error('AI filter is not configured'))
    const fallback = await resolveAIFilter('urgent bugs', optionsFor)
    expect(fallback.map(item => `${item.field}:${item.option.id}`)).toEqual(['priority:1', 'labels:l-bug'])
  })
})
