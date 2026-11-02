import type { Editor } from '@tiptap/core'
import { closeHistory } from '@tiptap/pm/history'
import type { Transaction } from '@tiptap/pm/state'
import { yUndoPluginKey } from '@tiptap/y-tiptap'
import type { SelectedDescription } from '@/components/issue/editor/structured-blocks'

/** Track the original content while creation is pending; selection changes are independent. */
export async function moveDocumentSelection<T>(editor: Editor, selection: SelectedDescription, create: () => Promise<T>) {
  let { from, to } = selection
  const original = editor.state.doc.slice(from, to)
  let changed = false
  const onTransaction = ({ transaction }: { transaction: Transaction }) => {
    for (const map of transaction.mapping.maps) {
      map.forEach((start, end) => {
        if ((start < to && end > from) || (start === end && start > from && start < to)) changed = true
      })
      from = map.map(from, 1)
      to = map.map(to, -1)
    }
  }
  editor.on('transaction', onTransaction)
  try {
    const created = await create()
    if (editor.isDestroyed || !editor.isEditable || changed || from >= to || !editor.state.doc.slice(from, to).eq(original)) return { created, removed: false }
    editor.off('transaction', onTransaction)
    const undoManager = yUndoPluginKey.getState(editor.state)?.undoManager
    undoManager?.stopCapturing()
    editor.view.dispatch(closeHistory(editor.state.tr).deleteRange(from, to))
    editor.view.dispatch(closeHistory(editor.state.tr))
    undoManager?.stopCapturing()
    return { created, removed: true }
  } finally {
    editor.off('transaction', onTransaction)
  }
}
