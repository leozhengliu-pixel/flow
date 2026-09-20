import { describe, expect, it } from 'vitest'
import type { DashboardWidget } from '@/types/flow'
import {
  addWidgetToLayout,
  autofixWidgets,
  deleteWidgetFromLayout,
  duplicateWidgetInLayout,
  layoutToWidgets,
  moveWidgetInLayout,
  traverseLayoutItems,
  widgetsToLayout,
} from './dashboard-layout'

function widget(id: string, position: number, width: 1 | 2 = 1): DashboardWidget {
  return {
    id,
    type: 'insight',
    title: id,
    position,
    width,
    config: {},
  }
}

describe('dashboard layout (LS-0180)', () => {
  it('adapts flat widgets to rows/columns and back', () => {
    const widgets = [widget('a', 0, 1), widget('b', 1, 1), widget('c', 2, 2)]
    const layout = widgetsToLayout(widgets)
    expect(traverseLayoutItems(layout)).toEqual(['a', 'b', 'c'])
    expect(layout.rows.length).toBeGreaterThanOrEqual(2)
    const roundTrip = layoutToWidgets(layout, new Map(widgets.map(item => [item.id, item])))
    expect(roundTrip.map(item => item.id)).toEqual(['a', 'b', 'c'])
  })

  it('autofix drops empty columns/rows and stretches single column', () => {
    const fixed = autofixWidgets({
      rows: [
        { id: 'r0', columns: [{ id: 'c0', width: 'half', items: [] }] },
        {
          id: 'r1',
          columns: [{ id: 'c1', width: 'half', items: [{ id: 'i1', widgetId: 'w1', height: 'one' }] }],
        },
      ],
    })
    expect(fixed.rows).toHaveLength(1)
    expect(fixed.rows[0].columns[0].width).toBe('full')
  })

  it('supports add/duplicate/delete/move', () => {
    let layout = widgetsToLayout([widget('a', 0), widget('b', 1)])
    layout = addWidgetToLayout(layout, 'c')
    expect(traverseLayoutItems(layout)).toContain('c')
    layout = duplicateWidgetInLayout(layout, 'a', 'a-copy')
    expect(traverseLayoutItems(layout)).toContain('a-copy')
    layout = deleteWidgetFromLayout(layout, 'b')
    expect(traverseLayoutItems(layout)).not.toContain('b')
    layout = moveWidgetInLayout(layout, 'c', { kind: 'widget', widgetId: 'a' })
    const ids = traverseLayoutItems(layout)
    expect(ids.indexOf('c')).toBeGreaterThan(ids.indexOf('a'))
  })
})
