import { Extension, type Editor } from '@tiptap/core'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import { Decoration, DecorationSet } from '@tiptap/pm/view'
import { lineCellPositions, lineCount, type TableLine } from './table-lines'

export const tableLineHighlightKey = new PluginKey<TableLine | null>('flowTableLineHighlight')

/** Tints the cells of the row / column whose menu is open without touching the selection (the selection only moves when a command runs). */
export function setLineHighlight(editor: Editor, line: TableLine | null) {
  editor.view.dispatch(editor.state.tr.setMeta(tableLineHighlightKey, line ?? false).setMeta('addToHistory', false))
}

export const TableLineHighlight = Extension.create({
  name: 'tableLineHighlight',
  addProseMirrorPlugins() {
    return [new Plugin<TableLine | null>({
      key: tableLineHighlightKey,
      state: {
        init: () => null,
        apply: (tr, value) => {
          const meta = tr.getMeta(tableLineHighlightKey) as TableLine | false | undefined
          if (meta !== undefined) return meta || null
          return value && tr.docChanged ? { ...value, tablePos: tr.mapping.map(value.tablePos) } : value
        },
      },
      props: {
        decorations: state => {
          const line = tableLineHighlightKey.getState(state)
          const node = line ? state.doc.nodeAt(line.tablePos) : null
          if (!line || !node || node.type.spec.tableRole !== 'table') return null
          const table = { pos: line.tablePos, node }
          const count = lineCount(table, line.axis)
          if (line.index >= count) return null
          return DecorationSet.create(state.doc, lineCellPositions(table, line.axis, line.index).flatMap(pos => {
            const cell = state.doc.nodeAt(pos)
            return cell ? [Decoration.node(pos, pos + cell.nodeSize, { class: 'flow-table-line-active' })] : []
          }))
        },
      },
    })]
  },
})
