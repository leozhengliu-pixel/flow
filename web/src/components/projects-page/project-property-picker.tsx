import * as Popover from '@radix-ui/react-popover'
import { Check, Send } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { NoAssigneeIcon, PriorityIcon } from '@/components/issue/issue-icons'
import styles from './project-row-menus.module.css'

export type ProjectPickerProperty = 'priority' | 'lead' | 'status'

export type ProjectPropertyOption = {
  avatarUrl?: string
  color?: string
  group?: string
  keywords?: string
  label: string
  shortcut?: string
  statusType?: string
  value: string
}

const PICKER_COPY: Record<ProjectPickerProperty, { placeholder: string; keys: [string, string] }> = {
  priority: { placeholder: 'Change priority…', keys: ['P', 'P'] },
  lead: { placeholder: 'Set lead…', keys: ['P', 'A'] },
  status: { placeholder: 'Change status…', keys: ['P', 'S'] },
}

export function ProjectPropertyPicker({ buttonClassName = '', children, label, onChange, options, property, value }: {
  buttonClassName?: string
  children: ReactNode
  label: string
  onChange: (value: string) => void
  options: ProjectPropertyOption[]
  property: ProjectPickerProperty
  value: string
}) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [activeValue, setActiveValue] = useState(value)
  const inputRef = useRef<HTMLInputElement>(null)
  const copy = PICKER_COPY[property]
  const normalized = query.trim().toLowerCase()
  const filtered = useMemo(() => options.filter(option => `${option.label} ${option.keywords ?? ''}`.toLowerCase().includes(normalized)), [normalized, options])
  const ordered = useMemo(() => property === 'lead' ? orderLeadOptions(filtered, value, normalized) : filtered, [filtered, normalized, property, value])

  useEffect(() => {
    if (!open) return
    setQuery('')
    setActiveValue(value)
  }, [open, value])

  const choose = (next: string) => {
    onChange(next)
    setOpen(false)
  }
  const move = (delta: number) => {
    if (!ordered.length) return
    const index = Math.max(0, ordered.findIndex(option => option.value === activeValue))
    setActiveValue(ordered[(index + delta + ordered.length) % ordered.length].value)
  }
  const keyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      move(event.key === 'ArrowDown' ? 1 : -1)
      return
    }
    if (event.key === 'Home' || event.key === 'End') {
      event.preventDefault()
      setActiveValue(ordered[event.key === 'Home' ? 0 : ordered.length - 1]?.value ?? '')
      return
    }
    if (event.key === 'Enter') {
      const active = ordered.find(option => option.value === activeValue) ?? ordered[0]
      if (active) { event.preventDefault(); choose(active.value) }
      return
    }
    if (!query && /^[0-9]$/.test(event.key)) {
      const option = options.find(candidate => candidate.shortcut === event.key)
      if (option) { event.preventDefault(); choose(option.value) }
    }
  }

  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Trigger asChild><button aria-expanded={open} aria-label={label} className={`lp-project-property-trigger ${buttonClassName}`} type="button">{children}</button></Popover.Trigger>
    <Popover.Portal>
      <Popover.Content align={property === 'status' ? 'end' : 'start'} className={styles.command} collisionPadding={8} onClick={event => event.stopPropagation()} onCloseAutoFocus={event => event.preventDefault()} onOpenAutoFocus={event => { event.preventDefault(); requestAnimationFrame(() => inputRef.current?.focus()) }} side="bottom" sideOffset={4}>
        <div className={styles.commandHeader}>
          <input aria-activedescendant={activeValue ? pickerOptionId(property, activeValue) : undefined} aria-label={copy.placeholder} onChange={event => { setQuery(event.target.value); setActiveValue('') }} onKeyDown={keyDown} placeholder={copy.placeholder} ref={inputRef} role="searchbox" value={query}/>
          <span className={styles.sequence} aria-label={`${copy.keys[0]}, then ${copy.keys[1]}`}><kbd>{copy.keys[0]}</kbd><span>then</span><kbd>{copy.keys[1]}</kbd></span>
        </div>
        <div aria-label={`${property} options`} className={styles.commandList} role="listbox">
          {!ordered.length && <div className={styles.empty}>No results</div>}
          {ordered.map((option, index) => {
            const previous = ordered[index - 1]
            const showGroup = property === 'lead' && option.group && option.group !== previous?.group
            return <div key={option.value || '__empty'}>
              {showGroup && <div className={styles.groupLabel}>{option.group}</div>}
              <button
                aria-selected={option.value === value}
                className={activeValue === option.value ? styles.active : ''}
                id={pickerOptionId(property, option.value)}
                onClick={() => choose(option.value)}
                onMouseEnter={() => setActiveValue(option.value)}
                role="option"
                type="button"
              >
                <OptionIcon option={option} property={property}/><span className={styles.optionLabel}>{option.label}</span>
                <span className={styles.optionEnd}>{option.value === value && <Check aria-hidden="true" size={15}/>} {option.shortcut && <span>{option.shortcut}</span>}</span>
              </button>
            </div>
          })}
          {property === 'lead' && !normalized && <><div className={styles.groupLabel}>New user</div><button onClick={() => setOpen(false)} type="button"><Send aria-hidden="true" size={14}/><span className={styles.optionLabel}>Invite and add…</span></button></>}
        </div>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

