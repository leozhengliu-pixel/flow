import { useState } from 'react'
import { NoAssigneeIcon } from '@/components/issue/issue-icons'
import { AgentHoverCard } from '@/components/agent/agent-hover-card'
import { AGENT_HOVER_CLASS, agentHandle } from '@/components/agent/agent-hover-class'
import { NestedDelegateAvatar } from '@/components/issue/core-property-pickers'
import { confirmRemoveDelegate } from '@/components/issue/confirm-remove-delegate'
import { avatarColor } from '@/components/issue/avatar-color'
import { usePeopleDirectory } from '@/components/property/people-context'
import { UserAvatar } from '@/components/ui/user-avatar'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { useI18n } from '@/i18n/i18n'
import { directoryPerson, personIdentifier } from '@/lib/people'
import { CLEAR_ASSIGNEE_AND_AGENT_ID, NO_AGENT_ID, assigneeShortcut } from '@/lib/agent-members'
import type { User } from '@/types/flow'
import { RowCommandPicker, type MyIssuesContextOption } from './my-issues-list'
import './issue-assignee-slot.css'

type Person = { id: string; name: string; avatarUrl?: string; color?: string }

/** Row id of the delegated agent hung under the assignee in the picker; choosing it opens the agent submenu. */
const DELEGATE_ROW_ID = '__delegate-row__'

/**
 * The assignee cell of a list row or board card, as the reference app draws it: the delegated agent's 18px avatar
 * sits behind the human assignee's (4.5px overlap, a ring where they meet); an agent with no human shows
 * the empty-assignee glyph in the human's place. One hover card describes both ("Name (handle)" with the agent on a
 * connector beneath, or the agent card alone) and one picker handles both: the agent hangs under the assignee row
 * with an "Agent" pill and opens a "No agent / agents" submenu; "No assignee" asks "Remove <agent> too?".
 */
