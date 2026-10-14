import { useRef, useState } from 'react'
import { ChevronDown, Lock, X } from 'lucide-react'

import { TeamIcon } from '@/components/issue/issue-icons'
import type { Team } from '@/types/flow'

import './oauth-team-combobox.css'

type Translate = (source: string) => string

/**
 * Multi-select team combobox (the reference app's "Select teams…" field): chips for picked teams, a filterable list below.
 * Private teams carry a lock after their name; an empty list reads "No matches".
 */
export function TeamCombobox({ teams, value, onChange, t, placeholder = 'Select teams…' }: { teams: Team[]; value: string[]; onChange: (value: string[]) => void; t: Translate; placeholder?: string }) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const options = teams.filter(team => !value.includes(team.id) && team.name.toLowerCase().includes(query.trim().toLowerCase()))
  const pick = (team: Team) => { onChange([...value, team.id]); setQuery(''); setActive(0); setOpen(false) }
  return <div className="oauth-team-picker" onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setOpen(false) }}>
    <div className="oauth-team-control" onMouseDown={event => { if (event.target === event.currentTarget) { event.preventDefault(); inputRef.current?.focus(); setOpen(true) } }}>
      {value.map(id => teams.find(team => team.id === id)).filter((team): team is Team => Boolean(team)).map(team => <span className="oauth-team-chip" key={team.id}>
        <span className="oauth-team-chip-label"><TeamIcon team={team} size={14}/><span data-i18n-ignore>{team.name}</span>{team.private && <Lock aria-hidden="true" className="oauth-team-lock"/>}</span>
        <button type="button" aria-label={t('Remove {team}').replace('{team}', team.name)} onMouseDown={event => event.preventDefault()} onClick={() => onChange(value.filter(item => item !== team.id))}><X aria-hidden="true"/></button>
      </span>)}
      <input ref={inputRef} role="combobox" aria-expanded={open} aria-controls="oauth-team-options" aria-label={t(placeholder)} placeholder={value.length ? '' : t(placeholder)} value={query}
        aria-activedescendant={open && options[active] ? `oauth-team-${options[active].id}` : undefined}
        onFocus={() => setOpen(true)}
        onChange={event => { setQuery(event.target.value); setOpen(true); setActive(0) }}
        onKeyDown={event => {
          if (event.key === 'ArrowDown') { event.preventDefault(); setOpen(true); setActive(current => Math.min(current + 1, Math.max(options.length - 1, 0))) }
          else if (event.key === 'ArrowUp') { event.preventDefault(); setActive(current => Math.max(current - 1, 0)) }
          else if (event.key === 'Enter' && open && options[active]) { event.preventDefault(); pick(options[active]) }
          else if (event.key === 'Backspace' && !query && value.length) onChange(value.slice(0, -1))
          else if (event.key === 'Escape') { event.preventDefault(); setOpen(false); setQuery('') }
        }}/>
      <button type="button" className="oauth-team-toggle" aria-label={t('Toggle menu')} onMouseDown={event => event.preventDefault()} onClick={() => { setOpen(current => !current); inputRef.current?.focus() }}><ChevronDown aria-hidden="true"/></button>
    </div>
    {open && <div id="oauth-team-options" className="oauth-team-menu" role="listbox" aria-label={t('Teams')}>
      {options.length ? options.map((team, index) => <button type="button" role="option" aria-selected={index === active} id={`oauth-team-${team.id}`} key={team.id} data-highlighted={index === active || undefined}
        onMouseDown={event => event.preventDefault()} onPointerMove={() => setActive(index)} onClick={() => pick(team)}>
        <TeamIcon team={team} size={14}/><span data-i18n-ignore>{team.name}</span>{team.private && <Lock aria-hidden="true" className="oauth-team-lock"/>}
      </button>) : <div className="oauth-team-empty">{t('No matches')}</div>}
    </div>}
  </div>
}
