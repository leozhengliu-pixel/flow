import type { Editor } from '@tiptap/core'
import { DOMSerializer, Fragment, type MarkType, type Node as ProseMirrorNode } from '@tiptap/pm/model'
import { Selection, type Transaction } from '@tiptap/pm/state'
import { CellSelection, TableMap, addColumn, addRow, removeColumn, removeRow, rowIsHeader, selectedRect, type TableRect } from '@tiptap/pm/tables'
import { tableAroundSelection, tableAt, type TableLocation } from './table-commands'

/**
 * Row / column transforms behind the per-row and per-column menus. Every operation takes an explicit table position and
 * line index (so a hover handle can act on a line the selection is not in), builds ONE transaction and dispatches it:
 * one undo step, and a normal transaction for the collaborative (Yjs) binding.
 */
export type TableAxis = 'row' | 'column'
export type TableLine = { tablePos: number; axis: TableAxis; index: number }
export type LineMark = 'bold' | 'italic' | 'underline' | 'strike'
export type MoveDirection = 'before' | 'after'

export function lineCount(table: TableLocation, axis: TableAxis) {
  const map = TableMap.get(table.node)
  return axis === 'row' ? map.height : map.width
}

/** The header row: the first row when it is made of header cells. It stays first; nothing is inserted above it and it is never moved, duplicated or deleted. */
export function isPinnedHeader(table: TableLocation, axis: TableAxis, index: number) {
  if (axis !== 'row' || index !== 0) return false
  return rowIsHeader(TableMap.get(table.node), table.node, 0)
}

/**
 * What a line's menu offers; unavailable commands are left out of the menu rather than disabled (measured on the reference
 * app). The header row only offers Insert below, Format and Copy text. A column (header cell or not) offers everything, Sort
 * included even with a single body row, and Delete is left out of a row menu when it would remove the last body row.
 */
export function lineAvailability(table: TableLocation, axis: TableAxis, index: number) {
  const count = lineCount(table, axis)
  const pinned = isPinnedHeader(table, axis, index)
  const headerFirst = isPinnedHeader(table, 'row', 0)
  const moveBefore = !pinned && index > 0 && !(axis === 'row' && index === 1 && headerFirst)
  const moveAfter = !pinned && index < count - 1
  return {
    insertBefore: !pinned,
    insertAfter: true,
    moveBefore,
    moveAfter,
    /** The reference app draws a divider before Duplicate when only "move before" is offered (the last row / column). */
    separateDuplicate: moveBefore && !moveAfter,
    duplicate: !pinned,
    sort: axis === 'column',
    delete: !pinned && (axis === 'column' || count - (headerFirst ? 1 : 0) > 1),
  }
}

function rectOf(table: TableLocation): TableRect {
  const map = TableMap.get(table.node)
  return { map, table: table.node, tableStart: table.pos + 1, left: 0, top: 0, right: map.width, bottom: map.height }
}

/** Absolute positions (before each cell) of the cells of a row / column. */
export function lineCellPositions(table: TableLocation, axis: TableAxis, index: number) {
  const map = TableMap.get(table.node)
  const positions: number[] = []
  if (axis === 'row') for (let col = 0; col < map.width; col++) push(map.map[index * map.width + col])
  else for (let row = 0; row < map.height; row++) push(map.map[row * map.width + index])
  function push(offset: number) {
    const pos = table.pos + 1 + offset
    if (!positions.includes(pos)) positions.push(pos)
  }
  return positions
}

function lineSelection(doc: ProseMirrorNode, table: TableLocation, axis: TableAxis, index: number) {
  const cells = lineCellPositions(table, axis, index)
  return new CellSelection(doc.resolve(cells[0]), doc.resolve(cells[cells.length - 1]))
}

function currentTable(editor: Editor, line: TableLine) {
  if (!editor.isEditable) return null
  const table = tableAt(editor, line.tablePos)
  if (!table || line.index < 0 || line.index >= lineCount(table, line.axis)) return null
  return table
}

