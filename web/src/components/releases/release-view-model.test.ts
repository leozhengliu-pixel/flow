import { describe, expect, it } from 'vitest'

import { completed, makeBootstrap, makeIssue } from '@/test/fixtures'
import type { Release, ReleasePipeline } from '@/types/flow'

import { pickChangelogTargetRelease, releaseIssueQuery, releaseProgress, releaseStatusForStage, releasesInInclusiveRange } from './release-view-model'

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

describe('pickChangelogTargetRelease', () => {
  it('prefers the latest releasedAt among completed releases', () => {
    const releases = [
      { id: 'a', status: 'released', releasedAt: '2026-01-01T00:00:00Z' },
      { id: 'b', status: 'released', releasedAt: '2026-03-01T00:00:00Z' },
      { id: 'c', status: 'planned' },
    ] as Release[]
    expect(pickChangelogTargetRelease(releases)?.id).toBe('b')
  })
})

describe('releasesInInclusiveRange', () => {
  it('returns the inclusive released range ordered by date', () => {
    const releases = [
      { id: 'a', status: 'released', releasedAt: '2026-01-01T00:00:00Z', createdAt: '2026-01-01T00:00:00Z' },
      { id: 'b', status: 'released', releasedAt: '2026-02-01T00:00:00Z', createdAt: '2026-02-01T00:00:00Z' },
      { id: 'c', status: 'released', releasedAt: '2026-03-01T00:00:00Z', createdAt: '2026-03-01T00:00:00Z' },
    ] as Release[]
    expect(releasesInInclusiveRange(releases, 'a', 'c').map(item => item.id)).toEqual(['a', 'b', 'c'])
  })
})

describe('release list helpers', () => {
  const now = new Date(2026, 9, 10, 9, 0)
  it('parses Linear fuzzy dates in English and Chinese', async () => {
    const { parseReleaseDateQuery } = await import('./release-view-model')
    expect(parseReleaseDateQuery('24h', now)).toBe('2026-10-11')
    expect(parseReleaseDateQuery('7 days', now)).toBe('2026-10-17')
    expect(parseReleaseDateQuery('2 weeks', now)).toBe('2026-10-24')
    expect(parseReleaseDateQuery('Feb 9', now)).toBe('2027-02-09')
    expect(parseReleaseDateQuery('7 天', now)).toBe('2026-10-17')
    expect(parseReleaseDateQuery('12月1日', now)).toBe('2026-12-01')
    expect(parseReleaseDateQuery('nonsense', now)).toBeUndefined()
  })

  it('groups stages started, planned, completed, canceled and sorts by release date', async () => {
    const { releaseStageGroups, sortReleases } = await import('./release-view-model')
    const make = (id: string, stage: string, targetDate?: string) => ({ id, name: id, version: '', stage, position: 0, createdAt: '2026-01-01', targetDate } as unknown as Release)
    const items = [make('a', 'Planning', '2026-10-01'), make('b', 'In progress', '2026-11-01'), make('c', 'Released'), make('d', 'Planning')]
    expect(releaseStageGroups(items, pipeline).map(group => group.stage)).toEqual(['In progress', 'Planning', 'Released'])
    expect(sortReleases(items, pipeline, 'releaseDate', 'desc').map(item => item.id)).toEqual(['b', 'a', 'c', 'd'])
  })

  it('picks the changelog target like Linear: latest completed, else the first scheduled release', async () => {
    const { changelogTargetRelease } = await import('./release-view-model')
    const scheduled = { ...pipeline, id: 'p', type: 'scheduled' } as ReleasePipeline
    const base = { pipelineId: 'p', issueIds: [], createdAt: '2026-01-01', position: 0 }
    const planned = { ...base, id: 'planned', status: 'planned' } as unknown as Release
    const shipped = { ...base, id: 'shipped', status: 'released', releasedAt: '2026-09-01T00:00:00Z', position: 1 } as unknown as Release
    expect(changelogTargetRelease(makeBootstrap({ releases: [planned] }), scheduled)?.id).toBe('planned')
    expect(changelogTargetRelease(makeBootstrap({ releases: [planned, shipped] }), scheduled)?.id).toBe('shipped')
    expect(changelogTargetRelease(makeBootstrap({ releases: [planned] }), { ...scheduled, type: 'continuous' })).toBeUndefined()
  })
})
