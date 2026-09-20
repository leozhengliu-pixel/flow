/**
 * LS-0180 DashboardPage layout engine — widgets → rows/columns/items (+ flat compat).
 */

import type { DashboardWidget } from '@/types/flow'

export type LayoutWidth = 'one' | 'half' | 'two-thirds' | 'full'
export type LayoutHeight = 'one' | 'two'

export type DashboardLayoutItem = {
  id: string
  widgetId: string
  height: LayoutHeight
}

export type DashboardLayoutColumn = {
  id: string
  width: LayoutWidth
  items: DashboardLayoutItem[]
}

export type DashboardLayoutRow = {
  id: string
  columns: DashboardLayoutColumn[]
}

export type DashboardWidgetLayout = {
  rows: DashboardLayoutRow[]
}

export const maxColumnsPerRow = 4

const WIDTH_FRACTION: Record<LayoutWidth, number> = {
  one: 0.25,
  half: 0.5,
  'two-thirds': 2 / 3,
  full: 1,
}

export function widthFromFraction(fraction: number): LayoutWidth {
  if (fraction >= 0.9) return 'full'
  if (fraction >= 0.6) return 'two-thirds'
  if (fraction >= 0.4) return 'half'
  return 'one'
}

export function fractionForWidth(width: LayoutWidth): number {
  return WIDTH_FRACTION[width]
}

/** Adapt flat widgets[] + width 1|2 into rows/columns/items. */
export function widgetsToLayout(widgets: DashboardWidget[]): DashboardWidgetLayout {
  const ordered = [...widgets].sort((a, b) => a.position - b.position)
  const rows: DashboardLayoutRow[] = []
  let row: DashboardLayoutRow | undefined
  let used = 0

  for (const widget of ordered) {
    const width: LayoutWidth = widget.width === 2 ? 'full' : 'half'
    const fraction = fractionForWidth(width)
    if (!row || used + fraction > 1.001 || row.columns.length >= maxColumnsPerRow) {
      row = { id: `row_${rows.length}`, columns: [] }
      rows.push(row)
      used = 0
    }
    row.columns.push({
      id: `column_${widget.id}`,
      width,
      items: [{ id: `item_${widget.id}`, widgetId: widget.id, height: 'one' }],
    })
    used += fraction
  }
  return autofixWidgets({ rows })
}

/** Flatten layout back to position/width widgets (keeps widget payloads). */
export function layoutToWidgets(
  layout: DashboardWidgetLayout,
  widgetsById: Map<string, DashboardWidget>,
): DashboardWidget[] {
  const next: DashboardWidget[] = []
  let position = 0
  for (const row of layout.rows) {
    for (const column of row.columns) {
      for (const item of column.items) {
        const widget = widgetsById.get(item.widgetId)
        if (!widget) continue
        next.push({
          ...widget,
          position,
          width: column.width === 'full' || column.width === 'two-thirds' ? 2 : 1,
        })
        position += 1
      }
    }
  }
  return next
}

/** Normalize column widths to sum≈1; drop empty columns/rows. */
export function autofixWidgets(layout: DashboardWidgetLayout): DashboardWidgetLayout {
  const rows: DashboardLayoutRow[] = []
  for (const row of layout.rows) {
    const columns = row.columns
      .map(column => ({
        ...column,
        items: column.items.filter(item => Boolean(item.widgetId)),
      }))
      .filter(column => column.items.length > 0)
    if (!columns.length) continue
    const total = columns.reduce((sum, column) => sum + fractionForWidth(column.width), 0) || 1
    const normalized = columns.map(column => ({
      ...column,
      width: widthFromFraction(fractionForWidth(column.width) / total),
    }))
    // If a single column remains, stretch to full.
    if (normalized.length === 1) normalized[0] = { ...normalized[0], width: 'full' }
    rows.push({ ...row, columns: normalized })
  }
  return { rows }
}