/** Re-reads the table at `pos` from the transaction's document (positions shift as the transaction edits it). */
function tableInTransaction(tr: Transaction, pos: number): TableLocation | null {
  const node = tr.doc.nodeAt(pos)
  return node && node.type.spec.tableRole === 'table' ? { pos, node } : null
}

function caretInCell(tr: Transaction, table: TableLocation, row: number, col: number) {
  const map = TableMap.get(table.node)
  const r = Math.max(0, Math.min(row, map.height - 1))
  const c = Math.max(0, Math.min(col, map.width - 1))
  const cellPos = table.pos + 1 + map.map[r * map.width + c]
  tr.setSelection(Selection.near(tr.doc.resolve(cellPos + 1), 1))
}

function dispatchLine(editor: Editor, line: TableLine, build: (tr: Transaction, table: TableLocation) => boolean) {
  const table = currentTable(editor, line)
  if (!table) return false
  const tr = editor.state.tr
  if (!build(tr, table)) return false
  editor.view.dispatch(tr.scrollIntoView())
  return true
}

/** Selects the whole row / column (the grey cell tint marks it, and Format / Copy / Delete then act on it). */
export function selectLine(editor: Editor, line: TableLine) {
  const table = currentTable(editor, line)
  if (!table) return false
  editor.view.dispatch(editor.state.tr.setSelection(lineSelection(editor.state.doc, table, line.axis, line.index)))
  return true
}

export function insertLine(editor: Editor, line: TableLine, side: MoveDirection) {
  return dispatchLine(editor, line, (tr, table) => {
    if (side === 'before' && isPinnedHeader(table, line.axis, line.index)) return false
    const at = side === 'before' ? line.index : line.index + 1
    if (line.axis === 'row') addRow(tr, rectOf(table), at)
    else addColumn(tr, rectOf(table), at)
    const next = tableInTransaction(tr, table.pos)
    if (next) caretInCell(tr, next, line.axis === 'row' ? at : 0, line.axis === 'row' ? 0 : at)
    return true
  })
}

/** Swaps the line with its neighbour; the moved line stays selected. */
export function moveLine(editor: Editor, line: TableLine, direction: MoveDirection) {
  return dispatchLine(editor, line, (tr, table) => {
    const target = line.index + (direction === 'before' ? -1 : 1)
    const available = lineAvailability(table, line.axis, line.index)
    if (!(direction === 'before' ? available.moveBefore : available.moveAfter) || target < 0 || target >= lineCount(table, line.axis)) return false
    const first = Math.min(line.index, target)
    const map = TableMap.get(table.node)
    if (line.axis === 'row') {
      let start = table.pos + 1
      for (let row = 0; row < first; row++) start += table.node.child(row).nodeSize
      const upper = table.node.child(first)
      const lower = table.node.child(first + 1)
      tr.replaceWith(start, start + upper.nodeSize + lower.nodeSize, Fragment.from([lower, upper]))
    } else {
      // Bottom-up so the positions of the rows still to be edited do not move.
      for (let row = map.height - 1; row >= 0; row--) {
        const left = map.map[row * map.width + first]
        const right = map.map[row * map.width + first + 1]
        const leftNode = table.node.nodeAt(left)!
        const rightNode = table.node.nodeAt(right)!
        const start = table.pos + 1 + left
        tr.replaceWith(start, table.pos + 1 + right + rightNode.nodeSize, Fragment.from([rightNode, leftNode]))
      }
    }
    const next = tableInTransaction(tr, table.pos)
    if (next) tr.setSelection(lineSelection(tr.doc, next, line.axis, target))
    return true
  })
}

