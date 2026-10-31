import { useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Dialog from '@radix-ui/react-dialog'
import { Command } from 'cmdk'
import { useI18n } from '@/i18n/i18n'
import { FlowDropdownMenuContent, FlowMenuItem, FlowMenuSeparator } from '@/components/ui/row-context-menu'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { PhoneCommandMenu } from '@/components/ui/phone-menu'
import { usePhoneLayout } from '@/hooks/use-phone-layout'
import { useUsageFeedbackEnabled } from '@/lib/usage-feedback'
import { RENAME_CHAT_SHORTCUT, deleteChatShortcut } from './agent-chat-commands'
import './agent-rename-palette.css'
import './agent-chat-actions.css'

/** The "…" chat menu shared by the Agent page and the floating chat. */
export function AgentChatMenu({ align = 'start', className, unsaved = false, onCopy, onDelete, onFeedback, onRename }: {
  align?: 'start' | 'end'
  className?: string
  /** A streaming chat the server does not know yet cannot be renamed or deleted. */
  unsaved?: boolean
  onCopy: () => void
  onDelete: () => void
  onFeedback: () => void
  onRename: () => void
}) {
  const { t } = useI18n()
  const phone = usePhoneLayout()
  const [phoneOpen, setPhoneOpen] = useState(false)
  const feedbackEnabled = useUsageFeedbackEnabled()
  if (phone) {
    return <>
      <button
        aria-expanded={phoneOpen}
        aria-haspopup="dialog"
        aria-label={t('Chat options')}
        className={className}
        data-state={phoneOpen ? 'open' : 'closed'}
        onClick={() => setPhoneOpen(open => !open)}
        type="button"
      ><FlowGlyph name="ellipsis" size={14}/></button>
      <PhoneCommandMenu
        className="agent-chat-options-phone-menu"
        label={t('Chat options')}
        onCloseAutoFocus={event => {
          event.preventDefault()
          document.querySelector<HTMLButtonElement>('button[aria-label="Chat options"]')?.focus({ preventScroll: true })
        }}
        onOpenChange={setPhoneOpen}
        open={phoneOpen}
        placeholder="Filter…"
      >
        <Command.Item className="menu-item" disabled={unsaved} onSelect={onRename} value={t('Rename…')}>
          <FlowGlyph name="edit" size={16}/><span>{t('Rename…')}</span>
        </Command.Item>
        <Command.Item className="menu-item" onSelect={onCopy} value={t('Copy as markdown')}>
          <FlowGlyph name="copyMarkdown" size={16}/><span>{t('Copy as markdown')}</span>
        </Command.Item>
        <Command.Separator><span aria-hidden="true" className="agent-chat-options-divider" /></Command.Separator>
        <Command.Item className="menu-item" disabled={unsaved} onSelect={onDelete} value={t('Delete')}>
          <FlowGlyph name="delete" size={16}/><span>{t('Delete')}</span>
        </Command.Item>
      </PhoneCommandMenu>
    </>
  }
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button aria-label={t('Chat options')} className={className} type="button"><FlowGlyph name="ellipsis" size={14}/></button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <FlowDropdownMenuContent align={align} label={t('Chat options')}>
          <FlowMenuItem icon={<FlowGlyph name="edit"/>} disabled={unsaved} label="Rename…" shortcut={RENAME_CHAT_SHORTCUT} onSelect={onRename}/>
          <FlowMenuItem icon={<FlowGlyph name="copy"/>} label="Copy as markdown" onSelect={onCopy}/>
          {feedbackEnabled && <FlowMenuItem icon={<FlowGlyph name="thumbsDown"/>} label="Leave feedback…" onSelect={onFeedback}/>}
          <FlowMenuSeparator/>
          <FlowMenuItem icon={<FlowGlyph name="delete"/>} disabled={unsaved} label="Delete" shortcut={deleteChatShortcut()} onSelect={onDelete}/>
        </FlowDropdownMenuContent>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}

/** The reference app's rename command palette: a filter field holding the title, then "Rename chat to …" and "Cancel". */
export function AgentRenamePalette({ open, title, onOpenChange, onRename }: {
  open: boolean
  title: string
  onOpenChange: (open: boolean) => void
  onRename: (title: string) => void
}) {
  const { t } = useI18n()
  const [value, setValue] = useState(title)
  const [active, setActive] = useState<'rename' | 'cancel'>('rename')
  const inputRef = useRef<HTMLInputElement>(null)
  // Prefill once per opening: a title refreshed meanwhile (generated title, realtime) must not wipe what is typed.
  const [wasOpen, setWasOpen] = useState(false)
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) {
      setValue(title)
      setActive('rename')
    }
  }
  const next = value.trim()
  const choose = (option: 'rename' | 'cancel') => {
    if (option === 'rename') {
      if (!next) return
      onOpenChange(false)
      if (next !== title) onRename(next)
    } else onOpenChange(false)
  }
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.nativeEvent.isComposing) return
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      setActive(current => current === 'rename' ? 'cancel' : 'rename')
    } else if (event.key === 'Enter') {
      event.preventDefault()
      choose(active)
    }
  }
  const row = (id: 'rename' | 'cancel', content: ReactNode, disabled = false) => (
    <div
      aria-disabled={disabled || undefined}
      aria-selected={active === id}
      className="agent-rename-palette__option"
      id={`agent-rename-${id}`}
      onClick={() => choose(id)}
      onMouseMove={() => !disabled && setActive(id)}
      role="option"
    >{content}</div>
  )
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Content
          aria-describedby={undefined}
          className="agent-rename-palette"
          data-flow-motion="dialog"
          onKeyDown={onKeyDown}
          onOpenAutoFocus={event => { event.preventDefault(); inputRef.current?.focus(); inputRef.current?.select() }}
        >
          <Dialog.Title className="agent-rename-palette__title">{t('Rename chat')}</Dialog.Title>
          <input
            ref={inputRef}
            aria-activedescendant={`agent-rename-${active}`}
            aria-controls="agent-rename-options"
            aria-expanded="true"
            aria-label={t('Rename chat')}
            autoComplete="off"
            className="agent-rename-palette__input"
            onChange={event => { setValue(event.target.value); setActive('rename') }}
            placeholder={t('Filter…')}
            role="combobox"
            spellCheck={false}
            value={value}
          />
          <div id="agent-rename-options" role="listbox">
            {next && row('rename', <><span>{t('Rename chat to')}</span> <span className="agent-rename-palette__value" data-i18n-ignore>"{next}"</span></>)}
            {row('cancel', t('Cancel'))}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
