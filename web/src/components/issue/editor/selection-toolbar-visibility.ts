import type { Editor } from '@tiptap/react'

/**
 * Whether the text-formatting toolbar floats over the current selection: a non-empty range of text, but not inside a code
 * block and not a selected embed (a video or file card has its own hover toolbar and nothing to format).
 */
export function showsSelectionToolbar({ from, to, editor }: { from: number; to: number; editor: Editor }) {
  return from !== to && !editor.isActive('codeBlock') && !editor.isActive('embed')
}
