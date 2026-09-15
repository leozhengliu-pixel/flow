import { describe, expect, it } from 'vitest'

import { completed, makeBootstrap, makeIssue } from '@/test/fixtures'
import type { Release, ReleasePipeline } from '@/types/flow'

import { releaseIssueQuery, releaseProgress, releaseStatusForStage } from './release-view-model'

const pipeline = {
  stages: ['Planning', 'In progress', 'Released', 'Canceled'],
  stageStatuses: {
    Planning: 'planned',
    'In progress': 'inProgress',
    Released: 'released',
    Canceled: 'canceled',
  },
} as unknown as ReleasePipeline

describe('releaseStatusForStage', () => {
  it('maps pipeline stages onto release statuses', () => {
    expect(releaseStatusForStage(pipeline, 'Planning')).toBe('planned')
    expect(releaseStatusForStage(pipeline, 'In progress')).toBe('inProgress')
    expect(releaseStatusForStage(pipeline, 'Released')).toBe('released')
    expect(releaseStatusForStage(pipeline, 'Canceled')).toBe('canceled')
  })

  it('falls back when a stage is unmapped', () => {
    expect(releaseStatusForStage(pipeline, 'Unassigned')).toBe('planned')
    expect(releaseStatusForStage(pipeline, 'Unassigned', 'canceled')).toBe('canceled')
  })
})

describe('releaseIssueQuery', () => {
  it('scopes the records query to one release without hiding by status', () => {
    expect(releaseIssueQuery('release-1')).toEqual({
      releaseId: 'release-1',
      archived: 'false',
      limit: 100,
      includeTotal: false,
      cursor: undefined,
    })
    expect(releaseIssueQuery('release-1', 'page-2').cursor).toBe('page-2')
  })
})

describe('releaseProgress', () => {
  const data = makeBootstrap({ issues: [] })
  const release = { issueIds: [] } as unknown as Release

  it('uses the server count when the paged issue collection is empty', () => {
    expect(releaseProgress(data, { ...release, issueCount: 18, completedCount: 9 } as Release)).toBe(50)
  })

  it('keeps the bootstrap fallback for older cached responses', () => {
    const issue = makeIssue({ state: completed })
    expect(releaseProgress(makeBootstrap({ issues: [issue] }), { ...release, issueIds: [issue.id] } as Release)).toBe(100)
  })
})
