import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Dialog from '@radix-ui/react-dialog'
import { deleteAgentSession, restoreAgentSession } from '@/lib/api'
import { toast } from '@/lib/toast'
import { useI18n } from '@/i18n/i18n'
import type { AgentSession } from '@/types/flow'
import { FlowDropdownMenuContent, FlowMenuItem, FlowMenuSeparator } from '@/components/ui/row-context-menu'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { useFlowHotkeys } from '@/components/ui/menu-shortcuts'
import { isMacPlatform } from '@/components/project-detail/project-detail-shortcuts'
import './agent-rename-palette.css'

export const RENAME_CHAT_SHORTCUT = '⇧ R'
/** ⌘ ⌫ on macOS, Ctrl Delete elsewhere (the reference app's hint). */
export function deleteChatShortcut() {
  return isMacPlatform() ? '⌘ ⌫' : '⌘ Delete'
}

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
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button aria-label={t('Chat options')} className={className} type="button"><FlowGlyph name="ellipsis" size={14}/></button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <FlowDropdownMenuContent align={align} label={t('Chat options')}>
          <FlowMenuItem icon={<FlowGlyph name="edit"/>} disabled={unsaved} label="Rename…" shortcut={RENAME_CHAT_SHORTCUT} onSelect={onRename}/>
          <FlowMenuItem icon={<FlowGlyph name="copy"/>} label="Copy as markdown" onSelect={onCopy}/>
          <FlowMenuItem icon={<FlowGlyph name="thumbsDown"/>} label="Leave feedback…" onSelect={onFeedback}/>
          <FlowMenuSeparator/>
          <FlowMenuItem icon={<FlowGlyph name="delete"/>} disabled={unsaved} label="Delete" shortcut={deleteChatShortcut()} onSelect={onDelete}/>
        </FlowDropdownMenuContent>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}

/** ⇧R renames and Ctrl Delete (⌘⌫) deletes the open chat, unless focus is in a text field or a menu or dialog is open; `scope` limits it to events from inside that selector. */
export function useAgentChatHotkeys(enabled: boolean, onRename: () => void, onDelete: () => void, scope?: string) {
  useFlowHotkeys({ [RENAME_CHAT_SHORTCUT]: onRename, [deleteChatShortcut()]: onDelete }, enabled, scope)
}

/**
 * Deletes a chat at once (no confirmation) and offers Undo, which restores it on the server.
 * `onDeleted` removes it from the UI; `onRestored` puts it back and shows it again.
 */
export function deleteAgentChat(session: AgentSession, { t, onDeleted, onRestored }: {
  t: (text: string) => string
  onDeleted: () => void
  onRestored: (session: AgentSession) => void
}) {
  return deleteAgentSession(session.id).then(
    () => {
      onDeleted()
      toast.info(t('Chat deleted'), {
        icon: <FlowGlyph name="delete" size={16}/>,
        button: {
          label: t('Undo'),
          onClick: () => void restoreAgentSession(session.id).then(onRestored, reason => toast.error(reason instanceof Error ? reason.message : t('Could not restore chat'))),
        },
      })
    },
    reason => { toast.error(reason instanceof Error ? reason.message : t('Could not delete chat')) },
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
  const titleRef = useRef(title)
  titleRef.current = title
  useEffect(() => {
    if (!open) return
    setValue(titleRef.current)
    setActive('rename')
  }, [open])
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