/** Copies the line right after itself and selects the copy. The header row cannot be duplicated (its menu has no Duplicate). */
export function duplicateLine(editor: Editor, line: TableLine) {
  return dispatchLine(editor, line, (tr, table) => {
    if (isPinnedHeader(table, line.axis, line.index)) return false
    const map = TableMap.get(table.node)
    if (line.axis === 'row') {
      let end = table.pos + 1
      for (let row = 0; row <= line.index; row++) end += table.node.child(row).nodeSize
      const source = table.node.child(line.index)
      tr.insert(end, source.copy(source.content))
    } else {
      for (let row = map.height - 1; row >= 0; row--) {
        const offset = map.map[row * map.width + line.index]
        const cell = table.node.nodeAt(offset)!
        tr.insert(table.pos + 1 + offset + cell.nodeSize, cell)
      }
    }
    const next = tableInTransaction(tr, table.pos)
    if (next) tr.setSelection(lineSelection(tr.doc, next, line.axis, line.index + 1))
    return true
  })
}

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })

function numericValue(text: string) {
  const trimmed = text.trim().replace(/,/g, '')
  if (!trimmed || !/^[-+]?(\d+\.?\d*|\.\d+)(e[-+]?\d+)?$/i.test(trimmed)) return null
  const value = Number(trimmed)
  return Number.isFinite(value) ? value : null
}

/** Numbers compare as numbers, everything else case-insensitively with embedded digits in numeric order. Empty cells sort first. */
export function compareCellText(a: string, b: string) {
  const left = numericValue(a)
  const right = numericValue(b)
  if (left !== null && right !== null) return left - right
  return collator.compare(a.trim(), b.trim())
}

/** Sorts the body rows by one column (stable; a header row stays on top). */
export function sortColumn(editor: Editor, line: TableLine, direction: 'asc' | 'desc') {
  return dispatchLine(editor, { ...line, axis: 'column' }, (tr, table) => {
    const map = TableMap.get(table.node)
    const firstBody = isPinnedHeader(table, 'row', 0) ? 1 : 0
    if (map.height - firstBody < 2) return false
    const rows: Array<{ node: ProseMirrorNode; key: string }> = []
    for (let row = firstBody; row < map.height; row++) {
      const cell = table.node.nodeAt(map.map[row * map.width + line.index])
      rows.push({ node: table.node.child(row), key: cell ? cell.textBetween(0, cell.content.size, '\n', ' ') : '' })
    }
    const sorted = [...rows].sort((a, b) => direction === 'asc' ? compareCellText(a.key, b.key) : compareCellText(b.key, a.key))
    if (sorted.every((entry, index) => entry.node === rows[index].node)) return false
    let start = table.pos + 1
    for (let row = 0; row < firstBody; row++) start += table.node.child(row).nodeSize
    tr.replaceWith(start, table.pos + table.node.nodeSize - 1, Fragment.from(sorted.map(entry => entry.node)))
    const next = tableInTransaction(tr, table.pos)
    if (next) tr.setSelection(lineSelection(tr.doc, next, 'column', line.index))
    return true
  })
}

function markType(editor: Editor, name: LineMark): MarkType | undefined {
  return editor.schema.marks[name]
}

/** Toggles one mark on all text of the line: when any of it already carries the mark it is removed everywhere, else added everywhere. */
export function formatLine(editor: Editor, line: TableLine, name: LineMark) {
  const type = markType(editor, name)
  if (!type) return false
  return dispatchLine(editor, line, (tr, table) => {
    const ranges = lineCellPositions(table, line.axis, line.index).map(pos => [pos + 1, pos + table.node.nodeAt(pos - table.pos - 1)!.nodeSize - 1] as const)
    const present = ranges.some(([from, to]) => tr.doc.rangeHasMark(from, to, type))
    for (const [from, to] of ranges) {
      if (present) tr.removeMark(from, to, type)
      else tr.addMark(from, to, type.create())
    }
    tr.setSelection(lineSelection(tr.doc, table, line.axis, line.index))
    return true
  })
}

