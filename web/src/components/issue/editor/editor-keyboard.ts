import type { Editor } from '@tiptap/core'

export function handleEditorSubmit(event: KeyboardEvent, submit?: () => void) {
  if (!(event.metaKey || event.ctrlKey) || event.key !== 'Enter') return false
  event.preventDefault()
  submit?.()
  return true
}

/**
 * Linear's block shortcuts, matched on the physical key (`event.code`) because Shift/Option change `event.key`
 * (⌘⇧7 reports "&", ⌘⌥1 reports "¡" on a Mac keyboard):
 * ⌘⌥0 text, ⌘⌥1/2/3/4 headings, ⌘⇧7 checklist, ⌘⇧8 bulleted, ⌘⇧9 numbered, ⌘⇧6 collapsible, ⌘⇧\ code block, ⌥⇧. quote.
 */
export function handleBlockShortcut(event: KeyboardEvent, editor: Editor | null) {
  if (!editor) return false
  const mod = event.metaKey || event.ctrlKey
  const run = (command: () => void) => { event.preventDefault(); command(); return true }
  if (mod && event.altKey && !event.shiftKey) {
    if (event.code === 'Digit0') return run(() => editor.chain().focus().setParagraph().run())
    const level = ({ Digit1: 1, Digit2: 2, Digit3: 3, Digit4: 4 } as Record<string, 1 | 2 | 3 | 4 | undefined>)[event.code]
    if (level) return run(() => editor.chain().focus().toggleHeading({ level }).run())
  }
  if (mod && event.shiftKey && !event.altKey) {
    if (event.code === 'Digit7') return run(() => editor.chain().focus().toggleTaskList().run())
    if (event.code === 'Digit8') return run(() => editor.chain().focus().toggleBulletList().run())
    if (event.code === 'Digit9') return run(() => editor.chain().focus().toggleOrderedList().run())
    if (event.code === 'Digit6') return run(() => editor.chain().focus().setDetails().run())
    if (event.code === 'Backslash') return run(() => editor.chain().focus().toggleCodeBlock().run())
  }
  if (event.altKey && event.shiftKey && !mod && event.code === 'Period') return run(() => editor.chain().focus().toggleBlockquote().run())
  return false
}
