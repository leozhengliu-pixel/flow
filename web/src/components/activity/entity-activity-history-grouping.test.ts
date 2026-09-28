import { describe, expect, it } from 'vitest'
import {
  actorBadgeLabel,
  activityBucketKey,
  buildActivityGrouping,
  groupActivityHistory,
  resolveActivityActor,
  summarizeActor,
} from './entity-activity-history-grouping'

describe('LS-0249 EntityActivityHistoryGrouping', () => {
  it('builds user / multi-user / AI / workflow actor keys', () => {
    expect(resolveActivityActor({ userIds: ['u2'] })?.key).toBe('user:u2')
    expect(resolveActivityActor({ userIds: ['u2', 'u1'] })?.key).toBe('users:u1,u2')
    expect(resolveActivityActor({ performedByLinearAi: true })?.key).toBe('linear')
    expect(resolveActivityActor({
      sourceMetadata: { type: 'workflow', id: 'auto-1' },
    })?.key).toBe('workflow:auto-1')
    expect(resolveActivityActor({
      sourceMetadata: { type: 'workflow', issueSuggestionAutomationMetadata: {} },
    })?.key).toBe('product-intelligence')
  })

  it('labels multi-user and AI actors for collapsed groups', () => {
    const multi = resolveActivityActor({
      userIds: ['a', 'b', 'c'],
      userNames: ['Ada', 'Bea', 'Cy'],
    })!
    expect(summarizeActor(multi)).toBe('3 users')
    expect(actorBadgeLabel('linear', 'Linear')).toBe('AI')
    expect(actorBadgeLabel('workflow:x', 'Workflow')).toBe('Workflow')
  })

  it('collapses by actorKey + category + bucketKey', () => {
    const stamp = '2026-09-20T08:15:00.000Z'
    const items = [
      { id: '1', createdAt: stamp, category: 'property' as const, userIds: ['u1'], userNames: ['Ada'], summaryItem: 'changed status' },
      { id: '2', createdAt: '2026-09-20T08:40:00.000Z', category: 'property' as const, userIds: ['u1'], userNames: ['Ada'], summaryItem: 'changed priority' },
      { id: '3', createdAt: stamp, category: 'comment' as const, userIds: ['u1'], userNames: ['Ada'] },
      { id: '4', createdAt: stamp, category: 'property' as const, performedByLinearAi: true },
    ]
    expect(activityBucketKey(stamp)).toBe('2026-09-20T08')
    const grouping = buildActivityGrouping(items[0])!
    expect(grouping.actorKey).toBe('user:u1')
    expect(grouping.bucketKey).toBe('2026-09-20T08')

    const groups = groupActivityHistory(items, { entityId: 'proj-1' })
    const propertyUser = groups.find(group => group.grouping.actorKey === 'user:u1' && group.grouping.category === 'property')
    expect(propertyUser?.items.map(item => item.id)).toEqual(['2', '1'])
    expect(propertyUser?.key).toContain('entity-activity-group:proj-1:property:user:u1:2026-09-20T08')
    expect(groups.some(group => group.grouping.actorKey === 'linear')).toBe(true)
    expect(groups.find(group => group.grouping.category === 'comment')?.items).toHaveLength(1)
  })
})
