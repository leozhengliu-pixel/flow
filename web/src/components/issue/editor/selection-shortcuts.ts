import type { DescriptionSelectionActions } from './structured-blocks'

/**
 * With text selected, ⌘J asks the agent about it and ⌘⇧O makes an issue from it (the floating toolbar's two actions).
 * Returns true when the key was taken, which also keeps the app-wide ⌘J (toggle the chat) from firing.
 */
export function handleSelectionShortcut(event: KeyboardEvent, selection: { from: number; to: number }, doc: { textBetween: (from: number, to: number, separator: string) => string }, actions?: DescriptionSelectionActions) {
  if (!(event.metaKey || event.ctrlKey) || event.altKey || selection.from === selection.to) return false
  const key = event.key.toLowerCase()
  const ask = key === 'j' && !event.shiftKey ? actions?.onAskAgent : key === 'o' && event.shiftKey ? actions?.onCreateIssue : undefined
  if (!ask) return false
  event.preventDefault()
  ask({ from: selection.from, to: selection.to, text: doc.textBetween(selection.from, selection.to, '\n') })
  return true
}
