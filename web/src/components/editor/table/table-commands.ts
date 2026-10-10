import type { Editor } from '@tiptap/core'
import type { Node as ProseMirrorNode } from '@tiptap/pm/model'
import { Selection } from '@tiptap/pm/state'
import { CellSelection, TableMap } from '@tiptap/pm/tables'
import { BetweenHorizontalStart, BetweenVerticalStart, Trash2, type LucideIcon } from 'lucide-react'

/** The Linear default: 3 rows x 2 columns whose first row is a (grey, bold) header row. */
export const DEFAULT_TABLE = { rows: 3, cols: 2, withHeaderRow: true } as const

export function insertDefaultTable(editor: Editor) {
  return editor.chain().focus().insertTable({ ...DEFAULT_TABLE }).run()
}

export type TableLocation = { pos: number; node: ProseMirrorNode }

/** The table around the selection, or null when the selection is not inside one. */
export function tableAroundSelection(editor: Editor): TableLocation | null {
  const { $from } = editor.state.selection
  for (let depth = $from.depth; depth > 0; depth--) {
    const node = $from.node(depth)
    if (node.type.spec.tableRole === 'table') return { pos: $from.before(depth), node }
  }
  return null
}

export function tableAt(editor: Editor, pos: number | undefined): TableLocation | null {
  if (pos === undefined) return null
  const node = editor.state.doc.nodeAt(pos)
  return node && node.type.spec.tableRole === 'table' ? { pos, node } : null
}

/** Document position (before the cell node) of the cell at a row/column of the table at `tablePos`. */
export function cellPosAt(table: TableLocation, row: number, col: number) {
  const map = TableMap.get(table.node)
  const r = Math.max(0, Math.min(row, map.height - 1))
  const c = Math.max(0, Math.min(col, map.width - 1))
  return table.pos + 1 + map.map[r * map.width + c]
}

export type CellTarget = 'keep' | 'first' | 'last'

/**
 * Runs `build` on a command chain whose selection sits in the table at `tablePos`: the current cell when the selection
 * already is in this table and `target` is 'keep', else the first cell / the bottom-right cell. Row and column commands
 * act on the selection, so hover affordances must move it into the table they belong to first.
 */
export function runInTable(editor: Editor, tablePos: number | undefined, target: CellTarget, build: (chain: ReturnType<Editor['chain']>) => ReturnType<Editor['chain']>) {
  const table = tableAt(editor, tablePos)
  if (!table || !editor.isEditable) return false
  const around = tableAroundSelection(editor)
  const inside = around?.pos === table.pos
  const chain = editor.chain()
  if (!(inside && target === 'keep')) {
    const map = TableMap.get(table.node)
    const pos = target === 'last' ? cellPosAt(table, map.height - 1, map.width - 1) : cellPosAt(table, 0, 0)
    chain.command(({ tr }) => {
      tr.setSelection(Selection.near(tr.doc.resolve(pos + 1), 1))
      return true
    })
  }
  const ran = build(chain).run()
  // Synchronous (Tiptap's chain.focus() defers to a frame, which would steal focus from a menu opened right after).
  editor.view.focus()
  return ran
}

/** Selects every cell of the table (the grey "selectedCell" tint shows the selection). */
export function selectWholeTable(editor: Editor, tablePos: number | undefined) {
  const table = tableAt(editor, tablePos)
  if (!table) return false
  const map = TableMap.get(table.node)
  const selection = CellSelection.create(editor.state.doc, cellPosAt(table, 0, 0), cellPosAt(table, map.height - 1, map.width - 1))
  editor.view.dispatch(editor.state.tr.setSelection(selection))
  editor.view.focus()
  return true
}

/** The table as GFM markdown (falls back to tab separated text if the markdown manager is unavailable). */
export function tableToText(editor: Editor, node: ProseMirrorNode) {
  try {
    const markdown = editor.markdown?.serialize({ type: 'doc', content: [node.toJSON()] })
    if (markdown?.trim()) return markdown.trim()
  } catch { /* fall through to plain text */ }
  const rows: string[] = []
  node.forEach(row => {
    const cells: string[] = []
    row.forEach(cell => cells.push(cell.textContent))
    rows.push(cells.join('\t'))
  })
  return rows.join('\n')
}

export function deleteWholeTable(editor: Editor, tablePos: number | undefined) {
  const table = tableAt(editor, tablePos)
  if (!table || !editor.isEditable) return false
  return runInTable(editor, tablePos, 'first', chain => chain.deleteTable())
}

export type TableSlashCommand = { id: string; label: string; keywords?: string; icon: LucideIcon; run: () => void }

/** Slash-menu items for editing the table around the caret. Empty outside tables. */
export function tableSlashCommands(editor: Editor): TableSlashCommand[] {
  if (!editor.isEditable || !tableAroundSelection(editor)) return []
  return [
    { id: 'table-insert-row', label: 'Insert row', keywords: 'table row add below', icon: BetweenHorizontalStart, run: () => void editor.chain().focus().addRowAfter().run() },
    { id: 'table-insert-column', label: 'Insert column', keywords: 'table column add right', icon: BetweenVerticalStart, run: () => void editor.chain().focus().addColumnAfter().run() },
    { id: 'table-delete-row', label: 'Delete row', keywords: 'table row remove', icon: Trash2, run: () => void editor.chain().focus().deleteRow().run() },
    { id: 'table-delete-column', label: 'Delete column', keywords: 'table column remove', icon: Trash2, run: () => void editor.chain().focus().deleteColumn().run() },
    { id: 'table-delete-table', label: 'Delete table', keywords: 'table remove', icon: Trash2, run: () => void editor.chain().focus().deleteTable().run() },
  ]
}
