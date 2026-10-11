import { Check } from 'lucide-react'
import { AgentBadge } from '@/components/agent/agent-badge'
import { useMemo } from 'react'
import type { User } from '@/types/flow'
import { UserAvatar } from '@/components/ui/user-avatar'
import { personMatchesQuery, personSearchText } from '@/lib/people'
import { matchesSearch } from '@/lib/search-match'
import { useI18n } from '@/i18n/i18n'
import { PersonHover } from './person-info'
import { SearchableMenuItems } from '@/components/ui/searchable-menu-items'
import './virtual-property-options.css'

export function PeopleMenuItems({ users, selectedId, onSelect, emptyLabel, itemClassName, contextMenu = false }: { users: User[]; selectedId?: string; onSelect: (id: string) => void; emptyLabel?: string; itemClassName?: string; contextMenu?: boolean }) {
  const { t } = useI18n()
  const options = useMemo(() => [...(emptyLabel ? [{ id: '', label: emptyLabel, person: undefined as User | undefined, entity: false }] : []), ...users.map(user => ({ id: user.id, label: user.displayName || user.name, keywords: personSearchText(user), person: user, entity: true }))], [emptyLabel, users])
  return <SearchableMenuItems options={options} className="people-menu-body" searchClassName="property-command-search" itemClassName={itemClassName} searchLabel="Search people" selectedId={selectedId} contextMenu={contextMenu}
    matches={(option, query) => option.person ? personMatchesQuery(option.person, query) : personMatchesQuery({ id: option.id, label: option.label }, query) || matchesSearch(query, [option.label], t)}
    onSelect={option => onSelect(option.id)} wrapOption={(option, row) => <PersonHover person={option.person}>{row}</PersonHover>}
    renderOption={option => <>{option.person && <span aria-hidden="true"><UserAvatar className="people-menu-avatar" avatarUrl={option.person.avatarUrl} name={option.label}/></span>}<span data-menu-label data-i18n-ignore={option.entity || undefined}>{option.entity ? option.label : t(option.label)}</span>{option.person?.app && <AgentBadge/>}{selectedId === option.id && <Check size={14}/>}</>}/>
}
