import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as ContextMenu from '@radix-ui/react-context-menu'
import { Check } from 'lucide-react'
import { useId, useMemo } from 'react'
import type { User } from '@/types/flow'
import { UserAvatar } from '@/components/ui/user-avatar'
import { personSearchText } from '@/lib/people'
import { useI18n } from '@/i18n/i18n'
import { PersonHover } from './person-info'
import { usePropertyCommand } from './use-property-command'

export function PeopleMenuItems({ users, selectedId, onSelect, emptyLabel, itemClassName, contextMenu = false }: { users: User[]; selectedId?: string; onSelect: (id: string) => void; emptyLabel?: string; itemClassName?: string; contextMenu?: boolean }) {
  const { t } = useI18n()
  const prefix = useId()
  const options = useMemo(() => [...(emptyLabel ? [{ id: '', label: emptyLabel }] : []), ...users.map(user => ({ id: user.id, label: user.displayName || user.name, keywords: personSearchText(user), person: user }))], [emptyLabel, users])
  const command = usePropertyCommand({ open: true, options, selectedIds: selectedId ? [selectedId] : [], onOpenChange: () => {}, onSelect: option => document.getElementById(`${prefix}-${option.id}`)?.click() })
  const Item = contextMenu ? ContextMenu.Item : DropdownMenu.Item
  return <>
    <div className="property-command-search"><input aria-label={t('Search people')} placeholder={t('Search people…')} ref={command.inputRef} value={command.query} onChange={event => command.onQueryChange(event.target.value)} onKeyDown={event => { event.stopPropagation(); command.onKeyDown(event) }}/></div>
    <div className="people-menu-options">{command.filteredOptions.map(option => <PersonHover key={option.id || 'none'} person={users.find(user => user.id === option.id)}><Item id={`${prefix}-${option.id}`} textValue={option.label} className={itemClassName} onSelect={() => onSelect(option.id)}>{option.id && <UserAvatar className="people-menu-avatar" avatarUrl={users.find(user => user.id === option.id)?.avatarUrl} name={option.label}/>}<span data-i18n-ignore={Boolean(option.id) || undefined}>{option.id ? option.label : t(option.label)}</span>{selectedId === option.id && <Check size={14}/>}</Item></PersonHover>)}{!command.filteredOptions.length && <div className="core-property-empty">{t('No results')}</div>}</div>
  </>
}