export function deleteLine(editor: Editor, line: TableLine) {
  return dispatchLine(editor, line, (tr, table) => {
    const count = lineCount(table, line.axis)
    if (count <= 1) {
      tr.delete(table.pos, table.pos + table.node.nodeSize)
      return true
    }
    if (!lineAvailability(table, line.axis, line.index).delete) return false
    if (line.axis === 'row') removeRow(tr, rectOf(table), line.index)
    else removeColumn(tr, rectOf(table), line.index)
    const next = tableInTransaction(tr, table.pos)
    if (next) caretInCell(tr, next, line.axis === 'row' ? Math.min(line.index, count - 2) : 0, line.axis === 'row' ? 0 : Math.min(line.index, count - 2))
    return true
  })
}

/** Plain text (the non-empty cells separated by blank lines, as in the reference app) and an html table of just that line. */
export function lineClipboard(editor: Editor, line: TableLine) {
  const table = currentTable(editor, line)
  if (!table) return null
  const cellText = (cell: ProseMirrorNode) => cell.textBetween(0, cell.content.size, '\n\n', ' ')
  const cells = lineCellPositions(table, line.axis, line.index).map(pos => table.node.nodeAt(pos - table.pos - 1)!)
  const text = cells.map(cellText).filter(value => value !== '').join('\n\n')
  const rowType = table.node.child(0).type
  const rows = line.axis === 'row' ? [rowType.create(null, cells)] : cells.map(cell => rowType.create(null, [cell]))
  const copy = table.node.type.create(table.node.attrs, rows)
  const holder = document.createElement('div')
  holder.appendChild(DOMSerializer.fromSchema(editor.schema).serializeNode(copy))
  const tableElement = holder.querySelector('table')
  // Like the reference app, the markup is the bare table: no column group, no inline min-width.
  tableElement?.querySelector('colgroup')?.remove()
  tableElement?.removeAttribute('style')
  tableElement?.querySelectorAll('td, th').forEach(cell => cell.removeAttribute('class'))
  // A line without header cells must not turn into a header row when it is pasted (see TablePaste).
  if (tableElement && !cells.some(cell => cell.type.spec.tableRole === 'header_cell')) tableElement.setAttribute('data-headerless', '')
  // (Chrome keeps an empty `style=""` after the attribute was removed from an element whose style object was touched.)
  return { text, html: holder.innerHTML.replace(/ style=""/g, '') }
}

export async function copyLineText(editor: Editor, line: TableLine) {
  const clipboard = lineClipboard(editor, line)
  if (!clipboard) return false
  if (typeof ClipboardItem !== 'undefined' && navigator.clipboard?.write) {
    try {
      await navigator.clipboard.write([new ClipboardItem({ 'text/plain': new Blob([clipboard.text], { type: 'text/plain' }), 'text/html': new Blob([clipboard.html], { type: 'text/html' }) })])
      return true
    } catch { /* fall back to plain text */ }
  }
  if (!navigator.clipboard?.writeText) return false
  await navigator.clipboard.writeText(clipboard.text)
  return true
}

/** The row / column of the caret or the cell selection, for the keyboard shortcuts. */
export function lineAtSelection(editor: Editor, axis: TableAxis): TableLine | null {
  const table = tableAroundSelection(editor)
  if (!table) return null
  try {
    const rect = selectedRect(editor.state)
    return { tablePos: table.pos, axis, index: axis === 'row' ? rect.top : rect.left }
  } catch {
    return null
  }
}

export function moveSelectedLine(editor: Editor, axis: TableAxis, direction: MoveDirection) {
  const line = lineAtSelection(editor, axis)
  if (!line) return false
  const table = tableAt(editor, line.tablePos)
  if (!table) return false
  const available = lineAvailability(table, axis, line.index)
  if (!(direction === 'before' ? available.moveBefore : available.moveAfter)) return false
  return moveLine(editor, line, direction)
}
