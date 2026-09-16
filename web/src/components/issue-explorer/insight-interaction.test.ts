import { describe, expect, it } from 'vitest'
import { aggregateInsightValues, durationAxis, insightHighlight, insightTargetMatches, sameInsightTarget } from './insight-interaction'
import type { SavedViewInsightsConfig } from './saved-view-panels'
import { buildInsightData } from './insight-data'
import { makeBootstrap, makeIssue, completed, started } from '@/test/fixtures'
import { issueToExplorerRow } from './issue-explorer-model'

const config: SavedViewInsightsConfig = { measure: 'issueCount', slice: 'status', segment: 'assignee', timeInStatusIds: [], colors: 'status', showArchived: false }
describe('insight graph and table interaction', () => {
  it('uses a symmetric logarithmic duration axis with calendar ticks and finite zero positions', () => {
    const day = 86400000
    const axis = durationAxis([29*day,184*day])
    expect(axis.ticks.map(tick => tick.label)).toEqual(['1w','1mo','1y'])
    expect(axis.fraction(29*day)).toBeGreaterThan(0)
    expect(axis.fraction(184*day)).toBeLessThan(1)
    expect(Number.isFinite(durationAxis([0]).fraction(0))).toBe(true)
  })
  it('distinguishes row, column and cell hover without losing a selection', () => {
    expect(insightHighlight({ slice: 's' }, { slice: 's', segment: 'a' })).toBe('strong')
    expect(insightHighlight({ segment: 'a' }, { slice: 's', segment: 'a' })).toBe('strong')
    expect(insightHighlight({ slice: 's', segment: 'a' }, { slice: 's', segment: 'b' })).toBe('weak')
    expect(insightHighlight({ slice: 's', segment: 'a' }, { slice: 't', segment: 'b' })).toBeUndefined()
    expect(sameInsightTarget({ slice: 's', segment: 'a' }, { slice: 's' })).toBe(false)
    expect(sameInsightTarget({ slice: 's' }, { slice: 's' })).toBe(true)
    expect(sameInsightTarget(undefined, {})).toBe(false)
  })
  it('drills into a segment intersection and includes no-value groups', () => {
    const sample = { item: 'issue', slices: ['s'], segments: ['none'], value: 10 }
    expect(insightTargetMatches(sample, { slice: 's', segment: 'none' })).toBe(true)
    expect(insightTargetMatches(sample, { slice: 's', segment: 'someone' })).toBe(false)
    expect(insightTargetMatches(sample, { segment: 'none' })).toBe(true)
    expect(insightTargetMatches(sample, { threshold: 10, operator: 'gt' })).toBe(false)
    expect(insightTargetMatches(sample, { threshold: 10, operator: 'lte' })).toBe(true)
  })
  it('uses measured duration statistics rather than summing durations', () => {
    expect(aggregateInsightValues([1, 2, 3, 100], 'average')).toBe(26.5)
    expect(aggregateInsightValues([1, 2, 3, 100], 'median')).toBe(2)
    expect(aggregateInsightValues([1, 2, 3, 100], 'p75')).toBe(3)
    expect(aggregateInsightValues([30, 30, 184], 'p75')).toBe(184)
    expect(aggregateInsightValues([], 'max')).toBeUndefined()
    const issues = [makeIssue({ id: 'a', startedAt: '2026-08-01T00:00:00Z', completedAt: '2026-08-02T00:00:00Z' }), makeIssue({ id: 'b', startedAt: '2026-08-01T00:00:00Z', completedAt: '2026-08-04T00:00:00Z' }), makeIssue({ id: 'c' })]
    const data = makeBootstrap({ issues })
    const result = buildInsightData(issues.map(issue => issueToExplorerRow(issue, 'workspace', issues, data)), { ...config, measure: 'cycleTime' }, data)
    expect(result.samples).toHaveLength(2)
    expect(result.rows[0].total).toBe(2 * 86400000)
    expect(result.rows[0].aggregations.max).toBe(3 * 86400000)
  })
  it('keeps identical names separate and orders statuses by workflow position', () => {
    const states = [started, { ...completed, name: started.name }]
    const issues = [makeIssue({ id: 'a', state: states[1] }), makeIssue({ id: 'b', state: states[0] })]
    const data = makeBootstrap({ issues, states })
    const result = buildInsightData(issues.map(issue => issueToExplorerRow(issue, 'workspace', issues, data)), config, data)
    expect(result.rows.map(row => row.id)).toEqual(states.map(state => state.id))
    expect(result.samples).toHaveLength(2)
  })
  it('aggregates ten thousand records without expanding multi-label samples', () => {
    const issue = makeIssue()
    const data = makeBootstrap()
    const row = issueToExplorerRow(issue, 'workspace', [issue], data)
    const rows = Array.from({ length: 10000 }, (_, i) => ({ ...row, id: `issue-${i}`, labels: [{ id: 'a', name: 'A', color: '#777777' }, { id: 'b', name: 'B', color: '#777777' }] }))
    const result = buildInsightData(rows, { ...config, slice: 'label' }, data)
    expect(result.samples).toHaveLength(10000)
    expect(result.rows.map(row => row.total)).toEqual([10000, 10000])
  })
})
