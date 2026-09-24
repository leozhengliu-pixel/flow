import { describe, expect, it } from 'vitest'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from './my-issues-surface'
import { parseNaturalLanguageFilter } from './natural-language-filter'

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
})
