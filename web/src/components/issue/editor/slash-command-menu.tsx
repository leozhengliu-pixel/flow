import type { LucideIcon } from 'lucide-react'
import { Fragment, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useI18n } from '@/i18n/i18n'
import type { SlashGroup } from './editor-commands'

export interface EditorCommand {
  id: string
  group: SlashGroup
  label: string
  description?: string
  keywords?: string
  shortcut?: string
  /** Only listed once the user has typed a query (Linear: Table, Divider). */
  searchOnly?: boolean
  icon: LucideIcon
  run: () => void
}

interface SlashCommandMenuProps {
  commands: EditorCommand[]
  selectedIndex: number
  /** Viewport coordinates: `top` is just below the caret, `caretTop` its top edge (the menu flips above when there is no room below). */
  position: { left: number; top: number; caretTop?: number }
  query: string
  onSelect: (command: EditorCommand) => void
  onDismiss?: () => void
}

export function SlashCommandMenu({ commands, selectedIndex, position, onSelect, onDismiss }: SlashCommandMenuProps) {
  const { t } = useI18n()
  const selectedRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const [placement, setPlacement] = useState<{ top: number; maxHeight?: number }>({ top: position.top })
  useEffectScroll(selectedRef, selectedIndex)
  // Opens below the caret, or above it (Linear opens upward at the page end) when it would run off the window.
  useLayoutEffect(() => {
    const menu = menuRef.current
    if (!menu) return
    // The natural height (the CSS max-height caps it at 442px) decides whether the menu needs to flip or shrink.
    const height = Math.min(menu.scrollHeight + 8, 442)
    const aboveTop = (position.caretTop ?? position.top - 24) - height - 6
    const room = window.innerHeight - position.top - 8
    let next: { top: number; maxHeight?: number }
    if (height <= room) next = { top: position.top }
    else if (aboveTop >= 8) next = { top: aboveTop }
    else next = { top: position.top, maxHeight: Math.max(room, 160) }
    setPlacement(current => current.top === next.top && current.maxHeight === next.maxHeight ? current : next)
  }, [commands.length, position.top, position.caretTop])

  return createPortal(<div className="description-slash-menu description-slash-menu--floating" ref={menuRef} style={{ left: position.left, top: placement.top, maxHeight: placement.maxHeight }} role="listbox" aria-label={t('Insert block')}>
    {commands.length === 0 ? <div className="description-slash-empty" role="status">
      <span>{t('No results found')}</span>
      <button type="button" className="description-slash-dismiss" onMouseDown={event => event.preventDefault()} onClick={onDismiss}>{t('Dismiss')}</button>
    </div> : commands.map((command, index) => {
      const Icon = command.icon
      const keys = slashKeys(command.shortcut)
      const divider = index > 0 && commands[index - 1].group !== command.group
      return <Fragment key={command.id}>
        {divider && <div className="description-slash-separator" role="separator"/>}
        <button ref={index === selectedIndex ? selectedRef : undefined} type="button" role="option" aria-selected={index === selectedIndex} onMouseDown={event => event.preventDefault()} onClick={() => onSelect(command)}>
          <span className="description-slash-main">
            <span className="description-command-icon"><Icon size={16}/></span>
            <span className="description-command-copy"><strong>{t(command.label)}</strong></span>
          </span>
          {keys && <span className="description-slash-kbd" aria-hidden="true">{keys.map((key, keyIndex) => <kbd key={`${command.id}-${keyIndex}`}>{key}</kbd>)}</span>}
        </button>
      </Fragment>
    })}
  </div>, document.body)
}

function useEffectScroll(ref: React.RefObject<HTMLButtonElement | null>, index: number) {
  useLayoutEffect(() => { ref.current?.scrollIntoView?.({ block: 'nearest' }) }, [ref, index])
}

function slashKeys(shortcut?: string) {
  if (!shortcut || !/(⌘|⌥|⌃|Ctrl)/.test(shortcut)) return null
  return [...shortcut]
}