function OptionIcon({ option, property }: { option: ProjectPropertyOption; property: ProjectPickerProperty }) {
  if (property === 'priority') return <PriorityIcon priority={priorityNumber(option.value)} size={15}/>
  if (property === 'lead') {
    if (!option.value) return <NoAssigneeIcon className={styles.optionIcon} size={15}/>
    if (option.avatarUrl) return <img alt="" className={styles.avatar} src={option.avatarUrl}/>
    return <span className={styles.avatar} style={{ backgroundColor: avatarColor(option.label) }}>{initials(option.label)}</span>
  }
  return <ProjectStatusGlyph color={option.color} name={option.label} type={option.statusType}/>
}

export function ProjectStatusGlyph({ color: _color, name, type }: { color?: string; name: string; type?: string }) {
  const normalized = `${type ?? ''} ${name}`.toLowerCase()
  const kind = normalized.includes('backlog') ? 'backlog' : normalized.includes('progress') || normalized.includes('started') ? 'started' : normalized.includes('complete') ? 'completed' : normalized.includes('cancel') ? 'canceled' : 'planned'
  const iconColor = ({ backlog: '#d6a526', started: '#d6a526', completed: '#5e6ad2', canceled: '#77777c', planned: '#b5b5ba' } as const)[kind]
  return <svg aria-hidden="true" className={styles.statusIcon} viewBox="0 0 16 16">
    {kind === 'backlog' && <circle cx="8" cy="8" fill="none" r="6" stroke={iconColor} strokeDasharray="1.2 1.8" strokeLinecap="round" strokeWidth="1.7"/>}
    {kind === 'planned' && <circle cx="8" cy="8" fill="none" r="5.5" stroke={iconColor} strokeWidth="1.6"/>}
    {kind === 'started' && <><circle cx="8" cy="8" fill="none" r="5.5" stroke={iconColor} strokeWidth="1.6"/><path d="M8 2.5a5.5 5.5 0 0 1 0 11Z" fill={iconColor}/></>}
    {kind === 'completed' && <><circle cx="8" cy="8" fill={iconColor} r="6"/><path d="m5 8.2 1.8 1.8L11.2 5.8" fill="none" stroke="#202022" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.5"/></>}
    {kind === 'canceled' && <><circle cx="8" cy="8" fill={iconColor} r="6"/><path d="m5.5 5.5 5 5m0-5-5 5" stroke="#202022" strokeLinecap="round" strokeWidth="1.4"/></>}
  </svg>
}

function orderLeadOptions(options: ProjectPropertyOption[], selected: string, searching: string) {
  if (searching) return options
  const empty = options.find(option => !option.value)
  const selectedMatch = selected ? options.find(option => option.value === selected) : undefined
  const selectedOption = selectedMatch ? { ...selectedMatch, group: undefined } : undefined
  const rest = options.filter(option => option.value && option.value !== selected).map(option => ({ ...option, group: option.group ?? 'Users from the project team' }))
  return [empty, selectedOption, ...rest].filter((option): option is ProjectPropertyOption => Boolean(option))
}

function pickerOptionId(property: string, value: string) { return `project-${property}-${value || 'none'}` }
function priorityNumber(value: string) { return ({ none: 0, urgent: 1, high: 2, medium: 3, low: 4 } as Record<string, number>)[value] ?? 0 }
function initials(name: string) { return name.split(/\s|@/).filter(Boolean).slice(0, 2).map(part => part[0]?.toUpperCase()).join('') }
function avatarColor(name: string) { return ['#d15f5f', '#5e6ad2', '#4c9a67', '#d09b42'][[...name].reduce((sum, value) => sum + value.charCodeAt(0), 0) % 4] }
