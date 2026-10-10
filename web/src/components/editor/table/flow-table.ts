import { mergeAttributes } from '@tiptap/core'
import { Table, TableCell, TableHeader, TableRow } from '@tiptap/extension-table'
import { FlowTableView } from './flow-table-view'

/**
 * Table with the Flow hover chrome (add row / add column, table actions, block handle). Column resizing keeps working:
 * the view wraps Tiptap's TableView DOM, which the prosemirror-tables resizing plugin expects.
 */
export const FlowTable = Table.extend({
  addNodeView() {
    return ({ node, view, getPos, HTMLAttributes, editor }) =>
      new FlowTableView(node, this.options.cellMinWidth, view, mergeAttributes(this.options.HTMLAttributes, HTMLAttributes), editor, getPos)
  },
})

export const FlowTableHeader = TableHeader.configure({ HTMLAttributes: { class: 'flow-table-header' } })
export const FlowTableCell = TableCell.configure({ HTMLAttributes: { class: 'flow-table-cell' } })

/** Spread this where `TableKit.configure({ table: { resizable: true } })` was used. */
export const flowTableKit = [FlowTable.configure({ resizable: true }), TableRow, FlowTableHeader, FlowTableCell]
