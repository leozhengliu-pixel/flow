import { describe, expect, it } from 'vitest'
import type { Dashboard, DashboardWidget } from '@/types/flow'
import {
  cloneWidget,
  formatOriginDescription,
  groupDashboardsForCopy,
  insightDefaultTitle,
} from './widget-actions'

describe('WidgetActions helpers (LS-0653)', () => {
  it('names insights from measure/slice/segment', () => {
    expect(insightDefaultTitle({ measure: 'issue_count', slice: 'status', segment: 'priority' })).toBe(
      'issue count by status and priority',
    )
    expect(formatOriginDescription('Ops')).toBe('Added from dashboard Ops')
  })

  it('clones widgets with a new id and groups copy targets', () => {
    const widget = {
      id: 'w1',
      type: 'insight',
      title: 'By status',
      position: 0,
      width: 1,
      config: {},
    } as DashboardWidget
    const clone = cloneWidget(widget, 3)
    expect(clone.id).not.toBe('w1')
    expect(clone.position).toBe(3)

    const dashboards = [
      { id: 'd1', name: 'One', visibility: 'workspace', teamIds: [], updatedAt: '' },
      { id: 'd2', name: 'Team board', visibility: 'team', teamIds: ['t1'], updatedAt: '' },
      { id: 'd3', name: 'Recent', visibility: 'private', teamIds: [], updatedAt: '' },
    ] as Dashboard[]
    const groups = groupDashboardsForCopy({
      dashboards,
      currentDashboardId: 'd1',
      currentTeamId: 't1',
      recentIds: ['d3'],
    })
    expect(groups[0]).toMatchObject({ id: 'recent' })
    expect(groups.find(group => group.id === 'team')?.dashboards.map(item => item.id)).toEqual(['d2'])
  })
})
