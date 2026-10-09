import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { chipParts } from '@/components/agent/agent-entity-chip'
import { PersonHover } from '@/components/property/person-info'
import { type MentionOption } from '@/components/editor/mentions/mention-options'
import { personDisplayName, personIdentifier } from '@/lib/people'
import { useI18n } from '@/i18n/i18n'

interface MentionMenuProps {
  options: MentionOption[]
  selectedIndex: number
  /** The caret's viewport rectangle: the menu opens below it (above when there is no room) and is never clipped by the editor. */
  anchor: { left: number; top: number; bottom: number }
  query: string
  onSelect: (option: MentionOption) => void
}

/** The "@" menu: people first, then issues, projects, documents and the other resources, each under its heading. */
/**
 * The menu is portaled to the page, outside any open modal: a click on it must not count as an outside click that dismisses the
 * dialog, and wheel / touch scrolling on it must not be cancelled by the dialog's scroll lock. Both listen on the document, so
 * stopping the event at the portal container (where React handles it) keeps it from them.
 */
function keepFromDialog(event: { nativeEvent: Event }) {
  event.nativeEvent.stopPropagation()
}

const MENU_WIDTH = 292
const MENU_MAX_HEIGHT = 320

export function MentionMenu({ options, selectedIndex, anchor, query, onSelect }: MentionMenuProps) {
  const { t } = useI18n()
  const selectedRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const [height, setHeight] = useState(MENU_MAX_HEIGHT)
  useEffect(() => { selectedRef.current?.scrollIntoView({ block: 'nearest' }) }, [selectedIndex])
  useLayoutEffect(() => { if (menuRef.current) setHeight(menuRef.current.offsetHeight) }, [options.length])
  const left = Math.max(8, Math.min(anchor.left, window.innerWidth - MENU_WIDTH - 8))
  const below = anchor.bottom + 6
  const above = anchor.top - 6 - height
  const top = below + height > window.innerHeight - 8 && above >= 8 ? above : below
  let lastGroup = ''
  return createPortal(<div className="description-mention-menu description-mention-menu--floating" ref={menuRef} style={{ left, top, pointerEvents: 'auto' }} role="listbox" aria-label={t('Mention')} onPointerDown={keepFromDialog} onWheel={keepFromDialog} onTouchMove={keepFromDialog}>
    {options.length === 0 ? <div className="description-slash-empty">{t('No results for “@{query}”').replace('{query}', query)}</div> : options.map((option, index) => {
      const heading = option.group !== lastGroup ? option.group : ''
      lastGroup = option.group
      const button = <button
        ref={index === selectedIndex ? selectedRef : undefined}
        type="button"
        role="option"
        aria-selected={index === selectedIndex}
        onMouseDown={event => event.preventDefault()}
        onClick={() => onSelect(option)}
      >
        <MentionOptionBody option={option}/>
      </button>
      return <div className="description-mention-option" key={option.key}>
        {heading && <div className="description-mention-group" role="presentation">{t(heading)}</div>}
        {option.entity.kind === 'user' ? <PersonHover person={option.entity.user}>{button}</PersonHover> : button}
      </div>
    })}
  </div>, document.body)
}

function MentionOptionBody({ option }: { option: MentionOption }) {
  const { t } = useI18n()
  if (option.entity.kind === 'user') {
    const user = option.entity.user
    const name = personDisplayName(user) || t('Unknown user')
    return <>
      <span className="description-mention-avatar" aria-hidden="true">{initials(name)}</span>
      <span className="description-command-copy"><strong data-i18n-ignore>{name}</strong>{(personIdentifier(user) || user.email) && <small data-i18n-ignore>{[...new Set([personIdentifier(user), user.email].filter(Boolean))].join(' · ')}</small>}</span>
    </>
  }
  const parts = chipParts(option.entity, t)
  return <>
    <span className="description-mention-icon" aria-hidden="true">{parts.icon}</span>
    <span className="description-command-copy"><strong data-i18n-ignore>{parts.identifier ? `${parts.identifier} ${parts.label}` : parts.label}</strong>{parts.suffix && <small data-i18n-ignore>{parts.suffix}</small>}</span>
  </>
}

function initials(name: string) {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  return (parts.length > 1 ? `${parts[0][0]}${parts.at(-1)?.[0] ?? ''}` : name.slice(0, 2)).toUpperCase()
}
