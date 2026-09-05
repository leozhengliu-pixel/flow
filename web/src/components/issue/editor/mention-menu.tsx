import { useEffect, useRef } from 'react'
import type { User } from '@/types/flow'

interface MentionMenuProps {
  users: User[]
  selectedIndex: number
  position: { left: number; top: number }
  query: string
  onSelect: (user: User) => void
}

export function MentionMenu({ users, selectedIndex, position, query, onSelect }: MentionMenuProps) {
  const selectedRef = useRef<HTMLButtonElement>(null)
  useEffect(() => { selectedRef.current?.scrollIntoView({ block: 'nearest' }) }, [selectedIndex])
  return <div className="description-mention-menu" style={position} role="listbox" aria-label="Mention a user">
    {users.length === 0 ? <div className="description-slash-empty">No people found for “@{query}”</div> : users.map((user, index) => <button
      key={user.id}
      ref={index === selectedIndex ? selectedRef : undefined}
      type="button"
      role="option"
      aria-selected={index === selectedIndex}
      onMouseDown={event => event.preventDefault()}
      onClick={() => onSelect(user)}
    >
      <span className="description-mention-avatar" aria-hidden="true">{initials(user.displayName || user.name)}</span>
      <span className="description-command-copy"><strong>{user.displayName || user.name}</strong>{user.email && <small>{user.email}</small>}</span>
    </button>)}
  </div>
}

function initials(name: string) {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  return (parts.length > 1 ? `${parts[0][0]}${parts.at(-1)?.[0] ?? ''}` : name.slice(0, 2)).toUpperCase()
}

