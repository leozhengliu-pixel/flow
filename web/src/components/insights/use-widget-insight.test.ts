import { describe, expect, it } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import {
  buildInsightDrillPath,
  getDashboardWidgetIssueFilter,
  parseInsightSearchParam,
  useWidgetInsight,
} from './use-widget-insight'

describe('useWidgetInsight (LS-0783)', () => {
  const widget = {
    id: 'w1',
    config: { teamIds: ['t1'], stateIds: ['s1'], display: 'chart' as const },
  }

  it('reads widget filters and hydrates from search params', () => {
    expect(getDashboardWidgetIssueFilter(widget)).toEqual({
      teamIds: ['t1'],
      stateIds: ['s1'],
      assigneeIds: undefined,
      labelIds: undefined,
    })
    expect(parseInsightSearchParam('?insightFilter=%7B%22teamIds%22%3A%5B%22x%22%5D%7D')).toEqual({
      teamIds: ['x'],
    })
    const drill = buildInsightDrillPath('/ws/all', { teamIds: ['t1'] }, { fullscreen: true, widgetId: 'w1' })
    expect(drill.search).toContain('insights=1')
    expect(drill.search).toContain('widget=w1')
  })

  it('wires hover, drill toggle, and explore path', () => {
    const { result } = renderHook(() =>
      useWidgetInsight({ widget, workspaceIssuesPath: '/acme/all', locationSearch: '' }),
    )
    act(() => result.current.onHover({ slice: 'todo' }))
    expect(result.current.hovered).toEqual({ slice: 'todo' })
    act(() => result.current.onDrill({ slice: 'todo', segment: 'high' }))
    expect(result.current.drill).toEqual({ slice: 'todo', segment: 'high' })
    const path = result.current.explore({ slice: 'done' })
    expect(path.pathname).toBe('/acme/all')
    expect(path.filters.stateIds).toEqual(['done'])
    act(() => result.current.reset())
    expect(result.current.hovered).toBeUndefined()
    expect(result.current.drill).toBeUndefined()
  })
})
