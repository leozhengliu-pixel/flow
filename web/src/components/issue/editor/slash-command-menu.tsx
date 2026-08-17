import type { LucideIcon } from 'lucide-react'
import { useEffect, useRef } from 'react'

export interface EditorCommand {
  id: string
  group: string
  label: string
  description?: string
  keywords?: string
  shortcut?: string
  icon: LucideIcon
  run: () => void
}

interface SlashCommandMenuProps {
  commands: EditorCommand[]
  selectedIndex: number
  position: { left: number; top: number }
  query: string
  onSelect: (command: EditorCommand) => void
}

export function SlashCommandMenu({ commands, selectedIndex, position, query, onSelect }: SlashCommandMenuProps) {
  const selectedRef = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    selectedRef.current?.scrollIntoView({ block: 'nearest' })
  }, [selectedIndex])

  return <div className="description-slash-menu" style={position} role="listbox" aria-label="Insert block">
    {commands.length === 0 ? <div className="description-slash-empty">No results for “{query}”</div> : groupCommands(commands).map(([group, items]) => <div className="description-command-group" key={group}>
      <div className="description-slash-title">{group}</div>
      {items.map(command => {
        const index = commands.indexOf(command)
        const Icon = command.icon
        return <button ref={index === selectedIndex ? selectedRef : undefined} key={command.id} type="button" role="option" aria-selected={index === selectedIndex} onMouseDown={event => event.preventDefault()} onClick={() => onSelect(command)}>
          <span className="description-command-icon"><Icon size={16}/></span>
          <span className="description-command-copy"><strong>{command.label}</strong>{command.description && <small>{command.description}</small>}</span>
          {command.shortcut && <kbd>{command.shortcut}</kbd>}
        </button>
      })}
    </div>)}
  </div>
}

function groupCommands(commands: EditorCommand[]) {
  const groups = new Map<string, EditorCommand[]>()
  for (const command of commands) groups.set(command.group, [...(groups.get(command.group) ?? []), command])
  return [...groups.entries()]
}
