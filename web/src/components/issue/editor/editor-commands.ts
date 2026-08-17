import type { EditorCommand } from './slash-command-menu'

export function filterEditorCommands(commands: EditorCommand[], query: string) {
  const normalized = query.trim().toLowerCase()
  if (!normalized) return commands
  return commands.filter(command => `${command.label} ${command.keywords ?? ''}`.toLowerCase().includes(normalized))
}
