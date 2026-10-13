import { Check } from 'lucide-react'
import { NoAssigneeIcon } from '@/components/issue/issue-icons'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { ASSIGNEE_INVITE_ID, assigneeShortcut } from '@/lib/agent-members'
import { AgentHover } from '@/components/agent/agent-hover-card'
import { openMemberInvite } from '@/lib/invite-navigation'
import { useMemo } from 'react'
import type { User } from '@/types/flow'
import { UserAvatar } from '@/components/ui/user-avatar'
import { personMatchesQuery, personSearchText } from '@/lib/people'
import { matchesSearch } from '@/lib/search-match'
import { useI18n } from '@/i18n/i18n'
import { PersonHover } from './person-info'
import { SearchableMenuItems } from '@/components/ui/searchable-menu-items'
import './virtual-property-options.css'

/**
 * A searchable people list for dropdown/context menus. `assignee` makes it the reference app's assignee list: "Assign to…" search,
 * number keys (empty row 0, people 1-9), agents without a pill (their hover card carries it), and a "New user /
 * Invite and assign…" row (`onInvite`).
 */
export function PeopleMenuItems({ users, selectedId, selectedIds, onSelect, emptyLabel, itemClassName, contextMenu = false, assignee = false, onInvite = openMemberInvite }: { users: User[]; selectedId?: string; selectedIds?: string[]; onSelect: (id: string) => void; emptyLabel?: string; itemClassName?: string; contextMenu?: boolean; assignee?: boolean; onInvite?: () => void }) {
  const { t } = useI18n()
  const options = useMemo(() => [
    ...(emptyLabel ? [{ id: '', label: emptyLabel, person: undefined as User | undefined, entity: false, shortcut: assignee ? '0' : undefined, groupLabel: undefined as string | undefined }] : []),
    ...users.map((user, index) => ({ id: user.id, label: user.displayName || user.name, keywords: personSearchText(user), person: user as User | undefined, entity: true, shortcut: assignee ? assigneeShortcut(index + 1) : undefined, groupLabel: undefined as string | undefined })),
    ...(assignee ? [{ id: ASSIGNEE_INVITE_ID, label: 'Invite and assign…', person: undefined as User | undefined, entity: false, shortcut: undefined as string | undefined, groupLabel: 'New user' as string | undefined }] : []),
  ], [assignee, emptyLabel, users])
  const checked = selectedIds ?? (selectedId === undefined ? [] : [selectedId])
  return <SearchableMenuItems options={options} className="people-menu-body" searchClassName="property-command-search" itemClassName={itemClassName} searchLabel={assignee ? 'Assign to…' : 'Search people'} selectedId={selectedId} contextMenu={contextMenu}
    matches={(option, query) => option.person ? personMatchesQuery(option.person, query) : personMatchesQuery({ id: option.id, label: option.label }, query) || matchesSearch(query, [option.label], t)}
    onSelect={option => option.id === ASSIGNEE_INVITE_ID ? onInvite() : onSelect(option.id)}
    wrapOption={(option, row) => option.person?.app ? <AgentHover agent={option.person}>{row}</AgentHover> : <PersonHover person={option.person}>{row}</PersonHover>}
    renderOption={option => <>
      {option.person && <span aria-hidden="true"><UserAvatar className={`people-menu-avatar${assignee ? ' is-compact' : ''}`} avatarUrl={option.person.avatarUrl} name={option.label}/></span>}
      {assignee && !option.person && (option.id === ASSIGNEE_INVITE_ID ? <FlowGlyph className="property-command-invite-icon" name="invitePlane" size={16}/> : <NoAssigneeIcon size={16}/>)}
      <span data-menu-label data-i18n-ignore={option.entity || undefined}>{option.entity ? option.label : t(option.label)}</span>
      {checked.includes(option.id) && <Check size={assignee ? 16 : 14}/>}
      {option.shortcut && <kbd>{option.shortcut}</kbd>}
    </>}/>
}
