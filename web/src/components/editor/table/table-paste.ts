import { Extension } from '@tiptap/core'
import { Fragment, Slice, type Node as ProseMirrorNode, type Schema } from '@tiptap/pm/model'
import { Plugin, PluginKey, Selection, type EditorState } from '@tiptap/pm/state'
import { tableNodeTypes } from '@tiptap/pm/tables'
import type { EditorView } from '@tiptap/pm/view'
import { isPlainTextWrapperHtml, normalizeNewlines } from '../markdown-paste/markdown-detect'

export const tablePasteKey = new PluginKey('flowTablePaste')

/** Beyond this the clipboard text is left to the default paste: building a huge table would freeze the editor. */
const MAX_TABULAR_PASTE = 200_000

function selectionInTable(state: EditorState) {
  const { $from, $to } = state.selection
  for (const $pos of [$from, $to]) {
    for (let depth = $pos.depth; depth > 0; depth--) {
      const role = $pos.node(depth).type.spec.tableRole
      if (role === 'cell' || role === 'header_cell' || role === 'table') return true
    }
  }
  return false
}

function selectionInCode(state: EditorState) {
  const { $from } = state.selection
  for (let depth = $from.depth; depth >= 0; depth--) if ($from.node(depth).type.spec.code) return true
  return false
}

function unquote(cell: string) {
  return cell.length > 1 && cell.startsWith('"') && cell.endsWith('"') ? cell.slice(1, -1).replace(/""/g, '"') : cell
}

/**
 * Rows of tab separated clipboard text (what a spreadsheet puts on the clipboard next to its html), or null when the text is
 * not a grid: at least two lines, every line holding a tab. A single line of text with a tab stays text.
 */
export function parseTabularText(input: string): string[][] | null {
  const text = normalizeNewlines(input).replace(/\n+$/, '')
  const lines = text.split('\n')
  if (lines.length < 2 || !lines.every(line => line.includes('\t'))) return null
  const rows = lines.map(line => line.split('\t').map(unquote))
  const width = Math.max(...rows.map(row => row.length))
  return rows.map(row => [...row, ...Array<string>(width - row.length).fill('')])
}

/** A table node from rows of text; the first row is the header row, as for `/table` and the default table. */
export function tableFromRows(schema: Schema, rows: string[][]): ProseMirrorNode {
  const types = tableNodeTypes(schema)
  return types.table.create(null, rows.map((cells, index) => types.row.create(null, cells.map(text => {
    const paragraph = schema.nodes.paragraph.create(null, text ? schema.text(text) : undefined)
    return (index === 0 ? types.header_cell : types.cell).create(null, paragraph)
  }))))
}

/** A pasted table whose first row has no header cells (spreadsheets copy plain `td`s) gets a header row. */
export function withHeaderRow(slice: Slice): Slice {
  let changed = false
  const visit = (node: ProseMirrorNode): ProseMirrorNode => {
    if (node.type.spec.tableRole === 'table' && node.childCount > 0) {
      const first = node.child(0)
      let hasHeader = false
      first.forEach(cell => { if (cell.type.spec.tableRole === 'header_cell') hasHeader = true })
      if (hasHeader) return node
      const header = tableNodeTypes(node.type.schema).header_cell
      const cells: ProseMirrorNode[] = []
      first.forEach(cell => cells.push(header.create(cell.attrs, cell.content, cell.marks)))
      changed = true
      return node.copy(node.content.replaceChild(0, first.copy(Fragment.from(cells))))
    }
    if (node.isLeaf || !node.childCount) return node
    const children: ProseMirrorNode[] = []
    node.forEach(child => children.push(visit(child)))
    return node.copy(Fragment.from(children))
  }
  const content: ProseMirrorNode[] = []
  slice.content.forEach(node => content.push(visit(node)))
  return changed ? new Slice(Fragment.from(content), slice.openStart, slice.openEnd) : slice
}

function insertTable(view: EditorView, table: ProseMirrorNode) {
  const { state } = view
  const { $from } = state.selection
  const start = $from.parent.content.size === 0 ? $from.before() : $from.after()
  const tr = state.tr.replaceSelectionWith(table, false)
  let tablePos = -1
  tr.doc.nodesBetween(Math.min(start, tr.doc.content.size), tr.doc.content.size, (node, pos) => {
    if (tablePos >= 0) return false
    if (node.type.spec.tableRole === 'table' && pos >= start) { tablePos = pos; return false }
    return true
  })
  if (tablePos >= 0) tr.setSelection(Selection.near(tr.doc.resolve(tablePos + 3), 1))
  view.dispatch(tr.scrollIntoView().setMeta('paste', true).setMeta('uiEvent', 'paste'))
}

/**
 * Pasting a spreadsheet: the html `<table>` (Excel, Google Sheets, Numbers, a web table) becomes a table whose first row is
 * the header row, and so does tab separated plain text spanning several lines. Pasting into an existing table's cells,
 * into code, as plain text (Shift) or with files is left to the stock handlers, and a table copied from this editor
 * (or a copied row without header cells) keeps exactly the header cells it had.
 *
 * Priority 200 puts the plain-text branch ahead of `MarkdownPaste` (150) so tab separated text is never read as Markdown.
 */
export const TablePaste = Extension.create({
  name: 'tablePaste',
  priority: 200,
  addProseMirrorPlugins() {
    let promoteHeader = false
    return [new Plugin({
      key: tablePasteKey,
      props: {
        transformPastedHTML: html => {
          promoteHeader = /<table[\s>]/i.test(html) && !/data-pm-slice|data-headerless/i.test(html)
          return html
        },
        transformPasted: (slice, view) => {
          const promote = promoteHeader
          promoteHeader = false
          if (!promote || selectionInTable(view.state)) return slice
          return withHeaderRow(slice)
        },
        handlePaste: (view, event) => {
          const data = event.clipboardData
          if (!data || !view.editable || data.files?.length) return false
          if ((view as unknown as { input?: { shiftKey?: boolean } }).input?.shiftKey) return false
          if (selectionInTable(view.state) || selectionInCode(view.state)) return false
          const html = data.getData('text/html')
          if (html.trim() && !isPlainTextWrapperHtml(html)) return false
          const text = data.getData('text/plain')
          if (!text || text.length > MAX_TABULAR_PASTE) return false
          const rows = parseTabularText(text)
          if (!rows) return false
          insertTable(view, tableFromRows(view.state.schema, rows))
          event.preventDefault()
          return true
        },
      },
    })]
  },
})
