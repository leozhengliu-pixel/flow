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
    {commands.length === 0 ? <div className="description-slash-empty">No results for “{query}”</div> : commands.map((command, index) => {
      const Icon = command.icon
      const keys = slashKeys(command.shortcut)
      return <button ref={index === selectedIndex ? selectedRef : undefined} key={command.id} type="button" role="option" aria-selected={index === selectedIndex} onMouseDown={event => event.preventDefault()} onClick={() => onSelect(command)}>
        <span className="description-slash-main">
          <span className="description-command-icon"><Icon size={16}/></span>
          <span className="description-command-copy"><strong>{command.label}</strong></span>
        </span>
        {keys && <span className="description-slash-kbd" aria-hidden="true">{keys.map((key, keyIndex) => <kbd key={`${command.id}-${keyIndex}`}>{key}</kbd>)}</span>}
      </button>
    })}
  </div>
}

function slashKeys(shortcut?: string) {
  if (!shortcut || !/(⌘|⌥|⌃|Ctrl)/.test(shortcut)) return null
  return [...shortcut]
}