export function addWidgetToLayout(
  layout: DashboardWidgetLayout,
  widgetId: string,
  options: { row?: number; column?: number } = {},
): DashboardWidgetLayout {
  const rows = layout.rows.map(row => ({
    ...row,
    columns: row.columns.map(column => ({ ...column, items: [...column.items] })),
  }))
  const item: DashboardLayoutItem = { id: `item_${widgetId}`, widgetId, height: 'one' }

  if (options.row === undefined || !rows[options.row]) {
    rows.push({
      id: `row_${rows.length}`,
      columns: [{ id: `column_${widgetId}`, width: 'full', items: [item] }],
    })
    return autofixWidgets({ rows })
  }

  const row = rows[options.row]
  if (options.column === undefined || !row.columns[options.column]) {
    if (row.columns.length >= maxColumnsPerRow) {
      rows.push({
        id: `row_${rows.length}`,
        columns: [{ id: `column_${widgetId}`, width: 'one', items: [item] }],
      })
    } else {
      row.columns.push({ id: `column_${widgetId}`, width: 'one', items: [item] })
    }
    return autofixWidgets({ rows })
  }

  row.columns[options.column].items.push(item)
  return autofixWidgets({ rows })
}

export function duplicateWidgetInLayout(
  layout: DashboardWidgetLayout,
  sourceWidgetId: string,
  newWidgetId: string,
): DashboardWidgetLayout {
  const rows = layout.rows.map(row => ({
    ...row,
    columns: row.columns.map(column => ({ ...column, items: [...column.items] })),
  }))
  for (const row of rows) {
    for (const column of row.columns) {
      const index = column.items.findIndex(item => item.widgetId === sourceWidgetId)
      if (index >= 0) {
        column.items.splice(index + 1, 0, {
          id: `item_${newWidgetId}`,
          widgetId: newWidgetId,
          height: column.items[index].height,
        })
        return autofixWidgets({ rows })
      }
    }
  }
  return addWidgetToLayout({ rows }, newWidgetId)
}

export function deleteWidgetFromLayout(
  layout: DashboardWidgetLayout,
  widgetId: string,
): DashboardWidgetLayout {
  const rows = layout.rows.map(row => ({
    ...row,
    columns: row.columns.map(column => ({
      ...column,
      items: column.items.filter(item => item.widgetId !== widgetId),
    })),
  }))
  return autofixWidgets({ rows })
}

/** Move a widget onto a drop target (row_* or column_* / widget id). */
export function moveWidgetInLayout(
  layout: DashboardWidgetLayout,
  sourceWidgetId: string,
  target: { kind: 'row'; rowIndex: number } | { kind: 'column'; rowIndex: number; columnIndex: number } | { kind: 'widget'; widgetId: string },
): DashboardWidgetLayout {
  let working = deleteWidgetFromLayout(layout, sourceWidgetId)
  if (target.kind === 'row') {
    return addWidgetToLayout(working, sourceWidgetId, { row: target.rowIndex })
  }
  if (target.kind === 'column') {
    return addWidgetToLayout(working, sourceWidgetId, { row: target.rowIndex, column: target.columnIndex })
  }
  // Place into the same column as the target widget, after it.
  const rows = working.rows.map(row => ({
    ...row,
    columns: row.columns.map(column => ({ ...column, items: [...column.items] })),
  }))
  for (const row of rows) {
    for (const column of row.columns) {
      const index = column.items.findIndex(item => item.widgetId === target.widgetId)
      if (index >= 0) {
        column.items.splice(index + 1, 0, {
          id: `item_${sourceWidgetId}`,
          widgetId: sourceWidgetId,
          height: 'one',
        })
        return autofixWidgets({ rows })
      }
    }
  }
  return addWidgetToLayout(working, sourceWidgetId)
}

export function traverseLayoutItems(layout: DashboardWidgetLayout): string[] {
  const ids: string[] = []
  for (const row of layout.rows) {
    for (const column of row.columns) {
      for (const item of column.items) ids.push(item.widgetId)
    }
  }
  return ids
}
