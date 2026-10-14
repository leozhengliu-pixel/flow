import type { Editor } from '@tiptap/core'
import type { DescriptionSelectionActions } from './structured-blocks'

/**
 * With text selected, ⌘J asks the agent about it, ⌘⇧O makes an issue from it and ⌘⌥M comments on it (the floating
 * toolbar's actions). Returns true when the key was taken, which also keeps the app-wide ⌘J (toggle the chat) from firing.
 */
export function handleSelectionShortcut(event: KeyboardEvent, selection: { from: number; to: number }, doc: { textBetween: (from: number, to: number, separator: string) => string }, actions?: DescriptionSelectionActions, editor?: Editor | null) {
  if (!(event.metaKey || event.ctrlKey) || selection.from === selection.to) return false
  const key = event.key.toLowerCase()
  // ⌥ changes `event.key` on a Mac (⌥M reports "µ"), so the comment shortcut matches the physical key.
  const ask = event.altKey
    ? event.code === 'KeyM' && !event.shiftKey ? actions?.onComment : undefined
    : key === 'j' && !event.shiftKey ? actions?.onAskAgent : key === 'o' && event.shiftKey ? actions?.onCreateIssue : undefined
  if (!ask) return false
  event.preventDefault()
  const picked = { from: selection.from, to: selection.to, text: doc.textBetween(selection.from, selection.to, '\n') }
  if (editor) ask(picked, editor)
  else ask(picked)
  return true
}
