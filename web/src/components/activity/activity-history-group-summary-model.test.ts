import { describe, expect, it } from 'vitest'
import {
  formatActivityHistoryGroupSummary,
  summarizeActivityHistoryGroup,
  summaryItemsFromActivityTexts,
  type ActivityHistorySummaryItem,
} from './activity-history-group-summary-model'

const items: ActivityHistorySummaryItem[] = [
  { action: 'changed', value: 'Status', weight: 3 },
  { action: 'changed', value: 'Priority', weight: 2 },
  { action: 'added', value: 'Label', weight: 1 },
  { action: 'breached', value: 'SLA', weight: 5 },
  { action: 'changed', value: 'Status', weight: 9 },
]

describe('ActivityHistoryGroupSummary (LS-0017)', () => {
  it('prioritizes breached then changed then added, uniqBy value, weight sorted', () => {
    expect(summarizeActivityHistoryGroup(items)).toBe('breached SLA, changed Status and Priority, added Label')
  })

  it('caps at five items across action buckets', () => {
    const many: ActivityHistorySummaryItem[] = [
      { action: 'breached', value: 'A', weight: 1 },
      { action: 'changed', value: 'B', weight: 5 },
      { action: 'changed', value: 'C', weight: 4 },
      { action: 'changed', value: 'D', weight: 3 },
      { action: 'changed', value: 'E', weight: 2 },
      { action: 'added', value: 'F', weight: 9 },
    ]
    expect(summarizeActivityHistoryGroup(many, 5)).toBe('breached A, changed B, C, D, and E')
  })

  it('formats Show N events label with ellipsis detail', () => {
    expect(formatActivityHistoryGroupSummary(4, items)).toBe(
      'Show 4 events: breached SLA, changed Status and Priority, added Label…',
    )
    expect(formatActivityHistoryGroupSummary(1, [])).toBe('Show 1 event')
  })

  it('maps activity categories onto summary actions', () => {
    const mapped = summaryItemsFromActivityTexts([
      { category: 'status', text: 'Moved to Done' },
      { category: 'assignment', text: 'Assigned Alice' },
      { category: 'system', text: 'SLA breached' },
    ])
    expect(mapped.map(item => item.action)).toEqual(['changed', 'added', 'breached'])
  })
})
