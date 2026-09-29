import { describe, expect, it } from 'vitest'
import type { Issue } from '@/types/flow'
import { similarIssues } from './similar-issues'

const issue = (id: string, extra: Partial<Issue> = {}) => ({ id, identifier: id.toUpperCase(), title: 'Recurring test: daily standup notes', team: { id: 't' }, state: { type: 'unstarted' }, relations: [], subIssueIds: [], ...extra }) as unknown as Issue

describe('similarIssues', () => {
  it('does not offer earlier occurrences of the same recurring series as duplicates', () => {
    const first = issue('dev-12')
    const second = issue('dev-13', { recurrenceSeriesId: 'dev-12' })
    const third = issue('dev-14', { recurrenceSeriesId: 'dev-12', recurrence: 'daily' })
    const unrelated = issue('dev-20')
    expect(similarIssues(third, [first, second, third, unrelated]).map(item => item.candidate.identifier)).toEqual(['DEV-20'])
    expect(similarIssues(first, [first, second, unrelated]).map(item => item.candidate.identifier)).toEqual(['DEV-20'])
  })
})
