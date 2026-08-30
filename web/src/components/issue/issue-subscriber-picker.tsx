import * as Popover from '@radix-ui/react-popover'
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'

import { Avatar } from '@/components/issue/issue-row'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { useI18n } from '@/i18n/i18n'
import type { Issue, User } from '@/types/flow'

import './issue-subscriber-picker.css'

export function IssueSubscriberPicker({ issue, users, onToggle }: { issue: Issue; users: User[]; onToggle: (id: string) => void | Promise<void> }) {
  const { t } = useI18n()
  const rootRef = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)
  const selectedSet = useMemo(() => new Set(issue.subscriberIds), [issue.subscriberIds])
  const options = useMemo(() => users.filter(user => user.active).sort((left, right) => Number(selectedSet.has(right.id)) - Number(selectedSet.has(left.id))).map(user => ({ id: user.id, label: user.displayName, keywords: `${user.name} ${user.email}` })), [selectedSet, users])
  const command = usePropertyCommand({ closeOnSelect: false, open, options, selectedIds: issue.subscriberIds, onOpenChange: setOpen, onSelect: option => onToggle(option.id) })
  const selected = command.filteredOptions.filter(option => selectedSet.has(option.id))
  const available = command.filteredOptions.filter(option => !selectedSet.has(option.id))

  useEffect(() => {
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (!event.metaKey || !event.shiftKey || event.ctrlKey || event.altKey || event.key.toLowerCase() !== 's' || isEditable(event.target)) return
      if (!rootRef.current?.getClientRects().length) return
      event.preventDefault()
      rootRef.current.querySelector<HTMLButtonElement>('.subscriber-avatars')?.click()
    }
    addEventListener('keydown', onKeyDown)
    return () => removeEventListener('keydown', onKeyDown)
  }, [])

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Escape') return
    command.onKeyDown(event)
  }
  return <div ref={rootRef} className="issue-subscriber-picker">
    <Popover.Root open={open} onOpenChange={next => { setOpen(next); if (!next) command.onQueryChange('') }}>
      <Popover.Trigger asChild><button type="button" className="subscriber-avatars" aria-label={t('Change subscribers')} aria-haspopup="dialog" aria-expanded={open}>{users.filter(user => selectedSet.has(user.id)).slice(0,3).map(user => <Avatar name={user.displayName} key={user.id}/>)}</button></Popover.Trigger>
      <Popover.Portal><Popover.Content className="issue-subscriber-menu" role="dialog" aria-label={t('Change subscribers')} align="end" side="bottom" sideOffset={4} collisionPadding={8} onOpenAutoFocus={event => event.preventDefault()} onKeyDown={onKeyDown}>
        <label className="issue-subscriber-search"><input ref={command.inputRef} role="searchbox" aria-label={t('Change subscribers…')} aria-activedescendant={command.activeId ? `subscriber-${command.activeId}` : undefined} placeholder={t('Change subscribers…')} value={command.query} onChange={event => command.onQueryChange(event.target.value)}/><kbd>⌘ ⇧ S</kbd></label>
        <div className="issue-subscriber-options" role="listbox" aria-label={t('Subscribers')} aria-multiselectable="true">
          {selected.map(option => <SubscriberOption active={option.id === command.activeId} assignee={issue.assignee?.id === option.id} checked key={option.id} option={option} user={users.find(user => user.id === option.id)!} onActive={() => command.setActiveId(option.id)} onChoose={() => command.choose(option)}/>)}
          {selected.length > 0 && available.length > 0 && <div className="issue-subscriber-separator" role="separator"/>}
          {available.map(option => <SubscriberOption active={option.id === command.activeId} assignee={issue.assignee?.id === option.id} checked={false} key={option.id} option={option} user={users.find(user => user.id === option.id)!} onActive={() => command.setActiveId(option.id)} onChoose={() => command.choose(option)}/>)}
          {!command.filteredOptions.length && <p>{t('No results')}</p>}
        </div>
      </Popover.Content></Popover.Portal>
    </Popover.Root>
  </div>
}

function SubscriberOption({ active, assignee, checked, option, user, onActive, onChoose }: { active: boolean; assignee: boolean; checked: boolean; option: { id: string; label: string }; user: User; onActive: () => void; onChoose: () => void }) {
  const { t } = useI18n()
  return <button id={`subscriber-${option.id}`} type="button" className="issue-subscriber-option" role="option" aria-selected={active} aria-checked={checked} onPointerMove={onActive} onFocus={onActive} onClick={onChoose}>
    <span className="issue-subscriber-option-bg"/><span className="issue-subscriber-checkbox" role="checkbox" aria-checked={checked}>{checked && <CheckboxMark/>}</span><Avatar name={user.displayName}/><span className="issue-subscriber-name" data-i18n-ignore>{user.displayName}</span>{assignee && <small>{t('Assignee')}</small>}
  </button>
}

function isEditable(target: EventTarget | null) { return target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLElement && target.isContentEditable }
