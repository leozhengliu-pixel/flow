import { InputRule, mergeAttributes } from '@tiptap/core'
import { Table, TableCell, TableHeader, TableRow } from '@tiptap/extension-table'
import { FlowTableView } from './flow-table-view'
import { DEFAULT_TABLE } from './table-commands'
import { TableLineHighlight } from './table-line-highlight'
import { moveSelectedLine } from './table-lines'
import { TablePaste } from './table-paste'
import { TableDefaultWidths } from './table-widths'

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

/**
 * Column widths travel as `data-colwidth` on the cells, like the reference app writes them (Tiptap's own `colwidth` attribute is
 * still read, so older clipboard html and stored content keep their widths). One value per spanned column, comma separated.
 */
const colwidthAttribute = {
  default: null,
  parseHTML: (element: HTMLElement) => {
    const raw = element.getAttribute('data-colwidth') ?? element.getAttribute('colwidth')
    const widths = raw ? raw.split(',').map(Number) : []
    return widths.length && widths.every(width => Number.isFinite(width) && width >= 0) && widths.some(width => width > 0) ? widths : null
  },
  renderHTML: (attributes: Record<string, unknown>) => {
    const widths = attributes.colwidth
    return Array.isArray(widths) && widths.length ? { 'data-colwidth': widths.join(',') } : {}
  },
}

export const FlowTableHeader = TableHeader.extend({
  addAttributes() { return { ...this.parent?.(), colwidth: colwidthAttribute } },
}).configure({ HTMLAttributes: { class: 'flow-table-header' } })
export const FlowTableCell = TableCell.extend({
  addAttributes() { return { ...this.parent?.(), colwidth: colwidthAttribute } },
}).configure({ HTMLAttributes: { class: 'flow-table-cell' } })

/** Spread this where `TableKit.configure({ table: { resizable: true } })` was used. */
export const flowTableKit = [FlowTable.configure({ resizable: true }), TableRow, FlowTableHeader, FlowTableCell, TablePaste, TableLineHighlight, TableDefaultWidths]
