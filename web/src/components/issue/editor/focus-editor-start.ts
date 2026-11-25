import type { Editor } from '@tiptap/react'
import { Selection } from '@tiptap/pm/state'

// Puts the caret at the start of the editor and focuses it in the same task. The editor's own `focus` command waits for the
// next animation frame, so keys typed right after Enter in a title (fast typing, or a tab that gets no frames) still went
// into the title.
export function focusEditorStart(editor: Editor | null) {
  if (!editor || editor.isDestroyed) return
  editor.view.dispatch(editor.state.tr.setSelection(Selection.atStart(editor.state.doc)).scrollIntoView())
  editor.view.focus()
}
