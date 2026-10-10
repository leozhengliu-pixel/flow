import type { EditorCommand } from './slash-command-menu'

/** Linear's slash menu order: each group is separated by a divider; "table" entries only show inside a table. */
export const slashGroupOrder = ['table', 'headings', 'lists', 'media', 'blocks'] as const
export type SlashGroup = typeof slashGroupOrder[number]

/**
 * Without a query only the regular entries show; the search-only ones (Table, Divider) join once something is typed.
 * Entries keep their group order so the menu can draw a divider whenever the group changes.
 */
export function filterEditorCommands(commands: EditorCommand[], query: string) {
  const normalized = query.trim().toLowerCase()
  const ordered = [...commands].sort((a, b) => slashGroupOrder.indexOf(a.group) - slashGroupOrder.indexOf(b.group))
  if (!normalized) return ordered.filter(command => !command.searchOnly)
  return ordered.filter(command => `${command.label} ${command.keywords ?? ''}`.toLowerCase().includes(normalized))
}