export function IssueAssigneeSlot({ issue, options, onChange, triggerClassName, avatarClassName, emptyClassName, label, hoverPlacement = 'below' }: {
  issue: { assignee?: Person; delegate?: Person; agentSessionState?: string }
  options: MyIssuesContextOption[]
  onChange: (id: string) => void | Promise<void>
  triggerClassName?: string
  avatarClassName: string
  emptyClassName?: string
  label?: string
  hoverPlacement?: 'left' | 'below'
}) {
  const { t } = useI18n()
  const { users } = usePeopleDirectory()
  const [open, setOpen] = useState(false)
  const [view, setView] = useState<'assignee' | 'agent'>('assignee')
  const { assignee, delegate } = issue
  const delegateUser = delegate ? toUser(delegate, users) : undefined
  const nested = Boolean(assignee && delegate && options.some(option => option.id === assignee.id))
  const agentOptions: MyIssuesContextOption[] = delegate ? [
    { id: NO_AGENT_ID, label: 'No agent', kind: 'assignee', shortcut: '0', icon: <FlowGlyph name="noAgent" size={16}/> },
    ...[delegate.id, ...options.filter(option => option.agent && option.id !== delegate.id).map(option => option.id)].map((id, index) => ({ ...(options.find(option => option.id === id) ?? { id, label: delegate.name, agent: true, kind: 'assignee' as const, avatarUrl: delegate.avatarUrl }), shortcut: assigneeShortcut(index + 1) })),
  ] : []
  const assigneeOptions: MyIssuesContextOption[] = nested && delegate && delegateUser
    ? options.filter(option => option.id !== delegate.id).flatMap(option => option.id === assignee?.id ? [option, {
        id: DELEGATE_ROW_ID, label: delegate.name, kind: 'assignee' as const, agent: true, handle: agentHandle(delegateUser), avatarUrl: delegate.avatarUrl,
        icon: <NestedDelegateAvatar user={delegateUser}/>,
        labelContent: <span className="assignee-slot-agent-row"><span data-i18n-ignore>{delegate.name}</span><span className="assignee-slot-agent-pill" data-i18n-ignore>Agent</span><i aria-hidden="true">▸</i></span>,
      }] : [option])
    : options
  const select = async (id: string) => {
    // The picker closes on every choice; reopening in the same batch swaps its list for the agent submenu.
    if (id === DELEGATE_ROW_ID) { setView('agent'); setOpen(true); return }
    if (view === 'agent') { if (id !== delegate?.id) await onChange(id); return }
    if (id === '' && delegate && delegateUser) {
      // Unassigning leaves the agent on the issue unless the user also removes it.
      if (!assignee) { await onChange(NO_AGENT_ID); return }
      const remove = await confirmRemoveDelegate(delegateUser, issue.agentSessionState === 'pending' || issue.agentSessionState === 'active', t)
      await onChange(remove ? CLEAR_ASSIGNEE_AND_AGENT_ID : '')
      return
    }
    if (assignee && delegate && id === delegate.id) return
    await onChange(id)
  }
  const ariaLabel = delegate && !assignee ? `Change assignee. ${delegate.name} is delegated` : assignee ? (delegate ? `Change assignee. ${assignee.name} is assigned` : `Assign to. Current assignee is ${assignee.name}`) : (label ?? 'Assign issue')
  const hover = assignee && delegate && delegateUser ? <AssigneeDelegateCard assignee={toUser(assignee, users)} delegate={delegateUser}/> : delegate && delegateUser ? <AgentHoverCard agent={delegateUser}/> : undefined
  const hoverClassName = assignee && delegate ? 'assignee-slot-hover-surface' : delegate ? AGENT_HOVER_CLASS : undefined
  const cluster = <span className="issue-assignee-cluster" data-delegated={delegate ? '' : undefined}>
    {delegate && <UserAvatar avatarUrl={delegate.avatarUrl} className={`${avatarClassName} issue-assignee-cluster__agent`} color={delegate.color ?? avatarColor(delegate.id)} name={delegate.name}/>}
    {assignee
      ? <UserAvatar avatarUrl={assignee.avatarUrl} className={`${avatarClassName} issue-assignee-cluster__member`} color={assignee.color ?? 'var(--avatar-fallback)'} name={assignee.name}/>
      : <span className={`${emptyClassName ?? ''} issue-assignee-cluster__empty`}><NoAssigneeIcon size={18}/></span>}
  </span>
  return <RowCommandPicker
    key={view}
    propertyLabel={view === 'agent' ? 'Delegate' : 'Assignee'}
    label={ariaLabel}
    searchLabel={view === 'agent' ? 'Delegate to…' : 'Assign to…'}
    searchShortcut={view === 'agent' ? '' : undefined}
    selectedIds={view === 'agent' ? [delegate?.id ?? NO_AGENT_ID] : [assignee?.id ?? '', ...(delegate && !nested ? [delegate.id] : [])]}
    options={view === 'agent' ? agentOptions : assigneeOptions}
    onSelect={select}
    open={open}
    onOpenChange={next => {
      setOpen(next)
      if (!next) setView('assignee')
    }}
    triggerClassName={triggerClassName}
    trigger={cluster}
    hoverContent={hover}
    hoverClassName={hoverClassName}
    hoverPlacement={hoverPlacement}
  />
}

function toUser(person: Person, users: ReadonlyMap<string, User>): User {
  const known = directoryPerson(users, person.id)
  return known ?? ({ id: person.id, name: person.name, displayName: person.name, avatarUrl: person.avatarUrl, email: '', active: true } as User)
}

/** "Name (handle)" over the agent on a connector: the reference hover card of an assignee with a delegate. */
function AssigneeDelegateCard({ assignee, delegate }: { assignee: User; delegate: User }) {
  const handle = assignee.username || personIdentifier(assignee)
  const agentName = delegate.displayName || delegate.name
  return <div className="assignee-slot-card">
    <div className="assignee-slot-card__person">
      <UserAvatar avatarUrl={assignee.avatarUrl} className="assignee-slot-card__avatar" color={avatarColor(assignee.id)} name={assignee.displayName || assignee.name}/>
      <span data-i18n-ignore>{assignee.displayName || assignee.name}</span>{handle && <em data-i18n-ignore>({handle})</em>}
    </div>
    <div className="assignee-slot-card__agent">
      <i aria-hidden="true"/>
      <UserAvatar avatarUrl={delegate.avatarUrl} className="assignee-slot-card__avatar" color={avatarColor(delegate.id)} name={agentName}/>
      <span data-i18n-ignore>{agentName}</span><em data-i18n-ignore>({agentHandle(delegate)})</em>
      <span className="assignee-slot-agent-pill" data-i18n-ignore>Agent</span>
    </div>
  </div>
}
