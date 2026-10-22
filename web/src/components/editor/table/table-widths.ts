import { Extension } from '@tiptap/core'
import type { Node as ProseMirrorNode } from '@tiptap/pm/model'
import { Plugin, PluginKey, type Transaction } from '@tiptap/pm/state'
import { TableMap } from '@tiptap/pm/tables'
import { ySyncPluginKey } from '@tiptap/y-tiptap'

/** A new table is laid out over this width (the editor content width of the reference app), split evenly across its columns. */
export const TABLE_DEFAULT_WIDTH = 780
/** Narrowest default column: tables with many columns grow wider than the default width instead of shrinking cells further. */
export const TABLE_MIN_DEFAULT_COLUMN = 90
/** Width of a column inserted into a table that no longer has room for an even split. */
export const TABLE_NEW_COLUMN_WIDTH = 200

export const tableWidthsKey = new PluginKey('flowTableDefaultWidths')

/** Even split of the default width, never narrower than the minimum column. */
export function defaultColumnWidth(columns: number) {
  return Math.max(TABLE_MIN_DEFAULT_COLUMN, Math.floor(TABLE_DEFAULT_WIDTH / Math.max(1, columns)))
}

/** The width recorded for each column (the first cell that has one), null where no cell of the column has a width. */
export function recordedColumnWidths(table: ProseMirrorNode): Array<number | null> {
  const map = TableMap.get(table)
  const widths: Array<number | null> = Array<number | null>(map.width).fill(null)
  for (let row = 0; row < map.height; row++) {
    for (let col = 0; col < map.width; col++) {
      if (widths[col] !== null) continue
      const cell = table.nodeAt(map.map[row * map.width + col])
      const colwidth = cell?.attrs.colwidth as number[] | null | undefined
      if (!Array.isArray(colwidth)) continue
      // A spanning cell lists one width per spanned column, starting at the column where it begins.
      const left = map.colCount(map.map[row * map.width + col])
      const value = colwidth[col - left]
      if (value > 0) widths[col] = value
    }
  }
  return widths
}

/**
 * The width every column should have, or null when each column already has one. Like the reference app: a table without widths
 * (a new table, a paste) gets an even split of the default width; a column added to an evenly split table that fits the default width makes all
 * columns share it evenly as long as each stays at least the new-column width, otherwise the added column gets that width and
 * the others keep theirs.
 */
export function planColumnWidths(table: ProseMirrorNode): number[] | null {
  const recorded = recordedColumnWidths(table)
  const missing = recorded.filter(width => width === null).length
  if (missing === 0 || recorded.length === 0) return null
  if (missing === recorded.length) return recorded.map(() => defaultColumnWidth(recorded.length))
  const known = recorded.filter((width): width is number => width !== null)
  const share = Math.floor(TABLE_DEFAULT_WIDTH / recorded.length)
  if (missing === 1 && known.length >= 2 && share >= TABLE_NEW_COLUMN_WIDTH && known.every(width => width === known[0]) && known.reduce((sum, width) => sum + width, 0) <= TABLE_DEFAULT_WIDTH) return recorded.map(() => share)
  return recorded.map(width => width ?? TABLE_NEW_COLUMN_WIDTH)
}

/** Writes `widths` onto the cells of the table at `pos`; false when nothing changed. */
function applyColumnWidths(tr: Transaction, pos: number, widths: number[]) {
  const table = tr.doc.nodeAt(pos)
  if (!table) return false
  const map = TableMap.get(table)
  let changed = false
  const seen = new Set<number>()
  for (let index = 0; index < map.map.length; index++) {
    const offset = map.map[index]
    if (seen.has(offset)) continue
    seen.add(offset)
    const cell = table.nodeAt(offset)
    if (!cell) continue
    const left = index % map.width
    const colspan = (cell.attrs.colspan as number) || 1
    const colwidth = widths.slice(left, left + colspan)
    const current = cell.attrs.colwidth as number[] | null
    if (current && current.length === colwidth.length && current.every((value, i) => value === colwidth[i])) continue
    tr.setNodeMarkup(pos + 1 + offset, undefined, { ...cell.attrs, colwidth })
    changed = true
  }
  return changed
}

/** Ranges of the final document that the transactions touched. */
function changedRanges(transactions: readonly Transaction[]) {
  let ranges: Array<[number, number]> = []
  for (const tr of transactions) {
    for (const step of tr.steps) {
      const map = step.getMap()
      ranges = ranges.map(([from, to]) => [map.map(from, -1), map.map(to, 1)])
      map.forEach((_oldStart, _oldEnd, newStart, newEnd) => ranges.push([newStart, newEnd]))
    }
  }
  return ranges
}

/**
 * Every table gets a width on every column, like the reference app: when a table is created (`/table`, `|--`), pasted
 * (html without widths, spreadsheet text), or has a column inserted. Only local edits are looked at (changes that arrive from
 * collaborators already carry the widths their author set, and loading a stored document never rewrites it), and only the
 * tables an edit touched.
 */
export const TableDefaultWidths = Extension.create({
  name: 'tableDefaultWidths',
  addProseMirrorPlugins() {
    return [new Plugin({
      key: tableWidthsKey,
      appendTransaction: (transactions, _oldState, newState) => {
        if (!transactions.some(tr => tr.docChanged)) return null
        if (transactions.some(tr => tr.getMeta('preventUpdate') || tr.getMeta(ySyncPluginKey)?.isChangeOrigin)) return null
        const positions = new Set<number>()
        for (const [from, to] of changedRanges(transactions)) {
          const end = Math.min(newState.doc.content.size, Math.max(from, to))
          newState.doc.nodesBetween(Math.min(from, end), end, (node, pos) => {
            if (node.type.spec.tableRole === 'table') { positions.add(pos); return false }
            return true
          })
        }
        if (!positions.size) return null
        const tr = newState.tr
        let changed = false
        for (const pos of positions) {
          const table = tr.doc.nodeAt(pos)
          const widths = table && planColumnWidths(table)
          if (widths && applyColumnWidths(tr, pos, widths)) changed = true
        }
        return changed ? tr : null
      },
    })]
  },
})
