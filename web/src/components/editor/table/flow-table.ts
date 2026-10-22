import { InputRule, mergeAttributes } from '@tiptap/core'
import { Table, TableCell, TableHeader, TableRow } from '@tiptap/extension-table'
import { FlowTableView } from './flow-table-view'
import { DEFAULT_TABLE } from './table-commands'
import { moveSelectedLine } from './table-lines'
import { TablePaste } from './table-paste'

/**
 * Table with the Flow hover chrome (add row / add column, table actions, row and column menus, block handle). Column
 * resizing keeps working: the view wraps Tiptap's TableView DOM, which the prosemirror-tables resizing plugin expects.
 */
export const FlowTable = Table.extend({
  addNodeView() {
    return ({ node, view, getPos, HTMLAttributes, editor }) =>
      new FlowTableView(node, this.options.cellMinWidth, view, mergeAttributes(this.options.HTMLAttributes, HTMLAttributes), editor, getPos)
  },

  addInputRules() {
    return [
      ...(this.parent?.() ?? []),
      // Typing `|--` at the start of an empty paragraph becomes the default table, like `/table`.
      new InputRule({
        find: /^\|--$/,
        undoable: false,
        handler: ({ state, range, chain }) => {
          // Tiptap has already inserted the typed character, so the paragraph reads exactly "|--" (nothing else, before or after).
          const parent = state.doc.resolve(range.from).parent
          if (parent.type.name !== 'paragraph' || parent.textContent !== '|--') return null
          chain().deleteRange(range).insertTable({ ...DEFAULT_TABLE }).run()
          return undefined
        },
      }),
    ]
  },

  addKeyboardShortcuts() {
    const move = (axis: 'row' | 'column', direction: 'before' | 'after') => () => moveSelectedLine(this.editor, axis, direction)
    return {
      ...(this.parent?.() ?? {}),
      'Mod-Alt-Shift-ArrowUp': move('row', 'before'),
      'Mod-Alt-Shift-ArrowDown': move('row', 'after'),
      'Mod-Alt-Shift-ArrowLeft': move('column', 'before'),
      'Mod-Alt-Shift-ArrowRight': move('column', 'after'),
    }
  },
})

export const FlowTableHeader = TableHeader.configure({ HTMLAttributes: { class: 'flow-table-header' } })
export const FlowTableCell = TableCell.configure({ HTMLAttributes: { class: 'flow-table-cell' } })

/** Spread this where `TableKit.configure({ table: { resizable: true } })` was used. */
export const flowTableKit = [FlowTable.configure({ resizable: true }), TableRow, FlowTableHeader, FlowTableCell, TablePaste]
